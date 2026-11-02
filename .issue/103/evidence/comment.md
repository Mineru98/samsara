## 작업 요약

`release` 가 노트 파일을 `<root>/.git/` 아래에 쓰고 있었는데, git worktree 에서는 `.git` 이 폴더가 아니라 `gitdir:` 를 담은 파일이라 `ENOTDIR` 로 죽었습니다. 이 시점은 태그 push 이후라 원격에 태그만 남았습니다.
노트 경로를 `git rev-parse --absolute-git-dir` 기준으로 바꾸고, 노트 쓰기를 **태그 생성 전**으로 옮겼습니다. 이제 노트 쓰기에 실패하면 태그를 아예 만들지 않고 exit 3 으로 끝납니다. `gh release create` 실패는 기존 롤백 경로(`RELEASE_ROLLED_BACK=1`)를 그대로 탑니다.

## 변경 전후

CLI 스크립트 변경이라 화면 캡처와 바운딩 박스는 해당 없습니다. 같은 재현 스크립트(임시 저장소에서 기본 체크아웃을 `work` 브랜치로 두고 `main` 을 별도 worktree 로 꺼낸 뒤 fake `gh` 로 `release v0.3.3`)의 출력을 비교합니다.

| 항목 | 전 | 후 |
| --- | --- | --- |
| worktree 에서 `release` (gh 정상) | `ENOTDIR` 로 비정상 종료, exit 1 | `✓ 릴리즈 발행`, exit 0 |
| 원격 태그 | **남음** (릴리즈 없음) | 생성됨 (릴리즈와 함께) |
| `gh release create` 실패 | 도달하지 못함 | `RELEASE_ROLLED_BACK=1`, 로컬·원격 태그 없음 |
| 노트 쓰기 실패 | 태그가 원격에 남음 | 태그를 만들지 않음, exit 3 |

원본 출력:
- 전: [`before/worktree-release.txt`](https://github.com/Mineru98/samsara/blob/dev/0.4.0-alpha/.issue/103/evidence/before/worktree-release.txt)
- 후: [`after/worktree-release.txt`](https://github.com/Mineru98/samsara/blob/dev/0.4.0-alpha/.issue/103/evidence/after/worktree-release.txt), [`after/test-suite.txt`](https://github.com/Mineru98/samsara/blob/dev/0.4.0-alpha/.issue/103/evidence/after/test-suite.txt)

## 변경 파일

- `skills/issue-version/scripts/issue-version.mjs` — `releaseNotesPath(root, tag)` 추가, `cmdRelease` 에서 노트 쓰기를 태그 생성 전으로 이동
- `skills/issue-version/scripts/issue-version.test.mjs` — worktree 테스트 4개 추가 (노트 경로 쓰기 가능 / release 성공 / gh 실패 롤백 / 노트 쓰기 실패 시 태그 없음)

## 검증

- `node --test skills/issue-version/scripts/issue-version.test.mjs` — 60개 전부 통과 (기존 56 + 신규 4)
- 신규 worktree 테스트 3개는 수정 전 코드에서 모두 실패하는 것을 확인
- 별도 리뷰 1회: 막는 문제 없음. "노트 쓰기 실패 경로 테스트 없음" 지적을 반영해 테스트 추가

## 남은 이슈

- 노트 임시 파일은 성공·롤백 후에도 git 디렉터리에 남습니다(기존 동작과 같음). 이번 범위에서는 손대지 않았습니다.
