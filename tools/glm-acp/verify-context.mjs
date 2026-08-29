#!/usr/bin/env node

import assert from "node:assert/strict";
import { spawn, spawnSync } from "node:child_process";
import { once } from "node:events";
import { createRequire } from "node:module";
import { existsSync, mkdtempSync, readFileSync, realpathSync, rmSync } from "node:fs";
import { dirname, isAbsolute, join } from "node:path";
import { tmpdir } from "node:os";
import { pathToFileURL } from "node:url";

const repositoryRoot = process.cwd();
const command = process.env.GLM_ACP_AGENT ?? "glm-acp-agent";
const executable = resolveExecutable(command);
verifyDocumentedReferences(repositoryRoot);
const help = spawnSync(executable, ["--help"], { encoding: "utf8" });

assert.equal(help.status, 0, help.stderr);
assert.match(help.stdout, /Start the ACP stdio loop/);

const cliRoot = dirname(dirname(realpathSync(executable)));
const requireFromCli = createRequire(join(cliRoot, "package.json"));
const sdk = await import(pathToFileURL(requireFromCli.resolve("@agentclientprotocol/sdk")).href);
const { AgentSideConnection, ClientSideConnection, PROTOCOL_VERSION, ndJsonStream } = sdk;
const { GlmAcpAgent } = await import(pathToFileURL(join(cliRoot, "dist/protocol/agent.js")).href);

const cliSession = await verifyCliSession({
  executable,
  repositoryRoot,
  protocolVersion: PROTOCOL_VERSION,
});
await verifyProjectContext({
  repositoryRoot,
  AgentSideConnection,
  ClientSideConnection,
  PROTOCOL_VERSION,
  ndJsonStream,
  GlmAcpAgent,
});

console.log(JSON.stringify({
  cliHelp: "passed",
  cliSessionNew: "passed",
  sessionId: cliSession,
  projectContext: "passed",
}));

function resolveExecutable(value) {
  if (isAbsolute(value)) return value;
  const resolver = process.platform === "win32" ? "where" : "which";
  const result = spawnSync(resolver, [value], { encoding: "utf8" });
  assert.equal(result.status, 0, `could not resolve ${value}: ${result.stderr}`);
  return result.stdout.trim().split(/\r?\n/)[0];
}

function verifyDocumentedReferences(repositoryRoot) {
  const commandMap = readFileSync(join(repositoryRoot, "commands/glm-acp.md"), "utf8");
  const projectContext = readFileSync(join(repositoryRoot, "AGENTS.md"), "utf8");
  const triggers = [
    "samsara onboard",
    "samsara create issue: <request>",
    "samsara start #<number>",
    "samsara finish #<number>",
    "samsara merge",
    "samsara sync",
    "samsara graph",
  ];
  for (const trigger of triggers) {
    assert.ok(commandMap.includes(`\`${trigger}\``), `command map missing ${trigger}`);
    assert.ok(projectContext.includes(`\`${trigger}\``), `AGENTS.md missing ${trigger}`);
  }
  for (const relativePath of [
    "skills/issue-create/SKILL.md",
    "skills/issue-start/SKILL.md",
    "skills/issue-end/SKILL.md",
    "skills/issue-merge/SKILL.md",
    "skills/issue-onboard/SKILL.md",
    "skills/issue-sync/SKILL.md",
    "skills/issue-viz/SKILL.md",
    "skills/glm-acp/SKILL.md",
    "agents/glm-acp-samsara.md",
    "skills/issue-create/scripts/issue-create.mjs",
    "skills/issue-start/scripts/issue-start.mjs",
    "skills/issue-end/scripts/issue-end.mjs",
    "skills/issue-merge/scripts/issue-merge.mjs",
    "skills/issue-onboard/scripts/issue-onboard.mjs",
    "skills/issue-sync/scripts/issue-sync.mjs",
    "skills/issue-viz/scripts/issue-viz.mjs",
  ]) {
    assert.ok(existsSync(join(repositoryRoot, relativePath)), `missing ${relativePath}`);
  }
}

