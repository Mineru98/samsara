## 작업 요약

issue-merge 와 herdr-orch(Claude Code판·Codex판)가 비판 검토 block 을 같은 기준으로 세도록 문서 세 개를 고쳤습니다(기준 브랜치 `dev/0.4.0-alpha`).
block 을 **연속**(사용자 판단 트리거, 결정 뒤 0)과 **누적**(묶음 전체, 되돌리지 않음) 두 값으로 나누고, herdr-orch 는 현재 사다리 단계의 누적값으로 사다리를 올립니다.
브랜치 `docs/134-block-count-basis`, 커밋 `98df66b` + `8fa29b0`.

## 증거 생략 사유

문서만 바뀌어 화면 캡처·성능 측정이 의미가 없습니다. 대신 변경 전후 발췌와 diff(`after/diff.patch`, `origin/dev/0.4.0-alpha` 대비)를 `.issue/134/evidence/` 에 남겼습니다.

## 변경 전 → 후

| 위치 | 전 | 후 |
| --- | --- | --- |
| `skills/issue-merge/references/merge-plan.md` 6절 | "같은 계획으로 두 번 연속 `block` → 사용자 판단" 만 있음. 결정 뒤 다시 세는지 없음 | `### block 횟수` 소절 신설. 연속 block(마지막 사용자 결정·`revise`·`proceed` 이후, 2 에서 사용자 판단, 결정 뒤 0) / 누적 block(묶음 전체, 결정·tab 교체에도 이어 셈). 새 tab 은 누적을 잇고 연속은 0 에서 시작. `review.md` 회차 헤더와 질문·보고에 `block 누적 N회 / 연속 M회` 기록 |
| `skills/herdr-orch/SKILL.md` 6절 사다리 조건(216행) | "비판 검토 block 이 세 번째 나옴" | "비판 검토 block 누적이 현재 사다리 단계에서 3회가 됨" |
| `skills/herdr-orch/SKILL.md` 6절 "merge tab 진행" 의 issue-merge 5단계 질문 응답(245행~) | "세 번째 block 이 나오면 묻지 않고 다음 단계" | merge-plan.md 정의 참조, `block 누적 N회` 로 판단(연속값은 쓰지 않음). 질문이 없어도 기준을 넘기면 `agent prompt` 로 "중단". 단계를 올릴 때 새 tab 첫 프롬프트에 `block 기준점 N회` 를 적고 `누적 − 기준점 = 3` 에서 다시 올림. 표기가 없는 구버전은 `review.md` 의 block 회차를 직접 셈 |
| `skills/herdr-orch/references/codex.md` (203행, 230행~) | 위와 같은 옛 문구 | SKILL.md 와 같은 변경 |

## 이슈 시나리오 대조

toy-calculate `/issue-merge #2 #3 #5 #6`: 1·2차 block(누적 2 / 연속 2) → 사용자 판단 → "반영 후 재검토" → 3차 block 은 **누적 3회 / 연속 1회** 로 기록되고, herdr-orch 는 묻지 않고 "중단" 후 사다리 다음 단계로 올립니다. 새 단계의 첫 block 은 누적 4(기준점 3)라 곧바로 다시 올라가지 않습니다.

## 완료 기준

- [x] merge-plan.md 와 herdr-orch SKILL.md(·codex.md) 의 block 횟수 정의가 같다 (herdr-orch 가 merge-plan.md 6절 정의를 참조)
- [x] issue-merge 가 누적 block 횟수를 review.md 에 남긴다 (회차 헤더 형식 명시)
- [x] 사용자 결정을 끼운 3회 block 시나리오의 herdr-orch 처리가 문서로 정해졌다

## 검증

- 세 문서의 `block 누적` 용어·정의 grep 대조 일치. `skills/` 아래 "세 번째 block" 옛 문구 0건.
- 별도 검증 패스(neji-verifier)에서 Codex판 누락·질문 없는 3회째 처리·tab 교체 시 연속값 처리를 지적받아 `8fa29b0` 에 반영.
- 이 문서들을 참조하는 계약·스크립트·테스트 없음(`git grep` 확인). `dev/0.4.0-alpha` 에는 `build-phase-capability-bundle.mjs` 가 없어 폐쇄 검사는 해당 없음.
