#!/usr/bin/env bash
# Poll herdr agents every INTERVAL seconds; exit when any becomes blocked/idle/done or disappears.
# usage: watch.sh [--interval 15] <agent-name>...
set -u
INTERVAL=15
if [ "${1:-}" = "--interval" ]; then INTERVAL=$2; shift 2; fi
while :; do
  for a in "$@"; do
    out=$(herdr agent get "$a" 2>&1)
    st=$(printf '%s' "$out" | python3 -c 'import sys,json
try: print(json.load(sys.stdin)["result"]["agent"]["agent_status"])
except Exception: print("gone")')
    printf '%s %s %s\n' "$(date +%H:%M:%S)" "$a" "$st"
    case "$st" in blocked|idle|done|gone) echo "EVENT $a $st"; exit 0;; esac
  done
  sleep "$INTERVAL"
done
