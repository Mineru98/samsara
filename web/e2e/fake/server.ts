// In-process fake runtime for browser scenarios (02 §25.2).
//
// Serves the production bundle and the /v1 wire contract from memory, seeded
// with the schema-validated payloads the real runtime wrote to
// runtime/api/fixtures. Cursor encoding, SSE framing, reset reasons and error
// bodies follow runtime/internal/{app,transport/httpapi,domain}. Every fault a
// scenario needs (dropped stream, replay, reset_required, split chunks, auth
// expiry, daemon restart, command outcomes) is a method call.
import crypto from "node:crypto";
import fs from "node:fs";
import http from "node:http";
import type { AddressInfo, Socket } from "node:net";
import path from "node:path";
import { fileURLToPath } from "node:url";

const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../../..");
export const STATIC_DIR = path.join(ROOT, "runtime/internal/webui/static");
const FIXTURE_DIR = path.join(ROOT, "runtime/api/fixtures");

/* eslint-disable @typescript-eslint/no-explicit-any */
type Json = any;
export type Entities = Record<string, Json[]>;

export interface ChangeSet {
  upserts?: Record<string, Json[]>;
  deletes?: Record<string, string[]>;
}

export interface RecordedRequest {
  method: string;
  path: string;
  authorization: string | undefined;
  idempotencyKey: string | undefined;
  ifMatch: string | undefined;
  body: Json;
  at: number;
}

export interface CommandReply {
  status: number;
  body: Json;
}

export type CommandHandler = (req: RecordedRequest, fake: FakeRuntime) => CommandReply | Promise<CommandReply>;

/** A loaded fixture payload, deep-cloned so tests can mutate it freely. */
export function fixturePayload(name: string, index: number): Json {
  const fx = JSON.parse(fs.readFileSync(path.join(FIXTURE_DIR, `${name}.json`), "utf8"));
  return structuredClone(fx.payloads[index].data);
}

export function fixtureSnapshot(name: string): Json {
  const fx = JSON.parse(fs.readFileSync(path.join(FIXTURE_DIR, `${name}.json`), "utf8"));
  const p = fx.payloads.find((x: Json) => x.schema === "snapshot.schema.json#/$defs/ProjectSnapshot");
  return structuredClone(p.data);
}

export function encodeCursor(scope: string, dbgen: string, seq: number): string {
  return Buffer.from(`${scope}|${dbgen}|${seq}`).toString("base64url");
}

function decodeCursor(c: string): { scope: string; dbgen: string; seq: number } | null {
  const parts = Buffer.from(c, "base64url").toString().split("|");
  if (parts.length !== 3 || !/^\d+$/.test(parts[2])) return null;
  return { scope: parts[0], dbgen: parts[1], seq: Number(parts[2]) };
}

const KEY_OF = (kind: string, e: Json): string => String(kind === "dependencies" ? e.dependency_id : e.id);
const TERMINAL_CANDIDATE = new Set(["MERGED", "CANCELLED", "FAILED"]);

interface Project {
  id: string;
  snap: Json;
  /** Business events with their global seq. */
  events: { seq: number; ev: Json }[];
  /** Oldest seq still retained; older cursors get CURSOR_EXPIRED. */
  retainedFrom: number;
  summaryOverride: Partial<Json>;
  github: Json;
  history: Json[];
}

interface Stream {
  res: http.ServerResponse;
  scope: string;
  seq: number;
}

export interface StreamOptions {
  /** Write each SSE frame in pieces of this many bytes (UI-T19). */
  splitBytes?: number;
}

export class FakeRuntime {
  url = "";
  epoch = "epoch_e2e_1";
  dbgen = "dbgen_e2e";
  version = "0.4.0-e2e";
  readonly requests: RecordedRequest[] = [];
  readonly projects = new Map<string, Project>();
  streamOptions: StreamOptions = {};
  commandHandler: CommandHandler | null = null;
  /** Per-project artificial snapshot latency (UI-T10 late responses). */
  readonly snapshotDelay = new Map<string, number>();

  private server: http.Server;
  private sockets = new Set<Socket>();
  private streams = new Set<Stream>();
  private seq = 0;
  private catalogVersion = 0;
  private catalogEvents: { seq: number; ev: Json }[] = [];
  private codes = new Set<string>();
  private tokens = new Set<string>();
  private down = false;

  constructor() {
    this.server = http.createServer((req, res) => void this.handle(req, res));
    this.server.on("connection", (s) => {
      s.setNoDelay(true);
      this.sockets.add(s);
      s.on("close", () => this.sockets.delete(s));
    });
  }

