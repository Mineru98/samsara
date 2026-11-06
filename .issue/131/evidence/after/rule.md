# after — 질문 대리 규칙 변경 후

## skills/herdr-orch/SKILL.md:65-75
```text
## 3. 질문 대리 [검증]

- `blocked` 에서는 `agent read --lines N` 이 `agent_not_idle` 로 거절된다. `--source visible` 로 읽는다.
- 화면의 질문·선택지·tab 의 진행 요약을 그대로 옮겨 AskUserQuestion 으로 사용자에게 묻는다.
  질문 제목에 `[#<N> tab 질문 대리]` 를 붙이고 `header` 도 `#<N>` 으로 둬 어느 이슈의 질문인지 드러낸다. 오케스트레이터의 권장 판단이 있으면 함께 적는다.
  선택지에는 tab 화면의 선택지 번호(`1.`, `tab 2번` 등)를 적지 않고 선택지 문구만 옮긴다. 사용자는 그 번호가 무엇을 가리키는지 알 수 없다.
  여러 tab 의 질문을 한 번에 물을 때도 질문마다 이슈 번호를 붙여 구분한다. 선택지와 tab 화면 번호의 대응은 오케스트레이터만 기억한다.
- 답 넣기: `agent send-keys issue-<N> down ... enter`. 키마다 화면을 다시 읽어 커서 위치(`❯`)를 확인한 뒤 enter.
  렌더링이 늦으니 `pane wait-output <pane> --match "❯ <번호>." --source visible --timeout 3000` 으로 확인한다.
- 질문이 여러 개(상단 `☐ A ☐ B ✔ Submit`)면 하나 답할 때마다 다음 질문이 나타난다. 마지막 `Review your answers` 에서 사용자 답과 일치하는지 확인하고 `Submit answers`.
- 답 넣은 직후 `agent wait` 는 아직 `blocked` 로 읽혀 바로 반환된다. `pane wait-output --match "User answered"` 로 반영을 확인한 뒤 기다린다.
```

## skills/herdr-orch/references/codex.md:16, 83-94
```text
| 오케스트레이터가 AskUserQuestion 으로 묻는다 | 이슈 번호·질문·선택지·권장안을 일반 메시지로 적고 턴을 끝내 답을 기다린다 |
...
## 3. 질문 대리 [검증: cx merge tab / 미검증: cx 이슈 tab]

- 화면은 `agent read issue-<N> --source visible` 로 읽는다.
- 화면의 질문·선택지·tab 의 진행 요약을 그대로 옮겨 일반 메시지로 사용자에게 묻고 턴을 끝낸다. 답이 오기 전에는 tab 에 아무것도 보내지 않는다.
  질문 제목에 `[#<N> tab 질문 대리]` 를 붙여 어느 이슈의 질문인지 드러낸다. 오케스트레이터의 권장 판단이 있으면 함께 적는다.
  선택지에는 tab 화면의 선택지 번호(`1.`, `tab 2번` 등)를 적지 않고 선택지 문구만 옮긴다. 사용자는 그 번호가 무엇을 가리키는지 알 수 없다.
  여러 tab 의 질문을 한 번에 물을 때도 질문마다 이슈 번호를 붙여 구분하고, 사용자가 `#<N>: <선택지 문구>` 로 답하게 한다. 선택지와 tab 화면 번호의 대응은 오케스트레이터만 기억한다.
- 답 넣기: `agent prompt issue-<N> "<답>"`. tab 이 번호 선택지를 적었으면 번호와 선택지 문구를 함께 보낸다(예: `"2. 검토 승인하고 PR 만들기"`).
  여러 질문을 한 번에 물었으면 질문별 답을 한 메시지에 번호를 맞춰 담는다.
- 보낸 뒤 `agent wait issue-<N> --until working --timeout 15000` 으로 답이 받아들여졌는지 확인한다.
  `blocked` 로 선택 화면(커서 `❯`)이 떠 있으면 그 화면은 `agent send-keys issue-<N> down ... enter` 로 고르고, 키마다 화면을 다시 읽어 커서 위치를 확인한 뒤 enter 한다.
- 사용자가 항상 같은 답을 고르는 질문은 해당 스킬 쪽을 고치는 이슈로 올린다(예: #82). 다만 tab 세션은 설치된 플러그인 버전의 스킬을 읽으므로, 고친 스킬이 배포되기 전까지는 오케스트레이터가 아래 "자동 응답" 표대로 답한다.
```

## diff (HEAD~1..HEAD)
```diff
diff --git a/skills/herdr-orch/SKILL.md b/skills/herdr-orch/SKILL.md
index 82484fc..10cdfab 100644
--- a/skills/herdr-orch/SKILL.md
+++ b/skills/herdr-orch/SKILL.md
@@ -66,7 +66,9 @@ agent 여러 개를 한 번에 넘길 수 있다. 하나라도 이벤트가 나
 
 - `blocked` 에서는 `agent read --lines N` 이 `agent_not_idle` 로 거절된다. `--source visible` 로 읽는다.
 - 화면의 질문·선택지·tab 의 진행 요약을 그대로 옮겨 AskUserQuestion 으로 사용자에게 묻는다.
-  질문 제목에 `[#<N> tab 질문 대리]` 를 붙이고, 선택지 설명에 tab 쪽 번호를 적는다. 오케스트레이터의 권장 판단이 있으면 함께 적는다.
+  질문 제목에 `[#<N> tab 질문 대리]` 를 붙이고 `header` 도 `#<N>` 으로 둬 어느 이슈의 질문인지 드러낸다. 오케스트레이터의 권장 판단이 있으면 함께 적는다.
+  선택지에는 tab 화면의 선택지 번호(`1.`, `tab 2번` 등)를 적지 않고 선택지 문구만 옮긴다. 사용자는 그 번호가 무엇을 가리키는지 알 수 없다.
+  여러 tab 의 질문을 한 번에 물을 때도 질문마다 이슈 번호를 붙여 구분한다. 선택지와 tab 화면 번호의 대응은 오케스트레이터만 기억한다.
 - 답 넣기: `agent send-keys issue-<N> down ... enter`. 키마다 화면을 다시 읽어 커서 위치(`❯`)를 확인한 뒤 enter.
   렌더링이 늦으니 `pane wait-output <pane> --match "❯ <번호>." --source visible --timeout 3000` 으로 확인한다.
 - 질문이 여러 개(상단 `☐ A ☐ B ✔ Submit`)면 하나 답할 때마다 다음 질문이 나타난다. 마지막 `Review your answers` 에서 사용자 답과 일치하는지 확인하고 `Submit answers`.
diff --git a/skills/herdr-orch/references/codex.md b/skills/herdr-orch/references/codex.md
index fcdfc68..95b783e 100644
--- a/skills/herdr-orch/references/codex.md
+++ b/skills/herdr-orch/references/codex.md
@@ -13,7 +13,7 @@ tab 실행 명령·모델·스킬 호출·질문에 답하는 방법이 다르
 | 스킬 호출 `/issue-start` · `/issue-merge` | `$issue-start` · `$issue-merge` (셸에서는 작은따옴표로 감싼다 — `"$issue…"` 는 변수로 풀린다) |
 | merge 사다리: 사용량 20% 미만이면 `cc` opus·fable | `cx` 사다리만 쓴다. 20% 미만·알 수 없음이면 띄우기 전에 사용자에게 묻는다 |
 | tab 질문은 AskUserQuestion 화면(`blocked`), `send-keys` 로 고른다 | 일반 텍스트 질문(`done`/`idle`), `agent prompt` 로 답한다 |
-| 오케스트레이터가 AskUserQuestion 으로 묻는다 | 질문·선택지 번호·권장안을 일반 메시지로 적고 턴을 끝내 답을 기다린다 |
+| 오케스트레이터가 AskUserQuestion 으로 묻는다 | 이슈 번호·질문·선택지·권장안을 일반 메시지로 적고 턴을 끝내 답을 기다린다 |
 | `run_in_background` 로 감시기를 띄운다 | 감시기를 포그라운드로 돌린다. 이벤트가 나면 스스로 끝난다 |
 | 스크립트를 그대로 부른다 | 스크립트 앞에 `ORCH=codex` 를 붙인다. `scripts/` 는 스킬 폴더(`SKILL.md` 옆) 기준 |
 
@@ -84,7 +84,9 @@ cx tab 은 질문을 띄우고 입력을 기다릴 때도 `done`/`idle` 이다.
 
 - 화면은 `agent read issue-<N> --source visible` 로 읽는다.
 - 화면의 질문·선택지·tab 의 진행 요약을 그대로 옮겨 일반 메시지로 사용자에게 묻고 턴을 끝낸다. 답이 오기 전에는 tab 에 아무것도 보내지 않는다.
-  질문 제목에 `[#<N> tab 질문 대리]` 를 붙이고, 선택지 설명에 tab 쪽 번호를 적는다. 오케스트레이터의 권장 판단이 있으면 함께 적는다.
+  질문 제목에 `[#<N> tab 질문 대리]` 를 붙여 어느 이슈의 질문인지 드러낸다. 오케스트레이터의 권장 판단이 있으면 함께 적는다.
+  선택지에는 tab 화면의 선택지 번호(`1.`, `tab 2번` 등)를 적지 않고 선택지 문구만 옮긴다. 사용자는 그 번호가 무엇을 가리키는지 알 수 없다.
+  여러 tab 의 질문을 한 번에 물을 때도 질문마다 이슈 번호를 붙여 구분하고, 사용자가 `#<N>: <선택지 문구>` 로 답하게 한다. 선택지와 tab 화면 번호의 대응은 오케스트레이터만 기억한다.
 - 답 넣기: `agent prompt issue-<N> "<답>"`. tab 이 번호 선택지를 적었으면 번호와 선택지 문구를 함께 보낸다(예: `"2. 검토 승인하고 PR 만들기"`).
   여러 질문을 한 번에 물었으면 질문별 답을 한 메시지에 번호를 맞춰 담는다.
 - 보낸 뒤 `agent wait issue-<N> --until working --timeout 15000` 으로 답이 받아들여졌는지 확인한다.
```
