## 작업 결과
macOS `/var`·`/private/var` 별칭 경로 때문에 관리 worker 등록 및 Standalone attach가 거부되던 문제를 수정했습니다. 새 worktree root의 상위 경로를 정규화하고, 재시작 시 기존 DB에 저장된 별칭 경로도 실제 root 안에 있는 경우에만 정규화합니다. root 자체의 symlink와 범위 밖 경로·symlink escape 거절은 유지합니다.

구현 커밋: `147030f` (`fix/74-macos-symlink-cwd-scope`, 기준 `ff71efb`, `dev/0.4.0-alpha`).

| 검증 | 수정 전 | 수정 후 |
| --- | --- | --- |
| 기본 macOS TMPDIR에서 Go 전체 테스트 | app 10개 실패 | 통과, exit 0 |
| app race 검사 | 같은 등록 실패 | 통과, exit 0 |
| go vet | — | 통과, exit 0 |
| alias worker·Standalone·기존 DB 재시작·root symlink·escape 회귀 | 미검증 | 5개 회귀 포함 통과 |

`#76`의 영속 gate 및 실제 Claude PreToolUse hook을 사용했습니다. controller가 검토한 현재 tree `73e5e12544e1f78794b883545ca4f9172665ce4d`에서 go-all/go-race/go-vet가 모두 PASS한 뒤에만 구현 완료→커밋→after 검사가 진행됐습니다. after에서도 같은 3개 명령이 exit 0이었고, 다음 단계 미완료 상태에서 커밋을 앞당기지 않았습니다.

[수정 전후 CLI 증거](https://github.com/Mineru98/samsara/tree/fix/74-macos-symlink-cwd-scope/.issue/74/evidence), `runtime/internal/app/alias_test.go` 신규 회귀 테스트.

화면 변경이 없는 runtime 작업이라 이미지 대신 실제 명령 출력과 종료코드를 기록했습니다. 코드 병합·이슈 종료는 수행하지 않았습니다.
