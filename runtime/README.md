# Samsara runtime (Go)

`docs/architecture/`의 설계 문서(00~05)를 기준으로 구현한 삼사라 로컬 오케스트레이션 런타임이다.
한 OS 사용자당 daemon 하나가 여러 프로젝트의 Claude Code 작업을 관리한다. 관리 대상은 의존성·승인·실행권(lease)·세션 관찰·Worktree·통합(merge)이다.

- 언어/런타임: Go 1.25+ (`go.mod`), SQLite는 `modernc.org/sqlite` v1.55.0에 포함된 엔진 **3.53.3**을 사용한다. 기준은 3.51.3 이상이고, 시작할 때 `sqlite_version()`으로 확인한다.
- 단일 바이너리 `samsara`가 CLI, daemon, Hook Bridge, worker launcher 역할을 모두 맡는다.
- React 대시보드(02 문서) 소스는 저장소 루트 `web/`에 있다. `pnpm -C web build`가 `internal/webui/static/`에 번들을 만들고, 런타임이 이를 embed해 같은 loopback origin에서 서빙한다. 번들 없이 빌드하면 `/`는 안내 페이지를 보여준다.

## 빌드와 테스트

```bash
pnpm -C web install && pnpm -C web build   # 대시보드 번들(선택). 없으면 / 는 안내 페이지
cd runtime
go build -o bin/samsara ./cmd/samsara
go test ./...            # 단위·불변식·실제 git/tmux 통합 테스트
go test -race ./internal/app/
```

테스트에는 실제 `git`이 필요하다. `tmux` 어댑터 테스트는 tmux가 없으면 건너뛴다.

## 배포·업그레이드·복원 (05 §17)

```bash
runtime/scripts/release.sh                 # dist/samsara_<VERSION>_<os>_<arch>.tar.gz + SHA256SUMS (linux/darwin × amd64/arm64)
runtime/scripts/drill.sh                   # release tarball만으로 설치 smoke → 업그레이드 → restore 롤백 → export 점검
```

- 버전은 저장소 `VERSION`을 `-ldflags "-X main.version=..."`으로 주입한다. SQLite는 pure-Go driver라 `CGO_ENABLED=0`으로 교차 빌드한다.
- `samsara install`: 실행한 binary를 `<data>/samsara/install/versions/<버전>/samsara`로 복사하고 `install/bin/samsara` 링크를 그쪽으로 바꾼다. 다른 버전은 지우지 않는다. hook은 링크가 아니라 버전 경로를 가리키고, install·upgrade 때 설치 디렉터리의 버전을 가리키던 hook(등록 프로젝트·관리 worktree)은 새로 설치한 버전으로 옮긴다. 이전 binary는 이미 hook을 읽은 세션을 위해 디스크에 남는다. 개발 빌드처럼 설치 디렉터리 밖 binary를 가리키는 hook은 건드리지 않는다.
- 플러그인·스크립트는 `scripts/samsara-bin.sh`로 binary 경로를 찾는다(`$SAMSARA_BIN` → install 링크 → `${CLAUDE_PLUGIN_ROOT}/runtime/dist/<os>_<arch>/samsara`(release.sh가 tarball과 함께 풀어 둔다) → PATH).
- `samsara upgrade [--start] [--prune-old]`는 **새 binary로** 실행한다. 실행 중인 start lock 획득(그 사이 hook이 구 binary로 daemon을 띄우지 못함) → daemon 정지 → daemon lock 획득(구·신 프로세스 동시 쓰기 차단) → 미적용 DB 백업(`state/backups/pre-upgrade-*.db`) → migration → `integrity_check`·`foreign_key_check` → 설치 → 새 daemon 시작·버전 확인. 실패하면 이후 단계를 하지 않고 복원할 백업 경로를 알려 준다. 더 새로운 binary가 만든 DB는 건드리지 않는다.
- `--prune-old`는 살아 있는 관리 세션이 있거나 등록 프로젝트·관리 worktree의 hook이 실행하는 버전을 남긴다(ACT-12).
- `samsara restore --from <backup>`은 daemon이 멈춘 상태에서만 동작한다. 백업을 검증(무결성, schema ≤ binary, checksum)하고 현재 DB를 `pre-restore-*.db`로 남긴다. 복사본에서 migration·복원 처리·무결성 확인을 모두 끝낸 뒤 rename 한 번으로 교체하므로, 중간에 끊겨도 현재 DB는 그대로다. 복원된 DB는 `database_generation`을 새로 받아 이전 UI cursor가 `reset_required`가 된다(EVT-11). 진행 중이던 outbox intent는 UNKNOWN이 되고(재전송 금지), PUBLISHING 후보는 실제 target을 다시 읽는다(DB-13). 외부를 먼저 확인하는 intent(worktree 생성, worker launch, git publish 등)는 reconcile이 다시 큐에 넣고, prompt·interrupt처럼 재전송이 위험한 intent는 사람이 판단할 때까지 UNKNOWN으로 남는다. 다음 daemon 시작이 backend·Git·GitHub를 reconcile한 뒤 새 작업을 받는다.

