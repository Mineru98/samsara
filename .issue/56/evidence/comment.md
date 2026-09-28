## 작업 성격 판정

`neither`: 검사 스크립트와 테스트만 바뀌었습니다. 화면도 HTTP API 도 없으므로 스크린샷 대신 **명령 출력 전후 비교**를 증거로 남깁니다.

## 완료 기준

| 완료 기준 | 결과 |
| --- | --- |
| onboard·sync 사본의 Git 탐지 코드를 한쪽만 바꾸면 비0 종료 + 어긋난 파일 출력 | ✅ `git detection drift: UNIX_TRUSTED_EXECUTABLE_CANDIDATES (skills/issue-sync/… differs from skills/issue-onboard/…)`, exit 1 |
| 현재 main 상태에서는 통과 | ✅ `git detection code: 2 copies match (12 declarations)` |
| 기존 `safeGraphTarget` drift 검출 유지 | ✅ 테스트로 고정 |

## 코드 대조: 구간을 통째로 비교할 수 없었습니다

- Git 탐지 코드는 **onboard·sync 두 사본에만** 있습니다. create·end·merge·start 에는 없습니다(이 네 스킬은 탐지를 하지 않습니다).
- onboard 와 sync 는 탐지 코드가 들어 있는 구간이 **이미 서로 다릅니다.** sync 에만 `trustedRegularFile` 이 끼어 있고, 주석도 다릅니다.

그래서 구간 비교 대신 **탐지 선언 12개를 이름별로 뽑아 비교**했습니다.

```text
UNIX_TRUSTED_COMMAND_DIRS · UNIX_TRUSTED_EXECUTABLE_CANDIDATES · UNIX_TRUSTED_EXECUTABLE_ROOTS
windowsExecutablePolicy · unixExecutablePolicy · executablePolicy · trustedCommandPath
isPathWithin · resolveTrustedExecutable · trustedExecutable · executableMissing · gitFailureMessage
```

`issue-common.mjs` 는 건드리지 않았습니다. 같은 파일을 고치는 PR #58(#55)과 충돌하지 않게 하려는 것입니다. 합친 트리에서도 검사가 통과하는 것을 확인했습니다.

## before / after

두 번 모두 저장소 파일이 아니라 임시 복사본을 변조해 재현했습니다. 변조 내용도 같습니다(sync 사본의 git 후보에서 `/usr/bin/git` 한 개 제거).

**before**: 사본이 어긋났는데도 통과합니다.

```text
<   git: ['/usr/bin/git', '/bin/git', '/opt/homebrew/bin/git', '/usr/local/bin/git'],
>   git: ['/bin/git', '/opt/homebrew/bin/git', '/usr/local/bin/git'],
$ sh scripts/check-shared.sh
shared cache-safety block: 6 copies match
exit=0
```

**after**: 어긋난 선언과 파일을 알리며 실패합니다.

```text
$ sh scripts/check-shared.sh
shared cache-safety block: 6 copies match
git detection drift: UNIX_TRUSTED_EXECUTABLE_CANDIDATES (skills/issue-sync/scripts/issue-common.mjs differs from skills/issue-onboard/scripts/issue-common.mjs)
exit=1
```

**after**: 한 사본이 탐지 코드를 통째로 잃는 경우(#40 을 되돌리는 잘못된 merge)도 잡습니다.

```text
git detection group mismatch: expected [skills/issue-onboard/… skills/issue-sync/…] found [skills/issue-sync/…]
exit=1
```

## 검증

| 확인 | 결과 |
| --- | --- |
| `node --test scripts/check-shared.test.mjs` (신규) | **10/10**: 통과·후보 drift·Windows 정책 drift·선언 누락·부분 사본·safeGraphTarget 유지·그룹 축소·isPathWithin drift·접두어 이름·닫는 줄 변형 |
| awk 구현별 | gawk · mawk · busybox awk 모두 check 통과 + 테스트 10/10 |
| `dash scripts/check-shared.sh` | 통과 |
| PR #58 과 합친 트리 | merge-tree 충돌 없음, check 통과 |

macOS 의 BSD awk(one-true-awk)로는 **직접 확인하지 못했습니다.** 이 머신의 `nawk` 가 gawk 로 연결돼 있어 목록에서 뺐습니다.
대신 GNU 전용 문법인 `grep '\|'` 를 `grep -E` 로 바꿨고, 리뷰어가 awk 정규식을 POSIX ERE 기준으로 검토했습니다.

## 별도 리뷰

코드 리뷰어가 따로 검토했고 막는 문제는 없었습니다. 아래를 반영했습니다(`c53dbbb`).
- **[중간]** 한 사본이 탐지 코드를 통째로 잃으면 그 사본이 그룹에서 조용히 빠져, 검사가 1개만 비교하고 통과했습니다 → 탐지 그룹을 onboard·sync 로 고정했습니다.
- 선언 끝(0열 `}` `};` `];`)을 못 찾고 파일이 끝나면 실패하게 했습니다. 추출 규칙과 비교 범위는 스크립트 주석에 적었습니다.
- 테스트 4건을 추가했습니다.

반영하지 않은 것도 있습니다. `run()` 처럼 탐지 선언을 **호출하는** 코드는 비교하지 않습니다. 두 사본의 주석이 이미 달라서 구간째 비교할 수 없습니다. 이 범위는 스크립트 주석에 적어 두었습니다.

## 변경 파일

- `scripts/check-shared.sh`: 탐지 코드 검사 추가 (기존 검사는 그대로)
- `scripts/check-shared.test.mjs`: 신규
