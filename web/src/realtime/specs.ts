import type { CatalogEvent, CatalogSnapshot, ProjectEvent, ProjectSnapshot, ProjectSummary } from "../api/contract";
import { ApiError, get } from "../api/http";
import { cmpDec, incDec, isDec } from "../store/decimal";
import { applyEvent, fromSnapshot, type ProjectState } from "../store/projectStore";
import type { StepResult, StreamSpec } from "./stream";

const enc = encodeURIComponent;

function mismatch(v: unknown): ApiError {
  return new ApiError(0, { code: "PROTOCOL_MISMATCH", message: `unsupported schema_version ${String(v)}`, retryable: false });
}

export function projectSpec(projectId: string): StreamSpec<ProjectState> {
  return {
    async load(signal) {
      const snap = await get<ProjectSnapshot>(`/v1/projects/${enc(projectId)}/snapshot`, signal);
      if (snap.schema_version !== "1") throw mismatch(snap.schema_version);
      return { state: fromSnapshot(snap), cursor: snap.snapshot_cursor };
    },
    eventsUrl: (cursor) => `/v1/projects/${enc(projectId)}/events?after=${enc(cursor)}`,
    changeEvent: "project.change",
    apply: (state, data, cursor) => applyEvent(state, data as ProjectEvent, cursor),
  };
}

export interface CatalogState {
  catalogVersion: string;
  projects: Record<string, ProjectSummary>;
}

export function applyCatalogEvent(state: CatalogState, ev: CatalogEvent): StepResult<CatalogState> {
  if (ev.schema_version !== "1") return { kind: "incompatible", reason: "catalog schema" };
  if (!isDec(ev.catalog_version)) return { kind: "gap", reason: "MALFORMED_EVENT" };
  if (cmpDec(ev.catalog_version, state.catalogVersion) <= 0) return { kind: "duplicate" };
  // The catalog stream is unfiltered and the runtime bumps catalog_version by
  // exactly one per event, so a jump means a missed project summary.
  if (ev.catalog_version !== incDec(state.catalogVersion)) return { kind: "gap", reason: "CATALOG_VERSION_GAP" };
  const p = ev.payload?.project;
  if (!p?.id || !isDec(p.version)) return { kind: "gap", reason: "MISSING_PROJECT" };
  const prev = state.projects[p.id];
  const projects = { ...state.projects };
  if (!prev || cmpDec(p.version, prev.version) >= 0) projects[p.id] = p;
  return { kind: "applied", state: { catalogVersion: ev.catalog_version, projects } };
}

export const catalogSpec: StreamSpec<CatalogState> = {
  async load(signal) {
    const snap = await get<CatalogSnapshot>("/v1/catalog/snapshot", signal);
    if (snap.schema_version !== "1") throw mismatch(snap.schema_version);
    const projects: Record<string, ProjectSummary> = {};
    for (const p of snap.projects) projects[p.id] = p;
    return { state: { catalogVersion: snap.catalog_version, projects }, cursor: snap.snapshot_cursor };
  },
  eventsUrl: (cursor) => `/v1/catalog/events?after=${enc(cursor)}`,
  changeEvent: "catalog.change",
  apply: (state, data) => applyCatalogEvent(state, data as CatalogEvent),
};
