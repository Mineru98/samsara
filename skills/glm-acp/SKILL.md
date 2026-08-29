---
name: glm-acp
description: Run the SAMSARA issue workflow from a glm-acp-agent session. Use when the user asks to use SAMSARA, an issue workflow, or its command triggers in an ACP client backed by GLM.
---

# GLM ACP integration

## Purpose

Provide a truthful bridge between the SAMSARA repository and `glm-acp-agent`.
The agent reads root `AGENTS.md` once when a new ACP session starts; this skill
maps that project context to the repository's existing skills and agent
playbooks.

## Compatibility contract

- `glm-acp-agent` communicates through ACP over stdio and uses the session
  working directory for file and command tools.
- It reads `AGENTS.md` first, then falls back to `CLAUDE.md`, when a session is
  created. Restart or start a new ACP session after changing `AGENTS.md`.
- ACP has no SAMSARA plugin manifest, slash-command registration, or subagent
  protocol. The command names in `commands/glm-acp.md` are natural-language
  triggers; agents are role documents read into the current session.
- `write_file` and `run_command` require ACP-client permission in the default
  session mode. Do not ask users to use `bypass_permissions` merely to run this
  workflow.

## Procedure

1. Confirm that the ACP session working directory is the target repository.
2. Read root `AGENTS.md` and then the matching command mapping in
   `commands/glm-acp.md`.
3. Read and follow the requested workflow skill under `skills/`.
4. When a workflow names a role, read the corresponding `agents/*.md` file and
   execute only its bounded review or planning responsibility in this session.
5. State the verified result, including any ACP permission prompt or external
   setup that remains for the user.

## Verification

Run `node tools/glm-acp/verify-context.mjs` from the repository root. The
smoke check invokes the installed `glm-acp-agent --help`, performs a real ACP
`initialize` / `session/new` handshake, and then uses the official ACP SDK with
a stub model to observe that the new session loaded `AGENTS.md`. It also checks
the `samsara onboard` trigger and the linked skill and agent files. No Z.AI
credential or network model request is needed.
