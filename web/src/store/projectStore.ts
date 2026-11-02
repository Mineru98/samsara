import type {
  ApprovalView,
  AttemptView,
  CandidateView,
  DependencyView,
  GateView,
  IssueView,
  OperationView,
  ProjectEvent,
  ProjectSnapshot,
  ProjectView,
  RunView,
  SessionView,
  SnapshotFreshness,
  TargetView,
  WorktreeView,
} from "../api/contract";
import { cmpDec, isDec } from "./decimal";

export interface Entities {
  issues: Record<string, IssueView>;
  runs: Record<string, RunView>;
  attempts: Record<string, AttemptView>;
  sessions: Record<string, SessionView>;
  worktrees: Record<string, WorktreeView>;
  dependencies: Record<string, DependencyView>;
  gates: Record<string, GateView>;
  approvals: Record<string, ApprovalView>;
  merge_candidates: Record<string, CandidateView>;
  integration_targets: Record<string, TargetView>;
  operations: Record<string, OperationView>;
}

export type EntityKind = keyof Entities;

const KINDS: EntityKind[] = [
  "issues",
  "runs",
  "attempts",
  "sessions",
  "worktrees",
  "dependencies",
  "gates",
  "approvals",
  "merge_candidates",
  "integration_targets",
  "operations",
];

/** Dependencies are keyed by dependency_id; everything else by id. */
function keyOf(kind: EntityKind, e: Record<string, unknown>): string {
  return String(kind === "dependencies" ? e.dependency_id : e.id);
}

/** Version used for regression checks; gates carry membership_version. */
function versionOf(kind: EntityKind, e: Record<string, unknown>): string | null {
  const v = kind === "gates" ? e.membership_version : e.version;
  return isDec(v) ? v : null;
}

export interface ProjectState {
  projectId: string;
  project: ProjectView;
  projectVersion: string;
  graphVersion: string;
  runtimeEpoch: string;
  databaseGeneration: string;
  generatedAt: string;
  freshness: SnapshotFreshness;
  /** Cursor of the last event (or snapshot) fully applied. */
  cursor: string;
  entities: Entities;
}

export type ApplyResult =
  | { kind: "applied"; state: ProjectState }
  | { kind: "duplicate" }
  | { kind: "gap"; reason: string }
  | { kind: "incompatible"; reason: string };

function emptyEntities(): Entities {
  const e = {} as Entities;
  for (const k of KINDS) (e as unknown as Record<string, unknown>)[k] = {};
  return e;
}

export function fromSnapshot(s: ProjectSnapshot): ProjectState {
  const entities = emptyEntities();
  for (const k of KINDS) {
    const list = (s.entities as unknown as Record<string, Record<string, unknown>[] | undefined>)[k] ?? [];
    const into = entities[k] as unknown as Record<string, unknown>;
    for (const e of list) into[keyOf(k, e)] = e;
  }
  return {
    projectId: s.project_id,
    project: s.project,
    projectVersion: s.project_version,
    graphVersion: s.graph_version,
    runtimeEpoch: s.runtime_epoch,
    databaseGeneration: s.database_generation,
    generatedAt: s.generated_at,
    freshness: s.freshness,
    cursor: s.snapshot_cursor,
    entities,
  };
}

/**
 * Apply one project.change event atomically (02 §15.2). The state is never
 * partially mutated: either a new state is returned or nothing changes.
 */
export function applyEvent(state: ProjectState, ev: ProjectEvent, cursor: string): ApplyResult {
  if (ev.schema_version !== "1") return { kind: "incompatible", reason: `schema_version ${String(ev.schema_version)}` };
  if (ev.project_id !== state.projectId) return { kind: "gap", reason: "PROJECT_MISMATCH" };
  if (!isDec(ev.project_version) || !isDec(ev.previous_project_version) || !isDec(ev.seq)) {
    return { kind: "gap", reason: "MALFORMED_EVENT" };
  }
  if (cmpDec(ev.project_version, state.projectVersion) <= 0) return { kind: "duplicate" };
  if (ev.previous_project_version !== state.projectVersion) return { kind: "gap", reason: "PROJECT_VERSION_GAP" };
  const changes = ev.payload?.changes;
  if (!changes || typeof changes !== "object") return { kind: "gap", reason: "MISSING_CHANGES" };

  const entities = { ...state.entities };
  const touched = new Set<EntityKind>();
  const table = (k: EntityKind) => {
    if (!touched.has(k)) {
      touched.add(k);
      (entities as unknown as Record<string, unknown>)[k] = { ...state.entities[k] };
    }
    return entities[k] as unknown as Record<string, Record<string, unknown>>;
  };
  let project = state.project;
  const upserts = (changes.upserts ?? {}) as Record<string, Record<string, unknown>[] | undefined>;
  for (const [kind, list] of Object.entries(upserts)) {
    if (!list) continue;
    if (kind === "projects") {
      for (const p of list) if (p.id === state.projectId) project = p as unknown as ProjectView;
      continue;
    }
    if (!KINDS.includes(kind as EntityKind)) continue; // unknown kind from a newer runtime
    const k = kind as EntityKind;
    for (const e of list) {
      const id = keyOf(k, e);
      const t = table(k);
      const prev = t[id];
      const pv = prev ? versionOf(k, prev) : null;
      const nv = versionOf(k, e);
      if (pv && nv && cmpDec(nv, pv) < 0) continue; // never regress an aggregate
      t[id] = e;
    }
  }
  for (const [kind, ids] of Object.entries(changes.deletes ?? {})) {
    if (!KINDS.includes(kind as EntityKind) || !Array.isArray(ids)) continue;
    const t = table(kind as EntityKind);
    for (const id of ids) delete t[id];
  }
  return {
    kind: "applied",
    state: {
      ...state,
      project,
      projectVersion: ev.project_version,
      graphVersion: project.graph_version ?? state.graphVersion,
      cursor,
      entities,
    },
  };
}

type Listener = () => void;

/** Observable holder for useSyncExternalStore (02 §14.1). */
export class Observable<T> {
  private listeners = new Set<Listener>();
  constructor(private value: T) {}
  get = (): T => this.value;
  set(v: T): void {
    if (Object.is(v, this.value)) return;
    this.value = v;
    for (const fn of this.listeners) fn();
  }
  subscribe = (fn: Listener): (() => void) => {
    this.listeners.add(fn);
    return () => this.listeners.delete(fn);
  };
}