  async start(): Promise<this> {
    await new Promise<void>((r) => this.server.listen(0, "127.0.0.1", r));
    this.url = `http://127.0.0.1:${(this.server.address() as AddressInfo).port}`;
    return this;
  }

  async stop(): Promise<void> {
    for (const s of this.streams) s.res.destroy();
    for (const s of this.sockets) s.destroy();
    await new Promise<void>((r) => this.server.close(() => r()));
  }

  // ---------------------------------------------------------------- setup

  /** Register a project from a ProjectSnapshot payload. Its cursor is rebased onto this server's seq/dbgen. */
  addProject(snap: Json, opts: { github?: Json; history?: Json[] } = {}): string {
    const id = snap.project_id as string;
    snap.database_generation = this.dbgen;
    snap.runtime_epoch = this.epoch;
    snap.schema_version = "1";
    const p: Project = {
      id,
      snap,
      events: [],
      retainedFrom: this.seq + 1,
      summaryOverride: {},
      github: opts.github ?? { error: "", freshness: "UNAVAILABLE", items: [], last_attempt_at: null, last_synced_at: null, repository: "", retry_at: null, sync: "OFF", truncated: false },
      history: opts.history ?? [],
    };
    this.projects.set(id, p);
    snap.snapshot_cursor = encodeCursor(id, this.dbgen, this.seq);
    this.bumpCatalog(id);
    return id;
  }

  project(id: string): Project {
    const p = this.projects.get(id);
    if (!p) throw new Error(`no project ${id}`);
    return p;
  }

  entity(projectId: string, kind: string, id: string): Json {
    return this.project(projectId).snap.entities[kind].find((e: Json) => KEY_OF(kind, e) === id);
  }

  /** A fresh one-time bootstrap URL for a path (60s/1 use like `samsara ui`). */
  bootstrapUrl(pathname = "/"): string {
    const code = crypto.randomBytes(16).toString("hex");
    this.codes.add(code);
    return `${this.url}${pathname}#bootstrap=${code}`;
  }

  // ---------------------------------------------------------------- business changes

  /**
   * Commit one project change: bump project_version, record a contract
   * ProjectEvent, push it to open streams and refresh the catalog summary.
   */
  change(projectId: string, changes: ChangeSet, type = "project.changed", opts: { publish?: boolean } = {}): Json {
    const p = this.project(projectId);
    const prev = String(p.snap.project_version);
    const next = String(BigInt(prev) + 1n);
    const seq = ++this.seq;
    const upserts = changes.upserts ?? {};
    const deletes = changes.deletes ?? {};
    for (const [kind, list] of Object.entries(upserts)) {
      if (kind === "projects") continue;
      const table: Json[] = (p.snap.entities[kind] ??= []);
      for (const e of list) {
        const i = table.findIndex((x) => KEY_OF(kind, x) === KEY_OF(kind, e));
        if (i >= 0) table[i] = structuredClone(e);
        else table.push(structuredClone(e));
      }
    }
    for (const [kind, ids] of Object.entries(deletes)) {
      p.snap.entities[kind] = (p.snap.entities[kind] ?? []).filter((x: Json) => !ids.includes(KEY_OF(kind, x)));
    }
    p.snap.project_version = next;
    p.snap.project.project_version = next;
    p.snap.generated_at = new Date().toISOString();
    p.snap.snapshot_cursor = encodeCursor(projectId, this.dbgen, seq);
    const first = Object.entries(upserts).find(([, l]) => l.length > 0);
    const ev = {
      aggregate_id: first ? KEY_OF(first[0], first[1][0]) : projectId,
      aggregate_type: first ? first[0].replace(/s$/, "") : "project",
      aggregate_version: first ? String(first[1][0].version ?? "1") : "1",
      event_id: `evt_e2e_${seq}`,
      occurred_at: new Date().toISOString(),
      payload: { changes: { deletes, upserts } },
      previous_project_version: prev,
      project_id: projectId,
      project_version: next,
      recorded_at: new Date().toISOString(),
      schema_version: "1",
      seq: String(seq),
      type,
    };
    p.events.push({ seq, ev });
    if (opts.publish !== false) this.publish(projectId, seq, ev);
    this.bumpCatalog(projectId);
    return ev;
  }

