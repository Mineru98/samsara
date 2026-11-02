#!/bin/sh
# Print the path of the samsara runtime binary for plugin scripts and hooks.
#
# Lookup order (first executable wins):
#   1. $SAMSARA_BIN                                        explicit override
#   2. <install>/bin/samsara                               `samsara install` link
#        <install> = $SAMSARA_HOME/install, else ${XDG_DATA_HOME:-~/.local/share}/samsara/install
#   3. ${CLAUDE_PLUGIN_ROOT:-<repo>}/runtime/dist/<os>_<arch>/samsara   unpacked release in the plugin
#   4. samsara on PATH
# Exit 1 with a hint on stderr when none is found.
set -eu

ok() { [ -n "$1" ] && [ -f "$1" ] && [ -x "$1" ]; }

if [ -n "${SAMSARA_BIN:-}" ]; then
  if ok "$SAMSARA_BIN"; then echo "$SAMSARA_BIN"; exit 0; fi
  echo "SAMSARA_BIN=$SAMSARA_BIN is not an executable file" >&2
  exit 1
fi

if [ -n "${SAMSARA_HOME:-}" ]; then
  install_dir="$SAMSARA_HOME/install"
else
  install_dir="${XDG_DATA_HOME:-$HOME/.local/share}/samsara/install"
fi
if ok "$install_dir/bin/samsara"; then echo "$install_dir/bin/samsara"; exit 0; fi

root="${CLAUDE_PLUGIN_ROOT:-$(cd "$(dirname "$0")/.." && pwd)}"
os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) arch=$(uname -m) ;;
esac
if ok "$root/runtime/dist/${os}_${arch}/samsara"; then echo "$root/runtime/dist/${os}_${arch}/samsara"; exit 0; fi

if p=$(command -v samsara 2>/dev/null) && ok "$p"; then echo "$p"; exit 0; fi

echo "samsara runtime not found: download a release tarball, run './samsara install', or set SAMSARA_BIN" >&2
exit 1
