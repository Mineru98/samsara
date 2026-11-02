## 작업 요약

이슈 본문에 `## 선행 이슈`(`- [ ] #N` / `- [x] #N`)와 `## 후행 이슈`(`- #N`)를 양방향으로 남기는 흐름을 추가했습니다.
`issue-create` 는 등록할 때(`--after` / `--before`), 또는 등록한 뒤(`link`) 상대 이슈 본문에도 반대 방향 링크를 넣습니다.
`issue-start` 는 착수 0단계에서 `prereq` 로 선행 이슈 상태를 실제로 조회합니다. 선행이 끝나지 않았으면 exit 3 으로 막습니다.
`issue-merge close` 는 이슈를 닫은 뒤 후행 이슈 본문의 선행 항목을 `[x]` 로 자동으로 바꿉니다. 다시 실행해도 결과가 같습니다.

## 변경 전후

| 전 | 후 |
| --- | --- |
| ![선행·후행 연결 명령 - 전](https://raw.githubusercontent.com/Mineru98/samsara/dev/0.4.0-alpha/.issue/98/evidence/before/01-dependency-links.webp) | ![선행·후행 연결 명령 - 후](https://raw.githubusercontent.com/Mineru98/samsara/dev/0.4.0-alpha/.issue/98/evidence/after/01-dependency-links.webp) |

빨간 박스가 비교 구간입니다.
변경 전에는 `prereq` · `link` · `--after` · `mark-successors` 가 모두 "알 수 없는 모드/옵션" 으로 거절됐고, 초안 템플릿에 선행 섹션도 없었습니다.
변경 후는 가짜 `gh` 시나리오로 확인했습니다. 시나리오의 각 단계와 결과는 다음과 같습니다.

- 없는 선행(#999)을 지정하면 이슈를 만들지 않습니다(exit 2).
- 닫힌 #61 을 선행으로 지정하면 `[x]` 로 기록되고, #61·#70 본문에 반대 방향 링크가 들어갑니다.
- 형제 #101 은 등록한 뒤 `link` 로 이었습니다. 같은 `link` 를 다시 돌리면 본문을 쓰지 않습니다.
- `prereq 101` 은 #100 이 열려 있을 때 exit 3 이었습니다. `close --issue 100` 뒤에는 #70·#101 의 `- [ ] #100` 이 `- [x]` 로 바뀌었고 exit 0 이 됐습니다.
- 섹션이 없는 #61 은 `PREREQ_TOTAL=0` 으로 그대로 통과했습니다(하위 호환).

화면 변경이 없는 CLI·문서 작업이라 터미널 출력을 렌더링해 캡처했습니다. 원본은 `.issue/98/evidence/{before,after}/01-dependency-links.txt` 입니다.

## 변경 파일

- `skills/{issue-create,issue-start,issue-merge}/scripts/issue-links.mjs` — 새 공용 모듈(세 사본이 바이트 단위로 같음). 본문 파싱·갱신과 `prereqStatus` / `linkIssues` / `markSuccessors` 를 담았습니다. 코드 펜스를 무시하고, `없음` 자리표시를 지우며, CRLF 를 보존합니다
- `skills/*/scripts/issue-tracker.mjs` (create/end/merge/start) — `issueEditBody` 추가 (github: `gh issue edit --body-file -`, jira: description PUT)
- `skills/issue-create/scripts/issue-create.mjs` — `create --after/--before` (여러 번 지정 가능), `link` 모드 추가. 실패 시 `LINK_FAILED=` / `LINK_RETRY=` 출력
- `skills/issue-start/scripts/issue-start.mjs` — `prereq <n>` 추가. exit 0 은 통과, exit 3 은 선행 미완료·조회 실패, exit 4 는 인증·조회 실패
- `skills/issue-merge/scripts/issue-merge.mjs` — `close` 뒤 후행 체크를 자동 실행하고, 결과를 JSON `successors` / `successorsOk` 에 담습니다. 실패하면 exit 5 와 함께 재시도용 `mark-successors` 를 안내합니다
- 문서: issue-create `issue-draft.md`(기본 틀·형식), `create-and-handoff.md`(옵션·`link`·형제 연결 절차), `split-requests.md`, `SKILL.md`. issue-start `SKILL.md` 0단계(`prereq` 필수 호출 + AskUserQuestion). issue-merge `SKILL.md`(non-goal 예외)·`verify-and-close.md` 5절
- `scripts/check-shared.sh` · `check-shared.test.mjs` — `issue-links.mjs` 세 사본의 drift 검사

## 검증

- `node --test skills/issue-start/scripts/issue-links.test.mjs` 22/22 통과. 대상은 파싱, 멱등성, 하위 호환, 펜스, CRLF, `###` 섹션, 번호 검증, 가짜 tracker 로 돌린 prereq/link/markSuccessors 입니다
- 전체 `node --test scripts/*.test.mjs skills/*/scripts/*.test.mjs` 결과는 224건 중 200건 통과, 24건 실패입니다. 실패한 24건은 변경 전 기준선(203건 중 24건 실패, onboard 캐시·locale 테스트)과 목록이 같습니다. 새로 생긴 실패는 없습니다
- `sh scripts/check-shared.sh` 통과 (`issue-links: 3 copies match`)
- 가짜 `gh` 로 create → link → prereq → close → mark-successors 흐름 전체를 재현했습니다(위 after 이미지). 실제 GitHub 이슈로는 실행하지 않았습니다

## 남은 이슈

- Jira: `issueEditBody` 는 본문을 Markdown 으로 바꿨다가 ADF 로 되돌려 description 전체를 덮어씁니다. 그래서 변환기가 다루지 않는 서식(표·멘션·중첩 목록 등)이 사라질 수 있습니다. `#N` 표기도 GitHub 기준이라, Jira 연결은 best-effort 입니다.
- `create` 에서 상대 이슈 링크가 실패해도 이슈는 이미 만들어졌으므로 exit 0 을 유지합니다. 재실행하면 이슈가 중복으로 생기기 때문입니다. 실패 여부는 `LINK_FAILED=` 로 판단하고 `LINK_RETRY=` 를 실행합니다.
- `\r\n` 과 `\n` 이 섞인 본문은 처음 갱신할 때 CRLF 로 통일되며 한 번 더 저장됩니다. 그 뒤로는 멱등입니다.
- 닫힘 사유(not planned·duplicate)와 관계없이, 닫힌 선행은 모두 완료로 봅니다.
- issue-sync / issue-viz 가 새 섹션을 읽는 일은 이슈의 범위 제외에 따라 하지 않았습니다. issue-onboard 의 `depends on #N` 마커와도 아직 연동하지 않았습니다.
- `contracts/issue-phase-capability-bundle-v1.json` 의 closure 해시는 이번 변경 전부터 SKILL.md 등과 이미 어긋나 있었고, 이번에도 갱신하지 않았습니다.
