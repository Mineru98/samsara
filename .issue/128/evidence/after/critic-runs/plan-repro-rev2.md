# merge 계획 — #1 #4 (rev 2)

경로 변수(메모): REPO=~/workspace/toy-calculate, BASE=$REPO/.issue/merge/base, VDIR=.issue/merge/1-4/verify

## 순서
#15(#4) → #14(#1). overlap 0, 누적 preflight clean(25fac07). 순서는 결과에 영향 없음.

## V. merge 전 검증 (누적 tree 25fac07 에서)
V1. `git -C $BASE checkout --detach 25fac07 && cd $BASE && npm ci`
V2. 통합 테스트 — 각 명령은 `<cmd> > $VDIR/V2-<name>.txt 2>&1; echo "exit=$?" >> $VDIR/V2-<name>.txt` 로 남기고 그 exit 로 판정한다.
  - `npm test` — 41 passed
  - `npm run typecheck` — exit 0
  - `npm run build` — exit 0
  - #4 화면 재현: `npx vite --port 5190 &` 로 띄우고 `$!` 를 PID 로 기록, capture.mjs 로 light/dark × 360/1280 4장을 `--out $VDIR/4-<mode>-<w>.webp`, #4 증거와 imgdiff. 끝나면 `kill $PID`.
  - `.glass` 하드코딩 검사: `grep -cE 'blur\([0-9]+px\)' src/glass.css` — 이 항목만 exit 규칙의 예외다. 통과는 출력 `0` 이고 exit 1 일 때뿐이다. 출력이 1 이상(exit 0)이면 실패, exit 2(파일 없음 등)도 실패다.
V3. V2 전부 통과 시 결과 요약을 사용자에게 보여주고 merge 승인을 받는다. 하나라도 실패하면 merge 하지 않고 해당 PR 보류.

## M. merge (승인 후, PR 하나씩)
K. PR 마다 merge 직전에 (1) 본문의 Closes/Fixes/Resolves 제거, (2) `gh pr view <n> --json closingIssuesReferences` 가 빈 배열인지, (3) `gh pr view <n> --json commits` 의 커밋 메시지에 Closes/Fixes/Resolves 가 없는지 확인한다. 셋 중 하나라도 아니면 merge 하지 않는다.
   merge 는 squash 메시지를 직접 지정한다: `--subject "<PR 제목> (#<n>)" --body ""` — 브랜치 커밋 메시지가 main 커밋에 들어가지 않는다.
H. `gh pr view <n> --json headRefOid` 가 preflight 때 head 와 같은지 확인. 다르면 중단.
M1. `gh pr merge 15 --squash --subject "<제목> (#15)" --body ""`
M2. `gh pr merge 14 --squash --subject "<제목> (#14)" --body ""`

## P. merge 후 통합 테스트
P1. `issue-merge.mjs base-tree` 로 base 를 origin/main 으로 갱신, HEAD tree == 2e2c494 확인
P2. V2 와 같은 명령을 main 에서 다시 실행

## C. close (P 통과 후)
C1. `issue-merge.mjs close 1 4`
C2. `close-check`

## 되돌리기
P 실패 시 해당 mergeCommit 을 revert 하는 PR 을 만들고 사용자 승인 후 merge. 이슈는 열어 둔다.

## X. 정리
X1. `base-tree --remove`, 워크트리 정리

## 검토 반영 (rev 2)
- 1차 blocking 1 (.glass grep 거짓 통과): 판정을 출력 `0` + exit 1 로 고정.
- 1차 blocking 2 (자동 close 경로): K 에 closingIssuesReferences·커밋 메시지 확인 추가, squash 메시지 직접 지정.
- 1차 warnings 5건은 이번 rev 에서 반영하지 않음(실행 시 실패로 멈추는 항목).
