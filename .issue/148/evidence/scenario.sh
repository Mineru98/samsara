#!/bin/sh
# 사용: scenario.sh <issue-start.mjs 절대경로>
# 원격에 main(skills/herdr-orch 있음)·dev/0.4.0-alpha(없음) 가 있고
# .issue/settings.json 의 baseBranch 가 낡은 dev 로 남은 저장소를 만든 뒤 명령을 돌린다.
set -u
SCRIPT="$1"
T=$(mktemp -d "${TMPDIR:-/tmp}/issue148.XXXXXX")
export HOME="$T/home"; mkdir -p "$HOME/.issue"
printf '{"worktree":{"layout":"sibling"}}\n' > "$HOME/.issue/settings.json"
export GIT_AUTHOR_NAME=t GIT_AUTHOR_EMAIL=t@t GIT_COMMITTER_NAME=t GIT_COMMITTER_EMAIL=t@t
q() { git "$@" >/dev/null 2>&1; }

q init --bare -b main "$T/origin.git"
q init -b main "$T/repo"; cd "$T/repo"
printf '.issue/**\n!.issue/*/evidence/**\n' > .gitignore
mkdir -p skills/issue-start && echo a > skills/issue-start/a.md
q add -A; q commit -m init; q remote add origin "$T/origin.git"; q push origin main
q switch -c dev/0.4.0-alpha; q push origin dev/0.4.0-alpha; q switch main
mkdir -p skills/herdr-orch && echo x > skills/herdr-orch/SKILL.md
q add -A; q commit -m herdr; q push origin main
q remote set-head origin main

mkdir -p .issue/129
printf '{"git":{"baseBranch":"dev/0.4.0-alpha","detectedAt":"2026-09-28T00:00:00.000Z"}}\n' > .issue/settings.json
node -e 'require("fs").writeFileSync(".issue/129/issue.json", JSON.stringify({number:129,labels:[{name:"bug"}],body:"## 영향 범위 (추정)\n\n- `skills/herdr-orch/SKILL.md` — 탭 감시\n- `skills/issue-start/a.md`\n"}))'

run() { echo "\$ node issue-start.mjs $*"; node "$SCRIPT" "$@" 2>&1; echo "(exit $?)"; echo; }

echo "## 1. 문서의 기준 변경 명령"
run base --set main
echo "## 2. 낡은 기준(dev/0.4.0-alpha)에서 워크트리 생성"
printf '{"git":{"baseBranch":"dev/0.4.0-alpha","detectedAt":"2026-09-28T00:00:00.000Z"}}\n' > .issue/settings.json
run worktree 129 --slug herdr-tab --no-status
echo "  결과: 워크트리 $(test -d "$T/repo-issue-129" && echo 생성됨 || echo 없음), 그 안의 skills/herdr-orch $(test -e "$T/repo-issue-129/skills/herdr-orch" && echo 있음 || echo 없음)"
echo
q worktree remove --force "$T/repo-issue-129"; q branch -D fix/129-herdr-tab
echo "## 3. 기준을 main 으로 고친 뒤 워크트리 생성"
printf '{"git":{"baseBranch":"main"}}\n' > .issue/settings.json
run worktree 129 --slug herdr-tab --no-status
echo "  결과: 워크트리 $(test -d "$T/repo-issue-129" && echo 생성됨 || echo 없음), 그 안의 skills/herdr-orch $(test -e "$T/repo-issue-129/skills/herdr-orch" && echo 있음 || echo 없음)"
rm -rf "$T"
