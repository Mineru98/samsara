## 작업 요약

`$issue-onboard`가 GitHub의 열린 이슈와 그래프 캐시를 확인하도록 바꿨습니다. 캐시가 없거나
불완전·유효하지 않거나 최신 열린 이슈와 다르면 `$issue-sync`를 자동 실행하고, 완전한 live
snapshot을 다시 검증한 뒤 우선순위를 안내합니다.

## 변경 전후

- 전: `.issue/graph.json`이 존재하면 최신 GitHub 이슈가 캐시에 반영되지 않아 오래된 그래프로
  온보딩하거나 안전성 오류로 중단될 수 있었습니다.
- 후: 단독 온보딩 실행이 캐시 상태를 검사하고 필요한 경우 sync를 선행한 뒤, 현재 열린 이슈와
  일치하고 온톨로지·관계 출처·무결성 digest를 통과한 `complete` snapshot 기반 결과만 출력합니다.
  sync 실패·partial·stale 결과에서는 추천하지 않습니다.
- 프로젝트·사용자 flavor 스킬 탐색은 유지하되, 현재 설치 묶음 밖의 자동 sync 스크립트에는
  provider credential 환경 변수를 전달하지 않습니다. bare repository fallback과 production
  test override도 거부합니다.
- CLI 동작 변경이라 화면 캡처 대신 명령 원문과 출력 로그를 증거로 남겼습니다.

## 변경 파일 (현재 PR 기준 18개)

- `skills/issue-onboard/scripts/issue-onboard.mjs` — 캐시 상태·열린 이슈 revision 비교·sync 부트스트랩
- `skills/issue-onboard/scripts/issue-common.mjs` — 스킬 탐색 필터와 trusted executable 경계
- `skills/issue-onboard/scripts/issue-tracker.mjs` — 고정된 실행 파일과 제한된 command 환경
- `skills/issue-onboard/scripts/issue-native-deps.mjs` — GitHub native dependency 실행 경계
- `skills/issue-onboard/scripts/issue-llm.mjs` — 자동 그래프 보강 경계
- `skills/issue-onboard/scripts/bootstrap.test.mjs` — 캐시·탐색·credential·실행 경계 회귀 테스트
- `skills/issue-onboard/scripts/link-roundtrip.test.mjs` 및 `native-deps.test.mjs` — 연계 회귀 보강
- `skills/issue-onboard/SKILL.md`, `references/dag-ops.md`, `references/graph-v2.md` — 사용자 계약 문서
- `skills/issue-sync/scripts/issue-sync.mjs`, `scripts/issue-common.mjs` — sync 실행 파일·환경 경계
- `skills/issue-sync/SKILL.md` — 온보딩 자동 부트스트랩 연계 문서
- `tools/issue-ontology/schemas/graph-v2.schema.json` — snapshot/관계 스키마
- `tools/issue-ontology/ontology.test.mjs` 및 두 fixture — 스키마·온톨로지 회귀 검증

## 검증

- 검증 기준 SHA: `dad84badb53a286423cf434c8ebb8cdd9a22cbac`
- `node --test skills/issue-onboard/scripts/*.test.mjs` — 42/42 통과
- `node --test tools/issue-ontology/ontology.test.mjs` — 13/13 통과
- 변경된 JS 전부 `node --check` — 통과
- `git diff --check dad84bad^ dad84bad` — 통과
- 런타임 감사 — `RUNTIME_AUDIT=pass`, untrusted resolver skip, bootstrap environment 경계,
  trusted executable 경계 확인
- 캐시를 제거한 상태에서 `node skills/issue-onboard/scripts/issue-onboard.mjs` 수동 실행 —
  `GRAPH_BOOTSTRAP=issue-sync`, `SNAPSHOT_STATUS=complete`, `ONBOARD_COUNT` 확인
- malformed `.issue/graph.json` 상태에서 같은 명령을 수동 실행 — `GRAPH_BOOTSTRAP_REASON=invalid`와
- project `.codex/skills` split 설치 — sync 실행과 `fake-project-token` 비전달 확인
- partial·failed·stale·ontology unavailable·bare repository fallback — 추천 없이 fail-closed 확인

## 증거

- 변경 전: `.issue/15/evidence/before/baseline.txt`
- 변경 후: `.issue/15/evidence/after/onboard-bootstrap.txt`
- review 보완 후: `.issue/15/evidence/after/malformed-cache-recovery.txt`
- 텍스트 기반 CLI 변경이라 webp 캡처는 생략했습니다.

## 리뷰

- 최종 exact-SHA 리뷰와 debugging runtime audit 결과는 PR handoff 시
  `.omo/evidence/issue-15-review-ledger.md`에 전체 SHA와 함께 기록합니다.
- 현재 검증 기준 SHA에서 goal / code / security / context / manual QA 및
  runtime audit 대상 경계를 확인할 수 있습니다.

## 남은 이슈

없음.
