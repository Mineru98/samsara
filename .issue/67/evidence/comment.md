## 작업 요약

실행 중인 run의 계획을 `pause → revise → resume` 순서로 바꿀 수 있게 했습니다. 새 명령 `samsara plan revise` / `/ipc/v1/plans/revise` 는 PAUSED이면서 조용한(quiescent) run에만 받습니다. 조용하다는 것은 실행·검증·통합 중인 작업이 없다는 뜻입니다. `expected_run_generation`·`expected_graph_version` 이 현재 값과 다르면 거절합니다.

제목만 바뀐 수정은 `METADATA_ONLY` 로 처리해 세대를 그대로 둡니다. 실행 의미가 바뀌면 `run_generation`·`graph_version` 을 올리고, 영향 범위(직접 바뀐 이슈와 그 하위 이슈)만 다시 평가합니다. 영향 없는 attempt는 같은 `attempt_id` 를 유지한 채 새 세대로 넘어가고, resume하면 lease를 다시 받습니다. 영향받은 이슈의 산출물은 지우지 않고 superseded로 표시만 하며, 통합 증거도 그대로 둡니다.

이전 세대 승인은 전부 무효화합니다(POL-05·POL-07). 그래서 revise된 run은 현재 세대의 PLAN 승인이 있어야 재개됩니다.

## 변경 전후

