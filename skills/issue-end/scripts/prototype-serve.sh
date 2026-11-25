#!/usr/bin/env bash
# 시각적 의사결정용 정적 프로토타입을 띄우고 접속 URL 을 출력한다.
#
#   prototype-serve.sh <dir> [port]   tailscale 이 로그인돼 있으면 tailnet HTTPS, 아니면 localhost
#   prototype-serve.sh stop <port>    매핑과 로컬 서버를 내린다
#
# 출력 마지막 줄 형식: url: <URL>   (mode: tailscale | localhost)
set -euo pipefail

PORT_MIN=19400
PORT_MAX=19499

die() { echo "error: $*" >&2; exit 1; }

command -v python3 >/dev/null || die "python3 not found"

port_busy() {
  python3 - "$1" <<'PY'
import socket, sys
s = socket.socket()
try:
    s.bind(("127.0.0.1", int(sys.argv[1])))
except OSError:
    sys.exit(0)
sys.exit(1)
PY
}

ts_ready() {
  command -v tailscale >/dev/null 2>&1 \
    && tailscale status --self --json 2>/dev/null \
    | python3 -c 'import json,sys; sys.exit(0 if json.load(sys.stdin).get("BackendState")=="Running" else 1)' 2>/dev/null
}

if [[ "${1:-}" == "stop" ]]; then
  port="${2:-}"
  [[ "$port" =~ ^[0-9]+$ ]] || die "usage: prototype-serve.sh stop <port>"
  if command -v tailscale >/dev/null 2>&1; then
    tailscale serve --https="$port" off >/dev/null 2>&1 || true
  fi
  pidfile="${TMPDIR:-/tmp}/samsara-prototype-$port.pid"
  [[ -f "$pidfile" ]] && kill "$(cat "$pidfile")" 2>/dev/null || true
  rm -f "$pidfile"
  echo "stopped: $port"
  exit 0
fi

dir="${1:-}"
[[ -n "$dir" && -d "$dir" ]] || die "usage: prototype-serve.sh <dir> [port]"
dir="$(cd "$dir" && pwd)"
[[ -f "$dir/index.html" ]] || die "$dir/index.html not found"

port="${2:-}"
if [[ -z "$port" ]]; then
  for p in $(seq "$PORT_MIN" "$PORT_MAX"); do
    port_busy "$p" || { port="$p"; break; }
  done
  [[ -n "$port" ]] || die "no free port in $PORT_MIN-$PORT_MAX"
elif port_busy "$port"; then
  die "port $port is already in use"
fi

log="${TMPDIR:-/tmp}/samsara-prototype-$port.log"
pidfile="${TMPDIR:-/tmp}/samsara-prototype-$port.pid"
nohup python3 -m http.server "$port" --bind 127.0.0.1 --directory "$dir" >"$log" 2>&1 &
echo $! >"$pidfile"
disown

for _ in $(seq 1 20); do
  curl -fs -o /dev/null "http://127.0.0.1:$port/" && break
  sleep 0.25
done
curl -fs -o /dev/null "http://127.0.0.1:$port/" || die "local server did not start (log: $log)"

if ts_ready; then
  tailscale serve --bg --https="$port" "http://127.0.0.1:$port" >/dev/null
  host=$(tailscale status --self --json | python3 -c 'import json,sys; print(json.load(sys.stdin)["Self"]["DNSName"].rstrip("."))')
  url="https://$host:$port/"
  code=$(curl -s -m 10 -o /dev/null -w '%{http_code}' "$url" || true)
  [[ "$code" == "200" ]] || die "tailnet URL returned $code: $url (stop with: prototype-serve.sh stop $port)"
  echo "mode: tailscale"
else
  url="http://localhost:$port/"
  echo "mode: localhost"
fi

echo "dir:  $dir"
echo "port: $port"
echo "url:  $url"
