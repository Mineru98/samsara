## 작업 요약

glm-acp(ZCode · GLM) 지원을 중단하고 관련 파일과 참조를 모두 걷어냈습니다. 유지 대상 CLI 는 Claude Code · Codex · Grok Build 셋입니다.

## 증거 형식에 대해

화면도 API 도 없는 배포 구성·문서 변경이라 스크린샷 대신 `git grep` 결과와 테스트·CLI 출력을 증거로 남겼습니다.
원본은 `.issue/51/evidence/before/` 와 `.issue/51/evidence/after/` 입니다.

## 변경 전후

| | 전 | 후 |
| --- | --- | --- |
| `glm-acp`/`zcode`/`glm` 참조 파일 (`.issue` 제외) | 9개 | 0개 |
| glm-acp 스킬·커맨드·에이전트·도구 | 있음 | 삭제 |
| `.zcode-plugin/`, 루트 `marketplace.json` | 있음 | 삭제 |
| issue-version 버전 소스 | 8개 | 6개 |
| issue-version 테스트 | 54/54 통과 | 54/54 통과 |

### 1. 참조 검색

```text
# before
README.md
agents/glm-acp-samsara.md
commands/glm-acp.md
marketplace.json
skills/glm-acp/SKILL.md
skills/issue-version/references/version-sources.md
skills/issue-version/scripts/issue-version.mjs
skills/issue-version/scripts/issue-version.test.mjs
tools/glm-acp/verify-context.mjs

# after
(일치 없음)
```

### 2. 실제 저장소에서 `issue-version current`

```text
FILE_VERSIONS=0.3.3
SOURCE_PROBLEMS=0
VERSION_DRIFT=0
```

남은 6개 소스(VERSION, .claude-plugin ×2, .codex-plugin, .grok-plugin, tools/issue-ontology)가 모두 0.3.3 으로 일치합니다.

## 손대지 않은 것

과거 이슈 증거(`.issue/*/evidence`)는 기록이라 그대로 두었습니다.
