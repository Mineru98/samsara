## 작업 요약

내부 스킬 `issue-review` 를 추가했습니다. 프론트매터에 `user-invocable: false` 를 두어 사용자가 직접 부르지 않고 `issue-start` 와 `issue-end` 만 호출합니다.
`ocr`(Open Code Review) CLI 의 delegation 모드가 리뷰 대상 파일과 규칙을 정하고, 구현 세션과 분리된 서브에이전트가 작업 브랜치의 변경을 검토합니다.

스킬 문서와 스크립트만 바뀌고 화면이나 API 변경이 없어서 캡처는 생략했습니다. 근거는 grep 결과, 테스트 출력, 그리고 이 브랜치 자체를 새 스킬로 리뷰한 실제 실행 기록입니다.

## 변경 전후

| 항목 | 전 | 후 |
| --- | --- | --- |
| `skills/` | 8개 스킬 | `issue-review` 추가 (9개) |
| `issue-start` | 8단계 커밋 뒤 곧바로 9단계 after 캡처 | 8.5단계 코드 리뷰를 거친 뒤 after 캡처 |
| `issue-end` 2단계 | 증거 완결성만 확인 | 리뷰 결과가 현재 코드 기준인지 먼저 확인 (없음 exit 3 / 낡음 exit 4) |
| 리뷰 기록 | 없음 | `.issue/<번호>/evidence/review.json` (검토한 커밋, 커버리지, 지적) |
| 테스트 | 117건 통과 | 127건 통과 (신규 10건) |
| `README.md` | 리뷰 단계 언급 없음 | 순서도 · 설치 안내 · 제4장에 반영 |

원본: `.issue/177/evidence/before/`, `.issue/177/evidence/after/`

## 이 브랜치를 새 스킬로 리뷰한 결과

`ocr` v1.14.3 을 설치해 스킬 문서에 적은 명령 그대로 실행했습니다 (`after/03-review-run.txt`).

| 회차 | 대상 커밋 | critical | high | medium | low | 조치 |
| --- | --- | ---: | ---: | ---: | ---: | --- |
| 1차 | `8129703` | 0 | 3 | 4 | 3 | high 3건과 medium · low 5건 수정 후 `36fec8d` 커밋 |
| 2차 | `36fec8d` | 0 | 0 | 3 | 3 | 통과. `review.json` 으로 확정 |

1차 리뷰가 잡은 high 3건은 다음과 같습니다 (`after/04-review-round1-findings.json`).

- 하위 디렉터리에서 실행하면 그 밖의 코드 커밋을 놓쳤습니다. 경로 기준을 저장소 루트로 고정했습니다.
- `record` 가 실행 시점의 커밋을 기록해, 리뷰 도중에 끼어든 커밋이 검토 없이 통과했습니다. `--target` 으로 리뷰한 커밋을 받아 대조하게 했습니다.
- `ocr` 이 설치된 장비에서 "ocr 없는 환경" 테스트가 실패했습니다. 테스트의 PATH 구성을 고쳤습니다.

`ocr` 은 마크다운을 `unsupported_ext` 로 제외했습니다. 리뷰 대상은 11개 파일 중 5개(`.mjs` 2개, 번들 JSON 3개)였고, `SKILL.md` 와 `README.md` 는 이 리뷰에서 검토되지 않았습니다.

## 변경 파일

- `skills/issue-review/SKILL.md`. 신규. 절차, 건너뜀 규칙, 수정 반복 상한(2회).
- `skills/issue-review/scripts/issue-review.mjs`. 신규. `target` / `record` / `skip` / `check`.
- `skills/issue-review/scripts/issue-review.test.mjs`. 신규. 10건.
- `skills/issue-start/SKILL.md`, `references/implementation.md`. 8.5단계, 단계 이름 정본, 체크리스트, 흐름도, 마무리 보고의 리뷰 행.
- `skills/issue-end/SKILL.md`, `references/evidence-recheck.md`. 2단계 리뷰 확인, 흐름도, 마무리 보고의 리뷰 행.
- `skills/issue-{start,end,merge}/contracts/issue-phase-capability-bundle-v1.json`. `SKILL.md` 변경에 따른 해시 재생성. phases 는 그대로입니다.
- `README.md`. 순서도, 설치 안내(`ocr` 선택 전제), 제4장.

