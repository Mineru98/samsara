import type { ReactNode } from "react";
import type { Tone } from "../status";

export function Badge({ tone, children, title }: { tone: Tone; children: ReactNode; title?: string }) {
  return (
    <span className={`badge tone-${tone}`} title={title}>
      {children}
    </span>
  );
}

export function Dot({ tone }: { tone: Tone }) {
  return <span className={`dot tone-${tone}`} aria-hidden="true" />;
}

const dtf = new Intl.DateTimeFormat("ko-KR", { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", second: "2-digit", hour12: false });
const rtf = new Intl.RelativeTimeFormat("ko", { numeric: "auto" });

/** Absolute local time with a relative hint; invalid or empty → "-". */
export function Time({ at }: { at: string | null | undefined }) {
  if (!at) return <span className="muted">-</span>;
  const d = new Date(at);
  if (Number.isNaN(d.getTime())) return <span className="muted">{at}</span>;
  return (
    <time dateTime={at} title={d.toISOString()}>
      {dtf.format(d)}
    </time>
  );
}

export function relative(at: string | null | undefined, now = Date.now()): string {
  if (!at) return "-";
  const t = new Date(at).getTime();
  if (Number.isNaN(t)) return "-";
  const s = Math.round((t - now) / 1000);
  const abs = Math.abs(s);
  if (abs < 60) return rtf.format(s, "second");
  if (abs < 3600) return rtf.format(Math.round(s / 60), "minute");
  if (abs < 86400) return rtf.format(Math.round(s / 3600), "hour");
  return rtf.format(Math.round(s / 86400), "day");
}

export function Mono({ children, title }: { children: ReactNode; title?: string }) {
  return (
    <span className="mono" title={title}>
      {children}
    </span>
  );
}

export function shortSha(sha: string | undefined | null): string {
  return sha ? sha.slice(0, 9) : "-";
}

export function Empty({ children }: { children: ReactNode }) {
  return <p className="empty">{children}</p>;
}

/** Only http(s) links are rendered as links; everything else is text (02 §21.1). */
export function SafeLink({ href, children }: { href: string; children: ReactNode }) {
  let ok = false;
  try {
    const u = new URL(href);
    ok = u.protocol === "https:" || u.protocol === "http:";
  } catch {
    ok = false;
  }
  if (!ok) return <span>{children}</span>;
  return (
    <a href={href} target="_blank" rel="noopener noreferrer">
      {children}
    </a>
  );
}
