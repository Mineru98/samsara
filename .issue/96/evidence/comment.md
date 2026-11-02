## 작업 요약

run 을 revise 하면 merge 후보와 해결 attempt 가 `PLAN_REVISED` 로 취소됩니다. 이때 둘 다 `superseded_by` 에 새 세대의 attempt 를 기록하도록 고쳤습니다.
`execution_attempts.superseded_by` 컬럼(migration 0004)을 추가하고 `AttemptView.superseded_by` 로 노출했습니다. 그래서 이슈 상세와 snapshot 에서 CONFLICT → 해결 요청 → PLAN_REVISED → 새 attempt 흐름을 한 줄기로 따라갈 수 있습니다.
해결 attempt worktree 정리 자격은 현재 동작이 의도대로라고 판단했습니다. 산출물이 없으면 바로 eligible 이고, 남은 작업은 기존 git 보존 조건이 지킵니다. 이 판정을 테스트로 고정했고, 런타임 동작은 바꾸지 않았습니다.

## 변경 전후

화면 변경이 없는 백엔드 수정이라 캡처 이미지는 생략했습니다. 이슈의 재현 probe(`TestResolveReviseProbe`)를 같은 조건으로 돌린 출력을 비교합니다.

| 항목 | 전 | 후 |
| --- | --- | --- |
| 취소된 후보 `superseded_by` | `""` | 새 attempt ID |
| 취소된 해결 attempt `superseded_by` | 컬럼 없음 | 새 attempt ID |
| 해결 attempt 상태 | `CANCELLED / PLAN_REVISED` | `CANCELLED / PLAN_REVISED` (동일) |
| 산출물 없는 worktree 정리 판정 | `eligible=true` | `eligible=true` (의도대로, 테스트로 고정) |

```text
# before (.issue/96/evidence/before/probe.txt)
after revise:  candidate=CANCELLED reason=PLAN_REVISED superseded_by="" resolution_attempt=att_972c… new_attempt=att_114a…
resolution attempt: state=CANCELLED reason=PLAN_REVISED
cleanup dry run: eligible=true reasons=[]

# after (.issue/96/evidence/after/probe.txt)
after revise:  candidate=CANCELLED reason=PLAN_REVISED superseded_by="att_b878…" resolution_attempt=att_f736… new_attempt=att_b878…
resolution attempt: state=CANCELLED reason=PLAN_REVISED superseded_by="att_b878…"
cleanup dry run: eligible=true reasons=[]
```

## 변경 파일

- `runtime/internal/app/plans.go`: `reviseAffectedIssue` 가 새 attempt ID 를 취소된 attempt·후보의 `superseded_by` 에 기록합니다. 새 attempt 와 취소된 후보도 변경 이벤트에 포함합니다(Touch).
- `runtime/internal/store/sqlite/migrations/0004_attempt_superseded_by.sql`, `db.go`: 컬럼 추가, `SchemaVersion` 4.
- `runtime/internal/store/sqlite/model.go`, `projection.go`: `Attempt.SupersededBy`, `AttemptView.superseded_by`.
- `runtime/api/schemas/entities.schema.json`, `runtime/api/ts/samsara-api.gen.ts`: 응답 계약에 선택 필드를 추가했습니다(TS 재생성).
- `runtime/internal/app/revise_test.go`: 테스트 2개를 추가했습니다.
  - `TestReviseLinksResolutionToNextAttempt`: 연결, 이슈 상세 projection, 산출물 없는 worktree 의 eligible 판정과 미머지 브랜치 보존(`UNMERGED_COMMITS`)을 확인합니다.
  - `TestReviseKeepsResolutionWorkInWorktree`: 해결 작업의 미커밋 변경이 있으면 `DIRTY_TRACKED` 로 보존되는지 확인합니다.

## 검증

- `go build ./...`, `go vet ./...`, `go test ./... -count=1` 모두 통과했습니다(`.issue/96/evidence/after/go-test.txt`).
- 수정 코드(`plans.go`)만 되돌리면 새 테스트가 `superseded_by=""` 로 실패합니다. 테스트가 이 회귀를 잡는다는 것을 확인했습니다.

## 남은 이슈

- 마이그레이션 번호가 겹칩니다. [PR #101](https://github.com/Mineru98/samsara/pull/101) 도 `0004_github_queue.sql` 을 씁니다. 둘 다 머지되면 런타임이 시작할 때 실패하므로, **나중에 머지되는 쪽이 0005 로 옮기고 `SchemaVersion` 을 5 로 올려야 합니다.**
- 후보의 `superseded_by` 는 기존 경로(해결 제출 → 새 후보 `mc_…`)와 이번 경로(PLAN_REVISED → 새 attempt `att_…`)로 두 종류의 ID 를 담습니다. `state_reason`(`SUPERSEDED` / `PLAN_REVISED`)으로 구분할 수 있습니다.
- 이미 끝나 있던(취소가 아닌) attempt 는 revise 시 연결하지 않습니다. 이슈 요구가 "취소되는 attempt" 라서 범위를 거기까지로 잡았습니다.
- outputs 의 `PLAN_REVISED:<세대>` 표식(#67 한계)은 이번 범위 밖입니다.
