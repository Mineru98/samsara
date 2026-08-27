---
name: issue-board
description: .issue/graph.json 을 칸반 HTML(board.html)로 바로 렌더링해 브라우저로 엽니다. 그래프를 fetch(sync)한 뒤 status↔classify·관계 연결선·상세 패널로 봅니다. "이슈 보드", "보드 열어줘", "$issue-board" 요청에 사용.
---

# Issue Board

`.issue/graph.json` 을 **그대로 칸반 형태 HTML 로 렌더링**해 브라우저로 바로 보는 스킬이다.
GitHub 이슈가 정본이고 graph.json 은 재생성 캐시다. 이 스킬은 그래프를 **fetch(sync)** 한 뒤
`board.html` + `graph-data.js` 사이드카를 만들어 연다. 렌더러는 외부 라이브러리·웹폰트 0 의
self-contained HTML 이라 `file://` 더블클릭으로도 동작한다.

## 입력

```text
$issue-board [--sync] [--no-open]
```

- `--sync`: graph.json 이 있어도 GitHub 에서 새로 fetch(재생성)한다. (없으면 어차피 자동 sync)
- `--no-open`: 파일만 생성하고 브라우저를 열지 않는다(경로만 출력).

## 절차

1. `.issue/graph.json` 을 확인한다. 없거나 `--sync` 면 형제 스킬 `issue-onboard sync --state all` 로 GitHub 전체 snapshot 을 만든다(fetch).
2. graph.json 을 `graph-data.js`(window.__ISSUE_GRAPH__)로 떨어뜨리고, 렌더러 자산 `assets/board.html` 을 `.issue/board.html` 로 복사한다.
   같은 데이터를 `<script>` 안에 인라인한 단일 파일 `.issue/board.standalone.html` 도 함께 만든다 — 이 파일 **하나만** 옮겨도 어디서든 열린다.
3. 기본 브라우저로 `.issue/board.html` 을 연다. 자동 열기 실패 시 파일 경로를 안내한다.

## 보이는 것

- **컬럼 토글**: `status`(open/plan/in-process/review/close) ↔ `classify`(착수 가능/막힘/진행 중/완료). classify 는 depends-on 선수 상태로 계산한다.
- **관계 연결선**: 노드 hover/클릭 시 depends-on/parent-of/duplicate-of/relates-to/supersedes 5종을 방향 표식(화살촉·출발점 점·이동 파티클·중점 라벨)과 함께 그린다. staleEdges 는 점선.
- **연관도(network)**: 완결 이슈까지 포함한 전체 관계망. 노드 hover/클릭 시 이웃과 함께 제목 툴팁.
- **상세 패널**: 카드 클릭 시 우측에 선수/후속·수정 파일(node.files)·context·증거·GitHub 링크.
- 완결(모두 close)된 연결 클러스터는 보드 기본 렌더에서 숨기고 network 에서 확인한다.

## 하드 규칙

- 서버·포트를 열지 않는다(`file://` 자체완결). 상태 변경 같은 **쓰기 동작은 없다**(읽기 전용).
- 그래프 캐시를 직접 부분 수정하지 않는다. 최신화는 fetch(sync)로만 한다.
- 불완전 snapshot(`SNAPSHOT_STATUS≠complete`)이면 보드를 만들지 않고 이유를 알린다.
- `.issue/board.html`·`graph-data.js`·`board.standalone.html` 은 재생성 산출물이라 커밋하지 않는다(graph.json 만 정본 캐시로 추적).
- 다른 머신으로 보드를 옮길 때는 `board.standalone.html` **한 개**를 쓴다. `board.html` 만 떼어 옮기면 사이드카를 못 찾아 **에러 없이 빈 보드**가 뜬다.

## 실행

```bash
node <skill>/scripts/issue-board.mjs            # 있으면 그대로, 없으면 fetch 후 열기
node <skill>/scripts/issue-board.mjs --sync     # 항상 최신 fetch 후 열기
node <skill>/scripts/issue-board.mjs --no-open  # 생성만(경로 출력)
```

## 보고 형식

```text
그래프 fetch(sync) …            (필요 시)
✓ 보드 생성 — 노드 N개, 엣지 M개
  .issue/board.html  (+ graph-data.js 사이드카)
  .issue/board.standalone.html  (데이터 인라인 · 파일 1개로 완결)
BOARD_HTML=<절대경로>
BOARD_STANDALONE=<절대경로>
BOARD=ok
```
