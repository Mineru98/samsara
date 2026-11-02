// Real runtime harness: builds `samsara`, runs its daemon in an isolated
// SAMSARA_HOME and drives projects through the CLI exactly like a user.
// Managed workers run in tmux as a harmless `sleep` (no Claude, no edits).
import { execFileSync, spawn, type ChildProcess } from "node:child_process";
import fs from "node:fs";
import net from "node:net";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

/* eslint-disable @typescript-eslint/no-explicit-any */
type Json = any;

const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../../..");
/** argv of the managed worker; the odd number makes its processes findable. */
export const WORKER_SECONDS = "3617";

export function buildRuntime(outDir: string): string {
  const bin = path.join(outDir, "samsara");
  try {
    execFileSync("go", ["build", "-o", bin, "./cmd/samsara"], { cwd: path.join(ROOT, "runtime"), stdio: "pipe" });
  } catch (e) {
    throw new Error(`go build failed (Go must be on PATH): ${(e as { stderr?: Buffer }).stderr?.toString() ?? e}`);
  }
  return bin;
}

async function freePort(): Promise<number> {
  return new Promise((resolve) => {
    const s = net.createServer().listen(0, "127.0.0.1", () => {
      const p = (s.address() as net.AddressInfo).port;
      s.close(() => resolve(p));
    });
  });
}

export interface PlanIssue {
  key: string;
  title?: string;
}
export interface PlanDep {
  from: string;
  to: string;
  milestone: "IMPLEMENTED" | "VERIFIED" | "RESULT_APPROVED" | "INTEGRATED";
}

export class RealRuntime {
  readonly bin: string;
  readonly home: string;
  url = "";
  private daemon: ChildProcess | null = null;
  private token = "";

  constructor(bin: string, root: string) {
    this.bin = bin;
    this.home = path.join(root, "home");
    fs.mkdirSync(this.home, { recursive: true });
  }

  private env(): NodeJS.ProcessEnv {
    return { ...process.env, SAMSARA_HOME: this.home };
  }

  cli(...args: string[]): Json {
    const out = execFileSync(this.bin, ["--json", ...args], { env: this.env(), stdio: ["ignore", "pipe", "pipe"], timeout: 60_000 }).toString();
    return out.trim() ? JSON.parse(out) : null;
  }

  async start(): Promise<this> {
    this.ensureCleanTmuxServer();
    const port = await freePort();
    this.url = `http://127.0.0.1:${port}`;
    const log = fs.openSync(path.join(this.home, "daemon.log"), "a");
    this.daemon = spawn(this.bin, ["daemon", "run", "--ui-port", String(port)], { env: this.env(), stdio: ["ignore", log, log] });
    for (let i = 0; i < 100; i++) {
      try {
        if ((await fetch(`${this.url}/v1/health`)).ok) return this;
      } catch {
        /* not up yet */
      }
      await new Promise((r) => setTimeout(r, 100));
    }
    throw new Error("daemon did not become healthy");
  }

  daemonPid(): number | undefined {
    return this.daemon?.pid;
  }

  daemonAlive(): boolean {
    return !!this.daemon && this.daemon.exitCode === null && this.cli("daemon", "status")?.status !== "NOT_RUNNING";
  }

  /** git repo + `samsara init` with a sleep worker on tmux. Returns project id. */
  createProject(name: string): { id: string; root: string } {
    const root = path.join(path.dirname(this.home), "repos", name);
    fs.mkdirSync(root, { recursive: true });
    const git = (...a: string[]) => execFileSync("git", ["-C", root, ...a], { stdio: "pipe" });
    git("init", "-q", "-b", "main");
    fs.writeFileSync(path.join(root, "README.md"), `# ${name}\n`);
    git("add", "-A");
    git("-c", "user.email=e2e@samsara", "-c", "user.name=e2e", "commit", "-qm", "init");
    const cfgDir = path.join(root, ".samsara");
    fs.mkdirSync(cfgDir, { recursive: true });
    fs.writeFileSync(
      path.join(cfgDir, "config.yaml"),
      [
        "version: 1",
        "backend:",
        "    mode: tmux",
        "    allow_fallback: false",
        "execution:",
        "    max_workers: 5",
        "    worker_command:",
        // The tmux backend appends the prompt to argv; sh ignores it.
        "        - sh",
        "        - -c",
        `        - "exec sleep ${WORKER_SECONDS}"`,
        "        - samsara-e2e-worker",
        "approval:",
        "    implementation_start: auto",
        "    result: manual",
        "    integration: manual",
        "github:",
        "    sync: off",
        "",
      ].join("\n"),
    );
    const res = this.cli("init", "--path", root);
    return { id: res.project.project_id, root };
  }

