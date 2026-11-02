#!/usr/bin/env python3
"""Verify an offline archive on a fresh, disposable root/systemd Linux host."""
import hashlib
import os
from pathlib import Path
import platform
import shutil
import subprocess
import sys
import tarfile
import tempfile

ROOT = Path(__file__).resolve().parents[1]


def main():
    if os.geteuid() != 0 or len(sys.argv) != 2:
        sys.exit('Usage: sudo python3 tests/offline-smoke.py /path/to/package.tar.gz')
    with tempfile.TemporaryDirectory(prefix='qinlin-offline-test-') as temporary:
        directory = Path(temporary)
        with tarfile.open(sys.argv[1], 'r:gz') as archive:
            for member in archive.getmembers():
                assert not Path(member.name).is_absolute() and '..' not in Path(member.name).parts
                assert member.isfile() or member.isdir()
            archive.extractall(directory)
        package, = directory.iterdir()
        arch_file = package / 'ARCH'
        manifest = package / 'SHA256SUMS'
        original_arch = arch_file.read_bytes()
        original_manifest = manifest.read_bytes()
        assert original_arch.decode().strip() == {'x86_64': 'amd64', 'aarch64': 'arm64'}[platform.machine()]

        def rejected(reason):
            result = subprocess.run(['bash', str(package / 'install-offline.sh'), '--mock'], text=True, capture_output=True)
            assert result.returncode != 0 and reason in result.stderr, result.stderr

        arch_file.write_text('corrupted\n')
        rejected('文件校验失败')
        arch_file.write_bytes(b'arm64\n' if original_arch == b'amd64\n' else b'amd64\n')
        manifest.write_text(''.join((hashlib.sha256(arch_file.read_bytes()).hexdigest() + '  ARCH\n') if line.endswith('  ARCH\n') else line for line in original_manifest.decode().splitlines(keepends=True)))
        rejected('架构不匹配')
        arch_file.write_bytes(original_arch)
        manifest.write_bytes(original_manifest)
        print('PASS corrupted files and mismatched CPU architecture rejected', flush=True)

        # Intercept any accidental attempt to download tools or contact GitHub.
        shims = directory / 'shims'
        shims.mkdir()
        log = directory / 'external-attempts'
        real_curl = shutil.which('curl')
        assert real_curl
        curl = shims / 'curl'
        curl.write_text("""#!/usr/bin/python3
import os, re, sys
urls = [value for value in sys.argv[1:] if value.startswith(('http://', 'https://'))]
if len(urls) != 1 or not re.fullmatch(r'http://127\\.0\\.0\\.1:[0-9]+/healthz', urls[0]):
    with open(os.environ['QINLIN_TEST_EXTERNAL_LOG'], 'a') as log:
        log.write('curl attempted an external request\\n')
    sys.exit(97)
os.execv(os.environ['QINLIN_TEST_REAL_CURL'], [os.environ['QINLIN_TEST_REAL_CURL'], *sys.argv[1:]])
""")
        curl.chmod(0o755)
        for command in ['git', 'go', 'apt-get', 'dnf', 'yum', 'wget']:
            shim = shims / command
            shim.write_text('#!/bin/bash\nprintf "%s\\n" "${0##*/}" >> "$QINLIN_TEST_EXTERNAL_LOG"\nexit 97\n')
            shim.chmod(0o755)
        env = dict(os.environ, PATH=str(shims) + ':' + os.environ['PATH'],
                   QINLIN_TEST_OFFLINE_DIR=str(package), QINLIN_TEST_REAL_CURL=real_curl,
                   QINLIN_TEST_EXTERNAL_LOG=str(log))
        env.pop('QINLIN_TEST_BOOTSTRAP', None)
        env.pop('QINLIN_TEST_PUBLISHED_UPDATE', None)
        subprocess.run([sys.executable, str(ROOT / 'tests/linux-smoke.py')], env=env, check=True)
        assert not log.exists(), 'offline installation attempted external tools/network: ' + log.read_text()
        print('PASS complete installation and management without GitHub, Go or package-manager downloads', flush=True)


if __name__ == '__main__':
    main()
