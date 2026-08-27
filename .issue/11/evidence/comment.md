## 작업 요약

`issue-board` 가 graph 데이터를 `<script>` 안에 인라인한 단일 파일 `.issue/board.standalone.html` 을 항상 함께 생성하도록 했습니다. 이 파일 **하나만** 복사하면 어떤 머신에서든 `file://` 로 열립니다.

기존 `board.html` + `graph-data.js` 산출물은 그대로 두는 **순수 추가**(삭제 0줄)라 하위 호환이 깨지지 않습니다. 구현 커밋은 `cbeb9a52a0e3bc7cb9614915d07fad9769a64974` 입니다.

## 문제 배경

`board.html:372` 이 `<script src="graph-data.js">` 로 데이터를 상대 경로로 읽어, 두 파일이 항상 같은 디렉터리에 붙어 있어야 했습니다. 문제는 실패가 드러나지 않는다는 점이었습니다 — `board.html:374` 의 `const G = (window.__ISSUE_GRAPH__ || {nodes:{}, edges:[]})` fallback 탓에 사이드카를 못 찾아도 **콘솔 에러 하나 없이 "정상처럼 보이는 빈 보드"** 가 렌더링됩니다. 게다가 `.gitignore` 의 `.issue/**` 규칙상 두 산출물 모두 저장소로 따라가지 않아, clone 한 머신에는 렌더러 자체가 없습니다.

## 변경 파일

- `skills/issue-board/scripts/issue-board.mjs` — standalone 생성 로직, `BOARD_STANDALONE=` 출력 추가 (+23줄)
- `skills/issue-board/SKILL.md` — 산출물·보고 형식·하드 규칙에 standalone 반영

## 구현 요점

- `</script>` 시퀀스를 이스케이프해 HTML 파서 조기 종료를 막습니다(JSON 에서 `\/` 는 `/` 로 읽히므로 데이터는 보존됩니다).
- 치환자를 **함수로** 넘겨 데이터 안의 `$&` 같은 패턴이 특수 해석되지 않게 합니다. 문자열 치환자를 쓰면 실제로 데이터가 손상됩니다(아래 검증 6번).
- standalone 생성이 실패해도 경고만 남기고 기존 2파일 산출물은 정상 생성됩니다.

## 검증

- 실행 시 산출물 **3종** 생성 확인, `BOARD_STANDALONE=` 출력 확인
- 빈 임시 디렉터리에 `board.standalone.html` **단독** 배치 → 외부 참조(`src=`/`href=*.css`) **0개**
- 인라인 데이터 무결성: 노드 `[1,3,4,6]` 4개가 `graph.json` 과 동일, `snapshot.digest` 보존
- 렌더 로직(숨김 필터·컬럼 버킷팅) 재현 → 카드 **4장** 렌더 확인(빈 보드 아님)
- 회귀: `board.html` 이 자산 원본과 바이트 동일, 사이드카 참조·`graph-data.js` 생성 모두 유지
- 엣지 케이스: 제목에 `</script><img src=x onerror=alert(1)> $& $` $' $1` 를 넣어도 미이스케이프 `</script>` 0개, 제목 원문 정확히 보존. 같은 조건에서 문자열 치환자는 데이터 손상 발생 확인

## 증거

브라우저 화면이나 실행 중인 서비스가 없는 도구 스크립트 작업이라 webp 캡처를 생략하고 CLI 검증 기록을 남겼습니다.

- `.issue/11/evidence/before/artifacts.md` — 수정 전 산출물 2종, standalone 부재, 외부 참조 1개
- `.issue/11/evidence/after/verification.md` — 산출물 3종, 단독 이식·무결성·렌더·회귀·엣지 케이스 6항목

## 남은 이슈

- 사이드카 로드 실패를 조용히 삼키는 `|| {nodes:{}, edges:[]}` fallback 자체는 이번 범위에 넣지 않았습니다. standalone 이 생겨도 2파일 경로는 남으므로, 로드 실패 시 경고를 띄우는 별도 이슈로 다룰 가치가 있습니다.
