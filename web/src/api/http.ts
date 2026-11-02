import type { HTTPError } from "./contract";

/** Non-2xx response with the contract error body (03 §7.3). */
export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly retryable: boolean;
  readonly details?: Record<string, unknown>;
  constructor(status: number, body: Partial<HTTPError>) {
    super(body.message || `HTTP ${status}`);
    this.status = status;
    this.code = body.code ?? (status === 0 ? "NETWORK" : "INTERNAL");
    this.retryable = body.retryable ?? false;
    this.details = body.details;
  }
}

type Listener = () => void;

/**
 * Holds the UI bearer in memory only (02 §21.1). A full page reload loses it
 * by design; the user runs `samsara ui --open` again.
 */
class Auth {
  private token: string | null = null;
  private expired = false;
  private listeners = new Set<Listener>();

  get(): string | null {
    return this.token;
  }
  isExpired(): boolean {
    return this.expired;
  }
  set(token: string | null): void {
    this.token = token;
    this.expired = false;
    this.emit();
  }
  markExpired(): void {
    if (this.expired) return;
    this.expired = true;
    this.emit();
  }
  // Arrow so it can be passed to useSyncExternalStore unbound.
  subscribe = (fn: Listener): (() => void) => {
    this.listeners.add(fn);
    return () => this.listeners.delete(fn);
  };
  private emit(): void {
    for (const fn of this.listeners) fn();
  }
}

export const auth = new Auth();

export function authHeaders(): Record<string, string> {
  const t = auth.get();
  return t ? { Authorization: `Bearer ${t}` } : {};
}

async function errorOf(res: Response): Promise<ApiError> {
  let body: Partial<HTTPError> = {};
  try {
    const j = (await res.json()) as { error?: HTTPError };
    body = j.error ?? {};
  } catch {
    // non-JSON error body: keep the status only
  }
  const err = new ApiError(res.status, body);
  if (err.code === "SESSION_EXPIRED") auth.markExpired();
  return err;
}

export interface Got<T> {
  data: T;
  etag: string | null;
}

export async function getJSON<T>(path: string, signal?: AbortSignal): Promise<Got<T>> {
  let res: Response;
  try {
    res = await fetch(path, { headers: { Accept: "application/json", ...authHeaders() }, signal });
  } catch (e) {
    if ((e as Error).name === "AbortError") throw e;
    throw new ApiError(0, { message: "런타임에 연결할 수 없습니다.", retryable: true });
  }
  if (!res.ok) throw await errorOf(res);
  return { data: (await res.json()) as T, etag: res.headers.get("ETag") };
}

export async function get<T>(path: string, signal?: AbortSignal): Promise<T> {
  return (await getJSON<T>(path, signal)).data;
}

export interface CommandOptions {
  /** Kept by the caller across retries of the same user intent (03 §6). */
  idempotencyKey: string;
  /** "<id>:v<version>" or a bare version the command is conditional on. */
  ifMatch?: string;
  body?: unknown;
}

/** POST a command. 202 means accepted, not completed (02 §16.1). */
export async function command<T>(path: string, opts: CommandOptions): Promise<T> {
  const headers: Record<string, string> = {
    Accept: "application/json",
    "Content-Type": "application/json",
    "Idempotency-Key": opts.idempotencyKey,
    ...authHeaders(),
  };
  if (opts.ifMatch) headers["If-Match"] = `"${opts.ifMatch}"`;
  let res: Response;
  try {
    res = await fetch(path, { method: "POST", headers, body: JSON.stringify(opts.body ?? {}) });
  } catch {
    // The request may or may not have reached the runtime: the result is unknown.
    throw new ApiError(0, { code: "DELIVERY_UNKNOWN", message: "요청 결과를 확인하지 못했습니다.", retryable: true });
  }
  if (!res.ok) throw await errorOf(res);
  return (await res.json()) as T;
}

export function newIdempotencyKey(): string {
  return `ui-${crypto.randomUUID()}`;
}
