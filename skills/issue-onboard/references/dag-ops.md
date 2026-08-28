# Onboard 그래프 연산 — sync · link · plan · next · validate

## sync — 그래프 갱신

```bash
node <skill>/scripts/issue-onboard.mjs sync [--state open|closed|all] [--limit <n>]
```

- 트래커에서 이슈를 끌어와 노드를 갱신한다(기본 `--state all`, `--limit 200`).
- 엣지는 **세 정본**에서 모은다(모두 GitHub 이 정본, graph.json 은 재생성 캐시).

```text
① 본문 마커        "depends on #N" / "depends-on #N" / "blocked by #N" / "needs #N" → depends-on
                   "blocks #N"                                            → 역방향 depends-on
② 결정 코멘트      <!-- issue-graph-v2-decision … --> (link/unlink 가 남긴 승인/철회)
③ 네이티브 의존성  GitHub 이슈 dependencies(blocked-by) — gh api graphql 로 조회
```

- ①③ 자동 엣지는 `createdBy=sync`/`github-native` 로 매번 다시 계산한다. ② 결정 엣지는 승인 코멘트가 남아 있는 한 보존된다.
- ③ 은 실패(구형 GitHub Enterprise·오프라인·권한)하면 조용히 건너뛴다. `--no-native` 로 끌 수 있다.
- 같은 `from|to|depends-on` 이 여러 정본에서 잡히면 본문 마커(근거가 풍부) → 네이티브 → 결정 순으로 하나만 남긴다.
- 출력 `CYCLE=` 이 비어 있지 않으면 순환이 있다. `validate` 로 경로를 확인한다. `NATIVE_QUERIED`/`NATIVE_SKIPPED` 로 네이티브 조회 상태를 확인한다.

인자 없이 온보딩을 실행하면 캐시가 없거나 현재 열린 이슈와 불일치할 때 위 sync를 자동으로
선행한다. 자동 sync가 실패하거나 완전 snapshot을 만들지 못하면 추천을 내지 않는다.

## link / unlink — 선수·후속 손보기

```bash
node <skill>/scripts/issue-onboard.mjs link <from> <to> [--type depends-on|parent-of|duplicate-of|relates-to|supersedes] [--why "<근거>"]
node <skill>/scripts/issue-onboard.mjs unlink <from> <to> [--type <type>]
```

- 방향: `from --depends-on--> to` = "to 가 선수(먼저 close), from 이 후속". 즉 `link 70 60` 은 #70 이 #60 을 기다리게 한다.
- V2 는 GitHub 이 정본이므로 로컬 엣지를 쓰지 않는다. `link` 는 대상(**from**) 이슈에 구조화된 **승인 결정 코멘트**를 남기고 곧바로 재-sync 해 엣지를 실체화한다.
- `--type` 기본값은 `depends-on`. `--why` 로 근거를 남긴다(provenance/evidence 에 들어간다).
- 순서 엣지(depends-on)가 순환을 만들면 **코멘트를 남기기 전에** 거부한다(exit 2). 자기 자신 엣지도 거부한다.
- `unlink` 는 같은 관계에 **철회 결정 코멘트**를 남기고 재-sync 한다(그 결정 엣지가 사라진다).
- 단, 본문 마커(`depends on #N`)나 GitHub 네이티브 의존성으로 생긴 엣지는 `unlink` 로 지워지지 않는다 — 본문을 고치거나 GitHub 에서 의존성 링크를 직접 해제해야 한다.

## plan (todo) — 분류 산출

```bash
node <skill>/scripts/issue-onboard.mjs plan [--json]
```

각 노드를 네 부류로 나눈다. 판정 규칙:

```text
done         status == close
blocked      선행(prereq) 중 close 가 아닌 것이 있음
in-progress  선행이 전부 close 이고 status ∈ {plan, in-process, review}
ready        선행이 전부 close 이고 status == open
```

- `ready` 와 `in-progress` 는 우선순위(priorityRank) → 번호 순으로 정렬한다.
- `blocked` 은 각 항목에 "대기 중인 선행 번호"를 함께 낸다.
- 기계 출력: `READY_NUMBERS` / `BLOCKED_NUMBERS` / `IN_PROGRESS_NUMBERS` / `DONE_NUMBERS`.
- 인자 없이 `issue-onboard` 만 부르면 온보딩을 시작한다.

## next — 다음 착수 추천

```bash
node <skill>/scripts/issue-onboard.mjs next
```

ready-frontier 의 첫 이슈(우선순위·번호 순)를 골라 `NEXT=$issue-start #N` 을 제안한다.
ready 가 비면 진행 중 목록을 안내한다.

## validate — 점검

```bash
node <skill>/scripts/issue-onboard.mjs validate
```

- **사이클**: 순서 엣지에서 순환을 찾으면 경로를 내고 exit 2.
- **dangling 엣지**: from/to 가 노드에 없는 엣지.
- **알 수 없는 타입**: EDGE_TYPES 밖의 엣지.
- **close 불일치**: done 인 노드가 아직 done 이 아닌 선행에 의존.
- 문제가 하나라도 있으면 exit 1(사이클이면 2). 없으면 `VALID=1`.

## 전형적 흐름

```text
sync                      # 그래프를 최신으로
plan                      # 지금 뭐부터 할 수 있나
link 70 60 --why "..."    # 자동 감지 못한 의존 보강
validate                  # 순환·불일치 없나
next                      # 다음 착수 1건 → $issue-start #N
```
