# Manual QA — issue #19 cache integrity

Overall verdict: PASS.

The onboarding CLI now serializes final graph reload, validation, classification, and synchronous recommendation output under `.issue/graph.json.lock`. `saveGraph` and every vendored `patchGraphNode` writer used by issue-create, issue-start, issue-end, issue-merge, issue-onboard, and issue-sync use the same exclusive sidecar protocol. A writer that cannot acquire the lock fails closed, so supported cache replacement cannot occur between the final validation and recommendation output. Every supported writer also rejects symlinked or hard-linked `.issue/graph.json` paths before reading or writing, reads through an `O_NOFOLLOW` descriptor, and anchors temporary-file creation, cleanup, and atomic replacement to the verified parent directory.

Review identity: branch `fix/19-onboard-cache-integrity`, implementation commit `8900cb9ce0681a67dc49642a6db52871a3ffa00e`, base `origin/main` `b9e0277e61dc56eabb5e7a19851169ef44943261`.

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
| Raw cache change before recommendation output | Node CLI subprocess fixture | `OUTPUT_CACHE_VALIDATION` exits 1 with no recommendation | PASS |
| Official writer at output boundary | Parent CLI plus child `saveGraph` | Writer exits 1 on lock; output is stable and cache is unchanged | PASS |
| Direct graph writer lock handling | Node unit test | `saveGraph` throws and `patchGraphNode` returns false while lock exists | PASS |
| Stale graph lock recovery | Node unit test | A valid lock owned by a dead PID is reclaimed; malformed or live-owner locks remain blocking | PASS |
| Stale lock handoff immediately before cleanup | Six official writers | Replacement lock is restored at the canonical path and a second writer is blocked | PASS |
| Symlinked graph writer paths | Six common-module writers | `.issue` and `graph.json` symlink cases return false and leave the outside graph unchanged | PASS |
| Hard-linked graph writer paths | Six common-module writers | Hard-linked cache files are rejected and both linked contents remain unchanged | PASS |
| Symlink swap at final open | Six common-module writers | `O_NOFOLLOW` rejects the swapped target and leaves the outside graph unchanged | PASS |
| Parent-directory symlink swap at final open | Six common-module writers | Parent inode validation rejects the replacement before any graph mutation and leaves the outside graph unchanged | PASS |
| Final-file symlink swap before replacement | Six common-module writers | Destination identity validation rejects the replacement and leaves the outside graph unchanged | PASS |
| `saveGraph` parent swap at final rename | Node unit test | Stable-parent rename fails closed and preserves outside graph/temp files | PASS |
| Ontology/trust regressions | Node test runners | All existing trust and ontology cases pass | PASS |

## Test results

| command | exit | observed |
| --- | ---: | --- |
| `node --test skills/issue-onboard/scripts/*.test.mjs` | 0 | 62 tests, 62 pass, 0 fail |
| `node --test tools/issue-ontology/ontology.test.mjs` | 0 | 13 tests, 13 pass, 0 fail |
| syntax checks for changed JavaScript files | 0 | all eight checks pass |
| `git diff --check origin/main...HEAD` | 0 | no whitespace errors |
| targeted cache/lock/path-integrity regression command | 0 | 14 tests, 14 pass; invalid, lock recovery, pre-rename lock handoff, hard-link, symlink, final-open, final-replacement, and parent-directory rename markers as recorded in `after/final-cache-validation.txt` |

## Evidence artifacts

- Runtime transcript: `.issue/19/evidence/19-manual-qa-runtime.txt`
- Status record: `.issue/19/evidence/19-manual-qa-status.txt`
- Full test summary: `.issue/19/evidence/after/test-suite.txt`
- Cache boundary summary: `.issue/19/evidence/after/final-cache-validation.txt`
- Before/after CLI fixtures: `.issue/19/evidence/before/toctou-cli.txt`, `.issue/19/evidence/after/toctou-cli.txt`

No browser screenshot was needed: this is a terminal/CLI cache-consistency defect, and the observable contract is exit status, output markers, and persisted graph contents.
