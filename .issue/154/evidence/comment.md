## 작업 요약

`issue-merge.mjs` 가 도움말 요청이나 모르는 옵션을 받으면 아무 동작 없이 멈추도록 고쳤습니다. `--help`/`-h` 는 어떤 서브커맨드에서든 사용법만 출력하고 exit 0 으로 끝납니다. `merge`·`close`·`cleanup`·`resolve` 는 받는 옵션 목록 밖의 옵션이 오면 오류와 사용법을 내고 exit 1 로 끝납니다.

화면 변경이 없는 스크립트라 캡처는 생략했습니다. 가짜 `gh`(호출을 파일에 기록만 하고 성공을 돌려주는 스크립트)를 PATH 앞에 둔 임시 저장소에서 같은 명령을 수정 전후 코드로 돌린 결과를 증거로 남깁니다.

## 변경 전후

| 명령 | 전 | 후 |
| --- | --- | --- |
| `merge --pr 151 --help` | exit 0, `gh pr merge 151 --squash` 실행 (`merged: true`) | exit 0, 사용법만 출력, gh 호출 0회 |
| `merge --pr 151 -h` | exit 0, `gh pr merge` 실행 | exit 0, 사용법만 출력, gh 호출 0회 |
| `merge --pr 151 --bogus x` | exit 0, `gh pr merge` 실행 | exit 1, `--bogus` 거부, gh 호출 0회 |
| `close --issue 1 --help` / `--bogus` | close 흐름 진입 (`gh issue view` 호출) | 사용법 출력 / exit 1, gh 호출 0회 |
| `cleanup --worktree <p> --bogus x` | exit 0, `git worktree remove` 시도 | exit 1, 실행 안 함 |
| `resolve --worktree <p> --help` | resolve 흐름 진입 | exit 0, 사용법만 출력 |

원본: `.issue/154/evidence/before/01-cli-guard.txt`, `.issue/154/evidence/after/01-cli-guard.txt`

## 변경 파일

- `skills/issue-merge/scripts/issue-merge.mjs`. 진입부에서 `parseArgs` 전에 `--help`/`-h` 를 먼저 처리하고, 네 서브커맨드의 허용 옵션 목록(`ALLOWED_OPTIONS`)과 대조합니다. `merge --dry-run` 처럼 지원하지 않는 플래그도 이제 거부됩니다.
- `skills/issue-merge/scripts/issue-merge-args.test.mjs`. 새 테스트입니다. 가짜 `gh` 로 도움말·모르는 옵션에서 호출 0회, 허용 옵션만 쓰면 merge 가 그대로 실행됨을 확인합니다.

`parseArgs`(공유 모듈)는 고치지 않았습니다. 검사를 `issue-merge.mjs` 진입부에 두어 공유 사본 4개는 그대로이며, `scripts/check-shared.sh` 도 통과합니다.

## 검증

- `node --test skills/issue-merge/scripts/*.test.mjs` 13건 통과 (새 테스트 3건 포함).
- `sh scripts/check-shared.sh` → `shared block: 4 copies match`.
- 원본: `.issue/154/evidence/after/02-tests.txt`

## 남은 이슈

- `inventory`·`preflight`·`status` 등 나머지 서브커맨드는 도움말 처리만 받고 옵션 목록 검사는 하지 않습니다. 이슈 요구대로 되돌리기 어려운 네 개부터 적용했습니다.
- 다른 스킬 스크립트(`issue-start.mjs` 등)도 같은 `parseArgs` 를 쓰므로 같은 문제가 있을 수 있습니다. 이번 범위 밖입니다.
