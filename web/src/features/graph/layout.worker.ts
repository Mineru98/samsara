/// <reference lib="webworker" />
// ELK runs off the UI thread (02 §9, §14). elk.bundled includes the layout
// engine itself; it is loaded from this same-origin worker file (CSP self).
import ELK from "elkjs/lib/elk.bundled.js";

const elk = new ELK();

self.onmessage = async (ev: MessageEvent<{ seq: number; graph: unknown }>) => {
  const { seq, graph } = ev.data;
  try {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const out = await elk.layout(graph as any);
    (self as unknown as Worker).postMessage({ seq, ok: true, graph: out });
  } catch (e) {
    (self as unknown as Worker).postMessage({ seq, ok: false, error: String(e) });
  }
};
