## 작업 결과
GitHub 일반 소스·문서 blob/raw/release 링크가 이미지로 오인되어 증거 게시가 차단되던 문제를 수정했습니다. 일반 링크·bare URL은 알려진 이미지 확장자 또는 user-attachments 자산일 때만 이미지 후보로 삼고, 명시적인 markdown 이미지와 HTML img의 기존 검증은 유지합니다.

구현: `6bf9ee2`, branch `fix/77-non-image-report-links`, 이슈 미디어 사본 5개에 동일 수정과 회귀 테스트 추가.

| 검증 | 수정 전 | 수정 후 |
| --- | --- | --- |
| 일반 .go/.md/.json/.txt 링크 | 이미지 오류로 거부 | 허용 |
| 이미지 blob/raw 일반 링크·인라인 blob·alt 없음·HTML img | 거부 | 거부 유지 |
| private raw 이미지 | 수동 업로드 필요 | 동일 |
| Node 전체 검사 | 기존 163개 | 182/182, exit 0 |
| 공유 코드 검사 | — | exit 0 |

controller가 검토한 현재 tree `3f03c81b19ce3540ecbc38e26beaaa3c5376d37f`에서 필수 node-all/shared-check가 PASS한 뒤에만 gate가 구현 완료→커밋→after 검사를 허용했습니다. after에서도 두 검사가 exit 0이었습니다.

[실제로 거부되었던 일반 Go 소스 링크](https://github.com/Mineru98/samsara/blob/fix/74-macos-symlink-cwd-scope/runtime/internal/app/alias_test.go), [새 회귀 테스트](https://github.com/Mineru98/samsara/blob/fix/77-non-image-report-links/scripts/issue-media.test.mjs), [CLI 증거](https://github.com/Mineru98/samsara/tree/fix/77-non-image-report-links/.issue/77/evidence).

이 리포트 자체의 일반 소스 링크가 수정된 report-check를 통과한 뒤 evidence-commit 및 게시까지 진행되는 실제 경로도 확인했습니다. 네 신규 이슈 결합 검증은 controller의 별도 결과에 기록합니다. 코드 병합과 이슈 종료는 수행하지 않았습니다.
