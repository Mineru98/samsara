## 작업 성격 판정

`neither` — 변경 대상이 `skills/` 아래 `SKILL.md` · `references/*.md` · `agents/openai.yaml` ·
`scripts/issue-create.mjs` 의 출력 문자열로 전부 문서·설정·기계 출력이다. UI·백엔드 로직 변경이 없고
첨부 스크린샷도 없어, 화면 캡처 대신 **명령 출력 전후 비교**를 증거로 삼는다.

## 이슈 본문 수치 정정

본문에 "`skills/` **21개** 파일 82줄"이라 적었으나 실제는 **26개 파일** 82줄 84토큰이다.
줄 수와 토큰 수는 맞고 파일 수만 어긋났다. 아래 before 증거의 합계가 정본이다.

## 무엇을 했나

`$` 를 없애는 대신 `/` 를 정본으로 세우고 `$` 를 병기했다. 설치 시점에 호스트별로 치환할 자리가
없기 때문이다 — `codex plugin add --help` 에 build/postinstall 옵션이 없고, 설치본
`~/.codex/plugins/cache/samsara/samsara/0.3.2/` 는 리포 트리를 그대로 복사하며,
`.claude-plugin` / `.codex-plugin` / `.grok-plugin` / `.zcode-plugin` 네 매니페스트가 모두
같은 `"skills": "./skills/"` 를 가리킨다.

| 부류 | 처리 | 대상 |
| --- | --- | --- |
| SKILL.md `description` | `/` 와 `$` 를 트리거로 **함께 나열** | 9개 |
| `agents/openai.yaml` | `Use /issue-x (or $issue-x) to …` | 6개 |
| `references/*.md` | 산문 첫 등장은 인라인 병기, 4지선다 템플릿은 펜스 위 주석 1줄 | 11개 |
| mermaid 노드 라벨 | `/` 만. 라벨을 따옴표로 감싸 모양·파싱 보존 | 8곳 |
| `issue-create.mjs` `NEXT=` | `/` 만 (기계가 파싱하는 값) | 1곳 |
| `$ref`/`$defs`/`$schema`/`$id`, 셸 변수 | **손대지 않음** | 117개 |

4지선다 템플릿 안에 `$` 를 끼워 넣으면 사용자에게 보이는 출력이 지저분해져, 그 파일들은
펜스 바로 위에 `호출 표기는 / 가 정본이다. Codex 계열에서는 $ 접두도 같은 스킬을 가리킨다` 를
한 줄 넣고 템플릿 자체는 `/` 로 깨끗하게 뒀다.

## before / after

### 1. 트리거 표기

**before**
```text
파일: 26
줄:   82
토큰: 84          ← 전부 $ 단독
```

**after**
```text
$ 표기       18건 (전부 병기)
/ 정본 표기  242건
고아 $ 표기  0건   ← 같은 줄에 / 병기가 없는 $ 토큰
```

`$` 18건은 SKILL.md description 9 + `agents/openai.yaml` 6 + 산문 인라인 병기 3 이다.

### 2. 스키마·변수 토큰 무손상

```text
before  총 스키마 토큰: 117
after   총 스키마 토큰: 117
```

### 3. `NEXT=` 출력

```text
before  455:  console.log(`NEXT=$issue-start #${number}`);
after   455:  console.log(`NEXT=/issue-start #${number}`);
```

### 4. mermaid 노드 라벨

```text
before  N -->|merge| O[$issue-merge 위임/]
after   N -->|merge| O["/issue-merge 위임"]

before  B4[$issue-create 위임] --> C
after   B4["/issue-create 위임"] --> C

before  A[/"$issue-end"/]
after   A[/"/issue-end"/]
```

`$` 를 `/` 로 바꾸면 `[/issue-merge 위임/]` 이 되어 사각형이 평행사변형으로 바뀌고,
`[/issue-create 위임]` 은 닫는 `/` 가 없어 파싱이 깨진다. 라벨을 따옴표로 감싸 둘 다 막았다.
`O[$issue-merge 위임/]` 끝의 `/` 는 짝 없는 오타로 보고 제거했다.

### 5. 정적 검사

```text
shared cache-safety block: 6 copies match
모든 .mjs 문법 통과 (50개)
openai.yaml 6개 YAML 파싱 통과
```

### 6. 건드리지 않은 경로

```text
git status --short -- .omo                     → 출력 없음
git diff --stat HEAD~1 HEAD -- .issue .omo     → 출력 없음
```

## 완료 기준 대조

- [x] 모든 SKILL.md `description` 에 `/` 와 `$` 표기가 함께 있다 — 9/9
- [x] `$` 단독 등장 0건 — 고아 `$` 표기 0건
- [x] 스키마 토큰 무손상 — 117 → 117
- [x] `NEXT=/issue-start #<번호>`
- [x] mermaid 문법 유효, 노드 모양 유지
- [x] `.issue/**`, `.omo/**` 무변경

## 범위 밖 관찰

`issue-onboard plan --json` 이 `Ajv 온톨로지를 사용할 수 없습니다. tools/issue-ontology에서
npm install을 실행하세요` 로 실패한다. `issue-start fetch` 단계에서 ready 판정만 생략될 뿐
진행을 막지 않아 이번 작업에는 영향이 없었다. 별도 이슈 후보다.

## 증거 원본

`.issue/47/evidence/before/`, `.issue/47/evidence/after/` — 각 7종 텍스트 파일
