#!/bin/sh
# Install smoke + upgrade + rollback drill (05 §17.1–17.3) for the host
# platform. Everything runs from release tarballs in an isolated
# SAMSARA_HOME and a PATH without Go or the source tree; nothing outside the
# temporary directory is touched.
#
#   runtime/scripts/drill.sh            # keep going, print PASS/FAIL per step
#   KEEP=1 runtime/scripts/drill.sh     # keep the temporary directory
#
# Needs: go (only to build the two tarballs), git, tar, python3 (JSON reads).
set -u

here=$(cd "$(dirname "$0")/.." && pwd)
tmp=$(mktemp -d)
fails=0
os=$(go env GOOS); arch=$(go env GOARCH); gobin=$(command -v go)
v1=0.4.0-drill.1; v2=0.4.0-drill.2

cleanup() {
  [ -x "${link:-}" ] && "$link" daemon stop >/dev/null 2>&1
  if [ "${KEEP:-}" = 1 ]; then echo "kept $tmp"; else rm -rf "$tmp"; fi
}
trap cleanup EXIT

step() { printf '\n== %s\n' "$*"; }
pass() { echo "PASS  $*"; }
fail() { echo "FAIL  $*"; fails=$((fails + 1)); }
check() { if eval "$1"; then pass "$2"; else fail "$2"; fi; }
json() { python3 -c "import json,sys; d=json.load(open(sys.argv[1])); print(eval(sys.argv[2]))" "$@" 2>/dev/null; }

step "build release tarballs ($os/$arch)"
"$here/scripts/release.sh" --version $v1 --out "$tmp/rel1" --targets "$os/$arch" >/dev/null || exit 1
"$here/scripts/release.sh" --version $v2 --out "$tmp/rel2" --targets "$os/$arch" >/dev/null || exit 1
(cd "$tmp/rel1" && sha256sum -c SHA256SUMS >/dev/null 2>&1 || shasum -a 256 -c SHA256SUMS >/dev/null) &&
  pass "SHA256SUMS verifies" || fail "SHA256SUMS verifies"
mkdir -p "$tmp/pkg1" "$tmp/pkg2"
tar -C "$tmp/pkg1" -xzf "$tmp/rel1/samsara_${v1}_${os}_${arch}.tar.gz"
tar -C "$tmp/pkg2" -xzf "$tmp/rel2/samsara_${v2}_${os}_${arch}.tar.gz"

# Isolation: no Go, no source tree, a throwaway SAMSARA_HOME.
export SAMSARA_HOME="$tmp/home"
export PATH="/usr/local/bin:/usr/bin:/bin"
unset XDG_RUNTIME_DIR XDG_STATE_HOME XDG_DATA_HOME
check "[ \"\$(command -v go)\" != '$gobin' ]" "build Go toolchain is not on PATH"
check "[ \"\$(cd / && command -v samsara)\" = '' ]" "no samsara on PATH"

step "install smoke (05 §17.1)"
"$tmp/pkg1/samsara" install > "$tmp/install.json"
link=$(json "$tmp/install.json" "d['install']['link']")
v1bin=$(json "$tmp/install.json" "d['install']['binary']")
check "[ -x '$link' ]" "install link exists: ${link#$tmp/}"
"$link" version > "$tmp/version.json"
check "[ \"\$(json $tmp/version.json \"d['version']\")\" = $v1 ]" "installed binary reports $v1"
"$link" doctor > "$tmp/doctor.json"; rc=$?
check "[ $rc = 0 ] || [ $rc = 4 ]" "doctor runs read-only (exit $rc: $(json "$tmp/doctor.json" "'; '.join(d['problems']) or 'no problems'"))"
check "[ -n \"\$(json $tmp/doctor.json \"d['sqlite_version']\")\" ]" "doctor sees bundled SQLite $(json "$tmp/doctor.json" "d['sqlite_version']")"
check "[ \"\$(json $tmp/doctor.json \"d['daemon_running']\")\" = False ]" "doctor does not start the daemon"

repo="$tmp/repo"
mkdir -p "$repo" && git -C "$repo" init -q && git -C "$repo" symbolic-ref HEAD refs/heads/main && git -C "$repo" -c user.name=d -c user.email=d@d commit -q --allow-empty -m init
"$link" init --path "$repo" > "$tmp/init.json" 2>&1
check "[ -f '$repo/.samsara/config.yaml' ]" "init registers a project"
check "grep -q '$v1bin' '$repo/.claude/settings.local.json'" "hooks point at the versioned $v1 binary"
"$link" daemon status > "$tmp/status1.json"
check "[ \"\$(json $tmp/status1.json \"d['runtime']['version']\")\" = $v1 ]" "daemon $v1 healthy"
gen1=$(json "$tmp/status1.json" "d['runtime']['database_generation']")
"$link" metrics > "$tmp/metrics.json"
check "[ -n \"\$(json $tmp/metrics.json \"d['gauges']['event_retention_days']\")\" ]" "metrics answer (retention $(json "$tmp/metrics.json" "d['gauges']['event_retention_days']") days)"

