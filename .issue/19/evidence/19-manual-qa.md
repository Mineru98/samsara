# Manual QA — issue #19 merge critique

Overall verdict: PASS for the revised runtime-validation plan. The branch artifacts and fresh branch-worktree runs support the 45 issue-onboard test claim, 13 ontology test claim, targeted cache evidence, and before/after behavior comparison. The branch worktree has a pre-existing untracked `.local/` directory; tracked files are clean, but the full worktree should not be described as pristine.

## manualQa.surfaceEvidence

| Scenario | Criterion reference | Surface | Exact invocation | Verdict | Artifact refs |
| --- | --- | --- | --- | --- | --- |
| S1 | Revised plan: issue-onboard suite is exactly 45 passing tests | Terminal / Node test runner | `node --test skills/issue-onboard/scripts/*.test.mjs` | PASS | A1, A2 |
| S2 | Revised plan: ontology suite is exactly 13 passing tests | Terminal / Node test runner | `node --test tools/issue-ontology/ontology.test.mjs` | PASS | A1, A3 |
| S3 | Revised plan: both syntax checks exit 0 | Terminal / Node syntax checker | `node --check skills/issue-onboard/scripts/issue-onboard.mjs`; `node --check skills/issue-onboard/scripts/bootstrap.test.mjs` | PASS | A1, A4 |
| S4 | Revised plan: malformed and schema-invalid caches fail closed; normal bootstrap remains valid | Terminal / Node targeted test runner | `node --test --test-name-pattern='reports invalid caches and fails when bootstrap fails|onboarding CLI bootstraps a complete sync' skills/issue-onboard/scripts/bootstrap.test.mjs` | PASS | A1, A5, A6 |
| S5 | Revised plan: TOCTOU cache mutation produces no recommendation and a non-zero result | Terminal / Node focused regression test | `node --test --test-name-pattern='fails closed when the graph cache changes during live issue reads' skills/issue-onboard/scripts/bootstrap.test.mjs` | PASS | A1, A7, A8 |
| S6 | Revised plan: before/after output comparison demonstrates the defect and fix | Branch evidence files | Read-only inspection of `.issue/19/evidence/before/toctou-cli.txt` and `.issue/19/evidence/after/toctou-cli.txt` | PASS | A8, A9, A10 |
| S7 | QA integrity: no tracked worktree mutation and no diff-check errors | Terminal / git worktree inspection | `git status --short --untracked-files=no`; `git diff --check origin/main...HEAD` | PASS with note | A1, A11 |

## manualQa.adversarialCases

| Scenario | Criterion reference | Adversarial class | Expected behavior | Verdict | Artifact refs |
| --- | --- | --- | --- | --- | --- |
| S1/S2 | Test-count claims | misleading_output | Runtime totals must match the recorded 45 and 13 claims, with zero failures/skips. | PASS | A1, A2, A3 |
| S4 | Invalid-cache criterion | malformed_input | Malformed and schema-invalid caches must exit 7, report validation failure, and emit no recommendation. | PASS | A5, A6 |
| S5 | TOCTOU criterion | stale_state | If the cache changes during live reads, onboarding must fail closed with no `ONBOARD_COUNT` or `PRIORITY`. | PASS | A1, A7, A8 |
| S1 | Bootstrap reliability | partial_sync | Failed or partial sync must not unlock recommendations. | PASS | A1 |
| S1 | Process safety | hung_command | Bootstrap execution must remain bounded; the suite's bounded-output/time test passes. | PASS | A1 |
| S7 | QA integrity | dirty_worktree | QA must detect pre-existing dirt and avoid claiming a pristine worktree; tracked files remain unchanged. | PASS with note | A11 |

## artifactRefs

| ID | Kind | Description | Path |
| --- | --- | --- | --- |
| A1 | terminal transcript | Fresh tmux terminal runs S1–S5 and read-only checks, summarized from the observed output | `.issue/19/evidence/19-manual-qa-runtime.txt` |
| A2 | test output | Branch-recorded issue-onboard suite, 45/45 pass | `/Users/mineru/SourceCode/samsara-issue-19/.issue/19/evidence/after/test-suite.txt` |
| A3 | test output | Branch-recorded ontology suite, 13/13 pass | `/Users/mineru/SourceCode/samsara-issue-19/.issue/19/evidence/after/test-suite.txt` |
| A4 | test output | Branch-recorded syntax checks | `/Users/mineru/SourceCode/samsara-issue-19/.issue/19/evidence/after/test-suite.txt` |
| A5 | targeted test output | Fresh targeted cache/bootstrap run | `.issue/19/evidence/19-manual-qa-runtime.txt` |
| A6 | recorded evidence | Malformed/schema-invalid cache results and targeted test result | `/Users/mineru/SourceCode/samsara-issue-19/.issue/19/evidence/after/cache-validation.txt` |
| A7 | targeted test output | Fresh focused TOCTOU regression run | `.issue/19/evidence/19-manual-qa-runtime.txt` |
| A8 | recorded evidence | Before/after TOCTOU CLI outputs | `/Users/mineru/SourceCode/samsara-issue-19/.issue/19/evidence/before/toctou-cli.txt`; `/Users/mineru/SourceCode/samsara-issue-19/.issue/19/evidence/after/toctou-cli.txt` |
| A9 | recorded evidence | Historical regression test was red before the fix | `/Users/mineru/SourceCode/samsara-issue-19/.issue/19/evidence/before/regression-red.txt` |
| A10 | recorded evidence | Normal cache remains valid and recommends successfully | `/Users/mineru/SourceCode/samsara-issue-19/.issue/19/evidence/after/normal-cli.txt` |
| A11 | git status/diff output | Tracked-clean status, pre-existing untracked `.local/`, and clean diff check | `.issue/19/evidence/19-manual-qa-status.txt` |
