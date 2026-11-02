#!/bin/sh
# Build release binaries of the samsara runtime (05 §17.1).
#
#   runtime/scripts/release.sh [--version V] [--out DIR] [--targets "linux/amd64 darwin/arm64"]
#
# Output: DIR/samsara_<V>_<os>_<arch>.tar.gz (one `samsara` binary each) and
# DIR/SHA256SUMS. The version defaults to the repository VERSION file and is
# injected with -ldflags "-X main.version=<V>". SQLite is the pure-Go
# modernc driver, so CGO stays off and every target cross-compiles.
set -eu

here=$(cd "$(dirname "$0")/.." && pwd)        # runtime/
version=$(cat "$here/../VERSION" 2>/dev/null || echo 0.0.0-dev)
out="$here/dist"
targets="linux/amd64 linux/arm64 darwin/amd64 darwin/arm64"

while [ $# -gt 0 ]; do
  case "$1" in
    --version) version=$2; shift 2 ;;
    --out) out=$2; shift 2 ;;
    --targets) targets=$2; shift 2 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done

case "$version" in
  ""|*/*|*" "*) echo "invalid version: $version" >&2; exit 2 ;;
esac

mkdir -p "$out"
out=$(cd "$out" && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

sha() {
  if command -v sha256sum >/dev/null 2>&1; then sha256sum "$@"; else shasum -a 256 "$@"; fi
}

for t in $targets; do
  os=${t%/*}; arch=${t#*/}
  name="samsara_${version}_${os}_${arch}"
  mkdir -p "$work/$name"
  (cd "$here" && CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath \
    -ldflags "-s -w -X main.version=$version" -o "$work/$name/samsara" ./cmd/samsara)
  tar -C "$work/$name" -czf "$out/$name.tar.gz" samsara
  # Unpacked copy for scripts/samsara-bin.sh (plugin layout runtime/dist/<os>_<arch>/samsara).
  mkdir -p "$out/${os}_${arch}"
  cp "$work/$name/samsara" "$out/${os}_${arch}/samsara"
  echo "built $out/$name.tar.gz"
done

(cd "$out" && sha samsara_"${version}"_*.tar.gz > SHA256SUMS)
echo "wrote $out/SHA256SUMS"
