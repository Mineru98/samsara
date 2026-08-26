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

## 실행 환경 진단

실행 명령:

```sh
grok --version
grok 1.0.5 (5115b46bc909) [stable]

grok inspect --json
```

`grok inspect --json`의 JSON을 파싱해 현재 작업 폴더를 기준으로 요약한 결과:

```json
{
  "grokVersion": "1.0.5",
  "channel": "stable",
  "cwd": "/Users/mineru/SourceCode/samsara-issue-1",
  "projectRoot": "/Users/mineru/SourceCode/samsara-issue-1/",
  "projectTrusted": true,
  "localProjectPlugins": []
}
```

이 결과는 `inspect`가 현재 활성 환경 진단 명령이며, marketplace에서 설치하지 않은 로컬 원본을
활성 플러그인으로 등록하지 않았음을 보여줍니다. 로컬 `.grok-plugin/plugin.json` 유효성은
`grok plugin validate .`와 JSON/path 검사로 확인했습니다.

## README 점검

`README.md:287-323`을 수동 검토해 Grok Build 설치·업데이트 명령, TUI marketplace 흐름,
`validate`/`inspect`의 역할, 전체 SHA 고정 안내와 xAI 공식 링크가 모두 포함된 것을 확인했습니다.

## Validator 요약 차이

구현 전 `grok plugin validate .`는 기존 Claude Code 호환 메타데이터를 사용해 `4 agent dir(s)`를
보고했고, 구현 후 Grok manifest를 사용해 `1 agent dir(s)`를 보고했습니다. 이는 validator가
선택한 manifest의 컴포넌트 요약 차이이며, 파일 시스템의 정확한 결과는 별도 path 검사에서
`agents/*.md` 4개로 확인했습니다. agent 파일 자체는 변경하지 않았습니다.

## 캡처 생략 사유

이번 이슈는 브라우저 화면이나 실행 중인 서비스가 아니라 플러그인 manifest와 README 문서 변경입니다.
따라서 before/after 이미지 대신 동일한 Grok validator, JSON/path 검사와 회귀 테스트 결과를 증거로
남겼습니다. 외부 xAI marketplace 등록 PR은 이 이슈 범위에 포함하지 않았습니다.
