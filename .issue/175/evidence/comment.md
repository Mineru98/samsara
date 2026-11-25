## 작업 요약

시각적 결정이 걸린 AskUserQuestion 앞에 "시안 작성 → 배포 → 본문 링크 먼저 → 질문" 순서를 강제하는 내부 레퍼런스를 추가했습니다.
Tailscale 이 로그인돼 있으면 tailnet HTTPS 로, 없으면 localhost 로 띄우며, 질문·선택지에는 링크를 넣지 않습니다.

## 증거를 캡처하지 않은 이유

화면이 바뀌는 작업이 아니라 스킬 문서·셸 스크립트·계약 번들만 바뀌었습니다. 캡처 대신 검증 명령의 원본 출력을 남깁니다.

- 원본 출력: `.issue/175/evidence/after/verify.txt`

## 변경 파일

- `skills/{issue-start,issue-create,issue-end,issue-merge}/references/visual-decision.md` — 해당 조건, 5단계 절차, 링크 금지, 실패 시 ASCII 목업 대체
- `skills/{issue-start,issue-create,issue-end,issue-merge}/scripts/prototype-serve.sh` — tailscale 로그인 시 tailnet, 아니면 localhost 로 서빙
- `skills/*/SKILL.md` — routing 과 hard-rule 한 줄씩 추가
- `skills/{issue-start,issue-end,issue-merge}/contracts/issue-phase-capability-bundle-v1.json` — SKILL.md 변경에 따른 재생성

## 검증

- `build-phase-capability-bundle.mjs --check` — closure 41/41, 번들 사본 3개 일치
- `check-shared.sh` — 공유 블록 4개 일치
- `node --test` — 23건 통과, 실패 0건
- `prototype-serve.sh` tailscale 경로 — tailnet URL 이 HTTP 200
- localhost 경로 — tailscale 이 없는 PATH 로 흉내 내서 `http://localhost:<port>/` 200 확인 (구현 시 수동 실행, verify.txt 에는 미포함)

## 남은 이슈

- 네 스킬의 레퍼런스·스크립트 사본은 `check-shared.sh` 검사 대상이 아니라 서로 달라져도 잡히지 않습니다.
- `herdr-orch` 가 하위 탭 질문을 대신 전달할 때 본문 링크가 함께 넘어가는지는 이 이슈 범위 밖입니다.
- 질문 순서(링크 먼저, 질문 나중)를 실제 이슈 흐름에서 돌려 본 검증은 아직 없습니다.
