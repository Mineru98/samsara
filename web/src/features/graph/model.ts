import type { DependencyView, GateView, IssueView } from "../../api/contract";
import type { Entities } from "../../store/projectStore";

export type GraphMode = "issues" | "worktrees";

export interface GNode {
  id: string;
  kind: "issue" | "gate" | "group";
  /** Parent group id in worktree mode. */
  parent?: string;
  width: number;
  height: number;
  issueId?: string;
  gateId?: string;
  label?: string;
}

export interface GEdge {
  id: string;
  source: string;
  target: string;
  met: boolean;
  milestone: string;
  phase: string;
}

export interface GraphModel {
  nodes: GNode[];
  edges: GEdge[];
  /** Changes only when nodes/edges are added or removed (02 §9.3). */
  structureKey: string;
}

export const ISSUE_W = 248;
export const ISSUE_H = 88;
const GATE_W = 132;
const GATE_H = 48;

/** Issues of the selected run, or every issue when no run is selected. */
export function issuesOf(e: Entities, runId: string | null): IssueView[] {
  const all = Object.values(e.issues);
  const list = runId ? all.filter((i) => i.run_id === runId) : all;
  return list.sort((a, b) => a.client_key.localeCompare(b.client_key) || a.id.localeCompare(b.id));
}

/** The run shown by default: the newest non-terminal run, else the newest. */
export function defaultRunId(e: Entities): string | null {
  const runs = Object.values(e.runs).sort((a, b) => b.created_at.localeCompare(a.created_at));
  const live = runs.find((r) => r.state === "ACTIVE" || r.state === "PAUSED" || r.state === "PLANNED");
  return (live ?? runs[0])?.id ?? null;
}

/** Current worktree of an issue via its current attempt. */
export function worktreeOfIssue(e: Entities, issue: IssueView): string | null {
  const att = issue.current_attempt_id ? e.attempts[issue.current_attempt_id] : undefined;
  return att?.worktree_id ?? null;
}

/**
 * Project server entities into a left→right display graph (02 §7–8).
 * AND requirements of one successor phase go through an Integration Gate
 * node; single requirements are direct edges. Worktree mode only groups the
 * same issue nodes; it never changes the dependency edges (02 §7.2).
 */
export function buildGraph(e: Entities, runId: string | null, mode: GraphMode): GraphModel {
  const issues = issuesOf(e, runId);
  const ids = new Set(issues.map((i) => i.id));
  const nodes: GNode[] = [];
  const edges: GEdge[] = [];

  if (mode === "worktrees") {
    const groups = new Map<string, string>();
    for (const i of issues) {
      const wt = worktreeOfIssue(e, i);
      const gid = `wt:${wt ?? "none"}`;
      if (!groups.has(gid)) {
        const w = wt ? e.worktrees[wt] : undefined;
        groups.set(gid, w ? w.branch_ref.replace(/^refs\/heads\//, "") : "Worktree 없음");
      }
    }
    for (const [gid, label] of groups) nodes.push({ id: gid, kind: "group", width: 0, height: 0, label });
  }
  for (const i of issues) {
    const parent = mode === "worktrees" ? `wt:${worktreeOfIssue(e, i) ?? "none"}` : undefined;
    nodes.push({ id: `issue:${i.id}`, kind: "issue", issueId: i.id, parent, width: ISSUE_W, height: ISSUE_H });
  }

  const gated = new Set<string>();
  const gates: GateView[] = Object.values(e.gates).filter(
    (g) => ids.has(g.successor_issue_id) && (!runId || g.run_id === runId) && g.members.length >= 2,
  );
  for (const g of gates) {
    const gid = `gate:${g.id}`;
    nodes.push({ id: gid, kind: "gate", gateId: g.id, width: GATE_W, height: GATE_H });
    for (const m of g.members) {
      gated.add(m.dependency_id);
      if (!ids.has(m.issue_id)) continue;
      edges.push({ id: `e:${m.dependency_id}`, source: `issue:${m.issue_id}`, target: gid, met: m.met, milestone: m.required_milestone, phase: g.downstream_phase });
    }
    edges.push({ id: `e:${gid}`, source: gid, target: `issue:${g.successor_issue_id}`, met: g.open, milestone: "", phase: g.downstream_phase });
  }
  const deps: DependencyView[] = Object.values(e.dependencies).filter(
    (d) => !gated.has(d.dependency_id) && ids.has(d.predecessor_issue_id) && ids.has(d.successor_issue_id),
  );
  for (const d of deps) {
    edges.push({
      id: `e:${d.dependency_id}`,
      source: `issue:${d.predecessor_issue_id}`,
      target: `issue:${d.successor_issue_id}`,
      met: d.met,
      milestone: d.required_milestone,
      phase: d.downstream_phase,
    });
  }
  edges.sort((a, b) => a.id.localeCompare(b.id));
  const structureKey = mode + "|" + nodes.map((n) => n.id + "@" + (n.parent ?? "")).join(",") + "|" + edges.map((x) => `${x.source}>${x.target}`).join(",");
  return { nodes, edges, structureKey };
}
