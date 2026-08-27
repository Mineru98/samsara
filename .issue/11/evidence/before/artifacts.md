# before — 수정 전 issue-board 산출물

$ git rev-parse --abbrev-ref HEAD
feat/11-issue-11

$ node skills/issue-board/scripts/issue-board.mjs --no-open
✓ 보드 생성 — 노드 4개, 엣지 0개
  .issue/board.html  (+ graph-data.js 사이드카)
BOARD_HTML=/Users/mineru/SourceCode/samsara-issue-11/.issue/board.html
BOARD=ok
  --no-open: 브라우저를 열지 않았다. 위 파일을 직접 열어라.

$ ls -1 .issue/*.html .issue/*.js
.issue/board.html
.issue/graph-data.js

## standalone 산출물 존재 여부
board.standalone.html: 없음 (미구현)

## board.html 의 외부 참조 (단독 이식 불가 원인)
script src="graph-data.js"
