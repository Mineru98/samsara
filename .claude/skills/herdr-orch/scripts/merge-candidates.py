#!/usr/bin/env python3
"""Classify issues/PRs/worktrees into MERGE / HOLD / EXCLUDE for /issue-merge.

usage: merge-candidates.py --base <branch> [--live <N> ...] [--owned <N> ...] [--merging]
  --base   target branch PRs must point to (e.g. dev/0.4.0-alpha)
  --live   issue numbers whose herdr tab is still running
  --owned  issue numbers this orchestrator started (others are never auto-merged)
Run from the main checkout. Reads gh and git only; changes nothing.
"""
import argparse, json, os, re, subprocess, sys, time


def run(*cmd, cwd=None):
    r = subprocess.run(cmd, cwd=cwd, capture_output=True, text=True)
    return r.stdout.strip() if r.returncode == 0 else None


def issue_no(branch):
    m = re.match(r"[a-z]+/(\d+)-", branch or "")
    return int(m.group(1)) if m else None


ap = argparse.ArgumentParser()
ap.add_argument("--base", required=True)
ap.add_argument("--live", type=int, nargs="*", default=[])
ap.add_argument("--owned", type=int, nargs="*", default=[])
ap.add_argument("--merging", action="store_true", help="an /issue-merge tab is still running")
a = ap.parse_args()

PR_FIELDS = ("number,title,headRefName,headRefOid,baseRefName,isDraft,mergeable,"
             "mergeStateStatus,statusCheckRollup,files")
# GitHub recomputes mergeability lazily after a push or base move; retry while UNKNOWN.
for attempt in range(7):
    prs = json.loads(run("gh", "pr", "list", "--state", "open", "--limit", "200", "--json", PR_FIELDS) or "[]")
    if not any(p["mergeable"] == "UNKNOWN" for p in prs):
        break
    time.sleep(5)
issues = {i["number"]: i for i in json.loads(run(
    "gh", "issue", "list", "--state", "all", "--limit", "500", "--json", "number,state,labels") or "[]")}

worktrees = {}
for block in (run("git", "worktree", "list", "--porcelain") or "").split("\n\n"):
    path = re.search(r"^worktree (.+)$", block, re.M)
    br = re.search(r"^branch refs/heads/(.+)$", block, re.M)
    if path and br:
        worktrees[br.group(1)] = path.group(1)


def status_label(n):
    labels = [l["name"] for l in issues.get(n, {}).get("labels", [])]
    return next((l for l in labels if l.startswith("status:")), "status:-")


rows, seen = [], set()
for pr in prs:
    n = issue_no(pr["headRefName"])
    seen.add(pr["headRefName"])
    why_hold, why_excl, notes = [], [], []
    if n is None:
        why_excl.append("브랜치에서 이슈 번호를 못 찾음")
    elif n not in a.owned:
        why_excl.append("이 오케스트레이터가 맡은 이슈가 아님")
    if n in a.live:
        why_hold.append("tab 이 아직 작업 중")
    if pr["baseRefName"] != a.base:
        why_excl.append(f"base 가 {pr['baseRefName']}")
    if pr["isDraft"]:
        why_hold.append("draft PR")
    if pr["mergeable"] != "MERGEABLE":
        why_hold.append(f"mergeable={pr['mergeable']}")
    if pr["mergeStateStatus"] not in ("CLEAN", "HAS_HOOKS", "UNSTABLE"):
        why_hold.append(f"mergeState={pr['mergeStateStatus']}")
    bad = [c for c in pr.get("statusCheckRollup") or []
           if (c.get("conclusion") or c.get("state")) not in ("SUCCESS", "NEUTRAL", "SKIPPED")]
    if bad:
        why_hold.append("check 미통과: " + ",".join(c.get("name") or c.get("context") for c in bad))
    if n is not None:
        st = issues.get(n, {}).get("state")
        if st != "OPEN":
            why_excl.append(f"이슈 #{n} 이 {st}")
        elif status_label(n) != "status:review":
            why_hold.append(f"이슈 상태 {status_label(n)} (status:review 아님)")
        files = [f["path"] for f in pr["files"]]
        if not any(f.startswith(f".issue/{n}/evidence/") for f in files):
            why_hold.append("PR 에 증거 파일 없음")
    wt = worktrees.get(pr["headRefName"])
    if wt:
        head = run("git", "rev-parse", "HEAD", cwd=wt)
        if head and head != pr["headRefOid"]:
            why_hold.append("워크트리 HEAD 와 PR head 가 다름(미push 커밋)")
        dirty = [l for l in (run("git", "status", "--porcelain", cwd=wt) or "").splitlines() if l]
        # issue-end leaves scratch files (e.g. pr-body.md) under .issue/<N>/ untracked; not code changes.
        scratch = [l[3:] for l in dirty if l.startswith("?? .issue/")]
        if len(dirty) > len(scratch):
            why_hold.append("워크트리에 저장 안 된 변경")
        elif scratch:
            notes.append("작업 파일만 남음: " + ", ".join(scratch))
    verdict = "EXCLUDE" if why_excl else "HOLD" if why_hold else "MERGE"
    rows.append({"kind": "pr", "issue": n, "pr": pr["number"], "branch": pr["headRefName"],
                 "verdict": verdict, "reasons": why_excl + why_hold + notes,
                 "files": [f["path"] for f in pr["files"] if not f["path"].startswith(".issue/")]})

# Worktrees without an open PR: never merge candidates, but report them.
for br, path in worktrees.items():
    if br in seen or path == os.getcwd():
        continue
    n = issue_no(br)
    if n is None:
        continue
    st = issues.get(n, {}).get("state")
    reason = "열린 PR 없음"
    if st == "CLOSED":
        reason = f"이슈 #{n} 이 이미 닫힘 — 남은 워크트리"
    elif n in a.live:
        reason = "tab 이 아직 작업 중 — PR 전"
    rows.append({"kind": "worktree", "issue": n, "pr": None, "branch": br,
                 "verdict": "HOLD" if n in a.live else "EXCLUDE", "reasons": [reason], "files": []})

# File overlap among MERGE candidates (merge order matters; issue-merge resolves conflicts).
merge = [r for r in rows if r["verdict"] == "MERGE"]
overlap = []
for i, x in enumerate(merge):
    for y in merge[i + 1:]:
        common = sorted(set(x["files"]) & set(y["files"]))
        if common:
            overlap.append({"a": x["issue"], "b": y["issue"], "files": common})

nums = sorted(r["issue"] for r in merge)
if not merge:
    action, why = "none", "MERGE 판정이 없다"
elif a.merging:
    action, why = "wait", "다른 /issue-merge tab 이 아직 돈다"
elif a.live:
    action, why = "wait", "아직 작업 중인 이슈 tab 이 있다: " + " ".join(f"#{n}" for n in a.live) + " — 끝나면 함께 묶는다"
else:
    action, why = "merge", "작업 중인 tab 이 없고 MERGE 판정이 있다"
out = {"base": a.base, "rows": rows, "overlap": overlap, "action": action, "action_reason": why,
       "merge_issues": nums,
       "merge_command": ("/issue-merge " + " ".join(f"#{n}" for n in nums)) if merge else None}
json.dump(out, sys.stdout, ensure_ascii=False, indent=1)
print()
