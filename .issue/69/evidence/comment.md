## 작업 요약

런타임이 React 대시보드(`web/`) 번들을 embed해 같은 loopback origin에서 서빙합니다. `samsara ui --open`으로 열면 프로젝트 목록, 좌→우 이슈 DAG·통합 게이트, Worktree 묶음, inspector, Merge Queue, 하단 이력 탭을 snapshot + fetch-streaming SSE로 실시간 표시하고, 승인·일시 정지·재개·통합 요청을 `Idempotency-Key`·`If-Match`로 보냅니다.
작업 중 사이드바 카운터가 갱신되지 않는 런타임 버그(카탈로그 이벤트 누락)를 찾아 함께 고쳤습니다.

범위는 사전 합의대로 **읽기 전용 + 제어 핵심**입니다. 1,000노드 성능 측정과 Playwright E2E 전체는 남은 이슈로 분리합니다.

## 변경 전후

| 전 | 후 |
| --- | --- |
| ![런타임 루트 - 전: 안내 페이지만 있음](https://raw.githubusercontent.com/Mineru98/samsara/dev/0.4.0-alpha/.issue/69/evidence/before/dashboard-root.webp) | ![런타임 루트 - 후: 대시보드와 프로젝트 목록](https://raw.githubusercontent.com/Mineru98/samsara/dev/0.4.0-alpha/.issue/69/evidence/after/dashboard-root.webp) |

빨간 박스가 변경 구간입니다. 같은 1440x900 뷰포트, 같은 `/` 경로입니다. 전에는 "UI 번들이 포함되어 있지 않다"는 안내문만 있었습니다.

<details>
<summary>프로젝트 화면 (after 3장)</summary>

| 화면 | 캡처 |
| --- | --- |
| 이슈 DAG·통합 게이트·PLAN 승인 제어 | ![프로젝트 대시보드 - 이슈 DAG와 승인 대기](https://raw.githubusercontent.com/Mineru98/samsara/dev/0.4.0-alpha/.issue/69/evidence/after/project-dag.webp) |
| 이슈 상세(차단 이유)·Worktree 탭·일시 정지 요청 | ![이슈 상세와 하단 Worktree 탭](https://raw.githubusercontent.com/Mineru98/samsara/dev/0.4.0-alpha/.issue/69/evidence/after/issue-inspector.webp) |
| Worktree 묶음 보기(같은 간선 유지) | ![Worktree 묶음 보기](https://raw.githubusercontent.com/Mineru98/samsara/dev/0.4.0-alpha/.issue/69/evidence/after/worktree-view.webp) |

캡처는 격리된 `SAMSARA_HOME`의 실제 런타임 데이터입니다(standalone backend, 이슈 5개, `api`→`auth` 결과 승인 의존, `release`는 `api`·`ui` 통합을 AND 게이트로 기다림). before 루트 화면 외에는 대응하는 before 화면이 없어서 after만 첨부합니다.
</details>

## 실제 동작 확인

- 브라우저에서 PLAN 승인 클릭 → 202 접수 → SSE로 `project_version 3 → 4` 반영, 카드가 "승인됨 / 접수됨"으로 바뀌고 사이드바·헤더 승인 대기 1 → 0.
- 콘솔 오류 없음. CSP `default-src 'self'` 그대로(ELK worker, xyflow 모두 same-origin).

## 변경 파일

- `web/` — 신규 대시보드. `realtime/`(SSE 파서·스트림 컨트롤러), `store/`(정규화 Store, decimal 비교), `features/`(그래프·inspector·queue·dock), 계약 타입은 `runtime/api/ts` 재사용
- `runtime/internal/webui/` — 빌드 번들 embed(번들이 없으면 기존 안내 페이지)
- `runtime/internal/transport/httpapi/server.go` — 정적 서빙 + SPA fallback, `/v1/*`는 HTML로 fallback하지 않음
- `runtime/internal/store/sqlite/tx.go` — issue·run·attempt·approval·candidate 변경 시에도 카탈로그 이벤트 기록
- `.gitignore`, `runtime/README.md` — 번들 산출물 무시, 빌드 방법

## 검증

- `pnpm -C web typecheck` / `pnpm -C web test`(28개: SSE 청크·UTF-8·CRLF, version gap·중복·역행, reset_required, 인증 만료, stop 후 이벤트 무시, fragment 제거, F-ABC/F-JOIN 투영) / `pnpm -C web build` 통과
- `go vet ./...` / `go test ./...` 통과. 신규 `TestStaticDashboard`, `TestCatalogFollowsSummaryCounters`(수정 전 실패 재현 → 수정 후 통과)
- UI 번들 없는 깨끗한 checkout에서 `go build` 통과
- 독립 리뷰 1회. 지적된 재시도 루프·스키마 불일치·멱등 키 재사용·fragment 제거 순서를 반영

## 남은 이슈

- 1,000노드 집약·성능 예산 측정(02 §22), Playwright 브라우저 시나리오(UI-T01~T24) 자동화
- 런 시작(`run start`)·계획 수정 같은 명령은 UI에 없음(CLI 사용)
- 카탈로그 스트림은 전역 `catalog_version` 공백을 감지하지 않음(중복만 무시)
- 같은 프로젝트 탭이 여러 개면 탭마다 SSE 연결 2개(02 §15.5 첫 버전 범위)
