import { useCallback, useEffect } from "react";
import type { ProjectSummary } from "../api/contract";
import { useMediaQuery, useStream } from "../hooks";
import { projectSpec } from "../realtime/specs";
import { setParams, useRoute } from "../router";
import { stateLabel } from "../status";
import { Badge, Mono, relative } from "../components/ui";
import { ConnectionBanner } from "./ConnectionBanner";
import { CommandButton } from "./CommandButton";
import { ActivityDock, type DockTab } from "./dock/ActivityDock";
import { IssueGraph } from "./graph/IssueGraph";
import { warmLayoutWorker } from "./graph/layout";
import { defaultRunId, issuesOf } from "./graph/model";
import { Inspector } from "./inspector/Inspector";
import { IssueTable } from "./IssueTable";
import { MergeQueuePanel } from "./queue/MergeQueuePanel";

type View = "issues" | "worktrees" | "list";
const VIEWS: { key: View; label: string }[] = [
  { key: "issues", label: "이슈 DAG" },
  { key: "worktrees", label: "Worktree 묶음" },
  { key: "list", label: "목록" },
];
const enc = encodeURIComponent;

export function ProjectPage({ projectId, summary }: { projectId: string; summary: ProjectSummary | undefined }) {
  const { view: stream, controller } = useStream(projectId, projectSpec);
  const { params } = useRoute();
  // Phones open on the list; the DAG stays one tap away (02 §19).
  const defaultView: View = useMediaQuery("(max-width: 767px)") ? "list" : "issues";
  const viewMode = (params.get("view") as View) || defaultView;
  const selected = params.get("sel");
  const tab = (params.get("tab") as DockTab) || "sessions";
  const select = useCallback((id: string | null) => setParams({ sel: id }, { replace: true }), []);
  useEffect(warmLayoutWorker, []);

  if (!stream || (stream.status === "connecting" && !stream.data)) {
    return <div className="center muted">프로젝트를 불러오는 중…</div>;
  }
  if (stream.status === "error" || !stream.data) {
    return (
      <div className="center" role="alert">
        <h1>프로젝트를 열 수 없습니다</h1>
        <p>
          <Mono>{projectId}</Mono> — {stream.error?.code} {stream.error?.message}
        </p>
        <p>
          <a href="/">프로젝트 목록으로</a>
        </p>
      </div>
    );
  }
  const data = stream.data;
  const e = data.entities;
  const runId = params.get("run") || defaultRunId(e);
  const run = runId ? e.runs[runId] : undefined;
  const runs = Object.values(e.runs).sort((a, b) => b.created_at.localeCompare(a.created_at));
  const live = stream.status === "live";
  const canCommand = live;
  const offlineReason = live ? "" : "실시간 연결이 최신이 아니어서 명령을 막았습니다.";
  const act = stateLabel(data.project.activation);
  const reg = stateLabel(data.project.registry_state);
  const issueCount = issuesOf(e, runId).length;

  return (
    <main className="project" aria-labelledby="project-title">
      <ConnectionBanner status={stream.status} lastAppliedAt={stream.lastAppliedAt} reason={stream.resyncReason} error={stream.error?.message ?? null} />
      <section className="summary" aria-label="프로젝트 요약">
        <div className="summary-top">
          <div className="min0">
            <div className="row">
              <h1 id="project-title">{data.project.display_name}</h1>
              <Badge tone={act.tone}>{act.label}</Badge>
              <Badge tone={reg.tone}>{reg.label}</Badge>
            </div>
            <div className="mono small muted ell">{data.project.root_path}</div>
          </div>
          <div className="row wrap end">
            <label className="row small">
              Run
              <select value={runId ?? ""} onChange={(ev) => setParams({ run: ev.target.value || null, sel: null })}>
                {runs.length === 0 && <option value="">Run 없음</option>}
                {runs.map((r) => (
                  <option key={r.id} value={r.id}>
                    {r.id.slice(0, 14)} · {stateLabel(r.state).label} · 세대 {r.run_generation}
                  </option>
                ))}
              </select>
            </label>
            {run && run.state === "ACTIVE" && (
              <CommandButton label="일시 정지 요청" path={`/v1/runs/${enc(run.id)}/pause`} ifMatch={`${run.id}:v${run.version}`} disabled={!canCommand} disabledReason={offlineReason} />
            )}
            {run && run.state === "PAUSED" && (
              <CommandButton label="재개 요청" path={`/v1/runs/${enc(run.id)}/resume`} ifMatch={`${run.id}:v${run.version}`} disabled={!canCommand} disabledReason={offlineReason} />
            )}
          </div>
        </div>
        <div className="metrics" data-testid="metrics">
          <Metric label="실행 중" value={summary?.running} tone="run" />
          <Metric label="승인 대기" value={summary?.approval_pending} tone="approval" />
          <Metric label="차단됨" value={summary?.blocked} tone="wait" />
          <Metric label="통합 대기" value={summary?.integration_pending} tone="idle" />
          <div className="metric-note small muted">
            범위: {run ? `Run ${stateLabel(run.state).label}` : "전체"} · 이슈 {issueCount}개 · <span data-testid="project-version">project_version {data.projectVersion}</span> · graph {data.graphVersion} · snapshot {relative(data.generatedAt)}
            <br />
            freshness: runtime {data.freshness.runtime} · backend {data.freshness.backend_observation} · GitHub {data.freshness.github}
          </div>
        </div>
      </section>
      <div className="toolbar" role="toolbar" aria-label="그래프 보기">
        <div role="group" aria-label="보기 방식" className="chips">
          {VIEWS.map((v) => (
            <button key={v.key} type="button" className="chip" aria-pressed={viewMode === v.key} onClick={() => setParams({ view: v.key === defaultView ? null : v.key }, { replace: true })}>
              {v.label}
            </button>
          ))}
        </div>
        <button type="button" className="btn ghost" onClick={() => controller?.resync("USER_REQUEST")}>
          다시 불러오기
        </button>
      </div>
      <div className="workspace">
        <div className="canvas">
          {viewMode === "list" ? (
            <IssueTable entities={e} runId={runId} selected={selected} onSelect={select} />
          ) : (
            <IssueGraph entities={e} runId={runId} mode={viewMode === "worktrees" ? "worktrees" : "issues"} selected={selected} onSelect={select} />
          )}
        </div>
        {selected ? (
          <Inspector key={selected} entities={e} selected={selected} canCommand={canCommand} offlineReason={offlineReason} onClose={() => select(null)} />
        ) : (
          <MergeQueuePanel entities={e} canCommand={canCommand} offlineReason={offlineReason} onSelectIssue={(id) => select(`issue:${id}`)} />
        )}
      </div>
      <ActivityDock projectId={projectId} projectVersion={data.projectVersion} entities={e} tab={tab} onTab={(t) => setParams({ tab: t === "sessions" ? null : t }, { replace: true })} />
    </main>
  );
}

function Metric({ label, value, tone }: { label: string; value: number | undefined; tone: string }) {
  return (
    <div className="metric">
      <span className="small muted">
        <span className={`dot tone-${tone}`} aria-hidden="true" /> {label}
      </span>
      <strong>{value ?? "-"}</strong>
    </div>
  );
}
