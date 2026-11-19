#!/usr/bin/env bash
set -euo pipefail

tag=${1:-}
output=${2:-dist}
if [[ ! $tag =~ ^v(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})$ ]]; then
  echo 'Usage: bash scripts/build-release.sh vMAJOR.MINOR.PATCH [empty-output-dir]' >&2
  exit 1
fi

mkdir -p -- "$output"
output=$(cd -- "$output" && pwd)
if [[ -n $(ls -A -- "$output") ]]; then
  echo "Release output directory must be empty: $output" >&2
  exit 1
fi
cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.."
revision=$(git rev-parse HEAD)

for target in linux/amd64 linux/arm64 windows/amd64; do
  platform=${target%/*}
  architecture=${target#*/}
  name="qinlin-server_${platform}_${architecture}"
  [[ $platform != windows ]] || name+=.exe
  CGO_ENABLED=0 GOOS="$platform" GOARCH="$architecture" go build -buildvcs=false -trimpath \
    -ldflags="-s -w -X qinlin/internal/version.Version=${tag#v} -X qinlin/internal/version.Revision=$revision" \
    -o "$output/$name" ./cmd/qinlin
done

python3 scripts/package-offline.py --version "$tag" --revision "$revision" --binary-dir "$output" --output "$output"
python3 - "$output" <<'PY'
import hashlib
from pathlib import Path
import sys

output = Path(sys.argv[1])
with (output / 'SHA256SUMS').open('x', encoding='ascii', newline='\n') as manifest:
    for asset in sorted(output.iterdir()):
        if asset.name != 'SHA256SUMS':
            with asset.open('rb') as binary:
                digest = hashlib.sha256()
                for chunk in iter(lambda: binary.read(1024 * 1024), b''):
                    digest.update(chunk)
            manifest.write(f'{digest.hexdigest()}  {asset.name}\n')
PY
