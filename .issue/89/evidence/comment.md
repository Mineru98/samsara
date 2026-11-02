## 작업 요약 (2차 — merge 검증 보류 대응)

1차 수정은 한글 로캘 판별을 `ps` 출력에 `년` 이 있는지로 했습니다. 그런데 Linux 의 procps-ng(4.0.4)는 한글 로캘에서 `lstart` 를 `화 11월  3 03:09:34 2026` 처럼 찍어 `년` 이 없습니다. 그래서 한글 로캘이 실제로 쓰이는 환경에서도 검증이 skip 됐습니다.

판별을 **한글 음절이 하나라도 있는지**(`/[가-힣]/`)로 바꿨습니다. skip 을 풀자 그동안 가려져 있던 결함이 하나 드러나 함께 고쳤습니다.

- 결함: `startKey`(`skills/issue-start/scripts/issue-gate.mjs`)가 연도가 맨 앞인 BSD/macOS 형식(`2026년 10월 29일 …`)만 해석하고, 연도가 맨 뒤인 procps 한글 형식은 해석하지 못했습니다. 그 결과 예전에 한글로 기록된 잠금을 가진 세션이 `complete` 에서 `NOT_OWNER` 로 거절됐습니다.
- 수정: 숫자 6개 중 마지막이 4자리 연도인 형식도 같은 키로 정규화합니다.

## 변경 전후

화면이 없는 테스트·스크립트 수정이라 캡처 대신 명령 출력을 증거로 남겼습니다. 한글 로캘 환경은 시스템에 설치하지 않고 `localedef -i ko_KR -f UTF-8` 로 만든 로캘을 `LOCPATH` 로 지정해 재현했습니다. 원본은 `.issue/89/evidence/before/test-ko-locale.txt`, `.issue/89/evidence/after/test-ko-locale.txt` 입니다.

| 항목 | 전 | 후 |
| --- | --- | --- |
| ko 로캘 `ps -o lstart=` | `화 11월  3 03:10:00 2026` | `화 11월  3 03:13:54 2026` |
| 대상 테스트 (ko 로캘 사용) | **skipped 1** — 한글이 나왔는데 `locale unavailable` | **pass 1**, skip 0 |
| 대상 테스트 (ko 로캘 없음) | skipped 1, 사유 출력 | skipped 1, 사유 출력 (유지) |
| 전체 `issue-gate.test.mjs` (ko 로캘 사용) | - | tests 24 / pass 24 / fail 0 / skipped 0 |
| 전체 `issue-gate.test.mjs` (ko 로캘 없음) | - | tests 24 / pass 23 / fail 0 / skipped 1 |

판별만 고친 중간 단계에서는 ko 로캘 환경에서 `complete` 가 `NOT_OWNER`(exit 5)로 실패했고, `startKey` 수정 후 통과했습니다.

## 변경 파일

- `skills/issue-start/scripts/issue-gate.test.mjs` — 판별·`koStart` 단언을 한글 음절 기준으로 변경, procps 형식 `startKey` 단언 추가(로캘 무관하게 항상 실행)
- `skills/issue-start/scripts/issue-gate.mjs` — `startKey` 에 procps locale 형식(연도 맨 뒤) 해석 추가 (+2줄)

## 검증

- 한글 로캘 사용 환경: 로캘 의존 검증이 skip 없이 실행되어 통과
- 한글 로캘 없는 환경: 해당 부분만 사유와 함께 skip, 나머지 통과
- `startKey` 비교 단언: skip 판별보다 앞에 있어 두 환경 모두 실행됨

## 남은 이슈

- 없음
