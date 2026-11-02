## 작업 요약

런타임 daemon이 사용자의 `gh` 로그인으로 GitHub의 issue·PR·commit·check·review를 읽기 전용으로 증분 동기화합니다. 결과는 `external_links`에 출처(`source`)·동기화 시각(`synced_at`)과 함께 기록되고, snapshot·`github-history`의 freshness가 `LIVE`/`STALE`로 반영됩니다. `POST /refresh`는 즉시 재조회를 걸고, `github.close_issues: true`일 때만 완료된 로컬 이슈에 연결된 GitHub 이슈를 닫습니다.

## 변경 전후

| 전 | 후 |
| --- | --- |
| ![github-history probe - 변경 전](https://raw.githubusercontent.com/Mineru98/samsara/dev/0.4.0-alpha/.issue/66/evidence/before/github-history.webp) | ![github-history probe - 변경 후](https://raw.githubusercontent.com/Mineru98/samsara/dev/0.4.0-alpha/.issue/66/evidence/after/github-history.webp) |

빨간 박스가 바뀐 구간입니다. 실제 daemon을 격리 환경에서 띄우고 origin=`github.com/Mineru98/samsara` 저장소를 등록한 뒤, 수동 링크 1건 → refresh → 조회 순으로 측정했습니다.

| 항목 | 전 | 후 |
| --- | ---: | ---: |
| `github-history.sync` | `NOT_CONFIGURED` | **`OK`** |
| `github-history.freshness` | `UNAVAILABLE` | **`LIVE`** |
| snapshot `freshness.github` (refresh 후) | `UNAVAILABLE` | **`LIVE`** |
| 이력 항목 수 | 1 (수동 링크) | **214** (PR 41 · commit 130 · issue 43) |
| 항목별 `synced_at`·`source` | 없음 | **있음** |
| DB·daemon 로그에서 `gh auth token` 값 검색 | 0 | 0 |

이 저장소의 PR에는 check run과 review가 실제로 0건이라 실측에서는 0건으로 나옵니다(`gh api`로 확인). check·review 기록은 단위 테스트로 검증했습니다.

원본: [before probe 출력](https://github.com/Mineru98/samsara/blob/dev/0.4.0-alpha/.issue/66/evidence/before/github-history.txt) · [after probe 출력](https://github.com/Mineru98/samsara/blob/dev/0.4.0-alpha/.issue/66/evidence/after/github-history.txt) · [probe 스크립트](https://github.com/Mineru98/samsara/blob/dev/0.4.0-alpha/.issue/66/evidence/probe.sh)

## 완료 기준 대조

- [x] 시험 저장소의 PR·commit이 이력에 나타나고 동기화 시각이 표시된다 — 위 실측(PR 41, commit 130, `last_synced_at`·항목별 `synced_at`).
- [x] GitHub 장애나 rate limit 상황에서 로컬 업무 상태가 바뀌지 않는다 — `TestGitHubFailuresLeaveLocalStateAlone`: rate limit(403 + reset)·404에서 이슈 version·상태 불변, 기존 이력 보존, rate limit 창 안에서는 refresh해도 재호출하지 않음. GitHub 404는 `REPO_NOT_FOUND`로, 없는 프로젝트는 그대로 `RESOURCE_NOT_FOUND`로 구분.
- [x] 토큰 값이 DB·로그에 남지 않는다 — 런타임은 토큰을 읽지 않고 `gh api` 하위 프로세스에 인증을 맡김. 오류 문자열은 토큰 형태를 가림 처리. 실측 grep 0건, 테스트로 `last_error` 가림 처리 확인.

## 변경 파일

- `runtime/internal/github/` (신규) — `gh api --include` 클라이언트, 오류 분류(`AUTH_REQUIRED`·`REPO_NOT_FOUND`·`RATE_LIMITED`·`PROVIDER_ERROR`), cursor 증분 sync, 이슈 닫기(GET 후 PATCH)
- `runtime/internal/app/github.go` (신규) — reconcile과 분리된 `GitHubLoop`, backoff, refresh, 완료 시 닫기 outbox(opt-in)
- `runtime/internal/store/sqlite/migrations/0002_github_sync.sql` — `external_links`에 `state`·`source`·`synced_at`·`detail`, `github_sync_state` 테이블
- `runtime/internal/config/config.go` — `github.sync`(기본 `auto`)·`repository`·`sync_interval_seconds`(300)·`close_issues`(기본 `false`)
- `runtime/internal/app/queries.go` — snapshot freshness, `github-history` 응답 확장(최근 500건 + `truncated`), github issue/PR 링크 key를 `owner/name#N`으로 검증
- `runtime/internal/transport/httpapi/server.go` — refresh 응답에 `github` 상태
- `runtime/internal/app/{merge,outbox,app}.go`, `runtime/internal/daemon/daemon.go` — 연결
- `runtime/README.md`, `docs/architecture/06-runtime-remaining-work.md` — 상태 갱신

## 검증

- `go vet ./...`, `go test ./...`, `go test -race ./internal/app/ ./internal/github/ ./internal/daemon/` 통과 — [출력](https://github.com/Mineru98/samsara/blob/dev/0.4.0-alpha/.issue/66/evidence/after/go-test.txt)
- 신규 테스트: 동기화·출처 보존, 장애 격리, 미설정·비활성, 진행 중 refresh 보존, 링크 key 검증, 닫기 opt-in(기본 off / on / 이미 닫힘), 인증 실패 후 닫기 재시도, commit 페이지 순회, PR 단위 실패 격리
- 독립 리뷰에서 나온 지적 8건 반영(commit 페이지 순회·24h 겹침, 열린 PR check 재조회, PR 단위 실패 격리 등)

## 남은 이슈

- commit은 기본 브랜치만, 한 번에 최대 300건 읽습니다.
- GitHub Enterprise 원격과 PR↔로컬 이슈 자동 연결은 지원하지 않습니다. 로컬 이슈 연결은 `samsara link add --issue`로 합니다.
- 닫기 outbox는 기존 재시도 상한(8회)을 따릅니다. 인증 실패는 10분 간격이라 약 80분 뒤 FAILED가 됩니다.
