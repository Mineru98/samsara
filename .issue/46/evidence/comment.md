## 작업 요약

문서 오류를 고치려다 그 서술이 가리키던 **코드가 실제로 새는 것**을 찾았습니다. 사용자 승인을 받아 범위를 문서 + 코드로 넓혔습니다.

`SKILL.md` 는 "태그가 하나도 없으면 `v0.1.0`" 이라고 단언했지만 실제 조건은 "태그**도 파일 버전도** 없을 때" 입니다. 그런데 그 서술을 코드와 대조하는 과정에서, 태그 없는 저장소에서 `bump` 의 안전장치가 통째로 무력하다는 것이 드러났습니다.

## 증거 형식에 대해

화면도 API 도 없어 스크린샷 대신 **임시 저장소를 만들어 네 경로를 실제로 돌린 CLI 로그**를 남겼습니다.
원본은 `.issue/46/evidence/before/state.txt` 와 `.issue/46/evidence/after/state.txt` 입니다.

## 사고 재현 — 태그 0개 + 파일 버전 불일치

버전 소스 파일이 `0.3.2` / `0.3.3` 으로 섞인 저장소입니다.

| | 전 | 후 |
| --- | --- | --- |
| `VERSION_DRIFT` | `0` | **`1`** |
| `DRIFT_DIRECTION` | `files-inconsistent` | `files-inconsistent` (동일) |
| `plan patch` | `NEXT_VERSION=0.1.0` · `FIRST_RELEASE=1` | **exit 3** |
| `bump patch` | **exit 0 — 통과** | **exit 3** |
| `bump --force` | 통과 | **exit 3** (이 상태는 force 로도 막는다) |
| bump 후 `VERSION` | **`0.1.0`** ← 되돌아감 | `0.3.2` — 손대지 않음 |

전에는 스크립트가 `DRIFT_DIRECTION=files-inconsistent` 로 **정확히 판정해 두고도** 게이트가 그 값을 보지 않아, `0.3.2`/`0.3.3` 이 섞인 저장소를 경고 한 줄 없이 `v0.1.0` 으로 되돌렸습니다.

## 원인 — 세 지점이 물려 있었습니다

**1. `resolveCurrent()` 의 drift 가 태그를 전제했습니다**

```js
const drift = Boolean(tagVersion) && (fileVersions.length !== 1 || fileVersions[0] !== tagVersion);
```

`tagVersion` 이 null 이면 단축 평가로 뒤 조건을 보지 않아 `drift = false`. `cmdBump` 의 게이트가 `drift` 만 보므로 태그 없는 저장소에서는 존재하지 않는 것과 같았습니다. 방향 기반으로 바꿨습니다 — `none` 과 `no-tag` 만 정상이고 나머지는 태그 유무와 무관하게 drift 입니다.

**2. `computeNext()` 가 "모르는 것" 을 "없는 것" 으로 해석했습니다**

```js
: parseSemver(context.fileVersions.length === 1 ? context.fileVersions[0] : '');
```

파일 버전이 여러 개면 빈 문자열을 파싱해 null 이 되고, `!baseVersion` 분기가 이를 첫 릴리즈로 봤습니다. **버전이 없는 것과 어느 것이 맞는지 모르는 것은 다릅니다.** `inconsistent` 를 따로 돌려 구분합니다.

**3. `readSourceVersion()` 이 빈 값을 버전으로 셌습니다**

빈 `VERSION` 파일의 `''` 가 값 목록에 들어가 `FILE_VERSIONS=,0.3.2` 가 되고, 값이 둘로 보여 `files-inconsistent` 로 오판했습니다. 빈 값은 세지 않습니다.

## 정상 경로 3종 — 회귀 없음

| 상황 | 결과 |
| --- | --- |
| 태그 0개 + 파일 버전 하나(`0.3.2`) | `NEXT_VERSION=0.3.3` · `FIRST_RELEASE=0` · `no-tag` — 첫 릴리즈로 오판하지 않음 |
| 태그도 파일 버전도 없음 | `NEXT_VERSION=0.1.0` · `FIRST_RELEASE=1` — 기존 동작 유지 |
| 태그 있음 (실제 저장소, v0.3.3) | `NEXT_VERSION=0.3.4` · `DRIFT_DIRECTION=none` |

## 변경 파일

- `skills/issue-version/scripts/issue-version.mjs` — `resolveCurrent()` · `computeNext()` · `readSourceVersion()` · `cmdPlan()` · `cmdBump()`
- `skills/issue-version/SKILL.md` — v0.1.0 서술 5곳(32·71·148·194·197행) + hard-rule 1줄 추가
- `skills/issue-version/scripts/issue-version.test.mjs` — 회귀 추가·수정

## 검증

```text
node --test skills/issue-version/scripts/issue-version.test.mjs
ℹ tests 49  ℹ pass 49  ℹ fail 0     (전: 45건)
```

### 기존 테스트 2건이 잘못된 동작을 고정하고 있었습니다

수정 후 이 둘이 깨졌고, 확인해 보니 **테스트 쪽이 틀렸습니다.**

- `태그가 하나도 없으면 v0.1.0` — `VERSION` 만 비우고 나머지 7개 매니페스트에 `0.3.2` 를 남긴 채 `v0.1.0` 을 기대했습니다. 빈 문자열이 값으로 세어져 `length === 1` 이 거짓이 되는 바람에 **우연히** 통과하던 것입니다. 8개 전부에서 버전을 없애도록 고쳤습니다.
- `태그와 파일 버전이 어긋나면 --force 로 통과` — 한 파일만 `9.9.9` 로 바꿔 사실은 `files-inconsistent` 를 만들었습니다. 8개를 함께 내려 `tag-ahead` 를 만들도록 고치고, `files-inconsistent` 는 별도 테스트로 분리했습니다.

### 추가한 회귀 4건

```text
파일끼리 버전이 다르면 태그가 없어도 bump 를 막는다 (--force 포함, 8개 파일 무변경 단언)
파일끼리 버전이 다르면 plan 이 v0.1.0 을 제안하지 않는다
태그가 없어도 파일 버전이 하나면 그 값에서 올린다
VERSION 파일이 비어도 나머지 파일 버전을 불일치로 오판하지 않는다
```

## 남은 이슈

라벨이 `documentation` 뿐인데 코드 수정이 포함됐습니다. `bug` 를 함께 붙일지 확인이 필요합니다.
