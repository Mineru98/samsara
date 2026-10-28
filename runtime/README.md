# Samsara runtime (Go)

`docs/architecture/`의 설계 문서(00~05)를 기준으로 구현한 삼사라 로컬 오케스트레이션 런타임이다.
한 OS 사용자당 daemon 하나가 여러 프로젝트의 Claude Code 작업을 관리한다. 관리 대상은 의존성·승인·실행권(lease)·세션 관찰·Worktree·통합(merge)이다.

- 언어/런타임: Go 1.25+ (`go.mod`), SQLite는 `modernc.org/sqlite` v1.55.0에 포함된 엔진 **3.53.3**을 사용한다. 기준은 3.51.3 이상이고, 시작할 때 `sqlite_version()`으로 확인한다.
- 단일 바이너리 `samsara`가 CLI, daemon, Hook Bridge, worker launcher 역할을 모두 맡는다.
- React 대시보드(02 문서)는 이 모듈에 포함되지 않는다. 대시보드가 쓸 HTTP·SSE API는 이 모듈에 구현돼 있다.

## 빌드와 테스트

```bash
cd runtime
go build -o bin/samsara ./cmd/samsara
go test ./...            # 단위·불변식·실제 git/tmux 통합 테스트
go test -race ./internal/app/
```

테스트에는 실제 `git`이 필요하다. `tmux` 어댑터 테스트는 tmux가 없으면 건너뛴다.

## 빠른 시작

```bash
samsara init                       # .samsara/config.yaml 생성(없을 때), .claude/settings.local.json에 관리 hook 추가, 로컬 trust 등록
samsara doctor --exercise          # claude/tmux/herdr/git/SQLite 버전, hook 설치, tmux 생성·종료 실측
samsara profile add --name unit -- go test ./...   # 검증 명령은 trust 등록한 profile만 실행
samsara plan validate --file plan.json             # 변경 없음
samsara plan apply --file plan.json                # PLANNED run 접수(worker 생성 없음)
samsara run start <run_id>                         # PLAN 승인 확인 후 ACTIVE → scheduler가 실행
samsara approval list / samsara approval decide <id> --approve
samsara merge queue / samsara merge enqueue <candidate_id>
samsara ui --open                                  # 일회용 코드로 loopback 대시보드 API 인증
```

worker 안에서는 구현을 commit한 뒤 `samsara output submit`으로 결과를 제출한다.
Standalone(직접 연 Claude 세션)은 scheduler가 준비한 worktree에서 Claude를 실행한 뒤 `samsara session attach --issue <id>`로 연결한다.

상태 디렉터리는 기본적으로 `~/.local/state/samsara`(DB·registry·로그), `$XDG_RUNTIME_DIR/samsara`(socket·lock), `~/.local/share/samsara/worktrees`이다.
`SAMSARA_HOME`을 지정하면 모든 경로가 그 아래로 바뀐다.

## 설정 (`.samsara/config.yaml`)

01 §4.3의 예시를 따른다. 알 수 없는 필드와 잘못된 값은 오류로 처리하고, 조용히 DISABLED로 바꾸지 않는다. 문서 예시에 더해 다음 필드를 둔다.

| 필드 | 기본값 | 의미 |
|---|---|---|
| `approval.result` | `manual` | VERIFIED 산출물의 RESULT 승인 |
| `execution.worker_command` | `[claude]` | 관리 worker로 실행할 argv(shell 미사용) |
| `execution.lease_ttl_seconds` / `heartbeat_seconds` | 30 / 5 | lease TTL과 launcher heartbeat |
| `git.branch_prefix` | `samsara` | `refs/heads/<prefix>/<project>/<issue>/<attempt>` |

## 문서 ↔ 코드 대응

