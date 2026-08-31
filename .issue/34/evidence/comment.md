## 작업 요약

`issue-start` 스킬의 "마무리 보고" 출력을 `text` 코드 블럭 공백 정렬 방식에서 마크다운 표(table)로 바꿨다. 세 파일 모두 항목 순서·이름은 그대로 두고 표현 형식만 변경했다.

## 변경 대상 및 내용

이번 변경은 UI/서버 동작이 아니라 스킬 문서(마크다운)만 다뤄, 화면 캡처 대신 실제 diff 를 증거로 남긴다.

### `skills/issue-start/SKILL.md` — 메인 "마무리 보고"

**Before**
```text
이슈      [#{issue_number} <제목>](<이슈 URL>)
핵심 발견  <3줄 이내>
계획      .issue/{issue_number}/plan.md
워크트리   <WORKTREE_DISPLAY 값> (<layout>)
상태      status:in-process
브랜치    <이름>
기본 브랜치 <base> (<판별 출처>)
커밋      <구현 커밋> + <증거 커밋>
증거      before <n>장 / after <n>장 (박스 <n>개)
코멘트    [리포트 보기](<이슈 코멘트 URL>)
동기화    <메인 최신화 결과 또는 건너뛴 사유>
다음      <사용자가 고른 행동>

현재 단계 — issue-start 13단계(다음 행동 선택) 완료
```

**After**
```markdown
| 항목 | 내용 |
| --- | --- |
| **이슈** | [#{issue_number} \<제목\>](\<이슈 URL\>) |
| **핵심 발견** | \<3줄 이내, 줄바꿈은 `<br>` 사용\> |
| **계획** | `.issue/{issue_number}/plan.md` |
| **워크트리** | \<WORKTREE_DISPLAY 값\> (\<layout\>) |
| **상태** | `status:in-process` |
| **브랜치** | \<이름\> |
| **기본 브랜치** | \<base\> (\<판별 출처\>) |
| **커밋** | \<구현 커밋\> + \<증거 커밋\> |
| **증거** | before \<n\>장 / after \<n\>장 (박스 \<n\>개) |
| **코멘트** | [리포트 보기](\<이슈 코멘트 URL\>) |
| **동기화** | \<메인 최신화 결과 또는 건너뛴 사유\> |
| **다음** | \<사용자가 고른 행동\> |

> 현재 단계 — issue-start 13단계(다음 행동 선택) 완료

값이 없는 항목은 행을 지우지 말고 `-` 로 채운다.
```

### `skills/issue-start/references/next-actions.md` — 4번(종료) "남은 일" 행

**Before**
```text
남은 일   PR 이 아직 없습니다 — 마무리하려면 $issue-end
```

**After**
```markdown
| 항목 | 내용 |
| --- | --- |
| **남은 일** | PR 이 아직 없습니다 — 마무리하려면 `$issue-end` |
```

### `skills/issue-start/references/evidence-capture.md` — "동기화" 행 값 예시

**Before**
```text
동기화    기본 브랜치 최신화 완료 (3 커밋 받음)
동기화    이미 최신이었습니다
동기화    건너뜀 — 메인 폴더에 저장 안 된 변경이 있습니다. 나중에 `git pull --rebase origin main`
```

**After**
```markdown
| 상황 | 동기화 |
| --- | --- |
| 정상 최신화 | 기본 브랜치 최신화 완료 (3 커밋 받음) |
| 이미 최신 | 이미 최신이었습니다 |
| 건너뜀 | 건너뜀 — 메인 폴더에 저장 안 된 변경이 있습니다. 나중에 `git pull --rebase origin main` |
```

두 차례 수정 이력:
1. 1차 게시 — 불릿 리스트. 완료 기준("표 형식 통일" + "항목 이름 유지")을 엄밀히 충족하지 못한다는 itachi-merge-critic 지적으로 재작업
2. 2차 게시 — `| **동기화** | 값1<br>값2<br>값3 |` 한 행에 `<br>` 로 병합. 그러나 SKILL.md 가 `<br>` 을 "한 보고 안의 줄바꿈" 문법으로 정의하고 있어, 원래 서로 배타적인 예시 3개가 "동시에 출력하라"는 의미로 읽히는 문제가 재차 지적됨(2차 block)
3. 최종 — 배타적 예시를 각각 별도 행으로 분리한 표로 교체. 원문의 "상황에 따라 셋 중 하나만 쓴다"는 의미를 보존하면서 표 형식·항목명(동기화 열) 유지 두 기준 모두 충족

## 검증

```bash
grep -n "마무리 보고" -A 20 skills/issue-start/SKILL.md
grep -n "남은 일" -A 5 skills/issue-start/references/next-actions.md
grep -n "동기화" -A 5 skills/issue-start/references/evidence-capture.md
```

세 파일 모두 `| --- |` 헤더 구분행과 열 개수가 맞는 표로 렌더링됨을 육안 확인했고, 요청한 5개 규칙(항목명 굵게, 식별자 인라인 코드, `<br>` 줄바꿈, "현재 단계" 인용문 분리, 값 없는 항목 `-`)이 모두 반영됐다.
