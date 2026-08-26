# Before evidence

이 기록은 `feat/1-grok-build-plugin-support` 워크트리를 만든 직후, 구현 파일을 수정하기 전에 수집했습니다.

## 상태

```text
## feat/1-grok-build-plugin-support...origin/main
 M .gitignore
```

`.gitignore` 변경은 issue-start의 `evidence-init`이 `.issue` 증거 경로를 보호하기 위해 생성한 것입니다.

## 기존 JSON 메타데이터

실행 명령:

```sh
node - <<'NODE'
const fs = require('node:fs');
const files = ['.codex-plugin/plugin.json','.claude-plugin/plugin.json','.claude-plugin/marketplace.json','.agents/plugins/marketplace.json'];
for (const file of files) JSON.parse(fs.readFileSync(file, 'utf8'));
console.log('JSON_VALID=' + files.length);
console.log('GROK_MANIFEST_PRESENT=' + (fs.existsSync('.grok-plugin/plugin.json') ? '1' : '0'));
NODE
```

결과:

```text
JSON_VALID=4
GROK_MANIFEST_PRESENT=0
```

## Grok Build 정적 검증

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
  components: 1 skill dir(s), 0 command dir(s), 4 agent dir(s)
```

현재 결과는 Claude Code 호환 경로를 통해 기존 `skills/`와 Markdown agents를 확인한 것입니다. 명시적인
`.grok-plugin/plugin.json`은 아직 없습니다.

## 기존 ontology 테스트

`node --test tools/issue-ontology/ontology.test.mjs` 결과는 10개 중 3개 통과, 7개 실패였습니다.
실패 원인은 워크트리에 `ajv` 의존성이 설치되지 않아 `ERR_MODULE_NOT_FOUND`가 발생한 것이며, 이번
플러그인 변경과 무관한 환경 전제입니다. 구현 후 의존성을 설치해 재실행합니다.
