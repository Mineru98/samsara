#!/usr/bin/env bash
# Pick the cx launcher for a merge tab by rung (Codex-only ladder, no cc fallback).
# usage: merge-launch.sh <rung 1..> <issue numbers...>
# prints: LADDER=cx  RUNG=<n>  LAUNCH=<shell command>  PROMPT=<first prompt>
#         (or EXHAUSTED=1; LOW_USAGE=1 when Codex usage is under 20% or unknown -> ask the user first)
set -euo pipefail
RUNG=$1; shift
ISSUES=$(printf '#%s ' "$@"); ISSUES=${ISSUES% }
HERE=$(cd "$(dirname "$0")" && pwd)
REM=$(python3 "$HERE/codex-usage.py" | sed -n 's/^REMAINING=\([0-9a-z]*\).*/\1/p')
REM=${REM:-unknown}
CX=( "cx --model gpt-6.1-terra -c model_reasoning_effort=high"
     "cx --model gpt-6.1-sol -c model_reasoning_effort=medium"
     "cx --model gpt-6.1-sol -c model_reasoning_effort=high"
     "cx --model gpt-6-astra -c model_reasoning_effort=low"
     "cx --model gpt-6-astra -c model_reasoning_effort=medium" )
L=cx; LIST=("${CX[@]}"); PROMPT="\$issue-merge $ISSUES — 이 저장소는 GitHub CI 가 없다(check 0개는 실패가 아니라 검사 없음). CI 대신 이슈 증거와 base-tree 통합 테스트를 게이트로 쓴다. 보정 push 와 PR 별 merge 는 승인을 물어라."
if [ "$REM" = unknown ] || [ "$REM" -lt 20 ]; then echo "LOW_USAGE=1 LADDER=$L CODEX_REMAINING=$REM"; exit 0; fi
if [ "$RUNG" -gt "${#LIST[@]}" ]; then echo "EXHAUSTED=1 LADDER=$L CODEX_REMAINING=$REM"; exit 0; fi
echo "LADDER=$L"; echo "RUNG=$RUNG/${#LIST[@]}"; echo "CODEX_REMAINING=$REM"
echo "LAUNCH=${LIST[$((RUNG-1))]}"; echo "PROMPT=$PROMPT"
