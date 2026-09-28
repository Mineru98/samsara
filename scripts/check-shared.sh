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
#   탐지 그룹  = `function executablePolicy(` 를 가진 사본. detection_files 와 정확히 같아야 한다
#                (한 사본이 탐지 코드를 통째로 잃어도 그룹에서 조용히 빠지지 않게 고정한다)
#   그룹 안    = 아래 선언이 전부 있고, 이름별로 바이트가 같아야 한다
#   그룹 밖    = 탐지 전용 선언이 하나라도 있으면 부분 사본이라 실패
#
# 선언 추출 규칙: 시작 줄부터 0열의 맨 `}` `};` `];` 줄까지. 닫는 줄에 주석을 붙이거나
# 한 줄 선언으로 바꾸면 끝을 못 찾아 실패한다. 탐지 선언은 이 형식을 지킨다.
# 비교 범위는 아래 12개 선언뿐이다. 이를 호출하는 run() 등은 사본마다 주석이 달라 비교하지 않는다.
# -----------------------------------------------------------------------------

detection_files='
skills/issue-onboard/scripts/issue-common.mjs
skills/issue-sync/scripts/issue-common.mjs
'

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
# 시작은 찾았는데 끝 표시 없이 파일이 끝나면 exit 2 — 뒤따르는 선언까지 섞어 비교하지 않는다.
extract_decl() {
  awk -v name="$2" '
    !found && ($0 ~ "^(export )?function " name "\\(" || $0 ~ "^(export )?const " name " = ") { found = 1 }
    found { print; if ($0 ~ /^(\}|\};|\];)$/) { closed = 1; exit } }
    END { if (found && !closed) exit 2 }
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

if [ "$(echo $group)" != "$(echo $detection_files)" ]; then
  echo "git detection group mismatch: expected [$(echo $detection_files)] found [$(echo $group)]" >&2
  exit 1
fi

for name in $detection_names; do
  ref=''
  ref_rel=''
  for rel in $group; do
    decl="$tmp/decl"
    if ! extract_decl "$root/$rel" "$name" > "$decl"; then
      echo "git detection declaration not closed (0열 } }; ]; 없음): $name in $rel" >&2
      exit 1
    fi
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
