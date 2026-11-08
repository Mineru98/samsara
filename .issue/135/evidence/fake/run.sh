#!/usr/bin/env bash
# usage: run.sh <herdr-orch scripts dir> — start-finish.sh 를 가짜 herdr 로 세 화면에 돌린다
S=$1; H=$(cd "$(dirname "$0")" && pwd); export PATH="$H/bin:$PATH" HERDR_WORKSPACE_ID=fW
M=$(mktemp -d); git -C "$M" init -q
for sc in finish-canon finish-variant finish-single finish-single-scroll; do
  export FAKE_SCENARIO=$sc FAKE_STATE=$(mktemp -d)
  echo "## start-finish · $sc"; echo "# 첫 화면"; FAKE_STATE=$(mktemp -d) herdr agent read x --source visible | sed 's/^/  /'
  bash "$S/start-finish.sh" issue-9 "$M"; echo "exit=$?"
  echo "# 보낸 키"; grep -E 'send-keys' "$FAKE_STATE/calls.log" | sed 's/^/  /'; echo
done
