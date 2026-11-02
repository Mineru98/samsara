## 작업 요약

issue-start 12단계와 issue-end 7단계(메인 체크아웃 최신화)에서 `sync-base` 가 `skipped: "dirty"` 를 돌려주면 더는 묻지 않습니다. 스킬 절차가 바로 `stash push -u -m "issue-sync"` → `sync-base` → `stash pop` 을 실행하고, 되돌리기까지 끝내야 완료로 칩니다. `sync-base` 스크립트는 바꾸지 않았습니다.

문제가 생길 때만 멈추고 묻습니다. 대상은 두 경우입니다. 하나는 되돌리기(`stash pop`)가 충돌해 변경이 stash 에 남은 경우이고, 다른 하나는 두 번째 `sync-base` 가 다시 `skipped`·`conflict`·`error` 를 돌려준 경우입니다(이때 변경은 복원돼 있습니다). 두 경우 모두 변경이 어디에 있는지와 꺼내는 명령을 함께 안내합니다.

리뷰 레인 지적을 반영해 두 가지를 더 넣었습니다. 첫째, 병렬 세션 보호를 위해 맨 위 stash 가 아니라 **방금 만든 `issue-sync` 항목만** 골라 꺼냅니다. 아무것도 치워지지 않았으면 꺼내지 않습니다. 둘째, 마무리 보고 "동기화" 표에 자동 처리 결과 행(대상 파일 포함)을 추가했고, 더는 나올 수 없는 "dirty 건너뜀" 행은 지웠습니다.

## 변경 전후

| 시나리오 | 전 | 후 |
| --- | --- | --- |
| 메인 폴더에 추적 파일 수정 + 추적 안 된 파일 | `skipped: "dirty"` → 3지선다 질문으로 멈춤 | 질문 없이 커밋 1개 받음, `notes.txt` 수정·`untracked.txt` 그대로 복원, stash 비어 있음 |
| 다른 세션의 stash(`someone-else`)가 이미 있음 | (동일하게 질문) | 최신화·복원 완료, `someone-else` stash 는 그대로 남음 |
| 받아온 커밋이 같은 줄을 바꿔 되돌리기 충돌 | (동일하게 질문) | `stash pop` 충돌 → `issue-sync` 가 stash 에 남음 → 이 경우에만 질문 |
| `conflict`·`other-branch`·`error` | 질문 | 질문 (변경 없음) |
| `no-main-checkout` | 한 줄 안내 | 한 줄 안내 (변경 없음) |

재현은 임시 저장소(bare origin + 메인 클론 + 링크된 워크트리)에서 문서의 `bash` 블록을 **그대로 추출해** 실행했습니다.

원본: [before/01-sync-base-dirty-repro.txt](https://github.com/Mineru98/samsara/blob/dev/0.4.0-alpha/.issue/82/evidence/before/01-sync-base-dirty-repro.txt) · [after/01-auto-stash-sync-repro.txt](https://github.com/Mineru98/samsara/blob/dev/0.4.0-alpha/.issue/82/evidence/after/01-auto-stash-sync-repro.txt) · [after/repro2.sh](https://github.com/Mineru98/samsara/blob/dev/0.4.0-alpha/.issue/82/evidence/after/repro2.sh) · [after/03-acceptance-checks.txt](https://github.com/Mineru98/samsara/blob/dev/0.4.0-alpha/.issue/82/evidence/after/03-acceptance-checks.txt) · [after/04-diff.patch](https://github.com/Mineru98/samsara/blob/dev/0.4.0-alpha/.issue/82/evidence/after/04-diff.patch)

## 변경 파일

- `skills/issue-start/references/evidence-capture.md` 9절: `dirty` 자동 절차, 실패 시 질문 형식, "동기화" 보고 행
- `skills/issue-end/references/report-and-pr.md` 7단계: 위와 같은 내용(스크립트명·단계 번호만 다름, 절차 블록 동일 확인)
- `skills/issue-start/SKILL.md` 12단계 문단과 흐름도
- `skills/issue-end/SKILL.md` 규칙(`확인 없이 치워두지 않는다` → 자동 처리, `reset --hard`·브랜치 전환 확인은 유지), 흐름도, 7단계 문단

## 검증

- 완료 기준 정적 확인: dirty 3지선다 잔존 0, conflict·other-branch 질문과 `reset --hard`·브랜치 전환 규칙 유지, `*.mjs` 변경 0 ([03-acceptance-checks.txt](https://github.com/Mineru98/samsara/blob/dev/0.4.0-alpha/.issue/82/evidence/after/03-acceptance-checks.txt))
- 문서 절차 실제 실행 3 시나리오 통과 (위 표)
- `scripts/check-shared.sh` 통과, 테스트 `issue-media`·`check-shared`·`issue-version`·`derive-status`·`trusted-executable` 통과
- 실패한 테스트(`ontology`·`link-roundtrip`·`bootstrap`·`native-deps`: 새 워크트리에 `tools/issue-ontology/node_modules`(ajv) 없음 / `issue-gate` 1건: 한글 로캘 `ps` 출력 기대)는 환경 문제이며, 이번 변경은 이 테스트들이 읽지 않는 문서 4개뿐입니다.
- 별도 리뷰 레인 검토 후 지적 4건(보고 행 누락, 두 번째 실패 시 안내 모순, `no-main-checkout` 처리 서술, 맨 위 stash pop 위험)을 반영했습니다.

## 화면 캡처 생략 사유

UI 변경이 없는 스킬 절차 문서 변경입니다. 그래서 스크린샷 대신 재현 로그와 정적 확인 결과를 증거로 커밋했습니다.

## 남은 이슈

- 기존 `.issue/settings.json` 의 `git.baseBranch` 가 `main` 이라, 이번 실행은 최근 PR(#81)과 같이 `dev/0.4.0-alpha` 를 `--base` 로 지정했습니다.
