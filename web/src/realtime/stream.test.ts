import { afterEach, describe, expect, test, vi } from "vitest";
import { auth } from "../api/http";
import type { ProjectState } from "../store/projectStore";
import { abcSnapshot, event } from "../test/fixtures";
import { projectSpec } from "./specs";
import { StreamController } from "./stream";

const enc = new TextEncoder();

function sse(frames: string[], keepOpen = false): ReadableStream<Uint8Array> {
  return new ReadableStream({
    start(c) {
      for (const f of frames) c.enqueue(enc.encode(f));
      if (!keepOpen) c.close();
    },
  });
}

function frame(id: string, data: unknown, type = "project.change"): string {
  return `id: ${id}\nevent: ${type}\ndata: ${JSON.stringify(data)}\n\n`;
}

async function until(fn: () => boolean, ms = 2000): Promise<void> {
  const t0 = Date.now();
  while (!fn()) {
    if (Date.now() - t0 > ms) throw new Error("timeout");
    await new Promise((r) => setTimeout(r, 5));
  }
}

afterEach(() => {
  vi.unstubAllGlobals();
  auth.set(null);
});

describe("StreamController (02 §15)", () => {
  test("snapshot → cursor replay → gap triggers a full snapshot reload (UI-T20)", async () => {
    auth.set("tok");
    const snap1 = abcSnapshot();
    const snap2 = { ...abcSnapshot(), project_version: "14", snapshot_cursor: "snapcur14" };
    const a = snap1.entities.issues[0];
    const urls: string[] = [];
    let snapshots = 0;
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string, init: RequestInit) => {
        urls.push(url);
        expect((init.headers as Record<string, string>).Authorization).toBe("Bearer tok");
        if (url.endsWith("/snapshot")) {
          snapshots++;
          return new Response(JSON.stringify(snapshots === 1 ? snap1 : snap2), { status: 200 });
        }
        if (url.includes("after=" + encodeURIComponent(snap1.snapshot_cursor))) {
          return new Response(
            sse([
              ": heartbeat\n\n",
              frame("c12", event("11", "12", { upserts: { issues: [{ ...a, version: "2", title: "갱신됨" }] }, deletes: {} })),
              frame("c11dup", event("10", "11", { upserts: {}, deletes: {} })),
              frame("c14", event("13", "14", { upserts: {}, deletes: {} })), // 13 never arrived
            ]),
            { status: 200 },
          );
        }
        return new Response(sse([], true), { status: 200 });
      }),
    );
    const c = new StreamController<ProjectState>(projectSpec("prj_fx001"));
    c.start();
    await until(() => snapshots === 2 && c.view.get().status === "live");
    const v = c.view.get();
    expect(v.data?.projectVersion).toBe("14");
    expect(urls.at(-1)).toContain("after=snapcur14");
    expect(v.resyncReason).toBeNull();
    c.stop();
  });

  test("reset_required closes the stream and reloads the snapshot (UI-T09)", async () => {
    auth.set("tok");
    let snapshots = 0;
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string) => {
        if (url.endsWith("/snapshot")) {
          snapshots++;
          return new Response(JSON.stringify(abcSnapshot()), { status: 200 });
        }
        return new Response(snapshots === 1 ? sse(['event: reset_required\ndata: {"reason":"CURSOR_EXPIRED"}\n\n']) : sse([], true), { status: 200 });
      }),
    );
    const c = new StreamController<ProjectState>(projectSpec("prj_fx001"));
    c.start();
    await until(() => snapshots === 2 && c.view.get().status === "live");
    c.stop();
  });

  test("an expired bearer stops retrying and asks for a new bootstrap (UI-T21)", async () => {
    auth.set("tok");
    const f = vi.fn(async () => new Response(JSON.stringify({ error: { code: "SESSION_EXPIRED", message: "expired" } }), { status: 401 }));
    vi.stubGlobal("fetch", f);
    const c = new StreamController<ProjectState>(projectSpec("prj_fx001"));
    c.start();
    await until(() => c.view.get().status === "auth_expired");
    expect(f).toHaveBeenCalledTimes(1);
    expect(auth.isExpired()).toBe(true);
  });

  test("events after stop() never reach the view (UI-T10)", async () => {
    auth.set("tok");
    let push: ((s: string) => void) | null = null;
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string) => {
        if (url.endsWith("/snapshot")) return new Response(JSON.stringify(abcSnapshot()), { status: 200 });
        return new Response(
          new ReadableStream({
            start(ctrl) {
              push = (s) => ctrl.enqueue(enc.encode(s));
            },
          }),
          { status: 200 },
        );
      }),
    );
    const c = new StreamController<ProjectState>(projectSpec("prj_fx001"));
    c.start();
    await until(() => c.view.get().status === "live" && push !== null);
    c.stop();
    try {
      push!(frame("c12", event("11", "12", { upserts: {}, deletes: {} })));
    } catch {
      /* stream already cancelled */
    }
    await new Promise((r) => setTimeout(r, 20));
    expect(c.view.get().data?.projectVersion).toBe("11");
  });

  test("an unknown snapshot schema stops as incompatible instead of retrying", async () => {
    auth.set("tok");
    const f = vi.fn(async () => new Response(JSON.stringify({ ...abcSnapshot(), schema_version: "2" }), { status: 200 }));
    vi.stubGlobal("fetch", f);
    const c = new StreamController<ProjectState>(projectSpec("prj_fx001"));
    c.start();
    await until(() => c.view.get().status === "incompatible");
    await new Promise((r) => setTimeout(r, 30));
    expect(f).toHaveBeenCalledTimes(1);
  });

  test("a permanent stream refusal reloads the snapshot and surfaces the error", async () => {
    auth.set("tok");
    let snapshots = 0;
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string) => {
        if (url.endsWith("/snapshot")) {
          snapshots++;
          return snapshots === 1
            ? new Response(JSON.stringify(abcSnapshot()), { status: 200 })
            : new Response(JSON.stringify({ error: { code: "RESOURCE_NOT_FOUND", message: "gone", retryable: false } }), { status: 404 });
        }
        return new Response(JSON.stringify({ error: { code: "RESOURCE_NOT_FOUND", message: "gone", retryable: false } }), { status: 404 });
      }),
    );
    const c = new StreamController<ProjectState>(projectSpec("prj_fx001"));
    c.start();
    await until(() => c.view.get().status === "error", 5000);
    expect(c.view.get().error?.code).toBe("RESOURCE_NOT_FOUND");
  });
});
