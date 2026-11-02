import { useCallback, useEffect, useRef, useState, useSyncExternalStore } from "react";
import { ApiError, command, newIdempotencyKey } from "./api/http";
import type { Observable } from "./store/projectStore";
import type { StreamController, StreamSpec, StreamView } from "./realtime/stream";
import { StreamController as Controller } from "./realtime/stream";

export function useObservable<T>(o: Observable<T>): T {
  return useSyncExternalStore(o.subscribe, o.get, o.get);
}

/**
 * One controller per key. Effects create and stop it so React StrictMode's
 * double mount never leaves a second stream open (02 §14.1, §15.4).
 */
export function useStream<S>(key: string | null, makeSpec: (key: string) => StreamSpec<S>): { view: StreamView<S> | null; controller: StreamController<S> | null } {
  const [controller, setController] = useState<StreamController<S> | null>(null);
  const specRef = useRef(makeSpec);
  specRef.current = makeSpec;
  useEffect(() => {
    if (!key) {
      setController(null);
      return;
    }
    const c = new Controller<S>(specRef.current(key));
    setController(c);
    c.start();
    return () => c.stop();
  }, [key]);
  const view = useSyncExternalStore(
    (fn) => (controller ? controller.view.subscribe(fn) : () => undefined),
    () => (controller ? controller.view.get() : null),
    () => null,
  );
  return { view, controller };
}

export type CommandPhase = "idle" | "pending" | "accepted" | "failed" | "unknown";

export interface CommandState {
  phase: CommandPhase;
  error: ApiError | null;
  result: unknown;
}

/**
 * A user intent to run one command. The idempotency key is kept for retries
 * of the same intent and replaced only after an accepted or definitive
 * failure (02 §16.1, 03 §6). Accepted (202) is not completion: the caller
 * watches the stream for the resulting state.
 */
export function useCommand(): {
  state: CommandState;
  run: (path: string, opts: { ifMatch?: string; body?: unknown }) => Promise<CommandPhase>;
  reset: () => void;
} {
  const keyRef = useRef<string>(newIdempotencyKey());
  // The exact request whose outcome is unknown. Only an identical retry may
  // reuse its key; the server rejects a reused key with another payload.
  const unknownRef = useRef<string | null>(null);
  const [state, setState] = useState<CommandState>({ phase: "idle", error: null, result: null });
  const run = useCallback(async (path: string, opts: { ifMatch?: string; body?: unknown }): Promise<CommandPhase> => {
    const fingerprint = JSON.stringify([path, opts.ifMatch ?? null, opts.body ?? null]);
    if (unknownRef.current !== null && unknownRef.current !== fingerprint) keyRef.current = newIdempotencyKey();
    unknownRef.current = null;
    setState({ phase: "pending", error: null, result: null });
    try {
      const result = await command(path, { idempotencyKey: keyRef.current, ...opts });
      keyRef.current = newIdempotencyKey();
      setState({ phase: "accepted", error: null, result });
      return "accepted";
    } catch (e) {
      const err = e instanceof ApiError ? e : new ApiError(0, { message: String(e) });
      if (err.code === "DELIVERY_UNKNOWN" || err.status === 0) {
        // Keep the same key: a retry must not execute the command twice.
        unknownRef.current = fingerprint;
        setState({ phase: "unknown", error: err, result: null });
        return "unknown";
      }
      keyRef.current = newIdempotencyKey();
      setState({ phase: "failed", error: err, result: null });
      return "failed";
    }
  }, []);
  const reset = useCallback(() => {
    keyRef.current = newIdempotencyKey();
    unknownRef.current = null;
    setState({ phase: "idle", error: null, result: null });
  }, []);
  return { state, run, reset };
}

/** Live CSS media query match (02 §19 breakpoints). */
export function useMediaQuery(query: string): boolean {
  const subscribe = useCallback(
    (fn: () => void) => {
      const m = window.matchMedia(query);
      m.addEventListener("change", fn);
      return () => m.removeEventListener("change", fn);
    },
    [query],
  );
  return useSyncExternalStore(subscribe, () => window.matchMedia(query).matches, () => false);
}
