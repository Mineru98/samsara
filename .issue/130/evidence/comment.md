## 작업 요약

`start-tab.sh` 가 첫 프롬프트를 보내기 전후로 화면을 읽어, 폴더 신뢰 화면이면 `TRUST_PROMPT`, codex hooks 화면이면 `HOOKS_REVIEW` 를 exit 2 로 알립니다. 선택지는 스스로 고르지 않습니다.
프롬프트는 `agent prompt --wait --until working` 으로 착수까지 확인하며, `working` 이 되지 않으면 정상 출력 대신 `NOT_STARTED:<status>`(exit 3)를 냅니다.
SKILL.md 1단계에 출력 표와 신뢰 화면 처리·재전송 절차를 넣었고, codex.md 에도 같은 내용을 반영했습니다.

## 변경 전후

화면(UI) 변경이 없는 셸 스크립트·문서 작업이라 스크린샷 대신 실측 출력을 남깁니다. herdr 0.9.3, `cc --mode=dev --model=opus` 기준입니다.

| 경우 | 전 | 후 |
| --- | --- | --- |
| 신뢰하지 않은 새 폴더 (`git init` 직후) | `... blocked` / exit 0 (프롬프트가 사라짐. 코드상 대기 루프가 `blocked` 에서 빠지지 않아 최대 60초 뒤 전송) | `... TRUST_PROMPT` / **exit 2** (7초, 전송 전에 감지) |
| 이미 신뢰한 폴더 (이 저장소) | `... working` / exit 0 | `... working` / exit 0 (10초) |
| 프롬프트가 들어가지 않음 | 그대로 상태를 출력 | `NOT_STARTED:<status>` / **exit 3** |

두 경우 모두 스크립트가 끝난 뒤에도 화면은 `❯ No, exit` / `Yes, I trust this folder` 선택지에 그대로 머물러 있었습니다. 스크립트는 선택하지 않았습니다.

원본 출력: `.issue/130/evidence/before/start-tab-untrusted.txt`, `.issue/130/evidence/after/start-tab.txt`

## 재전송 절차 실측

오케스트레이터가 만든 테스트 폴더에서 SKILL.md 절차대로 `down` → `enter` 로 신뢰를 고르고 첫 프롬프트를 다시 보냈습니다. 기동 직후라 Enter 가 빠져서, 입력창에 글자만 남고 `working` 이 되지 않았습니다. 이번 수정의 확인 조건이 바로 이 경우를 잡아냈습니다. `enter` 를 한 번 더 보내자 `working` 이 됐고 응답이 왔습니다. 이 보완 단계도 절차 5번으로 적어 두었습니다.

## 변경 파일

- `skills/herdr-orch/scripts/start-tab.sh` — 화면 감지(`gate`), `--until working` 착수 확인, 기동 대기 루프에 `blocked` 추가
- `skills/herdr-orch/SKILL.md` — 1단계 출력 표, 폴더 신뢰 화면 처리 절차, 6단계 hooks 항목에 `HOOKS_REVIEW` 연결
- `skills/herdr-orch/references/codex.md` — 1단계 수동 절차·출력 상태, 6단계 hooks 항목

## 검증

- `bash -n skills/herdr-orch/scripts/start-tab.sh` 통과
- 위 표의 세 경우를 herdr 안에서 실제로 실행했고, 테스트 tab 은 모두 닫았습니다
- 저장소에 자동 테스트 스크립트는 없습니다

## 남은 이슈

- `HOOKS_REVIEW` 감지는 codex 화면 문구("hooks need review")로 정규식만 넣었고 실측은 못 했습니다(`[미검증]` 표시).
- 수정 대상 브랜치는 herdr-orch 가 있는 `main` 입니다. 저장소 설정의 기준 브랜치가 `dev/0.4.0-alpha` 로 고정돼 있어 `--base main` 으로 워크트리를 만들었습니다.
