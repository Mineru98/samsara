#!/usr/bin/env bash
# usage: run.sh <worktree> — runs start-tab.sh / start-finish.sh of that tree against fake herdr
W=$1; H=$(cd "$(dirname "$0")" && pwd); export PATH="$H/bin:$PATH" HERDR_WORKSPACE_ID=fW
M=$(mktemp -d); git -C "$M" init -q
for sc in normal update enter-miss dead; do
  export FAKE_SCENARIO=$sc FAKE_STATE=$(mktemp -d)
  echo "## start-tab · $sc"
  echo "\$ LAUNCH=\"cx --model gpt-6-astra -c model_reasoning_effort=low\" start-tab.sh merge-1-2 \"#1 #2 merge\" '\$issue-merge #1 #2'"
  LAUNCH="cx --model gpt-6-astra -c model_reasoning_effort=low" bash "$W/skills/herdr-orch/scripts/start-tab.sh" merge-1-2 "#1 #2 merge" '$issue-merge #1 #2'; echo "exit=$?"
  echo "# herdr agent prompt/send-keys/wait 호출"; grep -E 'agent (prompt|send-keys|wait)' "$FAKE_STATE/calls.log" | sed 's/^/  /'; echo
done
export FAKE_SCENARIO=normal FAKE_STATE=$(mktemp -d)
echo "## start-tab · normal + START_TIMEOUT_MS=40000"
START_TIMEOUT_MS=40000 bash "$W/skills/herdr-orch/scripts/start-tab.sh" issue-9 "#9 start" "/issue-start #9"; echo "exit=$?"
grep -E 'agent prompt' "$FAKE_STATE/calls.log" | sed 's/^/  /'; echo
for sc in finish-canon finish-variant; do
  export FAKE_SCENARIO=$sc FAKE_STATE=$(mktemp -d)
  echo "## start-finish · $sc"; echo "# 첫 화면"; FAKE_STATE=$(mktemp -d) herdr agent read x --source visible | sed 's/^/  /'
  bash "$W/skills/herdr-orch/scripts/start-finish.sh" issue-9 "$M"; echo "exit=$?"
  echo "# 보낸 키"; grep -E 'send-keys' "$FAKE_STATE/calls.log" | sed 's/^/  /'; echo
done