| 설계 | 구현 위치 |
|---|---|
| 01 §3.2 package 구성 | `internal/{domain,app,project,backend/*,hooks,gitops,store/sqlite,transport/{ipc,httpapi},daemon}` |
| 01 §4 활성화·trust·drain | `app/projects.go`, `project/registry.go`(hook이 offline으로 읽는 registry) |
| 01 §5–9 resolver·herdr·tmux·Standalone | `backend/resolver.go`, `backend/{herdr,tmux,standalone,fake}` |
| 01 §10 Hook Bridge | `hooks/bridge`(stdin→IPC→hook JSON), `hooks/install.go`(`onFailure: block`, exec-form `args`) |
| 01 §11 lease·fencing·heartbeat | `app/leases.go`, `cmd/samsara` `launcher` |
| 01 §12 의존성·승인·scheduler·plan | `plan/`, `app/{plans,approvals,scheduler,runs}.go`, `store/sqlite/eval.go` |
| 01 §13–15 reconcile·outbox·복구 | `app/{reconcile,outbox}.go`, `daemon/` |
| 03 §5 SQLite 모델 | `store/sqlite/migrations/0001_init.sql`(부분 UNIQUE 인덱스, immutable·append-only trigger) |
| 03 §6 멱등·조건부 명령 | `app.idempotent`, `checkIfMatch`, HTTP `Idempotency-Key`/`If-Match`/`ETag` |
| 03 §7–9 HTTP·snapshot·SSE | `transport/httpapi`(fetch-streaming용 Bearer, cursor replay, `reset_required`) |
| 03 §10 Hook IPC | `transport/ipc`(사용자 전용 UDS, peer uid 검사, `/ipc/v1/*`) |
| 03 §11 브라우저 인증 | 일회용 bootstrap code(60초) → 메모리 bearer, Host/Origin allowlist, CSP, `no-store` |
| 04 §6–9 worktree·후보·로컬 큐 | `gitops/git.go`(`--porcelain -z`, `merge-tree --write-tree`, `update-ref` CAS), `app/merge.go` |

## 핵심 불변식과 검증 위치

| 불변식 | 구현 | 테스트 |
|---|---|---|
| R-01/INV-01 비활성 프로젝트 무동작 | bridge가 registry 파일만 읽고 즉시 종료(socket·probe·daemon 시작 0회) | `bridge_test.go TestInertForUnconfiguredAndDisabled` |
| R-02/INV-02 ACTIVE lease 1개 | `one_active_lease` 부분 UNIQUE, `one_live_attempt_per_issue` | `db_test.go TestConstraintsAndImmutability` |
| R-03/INV-04 만료≠종료, UNKNOWN 재할당 금지 | 만료 시 quarantine, 같은 binding·generation 생존 확인 후에만 새 lease·fence | `TestRestartEpochAndQuarantine`, `TestLeaseExpiryClockAndRenewal` |
| R-05/POL-14 관찰로 완료 판정 금지 | Stop·SessionEnd·PostToolUse는 상태 관찰만 함 | `TestStopAndToolObservationsNeverComplete` |
| R-07/INV-07 상태·event·audit·outbox 원자성 | `DB.Write` 하나의 tx에서 project별 event 1건 | `TestAtomicWriteRollsBackEverything`, `TestOneEventPerTransaction...` |
| R-08/INV-08 결과 불명 재전송 금지 | `resendSafe` 목록 밖(prompt·interrupt)은 UNKNOWN으로 보존 | `TestPauseResumePromptAckLoss`, `TestLaunchAckLossDoesNotDuplicateWorker` |
| R-09/INV-05 PASS ≠ allow | PASS 시 stdout 출력 없음, DENY 시 `permissionDecision: deny` | `TestPassDoesNotOverridePermissions` |
| R-10/INV-09 승인 digest 바인딩 | run generation·output·candidate revision·base·policy를 subject digest에 포함 | `TestConcurrentDecisionsIfMatch`, `TestABC...`(REVOKE) |
| INV-03 stale 실행권 거절 | resource fence를 철회할 때마다 증가, 제출·검사 때 현재 fence와 비교 | `TestStaleWorkerCannotSubmit` |
| INV-06 A 대기 시 B만 대기, C 계속 | 실행 edge 평가는 후행 closure에만 적용 | `TestABCPartialBlockingRevokeAndIntegration` |
| INV-10 target당 통합 권위 1개 | `integration_targets(repository, ref)` UNIQUE + 다른 authority 등록 거절, target lock·fence | `app/projects.go ensureTarget` |
| INV-11 SSE 정합성 | `previous_project_version` chain, scope·generation 불일치 시 reset | `TestEventsReplayAndCursorScope`, `server_test.go` |
| INV-12 dirty worktree 보존 | `--force`·`reset --hard`·`clean`·`stash` 사용 안 함 | `git_test.go`(dirty worktree 제거 거절) |
| ACT-05 동시 시작 시 1개 | `flock` 기반 lock(PID 파일 미사용) | `lock_test.go`(20회 경쟁) |

