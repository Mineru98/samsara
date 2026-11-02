import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import type { GitHubHistory, Page } from "../../api/contract";
import { get } from "../../api/http";
import type { Entities } from "../../store/projectStore";
import { stateLabel } from "../../status";
import { Badge, Empty, Mono, SafeLink, Time } from "../../components/ui";

export type DockTab = "sessions" | "events" | "github" | "worktrees";
const TABS: { key: DockTab; label: string }[] = [
  { key: "sessions", label: "세션" },
  { key: "events", label: "이벤트" },
  { key: "github", label: "GitHub" },
  { key: "worktrees", label: "Worktree" },
];
const enc = encodeURIComponent;

export function ActivityDock({ projectId, projectVersion, entities, tab, onTab }: { projectId: string; projectVersion: string; entities: Entities; tab: DockTab; onTab: (t: DockTab) => void }) {
  return (
    <section className="dock" aria-label="실행 기록" data-testid="activity-dock">
      <div role="tablist" className="tabs" aria-label="기록 종류">
        {TABS.map((t) => (
          <button
            key={t.key}
            role="tab"
            type="button"
            id={`tab-${t.key}`}
            aria-selected={tab === t.key}
            aria-controls={`panel-${t.key}`}
            tabIndex={tab === t.key ? 0 : -1}
            className="tab"
            onClick={() => onTab(t.key)}
            onKeyDown={(e) => {
              const i = TABS.findIndex((x) => x.key === tab);
              if (e.key === "ArrowRight") onTab(TABS[(i + 1) % TABS.length].key);
              if (e.key === "ArrowLeft") onTab(TABS[(i + TABS.length - 1) % TABS.length].key);
            }}
          >
            {t.label}
          </button>
        ))}
      </div>
      <div role="tabpanel" id={`panel-${tab}`} aria-labelledby={`tab-${tab}`} className="dock-body">
        {tab === "sessions" && <Sessions entities={entities} />}
        {tab === "events" && <Events projectId={projectId} projectVersion={projectVersion} />}
        {tab === "github" && <GitHub projectId={projectId} />}
        {tab === "worktrees" && <Worktrees entities={entities} />}
      </div>
    </section>
  );
}

function Sessions({ entities }: { entities: Entities }) {
  const list = Object.values(entities.sessions);
  if (list.length === 0) return <Empty>관찰 중인 세션이 없습니다.</Empty>;
  const issueOf = (attemptId?: string) => (attemptId ? entities.issues[entities.attempts[attemptId]?.issue_id ?? ""] : undefined);
  return (
    <table className="tbl">
      <thead>
        <tr>
          <th>이슈</th>
          <th>Backend</th>
          <th>생존</th>
          <th>입력</th>
          <th>관리</th>
          <th>마지막 관찰</th>
        </tr>
      </thead>
      <tbody>
        {list.map((s) => {
          const l = stateLabel(s.liveness);
          return (
            <tr key={s.id}>
              <td>{issueOf(s.attempt_id)?.client_key ?? <span className="muted">-</span>}</td>
              <td>{s.backend?.kind ?? s.source}</td>
              <td>
                <Badge tone={l.tone}>{l.label}</Badge>
              </td>
              <td>{s.input_state}</td>
              <td>{s.managed ? "managed" : "standalone"}</td>
              <td>
                <Time at={s.observed_at} />
              </td>
            </tr>
          );
        })}
      </tbody>
    </table>
  );
}

interface HistoryItem {
  id: string;
  actor: string;
  action: string;
  subject_type: string;
  subject_id: string;
  decision?: string;
  reason_code?: string;
  created_at: string;
}

