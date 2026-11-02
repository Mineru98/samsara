#!/usr/bin/env bash
# usage: repro2.sh <skill-dir> <doc.md> <scenario: normal|prior-stash|pop-conflict>
set -u
SKILL="$1"; DOC="$2"; SC="$3"
# 문서의 dirty 절차 bash 블록을 그대로 추출한다
PROC=$(awk '/^#### `dirty`/{d=1} d&&/^```bash/{f=1;next} f&&/^```/{exit} f{print}' "$DOC")
T=$(mktemp -d); cd "$T"; q(){ "$@" >/dev/null 2>&1; }
q git init -q --bare -b main origin.git; q git clone -q origin.git main; cd main
git config user.email t@t; git config user.name t
echo v1 > app.txt; echo notes > notes.txt; git add .; q git commit -qm init; q git push -q origin main
q git clone -q ../origin.git ../other
( cd ../other && git config user.email t@t && git config user.name t
  if [ "$SC" = pop-conflict ]; then echo "remote notes" > notes.txt; else echo v2 > app.txt; fi
  git commit -qam remote && git push -q origin main ) >/dev/null 2>&1
if [ "$SC" = prior-stash ]; then echo "older" > app.txt; git stash push -q -m "someone-else"; fi
echo "my local edit" >> notes.txt; echo scratch > untracked.txt
q git worktree add -q -b feat/1-x ../wt
echo "== 시나리오: $SC"; echo "== 시작 상태"; git status --short; echo "stash: $(git stash list --format=%s | tr '\n' ',')"
FIRST=$(cd ../wt && node "$SKILL/scripts/issue-start.mjs" sync-base --base main 2>&1)
echo "== sync-base 1차: $(echo "$FIRST" | grep -o '"skipped": "[a-z-]*"') $(echo "$FIRST" | tr -d '\n ' | grep -o '"dirtyPaths":\[[^]]*\]')"
echo "== 문서 절차 실행"
PROC2=$(printf '%s\n' "$PROC" | sed "s#^M=<메인 경로>.*#M=$PWD#; s#node <skill>/scripts/issue-start.mjs sync-base#(cd ../wt \&\& node $SKILL/scripts/issue-start.mjs sync-base --base main) | tr -d '\\\\n ' | grep -o '\"ok\":[a-z]*\\\\|\"skipped\":[^,]*\\\\|\"received\":[0-9]*' | tr '\\\\n' ' '; echo#")
bash -c "$PROC2" 2>&1 | sed 's/^/   /'; echo "   exit=${PIPESTATUS[0]}"
echo "== 결과"; echo "HEAD: $(git log --oneline -1)"; echo "app.txt: $(cat app.txt)"; git status --short; echo "stash: $(git stash list --format=%s | tr '\n' ',')"
rm -rf "$T"
