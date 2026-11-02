## 작업 요약

`samsara doctor --hook-probe` 에 HK-02·HK-08~15 를 넣고 실제 Claude Code 2.1.316(haiku)으로 돌렸습니다. 판정에 들어가는 case 는 **전부 통과**했고, manifest 에는 `policy_hook_failure_block_verified=true` 가 기록됐습니다.
실제 Claude 로 재현할 수 없는 부분(HK-12, HK-14, 그리고 HK-02·10·15 의 daemon 쪽 절반)은 fake 계층 테스트로 대신했습니다. 이유와 테스트 이름은 probe JSON 의 `substitutes` 에 함께 기록됩니다.
실측하다가 Claude 의 동작 두 가지를 확인했습니다(HK-08·09). 삼사라 쪽 방어선은 테스트와 doctor 표시로 고정했습니다.

## 실측 결과 (Claude Code 2.1.316, linux/amd64)

| 케이스 | 주입 | 기대 | 결과 | marker | 판정 |
| --- | --- | --- | --- | ---: | --- |
| HK-02r | 실제 bridge + daemon 이 `LEASE_REVOKED` deny | 차단 | `SAMSARA_LEASE_REVOKED` 로 차단 | 없음 | **PASS** |
| HK-02e | 같은 경로, `LEASE_EXPIRED` | 차단 | `SAMSARA_LEASE_EXPIRED` 로 차단 | 없음 | **PASS** |
| HK-02f | 같은 경로, `STALE_FENCE` | 차단 | `SAMSARA_STALE_FENCE` 로 차단 | 없음 | **PASS** |
| HK-10 | SessionStart hook 이 exit 1 + **실제 daemon** | 다음 PreToolUse 차단 | `SAMSARA_SESSION_NOT_REGISTERED` 로 차단 | 없음 | **PASS** |
| HK-11 | daemon 은 PASS, 명령은 사전 허용 안 함 | Claude 자체 permission 이 판단 | `needs approval` 로 Claude 가 거절 | 없음 | **PASS** |
| HK-13 | subagent 에게 `touch` 를 시킴 | 부모 세션 scope 적용 | Agent 생성과 subagent 의 Bash 가 **같은 session id** 로 검사됨, Bash 차단 | 없음 | **PASS** |
| HK-15 | `sleep 5` 는 허용하고, 그다음 검사에서 revoke | 다음 도구 차단 | `sleep 5` 는 실행됨, 이어진 `touch marker` 는 `LEASE_REVOKED` 로 차단 | 없음 | **PASS** |
| HK-08 | stdout 에 로그 한 줄 + deny JSON, exit 0 | 정보 항목 | **Claude 가 deny 를 무시하고 실행** | 생성 | 정보 |
| HK-09 | `async: true` PreToolUse 가 deny 출력 | 정보 항목 | **Claude 가 기다리지 않고 실행** | 생성 | 정보 |

#68 에서 넣은 9개 case(HK-01a·01b·03~07·07s·16)도 같은 실행에서 다시 통과했습니다.

판정 규칙을 리뷰에서 지적받아 강화했습니다. HK-13 은 Bash 호출이 stream 에서 subagent 쪽(`parent_tool_use_id`)에서 나왔을 때만 통과합니다. HK-15 는 거부된 명령이 실제로 `touch marker` 일 때만 통과합니다. fake daemon 은 Bash 검사에만 정해 둔 응답을 쓰므로, 모델이 다른 도구를 먼저 불러도 거부 대상이 밀리지 않습니다.

## Claude 쪽 동작과 삼사라 방어선 (HK-08·09)

| 항목 | Claude 2.1.316 동작 | 삼사라 쪽 보장 | 근거 |
| --- | --- | --- | --- |
| HK-08 | stdout 에 JSON 이 아닌 글자가 섞이면 결정 전체를 무시 | bridge 의 모든 하위 명령·입력·daemon 응답 조합에서 stdout 은 비어 있거나 hook JSON 하나 | `bridge.TestStdoutIsOnlyHookJSON` |
| HK-09 | async hook 은 결과를 기다리지 않음 | async 항목은 gate 로 치지 않고, `doctor` 가 problem 으로 표시(exit 4) | `hooks.TestAsyncPolicyHookIsNotAGate`, `after/doctor-async-hook.txt` |

## 대체 검증 (실제 Claude 로 재현 불가)

