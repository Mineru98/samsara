## 작업 요약

대시보드 브라우저 시나리오 UI-T01~T24(02 §25.2)를 Playwright 로 자동화했습니다. 실제 daemon 으로 F-ABC·F-JOIN·F-STREAM 의 화면 version 이 서버 projection 과 같은지, 브라우저를 닫아도 runtime·worker 가 계속 도는지도 확인합니다.
02 §22 성능 예산은 seed 109 의 200·1,000노드로 측정했고, 1,000노드 상태 반영 지연(p95 331ms, 예산 250ms 초과)은 그래프 노드 재사용으로 고쳤습니다.
시나리오가 드러낸 공백 둘도 고쳤습니다: 상세 패널을 Escape 로 닫고 연 요소로 포커스를 돌려줍니다(02 §20.1). 767px 이하에서는 목록을 기본 보기로 엽니다(02 §19).

## 성능 비교 (02 §22, `pnpm -C web perf`, 5회 반복)

| 지표 | 1차 측정 (최적화 전) | 최종 | 변화 | 예산 |
| --- | ---: | ---: | ---: | --- |
| 1,000노드 상태 반영 p95 (50 ev/s · 500건) | 331ms | 39ms | **-88%** | ≤ 250ms |
| 1,000노드 최초 유용 화면 p95 | 497ms | 838ms | **+69%** | ≤ 2,000ms |
| 1,000노드 ELK 배치 p95 | 2,379ms | 1,754ms | **-26%** | 예산 없음(200노드 기준), worker 제한 5,000ms 안 |
| 200노드 ELK 배치 p95 | 965ms | 810ms | **-16%** | ≤ 1,000ms |
| 200노드 최초 유용 화면 p95 | 366ms | 352ms | **-4%** | ≤ 2,000ms |
| 200노드 상태 반영 p95 | 93ms | 29ms | **-69%** | ≤ 250ms |

- 1차 측정은 측정 하네스를 처음 붙였을 때의 2회 반복 값이고, 최종은 5회 반복입니다. 측정 중 load average 가 3~5(4코어)라 절댓값이 흔들립니다. 1,000노드 첫 화면 +69% 가 그 흔들림 범위로 보이지만, 숨기지 않고 그대로 적습니다. 원본은 `.issue/109/evidence/after/perf.md`·`perf.json` 입니다.
- 이벤트 누락 0건이고, 측정이 끝난 뒤 화면 `project_version` 이 서버와 같습니다.
- 메모리(2시간 soak)와 10,000 이벤트 목록 스크롤은 `미측정 (이번 범위 밖, 장시간 측정 필요)` 입니다.
- 1,000노드 측정 데이터는 계약 형태로 만든 fake 데이터입니다. 실제 runtime 은 1,000 이슈 계획을 적용하지 못했습니다. before 측정에서 `samsara plan apply` 가 100개 0.23s, 200개 1.00s, 400개 6.15s 로 늘다가 1,000개에서 60초 IPC 제한을 넘겼습니다(`.issue/109/evidence/before/baseline.md`).

## 변경 전후

