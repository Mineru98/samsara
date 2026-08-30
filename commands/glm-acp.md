# GLM ACP command triggers

`glm-acp-agent` has no extension API for registering slash commands. Use these
phrases in a normal ACP prompt; after recognizing one, read the referenced skill
before taking action.

| Prompt trigger | Read first | Script entry point | Result |
| --- | --- | --- | --- |
| `samsara onboard` | `skills/issue-onboard/SKILL.md` | `skills/issue-onboard/scripts/issue-onboard.mjs` | Inspect the issue graph and suggest the next action. |
| `samsara create issue: <request>` | `skills/issue-create/SKILL.md` | `skills/issue-create/scripts/issue-create.mjs` | Draft, de-duplicate, and register an issue before a repository change. |
| `samsara start #<number>` | `skills/issue-start/SKILL.md` | `skills/issue-start/scripts/issue-start.mjs` | Collect the issue, create its isolated worktree, implement, and record evidence. |
| `samsara finish #<number>` | `skills/issue-end/SKILL.md` | `skills/issue-end/scripts/issue-end.mjs` | Recheck evidence and create the issue PR after user approval. |
| `samsara merge` | `skills/issue-merge/SKILL.md` | `skills/issue-merge/scripts/issue-merge.mjs` | Evaluate and integrate approved issue worktrees. |
| `samsara sync` | `skills/issue-sync/SKILL.md` | `skills/issue-sync/scripts/issue-sync.mjs` | Refresh the local GitHub issue graph cache. |
| `samsara graph` | `skills/issue-viz/SKILL.md` | `skills/issue-viz/scripts/issue-viz.mjs` | Render the local issue graph. |

These triggers are documentation for the current ACP session. They do not
change the client command palette and must not be described as installed slash
commands.
