## 작업 요약

`/issue-version {major|minor|patch}` 로 현재 버전을 판정해 한 단계 올리고, GitHub 태그와 릴리즈까지 잇는 `issue-version` 스킬을 추가했습니다.
1단계는 버전 소스 8개 파일을 갱신한 bump PR 까지만 만들고 멈추며, 그 PR 이 merge 된 뒤 다시 부르면 2단계로 태그와 릴리즈를 냅니다.
릴리즈 노트는 직전 태그 이후 커밋을 conventional commit type 별로 묶어 한국어로 만듭니다.

## 증거 형식에 대해

화면도 API 도 없는 변경이라 스크린샷·부하 측정 대신 **실제 저장소에서 명령을 돌린 CLI 로그**를 증거로 남겼습니다.
원본은 `.issue/41/evidence/before/state.txt` 와 `.issue/41/evidence/after/cli.txt` 입니다.

## 변경 전후

| | 전 | 후 |
| --- | --- | --- |
| 릴리즈 발행 스크립트 | 없음 (`gh release create` 를 손으로 호출) | `skills/issue-version/scripts/issue-version.mjs` |
| 버전 파일 갱신 | 8개 파일 수동 편집 | `bump` 한 번에 8개 동시 갱신 |
| 태그 ↔ 파일 불일치 | 감지 장치 없음 | `VERSION_DRIFT=1` 로 감지하고 `bump` 를 exit 3 으로 차단 |
| 릴리즈 노트 | 수기 작성 | 직전 태그 이후 커밋을 type 별로 그룹화 |

### 1. 현재 버전 판정 — `current`

```text
기본 브랜치 : main
최신 태그   : v0.3.2
  VERSION                              0.3.2
  marketplace.json                     0.3.2
  .claude-plugin/plugin.json           0.3.2
  .claude-plugin/marketplace.json      0.3.2
  .codex-plugin/plugin.json            0.3.2
  .grok-plugin/plugin.json             0.3.2
  .zcode-plugin/plugin.json            0.3.2
  tools/issue-ontology/package.json    0.3.2

LATEST_TAG=v0.3.2
FILE_VERSIONS=0.3.2
VERSION_DRIFT=0
```

### 2. 단계별 계산 — `plan`

| 인자 | 현재 | 다음 |
| --- | --- | --- |
| `patch` | v0.3.2 | **v0.3.3** |
| `minor` | v0.3.2 | **v0.4.0** |
| `major` | v0.3.2 | **v1.0.0** |
| (태그 0개일 때) | 없음 | **v0.1.0** — 인자와 무관 |

### 3. 실제 bump 실행 결과 — 직전 릴리즈 커밋과 동일한 범위

`3229b69 chore(release): bump version to v0.3.2` 가 손으로 건드린 파일과 정확히 같습니다.

```text
 .claude-plugin/marketplace.json   | 2 +-
 .claude-plugin/plugin.json        | 2 +-
 .codex-plugin/plugin.json         | 2 +-
 .grok-plugin/plugin.json          | 2 +-
 .zcode-plugin/plugin.json         | 2 +-
 VERSION                           | 2 +-
 marketplace.json                  | 2 +-
 tools/issue-ontology/package.json | 2 +-
 8 files changed, 8 insertions(+), 8 deletions(-)

매니페스트 7종 JSON 파싱 OK
version 필드 잔존 건수 = 0
```

검증 후 `git checkout` 으로 되돌려 이 브랜치에는 버전 변경이 남아 있지 않습니다.

### 4. 릴리즈 노트 — 실제 커밋(`v0.3.1..v0.3.2`, 26건)으로 생성

```markdown
## v0.3.2

v0.3.1 이후 커밋 26건.

### ✨ 기능

- **acp**: connect GLM workflow context (#33, #30)
- **marketplace**: add ZCode catalog (#32, #29)
- **plugin**: add ZCode compatibility (#31, #28)
- **issue-24**: issue-merge 마무리·완료 브리핑을 마크다운 표로 고정

### 🐛 버그 수정

- **issue-26**: merge guard가 문서 변경 이슈를 통과하도록 evidenceComplete 완화

### 📝 문서
...
### 🧹 유지보수

- **release**: bump version to v0.3.2 (#39, #38)

**전체 변경**: https://github.com/Mineru98/samsara/compare/v0.3.1...v0.3.2
```

### 5. 안전장치 실측

| 상황 | 결과 |
| --- | --- |
| 기본 브랜치가 아닌 곳에서 `release` | `✗ 태그는 기본 브랜치(main)에서 단다. 현재 브랜치: feat/41-issue-41` · exit 3 |
| 태그와 파일 버전이 어긋난 상태로 `bump` | exit 3, `--force` 로만 통과 |
| 이미 있는 태그로 `release` | `✗ 태그 v0.3.2 가 이미 있다.` · exit 3 |
| bump PR 이 merge 되기 전 `release` | `✗ bump PR 이 merge 됐는지 확인한다.` · exit 3 |
| `bump --dry-run` | 파일 변경 0건 |

## 변경 파일

- `skills/issue-version/scripts/issue-version.mjs` — `current` / `plan` / `bump` / `notes` / `pr` / `release` 6개 명령
- `skills/issue-version/scripts/issue-version.test.mjs` — 회귀 28건
- `skills/issue-version/SKILL.md` — 2단계 흐름과 hard-rules
- `skills/issue-version/references/version-sources.md` — 버전 소스 8개와 JSON 편집 규약
- `skills/issue-version/references/release-notes.md` — type→섹션 매핑과 노트 형식
- `README.md` — 제4장에 `8. issue-version — 결계 각인` 추가, 흐름도에 릴리즈 축 표기
- `AGENTS.md` — Skills 절에 2단계 동작과 "bump PR 을 스스로 merge 하지 않는다" 명시

## 검증

```text
node --test skills/issue-version/scripts/issue-version.test.mjs
ℹ tests 28  ℹ pass 28  ℹ fail 0
```

완료 기준 8개 항목 전부 실측했습니다.

| 완료 기준 | 확인 |
| --- | --- |
| SKILL.md · 스크립트 존재 | ✅ |
| patch/minor/major 계산 | ✅ v0.3.3 / v0.4.0 / v1.0.0 |
| 태그 없을 때 v0.1.0 | ✅ 세 인자 모두 |
| `--dry-run` 이 파일을 건드리지 않음 | ✅ `CHANGED_FILES=` 공백 |
| 8개 파일 갱신, 이전 버전 잔존 없음 | ✅ 잔존 0건 |
| 노트 type 별 그룹화 + 이슈·PR 번호 | ✅ |
| 1단계가 PR 에서 멈춤 | ✅ `pr` 이 `NEXT=` 안내만 출력 |
| 매니페스트 4종 JSON 유효 | ✅ 7종 파싱 OK |

## 남은 이슈

- 이 스킬 자체의 첫 실사용(v0.3.3 릴리즈)은 이 PR 이 merge 된 뒤에야 가능합니다. 스킬 코드는 워크트리에 있고 설치된 플러그인 캐시는 아직 0.3.2 이므로, `/issue-version` 슬래시 명령은 플러그인을 업데이트한 뒤 잡힙니다.
- 릴리즈 노트의 중복 항목(예: `docs(issue-34): 증거 자료 main 반영` 5회)은 실제 커밋을 그대로 반영한 결과입니다. 기록 충실성을 위해 접지 않았습니다.
