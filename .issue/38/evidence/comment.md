## 작업 요약

패치 버전을 0.3.1 → 0.3.2 로 올렸다. `.omc/RELEASE_RULE.md` 에 정리된 8개 버전 소스 파일을 일괄 갱신하고 `tools/issue-ontology` 회귀 테스트로 확인했다.

## 변경 대상 및 내용

### 버전 파일 8개 (Before → After)

```text
== VERSION ==
1:0.3.1                                          →  1:0.3.2

== marketplace.json ==
14:      "version": "0.3.1",                     →  14:      "version": "0.3.2",

== .claude-plugin/plugin.json ==
4:  "version": "0.3.1",                           →  4:  "version": "0.3.2",

== .claude-plugin/marketplace.json ==
12:      "version": "0.3.1",                      →  12:      "version": "0.3.2",

== .codex-plugin/plugin.json ==
3:  "version": "0.3.1",                           →  3:  "version": "0.3.2",

== .grok-plugin/plugin.json ==
3:  "version": "0.3.1",                           →  3:  "version": "0.3.2",

== .zcode-plugin/plugin.json ==
3:  "version": "0.3.1",                           →  3:  "version": "0.3.2",

== tools/issue-ontology/package.json ==
3:  "version": "0.3.1",                           →  3:  "version": "0.3.2",
```

`grep -rn "0.3.1"` 로 8개 파일 전체를 스캔한 결과 잔존 0건, `grep -rn "0.3.2"` 로 8건 모두 확인.

## 검증

```bash
cd tools/issue-ontology && node --test ontology.test.mjs
```

```text
ℹ tests 13
ℹ pass 13
ℹ fail 0
ℹ cancelled 0
ℹ skipped 0
```

13건 전부 통과. `package-lock.json` 은 로컬 `npm install` 산출물이며 이슈 범위(버전 bump) 밖이라 커밋에 포함하지 않았다.

## 다음 단계

PR merge 뒤 머지 커밋에 `v0.3.2` 태그를 걸고 GitHub 릴리즈를 발행할 예정(`issue-merge` 단계).
