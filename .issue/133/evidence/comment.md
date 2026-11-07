## 작업 요약

issue-merge 다음 행동의 "다른 이슈 착수" 재료가 `gh issue list` 그대로라 선행이 남은 이슈도 착수 후보로 소개됐습니다.
`issue-merge.mjs ready` 를 추가해 열린 이슈마다 `## 선행 이슈` 를 실제로 조회하고(CLOSED 또는 `status:close` 만 완료, 조회 실패는 미완료 — `issue-start prereq` 와 같은 기준) 착수 가능(`ready`)과 대기(`waiting`, 남은 선행 번호 포함)로 나눕니다.
`next-actions.md` 는 이 출력으로 착수 목록을 만들고, 대기 이슈는 `대기(선행 #N)` 로 따로 적도록 바꿨습니다. 기준 브랜치는 `dev/0.4.0-alpha` 입니다.

## 변경 전후

이미지 대신 같은 fixture(가짜 gh — 열린 #1 선행 없음, #5, #7 선행 #5 열림, #8 선행 #4 닫힘 / 닫힌 #4)로 돌린 출력을 남깁니다. 화면이 없는 CLI·문서 변경이라 캡처는 생략했습니다.

| | 전 | 후 |
| --- | --- | --- |
| 착수 후보 재료 | `gh issue list` → #1 #5 #7 #8 전부 (#7 포함) | `ready` → #1 #5 #8 |
| 선행 대기 구분 | 없음 (`ready` 명령 없음, exit 1) | `waiting` → #7 (`pending: [5]`) |

원본: [before/ready.txt](https://raw.githubusercontent.com/Mineru98/samsara/main/.issue/133/evidence/before/ready.txt) · [after/ready.txt](https://raw.githubusercontent.com/Mineru98/samsara/main/.issue/133/evidence/after/ready.txt) · [after/tests.txt](https://raw.githubusercontent.com/Mineru98/samsara/main/.issue/133/evidence/after/tests.txt)

## 변경 파일

- `skills/issue-merge/scripts/issue-merge.mjs` — `ready [--limit <n>]` 서브커맨드 추가 (Jira 는 목록에 본문이 없어 이슈별 재조회)
- `skills/issue-merge/references/next-actions.md` — 재료 수집을 `ready` 로, 1번 착수 목록은 `ready` 만, 대기는 `대기(선행 #N)` 로 표기
- `scripts/issue-merge-ready.test.mjs` — 완료 기준 2건 fixture 테스트

## 검증

- `node --test scripts/issue-merge-ready.test.mjs` — 2/2 통과 (선행 열림 → waiting, `[x]` 체크여도 실제 열림이면 대기, 조회 실패도 대기 / 선행 완료·없음 → ready)
- `node --test scripts/*.test.mjs` — 42/42 통과
- `sh scripts/check-shared.sh` — 통과 (공용 파일 미변경)

## 남은 이슈

- `skills/issue-merge/contracts/issue-phase-capability-bundle-v1.json` 의 `issue-merge.mjs` 해시는 dev 기준에서 이미 실제 파일과 달라 이번에 갱신하지 않았습니다.
- main 에는 #138 의 `unblocked --issue <n>` 가 따로 있어, dev → main 통합 때 두 안내를 합쳐야 합니다.
