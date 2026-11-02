#!/usr/bin/env bash
# usage: probe.sh <samsara-binary> <out-file>
# Spins up an isolated daemon, registers a git repo whose origin is
# github.com/Mineru98/samsara, records one manual link, then reports
# snapshot freshness, github-history and refresh responses.
set -u
BIN=$1; OUT=$2
WORK=$(mktemp -d ${TMPDIR:-/tmp}/samsara-probe.XXXX)
export SAMSARA_HOME=$WORK/home XDG_RUNTIME_DIR=$WORK/run
mkdir -p "$XDG_RUNTIME_DIR" && chmod 700 "$XDG_RUNTIME_DIR"
REPO=$WORK/repo
git init -q -b main "$REPO" && git -C "$REPO" commit -q --allow-empty -m init
git -C "$REPO" remote add origin https://github.com/Mineru98/samsara.git
PORT=$((20000 + RANDOM % 20000))
SAMSARA_UI_PORT=$PORT "$BIN" daemon run >"$WORK/daemon.log" 2>&1 &
DPID=$!
for _ in $(seq 50); do "$BIN" daemon status >/dev/null 2>&1 && break; sleep 0.1; done
exec >"$OUT" 2>&1
echo "# binary: $BIN"; echo "# commit: $(git -C "$(dirname "$BIN")" rev-parse --short HEAD 2>/dev/null || echo n/a)"
echo "# date: $(date -u +%FT%TZ)"
cd "$REPO"
echo "## samsara init"; "$BIN" --json init --no-hooks | head -c 600; echo
PID=$("$BIN" --json project list | python3 -c 'import json,sys;d=json.load(sys.stdin);d=d.get("items",d) if isinstance(d,dict) else d;print(d[0]["id"] if d and "id" in d[0] else d[0]["project_id"])')
echo "project_id=$PID"
echo "## samsara link add (manual)"; "$BIN" --json link add --project "$PID" --kind issue --key Mineru98/samsara#66 --url https://github.com/Mineru98/samsara/issues/66 --title manual-link; echo
CODE=$("$BIN" --json ui | python3 -c 'import json,sys;print(json.load(sys.stdin)["url"].split("bootstrap=")[1])')
TOK=$(curl -s -XPOST -H 'Content-Type: application/json' -d "{\"code\":\"$CODE\"}" "http://127.0.0.1:$PORT/v1/ui/sessions" | python3 -c 'import json,sys;print(json.load(sys.stdin)["token"])')
H=(-H "Authorization: Bearer $TOK" -H "Host: 127.0.0.1:$PORT")
echo "## GET snapshot .freshness"
curl -s "${H[@]}" "http://127.0.0.1:$PORT/v1/projects/$PID/snapshot" | python3 -c 'import json,sys;print(json.dumps(json.load(sys.stdin).get("freshness"),indent=1))'
echo "## POST refresh"
curl -s -XPOST "${H[@]}" -H "Idempotency-Key: probe-$RANDOM" "http://127.0.0.1:$PORT/v1/projects/$PID/refresh"; echo
sleep "${PROBE_WAIT:-0}"
# wait (max 120s) until the sync row reports something other than not-configured
for _ in $(seq 120); do
  s=$(curl -s "${H[@]}" "http://127.0.0.1:$PORT/v1/projects/$PID/github-history" | python3 -c 'import json,sys;print(json.load(sys.stdin).get("sync"))')
  [ "$s" != "NOT_CONFIGURED" ] && break; sleep 1
done
echo "## GET github-history (summary)"
curl -s "${H[@]}" "http://127.0.0.1:$PORT/v1/projects/$PID/github-history" | python3 -c '
import json,sys,collections
d=json.load(sys.stdin)
print("freshness =",d.get("freshness")); print("sync =",json.dumps(d.get("sync"),ensure_ascii=False))
items=d.get("items",[]); print("items =",len(items))
print("by kind =",dict(collections.Counter(i["kind"] for i in items)))
for k in ("issue","pull_request","commit","check","review"):
    xs=[i for i in items if i["kind"]==k][:2]
    for i in xs: print(" ",json.dumps({x:i.get(x) for x in ("kind","external_key","title","state","source","synced_at","observed_at") if x in i},ensure_ascii=False)[:220])
'
echo "## GET snapshot .freshness (after refresh)"
curl -s "${H[@]}" "http://127.0.0.1:$PORT/v1/projects/$PID/snapshot" | python3 -c 'import json,sys;print(json.dumps(json.load(sys.stdin).get("freshness"),indent=1))'
echo "## token leak check (DB + daemon log)"
GHTOK=$(gh auth token 2>/dev/null)
if [ -n "$GHTOK" ]; then
  n=$(grep -rlaF "$GHTOK" "$SAMSARA_HOME" "$WORK/daemon.log" 2>/dev/null | wc -l); echo "files containing gh token: $n"
else echo "gh token unavailable"; fi
kill $DPID 2>/dev/null; wait $DPID 2>/dev/null
echo "# workdir: $WORK"
