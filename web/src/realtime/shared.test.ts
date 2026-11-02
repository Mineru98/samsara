import { afterEach, describe, expect, test, vi } from "vitest";
import { auth } from "../api/http";
import type { ProjectState } from "../store/projectStore";
import { abcSnapshot } from "../test/fixtures";
import { SharedStream, type TabCoordination } from "./shared";
import { projectSpec } from "./specs";

/** In-memory Web Locks + BroadcastChannel shared by the fake "tabs". */
function fakeBrowser(): TabCoordination {
  const held = new Set<string>();
  const queue: { name: string; grant: () => void }[] = [];
  const channels = new Set<FakeChannel>();

  class FakeChannel {
    onmessage: ((e: MessageEvent) => void) | null = null;
    constructor(readonly name: string) {
      channels.add(this);
    }
    postMessage(data: unknown): void {
      const copy = structuredClone(data);
      for (const c of channels) {
        if (c !== this && c.name === this.name) queueMicrotask(() => c.onmessage?.({ data: copy } as MessageEvent));
      }
    }
    close(): void {
      channels.delete(this);
    }
  }

  function pump(): void {
    for (const w of [...queue]) {
      if (held.has(w.name)) continue;
      queue.splice(queue.indexOf(w), 1);
      w.grant();
    }
  }

  const locks = {
    request(name: string, opts: { signal?: AbortSignal }, cb: () => Promise<void> | undefined): Promise<void> {
      return new Promise<void>((resolve, reject) => {
        const w = {
          name,
          grant: () => {
            held.add(name);
            void Promise.resolve(cb()).then(() => {
              held.delete(name);
              resolve();
              pump();
            });
          },
        };
        opts.signal?.addEventListener("abort", () => {
          const i = queue.indexOf(w);
          if (i >= 0) {
            queue.splice(i, 1);
            reject(new DOMException("aborted", "AbortError"));
          }
        });
        queue.push(w);
        queueMicrotask(pump);
      });
    },
  } as unknown as TabCoordination["locks"];

  return { locks, channel: (name) => new FakeChannel(name) as unknown as BroadcastChannel };
}

async function until(fn: () => boolean, ms = 2000): Promise<void> {
  const t0 = Date.now();
  while (!fn()) {
    if (Date.now() - t0 > ms) throw new Error("timeout");
    await new Promise((r) => setTimeout(r, 5));
  }
}

function stubRuntime(): { streams: () => number; snapshots: () => number } {
  let streams = 0;
  let snapshots = 0;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, init: RequestInit) => {
      if (url.endsWith("/snapshot")) {
        snapshots++;
        return new Response(JSON.stringify(abcSnapshot()), { status: 200 });
      }
      streams++;
      return new Response(
        new ReadableStream({
          start(c) {
            init.signal?.addEventListener("abort", () => c.error(new DOMException("aborted", "AbortError")));
          },
        }),
        { status: 200 },
      );
    }),
  );
  return { streams: () => streams, snapshots: () => snapshots };
}

afterEach(() => {
  vi.unstubAllGlobals();
  auth.set(null);
});

describe("SharedStream (02 §15.5)", () => {
  test("three tabs of one project share a single SSE connection", async () => {
    auth.set("tok");
    const rt = stubRuntime();
    const browser = fakeBrowser();
    const tabs = [1, 2, 3].map(() => new SharedStream<ProjectState>("samsara-stream:prj_fx001", projectSpec("prj_fx001"), browser));
    for (const t of tabs) t.start();
    await until(() => tabs.every((t) => t.view.get().status === "live"));
    expect(rt.streams()).toBe(1);
    expect(rt.snapshots()).toBe(1);
    for (const t of tabs) expect(t.view.get().data?.projectVersion).toBe("11");
    for (const t of tabs) t.stop();
  });

  test("a follower's resync runs on the leader and reaches every tab", async () => {
    auth.set("tok");
    const rt = stubRuntime();
    const browser = fakeBrowser();
    const leader = new SharedStream<ProjectState>("s", projectSpec("prj_fx001"), browser);
    const follower = new SharedStream<ProjectState>("s", projectSpec("prj_fx001"), browser);
    leader.start();
    follower.start();
    await until(() => follower.view.get().status === "live");
    follower.resync("USER_REQUEST");
    await until(() => rt.snapshots() === 2 && follower.view.get().status === "live");
    expect(rt.streams()).toBe(2); // the old stream was replaced, not duplicated
    leader.stop();
    follower.stop();
  });

  test("closing the leader tab hands the stream to the next tab", async () => {
    auth.set("tok");
    const rt = stubRuntime();
    const browser = fakeBrowser();
    const a = new SharedStream<ProjectState>("s", projectSpec("prj_fx001"), browser);
    const b = new SharedStream<ProjectState>("s", projectSpec("prj_fx001"), browser);
    a.start();
    b.start();
    await until(() => b.view.get().status === "live");
    expect(rt.streams()).toBe(1);
    a.stop();
    await until(() => rt.streams() === 2 && b.view.get().status === "live");
    expect(b.view.get().data?.projectVersion).toBe("11");
    b.stop();
  });

  test("errors keep their contract fields across tabs", async () => {
    auth.set("tok");
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => new Response(JSON.stringify({ error: { code: "RESOURCE_NOT_FOUND", message: "gone", retryable: false } }), { status: 404 })),
    );
    const browser = fakeBrowser();
    const a = new SharedStream<ProjectState>("s", projectSpec("prj_fx001"), browser);
    const b = new SharedStream<ProjectState>("s", projectSpec("prj_fx001"), browser);
    a.start();
    b.start();
    await until(() => b.view.get().status === "error");
    expect(b.view.get().error?.code).toBe("RESOURCE_NOT_FOUND");
    expect(b.view.get().error?.status).toBe(404);
    a.stop();
    b.stop();
  });

  test("without Web Locks each tab streams on its own", async () => {
    auth.set("tok");
    const rt = stubRuntime();
    const a = new SharedStream<ProjectState>("s", projectSpec("prj_fx001"), null);
    const b = new SharedStream<ProjectState>("s", projectSpec("prj_fx001"), null);
    a.start();
    b.start();
    await until(() => a.view.get().status === "live" && b.view.get().status === "live");
    expect(rt.streams()).toBe(2);
    a.stop();
    b.stop();
  });
});
