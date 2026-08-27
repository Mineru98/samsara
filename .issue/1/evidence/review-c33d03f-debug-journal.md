# Debug Journal — issue #1 exact-SHA review
Started: 2026-08-26 Asia/Seoul
Goal: Read-only manual QA and review of c33d03fae7efe04130d4f21e22f494d0d1e8517f.

## Environment snapshot
- Runtime: Grok Build CLI 1.0.5, native macOS arm64 executable at `/Users/mineru/.grok/bin/grok`.
- Review worktree: `/tmp/samsara-issue-1-review-40c32a1`.
- Git HEAD: `c33d03fae7efe04130d4f21e22f494d0d1e8517f`; worktree clean before QA.
- Base: `main` at `4d5754f2bf0c9c7284f5c9a167bd9f7761045f77`.
- References read: debugging methodology 00-setup, 02-investigate, 08-qa, 09-cleanup, native-binary runtime reference.

## Hypotheses
1. [OPEN] The exact commit's explicit Grok manifest validates and exposes the documented default component paths. Distinguishing evidence: `grok plugin validate .` and JSON/path counts.
2. [OPEN] The README's `fac10ac...` ref is an immutable, manifest-containing source and avoids self-reference. Distinguishing evidence: `git cat-file`, `git ls-tree`, and README assertions.
3. [OPEN] The read-only QA path has no silent failure or dirty-worktree side effect. Distinguishing evidence: tmux exit codes/output plus before/after status and read-only state checks.

## Artifacts to revert
- [ ] tmux QA sessions created for this review; revert by killing each named session after capture.
- [ ] `/Users/mineru/SourceCode/samsara/.issue/1/evidence/review-c33d03f-qa.sh` — journaled read-only QA script; retain as command evidence.
- [ ] Review evidence files under `/Users/mineru/SourceCode/samsara/.issue/1/evidence/`; these are the requested QA artifacts and should be retained, not reverted.

## Findings
Runtime observations and exact command transcripts will be appended to the review evidence artifacts.
