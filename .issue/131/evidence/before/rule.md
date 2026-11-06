# before — 질문 대리 규칙 원문

## skills/herdr-orch/SKILL.md:65-73
```text
## 3. 질문 대리 [검증]

- `blocked` 에서는 `agent read --lines N` 이 `agent_not_idle` 로 거절된다. `--source visible` 로 읽는다.
- 화면의 질문·선택지·tab 의 진행 요약을 그대로 옮겨 AskUserQuestion 으로 사용자에게 묻는다.
  질문 제목에 `[#<N> tab 질문 대리]` 를 붙이고, 선택지 설명에 tab 쪽 번호를 적는다. 오케스트레이터의 권장 판단이 있으면 함께 적는다.
- 답 넣기: `agent send-keys issue-<N> down ... enter`. 키마다 화면을 다시 읽어 커서 위치(`❯`)를 확인한 뒤 enter.
  렌더링이 늦으니 `pane wait-output <pane> --match "❯ <번호>." --source visible --timeout 3000` 으로 확인한다.
- 질문이 여러 개(상단 `☐ A ☐ B ✔ Submit`)면 하나 답할 때마다 다음 질문이 나타난다. 마지막 `Review your answers` 에서 사용자 답과 일치하는지 확인하고 `Submit answers`.
- 답 넣은 직후 `agent wait` 는 아직 `blocked` 로 읽혀 바로 반환된다. `pane wait-output --match "User answered"` 로 반영을 확인한 뒤 기다린다.
```

## skills/herdr-orch/references/codex.md:83-92
```text
## 3. 질문 대리 [검증: cx merge tab / 미검증: cx 이슈 tab]

- 화면은 `agent read issue-<N> --source visible` 로 읽는다.
- 화면의 질문·선택지·tab 의 진행 요약을 그대로 옮겨 일반 메시지로 사용자에게 묻고 턴을 끝낸다. 답이 오기 전에는 tab 에 아무것도 보내지 않는다.
  질문 제목에 `[#<N> tab 질문 대리]` 를 붙이고, 선택지 설명에 tab 쪽 번호를 적는다. 오케스트레이터의 권장 판단이 있으면 함께 적는다.
- 답 넣기: `agent prompt issue-<N> "<답>"`. tab 이 번호 선택지를 적었으면 번호와 선택지 문구를 함께 보낸다(예: `"2. 검토 승인하고 PR 만들기"`).
  여러 질문을 한 번에 물었으면 질문별 답을 한 메시지에 번호를 맞춰 담는다.
- 보낸 뒤 `agent wait issue-<N> --until working --timeout 15000` 으로 답이 받아들여졌는지 확인한다.
  `blocked` 로 선택 화면(커서 `❯`)이 떠 있으면 그 화면은 `agent send-keys issue-<N> down ... enter` 로 고르고, 키마다 화면을 다시 읽어 커서 위치를 확인한 뒤 enter 한다.
- 사용자가 항상 같은 답을 고르는 질문은 해당 스킬 쪽을 고치는 이슈로 올린다(예: #82). 다만 tab 세션은 설치된 플러그인 버전의 스킬을 읽으므로, 고친 스킬이 배포되기 전까지는 오케스트레이터가 아래 "자동 응답" 표대로 답한다.
```
