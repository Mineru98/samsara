---
name: herdr-orch
description: herdr 안에서 이슈마다 tab 을 열어 `cc --mode=dev --model=opus` 로 /issue-start 를 돌리고, 15초 간격으로 감시하며 각 tab 의 AskUserQuestion 을 사용자에게 대신 묻고, 끝난 tab 을 닫고, 모인 PR 을 새 tab 의 /issue-merge #.. #.. 로 통합한다. "herdr-orch", "이슈들 tab 으로 병렬 진행", "herdr 로 이슈 오케스트레이션" 요청에 사용.
---

# herdr-orch

이 pane(오케스트레이터)은 직접 코드를 고치지 않는다. 이슈마다 tab 을 열어 작업을 맡기고,
감시·질문 대리·종료·통합 판단만 한다. 사용자는 이 pane 하고만 대화한다.

상태 표시: `[검증]` 은 이 저장소에서 실측으로 확인한 동작, `[미검증]` 은 아직 확인 전.

## 전제 [검증]

```bash
test "${HERDR_ENV:-}" = 1    # 아니면 herdr 밖이라고 알리고 멈춘다
herdr status                  # server running, endpoint_compatible: yes
```

`cc` 는 `claude` 를 감싸는 셸 함수다. herdr 는 그 프로세스를 `claude` agent 로 인식한다.
그래서 `agent start --kind claude` 가 아니라 `pane run` 으로 `cc` 를 띄운다.

## 0. 착수 순서 — 의존 관계로 파도(wave)를 나눈다 [검증]

- 선행 이슈가 없는 이슈만 지금 tab 으로 연다. 선행 이슈가 있으면 그 PR 이 merge 된 뒤에 연다. 사용자에게 묻지 않는다.
- 예(이 저장소): 1파 #71 #66 #70 #67 #64 → #71 merge 후 #69 · #66·#70 merge 후 #65 → 마지막 #72.
- 한 번에 열 tab 수는 독립 이슈 수만큼(실측 5개 동시 착수).

## 1. 이슈 tab 열기 [검증]

```bash
scripts/start-tab.sh issue-<N> "#<N> start: <짧은 제목>" "/issue-start #<N>"   # → <agent> <tab> <pane> <status>
```

스크립트가 하는 일(수동 절차):

```bash
R=$(herdr tab create --workspace "$HERDR_WORKSPACE_ID" --cwd "$PWD" \
      --label "#<N> start: <짧은 제목>" --no-focus)
P=<R 의 .result.root_pane.pane_id>
herdr pane rename "$P" "#<N> start: <짧은 제목>"
herdr pane run "$P" "cc --mode=dev --model=opus"
# pane get 의 agent=claude, agent_status=idle 이 될 때까지 2초 간격 확인 (보통 2~5초)
herdr agent rename "$P" issue-<N>
herdr agent prompt issue-<N> "/issue-start #<N>" --wait --timeout 10000
# timeout 오류는 정상. agent get 으로 working 이면 착수된 것
```

- tab 이름 형식: 착수 `#<N> start: <제목>`, 통합 `#<N> #<M> merge` [통합은 미검증].
- 포커스는 오케스트레이터 tab 에 남긴다(`--no-focus`).
- 이미 만든 pane 은 `pane move <pane> --new-tab --workspace <ws> --label <이름> --no-focus` 로 프로세스를 살린 채 tab 으로 옮길 수 있다.

## 2. 15초 감시 [검증]

```bash
scripts/watch.sh --interval 15 issue-<N> [issue-<M> ...]   # run_in_background
```

agent 여러 개를 한 번에 넘길 수 있다. 하나라도 이벤트가 나면 끝나므로, 처리한 뒤 남은 agent 로 다시 띄운다.
15초마다 상태를 찍고 `blocked | idle | done | gone` 이 되면 `EVENT <agent> <state>` 를 남기고 끝난다.
알림을 받으면 아래 3·4 로 처리하고, 계속 돌아야 하면 감시기를 다시 띄운다.

## 3. 질문 대리 [검증]

- `blocked` 에서는 `agent read --lines N` 이 `agent_not_idle` 로 거절된다. `--source visible` 로 읽는다.
- 화면의 질문·선택지·tab 의 진행 요약을 그대로 옮겨 AskUserQuestion 으로 사용자에게 묻는다.
  질문 제목에 `[#<N> tab 질문 대리]` 를 붙이고, 선택지 설명에 tab 쪽 번호를 적는다. 오케스트레이터의 권장 판단이 있으면 함께 적는다.
