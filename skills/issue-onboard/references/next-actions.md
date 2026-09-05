# 다음 행동 4지선다

issue-onboard 를 끝낼 때 AskUserQuestion 으로 아래를 제시한다. ready-frontier 가 비어 있지 않으면
"다음 착수" 를 첫 번째(권장)에 둔다.
호출 표기는 `/` 가 정본이다. Codex 계열에서는 `$` 접두도 같은 스킬을 가리키므로, 사용자가 쓰는 접두를 그대로 따라 적는다.

```text
1. 다음 착수 (권장)   ready-frontier 첫 이슈로 /issue-start #N 을 잇는다.
2. 명부 동기화        /issue-sync 로 graph.json 을 다시 맞춘다.
3. 의존 보강           빠진 의존을 link 로 걸고 다시 plan 을 낸다.
4. 종료               graph.json 만 갱신하고 마친다.
```

## AskUserQuestion 도구로 출력한다

이 선택지는 **반드시 AskUserQuestion 도구 호출로 낸다.** 선택지를 평문으로 늘어놓고 질문으로 끝내지 않는다 — 그러면 사용자가 클릭으로 고를 수 없다.

- `.claude/` 런타임: 위 텍스트 블록을 먼저 출력한 뒤, **같은 라벨·같은 순서로 AskUserQuestion 을 이어 호출한다.**
- `.codex/` 런타임: AskUserQuestion 이 없으므로 텍스트 블록만 출력하고 답을 기다린다.

도구 호출은 아래 JSON 형태를 그대로 따른다. 텍스트 블록의 각 줄이 그대로 매핑된다.

- `questions[0].question` — 머리표기(현재 단계) + 질문 문장 한 줄
- `questions[0].header` — 12자 이내 짧은 라벨 (예: "다음 행동")
- `questions[0].multiSelect` — `false` (하나만 고른다)
- `questions[0].options[]` — 텍스트 블록의 선택지마다 `{ label, description }`. 권장안은 label 끝에 " (권장)". 자유 입력이 필요하면 description 에 "직접 알려주세요"

```json
{
  "questions": [
    {
      "question": "issue-onboard 다음 행동 선택입니다. 무엇을 할까요?",
      "header": "다음 행동",
      "multiSelect": false,
      "options": [
        { "label": "다음 착수 (권장)", "description": "ready-frontier 첫 이슈로 /issue-start #N 을 잇는다" },
        { "label": "명부 동기화", "description": "/issue-sync 로 graph.json 을 다시 맞춘다" },
        { "label": "의존 보강", "description": "빠진 의존을 link 로 걸고 다시 plan 을 낸다" },
        { "label": "종료", "description": "graph.json 만 갱신하고 마친다" }
      ]
    }
  ]
}
```

ready 가 비어 있으면 1번 label 을 "막힌 이슈의 선행부터 착수" 로 바꾼다. 텍스트 블록과 도구 호출의 선택지가 어긋나지 않게 한다.

- ready 가 비어 있으면 1번을 "막힌 이슈의 선행부터 착수" 로 바꾼다.
- 여러 이슈가 ready 여도 한 번에 하나만 착수한다(워크트리 충돌 방지). 나머지는 안내만 한다.
- 파이프라인 순서: issue-create → issue-start → issue-end → issue-merge. issue-onboard 는 그 위에서
  "무엇을 다음에 할지" 를 정하는 계획 레이어다.
