import { auth } from "./http";

/**
 * Reads the one-time code from `#bootstrap=<code>`, removes the fragment
 * before anything else can observe it, and exchanges it for a bearer kept in
 * memory (03 §11.1, 02 §21.1). The code never goes into logs or the URL bar
 * history.
 */
export async function bootstrapFromFragment(loc: Location = window.location, hist: History = window.history): Promise<"exchanged" | "none" | "failed"> {
  const m = /(?:^#|&)bootstrap=([^&]+)/.exec(loc.hash);
  if (!m) return "none";
  // Remove the fragment first, whatever the exchange result.
  hist.replaceState(hist.state, "", loc.pathname + loc.search);
  try {
    const code = decodeURIComponent(m[1]);
    const res = await fetch("/v1/ui/sessions", {
      method: "POST",
      headers: { "Content-Type": "application/json", Accept: "application/json" },
      body: JSON.stringify({ code }),
    });
    if (!res.ok) return "failed";
    const body = (await res.json()) as { token?: string };
    if (!body.token) return "failed";
    auth.set(body.token);
    return "exchanged";
  } catch {
    return "failed";
  }
}
