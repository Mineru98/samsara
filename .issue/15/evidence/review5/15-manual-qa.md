# manualQa: issue #15

Target SHA: `7b5f2015700e49b13cdde9eba04c752d8e50f996`
Review worktree: `/Users/mineru/SourceCode/samsara-issue-15-review5`
Verdict: PASS
Severity: none
Confidence: high
Blocker: none

## surfaceEvidence

| Scenario | Criterion | Surface | Exact invocation | Verdict | Artifact refs |
|---|---|---|---|---|---|
| missing-cache-bootstrap | bootstrap missing cache, then recommend only complete snapshot | CLI | `node <fixture>/skills/issue-onboard/scripts/issue-onboard.mjs onboard --no-llm` | PASS, exit 0; `GRAPH_BOOTSTRAP_REASON=missing`, `SNAPSHOT_STATUS=complete`, `ONBOARD_COUNT=1`; `.issue/graph.json` existed | `cli-e2e` |
| stale-cache-recovery | stale revision/cache is refreshed before recommendation | CLI | same disposable CLI invocation with old title/revision cache | PASS, exit 0; `GRAPH_BOOTSTRAP_REASON=snapshot-changed`, `ONBOARD_COUNT=1` | `cli-e2e` |
| invalid-cache-recovery | malformed cache is treated as unusable and rebuilt | CLI | same disposable CLI invocation with `.issue/graph.json` = `{ malformed` | PASS, exit 0; parse error observed, `GRAPH_BOOTSTRAP_REASON=invalid`, `ONBOARD_COUNT=1` | `cli-e2e` |
| pagination-beyond-200 | default live snapshot probes beyond a full 200-item response | CLI | same invocation against a fake `gh` returning 200 then 201 items | PASS, exit 0; `--limit 200` and `--limit 400` observed, `OPEN_ISSUES=201`, `ONBOARD_COUNT=6`, `MORE_AVAILABLE=1` | `cli-e2e` |
| sync-partial-fail-closed | partial sync cannot unlock recommendations | CLI | same invocation with trusted sync fixture emitting `SNAPSHOT_STATUS=partial` and `GRAPH_SYNC=failed` | PASS, exit 1; no `ONBOARD_COUNT` or priority output | `cli-e2e` |
| sync-failed-fail-closed | nonzero sync cannot unlock recommendations | CLI | same invocation with trusted sync fixture exiting 7 | PASS, exit 7; no `ONBOARD_COUNT` or priority output | `cli-e2e` |
| ontology-unavailable | unavailable ontology validator fails closed | CLI | same invocation with external nonexistent `ISSUE_ONTOLOGY_ROOT` | PASS, exit 2; Ajv-unavailable error and no recommendation | `cli-e2e` |
| trusted-local-fallback | repository-local sync cannot replace trusted sibling | CLI | same invocation from trusted install with only malicious repository-local sync | PASS, exit 1; missing trusted `issue-sync` reported and marker was not created | `cli-e2e` |
| environment-stripping | bootstrap subprocess receives restricted environment | CLI + subprocess artifact | same invocation with sentinel and harmless `NODE_OPTIONS` set in parent | PASS, exit 0; child observed `stripped|node-options-stripped` | `cli-e2e` |
| symlink-issue-rejection | `.issue` symlink cannot receive graph writes | CLI | same invocation with `.issue` symlinked outside repository | PASS, exit 1; `.issue` symlink rejection observed and no bootstrap/recommendation output | `cli-e2e`, `automated` |
| exact-marker-parsing | success requires exact marker lines | CLI | `node <trusted-install>/skills/issue-sync/scripts/issue-sync.mjs` with near-match markers | PASS, exit 2; `complete-but-not-a-marker`/`GRAPH_SYNC=okay` rejected and `GRAPH_SYNC=failed` emitted | `cli-e2e` |
| output-bound | bootstrap child output is bounded | CLI | same invocation with trusted sync emitting 17 MiB | PASS, exit 1; parent-captured stdout bounded at 65,536 bytes and no recommendation | `cli-e2e` |
| timeout-bound | bootstrap subprocess is time-bounded | executable function path | Node driver invoking target `runSyncBootstrap` with injected spawn recorder | PASS; observed `timeout=120000`, `maxBuffer=16777216`, allowlisted env only | `cli-e2e`, `automated` |

