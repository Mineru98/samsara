# Manual QA — issue #19 cache integrity

Overall verdict: PASS.

The onboarding CLI now serializes final graph reload, validation, classification, and synchronous recommendation output under `.issue/graph.json.lock`. `saveGraph` and every vendored `patchGraphNode` writer used by issue-create, issue-start, issue-end, issue-merge, issue-onboard, and issue-sync use the same exclusive sidecar protocol. A writer that cannot acquire the lock fails closed, so supported cache replacement cannot occur between the final validation and recommendation output.

Review identity: branch `fix/19-onboard-cache-integrity`, source commit `6abb547e3a53d948cb555cae6b36d028e5b296d5`, base `origin/main` `1e13c115117a3342f733bad7af9f050b9a814d7d`.

Worktree note: `git status --short --untracked-files=no` is empty. The user-requested `.local/` state remains untracked and untouched.

## Scenario coverage

| scenario | surface | observed result | verdict |
| --- | --- | --- | --- |
| Normal automatic bootstrap | Node CLI subprocess fixture | Complete sync followed by `ONBOARD_COUNT=1` | PASS |
| Malformed/schema-invalid cache | Node CLI subprocess fixture | Failed bootstrap exits nonzero and emits no recommendation markers | PASS |
| Partial/failed sync | Node CLI subprocess fixture | Recommendation is not unlocked | PASS |
| Stale cache after successful sync | Node CLI subprocess fixture | Cache mismatch is rejected before recommendation | PASS |
| Cache change during live issue reads | Node CLI subprocess fixture | Nonzero exit and no `ONBOARD_COUNT`/`PRIORITY` | PASS |
| Cache change after final graph load | Node CLI subprocess fixture | `FINAL_CACHE_VALIDATION` exits 1 with no recommendation | PASS |
| Raw cache change during final guard | Node CLI subprocess fixture | `OUTPUT_CACHE_VALIDATION` exits 1 with no recommendation | PASS |
| Official writer at output boundary | Parent CLI plus child `saveGraph` | Writer exits 1 on lock; output is stable and cache is unchanged | PASS |
| Direct graph writer lock handling | Node unit test | `saveGraph` throws and `patchGraphNode` returns false while lock exists | PASS |
| Ontology/trust regressions | Node test runners | All existing trust and ontology cases pass | PASS |

## Test results

| command | exit | observed |
| --- | ---: | --- |
| `node --test skills/issue-onboard/scripts/*.test.mjs` | 0 | 49 tests, 49 pass, 0 fail |
| `node --test tools/issue-ontology/ontology.test.mjs` | 0 | 13 tests, 13 pass, 0 fail |
| syntax checks for changed JavaScript files | 0 | all eight checks pass |
| `git diff --check origin/main...HEAD` | 0 | no whitespace errors |
| targeted cache/lock regression command | 0 | 6 tests, 6 pass; invalid and lock markers as recorded in `after/final-cache-validation.txt` |

## Evidence artifacts

- Runtime transcript: `.issue/19/evidence/19-manual-qa-runtime.txt`
- Status record: `.issue/19/evidence/19-manual-qa-status.txt`
- Full test summary: `.issue/19/evidence/after/test-suite.txt`
- Cache boundary summary: `.issue/19/evidence/after/final-cache-validation.txt`
- Before/after CLI fixtures: `.issue/19/evidence/before/toctou-cli.txt`, `.issue/19/evidence/after/toctou-cli.txt`

No browser screenshot was needed: this is a terminal/CLI cache-consistency defect, and the observable contract is exit status, output markers, and persisted graph contents.
