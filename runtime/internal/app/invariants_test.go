package app_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Mineru98/samsara/runtime/internal/app"
	"github.com/Mineru98/samsara/runtime/internal/domain"
	"github.com/Mineru98/samsara/runtime/internal/store/sqlite"
)

const singlePlan = `{"spec_version":"1","project_id":"PROJECT","expected_graph_version":"0",
 "issues":[{"client_key":"x","title":"X"}],"dependencies":[]}`

// startSingle applies a one-issue plan, starts it and registers the worker.
func startSingle(t *testing.T, e *env) (issueID, wt string) {
	t.Helper()
	res := e.applyPlan(singlePlan)
	if _, err := e.app.StartRun(e.ctx, e.cmd, res.RunID); err != nil {
		t.Fatal(err)
	}
	e.reconcile(3)
	issueID = res.IssueMapping["x"]
	r := e.register(issueID, "claude-X")
	if r.LeaseID == "" {
		t.Fatalf("no lease: %+v", r)
	}
	wt = e.fake.SessionByAttempt(e.attempt(issueID).ID).Spec.Dir
	return issueID, wt
}

func TestPlanValidationAndApplyRules(t *testing.T) {
	e := newEnv(t, baseConfig)
	bad := []struct{ name, plan, code string }{
		{"dup", `{"spec_version":"1","project_id":"PROJECT","expected_graph_version":"0","issues":[{"client_key":"a","title":"A"},{"client_key":"a","title":"B"}]}`, "DUPLICATE_KEY"},
		{"dangling", `{"spec_version":"1","project_id":"PROJECT","expected_graph_version":"0","issues":[{"client_key":"a","title":"A"}],"dependencies":[{"predecessor_key":"a","successor_key":"zz"}]}`, "DANGLING_DEPENDENCY"},
		{"cycle", `{"spec_version":"1","project_id":"PROJECT","expected_graph_version":"0","issues":[{"client_key":"a","title":"A"},{"client_key":"b","title":"B"}],"dependencies":[{"predecessor_key":"a","successor_key":"b"},{"predecessor_key":"b","successor_key":"a"}]}`, domain.CodeCycleDetected},
		{"profile", `{"spec_version":"1","project_id":"PROJECT","expected_graph_version":"0","issues":[{"client_key":"a","title":"A","check_profile":"nope"}]}`, "UNREGISTERED_CHECK_PROFILE"},
		{"shell", `{"spec_version":"1","project_id":"PROJECT","expected_graph_version":"0","issues":[{"client_key":"a","title":"A","check_profile":"rm -rf /"}]}`, "INVALID_CHECK_PROFILE"},
		{"unknown field", `{"spec_version":"1","project_id":"PROJECT","expected_graph_version":"0","issues":[{"client_key":"a","title":"A","command":"x"}]}`, domain.CodeInvalidRequest},
	}
	for _, b := range bad {
		raw := json.RawMessage(strings.ReplaceAll(b.plan, "PROJECT", e.project))
		v, err := e.app.ValidatePlan(e.ctx, raw)
		if err != nil {
			if !strings.Contains(err.Error(), b.code) && !domain.IsCode(err, b.code) {
				t.Fatalf("%s: unexpected error %v", b.name, err)
			}
			continue
		}
		found := false
		for _, p := range v.Problems {
			found = found || p.Code == b.code
		}
		if v.Valid || !found {
			t.Fatalf("%s: expected %s, got %+v", b.name, b.code, v)
		}
		if _, err := e.app.ApplyPlan(e.ctx, e.cmd, raw); err == nil {
			t.Fatalf("%s: apply must fail", b.name)
		}
	}
	// PLN-01: rejected plans leave no rows behind.
	var n int
	_ = e.db.Read(e.ctx, func(q sqlite.Q) error { return q.QueryRowContext(e.ctx, "SELECT COUNT(*) FROM issues").Scan(&n) })
	if n != 0 {
		t.Fatalf("rejected plans must not mutate, found %d issues", n)
	}
	// PLN-02: same key + same plan → same run.
	cmd := e.cmd
	cmd.IdempotencyKey = "plan-1"
	raw := json.RawMessage(strings.ReplaceAll(singlePlan, "PROJECT", e.project))
	r1, err := e.app.ApplyPlan(e.ctx, cmd, raw)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := e.app.ApplyPlan(e.ctx, cmd, raw)
	if err != nil || r2.RunID != r1.RunID || r2.IssueMapping["x"] != r1.IssueMapping["x"] {
		t.Fatalf("replay: %+v %v", r2, err)
	}
	// Same key, different payload.
	other := json.RawMessage(strings.ReplaceAll(strings.ReplaceAll(singlePlan, "PROJECT", e.project), `"X"`, `"Y"`))
	if _, err := e.app.ApplyPlan(e.ctx, cmd, other); !domain.IsCode(err, domain.CodeIdempotencyKeyReused) {
		t.Fatalf("expected key reuse error, got %v", err)
	}
	// PLN-03: stale expected graph version.
	if _, err := e.app.ApplyPlan(e.ctx, e.cmd, other); !domain.IsCode(err, domain.CodeVersionMismatch) {
		t.Fatalf("expected VERSION_MISMATCH, got %v", err)
	}
	// PLN-04: unfinished run blocks a plain apply.
	fresh := json.RawMessage(strings.ReplaceAll(strings.ReplaceAll(singlePlan, "PROJECT", e.project), `"expected_graph_version":"0"`, `"expected_graph_version":"1"`))
	if _, err := e.app.ApplyPlan(e.ctx, e.cmd, fresh); !domain.IsCode(err, domain.CodeConflict) {
		t.Fatalf("expected CONFLICT, got %v", err)
	}
	if len(e.fake.CallsOf("launch")) != 0 {
		t.Fatal("plan apply must never launch workers")
	}
}

