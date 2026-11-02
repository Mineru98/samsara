## 작업 요약

대시보드에서 `run start`와 계획 수정(일시 정지 → 수정 → 재개)을 할 수 있게 했습니다. run 버튼은 이제 서버가 내려주는 `run.available_actions`로만 그립니다(PLANNED → 실행 시작, ACTIVE → 일시 정지, PAUSED → 재개·계획 수정). 명령은 모두 `Idempotency-Key`·`If-Match`로 보냅니다.
런타임 HTTP API에 `POST /v1/runs/{id}/start`, `POST /v1/runs/{id}/revise`, `GET /v1/runs/{id}/plan`을 추가했습니다. 거절된 명령은 CLI와 같은 `code — message`와 `details`(approval_id, current_run_generation 등)를 그대로 보여 줍니다.

## 변경 전후

| 전 | 후 |
| --- | --- |
| ![PLANNED run - 전: 시작 버튼 없음](https://raw.githubusercontent.com/Mineru98/samsara/dev/0.4.0-alpha/.issue/110/evidence/before/run-planned.webp) | ![PLANNED run - 후: 실행 시작 버튼](https://raw.githubusercontent.com/Mineru98/samsara/dev/0.4.0-alpha/.issue/110/evidence/after/run-planned.webp) |
| ![PAUSED run - 전: 재개만 있음](https://raw.githubusercontent.com/Mineru98/samsara/dev/0.4.0-alpha/.issue/110/evidence/before/run-paused.webp) | ![PAUSED run - 후: 재개와 계획 수정](https://raw.githubusercontent.com/Mineru98/samsara/dev/0.4.0-alpha/.issue/110/evidence/after/run-paused.webp) |

빨간 박스가 변경 구간(run 제어 영역)입니다. 1440x900, 같은 프로젝트 화면·같은 run 상태입니다. 전에는 PLANNED run에 버튼이 없었고, PAUSED run에는 재개만 있었습니다.

<details>
<summary>UI로 끝까지 진행한 흐름 (after 7장)</summary>

| 단계 | 캡처 |
| --- | --- |
| 1. 실행 시작 → PLAN 승인이 필요해 거절, CLI와 같은 `APPROVAL_REQUIRED`와 `approval_id` 표시 | ![실행 시작 거절 - APPROVAL_REQUIRED](https://raw.githubusercontent.com/Mineru98/samsara/dev/0.4.0-alpha/.issue/110/evidence/after/start-approval-required.webp) |
| 2. 승인 후 실행 시작 → ACTIVE, 버튼이 일시 정지로 바뀜 | ![실행 시작 성공 - ACTIVE](https://raw.githubusercontent.com/Mineru98/samsara/dev/0.4.0-alpha/.issue/110/evidence/after/run-started.webp) |
| 3. 계획 수정 패널: 현재 계획 JSON(graph 버전 반영)과 기준 세대·버전·digest | ![계획 수정 패널](https://raw.githubusercontent.com/Mineru98/samsara/dev/0.4.0-alpha/.issue/110/evidence/after/revise-panel.webp) |
| 4. 이슈 추가 수정 제출 → `EXECUTION · 세대 2`, plan digest, 새 PLAN 승인 id | ![계획 수정 결과와 digest](https://raw.githubusercontent.com/Mineru98/samsara/dev/0.4.0-alpha/.issue/110/evidence/after/revise-success.webp) |
| 5. 오래된 세대(1)로 다시 제출 → `VERSION_MISMATCH — run generation changed`, `current_run_generation: 2` | ![오래된 세대 거절 메시지](https://raw.githubusercontent.com/Mineru98/samsara/dev/0.4.0-alpha/.issue/110/evidence/after/revise-stale.webp) |
| 6. 재개 → 세대 2의 PLAN 승인이 필요해 거절, `approval_id` 표시 | ![재개 거절 - APPROVAL_REQUIRED](https://raw.githubusercontent.com/Mineru98/samsara/dev/0.4.0-alpha/.issue/110/evidence/after/resume-approval-required.webp) |
| 7. 승인 후 재개 → ACTIVE·세대 2, 수정 패널은 서버가 REVISE_PLAN을 내리지 않으므로 닫힘 | ![재개 성공 - ACTIVE 세대 2](https://raw.githubusercontent.com/Mineru98/samsara/dev/0.4.0-alpha/.issue/110/evidence/after/run-resumed.webp) |

캡처는 격리된 `SAMSARA_HOME`의 실제 런타임입니다(standalone backend, 이슈 2개 `api`→`ui`, 4단계에서 `docs` 추가). 모든 클릭은 브라우저 UI에서 했습니다. 콘솔 오류는 의도한 409/412 거절 응답뿐입니다.
</details>

## 변경 파일

- `runtime/internal/transport/httpapi/server.go` — start·revise·plan 경로. revise 본문은 CLI revision에서 `run_id`만 빠진 형태(경로가 run을 정함)
- `runtime/internal/store/sqlite/projection.go` — `RunView.available_actions`
- `runtime/internal/app/queries.go` — `RunPlan`: 현재 계획 + 읽는 시점의 graph 버전
- `runtime/internal/app/plans.go` — revise에서 If-Match 검사를 세대 검사 뒤로 이동. 대시보드는 둘을 함께 보내므로, 순서가 그대로면 오래된 세대가 `resource changed`로만 거절돼 세대 정보가 보이지 않았습니다. CLI revise는 If-Match를 보내지 않아 영향이 없습니다.
- `runtime/api/` — 스키마(`RunView` 필드는 구버전 호환을 위해 선택), `RunPlan`·`RunReviseRequest`, openapi, TS 재생성
- `web/src/features/RunControls.tsx`, `RevisePlanPanel.tsx`(신규), `CommandButton.tsx`(거절 details 표시), `ProjectPage.tsx`, `styles/app.css`

## 검증

- `go vet ./...` / `go test ./...` 통과. 신규 `TestRunStartAndReviseCommands`: 상태별 available_actions, 승인 전 start 409 → 승인 → 202, plan 조회, revise(EXECUTION·세대 2), 같은 키 재전송 시 저장된 결과 재생, 오래된 세대 412(If-Match 있음·없음), 승인 전 resume 409 → 승인 → ACTIVE
- `pnpm -C web typecheck` / `pnpm -C web test`(33개, 신규 5개) / `pnpm -C web build` 통과. "다시 불러오기" 테스트는 수정을 빼면 실패하는 것을 확인했습니다
- 독립 리뷰 1회. 수정 패널이 PAUSED가 아닐 때도 남던 문제, 계획이 같을 때 "다시 불러오기"가 편집기를 되돌리지 못하던 문제, 수정 후 재개·멱등 재전송 테스트 누락을 반영했습니다
- 원본 출력: `.issue/110/evidence/after/go-test.txt`, `web-check.txt`

## 남은 이슈

- `available_actions`는 run 상태만 봅니다. 프로젝트 비활성, 일시 정지 직후 아직 멈추는 중인 attempt(RUN_NOT_QUIESCENT), 세대 2 이상에서 승인 대기 중인 재개는 버튼이 보이고 누르면 서버가 거절합니다(03 §6.1처럼 snapshot이고 서버가 재검사). `enabled: false`와 `reason_code`로 미리 막을지는 따로 정할 일입니다.
- revise의 If-Match 검사 순서가 03 §6.1(If-Match → 상태·정책 재검사)과 다릅니다. 문서를 고칠지 순서를 되돌릴지 결정이 필요합니다.
- 기존 `StartRun`은 If-Match를 "이미 ACTIVE" 판정보다 먼저 검사합니다. 응답을 잃은 뒤 스트림이 따라오기 전에 재시도하면 실제로는 시작됐는데 `VERSION_MISMATCH`로 보일 수 있습니다(이번 변경 전부터 있던 동작).
- 계획 편집기는 JSON textarea입니다. 필드별 폼은 범위 밖입니다. 수정 결과에는 digest·세대·변경 종류·승인 id·영향 이슈 수를 보여 주고, 무효화된 승인·대체된 산출물 목록은 보여 주지 않습니다.