- 답 넣기: `agent send-keys issue-<N> down ... enter`. 키마다 화면을 다시 읽어 커서 위치(`❯`)를 확인한 뒤 enter.
  렌더링이 늦으니 `pane wait-output <pane> --match "❯ <번호>." --source visible --timeout 3000` 으로 확인한다.
- 질문이 여러 개(상단 `☐ A ☐ B ✔ Submit`)면 하나 답할 때마다 다음 질문이 나타난다. 마지막 `Review your answers` 에서 사용자 답과 일치하는지 확인하고 `Submit answers`.
- 답 넣은 직후 `agent wait` 는 아직 `blocked` 로 읽혀 바로 반환된다. `pane wait-output --match "User answered"` 로 반영을 확인한 뒤 기다린다.
- 사용자가 항상 같은 답을 고르는 질문은 해당 스킬 쪽을 고치는 이슈로 올린다(예: #82). 다만 tab 세션은 설치된 플러그인 버전의 스킬을 읽으므로, 고친 스킬이 배포되기 전까지는 오케스트레이터가 아래 "자동 응답" 표대로 답한다.

### 자동 응답 — 묻지 않고 답하는 질문 [검증]

| 질문 | 답 | 확인할 것 |
| --- | --- | --- |
| issue-start 12 · issue-end 7 메인 최신화 `dirty` | 잠시 치워두고 받아오기 | 여러 tab 이 동시에 이 질문에 있으면 **하나씩** 답한다. 옛 스킬은 맨 위 stash 를 꺼내므로, 메인 폴더 `git stash list` 에 `issue-sync` 가 사라진 뒤 다음 tab 에 답한다 |
| issue-start 13 다음 행동 | 검토 승인하고 PR 만들기 | 화면의 검증 결과가 통과(실패는 환경 원인으로 설명됨)일 때. 실패가 남아 있으면 사용자에게 묻는다 |
| issue-start 11 증거 이미지 렌더링 | 잘 보인다 | 코멘트 본문의 이미지 URL 이 모두 HTTP 200 이고, 내려받아 열어 본 after 이미지가 주장과 맞을 때. 아니면 "깨져 보인다" |
| issue-start 4 등 "도구 설치" | 이미 있으면 설치하지 않게 직접 입력 | 예: Go 는 `/home/mineru/.local/go/bin` 에 있다. 설치 선택지는 기존 설치를 덮어쓸 수 있으니 고르지 않는다(실측: #69) |
| issue-end 10 PR 생성 | 만든다 | 대상 브랜치가 작업 기준 브랜치와 같을 때. 다르면 사용자에게 묻는다 |
| issue-end 11 다음 행동 | 여기서 종료 → tab 닫기 | 4 참조 |
| issue-merge 3 후보 확정 · 6 PR merge · 7 정리 | 6 참조 | 6 참조 |

설계 범위·해석을 정하는 질문(issue-start 4단계 등), 원격 브랜치에 push·revert 하는 선택은 자동 응답하지 않고 사용자에게 묻는다.
선택지에 없는 지시를 넣을 때는 `Type something.` 줄로 커서를 옮기고 `herdr pane send-text <pane> "<지시>"` → 화면에 입력된 것을 확인 → `enter` (실측: #88 main revert + dev 재미러 지시).

작업 기준 브랜치가 저장소 기본값과 다르면, 메인 체크아웃뿐 아니라 각 워크트리의 `.issue/settings.json` 도 `main` 으로 다시 감지될 수 있다(실측: #88, #89). #90(PR #94) 이 merge·배포되기 전에는 tab 을 열 때 이 점을 지시에 넣거나, 미러 결과를 확인한다.

답을 보낼 셸이 zsh 이면 `set -- $x` 처럼 단어 분할에 기대는 반복문은 `bash -c` 로 감싼다(zsh 는 분할하지 않는다).

## 4. 종료 판단과 tab 닫기 — 묻지 않고 자동 [검증]

- issue-end 가 PR 을 만들고 마지막 "다음 행동" 질문(`issue-end 11단계`)을 띄우면 그 이슈의 tab 일은 끝난 것이다.
  이 질문은 사용자에게 묻지 않는다. merge 는 그 tab 에서 하지 않는다.
- "종료" 선택지 번호는 화면마다 다르다(3 또는 4). 번호로 옮기지 말고 `down` 을 하나씩 보내며 커서 줄에 `종료` 가 보일 때까지 옮긴다(실측: #89 는 3번).
- 자동 처리: "여기서 종료" 로 커서를 옮겨 확인 → enter → `pane wait-output --match "User answered"` →
  `agent wait --until idle --until done` → 마지막 출력(PR 번호, 남은 판단 거리)을 읽어 둔다 → `herdr tab close <tab_id>`.
- 닫은 뒤 사용자에게 결과 요약(PR, 남은 판단 거리)만 알린다. 오케스트레이터가 만든 tab 만 닫는다.
- tab 이 "워크트리 전부 merge" 를 권해도 따르지 않는다. 닫힌 이슈의 워크트리까지 묶으려 한다(실측: #55). merge 대상은 5 의 판정으로만 정한다.

## 5. merge 대상 판정 — tab 을 닫을 때마다 [검증: MERGE·HOLD(draft·UNKNOWN)·EXCLUDE(닫힌 이슈 워크트리)]

tab 을 하나 닫을 때마다 실행한다. 읽기만 하고 아무것도 바꾸지 않는다.

```bash
python3 scripts/merge-candidates.py --base <작업 기준 브랜치> \
  --live <아직 tab 이 도는 이슈들> --owned <이 오케스트레이터가 착수한 이슈들>
```

`--base` 는 저장소 설정이 아니라 실제 작업 기준 브랜치다(이 저장소는 `dev/0.4.0-alpha`, 설정값은 `main`).

| 판정 | 조건 |
| --- | --- |
| MERGE | 오케스트레이터가 맡은 이슈 · tab 종료 · base 일치 · draft 아님 · `MERGEABLE`+`CLEAN` · check 실패 없음 · 이슈가 열려 있고 `status:review` · PR 에 `.issue/<N>/evidence/` 있음 · 워크트리 HEAD = PR head · 워크트리 clean |
| HOLD | 위 조건 중 시간이 지나면 풀릴 수 있는 것이 걸림: tab 작업 중, draft, 충돌·미정 mergeState, check 미통과, `status:review` 아님, 증거 없음, 미push 커밋, 저장 안 된 변경 |
| EXCLUDE | 묶으면 안 되는 것: 맡지 않은 이슈의 PR, base 불일치, 닫힌 이슈, 이슈 번호 없는 브랜치, PR 없는 워크트리(예: 닫힌 #55 의 남은 워크트리) |

- PR 직후나 base 가 움직인 직후에는 `mergeable=UNKNOWN` 이 나온다. GitHub 가 다시 계산하는 중이니 몇 초 뒤 `gh pr view <PR> --json mergeable` 로 확인하고 재판정한다(실측: #83).
- `overlap` 은 MERGE 후보끼리 같은 파일을 고친 목록이다. 막지는 않지만 표에 함께 적는다(issue-merge 가 순서·충돌을 처리).
- `mergeable=UNKNOWN` 은 스크립트가 5초 간격으로 최대 30초 다시 조회한다.

### 자율 결정 — 묻지 않는다

merge 를 띄울지는 오케스트레이터가 스크립트의 `action` 으로 정한다. 사용자에게 묻지 않고, 결정과 판정표를 알리기만 한다.

| `action` | 조건 | 할 일 |
| --- | --- | --- |
| `merge` | MERGE 판정이 있고, 작업 중인 이슈 tab 도 다른 merge tab 도 없다 | 6 으로 바로 merge tab 을 연다 |
| `wait` | MERGE 가 있지만 작업 중인 이슈 tab 이나 merge tab 이 남아 있다 | 그 tab 이 끝나 닫힐 때 다시 판정해 함께 묶는다 |
| `none` | MERGE 판정이 없다 | 아무것도 띄우지 않는다 |

- HOLD 는 묶지 않고 사유만 알린다. 특히 draft 는 사용자가 고른 상태라 오케스트레이터가 ready 로 바꾸지 않는다.
- EXCLUDE 는 어떤 경우에도 묶지 않는다.
- 사용자가 직접 "지금 merge" · "ready 로 바꿔" 등을 지시하면 그 지시가 우선한다.

## 6. 통합 [진행 중 실측: #68 #82]

### merge 실행 사다리 [검증: 각 단계 기동·모델 표시 실측]

merge tab 은 아래 사다리의 1단계부터 띄운다. 어느 사다리를 쓸지는 Codex 남은 사용량으로 정한다.

```bash
python3 scripts/codex-usage.py            # REMAINING=<n> (최근 codex 세션의 rate_limits 기록. 지난 resets_at 은 초기화로 본다)
scripts/merge-launch.sh <단계> <N> <M>...  # LADDER · LAUNCH · PROMPT 출력, 단계가 끝나면 EXHAUSTED=1
LAUNCH="<LAUNCH>" scripts/start-tab.sh merge-<N>-<M> "#<N> #<M> merge[<단계 모델>]: …" "<PROMPT>"
```

| 남은 사용량 | 사다리 (앞에서부터) | 첫 프롬프트 |
| --- | --- | --- |
| 20% 이상 | `cx` 6.1 terra high → 6.1 sol medium → 6.1 sol high → 6 astra low → 6 astra medium | `$issue-merge #N #M …` (codex 는 `$` 로 스킬을 부른다) |
| 20% 미만 · 알 수 없음 | `cc --mode=dev --model=opus --effort=xhigh` → `cc --mode=dev --model=fable` | `/issue-merge #N #M …` |

- `cx` 실행 형식: `cx --model gpt-6.1-terra -c model_reasoning_effort=high` (`--tmux` 를 붙이지 않는다 — 붙이면 herdr pane 이 아니라 tmux 세션으로 빠진다). herdr 는 `codex` agent 로 인식한다.
- 기록값은 마지막 codex 세션 시점 값이다. cx tab 을 띄운 뒤 화면 하단 `weekly <n>% left` 가 실시간 값이니, 다음 단계로 올라갈 때는 그 값을 우선한다(실측: 기록 74% / 화면 72%).
- 다음 단계로 올라가는 조건: 비판 검토 block 이 세 번째 나옴, 또는 tab 이 충돌을 해소하지 못하고 멈춤(같은 오류 반복·스스로 중단 제안). 이때 지금 tab 에는 "중단"(아무것도 merge 안 됨)을 넣고 닫은 뒤, 같은 이슈 묶음으로 다음 단계 tab 을 연다. `.issue/merge/<묶음>/` 의 plan·review 는 그대로 두어 다음 단계가 이어 받게 한다.
- 단계를 올릴 때마다 사용량을 다시 확인한다. cx 사다리 도중 20% 아래로 내려가면 cc 사다리 1단계로 넘어간다.
- codex(cx) tab 의 차이 [검증: #64~#71 cx 회차]
  - issue-merge 의 "CI 통과" 조건을 문자 그대로 읽어 CI 가 없는 저장소의 PR 을 전부 보류한다. 그래서 cx 의 첫 프롬프트에 "CI 없음 = 검사 없음, 증거·base-tree 통합 테스트가 게이트" 를 붙인다(`merge-launch.sh` 가 붙인다). 이미 보류로 끝났으면 같은 내용을 `agent prompt` 로 보내 이어가게 한다.
  - 질문은 AskUserQuestion 화면이 아니라 일반 텍스트로 묻고, 입력을 기다리는 동안 상태가 `blocked` 가 아니라 `done`/`idle` 이다. 감시기는 `done` 에서도 멈추므로 화면을 읽어 질문인지 완료인지 가린다. 답은 `agent prompt <agent> "<답>"` 으로 보낸다.
  - `agent prompt` 직후 잠깐 `done` 으로 읽힌다. 감시기를 다시 띄우기 전에 `agent wait <agent> --until working --timeout 15000` 으로 작업 시작을 확인한다.
  - `~/.codex/hooks.json` 이 바뀌면 codex 가 시작할 때 "hooks need review" 화면을 띄워 첫 프롬프트를 삼킨다. 상태는 `done` 으로 읽힌다. hook 신뢰는 사용자에게 묻고(실측: 사용자가 trust all 선택 → `t`), `esc` 로 닫은 뒤 첫 프롬프트를 다시 보낸다.
  - 사다리 단계를 올릴 때 이전 단계가 남긴 `.issue/merge/<묶음>/` 계획·의미 충돌 목록·사용자 결정(예: S3)을 첫 프롬프트에 요약해 넘긴다.
- `EXHAUSTED=1` 이면 더 올리지 않고 사용자에게 상황을 보고하고 묻는다.
- tab 이름에 단계 모델을 넣는다. 예: `#64 #66 merge[6.1 sol medium]: …`.

### merge tab 진행

- MERGE 판정 이슈만 묶어 새 tab 을 연다. 이름 `#<N> #<M> merge: <각 이슈 요지>`, agent 이름 `merge-<N>-<M>`.
- 첫 프롬프트는 사다리의 `PROMPT`(cc 는 `/issue-merge #<N> #<M>`, cx 는 `$issue-merge #<N> #<M>`, 번호 오름차순)다. `--merging` 을 켠 채 감시한다.
- issue-merge 3단계 "후보 확정" 질문은 오케스트레이터가 답한다. tab 이 고른 대상이 `merge_issues` 와 정확히 같으면 "이대로 진행". preflight 가 이 질문 뒤에 돌더라도 충돌은 6단계 PR 별 승인에서 다시 걸러지므로 기다리지 않는다(실측: #64 #66 #67 #70 #71). 다르면 "일부 제외" 로 판정 밖 이슈를 빼거나, 판정 대상이 빠졌으면 사용자에게 알린다(실측: #68 #82, tab 도 #55 를 스스로 뺐다).
- issue-merge 6단계 "PR #X 를 merge 할까요?" 도 오케스트레이터가 답한다. 답하기 전에 직접 확인한다:
  PR 이 `merge_issues` 에 있음 · tab 이 고정한 head = `gh pr view` 의 headRefOid = 워크트리 HEAD · `MERGEABLE`+`CLEAN` · tab 이 보고한 사전 통합 테스트 통과.
  모두 맞으면 "merge 진행", 하나라도 다르면 답하지 않고 사용자에게 대신 묻는다(실측: PR #83 head 67d91ff).
- issue-merge 7단계 "워크트리 정리" 도 오케스트레이터가 답한다. 대상 PR 이 모두 `MERGED` 이고 워크트리가 clean 이면 "전부 정리"(merge 한 이슈만). 맡지 않은 이슈의 워크트리(예: #55)까지 지우는 선택지는 고르지 않는다.
- issue-merge 의 "보정 커밋 push" 승인도 오케스트레이터가 답한다. 답하기 전에 직접 확인한다:
  push 대상이 그 이슈의 작업 브랜치(`feat/·fix/·chore/<N>-…`)이고 `main`·작업 기준 브랜치가 아님 · force push 아님 ·
  diff 가 tab 이 계획에 적은 보정 항목(예: S1 마이그레이션 번호, S2 테스트, S4 schema)의 파일만 건드림 ·
  tab 이 보정 뒤 누적 트리에서 돌린 테스트가 통과했다고 보고함.
  모두 맞으면 승인, 하나라도 다르면(계획 밖 파일, force push, 테스트 실패·미실행) 사용자에게 묻는다.
  계획 항목 밖 파일이 끼어 있으면 그 파일의 diff 를 본다. 테스트 파일(`*_test.go` 등)에 보정 항목을 검증하는 단언만 추가됐으면 범위 안으로 보고, 제품 코드가 바뀌었으면 사용자에게 묻는다(실측: #71 `859bbf9` 의 schema 검증 테스트 5개).
  push 에 base 를 작업 브랜치로 병합한 커밋이 함께 실리면 `git show --cc <merge>` 로 충돌 해소 내용을 본다. 양쪽 변경을 모두 살린 합집합이면 승인 범위로 보고, 한쪽 줄을 버렸으면 사용자에게 묻는다(실측: #66 `5899b23` config.go 합집합).
- issue-merge 5단계 "비판 검토 block 후 계획 진행" 질문: 남은 block 이 기준 문구·절차 불일치이고 고칠 방법이 화면에 적혀 있으면 "반영 후 재검토" 를 고른다(재검토 생략은 고르지 않는다). block 이 범위·설계 문제면 사용자에게 묻는다. 세 번째 block 이 나오면 묻지 않고 "merge 실행 사다리" 의 다음 단계로 올린다(실측: #64~#71 2회차 block).
- 충돌을 "어떻게" 해소할지 고르는 질문(예: S3 처리 방식)처럼 범위·설계를 정하는 질문은 계속 사용자에게 대신 묻는다.
- 그 밖에 계획에 없던 되돌릴 수 없는 단계도 사용자에게 묻는다.
- issue-merge 가 끝나면 4 와 같이 묻지 않고 tab 을 닫는다. 닫은 뒤 판정을 다시 돌려 묶은 PR 이 사라졌는지(merged) 확인하고 결과를 알린다.
