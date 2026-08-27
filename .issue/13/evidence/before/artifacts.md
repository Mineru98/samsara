# before — 산출물 단일화 전

$ git rev-parse --abbrev-ref HEAD
feat/13-issue-13

$ node skills/issue-board/scripts/issue-board.mjs --no-open
✓ 보드 생성 — 노드 5개, 엣지 0개
  .issue/board.html  (+ graph-data.js 사이드카)
  .issue/board.standalone.html  (데이터 인라인 · 파일 1개로 완결)
BOARD_HTML=/Users/mineru/SourceCode/samsara-issue-13/.issue/board.html
BOARD_STANDALONE=/Users/mineru/SourceCode/samsara-issue-13/.issue/board.standalone.html
BOARD=ok
  --no-open: 브라우저를 열지 않았다. 위 파일을 직접 열어라.

$ ls -1 .issue/*.html .issue/*.js
.issue/board.html
.issue/board.standalone.html
.issue/graph-data.js

## 산출물 개수
3

## graph.html 존재 여부
graph.html: 없음 (미구현)

## 빈 보드 함정 — board.html 을 단독으로 열면?
외부 참조: script src="graph-data.js"
사이드카 동봉 여부: 없음
→ 렌더러 fallback 이 빈 그래프로 떨어져 에러 없이 빈 보드가 뜬다
