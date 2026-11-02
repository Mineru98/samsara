package app_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Mineru98/samsara/runtime/internal/app"
	"github.com/Mineru98/samsara/runtime/internal/domain"
	"github.com/Mineru98/samsara/runtime/internal/store/sqlite"
)

// advanceIntegration commits file=content on refs/heads/integration through
// a temporary worktree that is removed again (a checked-out target is not
// a valid local CAS target).
func (e *env) advanceIntegration(file, content string) string {
	e.t.Helper()
	tmp := filepath.Join(e.t.TempDir(), "integ")
	git(e.t, e.repo, "worktree", "add", "-q", tmp, "integration")
	e.commitIn(tmp, file, content)
	git(e.t, e.repo, "worktree", "remove", tmp)
	return git(e.t, e.repo, "rev-parse", "refs/heads/integration")
}

// submitAndApprove submits the attempt's worktree HEAD, verifies it and
// approves the RESULT, which creates the merge candidate.
func (e *env) submitAndApprove(issueID string) {
	e.t.Helper()
	ticket := e.fake.SessionByAttempt(e.attempt(issueID).ID).Spec.Env["SAMSARA_LAUNCH_TICKET"]
	if _, err := e.app.SubmitOutput(e.ctx, e.cmd, app.OutputSubmitRequest{Ticket: ticket}); err != nil {
		e.t.Fatal(err)
	}
	e.reconcile(2)
	var apID string
	_ = e.db.Read(e.ctx, func(q sqlite.Q) error {
		aps, _ := sqlite.ApprovalsWhere(e.ctx, q, "kind='RESULT' AND issue_id=? AND state='PENDING'", issueID)
		if len(aps) > 0 {
			apID = aps[len(aps)-1].ID
		}
		return nil
	})
	if apID == "" {
		e.t.Fatalf("no pending RESULT approval for %s (attempt %s)", issueID, e.attempt(issueID).State)
	}
	if _, err := e.app.DecideApproval(e.ctx, e.cmd, apID, app.DecisionRequest{Decision: "APPROVE"}); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) candidates(issueID string) []*sqlite.Candidate {
	e.t.Helper()
	var cs []*sqlite.Candidate
	_ = e.db.Read(e.ctx, func(q sqlite.Q) error {
		cs, _ = sqlite.CandidatesWhere(e.ctx, q, "issue_id=?", issueID)
		return nil
	})
	return cs
}

func (e *env) candidate(id string) (*sqlite.Candidate, *sqlite.CandidateView) {
	e.t.Helper()
	var c *sqlite.Candidate
	var v *sqlite.CandidateView
	if err := e.db.Read(e.ctx, func(q sqlite.Q) error {
		var err error
		if c, err = sqlite.GetCandidate(e.ctx, q, id); err != nil {
			return err
		}
		v, err = sqlite.CandidateProjection(e.ctx, q, id)
		return err
	}); err != nil {
		e.t.Fatal(err)
	}
	return c, v
}

