## 작업 요약

`issue-merge` 충돌 해소에서 **사람 결정을 기다리는 상태**를 해소 실패와 분리했습니다. 한쪽에만 있는 경로(`deleted-by-*`, `added-by-*`, `both-deleted`) 때문에 `--continue` 가 거부되면 더 이상 사다리를 올리지 않고 사용자에게 묻습니다. 받은 `git add`/`git rm` 결정은 `--continue` 바로 앞에서 적용하므로 `--abort` 에 지워지지 않습니다. Claude 재시도도 Codex 와 같은 절차(abort → 다시 깔기 → 이전 출력·해소 diff·실패 사유 전달)를 따른다고 적었습니다.

문서만 바뀌고 화면·API 변경이 없어 캡처는 생략했습니다. 변경 근거는 변경 전후 문구 발췌와 테스트 출력으로 남깁니다.

## 변경 전후

| 항목 | 전 | 후 |
| --- | --- | --- |
| 실패 조건 | "`--continue` 가 남은 충돌 때문에 커밋을 거부했다" (결정 대기도 실패) | "마커나 생성물이 남아서 거부" 로 좁힘. `remaining` 에 `hunks`·`unreadable` 이 있거나 한쪽 경로가 아닌 kind 가 있을 때만 |
| 결정 대기 | 사다리가 오르고 `--abort` 가 먼저 돎 | abort 없음, 사다리 유지, `ask.md` 3절 형식으로 질문 후 적용·`--continue` 재실행 |
| 마커·한쪽 경로 혼재 | 규칙 없음 | 마커 쪽만 실패로 세고, 한쪽 경로는 마커가 풀린 뒤 질문 |
| `git add`/`git rm` 적용 시점 | 명시 없음 (받는 즉시 적용 가능 → abort 에 지워짐) | diff 승인 때 함께 묻고 `plan.md` 에 기록, `--continue` 직전에 적용. abort 뒤 다시 깔면 기록에서 재적용 |
| Claude 재시도 | "최대 2회" 만 | 첫 시도 1단계, 재시도 2·3단계. abort 후 이전 결과 전달을 명시. resolver 지침의 "2단계 이상으로 다시 불렸으면" 과 용어 일치 |
| 흐름도 R7 | resolver 다음 단계 · 이전 결과 전달 | `resolve --abort` 를 단계에 명시 |

원본: `.issue/153/evidence/before/`, `.issue/153/evidence/after/`

## 변경 파일

- `skills/issue-merge/references/merge-plan.md`. 5절 kind 표, "결과를 보여주고 승인받는다", "다시 확인한다", 상한 표.
- `skills/issue-merge/SKILL.md`. 재시도 hard-rule, "Codex resolver 사다리" 절 끝의 Claude 문단, 흐름도 R7.
- `agents/yamato-merge-resolver.md`, `.toml` 은 수정하지 않았습니다. 문서가 Claude 에도 "단계" 용어를 쓰도록 맞췄습니다.

## 검증

- `node --test skills/issue-merge/scripts/*.test.mjs` — 변경 전후 모두 10/10 통과 (`before/03-tests.txt`, `after/03-tests.txt`).
- 변경 문구를 `--continue` 거부 로직(`skills/issue-merge/scripts/issue-merge.mjs` 414-424행, `remaining[].kind/hunks/unreadable`)과 대조했습니다.
- 별도 리뷰 레인에서 완료 기준 4개 PASS, 지적 사항 반영.

## 남은 이슈

- `SKILL.md:52`, `verify-and-close.md:68` 의 "Codex 사다리 5단계" 는 총 시도 횟수, "Claude 최대 2회" 는 재시도 횟수라 단위가 다릅니다. `verify-and-close.md:69` 의 "사다리는 직전 단계의 다음부터" 도 Claude 에는 사다리가 없습니다. 이번 범위 밖이라 그대로 두었습니다.
