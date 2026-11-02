#!/usr/bin/env bash
# Open a herdr tab for one task, start cc, name the agent, send a prompt.
# usage: [LAUNCH="<cmd>"] start-tab.sh <agent-name> <tab-label> <prompt>
#   LAUNCH defaults to "cc --mode=dev --model=opus"; merge tabs pass merge-launch.sh output.
# prints: <agent-name> <tab_id> <pane_id> <status>
set -euo pipefail
NAME=$1 LABEL=$2 PROMPT=$3
j() { python3 -c "import sys,json;d=json.load(sys.stdin);print($1)"; }
R=$(herdr tab create --workspace "$HERDR_WORKSPACE_ID" --cwd "$PWD" --label "$LABEL" --no-focus)
T=$(echo "$R" | j 'd["result"]["tab"]["tab_id"]'); P=$(echo "$R" | j 'd["result"]["root_pane"]["pane_id"]')
herdr pane rename "$P" "$LABEL" >/dev/null
herdr pane run "$P" "${LAUNCH:-cc --mode=dev --model=opus}" >/dev/null
for _ in $(seq 1 30); do
  s=$(herdr pane get "$P" | j 'str(d["result"]["pane"].get("agent"))+" "+d["result"]["pane"]["agent_status"]')
  case "$s" in None*) ;; *" idle"|*" done") break;; esac
  herdr pane wait-output "$P" --regex 'zzz_never' --timeout 2000 >/dev/null 2>&1 || true
done
herdr agent rename "$P" "$NAME" >/dev/null
herdr agent prompt "$NAME" "$PROMPT" --wait --timeout 10000 >/dev/null 2>&1 || true
echo "$NAME $T $P $(herdr agent get "$NAME" | j 'd["result"]["agent"]["agent_status"]')"