## 검증

- `node --test scripts/*.test.mjs skills/*/scripts/*.test.mjs` 127건 통과 (`after/02-tests.txt`).
- `node scripts/build-phase-capability-bundle.mjs --check`, `sh scripts/check-shared.sh` 통과.
- 리뷰 뒤에 코드 커밋이 쌓이면 `check` 가 exit 4, 증거 커밋만 쌓이면 exit 0, `ocr` 이 없으면 `OCR=0` 과 건너뜀 기록 후 exit 0 인 것을 테스트로 확인.
- 코드 리뷰: reviewed. critical 0 / high 0 / medium 3 / low 3 (`review.json`).
- 새 문서의 절차를 일회용 복제본에서 그대로 따라간 기록 (`after/05-gate-walkthrough.txt`):
  - `issue-end` 2단계. 리뷰 뒤에 코드 커밋을 하나 얹자 `check` 가 `STATUS=stale`, exit 4 로 재리뷰를 요구했습니다. 낡은 커밋을 `--target` 으로 넘겨 기존 결과를 재사용하려는 `record` 도 exit 4 로 거부했습니다.
  - `ocr` 이 PATH 에 없는 경우. `target` 이 `OCR=0` 을 내고, `skip` 기록 뒤 `check` 가 `STATUS=skipped`, `SKIP_REASON=ocr 미설치`, exit 0 을 냈습니다. 마무리 보고의 리뷰 행은 `skipped: ocr 미설치` 로 채워집니다.
  - 이 이슈의 실제 `issue-end` 실행에서는 증거 커밋이 얹힌 상태로 `check` 가 exit 0 을 냈습니다.
- 한계: 설치된 플러그인(0.3.8)에는 새 단계가 없어서, 위 기록은 스킬이 자동으로 호출한 것이 아니라 새 문서의 명령을 사람이 따라 실행한 것입니다. 복제본에서 재리뷰(서브에이전트 검토) 자체는 반복하지 않았습니다. Codex 런타임에서의 동작도 확인하지 않았습니다.

## 남은 이슈

2차 리뷰의 medium · low 지적입니다. 고치지 않고 남겼습니다.

- (medium) 하위 디렉터리 테스트가 경로 기준 회귀를 잡지 못합니다. 구현은 올바르지만, 수정 전 코드로 되돌려도 테스트가 통과합니다.
- (medium) `.issue` 밖에 untracked 파일이 하나만 있어도 `check` 가 exit 4 를 냅니다. 출력에 개수만 있고 경로가 없어 원인을 알기 어렵습니다.
- (medium) `record` 가 커버리지 숫자를 실제 `ocr delegate preview` 결과와 대조하지 않습니다. 서브에이전트가 적은 값의 합만 검사합니다.
- (low) 없는 커밋을 `--target` 으로 넘기면 exit 2 가 아니라 exit 4 가 나옵니다.
- (low) 작업 트리에 미커밋 변경이 있어도 `record` · `skip` 은 성공하고, 뒤의 `check` 에서야 막힙니다.
- (low) `review.json` 을 손으로 고쳐 `findings` 가 배열이 아니면 `check` 가 exit 1 로 끝납니다.
- 리뷰 서브에이전트는 새 에이전트 정의 없이 기본 서브에이전트를 씁니다. `neji-verifier` 는 출력이 한 줄 판정으로 고정되어 있어 지적 목록을 낼 수 없습니다.
- 단계 계약의 phases 에는 8.5단계가 없습니다. `issue-end` 의 7.5단계와 같은 방식이며, 자동 오케스트레이터(`phase` API)는 리뷰를 강제하지 않습니다.
