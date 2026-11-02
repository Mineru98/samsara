import type { RunView } from "../api/contract";
import { CommandButton } from "./CommandButton";

const enc = encodeURIComponent;

const COMMANDS: Record<string, { label: string; path: string }> = {
  START_RUN: { label: "실행 시작", path: "start" },
  PAUSE_RUN: { label: "일시 정지 요청", path: "pause" },
  RESUME_RUN: { label: "재개 요청", path: "resume" },
};

/**
 * Run commands exactly as the server lists them in available_actions
 * (03 §13): an action the server does not offer gets no button.
 */
export function RunControls({ run, canCommand, offlineReason, revising, onRevise }: { run: RunView; canCommand: boolean; offlineReason: string; revising: boolean; onRevise: () => void }) {
  const ifMatch = `${run.id}:v${run.version}`;
  return (
    <>
      {(run.available_actions ?? []).map((a) => {
        const reason = !canCommand ? offlineReason : !a.enabled ? a.reason_code : undefined;
        if (a.action === "REVISE_PLAN") {
          return (
            <button key={a.action} type="button" className="btn" aria-expanded={revising} disabled={!canCommand || !a.enabled} title={reason} onClick={onRevise}>
              계획 수정
            </button>
          );
        }
        const c = COMMANDS[a.action];
        if (!c) return null;
        return <CommandButton key={a.action} label={c.label} path={`/v1/runs/${enc(run.id)}/${c.path}`} ifMatch={ifMatch} disabled={!canCommand || !a.enabled} disabledReason={reason} />;
      })}
    </>
  );
}
