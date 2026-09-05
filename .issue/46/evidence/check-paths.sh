#!/usr/bin/env bash
# 이슈 #46 완료 기준 실측. 저장소 루트에서 실행한다.
# 임시 저장소 네 개를 만들어 각 경로를 돌리고 기대값과 대조한다.
# 모두 통과하면 exit 0, 하나라도 어긋나면 exit 1.
set -uo pipefail

V="$(pwd)/skills/issue-version/scripts/issue-version.mjs"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
fails=0

seed() {  # seed <dir> <공통버전> <package.json 버전>
  local d="$TMP/$1"; mkdir -p "$d"; cd "$d"
  git init -q -b main && git config user.email t@e.com && git config user.name t
  printf '%s\n' "$2" > VERSION
  mkdir -p .claude-plugin .codex-plugin .grok-plugin .zcode-plugin tools/issue-ontology
  printf '{\n  "plugins": [\n    {\n      "version": "%s"\n    }\n  ]\n}\n' "$2" > marketplace.json
  printf '{\n  "plugins": [\n    {\n      "version": "%s"\n    }\n  ]\n}\n' "$2" > .claude-plugin/marketplace.json
  for x in .claude-plugin .codex-plugin .grok-plugin .zcode-plugin; do
    printf '{\n  "version": "%s"\n}\n' "$2" > "$x/plugin.json"
  done
  printf '{\n  "version": "%s"\n}\n' "$3" > tools/issue-ontology/package.json
  git add -A && git commit -qm seed
}

check() {  # check <설명> <실제> <기대>
  if [ "$2" = "$3" ]; then
    printf '  PASS  %s\n' "$1"
  else
    printf '  FAIL  %s  (기대 %s / 실제 %s)\n' "$1" "$3" "$2"
    fails=$((fails + 1))
  fi
}

# 1. 사고 경로 — 태그 0개 + 파일 버전 불일치
seed bad 0.3.2 0.3.3
node "$V" bump patch >/dev/null 2>&1; bump_exit=$?
node "$V" bump patch --force >/dev/null 2>&1; force_exit=$?
node "$V" plan patch >/dev/null 2>&1; plan_exit=$?
drift="$(node "$V" current 2>/dev/null | sed -n 's/^VERSION_DRIFT=//p')"
check "불일치: bump 가 막힌다"          "$bump_exit"  "3"
check "불일치: --force 로도 막힌다"      "$force_exit" "3"
check "불일치: plan 이 막힌다"           "$plan_exit"  "3"
check "불일치: VERSION_DRIFT=1"          "$drift"      "1"
check "불일치: 파일이 그대로다"           "$(cat VERSION)" "0.3.2"

# 2. 태그 0개 + 파일 버전 하나 → 그 값에서 올린다
seed one 0.3.2 0.3.2
out="$(node "$V" plan patch 2>/dev/null)"
check "태그 없음+일치: 파일 값에서 올린다" "$(printf '%s' "$out" | sed -n 's/^NEXT_VERSION=//p')" "0.3.3"
check "태그 없음+일치: 첫 릴리즈가 아니다" "$(printf '%s' "$out" | sed -n 's/^FIRST_RELEASE=//p')" "0"

# 3. 태그도 파일 버전도 없음 → v0.1.0
seed none "" ""
: > VERSION
for f in marketplace.json .claude-plugin/marketplace.json; do
  printf '{\n  "plugins": [\n    {\n      "name": "x"\n    }\n  ]\n}\n' > "$f"
done
for x in .claude-plugin .codex-plugin .grok-plugin .zcode-plugin; do printf '{\n  "name": "x"\n}\n' > "$x/plugin.json"; done
printf '{\n  "name": "x"\n}\n' > tools/issue-ontology/package.json
git add -A && git commit -qm empty
out="$(node "$V" plan patch 2>/dev/null)"
check "버전 전무: v0.1.0"                "$(printf '%s' "$out" | sed -n 's/^NEXT_VERSION=//p')" "0.1.0"
check "버전 전무: FIRST_RELEASE=1"       "$(printf '%s' "$out" | sed -n 's/^FIRST_RELEASE=//p')" "1"

# 4. VERSION 만 비어도 불일치로 오판하지 않는다
seed emptyver 0.3.2 0.3.2
printf '\n' > VERSION
check "VERSION 만 빔: no-tag 로 본다"     "$(node "$V" current 2>/dev/null | sed -n 's/^DRIFT_DIRECTION=//p')" "no-tag"

printf '  → %s\n' "$([ $fails -eq 0 ] && echo '전부 통과' || echo "$fails 건 실패")"
exit $((fails > 0))
