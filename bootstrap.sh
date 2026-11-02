#!/usr/bin/env bash
set -Eeuo pipefail

if [[ ${EUID} -ne 0 ]]; then
  echo '请以 root 运行，例如：curl -fsSL https://raw.githubusercontent.com/bunnya33/qinlin_server/main/bootstrap.sh | sudo bash' >&2
  exit 1
fi

temporary_dir=$(mktemp -d)
trap 'rm -rf -- "$temporary_dir"' EXIT
curl -fLSs --connect-timeout 15 --max-time 180 --retry 2 'https://raw.githubusercontent.com/bunnya33/qinlin_server/main/install.sh' -o "$temporary_dir/install.sh"
bash "$temporary_dir/install.sh" "$@"
