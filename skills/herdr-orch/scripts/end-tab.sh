#!/usr/bin/env bash
# Answer "종료" on the final issue-end/issue-start "다음 행동" question and close the agent's tab.
# usage: end-tab.sh <agent-name>
# Moves the cursor down until the cursor line contains 종료 (its number differs per screen), then closes the tab.
set -uo pipefail
A=$1
j() { python3 -c "import sys,json;d=json.load(sys.stdin)['result']['agent'];print(d[\"$1\"])"; }
P=$(herdr agent get "$A" | j pane_id); T=$(herdr agent get "$A" | j tab_id)
c=""
for _ in 1 2 3 4 5 6; do
  c=$(herdr agent read "$A" --source visible | grep "❯" | tail -1)
  case "$c" in *종료*) break;; esac
  herdr agent send-keys "$A" down >/dev/null
  herdr pane wait-output "$P" --regex zzz_never --timeout 400 >/dev/null 2>&1
done
case "$c" in *종료*) ;; *) echo "SKIP: 종료 선택지를 찾지 못함 ($c)"; exit 1;; esac
herdr agent send-keys "$A" enter >/dev/null
herdr pane wait-output "$P" --match "User answered" --source visible --timeout 8000 >/dev/null 2>&1
herdr agent wait "$A" --until working --timeout 10000 >/dev/null 2>&1
herdr agent wait "$A" --until idle --until done --timeout 180000 >/dev/null 2>&1
herdr tab close "$T" >/dev/null && echo "closed $T ($A)"
