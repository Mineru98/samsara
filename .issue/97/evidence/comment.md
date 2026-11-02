## 작업 요약

issue-merge 회차 끝(close 다음, 정리·완료 브리핑 전)에 merge 된 이슈를 다시 조회해 `status:close` + CLOSED 로 맞추는 `close-check` 서브커맨드와 절차를 추가했습니다.
통합 테스트 실패로 보류한 이슈는 `--hold` 로 넘겨 손대지 않고, 보류인데 이미 닫혀 있으면 재오픈 경고를 붙입니다.
결과 요약(`확인 n건 / 보정 m건 / 보류 k건`)은 완료 브리핑 표의 `close 확인` 행에 그대로 들어갑니다.

## 변경 전후

| 전 | 후 |
| --- | --- |
| ![close-check 실행 결과 - 전](https://raw.githubusercontent.com/Mineru98/samsara/dev/0.4.0-alpha/.issue/97/evidence/before/01-close-check.webp) | ![close-check 실행 결과 - 후](https://raw.githubusercontent.com/Mineru98/samsara/dev/0.4.0-alpha/.issue/97/evidence/after/01-close-check.webp) |

빨간 박스가 비교 구간입니다. 전에는 명령이 없어 Usage 로 끝나고 문서에도 확인 절이 없었습니다.
후에는 가짜 `gh` 시나리오에서 `status:review` + OPEN 이던 #21 이 `status:close` + CLOSED 가 되고(결과 코멘트 1건), 정상인 #53 은 쓰기 없이 확인, 보류 #64 는 그대로입니다.
화면 변경이 없는 CLI·문서 작업이라 터미널 출력을 렌더링해 캡처했습니다. 원본은 `.issue/97/evidence/{before,after}/01-close-check.txt` 입니다.

## 변경 파일

- `skills/issue-merge/scripts/issue-merge.mjs` — `close-check <n...> [--hold] [--comment-dir] [--fix]` 추가. `--fix` 없으면 조회만 하고 보정 필요 시 exit 1. close 뒤에 코멘트를 달아 재실행해도 코멘트가 겹치지 않음
- `skills/issue-merge/SKILL.md` — 하드 규칙 1줄, 흐름도, 6~7단계 설명, 마무리 표 `close 확인` 행
- `skills/issue-merge/references/verify-and-close.md` — 5절에 "close 확인과 보정" 하위 절
- `skills/issue-merge/references/ask.md` — 6절 표 골격에 `close 확인` 행
- `scripts/issue-merge-close-check.test.mjs` — 가짜 `gh` 로 재현하는 테스트 5건

## 검증

- `node --test scripts/*.test.mjs` 34/34 통과 (구현을 빼면 새 테스트가 실패하는 것도 확인)
- `sh scripts/check-shared.sh` 통과, `node --check skills/issue-merge/scripts/issue-merge.mjs` 통과
- 완료 기준 재현: `status:review` + OPEN → `status:close` + CLOSED, 보류 이슈 불변 (위 after 이미지와 테스트)
- 실제 GitHub 이슈로는 실행하지 않았습니다. 트래커 호출은 가짜 `gh` 로만 검증했습니다.

## 남은 이슈

- `node --test scripts/` (디렉터리 지정)는 Node 24 에서 이번 변경과 무관하게 실패합니다. 파일 글롭(`scripts/*.test.mjs`)으로는 정상입니다.
- `contracts/issue-phase-capability-bundle-v1.json` 의 closure 해시는 이번 변경 전부터 `SKILL.md`·`issue-merge.mjs` 와 이미 어긋나 있었고, 이번에 갱신하지 않았습니다.
