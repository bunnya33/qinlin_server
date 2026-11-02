#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

INSTALL_DIR=/opt/qinlin
CONFIG_DIR=/etc/qinlin
CONFIG_FILE=$CONFIG_DIR/qinlin.env
DATA_DIR=/var/lib/qinlin
UNIT_FILE=/etc/systemd/system/qinlin.service
COMMAND_FILE=/usr/local/bin/qinlin
SOURCE_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
GO_VERSION=1.26.2
port=8080
public_url=''
mock=false
offline=false

die() { echo "安装失败：$*" >&2; exit 1; }
[[ ${EUID} -eq 0 ]] || die '请运行 sudo bash install.sh'
[[ $(uname -s) == Linux ]] || die '此脚本适用于使用 systemd 的 Linux 服务器。'
command -v systemctl >/dev/null || die '系统未安装 systemd。'
[[ -d /run/systemd/system ]] || die 'systemd 未运行，不能注册系统服务。'

while [[ $# -gt 0 ]]; do
  case "$1" in
    --port) [[ $# -ge 2 ]] || die '--port 缺少参数'; port=$2; shift 2 ;;
    --public-url) [[ $# -ge 2 ]] || die '--public-url 缺少参数'; public_url=$2; shift 2 ;;
    --mock) mock=true; shift ;;
    --offline) offline=true; shift ;;
    -h|--help) echo '用法：sudo bash install.sh [--port 8080] [--public-url http://服务器IP:8080] [--mock] [--offline]'; exit 0 ;;
    *) die "未知参数：$1" ;;
  esac
done
[[ $port =~ ^[0-9]{1,5}$ ]] || die '端口必须为 1～65535 的整数。'
port=$((10#$port))
((port >= 1 && port <= 65535)) || die '端口必须为 1～65535。'
if [[ $offline == true ]]; then
  [[ -n ${QINLIN_PREBUILT_BINARY:-} && -f $QINLIN_PREBUILT_BINARY ]] || die '离线安装需要预编译程序，请使用离线包中的 install-offline.sh。'
  [[ -f "$SOURCE_DIR/deploy/manage.sh" && -f "$SOURCE_DIR/deploy/qinlin.service" ]] || die '离线安装包不完整，缺少 deploy 管理文件。'
fi

install_dependencies() {
  local missing=false dependency missing_names='' required=(curl python3 flock ss useradd groupadd getent install sha256sum)
  [[ $offline == true ]] || required+=(tar git)
  for dependency in "${required[@]}"; do
    if ! command -v "$dependency" >/dev/null; then missing=true; missing_names+=" $dependency"; fi
  done
  if [[ ! -s /etc/ssl/certs/ca-certificates.crt && ! -s /etc/pki/tls/certs/ca-bundle.crt ]]; then missing=true; missing_names+=' ca-certificates'; fi
  [[ $missing == true ]] || return 0
  [[ $offline != true ]] || die "缺少系统依赖：$missing_names。离线模式不会联网安装依赖，请先通过系统软件源安装 curl、python3、util-linux、iproute2（RPM 系为 iproute）、passwd/shadow-utils 和 ca-certificates 后重试。"
  echo '正在安装必要依赖……'
  if command -v apt-get >/dev/null; then
    apt-get update
    DEBIAN_FRONTEND=noninteractive apt-get install -y ca-certificates curl tar git python3 util-linux iproute2 passwd
  elif command -v dnf >/dev/null; then
    dnf install -y ca-certificates curl tar git python3 util-linux iproute shadow-utils
  elif command -v yum >/dev/null; then
    yum install -y ca-certificates curl tar git python3 util-linux iproute shadow-utils
  else
    die '请手动安装 curl、tar、git、python3、util-linux、iproute2 和 useradd。'
  fi
}
install_dependencies
exec 9>/run/lock/qinlin-manage.lock
flock -n 9 || die '另一个安装或管理操作正在进行，请稍后重试。'

temporary_dir=$(mktemp -d)
trap 'rm -rf -- "$temporary_dir"' EXIT

# The bootstrap entry point downloads this same installer first. Dependencies
# are installed before unpacking source; Git-clone installs use their checkout.
if [[ ! -f "$SOURCE_DIR/go.mod" && $offline != true ]]; then
  curl -fLSs --connect-timeout 15 --max-time 180 --retry 2 'https://codeload.github.com/bunnya33/qinlin_server/tar.gz/refs/heads/main' -o "$temporary_dir/source.tar.gz"
  mkdir "$temporary_dir/source"
  tar -xzf "$temporary_dir/source.tar.gz" -C "$temporary_dir/source" --strip-components=1
  SOURCE_DIR=$temporary_dir/source
fi

find_go() {
  local candidate current
  for candidate in "$(command -v go || true)" "$INSTALL_DIR/toolchain/go/bin/go"; do
    [[ -n $candidate && -x $candidate ]] || continue
    current=$("$candidate" version | awk '{print $3}')
    if python3 - "$current" <<'PY'
import re, sys
match = re.match(r'go(\d+)\.(\d+)', sys.argv[1])
sys.exit(0 if match and tuple(map(int, match.groups())) >= (1, 26) else 1)
PY
    then
      go_binary=$candidate
      return
    fi
  done
  local arch archive checksum
  case "$(uname -m)" in
    x86_64|amd64) arch=amd64 ;;
    aarch64|arm64) arch=arm64 ;;
    *) die '自动下载 Go 仅支持 amd64 和 arm64，可自行安装 Go 1.26+ 后重试。' ;;
  esac
  archive="go${GO_VERSION}.linux-${arch}.tar.gz"
  echo "正在下载专用于本项目的 Go ${GO_VERSION}……"
  curl -fLSs --connect-timeout 15 --max-time 120 --retry 2 'https://go.dev/dl/?mode=json&include=all' -o "$temporary_dir/go-releases.json"
  checksum=$(python3 - "$temporary_dir/go-releases.json" "$archive" <<'PY'
import json, sys
for release in json.load(open(sys.argv[1])):
    for file in release['files']:
        if file['filename'] == sys.argv[2]:
            print(file['sha256'])
            sys.exit(0)
sys.exit('没有找到对应 Go 官方校验值')
PY
  )
  curl -fLSs --connect-timeout 15 --max-time 600 --retry 2 "https://go.dev/dl/$archive" -o "$temporary_dir/$archive"
  printf '%s  %s\n' "$checksum" "$temporary_dir/$archive" | sha256sum -c -
  mkdir -p "$INSTALL_DIR/toolchain"
  tar -xzf "$temporary_dir/$archive" -C "$INSTALL_DIR/toolchain"
  go_binary=$INSTALL_DIR/toolchain/go/bin/go
}

