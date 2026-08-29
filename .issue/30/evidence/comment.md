## 작업 요약

GLM ACP가 새 세션에서 읽는 루트 `AGENTS.md`를 추가하고, SAMSARA의 기존 skill·agent
플레이북과 실제 스크립트 진입점을 재사용하도록 연결했습니다. ACP에 없는 slash-command
자동 등록이나 subagent 실행을 주장하지 않으며, 권한 모드와 새 세션 갱신 조건도 문서화했습니다.

## 변경 전후

| 전 | 후 |
| --- | --- |
| 루트 `AGENTS.md`와 ACP용 command map이 없고, GLM 세션과 SAMSARA 워크플로우의 연결 경로가 없음 | `AGENTS.md`가 7개 prompt trigger, 7개 skill/script 진입점, ACP 권한 주의사항을 안내하고 `glm-acp-agent` 세션이 이를 읽음 |

문서·설정과 검증 도구만 변경되어 실행 화면 캡처는 생략했습니다. before/after 텍스트 증거는
`.issue/30/evidence/`에 보존했습니다.

## 변경 파일

- `AGENTS.md` — GLM ACP 프로젝트 컨텍스트 진입점과 workflow 순서
- `commands/glm-acp.md` — 7개 자연어 trigger와 기존 skill/script 매핑
- `skills/glm-acp/SKILL.md` — ACP 호환 계약, 권한·세션 제약, 검증 절차
- `agents/glm-acp-samsara.md` — 현재 세션에서 재사용하는 workflow role
- `tools/glm-acp/verify-context.mjs` — CLI와 ACP 컨텍스트를 재현하는 no-credential smoke check
- `README.md` — 설치·사용·제약·검증 안내

## 검증

- `node tools/glm-acp/verify-context.mjs` 통과: 설치된 CLI `--help`, 실제 ACP
  `initialize/session/new`, 공식 SDK 기반 `samsara onboard` prompt와 `AGENTS.md` 컨텍스트 확인
- `node --test tools/issue-ontology/ontology.test.mjs` 통과: 13 passed, 0 failed
- `bash scripts/check-shared.sh` 통과
- 기존 `.claude-plugin/plugin.json`, `.codex-plugin/plugin.json`, `.grok-plugin/plugin.json` 파싱 통과
- `node --check tools/glm-acp/verify-context.mjs`, `git diff --check` 통과
- 구현 커밋: `252af81b4241912f06904a87fea8d729e980c169`

## 남은 이슈

없음. 실제 GLM 모델 호출은 credential과 외부 네트워크가 필요하므로 검증하지 않았습니다.
