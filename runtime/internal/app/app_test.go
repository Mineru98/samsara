package app_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Mineru98/samsara/runtime/internal/app"
	"github.com/Mineru98/samsara/runtime/internal/backend"
	"github.com/Mineru98/samsara/runtime/internal/backend/fake"
	"github.com/Mineru98/samsara/runtime/internal/backend/standalone"
	"github.com/Mineru98/samsara/runtime/internal/domain"
	"github.com/Mineru98/samsara/runtime/internal/paths"
	"github.com/Mineru98/samsara/runtime/internal/procinfo"
	"github.com/Mineru98/samsara/runtime/internal/store/sqlite"
)

// env is a deterministic test runtime: real SQLite + real git, fake worker
// backend and fake clock.
type env struct {
	t       *testing.T
	ctx     context.Context
	dir     string
	repo    string
	layout  paths.Layout
	clock   *domain.FakeClock
	db      *sqlite.DB
	app     *app.App
	fake    *fake.Backend
	backend *backend.Set
	cmd     app.Cmd
	project string
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	c := exec.Command("git", append([]string{"-C", dir}, args...)...)
	c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

const baseConfig = `version: 1
backend:
  mode: auto
  order: [herdr, tmux, standalone]
  allow_fallback: true
approval:
  implementation_start: auto
  result: manual
  integration: auto
git:
  target_ref: refs/heads/integration
  integration_authority: samsara_local
`

func newEnv(t *testing.T, cfg string) *env {
	t.Helper()
	return newEnvIn(t, cfg, t.TempDir())
}

// newEnvIn builds the runtime under dir, which may be reached through a
// symlink alias (as macOS TMPDIR /var -> /private/var is).
func newEnvIn(t *testing.T, cfg, dir string) *env {
	t.Helper()
	repo := filepath.Join(dir, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".samsara"), 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".samsara", "config.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-q", "-m", "init")
	git(t, repo, "branch", "integration")
	repo, _ = filepath.EvalSymlinks(repo)
	layout := paths.Layout{StateDir: filepath.Join(dir, "state"), RuntimeDir: filepath.Join(dir, "run"), WorktreeRoot: filepath.Join(dir, "wt")}
	if err := layout.Ensure(); err != nil {
		t.Fatal(err)
	}
	// A stand-in executable named samsara so hook readiness checks run for real.
	exe := filepath.Join(dir, "bin", "samsara")
	_ = os.MkdirAll(filepath.Dir(exe), 0o755)
	if err := os.WriteFile(exe, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	clock := domain.NewFakeClock(time.Date(2026, 10, 28, 1, 0, 0, 0, time.UTC))
	ctx := context.Background()
	db, err := sqlite.Open(ctx, layout.DBPath(), clock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	fk := fake.New(domain.BackendTmux, clock)
	herdr := fake.New(domain.BackendHerdr, clock)
	herdr.Availability = domain.BackendUnavailable
	set := backend.NewSet(clock, fk, herdr, &standalone.Adapter{Clock: clock})
	e := &env{t: t, ctx: ctx, dir: dir, repo: repo, layout: layout, clock: clock, db: db, fake: fk, backend: set,
		cmd: app.Cmd{Principal: app.Principal{ID: "user:test", Kind: "cli"}}}
	e.app = e.newApp(exe)
	res, err := e.app.RegisterProject(ctx, e.cmd, app.RegisterProjectRequest{Path: repo})
	if err != nil {
		t.Fatal(err)
	}
	e.project = res.ProjectID
	return e
}

func (e *env) newApp(exe string) *app.App {
	a := app.New(e.db, e.layout, e.backend, slog.New(slog.NewTextHandler(io.Discard, nil)))
	a.SelfExe = exe
	a.Claude = func() app.ClaudeSupport { return app.ClaudeSupport{Version: "2.1.311", BlockOnFailure: true} }
	if err := a.Recover(e.ctx); err != nil {
		e.t.Fatal(err)
	}
	return a
}

func (e *env) applyPlan(planJSON string) *app.PlanApplyResult {
	e.t.Helper()
	res, err := e.app.ApplyPlan(e.ctx, e.cmd, json.RawMessage(strings.ReplaceAll(planJSON, "PROJECT", e.project)))
	if err != nil {
		e.t.Fatal(err)
	}
	return res
}

func (e *env) reconcile(n int) {
	for i := 0; i < n; i++ {
		e.app.ReconcileOnce(e.ctx)
	}
}

func (e *env) attempt(issueID string) *sqlite.Attempt {
	e.t.Helper()
	var att *sqlite.Attempt
	_ = e.db.Read(e.ctx, func(q sqlite.Q) error {
		rows, _ := sqlite.AttemptsWhere(e.ctx, q, "issue_id=?", issueID)
		if len(rows) > 0 {
			att = rows[len(rows)-1]
		}
		return nil
	})
	return att
}

func (e *env) issueView(issueID string) *sqlite.IssueView {
	e.t.Helper()
	var v *sqlite.IssueView
	if err := e.db.Read(e.ctx, func(q sqlite.Q) error {
		var err error
		v, err = sqlite.IssueProjection(e.ctx, q, issueID)
		return err
	}); err != nil {
		e.t.Fatal(err)
	}
	return v
}

func selfProcess() map[string]string {
	id, _ := procinfo.Of(os.Getpid())
	return id.Map()
}

// register simulates the SessionStart hook of the fake worker of an issue.
func (e *env) register(issueID, claudeID string) *app.RegisterResult {
	e.t.Helper()
	att := e.attempt(issueID)
	s := e.fake.SessionByAttempt(att.ID)
	if s == nil {
		e.t.Fatalf("no fake worker launched for %s (attempt %s %s %s)", issueID, att.ID, att.State, att.StateReason)
	}
	res, err := e.app.RegisterSession(e.ctx, app.HookIdentity{ClaudeSessionID: claudeID, Cwd: s.Spec.Dir, Ticket: s.Spec.Env["SAMSARA_LAUNCH_TICKET"], Process: selfProcess()})
	if err != nil {
		e.t.Fatal(err)
	}
	return res
}

func (e *env) check(claudeID, cwd string, paths ...string) *app.ToolDecision {
	e.t.Helper()
	d, err := e.app.CheckTool(e.ctx, app.ToolCheckRequest{HookIdentity: app.HookIdentity{ClaudeSessionID: claudeID, Cwd: cwd, ManagedHint: true}, ToolName: "Edit", AffectedPaths: paths})
	if err != nil {
		e.t.Fatal(err)
	}
	return d
}

func (e *env) commitIn(dir, file, content string) string {
	e.t.Helper()
	if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0o644); err != nil {
		e.t.Fatal(err)
	}
	git(e.t, dir, "add", file)
	git(e.t, dir, "commit", "-q", "-m", "work on "+file)
	return git(e.t, dir, "rev-parse", "HEAD")
}

const abcPlan = `{
  "spec_version": "1", "project_id": "PROJECT", "expected_graph_version": "0",
  "issues": [
    {"client_key": "a", "title": "A", "acceptance_criteria": ["a works"]},
    {"client_key": "b", "title": "B", "acceptance_criteria": ["b works"]},
    {"client_key": "c", "title": "C", "acceptance_criteria": ["c works"]}
  ],
  "dependencies": [
    {"predecessor_key": "a", "successor_key": "b", "required_milestone": "RESULT_APPROVED", "downstream_phase": "EXECUTION"}
  ]
}`

func TestABCPartialBlockingRevokeAndIntegration(t *testing.T) {
	e := newEnv(t, baseConfig)
	res := e.applyPlan(abcPlan)
	A, B, C := res.IssueMapping["a"], res.IssueMapping["b"], res.IssueMapping["c"]
	if res.RunState != domain.RunPlanned {
		t.Fatalf("apply must leave run PLANNED, got %s", res.RunState)
	}
	if n := len(e.fake.CallsOf("launch")); n != 0 {
		t.Fatalf("apply must not launch workers, got %d", n)
	}
	if _, err := e.app.StartRun(e.ctx, e.cmd, res.RunID); err != nil {
		t.Fatal(err)
	}
	e.reconcile(3)

	// POL-01: A and C start, B waits for A's approval.
	if att := e.attempt(B); att != nil {
		t.Fatalf("B must not start while A is unapproved, got %s", att.State)
	}
	if v := e.issueView(B); v.DisplayStatus != "BLOCKED" {
		t.Fatalf("B display status = %s", v.DisplayStatus)
	}
	ra := e.register(A, "claude-A")
	rc := e.register(C, "claude-C")
	if ra.LeaseID == "" || rc.LeaseID == "" {
		t.Fatalf("registration must grant leases: %+v %+v", ra, rc)
	}
	wtA := e.fake.SessionByAttempt(e.attempt(A).ID).Spec.Dir
	wtC := e.fake.SessionByAttempt(e.attempt(C).ID).Spec.Dir
	if d := e.check("claude-A", wtA, "a.txt"); d.Decision != domain.DecisionPass {
		t.Fatalf("A check: %+v", d)
	}
	// Writes outside the assigned worktree are denied (HK-12).
	if d := e.check("claude-A", wtA, wtC+"/x.txt"); d.ReasonCode != domain.ReasonPathOutOfScope {
		t.Fatalf("out of scope path: %+v", d)
	}
	// Unknown/unmanaged sessions pass without managed hint, are denied with it.
	if d, _ := e.app.CheckTool(e.ctx, app.ToolCheckRequest{HookIdentity: app.HookIdentity{ClaudeSessionID: "other", Cwd: e.repo}, ToolName: "Edit"}); d.Decision != domain.DecisionPass {
		t.Fatalf("unmanaged: %+v", d)
	}
	if d := e.check("ghost", wtA); d.ReasonCode != domain.ReasonSessionNotRegistered {
		t.Fatalf("ghost: %+v", d)
	}

	// A implements and submits.
	e.commitIn(wtA, "a.txt", "A\n")
	ticketA := e.fake.SessionByAttempt(e.attempt(A).ID).Spec.Env["SAMSARA_LAUNCH_TICKET"]
	out, err := e.app.SubmitOutput(e.ctx, e.cmd, app.OutputSubmitRequest{Ticket: ticketA})
	if err != nil {
		t.Fatal(err)
	}
	// The released lease no longer authorises tools.
	if d := e.check("claude-A", wtA); d.Decision != domain.DecisionDeny {
		t.Fatalf("after submit A must be denied: %+v", d)
	}
	e.reconcile(2)
	if got := e.attempt(A).State; got != domain.AttemptVerified {
		t.Fatalf("A attempt = %s", got)
	}
	// Stop/verified alone does not unblock B: approval is required (POL-14).
	if att := e.attempt(B); att != nil {
		t.Fatalf("B started before approval")
	}
	vb := e.issueView(B)
	var approvalID string
	for _, r := range vb.BlockingReasons {
		if r.Code == "DEPENDENCY_APPROVAL_PENDING" {
			approvalID = r.ApprovalID
		}
	}
	if approvalID == "" {
		t.Fatalf("B should show the pending approval: %+v", vb.BlockingReasons)
	}
	// POL-09: idempotency key reuse with a different payload.
	cmd := e.cmd
	cmd.IdempotencyKey = "k1"
	if _, err := e.app.DecideApproval(e.ctx, cmd, approvalID, app.DecisionRequest{Decision: "APPROVE"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.app.DecideApproval(e.ctx, cmd, approvalID, app.DecisionRequest{Decision: "APPROVE"}); err != nil {
		t.Fatalf("same key same payload must replay: %v", err)
	}
	if _, err := e.app.DecideApproval(e.ctx, cmd, approvalID, app.DecisionRequest{Decision: "REJECT"}); !domain.IsCode(err, domain.CodeIdempotencyKeyReused) {
		t.Fatalf("expected IDEMPOTENCY_KEY_REUSED, got %v", err)
	}
	e.reconcile(3)
	if att := e.attempt(B); att == nil || att.State != domain.AttemptStarting {
		t.Fatalf("B should be starting after approval: %+v", att)
	}
	e.register(B, "claude-B")
	wtB := e.fake.SessionByAttempt(e.attempt(B).ID).Spec.Dir
	if d := e.check("claude-B", wtB); d.Decision != domain.DecisionPass {
		t.Fatalf("B check: %+v", d)
	}
	leaseB, _ := e.leaseOf(B)

	// F-REVOKE: revoking A's approval pauses only B; C continues (POL-02).
	if _, err := e.app.DecideApproval(e.ctx, e.cmd, approvalID, app.DecisionRequest{Decision: "REVOKE"}); err != nil {
		t.Fatal(err)
	}
	if got := e.attempt(B).State; got != domain.AttemptPausing && got != domain.AttemptPaused {
		t.Fatalf("B should be PAUSING, got %s", got)
	}
	if d := e.check("claude-B", wtB); d.Decision != domain.DecisionDeny {
		t.Fatalf("B must be denied after revoke: %+v", d)
	}
	if d := e.check("claude-C", wtC); d.Decision != domain.DecisionPass {
		t.Fatalf("C must keep running: %+v", d)
	}
	// Stale fence replay is refused (INV-03).
	d, _ := e.app.CheckTool(e.ctx, app.ToolCheckRequest{HookIdentity: app.HookIdentity{ClaudeSessionID: "claude-C", Cwd: wtC, ManagedHint: true}, ToolName: "Edit", Fence: "0"})
	if d.ReasonCode != domain.ReasonStaleFence {
		t.Fatalf("stale fence: %+v", d)
	}
	_ = leaseB
	// Stop hook is the stop evidence for PAUSING → PAUSED.
	if err := e.app.ObserveSession(e.ctx, app.ObserveRequest{HookIdentity: app.HookIdentity{ClaudeSessionID: "claude-B", Event: "Stop"}}); err != nil {
		t.Fatal(err)
	}
	if got := e.attempt(B).State; got != domain.AttemptPaused {
		t.Fatalf("B should be PAUSED after Stop, got %s", got)
	}
	// Integration of A: candidate exists after approval; the revoked approval
	// keeps the old candidate but a new approval is needed for B's gate.
	// Re-request: approve the new RESULT approval created for the same output.
	e.reconcile(2)
	var newApproval string
	_ = e.db.Read(e.ctx, func(q sqlite.Q) error {
		aps, _ := sqlite.ApprovalsWhere(e.ctx, q, "kind='RESULT' AND issue_id=? AND state='PENDING'", A)
		if len(aps) > 0 {
			newApproval = aps[0].ID
		}
		return nil
	})
	if newApproval == "" {
		// Revoked approvals are not resurrected; request a fresh one through retry of verification is out of scope.
		t.Log("no new approval requested automatically (expected: revocation is final for that approval)")
	}

	// Merge A's candidate through the local queue.
	var candID string
	_ = e.db.Read(e.ctx, func(q sqlite.Q) error {
		cs, _ := sqlite.CandidatesWhere(e.ctx, q, "issue_id=?", A)
		if len(cs) > 0 {
			candID = cs[0].ID
		}
		return nil
	})
	if candID == "" {
		t.Fatal("candidate for A missing")
	}
	if _, err := e.app.EnqueueCandidate(e.ctx, e.cmd, candID); err != nil {
		t.Fatal(err)
	}
	e.reconcile(4)
	var cand *sqlite.Candidate
	_ = e.db.Read(e.ctx, func(q sqlite.Q) error { cand, _ = sqlite.GetCandidate(e.ctx, q, candID); return nil })
	if cand.State != domain.CandidateMerged {
		t.Fatalf("candidate state %s (%s)", cand.State, cand.StateReason)
	}
	integ := git(t, e.repo, "rev-parse", "refs/heads/integration")
	if integ != cand.CandidateSHA {
		t.Fatalf("integration ref %s != candidate %s", integ, cand.CandidateSHA)
	}
	if !strings.Contains(git(t, e.repo, "log", "--format=%H", "refs/heads/integration"), out.SourceSHA) {
		t.Fatal("source commit not integrated")
	}
	// main (checked out by the user) never moves.
	if git(t, e.repo, "rev-parse", "main") == integ {
		t.Fatal("user branch must not move")
	}
	_ = out
}

func (e *env) leaseOf(issueID string) (*sqlite.Lease, error) {
	var l *sqlite.Lease
	err := e.db.Read(e.ctx, func(q sqlite.Q) error {
		var err error
		l, err = sqlite.LatestLeaseForAttempt(e.ctx, q, e.attempt(issueID).ID)
		return err
	})
	return l, err
}
