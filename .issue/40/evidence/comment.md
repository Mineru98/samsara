## 작업 성격 판정

`neither` — 변경 대상이 CLI 스크립트 내부 모듈(`issue-common.mjs`)이라 화면도 HTTP API 도 없다.
스크린샷 대신 **명령 출력 전후 비교**를 증거로 삼는다.

## 증거 형식에 대해 — 먼저 한계를 밝힙니다

**개발 머신이 macOS(darwin/arm64)라 Windows 바이너리를 실행할 수 없습니다.**
저장소에 `.github/workflows` 가 없어 Windows runner 도 없습니다.

그래서 "실제 Windows 에서 `git.exe` 가 떠서 저장소를 인식한다"까지는 이 회차에서 증명하지 못합니다.
대신 탐색 로직을 **플랫폼 주입형으로 바꿔**, Windows 의 플랫폼·환경변수·파일시스템 응답을 넣었을 때
정책이 어떻게 판정하는지를 회귀 테스트로 고정했습니다.

```text
증명한다      경로 정책과 권한 검사 분기가 Windows 입력에 대해 의도대로 판정한다
              macOS/Linux 동작과 보안 검증이 그대로다
              신뢰할 수 없는 실행 파일이 양쪽 플랫폼에서 계속 거부된다
증명 못 한다   실제 Windows OS 에서 spawnSync 가 git.exe 를 띄우는 것까지
```

## 이슈 본문보다 범위가 넓었습니다 — 깨지는 곳이 네 곳입니다

이슈는 후보 경로 목록을 지목했습니다. 코드를 읽으니 **그것만 고치면 여전히 실패합니다.**

| # | 지점 | Windows 에서 무슨 일이 나는가 |
| --- | --- | --- |
| 1 | `TRUSTED_EXECUTABLE_CANDIDATES` | `git.exe` 가 후보에 없어 루프가 빈손으로 끝난다 |
| 2 | `TRUSTED_EXECUTABLE_ROOTS` | 후보에 올려도 Unix 루트 밖이라 거부된다 |
| 3 | **`stat.mode` 권한 검사** | **핵심.** Node 가 Windows 에서 `0o100666` 을 준다. 실행 비트가 0 이라 `(mode & 0o111) === 0` 이 참이 되어 **모든 실행 파일이 탈락**한다. `(mode & 0o022) !== 0` 도 항상 걸린다 |
| 4 | `TRUSTED_COMMAND_PATH` | 자식 프로세스 `PATH` 가 존재하지 않는 Unix 디렉터리로 채워진다 |

3번이 가장 중요합니다. Windows 의 파일 권한은 ACL 이라 POSIX 모드 비트로 표현되지 않습니다.
경로만 추가하는 수정은 후보 등록까지만 성공하고 권한 검사에서 다시 전부 떨어집니다.

## before / after

### 1. 탐색 후보

**before** — Windows 경로(`.exe` / `ProgramFiles` / 드라이브 문자)를 담은 후보 **0개**

```text
git: ['/usr/bin/git', '/bin/git', '/opt/homebrew/bin/git', '/usr/local/bin/git']
```

**after** — Windows 후보 **6개**. 드라이브 문자를 하드코딩하지 않고 환경변수에서 조립합니다.

```text
C:\Program Files\Git\cmd\git.exe
C:\Program Files\Git\bin\git.exe
C:\Program Files (x86)\Git\cmd\git.exe
C:\Program Files (x86)\Git\bin\git.exe
C:\Users\dev\AppData\Local\Programs\Git\cmd\git.exe
C:\Users\dev\AppData\Local\Programs\Git\bin\git.exe
```

### 2. 같은 입력을 넣었을 때의 판정

`stat` 전제는 양쪽 동일합니다 — `mode=0o100666`, 실행 비트 없음, 쓰기 비트 열림.

| 입력 | before | after |
| --- | --- | --- |
| `C:\Program Files\Git\cmd\git.exe` (이슈 재현 환경) | `null` | **찾음** |
| `C:\Program Files\Git\bin\git.exe` | `null` | 찾음 |
| `C:\Program Files (x86)\Git\cmd\git.exe` | `null` | 찾음 |
| `%LOCALAPPDATA%\Programs\Git\cmd\git.exe` | `null` | 찾음 |
| 시스템 드라이브가 `D:` 인 환경 | `null` | 찾음 |

before 에서 `null` 이 나온 이유를 네 단계로 나눠 보면 이렇습니다.

```text
1) 후보 목록에 그 경로가 있는가 : false
2) 신뢰 루트 안에 있는가       : false
3) 실행 비트 검사 통과하는가   : false   (mode=0o100666)
4) 쓰기 비트 검사 통과하는가   : false
```

### 3. 신뢰할 수 없는 것은 계속 거부합니다 (완료 기준 5)

모드 비트 검사를 끈 것이 "아무 실행 파일이나 받아들인다"는 뜻이 아닙니다.
**검사 축을 그 플랫폼에서 의미 있는 것으로 바꿨을 뿐**이고, 심볼릭 링크 해소·정규 파일 확인·신뢰 루트 포함은 그대로 요구합니다.

