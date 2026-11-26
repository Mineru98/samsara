---
name: issue-review
description: issue-start 8.5단계와 issue-end 2단계가 호출하는 내부 코드 리뷰 스킬입니다. ocr(open-code-review) delegation 모드로 리뷰 대상 파일과 규칙을 받아, 구현 세션과 분리된 서브에이전트가 작업 브랜치의 변경을 검토하고 결과를 `.issue/<번호>/evidence/review.json` 에 남깁니다. 사용자가 직접 부르지 않습니다.
user-invocable: false
---

<skill>
  <purpose>
    PR 을 만들기 전에 작업 브랜치의 코드 변경을 한 번 검토한다.
    리뷰할 파일과 규칙은 `ocr` CLI 가 정하고, 검토는 구현 세션과 분리된 서브에이전트가 한다.
    결과에는 검토한 커밋을 함께 적어, 리뷰 뒤에 코드가 바뀌었는지 `issue-end` 가 기계적으로 확인할 수 있게 한다.
  </purpose>

  <inputs>
    <arg name="issue" required="true">이슈 번호. 호출한 스킬이 넘긴다</arg>
    <arg name="background" required="false">이슈 요약과 완료 기준. `.issue/<번호>/plan.md` 1절을 쓴다</arg>
  </inputs>

  <preconditions>
    <item>호출자가 `issue-start` 또는 `issue-end` 이고, 현재 디렉터리가 그 이슈의 워크트리</item>
    <item>리뷰할 변경이 커밋되어 있다. 커밋되지 않은 변경은 리뷰 대상이 아니다</item>
    <item>git 2.41+, Node 18+. `ocr` CLI 는 없어도 흐름이 멈추지 않는다 (건너뜀으로 기록)</item>
  </preconditions>

  <hard-rules>
    <rule>리뷰는 구현을 한 세션이 직접 하지 않는다. 반드시 새 서브에이전트를 띄워 맡긴다.</rule>
    <rule>리뷰 서브에이전트는 코드를 고치지 않는다. findings 만 쓴다. 수정은 호출한 스킬이 한다.</rule>
    <rule>`ocr delegate preview` 가 낸 리뷰 대상 파일은 전부 reviewed 또는 skipped(사유 포함)로 끝나야 한다. `record` 가 합계를 검사한다.</rule>
    <rule>리뷰를 못 했으면 못 했다고 남긴다. `skip` 으로 사유를 기록하고, 없는 결과를 지어내지 않는다.</rule>
    <rule>`ocr review`(native 모드)는 쓰지 않는다. 별도 API 키가 필요하고 이 스킬의 전제가 아니다.</rule>
    <rule>이슈 코멘트, push, PR 생성을 하지 않는다. 결과 파일을 남기고 호출자에게 돌아간다.</rule>
  </hard-rules>
</skill>

# 스크립트 경로

아래 중 **존재하는 첫 번째 경로**를 `<issue-review>` 로 쓴다.

```text
skills/issue-review              # 플러그인 루트 (Claude Code / Codex)
~/.claude/skills/issue-review    # 홈 설치
~/.codex/skills/issue-review     # 홈 설치
```

# 실행 순서

## 1. 대상 확인

```bash
node <issue-review>/scripts/issue-review.mjs target
```

```text
BASE=main                 기준 브랜치 (.issue/settings.json 기록 → origin/HEAD)
MERGE_BASE=<sha>          작업 브랜치가 갈라진 지점
REVIEW_TARGET=<sha>       `.issue/` 밖을 건드린 마지막 커밋. 증거 커밋은 세지 않는다
OCR=1|0                   ocr CLI 설치 여부
```

기준 브랜치를 판별하지 못하면 exit 2 다. 호출자가 아는 값을 `--base <branch>` 로 넘겨 다시 실행한다.

## 2. ocr 이 없을 때

`OCR=0` 이면 AskUserQuestion 으로 한 번 묻는다.

```text
질문   <호출 스킬의 현재 단계>입니다. 코드 리뷰에 쓰는 ocr CLI 가 설치되어 있지 않습니다. 어떻게 할까요?

1. 설치하고 리뷰 (권장)   npm install -g @alibaba-group/open-code-review 를 실행한 뒤 1절부터 다시 합니다
2. 이번만 건너뛰기        리뷰 없이 진행하고, 건너뛴 사실을 결과와 보고에 남깁니다
```

건너뛰면 사유를 기록하고 5절로 간다.

```bash
node <issue-review>/scripts/issue-review.mjs skip --issue {issue_number} --reason "ocr 미설치"
```

## 3. 리뷰 대상 파일

