#!/usr/bin/env bash
# Answer issue-start's final multi-question screen: 메인 최신화 → "잠시 치워두고 받아오기", 다음 행동 → "검토 승인하고 PR 만들기",
# then Submit. Each answer is sent only when the cursor line shows the expected option; otherwise stops.
# Refuses when another session's issue-sync stash is in the main checkout (old skills pop the top stash).
# usage: start-finish.sh <agent-name> <main-checkout>
set -uo pipefail
A=$1; M=$2
P=$(herdr agent get "$A" | python3 -c "import sys,json;print(json.load(sys.stdin)['result']['agent']['pane_id'])")
[ "$(git -C "$M" stash list | grep -c issue-sync)" = 0 ] || { echo "WAIT: 메인 폴더에 다른 세션의 issue-sync stash 가 있음"; exit 2; }
for _ in 1 2 3 4 5 6 7 8; do
  scr=$(herdr agent read "$A" --source visible); c=$(echo "$scr" | grep "❯" | tail -1)
  case "$c" in
    *"잠시 치워두고"*|*"stash 후 받아오고"*|*"검토 승인하고 PR"*) herdr agent send-keys "$A" enter >/dev/null ;;
    *"1. Submit answers"*) echo "$scr" | grep "→"; herdr agent send-keys "$A" enter >/dev/null
        herdr pane wait-output "$P" --match "User answered" --source visible --timeout 8000 >/dev/null 2>&1 && echo "submitted"; exit 0 ;;
    *) # 원하는 선택지가 커서 아래에 있으면 내려가서 찾는다(번호는 화면마다 다르다)
       if echo "$scr" | grep -qE "잠시 치워두고|stash 후 받아오고|검토 승인하고 PR"; then herdr agent send-keys "$A" down >/dev/null
       else echo "STOP: 예상 밖 선택지 ($c)"; exit 1; fi ;;
  esac
  herdr pane wait-output "$P" --regex zzz_never --timeout 1200 >/dev/null 2>&1
done
echo "STOP: Submit 화면에 도달하지 못함"; exit 1
