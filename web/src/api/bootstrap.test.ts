import { afterEach, describe, expect, test, vi } from "vitest";
import { bootstrapFromFragment } from "./bootstrap";
import { auth } from "./http";

afterEach(() => {
  vi.unstubAllGlobals();
  auth.set(null);
});

describe("bootstrap fragment (03 §11.1)", () => {
  test("removes the fragment before exchanging and keeps the token in memory only", async () => {
    window.history.replaceState(null, "", "/projects/p1?view=list#bootstrap=abc123");
    let hashAtFetch = "unset";
    const fetchMock = vi.fn(async (_url: string, init: RequestInit) => {
      hashAtFetch = window.location.hash;
      expect(JSON.parse(String(init.body))).toEqual({ code: "abc123" });
      return new Response(JSON.stringify({ token: "tok", expires_at: "x" }), { status: 200 });
    });
    vi.stubGlobal("fetch", fetchMock);
    const before = window.localStorage.length;
    expect(await bootstrapFromFragment()).toBe("exchanged");
    expect(hashAtFetch).toBe("");
    expect(window.location.pathname + window.location.search).toBe("/projects/p1?view=list");
    expect(auth.get()).toBe("tok");
    expect(window.localStorage.length).toBe(before);
    expect(window.sessionStorage.getItem("tok")).toBeNull();
  });

  test("removes the fragment even when the code is expired or used", async () => {
    window.history.replaceState(null, "", "/#bootstrap=old");
    vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({ error: { code: "SESSION_EXPIRED" } }), { status: 401 })));
    expect(await bootstrapFromFragment()).toBe("failed");
    expect(window.location.hash).toBe("");
    expect(auth.get()).toBeNull();
  });

  test("removes a malformed fragment instead of throwing", async () => {
    window.history.replaceState(null, "", "/#bootstrap=%E0%A4%A");
    vi.stubGlobal("fetch", vi.fn());
    expect(await bootstrapFromFragment()).toBe("failed");
    expect(window.location.hash).toBe("");
  });

  test("does nothing without a fragment", async () => {
    window.history.replaceState(null, "", "/");
    const f = vi.fn();
    vi.stubGlobal("fetch", f);
    expect(await bootstrapFromFragment()).toBe("none");
    expect(f).not.toHaveBeenCalled();
  });
});
