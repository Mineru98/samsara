## 작업 성격 판정

`neither`: CLI 스크립트와 문서만 바뀌었습니다. 화면도 HTTP API 도 없으므로 스크린샷 대신 **명령 출력 전후 비교**를 증거로 남깁니다.

## 먼저 밝혀 둘 것: 이번 회차에서는 이슈가 끝나지 않습니다

실측 수단은 **수동 실측**으로 정했습니다(GitHub Actions Windows 러너는 쓰지 않기로 함). 개발 머신은 Linux 입니다.
그래서 이번 회차의 산출물은 **실제 Windows 에서 한 번에 돌릴 수 있는 프로브와 절차**입니다. 실측 결과는 아직 없습니다.

| 완료 기준 | 상태 |
| --- | --- |
| 1. 실제 Windows 에서 `git 저장소가 아닙니다` 로 실패 안 함 (로그 증거) | ⏳ 실측 대기 — 아래 절차로 돌린 결과가 필요 |
| 2. onboard 테스트가 Windows 에서 통과 | ⏳ 실측 대기 |
| 3. 실측 방식이 저장소에 재현 가능하게 남음 | ✅ `windows-probe.mjs` + `references/windows-verification.md` |
| 4. 결함 발견 시 수정 | 🔶 코드 대조로 확정한 H1 만 수정. H2~H4 는 실측 뒤 판단 |

## 코드 대조로 찾은 Windows 위험 지점

#40 은 `issue-common.mjs` 의 탐지 정책을 고쳤습니다. 그런데 **gh 를 부르는 쪽은 별도 모듈**이라 그 수정이 닿지 않았습니다.

| # | 지점 | 내용 | 이번 처리 |
| --- | --- | --- | --- |
| H1 | `issue-common.mjs:138` (onboard·sync) | `'C:\Windows'` 가 JS 에서 `"C:Windows"` 가 된다. SystemRoot·windir 가 없으면 명령 디렉터리가 상대 경로가 된다 | **수정** + 회귀 테스트 |
| H2 | gh 후보 경로 | `…\GitHub CLI\bin\gh.exe` 만 본다. 표준 설치 위치가 이와 다르면 gh 미탐지 | 추정 — 프로브 2·3절이 판정 |
| H3 | `issue-tracker.mjs:40`, `issue-native-deps.mjs:19` | 자식 프로세스 PATH 가 Unix 디렉터리로 고정돼 있다 | 추정 — 프로브 5·6절이 판정 |
| H4 | 두 모듈의 `COMMAND_ENV_KEYS` | `SystemRoot`·`APPDATA`·`USERPROFILE`·`PATHEXT` 가 허용 목록에 없다. Windows 자식 프로세스의 네트워크 초기화가 실패할 수 있다 | 추정 — 프로브 5·6절이 판정 |

H2~H4 는 확인 없이 고치면 추측 수정이 됩니다. 실측 로그로 확정한 것만 고칩니다.

## before / after

### H1 — SystemRoot·windir 가 없을 때

**before**

```text
curl 후보: [ 'C:Windows\\System32\\curl.exe' ]
commandDirs: [ 'C:Windows\\System32', 'C:Windows', 'C:Windows\\System32\\Wbem' ]
```

**after**

```text
curl 후보: [ 'C:\\Windows\\System32\\curl.exe' ]
commandDirs: [ 'C:\\Windows\\System32', 'C:\\Windows', 'C:\\Windows\\System32\\Wbem' ]
```

신규 회귀 테스트는 수정 전 코드에서 **실패**합니다(pass 22 / fail 1). 수정 후에는 통과합니다.

### 실측 수단

| | before | after |
| --- | --- | --- |
| Windows 실측 스크립트 | 없음 | `skills/issue-onboard/scripts/windows-probe.mjs` |
| 실측 절차·판정 기준 | 없음 | `skills/issue-onboard/references/windows-verification.md` |

