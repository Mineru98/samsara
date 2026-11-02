import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import type { PlanReviseResult, RunPlan } from "../api/contract";
import { get } from "../api/http";
import { useCommand } from "../hooks";
import { Mono } from "../components/ui";
import { CommandError } from "./CommandButton";

const enc = encodeURIComponent;

/**
 * Plan revise for a PAUSED run (CLI `plan revise`, 01 §12.4). The plan, run
 * version and generation are fixed when the panel loads them; the server
 * refuses the revision if the run moved on since (VERSION_MISMATCH), and the
 * user reloads to edit the latest plan.
 */
export function RevisePlanPanel({ runId, canCommand, offlineReason, onClose }: { runId: string; canCommand: boolean; offlineReason: string; onClose: () => void }) {
  const base = useQuery({
    queryKey: ["run-plan", runId],
    queryFn: ({ signal }) => get<RunPlan>(`/v1/runs/${enc(runId)}/plan`, signal),
    staleTime: Infinity,
    gcTime: 0,
    refetchOnWindowFocus: false,
  });
  const { state, run, reset } = useCommand();
  const [text, setText] = useState("");
  const [parseError, setParseError] = useState<string | null>(null);
  useEffect(() => {
    if (base.data) setText(JSON.stringify(base.data.plan, null, 2));
  }, [base.data]);

  const reload = async () => {
    reset();
    setParseError(null);
    // Reset the editor even when the refetched plan is unchanged: structural
    // sharing keeps the same data reference, so the effect above won't run.
    const r = await base.refetch();
    if (r.data) setText(JSON.stringify(r.data.plan, null, 2));
  };
  const submit = async () => {
    if (!base.data) return;
    let plan: unknown;
    try {
      plan = JSON.parse(text);
    } catch (e) {
      setParseError((e as Error).message);
      return;
    }
    setParseError(null);
    await run(`/v1/runs/${enc(runId)}/revise`, {
      ifMatch: `${runId}:v${base.data.version}`,
      body: { expected_run_generation: base.data.run_generation, plan },
    });
  };
  const result = state.phase === "accepted" ? (state.result as PlanReviseResult) : null;
  const stale = state.phase === "failed" && state.error?.code === "VERSION_MISMATCH";

  return (
    <section className="card revise" aria-label="계획 수정" data-testid="revise-plan">
      <div className="row">
        <h2 className="small">계획 수정</h2>
        {base.data && (
          <span className="muted small" data-testid="revise-base">
            기준 세대 {base.data.run_generation} · graph {base.data.graph_version} · run v{base.data.version} · <Mono>{base.data.plan_digest}</Mono>
          </span>
        )}
        <div className="row end">
          <button type="button" className="btn ghost" onClick={() => void reload()}>
            다시 불러오기
          </button>
          <button type="button" className="btn ghost" onClick={onClose}>
            닫기
          </button>
        </div>
      </div>
      {base.isLoading && <p className="muted small">현재 계획을 불러오는 중…</p>}
      {base.error && <p className="small bad">계획을 불러오지 못했습니다: {(base.error as Error).message}</p>}
      {base.data && (
        <>
          <p className="small muted">전체 계획 JSON을 수정합니다. 이슈 제거는 거부되고, 실행 의미가 바뀌면 세대가 올라가며 재개 전에 PLAN 승인을 다시 받습니다.</p>
          <textarea className="plan-editor" aria-label="계획 JSON" spellCheck={false} value={text} onChange={(e) => setText(e.target.value)} />
          <div className="row">
            <button type="button" className="btn" disabled={!canCommand || state.phase === "pending"} title={!canCommand ? offlineReason : undefined} onClick={() => void submit()}>
              수정 제출
            </button>
            {!canCommand && <span className="small muted">{offlineReason}</span>}
            {state.phase === "pending" && <span className="small">요청 보내는 중…</span>}
            {state.phase === "unknown" && <span className="small cmd-unknown">결과를 확인하지 못했습니다. 같은 내용으로 다시 제출해도 중복 적용되지 않습니다.</span>}
          </div>
          {parseError && <p className="small bad">JSON 형식 오류: {parseError}</p>}
          {state.phase === "failed" && state.error && (
            <div>
              {stale && <p className="small cmd-failed">계획을 불러온 뒤 run이 바뀌어 거절되었습니다(오래된 세대·버전). 다시 불러와 최신 계획에서 수정하세요.</p>}
              <CommandError error={state.error} />
            </div>
          )}
          {result && (
            <dl className="kv small cmd-accepted" data-testid="revise-result">
              <dt>결과</dt>
              <dd>
                {result.change_kind} · 세대 {result.run_generation} · graph {result.graph_version} · 상태 {result.run_state}
              </dd>
              <dt>plan digest</dt>
              <dd>
                <Mono>{result.plan_digest}</Mono>
              </dd>
              <dt>영향 이슈</dt>
              <dd>{result.affected_issue_ids.length}개</dd>
              {result.plan_approval_id && (
                <>
                  <dt>PLAN 승인</dt>
                  <dd>
                    <Mono>{result.plan_approval_id}</Mono> 승인 후 재개할 수 있습니다.
                  </dd>
                </>
              )}
            </dl>
          )}
        </>
      )}
    </section>
  );
}
