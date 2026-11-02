#!/usr/bin/env python3
"""Print remaining Codex usage percent from the newest rate_limits record in ~/.codex/sessions.

Output: REMAINING=<0-100> USED=<n> WINDOW_MIN=<n> RECORDED_AT=<ts> (or REMAINING=unknown)
Remaining = 100 - max(used_percent of primary/secondary windows). A window whose resets_at
has passed counts as fully reset. The record is only as fresh as the last codex session.
"""
import glob, json, os, time

files = sorted(glob.glob(os.path.expanduser("~/.codex/sessions/*/*/*/*.jsonl")), key=os.path.getmtime)[-20:]
best = None
for f in files:
    for line in open(f, errors="ignore"):
        if '"rate_limits"' not in line:
            continue
        try:
            d = json.loads(line)
        except ValueError:
            continue
        p = d.get("payload", d)
        rl = p.get("rate_limits") or (p.get("info") or {}).get("rate_limits")
        if rl and (best is None or (d.get("timestamp") or "") >= (best[0] or "")):
            best = (d.get("timestamp"), rl)
if not best:
    print("REMAINING=unknown")
    raise SystemExit(0)
ts, rl = best
now = time.time()
used, window = 0.0, None
for key in ("primary", "secondary"):
    w = rl.get(key)
    if not w:
        continue
    u = 0.0 if (w.get("resets_at") or 0) < now else float(w.get("used_percent") or 0)
    if u >= used:
        used, window = u, w.get("window_minutes")
print(f"REMAINING={100 - used:.0f} USED={used:.0f} WINDOW_MIN={window} RECORDED_AT={ts}")
