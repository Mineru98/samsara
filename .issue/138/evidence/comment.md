## 작업 요약

`.issue/graph.json` 그래프 캐시와 이를 만들고 읽던 `issue-onboard`·`issue-viz`·`issue-sync` 스킬을 제거했습니다. 그래프에 기대던 판단은 GitHub 이슈를 직접 조회하는 방식으로 바꿨습니다. 기준 브랜치는 `main`(0.3.x)입니다.

화면이나 API 변경이 없는 스크립트·문서 정리라 캡처와 성능 측정은 생략했습니다. 변경 근거는 아래 grep·테스트 결과로 남깁니다.

## 변경 전후

| 항목 | 전 | 후 |
| --- | --- | --- |
| `skills/issue-*` | 8개 (onboard·sync·viz 포함) | 5개 (create·start·end·merge·version) |
| 그래프 참조 grep (세 스킬 밖) | 166줄 | **0줄** |
| `sh scripts/check-shared.sh` | `6 copies match` + Git 탐지 2벌 | `shared block: 4 copies match` |
| ontology + check-shared (+ unblocked) 테스트 | 23건 통과 (그래프 스키마 테스트 포함) | 16건 통과 (그래프 테스트 제거, ready 판정 2건·unblocked 6건 추가) |
| merge guard | `graphPresent: true` 필수 → graph.json 이 없으면 실패 | `graphPresent` 없이 통과 |
| issue-start 착수 판정 | graph.json 이 있을 때만 `issue-onboard plan` 으로 판정 | 본문 `## 선행 이슈` 를 `gh` 로 하나씩 조회해 판정 |
| close 로 풀린 이슈 안내 | `issue-onboard plan` (그래프 필요) | `issue-merge.mjs unblocked --issue <n>` 이 gh 검색 + 선행 조회로 계산 |

원본: `.issue/138/evidence/before/`, `.issue/138/evidence/after/`

## 변경 파일

- `skills/issue-onboard/`, `skills/issue-viz/`, `skills/issue-sync/`, `design/board-mockup.html`, `.issue/graph.json` — 삭제
- `skills/{issue-create,issue-start,issue-end,issue-merge}/scripts/issue-common.mjs` — 그래프 캐시 잠금·patch 블록, `GRAPH_FILE_NAME`, ignore 예외 제거. 공유 블록 표식을 `resolveStatus` 로 변경
- `skills/{issue-create,issue-start,issue-end,issue-merge}/scripts/issue-tracker.mjs` — `setTrackerStatus` 의 그래프 patch 제거
- `scripts/check-shared.sh`, `scripts/check-shared.test.mjs` — 4개 사본 기준으로 정리, onboard·sync 전용 Git 탐지 검사 제거
- `skills/issue-start/scripts/issue-start.mjs` — `readyFact` 를 gh 조회로 대체, `predecessorNumbers` 추가
- `skills/issue-merge/scripts/issue-merge.mjs`, `tools/issue-ontology/schemas/actions/merge.schema.json` — `graphPresent` 제거
- `skills/issue-merge/scripts/issue-merge.mjs` — `unblocked --issue <n>` 명령 추가. `skills/issue-merge/references/next-actions.md` 가 이 명령을 안내
- `skills/*/scripts/issue-common.mjs` 공유 블록 — `predecessorNumbers`·`isIssueDone`·`unblockedSuccessors` 추가 (issue-start `readyFact` 도 사용)
- `skills/issue-merge/scripts/issue-unblocked.test.mjs` — 신규 테스트
- `tools/issue-ontology/` — `graph-v2.schema.json`, graph 픽스처, `validateGraphDocument` 제거
- `README.md`, `skills/issue-create/references/create-and-handoff.md`, `.codex-plugin/plugin.json`, `.gitignore` — 그래프·세 스킬 언급 정리

## 검증

- `sh scripts/check-shared.sh` 통과
- `node --test scripts/check-shared.test.mjs tools/issue-ontology/ontology.test.mjs skills/issue-merge/scripts/issue-unblocked.test.mjs skills/issue-version/scripts/issue-version.test.mjs` 72건 통과
- 실제 GitHub 에서 `issue-merge.mjs unblocked --issue 137` 실행: #138 은 `## 참고` 에서만 #137 을 언급해 후보에서 제외됨 (`after/06-unblocked.txt`)
- `skills/issue-*/scripts/*.mjs`, `tools/issue-ontology/*.mjs`, `scripts/*.mjs` 전부 `node --check` 통과
- 워크트리에서 `issue-start.mjs fetch 138`, `issue-create.mjs mode`, `issue-merge.mjs` 실행 확인

## 남은 이슈

- `issue-sync` 라는 문자열은 stash 메시지 표식으로만 남았습니다(`evidence-capture.md`, `report-and-pr.md`, `herdr-orch`). 옛 설치와 같은 stash 를 알아보기 위한 이름이라 그대로 뒀습니다.
- #137 은 이 작업으로 대체됩니다. merge 때 함께 정리합니다.
- `dev/0.4.0-alpha` 반영은 범위 밖입니다.