func TestPlanApprovalRequiredBeforeStart(t *testing.T) {
	e := newEnv(t, strings.Replace(baseConfig, "implementation_start: auto", "implementation_start: manual", 1))
	res := e.applyPlan(singlePlan)
	out, err := e.app.StartRun(e.ctx, e.cmd, res.RunID)
	if !domain.IsCode(err, domain.CodeApprovalRequired) || out.ApprovalID == "" {
		t.Fatalf("expected APPROVAL_REQUIRED with approval id, got %+v %v", out, err)
	}
	e.reconcile(3)
	if len(e.fake.CallsOf("launch")) != 0 || e.attempt(res.IssueMapping["x"]) != nil {
		t.Fatal("PLN-06: no worker before PLAN approval")
	}
	if _, err := e.app.DecideApproval(e.ctx, e.cmd, out.ApprovalID, app.DecisionRequest{Decision: "APPROVE"}); err != nil {
		t.Fatal(err)
	}
	// Approval alone does not start the run: the explicit start re-checks.
	e.reconcile(2)
	if e.attempt(res.IssueMapping["x"]) != nil {
		t.Fatal("approval must not start the run by itself")
	}
	if _, err := e.app.StartRun(e.ctx, e.cmd, res.RunID); err != nil {
		t.Fatal(err)
	}
	e.reconcile(3)
	if att := e.attempt(res.IssueMapping["x"]); att == nil || att.State != domain.AttemptStarting {
		t.Fatalf("expected STARTING attempt, got %+v", att)
	}
}

