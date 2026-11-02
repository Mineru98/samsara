## 작업 요약

런타임을 설치·업그레이드·롤백할 수 있게 하고, 운영 상태를 볼 수 있게 했습니다. 05 §17 과 01 §18 을 기준으로 했습니다.
`release.sh` 로 linux/darwin binary 를 만들고, `samsara install / upgrade / restore / metrics / doctor --export` 를 추가했습니다. 이벤트 보존 기간은 이제 `observation.event_retention_days` 설정을 따릅니다.
release tarball 만으로 격리된 홈에서 돌린 drill 34단계가 모두 통과했습니다.

## 변경 전후

| 전 | 후 |
| --- | --- |
| ![운영 명령 - 전](https://raw.githubusercontent.com/Mineru98/samsara/dev/0.4.0-alpha/.issue/72/evidence/before/ops.webp) | ![운영 명령 - 후](https://raw.githubusercontent.com/Mineru98/samsara/dev/0.4.0-alpha/.issue/72/evidence/after/ops.webp) |

빨간 박스가 변경 구간입니다.
- 전: restore·upgrade·install·metrics 는 `unknown command`, `doctor --export` 는 없는 flag 였습니다. retention 은 7일 고정이었고, `RotateDatabaseGeneration` 은 호출하는 곳이 없었습니다.
- 후: 각 명령이 동작하고, retention 은 설정값을 따르며, restore 가 generation 을 회전합니다. 아래쪽 박스는 export 에서 심어 둔 비밀값이 0건이라는 점검 결과입니다.

## 동작

- **배포**: `runtime/scripts/release.sh` 가 `CGO_ENABLED=0 -trimpath -X main.version=$(cat VERSION)` 로 4개 target 을 빌드합니다. 결과물은 tarball, `SHA256SUMS`, 그리고 풀어 둔 `dist/<os>_<arch>/samsara` 입니다.
  - 플러그인은 `scripts/samsara-bin.sh` 로 binary 를 찾습니다. 순서는 `$SAMSARA_BIN` → install 링크 → `${CLAUDE_PLUGIN_ROOT}/runtime/dist/…` → PATH 입니다.
- **install**: binary 를 `install/versions/<버전>/samsara` 로 복사하고 `install/bin/samsara` 링크를 바꿉니다.
  - 설치 디렉터리의 버전을 가리키던 hook 은 새 버전으로 옮깁니다. 롤백할 때도 같습니다.
- **upgrade (05 §17.2)**: 순서는 다음과 같습니다.
  1. start lock 을 잡아, 그 사이 hook 이 구 binary 로 daemon 을 띄우지 못하게 합니다.
  2. daemon 을 정지하고 instance lock 을 잡습니다.
  3. migration 전 DB 를 백업합니다.
  4. migration 후 `integrity_check`·`foreign_key_check` 를 돌립니다.
  5. 새 binary 를 설치하고 hook 을 재지정합니다.
  6. 새 daemon 을 시작하고 버전을 확인합니다.
  - 중간에 실패하면 이후 단계를 하지 않고, 복원할 백업 경로를 알려 줍니다. 더 새로운 binary 가 만든 DB 는 건드리지 않습니다.
- **ACT-12**: upgrade 는 이전 binary 를 지우지 않습니다. `--prune-old` 는 살아 있는 관리 세션이 있거나 hook 이 실행 중인 버전을 남깁니다.
- **restore (05 §17.3, DB-12/13, EVT-11)**: daemon 이 정지된 상태에서만 동작하고, 백업 파일을 먼저 검증합니다.
  - 검증 항목: 무결성, schema ≤ binary, checksum.
  - 현재 DB 는 `pre-restore-*.db` 로 남깁니다.
  - 복사본에서 migration·복원 처리·무결성 확인을 모두 끝낸 뒤 rename 한 번으로 교체합니다. 그래서 중간에 끊겨도 현재 DB 는 그대로입니다.
  - 복원 처리는 세 가지입니다. `database_generation` 을 회전해 이전 cursor 가 `reset_required` 를 받게 합니다. 진행 중이던 outbox intent 는 UNKNOWN 으로 바꿔 재전송하지 않습니다. PUBLISHING 후보는 실제 target 을 다시 읽게 합니다.
- **metrics (01 §18.1)**: 메모리 지표와 DB gauge 두 종류입니다.
  - 메모리 지표: hook decision latency p50/p95/p99, decision·deny reason 별 count, SQLite writer latency, SSE 연결·재연결·`reset_required` 사유별 count.
  - DB gauge: active attempts, UNKNOWN sessions, quarantined attempts, outbox pending·lag, ambiguous operations, merge queue depth, event retention.
- **doctor --export**: doctor 결과, manifest, registry, config, runtime·metrics, `daemon.log` 마지막 200줄을 0600 JSON 으로 씁니다.
  - token·prompt·tool 입력·ticket·payload 같은 필드는 `[REDACTED]` 로 바꿉니다.
  - 본문 속 GitHub·Anthropic·Slack·AWS 자격증명, Bearer, URL 인증정보는 지웁니다.
- **retention**: 이벤트 cursor 경계가 전역이라, 활성 프로젝트 중 가장 긴 `event_retention_days` 를 씁니다. 각 프로젝트는 설정한 기간 이상 보존됩니다.

## 변경 파일

- `runtime/internal/ops/ops.go`: install·upgrade·restore·prune·hook 재지정 (신규)
- `runtime/internal/metrics`, `runtime/internal/app/metrics.go`: 지표 수집과 snapshot (신규)
- `runtime/internal/diag/diag.go`: redaction·log tail (신규)
- `runtime/internal/app/restore.go`: `PrepareRestored` (신규)
- `runtime/internal/store/sqlite/db.go`: `Inspect`·`BackupFile`·`CheckIntegrity`, writer latency
- `runtime/internal/app/{sessions,reconcile,app}.go`: hook 결정 기록, retention
- `runtime/internal/transport/{httpapi/sse.go,ipc/ipc.go}`: SSE 계측, `/ipc/v1/metrics`
- `runtime/internal/daemon/daemon.go`, `hooks/install.go`, `paths/paths.go`: `Spawn`, `CommandPaths`, `InstallDir`·`BackupDir`
- `runtime/cmd/samsara/main.go`: CLI 명령
- `runtime/api/schemas/ipc.schema.json`, `runtime/api/ts/samsara-api.gen.ts`: `MetricsView` 계약
- `runtime/scripts/{release,drill}.sh`, `scripts/samsara-bin.sh`, `runtime/README.md`, `docs/architecture/06-runtime-remaining-work.md`

## 검증

- `go vet ./...` / `go test ./... -count=1` 전부 통과 (원본 `.issue/72/evidence/after/go-test.txt`, 기준선 `before/go-test.txt`)
- 신규 테스트
  - 복원 후 옛 cursor 가 `DATABASE_GENERATION_CHANGED` 를 받고 PENDING intent 가 UNKNOWN 이 되는지. 회전하지 않은 대조군은 reset 되지 않는 것도 확인했습니다.
  - retention 7/30일 경계
  - hook deny·latency·outbox lag 지표
  - SSE 재연결·reset 지표
  - upgrade 의 백업·migration·설치와, 더 새로운 schema 거절
  - restore 의 세대 회전과 거절 3종 (daemon 실행 중, 더 새로운 schema 의 백업, DB 가 아닌 파일)
  - hook 재지정 (개발 빌드 hook 은 유지)
  - ACT-12 prune 보존
  - redaction
- 변이 검사: retention 을 7일 고정으로 되돌리면, 또 generation 회전을 빼면 각각 해당 테스트가 실패합니다.
- `runtime/scripts/drill.sh` 34단계 모두 PASS (원본 `after/drill.txt`). 순서는 설치 smoke → v1→v2 업그레이드 → restore 롤백 → 미사용 버전 정리 → export 입니다.
- export 점검: 심어 둔 token·prompt 0건, `[REDACTED]` 9건 (원본 `after/export-scan.txt`).
- 별도 리뷰 레인의 지적을 반영했습니다.
  - restore 가 중간에 끊기면 백업의 intent 가 재전송되던 문제
  - upgrade 뒤 hook 이 구 binary 로 daemon 을 띄우던 경합
  - stale WAL 처리
  - install 실패 시 복구 안내
  - redaction 누락 (URL 인증정보, ticket, probe 명령)
  - dist 경로
  - drill 이식성

## 완료 기준 대비

| 기준 | 상태 |
| --- | --- |
| 설치 smoke(05 §17.1)와 업그레이드·롤백 drill 통과 | 충족 — `drill.sh` 34/34 PASS |
| 백업 복원 후 기존 UI cursor 가 reset_required (EVT-11) | 충족 — 단위 테스트와 drill 의 generation 변화 |
| 진단 export 에 비밀값 없음 | 충족 — 심어 둔 값 0건 |

## 남은 이슈

- 넣지 않은 metric: lease renewal failure, backend observation age, scheduler ready-to-start latency, revalidation count.
- 복원 시점에 보내지 않았던 prompt·interrupt intent 는 UNKNOWN 으로 남습니다. 이것을 사람이 해소하는 API 는 아직 없습니다.
- drill 의 v1·v2 는 schema 가 같습니다. schema 가 바뀌는 업그레이드는 단위 테스트로만 확인했습니다.
- 실제 Claude 관리 세션을 띄운 상태의 업그레이드는 실측하지 않았습니다.
