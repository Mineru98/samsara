## 작업 요약

ZCode가 우선 탐색하는 `.zcode-plugin/plugin.json`을 추가하고 기존 `skills/`와 `agents/`를
플러그인 컴포넌트로 연결했습니다. README에 ZCode 설치·활성화·새로고침·신뢰 주의사항을
추가했으며, 루트 `marketplace.json`은 후속 이슈 #29의 범위로 남겼습니다.

## 변경 전후

| 전 | 후 |
| --- | --- |
| `.zcode-plugin/plugin.json` 없음, README에 ZCode 설치 안내 없음 | `samsara@0.3.1` 매니페스트가 `./skills/`와 `./agents/`를 연결하고 README에 설치·운영 안내 추가 |

문서·설정과 플러그인 메타데이터만 변경되어 실행 화면 캡처는 생략했습니다. ZCode CLI가 이
환경에 설치되어 있지 않아 클라이언트 설치 대신 매니페스트 파싱, 컴포넌트 경로, README 계약을
정적으로 검증했습니다.

## 변경 파일

- `.zcode-plugin/plugin.json` — ZCode 호환 매니페스트와 기존 9개 스킬·4개 에이전트 연결
- `README.md` — ZCode 설치, 활성화, 새로고침, 비활성화·삭제 및 신뢰 주의사항

## 검증

- 커스텀 정적 검증 통과: 매니페스트 `samsara@0.3.1`, 스킬 9개, 에이전트 4개, README 주장 및 기존 매니페스트 버전 보존
- `node --test tools/issue-ontology/ontology.test.mjs` 통과: 13 passed, 0 failed
- `git diff --check` 통과
- 구현 커밋: `5fdb573aa3417ca3d6b57d5364328f7584ac63a2`

## 남은 이슈

- #29에서 루트 `marketplace.json`을 추가하고 `Mineru98/samsara` Personal marketplace 설치 흐름을 검증합니다.
