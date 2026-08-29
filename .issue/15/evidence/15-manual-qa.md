# Manual QA: issue #15, commit `e869aa2ad0bb0f28976ae22bac8ac0f38736d267`

Verdict: **FAIL**. Confidence: **high**. Severity: **P1 integrity defect** (S15; concurrent/local cache rewrite can produce a successful recommendation from an invalid on-disk graph). The original review3 worktree was verified at this SHA and its required commands were run before it disappeared during concurrent cleanup. Remaining probes ran from a read-only `git archive` of the exact SHA in `/private/tmp`; target source and prohibited target evidence were never modified.

## P0/P1/P2 coverage brainstorm

- P0: missing cache bootstrap, complete snapshot consumption, failed/partial sync, schema-invalid cache, cycles/dangling, recommendation suppression, sync timeout/hang.
- P1: spoofed success markers, stale cache/freshness, missing sibling skill, resolver symlink path, concurrent cache mutation/TOCTOU, repeated invocation stability.
- P2: inherited environment handling, malformed JSON diagnostics, no unexpected fixture side effects.

## `manualQa` matrix

### surfaceEvidence

| scenario id | criterion | surface | exact invocation | verdict | artifactRefs |
|---|---|---|---|---|---|
| S01 | C1/C2 | issue-onboard CLI | `node <fixture>/skills/issue-onboard/scripts/issue-onboard.mjs --all` | PASS | A-S01 |
| S02 | C1 | issue-onboard CLI | same CLI with malformed `.issue/graph.json` | PASS | A-S02 |
| S03 | C1/C2 | issue-onboard CLI | same CLI with `snapshot.status=partial` cache | PASS | A-S03 |
| S04 | C1 | issue-onboard CLI | same CLI with Ajv-invalid labels shape | PASS | A-S04 |
| S05 | C1 | issue-onboard CLI, repeated read | same CLI twice with complete cache | PASS | A-S05 |
| S06 | C2 | issue-onboard CLI | same CLI with helper stdout spoofing complete/ok and no graph | PASS | A-S06 |
| S07 | C2 | issue-onboard CLI | same CLI with helper writing partial graph but spoofing complete/ok | PASS | A-S07 |
| S08 | C2 | issue-onboard CLI | same CLI with helper exit 7 / failed sync | PASS | A-S08 |
| S09 | C2 | issue-onboard CLI | same CLI without `issue-sync` sibling skill | PASS | A-S09 |
| S10 | C3 | `plan --json` | `node <fixture>/.../issue-onboard.mjs plan --json` | PASS | A-S10 |
| S11 | C3 | `next` | `node <fixture>/.../issue-onboard.mjs next` | PASS | A-S11 |
| S12 | C3 | `plan --json` | same with dangling edge | PASS | A-S12 |
| S13 | C3 | `next` | same with dangling edge | PASS | A-S13 |
| S14 | C1/C2 | symlink launcher CLI + env | `node <fixture>/launcher.mjs --all` | PASS | A-S14 |
| S15 | C2/C3 | CLI + hostile `gh` child rewrite | `node <fixture>/.../issue-onboard.mjs --all` | **FAIL** | A-S15 |
| S16 | C2 | hung sync CLI | `node <fixture>/.../issue-onboard.mjs --all` (executor timeout 1000 ms) | PASS | A-S16 |
| T01 | required tests | Node test runner | `node --test skills/issue-onboard/scripts/bootstrap.test.mjs` | PASS | A-TESTS |
| T02 | required tests | Node test runner | `node --test skills/issue-onboard/scripts/*.test.mjs` | PASS | A-TESTS |
| T03 | required tests | Node test runner | `node tools/issue-ontology/ontology.test.mjs` | PASS | A-TESTS |
| T04 | required syntax check | Node parser | `node --check skills/issue-onboard/scripts/issue-onboard.mjs` | PASS | A-TESTS |

Observed required-test exits: T01 `0` (7/7), T02 `0` (21/21), T03 `0` (13/13), T04 `0`. Every PASS above has a non-empty artifact with actual stdout/stderr/exit and post-run observations.

### adversarialCases

