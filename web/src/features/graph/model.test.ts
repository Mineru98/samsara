import { describe, expect, test } from "vitest";
import type { GateView } from "../../api/contract";
import { fromSnapshot } from "../../store/projectStore";
import { abcSnapshot } from "../../test/fixtures";
import { simpleLayout } from "./elkGraph";
import { buildGraph, defaultRunId } from "./model";

describe("graph projection", () => {
  test("F-ABC: B depends on A, C independent, left→right layering", () => {
    const s = fromSnapshot(abcSnapshot());
    const run = defaultRunId(s.entities);
    expect(run).toBe("run_fx001");
    const g = buildGraph(s.entities, run, "issues");
    expect(g.nodes.map((n) => n.id).sort()).toEqual(["issue:issue_fx001", "issue:issue_fx002", "issue:issue_fx003"]);
    expect(g.edges).toHaveLength(1);
    const [e] = g.edges;
    expect(e.source).toBe("issue:issue_fx002"); // a
    expect(e.target).toBe("issue:issue_fx003"); // b
    expect(e.met).toBe(false);
    const pos = simpleLayout(g).pos;
    expect(pos["issue:issue_fx003"].x).toBeGreaterThan(pos["issue:issue_fx002"].x);
    expect(pos["issue:issue_fx001"].x).toBe(pos["issue:issue_fx002"].x); // both roots
  });

  test("F-JOIN: an AND requirement goes through one gate node", () => {
    const s = fromSnapshot(abcSnapshot());
    const e = s.entities;
    const dep = e.dependencies.dep_fx001;
    e.dependencies.dep_join2 = { ...dep, dependency_id: "dep_join2", predecessor_issue_id: "issue_fx001" };
    const gate: GateView = {
      id: "gate_1",
      downstream_phase: "INTEGRATION",
      members: [
        { dependency_id: "dep_fx001", issue_id: "issue_fx002", met: true, required_milestone: "INTEGRATED" },
        { dependency_id: "dep_join2", issue_id: "issue_fx001", met: false, required_milestone: "INTEGRATED" },
      ],
      membership_digest: "d",
      membership_version: "1",
      open: false,
      run_generation: "1",
      run_id: "run_fx001",
      successor_issue_id: "issue_fx003",
    };
    e.gates.gate_1 = gate;
    const g = buildGraph(e, "run_fx001", "issues");
    expect(g.nodes.filter((n) => n.kind === "gate")).toHaveLength(1);
    expect(g.edges.map((x) => `${x.source}>${x.target}`).sort()).toEqual([
      "gate:gate_1>issue:issue_fx003",
      "issue:issue_fx001>gate:gate_1",
      "issue:issue_fx002>gate:gate_1",
    ]);
  });

  test("worktree mode groups the same issues without changing edges", () => {
    const s = fromSnapshot(abcSnapshot());
    const a = buildGraph(s.entities, "run_fx001", "issues");
    const b = buildGraph(s.entities, "run_fx001", "worktrees");
    expect(b.edges).toEqual(a.edges);
    expect(b.nodes.filter((n) => n.kind === "issue")).toHaveLength(3);
    expect(b.nodes.filter((n) => n.kind === "group").length).toBeGreaterThanOrEqual(2);
  });

  test("status-only changes keep the structure key (layout stays stable)", () => {
    const s = fromSnapshot(abcSnapshot());
    const k1 = buildGraph(s.entities, "run_fx001", "issues").structureKey;
    s.entities.issues.issue_fx003 = { ...s.entities.issues.issue_fx003, display_status: "RUNNING", version: "9" };
    expect(buildGraph(s.entities, "run_fx001", "issues").structureKey).toBe(k1);
  });
});