step "upgrade $v1 -> $v2 (05 §17.2)"
"$tmp/pkg2/samsara" upgrade > "$tmp/upgrade.json"; rc=$?
check "[ $rc = 0 ]" "upgrade exit 0"
check "[ \"\$(json $tmp/upgrade.json \"d['upgrade']['from_version']\")\" = $v1 ]" "old daemon $v1 stopped first"
backup=$(json "$tmp/upgrade.json" "d['upgrade']['backup']")
check "[ -f '$backup' ]" "pre-upgrade backup written"
check "[ \"\$(json $tmp/upgrade.json \"d['upgrade']['integrity']\")\" = ok ]" "migration + integrity check ok"
check "[ \"\$(json $tmp/upgrade.json \"d['upgrade']['daemon_version']\")\" = $v2 ]" "new daemon $v2 started"
check "[ \"\$(readlink '$link')\" = \"\$(json $tmp/upgrade.json \"d['upgrade']['install']['binary']\")\" ]" "link now points at $v2"
check "[ -x '$v1bin' ]" "ACT-12: $v1 binary kept"
v2bin=$(json "$tmp/upgrade.json" "d['upgrade']['install']['binary']")
check "grep -q '$v2bin' '$repo/.claude/settings.local.json'" "project hooks retargeted to $v2 (old binary cannot open a migrated DB)"

step "rollback $v2 -> $v1 with restore (05 §17.3)"
"$link" daemon stop >/dev/null
"$v1bin" restore --from "$tmp/does-not-exist.db" >/dev/null 2>&1
check "[ $? != 0 ]" "restore refuses a missing backup"
"$link" daemon start >/dev/null
"$v1bin" restore --from "$backup" > "$tmp/restore-running.json" 2>&1; rc=$?
check "[ $rc = 4 ]" "restore refuses while the daemon runs (exit $rc)"
"$link" daemon stop >/dev/null
"$v1bin" install > "$tmp/reinstall.json"
check "grep -q '$v1bin' '$repo/.claude/settings.local.json'" "rollback install points hooks back at $v1"
"$v1bin" restore --from "$backup" > "$tmp/restore.json"; rc=$?
check "[ $rc = 0 ]" "restore exit 0"
gen2=$(json "$tmp/restore.json" "d['restore']['database_generation']")
check "[ -n '$gen2' ] && [ '$gen2' != '$gen1' ]" "database_generation rotated (${gen1#dbgen_} -> ${gen2#dbgen_})"
check "[ -f \"\$(json $tmp/restore.json \"d['restore']['pre_restore_backup']\")\" ]" "pre-restore backup kept"
"$link" daemon start > "$tmp/status2.json"
check "[ \"\$(json $tmp/status2.json \"d['runtime']['version']\")\" = $v1 ]" "daemon back on $v1"
check "[ \"\$(json $tmp/status2.json \"d['runtime']['database_generation']\")\" = '$gen2' ]" "daemon serves the new generation"

step "prune old versions (ACT-12)"
"$link" daemon stop >/dev/null
"$link" upgrade --start --prune-old > "$tmp/prune.json"; rc=$?
check "[ $rc = 0 ] && [ \"\$(json $tmp/prune.json \"d['prune']['removed']\")\" = \"['$v2']\" ]" "unused $v2 removed, current $v1 kept (exit $rc)"
check "[ -x '$v1bin' ] && [ ! -e '$v2bin' ]" "only the version in use remains on disk"

step "diagnostics export (no secrets)"
secret_tok=ghp_DRILLDRILLDRILLDRILLDRILL0123456789
secret_prompt="drill user prompt: refactor billing"
printf '{"level":"WARN","msg":"gh failed","err":"token=%s","prompt":"%s"}\n' "$secret_tok" "$secret_prompt" >> "$SAMSARA_HOME/state/daemon.log"
echo "Authorization: Bearer $secret_tok" >> "$SAMSARA_HOME/state/daemon.log"
"$link" doctor --export "$tmp/diag.json" > "$tmp/doctor-export.json"
check "[ -f '$tmp/diag.json' ]" "export written ($(json "$tmp/doctor-export.json" "d['export']['redacted_values']") values redacted)"
check "! grep -q '$secret_tok' '$tmp/diag.json' && ! grep -q 'refactor billing' '$tmp/diag.json'" "planted token and prompt absent from export"
check "[ \"\$(stat -c %a '$tmp/diag.json' 2>/dev/null || stat -f %Lp '$tmp/diag.json')\" = 600 ]" "export is private (0600)"
cp "$tmp/diag.json" "${DRILL_EXPORT:-/dev/null}" 2>/dev/null

printf '\n%s\n' "$( [ $fails = 0 ] && echo "DRILL PASSED" || echo "DRILL FAILED: $fails step(s)")"
[ $fails = 0 ]
