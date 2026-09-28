## 작업 성격 판정

`neither`: 설정 파일과 CLI 스크립트만 바뀌었습니다. 화면도 HTTP API 도 없으므로 스크린샷 대신 **명령 출력 전후 비교**를 증거로 남깁니다.

## 완료 기준

| 완료 기준 | 결과 |
| --- | --- |
| 새 워크트리에서 `cd tools/issue-ontology && npm ci` 성공 | ✅ `added 5 packages`, exit 0 |
| 이어서 ontology 13/13, onboard 84/84 통과 | ✅ 13/13, 84/84 |
| 잠금 파일이 `ajv` 8.17.1 을 고정 | ✅ 설치된 ajv `8.17.1` |

## 이슈 본문보다 범위가 한 곳 넓었습니다

`issue-version` 은 릴리즈할 때 `tools/issue-ontology/package.json` 의 version 을 올립니다.
잠금 파일에도 같은 값이 두 곳(`version`, `packages[""].version`) 들어 있습니다. 잠금 파일만 추가하고 끝내면 **다음 bump 때마다 잠금 파일만 옛 버전으로 남습니다.**
그래서 잠금 파일을 버전 소스에 넣었습니다.

- 한 파일에 버전 자리가 여럿이라 `paths` 필드를 새로 두었습니다. 같은 파일을 소스 두 개로 나누면 두 번째 쓰기가 첫 번째 갱신을 덮기 때문입니다.
- 의존성 항목(`node_modules/*`)의 version 은 건드리지 않습니다. 테스트로 고정했습니다.

## before / after

**before**: 새 워크트리에서

```text
$ cd tools/issue-ontology && npm ci
npm error code EUSAGE
npm error The `npm ci` command can only install with an existing package-lock.json ...
exit=1

# 설치 전 테스트
ontology  13 중 3 통과 / 10 실패
onboard   84 중 61 통과 / 23 실패   (Ajv 를 불러오지 못함)
```

**after**: 같은 조건(새 워크트리, `node_modules` 없음)

```text
$ cd tools/issue-ontology && npm ci
added 5 packages, and audited 6 packages in 1s
exit=0

ajv 8.17.1
ontology  13/13
onboard   84/84
git status --short → 변경 없음 (npm ci 가 추적 파일을 바꾸지 않음)
```

**버전 소스**: `issue-version current`

```text
before   tools/issue-ontology/package.json    0.3.3        (lockfile 없음)
after    tools/issue-ontology/package.json    0.3.3
         tools/issue-ontology/package-lock.json 0.3.3, 0.3.3
```

## 검증

| 명령 | 전 | 후 |
| --- | ---: | ---: |
| `node --test skills/issue-version/scripts/issue-version.test.mjs` | 54 | **56** (lockfile bump 1, 두 자리 불일치 판정 1) |
| `node --test "skills/issue-onboard/scripts/*.test.mjs"` (npm ci 후) | — | 84/84 |
| `node --test tools/issue-ontology/ontology.test.mjs` (npm ci 후) | — | 13/13 |
| `sh scripts/check-shared.sh` | match | match |
| `node --check` (skills·tools) | 실패 0 | 실패 0 |

구현 중 기존 테스트 1건이 실패했습니다("태그도 파일 버전도 없으면 v0.1.0 에서 시작"). 테스트 준비 코드가 JSON 의 최상위 `version` 만 지우고 잠금 파일의 `packages[""].version` 은 남겨 두었기 때문입니다. 준비 코드를 고쳐 통과시켰습니다.

## 별도 리뷰

코드 리뷰어가 따로 검토해 **APPROVE**(막는 문제 없음)를 냈습니다. 낮은 심각도 지적 3건을 받았습니다.
- 테스트 제목과 주석에 "6개 파일" 이 남아 있었습니다 → 개수를 빼고 고쳤습니다.
- 잠금 파일 형식이 바뀌면(CRLF 등) 문자열 치환 폴백으로 넘어갑니다. 그때 의존성 버전이 프로젝트 버전과 같으면 bump 가 **아무것도 바꾸지 않고 멈춥니다**(파일을 망가뜨리지는 않음) → `version-sources.md` 에 형식 유지와 복구 방법을 적었습니다.

## 참고 — 이번 범위 밖

`npm audit` 이 `ajv` 8.17.1 의 moderate 취약점(GHSA-2g4f-4pwh-qvx6, `$data` 옵션 사용 시 ReDoS)을 알립니다.
`validate.mjs:60` 은 `new Ajv2020({ allErrors: true, strict: true })` 로 `$data` 를 쓰지 않으므로 해당하지 않습니다. ajv 를 올릴지는 별도로 판단합니다.

## 변경 파일

- `tools/issue-ontology/package-lock.json`: 신규 (lockfileVersion 3)
- `skills/issue-version/scripts/issue-version.mjs`: `paths` 지원, lockfile 소스 추가
- `skills/issue-version/scripts/issue-version.test.mjs`: 시드에 lockfile 추가, 테스트 2건 추가, 문구 정리
- `skills/issue-version/references/version-sources.md`: 소스 목록·형식 안내
- `tools/issue-ontology/validate.mjs`, `skills/issue-onboard/scripts/issue-onboard.mjs`: 안내 문구 `npm install` → `npm ci`
