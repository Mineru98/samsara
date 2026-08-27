# after — standalone 산출물 검증

## 1. 실행 결과 (산출물 3종)
$ node skills/issue-board/scripts/issue-board.mjs --no-open
✓ 보드 생성 — 노드 4개, 엣지 0개
  .issue/board.html  (+ graph-data.js 사이드카)
  .issue/board.standalone.html  (데이터 인라인 · 파일 1개로 완결)
BOARD_HTML=/Users/mineru/SourceCode/samsara-issue-11/.issue/board.html
BOARD_STANDALONE=/Users/mineru/SourceCode/samsara-issue-11/.issue/board.standalone.html
BOARD=ok
  --no-open: 브라우저를 열지 않았다. 위 파일을 직접 열어라.

$ ls -1 .issue/*.html .issue/*.js
.issue/board.html
.issue/board.standalone.html
.issue/graph-data.js

## 2. 단독 이식 검증 — 빈 디렉터리에 standalone 만 복사
$ ls -A /tmp/sa-test
board.standalone.html

외부 참조(src= / href=*.css) 개수:
0
→ 0 이면 이 파일 하나로 완결(네트워크·형제파일 불필요)

## 3. 인라인 데이터 무결성 — graph.json 과 노드 수 대조
graph.json  노드: 4 [1,3,4,6]
standalone  노드: 4 [1,3,4,6]
일치: ✓ 동일
digest 보존: ✓

## 4. 렌더 결과 재현 — 실제로 카드가 그려지는가
  ready    (없음)
  blocked  (없음)
  inprog   (없음)
  done     #1 #3 #4 #6
렌더되는 카드: 4 장  → 빈 보드 아님

## 5. 회귀 확인 — 기존 2파일 산출물 동작 유지
board.html 이 자산 원본과 동일: ✓
graph-data.js 사이드카 존재: ✓
board.html 의 사이드카 참조 유지: ✓

## 6. 엣지 케이스 — 악성 문자열이 데이터에 섞여도 견디는가
테스트 제목: `</script><img src=x onerror=alert(1)> $& $` + 백틱/따옴표/$1 조합

인라인 블록 안의 미이스케이프 </script>: 0 ✓ 없음(파서 조기종료 불가)
제목 원문 보존: ✓ 정확히 일치
  기대: "</script><img src=x onerror=alert(1)> $& $` $' $1"
  실제: "</script><img src=x onerror=alert(1)> $& $` $' $1"
문자열 치환자였다면: 데이터 손상 발생