func TestRestartEpochAndQuarantine(t *testing.T) {
	e := newEnv(t, baseConfig)
	issue, wt := startSingle(t, e)
	l1, _ := e.leaseOf(issue)
	// A new daemon epoch on the same DB (POL-12).
	e.app = e.newApp(e.app.SelfExe)
	l, _ := e.leaseOf(issue)
	if l.State != domain.LeaseExpired {
		t.Fatalf("old-epoch lease must expire at recovery, got %s", l.State)
	}
	// INV-04: observation broken → no re-authorisation, no reassignment.
	e.fake.ObserveError = true
	if d := e.check("claude-X", wt); d.Decision != domain.DecisionDeny {
		t.Fatalf("unverified worker must be denied: %+v", d)
	}
	e.reconcile(2)
	if att := e.attempt(issue); att.State != domain.AttemptRunning || att.QuarantineReason == "" {
		t.Fatalf("attempt must stay RUNNING+quarantined, got %s/%s", att.State, att.QuarantineReason)
	}
	if n := len(e.fake.CallsOf("launch")); n != 1 {
		t.Fatalf("no duplicate worker after restart, launches=%d", n)
	}
	// Verified alive with the same generation → new lease with a new fence.
	e.fake.ObserveError = false
	if d := e.check("claude-X", wt); d.Decision != domain.DecisionPass {
		t.Fatalf("verified worker re-authorised: %+v", d)
	}
	l2, _ := e.leaseOf(issue)
	if l2.ID == l1.ID || l2.Fence <= l1.Fence {
		t.Fatalf("re-issued lease must be new with higher fence: %+v vs %+v", l2, l1)
	}
	// BE-07: same pane, new generation → not the old worker.
	e.fake.Reincarnate(e.attempt(issue).ID)
	e.clock.Advance(time.Minute)
	e.reconcile(1)
	if d := e.check("claude-X", wt); d.Decision != domain.DecisionDeny {
		t.Fatalf("reincarnated pane must not inherit authority: %+v", d)
	}
}

func TestLeaseExpiryClockAndRenewal(t *testing.T) {
	e := newEnv(t, baseConfig)
	issue, wt := startSingle(t, e)
	start := e.clock.Now()
	// POL-13: wall clock moves backwards → no authority.
	e.clock.Set(start.Add(-time.Hour))
	if d := e.check("claude-X", wt); d.ReasonCode != domain.ReasonClockAnomaly {
		t.Fatalf("clock regression: %+v", d)
	}
	e.clock.Set(start)
	// Heartbeat renewal through the launcher ticket.
	ticket := e.fake.SessionByAttempt(e.attempt(issue).ID).Spec.Env["SAMSARA_LAUNCH_TICKET"]
	e.clock.Advance(20 * time.Second)
	if r, err := e.app.RenewLease(e.ctx, app.RenewRequest{Ticket: ticket}); err != nil || !r.Renewed {
		t.Fatalf("renew: %+v %v", r, err)
	}
	// R-03: expiry is not death. Expire, then the same live worker regains a
	// fresh lease only after verification.
	e.clock.Advance(2 * time.Minute)
	e.reconcile(1)
	l, _ := e.leaseOf(issue)
	if l.State != domain.LeaseExpired {
		t.Fatalf("expected EXPIRED, got %s", l.State)
	}
	if att := e.attempt(issue); att.State != domain.AttemptRunning || att.QuarantineReason != "LEASE_EXPIRED" {
		t.Fatalf("attempt after expiry: %s %s", att.State, att.QuarantineReason)
	}
	if r, err := e.app.RenewLease(e.ctx, app.RenewRequest{Ticket: ticket}); err != nil || !r.Renewed {
		t.Fatalf("verified re-issue: %+v %v", r, err)
	}
	if att := e.attempt(issue); att.QuarantineReason != "" {
		t.Fatalf("quarantine should clear, got %s", att.QuarantineReason)
	}
	// Wrong ticket is refused.
	if _, err := e.app.RenewLease(e.ctx, app.RenewRequest{Ticket: "forged"}); err == nil {
		t.Fatal("forged ticket accepted")
	}
}

