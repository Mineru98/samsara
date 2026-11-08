#!/usr/bin/env bash
# Poll herdr agents every INTERVAL seconds; exit when any becomes blocked/idle/done or disappears.
# usage: watch.sh [--interval 15] <agent-name>...
set -u
INTERVAL=15
usage() { echo "usage: watch.sh [--interval 15] <agent-name>..." >&2; exit 2; }
case "${1:-}" in --interval) [ $# -ge 2 ] || usage; INTERVAL=$2; shift 2;; esac
[ $# -ge 1 ] || usage
for a in "$@"; do case "$a" in -*) usage;; esac; done
while :; do
  for a in "$@"; do
    out=$(herdr agent get "$a" 2>&1)
    st=$(printf '%s' "$out" | python3 -c 'import sys,json
try: print(json.load(sys.stdin)["result"]["agent"]["agent_status"])
except Exception: print("gone")')
    printf '%s %s %s\n' "$(date +%H:%M:%S)" "$a" "$st"
    # A Claude session waiting on its own background shells reads as done/idle; not a stop point.
    if [ "$st" = done ] || [ "$st" = idle ]; then
      if herdr agent read "$a" --source visible 2>/dev/null | tail -4 | grep -qE "shells? still running|[0-9]+ shells?( |·|$)"; then
        printf '%s %s %s\n' "$(date +%H:%M:%S)" "$a" "waiting-on-background"; continue
      fi
    fi
    case "$st" in blocked|idle|done|gone) echo "EVENT $a $st"; exit 0;; esac
  done
  sleep "$INTERVAL"
done
