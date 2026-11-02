## 작업 요약

`owner liveness is locale independent` 테스트가 한글 로캘(`ko_KR.UTF-8`)이 없는 환경에서 `assert.match(koStart, /년/)` 로 실패하던 문제를 고쳤습니다.
`startKey` 비교 단언을 먼저 실행한 뒤, 세션을 만들기 전에 실제 `ps -o lstart=` 를 ko 로캘로 돌려 `년` 이 없으면 `t.skip(이유)` 로 나머지 로캘 의존 단언만 건너뜁니다.
한글 로캘이 있으면 판별을 통과해 기존 검증이 그대로 진행됩니다.

## 변경 전후

화면 변경이 없는 테스트 코드 수정이라 캡처 대신 명령 출력을 증거로 남겼습니다. 원본은 `.issue/89/evidence/before/test.txt`, `.issue/89/evidence/after/test.txt` 입니다.

| 항목 | 전 | 후 |
| --- | --- | --- |
| `locale -a` 의 ko 로캘 | 없음 | 없음 |
| 대상 테스트 결과 | **fail 1** (`actual: 'Tue Nov  3 ... 2026'`) | **skipped 1**, 이유 출력 |
| skip 이유 | - | `ko_KR.UTF-8 locale unavailable: ps lstart rendered "Tue Nov  3 01:17:32 2026"` |
| `issue-gate.test.mjs` 전체 | - | tests 24 / pass 23 / fail 0 / skipped 1 |

## 변경 파일

- `skills/issue-start/scripts/issue-gate.test.mjs` — 로캘 판별 후 `t.skip` 분기 추가 (+5줄)

## 검증

- 한글 로캘 없는 환경: `node --test skills/issue-start/scripts/issue-gate.test.mjs` 실패 0, skip 이유 출력 확인
- `startKey` 비교 단언: skip 판별보다 앞에 있어 두 환경 모두 실행됨
- 한글 로캘 있는 환경: 미검증 (이 머신에 `ko_KR.utf8` 이 없음). `년` 이 포함되면 분기를 지나 기존 코드가 그대로 실행되는 구조

## 남은 이슈

- 없음
