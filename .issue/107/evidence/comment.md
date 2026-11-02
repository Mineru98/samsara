## 작업 요약

github_queue 모드의 `samsara/integration-policy` check 를 실제로 게시하는 GitHub App 게시자와 `merge_group` manifest 검증(04 §10.8 1~3)을 추가했습니다.
자격증명(`<state>/github-app.json`)이 없으면 지금처럼 `POLICY_PUBLISHER_UNAVAILABLE` 로 미지원이 유지됩니다.
**시험 저장소 e2e 는 실행하지 않았습니다.** github_queue 는 핵심 경로(`samsara_local`, 04 §9)가 아닌 선택 모드(04 §5.2·단계 3)라서, App·org 저장소 준비 없이 마무리하기로 했습니다. 시험 코드(`-tags e2e`)는 커밋에 들어 있습니다.

## 변경 전후

| 항목 | 전 (`ce655a7`) | 후 (`f07f97d`) |
| --- | --- | --- |
| check 게시자 | 인터페이스만 있고 구현·배선 없음 ([근거](https://github.com/Mineru98/samsara/blob/dev/0.4.0-alpha/.issue/107/evidence/before/publisher-absent.txt)) | `AppPublisher` (JWT → `checks:write` 설치 토큰, 같은 external_id 는 PATCH) |
| merge_group 검증 | 없음 | Workflow run·정의 고정·artifact digest·manifest 대조, 실패 시 check failure |
| `doctor` | `github_app` 항목 없음 ([전](https://github.com/Mineru98/samsara/blob/dev/0.4.0-alpha/.issue/107/evidence/before/doctor.json)) | `github_app.configured=false` 와 미지원 사유 보고 ([후](https://github.com/Mineru98/samsara/blob/dev/0.4.0-alpha/.issue/107/evidence/after/doctor.json)) |
| GitHubQueue 테스트 | 4개 통과 ([전](https://github.com/Mineru98/samsara/blob/dev/0.4.0-alpha/.issue/107/evidence/before/queue-tests.txt)) | 6개 통과, 신규 2개 ([후](https://github.com/Mineru98/samsara/blob/dev/0.4.0-alpha/.issue/107/evidence/after/queue-tests.txt)) |
| github 패키지 신규 테스트 | - | 19건 통과 ([후](https://github.com/Mineru98/samsara/blob/dev/0.4.0-alpha/.issue/107/evidence/after/app-manifest-tests.txt)) |
| e2e | - | 미측정 (사용자 결정, [사유](https://github.com/Mineru98/samsara/blob/dev/0.4.0-alpha/.issue/107/evidence/after/e2e-status.txt)) |

## 변경 파일

- `runtime/internal/github/app.go` — App 자격증명 로드(0600·symlink 거부·디렉터리 권한 확인), 설치 토큰 메모리 캐시, check run 생성·갱신, 오류 문자열의 토큰 가림
- `runtime/internal/github/manifest.go` — `VerifyMergeGroupManifest`: PENDING / VERIFIED / FAILED 판정과 사유 코드
- `runtime/internal/app/github_queue.go` — manifest 판정을 group 상태에 기록, 실패 시 `FAILURE_REQUESTED` → failure check 게시와 MERGE 승인 무효화, success 는 `VERIFIED` 일 때만
- `runtime/internal/store/sqlite/migrations/0005_github_queue_manifest.sql` — group 의 check_state 확장, manifest 컬럼 추가 (`SchemaVersion=5`)
- `runtime/internal/config/config.go`, `api/schemas/config.schema.json`, `api/ts/samsara-api.gen.ts` — `github.merge_queue.manifest_workflow` / `manifest_artifact`
- `runtime/internal/daemon/daemon.go`, `runtime/cmd/samsara/main.go` — daemon 의 게시자 로드, doctor `github_app` 보고
- `runtime/internal/app/github_queue_e2e_test.go` — 실제 org 저장소 e2e (merged / manifest_rejected)
- `runtime/README.md`, `docs/architecture/06-runtime-remaining-work.md` — 자격증명·collector Workflow 안내, 현황 갱신

## 검증

- `go vet ./...`, `go vet -tags e2e ./internal/app`, `go test ./... -count=1` 통과 ([출력](https://github.com/Mineru98/samsara/blob/dev/0.4.0-alpha/.issue/107/evidence/after/full-suite.txt))
- 모두 fake provider(httptest) 기반입니다. 실제 GitHub API 로는 실행하지 않았습니다

## 남은 이슈

- 시험 저장소 e2e 미실행. 완료 기준 3번째 항목은 충족하지 못했습니다
- enqueue 전에 GitHub 가 PR HEAD 에 required check 통과를 요구하는지는 e2e 에서만 확인할 수 있습니다 (04 §10.7 사전검사 미구현)
- `gh api` 의 artifact zip 302 redirect 다운로드는 실제 환경에서 확인하지 않았습니다
- workflow run·artifact 목록은 첫 100개만 읽습니다