if [[ -n ${QINLIN_PREBUILT_BINARY:-} ]]; then
  [[ -f $QINLIN_PREBUILT_BINARY ]] || die '指定的预编译程序不存在。'
  install -m 0755 "$QINLIN_PREBUILT_BINARY" "$temporary_dir/qinlin-server"
else
  find_go
  echo '正在编译亲邻开门服务……'
  (cd "$SOURCE_DIR" && CGO_ENABLED=0 "$go_binary" build -buildvcs=false -trimpath -ldflags='-s -w' -o "$temporary_dir/qinlin-server" ./cmd/qinlin)
fi
"$temporary_dir/qinlin-server" --version

getent group qinlin >/dev/null || groupadd --system qinlin
if ! id qinlin >/dev/null 2>&1; then
  useradd --system --gid qinlin --home-dir "$DATA_DIR" --no-create-home --shell /usr/sbin/nologin qinlin
fi
install -d -m 0755 "$INSTALL_DIR" "$INSTALL_DIR/bin" "$INSTALL_DIR/lib"
install -d -m 0750 -o root -g qinlin "$CONFIG_DIR"
install -d -m 0700 -o qinlin -g qinlin "$DATA_DIR"

new_config=false
if [[ ! -e $CONFIG_FILE ]]; then
  if [[ -z $public_url ]]; then
    address=$(hostname -I 2>/dev/null | awk '{print $1}')
    address=${address:-127.0.0.1}
    [[ $address != *:* ]] || address="[$address]"
    public_url="http://$address:$port"
  fi
  init_args=(init --config "$CONFIG_FILE" --addr "0.0.0.0:$port" --public-url "$public_url" --db "$DATA_DIR/qinlin.db")
  [[ $mock == false ]] || init_args+=(--mock)
  "$temporary_dir/qinlin-server" "${init_args[@]}"
  new_config=true
else
  echo '发现已有配置：保留管理员凭据、加密密钥、端口及业务数据。'
fi
chown root:qinlin "$CONFIG_FILE"
chmod 0640 "$CONFIG_FILE"
"$temporary_dir/qinlin-server" check-config --config "$CONFIG_FILE"
if [[ $offline == true && ! -e "$DATA_DIR/updates/preferences.json" ]]; then
  install -d -m 0700 -o qinlin -g qinlin "$DATA_DIR/updates"
  printf '%s\n' '{"auto_check":false,"auto_download":false}' > "$DATA_DIR/updates/preferences.json"
  chown qinlin:qinlin "$DATA_DIR/updates/preferences.json"
  chmod 0600 "$DATA_DIR/updates/preferences.json"
