#!/bin/sh
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
tmp=$(mktemp -d "${TMPDIR:-/tmp}/issue-common-check.XXXXXX")
trap 'rm -rf "$tmp"' EXIT HUP INT TERM

files='
skills/issue-create/scripts/issue-common.mjs
skills/issue-end/scripts/issue-common.mjs
skills/issue-merge/scripts/issue-common.mjs
skills/issue-start/scripts/issue-common.mjs
'

reference=''
for rel in $files; do
  file="$root/$rel"
  if [ ! -f "$file" ]; then
    echo "missing shared copy: $rel" >&2
    exit 1
  fi

  block="$tmp/block"
  awk '
    /^export function resolveStatus\(raw\) \{$/ { found = 1 }
    found { print }
  ' "$file" > "$block"
  if [ ! -s "$block" ]; then
    echo "shared block not found: $rel" >&2
    exit 1
  fi

  if [ -z "$reference" ]; then
    reference="$tmp/reference"
    cp "$block" "$reference"
  elif ! cmp -s "$reference" "$block"; then
    echo "shared block drift: $rel" >&2
    exit 1
  fi
done

echo "shared block: 4 copies match"
