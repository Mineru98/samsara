## 작업 요약

pure phase API(`issue-start.mjs phase`)는 stateless 계약 그대로 두었습니다. 그 앞에 영속 단계 gate(`skills/issue-start/scripts/issue-gate.mjs`)와 Claude PreToolUse hook 을 붙였습니다.
gate 는 git common dir 의 ledger·lock(fence)에서만 상태를 만듭니다. 실제 파일·git·tracker 사실을 확인한 뒤에만 API 에 receipt 를 넘겨 다음 단계를 엽니다. 호출자의 완료 주장을 받는 입력은 없습니다.
브랜치: `fix/76-persistent-phase-gate` (구현 커밋 `a2d5839`·locale 수정 `9c4c142`·ref 범위 수정 `90fdc7e`, 기준 `ff71efb` dev/0.4.0-alpha)

## 변경 전후

| 시나리오 | 전 (ff71efb) | 후 (gate) |
| --- | --- | --- |
| 위조 state + `effectResult.succeeded` 로 implement 완료 | API exit 0 complete | gate 에는 해당 입력 없음. 변경 없는 complete → exit 3 `VERIFY_FAILED` |
| 같은 요청 replay | exit 0 complete | `already-complete`, ledger 바이트 동일 / `run` 재실행 exit 2, 효과 0 |
| 순서 위반 | exit 2 (요청 단위만) | 영속 ledger 기준 exit 2 `PHASE_SEQUENCE`, worktree 효과 0 |
| 같은 이슈 동시 worker 8개 | 잠금 없음 | 1개만 exit 0, 7개 exit 5 `LOCKED` |
| stale owner | 개념 없음 | exit 6 `RECONCILE_REQUIRED`, 자동 완료 없이 `abandoned`, 옛 세션 이후 행동 exit 5 |
| 효과 실패 / gate SIGKILL | 개념 없음 | exit 4, 커밋 0 / 자식 생존 중 exit 6 `STILL_RUNNING`, 실제 커밋 확인 후에만 완료 |
| Bash 우회(`printf >`, `python3 subprocess git commit`, 가짜 `*issue-gate.mjs`, `VAR=` 주입) | 제어 없음 | before/implement/after/무잠금 모두 deny |
| legacy CLI(`evidence-commit`) 직접·fence 복제 | 제어 없음 | exit 7 `GATE_REQUIRED`, 커밋 0 |
| 테스트 실행 | 제어 없음 | controller 등록 profile + 승인 tree 만(`gate exec`), ref 변화 시 quarantine |
| implement 완료 조건 | 없음 | 필수 profile 이 현재 tree 에서 PASS, 커밋 tree == 검증 tree |

측정: 아래 `검증` 표의 명령. 원본은 작업 브랜치의 `.issue/76/evidence/` (before: `pure-phase-probe.txt`, `no-gate.txt`, `node-tests.tap` / after: `gate-tests.txt`, `node-tests.tap`, `pure-phase-probe.txt`, `gate-present.txt`).
브랜치 증거 디렉터리: https://github.com/Mineru98/samsara/tree/fix/76-persistent-phase-gate/.issue/76/evidence

## 변경 파일

- `skills/issue-start/scripts/issue-gate.mjs` — ledger·lock·verifier·실행 broker·reconcile·PreToolUse hook·install-hook (신규)
- `skills/issue-start/scripts/issue-gate.test.mjs` — 실제 git 저장소·프로세스 기반 회귀 24개 (신규)
- `skills/issue-start/scripts/issue-start.mjs` — gated 이슈의 legacy 부수효과 서브커맨드는 현재 gate step 의 직접 자식만 허용
- `hooks/hooks.json` — 플러그인 PreToolUse hook (`|| exit 2`, `onFailure: block`)
- `skills/issue-start/SKILL.md` — gate 경계 요약
- `skills/issue-start/scripts/issue-start-phase.mjs` — **무변경** (closure sha256 `e64632a8…` 그대로)

## 검증

