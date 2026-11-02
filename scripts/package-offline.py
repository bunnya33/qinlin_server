#!/usr/bin/env python3
"""Build uploadable Linux installation archives using Go and Python 3 only."""
import argparse
import gzip
import hashlib
import os
from pathlib import Path
import re
import shutil
import struct
import subprocess
import tarfile
import tempfile

ROOT = Path(__file__).resolve().parents[1]


def run(command, **kwargs):
    return subprocess.run(command, cwd=ROOT, check=True, **kwargs)


def check_binary(binary, arch):
    with binary.open('rb') as stream:
        header = stream.read(64)
        if header[:6] != b'\x7fELF\x02\x01' or struct.unpack_from('<H', header, 18)[0] != {'amd64': 62, 'arm64': 183}[arch]:
            raise ValueError('binary is not a matching 64-bit Linux ELF: ' + str(binary))
        offset = struct.unpack_from('<Q', header, 32)[0]
        entry_size, count = struct.unpack_from('<HH', header, 54)
        stream.seek(offset)
        for _ in range(count):
            entry = stream.read(entry_size)
            if struct.unpack_from('<I', entry)[0] == 3:
                raise ValueError('binary requires a dynamic interpreter: ' + str(binary))


def readme(name, arch, version):
    return f'''# 亲邻开门离线安装包 {version} / Linux {arch}

上传整个 {name}.tar.gz，在服务器执行：

```bash
tar -xzf {name}.tar.gz
cd {name}
sudo bash install-offline.sh --public-url http://你的服务器公网IP:8080
```

请将示例中的 IP 换成服务器实际公网 IP。如果使用其他端口，同时传入
`--port 端口号` 并修改 `--public-url` 中的端口。需在云安全组允许该端口。

安装完成显示访问地址和随机管理员账号密码。管理命令为 `sudo qinlin`。
支持使用 systemd 的 Debian/Ubuntu、Rocky/AlmaLinux 等常见 Linux 系统。
`uname -m` 为 x86_64 时使用 amd64 包，aarch64 时使用 arm64 包。

此包自带静态程序、管理菜单和服务定义，不需要 Git、Go 或外部代码仓库，
不访问 GitHub，不联网安装任何组件。安装前会验证 SHA-256 和 CPU 架构。
系统需已有 bash、curl、python3、flock、ss、useradd/groupadd、getent、
coreutils 和 CA 证书。缺少时脚本会列出依赖；可通过国内系统软件源预先安装。
Ubuntu/Debian 通常使用：
`sudo apt-get install -y curl python3 util-linux iproute2 passwd coreutils ca-certificates`
Rocky/AlmaLinux 通常使用：
`sudo dnf install -y curl python3 util-linux iproute shadow-utils coreutils ca-certificates`

默认安装到 /opt/qinlin，配置在 /etc/qinlin，业务数据在 /var/lib/qinlin。
重复执行本包或新版离线包的安装命令可更新程序，并保留已有配置、账号和数据。
已有配置的端口和访问地址继续沿用；可通过 `sudo qinlin` 菜单修改。
安装包不包含开发电脑的账号、密钥、门禁令牌或数据库，新安装需要自行关联亲邻账号。

离线安装首次创建更新设置时，默认关闭 GitHub 自动检查与自动下载。
已有更新设置会保留；无法访问 GitHub 的服务器可在后台关闭这两项。
以后升级：上传新版离线包，解压后再次运行 `sudo bash install-offline.sh`。
亲邻短信登录、账号检测与开门仍需要服务器能访问亲邻服务。
'''


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--version')
    parser.add_argument('--revision')
    parser.add_argument('--output', type=Path, default=ROOT / 'dist')
    parser.add_argument('--binary-dir', type=Path, help='reuse release binaries named qinlin-server_linux_ARCH')
    parser.add_argument('--arch', choices=['amd64', 'arm64'], action='append')
    args = parser.parse_args()
    version = args.version or re.search(r'var Version = "([^"]+)"', (ROOT / 'internal/version/version.go').read_text(encoding='utf-8')).group(1)
    version = version.removeprefix('v')
    if not re.fullmatch(r'(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})', version):
        parser.error('version must be a stable MAJOR.MINOR.PATCH')
    revision = args.revision or run(['git', 'rev-parse', 'HEAD'], text=True, capture_output=True).stdout.strip()
    if not re.fullmatch(r'[0-9a-f]{7,40}', revision):
        parser.error('revision must be a Git commit hash')
    output = args.output.resolve()
    output.mkdir(parents=True, exist_ok=True)
    for arch in dict.fromkeys(args.arch or ['amd64', 'arm64']):
        name = f'qinlin-v{version}-linux-{arch}'
        with tempfile.TemporaryDirectory(prefix='qinlin-package-') as staging:
            package = Path(staging) / name
            (package / 'bin').mkdir(parents=True)
            (package / 'deploy').mkdir()
            binary = package / 'bin/qinlin-server'
            if args.binary_dir:
                shutil.copyfile(args.binary_dir / f'qinlin-server_linux_{arch}', binary)
            else:
                env = dict(os.environ, GOOS='linux', GOARCH=arch, CGO_ENABLED='0')
                run(['go', 'build', '-buildvcs=false', '-trimpath', '-ldflags', f'-s -w -X qinlin/internal/version.Version={version} -X qinlin/internal/version.Revision={revision}', '-o', str(binary), './cmd/qinlin'], env=env)
            check_binary(binary, arch)
            files = {'install.sh': ROOT / 'install.sh', 'install-offline.sh': ROOT / 'deploy/install-offline.sh', 'deploy/manage.sh': ROOT / 'deploy/manage.sh', 'deploy/qinlin.service': ROOT / 'deploy/qinlin.service', 'THIRD_PARTY_NOTICES.md': ROOT / 'THIRD_PARTY_NOTICES.md'}
            for target, source in files.items():
                (package / target).write_bytes(source.read_bytes().replace(b'\r\n', b'\n'))
            for target, content in {'ARCH': arch + '\n', 'VERSION': version + '\n', 'REVISION': revision + '\n', 'README-OFFLINE.md': readme(name, arch, version)}.items():
                (package / target).write_bytes(content.encode('utf-8'))
            manifest = ''.join(hashlib.sha256(path.read_bytes()).hexdigest() + '  ' + path.relative_to(package).as_posix() + '\n' for path in sorted(package.rglob('*')) if path.is_file())
            (package / 'SHA256SUMS').write_bytes(manifest.encode('ascii'))
            archive = output / (name + '.tar.gz')
            def normalize(info):
                info.uid = info.gid = 0
                info.uname = info.gname = ''
                info.mtime = 0
                info.mode = 0o755 if info.isdir() or info.name.endswith('.sh') or info.name.endswith('/bin/qinlin-server') else 0o644
                return info
            with archive.open('wb') as stream, gzip.GzipFile(filename='', mode='wb', fileobj=stream, mtime=0) as compressed, tarfile.open(fileobj=compressed, mode='w') as tar:
                tar.add(package, arcname=name, filter=normalize)
            digest = hashlib.sha256(archive.read_bytes()).hexdigest()
            (output / (archive.name + '.sha256')).write_bytes((digest + '  ' + archive.name + '\n').encode('ascii'))
            print(f'{archive} ({archive.stat().st_size / 1048576:.1f} MiB)', flush=True)


if __name__ == '__main__':
    main()
