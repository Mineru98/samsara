//go:build e2e

package app_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Mineru98/samsara/runtime/internal/app"
	"github.com/Mineru98/samsara/runtime/internal/domain"
	"github.com/Mineru98/samsara/runtime/internal/github"
	"github.com/Mineru98/samsara/runtime/internal/store/sqlite"
)

// The trusted collector the e2e target branch carries (runtime/README.md).
const e2eWorkflow = `name: samsara-merge-group
on:
  merge_group:
    types: [checks_requested]
permissions: {}
jobs:
  manifest:
    runs-on: ubuntu-latest
    steps:
      - env:
          REPO: ${{ github.repository }}
          RUN_ID: ${{ github.run_id }}
          RUN_ATTEMPT: ${{ github.run_attempt }}
          HEAD_SHA: ${{ github.event.merge_group.head_sha }}
          BASE_SHA: ${{ github.event.merge_group.base_sha }}
          HEAD_REF: ${{ github.event.merge_group.head_ref }}
        run: |
          mkdir out
          jq -n --arg r "$REPO" --argjson id "$RUN_ID" --argjson at "$RUN_ATTEMPT" --arg h "$HEAD_SHA" --arg b "$BASE_SHA" --arg ref "$HEAD_REF" \
            '{repository:$r,run_id:$id,run_attempt:$at,event:"merge_group",head_sha:$h,base_sha:$b,head_ref:$ref}' > out/manifest.json
      - uses: actions/upload-artifact@v4
        with:
          name: samsara-merge-group-manifest
          path: out/manifest.json
`

const e2eConfig = `version: 1
backend:
  mode: auto
  order: [herdr, tmux, standalone]
  allow_fallback: true
approval:
  implementation_start: auto
  result: manual
  integration: manual
git:
  target_ref: refs/heads/%s
  integration_authority: github_queue
github:
  sync: "off"
  repository: %s
  merge_queue:
    policy_app_id: %d
    manifest_workflow: .github/workflows/samsara-merge-group.yml
`

// TestGitHubQueueE2E drives candidates through the merge queue of a real
// org repository with the real App publisher (#107, 04 §10.8). The repo
// needs a ruleset on refs/heads/samsara-e2e/** with a merge queue (ALLGREEN,
// build/merge size 1) and the required check samsara/integration-policy
// pinned to the App.
//
//	SAMSARA_E2E_REPO=org/name SAMSARA_E2E_STATE=<dir with github-app.json> \
//	SAMSARA_E2E_LOG=<file> go test -tags e2e ./internal/app -run E2E -v -timeout 60m
func TestGitHubQueueE2E(t *testing.T) {
	repo, state := os.Getenv("SAMSARA_E2E_REPO"), os.Getenv("SAMSARA_E2E_STATE")
	if repo == "" || state == "" {
		t.Skip("SAMSARA_E2E_REPO and SAMSARA_E2E_STATE are required")
	}
	pub, err := github.LoadAppPublisher(state)
	if err != nil || pub == nil {
		t.Fatalf("app credential: %v", err)
	}
	stamp := time.Now().UTC().Format("20060102-150405")
	t.Run("merged", func(t *testing.T) {
		runE2E(t, pub, repo, "samsara-e2e/"+stamp+"-merged", "feature.txt", "e2e\n", "MERGED")
	})
	t.Run("manifest_rejected", func(t *testing.T) {
		// The pull request rewrites the collector: its group must get a
		// failing check and never a success.
		runE2E(t, pub, repo, "samsara-e2e/"+stamp+"-rejected", ".github/workflows/samsara-merge-group.yml", e2eWorkflow+"# rewritten by the pull request\n", "REJECTED")
	})
}

func e2eLog(t *testing.T, format string, args ...any) {
	t.Helper()
	line := fmt.Sprintf("%s %s", time.Now().UTC().Format(time.RFC3339), fmt.Sprintf(format, args...))
	t.Log(line)
	if p := os.Getenv("SAMSARA_E2E_LOG"); p != "" {
		if f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			fmt.Fprintln(f, line)
			f.Close()
		}
	}
}

