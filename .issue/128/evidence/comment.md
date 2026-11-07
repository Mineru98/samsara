## 작업 요약

`itachi-merge-critic` 지침과 issue-merge 4~5단계 설명에 같은 기준 블록 세 개를 넣었습니다. 첫째는 **blocking 문턱**입니다. 그대로 실행하면 되돌릴 수 없는 잘못된 결과(잘못된 merge·close·push, 데이터 손실)로 이어지는 것만 blocking 으로 봅니다. 사전 검증이 실패로 드러내고 멈추는 실행 세부는 warnings 입니다. 둘째는 **위험도별 검토 범위**로, PR 수·overlap·충돌 해소·보정 push 에 따라 낮음·보통·높음으로 나눕니다. 셋째는 **재검토 한정**입니다. 2차부터는 직전 지적의 반영 여부와 그 수정이 만든 새 위험만 봅니다.
"되돌릴 수 없는 단계는 검증 뒤" 규칙은 그대로 무조건 blocking 이고, 재검토에서도 예외로 남겼습니다.

## 변경 전후

화면이 없는 지침·문서 변경이라 캡처는 생략했습니다. 대신 재현 조건(toy-calculate #1 #4, PR 2개, overlap 0, 충돌 해소·보정 push 없음)을 본뜬 계획을 opus critic 에 돌린 판정을 남깁니다. critic 은 지침 파일만 기준으로 삼았고 저장소와 GitHub 는 건드리지 않았습니다.

| 회차 | 지침 | 계획 | verdict | blocking | warnings |
| --- | --- | --- | --- | ---: | ---: |
| 1차 | 변경 전 | 재현 rev 1 | block | 2 | 9 |
| 1차 | 변경 후 | 재현 rev 1 | block | 2 | 4 |
| 2차 | 변경 후 | rev 2 (불완전 수정) | block | 1 | 3 |
| 2차 | 변경 후 | rev 2b (지적 반영) | **revise** | **0** | 9 |
| 1차 | 변경 후 | 반례 (merge·close 가 통합 테스트 앞) | **block** | 2 | 3 |

- 변경 후 1차 blocking 2건은 새 문턱에 정확히 걸리는 유형입니다. 하나는 grep 판정이 거꾸로 돼 실패를 통과로 보고하는 문제(거짓 통과 → merge)이고, 다른 하나는 squash 메시지로 이슈가 자동으로 닫혀 통합 테스트보다 먼저 close 되는 문제입니다. 경로·cwd, vite PID, 상대 경로 같은 실행 세부는 모두 warnings 로 내려갔습니다.
- 2차 rev 2 의 block 은 제가 만든 수정이 불완전했던 탓입니다(닫기 키워드 9종 중 3종만 검사, `--body ""`). 이때 critic 은 직전 지적의 반영 여부만 봤고, 이미 통과한 영역은 다시 파지 않았습니다. 지적을 제대로 반영한 rev 2b 는 2회 안에 `revise` 로 수렴했습니다.
- 반례 계획은 변경 후 지침에서도 `block` 입니다. blocking 2건 모두 "되돌릴 수 없는 단계가 검증 앞" 유형입니다.

원본: [before/state.txt](https://github.com/Mineru98/samsara/blob/dev/0.4.0-alpha/.issue/128/evidence/before/state.txt) · [after/state.txt](https://github.com/Mineru98/samsara/blob/dev/0.4.0-alpha/.issue/128/evidence/after/state.txt) · [변경 전 판정](https://github.com/Mineru98/samsara/blob/dev/0.4.0-alpha/.issue/128/evidence/before/critic-runs/repro-r1.json) · [변경 후 1차](https://github.com/Mineru98/samsara/blob/dev/0.4.0-alpha/.issue/128/evidence/after/critic-runs/repro-r1.json) · [2차 rev 2](https://github.com/Mineru98/samsara/blob/dev/0.4.0-alpha/.issue/128/evidence/after/critic-runs/repro-r2-rev2.json) · [2차 rev 2b](https://github.com/Mineru98/samsara/blob/dev/0.4.0-alpha/.issue/128/evidence/after/critic-runs/repro-r2-rev2b.json) · [반례](https://github.com/Mineru98/samsara/blob/dev/0.4.0-alpha/.issue/128/evidence/after/critic-runs/counter-r1.json)

## 변경 파일

- `agents/itachi-merge-critic.md` — 기준 블록 추가, 출력 규칙·판단 원칙을 문턱에 연결
- `agents/itachi-merge-critic.toml` — 위와 같은 문구 (`developer_instructions`)
- `skills/issue-merge/SKILL.md` — 4~5단계에 `비판 검토` 절과 같은 기준 블록 추가
- `skills/issue-merge/references/merge-plan.md` — critic 프롬프트에 묶음 위험도와 직전 review.md 추가

## 검증

- 기준 블록이 md / toml `developer_instructions` / SKILL.md 세 곳에 바이트 단위로 같게 들어 있는지 확인했습니다(tomllib 로 파싱해 비교).
- `sh scripts/check-shared.sh` 와 `node --test scripts/*.test.mjs`(3개) 모두 통과했습니다.

## 남은 이슈

- 실제 toy-calculate 의 rev 1~3 계획 원본은 남아 있지 않아(현재 rev 6), 재현 조건을 본뜬 계획으로 검증했습니다.
- md 에만 있는 `주술(페르소나 연출)` 절은 원래부터 toml 에 없었습니다. 이번 범위 밖이라 그대로 두었습니다.
- `skills/issue-merge/contracts/issue-phase-capability-bundle-v1.json` 의 closure 해시는 이 브랜치에서 이미 현재 파일과 맞지 않았습니다(재생성 스크립트 없음). 이번 변경에서는 손대지 않았습니다.