func TestLaunchAckLossDoesNotDuplicateWorker(t *testing.T) {
	e := newEnv(t, baseConfig)
	e.fake.LoseLaunchAck = true
	res := e.applyPlan(singlePlan)
	if _, err := e.app.StartRun(e.ctx, e.cmd, res.RunID); err != nil {
		t.Fatal(err)
	}
	e.reconcile(3)
	var state string
	_ = e.db.Read(e.ctx, func(q sqlite.Q) error {
		return q.QueryRowContext(e.ctx, "SELECT state FROM outbox WHERE kind='worker.launch'").Scan(&state)
	})
	if state != "UNKNOWN" {
		t.Fatalf("lost ack must be UNKNOWN, got %s", state)
	}
	e.clock.Advance(time.Minute)
	e.reconcile(3)
	if len(e.fake.Sessions) != 1 {
		t.Fatalf("BE-10: exactly one worker, got %d", len(e.fake.Sessions))
	}
	_ = e.db.Read(e.ctx, func(q sqlite.Q) error {
		return q.QueryRowContext(e.ctx, "SELECT state FROM outbox WHERE kind='worker.launch'").Scan(&state)
	})
	if state != "DONE" {
		t.Fatalf("re-checked launch should adopt and finish, got %s", state)
	}
	if r := e.register(res.IssueMapping["x"], "claude-X"); r.LeaseID == "" {
		t.Fatalf("adopted worker registers: %+v", r)
	}
}

func TestPauseResumePromptAckLoss(t *testing.T) {
	e := newEnv(t, baseConfig)
	issue, wt := startSingle(t, e)
	run := e.attempt(issue).RunID
	if _, err := e.app.PauseRun(e.ctx, e.cmd, run); err != nil {
		t.Fatal(err)
	}
	if d := e.check("claude-X", wt); d.Decision != domain.DecisionDeny {
		t.Fatalf("paused run must deny: %+v", d)
	}
	// The worker finished its response (Stop) → PAUSED, input READY.
	if err := e.app.ObserveSession(e.ctx, app.ObserveRequest{HookIdentity: app.HookIdentity{ClaudeSessionID: "claude-X", Event: "Stop"}}); err != nil {
		t.Fatal(err)
	}
	if got := e.attempt(issue).State; got != domain.AttemptPaused {
		t.Fatalf("expected PAUSED, got %s", got)
	}
	e.fake.LosePromptAck = true
	if _, err := e.app.ResumeRun(e.ctx, e.cmd, run); err != nil {
		t.Fatal(err)
	}
	e.reconcile(3)
	att := e.attempt(issue)
	if att.State != domain.AttemptRunning || !att.ResumeRequired {
		t.Fatalf("resumed attempt: %s resume_required=%v", att.State, att.ResumeRequired)
	}
	if n := len(e.fake.CallsOf("submit")); n != 1 {
		t.Fatalf("one automated prompt expected, got %d", n)
	}
	e.clock.Advance(5 * time.Minute)
	e.reconcile(3)
	if n := len(e.fake.CallsOf("submit")); n != 1 {
		t.Fatalf("INV-08: unknown prompt delivery must not be resent, got %d submits", n)
	}
	var dstate string
	_ = e.db.Read(e.ctx, func(q sqlite.Q) error {
		return q.QueryRowContext(e.ctx, "SELECT state FROM dispatches").Scan(&dstate)
	})
	if dstate != "UNKNOWN" {
		t.Fatalf("dispatch state %s", dstate)
	}
	if d := e.check("claude-X", wt); d.Decision != domain.DecisionPass {
		t.Fatalf("resumed worker allowed: %+v", d)
	}
}

