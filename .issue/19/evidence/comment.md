## 작업 요약

`issue-onboard`가 live issue 목록을 읽는 동안 `.issue/graph.json`이 바뀌어도, 이전에 메모리에 적재한 정상 그래프로 추천을 계속하던 P1 결함을 수정했습니다.

live snapshot 직후 그래프 캐시를 다시 읽고 검증하며, 캐시가 깨졌거나 동기화가 실패하면 `ONBOARD_COUNT`와 `PRIORITY`를 출력하지 않고 종료합니다.

## 재현 결과

| 시나리오 | 수정 전 | 수정 후 |
| --- | --- | --- |
| `gh issue list` 직후 그래프를 `{ TOCTOU invalid`로 변경 | `CLI_EXIT=0`, `ONBOARD_COUNT=1`, `PRIORITY=#19` | `SYNC_FAILED=1`, `CLI_EXIT=7`, 추천 출력 없음 |

수정 전 원본: `.issue/19/evidence/before/toctou-cli.txt`

수정 후 동일 시나리오: `.issue/19/evidence/after/toctou-cli.txt`

수정 전 회귀 테스트 실패 원본: `.issue/19/evidence/before/regression-red.txt` (`REGRESSION_TEST_EXIT=1`)

정상 캐시 확인 원본: `.issue/19/evidence/after/normal-cli.txt` (`CLI_EXIT=0`, `POST_GRAPH=valid`)

CLI/백엔드 검증 경계의 결함이라 화면 캡처는 생략했습니다. 동일한 fixture와 명령을 사용해 상태·출력·종료 코드를 비교했습니다.

## 변경 파일

- `skills/issue-onboard/scripts/issue-onboard.mjs` — live snapshot 이후 그래프 캐시 재로드 및 post-sync 재검증
- `skills/issue-onboard/scripts/bootstrap.test.mjs` — 동시 캐시 변경 회귀 fixture와 실패 우선 테스트

## 검증

- `node --test skills/issue-onboard/scripts/*.test.mjs` — 44 passed
- `node --test tools/issue-ontology/ontology.test.mjs` — 13 passed
- `node --check skills/issue-onboard/scripts/issue-onboard.mjs` — `CHECK_issue-onboard-syntax=pass`
- `node --check skills/issue-onboard/scripts/bootstrap.test.mjs` — `CHECK_bootstrap-test-syntax=pass`

전체 원본 출력은 `.issue/19/evidence/after/test-suite.txt`에 보존했습니다.
