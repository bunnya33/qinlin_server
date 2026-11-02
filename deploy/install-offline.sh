#!/usr/bin/env bash
set -Eeuo pipefail

package_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
cd -- "$package_dir"
[[ ${EUID} -eq 0 ]] || { echo '请运行 sudo bash install-offline.sh。' >&2; exit 1; }
for dependency in sha256sum uname bash; do
  command -v "$dependency" >/dev/null || { echo "缺少系统命令：$dependency" >&2; exit 1; }
done
[[ -f SHA256SUMS && -f ARCH ]] || { echo '离线包不完整，请重新上传并解压。' >&2; exit 1; }
sha256sum --check --status SHA256SUMS || { echo '文件校验失败，请重新上传完整的离线包。' >&2; exit 1; }
case "$(uname -m)" in
  x86_64|amd64) machine_arch=amd64 ;;
  aarch64|arm64) machine_arch=arm64 ;;
  *) echo '此离线包支持 Linux amd64 和 arm64。' >&2; exit 1 ;;
esac
read -r package_arch < ARCH
[[ $machine_arch == "$package_arch" ]] || { echo "架构不匹配：服务器为 $machine_arch，离线包为 $package_arch，请上传匹配的安装包。" >&2; exit 1; }
export QINLIN_PREBUILT_BINARY="$package_dir/bin/qinlin-server"
exec bash "$package_dir/install.sh" --offline "$@"