## 관측 (01 §18)

- `samsara metrics`: hook decision latency(p50/p95/p99), decision·deny reason별 count, SQLite writer latency, SSE 연결·재연결·`reset_required` 사유별 count(메모리, 현재 epoch 기준)와 DB gauge(active attempts, UNKNOWN sessions, quarantined attempts, outbox pending·lag, ambiguous operations, merge queue depth, event retention).
- `samsara doctor --export <새 파일>`: doctor 결과, manifest, registry, 프로젝트 config, runtime·metrics, `daemon.log` 마지막 200줄을 0600 JSON으로 쓴다. token·secret·password·credential·authorization·cookie·api key·prompt·transcript·tool input/response·payload·bootstrap 이름의 필드는 `[REDACTED]`, GitHub·Anthropic·Slack·AWS 자격증명과 Bearer 값은 본문에서 지운다.
- 이벤트 replay 보존 기간은 `observation.event_retention_days`(기본 7)다. 이벤트 cursor 경계가 전역이라 활성 프로젝트 중 가장 긴 값을 쓴다(각 프로젝트는 설정한 기간 이상 보존된다).

## 빠른 시작

```bash
samsara init                       # .samsara/config.yaml 생성(없을 때), .claude/settings.local.json에 관리 hook 추가, 로컬 trust 등록
samsara doctor --exercise          # claude/tmux/herdr/git/SQLite 버전, hook 설치, tmux 생성·종료 실측
samsara doctor --hook-probe --write-manifest   # 실제 Claude에 hook 실패를 주입해 fail-closed 실측(모델 호출 발생)
samsara profile add --name unit -- go test ./...   # 검증 명령은 trust 등록한 profile만 실행
samsara plan validate --file plan.json             # 변경 없음
samsara plan apply --file plan.json                # PLANNED run 접수(worker 생성 없음)
samsara run start <run_id>                         # PLAN 승인 확인 후 ACTIVE → scheduler가 실행
samsara plan revise --run <run_id> --expected-run-generation N --file plan.json   # PAUSED run의 계획 수정(run pause → revise → run resume)
samsara approval list / samsara approval decide <id> --approve
samsara merge queue / samsara merge enqueue <candidate_id>
samsara merge resolve <candidate_id>               # CONFLICT 후보를 해결할 새 attempt(보존된 worktree 재사용)
samsara merge prioritize <candidate_id> --before <id> --reason <text>   # 의존 순서를 거스르면 거부
samsara worktree cleanup --dry-run [--delete-branch] [<worktree_id>...]  # 정리 자격 판정, --force 없음
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
| `github.sync` | `auto` | `auto`면 origin이 github.com이고 `gh`가 로그인돼 있을 때 이력을 동기화, `off`면 조회하지 않음 |
| `github.repository` | `""` | `owner/name`. 비우면 origin remote에서 추론 |
| `github.sync_interval_seconds` | 300 | 동기화 주기(최소 60). 최근 성공이 2주기 이내면 freshness `LIVE`, 넘으면 `STALE` |
| `github.close_issues` | `false` | `true`일 때만 로컬 이슈 COMPLETED 시 `samsara link add --issue`로 연결한 GitHub 이슈를 닫는다(쓰기) |

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
- **GitHub native merge queue(`github_queue`)**: daemon이 target branch rules를 조회해(04 §10.2) Merge Queue·ALLGREEN·build/merge 크기 1·`github.merge_queue.policy_app_id`로 고정된 필수 `samsara/integration-policy` check·check 게시자(GitHub App 자격증명)를 모두 확인한 경우에만 enqueue를 받는다. 하나라도 빠지면 후보는 `WAITING(<사유>)`(예: `MERGE_QUEUE_DISABLED`, `POLICY_PUBLISHER_UNAVAILABLE`)에 머물고 enqueue는 `UNSUPPORTED_CAPABILITY`다. 이 빌드에는 App 자격증명 게시자가 없으므로 실제 환경에서는 미지원 상태가 유지된다. 지원 target에서는 head가 승인된 source인 열린 PR을 GraphQL `enqueuePullRequest`(expectedHeadOid)로 넣고(`WAITING → PUBLISHING`), polling으로 `native_queue_state`를 추적한다. GitHub가 만든 group(entry·head·base)마다 revision을 올려 이전 MERGE 승인을 무효화하고 policy check를 in_progress로 재설정하며, 그 group에 대한 MERGE 승인이 같은 transaction에서 확인될 때만 success를 게시한다. PR이 merge되면 provider 증거로 `MERGED`를 기록한다. 실제 org 시험 저장소·App으로 한 end-to-end 검증은 아직 없다. `manual` authority는 사용자가 외부에서 반영하면 target에 source가 포함된 것을 관찰해 증거를 기록한다.
- **GitHub 이력 동기화**: 사용자의 `gh` 로그인으로 issue·PR·commit·check·review를 읽기 전용 조회해 `external_links`에 출처(`source`)·동기화 시각(`synced_at`)과 함께 기록한다. 토큰은 런타임이 읽지도 저장하지도 않는다. cursor(`since`) 증분 조회, rate limit은 reset 시각까지 대기, 그 밖의 장애는 지수 backoff다. 장애·rate limit은 `github-history`의 `sync` 상태(`AUTH_REQUIRED`·`REPO_NOT_FOUND`·`RATE_LIMITED`·`PROVIDER_ERROR`)로만 나타나고 로컬 업무 상태는 바꾸지 않는다. `POST /v1/projects/{id}/refresh`는 다음 동기화를 즉시 실행한다(rate limit 창은 유지). GitHub Enterprise 원격과 PR↔로컬 이슈 자동 연결은 지원하지 않는다.
- **실행 중 계획 수정(pause+revise)**: 미구현이다. 실행 중 apply는 409를 반환하고, 새 계획은 기존 run을 취소한 뒤 적용한다.
- **Hook 실패 차단의 실측**: `claude --version` ≥ 2.1.295일 때만 managed execution을 허용한다. `samsara doctor --hook-probe`는 임시 디렉터리에서 실제 Claude(haiku)에 HK-01·03·04·05·06·07·16을 주입하고, `touch marker` 실행 여부로 차단을 판정한다. 결과는 `<state>/hook-failure-probe.json`에 남고, `--write-manifest`를 함께 주면 통과한 Claude 버전만 manifest의 `policy_hook_failure_block_verified=true`로 기록한다. probe 없이 Claude 버전이 바뀌면 이 값은 false로 돌아간다. doctor의 `claude_fail_closed`는 버전 조건과 실측을 모두 통과했을 때만 true다. managed execution 허용 조건은 아직 버전 숫자만 본다. 참고로 Claude는 문법이 깨진 hook stdout(exit 0)을 plain text로 보고 native permission에 맡긴다(HK-07s, 정보 항목). bridge는 JSON을 한 번에 쓰고 실패하면 exit 2로 끝나므로 이 경로를 만들지 않는다.
- **Windows, 원격 호스트, OS sandbox**: 범위 밖이다(ADR 23). Hook과 lease는 협력적 정책 경계이며, 같은 OS 사용자의 임의 프로세스를 격리하지 않는다.
- **대시보드 UI 번들**: `/`에는 안내 페이지만 있다. React 앱은 02 문서의 별도 작업이다.
