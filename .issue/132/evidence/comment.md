## 작업 요약

`issue-end` 문서의 PR 생성 예시가 PR 본문을 증거 폴더(`.issue/<N>/evidence/pr-body.md`, 커밋 대상)에 쓰게 해서 PR 뒤 워크트리가 dirty 로 남던 문제를 고쳤습니다.
예시 경로를 증거 폴더 밖의 무시 경로 `.issue/<N>/pr-body.md` 로 옮기고, 증거 폴더에 `pr-body.md` 가 남아 있으면 `issue-end.mjs context` 가 `evidence.stray` 로 따로 알리게 했습니다.
기준 브랜치는 `dev/0.4.0-alpha` 입니다.

## 변경 전후

화면 변경이 없는 문서·CLI 수정이라 스크린샷 대신 CLI 출력 텍스트를 증거로 남겼습니다.

| 확인 항목 | 전 | 후 |
| --- | --- | --- |
| 문서 예시대로 PR 본문을 쓴 뒤 `git status --porcelain -uall` | `?? .issue/132/evidence/pr-body.md` (dirty) | 빈 출력 (clean, `.issue/**` 로 무시) |
| 증거 폴더에 `pr-body.md` 가 있을 때 `context` 의 `evidence` | 증거로 집계됨 (`total: 1`, `files` 에 포함) | `files` 에서 빠지고 `stray` 로 따로 보고 |

- 전: [01-old-path-dirty.txt](https://github.com/Mineru98/samsara/blob/dev/0.4.0-alpha/.issue/132/evidence/before/01-old-path-dirty.txt)
- 후: [01-new-path-clean.txt](https://github.com/Mineru98/samsara/blob/dev/0.4.0-alpha/.issue/132/evidence/after/01-new-path-clean.txt), [02-stray-detected.txt](https://github.com/Mineru98/samsara/blob/dev/0.4.0-alpha/.issue/132/evidence/after/02-stray-detected.txt)

## 변경 파일

- `skills/issue-end/references/report-and-pr.md` — `--body-file` 예시를 `.issue/59/pr-body.md` 로 바꾸고, 증거 폴더 밖에 두는 이유와 PR 뒤 `git status --porcelain` 확인, `evidence.stray` 처리 안내 추가
- `skills/issue-end/scripts/issue-end.mjs` — `evidenceSummary` 가 `pr-body.md` 를 증거 집계·게시 비교에서 빼고 `evidence.stray` 로 돌려줌 (게시 비교에서 "누락"으로 잡혀 `evidencePublished: false` 가 되던 부수 증상도 함께 해소)
- `skills/issue-end/references/context-triage.md` — `evidence.stray` 필드 설명과 분기표 행 추가

## 검증

- `node --check skills/issue-end/scripts/issue-end.mjs` 통과
- `sh scripts/check-shared.sh` 통과 (공유 블록 무변경)
- 위 전후 출력 재현

## 남은 이슈

- `skills/issue-end/contracts/issue-phase-capability-bundle-v1.json` 의 closure 해시는 `.claude/skills/...` 경로를 가리키고 `dev/0.4.0-alpha` 에는 검증기가 없어 이번 범위에서 갱신하지 않았습니다.
