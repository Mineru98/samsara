import { useRef } from "react";
import type { CandidateView } from "../../api/contract";
import type { Entities } from "../../store/projectStore";
import { stateLabel } from "../../status";
import { Badge, Empty, Mono, shortSha } from "../../components/ui";
import { cmpDec } from "../../store/decimal";
import { CommandButton } from "../CommandButton";
import { ApprovalCard } from "../inspector/Inspector";

const enc = encodeURIComponent;
const TERMINAL = new Set(["MERGED", "CANCELLED", "FAILED"]);

/** Merge Queue per integration target (02 §11). Implementation done ≠ integrated. */
export function MergeQueuePanel({ entities, canCommand, offlineReason, onSelectIssue }: { entities: Entities; canCommand: boolean; offlineReason: string; onSelectIssue: (issueId: string) => void }) {
  const targets = Object.values(entities.integration_targets).sort((a, b) => a.target_ref.localeCompare(b.target_ref));
  const byTarget = new Map<string, CandidateView[]>();
  for (const c of Object.values(entities.merge_candidates)) {
    if (!byTarget.has(c.target_id)) byTarget.set(c.target_id, []);
    byTarget.get(c.target_id)!.push(c);
  }
  // Approvals bound to a run or candidate (e.g. PLAN) have no issue node to
  // open them from, so the default panel lists every pending approval. One
  // decided while the panel is open stays with its new state, so the user
  // sees the outcome instead of a card that silently vanishes.
  const shown = useRef(new Set<string>());
  const pending = Object.values(entities.approvals)
    .filter((a) => a.state === "PENDING" || shown.current.has(a.id))
    .sort((a, b) => a.requested_at.localeCompare(b.requested_at));
  for (const a of pending) shown.current.add(a.id);
  const pendingCount = pending.filter((a) => a.state === "PENDING").length;
  return (
    <aside className="inspector" aria-label="승인 대기와 Merge Queue" data-testid="merge-queue">
      {pending.length > 0 && (
        <section className="queue" aria-label="승인 대기" data-testid="pending-approvals">
          <h2 className="queue-h">승인 대기 {pendingCount}</h2>
          {pending.map((a) => (
            <ApprovalCard
              key={a.id}
              id={a.id}
              approval={a}
              canCommand={canCommand}
              offlineReason={offlineReason}
              title={a.issue_id ? (entities.issues[a.issue_id]?.title ?? a.issue_id) : a.kind === "PLAN" ? "실행 계획 시작 승인" : undefined}
            />
          ))}
        </section>
      )}
      <div className="panel-head">
        <h2>Merge Queue</h2>
        <span className="muted small">대상 {targets.length}</span>
      </div>
      {targets.length === 0 && <Empty>통합 대상이 아직 없습니다. 결과가 제출되면 대상 브랜치별 Queue가 생깁니다.</Empty>}
      {targets.map((t) => {
        const list = (byTarget.get(t.id) ?? []).sort((a, b) => Number(TERMINAL.has(a.state)) - Number(TERMINAL.has(b.state)) || cmpDec(a.queue_position, b.queue_position));
        return (
          <section key={t.id} className="queue">
            <h3 className="queue-h">
              <Mono>{t.target_ref.replace(/^refs\/heads\//, "")}</Mono> <span className="muted small">{t.authority}</span>
            </h3>
            {list.length === 0 ? (
              <Empty>대기 중인 후보가 없습니다.</Empty>
            ) : (
              <ol className="qlist">
                {list.map((c) => {
                  const s = stateLabel(c.state);
                  const issue = entities.issues[c.issue_id];
                  const ifMatch = `${c.id}:v${c.version}`;
                  const open = !TERMINAL.has(c.state);
                  return (
                    <li key={c.id} className="qitem">
                      <div className="row">
                        <button type="button" className="linklike ell" data-opens={`issue:${c.issue_id}`} onClick={() => onSelectIssue(c.issue_id)}>
                          {issue ? `${issue.client_key} ${issue.title}` : c.issue_id}
                        </button>
                        <Badge tone={s.tone}>{s.label}</Badge>
                      </div>
                      <div className="small muted">
                        source <Mono>{shortSha(c.source_sha)}</Mono> · 검증 기준 <Mono>{shortSha(c.base_sha)}</Mono>
                        {c.candidate_sha && (
                          <>
                            {" "}
                            · 결과 <Mono>{shortSha(c.candidate_sha)}</Mono>
                          </>
                        )}
                        {c.enqueued ? " · 대기열 등록됨" : ""}
                      </div>
                      {c.state === "STALE_BASE" && <div className="small warn">대상 커밋이 바뀌어 재검증이 필요합니다.</div>}
                      {c.conflict && <div className="small bad">충돌 파일 {c.conflict.files.length}개</div>}
                      {c.blocking_reasons.length > 0 && <div className="small">{c.blocking_reasons[0].message}</div>}
                      {open && (
                        <div className="row">
                          {!c.enqueued && (
                            <CommandButton label="통합 요청" path={`/v1/merge-candidates/${enc(c.id)}/enqueue`} ifMatch={ifMatch} disabled={!canCommand} disabledReason={offlineReason} />
                          )}
                          <CommandButton label="후보 취소" danger path={`/v1/merge-candidates/${enc(c.id)}/cancel`} ifMatch={ifMatch} disabled={!canCommand} />
                        </div>
                      )}
                    </li>
                  );
                })}
              </ol>
            )}
          </section>
        );
      })}
    </aside>
  );
}
