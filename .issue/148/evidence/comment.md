## 작업 요약

`issue-start.mjs worktree` 가 새 브랜치를 기준 브랜치에서 떼기 전에, 기준 기록이 낡았는지 확인합니다. 이슈 본문 `## 영향 범위` 의 경로가 기준 브랜치에는 없고 `origin/HEAD`(없으면 main → master)에는 있으면, 워크트리를 만들지 않고 exit 5 로 멈춥니다. 그리고 `issue-start.mjs base [--set|--default]` 명령을 새로 만들고, `worktree.md` 의 없는 명령(`issue-create.mjs base`) 안내를 이 명령으로 바꿨습니다.

화면 변경이 없는 스크립트라 캡처는 생략했습니다. 임시 git 저장소에서 같은 시나리오(`.issue/148/evidence/scenario.sh`)를 수정 전후 코드에 돌린 출력을 증거로 남깁니다. 시나리오의 원격에는 main(`skills/herdr-orch` 있음)과 dev/0.4.0-alpha(없음)가 있고, `.issue/settings.json` 의 기준은 dev 로 남아 있습니다.

## 변경 전후

| 항목 | 전 | 후 |
| --- | --- | --- |
| `base --set main` | `✗ 알 수 없는 모드: base` (exit 1) | `BASE=main` / `SOURCE=project` (exit 0) |
| 낡은 기준(dev)에서 `worktree` | dev 위에 워크트리 생성, 고칠 `skills/herdr-orch` 없음 (exit 0) | 생성 전에 멈춤, `BASE_PATHS_MISSING=skills/herdr-orch/SKILL.md` · `BASE_CANDIDATE=main` (exit 5) |
| 정상 기준(main)에서 `worktree` | 생성 (exit 0) | 생성 (exit 0), 그대로 |

기준이 `origin/HEAD` 와 같으면 검사를 건너뜁니다. 새로 만들 파일이나 상대 경로처럼 어느 브랜치에도 없는 경로는 멈출 근거로 쓰지 않습니다. 리뷰어가 이 저장소의 최근 이슈 본문 60건으로 확인했습니다. main 기준에서는 0건이 막혔고, 낡은 dev 기준에서는 원래 사고였던 #129·#130 을 포함해 5건이 막혔습니다.

원본: `.issue/148/evidence/before/01-stale-base.txt`, `.issue/148/evidence/after/01-stale-base.txt`, `.issue/148/evidence/after/02-base-test.txt`

## 변경 파일

- `skills/issue-start/scripts/issue-start.mjs` — `affectedPaths`·`checkBasePaths`·`cmdBase` 추가, `--set`/`--default`/`--allow-missing-paths` 옵션. 검사는 새 브랜치를 기준에서 뗄 때만 돌고, 디렉터리를 만들기 전에 실행됩니다.
- `skills/issue-start/scripts/issue-start-base.test.mjs` — 새 테스트 7건.
- `skills/issue-start/references/worktree.md` — `base` 명령 안내 수정, `기록이 낡았을 때` 절차(AskUserQuestion 3지선다) 추가.
- `skills/issue-start/SKILL.md` — 5단계에 exit 5 처리 한 줄.

## 검증

- `node --test skills/issue-start/scripts/issue-start-base.test.mjs` 7건 통과.
- `sh scripts/check-shared.sh` 통과. issue-merge·issue-version·check-shared 테스트 76건 통과.
- `tools/issue-ontology/ontology.test.mjs` 3건 실패는 main 에서도 똑같이 실패하는 기존 문제입니다.
- 별도 리뷰어가 두 차례 검토했습니다. 지적한 오탐(새 파일·상대 경로·중괄호 경로), 출처 오판, 빈 `--set` 값, origin/HEAD 가 없을 때를 반영했습니다.

## 남은 이슈

- `--dry-run` 은 이 검사를 하지 않습니다. 실제 실행에서만 멈춥니다.
- phase API(`issue-start.worktree`)는 `--base`·`--allow-missing-paths` 를 넘길 수 없습니다. 그래서 exit 5 가 나면 단계 실패로만 보입니다.
- children 배치에서는 `.gitignore` 보정(`ensureIgnoreBlock`)이 검사보다 먼저 실행됩니다. 원래부터 이 순서였습니다.
- `contracts/issue-phase-capability-bundle-v1.json` 의 closure 해시는 main 에서도 이미 실제 파일과 어긋나 있습니다. 이번 범위 밖입니다.
