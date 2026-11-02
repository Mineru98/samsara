## 작업 요약

`integration_authority: github_queue` 를 04 §10 설계대로 연결했습니다. 합의한 범위는 **안전 게이트 + 상태기계**입니다.
대상 브랜치 규칙을 조회해 조건이 모두 확인될 때만 GitHub Merge Queue 에 후보를 넣고, polling 으로 group·승인·policy check·MERGED 증거를 추적합니다.
조건이 하나라도 빠지면 후보는 구체 사유와 함께 `WAITING` 에 머물고 enqueue 는 `UNSUPPORTED_CAPABILITY` 입니다.

## 변경 전후

| 전 | 후 |
| --- | --- |
| ![github_queue 동작 - 전](https://raw.githubusercontent.com/Mineru98/samsara/dev/0.4.0-alpha/.issue/65/evidence/before/github-queue.webp) | ![github_queue 동작 - 후](https://raw.githubusercontent.com/Mineru98/samsara/dev/0.4.0-alpha/.issue/65/evidence/after/github-queue.webp) |

빨간 박스가 변경 구간입니다. 전: 후보는 `WAITING(GITHUB_QUEUE_NOT_VERIFIED)` 고정이었고 enqueue 는 무조건 거절됐습니다. 후: 미지원 사유 7종의 유지, enqueue → group 검증 → MERGED, 그리고 실제 저장소 probe 결과(`MERGE_QUEUE_DISABLED`)입니다.

## 동작

- **호환성 탐색(04 §10.2)**: `GET repos/{r}/rules/branches/{b}` 로 merge queue, ALLGREEN, build·merge 크기 1, 필수 `samsara/integration-policy` check 를 확인합니다. 이 check 는 `github.merge_queue.policy_app_id` App 으로 고정돼 있어야 하고, check 게시자도 있어야 합니다.
  - 미지원 사유: `MERGE_QUEUE_DISABLED`, `MERGE_QUEUE_RULES_AMBIGUOUS`, `MERGE_QUEUE_HEADGREEN_UNSUPPORTED`, `MERGE_QUEUE_GROUP_SIZE_UNSUPPORTED`, `POLICY_CHECK_NOT_REQUIRED`, `POLICY_CHECK_SOURCE_UNPINNED`, `POLICY_PUBLISHER_UNAVAILABLE`
- **enqueue(03 §3.4)**: 막 받은 probe 결과, 유효한 RESULT 승인, 의존 해소, head 가 승인된 source 인 열린 PR 을 모두 확인합니다. 그다음 `WAITING → PUBLISHING` 으로 바꾸고, GraphQL `enqueuePullRequest`(expectedHeadOid=source) 로 넣습니다. direct merge 와 bypass 는 쓰지 않습니다.
- **polling(04 §10.8)**: `native_queue_state` 를 REQUESTED·QUEUED·VALIDATING·MERGED·REMOVED·UNKNOWN 으로 추적합니다. 재시작으로 남은 REQUESTED 는 PR 을 읽어 정리합니다.
- **INV-09·§10.9**: GitHub 가 만든 group(entry·head·base)마다 revision 을 올리고, 이전 MERGE 승인을 무효화하고, policy check 를 in_progress 로 재설정합니다. head SHA 가 같아도 entry 가 다르면 새 group 으로 봅니다.
- **§10.10**: 같은 transaction 에서 현재 group 과 MERGE 승인을 다시 확인한 뒤에만 success intent 를 기록합니다. 전송 전에 철회되면 그 intent 는 취소됩니다.
- **INV-10**: github_queue target 은 로컬 publish(`git.publish`)나 로컬 ref 비교 경로를 타지 않습니다.
- **MERGED**: PR merge 를 provider 증거(PR·merge commit·group·check run)로 기록합니다. success 를 게시하지 않았는데 merge 된 경우는 사실로 보존합니다. 대신 `MERGED_WITHOUT_POLICY_SUCCESS`·`policy_authorized:false` 로 남겨 정상 통합과 구분합니다.

## 변경 파일

- `runtime/internal/github/mergequeue.go`: probe, commit→PR 조회, GraphQL enqueue·관찰 (신규)
- `runtime/internal/app/github_queue.go`: enqueue·polling·group·승인·policy check 상태기계, `PolicyCheckPublisher` (신규)
- `runtime/internal/store/sqlite/migrations/0004_github_queue.sql`: target probe 컬럼, 후보 PR·group 컬럼, `provider_validation_groups`
- `runtime/internal/app/merge.go`, `outbox.go`, `app.go`, `github.go`: 분기 연결, native 후보를 로컬 reconcile 에서 제외
- `runtime/internal/config/config.go`, `runtime/api/schemas/config.schema.json`, `runtime/api/ts/samsara-api.gen.ts`: `github.merge_queue.policy_app_id`
- `runtime/README.md`, `docs/architecture/06-runtime-remaining-work.md`: github_queue 설명 줄

## 검증

- `go vet ./...` / `go test ./... -count=1` 전부 통과 (원본 `.issue/65/evidence/after/go-test.txt`)
- 신규 시나리오 4개(하위 7개)는 fake GitHub 클라이언트와 fake check 게시자로 돌렸고 모두 통과했습니다 (원본 `.issue/65/evidence/after/scenarios.txt`).
  - 미지원 사유별 `WAITING` 유지와 enqueue 거절
  - enqueue → group 1 → base 변경 group 2(이전 승인 무효) → 같은 SHA 새 entry(check 재설정, 새 승인) → MERGED, 로컬 publish 0건
  - queue 에서 제거되거나 enqueue 가 거절되면 `WAITING` 으로 복귀
  - 유실된 enqueue intent 복구와 미승인 merge 표시
- 변이 검사: group 비교에서 entry id 를 빼면 같은 SHA 시나리오가 실패하는 것을 확인했습니다.
- 실제 `gh` 로그인으로 이 저장소를 probe 했습니다. 개인 저장소라 `MERGE_QUEUE_DISABLED` 로 미지원이 유지됩니다. GraphQL PR 관찰 응답도 실제 PR #85 로 파싱을 확인했습니다 (원본 `.issue/65/evidence/after/live-probe.txt`).
- 별도 리뷰 레인의 지적을 반영했습니다: 재시작 고착, success intent dedupe 재사용, 미승인 merge 기록, 오래된 probe 사용, 승인 반복 생성, 다중 ruleset.

## 완료 기준 대비

| 기준 | 상태 |
| --- | --- |
| 시험 저장소에서 enqueue → group 검증 → MERGED | **미검증 (org 시험 저장소·GitHub App 필요)**. fake provider 로만 확인했습니다 |
| base·멤버 변경 시 이전 MERGE 승인 미사용 (INV-09) | 테스트로 확인 |
| 같은 repo/target 에서 로컬 publication 과 동시 수행 금지 (INV-10) | 테스트로 확인 |
| 필수 check 를 확인할 수 없는 환경에서 미지원 유지 | 테스트와 실제 probe 로 확인 |

## 남은 이슈

- Checks API 를 쓰는 `PolicyCheckPublisher` 의 GitHub App 구현과 자격증명 보관이 없습니다. 그래서 이 빌드는 실제 환경에서 항상 `POLICY_PUBLISHER_UNAVAILABLE` 입니다.
- Actions `merge_group` manifest artifact 검증(04 §10.8 1~3)과 실제 시험 저장소 e2e 가 남아 있습니다.
- MVP 는 target 당 queue 진입 후보를 1개로 제한합니다. FAILED(`PR_CLOSED`·`PR_HEAD_CHANGED`) 후보를 다시 enqueue 하는 경로와 `provider_validation_id` 의 API 노출도 없습니다.
- 승인 subject 가 영구적으로 무효가 되면 후보는 FAILED 로 멈춥니다. 다만 GitHub queue 에서 PR 을 빼는 호출은 하지 않습니다(check 가 green 이 되지 않아 merge 는 막힙니다).
