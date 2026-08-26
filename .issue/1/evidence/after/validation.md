# After evidence

## 변경

- `.grok-plugin/plugin.json` 추가
- `README.md`에 Grok Build 설치·업데이트·검증·호환 범위·SHA 고정 안내 추가
- 기존 Claude Code/Codex 메타데이터 유지

## Grok Build 검증

실행 명령:

```sh
grok plugin validate .
```

결과:

```text
Plugin manifest is valid.
  name: samsara
  version: 0.1.0
  description: Issue ontology harness for disciplined, evidence-backed software changes.
  components: 1 skill dir(s), 0 command dir(s), 1 agent dir(s)
```

Grok Build `1.0.5`에서 저장소 루트의 `.grok-plugin/plugin.json`을 포함한 플러그인 원본이
유효한 manifest로 판정되었습니다.

## 메타데이터·경로 검증

실행한 검증은 다음을 확인합니다.

- `.grok-plugin/plugin.json`, `.codex-plugin/plugin.json`, `.claude-plugin/plugin.json`,
  `.claude-plugin/marketplace.json`, `.agents/plugins/marketplace.json` 5개 JSON 파싱
- Grok manifest 필수 메타데이터와 `assets/logo.png` 존재
- `skills/` 아래 `SKILL.md` 8개와 `agents/` 아래 Markdown agent 4개 존재

결과:

```json
{
  "jsonFiles": 5,
  "manifest": "samsara",
  "version": "0.1.0",
  "skillCount": 8,
  "agentCount": 4,
  "logo": "assets/logo.png"
}
```

## 회귀 검증

```text
node --test tools/issue-ontology/ontology.test.mjs
tests 10
pass 10
fail 0

git diff origin/main...HEAD --check
exit 0
```

ontology 테스트는 워크트리에 `ajv`를 설치한 뒤 실행했습니다. 설치 과정에서 현재 의존성 트리에
moderate audit warning 1건이 보고되었지만, 이번 변경에서는 의존성 파일을 수정하지 않았습니다.

## 캡처 생략 사유

이번 이슈는 브라우저 화면이나 실행 중인 서비스가 아니라 플러그인 manifest와 README 문서 변경입니다.
따라서 before/after 이미지 대신 동일한 Grok validator, JSON/path 검사와 회귀 테스트 결과를 증거로
남겼습니다. 외부 xAI marketplace 등록 PR은 이 이슈 범위에 포함하지 않았습니다.
