import type { ConnectionStatus } from "../realtime/stream";
import { relative } from "../components/ui";

const TEXT: Record<ConnectionStatus, { title: string; body: string; tone: "info" | "warn" | "bad" } | null> = {
  live: null,
  connecting: { title: "불러오는 중", body: "프로젝트 snapshot을 불러오고 있습니다.", tone: "info" },
  resyncing: { title: "다시 맞추는 중", body: "이벤트 순서가 이어지지 않아 전체 snapshot을 다시 불러옵니다. 그동안 화면은 최신이 아닐 수 있습니다.", tone: "warn" },
  reconnecting: { title: "실시간 연결 끊김", body: "마지막으로 적용한 위치부터 다시 연결하고 있습니다. 표시된 상태는 최신이 아닐 수 있고, 제어 명령은 잠시 막힙니다.", tone: "warn" },
  auth_expired: { title: "인증 만료", body: "터미널에서 samsara ui --open 을 다시 실행해 연결하세요. 토큰은 브라우저에 저장하지 않습니다.", tone: "bad" },
  incompatible: { title: "호환되지 않는 런타임", body: "이 대시보드가 이해하지 못하는 이벤트 형식입니다. 제어 명령을 막았습니다. UI와 런타임 버전을 맞춰 주세요.", tone: "bad" },
  error: { title: "불러오기 실패", body: "", tone: "bad" },
};

export function ConnectionBanner({ status, lastAppliedAt, reason, error }: { status: ConnectionStatus; lastAppliedAt: number | null; reason: string | null; error: string | null }) {
  const t = TEXT[status];
  if (!t) return null;
  return (
    <div role={t.tone === "info" ? "status" : "alert"} className={`banner banner-${t.tone}`} data-testid="connection-banner">
      <strong>{t.title}</strong> <span>{t.body || error}</span>
      {reason && <span className="mono muted"> 사유 {reason}</span>}
      {lastAppliedAt && <span className="muted"> · 마지막 반영 {relative(new Date(lastAppliedAt).toISOString())}</span>}
    </div>
  );
}