async function verifyCliSession({ executable, repositoryRoot, protocolVersion }) {
  const sessionDirectory = mkdtempSync(join(tmpdir(), "samsara-glm-acp-"));
  const child = spawn(executable, [], {
    cwd: repositoryRoot,
    env: { ...process.env, ACP_GLM_SESSION_DIR: sessionDirectory },
    stdio: ["pipe", "pipe", "pipe"],
  });
  let buffer = "";
  let stderr = "";
  const pending = new Map();
  child.stdout.setEncoding("utf8");
  child.stderr.setEncoding("utf8");
  child.stdout.on("data", (chunk) => {
    buffer += chunk;
    while (true) {
      const newline = buffer.indexOf("\n");
      if (newline < 0) break;
      const line = buffer.slice(0, newline).trim();
      buffer = buffer.slice(newline + 1);
      if (!line) continue;
      let message;
      try {
        message = JSON.parse(line);
      } catch {
        for (const reject of pending.values()) reject(new Error(`invalid ACP output: ${line}`));
        pending.clear();
        continue;
      }
      if (message.id !== undefined && pending.has(message.id)) {
        const { resolve, reject } = pending.get(message.id);
        pending.delete(message.id);
        if (message.error) {
          reject(new Error(`ACP ${message.error.code}: ${message.error.message}`));
        } else {
          resolve(message);
        }
      }
    }
  });
  child.stderr.on("data", (chunk) => { stderr += chunk; });
  child.on("error", (error) => {
    for (const { reject } of pending.values()) reject(error);
    pending.clear();
  });

  const call = (id, method, params) => new Promise((resolve, reject) => {
    pending.set(id, { resolve, reject });
    child.stdin.write(JSON.stringify({ jsonrpc: "2.0", id, method, params }) + "\n", (error) => {
      if (error) {
        pending.delete(id);
        reject(error);
      }
    });
  });

  try {
    const initialize = await call(1, "initialize", {
      protocolVersion,
      clientCapabilities: {},
    });
    assert.equal(initialize.result?.agentInfo?.name, "glm-acp-agent");
    child.stdin.write(JSON.stringify({
      jsonrpc: "2.0",
      method: "notifications/initialized",
      params: {},
    }) + "\n");
    const session = await call(2, "session/new", {
      cwd: repositoryRoot,
      mcpServers: [],
    });
    const sessionId = session.result?.sessionId;
    assert.match(sessionId ?? "", /^[0-9a-f-]{36}$/);
    child.stdin.end();
    await once(child, "close");
    return sessionId;
  } catch (error) {
    child.kill();
    throw new Error(`${error instanceof Error ? error.message : error}; ${stderr.trim()}`);
  } finally {
    rmSync(sessionDirectory, { recursive: true, force: true });
  }
}

async function verifyProjectContext({
  repositoryRoot,
  AgentSideConnection,
  ClientSideConnection,
  PROTOCOL_VERSION,
  ndJsonStream,
  GlmAcpAgent,
}) {
  const agentToClient = new TransformStream();
  const clientToAgent = new TransformStream();
  const agentStream = ndJsonStream(agentToClient.writable, clientToAgent.readable);
  const clientStream = ndJsonStream(clientToAgent.writable, agentToClient.readable);
  let systemPrompt = "";
  const glm = {
    async *streamChat(messages) {
      const system = messages.find((message) => message.role === "system");
      systemPrompt = typeof system?.content === "string" ? system.content : "";
      yield { text: "verified" };
      yield { done: true, stopReason: "stop" };
    },
  };
  const agentConnection = new AgentSideConnection(
    (connection) => new GlmAcpAgent(connection, { glm, sessionStore: null }),
    agentStream,
  );
  const updates = [];
  const client = new ClientSideConnection((() => ({
    async sessionUpdate(params) { updates.push(params); },
  })), clientStream);
  const initialized = await client.initialize({
    protocolVersion: PROTOCOL_VERSION,
    clientCapabilities: {},
  });
  assert.equal(initialized.agentInfo?.name, "glm-acp-agent");
  const session = await client.newSession({ cwd: repositoryRoot, mcpServers: [] });
  const prompt = await client.prompt({
    sessionId: session.sessionId,
    prompt: [{ type: "text", text: "samsara onboard" }],
  });
  assert.equal(prompt.stopReason, "end_turn");
  assert.match(systemPrompt, /# SAMSARA for GLM ACP/);
  assert.match(systemPrompt, /samsara onboard/);
  assert.match(systemPrompt, /skills\/glm-acp\/SKILL\.md/);
  assert.match(systemPrompt, /agents\/glm-acp-samsara\.md/);
  assert.ok(updates.some((item) => item.update?.sessionUpdate === "agent_message_chunk"));
  void agentConnection;
}
