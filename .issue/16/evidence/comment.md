## 작업 요약

`issue-board` 스킬의 공개 명칭과 디렉터리를 `issue-viz`로 변경하고, 실행 파일·호출 예시·내부 주석을 새 명칭으로 통일했습니다. README에는 현재 9개 스킬 목록과 `issue-viz`의 역할·실행법을 반영했습니다. 과거 `.issue/11`, `.issue/13` 증거 기록은 변경하지 않았습니다.

## 변경 전후

| 전 | 후 |
| --- | --- |
| ![이슈 시각화 화면 - 전](https://raw.githubusercontent.com/Mineru98/samsara/main/.issue/16/evidence/before/board-desktop.webp) | ![이슈 시각화 화면 - 후](https://raw.githubusercontent.com/Mineru98/samsara/main/.issue/16/evidence/after/board-desktop.webp) |

두 캡처는 동일한 1440x900 뷰포트와 `.topbar` 박스를 사용했습니다. 이번 변경은 렌더링 UI가 아니라 스킬 명칭·경로·문서만 바꾸므로 화면은 의도적으로 동일하며, 이미지 비교 결과는 100/100 similarity, 0 pixels differ입니다.

## 변경 파일

- `skills/issue-viz/SKILL.md` — 스킬 메타데이터, 호출법, 실행 예시를 `issue-viz`로 변경
- `skills/issue-viz/scripts/issue-viz.mjs` — 실행 파일명과 경로 주석 변경
- `skills/issue-viz/assets/board.html` — 내부 스킬명 주석 변경
- `README.md` — `issue-viz` 설명·호출법·9개 스킬 목록·흐름 반영

## 검증

- `node --check skills/issue-viz/scripts/issue-viz.mjs` 통과
- `node --test tools/issue-ontology/ontology.test.mjs` 13/13 통과
- `node skills/issue-viz/scripts/issue-viz.mjs --no-open`에서 `BOARD=ok` 확인
- 활성 `skills/`와 README에 `issue-board` 잔여 참조 없음, 플러그인 매니페스트 JSON 파싱 통과
- `.issue/11/evidence`, `.issue/13/evidence`의 역사적 `issue-board` 기록 보존 확인
- 두 read-only 게이트 리뷰 `PASS`, `git diff --check` 통과

## 남은 이슈

- 없음. 기존 화면의 우측 하단 범례와 마지막 카드 겹침은 변경 전에도 동일한 레이아웃으로 이번 이슈 범위에서 유지했습니다.