func TestHumanTakeoverBlocksAutomatedInput(t *testing.T) {
	e := newEnv(t, baseConfig)
	issue, _ := startSingle(t, e)
	run := e.attempt(issue).RunID
	// The user typed into the managed session (no dispatch marker).
	_ = e.app.ObserveSession(e.ctx, app.ObserveRequest{HookIdentity: app.HookIdentity{ClaudeSessionID: "claude-X", Event: "UserPromptSubmit"}})
	_, _ = e.app.PauseRun(e.ctx, e.cmd, run)
	_ = e.app.ObserveSession(e.ctx, app.ObserveRequest{HookIdentity: app.HookIdentity{ClaudeSessionID: "claude-X", Event: "Stop"}})
	_, _ = e.app.ResumeRun(e.ctx, e.cmd, run)
	e.reconcile(3)
	if n := len(e.fake.CallsOf("submit")); n != 0 {
		t.Fatalf("BE-08: no automated input after human takeover, got %d", n)
	}
	if att := e.attempt(issue); !att.ResumeRequired {
		t.Fatal("user must be told to resume manually")
	}
}

func TestDisableDrainKeepsGuard(t *testing.T) {
	e := newEnv(t, baseConfig)
	issue, wt := startSingle(t, e)
	if _, err := e.app.DisableProject(e.ctx, e.cmd, e.project); err != nil {
		t.Fatal(err)
	}
	if d := e.check("claude-X", wt); d.Decision != domain.DecisionDeny {
		t.Fatalf("ACT-07: draining project must keep denying: %+v", d)
	}
	e.reconcile(1)
	var drain string
	_ = e.db.Read(e.ctx, func(q sqlite.Q) error {
		return q.QueryRowContext(e.ctx, "SELECT drain_state FROM projects WHERE id=?", e.project).Scan(&drain)
	})
	if drain != domain.DrainDraining {
		t.Fatalf("ACT-08: tool may still run → DRAINING, got %s", drain)
	}
	_ = e.app.ObserveSession(e.ctx, app.ObserveRequest{HookIdentity: app.HookIdentity{ClaudeSessionID: "claude-X", Event: "Stop"}})
	e.reconcile(1)
	_ = e.db.Read(e.ctx, func(q sqlite.Q) error {
		return q.QueryRowContext(e.ctx, "SELECT drain_state FROM projects WHERE id=?", e.project).Scan(&drain)
	})
	if drain != domain.DrainComplete {
		t.Fatalf("drain should complete after quiescence, got %s", drain)
	}
	// Results and history are preserved.
	if att := e.attempt(issue); att == nil {
		t.Fatal("history lost")
	}
	// New runs are refused while disabled.
	if _, err := e.app.ApplyPlan(e.ctx, e.cmd, json.RawMessage(strings.ReplaceAll(strings.ReplaceAll(singlePlan, "PROJECT", e.project), `"0"`, `"1"`))); err == nil {
		t.Fatal("disabled project must refuse new plans")
	}
}