## 실제 환경 smoke 결과 (이 저장소 개발 중 수행)

Claude Code 2.1.311, tmux 3.4, git 2.43, SQLite 3.53.3, Linux 환경에서 실제 daemon을 띄워 확인한 항목이다. Claude 실행 파일 대신 hook을 순서대로 호출하는 스크립트를 worker 명령으로 사용했다.

1. `init` → plan apply → run start 후 tmux 전용 socket에 launcher와 worker가 생성됐다.
2. SessionStart ticket 등록 후 lease를 받았다. worktree 안 Write는 PASS(출력 없음)였고, 밖은 `PATH_OUT_OF_SCOPE`, background Bash는 `BACKGROUND_MUTATION_UNSUPPORTED`로 거절됐다.
3. `output submit` → VERIFIED → RESULT 승인 → `merge enqueue` → `merge-tree` → CAS로 `refs/heads/integration`에 반영됐다. 사용자가 checkout한 `main`은 그대로였고 issue·run이 COMPLETED가 됐다.
4. launcher가 종료를 보고하자 session EXITED가 기록됐다. SSE에서 5개 이벤트의 version chain이 이어지는 것을 확인했다. 과거 run은 HISTORICAL snapshot으로 조회됐다.

## 아직 구현하지 않았거나 의도적으로 막아 둔 범위

전체 목록과 추적 이슈(#64~#72)는 [`docs/architecture/06-runtime-remaining-work.md`](../docs/architecture/06-runtime-remaining-work.md)에 정리했다.

문서의 "확인 전 강제 기능 비활성" 원칙에 따라 다음을 명시적으로 미지원으로 반환한다.

- **herdr**: 설치된 binary의 version과 `herdr api schema --json` hash는 기록한다. 하지만 compatibility manifest에서 protocol 검증(`herdr_protocol_verified`)을 하기 전에는 생성·입력 capability를 `UNSUPPORTED(HERDR_COMPAT_UNVERIFIED)`로 보고한다. 그래서 auto mode는 tmux로 넘어간다. 실제 herdr CLI·socket adapter 구현은 M5 범위다.
- **GitHub native merge queue(`github_queue`)**: provider 정책 검사 연동을 검증하기 전이므로 enqueue 요청에 `UNSUPPORTED_CAPABILITY`를 반환한다. `manual` authority는 사용자가 외부에서 반영하면 target에 source가 포함된 것을 관찰해 증거를 기록한다.
- **GitHub 이력 동기화**: `samsara link add`로 기록한 issue·PR·commit 링크만 제공한다. 원격 동기화는 하지 않고, freshness를 `UNAVAILABLE`로 표시한다.
- **실행 중 계획 수정(pause+revise)**: 미구현이다. 실행 중 apply는 409를 반환하고, 새 계획은 기존 run을 취소한 뒤 적용한다.
- **Hook 실패 차단의 실측**: `claude --version` ≥ 2.1.295일 때만 managed execution을 허용한다. 실제 Claude에 missing binary·timeout을 주입하는 검증(HK-04~06)은 수동 절차이며, 결과는 manifest의 `policy_hook_failure_block_verified`에 기록한다. 이 값을 자동으로 true로 설정하지 않는다.
- **Windows, 원격 호스트, OS sandbox**: 범위 밖이다(ADR 23). Hook과 lease는 협력적 정책 경계이며, 같은 OS 사용자의 임의 프로세스를 격리하지 않는다.
- **대시보드 UI 번들**: `/`에는 안내 페이지만 있다. React 앱은 02 문서의 별도 작업이다.
