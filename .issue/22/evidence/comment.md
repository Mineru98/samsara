## 작업 요약

프로젝트 패치 버전을 `0.3.0`에서 `0.3.1`로 한 단계 올렸습니다.
루트 버전 파일, 세 플러그인 매니페스트, Claude marketplace 매니페스트, ontology 패키지, README 표기를 모두 같은 값으로 맞췄습니다.

## 변경 전후

| 항목 | 전 | 후 |
| --- | --- | --- |
| 기준 버전 | `0.3.0` | `0.3.1` |
| 동기화된 버전 참조 | 7개 | 7개 |
| 별도 lockfile | 없음 | 없음 |

이 작업은 릴리스 메타데이터만 변경하므로 화면·HTTP 동작에 대한 URL, 뷰포트, 스크린샷 증거는 해당하지 않습니다. 대신 값 일치 검사와 기존 테스트 결과를 텍스트 원본으로 남겼습니다.

## 변경 파일

- `VERSION` — 기준 릴리스 버전
- `.codex-plugin/plugin.json` — Codex 플러그인 버전
- `.claude-plugin/plugin.json` — Claude 플러그인 버전
- `.grok-plugin/plugin.json` — Grok 플러그인 버전
- `.claude-plugin/marketplace.json` — marketplace 버전
- `tools/issue-ontology/package.json` — ontology 패키지 버전
- `README.md` — 플러그인 버전 및 capability bundle 문서 표기

## 검증

- 버전 일치 검사: 7개 값과 README 표기 모두 `0.3.1`
- `node --test tools/issue-ontology/ontology.test.mjs`: 13 passed, 0 failed
- `node --test skills/issue-onboard/scripts/*.test.mjs`: 62 passed, 0 failed
- `git diff --check origin/main...HEAD`: 통과
- 구현 커밋: `2466260820ba758c9b4c4b88e182e59fcc06cdcb`

원본 증거: `.issue/22/evidence/before/version.txt`, `.issue/22/evidence/after/`

## 남은 이슈

없음
