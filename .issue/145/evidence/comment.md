## 작업 요약

능력 번들(`issue-phase-capability-bundle-v1.json`)의 closure 가 사라진 `.claude/` `.codex/` 미러 구조를 가리키던 것을, 실제 플러그인 루트 구조(`skills/<name>/…`)로 다시 만들었습니다.
번들을 다시 만드는 생성기와 불일치 검사(`--check`)를 저장소에 두었고, 스킬 파일이 바뀌었는데 번들을 갱신하지 않으면 검사가 실패합니다.

## 변경 전후

| 지표 | 전 | 후 |
| --- | ---: | ---: |
| closure 일치 — 저장소 체크아웃 | 0/87 (없음 85, 해시 불일치 2) | **41/41** |
| closure 일치 — codex 0.3.7 설치본 | 0/87 (없음 85, 해시 불일치 2) | **41/41** (설치본 모사) |
| 번들 생성기 | 없음 | `scripts/build-phase-capability-bundle.mjs` |
| 재생성 결과 | — | 바이트 동일 (`git diff --exit-code` 통과) |
| 스킬 파일만 변경 시 검사 | — | **exit 1** (`closure drift: skills/issue-merge/SKILL.md`) |

항목 수가 87 → 41 로 줄었습니다. 같은 파일이 `.claude` / `.codex` 두 벌로 나뉘어 있던 것을 한 벌로 합쳤고, 지금은 없는 루트 `contracts/` `schemas/` `tools/` 와 무관한 `README.md` 는 뺐습니다.
"설치본 모사"는 codex 0.3.7 설치본을 복사해 이 브랜치의 `skills/issue-{start,end,merge}` 와 생성기를 덮어쓴 폴더입니다. 실제 설치본은 릴리즈 뒤에 바뀝니다.

원본 출력: [before](.issue/145/evidence/before/closure-check.txt) / [after](.issue/145/evidence/after/closure-check.txt) (`.issue/145/evidence/`)

## 변경 파일

- `scripts/build-phase-capability-bundle.mjs` — 신규. phases 는 기존 번들에서 그대로 가져오고 closure·mirrors·해시만 다시 계산해 세 사본에 씁니다. `--check [--root <플러그인 루트>]` 는 쓰지 않고 비교만 합니다.
- `scripts/build-phase-capability-bundle.test.mjs` — 신규. 저장소 일치, 재생성 결정성, 스킬 파일 변조·하위 폴더 추가·사본 불일치·대상 파일 삭제 시 실패.
- `skills/issue-{start,end,merge}/contracts/issue-phase-capability-bundle-v1.json` — 재생성 (세 사본 sha256 동일).
- `skills/issue-{start,end,merge}/schemas/issue-phase/capability-bundle-v1.schema.json` — `mirrors[].root` 를 `"."` 로. 두 런타임 모두 같은 플러그인 루트에 설치됩니다.
- `skills/issue-{start,end,merge}/SKILL.md` — 폐쇄 경로 기준(플러그인 루트)과 검사 명령 한 줄 추가.

## 검증

- `node scripts/build-phase-capability-bundle.mjs --check` → `closure: 41/41 match`, `3 copies match`
- `node --test scripts/build-phase-capability-bundle.test.mjs scripts/check-shared.test.mjs` → 10/10 통과
- `sh scripts/check-shared.sh` → 통과, issue-merge 기존 테스트 10/10 통과

## 남은 이슈

- codex tab 에서 `$issue-merge` 가 계약 검증을 넘는 화면 증거: 미측정 (설치본이 릴리즈 후에야 바뀜. 이 PR 머지·릴리즈 뒤 확인 필요)
- `capabilitySetSha256` 의 원래 입력 정의는 생성기가 유실돼 재현하지 못했습니다. 이제 `capabilitySetSha256`·`closure` 를 뺀 번들 필드의 정규 JSON 해시로 고정했습니다.
- 번들은 원시 바이트 해시라서 Windows 에서 CRLF 로 체크아웃한 설치본이면 검사가 실패합니다. 지금 플러그인 캐시 설치는 해당하지 않지만 `.gitattributes` 로 LF 를 고정하는 별도 작업이 필요할 수 있습니다.