  /** Upsert entities by id, merging fields into the current projection. */
  patch(projectId: string, kind: string, id: string, fields: Json, type?: string): Json {
    const cur = this.entity(projectId, kind, id);
    if (!cur) throw new Error(`no ${kind} ${id}`);
    const nextVersion = cur.version !== undefined ? String(BigInt(cur.version) + 1n) : undefined;
    const merged = { ...cur, ...fields, ...(nextVersion && fields.version === undefined ? { version: nextVersion } : {}) };
    return this.change(projectId, { upserts: { [kind]: [merged] } }, type);
  }

  /** Re-send already published events to every open stream of the project (UI-T08). */
  replay(projectId: string, fromIndex = 0): void {
    for (const { seq, ev } of this.project(projectId).events.slice(fromIndex)) this.publish(projectId, seq, ev);
  }

  /** Send a raw SSE frame to the project's open streams. */
  sendRaw(projectId: string, frame: string): void {
    for (const s of this.streams) if (s.scope === projectId) void this.write(s, frame);
  }

  /** Prune every event committed so far (retention floor, UI-T09). */
  expireRetention(projectId: string): void {
    const p = this.project(projectId);
    p.retainedFrom = this.seq + 1;
    p.events = p.events.filter((x) => x.seq >= p.retainedFrom);
  }

  /** Ask open streams of a scope to resync (CURSOR_EXPIRED / RESET_REQUIRED). */
  resetStreams(scope: string, reason: string): void {
    for (const s of [...this.streams]) {
      if (s.scope !== scope) continue;
      void this.write(s, `event: reset_required\ndata: ${JSON.stringify({ reason })}\n\n`).then(() => s.res.end());
      this.streams.delete(s);
    }
  }

  /** Drop every open stream without a reset (network blip). */
  dropStreams(scope?: string): void {
    for (const s of [...this.streams]) {
      if (scope && s.scope !== scope) continue;
      s.res.socket?.destroy();
      this.streams.delete(s);
    }
  }

  /** Revoke all bearers: next request is 401 SESSION_EXPIRED. */
  expireAuth(): void {
    this.tokens.clear();
    this.dropStreams();
  }

  /**
   * Daemon restart with the browser still open (UI-T23): connections drop,
   * requests fail for `downMs`, then a new runtime_epoch serves the same
   * database. Bearers stay valid because the server keeps token hashes.
   */
  async restart(downMs: number, during?: () => void): Promise<void> {
    this.down = true;
    for (const s of this.streams) s.res.socket?.destroy();
    this.streams.clear();
    for (const s of this.sockets) s.destroy();
    during?.();
    await new Promise((r) => setTimeout(r, downMs));
    this.epoch = `epoch_e2e_${Number(this.epoch.split("_").pop()) + 1}`;
    for (const p of this.projects.values()) p.snap.runtime_epoch = this.epoch;
    this.down = false;
  }

  openStreams(scope?: string): number {
    return [...this.streams].filter((s) => !scope || s.scope === scope).length;
  }

  commands(): RecordedRequest[] {
    return this.requests.filter((r) => r.method === "POST" && r.path !== "/v1/ui/sessions");
  }

  // ---------------------------------------------------------------- internals

  private summaryOf(p: Project): Json {
    const e = p.snap.entities;
    const issues: Json[] = e.issues ?? [];
    const runs: Json[] = [...(e.runs ?? [])].sort((a, b) => String(b.created_at).localeCompare(String(a.created_at)));
    return {
      ...p.snap.project,
      approval_pending: (e.approvals ?? []).filter((a: Json) => a.state === "PENDING").length,
      blocked: issues.filter((i) => i.display_status === "BLOCKED").length,
      integration_pending: (e.merge_candidates ?? []).filter((c: Json) => !TERMINAL_CANDIDATE.has(c.state)).length,
      running: issues.filter((i) => ["RUNNING", "STARTING", "VERIFYING"].includes(i.display_status)).length,
      ...(runs[0] ? { latest_run_id: runs[0].id, latest_run_state: runs[0].state } : {}),
      ...p.summaryOverride,
    };
  }

  private bumpCatalog(projectId: string): void {
    const p = this.project(projectId);
    const seq = ++this.seq;
    this.catalogVersion++;
    const ev = { catalog_version: String(this.catalogVersion), payload: { project: this.summaryOf(p) }, project_id: projectId, recorded_at: new Date().toISOString(), schema_version: "1", seq: String(seq) };
    this.catalogEvents.push({ seq, ev });
    this.publish("catalog", seq, ev);
  }

  private publish(scope: string, seq: number, ev: Json): void {
    const frame = this.frame(scope, seq, ev);
    for (const s of this.streams) {
      if (s.scope !== scope) continue;
      s.seq = Math.max(s.seq, seq);
      void this.write(s, frame);
    }
  }