| 전 | 후 |
| --- | --- |
| ![F-ABC 3노드 실제 runtime 화면 - 전](https://raw.githubusercontent.com/Mineru98/samsara/dev/0.4.0-alpha/.issue/109/evidence/before/abc-dag.webp) | ![F-ABC 3노드 실제 runtime 화면 - 후](https://raw.githubusercontent.com/Mineru98/samsara/dev/0.4.0-alpha/.issue/109/evidence/after/abc-dag.webp) |

빨간 박스가 그래프 영역입니다. 같은 1440x900, 같은 실제 runtime 데이터입니다. 3노드 화면은 일부러 바꾸지 않았고, 회귀가 없음을 보여 줍니다.

<details>
<summary>대규모 그래프 화면 (before 1장 / after 2장)</summary>

| 화면 | 캡처 |
| --- | --- |
| 400노드 실제 runtime - 전 | ![400노드 실제 runtime 화면 - 전](https://raw.githubusercontent.com/Mineru98/samsara/dev/0.4.0-alpha/.issue/109/evidence/before/graph-400.webp) |
| 400노드 실제 runtime - 후 | ![400노드 실제 runtime 화면 - 후](https://raw.githubusercontent.com/Mineru98/samsara/dev/0.4.0-alpha/.issue/109/evidence/after/graph-400.webp) |
| 1,000노드 fake 데이터 - 후 | ![1,000노드 fake 데이터 화면 - 후](https://raw.githubusercontent.com/Mineru98/samsara/dev/0.4.0-alpha/.issue/109/evidence/after/graph-1000.webp) |

빨간 박스가 그래프 영역입니다. 400·1,000노드는 수치 예산은 맞지만, fitView 가 최소 배율 0.1 에 걸려 카드 글자를 읽을 수 없습니다. 02 §22 의 "기본 집약·필터·분할" 은 사용자 결정에 따라 별도 이슈로 분리합니다.
</details>

## 브라우저 시나리오 (`pnpm -C web e2e`, 27개 통과)

- `e2e/ui.spec.ts` — UI-T01~T24 중 23개. runtime 이 직접 쓴 계약 fixture(`runtime/api/fixtures`)로 시드한 in-process fake runtime 에서 돌립니다. SSE 단절·중복 재생·`reset_required`·cursor 만료·JSON/UTF-8 중간 청크 분할·인증 만료·daemon 재시작·412/409 응답을 주입합니다.
- `e2e/runtime.spec.ts` — 실제 `samsara` daemon(격리 `SAMSARA_HOME`, tmux backend, `sleep` worker) 4개
  - F-ABC·F-JOIN·F-STREAM: 화면 `project_version` 과 모든 이슈 노드 version 이 `GET /v1/projects/{id}/snapshot` 과 같은지 확인합니다.
  - UI-T13: 브라우저를 닫고 6초 뒤에도 daemon PID 가 같고, 그 테스트의 worker 2개(tmux pane 기준)가 살아 있고, run 이 ACTIVE 인지 확인합니다. 이어서 CLI 로 일시 정지한 상태가 재접속 화면에 반영되는지 봅니다.
- 시나리오별 결과: `.issue/109/evidence/after/e2e-report.md`. 최종 커밋에서 전체 연속 2회, 실제 runtime 시나리오 연속 3회 통과했습니다.

## 변경 파일

- `web/src/features/graph/IssueGraph.tsx` — 바뀌지 않은 노드·간선 객체 재사용, `data-layout` 노출
- `web/src/features/graph/layout.ts`, `ProjectPage.tsx` — layout worker 미리 기동, 767px 이하 목록 기본 보기
- `web/src/features/inspector/Inspector.tsx`, `queue/MergeQueuePanel.tsx`, `hooks.ts` — Escape 닫기·포커스 복귀, `useMediaQuery`
- `web/e2e/**`, `web/playwright.config.ts` — fake runtime, 실제 runtime 하네스, UI·runtime·perf spec
- `web/package.json`(`e2e`·`perf`, `@playwright/test` 1.64.1), `web/tsconfig.json`(e2e 타입 검사), `.gitignore`, `runtime/README.md`

## 검증

- `pnpm -C web typecheck`(e2e 포함) / `pnpm -C web test`(28개) / `pnpm -C web build` 통과
- `pnpm -C web e2e` 27개, `pnpm -C web perf` 2개 통과
- `go vet ./...` / `go test ./...` 통과(runtime 코드는 바꾸지 않음)
- 독립 리뷰 1회. 지적된 점을 반영했습니다.
  - UI-T13 이 다른 테스트의 worker 로도 통과할 수 있던 문제를 고쳤습니다.
  - 공용 tmux 서버를 e2e 가 처음 띄울 때 환경 변수가 새는 문제를 막았습니다.
  - teardown 에서 정리가 빠질 수 있던 문제를 고쳤습니다.
  - Merge Queue 에서 연 상세의 포커스 복귀를 고쳤습니다.
  - 빈 단언을 없앴습니다.

## 남은 이슈

- 1,000노드 기본 집약·필터·분할(02 §22) — 별도 이슈로 분리
- 실제 runtime `plan apply` 가 이슈 수에 따라 초선형으로 느려지고, 1,000개는 60초 IPC 제한 초과
- UI 에 operation 상태 표시가 없음. UI-T24 는 "202 를 완료로 보이지 않고 결과를 이벤트로 반영" 까지만 검증
- 실제 runtime 시나리오는 사용자 공용 tmux 소켓(`samsara-<uid>`)에 `samsara-<attempt_id>` 세션을 만들고 끝나면 그것만 지웁니다. 전용 소켓 설정 knob 은 runtime 에 없음
- 미니맵이 대규모 그래프에서 빈 상자로 보임(before 부터 동일)
