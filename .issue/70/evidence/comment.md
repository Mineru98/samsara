## 작업 요약

런타임이 `RETAINED` 이후의 worktree 정리, `CONFLICT` 후보의 해결 경로, 큐 우선순위 변경을 다루게 했습니다.
정리는 안전 조건을 모두 만족할 때만 하고 `--force`·`clean`·`reset` 은 쓰지 않습니다(INV-12).
충돌 후보는 Git 이 확인한 충돌 종류를 보여 주고, 해결용 새 attempt 를 거친 새 제출이 최신 base 에서 다시 검증됩니다.

브랜치 `feat/70-worktree-cleanup-conflict-flow`, 구현 커밋 `b8e7214`. 기준 브랜치는 `dev/0.4.0-alpha` 입니다.

## 변경 전후

| 항목 | 전 | 후 |
| --- | --- | --- |
| worktree 정리 | 없음. 끝난 worktree 가 디스크에 계속 남음 | `samsara worktree cleanup [--dry-run] [--delete-branch] [<id>...]` |
| 정리 자격 | - | managed, 세션 종료 확인(ALIVE·UNKNOWN 이면 보존), 활성 attempt·검증·merge 후보 없음, 추적·미추적·ignored 파일 없음, HEAD 와 output commit 이 다른 ref 로 참조됨, 보존 기간(`git.worktree_retention_hours`, 기본 168) 경과 또는 id 를 직접 지정 |
| worktree / branch 제거 | - | 서로 다른 단계. `git worktree remove` 가 거절하면 `RETAINED(REMOVE_REFUSED)` 로 근거를 남기고 멈춤. branch 는 tip 이 다른 ref 에 포함될 때만 CAS 로 삭제(squash 통합이면 보존) |
| 충돌 분류 | `MERGE_CONFLICT` 한 줄 | 파일별 `TEXT / BINARY / RENAME / DIRECTORY / OTHER`. 출처는 `git_merge_tree` 로 표시해 예상 충돌과 구분 |
| 충돌 해결 | 취소만 가능 | `samsara merge resolve <id>`: 보존된 worktree 를 재사용하는 새 attempt(살아 있는 세션이 있으면 거부). 새 제출이 이전 후보를 `CANCELLED(SUPERSEDED)` 로 대체하고 큐 자리는 이어받음. 승인은 이어받지 않음 |
| 재시도 정책 | - | 같은 source/base 의 CONFLICT 는 다시 계산하지 않음. target 이 움직이면 새 revision 으로 재검증. 해결 attempt 가 진행 중이면 재enqueue 거부 |
| 우선순위 | 생성 순서로 고정 | `samsara merge prioritize <id> [--before <id>] --reason <text>`: 진행 중인 후보는 움직이지 않음, 의존 역전은 거부, 사유와 변경자를 audit 에 기록 |
| 기아 표시 | - | 후보 view 에 `waiting_since`, `revalidation_count`, `starvation_risk`(재검증 3회 이상) |

화면 변경이 없어 스크린샷 대신 테스트와 CLI 출력을 증거로 남겼습니다.

## 완료 기준 확인

- [x] dirty·untracked·생존 불명 worktree 는 정리되지 않는다(INV-12): `TestCleanupPreservesDirtyUntrackedRenameBinary`, `TestCleanupRequiresConfirmedExitAndRetention`
- [x] 충돌 해결 후 새 revision 이 최신 base 에서 재검증된다: `TestConflictResolutionRevalidatesOnLatestBase`
- [x] F-GIT fixture(clean, dirty, untracked, rename, binary, squash) 테스트 통과: 위 테스트, `TestCleanupCleanWorktreeKeepsUnmergedBranch`, `TestCleanupBranchAfterSquashAndMerge`, `TestMergeTreeConflictClassification`

## 검증

검증은 모두 실제 git 저장소와 실제 SQLite 를 썼습니다. worker 백엔드는 Fake 이고, 실제 herdr·tmux·Claude 세션 검증은 하지 않았습니다.

- `go vet ./...` 통과, `go test -count=1 ./...` 11개 패키지 통과
- 신규 테스트 8개(하위 테스트 5개 포함) 통과
- 원본: [before go test](https://github.com/Mineru98/samsara/blob/dev/0.4.0-alpha/.issue/70/evidence/before/go-test.txt) · [before CLI](https://github.com/Mineru98/samsara/blob/dev/0.4.0-alpha/.issue/70/evidence/before/cli.txt) · [after go test](https://github.com/Mineru98/samsara/blob/dev/0.4.0-alpha/.issue/70/evidence/after/go-test.txt) · [after 신규 테스트](https://github.com/Mineru98/samsara/blob/dev/0.4.0-alpha/.issue/70/evidence/after/new-tests.txt) · [after CLI](https://github.com/Mineru98/samsara/blob/dev/0.4.0-alpha/.issue/70/evidence/after/cli.txt)

## 변경 파일

- `runtime/internal/app/cleanup.go` — 정리 자격 판정과 제거(신규)
- `runtime/internal/app/merge.go` — 충돌 분류 저장, `ResolveConflict`, `PrioritizeCandidate`, 재enqueue 정책, 이전 후보 대체
- `runtime/internal/gitops/git.go` — `merge-tree -z --name-only` 충돌 파싱, `StatusWithIgnored`, `RefsContaining`, `DeleteRefCAS`
- `runtime/internal/store/sqlite/migrations/0002_cleanup_conflict.sql` — `conflict_files`, `resolution_attempt_id`, `superseded_by` 컬럼 추가(스키마 2)
- `runtime/internal/hooks/install.go` — `OnlyOurs`: 삼사라 hook 만 담긴 설정 파일인지 판정(정리 전 삼사라 자신의 파일만 지움)
- `runtime/internal/app/outbox.go` — 해결용 attempt 의 worker 프롬프트에 대상 ref 와 충돌 파일 안내
- `runtime/internal/config/config.go`, `runtime/internal/store/sqlite/{model,projection}.go`, `runtime/internal/transport/ipc/ipc.go`, `runtime/cmd/samsara/main.go`, `runtime/README.md`

## 남은 이슈

- 정리는 CLI 요청으로만 실행합니다. 데몬이 주기적으로 정리하지는 않습니다.
- HTTP API(UI) 에는 아직 resolve·prioritize·cleanup 경로를 노출하지 않았습니다.
- ignored 파일이 하나라도 있으면 정리하지 않습니다. 빌드 캐시가 있는 worktree 는 사용자가 직접 비워야 정리됩니다.