```bash
ocr delegate preview --format json --from origin/<BASE> --to <REVIEW_TARGET> --exclude '.issue/**'
```

`reviewable_files` 가 0개면(문서만 바뀐 이슈 등) 리뷰할 것이 없다. 건너뜀으로 기록하고 5절로 간다.

```bash
node <issue-review>/scripts/issue-review.mjs skip --issue {issue_number} --reason "리뷰 대상 파일 0개"
```

`ocr` 이 `unknown flag: --format` 으로 실패하면 플래그를 빼고 텍스트 출력을 쓴다. 그 밖의 오류는 원문을 보고하고 `skip --reason "<오류 요약>"` 으로 남긴다.

## 4. 서브에이전트 리뷰

**새 서브에이전트를 하나 띄운다.** 구현 대화의 맥락을 넘기지 않고 아래만 준다.

```text
- 워크트리 경로, MERGE_BASE, REVIEW_TARGET
- 3절 preview 출력 (reviewable_files, excluded)
- 이슈 요약과 완료 기준 (plan.md 1절)
- findings 를 쓸 경로: .issue/{issue_number}/review-findings.json
- 코드를 고치지 말 것, 리뷰 대상 파일을 하나도 빠뜨리지 말 것
```

서브에이전트는 파일마다 아래를 수행한다.

```bash
ocr delegate rule --format json --from origin/<BASE> --to <REVIEW_TARGET> <path...>
git --no-pager diff --no-ext-diff --no-textconv --no-color <MERGE_BASE>..<REVIEW_TARGET> -- "<path>"
git --no-pager show --no-ext-diff --no-textconv --no-color "<REVIEW_TARGET>:<path>"    # 맥락이 필요할 때
```

규칙 그룹을 체크리스트로 삼아 diff 를 검토하고, 지적마다 실제 코드에서 근거를 확인한다. 근거를 대지 못한 지적은 버린다.
결과는 아래 형식의 JSON 으로 쓴다.

```json
{
  "coverage": {
    "total_files": 3,
    "reviewed_files": 2,
    "skipped_files": [{ "path": "dist/app.min.js", "reason": "생성 파일" }]
  },
  "findings": [
    {
      "path": "src/orders.js",
      "start_line": 42,
      "end_line": 47,
      "category": "bug",
      "severity": "high",
      "content": "빈 배열일 때 orders[0] 을 읽어 TypeError 가 난다"
    }
  ]
}
```

```text
severity   critical / high / medium / low
category   bug / security / performance / maintainability / test / style / documentation / other
```

## 5. 결과 확정

```bash
node <issue-review>/scripts/issue-review.mjs record --issue {issue_number} \
  --input .issue/{issue_number}/review-findings.json
```

`.issue/{issue_number}/evidence/review.json` 이 생긴다. 증거 폴더에 있으므로 증거 커밋·미러에 함께 실린다.
출력의 `BLOCKING=` 이 critical + high 개수다. exit 2 면 입력 형식이나 커버리지 합이 틀린 것이니 서브에이전트에 고쳐 쓰게 한다.

## 6. 호출자에게 돌려주기

```text
BLOCKING=0   medium / low 지적을 요약해 돌려준다. 호출자는 리포트의 `## 남은 이슈` 에 옮긴다
BLOCKING>0   지적 목록을 돌려준다. 호출자가 고쳐 커밋한 뒤 이 스킬을 1절부터 한 번 더 부른다
```

두 번째 리뷰에서도 `BLOCKING>0` 이면 더 반복하지 않는다. 호출자가 AskUserQuestion 으로 묻는다.

```text
질문   <호출 스킬의 현재 단계>입니다. 수정 뒤에도 리뷰에서 심각도 높은 지적이 <n>건 남았습니다. 어떻게 할까요?

1. 지적을 보고 직접 정하기 (권장)   지적별로 고칠지 넘어갈지 알려 주세요
2. 남긴 채 진행                     리포트의 남은 이슈에 그대로 적고 다음 단계로 갑니다
3. 여기서 중단                      워크트리와 커밋은 그대로 둡니다
```

# 최신 여부 확인

`issue-end` 는 PR 전에 이 명령으로 리뷰가 현재 코드에 대한 것인지 본다.

```bash
node <issue-review>/scripts/issue-review.mjs check --issue {issue_number}
```

```text
exit 0   최신. STATUS=reviewed 또는 skipped (건너뛴 기록도 통과한다. SKIP_REASON 을 보고에 옮긴다)
exit 3   review.json 이 없다        → 1절부터 실행
exit 4   리뷰 뒤에 코드가 바뀌었다    → 1절부터 다시 실행
```
