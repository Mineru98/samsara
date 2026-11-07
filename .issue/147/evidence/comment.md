## 작업 요약

구현 커밋에 `Closes #N` 같은 자동 종료 문구가 들어가는 것과, report-check 가 증거 텍스트 파일 링크를 이미지로 오판하던 문제를 고쳤습니다.
`guard --message` 가 자동 종료 문구를 찾으면 exit 3 으로 커밋을 막고, report-check 는 이미지 확장자 또는 `user-attachments` 링크만 이미지로 봅니다.
게시 절차는 `report-check ... && gh issue comment ...` 로 묶어 검사가 실패하면 게시되지 않게 했습니다.

## 원인

- `guard` 는 워크트리·브랜치만 검사하고 커밋 메시지를 볼 수단이 없었습니다(`issue-start.mjs` `cmdGuard`).
- `collectImageReferences` 가 일반 링크·bare URL 중 `blob`/`raw`/`release` 주소면 확장자와 상관없이 이미지 후보로 넣었습니다(`issue-media.mjs`). 그래서 `.txt` 증거 링크가 "이미지가 일반 링크로 작성됨" 으로 실패했습니다.
- SKILL.md·레퍼런스의 게시 명령이 report-check 와 `gh issue comment` 를 따로 적어 실패 뒤에도 게시가 이어질 수 있었습니다.

## 변경 전후 (출력 비교)

| 확인 항목 | 전 | 후 |
| --- | --- | --- |
| `Closes #147` 이 든 메시지로 `guard` | 메시지를 넘길 옵션 없음(`--message` 미지원), 메시지 없이 `ok: true` | **`noClosingKeyword: false`, exit 3** |
| 증거 `.txt` blob 링크만 있는 리포트 | `ok=false` (이미지 오판 2건) | **`ok=true`** |
| 증거 `.txt` raw bare URL | `ok=false` | **`ok=true`** |
| blob 주소로 쓴 webp 이미지 | `ok=false` | `ok=false` (유지) |
| webp 이미지를 일반 링크로 씀 | `ok=false` | `ok=false` (유지) |
| 정상 raw 이미지 | `ok=true` | `ok=true` (유지) |

원본 출력:
- 전: [guard.txt](https://github.com/Mineru98/samsara/blob/main/.issue/147/evidence/before/guard.txt) · [report-check.txt](https://github.com/Mineru98/samsara/blob/main/.issue/147/evidence/before/report-check.txt)
- 후: [guard.txt](https://github.com/Mineru98/samsara/blob/main/.issue/147/evidence/after/guard.txt) · [report-check.txt](https://github.com/Mineru98/samsara/blob/main/.issue/147/evidence/after/report-check.txt)
- 검사 스크립트: [probe-report.mjs](https://github.com/Mineru98/samsara/blob/main/.issue/147/evidence/probe-report.mjs)

이 리포트 자체가 증거 텍스트 파일 링크만 담고 있고, 고친 report-check 를 통과한 뒤 게시됐습니다.

## 검증

- `node --test skills/issue-start/scripts/issue-start.test.mjs` — 5/5 통과 (신규: 종료 문구 판정, 파일 링크 통과, 깨진 이미지 링크 실패)
- 기존 테스트 `scripts/check-shared.test.mjs`, `skills/issue-merge`·`skills/issue-version` 테스트 — 변경 전후 모두 69/69 통과
- `sh scripts/check-shared.sh` 통과, `issue-media.mjs` 4개 사본 동일(`cmp`)

## 완료 기준

- [x] `Closes #N` 이 든 커밋 시도가 거부된다 — after/guard.txt
- [x] 증거 텍스트 파일 링크만 있는 코멘트가 report-check 를 통과한다 — after/report-check.txt (a, a2)
- [x] 깨진 이미지 링크는 여전히 실패한다 — after/report-check.txt (b, b2)
- [x] report-check 실패 시 게시하지 않는 절차가 SKILL.md·레퍼런스에 있다 — `SKILL.md` hard-rule·10~11단계, `references/evidence-capture.md`, `references/implementation.md`

## 참고

- 캡처 대신 CLI 출력으로 증거를 남겼습니다(화면 변경 없음).
- `contracts/issue-phase-capability-bundle-v1.json` 의 파일 해시 묶음은 main 에서도 이미 21개 파일이 어긋나 있어 이번 범위에서 갱신하지 않았습니다.
