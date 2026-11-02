import { ApiError, auth, authHeaders } from "../api/http";
import { Observable } from "../store/projectStore";
import { SSEParser, type SSEMessage } from "./sse";

export type ConnectionStatus =
  | "connecting" // first snapshot loading
  | "live" // snapshot applied and stream open
  | "resyncing" // gap/reset: reloading the snapshot, display is not current
  | "reconnecting" // stream dropped, retrying from the last applied cursor
  | "auth_expired" // bearer gone: user must run `samsara ui --open` again
  | "incompatible" // breaking schema: commands disabled, upgrade needed
  | "error"; // snapshot itself failed (e.g. project not found)

export interface StreamView<S> {
  status: ConnectionStatus;
  data: S | null;
  /** When the last business event or snapshot was applied. */
  lastAppliedAt: number | null;
  error: ApiError | null;
  /** Reason of the last resync, for the connection banner. */
  resyncReason: string | null;
}

export type StepResult<S> =
  | { kind: "applied"; state: S }
  | { kind: "duplicate" }
  | { kind: "gap"; reason: string }
  | { kind: "incompatible"; reason: string };

export interface StreamSpec<S> {
  /** Loads a consistent snapshot and the cursor to resume from. */
  load(signal: AbortSignal): Promise<{ state: S; cursor: string }>;
  eventsUrl(cursor: string): string;
  /** Event type carrying business changes, e.g. "project.change". */
  changeEvent: string;
  apply(state: S, data: unknown, cursor: string): StepResult<S>;
}

const MAX_BACKOFF_MS = 15_000;

/**
 * Snapshot + fetch-streaming SSE controller (02 §15, 03 §9). One instance per
 * subscribed scope. stop() bumps the generation so late chunks from a previous
 * project never touch the new view (UI-T10).
 */
export class StreamController<S> {
  readonly view: Observable<StreamView<S>>;
  private generation = 0;
  private abort: AbortController | null = null;
  private cursor = "";
  private state: S | null = null;
  private failures = 0;
  private resyncs = 0;
  private publishScheduled = false;

  constructor(private readonly spec: StreamSpec<S>) {
    this.view = new Observable<StreamView<S>>({ status: "connecting", data: null, lastAppliedAt: null, error: null, resyncReason: null });
  }

  start(): void {
    const gen = ++this.generation;
    void this.run(gen, "connecting", null);
  }

  stop(): void {
    this.generation++;
    this.abort?.abort();
    this.abort = null;
  }

  /** Force a full snapshot reload (used after commands or user request). */
  resync(reason: string): void {
    const gen = ++this.generation;
    this.abort?.abort();
    void this.run(gen, "resyncing", reason);
  }

  private patch(p: Partial<StreamView<S>>): void {
    this.view.set({ ...this.view.get(), ...p });
  }

  private alive(gen: number): boolean {
    return gen === this.generation;
  }

  private async run(gen: number, initial: ConnectionStatus, reason: string | null): Promise<void> {
    this.patch({ status: initial, resyncReason: reason });
    // eslint-disable-next-line no-constant-condition
    while (this.alive(gen)) {
      const ac = new AbortController();
      this.abort = ac;
      try {
        if (this.state === null || this.view.get().status === "resyncing" || this.view.get().status === "connecting") {
          const snap = await this.spec.load(ac.signal);
          if (!this.alive(gen)) return;
          this.state = snap.state;
          this.cursor = snap.cursor;
          this.patch({ data: snap.state, lastAppliedAt: Date.now(), error: null });
        }
        const outcome = await this.stream(gen, ac);
        if (!this.alive(gen)) return;
        if (outcome === "resync") {
          this.patch({ status: "resyncing" });
          // Back off when resyncs repeat, e.g. a reset sent right after every
          // fresh snapshot, so the client never hammers the snapshot endpoint.
          this.resyncs++;
          if (this.resyncs > 1) await sleep(Math.min(MAX_BACKOFF_MS, 500 * 2 ** Math.min(this.resyncs - 1, 5)));
          continue;
        }
        if (outcome === "incompatible") return;
        // Stream ended normally (server closed): reconnect from the cursor.
        this.patch({ status: "reconnecting" });
      } catch (e) {
        if (!this.alive(gen)) return;
        const err = e instanceof ApiError ? e : new ApiError(0, { message: (e as Error).message, retryable: true });
        if (err.code === "SESSION_EXPIRED" || auth.isExpired()) {
          this.patch({ status: "auth_expired", error: err });
          return;
        }
        if (err.code === "PROTOCOL_MISMATCH") {
          this.patch({ status: "incompatible", error: err, resyncReason: err.message });
          return;
        }
        if (this.state !== null && err.status >= 400 && err.status < 500 && !err.retryable) {
          // The stream refused permanently (e.g. project gone): reload the
          // snapshot so the real error surfaces instead of endless reconnects.
          this.state = null;
        }
        if (this.state === null && !err.retryable && err.status !== 0) {
          this.patch({ status: "error", error: err });
          return;
        }
        this.patch({ status: this.state === null ? "connecting" : "reconnecting", error: err });
      }
      this.failures++;
      const base = Math.min(MAX_BACKOFF_MS, 500 * 2 ** Math.min(this.failures, 5));
      await sleep(base / 2 + Math.random() * (base / 2));
    }
  }