func run(t *testing.T, dir, name string, args ...string) string {
	t.Helper()
	c := exec.Command(name, args...)
	c.Dir = dir
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func runE2E(t *testing.T, pub *github.AppPublisher, repo, target, file, content, want string) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "repo", ".github", "workflows"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "repo", ".github", "workflows", "samsara-merge-group.yml"), []byte(e2eWorkflow), 0o644); err != nil {
		t.Fatal(err)
	}
	e := newEnvIn(t, fmt.Sprintf(e2eConfig, target, repo, pub.AppID()), dir)
	git(t, e.repo, "branch", target, "main")
	git(t, e.repo, "remote", "add", "origin", "https://github.com/"+repo+".git")
	run(t, e.repo, "git", "-c", "credential.helper=", "-c", "credential.helper=!gh auth git-credential", "push", "-q", "origin", "refs/heads/"+target+":refs/heads/"+target)
	e2eLog(t, "[%s] target %s pushed at %s", want, target, git(t, e.repo, "rev-parse", target))

	issueID, wt := startSingle(t, e)
	e.commitIn(wt, file, content)
	e.submitAndApprove(issueID)
	cs := e.candidates(issueID)
	if len(cs) != 1 {
		t.Fatalf("candidates: %d", len(cs))
	}
	c := cs[0]
	src := target + "-src"
	run(t, e.repo, "git", "-c", "credential.helper=", "-c", "credential.helper=!gh auth git-credential", "push", "-q", "origin", c.SourceSHA+":refs/heads/"+src)
	pr := run(t, e.repo, "gh", "pr", "create", "--repo", repo, "--base", target, "--head", src,
		"--title", "samsara e2e "+want+" "+target, "--body", "Samsara #107 e2e candidate "+c.ID)
	e2eLog(t, "[%s] candidate %s source %s pull request %s", want, c.ID, c.SourceSHA, pr)

	e.app.GitHub, e.app.PolicyChecks = github.NewCLI(), pub
	deadline := time.Now().Add(25 * time.Minute)
	last, enqueued := "", false
	for time.Now().Before(deadline) {
		e.clock.Advance(11 * time.Second)
		e.app.ReconcileOnce(e.ctx)
		cur, _ := e.candidate(c.ID)
		var g *sqlite.ProviderGroup
		if cur.ProviderValidationID != "" {
			_ = e.db.Read(e.ctx, func(q sqlite.Q) error { g, _ = sqlite.GetProviderGroup(e.ctx, q, cur.ProviderValidationID); return nil })
		}
		snap := fmt.Sprintf("candidate %s(%s) native=%s", cur.State, cur.StateReason, cur.NativeQueueState)
		if g != nil {
			snap += fmt.Sprintf(" group=%s head=%s base=%s check=%s run=%s manifest=%s/%s/%s", g.ID, g.HeadSHA, g.BaseSHA, g.CheckState, g.CheckRunID, g.ManifestState, g.ManifestRunID, g.ManifestReason)
		}
		if snap != last {
			e2eLog(t, "[%s] %s", want, snap)
			last = snap
		}
		if cur.State == domain.CandidateWaiting && cur.StateReason == "AWAITING_ENQUEUE" && !enqueued {
			if _, err := e.app.EnqueueCandidate(e.ctx, e.cmd, c.ID); err != nil {
				t.Fatalf("enqueue: %v", err)
			}
			enqueued = true
			e2eLog(t, "[%s] enqueue requested", want)
		}
		for _, ap := range e.mergeApprovals(c.ID) {
			if ap.State == domain.ApprovalPending {
				if _, err := e.app.DecideApproval(e.ctx, e.cmd, ap.ID, app.DecisionRequest{Decision: "APPROVE"}); err == nil {
					e2eLog(t, "[%s] MERGE approval %s approved (candidate revision %d)", want, ap.ID, ap.CandidateRevision)
				}
			}
		}
		switch {
		case want == "MERGED" && cur.State == domain.CandidateMerged:
			var ev string
			_ = e.db.Read(e.ctx, func(q sqlite.Q) error {
				return q.QueryRowContext(e.ctx, "SELECT method||' '||target_after||' '||provider_evidence FROM integration_evidence WHERE candidate_id=?", c.ID).Scan(&ev)
			})
			e2eLog(t, "[%s] evidence %s", want, ev)
			if cur.StateReason == "MERGED_WITHOUT_POLICY_SUCCESS" || !strings.Contains(ev, `"policy_authorized":true`) {
				t.Fatalf("merge must be policy authorized: %s", ev)
			}
			return
		case want == "REJECTED" && g != nil && g.CheckState == "FAILED":
			e2eLog(t, "[%s] failing check %s published for group head %s (%s)", want, g.CheckRunID, g.HeadSHA, g.ManifestReason)
			if g.ManifestReason != "MANIFEST_WORKFLOW_MODIFIED" {
				t.Fatalf("reason: %s", g.ManifestReason)
			}
			run(t, e.repo, "gh", "pr", "close", pr, "--repo", repo)
			return
		case cur.State == domain.CandidateFailed, cur.State == domain.CandidateMerged:
			t.Fatalf("unexpected end: %s", snap)
		}
		time.Sleep(5 * time.Second)
	}
	t.Fatalf("timed out: %s", last)
}
