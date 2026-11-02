import { useEffect, useRef } from "react";
import { useQuery } from "@tanstack/react-query";
import type { ApprovalView, IssueDetail } from "../../api/contract";
import { get } from "../../api/http";
import type { Entities } from "../../store/projectStore";
import { displayStatus, milestoneLabel, stateLabel } from "../../status";
import { Badge, Empty, Mono, Time, shortSha } from "../../components/ui";
import { CommandButton } from "../CommandButton";

const enc = encodeURIComponent;

export function Inspector({ entities, selected, canCommand, offlineReason, onClose }: { entities: Entities; selected: string; canCommand: boolean; offlineReason: string; onClose: () => void }) {
  const [kind, id] = [selected.slice(0, selected.indexOf(":")), selected.slice(selected.indexOf(":") + 1)];
  // The node or list row that opened this panel gets focus back on close,
  // and Escape closes it from anywhere (02 §20.1).
  const opener = useRef<HTMLElement | null>(null);
  const ref = useRef<HTMLElement>(null);
  if (opener.current === null && typeof document !== "undefined") opener.current = document.activeElement as HTMLElement | null;
  const close = useRef(() => {});
  close.current = () => {
    const o = opener.current;
    onClose();
    if (o && o.isConnected && o !== document.body) o.focus();
    // The Merge Queue item that opened the panel is re-rendered after close:
    // find it again by what it opens.
    else requestAnimationFrame(() => document.querySelector<HTMLElement>(`[data-opens="${CSS.escape(selected)}"]`)?.focus());
  };
  useEffect(() => {
    ref.current?.scrollIntoView?.({ block: "nearest" });
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape" && !e.defaultPrevented) close.current();
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, []);
  return (
    <aside ref={ref} className="inspector" aria-label="상세" data-testid="inspector">
      <div className="panel-head">
        <h2>{kind === "gate" ? "게이트 상세" : "이슈 상세"}</h2>
        <button type="button" className="btn ghost" onClick={() => close.current()} aria-label="상세 닫고 Merge Queue 보기">
          닫기
        </button>
      </div>
      {kind === "issue" && <IssueInspector entities={entities} id={id} canCommand={canCommand} offlineReason={offlineReason} />}
      {kind === "gate" && <GateInspector entities={entities} id={id} />}
    </aside>
  );
}

function IssueInspector({ entities, id, canCommand, offlineReason }: { entities: Entities; id: string; canCommand: boolean; offlineReason: string }) {
  const issue = entities.issues[id];
  // Detail is request-scoped data; its key includes the live version so it
  // refetches when the stream moves the issue forward (02 §14.1).
  const detail = useQuery({
    queryKey: ["issue", id, issue?.version],
    queryFn: ({ signal }) => get<IssueDetail>(`/v1/issues/${enc(id)}`, signal),
    enabled: !!issue,
  });
  if (!issue) return <Empty>이 이슈는 현재 snapshot에 없습니다.</Empty>;
  const s = displayStatus(issue.display_status);
  const run = issue.run_id ? entities.runs[issue.run_id] : undefined;
  const att = issue.current_attempt_id ? entities.attempts[issue.current_attempt_id] : undefined;
  const wt = att?.worktree_id ? entities.worktrees[att.worktree_id] : undefined;
  const sess = att?.session_id ? entities.sessions[att.session_id] : undefined;
  const approvals = [...new Set(issue.available_actions.filter((a) => a.action === "OPEN_APPROVAL" && a.resource_id).map((a) => a.resource_id!))];
  return (
    <div className="insp-body">
      <div className="insp-title">
        <Mono>{issue.client_key}</Mono>
        <Badge tone={s.tone}>{s.label}</Badge>
        <span className="muted small mono" data-testid="issue-version">
          v{issue.version}
        </span>
      </div>
      <h3 className="insp-h">{issue.title}</h3>
      <dl className="kv">
        <dt>실행</dt>
        <dd>{issue.execution_state ? stateLabel(issue.execution_state).label : "-"}</dd>
        <dt>통합</dt>
        <dd>{issue.integration_state ? stateLabel(issue.integration_state).label : "-"}</dd>
        <dt>수명</dt>
        <dd>{issue.lifecycle}</dd>
        <dt>검증 profile</dt>
        <dd className="mono">{issue.check_profile || "-"}</dd>
      </dl>

      <section>
        <h4>차단 이유 {issue.blocking_reasons.length > 0 && <span className="muted">{issue.blocking_reasons.length}</span>}</h4>
        {issue.blocking_reasons.length === 0 ? (
          <Empty>차단 이유가 없습니다.</Empty>
        ) : (
          <ul className="reasons">
            {issue.blocking_reasons.map((b, i) => (
              <li key={i}>
                <Mono>{b.code}</Mono> {b.message}
                {b.source_issue_id && entities.issues[b.source_issue_id] && (
                  <span className="muted small">
                    {" "}
                    · 선행 {entities.issues[b.source_issue_id].client_key}
                    {b.required_milestone ? ` ${milestoneLabel(b.required_milestone)} 필요` : ""}
                  </span>
                )}
              </li>
            ))}
          </ul>
        )}
      </section>

      {approvals.length > 0 && (
        <section>
          <h4>삼사라 승인</h4>
          {approvals.map((aid) => (
            <ApprovalCard key={aid} approval={entities.approvals[aid]} id={aid} canCommand={canCommand} offlineReason={offlineReason} />
          ))}
        </section>
      )}

      {issue.resume_required && (
        <section>
          <h4>수동 계속 필요</h4>
          <p className="small">
            이 실행은 자동으로 이어지지 않습니다. 터미널에서 worktree로 이동해 Claude를 실행한 뒤 <Mono>samsara session attach --issue {issue.id}</Mono> 로 연결하세요.
            {wt && (
              <>
                {" "}
                Worktree: <Mono>{wt.path}</Mono>
              </>
            )}
          </p>
        </section>
      )}

      <section>
        <h4>현재 실행</h4>
        {att ? (
          <dl className="kv">
            <dt>Attempt</dt>
            <dd className="mono ell">{att.id}</dd>
            <dt>상태</dt>
            <dd>
              {stateLabel(att.state).label}
              {att.state_reason && <span className="muted small mono"> {att.state_reason}</span>}
            </dd>
            <dt>Backend</dt>
            <dd>{att.backend_kind ?? "-"}</dd>
            <dt>Lease</dt>
            <dd>
              {att.lease_state ?? "-"}
              {att.lease_fence && <span className="muted small mono"> fence {att.lease_fence}</span>}
            </dd>
            <dt>세션</dt>
            <dd>{sess ? `${stateLabel(sess.liveness).label} · 입력 ${sess.input_state}` : "-"}</dd>
            <dt>Worktree</dt>
            <dd className="mono ell" title={wt?.path}>
              {wt ? wt.branch_ref.replace(/^refs\/heads\//, "") : "-"}
            </dd>
            <dt>Run</dt>
            <dd>{run ? `${stateLabel(run.state).label} · 세대 ${run.run_generation}` : "-"}</dd>
          </dl>
        ) : (
          <Empty>아직 시작한 실행이 없습니다.</Empty>
        )}
      </section>

      <section>
        <h4>통합 후보</h4>
        {detail.isLoading && <p className="muted small">불러오는 중…</p>}
        {detail.error && <p className="small bad">상세를 불러오지 못했습니다: {(detail.error as Error).message}</p>}
        {detail.data &&
          (detail.data.merge_candidates.length === 0 ? (
            <Empty>통합 후보가 없습니다.</Empty>
          ) : (
            <ul className="plain">
              {detail.data.merge_candidates.map((c) => (
                <li key={c.id}>
                  <Badge tone={stateLabel(c.state).tone}>{stateLabel(c.state).label}</Badge> <Mono>{c.target_ref}</Mono> · source <Mono>{shortSha(c.source_sha)}</Mono> · base <Mono>{shortSha(c.base_sha)}</Mono>
                </li>
              ))}
            </ul>
          ))}
      </section>

      {detail.data && detail.data.attempts.length > 1 && (
        <section>
          <h4>과거 실행</h4>
          <ul className="plain small">
            {detail.data.attempts.map((a) => (
              <li key={a.id}>
                <Mono>{a.id}</Mono> {stateLabel(a.state).label} · <Time at={a.updated_at} />
              </li>
            ))}
          </ul>
        </section>
      )}
    </div>
  );
}

export function ApprovalCard({ approval, id, canCommand, offlineReason, title }: { approval: ApprovalView | undefined; id: string; canCommand: boolean; offlineReason: string; title?: string }) {
  if (!approval) return <p className="small muted">승인 {id} 는 현재 snapshot 범위 밖입니다.</p>;
  const pending = approval.state === "PENDING";
  const disabled = !canCommand || !pending;
  const reason = !canCommand ? offlineReason : !pending ? "이미 처리된 승인입니다." : undefined;
  const path = `/v1/approvals/${enc(approval.id)}/decisions`;
  const ifMatch = `${approval.id}:v${approval.version}`;
  return (
    <div className="card" data-testid="approval-card">
      <div className="insp-title">
        <Badge tone={stateLabel(approval.state).tone}>{approval.kind} · {stateLabel(approval.state).label}</Badge>
        {title && <span className="small">{title}</span>}
        <span className="muted small">
          요청 <Time at={approval.requested_at} />
        </span>
      </div>
      <details>
        <summary className="small">승인 대상 내용 (digest {approval.subject_digest.slice(0, 12)})</summary>
        <pre className="subject">{JSON.stringify(approval.subject, null, 2)}</pre>
      </details>
      <p className="small muted">승인은 위 내용(digest)에만 묶입니다. 내용이 바뀌면 서버가 거부하고 최신 내용으로 다시 검토해야 합니다.</p>
      <div className="row">
        <CommandButton label="승인" path={path} ifMatch={ifMatch} body={{ decision: "APPROVE", subject_digest: approval.subject_digest }} disabled={disabled} disabledReason={reason} />
        <CommandButton label="반려" danger path={path} ifMatch={ifMatch} body={{ decision: "REJECT", subject_digest: approval.subject_digest }} disabled={disabled} />
      </div>
    </div>
  );
}

function GateInspector({ entities, id }: { entities: Entities; id: string }) {
  const g = entities.gates[id];
  if (!g) return <Empty>이 게이트는 현재 snapshot에 없습니다.</Empty>;
  const succ = entities.issues[g.successor_issue_id];
  return (
    <div className="insp-body">
      <div className="insp-title">
        <Badge tone={g.open ? "ok" : "wait"}>{g.open ? "열림" : "닫힘"}</Badge>
        <span className="small">{g.downstream_phase === "INTEGRATION" ? "통합 단계" : "실행 단계"} AND 조건</span>
      </div>
      <p className="small">
        후속 이슈 <Mono>{succ?.client_key ?? g.successor_issue_id}</Mono> 는 아래 조건이 모두 충족되어야 진행합니다.
      </p>
      <ul className="plain">
        {g.members.map((m) => (
          <li key={m.dependency_id}>
            <Badge tone={m.met ? "ok" : "wait"}>{m.met ? "충족" : "미충족"}</Badge> {entities.issues[m.issue_id]?.client_key ?? m.issue_id} · {milestoneLabel(m.required_milestone)}
          </li>
        ))}
      </ul>
      <p className="muted small mono">membership v{g.membership_version}</p>
    </div>
  );
}
