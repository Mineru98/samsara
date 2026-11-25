## 작업 요약

증거 폴더 안의 로그인 세션 파일(`.auth.json`, `storage-state.json`)이 GitHub 첨부, 증거 커밋, 미러 커밋에 들어가던 문제를 막았습니다.
`listEvidence()` 가 숨김 항목을 건너뛰고 이미지·텍스트 확장자(webp/png/jpg/jpeg/gif/txt/md)만 돌려주며, 폴더 단위 `git add -f` 는 그 목록의 파일 단위로 바꿨습니다.
이름 기반 제외(이슈 제안 1번)와 이미지 확장자 한정(2번)은 허용 목록 하나로 합쳤습니다. 허용 목록은 `cookies.json` 처럼 이름이 다른 세션 파일도 막습니다.

## 변경 전후 (재현 구조: 증거 폴더에 세션 파일 4종과 숨김 텍스트 포함)

| 항목 | 전 | 후 |
| --- | --- | --- |
| `listEvidence` 반환 | 8개 (`.DS_Store`, `.auth.json`, `storage-state.json`, `.hidden.md` 포함) | 4개 (webp 2, `bench.txt`, `comment.md`) |
| 스테이징된 파일 | 세션 파일 포함 9개 (`.gitignore` 포함) | 증거 4개 + `.gitignore` |
| `evidence-urls` 의 `images` | 목록 전체 | 이미지 2개만 |

- before 원본: [repro.txt](https://github.com/Mineru98/samsara/blob/main/.issue/173/evidence/before/repro.txt)
- after 원본: [verify.txt](https://github.com/Mineru98/samsara/blob/main/.issue/173/evidence/after/verify.txt)

실제 CLI 확인(증거 폴더에 `.auth.json`, `storage-state.json`, `.DS_Store` 를 둔 상태):

- `evidence-urls`: `images` 에 `after/home.webp`, `before/home.webp` 만 나왔습니다.
- `evidence-mirror`(push 없음): 미러 커밋에 webp 2개와 `comment.md` 만 들어갔습니다.
- `evidence-commit`(임시 저장소): 커밋에 증거 3개와 `.gitignore` 만 들어갔고, 세션 파일은 디스크에만 남았습니다.

## 변경 파일

- `skills/issue-{start,end,merge,create}/scripts/issue-common.mjs`: `listEvidence` 허용 목록, `isImageEvidence` 추가, `mirrorEvidence` 의 `git add -f` 를 파일 단위로 변경
- `skills/issue-{start,end,merge,create}/scripts/issue-tracker.mjs`: `evidenceUrls` 가 이미지만 업로드 대상으로 삼음
- `skills/issue-start/scripts/issue-start.mjs`, `skills/issue-end/scripts/issue-end.mjs`: `evidence-commit` 의 `git add -f` 를 파일 단위로 변경 (`issue-end.mjs` 는 이슈 본문에 없던 위치)
- `skills/issue-{start,end,merge}/contracts/issue-phase-capability-bundle-v1.json`: 스크립트 해시 재생성 (필수 후속 수정)
- `skills/issue-start/references/evidence-capture.md`: `--save-storage` 예시 경로를 `.issue/59/.auth.json` 으로 이동
- `skills/issue-start/scripts/issue-start.test.mjs`: 회귀 테스트 2개 추가

## 검증

- `node --test` 116개 통과 (기준선 114개 + 신규 2개)
- `scripts/check-shared.sh`: 4개 사본 일치
- `build-phase-capability-bundle.mjs --check`: 41/41 일치

## 남은 이슈

- 이 수정은 `main`(0.3.8) 기준입니다. `dev/0.4.0-alpha` 는 사본이 6곳(`issue-onboard`, `issue-sync` 추가)이고 줄 번호가 달라서 같은 수정이 반영되지 않았습니다. 별도 반영이 필요합니다.
- 이미 올라간 `.auth.json` 첨부는 API 로 지울 수 없습니다. 해당 세션의 토큰 폐기나 첨부 수동 삭제는 코드 수정과 별개로 해야 합니다.
- 비공개 저장소의 `gh attach upload` 경로는 이 저장소가 PUBLIC 이라 실행하지 못했습니다. 필터가 업로드 이전 단계(`images` 구성)에 적용되는 것까지만 확인했습니다. `issue-end` 의 `evidence-commit` 도 `issue-start` 와 같은 한 줄 변경이라 직접 실행하지는 않았습니다.
- 허용 확장자에 `.json`, `.log` 는 넣지 않았습니다. 해당 형식의 증거가 필요해지면 허용 목록에 추가해야 합니다.