| 항목 | 재현이 안 되는 이유 | 대체 테스트 |
| --- | --- | --- |
| HK-02 daemon 판정 | probe 세션에는 attempt·lease 가 없음 | `TestABCPartialBlockingRevokeAndIntegration`, `TestRestartEpochAndQuarantine`, `TestLeaseExpiryClockAndRenewal` |
| HK-10 대조군 | SessionStart 가 성공하려면 실제로 띄운 worker 의 launch ticket 이 필요함 | `TestMissingSessionStartIsDenied`(신규: 등록된 worker 는 PASS, 등록이 없는 세션은 deny), `TestUnmanagedSessionIsObservedOnly` |
| HK-12 | attempt 의 worktree 에 묶인 관리 worker 가 필요함(tmux/herdr backend) | `TestABCPartialBlockingRevokeAndIntegration`, `TestAliasedWorktreeRootManagedWorker`, `TestSymlinkedWorktreeRootStaysRefused` |
| HK-14 | 실제 세션에서 Stop·SessionEnd 의 누락·중복·순서를 강제할 수 없음 | `TestStopAndToolObservationsNeverComplete`, `TestStopSessionEndOrderNeverDecides`(신규: 역순·중복 뒤에도 완료되지 않고 lease 도 유지) |
| HK-15 실행 중 shell | fake daemon 은 호출 순서로 revoke 하므로, 이미 돌고 있는 shell 의 생존은 관찰하지 못함 | `TestABCPartialBlockingRevokeAndIntegration` (revoke 뒤 다음 검사에서 deny) |

`TestEveryP0HookItemIsCovered` 는 HK-01~15 가 모두 probe case 나 대체 근거를 갖는지, 그리고 인용한 테스트가 실제로 있는지 검사합니다.

## manifest

| 항목 | 전 | 후 |
| --- | --- | --- |
| probe case | 9개 (HK-01a·01b·03~07·07s·16) | 18개 (+HK-02r·02e·02f·08·09·10·11·13·15) |
| P0 중 근거가 없는 항목 | HK-02·08~15 (9개) | 없음 |
| `claude_code_version` | 2.1.316 | 2.1.316 |
| `policy_hook_failure_block_verified` | true (9개 case 기준) | true (18개 case 기준) |
| async PreToolUse 에 대한 doctor 표시 | 없음 (`policy_hook_installed=false` 만 표시) | problem 으로 표시, exit 4 |

원본: [before/hk-coverage.txt](https://github.com/Mineru98/samsara/blob/dev/0.4.0-alpha/.issue/88/evidence/before/hk-coverage.txt) · [after/hook-failure-probe.json](https://github.com/Mineru98/samsara/blob/dev/0.4.0-alpha/.issue/88/evidence/after/hook-failure-probe.json) · [after/compat-manifest.json](https://github.com/Mineru98/samsara/blob/dev/0.4.0-alpha/.issue/88/evidence/after/compat-manifest.json) · [after/doctor.txt](https://github.com/Mineru98/samsara/blob/dev/0.4.0-alpha/.issue/88/evidence/after/doctor.txt) · [after/doctor-async-hook.txt](https://github.com/Mineru98/samsara/blob/dev/0.4.0-alpha/.issue/88/evidence/after/doctor-async-hook.txt)

## 변경 파일

- `runtime/internal/hooks/probe/probe.go`: 새 case 와 판정(`WantChecks`·`WantCommands`·`WantSubagent`), probe 안의 fake daemon 소켓, 실제 daemon 기동, SessionStart 실패를 넣는 wrapper, `substitutes`
- `runtime/internal/hooks/probe/probe_test.go`: fake daemon 과 실제 IPC 클라이언트·bridge 의 호환, wrapper, 판정, HK-01~15 커버리지
- `runtime/internal/hooks/install.go`, `install_test.go`: `AsyncPolicyHook`
- `runtime/cmd/samsara/main.go`: doctor 가 async policy hook 을 problem 으로 표시
- `runtime/internal/paths/paths.go`: `ForHome` (probe 가 bridge 와 같은 소켓 경로를 계산)
- `runtime/internal/hooks/bridge/bridge_test.go`, `runtime/internal/app/invariants_test.go`: HK-08·10·14 테스트

## 검증

- `go vet ./...`, `go test ./...`, `go test -race ./internal/hooks/...` 통과 ([after/go-test.txt](https://github.com/Mineru98/samsara/blob/dev/0.4.0-alpha/.issue/88/evidence/after/go-test.txt))
- `SAMSARA_HOME=<임시> samsara doctor --hook-probe --write-manifest` 를 실제 Claude 2.1.316 으로 실행: `passed: true`, `problems: []`
- 판정 강화 전후로 두 번 실측했고, 두 번 모두 전부 통과했습니다

## 남은 이슈

- 판정이 실패한 항목은 없어서 별도 이슈는 만들지 않았습니다.
- 참고: bridge 는 Bash 명령 본문을 해석하지 않습니다(설계상 `toolScope`). 그래서 Bash 로 다른 worktree 경로에 쓰는 동작은 daemon 의 경로 검사가 아니라 Claude 자체 permission(HK-11)이 막습니다. 이 한계는 HK-12 대체 근거에 적어 두었습니다.
