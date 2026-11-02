import { useSyncExternalStore } from "react";

// Minimal history router: URL keeps project, run, view, selection and tab so
// deep links and back/forward work (02 §3, §3.1).

type Listener = () => void;
const listeners = new Set<Listener>();
let snapshot = typeof window === "undefined" ? "/" : window.location.pathname + window.location.search;

function refresh(): void {
  const next = window.location.pathname + window.location.search;
  if (next === snapshot) return;
  snapshot = next;
  for (const fn of listeners) fn();
}

if (typeof window !== "undefined") window.addEventListener("popstate", refresh);

export function navigate(to: string, opts: { replace?: boolean } = {}): void {
  if (opts.replace) window.history.replaceState(null, "", to);
  else window.history.pushState(null, "", to);
  refresh();
}

export function useLocationString(): string {
  return useSyncExternalStore(
    (fn) => {
      listeners.add(fn);
      return () => listeners.delete(fn);
    },
    () => snapshot,
    () => "/",
  );
}

export interface Route {
  projectId: string | null;
  params: URLSearchParams;
}

export function parseRoute(loc: string): Route {
  const url = new URL(loc, "http://x");
  const m = /^\/projects\/([^/]+)/.exec(url.pathname);
  return { projectId: m ? decodeURIComponent(m[1]) : null, params: url.searchParams };
}

export function useRoute(): Route {
  return parseRoute(useLocationString());
}

/** Update query params of the current URL, dropping empty ones. */
export function setParams(patch: Record<string, string | null>, opts: { replace?: boolean } = {}): void {
  const url = new URL(window.location.href);
  for (const [k, v] of Object.entries(patch)) {
    if (v === null || v === "") url.searchParams.delete(k);
    else url.searchParams.set(k, v);
  }
  navigate(url.pathname + url.search, opts);
}

export function projectPath(id: string): string {
  return `/projects/${encodeURIComponent(id)}`;
}
