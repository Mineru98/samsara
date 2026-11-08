## 작업 요약

main(61120ba) 의 `skills/herdr-orch` 를 그대로 `dev/0.4.0-alpha` 에 가져왔습니다(커밋 1).
그 위에 0.4.0 issue-* 와 어긋나는 세 곳을 고쳤습니다(커밋 2). 이슈를 쓴 뒤 main 에 herdr-orch 커밋이 5개(#141 #142 #159 등) 더 쌓여서, 본문의 3커밋이 아니라 최신 main 을 기준으로 삼았습니다.
문서·셸 스크립트만 바뀌어 화면 캡처는 의미가 없습니다. 대신 명령 출력을 텍스트 증거로 남겼습니다.

## 0.4.0 과 다른 점과 처리

| 위치 | main(0.3.x) 기준 내용 | 0.4.0 동작 | 처리 |
| --- | --- | --- | --- |
| `SKILL.md` 자동 응답 표 — 메인 최신화 `dirty` | 질문에 "잠시 치워두고 받아오기" 로 답 | dirty 는 묻지 않고 치워두고 받아온 뒤 방금 만든 stash 만 꺼냄 | 0.4.0 에서 이 질문이 뜨는 경우(되돌리기 충돌·conflict 등)는 사용자에게 묻는다고 명시 |
| `SKILL.md` 기준 브랜치 주의 | "#90(PR #94) 이 배포되기 전에는…" | PR #94 가 dev 에 merge 됨. 5·10단계에서 `BASE_DRIFT` 질문으로 멈춤 | 증거가 올라갈 브랜치를 정하는 질문이라 자동 응답하지 않는다고 명시. 0.3.x 설치본 주의는 유지 |
| `scripts/start-finish.sh` | 2문항 화면(메인 최신화 + 다음 행동)과 Submit 화면을 전제 | 마지막 화면은 다음 행동 1문항뿐이고 Submit 화면이 없음 | 답이 들어간 뒤 `down` 키 7번을 입력창에 보내고 exit 1 로 끝나던 문제를 고침. 선택지 창이 사라졌는지로 반영을 확인 |

## 변경 전후

| 항목 | 전 | 후 |
| --- | --- | --- |
| dev 의 `skills/` 에 herdr-orch | 없음 | 있음 |
| start-finish · 2문항(finish-canon / finish-variant) | exit 0 / exit 0 | exit 0 / exit 0 (보낸 키 동일) |
| start-finish · 1문항(finish-single) | exit 1, `down` 7번 오입력 | exit 0, `enter` 1번 |
| start-finish · 1문항 + 화면 위 예전 답(finish-single-scroll) | exit 1 | exit 0 |

가짜 herdr 는 #146 의 것에 0.4.0 1문항 시나리오 두 개를 더했습니다.

- 전: [01-skills-dirs.txt](https://github.com/Mineru98/samsara/blob/dev/0.4.0-alpha/.issue/135/evidence/before/01-skills-dirs.txt) · [02-start-finish.txt](https://github.com/Mineru98/samsara/blob/dev/0.4.0-alpha/.issue/135/evidence/before/02-start-finish.txt)
- 후: [01-skills-dirs.txt](https://github.com/Mineru98/samsara/blob/dev/0.4.0-alpha/.issue/135/evidence/after/01-skills-dirs.txt) · [02-start-finish.txt](https://github.com/Mineru98/samsara/blob/dev/0.4.0-alpha/.issue/135/evidence/after/02-start-finish.txt) · [03-diff-vs-main.txt](https://github.com/Mineru98/samsara/blob/dev/0.4.0-alpha/.issue/135/evidence/after/03-diff-vs-main.txt) · [04-checks.txt](https://github.com/Mineru98/samsara/blob/dev/0.4.0-alpha/.issue/135/evidence/after/04-checks.txt)
- 재현 도구: [fake/run.sh](https://github.com/Mineru98/samsara/blob/dev/0.4.0-alpha/.issue/135/evidence/fake/run.sh) · [fake/bin/herdr](https://github.com/Mineru98/samsara/blob/dev/0.4.0-alpha/.issue/135/evidence/fake/bin/herdr)

## 변경 파일

- `skills/herdr-orch/**` — main 사본 그대로 추가(10개 파일)
- `skills/herdr-orch/SKILL.md` — 자동 응답 표의 dirty 행, start-finish 설명, 기준 브랜치 주의 문단
- `skills/herdr-orch/scripts/start-finish.sh` — 1문항(Submit 화면 없음) 반영 인식, enter 직후 화면 변화를 기다림

main 대비 남는 차이는 이 두 파일, 17줄 추가 5줄 삭제뿐입니다([03-diff-vs-main.txt](https://github.com/Mineru98/samsara/blob/dev/0.4.0-alpha/.issue/135/evidence/after/03-diff-vs-main.txt)).

## 검증

- `bash -n`(셸 6개), `python3 -m py_compile`(파이썬 2개) 통과
- `sh scripts/check-shared.sh` 통과, `node --test scripts/*.test.mjs` 40/40 통과
- 매니페스트는 `"skills": "./skills/"` 로 폴더를 자동 탐색하므로 고칠 목록이 없습니다. 계약 해시(`skills/issue-*/contracts`)는 herdr-orch 를 대상으로 하지 않습니다.
- 진짜 herdr tab 에서 start-finish 를 돌려 보지는 않았습니다. 가짜 herdr 4개 시나리오로만 확인했습니다.

## 남은 이슈

- 완료 기준 1(`origin/dev/0.4.0-alpha` 의 `skills/` 에 herdr-orch 가 나온다)은 PR 이 merge 된 뒤에 충족됩니다. 지금은 작업 브랜치 HEAD 에서만 확인했습니다.
- 이 저장소 워크트리의 `.issue/settings.json` 기준 브랜치가 `main` 으로 다시 감지돼 있었습니다. 증거 미러는 `--base dev/0.4.0-alpha` 를 직접 넘겨 dev 로 올렸습니다.