| 전 | 후 |
| --- | --- |
| ![실행 중 run 계획 변경 경로 - 전](https://raw.githubusercontent.com/Mineru98/samsara/dev/0.4.0-alpha/.issue/67/evidence/before/behavior.webp) | ![실행 중 run 계획 변경 경로 - 후](https://raw.githubusercontent.com/Mineru98/samsara/dev/0.4.0-alpha/.issue/67/evidence/after/behavior.webp) |

빨간 박스가 변경 구간입니다.

- **전**: PAUSED run에 계획을 다시 적용하면 `CONFLICT`(409)로 거절됐고, IPC와 CLI에는 `validate`·`apply` 경로만 있었습니다.
- **후**: `plans/revise` 경로와 `plan revise` CLI가 생겼고, revise 테스트 4개가 통과합니다.

| 완료 기준 | 결과 | 근거 테스트 |
| --- | --- | --- |
| revise 후 영향받지 않은 attempt의 lease 유지 | 통과: 같은 attempt가 세대 2로 넘어가고, resume 후 ACTIVE lease를 받아 tool 사용이 허용됩니다 | `TestRevisePausedRun` |
| 이전 generation 승인 재사용 금지 (POL-05·07) | 통과: 이전 세대 RESULT·PLAN 승인은 INVALIDATED로 바뀌어 B의 게이트를 열지 못하고, 새 세대 승인을 다시 요청합니다 | `TestRevisePausedRun`, `TestReviseCarriesUnaffectedResults` |
| HISTORICAL snapshot 불변 | 통과: 끝난 run의 snapshot JSON이 revise 전후로 같습니다 | `TestRevisePausedRun` |
| CLI와 IPC가 같은 오류·digest 반환 | 통과: CLI와 같은 `plan.Revision` 본문을 IPC로 보내면 직접 호출과 같은 오류 코드·HTTP 상태를 받고, `plan_digest` 가 `validate` 결과와 같습니다 | `TestReviseCLIAndIPCAgree` |

원본: [before/behavior.txt](https://github.com/Mineru98/samsara/blob/dev/0.4.0-alpha/.issue/67/evidence/before/behavior.txt) · [before/go-test.txt](https://github.com/Mineru98/samsara/blob/dev/0.4.0-alpha/.issue/67/evidence/before/go-test.txt) · [after/behavior.txt](https://github.com/Mineru98/samsara/blob/dev/0.4.0-alpha/.issue/67/evidence/after/behavior.txt) · [after/go-test.txt](https://github.com/Mineru98/samsara/blob/dev/0.4.0-alpha/.issue/67/evidence/after/go-test.txt) · [after/revise-tests.txt](https://github.com/Mineru98/samsara/blob/dev/0.4.0-alpha/.issue/67/evidence/after/revise-tests.txt)

## 결정 사항

착수 때 확인받은 내용입니다.

- **승인**: revise로 세대가 오르면, 영향 여부와 관계없이 그 run의 PENDING·APPROVED 승인을 전부 무효화합니다.
  - 영향 없는 이슈의 검증된 산출물에는 새 세대 RESULT 승인을 다시 요청합니다.
  - 사용자가 REJECT·REVOKE한 결과는 다시 요청하지 않습니다.
- **lease**: pause가 이미 lease를 모두 회수하므로, "lease 유지"는 attempt를 그대로 두고 resume 때 lease를 다시 발급하는 것으로 구현했습니다. pause 동작 자체는 바꾸지 않았습니다.
- **이슈 삭제**: revise로 이슈를 빼는 요청은 `INVALID_REQUEST` 로 거절합니다. 삭제 지원은 후속 작업입니다.

## 변경 파일

- `runtime/internal/plan/plan.go`
  - `Revision` 파싱
  - `Compare` / `Affected`: 변경 종류와 영향 범위 계산
  - `SemanticDigest`: 제목과 배열 순서를 제외한 실행 의미 digest
- `runtime/internal/app/plans.go`: `RevisePlan` 추가. quiescence 확인, 세대 증가, 영향 이슈 재평가, 승인 무효화와 재요청을 처리합니다.
- `runtime/internal/app/runs.go`
  - PLAN 승인 subject를 실행 의미 digest에 묶었습니다.
  - revise된 run의 `ResumeRun` 은 현재 세대 PLAN 승인을 확인합니다.
- `runtime/internal/app/merge.go`: MERGE 승인에 `run_generation` 을 묶었습니다. 이전에는 0으로 저장돼 승인이 늘 stale로 판정되던 결함이 있었습니다.
- `runtime/internal/store/sqlite/model.go`: `CurrentOutput` 이 superseded가 아닌 이전 세대 산출물을 이어받도록 바꿨습니다.
- `runtime/internal/transport/ipc/ipc.go`, `runtime/cmd/samsara/main.go`, `runtime/README.md`: IPC 경로, CLI 명령, 명령 목록을 추가했습니다.
- `runtime/internal/app/revise_test.go`: 테스트 4개를 추가했습니다.

## 검증

- `go build ./... && go vet ./... && go test -count=1 ./...` 통과(테스트 있는 11개 패키지 모두 ok), `gofmt -l` 결과 없음
- revise 테스트 4개 통과. 거절 경로도 모두 확인했습니다.
  - ACTIVE run, 멈춤 확인 전 attempt, 검증 중인 산출물, 오래된 세대·그래프 버전, 이슈 삭제는 거절됩니다.
  - 순서만 바꾸거나 제목만 바꾼 revise는 PLAN 승인이 유지됩니다.
- 별도 리뷰 레인의 지적 중 상·중 등급을 반영했습니다.
  - 검증·통합이 진행 중일 때 revise하던 문제
  - 배열 순서에 민감하던 digest
  - 업그레이드 전에 만든 run의 resume 회귀
  - 거부된 결과를 다시 요청하던 문제
  - 격리된 attempt를 우회하던 문제

## 남은 이슈

- 업그레이드 전에 PLANNED 상태였던 run은 PLAN 승인 subject 형식이 바뀌어 승인을 다시 요청받습니다.
- `superseded_by` 에는 산출물 ID 대신 `PLAN_REVISED:<세대>` 표식을 기록합니다.
- 빠진 edge 행은 삭제하며, 이전 그래프는 감사 기록의 `previous_plan_snapshot` 으로 남습니다.
- target 기본값을 명시로 바꾸기만 해도 실행 의미 변경으로 판정됩니다(과잉 재실행).
- revise로 이슈를 삭제하는 기능은 아직 없습니다.