function Events({ projectId, projectVersion }: { projectId: string; projectVersion: string }) {
  const q = useInfiniteQuery({
    queryKey: ["history", projectId, projectVersion],
    queryFn: ({ pageParam, signal }) => get<Page>(`/v1/projects/${enc(projectId)}/history?limit=50${pageParam ? `&cursor=${enc(pageParam)}` : ""}`, signal),
    initialPageParam: "",
    getNextPageParam: (p) => p.next_cursor || undefined,
    placeholderData: (prev) => prev,
  });
  if (q.isLoading) return <p className="muted small">불러오는 중…</p>;
  if (q.error) return <p className="bad small">이력을 불러오지 못했습니다: {(q.error as Error).message}</p>;
  const items = (q.data?.pages ?? []).flatMap((p) => p.items as unknown as HistoryItem[]);
  if (items.length === 0) return <Empty>기록된 이벤트가 없습니다.</Empty>;
  return (
    <>
      <table className="tbl">
        <thead>
          <tr>
            <th>시각</th>
            <th>행동</th>
            <th>대상</th>
            <th>결정</th>
            <th>주체</th>
          </tr>
        </thead>
        <tbody>
          {items.map((h) => (
            <tr key={h.id}>
              <td>
                <Time at={h.created_at} />
              </td>
              <td className="mono">{h.action}</td>
              <td className="mono ell">
                {h.subject_type} {h.subject_id}
              </td>
              <td>
                {h.decision ?? ""} {h.reason_code && <span className="muted mono">{h.reason_code}</span>}
              </td>
              <td className="mono">{h.actor}</td>
            </tr>
          ))}
        </tbody>
      </table>
      {q.hasNextPage && (
        <button type="button" className="btn ghost" onClick={() => q.fetchNextPage()} disabled={q.isFetchingNextPage}>
          더 보기
        </button>
      )}
    </>
  );
}

function GitHub({ projectId }: { projectId: string }) {
  const q = useQuery({ queryKey: ["github", projectId], queryFn: ({ signal }) => get<GitHubHistory>(`/v1/projects/${enc(projectId)}/github-history`, signal), refetchInterval: 60_000 });
  if (q.isLoading) return <p className="muted small">불러오는 중…</p>;
  if (q.error) return <p className="bad small">GitHub 이력을 불러오지 못했습니다: {(q.error as Error).message}</p>;
  const h = q.data!;
  const f = stateLabel(h.freshness);
  return (
    <>
      <p className="small">
        <Badge tone={f.tone}>{f.label}</Badge> <Mono>{h.repository || "저장소 미설정"}</Mono> · 동기화 {h.sync} · 마지막 성공 <Time at={h.last_synced_at} />
        {h.error && <span className="warn"> · {h.error}</span>}
      </p>
      {h.items.length === 0 ? (
        <Empty>연결된 GitHub 이력이 없습니다.</Empty>
      ) : (
        <table className="tbl">
          <thead>
            <tr>
              <th>종류</th>
              <th>제목</th>
              <th>상태</th>
              <th>관찰</th>
            </tr>
          </thead>
          <tbody>
            {h.items.map((i) => (
              <tr key={i.id}>
                <td>{i.kind}</td>
                <td>
                  <SafeLink href={i.url}>{i.title || i.external_key}</SafeLink>
                </td>
                <td>{i.state}</td>
                <td>
                  <Time at={i.observed_at} />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </>
  );
}

function Worktrees({ entities }: { entities: Entities }) {
  const list = Object.values(entities.worktrees);
  if (list.length === 0) return <Empty>Worktree가 없습니다.</Empty>;
  return (
    <table className="tbl">
      <thead>
        <tr>
          <th>브랜치</th>
          <th>상태</th>
          <th>소유</th>
          <th>기준</th>
          <th>경로</th>
        </tr>
      </thead>
      <tbody>
        {list.map((w) => {
          const s = stateLabel(w.state);
          return (
            <tr key={w.id}>
              <td className="mono">{w.branch_ref.replace(/^refs\/heads\//, "")}</td>
              <td>
                <Badge tone={s.tone}>{s.label}</Badge>
              </td>
              <td>{w.ownership}</td>
              <td className="mono">{w.base_sha.slice(0, 9)}</td>
              <td className="mono ell" title={w.path}>
                {w.path}
              </td>
            </tr>
          );
        })}
      </tbody>
    </table>
  );
}
