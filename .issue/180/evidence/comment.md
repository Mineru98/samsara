## 작업 요약

`gh-setup`, `github-issue-pr-convention` 두 스킬의 프론트매터에 `user-invocable: false` 를 추가해 `/` 메뉴에서 숨겼습니다.
`issue-review` 와 같은 위치(description 다음 줄)에 두었고, 모델의 Skill 호출 경로는 그대로입니다.

## 변경 전후

문서(프론트매터)만 바뀐 이슈라 화면 캡처·수치 측정은 생략하고, 프론트매터 원문과 파싱 결과를 텍스트 증거로 남겼습니다.

| 파일 | 전 | 후 |
| --- | --- | --- |
| `skills/gh-setup/SKILL.md` | `user-invocable` 없음 (기본 true) | `user-invocable: false` |
| `skills/github-issue-pr-convention/SKILL.md` | `user-invocable` 없음 (기본 true) | `user-invocable: false` |

원본: `.issue/180/evidence/before/frontmatter.txt`, `.issue/180/evidence/after/frontmatter.txt`

## 변경 파일

- `skills/gh-setup/SKILL.md` — 프론트매터 +1줄
- `skills/github-issue-pr-convention/SKILL.md` — 프론트매터 +1줄

## 검증

- 두 파일 프론트매터 파싱: 키 `name,description,user-invocable`, `user-invocable=false`
- `git show --name-only HEAD`: 위 두 파일만 변경 (각 +1, -0)
- `node scripts/build-phase-capability-bundle.mjs --check`: closure 41/41 match, capability bundle 3 copies match
- 코드 리뷰: 건너뜀 — 리뷰 대상 파일 0개 (`ocr delegate preview` 가 SKILL.md 2개를 `unsupported_ext` 로 제외)

## 남은 이슈

- `gh-setup` description 에 `/gh-setup`, `$gh-setup` 호출 표기가 남아 있습니다. 이번 범위 밖이라 그대로 두었습니다.