## adversarialCases

| Scenario | Criterion | Adversarial class | Expected behavior | Verdict | Artifact refs |
|---|---|---|---|---|---|
| missing-cache-bootstrap | complete snapshot required | missing input / misleading success | sync then validate before output | PASS | `cli-e2e` |
| stale-cache-recovery | live/cache agreement | stale state | rebuild, do not trust old cache | PASS | `cli-e2e` |
| invalid-cache-recovery | malformed cache recovery | malformed input | recover through sync, never parse into recommendations | PASS | `cli-e2e` |
| pagination-beyond-200 | complete live coverage | boundary at 200 | continue probing and expose six-item default limit | PASS | `cli-e2e` |
| sync-partial-fail-closed | fail closed on incomplete source | partial output | nonzero/no recommendation | PASS | `cli-e2e` |
| sync-failed-fail-closed | fail closed on subprocess failure | child failure | propagate failure/no recommendation | PASS | `cli-e2e` |
| ontology-unavailable | validator required | unavailable dependency | reject without recommendation | PASS | `cli-e2e` |
| trusted-local-fallback | trusted executable resolution | path substitution | reject repository-local replacement | PASS | `cli-e2e` |
| environment-stripping | restricted bootstrap | environment injection | strip unallowlisted sentinel and `NODE_OPTIONS` | PASS | `cli-e2e` |
| symlink-issue-rejection | safe filesystem writes | symlink traversal | reject before outside write | PASS | `cli-e2e`, `automated` |
| exact-marker-parsing | exact success contract | misleading marker | reject near-match markers | PASS | `cli-e2e` |
| output-bound | bounded child output | output flood | terminate/fail without recommendation | PASS | `cli-e2e` |
| timeout-bound | bounded child execution | hung command | invoke child with 120s timeout | PASS | `cli-e2e` |
| all scenarios | clean side effects | dirty fixture / residue | disposable fixtures removed; no outside `graph.json` observed | PASS | `cleanup` |

## artifactRefs

| ID | Kind | Description | Path |
|---|---|---|---|
| `cli-e2e` | CLI transcript | Complete independent disposable-fixture scenarios with exit code, stdout/stderr summaries, fake-`gh` calls, and checks | `/Users/mineru/SourceCode/samsara-issue-15-review5/.issue/15/evidence/review5/cli-e2e-transcript.txt` |
| `automated` | test output | Repository issue-onboard suite (29/29), ontology suite (13/13), syntax and diff checks | `/Users/mineru/SourceCode/samsara-issue-15-review5/.issue/15/evidence/review5/automated-suites.txt` |
| `cleanup` | filesystem check | Disposable fixture cleanup and absence of outside graph side effects | `/Users/mineru/SourceCode/samsara-issue-15-review5/.issue/15/evidence/review5/cleanup.txt` |

## Code references

- Trusted installation and ontology checks: `skills/issue-onboard/scripts/issue-onboard.mjs:77-112`.
- Symlink-safe graph I/O: `skills/issue-onboard/scripts/issue-onboard.mjs:152-227`.
- Complete-list probing: `skills/issue-onboard/scripts/issue-onboard.mjs:398-409`.
- Bootstrap environment, timeout, output cap, exact markers: `skills/issue-onboard/scripts/issue-onboard.mjs:929-969`.
- Live snapshot validation and fail-closed onboarding: `skills/issue-onboard/scripts/issue-onboard.mjs:979-1029`.
- Exact sync wrapper marker check: `skills/issue-sync/scripts/issue-sync.mjs:9-31`.
