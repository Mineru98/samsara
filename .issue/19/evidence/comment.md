## 작업 요약

issue #19의 `issue-onboard`가 live issue 목록을 읽거나 최종 추천을 출력하는 사이 `.issue/graph.json`이 바뀌면, 이전에 읽은 그래프로 추천할 수 있던 TOCTOU 결함을 수정했습니다.

최종 검증 기준 source commit: `b02b1c14dedee21faf65145632415647de496b00`

최종 graph reload·온톨로지/관계 검증·분류·추천 출력을 `.issue/graph.json.lock` 아래에서 수행하고, `saveGraph`와 issue-create/start/end/merge/onboard/sync의 `patchGraphNode`가 같은 exclusive sidecar lock을 사용합니다. 공식 writer가 잠금을 얻지 못하면 갱신하지 않으므로 지원되는 캐시 교체는 검증과 출력 사이에 끼어들 수 없습니다. 출력은 동기식 stdout 쓰기로 완료한 뒤 잠금을 해제합니다.

모든 공식 graph writer는 `.issue` 디렉터리와 `graph.json` 심볼릭 링크·하드링크를 거부하고, 최종 파일 읽기에도 `O_NOFOLLOW` descriptor를 사용합니다. `.issue` 부모 디렉터리와 그래프 inode를 검증하며, writer는 임시 파일을 검증된 부모 안에서 만든 뒤 대상 inode와 부모가 유지될 때만 원자 교체합니다. 검증 사이에 부모·대상·임시 파일이 바뀌면 fail-closed 하고 안전한 부모 기준으로 정리합니다.

## 재현 및 검증 결과

| 시나리오 | 결과 |
| --- | --- |
| live issue 목록 읽는 동안 malformed cache 변경 | `SYNC_FAILED=1`, 비정상 종료, 추천 출력 없음 |
| 최종 graph load 뒤 cache 변경 | `FINAL_CACHE_VALIDATION=changed-after-load EXIT=1`, 추천 출력 없음 |
| 최종 validation/output 구간의 cache 변경 | `OUTPUT_CACHE_VALIDATION=changed-during-emission EXIT=1`, 추천 출력 없음 |
| 최종 output 구간에 공식 `saveGraph` writer 실행 | writer `EXIT=1`, `OUTPUT_CACHE_LOCK=official-writer-blocked EXIT=0 RECOMMENDATION=stable`, graph.json 불변 |
| lock 보유 중 `saveGraph`/`patchGraphNode` 호출 | 각각 예외/false로 fail-closed |
| 여섯 공식 `patchGraphNode`의 `.issue`/`graph.json` 링크 입력 및 최종 open·교체 직전 링크 교체 | 모두 `false`, 외부 graph 불변 |
| 여섯 공식 `patchGraphNode`의 하드링크 입력 | 모두 `false`, 외부 graph와 cache 불변 |
| 여섯 공식 `patchGraphNode`의 최종 open 직전 `.issue` 부모 디렉터리 교체 | 모두 `false`, 외부 graph 불변 |
| `saveGraph` 최종 rename 직전 부모 디렉터리 교체 | 예외, 외부 graph/temp 불변 |

## 변경 파일

- `skills/issue-onboard/scripts/issue-onboard.mjs` — 최종 추천 구간 잠금, 안정성 검사, 동기식 출력
- `skills/issue-onboard/scripts/bootstrap.test.mjs` — 최종 cache boundary와 공식 writer 잠금 회귀 테스트
- `skills/issue-{onboard,create,start,end,merge,sync}/scripts/issue-common.mjs` — 모든 상태 전이 graph writer에 동일 잠금 적용
- `skills/issue-onboard/SKILL.md` — lock protocol과 stale lock 운영 규칙 문서화

## 검증

- `node --test skills/issue-onboard/scripts/*.test.mjs` — 55/55 passed
- `node --test tools/issue-ontology/ontology.test.mjs` — 13/13 passed
- 변경 JavaScript 8개 `node --check` — 모두 통과
- targeted cache/lock/path-integrity 회귀 테스트 — 12/12 passed
- `git diff --check origin/main...HEAD` — 통과

전체 결과: `.issue/19/evidence/after/test-suite.txt`, 경계별 결과: `.issue/19/evidence/after/final-cache-validation.txt`.
