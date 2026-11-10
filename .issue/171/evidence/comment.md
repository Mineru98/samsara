## 작업 요약

`.issue/settings.json`(기준 브랜치 기록 등 저장소 공통 설정)을 현재 체크아웃이 아니라 **메인 체크아웃 한 곳**에서 읽고 쓰게 했습니다.
워크트리 안에서 실행해도 메인 체크아웃의 `base --set` 기록을 그대로 쓰고, 워크트리에 별도 `settings.json` 을 만들지 않습니다.
`sync-base` 가 `other-branch` 로 건너뛰었을 때 메인 폴더 브랜치를 옮기자는 선택지를 문서에서 없앴습니다. 이제 묻지 않고 건너뛴 사실만 알립니다.

원래 신고("워크트리 대신 기존 브랜치를 전환한다")에 대해: 스크립트가 `switch`/`checkout` 으로 메인 폴더 브랜치를 바꾸는 코드는 찾지 못했고, 실제 전환 실행도 재현하지 못했습니다. 확인된 것은 기준 브랜치 오판정과, 문서가 전환을 선택지로 제시하던 것 두 가지입니다.

## 변경 전후

화면이나 성능 지표가 없는 변경이라 캡처 대신 같은 명령의 출력을 전후로 남겼습니다. 원본은 `.issue/171/evidence/{before,after}/` 에 있습니다.

조건: 임시 저장소. `origin/HEAD` = `main`, 작업 기준 브랜치 `dev`, 메인 체크아웃은 `main` 에 있음. 메인 체크아웃에서 `base --set dev` 를 한 뒤 `origin/dev` 에서 워크트리를 만들었습니다.

| 명령 | 전 | 후 |
| --- | --- | --- |
| (메인) `base` | `BASE=dev SOURCE=project` | `BASE=dev SOURCE=project` |
| (워크트리) `base` | `BASE=main SOURCE=detected` | `BASE=dev SOURCE=project` |
| (워크트리) `sync-base` | `base: main`, `ok: true` — 메인 폴더의 `main` 을 받아옴 | `base: dev`, `skipped: other-branch` — 메인에서 실행한 결과와 동일 |
| (워크트리) `ls -A .issue` | `1`, `settings.json` | `1` |
| (메인) `git branch --show-current` (전 과정 뒤) | `main` | `main` |
| `other-branch` 안내 | "기본 브랜치로 옮긴 뒤 받아오기" 선택지 있음 | 선택지 없음. 한 줄로 알리고 넘어감 |

메인 폴더 브랜치는 수정 전에도 바뀌지 않았습니다. 수정 전의 문제는 워크트리가 고정한 `dev` 대신 `main` 을 기준으로 삼은 것입니다.

## 변경 파일

- `skills/{issue-create,issue-start,issue-end,issue-merge}/scripts/issue-common.mjs` — `projectSettingsPath` / `writeProjectSettings` 가 주 체크아웃을 쓰도록 변경. 네 사본 동일
- `skills/issue-start/scripts/issue-start-base.test.mjs` — 회귀 테스트 1개 (워크트리 base 일치, 워크트리에 settings.json 미생성, 워크트리 `--set` 이 메인에 기록, sync-base 판정 일치, 메인 브랜치 보존)
- `skills/issue-start/references/evidence-capture.md`, `skills/issue-end/references/report-and-pr.md` — `other-branch` 안내에서 전환 선택지 제거
- `skills/issue-start/references/worktree.md` — 설정 파일이 메인 체크아웃 한 곳에 있다는 설명
- `skills/{issue-start,issue-end,issue-merge}/contracts/issue-phase-capability-bundle-v1.json` — 공통 코드 해시 갱신(재생성)

## 검증

- `node --test skills/*/scripts/*.test.mjs scripts/*.test.mjs` — 104개 통과 (기존 103 + 신규 1)
- `sh scripts/check-shared.sh` — 공유 블록 4개 사본 일치
- `node scripts/build-phase-capability-bundle.mjs --check` — 폐쇄 41/41 일치
- 별도 리뷰를 한 번 거쳤습니다. 차단급 결함은 없었고, `other-branch` 가 "건너뛰면 묻는다"는 일반 규칙과 충돌한다는 지적을 반영해 예외 문구를 넣었습니다.
- 신규 테스트가 수정 없이 실패하는지는 따로 실행하지 않았습니다. 같은 절차의 수정 전 출력(`before/repro.txt`)이 테스트의 첫 단언과 어긋납니다.

## 남은 이슈

- `issue-version` 은 공통 코드를 쓰지 않고 현재 체크아웃의 `.issue/settings.json` 을 직접 읽습니다(`skills/issue-version/scripts/issue-version.mjs` 의 `baseBranch`). 워크트리에서 실행하면 같은 오판정이 납니다. 또 `skills/issue-version/SKILL.md` 의 릴리즈 2단계가 `git switch <base>` 를 지시합니다. 릴리즈 흐름이라 이 이슈에서는 고치지 않았습니다.
- 워크트리에서 `base --set` 을 하면, 메인 폴더의 `.gitignore` 에 `.issue` 블록이 없을 때 메인 폴더의 `.gitignore` 가 수정됩니다. 브랜치는 바뀌지 않습니다.
- 여러 워크트리가 메인 폴더의 설정 파일 하나에 잠금 없이 씁니다. 동시에 `base --set` 과 자동 판별 기록이 겹치면 한쪽이 덮일 수 있습니다(재현하지 않음).
- bare 저장소에 워크트리만 붙인 구성은 메인 폴더가 없어 종전처럼 워크트리별 파일을 씁니다.
- `skills/herdr-orch/SKILL.md`, `skills/herdr-orch/references/codex.md` 에 "각 워크트리의 `.issue/settings.json` 도 `main` 으로 다시 감지될 수 있다"는 예전 설명이 남아 있습니다.
- 이미 만들어진 워크트리에 남은 `.issue/settings.json` 은 지우지 않습니다. 더는 읽지 않습니다.
- 이 작업은 메인 폴더의 기준 브랜치 기록(`dev/0.4.0-alpha`)이 아니라 `main` 기준으로 진행했습니다. `dev/0.4.0-alpha` 에는 반영되지 않았습니다.
