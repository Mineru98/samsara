# 버전 소스 파일

이 저장소에서 버전이 박혀 있는 곳은 아래 8개다. 릴리즈는 이 값들이 **전부 같을 때만** 성립한다.

| 파일 | 위치 | 형식 |
| --- | --- | --- |
| `VERSION` | 파일 전체 | 순수 텍스트 `0.3.2` + 개행 |
| `marketplace.json` | `plugins[].version` | JSON |
| `.claude-plugin/plugin.json` | `version` | JSON |
| `.claude-plugin/marketplace.json` | `plugins[].version` | JSON |
| `.codex-plugin/plugin.json` | `version` | JSON |
| `.grok-plugin/plugin.json` | `version` | JSON |
| `.zcode-plugin/plugin.json` | `version` | JSON |
| `tools/issue-ontology/package.json` | `version` | JSON |

목록의 정본은 스크립트의 `VERSION_SOURCES` 상수다. 배포 대상이 늘면 **문서가 아니라 그 상수를 먼저 고친다.**

## v 접두사 규약

```text
git 태그   v0.3.2      v 를 붙인다
파일       0.3.2       v 를 붙이지 않는다
```

섞이면 플러그인 호스트가 버전을 파싱하지 못한다. 스크립트가 양쪽을 정규화하므로
`plan` · `bump` 는 `v` 없는 값을, `pr` · `release` 는 `v` 붙은 값을 받는다.

## plugins[] 는 전부 갱신한다

`marketplace.json` 계열은 배열이라 항목이 늘 수 있다. 경로의 `*` 가 배열 전체를 뜻하며
`plugins[0]` 만 고치고 나머지를 두지 않는다.

## JSON 을 고치는 방법

정규식으로 `0.3.2` 를 통째로 바꾸지 않는다. `description` 이나 `defaultPrompt` 안에 같은
숫자가 들어 있으면 함께 오염된다. 대신 이 순서를 지킨다.

```text
1. JSON.parse 로 읽어 해당 경로의 값만 바꾼다
2. 원본을 그대로 재직렬화했을 때 바이트가 같은지 확인한다 (= 2-space 정규 포맷인가)
3. 같으면 재직렬화 결과를 쓴다 — 포맷이 보존된다
4. 다르면 "version" 필드의 값만 문자열로 정밀 치환한다 (탭 들여쓰기 등 특수 포맷 보존)
```

마지막 개행은 원본에 있던 대로 유지한다.

## 어긋났을 때

`current` 가 `VERSION_DRIFT=1` 을 내면 태그와 파일이 다르다는 뜻이다. **그 자체는 사고가 아니다.**
`DRIFT_DIRECTION` 이 뜻을 정한다.

| 값 | 상황 | 대응 |
| --- | --- | --- |
| `files-ahead` | bump PR 은 merge 됐고 태그가 아직 없다 | 정상. `release` 로 이어간다. 묻지 않는다 |
| `tag-ahead` | 태그만 달고 파일을 안 고쳤다 | 사고. 사용자에게 묻는다 |
| `files-inconsistent` | 파일끼리 값이 다르다 | 사고. 어느 값으로 통일할지 묻는다 |

`files-ahead` 를 사고로 취급해 질문으로 막으면, 이 스킬의 정상적인 2단계 진입이 첫 시도에서 막힌다.

## drift 로는 못 잡는 것

`VERSION_DRIFT` 는 **남아 있는 파일들의 값만** 본다. 파일이 아예 사라졌거나 JSON 이 깨져 값을 못 읽으면
그 파일은 비교 대상에서 빠지고, 나머지가 태그와 같으면 `VERSION_DRIFT=0` 이 나온다.
즉 이 스킬이 막으려는 사고("파일 하나를 빠뜨린 릴리즈")를 drift 만으로는 잡지 못한다.

그래서 `SOURCE_PROBLEMS` 를 따로 낸다. **0 이 아니면 bump 도 release 도 하지 않는다.**

## 2단계 진입 신호의 한계

`release` 는 "기본 브랜치의 파일 버전이 목표 버전과 같은가" 하나로 bump PR 의 merge 여부를 판단한다.
이 신호는 **누가 bump 했는지 구분하지 못한다.** 이 스킬을 거치지 않고 손으로 버전 파일을 올린 PR 이
merge 돼도 같은 신호가 뜬다.

그래서 `release` 는 반드시 노트를 먼저 보여주고 사용자 승인을 받은 뒤에 발행한다.
자동 파이프라인에서 승인 없이 부를 수 있게 만들지 않는다.
