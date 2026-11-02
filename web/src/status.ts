// One place maps server states to Korean labels and tones so the graph,
// tables and inspector never disagree (02 §8.2, §23.1).

export type Tone = "run" | "ok" | "wait" | "approval" | "bad" | "idle" | "unknown";

const DISPLAY: Record<string, [string, Tone]> = {
  RUNNING: ["실행 중", "run"],
  STARTING: ["시작 중", "run"],
  VERIFYING: ["검증 중", "run"],
  PAUSED: ["일시 정지", "wait"],
  CANCELLING: ["취소 중", "wait"],
  BLOCKED: ["차단됨", "wait"],
  APPROVAL_PENDING: ["승인 대기", "approval"],
  VERIFIED: ["검증 완료", "ok"],
  INTEGRATING: ["통합 대기", "run"],
  INTEGRATED: ["통합 완료", "ok"],
  READY: ["실행 가능", "idle"],
  PLANNED: ["계획됨", "idle"],
  IDLE: ["유휴", "idle"],
  FAILED: ["실패", "bad"],
  ATTENTION: ["확인 필요", "bad"],
  CANCELLED: ["취소됨", "idle"],
};

export function displayStatus(s: string | undefined | null): { label: string; tone: Tone } {
  if (!s) return { label: "알 수 없음", tone: "unknown" };
  const hit = DISPLAY[s];
  // A newer runtime may send a value this build does not know (03 §12.1).
  return hit ? { label: hit[0], tone: hit[1] } : { label: `알 수 없음 (${s})`, tone: "unknown" };
}

const GENERIC: Record<string, [string, Tone]> = {
  // run
  ACTIVE: ["진행 중", "run"],
  COMPLETED: ["완료", "ok"],
  // approval
  PENDING: ["대기", "approval"],
  APPROVED: ["승인됨", "ok"],
  REJECTED: ["반려됨", "bad"],
  INVALIDATED: ["무효화됨", "idle"],
  // candidate
  WAITING: ["대기", "wait"],
  VALIDATING: ["검증 중", "run"],
  READY: ["준비됨", "ok"],
  PUBLISHING: ["반영 중", "run"],
  MERGED: ["통합됨", "ok"],
  STALE_BASE: ["기준 변경됨", "wait"],
  CONFLICT: ["충돌", "bad"],
  // worktree
  CREATING: ["생성 중", "run"],
  IN_USE: ["사용 중", "run"],
  QUIESCING: ["정리 대기", "wait"],
  RETAINED: ["보존됨", "idle"],
  CLEANUP_ELIGIBLE: ["정리 가능", "idle"],
  REMOVED: ["제거됨", "idle"],
  QUARANTINED: ["격리됨", "bad"],
  // session liveness / freshness
  ALIVE: ["살아 있음", "ok"],
  EXITED: ["종료됨", "idle"],
  UNKNOWN: ["관찰 불가", "unknown"],
  LIVE: ["최신", "ok"],
  PARTIAL: ["일부", "wait"],
  STALE: ["오래됨", "wait"],
  UNAVAILABLE: ["사용 불가", "bad"],
  // project
  ENABLED: ["사용 중", "ok"],
  DISABLED: ["비활성", "idle"],
  UNCONFIGURED: ["미설정", "idle"],
  PRESENT: ["경로 확인됨", "ok"],
  MISSING: ["경로 없음", "bad"],
  ARCHIVED: ["보관됨", "idle"],
  LOST: ["유실", "bad"],
  CANCEL_REQUESTED: ["취소 요청", "wait"],
  IMPLEMENTED: ["구현 완료", "ok"],
};

/** Label for run/approval/candidate/worktree/session states. */
export function stateLabel(s: string | undefined | null): { label: string; tone: Tone } {
  if (!s) return { label: "-", tone: "unknown" };
  const hit = GENERIC[s] ?? DISPLAY[s];
  return hit ? { label: hit[0], tone: hit[1] } : { label: s, tone: "unknown" };
}

const MILESTONE: Record<string, string> = {
  IMPLEMENTED: "구현",
  VERIFIED: "검증",
  RESULT_APPROVED: "결과 승인",
  INTEGRATED: "통합",
};

export function milestoneLabel(m: string): string {
  return MILESTONE[m] ?? m;
}