프로브는 읽기 전용이고 토큰 값을 출력하지 않습니다. 여섯 절로 나뉘며, 각 절을 PASS / FAIL / SKIP 으로 판정합니다.
FAIL 이 하나라도 있으면 exit 1 로 끝납니다.

```text
1. 실행 환경             플랫폼·Node·탐지에 쓰이는 환경변수 존재 여부
2. 실제 설치 위치         제한 없는 PATH 로 찾은 git / gh (where)
3. 탐지 정책             후보 경로별 존재 여부와 채택 결과      → H1·H2
4. 저장소 인식            issue-onboard 와 같은 git() 경로        → #40 기준 3
5. gh 호출               트래커의 제한 PATH·환경변수로 gh 실행   → H3·H4
6. 네이티브 의존성 조회    issue-native-deps 의 제한 환경으로 GraphQL → H3·H4
```

개발 머신(Linux)에서 돌린 결과: `PASS 5 / FAIL 0 / SKIP 0` (형식 확인용이며 Windows 결과가 아닙니다)

## 검증

| 명령 | 전 | 후 |
| --- | ---: | ---: |
| `node --test "skills/issue-onboard/scripts/*.test.mjs"` | 84 통과 | **90 통과** (+H1 1, 프로브 5) |
| `node --test tools/issue-ontology/ontology.test.mjs` | 13 | 13 |
| `sh scripts/check-shared.sh` | match | match |
| `node --check skills/*/scripts/*.mjs` | 실패 0 | 실패 0 |

절차 문서의 명령은 Linux 에서 한 번씩 돌려 확인했습니다. 그 과정에서 두 가지를 고쳤습니다.
- `node --test <폴더>` 는 동작하지 않았습니다 → 따옴표로 감싼 glob 으로 바꿨습니다(Node 22+).
- `sync` 가 저장소에 커밋된 `.issue/graph.json` 을 수정합니다 → 절차에 `git restore` 단계를 넣었습니다.

## 별도 리뷰 반영

코드 리뷰어가 따로 검토한 결과 막는 문제는 없었고, 아래를 반영했습니다(`e802776`).
- 6절: 조회할 이슈가 없거나 PR 이면 Windows 와 무관한 FAIL 이 났습니다 → 사유를 출력하고 `--issue` 로 번호를 지정하게 했습니다. API 미지원은 SKIP 으로 판정합니다.
- gh 조회가 실패해 origin URL 로 대신 잡은 경우 slug 출처를 표시해, gh 실패가 가려지지 않게 했습니다.
- 보고서가 공개 코멘트로 올라가므로 사용자 홈 아래 경로를 `~` 로 가립니다.
- 절차: `npm.cmd --prefix`(PowerShell 실행 정책), `sync --no-llm`, 결과 파일을 `$env:TEMP` 에 두기, 정리 뒤 `git status` 확인.

반영하지 않은 것: 프로브 테스트가 실제 gh·네트워크를 쓴다는 지적. 판정 로직을 주입형으로 분리하는 리팩터가 필요해서 이번 범위에서 뺐습니다.

## 변경 파일

- `skills/issue-onboard/scripts/windows-probe.mjs`: 신규 프로브
- `skills/issue-onboard/scripts/windows-probe.test.mjs`: 신규(형식·토큰 미노출·홈 경로 마스킹·저장소 무변경·인자 거부)
- `skills/issue-onboard/references/windows-verification.md`: 신규 절차
- `skills/issue-onboard/scripts/issue-common.mjs`, `skills/issue-sync/scripts/issue-common.mjs`: H1
- `skills/issue-onboard/scripts/trusted-executable.test.mjs`: H1 회귀 테스트

## 남은 일: 실측 부탁드립니다

Windows 에서 `skills/issue-onboard/references/windows-verification.md` 절차(브랜치 `fix/55-windows-onboard-probe`)를 돌려 주세요.
그리고 `windows-probe.txt` 전문과 테스트·`sync` 출력의 끝부분을 이 이슈 코멘트로 남겨 주세요.
FAIL 이 나온 절이 H2~H4 중 무엇을 확정하는지 보고 같은 브랜치에서 수정합니다.
