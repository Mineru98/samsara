## 작업 요약

카탈로그 스트림이 `catalog_version` 공백(건너뛴 값)을 받으면 이제 snapshot 을 다시 불러옵니다. 런타임은 카탈로그 이벤트마다 전역 `catalog_version` 을 정확히 1 올리고 필터 없이 내보내므로 `state + 1` 이 아닌 값은 놓친 이벤트입니다.
같은 origin 의 여러 탭은 스트림(카탈로그, 프로젝트별)마다 SSE 연결 하나를 공유합니다. Web Locks 를 쥔 탭만 `StreamController` 를 돌리고 `BroadcastChannel` 로 view 를 나눠 주며, 리더 탭이 닫히면 다음 탭이 이어받습니다(02 §15.5).

## 변경 전후

화면 모양이 바뀌지 않는 동작 개선이라 스크린샷과 바운딩 박스는 해당 없습니다. 실제 런타임(격리 `SAMSARA_HOME`, `pnpm -C web build` 번들을 embed 한 `go build` 바이너리)에서 같은 프로젝트 화면을 headless Chromium 한 프로필의 탭 여러 개로 열고 미완료 `/events` 요청 수를 셌습니다.

| 지표 | 전 | 후 | 변화 |
| --- | ---: | ---: | ---: |
| 탭 3개 — 열린 SSE 연결 (브라우저) | 6 | 2 | **-67%** |
| 탭 3개 — 서버 포트 ESTABLISHED TCP (`ss`) | 6 | 4 | **-33%** |
| 탭 4개 — 열린 SSE 연결 (브라우저) | 6 (4번째 탭 로드 실패) | 2 | **-67%** |
| 탭 4개 — 열리는 탭 | 3 / 4 | 4 / 4 | **+1** |
| 리더 탭 닫은 뒤 남은 2탭의 SSE 연결 | 미측정 (공유 없음) | 2 (tab2 가 승계) | - |

전에는 4번째 탭이 `page.goto` 15초 타임아웃으로 아예 열리지 않았습니다. 탭 3개의 SSE 6개가 Chromium 의 호스트당 HTTP/1.1 연결 한도(6)를 다 썼기 때문입니다. 후의 `ss` 4개 중 2개는 snapshot·`/v1/runtime` 요청이 남긴 keep-alive 연결입니다.

| 완료 기준 | 결과 |
| --- | --- |
| `catalog_version` 공백 → 재조회 (테스트) | 신규 테스트 3개가 수정 전 코드에서 실패, 수정 후 통과 |
| 같은 프로젝트 여러 탭에서 SSE 연결 감소 (측정) | 2N → 2 |

원본 출력(`dev/0.4.0-alpha` 브랜치의 `.issue/111/evidence/`):
- 전: `before/sse-connections.txt`, `before/catalog-gap.txt`
- 후: `after/sse-connections.txt`, `after/test-suite.txt`

## 변경 파일

- `web/src/realtime/specs.ts` — `applyCatalogEvent` 에 `CATALOG_VERSION_GAP` 검사, catalog snapshot 의 `catalog_version` 형식 검사
- `web/src/store/decimal.ts` — `incDec`(10진 문자열 +1, number 변환 없음)
- `web/src/realtime/shared.ts` — 신규 `SharedStream`. 팔로워 `resync` 는 리더로 전달, `ApiError` 는 필드를 살려 전달, `auth_expired` 는 전파하지 않음(그 탭이 AuthGate 로 내려가며 lock 을 놓음), 승계 중에는 화면을 비우지 않고 `resyncing(LEADER_HANDOVER)` 으로 표시. Web Locks·BroadcastChannel 이 없으면 기존처럼 탭별 연결
- `web/src/hooks.ts` — `useStream` 이 `SharedStream` 사용
- `web/src/realtime/specs.test.ts`, `web/src/realtime/shared.test.ts` — 신규 테스트 10개

## 검증

- `pnpm -C web typecheck` / `pnpm -C web test`(38개, 신규 10) / `pnpm -C web build` 통과
- 실제 Chromium 에서 탭 3·4개 공유와 리더 탭 종료 후 승계 확인(위 표)
- 별도 리뷰 1회. 서버 전제(`catalog_version` 1씩 증가·무필터·snapshot 일관성) 확인. 지적된 "리더 승계 시 모든 탭이 잠깐 비는 문제"를 고치고 회귀 테스트 추가(수정 전 실패 확인)

## 남은 이슈

- 리더는 처음 lock 을 잡은 탭이라 백그라운드 탭일 수 있습니다. Chrome 이 숨겨진 탭의 타이머를 늦추면 재연결 backoff 가 길어질 수 있습니다(보이는 탭 우선 리더 선출은 미구현).
- 팔로워는 view 가 바뀔 때마다 상태 전체를 구조화 복제로 받아 그래프 memo 가 매번 다시 계산됩니다. 큰 프로젝트 성능은 #109 측정 범위에서 함께 봐야 합니다.
- 팔로워 탭의 bearer 는 스트림에 쓰이지 않아, 그 탭의 세션 만료는 `/v1/runtime` 30초 폴링에서 감지됩니다.