| Windows | 결과 |
| --- | --- |
| `C:\Users\dev\Downloads\git.exe` | 거부 |
| `C:\Temp\Git\cmd\git.exe` | 거부 |
| 심볼릭 링크가 신뢰 루트 밖을 가리킴 | 거부 |
| 디렉터리를 실행 파일로 위장 | 거부 |
| 정상 설치 (대조군) | 허용 |

| Unix — 기존 보안 검증이 그대로인가 | 결과 |
| --- | --- |
| 실행 비트 없음 (`0o666`) | 거부 |
| 그룹·기타 쓰기 가능 (`0o777`) | 거부 |
| 신뢰 루트 밖 (`/tmp/git`) | 거부 |
| 남의 소유 (uid 999) | 거부 |
| 정상 (`0o755`, root 소유 — 대조군) | 허용 |

### 4. 오류 메시지 (완료 기준 4)

`repoRoot` 가 종료 코드만 보고 `err` 을 버려서 진짜 원인이 가려져 있었습니다.

```text
trustedExecutable('git')  → null
run()                     → { code: 1, err: 'trusted executable not found: git' }   ← 진짜 원인
repoRoot()                → "git 저장소가 아닙니다"                                  ← 사용자가 보던 것
```

**after** — 두 사유를 분리했습니다.

```text
실행 파일 미탐지  → git 실행 파일을 찾지 못했습니다 (platform: win32).
                    Git 이 설치돼 있는지, 표준 설치 경로에 있는지 확인하세요.
저장소가 아님     → git 저장소가 아닙니다. 저장소 안에서 실행하세요.
```

### 5. macOS 회귀

```text
before  trustedExecutable('git') → /usr/bin/git
after   trustedExecutable('git') → /usr/bin/git        (동일)

before  PATH → /usr/bin:/usr/sbin:/bin:/sbin:/System/Cryptexes/App/usr/bin
after   PATH → /usr/bin:/usr/sbin:/bin:/sbin:/System/Cryptexes/App/usr/bin   (동일)
```

`issue-onboard` 실제 실행도 그대로 동작합니다.

## 수정 범위 — 6벌이 아니라 2벌입니다

`issue-common.mjs` 는 6벌 있지만 **byte-identical 이 아닙니다.** 세 그룹으로 갈려 있습니다.

| 그룹 | 사본 | 줄 수 | `trustedExecutable` |
| --- | --- | ---: | --- |
| A | `issue-create` · `issue-end` · `issue-merge` · `issue-start` | 1525 | **없음** |
| B | `issue-onboard` | 1580 | 있음 |
| C | `issue-sync` | 1588 | 있음 + `trustedRegularFile` |

- 탐색 로직 수정 대상은 **B·C 2벌뿐**입니다. A 에는 함수 자체가 없어 넣을 이유가 없습니다.
- `scripts/check-shared.sh` 는 파일 전체가 아니라 `safeGraphTarget` **이후 꼬리 블록만** 6벌 일치를 요구합니다.
  이번 수정 구간은 그 블록 앞이라 걸리지 않지만, 수정 후 실행해 `6 copies match` 를 확인했습니다.

## 검증

```text
node --test skills/issue-onboard/scripts/*.test.mjs
  전: tests 62  pass 62  fail 0
  후: tests 84  pass 84  fail 0      (신규 22건 추가, 기존 62건 그대로)

node --test tools/issue-ontology/ontology.test.mjs   13/13 pass
sh scripts/check-shared.sh                           shared cache-safety block: 6 copies match
for f in skills/*/scripts/*.mjs; do node --check; done   구문 실패 0건
node skills/issue-onboard/scripts/issue-onboard.mjs plan  정상 동작
```

신규 회귀 22건은 Windows 탐지 9건, 거부 정책 6건, Unix 회귀 4건, PATH 구성 1건,
오류 메시지 분기 1건, 실제 환경 확인 1건입니다.

### 기준선에 대한 메모

작업 시작 시점에 기존 테스트 62건 중 **23건이 이미 실패**하고 있었습니다.
`tools/issue-ontology` 에 `ajv` 가 설치되지 않아 생긴 것으로, 이 이슈와 무관합니다.
`npm install` 로 해소한 뒤 기준선을 잡았습니다. `node_modules/` 는 gitignore 대상이고,
설치 중 생긴 `package-lock.json` 은 저장소가 추적하지 않는 파일이라 커밋에 넣지 않았습니다.

## 변경 파일

- `skills/issue-onboard/scripts/issue-common.mjs` — 플랫폼 정책 분기, `resolveTrustedExecutable`, `trustedCommandPath`, `gitFailureMessage`
- `skills/issue-sync/scripts/issue-common.mjs` — 위와 동일
- `skills/issue-onboard/scripts/trusted-executable.test.mjs` — 신규 회귀 22건

## 남은 것

실제 Windows 실행 확인은 이 회차 범위 밖입니다. 필요하시면 Windows 머신에서
`node skills/issue-onboard/scripts/issue-onboard.mjs` 를 돌려 결과를 알려 주시면 이 이슈에 덧붙이겠습니다.
