# #109 Playwright 브라우저 시나리오 결과

명령: `pnpm -C web e2e` (chromium headless shell 1248, 1440x900, 프로덕션 번들) · 통과 27 · 실패 0 · flaky 0 · 건너뜀 0 · 45.9s · 최종 커밋에서 전체 연속 2회·실제 runtime 시나리오 연속 3회 전부 통과

| 파일 | 시나리오 | 결과 | 시간 |
| --- | --- | --- | ---: |
| runtime.spec.ts | F-ABC 실제 runtime: 화면 version 이 서버 projection 과 같다 | 통과 | 0.9s |
| runtime.spec.ts | F-JOIN 실제 runtime: 합류 게이트와 version 이 서버 projection 과 같다 | 통과 | 0.7s |
| runtime.spec.ts | F-STREAM 실제 runtime: 두 프로젝트 이벤트가 교차해도 화면은 자기 projection 만 따른다 | 통과 | 1.3s |
| runtime.spec.ts | UI-T13 브라우저를 닫고 재접속: runtime·worker 는 계속 동작하고 최신 상태를 복구한다 | 통과 | 8.4s |
| ui.spec.ts | UI-T01 A 승인 대기·B는 A 의존·C 독립: 이유가 보이고 C 갱신은 계속된다 | 통과 | 0.7s |
| ui.spec.ts | UI-T02 A 승인 후 B 조건 충족·Standalone: B를 자동 완료로 보이지 않고 수동 계속을 안내한다 | 통과 | 0.5s |
| ui.spec.ts | UI-T03 herdr 관찰이 blocked: 삼사라 승인 대기로 바꾸지 않는다 | 통과 | 0.4s |
| ui.spec.ts | UI-T04 herdr 연결 단절·워커 생존 미확인: 관찰 불가로 두고 tmux 이동·종료로 보이지 않는다 | 통과 | 0.6s |
| ui.spec.ts | UI-T05 결과 제출 후 Queue 대기: 구현 결과 제출과 통합 대기를 함께 보인다 | 통과 | 0.6s |
| ui.spec.ts | UI-T06 Queue 검증 중 대상 Commit 변경: 이전 검증 기준과 재검증 필요를 보인다 | 통과 | 0.4s |
| ui.spec.ts | UI-T07 서로 다른 대상 브랜치 Queue: 하나가 대기해도 다른 Queue 진행을 보인다 | 통과 | 0.5s |
| ui.spec.ts | UI-T08 SSE 단절 후 중복 재생: 중복 이벤트·노드·카운터가 없다 | 통과 | 1.8s |
| ui.spec.ts | UI-T09 cursor 보관 범위 만료: 전체 Snapshot으로 복구하고 잘못된 실시간 표시가 없다 | 통과 | 1.3s |
| ui.spec.ts | UI-T10 프로젝트 빠르게 전환: 이전 이벤트·응답이 새 화면을 오염하지 않는다 | 통과 | 2.2s |
| ui.spec.ts | UI-T11 다른 탭에서 승인 먼저 처리: 처리된 결과를 보이고 이중 승인이 없다 | 통과 | 0.9s |
| ui.spec.ts | UI-T12 승인 내용 revision 변경: 이전 내용 승인은 거부되고 최신 내용을 다시 검토한다 | 통과 | 0.6s |
| ui.spec.ts | UI-T14 과거·비활성 프로젝트 선택: 자동 활성화 없이 기록을 표시한다 | 통과 | 1.1s |
| ui.spec.ts | UI-T15 GitHub 연결 끊김: 로컬 상태 갱신은 계속되고 외부 정보 신선도를 구분한다 | 통과 | 0.5s |
| ui.spec.ts | UI-T16 긴 제목·다중 루트·교차 간선: 노드·간선 라벨이 겹치거나 잘리지 않는다 | 통과 | 0.8s |
| ui.spec.ts | UI-T17 키보드만 사용: 프로젝트→차단 이유→승인 검토→복귀가 가능하다 | 통과 | 1.3s |
| ui.spec.ts | UI-T18 화면 축소·동작 줄이기: 목록 대안·포커스·비애니메이션이 정상이다 | 통과 | 0.5s |
| ui.spec.ts | UI-T19 SSE 청크가 JSON·UTF-8 중간에서 분리: 하나의 올바른 이벤트로 복원한다 | 통과 | 0.6s |
| ui.spec.ts | UI-T20 프로젝트 버전 건너뜀·reset_required: 부분 적용 없이 Snapshot으로 다시 맞춘다 | 통과 | 2.3s |
| ui.spec.ts | UI-T21 인증 만료·페이지 새로고침: 토큰을 저장하지 않고 재연결을 안내하며 무한 재시도하지 않는다 | 통과 | 5.8s |
| ui.spec.ts | UI-T22 offline 중 승인 클릭 후 연결 복구: 명령을 자동 대기열로 실행하지 않는다 | 통과 | 2.8s |
| ui.spec.ts | UI-T23 브라우저는 열린 채 데몬만 재시작: 유효 token으로 재연결하고 새 epoch 상태를 복구한다 | 통과 | 3.2s |
| ui.spec.ts | UI-T24 명령이 202 접수 후 오래 진행: 접수를 완료로 표시하지 않고 결과는 이벤트로 반영한다 | 통과 | 2.1s |

- `ui.spec.ts`: in-process fake runtime(runtime 이 쓴 계약 fixture 기반, 장애 주입). `runtime.spec.ts`: 격리 `SAMSARA_HOME` 의 실제 daemon, tmux backend, `sleep` worker.