| 명령 | 종료코드 | 결과 |
| --- | ---: | --- |
| `node --test --test-concurrency=1 issue-gate.test.mjs` | 0 | **24 / 24 pass** |
| `node --test $(find . -name '*.test.mjs' -not -path '*/node_modules/*')` (before) | 0 | 163 / 163 pass |
| 같은 명령 (after) | 0 | **187 / 187 pass** |
| `issue-start.mjs phase --request <forged/replay>` (after) | 0 | 의도대로 무변경(pure API 는 강제 지점이 아님) |

controller 독립 재실행도 a2d5839 기준 185/185(exit 0)이었습니다. 실제 병렬 실행에서 ko/C 로케일 차이로 산 owner 를 UNKNOWN 으로 보는 결함이 발견되었고, locale 독립 비교로 수정했습니다(회귀 추가). 또 같은 common dir 을 공유하는 병렬 worktree 에서, broker 검사가 저장소 전체 ref 를 비교해 #76 의 정상 commit/push 를 #75 변조로 오탐 격리한 실제 장애가 있었습니다. 검사 대상을 이슈 소유 ref 로 한정해 수정했습니다(동시 commit/push 회귀 추가, 근거 `after/incident-75-ref-scope.txt`). 오케스트레이터가 실제 Claude PreToolUse 경로를 실측했습니다(`cc --print`). 결과는 잠긴 Write 거부, before 확인 후 begin implement, 같은 Write 허용입니다. hook missing binary·timeout 은 `onFailure: block` 으로 파일 생성 0 이었습니다. 이 실측은 위 대역(sleep pid 세션) 테스트와 별도 결과입니다.

## 남은 이슈

- 보장 범위는 Claude 도구 호출(Edit/Write/MultiEdit/NotebookEdit/Bash)과 gate·issue-start CLI 경로입니다. 같은 OS 사용자의 임의 프로세스나 hook 없는 세션을 막는 **OS sandbox 가 아닙니다**.
- `gate exec` 는 승인된 tree 의 코드를 실행합니다. 그 안의 commit/push 는 사전에 막지 못하고, 로컬 ref 변화만 감지해 격리합니다. 원격 push 는 되돌릴 수 없으므로 예방은 controller 의 tree 검토(`approve-exec`)에 달려 있습니다.
- Read·MCP 도구는 gate 대상이 아닙니다. 서브에이전트 도구 호출의 session 결합은 실측하지 않았습니다.
- 기본 브랜치 증거 미러(`evidence-mirror`)·`sync-base` 는 다른 작업과 충돌하므로 오케스트레이터에 위임해 보류했습니다. 그래서 이 리포트의 증거 링크는 작업 브랜치 파일입니다.
- 실제 herdr의 #74/#75/#77 worker가 gate의 13단계 완료까지 수행했습니다. 원본은 `after/real-managed-workers.json` 및 native/병렬 실행 증거 파일에 추가했습니다.

## Controller 실제 결합 검증

실제 herdr `samsara #74`, `samsara #75`, `samsara #77` 탭의 Claude(Opus) 작업이 독립 lock을 사용했고, 각 작업의 필수 검사 PASS→구현 완료→커밋→after→증거 게시→이슈 코멘트→sync-base→handback 순서로 13단계를 완료했습니다. #76은 이 gate 자체를 만드는 bootstrap 작업이므로 그 구현이 처음부터 이 gate로 보호됐다고 주장하지 않습니다.

실제 실패도 다음 단계를 막았습니다. #75의 병렬 ref 오탐 격리 중에는 커밋이 생성되지 않았고, source 검토·원인 수정·controller 격리 해제 뒤 같은 단계에서 재시도했습니다. #74 증거 게시 실패 직후 comment 시도는 exit 2 PHASE_SEQUENCE였으며 후속 게시 효과가 없었습니다.

네 수정 결합 검증: Node **206/206**, npm audit **0건**, macOS 기본 TMPDIR에서 Go 전체·app race·vet·빌드 통과. 별도 alias SAMSARA_HOME CLI smoke의 init/status/doctor --exercise/daemon stop도 모두 exit 0, policy hook 설치 인식과 실제 tmux 생성·종료를 확인했습니다. 자료는 이번 controller의 `.issue/recheck-20261029/`와 위 after 증거에 있습니다.