func TestStopAndToolObservationsNeverComplete(t *testing.T) {
	e := newEnv(t, baseConfig)
	issue, _ := startSingle(t, e)
	for _, ev := range []string{"PostToolUse", "Stop", "SubagentStop", "Notification"} {
		if err := e.app.ObserveSession(e.ctx, app.ObserveRequest{HookIdentity: app.HookIdentity{ClaudeSessionID: "claude-X", Event: ev}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.app.FinishSession(e.ctx, app.FinishRequest{HookIdentity: app.HookIdentity{ClaudeSessionID: "claude-X", Event: "SessionEnd"}}); err != nil {
		t.Fatal(err)
	}
	e.reconcile(1)
	att := e.attempt(issue)
	if att.State != domain.AttemptRunning {
		t.Fatalf("POL-14: observations must not change completion, got %s", att.State)
	}
	if v := e.issueView(issue); v.Lifecycle != domain.IssueOpen {
		t.Fatalf("issue completed by observation: %s", v.Lifecycle)
	}
}

func TestStaleWorkerCannotSubmit(t *testing.T) {
	e := newEnv(t, baseConfig)
	issue, wt := startSingle(t, e)
	e.commitIn(wt, "f.txt", "x")
	ticket := e.fake.SessionByAttempt(e.attempt(issue).ID).Spec.Env["SAMSARA_LAUNCH_TICKET"]
	if _, err := e.app.SubmitOutput(e.ctx, e.cmd, app.OutputSubmitRequest{Ticket: ticket, Fence: "999"}); !domain.IsCode(err, domain.CodeStaleFence) {
		t.Fatalf("INV-03: stale fence submission accepted: %v", err)
	}
	_, _ = e.app.PauseRun(e.ctx, e.cmd, e.attempt(issue).RunID)
	if _, err := e.app.SubmitOutput(e.ctx, e.cmd, app.OutputSubmitRequest{Ticket: ticket}); !domain.IsCode(err, domain.CodeStaleFence) {
		t.Fatalf("revoked worker submission accepted: %v", err)
	}
}

func TestConcurrentDecisionsIfMatch(t *testing.T) {
	e := newEnv(t, strings.Replace(baseConfig, "implementation_start: auto", "implementation_start: manual", 1))
	res := e.applyPlan(singlePlan)
	out, _ := e.app.StartRun(e.ctx, e.cmd, res.RunID)
	ap, err := e.app.Lookup(e.ctx, sqlite.KindApproval, out.ApprovalID)
	if err != nil {
		t.Fatal(err)
	}
	v := ap.(sqlite.ApprovalView).Version
	c1, c2 := e.cmd, e.cmd
	c1.IfMatch, c2.IfMatch = v, v
	c1.IdempotencyKey, c2.IdempotencyKey = "a", "b"
	if _, err := e.app.DecideApproval(e.ctx, c1, out.ApprovalID, app.DecisionRequest{Decision: "APPROVE"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.app.DecideApproval(e.ctx, c2, out.ApprovalID, app.DecisionRequest{Decision: "REJECT"}); !domain.IsCode(err, domain.CodeVersionMismatch) {
		t.Fatalf("POL-10: second decision must fail with VERSION_MISMATCH, got %v", err)
	}
	if _, err := e.app.DecideApproval(e.ctx, e.cmd, out.ApprovalID, app.DecisionRequest{Decision: "APPROVE", SubjectDigest: "sha256:other"}); !domain.IsCode(err, domain.CodeApprovalSubjectChanged) {
		t.Fatalf("digest mismatch must be refused, got %v", err)
	}
}

func TestEventsReplayAndCursorScope(t *testing.T) {
	e := newEnv(t, baseConfig)
	snap, err := e.app.ProjectSnapshot(e.ctx, e.project)
	if err != nil {
		t.Fatal(err)
	}
	cur, _ := app.DecodeCursor(snap.SnapshotCursor)
	res := e.applyPlan(singlePlan)
	_, _ = e.app.StartRun(e.ctx, e.cmd, res.RunID)
	b, err := e.app.ReadProjectEvents(e.ctx, e.project, cur, 100)
	if err != nil || len(b.Events) < 2 {
		t.Fatalf("replay: %v %+v", err, b)
	}
	prev := snap.ProjectVersion
	for _, ev := range b.Events {
		if ev.PreviousProjectVersion != prev {
			t.Fatalf("EVT-06: chain broken %s != %s", ev.PreviousProjectVersion, prev)
		}
		prev = ev.ProjectVersion
	}
	// Snapshot after the events reports the same version as the last event.
	s2, _ := e.app.ProjectSnapshot(e.ctx, e.project)
	if s2.ProjectVersion != prev {
		t.Fatalf("snapshot %s vs events %s", s2.ProjectVersion, prev)
	}
	other := cur
	other.Scope = "prj_other"
	if b, _ := e.app.ReadProjectEvents(e.ctx, e.project, other, 10); b.ResetRequired == "" {
		t.Fatal("EVT-13: foreign cursor must reset")
	}
	gen := cur
	gen.DBGen = "dbgen_old"
	if b, _ := e.app.ReadProjectEvents(e.ctx, e.project, gen, 10); b.ResetRequired != "DATABASE_GENERATION_CHANGED" {
		t.Fatal("EVT-11: old database generation must reset")
	}
}

func TestUnmanagedSessionIsObservedOnly(t *testing.T) {
	e := newEnv(t, baseConfig)
	r, err := e.app.RegisterSession(context.Background(), app.HookIdentity{ClaudeSessionID: "plain", Cwd: e.repo})
	if err != nil {
		t.Fatal(err)
	}
	if r.Managed || r.LeaseID != "" {
		t.Fatalf("ordinary session must not become a worker: %+v", r)
	}
	if d := e.check("plain", e.repo); d.ReasonCode != domain.ReasonSessionNotRegistered {
		// with managed hint the unmanaged session is denied; without it passes
		t.Fatalf("managed-hint on unmanaged session: %+v", d)
	}
	d, _ := e.app.CheckTool(e.ctx, app.ToolCheckRequest{HookIdentity: app.HookIdentity{ClaudeSessionID: "plain", Cwd: e.repo}, ToolName: "Edit"})
	if d.Decision != domain.DecisionPass || d.ReasonCode != domain.ReasonUnmanaged {
		t.Fatalf("unmanaged pass: %+v", d)
	}
}

func TestStandaloneManualAttach(t *testing.T) {
	cfg := strings.Replace(baseConfig, "mode: auto", "mode: standalone", 1)
	e := newEnv(t, cfg)
	res := e.applyPlan(singlePlan)
	if _, err := e.app.StartRun(e.ctx, e.cmd, res.RunID); err != nil {
		t.Fatal(err)
	}
	e.reconcile(3)
	issue := res.IssueMapping["x"]
	att := e.attempt(issue)
	if att.State != domain.AttemptPending || att.StateReason != "MANUAL_ATTACH_REQUIRED" {
		t.Fatalf("BE-03/BE-13: standalone waits for the user, got %s/%s", att.State, att.StateReason)
	}
	if len(e.fake.CallsOf("launch")) != 0 {
		t.Fatal("standalone must not create workers")
	}
	if v := e.issueView(issue); v.DisplayStatus != "READY" {
		t.Fatalf("display: %s %+v", v.DisplayStatus, v.BlockingReasons)
	}
	wt, err := e.app.Lookup(e.ctx, sqlite.KindWorktree, att.WorktreeID)
	if err != nil {
		t.Fatal(err)
	}
	path := wt.(sqlite.WorktreeView).Path
	// The user starts Claude in the prepared worktree: SessionStart observes only.
	r, err := e.app.RegisterSession(e.ctx, app.HookIdentity{ClaudeSessionID: "user-1", Cwd: path, Process: selfProcess()})
	if err != nil || r.Managed {
		t.Fatalf("observation registration: %+v %v", r, err)
	}
	if d := e.check("user-1", path); d.ReasonCode != domain.ReasonSessionNotRegistered {
		t.Fatalf("not attached yet: %+v", d)
	}
	ar, err := e.app.AttachSession(e.ctx, e.cmd, app.AttachRequest{ClaudeSessionID: "user-1", IssueID: issue})
	if err != nil || ar.LeaseID == "" {
		t.Fatalf("attach: %+v %v", ar, err)
	}
	if d := e.check("user-1", path, "a.txt"); d.Decision != domain.DecisionPass {
		t.Fatalf("attached session allowed: %+v", d)
	}
	if d := e.check("user-1", e.repo); d.ReasonCode != domain.ReasonCwdOutOfScope {
		t.Fatalf("user checkout is outside the attempt worktree: %+v", d)
	}
	// Standalone cannot receive automated prompts.
	if _, err := e.app.FocusSession(e.ctx, ar.SessionID); err != nil {
		t.Fatalf("focus instruction: %v", err)
	}
}
