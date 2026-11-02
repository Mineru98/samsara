import { afterEach, describe, expect, test, vi } from "vitest";
import { auth } from "../api/http";
import type { CatalogEvent, ProjectSummary } from "../api/contract";
import { applyCatalogEvent, catalogSpec, type CatalogState } from "./specs";
import { StreamController } from "./stream";

const enc = new TextEncoder();

function summary(id: string, version: string): ProjectSummary {
  return { id, version } as ProjectSummary;
}

function catalogEvent(catalogVersion: string, project: ProjectSummary): CatalogEvent {
  return {
    schema_version: "1",
    seq: catalogVersion,
    catalog_version: catalogVersion,
    project_id: project.id,
    recorded_at: "2026-11-03T00:00:00Z",
    payload: { project },
  };
}

function state(catalogVersion: string): CatalogState {
  return { catalogVersion, projects: { prj_a: summary("prj_a", "3") } };
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

describe("applyCatalogEvent", () => {
  test("the next catalog_version is applied", () => {
    const r = applyCatalogEvent(state("7"), catalogEvent("8", summary("prj_a", "4")));
    expect(r).toEqual({ kind: "applied", state: { catalogVersion: "8", projects: { prj_a: summary("prj_a", "4") } } });
  });

  test("a skipped catalog_version is a gap, not an apply", () => {
    expect(applyCatalogEvent(state("7"), catalogEvent("9", summary("prj_a", "5")))).toEqual({ kind: "gap", reason: "CATALOG_VERSION_GAP" });
  });

  test("the gap check carries across decimal digits without number conversion", () => {
    expect(applyCatalogEvent(state("9"), catalogEvent("10", summary("prj_a", "4"))).kind).toBe("applied");
    expect(applyCatalogEvent(state("9007199254740993"), catalogEvent("9007199254740994", summary("prj_a", "4"))).kind).toBe("applied");
    expect(applyCatalogEvent(state("9007199254740993"), catalogEvent("9007199254740995", summary("prj_a", "4"))).kind).toBe("gap");
  });

  test("replayed or older catalog_version stays a duplicate", () => {
    expect(applyCatalogEvent(state("7"), catalogEvent("7", summary("prj_a", "9")))).toEqual({ kind: "duplicate" });
    expect(applyCatalogEvent(state("7"), catalogEvent("5", summary("prj_a", "9")))).toEqual({ kind: "duplicate" });
  });
});

describe("catalog stream (02 §15.2)", () => {
  test("a catalog_version gap reloads the catalog snapshot", async () => {
    auth.set("tok");
    let snapshots = 0;
    const urls: string[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string) => {
        urls.push(url);
        if (url.endsWith("/snapshot")) {
          snapshots++;
          const v = snapshots === 1 ? "7" : "9";
          return new Response(
            JSON.stringify({ schema_version: "1", catalog_version: v, database_generation: "g1", snapshot_cursor: `cur${v}`, projects: [summary("prj_a", v)] }),
            { status: 200 },
          );
        }
        if (url.includes("after=cur7")) {
          // catalog_version 8 never arrives: 9 is a gap.
          const ev = catalogEvent("9", summary("prj_a", "9"));
          return new Response(
            new ReadableStream({
              start(c) {
                c.enqueue(enc.encode(`id: c9\nevent: catalog.change\ndata: ${JSON.stringify(ev)}\n\n`));
                c.close();
              },
            }),
            { status: 200 },
          );
        }
        return new Response(new ReadableStream({ start() {} }), { status: 200 });
      }),
    );
    const c = new StreamController<CatalogState>(catalogSpec);
    c.start();
    await until(() => snapshots === 2 && c.view.get().status === "live");
    expect(c.view.get().data?.catalogVersion).toBe("9");
    expect(urls.at(-1)).toContain("after=cur9");
    c.stop();
  });
});
