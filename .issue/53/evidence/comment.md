## 작업 요약

issue-create 초안 승인 선택지를 `등록 / 등록 없이 진행 / 수정 / 취소` 네 가지로 늘렸습니다.
`등록 없이 진행`은 이슈 없이 원래 변경 요청을 계속하고, `취소`는 원래 요청도 멈춥니다.

## 증거 형식

문서(스킬 지침)만 바뀐 작업이라 화면 캡처·측정은 의미가 없어 생략했습니다. 대신 변경 전후 선택지 정의를 `grep` 원본으로 남겼습니다.

- 전: `.issue/53/evidence/before/options.txt`
- 후: `.issue/53/evidence/after/options.txt`

## 변경 전후

| 전 | 후 |
| --- | --- |
| 승인 · 수정 · 취소 (취소 시 원래 요청 계속 여부를 다시 확인) | 등록 · 등록 없이 진행 · 수정 · 취소 |
| 흐름도 `H -- 승인 / 일부 수정 / 취소` | 흐름도에 `H -- 등록 없이 진행 --> Z1(조용히 종료 · 원래 요청 계속)` 추가 |

## 변경 파일

- `skills/issue-create/references/issue-draft.md` — 승인 절 4지선다와 `등록 없이 진행` 동작(설정 불변, request.md·7·8단계 생략)
- `skills/issue-create/SKILL.md` — hard-rule 한 줄, 흐름도 분기, 5단계 설명

## 검증

- `grep -n "등록 없이 진행"` — 두 파일의 선택지 이름 일치
- `grep -n "일괄 승인 / 일부 수정\|H -- 승인\|H -- 일부 수정"` — 구 표기 잔존 없음
- `sh scripts/check-shared.sh` — `shared cache-safety block: 6 copies match`

## 남은 이슈

- 분할안 승인 단계(승인 / 병합 / 분리 / 취소)에는 `등록 없이 진행`을 넣지 않았습니다. 범위 밖.
