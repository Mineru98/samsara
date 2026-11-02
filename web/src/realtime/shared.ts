import { ApiError } from "../api/http";
import { Observable } from "../store/projectStore";
import { StreamController, type StreamSpec, type StreamView } from "./stream";

/** What screens use from a stream: the view and a manual resync. */
export interface StreamHandle<S> {
  readonly view: Observable<StreamView<S>>;
  resync(reason: string): void;
}

/** Cross-tab primitives; injectable for tests. */
export interface TabCoordination {
  locks: Pick<LockManager, "request">;
  channel(name: string): BroadcastChannel;
}

export function browserCoordination(): TabCoordination | null {
  if (typeof navigator === "undefined" || !navigator.locks || typeof BroadcastChannel === "undefined") return null;
  return { locks: navigator.locks, channel: (name) => new BroadcastChannel(name) };
}

// ApiError loses its fields in structured clone, so it travels as plain data.
interface WireError {
  status: number;
  code: string;
  message: string;
  retryable: boolean;
  details?: Record<string, unknown>;
}
type WireView<S> = Omit<StreamView<S>, "error"> & { error: WireError | null };
type Message<S> = { t: "view"; view: WireView<S> } | { t: "hello" } | { t: "resync"; reason: string };

function toWire<S>(v: StreamView<S>): WireView<S> {
  const e = v.error;
  return { ...v, error: e ? { status: e.status, code: e.code, message: e.message, retryable: e.retryable, details: e.details } : null };
}

function fromWire<S>(v: WireView<S>): StreamView<S> {
  const e = v.error;
  return { ...v, error: e ? new ApiError(e.status, e) : null };
}

/**
 * One SSE connection per scope across the tabs of this origin (02 §15.5).
 * The tab holding the Web Lock for `name` runs the StreamController and
 * broadcasts its view; the other tabs mirror it and forward resync requests.
 * Closing or leaving the leader tab releases the lock and the next tab takes
 * over with its own snapshot. Without Web Locks or BroadcastChannel every tab
 * streams on its own, as before.
 */
export class SharedStream<S> implements StreamHandle<S> {
  readonly view = new Observable<StreamView<S>>({ status: "connecting", data: null, lastAppliedAt: null, error: null, resyncReason: null });
  private controller: StreamController<S> | null = null;
  private channel: BroadcastChannel | null = null;
  private abort: AbortController | null = null;
  private release: (() => void) | null = null;
  private unsubscribe: (() => void) | null = null;

  constructor(
    private readonly name: string,
    private readonly spec: StreamSpec<S>,
    private readonly coord: TabCoordination | null = browserCoordination(),
  ) {}

  start(): void {
    if (!this.coord) {
      this.lead();
      return;
    }
    const ch = this.coord.channel(this.name);
    this.channel = ch;
    ch.onmessage = (e: MessageEvent<Message<S>>) => this.onMessage(e.data);
    const ac = new AbortController();
    this.abort = ac;
    this.coord.locks
      .request(this.name, { signal: ac.signal }, () => {
        if (this.abort !== ac) return undefined; // stopped while waiting
        this.lead();
        return new Promise<void>((r) => (this.release = r));
      })
      .catch(() => undefined); // AbortError after stop()
    ch.postMessage({ t: "hello" } satisfies Message<S>);
  }

  stop(): void {
    this.abort?.abort();
    this.abort = null;
    this.unsubscribe?.();
    this.unsubscribe = null;
    this.controller?.stop();
    this.controller = null;
    this.release?.();
    this.release = null;
    this.channel?.close();
    this.channel = null;
  }

  resync(reason: string): void {
    if (this.controller) this.controller.resync(reason);
    else this.channel?.postMessage({ t: "resync", reason } satisfies Message<S>);
  }

  private lead(): void {
    const c = new StreamController<S>(this.spec);
    this.controller = c;
    this.unsubscribe = c.view.subscribe(() => this.publish(c.view.get()));
    c.start();
    this.publish(c.view.get());
  }

  private publish(v: StreamView<S>): void {
    // Taking over from a leader: keep showing the mirrored data, marked as not
    // current, until the new snapshot lands instead of blanking every tab.
    const prev = this.view.get();
    if (v.data === null && v.status === "connecting" && prev.data !== null) {
      v = { ...v, status: "resyncing", data: prev.data, lastAppliedAt: prev.lastAppliedAt, resyncReason: "LEADER_HANDOVER" };
    }
    this.view.set(v);
    this.broadcast();
  }

  private broadcast(): void {
    const v = this.view.get();
    // An expired bearer belongs to this tab only: it drops to the auth gate,
    // stops the stream and hands the lock to a tab whose bearer still works.
    if (v.status === "auth_expired") return;
    this.channel?.postMessage({ t: "view", view: toWire(v) } satisfies Message<S>);
  }

  private onMessage(m: Message<S>): void {
    if (this.controller) {
      if (m.t === "hello") this.broadcast();
      else if (m.t === "resync") this.controller.resync(m.reason);
      return;
    }
    if (m.t === "view") this.view.set(fromWire(m.view));
  }
}
