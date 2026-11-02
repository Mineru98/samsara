import { describe, expect, test } from "vitest";
import { abcSnapshot, event } from "../test/fixtures";
import { cmpDec } from "./decimal";
import { applyEvent, fromSnapshot } from "./projectStore";

describe("decimal strings (EVT-09)", () => {
  test("compares beyond Number.MAX_SAFE_INTEGER without precision loss", () => {
    expect(cmpDec("9007199254740993", "9007199254740992")).toBe(1);
    expect(Number("9007199254740993") === Number("9007199254740992")).toBe(true); // why strings
    expect(cmpDec("10", "9")).toBe(1);
    expect(cmpDec("007", "7")).toBe(0);
  });
});

describe("project store", () => {
  test("snapshot keeps the server project_version and entity versions as strings", () => {
    const s = fromSnapshot(abcSnapshot());
    expect(s.projectVersion).toBe("11");
    expect(typeof s.entities.issues.issue_fx003.version).toBe("string");
    expect(s.entities.issues.issue_fx003.display_status).toBe("BLOCKED");
    expect(s.entities.dependencies.dep_fx001.successor_issue_id).toBe("issue_fx003");
  });

  test("applies a contiguous change set atomically and advances version and cursor", () => {
    const s = fromSnapshot(abcSnapshot());
    const a = s.entities.issues.issue_fx002;
    const r = applyEvent(s, event("11", "12", { upserts: { issues: [{ ...a, version: "2", display_status: "APPROVAL_PENDING" }] }, deletes: { sessions: ["ses_fx001"] } }), "cur12");
    expect(r.kind).toBe("applied");
    if (r.kind !== "applied") return;
    expect(r.state.projectVersion).toBe("12");
    expect(r.state.cursor).toBe("cur12");
    expect(r.state.entities.issues.issue_fx002.display_status).toBe("APPROVAL_PENDING");
    expect(r.state.entities.sessions.ses_fx001).toBeUndefined();
    // The previous state object is untouched (no partial mutation).
    expect(s.entities.issues.issue_fx002.display_status).toBe("RUNNING");
    expect(s.entities.sessions.ses_fx001).toBeDefined();
  });

  test("treats replayed or older project versions as duplicates (UI-T08)", () => {
    const s = fromSnapshot(abcSnapshot());
    expect(applyEvent(s, event("10", "11", { upserts: {}, deletes: {} }), "c").kind).toBe("duplicate");
    expect(applyEvent(s, event("9", "10", { upserts: {}, deletes: {} }), "c").kind).toBe("duplicate");
  });

  test("detects a project version gap instead of applying (UI-T20)", () => {
    const s = fromSnapshot(abcSnapshot());
    const r = applyEvent(s, event("12", "13", { upserts: {}, deletes: {} }), "c");
    expect(r).toEqual({ kind: "gap", reason: "PROJECT_VERSION_GAP" });
  });

  test("never regresses an aggregate to an older version", () => {
    const s = fromSnapshot(abcSnapshot());
    const w = s.entities.worktrees.wt_fx001;
    const r = applyEvent(s, event("11", "12", { upserts: { worktrees: [{ ...w, version: "2", state: "CREATING" }] }, deletes: {} }), "c");
    if (r.kind !== "applied") throw new Error(r.kind);
    expect(r.state.entities.worktrees.wt_fx001.version).toBe("3");
    expect(r.state.entities.worktrees.wt_fx001.state).toBe(w.state);
  });

  test("ignores unknown entity kinds from a newer runtime but rejects a breaking schema", () => {
    const s = fromSnapshot(abcSnapshot());
    const ok = applyEvent(s, event("11", "12", { upserts: { widgets: [{ id: "w" }] } as never, deletes: {} }), "c");
    expect(ok.kind).toBe("applied");
    const bad = applyEvent(s, event("11", "12", { upserts: {}, deletes: {} }, { schema_version: "2" as never }), "c");
    expect(bad.kind).toBe("incompatible");
  });

  test("rejects events of another project", () => {
    const s = fromSnapshot(abcSnapshot());
    expect(applyEvent(s, event("11", "12", { upserts: {}, deletes: {} }, { project_id: "prj_other" }), "c").kind).toBe("gap");
  });
});
