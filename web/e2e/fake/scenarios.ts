// Scenario snapshots derived from the runtime-written contract fixtures.
// IDs are the fixture's own: in abc-partial-block, a=issue_fx002,
// b=issue_fx003 (depends on a), c=issue_fx001.
import { fixtureSnapshot, type Entities } from "./server";

/* eslint-disable @typescript-eslint/no-explicit-any */
type Json = any;

export const ABC = { project: "prj_fx001", run: "run_fx001", a: "issue_fx002", b: "issue_fx003", c: "issue_fx001", attA: "att_fx002", attC: "att_fx001", sesC: "ses_fx001", dep: "dep_fx001", target: "tgt_fx001" } as const;

export const DIGEST_1 = "sha256:" + "1".repeat(64);
export const DIGEST_2 = "sha256:" + "2".repeat(64);

export function find(e: Entities, kind: string, id: string): Json {
  const hit = e[kind].find((x: Json) => (kind === "dependencies" ? x.dependency_id : x.id) === id);
  if (!hit) throw new Error(`${kind} ${id} missing`);
  return hit;
}

/** F-ABC as the runtime wrote it: A, C running; B blocked on A's result. */
export function abc(): Json {
  return fixtureSnapshot("abc-partial-block");
}

export function resultApproval(issueId: string, over: Json = {}): Json {
  return {
    id: "apr_res_a",
    kind: "RESULT",
    state: "PENDING",
    issue_id: issueId,
    run_id: ABC.run,
    output_id: "out_a1",
    project_id: ABC.project,
    policy_version: "1",
    requested_at: "2026-10-28T01:05:00Z",
    subject: { output_id: "out_a1", source_sha: "0000000000000000000000000000000000000a01", run_generation: 1 },
    subject_digest: DIGEST_1,
    version: "1",
    ...over,
  };
}

/** UI-T01: A waits for its RESULT approval, B is blocked on it, C runs on. */
export function abcApprovalPending(): Json {
  const s = abc();
  const e = s.entities;
  const a = find(e, "issues", ABC.a);
  Object.assign(a, {
    display_status: "APPROVAL_PENDING",
    execution_state: "VERIFIED",
    available_actions: [{ action: "OPEN_APPROVAL", enabled: true, resource_id: "apr_res_a" }],
    version: "4",
  });
  Object.assign(find(e, "attempts", ABC.attA), { state: "VERIFIED" });
  e.approvals.push(resultApproval(ABC.a));
  Object.assign(find(e, "issues", ABC.b), {
    display_status: "BLOCKED",
    blocking_reasons: [
      { code: "DEPENDENCY_APPROVAL_PENDING", dependency_id: ABC.dep, downstream_phase: "EXECUTION", message: "선행 이슈의 결과 승인이 필요합니다.", required_milestone: "RESULT_APPROVED", source_issue_id: ABC.a, approval_id: "apr_res_a" },
    ],
  });
  return s;
}

/** A second project for switching and catalog scenarios. */
export function otherProject(id = "prj_other", name = "other-repo"): Json {
  const s = fixtureSnapshot("merged-history");
  s.project_id = id;
  s.project = { ...s.project, id, display_name: name, root_path: `/fixture/${name}` };
  const rename = (x: Json) => (x.project_id = id);
  for (const list of Object.values(s.entities) as Json[][]) list.forEach(rename);
  return s;
}

/** Seeded F-GRAPH DAG: n issues, ~1.25 edges each, multiple roots and joins. */
export function graphSnapshot(n: number, seed = 109): Json {
  let x = seed >>> 0;
  const rnd = () => ((x = (Math.imul(x, 1664525) + 1013904223) >>> 0) / 2 ** 32);
  const s = abc();
  const pid = "prj_graph";
  s.project_id = pid;
  s.project = { ...s.project, id: pid, display_name: `graph-${n}`, root_path: `/fixture/graph-${n}` };
  const run = { ...s.entities.runs[0], id: "run_graph", project_id: pid, alias_mapping: {} };
  const statuses = ["PLANNED", "READY", "RUNNING", "BLOCKED", "VERIFIED", "INTEGRATED", "APPROVAL_PENDING", "PAUSED"];
  const issues: Json[] = [];
  const deps: Json[] = [];
  const seen = new Set<string>();
  for (let i = 0; i < n; i++) {
    const key = `i${String(i).padStart(4, "0")}`;
    issues.push({
      acceptance_criteria: ["ok"],
      available_actions: [],
      blocking_reasons: [],
      check_profile: "",
      client_key: key,
      display_status: statuses[Math.floor(rnd() * statuses.length)],
      id: `issue_g${i}`,
      lifecycle: "OPEN",
      project_id: pid,
      required_capabilities: [],
      resume_required: false,
      run_id: run.id,
      title: `이슈 ${key} 그래프 규모 측정용 긴 한국어 제목`,
      version: "1",
    });
    if (i === 0) continue;
    const k = [0, 1, 1, 2][Math.floor(rnd() * 4)];
    for (let j = 0; j < k; j++) {
      const p = Math.max(0, i - 60) + Math.floor(rnd() * Math.min(60, i));
      const id = `dep_g${p}_${i}`;
      if (seen.has(id)) continue;
      seen.add(id);
      deps.push({ dependency_id: id, downstream_phase: "EXECUTION", graph_version: "1", invalidation_policy: "PAUSE_DESCENDANTS", met: rnd() < 0.5, predecessor_issue_id: `issue_g${p}`, project_id: pid, required_milestone: "RESULT_APPROVED", required_output_id: null, required_run_generation: "1", run_id: run.id, successor_issue_id: `issue_g${i}`, version: "1" });
    }
  }
  s.entities = { approvals: [], attempts: [], dependencies: deps, gates: [], integration_targets: s.entities.integration_targets.map((t: Json) => ({ ...t })), issues, merge_candidates: [], operations: [], runs: [run], sessions: [], worktrees: [] };
  return s;
}