  /** Reads the stream until it ends. Returns why it stopped. */
  private async stream(gen: number, ac: AbortController): Promise<"ended" | "resync" | "incompatible"> {
    const res = await fetch(this.spec.eventsUrl(this.cursor), {
      headers: { Accept: "text/event-stream", ...authHeaders() },
      signal: ac.signal,
      cache: "no-store",
    });
    if (!res.ok) {
      let body = {};
      try {
        body = ((await res.json()) as { error?: object }).error ?? {};
      } catch {
        /* ignore */
      }
      const err = new ApiError(res.status, body);
      if (err.code === "SESSION_EXPIRED") auth.markExpired();
      throw err;
    }
    if (!this.alive(gen)) return "ended";
    this.patch({ status: "live", error: null, resyncReason: null });
    this.failures = 0;
    let verdict: "ended" | "resync" | "incompatible" = "ended";
    const parser = new SSEParser((m) => {
      if (verdict !== "ended" || !this.alive(gen)) return;
      const v = this.onMessage(m);
      if (v) {
        verdict = v;
        ac.abort();
      }
    });
    const reader = res.body!.getReader();
    try {
      for (;;) {
        const { done, value } = await reader.read();
        if (done) break;
        parser.push(value);
        if (verdict !== "ended" || !this.alive(gen)) break;
      }
      parser.end();
    } catch (e) {
      if (verdict === "ended" && this.alive(gen) && (e as Error).name !== "AbortError") throw e;
    } finally {
      reader.cancel().catch(() => undefined);
    }
    return verdict;
  }

  private onMessage(m: SSEMessage): "resync" | "incompatible" | null {
    if (m.event === "reset_required") {
      let reason = "RESET_REQUIRED";
      try {
        reason = (JSON.parse(m.data) as { reason?: string }).reason ?? reason;
      } catch {
        /* keep default */
      }
      this.patch({ resyncReason: reason });
      return "resync";
    }
    if (m.event !== this.spec.changeEvent || this.state === null) return null;
    let data: unknown;
    try {
      data = JSON.parse(m.data);
    } catch {
      this.patch({ resyncReason: "MALFORMED_JSON" });
      return "resync";
    }
    const cursor = m.id ?? this.cursor;
    const r = this.spec.apply(this.state, data, cursor);
    switch (r.kind) {
      case "applied":
        this.state = r.state;
        this.cursor = cursor;
        this.resyncs = 0;
        this.schedulePublish();
        return null;
      case "duplicate":
        this.cursor = cursor;
        return null;
      case "gap":
        this.patch({ resyncReason: r.reason });
        return "resync";
      case "incompatible":
        this.patch({ status: "incompatible", resyncReason: r.reason });
        return "incompatible";
    }
  }

  /** Coalesce events parsed from one chunk into a single render. */
  private schedulePublish(): void {
    if (this.publishScheduled) return;
    this.publishScheduled = true;
    queueMicrotask(() => {
      this.publishScheduled = false;
      this.patch({ data: this.state, lastAppliedAt: Date.now() });
    });
  }
}

function sleep(ms: number): Promise<void> {
  return new Promise((r) => setTimeout(r, ms));
}
