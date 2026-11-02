## 작업 요약

`samsara doctor --hook-probe` 를 추가했습니다. 임시 디렉터리에서 실제 Claude Code(haiku)에 hook 실패를 주입하고, `touch marker` 가 실제로 실행됐는지를 marker 파일로 판정합니다. `--write-manifest` 와 함께 쓰면 **통과한 Claude 버전만** manifest 의 `policy_hook_failure_block_verified=true` 로 기록합니다.
실측 중에 결함 하나를 찾아 고쳤습니다. probe 없이 `doctor --write-manifest` 를 돌리면 다른 Claude 버전에서 받은 `true` 가 새 버전으로 그대로 옮겨 붙고 있었습니다.

## 실측 결과 (Claude Code 2.1.316, linux/amd64)

| 케이스 | 주입 | 기대 | 결과 | marker | 판정 |
| --- | --- | --- | --- | ---: | --- |
| HK-01a | PASS hook, 명령 사전 허용 | 실행 | 실행 | 생성 | **PASS** |
| HK-01b | PASS hook, 사전 허용 없음 | Claude 자체 permission 이 판단 | `needs approval` 로 Claude 가 거절 | 없음 | **PASS** |
| HK-03 | 관리 세션 + daemon 없음 (실제 samsara 바이너리) | bridge deny | `SAMSARA_RUNTIME_UNAVAILABLE` 로 차단 | 없음 | **PASS** |
| HK-04 | hook 바이너리 없음 | 차단 | `failed; blocking because onFailure is "block"` | 없음 | **PASS** |
| HK-05 | 실행 권한 없음 | 차단 | 같은 이유로 차단 | 없음 | **PASS** |
| HK-06 | timeout 초과(3s, sleep 10) | 차단 | `timed out; blocking …` | 없음 | **PASS** |
| HK-07 | schema 위반 JSON (`permissionDecision: bogus`) | 차단 | JSON 검증 실패로 차단 | 없음 | **PASS** |
| HK-16 | 한글·공백·괄호가 든 설치 경로 | hook 실행, 도구 실행 | 그대로 실행됨 | 생성 | **PASS** |
| HK-07s | 문법이 깨진 JSON, exit 0 | 정보 항목 | Claude 가 plain text 로 보고 native permission 에 맡김 | 생성 | 정보 |

판정 규칙은 이렇습니다. 모델이 Bash 를 시도했고 그 결과(tool_result)를 받은 경우만 판정합니다. 시도하지 않았거나 실행 도중 죽으면 `INCONCLUSIVE` 이고 통과로 치지 않습니다. HK-03~07 은 Claude 의 hook 오류 문구까지 확인하므로, Claude 자체 permission 거절로는 통과할 수 없습니다.

HK-07s 는 판정에서 뺀 정보 항목입니다. Claude 는 파싱할 수 없는 stdout 을 "결정 없음"으로 봅니다. 그래서 삼사라가 allow 를 준 것은 아니고, 사용자 permission 이 허용하면 도구가 실행됩니다. bridge 는 JSON 을 marshal 한 뒤 한 번에 쓰고, 쓰기에 실패하면 exit 2 로 끝납니다. 따라서 bridge 에는 이 출력을 만드는 경로가 없습니다.

## manifest 변화

| 시나리오 | 전 | 후 |
| --- | --- | --- |
| `doctor --hook-probe` | 플래그 없음 (exit 2) | 위 표 실행, `hook-failure-probe.json` 저장 |
| `--hook-probe --write-manifest` (2.1.316) | 검증 경로 없음, 항상 `false` | `claude_code_version=2.1.316`, `verified=true`, `claude_fail_closed=true` |
| manifest 를 2.1.300+`true` 로 바꾼 뒤 probe 없이 `--write-manifest` | 2.1.316 이 `failure_injection_verified: true` 로 **잘못 표시** | `verified=false`, `claude_fail_closed=false` |

원본: [before/doctor.txt](BEFORE_TXT) · [after/doctor.txt](AFTER_TXT) · [after/hook-failure-probe.json](AFTER_JSON)

## 변경 파일

- `runtime/internal/hooks/probe/probe.go` (신규): 케이스 정의, 실제 설치 형식(`hooks.Install`)으로 시험 디렉터리 구성, stream-json 판정
- `runtime/internal/hooks/probe/probe_test.go` (신규): 판정·파서 단위 테스트, 가짜 claude 로 실행 경로 확인
- `runtime/cmd/samsara/main.go`: `doctor --hook-probe`, `claude_fail_closed` 표시, probe 중 버전이 바뀌면 불통과
- `runtime/internal/daemon/daemon.go`, `lock_test.go`: `Manifest.RecordClaude`, 버전이 바뀌면 판정을 초기화
- `runtime/README.md`, `docs/architecture/06-runtime-remaining-work.md`: 절차 갱신

## 검증

- `go vet ./...`, `go test ./...` (runtime) 통과
- 실제 Claude 실측 `doctor --hook-probe --write-manifest`: exit 0, 11.6s, 필수 8케이스 모두 PASS
- 별도 리뷰 레인에서 거짓 PASS 경로 2건을 지적받았습니다(실행 도중 종료를 차단으로 판정, native 거절을 hook 차단으로 판정). 둘 다 반영한 뒤 다시 실측했습니다.

## 화면 캡처 생략 사유

UI 변경이 없는 CLI·런타임 작업입니다. 그래서 스크린샷 대신 명령 출력 원본과 probe JSON 을 증거로 커밋했습니다.

## 남은 이슈

- managed execution 허용 조건(`scheduler.go`·`sessions.go` readiness)은 아직 버전 숫자(`BlockOnFailure`)만 봅니다. 이 이슈는 "표시" 범위로 해석했습니다. 실측 통과를 실행 조건으로 올리면 probe 를 돌리지 않은 기존 사용자가 모두 막히므로 별도 결정이 필요합니다.
- HK-08·HK-09·HK-10~15 는 실제 Claude 가 필요 없는 bridge/app 계층 항목이라 이번 실측에 넣지 않았습니다.
