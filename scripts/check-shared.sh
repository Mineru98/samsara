#!/bin/sh
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
tmp=$(mktemp -d "${TMPDIR:-/tmp}/issue-common-check.XXXXXX")
trap 'rm -rf "$tmp"' EXIT HUP INT TERM

files='
skills/issue-create/scripts/issue-common.mjs
skills/issue-end/scripts/issue-common.mjs
skills/issue-merge/scripts/issue-common.mjs
skills/issue-onboard/scripts/issue-common.mjs
skills/issue-start/scripts/issue-common.mjs
skills/issue-sync/scripts/issue-common.mjs
'

reference=''
for rel in $files; do
  file="$root/$rel"
  if [ ! -f "$file" ]; then
    echo "missing shared copy: $rel" >&2
    exit 1
  fi

  block="$tmp/block"
  awk '
    /^function safeGraphTarget\(root\) \{$/ { found = 1 }
    found { print }
  ' "$file" > "$block"
  if [ ! -s "$block" ]; then
    echo "shared cache-safety block not found: $rel" >&2
    exit 1
  fi

  if [ -z "$reference" ]; then
    reference="$tmp/reference"
    cp "$block" "$reference"
  elif ! cmp -s "$reference" "$block"; then
    echo "shared cache-safety block drift: $rel" >&2
    exit 1
  fi
done

echo "shared cache-safety block: 6 copies match"

# -----------------------------------------------------------------------------
# Git 실행 파일 탐지 코드 (이슈 #40, #56)
#
# 탐지 코드는 일부 사본에만 있고, 그 사본들의 주변 코드는 이미 서로 다르다
# (예: issue-sync 에만 trustedRegularFile 이 있다). 그래서 구간을 통째로 비교하지
# 않고 탐지에 쓰이는 최상위 선언을 이름별로 뽑아 비교한다.
#   탐지 그룹  = `function executablePolicy(` 를 가진 사본
#   그룹 안    = 아래 선언이 전부 있고, 이름별로 바이트가 같아야 한다
#   그룹 밖    = 탐지 전용 선언이 하나라도 있으면 부분 사본이라 실패
# -----------------------------------------------------------------------------

detection_names='
UNIX_TRUSTED_COMMAND_DIRS
UNIX_TRUSTED_EXECUTABLE_CANDIDATES
UNIX_TRUSTED_EXECUTABLE_ROOTS
windowsExecutablePolicy
unixExecutablePolicy
executablePolicy
trustedCommandPath
isPathWithin
resolveTrustedExecutable
trustedExecutable
executableMissing
gitFailureMessage
'

# 그룹 밖 사본에 있으면 안 되는 이름. isPathWithin 은 모든 사본에 (옛 형태로) 있으므로 뺀다.
detection_only_pattern='^(export )?(const|function) (UNIX_TRUSTED_[A-Z_]+|windowsExecutablePolicy|unixExecutablePolicy|executablePolicy|trustedCommandPath|resolveTrustedExecutable|trustedExecutable|executableMissing|gitFailureMessage)[ (=]'

# 최상위 선언 하나를 뽑는다. 시작 줄부터 첫 번째 0열 닫힘(`}` `};` `];`)까지.
extract_decl() {
  awk -v name="$2" '
    !found && ($0 ~ "^(export )?function " name "\\(" || $0 ~ "^(export )?const " name " = ") { found = 1 }
    found { print; if ($0 ~ /^(\}|\};|\];)$/) exit }
  ' "$1"
}

group=''
group_count=0
for rel in $files; do
  file="$root/$rel"
  if grep -Eq '^(export )?function executablePolicy\(' "$file"; then
    group="$group $rel"
    group_count=$((group_count + 1))
  elif grep -Eq "$detection_only_pattern" "$file"; then
    echo "git detection partial copy (executablePolicy 없이 탐지 선언만 있음): $rel" >&2
    exit 1
  fi
done

if [ "$group_count" -eq 0 ]; then
  echo "git detection code not found in any shared copy" >&2
  exit 1
fi

for name in $detection_names; do
  ref=''
  ref_rel=''
  for rel in $group; do
    decl="$tmp/decl"
    extract_decl "$root/$rel" "$name" > "$decl"
    if [ ! -s "$decl" ]; then
      echo "git detection declaration missing: $name in $rel" >&2
      exit 1
    fi
    if [ -z "$ref" ]; then
      ref="$tmp/ref-$name"
      ref_rel="$rel"
      cp "$decl" "$ref"
    elif ! cmp -s "$ref" "$decl"; then
      echo "git detection drift: $name ($rel differs from $ref_rel)" >&2
      exit 1
    fi
  done
done

echo "git detection code: $group_count copies match ($(echo $detection_names | wc -w | tr -d ' ') declarations)"
