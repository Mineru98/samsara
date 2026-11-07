## 작업 요약

`issue-create.mjs` 인자 파싱에서 `--label` 값의 앞뒤 공백을 다듬고 빈 값은 버리도록 고쳤습니다.
이제 빈 라벨은 성격 라벨로 세지 않아 `create` 가 기존 "성격 라벨 없음" 경로로 exit 2 에서 멈추고, `label` 명령도 빈 라벨을 붙이지 않습니다.
기준 브랜치는 `dev/0.4.0-alpha` 입니다.

## 원인

- `skills/issue-create/scripts/issue-create.mjs` 인자 파싱이 `--label` 값을 그대로 `opts.labels` 에 넣었습니다.
- `typeLabels`(`issue-common.mjs`)는 `status:*` 만 거르므로 빈 문자열이 성격 라벨 하나로 세어져 검사를 통과했습니다.

## 변경 전후 (CLI, `--dry-run`)

| 호출 | 전 | 후 |
| --- | --- | --- |
| `create --label ""` | EXIT=0, `gh issue create ... --label ` | EXIT=2, 성격 라벨 없음 |
| `create --label "  "` | EXIT=0, `--label "  "` 전달 | EXIT=2, 성격 라벨 없음 |
| `create --label "" --label bug` | `--label  --label bug` 전달 | `--label bug` 만 전달 |
| `label 1 --label ""` | EXIT=0, `#1 ← ` (빈 라벨 부착) | EXIT=1, `--label 또는 --remove-label 이 하나 이상 필요하다` |

원본 출력: [before/cli.txt](https://github.com/Mineru98/samsara/blob/main/.issue/136/evidence/before/cli.txt) · [after/cli.txt](https://github.com/Mineru98/samsara/blob/main/.issue/136/evidence/after/cli.txt)

CLI 동작 변경이라 화면 캡처 대신 동일 명령의 터미널 출력을 증거로 남겼습니다(같은 명령 스크립트로 수정 전 `a890bea` / 수정 후 `9fdf367` 에서 실행).

## 변경 파일

- `skills/issue-create/scripts/issue-create.mjs` — `--label` 값 trim, 빈 값 버림 (create·label 공통 경로)
- `skills/issue-create/scripts/issue-create.test.mjs` — 회귀 테스트 3건 추가

`issue-common.mjs` 사본들의 `typeLabels` 는 파싱 단계에서 해결되므로 건드리지 않았습니다.

## 검증

- `node --test skills/issue-create/scripts/issue-create.test.mjs` — 수정 후 3/3 통과, 수정 전 코드에서는 3/3 실패 확인 ([after/test.txt](https://github.com/Mineru98/samsara/blob/main/.issue/136/evidence/after/test.txt))
- `node --test scripts/check-shared.test.mjs` — 12/12 통과

## 완료 기준

- [x] `create --label ""` 와 `--label "  "` 가 exit 2 로 끝나고 이슈를 만들지 않는다
- [x] 정상 라벨과 빈 라벨이 섞이면 빈 라벨만 버리고 등록한다
- [x] `label` 명령도 빈 라벨을 붙이려 하지 않는다
