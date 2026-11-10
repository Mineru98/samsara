## 작업 요약

`issue-create` 가 이슈를 등록할 때 대상 버전 라벨(`version:X.Y.Z`)을 함께 붙입니다.
현재 버전의 패치 +1 을 묻지 않고 붙이고, 큰 변경이면 마이너 올림을 제안하며, 메이저는 사용자가 명시했을 때만 씁니다.
라벨이 없으면 노란색(`fbca04`)으로 만들고, 버전 태그·버전 파일이 없는 저장소에서는 붙이지 않습니다.

## 변경 전후

화면이나 성능 지표가 없는 변경이라 캡처 대신 같은 명령의 출력을 전후로 남겼습니다. 원본은 `.issue/169/evidence/{before,after}/` 에 있습니다.

| 명령 | 전 | 후 |
| --- | --- | --- |
| `version-label` | `✗ 알 수 없는 모드` (exit 1) | `CURRENT_VERSION=0.3.8`, `VERSION_LABEL=version:0.3.9`, `MINOR_LABEL=version:0.4.0` (exit 0) |
| `create --dry-run --label enhancement` | `--label enhancement` | `--label enhancement --label version:0.3.9` |
| `create --dry-run --label version:0.3.9` (성격 라벨 없음) | 통과 (exit 0) | 성격 라벨 게이트에서 차단 (exit 2) |

세 번째 줄은 기존 결함입니다. 버전 라벨이 성격 라벨로 세어져 게이트를 통과하던 것을 함께 막았습니다.

## 변경 파일

- `skills/issue-create/scripts/issue-create.mjs` — 현재 버전 판정, `version-label` 명령, `create` 의 버전 라벨 부착(`--bump`, `--no-version`), 버전 라벨을 성격 라벨에서 제외
- `skills/issue-create/scripts/issue-create.test.mjs` — 단위 테스트 10개 (신규)
- `skills/issue-create/SKILL.md` — hard-rule, 5·6단계, 마무리 보고
- `skills/issue-create/references/label-audit.md` — 대상 버전 라벨 축과 생성 규칙
- `skills/issue-create/references/issue-draft.md` — 큰 변경 기준과 마이너 제안 질문
- `skills/issue-create/references/create-and-handoff.md` — `create` 옵션과 출력

## 검증

- `node --test skills/*/scripts/*.test.mjs scripts/*.test.mjs` — 113개 통과 (기존 103 + 신규 10)
- `sh scripts/check-shared.sh` — 공유 블록 4개 사본 일치
- `node scripts/build-phase-capability-bundle.mjs --check` — 폐쇄 41/41 일치
- 별도 리뷰를 한 번 거쳐 권고 사항(라벨 목록 100개 제한, 형식이 틀린 버전 라벨, `--repo` 등록, toml 오탐)을 반영했습니다.
- 실제 라벨 생성·부착 경로는 가짜 트래커 단위 테스트와 dry-run 으로만 확인했습니다. 수정된 스크립트로 실제 이슈를 등록해 보지는 않았습니다.

## 남은 이슈

- 현재 버전은 지금 체크아웃 기준입니다(HEAD 에서 닿는 semver 태그와 버전 파일 중 최댓값). 뒤처진 체크아웃이나 날짜 형식 태그(`2024.11.01`)에서는 값이 어긋날 수 있고, 그때는 `--label version:X.Y.Z` 또는 `--no-version` 으로 덮습니다.
- `issue-version` 은 `version:*` 라벨을 읽지 않습니다. 릴리즈 버전이 라벨과 달라졌을 때 열린 이슈의 라벨을 옮기는 절차는 이 이슈 범위 밖입니다.
- 이 작업은 기준 브랜치 기록(`dev/0.4.0-alpha`)이 아니라 `main` 기준으로 진행했습니다. `dev/0.4.0-alpha` 에는 반영되지 않았습니다.
