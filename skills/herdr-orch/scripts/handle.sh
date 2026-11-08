#!/usr/bin/env bash
# Auto-answer routine, verified screens for each agent; print the rest for the orchestrator.
#   - issue-end 10 "PR 생성" with cursor on "1. 만든다"  → enter
#   - issue-end 11 "다음 행동" (PR 까지 끝남)            → end-tab.sh (종료 + tab close)
# Anything else blocked/done/idle is printed as NEEDS <agent> with the screen tail.
# usage: handle.sh <agent>...
H=$(cd "$(dirname "$0")" && pwd)
for a in "$@"; do
  st=$(herdr agent get "$a" 2>/dev/null | python3 -c 'import sys,json;print(json.load(sys.stdin)["result"]["agent"]["agent_status"])' 2>/dev/null || echo gone)
  case "$st" in blocked|done|idle) ;; *) echo "OK $a $st"; continue;; esac
  scr=$(herdr agent read "$a" --source visible 2>/dev/null); cur=$(echo "$scr" | grep "❯" | tail -1)
  if echo "$scr" | grep -q "issue-end 10단계" && echo "$cur" | grep -q "1. 만든다"; then
    herdr agent send-keys "$a" enter >/dev/null; echo "AUTO $a PR 생성 → 만든다"
  elif echo "$scr" | grep -q "issue-end 11단계" && echo "$scr" | grep -q "종료"; then
    echo "AUTO $a $("$H/end-tab.sh" "$a")"
  elif echo "$scr" | grep -q "issue-merge 6단계" && echo "$cur" | grep -qE "1\. merge"; then
    # PR merge approval: verify head pin = PR head = worktree HEAD, MERGEABLE+CLEAN, worktree clean.
    pr=$(echo "$scr" | grep -oE "PR #[0-9]+" | tail -1 | tr -dc 0-9); pin=$(echo "$scr" | grep -oE "head [0-9a-f]{7}" | tail -1 | cut -d" " -f2)
    for _ in 1 2 3 4 5 6; do v=$(gh pr view "$pr" --json headRefOid,mergeable,mergeStateStatus,headRefName -q '"\(.headRefOid[0:7]) \(.mergeable) \(.mergeStateStatus) \(.headRefName)"'); case "$v" in *UNKNOWN*) sleep 5;; *) break;; esac; done
    set -- $v; wt=$(git worktree list --porcelain | awk -v b="refs/heads/$4" '$1=="worktree"{w=$2} $1=="branch"&&$2==b{print w}')
    whead=$(git -C "$wt" rev-parse --short=7 HEAD 2>/dev/null); dirty=$(git -C "$wt" status --porcelain 2>/dev/null | grep -vc '^?? .issue/')
    if [ "$1" = "$pin" ] && [ "$1" = "$whead" ] && [ "$2" = MERGEABLE ] && [ "$3" = CLEAN ] && [ "$dirty" = 0 ]; then
      herdr agent send-keys "$a" enter >/dev/null; echo "AUTO $a PR #$pr merge 승인 (head $1 = pin = worktree, MERGEABLE CLEAN)"
    else echo "NEEDS $a PR #$pr merge 기준 불일치: pr=$1 pin=$pin wt=$whead $2 $3 dirty=$dirty"; fi
  else
    echo "NEEDS $a $st"; echo "$scr" | grep -v "^\s*$" | tail -22
  fi
done
