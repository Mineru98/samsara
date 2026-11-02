## 작업 요약

`github_queue` 의 실패 후보 처리 경로를 채웠습니다. PR 을 잃어 FAILED 된 후보(`PR_CLOSED`·`PR_HEAD_CHANGED`)는 열린 PR 로 다시 enqueue 할 수 있고, 승인 subject 가 영구 무효가 되면 PR 을 GitHub merge queue 에서 뺍니다. `provider_validation_id` 는 후보 API(snapshot·후보 조회)와 schema·TS 에 노출됩니다.

브랜치 `feat/108-github-queue-retry-dequeue` · 커밋 `bcc6c1a` · 기준 `dev/0.4.0-alpha`

## 변경 전후 (fake provider 테스트)

| 항목 | 전 (`ce655a7`) | 후 (`bcc6c1a`) |
| --- | --- | --- |
| FAILED(`PR_CLOSED`·`PR_HEAD_CHANGED`) 재enqueue | 거부 — `only WAITING candidates are delivered` | **통과** — 새 provider group 으로 재검증 |
| 영구 무효(`RUN_CANCELLED`) 시 dequeue 호출 | 0회 — PR 이 큐에 남음 | **1회** — `dequeuePullRequest`, `native_queue_state=REMOVED` |
| dequeue 거절·재시도 소진 | 경로 없음 | REMOVED 로 오기록하지 않고 재시도, 소진 시 `UNKNOWN` |
| `provider_validation_id` API·schema | 없음 | `CandidateView`·`entities.schema.json`·`samsara-api.gen.ts` |

| 테스트 | 전 | 후 |
| --- | ---: | ---: |
| `TestGitHubQueueFailedCandidateReenqueue` | FAIL | **PASS** |
| `TestGitHubQueueDequeueOnPermanentlyInvalidSubject` | FAIL | **PASS** |
| `TestGitHubQueueDequeueRefused` (2 subtests) | 미측정 (이번에 추가된 경로) | **PASS** |
| `go test ./...` (14 packages) | 미측정 (기준선은 큐 테스트만) | **ok** |

"전" 은 base 구현(`github_queue.go`·`outbox.go`·`mergequeue.go`)에 새 테스트만 얹어 돌린 결과입니다. 원본은 `.issue/108/evidence/before/`, `.issue/108/evidence/after/` 에 있습니다. 백엔드 변경이라 스크린샷 대신 테스트 출력을 증거로 남겼습니다.

## 변경 파일

- `runtime/internal/github/mergequeue.go` — `DequeuePullRequest` (GraphQL `dequeuePullRequest(input:{id})`)
- `runtime/internal/app/github_queue.go` — 재enqueue 허용 사유(`PR_CLOSED`·`PR_HEAD_CHANGED`), 영구 무효 분기에서 `github.queue.dequeue` outbox 등록, `handleNativeDequeue` (provider 를 먼저 읽고 entry 가 사라진 것을 본 뒤에만 REMOVED, 같은 PR 을 새 후보가 쓰면 호출 생략)
- `runtime/internal/app/outbox.go` — kind 디스패치, 재시작 후 재전송 안전 목록에 추가
- `runtime/internal/store/sqlite/projection.go` — `CandidateView.provider_validation_id`
- `runtime/api/schemas/entities.schema.json`, `runtime/api/ts/samsara-api.gen.ts` — 계약 반영(TS 는 `go test ./api -run TestTypeScriptUpToDate -update` 로 재생성)
- `runtime/internal/app/github_queue_test.go` — fake provider 에 dequeue 응답 추가, 테스트 3개

## 검증

- `go vet ./...` 통과
- `go test ./... -count=1` 전체 통과
- 새 테스트 2개는 base 구현에서 실패하는 것을 확인(이슈의 결함 지점에서 실패)
- 독립 코드 리뷰에서 나온 dequeue 실패 처리 지적 4건 반영

## 남은 이슈

- `web/` 의 `tsc` 는 돌리지 않았습니다(워크트리에 `node_modules` 없음). 추가한 필드는 선택(optional) 필드입니다.
- `approval.integration: auto` 이면 승인 무효 검사에 도달하지 않아, 취소된 run 의 PR 도 success check 를 받을 수 있습니다. 이번 변경 전부터 있던 동작이라 손대지 않았습니다.
- 실제 org 저장소·GitHub App 으로 한 end-to-end 검증은 여전히 없습니다.