  private frame(scope: string, seq: number, ev: Json): string {
    const type = scope === "catalog" ? "catalog.change" : "project.change";
    return `id: ${encodeCursor(scope, this.dbgen, seq)}\nevent: ${type}\ndata: ${JSON.stringify(ev)}\n\n`;
  }

  private chain = new WeakMap<http.ServerResponse, Promise<void>>();

  /** Writes are serialized per stream so split pieces never interleave. */
  private write(s: Stream, frame: string): Promise<void> {
    const prev = this.chain.get(s.res) ?? Promise.resolve();
    const next = prev.then(async () => {
      if (s.res.destroyed || s.res.writableEnded) return;
      const buf = Buffer.from(frame, "utf8");
      const n = this.streamOptions.splitBytes;
      if (!n) {
        s.res.write(buf);
        return;
      }
      for (let i = 0; i < buf.length; i += n) {
        if (s.res.destroyed) return;
        s.res.write(buf.subarray(i, i + n));
        await new Promise((r) => setTimeout(r, 2));
      }
    });
    this.chain.set(s.res, next);
    return next;
  }

  private json(res: http.ServerResponse, status: number, body: Json, headers: Record<string, string> = {}): void {
    res.writeHead(status, { "Content-Type": "application/json", "Cache-Control": "no-store", ...headers });
    res.end(JSON.stringify(body));
  }

  private err(res: http.ServerResponse, status: number, code: string, message: string, extra: Json = {}): void {
    this.json(res, status, { error: { code, message, request_id: "", retryable: false, ...extra } });
  }

  private async body(req: http.IncomingMessage): Promise<Json> {
    const chunks: Buffer[] = [];
    for await (const c of req) chunks.push(c as Buffer);
    const s = Buffer.concat(chunks).toString();
    try {
      return s ? JSON.parse(s) : null;
    } catch {
      return s;
    }
  }

  private async handle(req: http.IncomingMessage, res: http.ServerResponse): Promise<void> {
    if (this.down) {
      req.socket.destroy();
      return;
    }
    const url = new URL(req.url ?? "/", this.url);
    const p = url.pathname;
    if (!p.startsWith("/v1/")) return this.static(p, res);
    const body = req.method === "POST" ? await this.body(req) : null;
    const rec: RecordedRequest = {
      method: req.method ?? "GET",
      path: p + url.search,
      authorization: req.headers.authorization,
      idempotencyKey: req.headers["idempotency-key"] as string | undefined,
      ifMatch: req.headers["if-match"] as string | undefined,
      body,
      at: Date.now(),
    };
    this.requests.push(rec);

    if (p === "/v1/health") return this.json(res, 200, { status: "ok" });
    if (req.method === "POST" && p === "/v1/ui/sessions") {
      if (!body?.code || !this.codes.delete(body.code)) return this.err(res, 401, "SESSION_EXPIRED", "bootstrap code expired or used");
      const token = crypto.randomBytes(24).toString("hex");
      this.tokens.add(token);
      return this.json(res, 201, { token, expires_at: new Date(Date.now() + 3600_000).toISOString() });
    }
    const bearer = /^Bearer (.+)$/.exec(req.headers.authorization ?? "")?.[1];
    if (!bearer || !this.tokens.has(bearer)) return this.err(res, 401, "SESSION_EXPIRED", bearer ? "session expired" : "missing bearer token");

    if (req.method === "POST") {
      if (!rec.idempotencyKey) return this.err(res, 400, "INVALID_REQUEST", "Idempotency-Key header required for commands");
      const reply = this.commandHandler ? await this.commandHandler(rec, this) : { status: 202, body: { command_id: `cmd_${this.requests.length}`, state: "ACCEPTED" } };
      return this.json(res, reply.status, reply.body);
    }

    let m: RegExpExecArray | null;
    if (p === "/v1/runtime") {
      return this.json(res, 200, { backends: {}, claude: {}, database_generation: this.dbgen, host_id: "host_e2e", pid: 4242, ready: true, runtime_epoch: this.epoch, schema_version: "1", sqlite_version: "3.53.3", version: this.version });
    }
    if (p === "/v1/catalog/snapshot") {
      return this.json(res, 200, { catalog_version: String(this.catalogVersion), database_generation: this.dbgen, projects: [...this.projects.values()].map((x) => this.summaryOf(x)), schema_version: "1", snapshot_cursor: encodeCursor("catalog", this.dbgen, this.seq) });
    }
    if (p === "/v1/catalog/events") return this.openStream(req, res, url, "catalog");
    if ((m = /^\/v1\/projects\/([^/]+)\/(snapshot|events|history|github-history)$/.exec(p))) {
      const id = decodeURIComponent(m[1]);
      const proj = this.projects.get(id);
      if (!proj) return this.err(res, 404, "RESOURCE_NOT_FOUND", `project ${id} not found`);
      if (m[2] === "snapshot") {
        const delay = this.snapshotDelay.get(id);
        if (delay) await new Promise((r) => setTimeout(r, delay));
        // The cursor is the global seq at read time, as the runtime issues it.
        const snap = { ...proj.snap, snapshot_cursor: encodeCursor(id, this.dbgen, this.seq), summary: this.summaryOf(proj) };
        return this.json(res, 200, snap, { ETag: `"${proj.snap.project_version}"` });
      }
      if (m[2] === "events") return this.openStream(req, res, url, id);
      if (m[2] === "history") return this.json(res, 200, { items: proj.history });
      return this.json(res, 200, proj.github);
    }
    if ((m = /^\/v1\/issues\/([^/]+)$/.exec(p))) {
      const id = decodeURIComponent(m[1]);
      for (const proj of this.projects.values()) {
        const e = proj.snap.entities;
        const issue = (e.issues ?? []).find((i: Json) => i.id === id);
        if (!issue) continue;
        return this.json(res, 200, {
          issue,
          attempts: (e.attempts ?? []).filter((a: Json) => a.issue_id === id),
          dependencies: (e.dependencies ?? []).filter((d: Json) => d.successor_issue_id === id || d.predecessor_issue_id === id),
          merge_candidates: (e.merge_candidates ?? []).filter((c: Json) => c.issue_id === id),
          outputs: [],
        });
      }
      return this.err(res, 404, "RESOURCE_NOT_FOUND", `issue ${id} not found`);
    }
    return this.err(res, 404, "RESOURCE_NOT_FOUND", `no route ${p}`);
  }

