## 작업 요약

Codex 에서 `issue-merge` 의 merge 에이전트 세 개를 지정한 모델로 바꿨습니다. 충돌 해소 에이전트(`yamato-merge-resolver`)는 해소가 실패할 때마다 한 단계씩 더 강한 설정으로 올라갑니다. 단계는 `gpt-6.1-sol` low → medium → high → `gpt-6-astra` low → medium 입니다. Claude 쪽(`opus`)은 그대로입니다.

설정과 문서만 바뀌고 화면이나 API 변경이 없어서 캡처는 생략했습니다. 변경 근거는 grep 결과와 Codex 실측 세션 기록으로 남깁니다.

## 변경 전후

| 항목 | 전 | 후 |
| --- | --- | --- |
| analyst `.toml` | `gpt-5.6-terra` / medium | `gpt-6.1-terra` / medium |
| critic `.toml` | `gpt-5.6-sol` / medium | `gpt-6.1-sol` / high |
| resolver `.toml` | `gpt-5.6-sol` / medium 고정 | model 없음. 스폰할 때 사다리 단계를 넘김 |
| `SKILL.md` codex-model 표기 | luna / terra / luna (실제 값과 다름) | `.toml` 과 일치. resolver 사다리 표 추가 |
| 해소 재시도 | 같은 설정으로 최대 2회, 이전 결과 전달 없음 | Codex 사다리 5단계. 단계마다 이전 출력·diff·실패 사유 전달 |
| critic 이 해소 결과를 block | plan.md 수정만 | resolver 를 다음 단계로 올림 |

원본: `.issue/149/evidence/before/`, `.issue/149/evidence/after/`

## 설계 근거 (실측)

`.toml` 에 model 이 고정된 역할은 `spawn_agent` 의 `model`/`reasoning_effort` 덮어쓰기가 무시됩니다. 그래서 resolver `.toml` 에서만 model 을 뺐습니다. 수정한 `.toml` 세 개로 Codex 0.167.0 에서 띄운 결과입니다(`after/03-codex-probe.txt`).

| 호출 | 넘긴 값 | 실제로 돈 값 |
| --- | --- | --- |
| resolver 1단계 | `gpt-6.1-sol` low | `gpt-6.1-sol` low |
| resolver 5단계 | `gpt-6-astra` medium | `gpt-6-astra` medium |
| analyst | 없음 | `gpt-6.1-terra` medium |
| critic | 없음 | `gpt-6.1-sol` high |

## 변경 파일

- `agents/shikamaru-merge-analyst.toml`, `agents/itachi-merge-critic.toml`. 모델과 effort 변경.
- `agents/yamato-merge-resolver.toml`. model 과 effort 줄 삭제.
- `skills/issue-merge/SKILL.md`. subagents 표기, 재시도 규칙, 서브에이전트 표, "Codex resolver 사다리" 절, 흐름도.
- `skills/issue-merge/references/merge-plan.md`. 5절에 단계를 올리는 조건, 넘길 맥락, 상한 표. 6절 block 처리.
- `skills/issue-merge/references/verify-and-close.md`. 재시도 상한을 5절로 연결.

## 검증

- grep 으로 `.toml` 과 문서 값 일치 확인 (`after/01-toml.txt`, `after/02-skill-refs.txt`).
- Codex 실측으로 네 호출 모두 의도한 모델로 실행 확인 (`after/03-codex-probe.txt`).
- `node --test skills/issue-merge/scripts/issue-unblocked.test.mjs` 6건 통과 (`after/04-tests.txt`).

## 남은 이슈

- 사다리는 오케스트레이터가 스폰 인자를 넘겨야 동작합니다. 넘기지 않으면 부모 모델을 물려받습니다. 지침에만 적혀 있고 스크립트로 강제하지는 않습니다.
- resolve 스크립트와 resolver 지침 개선은 [#150](https://github.com/Mineru98/samsara/issues/150) 에서 다룹니다.
- `neji-verifier` 의 Codex 모델은 범위 밖입니다.
