#!/bin/sh
# usage: repro-90.sh <skill-dir>   (issue-start 스킬 디렉터리)
SKILL=$(cd "$1" && pwd)
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
export HOME="$T/home"; mkdir -p "$HOME/.issue"
echo '{"worktree":{"layout":"sibling"}}' > "$HOME/.issue/settings.json"
export GIT_AUTHOR_NAME=t GIT_AUTHOR_EMAIL=t@t GIT_COMMITTER_NAME=t GIT_COMMITTER_EMAIL=t@t
mkdir -p "$T/bin"; cat > "$T/bin/gh" <<'G'
#!/bin/sh
[ "$1 $2" = "pr list" ] && { echo "[{\"baseRefName\":\"$FAKE_PR_BASE\"},{\"baseRefName\":\"$FAKE_PR_BASE\"},{\"baseRefName\":\"main\"}]"; exit 0; }
exit 1
G
chmod +x "$T/bin/gh"; export PATH="$T/bin:$PATH"
q() { git "$@" >/dev/null 2>&1; }
setup() {  # $1=name $2=main-checkout-branch $3=recorded
  R="$T/$1"; q init --bare -b main "$T/$1.git"
  q clone "$T/$1.git" "$R"; cd "$R"
  printf '.issue/**\n!.issue/*/\n!.issue/*/evidence/\n!.issue/*/evidence/**\n' > .gitignore
  q add .gitignore; q commit -m init; q push origin main
  q checkout -b dev/x; q push origin dev/x; q checkout "$2"
  q remote set-head origin main
  mkdir -p .issue; echo "{\"git\":{\"baseBranch\":\"$3\"}}" > .issue/settings.json
}
show() { echo "\$ $*"; "$@" 2>&1 | grep -E 'BASE_|브랜치|ERROR|exit|SOURCE|BRANCH=' ; echo "  → exit $?"; }
wt() { node "$SKILL/scripts/issue-start.mjs" worktree "$1" --slug t --no-status; rc=$?; echo "exit=$rc"; }
base_in() { (cd "$1" && node -e "import('$SKILL/scripts/issue-common.mjs').then(m=>console.log('detectBase =', m.detectBase(process.cwd())))"); }

echo "== A. 기록 main / 주 체크아웃 dev/x / 최근 PR base dev/x =="
export FAKE_PR_BASE=dev/x
setup a dev/x main
wt 1 2>&1 | grep -E 'BASE_|브랜치|exit=|ERROR'
echo
echo "== B. 'base --set dev/x' 로 기록 후 다시 실행 =="
node "$SKILL/scripts/issue-start.mjs" base --set dev/x 2>&1 | grep -E 'BASE|SOURCE|Usage|알 수 없는' | head -3
wt 2 2>&1 | grep -E 'BASE_|브랜치|exit=|ERROR'
echo "  기록값: $(cat .issue/settings.json | tr -d ' \n')"
echo
echo "== C. 링크된 워크트리 안에서 기준 브랜치 판별 (주 체크아웃 기록 dev/x) =="
setup c dev/x dev/x
q worktree add -b fix/3-t "$T/c-wt" origin/dev/x
base_in "$T/c-wt"
echo "  워크트리 쪽 기록 파일: $( [ -f "$T/c-wt/.issue/settings.json" ] && tr -d ' \n' < "$T/c-wt/.issue/settings.json" || echo 없음)"
echo
echo "== D. 기록·신호 일치 저장소 (기록 main / 주 체크아웃 main / PR base main) =="
export FAKE_PR_BASE=main
setup d main main
wt 4 2>&1 | grep -E 'BASE_|브랜치|exit=|ERROR'
