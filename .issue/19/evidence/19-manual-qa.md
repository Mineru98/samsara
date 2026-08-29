# Manual QA — issue #19 cache integrity

Overall verdict: PASS.

The onboarding CLI now serializes final graph reload, validation, classification, and synchronous recommendation output under `.issue/graph.json.lock`. `saveGraph` and every vendored `patchGraphNode` writer used by issue-create, issue-start, issue-end, issue-merge, issue-onboard, and issue-sync use the same exclusive sidecar protocol. A writer that cannot acquire the lock fails closed, so supported cache replacement cannot occur between the final validation and recommendation output. Every supported writer also rejects symlinked `.issue` and `graph.json` paths before reading or writing, opens the final file with `O_NOFOLLOW`, and verifies the parent/file inode so a last-moment path replacement cannot redirect the write.

Review identity: branch `fix/19-onboard-cache-integrity`, source commit `c3f508252a4ba8798a734457243d02fdb453b66d`, base `origin/main` `3f46d8a857ce9bd87714e0064a74bccd41b77d10`.

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
| Symlinked graph writer paths | Six common-module writers | `.issue` and `graph.json` symlink cases return false and leave the outside graph unchanged | PASS |
| Symlink swap at final open | Six common-module writers | `O_NOFOLLOW` rejects the swapped target and leaves the outside graph unchanged | PASS |
| Parent-directory symlink swap at final open | Six common-module writers | Parent inode validation rejects the replacement before truncate/write and leaves the outside graph unchanged | PASS |
| Ontology/trust regressions | Node test runners | All existing trust and ontology cases pass | PASS |

## Test results

| command | exit | observed |
| --- | ---: | --- |
| `node --test skills/issue-onboard/scripts/*.test.mjs` | 0 | 52 tests, 52 pass, 0 fail |
| `node --test tools/issue-ontology/ontology.test.mjs` | 0 | 13 tests, 13 pass, 0 fail |
| syntax checks for changed JavaScript files | 0 | all eight checks pass |
| `git diff --check origin/main...HEAD` | 0 | no whitespace errors |
| targeted cache/lock/symlink regression command | 0 | 9 tests, 9 pass; invalid, lock, symlink, final-open, and parent-directory swap markers as recorded in `after/final-cache-validation.txt` |

## Evidence artifacts

- Runtime transcript: `.issue/19/evidence/19-manual-qa-runtime.txt`
- Status record: `.issue/19/evidence/19-manual-qa-status.txt`
- Full test summary: `.issue/19/evidence/after/test-suite.txt`
- Cache boundary summary: `.issue/19/evidence/after/final-cache-validation.txt`
- Before/after CLI fixtures: `.issue/19/evidence/before/toctou-cli.txt`, `.issue/19/evidence/after/toctou-cli.txt`

No browser screenshot was needed: this is a terminal/CLI cache-consistency defect, and the observable contract is exit status, output markers, and persisted graph contents.