  private openStream(req: http.IncomingMessage, res: http.ServerResponse, url: URL, scope: string): void {
    const raw = url.searchParams.get("after") ?? (req.headers["last-event-id"] as string | undefined) ?? "";
    if (!raw) return this.err(res, 400, "INVALID_REQUEST", "cursor required: start from the snapshot_cursor of a snapshot");
    res.writeHead(200, { "Content-Type": "text/event-stream; charset=utf-8", "Cache-Control": "no-store", "X-Accel-Buffering": "no" });
    res.flushHeaders();
    const cur = decodeCursor(raw);
    const reset = (reason: string): void => void res.end(`event: reset_required\ndata: ${JSON.stringify({ reason })}\n\n`);
    if (!cur) return reset("CURSOR_INVALID");
    if (cur.scope !== scope) return reset("CURSOR_SCOPE_MISMATCH");
    if (cur.dbgen !== this.dbgen) return reset("DATABASE_GENERATION_CHANGED");
    const log = scope === "catalog" ? this.catalogEvents : this.project(scope).events;
    if (scope !== "catalog") {
      // Same rule as app.ReadProjectEvents: the cursor is older than the
      // pruned floor and the first event after it is gone (or none is left).
      const pruned = this.project(scope).retainedFrom - 1;
      const next = log.find((x) => x.seq > cur.seq);
      if (cur.seq < pruned && (!next || next.seq <= pruned)) return reset("CURSOR_EXPIRED");
    }
    const s: Stream = { res, scope, seq: cur.seq };
    this.streams.add(s);
    res.on("close", () => this.streams.delete(s));
    for (const { seq, ev } of log) if (seq > cur.seq) void this.write(s, this.frame(scope, seq, ev));
    void this.write(s, ": connected\n\n");
  }

  private static(p: string, res: http.ServerResponse): void {
    const file = path.join(STATIC_DIR, path.normalize(p).replace(/^(\.\.[/\\])+/, ""));
    const target = fs.existsSync(file) && fs.statSync(file).isFile() ? file : path.join(STATIC_DIR, "index.html");
    const type = { ".js": "text/javascript", ".css": "text/css", ".html": "text/html; charset=utf-8", ".svg": "image/svg+xml" }[path.extname(target)] ?? "application/octet-stream";
    res.writeHead(200, { "Content-Type": type, "Cache-Control": "no-store" });
    fs.createReadStream(target).pipe(res);
  }
}
