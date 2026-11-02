package app_test

import (
	"strings"

	"github.com/Mineru98/samsara/runtime/internal/app"
	"testing"
	"time"
)

// A launcher that types the worker command into a shell (herdr) never gets
// issue text in argv; the runtime sends it as the first dispatch once the
// worker registered and holds a lease (01 §7.2 step 9).
func TestDeferredInitialPromptIsDispatchedAfterRegistration(t *testing.T) {
	e := newEnv(t, baseConfig)
	e.fake.DeferPrompt = true
	issueID, _ := startSingle(t, e)
	s := e.fake.SessionByAttempt(e.attempt(issueID).ID)
	if s.Spec.Prompt == "" || strings.Contains(strings.Join(s.Spec.Argv, "\x00"), s.Spec.Prompt) {
		t.Fatalf("prompt must travel outside argv: prompt=%q argv=%q", s.Spec.Prompt, s.Spec.Argv)
	}
	e.reconcile(2)
	if n := len(e.fake.CallsOf("submit")); n != 1 {
		t.Fatalf("expected one initial dispatch, got %d", n)
	}
	if len(s.Prompts) != 1 || !strings.Contains(s.Prompts[0], "삼사라 작업: X") || !strings.Contains(s.Prompts[0], "[samsara-dispatch:") {
		t.Fatalf("initial prompt: %q", s.Prompts)
	}
}

// The worker can register before its launch receipt (and so its binding) is
// recorded; the initial dispatch must survive that and arrive exactly once.
func TestDeferredInitialPromptSurvivesRegistrationBeforeBinding(t *testing.T) {
	e := newEnv(t, baseConfig)
	e.fake.DeferPrompt, e.fake.LoseLaunchAck = true, true
	res := e.applyPlan(singlePlan)
	if _, err := e.app.StartRun(e.ctx, e.cmd, res.RunID); err != nil {
		t.Fatal(err)
	}
	e.reconcile(3)
	issueID := res.IssueMapping["x"]
	if r := e.register(issueID, "claude-X"); r.LeaseID == "" {
		t.Fatalf("no lease: %+v", r)
	}
	s := e.fake.SessionByAttempt(e.attempt(issueID).ID)
	for i := 0; i < 6; i++ {
		e.clock.Advance(15 * time.Second)
		// the launcher heartbeat keeps the lease alive meanwhile
		if _, err := e.app.RenewLease(e.ctx, app.RenewRequest{Ticket: s.Spec.Env["SAMSARA_LAUNCH_TICKET"], AttemptID: e.attempt(issueID).ID}); err != nil {
			t.Fatal(err)
		}
		e.reconcile(2)
	}
	if len(s.Prompts) != 1 || !strings.Contains(s.Prompts[0], "삼사라 작업: X") {
		t.Fatalf("initial prompt after late binding: %q", s.Prompts)
	}
}

// An agent that is not ready yet delays the initial task; it is not dropped.
func TestDeferredInitialPromptRetriesWhileAgentNotReady(t *testing.T) {
	e := newEnv(t, baseConfig)
	e.fake.DeferPrompt, e.fake.SubmitNotReady = true, 2
	issueID, _ := startSingle(t, e)
	for i := 0; i < 4; i++ {
		e.reconcile(2)
		e.clock.Advance(11 * time.Second)
	}
	s := e.fake.SessionByAttempt(e.attempt(issueID).ID)
	if len(s.Prompts) != 1 || len(e.fake.CallsOf("submit")) != 3 {
		t.Fatalf("prompts %q after %d submits", s.Prompts, len(e.fake.CallsOf("submit")))
	}
	if e.attempt(issueID).ResumeRequired {
		t.Fatal("a delivered initial prompt must not leave resume_required")
	}
}

func TestArgvLauncherKeepsPromptWithoutExtraDispatch(t *testing.T) {
	e := newEnv(t, baseConfig)
	issueID, _ := startSingle(t, e)
	s := e.fake.SessionByAttempt(e.attempt(issueID).ID)
	if argv := s.Spec.ArgvWithPrompt(); argv[len(argv)-1] != s.Spec.Prompt || !strings.Contains(s.Spec.Prompt, "삼사라 작업: X") {
		t.Fatalf("argv launchers get the prompt as the last argument: %q", argv)
	}
	e.reconcile(2)
	if n := len(e.fake.CallsOf("submit")); n != 0 {
		t.Fatalf("no automated dispatch expected, got %d", n)
	}
}

// A launch step result recorded before a failure is handed to the retry of
// the same intent, so it can resume instead of creating a second worker.
func TestLaunchCheckpointsReachTheRetry(t *testing.T) {
	e := newEnv(t, baseConfig)
	e.fake.FailLaunchBeforeSend = true
	res := e.applyPlan(singlePlan)
	if _, err := e.app.StartRun(e.ctx, e.cmd, res.RunID); err != nil {
		t.Fatal(err)
	}
	e.reconcile(3)
	e.clock.Advance(20 * time.Second)
	e.reconcile(2)
	cps := e.fake.LaunchCheckpoints
	if len(cps) < 2 || cps[0]["fake_step"] != "" || cps[1]["fake_step"] != "1" {
		t.Fatalf("retry did not receive the first attempt's checkpoint: %v", cps)
	}
	if e.fake.SessionByAttempt(e.attempt(res.IssueMapping["x"]).ID) == nil {
		t.Fatal("retry did not launch")
	}
}

func TestConflictPromptReachesDeferredLauncher(t *testing.T) {
	e := newEnv(t, baseConfig)
	e.fake.DeferPrompt = true
	issueID, wt := startSingle(t, e)
	e.commitIn(wt, "shared.txt", "worker\n")
	e.submitAndApprove(issueID)
	oldID := e.candidates(issueID)[0].ID
	e.advanceIntegration("shared.txt", "target\n")
	if _, err := e.app.EnqueueCandidate(e.ctx, e.cmd, oldID); err != nil {
		t.Fatal(err)
	}
	e.reconcile(4)
	e.exitWorker(issueID)
	e.clock.Advance(time.Second)
	if _, err := e.app.ResolveConflict(e.ctx, e.cmd, oldID); err != nil {
		t.Fatal(err)
	}
	e.reconcile(3)
	s := e.fake.SessionByAttempt(e.attempt(issueID).ID)
	if s == nil {
		t.Fatal("resolution worker not launched")
	}
	e.register(issueID, "claude-X2")
	e.reconcile(3)
	if len(s.Prompts) != 1 {
		t.Fatalf("expected one deferred prompt, got %q", s.Prompts)
	}
	for _, want := range []string{"충돌 해결", "shared.txt (TEXT)", "refs/heads/integration"} {
		if !strings.Contains(s.Prompts[0], want) {
			t.Fatalf("deferred prompt lacks %q: %s", want, s.Prompts[0])
		}
	}
}
