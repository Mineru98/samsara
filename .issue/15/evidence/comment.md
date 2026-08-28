## 작업 요약

`$issue-onboard`가 GitHub의 열린 이슈와 그래프 캐시를 확인하도록 바꿨습니다. 캐시가 없거나
불완전·유효하지 않거나 최신 열린 이슈와 다르면 `$issue-sync`를 자동 실행하고, 완전한 snapshot을
다시 읽은 뒤 우선순위를 안내합니다.

## 변경 전후

- 전: `.issue/graph.json`이 존재하면 최신 GitHub 이슈가 캐시에 반영되지 않아 오래된 그래프로
  온보딩하거나 안전성 오류로 중단될 수 있었습니다.
- 후: 단독 온보딩 실행이 캐시 상태를 검사하고 필요한 경우 sync를 선행한 뒤, `complete` snapshot
  기반 결과만 출력합니다. sync 실패·partial 결과에서는 추천하지 않습니다.
- CLI 동작 변경이라 화면 캡처 대신 명령 원문과 출력 로그를 증거로 남겼습니다.

## 변경 파일

- `skills/issue-onboard/scripts/issue-onboard.mjs` — 캐시 상태·열린 이슈 revision 비교와 sync 부트스트랩
- `skills/issue-onboard/scripts/bootstrap.test.mjs` — 캐시 판정과 sync 실행 회귀 테스트
- `skills/issue-onboard/SKILL.md` — 자동 갱신 절차와 하드 규칙 문서화
- `skills/issue-onboard/references/dag-ops.md` — 자동 sync 동작 문서화
- `skills/issue-sync/SKILL.md` — 온보딩 자동 부트스트랩 연계 문서화

## 검증

- `node --test skills/issue-onboard/scripts/*.test.mjs` — 19개 통과
- `node tools/issue-ontology/ontology.test.mjs` — 13개 통과
- `node --check skills/issue-onboard/scripts/issue-onboard.mjs` — 통과
- `git diff --check` — 통과
- 캐시를 제거한 상태에서 `node skills/issue-onboard/scripts/issue-onboard.mjs` 수동 실행 —
  `GRAPH_BOOTSTRAP=issue-sync`, `SNAPSHOT_STATUS=complete`, `ONBOARD_COUNT=2` 확인
- malformed `.issue/graph.json` 상태에서 같은 명령을 수동 실행 — `GRAPH_BOOTSTRAP_REASON=invalid`와
  유효한 graph 재생성, `ONBOARD_COUNT=2` 확인

## 증거

- 변경 전: `.issue/15/evidence/before/baseline.txt`
- 변경 후: `.issue/15/evidence/after/onboard-bootstrap.txt`
- review 보완 후: `.issue/15/evidence/after/malformed-cache-recovery.txt`
- 텍스트 기반 CLI 변경이라 webp 캡처는 생략했습니다.

## 남은 이슈

없음.
