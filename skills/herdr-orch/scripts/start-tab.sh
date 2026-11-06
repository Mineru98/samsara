#!/usr/bin/env bash
# Open a herdr tab for one task, start cc (or cx), name the agent, send a prompt.
# usage: [ORCH=codex] [LAUNCH="<cmd>"] start-tab.sh <agent-name> <tab-label> <prompt>
#   LAUNCH defaults to "cc --mode=dev --model=opus"
#   (ORCH=codex: "cx --mode=dev --model gpt-6.1-terra -c model_reasoning_effort=high"); merge tabs pass merge-launch.sh output.
# prints: <agent-name> <tab_id> <pane_id> <status>
#   exit 0  working        first prompt was submitted and the agent started
#   exit 2  TRUST_PROMPT   folder trust screen is up; the prompt was not (or may not have been) taken
#   exit 2  HOOKS_REVIEW   codex "hooks need review" screen is up
#   exit 3  NOT_STARTED:<status>  prompt sent but the agent never reached working
#   The script never answers these screens itself; see SKILL.md step 1.
set -euo pipefail
NAME=$1 LABEL=$2 PROMPT=$3
j() { python3 -c "import sys,json;d=json.load(sys.stdin);print($1)"; }
R=$(herdr tab create --workspace "$HERDR_WORKSPACE_ID" --cwd "$PWD" --label "$LABEL" --no-focus)
T=$(echo "$R" | j 'd["result"]["tab"]["tab_id"]'); P=$(echo "$R" | j 'd["result"]["root_pane"]["pane_id"]')
herdr pane rename "$P" "$LABEL" >/dev/null
if [ "${ORCH:-}" = codex ]; then DEF="cx --mode=dev --model gpt-6.1-terra -c model_reasoning_effort=high"; else DEF="cc --mode=dev --model=opus"; fi
herdr pane run "$P" "${LAUNCH:-$DEF}" >/dev/null
for _ in $(seq 1 30); do
  s=$(herdr pane get "$P" | j 'str(d["result"]["pane"].get("agent"))+" "+d["result"]["pane"]["agent_status"]')
  case "$s" in None*) ;; *" idle"|*" done"|*" blocked") break;; esac
  herdr pane wait-output "$P" --regex 'zzz_never' --timeout 2000 >/dev/null 2>&1 || true
done
herdr agent rename "$P" "$NAME" >/dev/null
gate() {
  local v; v=$(herdr pane read "$P" --source visible --lines 60 2>/dev/null || true)
  if grep -Eqi 'one you trust|I trust this folder|trust the contents of this directory' <<<"$v"; then echo "$NAME $T $P TRUST_PROMPT"; exit 2; fi
  if grep -Eqi 'hooks? needs? review' <<<"$v"; then echo "$NAME $T $P HOOKS_REVIEW"; exit 2; fi
}
gate
if herdr agent prompt "$NAME" "$PROMPT" --wait --until working --timeout 15000 >/dev/null 2>&1; then echo "$NAME $T $P working"; exit 0; fi
gate
echo "$NAME $T $P NOT_STARTED:$(herdr agent get "$NAME" | j 'd["result"]["agent"]["agent_status"]')"; exit 3
