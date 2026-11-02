# 06. 런타임 구현 현황과 남은 작업

- 작성일: 2026-10-28, Asia/Seoul
- 대상 구현: `runtime/` Go 모듈(브랜치 `docs/samsara-architecture-plan` → `dev/0.4.0-alpha`)
- 목적: 00~05 문서 가운데 이번 구현에 들어가지 않은 부분을 분리해 이슈로 추적한다.

이 문서는 00~05의 설계를 바꾸지 않는다. 구현 범위와 추적 이슈만 기록한다.
구현된 범위와 불변식별 테스트 위치는 [`runtime/README.md`](../../runtime/README.md)에 있다.

## 1. 이번에 구현한 범위 요약

| milestone | 상태 | 비고 |
|---|---|---|
| M0 계약·호환성 | 부분 | compatibility manifest와 `samsara doctor`는 있음. 생성 schema·fixture는 [#71](https://github.com/Mineru98/samsara/issues/71) |
| M1 활성화·DB·IPC·Standalone | 구현 | opt-in registry, SQLite 스키마, UDS IPC, Standalone attach |
| M2 Fake 정책·lease·승인·복구 | 구현 | A/B/C, revoke, 재시작 epoch, ACK 유실, drain 테스트 |
| M3 조회 API·snapshot·SSE | 구현 | cursor replay, `reset_required`, bootstrap 인증 |
| M4 읽기 전용 React UI | 미구현 | [#69](https://github.com/Mineru98/samsara/issues/69) |
| M5 실제 backend·Git·GitHub 조회 | 부분 | tmux, 로컬 Git 후보, GitHub 이력 조회([#66](https://github.com/Mineru98/samsara/issues/66))는 구현. herdr [#64](https://github.com/Mineru98/samsara/issues/64) |
| M6 승인·중단·재개·통합 제어 | 부분 | API·CLI 제어와 `samsara_local` 통합은 구현. `github_queue` [#65](https://github.com/Mineru98/samsara/issues/65), 제어 UI [#69](https://github.com/Mineru98/samsara/issues/69) |
| M7 배포·백업·성능·파일럿 | 부분 | release binary·install·upgrade·restore·metrics·진단 export는 구현([#72](https://github.com/Mineru98/samsara/issues/72)). 성능 측정·파일럿은 미구현 |

## 2. 남은 작업과 추적 이슈

| # | 남은 작업 | 설계 근거 | 현재 런타임 동작 | 이슈 |
|---|---|---|---|---|
| 1 | herdr 어댑터 실제 구현과 protocol 검증 | 01 §5·§7, 05 §9 | version·schema hash만 기록하고, 모든 capability를 `UNSUPPORTED(HERDR_COMPAT_UNVERIFIED)`로 보고 → auto mode는 tmux 선택 | [#64](https://github.com/Mineru98/samsara/issues/64) |
| 2 | GitHub native merge queue(`github_queue`) | 04 §10, 03 §3.4 | branch rules probe·enqueue·polling·group별 MERGE 승인/policy check 상태기계 구현(fake provider 테스트). GitHub App check 게시자(`<state>/github-app.json`)와 `merge_group` manifest 검증 구현([#107](https://github.com/Mineru98/samsara/issues/107), fake provider 테스트). 자격증명이 없으면 `WAITING(POLICY_PUBLISHER_UNAVAILABLE 등)`·`UNSUPPORTED_CAPABILITY` 유지. 실제 시험 저장소 e2e 미검증(`-tags e2e` 시험 코드만 있음) | [#65](https://github.com/Mineru98/samsara/issues/65), [#107](https://github.com/Mineru98/samsara/issues/107) |
| 3 | GitHub 이슈·PR·commit 이력 동기화, refresh | 01 §17, 04 §15, 02 §12 | `gh` 읽기 전용 증분 동기화(issue·PR·commit·check·review), freshness `LIVE`/`STALE`, refresh 즉시 재조회, `github.close_issues` opt-in 닫기. GitHub Enterprise·PR↔로컬 이슈 자동 연결은 미지원 | [#66](https://github.com/Mineru98/samsara/issues/66) |
| 4 | 실행 중 계획 수정(pause + revise) | 01 §12.4, 03 §4.6 | 실행 중 apply는 409, run 취소 후 재적용만 가능 | [#67](https://github.com/Mineru98/samsara/issues/67) |
| 5 | Hook 실패 차단 실측(HK-04~HK-09) | 01 §10.5, 05 §8 | `samsara doctor --hook-probe`로 실제 Claude 실측(HK-01·03·04·05·06·07·16), 통과한 버전만 manifest `policy_hook_failure_block_verified=true`. managed 실행 허용은 아직 버전 숫자 기준 | [#68](https://github.com/Mineru98/samsara/issues/68) |
| 6 | React 대시보드(읽기 전용 → 제어) | 02 전체 | API·SSE만 있고, `/`는 안내 페이지 | [#69](https://github.com/Mineru98/samsara/issues/69) |
| 7 | Worktree 정리 수명주기, 충돌 해결 흐름, 큐 우선순위 | 04 §6.3·§8.3·§12 | `RETAINED`까지만 관리, `CONFLICT`는 표시와 취소만 가능 | [#70](https://github.com/Mineru98/samsara/issues/70) |
| 8 | OpenAPI·JSON Schema·TS 타입·계약 fixture | 03 §12.2 | wire 타입은 Go struct로만 존재 | [#71](https://github.com/Mineru98/samsara/issues/71) |
| 9 | 배포·업그레이드·복원·metrics·진단 export | 01 §14·§18, 05 §16~§17 | `release.sh`·`install`·`upgrade`·`restore`·`metrics`·`doctor --export`, `observation.event_retention_days` 반영. lease renewal failure·backend observation age·scheduler latency metric은 미구현 | [#72](https://github.com/Mineru98/samsara/issues/72) |

## 3. 이슈로 만들지 않은 범위

설계 문서가 이미 이후 범위로 정한 항목이다. 이번 구현에서도 지원하지 않는다.

| 범위 | 근거 |
|---|---|
| Windows 일반 지원, 원격 호스트 worker | ADR 23, 01 §1.1·§22 |
| OS sandbox·강한 프로세스 격리 | 01 §2, 03 §10.5 |
| 자동 headless Standalone runner | ADR 07, 01 §9.2 |
| 검증되지 않은 nested mutation subagent, background mutation | 01 §10.7(런타임은 background Bash를 거절) |
| `delivery_kind=ARTIFACT`(Git 통합 없는 이슈 완료) | 03 §4.5 |
| 저장소 간 원자적 통합 | GIT-12 |

## 4. 현재 구현을 쓸 때의 주의

- 기본 설정(`integration_authority: manual`)에서는 사용자가 외부에서 target에 반영한 것을 런타임이 관찰해 증거를 기록한다. 런타임이 직접 반영하려면 `samsara_local`과, 어떤 worktree에도 checkout되지 않은 target ref가 필요하다.
- 관리 worker는 Claude Code 버전이 기준 이상일 때만 생성된다. 기준 미만이면 attempt가 `POLICY_GATE_UNSUPPORTED_CLAUDE_VERSION`으로 대기한다.
- 실측 검증 상태는 `samsara doctor --write-manifest`가 만드는 compatibility manifest에 기록한다. 수동 검증 항목을 자동으로 true로 설정하지 않는다.
