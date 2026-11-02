import { fromElk, toElk, type ElkNodeOut, type Positions } from "./elkGraph";
import type { GraphModel } from "./model";

const TIMEOUT_MS = 5000;

let worker: Worker | null = null;
let seq = 0;
const pending = new Map<number, { resolve: (p: Positions) => void; reject: (e: Error) => void; timer: ReturnType<typeof setTimeout> }>();

function getWorker(): Worker | null {
  if (worker) return worker;
  if (typeof Worker === "undefined") return null;
  worker = new Worker(new URL("./layout.worker.ts", import.meta.url), { type: "module" });
  worker.onmessage = (ev: MessageEvent<{ seq: number; ok: boolean; graph?: ElkNodeOut; error?: string }>) => {
    const p = pending.get(ev.data.seq);
    if (!p) return;
    pending.delete(ev.data.seq);
    clearTimeout(p.timer);
    if (ev.data.ok && ev.data.graph) p.resolve(fromElk(ev.data.graph));
    else p.reject(new Error(ev.data.error ?? "layout failed"));
  };
  return worker;
}

/**
 * Lay out a graph in the ELK worker. The caller compares the returned
 * structure key with its current graph so a slow answer for an old project
 * or old structure never overwrites a newer view (02 §22).
 */
export function layoutInWorker(g: GraphModel): Promise<Positions> {
  const w = getWorker();
  if (!w) return Promise.reject(new Error("worker unavailable"));
  const id = ++seq;
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => {
      pending.delete(id);
      reject(new Error("layout timeout"));
    }, TIMEOUT_MS);
    pending.set(id, { resolve, reject, timer });
    w.postMessage({ seq: id, graph: toElk(g) });
  });
}
