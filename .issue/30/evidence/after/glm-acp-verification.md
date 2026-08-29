# After: GLM ACP integration verification

## Added integration surfaces

- `AGENTS.md` is the root project-context entry point that `glm-acp-agent`
  reads for a new session.
- `commands/glm-acp.md` maps normal ACP prompt triggers to existing SAMSARA
  workflow skills without claiming slash-command registration.
- `skills/glm-acp/SKILL.md` records the ACP compatibility contract and session
  verification procedure.
- `agents/glm-acp-samsara.md` provides the current-session workflow role and
  explicitly avoids a false subagent-spawn claim.
- `tools/glm-acp/verify-context.mjs` provides a reproducible no-credential
  smoke check for the installed CLI and the ACP project-context path.

## Checks passed

- All command-map skill and script entry-point paths exist and are linked from
  `AGENTS.md` through the command map.
- All seven documented prompt triggers are present in both the command map and
  `AGENTS.md`.
- Existing plugin JSON files parse successfully and `git diff --check` passes.
- `bash scripts/check-shared.sh` passes.
- `node --test tools/issue-ontology/ontology.test.mjs` passes all 13 tests
  after installing the tool's missing local `ajv` dependency without a lockfile.
- `node tools/glm-acp/verify-context.mjs` passed. It invoked the installed
  `glm-acp-agent --help`, opened a real ACP `initialize` / `session/new` session
  with this repository as `cwd`, and completed a `samsara onboard` prompt over
  the official ACP SDK with a stubbed model. The observed system context
  contained:
  - `SAMSARA for GLM ACP`
  - `samsara create issue: <request>`
  - `agents/glm-acp-samsara.md`

No Z.AI credential or network model request was used for this verification.
