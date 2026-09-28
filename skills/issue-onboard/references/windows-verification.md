# Windows 실측 절차

issue-onboard 의 Git·gh 탐지는 개발 머신(macOS/Linux)에서 플랫폼을 주입한 시뮬레이션으로만 검증된다.
실제 Windows 에서 동작하는지는 이 절차로 확인한다. 저장소 CI 에 Windows 러너가 없으므로 사람이 한 번 돌린다(이슈 #55).

## 준비

```text
OS        Windows 10/11 (native — WSL 아님)
Node      22 이상 (native Windows 설치본 — 3번의 따옴표 glob 을 Node 가 직접 펼친다)
Git       Git for Windows 표준 설치 (예: C:\Program Files\Git\cmd\git.exe)
gh        GitHub CLI 표준 설치, `gh auth login` 완료
셸        PowerShell 또는 cmd (Git Bash 아님 — Git Bash 는 PATH·경로 표기가 달라 결과가 섞인다)
```

## 실행

samsara 저장소를 clone 한 폴더에서 실행한다. 1~3번은 저장소를 바꾸지 않는다(2번은 `node_modules` 만 만든다). 4번은 저장소에 커밋된 `.issue/graph.json` 캐시를 갱신하므로 5번으로 되돌린다.

```powershell
# 1. 환경·탐지·gh 호출을 한 번에 확인 — 결과를 파일로 남긴다
#    결과 파일은 저장소 밖(임시 폴더)에 둔다 — 실수로 커밋되지 않게
node skills/issue-onboard/scripts/windows-probe.mjs --out "$env:TEMP\windows-probe.txt"

# 2. 의존성 설치 (ontology 검증에 필요)
#    npm.cmd 로 부른다 — PowerShell 기본 실행 정책은 npm.ps1 을 막는다
npm.cmd --prefix tools/issue-ontology install

# 3. 단위 테스트 (따옴표를 빼면 PowerShell 에서 파일을 찾지 못한다)
node --test "skills/issue-onboard/scripts/*.test.mjs"

# 4. 실제 진입점 — 이슈 #40 완료 기준 3 의 원래 재현 명령
#    --no-llm: LLM 호출 없이 GitHub 조회만 한다
node skills/issue-onboard/scripts/issue-onboard.mjs sync --limit 5 --no-llm

# 5. 4번이 바꾼 캐시 되돌리기
git restore .issue/graph.json
git status --short   # 아무것도 나오지 않아야 한다
```

## 판정

| 확인 | 통과 조건 |
| --- | --- |
| 프로브 | 마지막 줄 `## 요약: ... FAIL 0 ...`, exit 0 |
| 단위 테스트 | `fail 0` |
| 진입점 | `git 저장소가 아닙니다` 나 `trusted executable not found` 없이 끝남 |

## 결과 회신

`$env:TEMP\windows-probe.txt` 전문과 3·4번의 출력 끝부분을 이슈 #55 코멘트로 남긴다. 프로브는 토큰 값을 출력하지 않고, 사용자 홈 아래 경로는 `~` 로 가린다.
6절이 FAIL 이고 사유가 "이슈가 없거나 PR" 이면 `--issue <실제 이슈 번호>` 를 붙여 1번을 다시 돌린다.
FAIL 이 있으면 프로브의 `## 2. 실제 설치 위치` 와 `## 3. 탐지 정책` 절을 비교하면 어느 후보 경로가 빠졌는지 보인다.