| scenario id | criterion | adversarial class | expected behavior | verdict | artifactRefs |
|---|---|---|---|---|---|
| S02 | C1 | malformed_input | auto-sync, then recommend only complete graph | PASS | A-S02 |
| S03 | C2 | stale_state/partial_cache | recover via sync; never consume partial snapshot | PASS | A-S03 |
| S04 | C1 | schema_invalid | auto-sync before recommendation | PASS | A-S04 |
| S06/S07 | C2 | misleading_output/marker_spoofing | require actual complete usable cache, no recommendation otherwise | PASS | A-S06, A-S07 |
| S08 | C2 | failed_dependency | propagate nonzero failure; no recommendation | PASS | A-S08 |
| S09 | C2 | missing_dependency | name missing issue-sync skill; no recommendation | PASS | A-S09 |
| S10/S11 | C3 | cycle | reject with nonzero exit and empty plan/next sentinel | PASS | A-S10, A-S11 |
| S12/S13 | C3 | dangling_reference | reject with nonzero exit and empty plan/next sentinel | PASS | A-S12, A-S13 |
| S14 | C1 | symlink_boundary | resolve symlink launcher and complete bootstrap safely | PASS | A-S14 |
| S14 | C2 | inherited_env_exposure | no sentinel secret in CLI stdout/stderr | PASS | A-S14 |
| S05 | C1 | repeated_reads | second invocation stable and does not re-sync complete cache | PASS | A-S05 |
| S15 | C2/C3 | TOCTOU/concurrent_cache_mutation | detect changed/invalid cache before recommendation | **FAIL** | A-S15 |
| S16 | C2 | hung_command | bounded nonzero termination; timeout/hang not a pass | PASS | A-S16 |

## Findings and blockers

S15 is reproducible: fake `gh issue list` rewrites `.issue/graph.json` to `{ TOCTOU invalid` after `loadGraph()` but before classification. Actual result was exit `0`, `ONBOARD_COUNT=2`, and empty stderr; post-run graph parse failed. This violates the “never recommend on schema-invalid” boundary under a feasible local concurrent rewrite. Recommend re-reading/verifying cache (or digest/stat consistency) immediately before classification.

The requested target directory `/Users/mineru/SourceCode/samsara-issue-15-review3` no longer exists after initial verification; this is an execution-environment blocker for re-running against that exact checkout path. Exact SHA source verification remained possible through the immutable archive and all required tests passed there with local Ajv dependency.

## artifactRefs

| id | kind | description | path |
|---|---|---|---|
| A-TESTS | test-output | Required test and syntax command exits/counts | [.issue/15/evidence/qa-lane/mandated-tests.txt](./qa-lane/mandated-tests.txt) |
| A-S01 | cli-transcript | Missing cache complete bootstrap | [.issue/15/evidence/qa-lane/S01-missing-cache.txt](./qa-lane/S01-missing-cache.txt) |
| A-S02 | cli-transcript | Malformed cache recovery | [.issue/15/evidence/qa-lane/S02-malformed-cache.txt](./qa-lane/S02-malformed-cache.txt) |
| A-S03 | cli-transcript | Partial cache recovery | [.issue/15/evidence/qa-lane/S03-partial-cache.txt](./qa-lane/S03-partial-cache.txt) |
| A-S04 | cli-transcript | Schema-invalid cache recovery | [.issue/15/evidence/qa-lane/S04-schema-invalid.txt](./qa-lane/S04-schema-invalid.txt) |
| A-S05 | cli-transcript | Complete cache repeated invocation | [.issue/15/evidence/qa-lane/S05-complete-cache-repeat.txt](./qa-lane/S05-complete-cache-repeat.txt) |
| A-S06 | cli-transcript | Spoofed markers, no write | [.issue/15/evidence/qa-lane/S06-spoof-no-write.txt](./qa-lane/S06-spoof-no-write.txt) |
| A-S07 | cli-transcript | Spoofed markers, partial write | [.issue/15/evidence/qa-lane/S07-spoof-partial.txt](./qa-lane/S07-spoof-partial.txt) |
| A-S08 | cli-transcript | Failed sync | [.issue/15/evidence/qa-lane/S08-failed-sync.txt](./qa-lane/S08-failed-sync.txt) |
| A-S09 | cli-transcript | Missing issue-sync skill | [.issue/15/evidence/qa-lane/S09-missing-sync.txt](./qa-lane/S09-missing-sync.txt) |
| A-S10 | cli-transcript | Cycle plan rejection | [.issue/15/evidence/qa-lane/S10-cycle.txt](./qa-lane/S10-cycle.txt) |
| A-S11 | cli-transcript | Cycle next rejection | [.issue/15/evidence/qa-lane/S11-cycle-next.txt](./qa-lane/S11-cycle-next.txt) |
| A-S12 | cli-transcript | Dangling plan rejection | [.issue/15/evidence/qa-lane/S12-dangling.txt](./qa-lane/S12-dangling.txt) |
| A-S13 | cli-transcript | Dangling next rejection | [.issue/15/evidence/qa-lane/S13-dangling-next.txt](./qa-lane/S13-dangling-next.txt) |
| A-S14 | cli-transcript | Symlink launcher and env sentinel check | [.issue/15/evidence/qa-lane/S14-symlink-entry-env.txt](./qa-lane/S14-symlink-entry-env.txt) |
| A-S15 | cli-transcript | Reproducible cache TOCTOU failure | [.issue/15/evidence/qa-lane/S15-toctou-cache-rewrite.txt](./qa-lane/S15-toctou-cache-rewrite.txt) |
| A-S16 | cli-transcript | Hung sync bounded failure | [.issue/15/evidence/qa-lane/S16-hung-sync.txt](./qa-lane/S16-hung-sync.txt) |

