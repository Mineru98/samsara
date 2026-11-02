## 작업 요약

증거 미러가 `main` 으로 가던 직접 원인은 **기준 브랜치 기록이 워크트리마다 따로 생기는 것**이었습니다. 워크트리 안에서 스크립트를 돌리면 메인 폴더의 `.issue/settings.json`(`dev/0.4.0-alpha`)을 못 보고, `origin/HEAD`(=`main`)를 판별해 워크트리 쪽에 새로 기록했습니다. #89 의 `4d04d72` 도 같은 경로였습니다(워크트리 기록 생성 01:17:28 → 미러 01:18:55).
그래서 기록은 주 체크아웃 한 곳에서 공유하도록 고쳤습니다. 기록값이 실제 흐름(주 체크아웃 브랜치·최근 PR base)과 어긋나면 워크트리 생성과 미러 push 전에 멈추도록 했고, 확인·기록 명령 `issue-start.mjs base --set` 을 추가했습니다.

## 변경 전후

재현 스크립트(`.issue/90/evidence/repro-90.sh`)는 임시 원격과 PATH 앞에 둔 가짜 `gh` 로 실행했습니다.

| 시나리오 | 전 | 후 |
| --- | --- | --- |
| A. 기록 `main` · 주 체크아웃 `dev/x` · PR base `dev/x` | `origin/main` 기준으로 워크트리 생성 (exit 0) | **exit 8**, `BASE_DRIFT=1` `BASE_CANDIDATE=dev/x`, 아무것도 만들지 않음 |
| B. `base --set dev/x` 후 재실행 | `알 수 없는 모드: base`, 계속 `origin/main` | 기록되고 묻지 않고 `origin/dev/x` 기준 생성 |
| C. 링크된 워크트리 안에서 판별 (주 체크아웃 기록 `dev/x`) | `main` + 워크트리에 `main` 새로 기록 | `dev/x`, 워크트리 쪽 기록 파일 없음 |
| D. 기록·신호 일치 (`main`) | `origin/main` 기준 생성 | **동일** |

원본: `.issue/90/evidence/before/repro.txt`, `.issue/90/evidence/after/repro.txt`

## 변경 파일

- `skills/*/scripts/issue-common.mjs` (6사본 동일) — `projectRoot`, `baseDrift`, `assertBaseAgreed`, `setProjectBaseBranch` 추가, 설정 읽기·쓰기를 주 체크아웃 기준으로, `mirrorEvidence` push 전 검사 (issue-end 미러에도 적용)
- `skills/issue-start/scripts/issue-start.mjs` — `worktree` 전 검사, `base [--set|--default]` 명령
- `skills/issue-start/SKILL.md`, `references/worktree.md` — exit 8 처리와 질문 형식, 없던 `issue-create.mjs base` 안내를 실제 명령으로 교체
- `scripts/check-shared.sh`, `scripts/check-shared.test.mjs` — 새 선언 4개 6사본 비교
- `skills/issue-start/scripts/base-drift.test.mjs` — 9개 시나리오 테스트

## 검증

- `node --test skills/issue-start/scripts/base-drift.test.mjs scripts/check-shared.test.mjs` — 20/20 통과
- `sh scripts/check-shared.sh` — 3개 검사 통과
- 이 워크트리 안에서 `issue-start.mjs base` → `BASE=dev/0.4.0-alpha`, 신호 둘 다 `dev/0.4.0-alpha` (이번 미러도 이 경로로 올라감)
- 원본: `.issue/90/evidence/after/tests.txt`

## 남은 이슈

- `issue-gate.test.mjs` 의 `owner liveness is locale independent` 1건은 변경 전 기준에서도 실패합니다(#89 대상, 이번 변경과 무관).
- 기존 워크트리(55·64·66·67·70·71·89)에 남은 `baseBranch: main` 파일은 이제 읽히지 않습니다. 지워도 되고 그대로 두어도 됩니다.
- 판정 규칙은 "두 신호가 같은 다른 브랜치를 가리킬 때만" 멈춥니다. gh 를 못 쓰는 환경에서는 PR 신호가 없어 멈추지 않습니다.
