## 작업 요약

`runtime/api/` 에 wire 계약의 단일 기준을 만들었습니다. OpenAPI 3.1과 JSON Schema 7종(common·entities·snapshot·event·plan·config·ipc)이 기준이고, Go 응답은 테스트에서 이 schema로 strict 검증합니다. TypeScript 타입은 schema에서 생성하며(int64는 `Decimal` 문자열), Go·TS 테스트가 같은 fixture를 씁니다.

## 변경 전후

화면이 없는 백엔드 작업이라 터미널 출력을 캡처했습니다. 같은 명령을 변경 전 `500698f` 와 변경 후 `37a4e04` 에서 실행했습니다.

| 전 | 후 |
| --- | --- |
| ![계약 산출물·테스트 - 전](https://raw.githubusercontent.com/Mineru98/samsara/dev/0.4.0-alpha/.issue/71/evidence/before/contract-tests.webp) | ![계약 산출물·테스트 - 후](https://raw.githubusercontent.com/Mineru98/samsara/dev/0.4.0-alpha/.issue/71/evidence/after/contract-tests.webp) |

빨간 박스가 변경 구간입니다. `runtime/api` 와 schema·OpenAPI·생성 TS 파일이 0개에서 9개가 됐고, 계약 테스트가 Go 7개와 TS 7개로 늘었습니다.

| 지표 | 전 | 후 |
| --- | ---: | ---: |
| schema·OpenAPI·생성 TS 파일 | 0 | **9** |
| 계약 fixture | 0 | **6** |
| `runtime/api` Go 테스트 | 0 | **7** |
| TS 계약 테스트 (`node --test`) | 0 | **7** |
| 실제 응답을 strict 검증하는 기존 테스트 | 0 | **3** (A/B/C, ACK 유실, httpapi) |

원본 출력은 `.issue/71/evidence/{before,after}/contract-tests.txt` 에 있습니다.

## 완료 기준 대조

- [x] `go test` 에 snapshot·event·error 응답의 schema 검증이 들어갔습니다. A/B/C·ACK 유실 시나리오의 snapshot과 전체 event, HTTP 오류 본문, `/v1/runtime`·`/v1/catalog/snapshot`·`/v1/projects` 응답을 검증합니다. schema에 없는 필드를 Go에 추가하면 실패합니다(필드를 임시로 넣어 `additional properties 'drift_probe' not allowed` 로 실패하는 것을 확인).
- [x] 생성된 TS 타입(`runtime/api/ts/samsara-api.gen.ts`)을 UI에서 쓸 수 있습니다. `tsc --strict` 를 저장소 밖에서 한 번 돌려 통과를 확인했습니다. schema를 바꾸고 다시 생성하지 않으면 `TestTypeScriptUpToDate` 가 실패합니다.
- [x] enum unknown 처리 테스트(03 §12.1)를 넣었습니다. enum 필드는 `Open<T>` 타입이고 `is<Enum>`·`known()` 가드를 함께 생성합니다. `unknown-enum` fixture(미래 값 `HIBERNATING`·`DELAYED`)를 넣으면 Go schema는 거부하고, TS 클라이언트는 실패하지 않고 `UNKNOWN` 으로 표시합니다. Go enum 상수와 schema enum의 일치도 검사합니다.

## 변경 파일

- `runtime/api/openapi.yaml`: `/v1/*` 35개 operation, Idempotency-Key·If-Match·ETag, 오류 코드별 상태, SSE 이벤트를 기술합니다.
- `runtime/api/schemas/*.schema.json`: draft 2020-12 schema 7종입니다. IPC는 40개 route의 요청·응답 목록(`IpcRoutes`)을 담았습니다.
- `runtime/api/api.go`: schema를 내장하고 검증합니다. tolerant 모드는 클라이언트용으로 추가 필드를 허용하고, strict 모드는 서버 drift를 감지합니다.
- `runtime/api/tsgen.go`, `ts/samsara-api.gen.ts`: schema에서 TS를 생성하는 생성기와 생성물입니다.
- `runtime/api/apitest/`: 실제 응답 검증 도우미와 fixture 재생성기입니다(`SAMSARA_UPDATE_FIXTURES=1`).
- `runtime/api/fixtures/`: `abc-partial-block`, `stale-approval`, `merged-history`, `unknown-delivery`, `http-errors`, `unknown-enum` 입니다.
- `runtime/api/api_test.go`, `ts/contract.test.ts`: 계약 테스트입니다.
- `runtime/internal/app/*_test.go`, `internal/transport/httpapi/server_test.go`: 기존 시나리오에 계약 검증과 fixture 기록을 붙였습니다.
- `runtime/go.mod`: 의존성 `github.com/santhosh-tekuri/jsonschema/v6` 를 추가했습니다.

## 검증

- `cd runtime && go vet ./... && go test ./...` 전부 통과
- `node --test runtime/api/ts/contract.test.ts` 7/7 통과
- `tsc --strict`(5.9, 저장소 밖 1회) 오류 없음
- 독립 리뷰에서 나온 5건을 반영했습니다. 그중 `GET /v1/runtime` 에서 미조사 capability의 `level: ""` 가 schema를 위반하던 건은 테스트를 추가해 막았습니다.

## 남은 이슈

- Go enum 일치 테스트는 Go 상수를 테스트에 직접 나열합니다. 새 상수를 추가할 때 이 목록도 함께 갱신해야 합니다(Go는 상수를 리플렉션으로 읽을 수 없습니다).
- fixture를 재생성하면 같은 시각에 생긴 행의 ID 순서에 따라 placeholder 번호가 바뀔 수 있습니다. 계약이 그대로여도 diff가 생길 수 있습니다.
- TS 테스트는 Node의 type stripping으로 실행되고 `tsc` 는 돌리지 않습니다. UI 프로젝트가 생기면 그쪽 tsc에 생성 파일을 연결해야 합니다.
- Go 타입 생성(03 §12.2)은 이번 범위에서 뺐습니다. 기존 Go struct를 schema로 검증하는 방식입니다.
- 기준 브랜치는 `main` 이 아니라 `dev/0.4.0-alpha` 입니다. `main` 에는 `runtime/` 이 없습니다.
