## 작업 요약

`watch.sh` 가 `--interval` 말고는 옵션을 몰라서 `--merging` 같은 인자를 agent 이름으로 조회하고 `EVENT --merging gone` 으로 끝나던 문제를 고쳤습니다. 구현 커밋은 `8f3a020` 입니다.

`--merging` 은 원래 `merge-candidates.py` 의 옵션(다른 merge tab 이 돌고 있으면 `action=wait`)입니다. merge tab 감시에 별도 done/idle 판정은 필요 없어서(감시기는 done/idle 에서 멈추고 화면 확인은 오케스트레이터가 함) 스크립트에 구현하지 않고 문서를 고쳤습니다.

## 변경 전후

| 호출 | 변경 전 | 변경 후 |
|---|---|---|
| `watch.sh --interval 1 --merging merge-1-4` | `EVENT --merging gone`, exit 0 | usage, **exit 2** |
| `watch.sh --bogus x` | `EVENT --bogus gone`, exit 0 | usage, **exit 2** |
| `watch.sh --interval 1 merge-1-4` (문서의 새 merge tab 감시 호출) | working → done, `EVENT merge-1-4 done` | 동일 (gone 오판 없음) |
| `watch.sh --interval 1 issue-1 issue-2` | 두 agent 모두 상태 출력 후 `EVENT issue-1 done` | 동일 |
| `watch.sh merge-1-4` (기본 15초) | 첫 상태 출력 후 10초 timeout(124) | 동일 |
| `watch.sh` (인자 없음) / `watch.sh --interval` (값 없음) | — | usage, exit 2 |

## 변경 파일

- `skills/herdr-orch/scripts/watch.sh` — `-` 로 시작하는 모르는 인자·agent 0개·값 없는 `--interval` 이면 usage 를 stderr 로 내고 exit 2
- `skills/herdr-orch/SKILL.md`, `skills/herdr-orch/references/codex.md` — "`--merging` 을 켠 채 감시한다" → "`scripts/watch.sh --interval 15 merge-<N>-<M>` 으로 감시하고, 그동안 판정을 다시 돌릴 때는 `merge-candidates.py` 에 `--merging` 을 붙인다"

## 검증

- `bash -n watch.sh` 통과
- PATH 앞에 둔 fake `herdr` 스텁(`merge-*`/`issue-*` 만 존재, 첫 조회 working → 이후 done)으로 같은 명령 세트를 변경 전후에 실행

## 증거

화면이 없는 CLI 스크립트 작업이라 webp 캡처 대신 터미널 출력 원본을 남겼습니다.

- `.issue/129/evidence/before/watch-cli.txt`, `.issue/129/evidence/after/watch-cli.txt` — 같은 명령 세트의 전후 출력 (시각은 `HH:MM:SS` 로 치환)
- `.issue/129/evidence/before/docs-grep.txt`, `.issue/129/evidence/after/docs-grep.txt` — `--merging` 등장 위치 전후

## 남은 이슈

- 실제 herdr merge tab 대상 감시는 fake agent 로 대신했습니다. 감시 로직(상태 조회·판정)은 바뀌지 않았고 인자 파싱만 바뀌었습니다.
