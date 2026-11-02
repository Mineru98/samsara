## 작업 요약

herdr 어댑터가 실제로 worker 를 만들고·관찰하고·프롬프트를 보내도록 구현했습니다. 설치된 herdr 0.9.3(protocol 22)의 schema 를 fixture 로 고정했고, `samsara doctor --exercise --write-manifest` 가 실제 생성·관찰·종료를 통과했을 때만 manifest 에 `herdr_protocol_verified=true` 를 기록합니다. 검증 전이나 schema 가 바뀌면 지금처럼 미지원(`HERDR_COMPAT_UNVERIFIED` / `HERDR_SCHEMA_MISMATCH`)으로 남아 tmux 로 넘어갑니다.

worker 생성 방식은 착수 중 확인한 사실에 따라 정했습니다. herdr 의 `agent start` 는 셸에 `claude <args>` 를 직접 입력하므로 lease 하트비트를 맡은 `samsara launcher` 를 끼울 수 없습니다. 그래서 소유 workspace(cwd·env) 생성 → launcher 명령 입력의 2단계로 만들고, 이슈 프롬프트는 argv 에 넣지 않고 등록 후 첫 dispatch(`agent.prompt`)로 보냅니다.

## 변경 전후

| 항목 | 전 | 후 |
| --- | --- | --- |
| herdr probe (검증 전) | DEGRADED, 전 capability `UNSUPPORTED(HERDR_COMPAT_UNVERIFIED)` | 동일 (검증 전에는 그대로 미지원) |
| 검증 절차 | 없음 (`herdr_protocol_verified` 를 쓰는 경로 없음) | `doctor --exercise --write-manifest` → herdr exercise `ok: true` → `herdr_protocol_verified: true` |
| herdr probe (검증 후) | — | **AVAILABLE**, create_worker `SUPPORTED`, submit_prompt `CONDITIONAL` |
| auto 모드 신규 worker | tmux | **herdr** (BE-01) |
| contract suite (실제 herdr) | 없음 | **13/13 PASS** (BE-01~BE-11 + 보조 2) |
| contract suite (가짜 서버, CI) | 없음 | **13/13 PASS** |

측정: 격리된 herdr 0.9.3 서버(별도 `XDG_CONFIG_HOME`, `/bin/sh` pane, 입력을 그대로 출력하는 stand-in `claude`)에서 실행했고, 사용자의 herdr 세션은 건드리지 않았습니다. 원본: before [go-test](https://github.com/Mineru98/samsara/blob/dev/0.4.0-alpha/.issue/64/evidence/before/go-test.txt) · [doctor-herdr](https://github.com/Mineru98/samsara/blob/dev/0.4.0-alpha/.issue/64/evidence/before/doctor-herdr.txt) / after [go-test](https://github.com/Mineru98/samsara/blob/dev/0.4.0-alpha/.issue/64/evidence/after/go-test.txt) · [doctor-herdr](https://github.com/Mineru98/samsara/blob/dev/0.4.0-alpha/.issue/64/evidence/after/doctor-herdr.txt) · [contract-real-herdr](https://github.com/Mineru98/samsara/blob/dev/0.4.0-alpha/.issue/64/evidence/after/contract-real-herdr.txt)

| BE | 확인 내용 (실제 herdr) |
| --- | --- |
| BE-01 | 검증된 herdr 가 auto 모드에서 선택됨 |
| BE-02 / BE-06 | 서버 중단 시 worker 는 `UNKNOWN`(오류와 함께) 으로 남고, 신규 worker 만 tmux 로 감 |
| BE-03 / BE-04 | herdr·tmux 모두 없으면 standalone, tmux 명시 시 tmux |
| BE-05 | 삼사라 소유가 아닌 workspace 는 Lookup·입력·종료 모두 거부 |
| BE-07 | 서버 재시작·복원 후 같은 pane id 는 이전 worker 로 보지 않음 |
| BE-08 | agent 가 working·blocked 이면 자동 입력 보류, idle 일 때만 전달 |
| BE-09 | `agent.prompt` 응답 유실 → `UNKNOWN` (실제로는 입력됨 — 재전송 금지) |
| BE-10 | `workspace.create`·`pane.send_text` 응답 유실 후 재시도해도 workspace 1개, 명령 1회 실행 |
| BE-11 | `events_lost` 주입 → stale 표시 → 재구독 → authoritative 재조회, reconcile 트리거 |
| schema | hash 불일치 시 `HERDR_SCHEMA_MISMATCH` 로 미지원 처리 |

## 변경 파일

- `runtime/internal/backend/herdr/client.go` — socket client(요청당 연결 1개)와 구독, 응답 유실(Sent) 구분
- `runtime/internal/backend/herdr/herdr.go` — probe 검증, 2단계 생성+checkpoint, 관찰·generation, prompt·interrupt·close, Watch(stale)
- `runtime/internal/backend/herdr/testdata/schema-0.9.3-protocol22.json` — 고정 schema fixture
- `runtime/internal/backend/backend.go`, `backend/tmux/tmux.go` — `LaunchSpec.Prompt`·checkpoint, tmux 는 argv 끝에 프롬프트(기존 동작 유지)
- `runtime/internal/app/outbox.go`, `sessions.go`, `scheduler.go` — launch checkpoint 를 outbox 에 저장·재사용, herdr 초기 프롬프트 dispatch 와 일시 상태 재시도
- `runtime/internal/daemon/daemon.go`, `runtime/cmd/samsara/main.go` — manifest 버전 전달·구독 기동, doctor herdr exercise 와 verified 기록
- 테스트: `herdr/{contract,fakeserver,realserver,herdr}_test.go`, `app/deferred_prompt_test.go`, `backend/fake/fake.go`(주입 옵션)

## 검증

- `go vet ./...`, `go test ./...` 통과
- `SAMSARA_HERDR_IT=1 go test ./internal/backend/herdr/ -run Contract -v` — 실제 herdr 0.9.3 에서 13/13 PASS
- 별도 리뷰 에이전트 검토에서 나온 높음 1·중간 2건(등록이 binding 보다 먼저 올 때 초기 프롬프트 유실, 일시 상태로 초기 프롬프트 SKIP, 명령 재입력 시 한 줄 중복)을 고치고 재현 테스트를 추가했습니다.

## 남은 이슈

- 실제 Claude 로는 시험하지 않았습니다. launcher 로 감싼 claude 를 herdr 가 agent 로 인식하는 것까지는 확인했지만, 상태가 `unknown` 에서 `idle` 로 바뀌려면 herdr 의 Claude 연동(hook) 또는 화면 감지가 필요합니다. 끝내 `idle` 이 안 되면 초기 프롬프트는 약 5분 재시도 후 `resume_required` 로 표시됩니다.
- 기존 세션 연결(attach_session), output 조회, 대화 복원은 이번 범위 밖이라 `UNSUPPORTED` 로 보고합니다.
- 명령 줄 quote 는 POSIX 셸·zsh 기준입니다. fish 에서는 argv 에 백슬래시가 있으면 달라질 수 있습니다(설정 argv 에만 해당).
- `docs/architecture/06-runtime-remaining-work.md` 의 herdr 항목 갱신은 하지 않았습니다.
