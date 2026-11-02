import type { ReactNode } from "react";
import { useCommand } from "../hooks";

const PHASE_TEXT = {
  idle: "",
  pending: "요청 보내는 중…",
  accepted: "접수됨 — 결과는 실시간 상태로 반영됩니다.",
  failed: "",
  unknown: "결과를 확인하지 못했습니다. 같은 요청으로 다시 시도해도 중복 실행되지 않습니다.",
} as const;

/**
 * A command button with its own status line (CommandStatus, 02 §16).
 * Disabled while offline/stale or when the server says the action is not
 * available. Accepted is shown as accepted, never as done.
 */
export function CommandButton({
  label,
  path,
  ifMatch,
  body,
  disabled,
  disabledReason,
  danger,
  onAccepted,
}: {
  label: ReactNode;
  path: string;
  ifMatch?: string;
  body?: unknown;
  disabled?: boolean;
  disabledReason?: string;
  danger?: boolean;
  onAccepted?: () => void;
}) {
  const { state, run } = useCommand();
  const busy = state.phase === "pending";
  return (
    <span className="cmd">
      <button
        type="button"
        className={danger ? "btn danger" : "btn"}
        disabled={disabled || busy}
        title={disabled ? disabledReason : undefined}
        onClick={async () => {
          if ((await run(path, { ifMatch, body })) === "accepted") onAccepted?.();
        }}
      >
        {label}
      </button>
      {disabled && disabledReason && <span className="small muted">{disabledReason}</span>}
      {state.phase !== "idle" && (
        <span role="status" className={`small cmd-${state.phase}`}>
          {state.phase === "failed" ? `거절됨: ${state.error?.code} — ${state.error?.message}` : PHASE_TEXT[state.phase]}
        </span>
      )}
    </span>
  );
}
