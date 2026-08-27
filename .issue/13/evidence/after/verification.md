# after — .issue/graph.html 단일 산출물 검증

## 0. 실행 직전 상태 (옛 산출물 3종이 남아 있는 상황을 재현)
.issue/board.html
.issue/board.standalone.html
.issue/graph-data.js
.issue/graph.html
개수: 4

## 1. 실행 — 옛 산출물 정리 + graph.html 생성
$ node skills/issue-board/scripts/issue-board.mjs --no-open
✓ 보드 생성 — 노드 5개, 엣지 0개
  .issue/graph.html  (데이터 인라인 · 파일 1개로 완결)
  옛 산출물 3개 정리 — board.html, graph-data.js, board.standalone.html
BOARD_HTML=/Users/mineru/SourceCode/samsara-issue-13/.issue/graph.html
BOARD=ok
  --no-open: 브라우저를 열지 않았다. 위 파일을 직접 열어라.

## 2. 산출물 목록 (graph.html 하나여야 한다)
.issue/graph.html
산출물 개수: 1

## 3. 옛 산출물 부재 확인
  board.html: ✓ 없음
  graph-data.js: ✓ 없음
  board.standalone.html: ✓ 없음

## 4. 단독 이식 — 빈 디렉터리에 graph.html 만 복사
$ ls -A /tmp/gh-test
graph.html
외부 참조 개수: 0

## 5. 데이터 무결성 + 렌더 재현
  노드: 5 [1,11,3,4,6]  graph.json 일치: ✓
  digest 보존: ✓
  렌더되는 카드: 5 장

## 6. 옵션 회귀 — --no-open 동작 유지
  --no-open: ✓ 브라우저를 열지 않음

## 7. 재실행 멱등성 — 두 번 돌려도 산출물 1개
  재실행 후 산출물 개수: 1  (목록: .issue/graph.html )
