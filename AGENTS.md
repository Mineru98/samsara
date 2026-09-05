# SAMSARA for GLM ACP

`glm-acp-agent` reads this file when it creates a session whose working directory
is this repository. It is an ACP project-context integration, not a plugin
manifest: ACP does not automatically register slash commands, skills, or
subagents from directories.

## First read

For a request that changes an existing repository, begin with
[`skills/issue-create/SKILL.md`](skills/issue-create/SKILL.md). Do not edit code
before an issue exists unless that skill's maturity gate says to continue
directly. For an existing issue, use
[`skills/issue-start/SKILL.md`](skills/issue-start/SKILL.md).

Read [`skills/glm-acp/SKILL.md`](skills/glm-acp/SKILL.md) before using the GLM
ACP integration. It defines the compatible command triggers and ACP-specific
constraints.

## Commands

The user can write any of the following in a normal ACP prompt. They are text
triggers, not client-registered slash commands. Read
[`commands/glm-acp.md`](commands/glm-acp.md) for the exact mapping before acting.

- `samsara onboard`
- `samsara create issue: <request>`
- `samsara start #<number>`
- `samsara finish #<number>`
- `samsara merge`
- `samsara sync`
- `samsara graph`

## Skills

Each workflow is self-contained under `skills/<name>/SKILL.md`. Read the named
skill and its referenced files before running its scripts. The core order is:

`issue-create` → `issue-start` → `issue-end` → `issue-merge`

Use `issue-onboard`, `issue-sync`, and `issue-viz` to inspect or refresh the
issue graph; use `gh-setup` when GitHub CLI authentication is unavailable.

`issue-version` is separate from that order. It takes `major`, `minor`, or
`patch`, bumps every file that carries the version, opens a bump PR, and stops
there. Call it again after that PR merges to create the tag and the GitHub
release. Do not merge the bump PR yourself.

## Agents

[`agents/glm-acp-samsara.md`](agents/glm-acp-samsara.md) is the ACP-compatible
workflow role. Read it into the current GLM session when a user asks to run the
SAMSARA workflow. GLM ACP does not expose a subagent-spawn protocol, so do not
claim that this role runs concurrently.

The existing role documents in `agents/*.md` are reusable review playbooks. Read
the relevant file and perform its bounded role in the current session when the
matching workflow calls for it.

## ACP safety

`glm-acp-agent` asks the client for permission before writes and shell commands
in its default mode. Keep that mode unless the user explicitly selects another
mode. Never put `Z_AI_API_KEY` in this repository, an issue, or a command
example.