  applyPlan(projectId: string, issues: PlanIssue[], deps: PlanDep[]): Json {
    const file = path.join(this.home, `plan-${projectId}.json`);
    fs.writeFileSync(
      file,
      JSON.stringify({
        spec_version: "1",
        project_id: projectId,
        expected_graph_version: "0",
        issues: issues.map((i) => ({ client_key: i.key, title: i.title ?? i.key.toUpperCase(), acceptance_criteria: [`${i.key} works`] })),
        dependencies: deps.map((d) => ({ predecessor_key: d.from, successor_key: d.to, required_milestone: d.milestone, downstream_phase: "EXECUTION" })),
      }),
    );
    return this.cli("plan", "apply", "--file", file);
  }

  /** One-time dashboard URL for a path, as `samsara ui` prints it. */
  bootstrapUrl(pathname = "/"): string {
    const u = new URL(this.cli("ui").url);
    return `${this.url}${pathname}${u.hash}`;
  }

  /** GET a /v1 resource with this harness's own bearer. */
  async get(p: string): Promise<Json> {
    if (!this.token) {
      const code = new URL(this.cli("ui").url).hash.replace(/^#bootstrap=/, "");
      const r = await fetch(`${this.url}/v1/ui/sessions`, { method: "POST", headers: { "Content-Type": "application/json", Origin: this.url }, body: JSON.stringify({ code }) });
      this.token = ((await r.json()) as Json).token;
    }
    const r = await fetch(`${this.url}${p}`, { headers: { Authorization: `Bearer ${this.token}`, Accept: "application/json" } });
    if (!r.ok) throw new Error(`${p}: HTTP ${r.status} ${await r.text()}`);
    return r.json();
  }

  snapshot(projectId: string): Promise<Json> {
    return this.get(`/v1/projects/${encodeURIComponent(projectId)}/snapshot`);
  }

  /** tmux socket of the runtime's tmux backend (per user, shared). */
  static readonly tmuxSocket = `samsara-${os.userInfo().uid}`;
  private ownTmuxServer = false;

  /**
   * Live worker processes of the given attempts: the pane process of each
   * attempt's tmux session (the launcher) and its `sleep` child.
   */
  workerPids(attemptIds: string[]): number[] {
    const pids: number[] = [];
    for (const a of attemptIds) {
      let pane: number;
      try {
        pane = Number(execFileSync("tmux", ["-L", RealRuntime.tmuxSocket, "list-panes", "-t", `=samsara-${a}`, "-F", "#{pane_pid}"], { stdio: ["ignore", "pipe", "ignore"] }).toString().trim().split("\n")[0]);
      } catch {
        continue;
      }
      try {
        const kids = execFileSync("pgrep", ["-P", String(pane), "-f", `^sleep ${WORKER_SECONDS}$`]).toString().trim().split(/\s+/).filter(Boolean).map(Number);
        pids.push(...kids);
      } catch {
        /* launcher alive but worker gone */
      }
    }
    return pids;
  }

  /**
   * The tmux server keeps the environment of whoever starts it. If the e2e
   * daemon started it, the user's own workers launched later would inherit
   * this run's SAMSARA_HOME. Start it from a clean environment instead.
   */
  ensureCleanTmuxServer(): void {
    const env = { ...process.env };
    delete env.SAMSARA_HOME;
    const tmux = (...a: string[]) => execFileSync("tmux", ["-L", RealRuntime.tmuxSocket, ...a], { stdio: "ignore", env });
    try {
      tmux("has-session");
      return; // already running with its own environment
    } catch {
      tmux("new-session", "-d", "-s", "samsara-e2e-keepalive", "sleep", "86400");
      this.ownTmuxServer = true;
    }
  }

  async stop(attemptIds: string[]): Promise<void> {
    try {
      this.cli("daemon", "stop");
    } catch {
      this.daemon?.kill("SIGTERM");
    }
    // Workers outlive the daemon by design; remove only the tmux sessions this run created.
    const kill = (name: string) => {
      try {
        execFileSync("tmux", ["-L", RealRuntime.tmuxSocket, "kill-session", "-t", `=${name}`], { stdio: "ignore" });
      } catch {
        /* already gone */
      }
    };
    for (const a of attemptIds) kill(`samsara-${a}`);
    if (this.ownTmuxServer) kill("samsara-e2e-keepalive");
    this.daemon?.kill("SIGKILL");
  }
}

export async function waitFor<T>(fn: () => Promise<T> | T, ok: (v: T) => boolean, what: string, timeoutMs = 30_000): Promise<T> {
  const end = Date.now() + timeoutMs;
  let last: T | undefined;
  while (Date.now() < end) {
    last = await fn();
    if (ok(last)) return last;
    await new Promise((r) => setTimeout(r, 250));
  }
  throw new Error(`timed out waiting for ${what}: ${JSON.stringify(last)?.slice(0, 500)}`);
}
