import { useMemo, useState } from "react";
import type { ProjectSummary } from "../../api/contract";
import { navigate, projectPath } from "../../router";
import { stateLabel } from "../../status";
import { Badge, relative } from "../../components/ui";

type Filter = "all" | "running" | "attention" | "idle" | "archived";

const FILTERS: { key: Filter; label: string }[] = [
  { key: "all", label: "전체" },
  { key: "running", label: "실행 중" },
  { key: "attention", label: "확인 필요" },
  { key: "idle", label: "유휴·비활성" },
  { key: "archived", label: "보관·과거" },
];

function matches(p: ProjectSummary, f: Filter): boolean {
  const archived = p.registry_state === "ARCHIVED" || p.registry_state === "MISSING";
  switch (f) {
    case "all":
      return true;
    case "archived":
      return archived;
    case "running":
      return !archived && p.running > 0;
    case "attention":
      return !archived && (p.approval_pending > 0 || p.blocked > 0);
    case "idle":
      return !archived && p.running === 0;
  }
}

/** Sort: running first, then attention, then most recent activity (02 §5.2). */
export function sortProjects(list: ProjectSummary[]): ProjectSummary[] {
  const rank = (p: ProjectSummary) => (p.registry_state === "ARCHIVED" ? 3 : p.running > 0 ? 0 : p.approval_pending + p.blocked > 0 ? 1 : 2);
  return [...list].sort((a, b) => rank(a) - rank(b) || (b.last_activity_at ?? "").localeCompare(a.last_activity_at ?? "") || a.display_name.localeCompare(b.display_name));
}

export function ProjectNavigator({ projects, currentId }: { projects: ProjectSummary[]; currentId: string | null }) {
  const [q, setQ] = useState("");
  const [filter, setFilter] = useState<Filter>("all");
  const sorted = useMemo(() => sortProjects(projects), [projects]);
  const shown = sorted.filter((p) => matches(p, filter) && (q === "" || (p.display_name + " " + p.root_path).toLowerCase().includes(q.toLowerCase())));
  return (
    <nav className="side" aria-label="프로젝트 목록" data-testid="project-nav">
      <div className="side-head">
        <h2>프로젝트</h2>
        <span className="muted">{projects.length}</span>
      </div>
      <label className="field">
        <span className="sr-only">이름·경로로 프로젝트 검색</span>
        <input type="search" value={q} onChange={(e) => setQ(e.target.value)} placeholder="이름·경로 검색" />
      </label>
      <div role="group" aria-label="프로젝트 필터" className="chips">
        {FILTERS.map((f) => (
          <button key={f.key} type="button" className="chip" aria-pressed={filter === f.key} onClick={() => setFilter(f.key)}>
            {f.label} <span className="muted">{sorted.filter((p) => matches(p, f.key)).length}</span>
          </button>
        ))}
      </div>
      <ul className="side-list">
        {shown.map((p) => {
          const act = stateLabel(p.activation);
          const reg = p.registry_state !== "PRESENT" ? stateLabel(p.registry_state) : null;
          const stats = [p.running ? `실행 ${p.running}` : null, p.approval_pending ? `승인 ${p.approval_pending}` : null, p.blocked ? `차단 ${p.blocked}` : null, p.integration_pending ? `통합 ${p.integration_pending}` : null].filter(Boolean).join(" · ");
          return (
            <li key={p.id}>
              <a
                href={projectPath(p.id)}
                className="proj"
                aria-current={p.id === currentId ? "page" : undefined}
                onClick={(e) => {
                  if (e.metaKey || e.ctrlKey || e.shiftKey || e.button !== 0) return;
                  e.preventDefault();
                  navigate(projectPath(p.id));
                }}
              >
                <span className="proj-row">
                  <span className="proj-name ell">{p.display_name}</span>
                  <Badge tone={reg ? reg.tone : act.tone}>{reg ? reg.label : act.label}</Badge>
                </span>
                <span className="mono ell muted small">{p.root_path}</span>
                <span className="proj-row small">
                  <span className="ell">{stats || (p.latest_run_state ? `최근 Run ${stateLabel(p.latest_run_state).label}` : "기록 없음")}</span>
                  <span className="muted">{relative(p.last_activity_at)}</span>
                </span>
              </a>
            </li>
          );
        })}
      </ul>
      {shown.length === 0 && <p className="empty">{projects.length === 0 ? "등록된 프로젝트가 없습니다. 저장소에서 samsara init 으로 등록하세요." : "조건에 맞는 프로젝트가 없습니다."}</p>}
      <p className="side-foot">목록은 런타임 프로젝트 카탈로그 기준입니다. 열람만으로 프로젝트를 활성화하지 않습니다.</p>
    </nav>
  );
}
