## 작업 요약

`start-tab.sh` 가 codex "Update available" 화면을 `UPDATE_PROMPT`(exit 2)로 알리고, 업데이트는 스스로 고르지 않습니다. 첫 프롬프트가 입력창에 글자로만 남고 `working` 이 아니면 `enter` 를 한 번 더 보내 다시 확인합니다. 대기 시간은 `START_TIMEOUT_MS`(기본 15000)로 바꿀 수 있습니다.
`start-finish.sh` 는 13단계 다음 행동이 "검토 후 issue-end / 바로 issue-end" 로 떠도 "바로 issue-end" 를 고릅니다("검토 후" 는 고르지 않음 — 착수 단계에서 정한 결정).
SKILL.md·codex.md 에 업데이트 화면 처리, 6단계 merge tab 의 exit 2·3 처리, `[#<N> tab 질문 대리]` 표기의 신뢰 질문, 자동 응답표 13단계 문구 규칙을 넣었습니다.

## 변경 전후

화면(UI) 변경이 없는 셸 스크립트·문서 작업이라 스크린샷 대신 출력 증거를 남깁니다. 업데이트 화면·Enter 누락은 재현이 어려워, herdr 0.9.3 CLI 응답을 흉내 내는 fake `herdr`(PATH 앞에 둔 셸 스텁)로 같은 시나리오를 변경 전후에 돌렸습니다. 정상 경로는 실제 herdr 에서도 확인했습니다.

| 경우 | 전 | 후 |
| --- | --- | --- |
| codex 업데이트 화면 (fake) | `NOT_STARTED:idle` / exit 3 — 화면 종류 구분 없음 | `UPDATE_PROMPT` / **exit 2**, 프롬프트 전송 전에 감지 |
| Enter 누락 — 입력창에 프롬프트만 남음 (fake) | `NOT_STARTED:idle` / exit 3 | `enter` 재전송 → `working` / **exit 0** |
| 재전송해도 착수 안 됨 (fake) | `NOT_STARTED:idle` / exit 3 | `NOT_STARTED:idle` / exit 3 (재전송은 프롬프트가 보일 때만) |
| 화면 없음 (fake) | `working` / exit 0 | `working` / exit 0 |
| 화면 없음 (실제 herdr, 이 워크트리) | - | `working` / exit 0 (7초) |
| `START_TIMEOUT_MS=40000` (fake) | `--timeout 15000` 고정 | `--timeout 40000` 전달 |
| 13단계 정본 문구 "검토 승인하고 PR 만들기" (fake) | `submitted` / exit 0 | `submitted` / exit 0 |
| 13단계 "검토 후 issue-end / 바로 issue-end" (fake) | `STOP: 예상 밖 선택지` / exit 1 | `down` → "바로 issue-end" → `submitted` / **exit 0** |

원본 출력: `.issue/146/evidence/before/fake-run.txt`, `.issue/146/evidence/after/fake-run.txt`, `.issue/146/evidence/after/real-start-tab.txt`, 문서 발췌 `.issue/146/evidence/after/docs.txt`
fake herdr 와 실행기: `.issue/146/evidence/fake/bin/herdr`, `.issue/146/evidence/fake/run.sh` (`run.sh <워크트리>` 로 같은 표를 다시 만들 수 있습니다)

## 변경 파일

- `skills/herdr-orch/scripts/start-tab.sh` — 업데이트 화면 감지(`gate`), Enter 누락 재전송 1회, `START_TIMEOUT_MS`
- `skills/herdr-orch/scripts/start-finish.sh` — 13단계 "바로 issue-end" 매칭
- `skills/herdr-orch/SKILL.md` — 1단계 출력 표·timeout 근거·업데이트 화면 처리·신뢰 질문 표기, 3단계 자동 응답표, 6단계 merge tab exit 처리
- `skills/herdr-orch/references/codex.md` — 같은 내용의 Codex 판

## 검증

- `bash -n` 두 스크립트 통과
- 위 표의 fake 시나리오 8개와 실제 herdr 정상 경로 1회 실행, 테스트 tab 은 닫음
- 저장소에 herdr-orch 자동 테스트는 없습니다

## 남은 이슈

- 업데이트 화면 감지는 `Update available` 과 `Update now`/`Skip until next version` 문구가 함께 보일 때입니다. 실제 codex 화면으로는 아직 확인하지 못해 문서에 `[미검증]` 으로 적었습니다.
- Enter 누락 재전송은 fake 화면으로만 확인했습니다. 재전송 조건은 화면에 프롬프트 첫 줄이 보이는 것이라, 이미 제출돼 끝난 경우에는 빈 입력창에 `enter` 가 한 번 더 갈 수 있습니다(무해).