fi

was_running=false
systemctl is-active --quiet qinlin.service && was_running=true
[[ ! -f "$INSTALL_DIR/bin/qinlin-server" ]] || cp -p "$INSTALL_DIR/bin/qinlin-server" "$temporary_dir/previous-binary"
[[ ! -f "$INSTALL_DIR/bin/qinlin-supervisor" ]] || cp -p "$INSTALL_DIR/bin/qinlin-supervisor" "$temporary_dir/previous-supervisor"
[[ ! -f $UNIT_FILE ]] || cp -p "$UNIT_FILE" "$temporary_dir/previous-unit"
[[ ! -f "$INSTALL_DIR/lib/manage.sh" ]] || cp -p "$INSTALL_DIR/lib/manage.sh" "$temporary_dir/previous-manager"
[[ ! -f $COMMAND_FILE ]] || cp -p "$COMMAND_FILE" "$temporary_dir/previous-command"
if [[ -f $UNIT_FILE ]] || systemctl is-active --quiet qinlin.service; then
  systemctl stop qinlin.service || die '无法停止现有服务，已取消更新。'
fi
install -m 0755 "$temporary_dir/qinlin-server" "$INSTALL_DIR/bin/qinlin-server"
install -m 0755 "$temporary_dir/qinlin-server" "$INSTALL_DIR/bin/qinlin-supervisor"
install -m 0755 "$SOURCE_DIR/deploy/manage.sh" "$INSTALL_DIR/lib/manage.sh"
install -m 0644 "$SOURCE_DIR/deploy/qinlin.service" "$UNIT_FILE"
cat > "$COMMAND_FILE" <<'SH'
#!/usr/bin/env bash
exec bash /opt/qinlin/lib/manage.sh "$@"
SH
chmod 0755 "$COMMAND_FILE"

credentials=''
if [[ $new_config == true ]]; then
  credentials=$("$INSTALL_DIR/bin/qinlin-server" admin reset --config "$CONFIG_FILE")
fi
systemctl daemon-reload
systemctl enable qinlin.service

local_url=$("$INSTALL_DIR/bin/qinlin-server" config show --config "$CONFIG_FILE" | python3 -c 'import json,sys; a=json.load(sys.stdin)["addr"]; h,p=a.rsplit(":",1); h=h.strip("[]"); h="127.0.0.1" if h in ("","0.0.0.0") else "::1" if h=="::" else h; print("http://"+("["+h+"]" if ":" in h else h)+":"+p+"/healthz")')
ready=false
systemctl reset-failed qinlin.service 2>/dev/null || true
if systemctl restart qinlin.service; then
  for ((attempt=0;attempt<20;attempt++)); do
    if curl --noproxy '*' -fsS --max-time 2 "$local_url" >/dev/null 2>&1; then ready=true; break; fi
    sleep 1
  done
fi
if [[ $ready != true ]]; then
  systemctl stop qinlin.service 2>/dev/null || true
  if [[ -f "$temporary_dir/previous-binary" ]]; then
    install -m 0755 "$temporary_dir/previous-binary" "$INSTALL_DIR/bin/qinlin-server"
    [[ ! -f "$temporary_dir/previous-supervisor" ]] || install -m 0755 "$temporary_dir/previous-supervisor" "$INSTALL_DIR/bin/qinlin-supervisor"
    [[ ! -f "$temporary_dir/previous-unit" ]] || install -m 0644 "$temporary_dir/previous-unit" "$UNIT_FILE"
    [[ ! -f "$temporary_dir/previous-manager" ]] || install -m 0755 "$temporary_dir/previous-manager" "$INSTALL_DIR/lib/manage.sh"
    [[ ! -f "$temporary_dir/previous-command" ]] || install -m 0755 "$temporary_dir/previous-command" "$COMMAND_FILE"
    systemctl daemon-reload
    systemctl reset-failed qinlin.service 2>/dev/null || true
    [[ $was_running != true ]] || systemctl start qinlin.service
  fi
  die '服务启动检查失败，已有程序已尝试回滚，配置和业务数据保留。请运行 journalctl -u qinlin -n 50 查看日志。'
fi

access_url=$("$INSTALL_DIR/bin/qinlin-server" config show --config "$CONFIG_FILE" | python3 -c 'import json,sys;print(json.load(sys.stdin)["public_url"])')
echo
echo '亲邻开门服务安装完成，已启动并设置开机自启。'
echo "访问地址：$access_url"
if [[ -n $credentials ]]; then printf '%s\n' "$credentials"; fi
echo '管理菜单：sudo qinlin'
echo '重复安装保留配置与数据；如服务器有云安全组或防火墙，请允许所配置的服务端口。'
