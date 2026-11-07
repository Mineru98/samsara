# merge 계획 — #1 #4 (rev 1, 반례)

경로 변수(메모): REPO=~/workspace/toy-calculate, BASE=$REPO/.issue/merge/base, VDIR=$REPO/.issue/merge/1-4/verify

## 순서
#15(#4) → #14(#1). overlap 0, 누적 preflight clean(25fac07).

## M. merge (사용자 승인 후, PR 하나씩)
K. merge 직전 PR 본문의 Closes/Fixes/Resolves 제거 후 `gh pr view` 로 0건 확인. 0 이 아니면 merge 하지 않음.
H. `gh pr view <n> --json headRefOid` 가 preflight 때 head 와 같은지 확인. 다르면 중단.
M1. `gh pr merge 15 --squash`
M2. `gh pr merge 14 --squash`

## C. close
C1. `issue-merge.mjs close 1 4`
C2. `close-check`

## P. 통합 테스트 (main 에서)
P1. `issue-merge.mjs base-tree` 로 base 를 origin/main 으로 갱신
P2. `cd $BASE && npm ci && npm test && npm run typecheck && npm run build` — 각 exit 0
P3. #4 화면 재현: vite 를 5190 에 띄우고 capture.mjs 로 4장, #4 증거와 imgdiff 0
P 실패 시 해당 mergeCommit 을 revert 하는 PR 을 만들고 이슈를 다시 연다.

## X. 정리
X1. `base-tree --remove`, 워크트리 정리
