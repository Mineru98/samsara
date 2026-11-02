# #109 before 기준선 (ce655a7, 파일 수정 전)

측정 환경: Intel i5-7500 4코어 · 60GB RAM · Linux 7.0.0-34 · Node 24.22.1 · Go 1.27.1 · Chromium headless shell 1248(Playwright 1.64.1) · 1440x900 · `pnpm -C web build` 프로덕션 번들 · runtime standalone backend, 격리 `SAMSARA_HOME`

## 브라우저 자동화

- `web/e2e/` 없음. Playwright 의존성·설정 없음 → UI-T01~T24 자동 실행 0건
- 단위 테스트만 존재: `pnpm -C web test` → Test Files 5 passed, Tests 28 passed

## 실제 runtime 화면(1회 측정, 참고값)

| 화면 | 그래프 노드 첫 표시 | 비고 |
| --- | ---: | --- |
| F-ABC 3노드 | 526ms | bootstrap 교환 포함 |
| seed 109 400노드 | 2,153ms | fitView 에서 글자 판독 불가, 미니맵 빈 상자 |
| 1,000노드 | 미측정 (plan apply 실패) | 아래 참고 |

## 1,000노드 실제 runtime 적재 불가

같은 seed(109) DAG 를 `samsara plan apply` 로 적용한 시간:

| 이슈 수 | 적용 시간 |
| ---: | ---: |
| 100 | 0.23s |
| 200 | 1.00s |
| 400 | 6.15s |
| 1,000 | 실패 — `INTERNAL: context deadline exceeded` (CLI IPC 60초 제한 초과, graph_version 0 유지) |

따라서 1,000노드 성능 측정은 runtime 계약(snapshot.schema)에 맞춰 생성한 fake 데이터로 수행한다(02 §25: 실제 Backend 검증과 Fake 데이터 검증을 구분해 기록).
