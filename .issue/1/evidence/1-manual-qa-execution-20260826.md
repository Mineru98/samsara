# Manual QA execution evidence — issue #1 / PR #2

Date: 2026-08-26 (Asia/Seoul)

Scope: `/Users/mineru/SourceCode/samsara-issue-1` at `fba26c089e85ba6645360fb97344e7b5060957e2`; base tree `/Users/mineru/SourceCode/samsara/.issue/merge/base` at `762ed2f545ddb8cb904dc5c55d7e63aef874d167`.

No install, fetch, checkout, merge, push, issue/PR mutation, or mutating Grok command was run.

## Executed commands and results

### Feature Grok validation surface

Invocation from `/Users/mineru/SourceCode/samsara-issue-1`:

```sh
grok plugin validate .
```

Exit `0`:

```text
Plugin manifest is valid.
  name: samsara
  version: 0.1.0
  description: Issue ontology harness for disciplined, evidence-backed software changes.
  components: 1 skill dir(s), 0 command dir(s), 1 agent dir(s)
```

Invocation from the same feature worktree:

```sh
grok inspect --json
```

Exit `0`; the live JSON parsed successfully. Observed summary: `grokVersion=1.0.5`, `cwd=/Users/mineru/SourceCode/samsara-issue-1`, `projectTrusted=true`, `marketplaces=[]`, and one enabled user `samsara` plugin providing 8 skills, 4 agents, no hooks, and no MCP servers.

### Feature JSON/path/README surface

Invocation from `/Users/mineru/SourceCode/samsara-issue-1`: a Node assertion script parsed the five JSON files `.grok-plugin/plugin.json`, `.claude-plugin/plugin.json`, `.codex-plugin/plugin.json`, `.claude-plugin/marketplace.json`, and `.agents/plugins/marketplace.json`; checked `.grok-plugin/plugin.json`, all existing metadata, `assets/logo.png`, root `skills/`, and root `agents/`; counted `8` `skills/*/SKILL.md` files and `4` root agent Markdown files; and asserted README install, update, coexistence/compatibility, SHA/trust, Claude, Codex, and official xAI references plus a lowercase 40-character `@SHA`.

```sh
node --input-type=module -e '<JSON/path/README assertion script described above>'
```

Exit `0`:

```json
{
  "jsonFiles": 5,
  "manifest": "samsara",
  "version": "0.1.0",
  "skillCount": 8,
  "agentCount": 4,
  "shaPins": ["fac10ac385f41c217f94d9565e0cec416288d37e"],
  "checks": {
    "install": true,
    "update": true,
    "compatibility": true,
    "shaTrust": true,
    "officialXai": true,
    "claude": true,
    "codex": true
  }
}
```

The feature-to-base preservation assertion also exited `0`:

```sh
git diff --quiet 762ed2f545ddb8cb904dc5c55d7e63aef874d167..HEAD -- .claude-plugin .codex-plugin skills agents
```

### Base ontology test surface

Invocation from `/Users/mineru/SourceCode/samsara/.issue/merge/base`:

```sh
node --test tools/issue-ontology/ontology.test.mjs
```

Exit `0`: `tests 10`, `pass 10`, `fail 0`, `cancelled 0`, `skipped 0`, `todo 0`.

### Git integrity surface

Invocations from the feature worktree:

```sh
git diff origin/main...HEAD --check
git show --check --oneline --no-renames HEAD
```

Both exited `0`; the second reported `fba26c0 docs(issue-1): clarify evidence provenance`.

### Remote and tracker surface

Read-only invocations:

```sh
git ls-remote --heads origin refs/heads/feat/1-grok-build-plugin-support refs/heads/main
gh pr view 2 --repo Mineru98/samsara --json number,state,isDraft,mergeStateStatus,statusCheckRollup,headRefName,headRefOid,baseRefName,baseRefOid,url
gh issue view 1 --repo Mineru98/samsara --json number,state,title,url
gh pr view 2 --repo Mineru98/samsara --json body --jq '.body' | rg -n -i 'closes|fixes|resolves[[:space:]]+#?1' || true
```

Observed: remote feature `fba26c0…`, remote main `762ed2f…`; PR #2 OPEN, non-draft, `mergeStateStatus=CLEAN`, head `fba26c0…`, `statusCheckRollup=[]`; issue #1 OPEN; trigger scan produced no match. GitHub API separately reported PR base OID `0bfaa6b…` while the live main ref is `762ed2f…`; this is a final-preflight warning requiring recheck immediately before merge.

### Worktree integrity surface

Invocation:

```sh
git status --short --branch
git -C /Users/mineru/SourceCode/samsara/.issue/merge/base status --short --branch
git -C /Users/mineru/SourceCode/samsara worktree list --porcelain
```

Both feature and base status outputs contained only their branch/HEAD headers, with no changed paths. Worktree list showed the expected three worktrees at the assigned SHAs.

## Interpretation

Feature acceptance checks pass. No CI checks are configured, and the PR base OID discrepancy is retained as a final-preflight warning, not a feature failure. The direct assigned-base tree comparison shows `.issue/1/evidence/pr-body.md` exists on main at `762ed2f…` but not on the feature tree; it was added on main after the shared merge base, and it is absent from the live PR file list, so no deletion blocker is inferred from that comparison.