// 04 §12: a Git-confirmed conflict is classified, the same source/base is
// not recomputed, a moved base is re-validated as a new revision, and the
// resolution attempt's new source is validated and merged on the latest base.
func TestConflictResolutionRevalidatesOnLatestBase(t *testing.T) {
	e := newEnv(t, baseConfig)
	issueID, wt := startSingle(t, e)
	e.commitIn(wt, "shared.txt", "worker\n")
	e.submitAndApprove(issueID)
	cs := e.candidates(issueID)
	if len(cs) != 1 {
		t.Fatalf("candidates: %d", len(cs))
	}
	oldID := cs[0].ID
	base1 := e.advanceIntegration("shared.txt", "target\n")
	if _, err := e.app.EnqueueCandidate(e.ctx, e.cmd, oldID); err != nil {
		t.Fatal(err)
	}
	e.reconcile(4)
	c, v := e.candidate(oldID)
	if c.State != domain.CandidateConflict || c.BaseSHA != base1 {
		t.Fatalf("expected CONFLICT on %s, got %s base %s", base1, c.State, c.BaseSHA)
	}
	if v.Conflict == nil || v.Conflict.Source != "git_merge_tree" || len(v.Conflict.Kinds) != 1 || v.Conflict.Kinds[0] != "TEXT" ||
		!strings.Contains(string(v.Conflict.Files[0]), "shared.txt") {
		t.Fatalf("conflict view: %+v", v.Conflict)
	}

	// 12.3: same source/base is a deterministic conflict: not re-queued.
	if _, err := e.app.EnqueueCandidate(e.ctx, e.cmd, oldID); !domain.IsCode(err, domain.CodeConflict) {
		t.Fatalf("re-enqueue on the same base must be refused: %v", err)
	}
	// A moved base is re-validated as a new revision (still conflicting).
	base2 := e.advanceIntegration("other.txt", "unrelated\n")
	if _, err := e.app.EnqueueCandidate(e.ctx, e.cmd, oldID); err != nil {
		t.Fatalf("re-enqueue after the base moved: %v", err)
	}
	e.reconcile(4)
	c, v = e.candidate(oldID)
	if c.State != domain.CandidateConflict || c.Revision != 2 || c.BaseSHA != base2 || v.RevalidationCount != "1" {
		t.Fatalf("revision 2 on %s expected: %s rev %d base %s count %s", base2, c.State, c.Revision, c.BaseSHA, v.RevalidationCount)
	}

	// 12.2: the resolution attempt never shares the worktree with a live worker.
	if _, err := e.app.ResolveConflict(e.ctx, e.cmd, oldID); !domain.IsCode(err, domain.CodeConflict) {
		t.Fatalf("resolve with a live session must be refused: %v", err)
	}
	e.exitWorker(issueID)
	firstAttempt := e.attempt(issueID)
	e.clock.Advance(time.Second)
	if _, err := e.app.ResolveConflict(e.ctx, e.cmd, oldID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.app.ResolveConflict(e.ctx, e.cmd, oldID); !domain.IsCode(err, domain.CodeConflict) {
		t.Fatalf("a second resolution while one is queued must be refused: %v", err)
	}
	// While the resolution is pending, the old candidate is not re-queued
	// even if the base moves (its replacement is on the way).
	e.advanceIntegration("meanwhile.txt", "m\n")
	if _, err := e.app.EnqueueCandidate(e.ctx, e.cmd, oldID); !domain.IsCode(err, domain.CodeConflict) {
		t.Fatalf("enqueue during a pending resolution must be refused: %v", err)
	}
	e.reconcile(3)
	res := e.attempt(issueID)
	if res.ID == firstAttempt.ID || res.WorktreeID != firstAttempt.WorktreeID {
		t.Fatalf("resolution attempt must reuse the retained worktree: %+v", res)
	}
	launch := e.fake.SessionByAttempt(res.ID)
	if launch == nil {
		t.Fatalf("resolution worker not launched (%s %s)", res.State, res.StateReason)
	}
	prompt := launch.Spec.Argv[len(launch.Spec.Argv)-1]
	if !strings.Contains(prompt, "충돌 해결") || !strings.Contains(prompt, "shared.txt (TEXT)") || !strings.Contains(prompt, "refs/heads/integration") {
		t.Fatalf("prompt lacks conflict guidance:\n%s", prompt)
	}
	e.register(issueID, "claude-X2")
	// The worker merges the latest target and resolves the conflict.
	m := exec.Command("git", "-C", wt, "merge", "--no-edit", "refs/heads/integration")
	m.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	_ = m.Run()
	if err := os.WriteFile(filepath.Join(wt, "shared.txt"), []byte("resolved\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, wt, "add", "shared.txt")
	git(t, wt, "commit", "-q", "--no-edit")
	e.submitAndApprove(issueID)

	old, _ := e.candidate(oldID)
	if old.State != domain.CandidateCancelled || old.StateReason != "SUPERSEDED" || old.SupersededBy == "" {
		t.Fatalf("conflicted candidate must be superseded: %+v", old)
	}
	nw, _ := e.candidate(old.SupersededBy)
	if nw.QueuePosition != old.QueuePosition || nw.SourceSHA == old.SourceSHA {
		t.Fatalf("new candidate must keep the slot with a new source: %+v", nw)
	}
	// The latest base moves once more before integration.
	base3 := e.advanceIntegration("late.txt", "late\n")
	if _, err := e.app.EnqueueCandidate(e.ctx, e.cmd, nw.ID); err != nil {
		t.Fatal(err)
	}
	e.reconcile(4)
	nw, _ = e.candidate(nw.ID)
	if nw.State != domain.CandidateMerged || nw.BaseSHA != base3 {
		t.Fatalf("resolved candidate must merge on the latest base %s: %s (%s) base %s", base3, nw.State, nw.StateReason, nw.BaseSHA)
	}
	if got := git(t, e.repo, "rev-parse", "refs/heads/integration"); got != nw.CandidateSHA {
		t.Fatalf("integration %s != candidate %s", got, nw.CandidateSHA)
	}
	if got := git(t, e.repo, "show", "refs/heads/integration:shared.txt"); got != "resolved" {
		t.Fatalf("integrated content %q", got)
	}
}

const queuePlan = `{"spec_version":"1","project_id":"PROJECT","expected_graph_version":"0",
 "issues":[{"client_key":"a","title":"A"},{"client_key":"c","title":"C"},{"client_key":"b","title":"B"}],
 "dependencies":[{"predecessor_key":"a","successor_key":"b","required_milestone":"RESULT_APPROVED","downstream_phase":"EXECUTION"}]}`

// 04 §8.3: reordering records a reason, never puts a successor ahead of its
// predecessor, and shows waiting time and re-validation counts.
func TestPrioritizeRespectsDependencies(t *testing.T) {
	e := newEnv(t, baseConfig)
	res := e.applyPlan(queuePlan)
	A, B, C := res.IssueMapping["a"], res.IssueMapping["b"], res.IssueMapping["c"]
	if _, err := e.app.StartRun(e.ctx, e.cmd, res.RunID); err != nil {
		t.Fatal(err)
	}
	e.reconcile(3)
	for _, is := range []struct{ id, claude string }{{A, "claude-A"}, {C, "claude-C"}} {
		e.register(is.id, is.claude)
		e.commitIn(e.fake.SessionByAttempt(e.attempt(is.id).ID).Spec.Dir, is.claude+".txt", "x\n")
		e.submitAndApprove(is.id)
	}
	e.reconcile(3)
	e.register(B, "claude-B")
	e.commitIn(e.fake.SessionByAttempt(e.attempt(B).ID).Spec.Dir, "b.txt", "b\n")
	e.submitAndApprove(B)
	ca, cb, cc := e.candidates(A)[0], e.candidates(B)[0], e.candidates(C)[0]
	if !(ca.QueuePosition < cc.QueuePosition && cc.QueuePosition < cb.QueuePosition) {
		t.Fatalf("initial order a,c,b expected: %d %d %d", ca.QueuePosition, cc.QueuePosition, cb.QueuePosition)
	}
	if _, err := e.app.PrioritizeCandidate(e.ctx, e.cmd, cc.ID, app.PrioritizeRequest{}); !domain.IsCode(err, domain.CodeInvalidRequest) {
		t.Fatalf("a reason is required: %v", err)
	}
	if _, err := e.app.PrioritizeCandidate(e.ctx, e.cmd, cb.ID, app.PrioritizeRequest{Reason: "hotfix"}); !domain.IsCode(err, domain.CodeConflict) {
		t.Fatalf("successor ahead of predecessor must be refused: %v", err)
	}
	if _, err := e.app.PrioritizeCandidate(e.ctx, e.cmd, ca.ID, app.PrioritizeRequest{Before: cb.ID, Reason: "noop"}); err != nil {
		t.Fatalf("keeping the predecessor first is allowed: %v", err)
	}
	if _, err := e.app.PrioritizeCandidate(e.ctx, e.cmd, cc.ID, app.PrioritizeRequest{Reason: "release blocker"}); err != nil {
		t.Fatal(err)
	}
	pa, _ := e.candidate(ca.ID)
	pb, _ := e.candidate(cb.ID)
	pc, vc := e.candidate(cc.ID)
	if !(pc.QueuePosition < pa.QueuePosition && pa.QueuePosition < pb.QueuePosition) {
		t.Fatalf("order c,a,b expected: a=%d b=%d c=%d", pa.QueuePosition, pb.QueuePosition, pc.QueuePosition)
	}
	if vc.WaitingSince == "" || vc.RevalidationCount != "0" || vc.StarvationRisk {
		t.Fatalf("starvation fields: %+v", vc)
	}
	var actor, detail string
	_ = e.db.Read(e.ctx, func(q sqlite.Q) error {
		return q.QueryRowContext(e.ctx, "SELECT actor, detail FROM audit_entries WHERE action='merge.prioritize_detail' AND subject_id=?", cc.ID).Scan(&actor, &detail)
	})
	if actor != e.cmd.Principal.ID || !strings.Contains(detail, `"reason":"release blocker"`) {
		t.Fatalf("reason and actor must be audited, got %q %q", actor, detail)
	}
}
