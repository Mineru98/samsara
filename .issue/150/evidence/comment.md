## 작업 요약

`issue-merge.mjs resolve` 가 충돌 해소 에이전트에게 넘기는 정보를 보강했습니다. 그리고 삭제가 섞인 충돌이 사람 확인 없이 커밋되던 버그를 고쳤습니다. 이제 충돌 마커에 공통 조상이 함께 찍히고, 충돌 종류가 다섯 가지로 나뉘며, 파일마다 반대편 커밋이 붙습니다. `--continue` 는 삭제형 충돌을 사람이 정하기 전에는 커밋하지 않고, 충돌 경로만 커밋에 담습니다.

화면 변경이 없는 스크립트라 캡처는 생략했습니다. 임시 git 저장소로 같은 테스트를 수정 전후 코드에 돌린 결과를 증거로 남깁니다.

## 변경 전후

| 항목 | 전 | 후 |
| --- | --- | --- |
| 충돌 마커 | 이 브랜치와 반대편 두 덩어리 | `\|\|\|\|\|\|\|` 아래에 공통 조상까지 표시 (zdiff3) |
| `kind` | 경로 정규식으로 `content` / `generated` 두 가지 | stage 로 `content` / `both-added` / `deleted-by-us` / `deleted-by-them` / `generated` |
| 반대편 정보 | 오케스트레이터의 자연어 요약 | 파일마다 `incoming` 커밋 목록 |
| 삭제형 충돌에서 `--continue` | 마커가 없어 검사 통과, 남아 있는 쪽이 커밋됨 | exit 1 로 거부. `git add`(남김) 또는 `git rm`(지움)으로 정해야 커밋 |
| `--continue` 의 add 범위 | `git add -A` (검사 산출물까지 커밋) | 충돌 경로만 |
| resolver 지침 | 테스트·빌드 금지, "애매하면 escalate" | 마커 읽는 법, 검사 실행과 `checks` 출력, escalate 는 세 가지 조건일 때만 |

같은 테스트 3건의 결과입니다.

| 테스트 | 수정 전 | 수정 후 |
| --- | --- | --- |
| 충돌 종류·공통 조상·반대편 커밋 | 실패 (삭제형·양쪽 추가가 모두 `content`) | 통과 |
| 삭제형 충돌이 남으면 커밋하지 않음 | 실패 (exit 0 으로 커밋됨) | 통과 |
| 충돌 경로만 담고 추적 안 하는 파일 제외 | 실패 (검사 산출물이 커밋됨) | 통과 |

원본: `.issue/150/evidence/before/01-resolve-test.txt`, `.issue/150/evidence/after/01-resolve-test.txt`

## 변경 파일

- `skills/issue-merge/scripts/issue-merge.mjs`. `cmdResolve` 의 zdiff3, `unmergedEntries`·`unmergedKind`·`withIncoming` 추가, `--continue` 의 거부 조건과 add 범위. `preflight` 는 바꾸지 않았습니다.
- `skills/issue-merge/scripts/issue-merge-resolve.test.mjs`. 새 테스트입니다.
- `agents/yamato-merge-resolver.md`, `agents/yamato-merge-resolver.toml`. 같은 본문으로 지침을 갱신했습니다.
- `skills/issue-merge/references/merge-plan.md`. 5절 resolve 출력 예시, kind 표, 프롬프트 항목, `--continue` 설명.

## 검증

- `node --test skills/issue-merge/scripts/issue-merge-resolve.test.mjs skills/issue-merge/scripts/issue-unblocked.test.mjs` 9건 통과.
- `node --check skills/issue-merge/scripts/issue-merge.mjs` 통과.
- 지침 `.md` 와 `.toml` 본문 diff 없음.

## 남은 이슈

- [#149](https://github.com/Mineru98/samsara/issues/149) 와 `agents/yamato-merge-resolver.toml`, `merge-plan.md` 를 함께 건드립니다. 고친 구간은 서로 다릅니다.
- resolver 의 판단 품질 자체는 단위 테스트로 고정할 수 없습니다. 실제 충돌 사례를 모아 비교하는 평가는 이번 범위 밖입니다.
