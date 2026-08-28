---
name: issue-sync
description: GitHub 이슈 완전 스냅샷으로 .issue/graph.json 캐시를 생성·갱신합니다. "$issue-sync", "이슈 그래프 갱신", "그래프 만들기" 요청과 issue-onboard의 자동 부트스트랩에 사용.
---

# Issue Sync

GitHub 이슈가 정본이고 `.issue/graph.json`은 재생성 가능한 캐시다. 이 스킬은 부분 수정하지 않고
`issue-onboard sync --state all`로 전체 스냅샷을 다시 만든다.

## 실행

```bash
node <issue-sync>/scripts/issue-sync.mjs
```

수동 실행에서는 같은 플러그인의 `skills/issue-onboard`를 먼저 찾고, 없으면 사용자 스킬 디렉터리를 찾아 실행한다.
`issue-onboard`가 자동으로 부트스트랩할 때는 보안상 현재 설치 묶음에 있는 신뢰된 형제
`issue-sync`만 실행하며, 프로젝트 저장소의 동명 스크립트로 대체하지 않는다.

## 성공 조건

출력에 정확히 `SNAPSHOT_STATUS=complete`와 `GRAPH_SYNC=ok` 줄이 함께 나와야 한다. 기본 실행은
목록 한도가 찬 페이지를 계속 조회해 전체 이슈 목록을 확인하며, 최대 한도에서도 끝나지 않으면
`partial`로 남긴다. GitHub 네이티브 의존성 조회나 참조 이슈 조회를 끝까지 증명하지 못한
경우도 `partial`로 남긴다. partial·실패·무결성 digest 불일치 결과는 온보딩 추천에 사용하지 않는다.

`issue-onboard`는 graph.json이 없거나 캐시가 불완전·유효하지 않거나 현재 전체 이슈 목록과
불일치할 때 이 스킬을 먼저 호출한다. 온톨로지 검증기를 사용할 수 없거나 `.issue` 경로가
심볼릭 링크이면 추천하지 않는다. 상태 전이 뒤의 선택적 캐시 갱신은 `setTrackerStatus` 내부에서
자동으로 처리한다.
