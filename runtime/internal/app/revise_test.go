package app_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Mineru98/samsara/runtime/internal/app"
	"github.com/Mineru98/samsara/runtime/internal/domain"
	"github.com/Mineru98/samsara/runtime/internal/plan"
	"github.com/Mineru98/samsara/runtime/internal/store/sqlite"
	"github.com/Mineru98/samsara/runtime/internal/transport/ipc"
)

// revisedABC is abcPlan with A's acceptance criteria changed (execution
// meaning) or only C's title changed (metadata).
func revisedABC(graphVersion, aCriteria, cTitle string) string {
	p := strings.ReplaceAll(abcPlan, `"expected_graph_version": "0"`, `"expected_graph_version": "`+graphVersion+`"`)
	p = strings.ReplaceAll(p, `["a works"]`, `["`+aCriteria+`"]`)
	return strings.ReplaceAll(p, `"title": "C"`, `"title": "`+cTitle+`"`)
}

func (e *env) revise(runID, gen, planJSON string) (*app.PlanReviseResult, error) {
	e.t.Helper()
	body, err := json.Marshal(plan.Revision{RunID: runID, ExpectedRunGeneration: gen,
		Plan: json.RawMessage(strings.ReplaceAll(planJSON, "PROJECT", e.project))})
	if err != nil {
		e.t.Fatal(err)
	}
	return e.app.RevisePlan(e.ctx, e.cmd, body)
}

func (e *env) approvals(where string, args ...any) []*sqlite.Approval {
	e.t.Helper()
	var out []*sqlite.Approval
	if err := e.db.Read(e.ctx, func(q sqlite.Q) error {
		var err error
		out, err = sqlite.ApprovalsWhere(e.ctx, q, where, args...)
		return err
	}); err != nil {
		e.t.Fatal(err)
	}
	return out
}

func (e *env) output(id string) *sqlite.Output {
	e.t.Helper()
	var o *sqlite.Output
	if err := e.db.Read(e.ctx, func(q sqlite.Q) error {
		var err error
		o, err = sqlite.GetOutput(e.ctx, q, id)
		return err
	}); err != nil {
		e.t.Fatal(err)
	}
	return o
}

// TestRevisePausedRun covers PLN-04 with an explicit pause → revise → resume:
// unaffected work keeps its attempt and regains its lease, approvals of the
// old generation are never reused, and finished runs stay HISTORICAL.
func TestRevisePausedRun(t *testing.T) {
	e := newEnv(t, baseConfig)

	// A finished run whose historical snapshot must not change.
	old := e.applyPlan(`{"spec_version": "1", "project_id": "PROJECT", "expected_graph_version": "0",
		"issues": [{"client_key": "old", "title": "Old", "acceptance_criteria": ["x"]}], "dependencies": []}`)
	if _, err := e.app.CancelRun(e.ctx, e.cmd, old.RunID); err != nil {
		t.Fatal(err)
	}
	histBefore, err := e.app.RunSnapshot(e.ctx, old.RunID)
	if err != nil {
		t.Fatal(err)
	}

	res := e.applyPlan(strings.ReplaceAll(abcPlan, `"expected_graph_version": "0"`, `"expected_graph_version": "1"`))
	A, B, C := res.IssueMapping["a"], res.IssueMapping["b"], res.IssueMapping["c"]
	if _, err := e.app.StartRun(e.ctx, e.cmd, res.RunID); err != nil {
		t.Fatal(err)
	}
	e.reconcile(3)
	e.register(A, "claude-A")
	e.register(C, "claude-C")
	wtA := e.fake.SessionByAttempt(e.attempt(A).ID).Spec.Dir
	wtC := e.fake.SessionByAttempt(e.attempt(C).ID).Spec.Dir
	e.commitIn(wtA, "a.txt", "A\n")
	out, err := e.app.SubmitOutput(e.ctx, e.cmd, app.OutputSubmitRequest{Ticket: e.fake.SessionByAttempt(e.attempt(A).ID).Spec.Env["SAMSARA_LAUNCH_TICKET"]})
	if err != nil {
		t.Fatal(err)
	}
	e.reconcile(2)
	resultAp := e.approvals("kind='RESULT' AND issue_id=? AND state='PENDING'", A)
	if len(resultAp) != 1 {
		t.Fatalf("A RESULT approval missing: %+v", resultAp)
	}
	if _, err := e.app.DecideApproval(e.ctx, e.cmd, resultAp[0].ID, app.DecisionRequest{Decision: "APPROVE"}); err != nil {
		t.Fatal(err)
	}

	// The general apply and revise are refused while the run is ACTIVE.
	if _, err := e.app.ApplyPlan(e.ctx, e.cmd, json.RawMessage(strings.ReplaceAll(revisedABC("2", "a v2", "C"), "PROJECT", e.project))); !domain.IsCode(err, domain.CodeConflict) {
		t.Fatalf("apply on an unfinished run must be CONFLICT, got %v", err)
	}
	if _, err := e.revise(res.RunID, "1", revisedABC("2", "a v2", "C")); !domain.IsCode(err, domain.CodeConflict) {
		t.Fatalf("revise on ACTIVE run must be CONFLICT, got %v", err)
	}

	cAttempt := e.attempt(C).ID
	if _, err := e.app.PauseRun(e.ctx, e.cmd, res.RunID); err != nil {
		t.Fatal(err)
	}
	// C's tool may still be in flight: not quiescent until stop evidence.
	if _, err := e.revise(res.RunID, "1", revisedABC("2", "a v2", "C")); !domain.IsCode(err, domain.CodeConflict) || !strings.Contains(err.Error(), "quiescent") {
		t.Fatalf("revise before stop evidence must be refused, got %v", err)
	}
	if err := e.app.ObserveSession(e.ctx, app.ObserveRequest{HookIdentity: app.HookIdentity{ClaudeSessionID: "claude-C", Event: "Stop"}}); err != nil {
		t.Fatal(err)
	}
	if got := e.attempt(C).State; got != domain.AttemptPaused {
		t.Fatalf("C should be PAUSED, got %s", got)
	}

	// Stale generation / graph version and issue removal are refused.
	if _, err := e.revise(res.RunID, "0", revisedABC("2", "a v2", "C")); !domain.IsCode(err, domain.CodeVersionMismatch) {
		t.Fatalf("stale run generation must be VERSION_MISMATCH, got %v", err)
	}
	if _, err := e.revise(res.RunID, "1", revisedABC("1", "a v2", "C")); !domain.IsCode(err, domain.CodeVersionMismatch) {
		t.Fatalf("stale graph version must be VERSION_MISMATCH, got %v", err)
	}
	removed := strings.Replace(revisedABC("2", "a works", "C"), `,
    {"client_key": "c", "title": "C", "acceptance_criteria": ["c works"]}`, "", 1)
	if _, err := e.revise(res.RunID, "1", removed); !domain.IsCode(err, domain.CodeInvalidRequest) {
		t.Fatalf("removing an issue must be INVALID_REQUEST, got %v", err)
	}

	// Title-only change: metadata, generation unchanged.
	meta, err := e.revise(res.RunID, "1", revisedABC("2", "a works", "C renamed"))
	if err != nil {
		t.Fatal(err)
	}
	if meta.ChangeKind != plan.ChangeMetadataOnly || meta.RunGeneration != "1" || meta.GraphVersion != "2" || len(meta.InvalidatedApprovalIDs) != 0 {
		t.Fatalf("metadata revise: %+v", meta)
	}
	if v := e.issueView(C); v.Title != "C renamed" {
		t.Fatalf("title not updated: %s", v.Title)
	}
	if got := e.approvals("kind='RESULT' AND issue_id=? AND state='APPROVED'", A); len(got) != 1 {
		t.Fatalf("metadata revise must keep approvals: %+v", got)
	}

	// Execution change: A's acceptance criteria → A and its descendant B.
	e.clock.Advance(time.Second)
	rv, err := e.revise(res.RunID, "1", revisedABC("2", "a v2", "C renamed"))
	if err != nil {
		t.Fatal(err)
	}
	if rv.ChangeKind != plan.ChangeExecution || rv.RunGeneration != "2" || rv.GraphVersion != "3" || rv.RunState != domain.RunPaused {
		t.Fatalf("execution revise: %+v", rv)
	}
	if !reflect.DeepEqual(sortedCopy(rv.AffectedIssueIDs), sortedCopy([]string{A, B})) {
		t.Fatalf("affected = %v, want A,B", rv.AffectedIssueIDs)
	}
	// Outputs are kept and recorded as superseded, never deleted.
	if o := e.output(out.OutputID); !strings.HasPrefix(o.SupersededBy, "PLAN_REVISED") {
		t.Fatalf("A output must be superseded, got %q", o.SupersededBy)
	}
	// Unaffected C keeps its attempt and moves to the new generation.
	if att := e.attempt(C); att.ID != cAttempt || att.RunGeneration != 2 || att.State != domain.AttemptPaused {
		t.Fatalf("C attempt must be kept: %+v", att)
	}
	// A gets a fresh attempt of generation 2.
	if att := e.attempt(A); att.RunGeneration != 2 || att.State != domain.AttemptPending {
		t.Fatalf("A must be re-queued in generation 2: %+v", att)
	}

	// POL-05/POL-07: the old RESULT and PLAN approvals are not reusable.
	if got := e.approvals("run_id=? AND state IN ('PENDING','APPROVED') AND run_generation<>2", res.RunID); len(got) != 0 {
		t.Fatalf("old-generation approvals still usable: %+v", got)
	}
	if _, err := e.app.DecideApproval(e.ctx, e.cmd, resultAp[0].ID, app.DecisionRequest{Decision: "REVOKE"}); err == nil {
		t.Fatal("deciding an old-generation approval must fail")
	}
	if bl := e.issueView(B).BlockingReasons; len(bl) == 0 || bl[0].Code != "DEPENDENCY_NOT_IMPLEMENTED" {
		t.Fatalf("B must wait for A's new result, got %+v", bl)
	}
	if plans := e.approvals("kind='PLAN' AND run_id=? AND run_generation=2 AND state='APPROVED'", res.RunID); len(plans) != 1 {
		t.Fatalf("new PLAN approval (auto) missing: %+v", plans)
	}
	// A stale revise after the bump is refused.
	if _, err := e.revise(res.RunID, "1", revisedABC("3", "a v3", "C renamed")); !domain.IsCode(err, domain.CodeVersionMismatch) {
		t.Fatalf("old-generation revise must be refused, got %v", err)
	}

	// Resume: C regains a lease on the same attempt and may use tools again.
	if _, err := e.app.ResumeRun(e.ctx, e.cmd, res.RunID); err != nil {
		t.Fatal(err)
	}
	e.reconcile(3)
	l, err := e.leaseOf(C)
	if err != nil || l == nil || l.State != domain.LeaseActive || l.OwnerAttemptID != cAttempt {
		t.Fatalf("C lease after resume: %+v %v", l, err)
	}
	if d := e.check("claude-C", wtC, "c.txt"); d.Decision != domain.DecisionPass {
		t.Fatalf("C must run after resume: %+v", d)
	}
	if att := e.attempt(A); att.RunGeneration != 2 || att.State != domain.AttemptStarting {
		t.Fatalf("A's generation-2 attempt must be scheduled after resume: %+v", att)
	}

	histAfter, err := e.app.RunSnapshot(e.ctx, old.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if mustMarshal(t, histBefore) != mustMarshal(t, histAfter) {
		t.Fatalf("HISTORICAL snapshot changed:\n%s\n%s", mustMarshal(t, histBefore), mustMarshal(t, histAfter))
	}
}

// TestRevisePlanApprovalRequiredOnResume: with a manual PLAN policy the
// revision needs a new PLAN decision before the run may resume.
func TestRevisePlanApprovalRequiredOnResume(t *testing.T) {
	e := newEnv(t, strings.Replace(baseConfig, "implementation_start: auto", "implementation_start: manual", 1))
	res := e.applyPlan(abcPlan)
	if _, err := e.app.StartRun(e.ctx, e.cmd, res.RunID); !domain.IsCode(err, domain.CodeApprovalRequired) {
		t.Fatalf("start must need PLAN approval, got %v", err)
	}
	first := e.approvals("kind='PLAN' AND run_id=? AND state='PENDING'", res.RunID)
	if _, err := e.app.DecideApproval(e.ctx, e.cmd, first[0].ID, app.DecisionRequest{Decision: "APPROVE"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.app.StartRun(e.ctx, e.cmd, res.RunID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.app.PauseRun(e.ctx, e.cmd, res.RunID); err != nil {
		t.Fatal(err)
	}
	// A paused, never-revised run resumes with its existing approval.
	if _, err := e.app.ResumeRun(e.ctx, e.cmd, res.RunID); err != nil {
		t.Fatalf("resume with current PLAN approval: %v", err)
	}
	if _, err := e.app.PauseRun(e.ctx, e.cmd, res.RunID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.revise(res.RunID, "1", revisedABC("1", "a v2", "C")); err != nil {
		t.Fatal(err)
	}
	if _, err := e.app.ResumeRun(e.ctx, e.cmd, res.RunID); !domain.IsCode(err, domain.CodeApprovalRequired) {
		t.Fatalf("resume after revise must need a new PLAN approval, got %v", err)
	}
	pending := e.approvals("kind='PLAN' AND run_id=? AND run_generation=2 AND state='PENDING'", res.RunID)
	if len(pending) != 1 {
		t.Fatalf("new-generation PLAN approval missing: %+v", pending)
	}
	if _, err := e.app.DecideApproval(e.ctx, e.cmd, pending[0].ID, app.DecisionRequest{Decision: "APPROVE"}); err != nil {
		t.Fatal(err)
	}
	if r, err := e.app.ResumeRun(e.ctx, e.cmd, res.RunID); err != nil || r.State != domain.RunActive {
		t.Fatalf("resume after approval: %+v %v", r, err)
	}

	// Reordering issues and renaming one is metadata: the generation and the
	// PLAN approval stay, so resume needs no new decision.
	if _, err := e.app.PauseRun(e.ctx, e.cmd, res.RunID); err != nil {
		t.Fatal(err)
	}
	reordered := strings.Replace(strings.Replace(revisedABC("2", "a v2", "C renamed"),
		`{"client_key": "a", "title": "A", "acceptance_criteria": ["a v2"]},`, "", 1),
		`{"client_key": "c", "title": "C renamed", "acceptance_criteria": ["c works"]}`,
		`{"client_key": "c", "title": "C renamed", "acceptance_criteria": ["c works"]},
    {"client_key": "a", "title": "A", "acceptance_criteria": ["a v2"]}`, 1)
	meta, err := e.revise(res.RunID, "2", reordered)
	if err != nil {
		t.Fatal(err)
	}
	if meta.ChangeKind != plan.ChangeMetadataOnly || meta.RunGeneration != "2" {
		t.Fatalf("reorder + rename must be metadata only: %+v", meta)
	}
	if r, err := e.app.ResumeRun(e.ctx, e.cmd, res.RunID); err != nil || r.State != domain.RunActive {
		t.Fatalf("metadata revise must keep the PLAN approval: %+v %v", r, err)
	}
}

// TestReviseCarriesUnaffectedResults: verified outputs of unaffected issues
// stay current in the new generation and get a new RESULT request, except
// results the user rejected. Verification in flight blocks the revision.
func TestReviseCarriesUnaffectedResults(t *testing.T) {
	e := newEnv(t, baseConfig)
	res := e.applyPlan(`{
  "spec_version": "1", "project_id": "PROJECT", "expected_graph_version": "0",
  "issues": [
    {"client_key": "a", "title": "A", "acceptance_criteria": ["a works"]},
    {"client_key": "c", "title": "C", "acceptance_criteria": ["c works"]},
    {"client_key": "d", "title": "D", "acceptance_criteria": ["d works"]}
  ],
  "dependencies": []
}`)
	C, D := res.IssueMapping["c"], res.IssueMapping["d"]
	if _, err := e.app.StartRun(e.ctx, e.cmd, res.RunID); err != nil {
		t.Fatal(err)
	}
	e.reconcile(3)
	outs := map[string]string{}
	for _, k := range []string{"a", "c", "d"} {
		id := res.IssueMapping[k]
		e.register(id, "claude-"+k)
		s := e.fake.SessionByAttempt(e.attempt(id).ID)
		e.commitIn(s.Spec.Dir, k+".txt", k+"\n")
		o, err := e.app.SubmitOutput(e.ctx, e.cmd, app.OutputSubmitRequest{Ticket: s.Spec.Env["SAMSARA_LAUNCH_TICKET"]})
		if err != nil {
			t.Fatal(err)
		}
		outs[k] = o.OutputID
	}
	if _, err := e.app.PauseRun(e.ctx, e.cmd, res.RunID); err != nil {
		t.Fatal(err)
	}
	newPlan := `{
  "spec_version": "1", "project_id": "PROJECT", "expected_graph_version": "1",
  "issues": [
    {"client_key": "a", "title": "A", "acceptance_criteria": ["a v2"]},
    {"client_key": "c", "title": "C", "acceptance_criteria": ["c works"]},
    {"client_key": "d", "title": "D", "acceptance_criteria": ["d works"]}
  ],
  "dependencies": []
}`
	// Outputs still being verified would be superseded mid-flight.
	if _, err := e.revise(res.RunID, "1", newPlan); !domain.IsCode(err, domain.CodeConflict) || !strings.Contains(err.Error(), "quiescent") {
		t.Fatalf("revise during verification must be refused, got %v", err)
	}
	e.reconcile(2)
	for _, k := range []string{"c", "d"} {
		if got := e.attempt(res.IssueMapping[k]).State; got != domain.AttemptVerified {
			t.Fatalf("%s attempt = %s", k, got)
		}
	}
	cAp := e.approvals("kind='RESULT' AND issue_id=? AND state='PENDING'", C)
	dAp := e.approvals("kind='RESULT' AND issue_id=? AND state='PENDING'", D)
	if _, err := e.app.DecideApproval(e.ctx, e.cmd, cAp[0].ID, app.DecisionRequest{Decision: "APPROVE"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.app.DecideApproval(e.ctx, e.cmd, dAp[0].ID, app.DecisionRequest{Decision: "REJECT"}); err != nil {
		t.Fatal(err)
	}

	e.clock.Advance(time.Second)
	rv, err := e.revise(res.RunID, "1", newPlan)
	if err != nil {
		t.Fatal(err)
	}
	if len(rv.AffectedIssueIDs) != 1 || rv.AffectedIssueIDs[0] != res.IssueMapping["a"] {
		t.Fatalf("only A is affected: %+v", rv)
	}
	if o := e.output(outs["c"]); o.SupersededBy != "" || o.RunGeneration != 1 {
		t.Fatalf("C output must be carried forward: %+v", o)
	}
	var cur *sqlite.Output
	_ = e.db.Read(e.ctx, func(q sqlite.Q) error {
		cur, _ = sqlite.CurrentOutput(e.ctx, q, res.RunID, C, 2)
		return nil
	})
	if cur == nil || cur.ID != outs["c"] {
		t.Fatalf("C's generation-1 output must be current in generation 2: %+v", cur)
	}
	if got := e.approvals("kind='RESULT' AND output_id=? AND state='PENDING' AND run_generation=2", outs["c"]); len(got) != 1 {
		t.Fatalf("C needs a new generation-2 RESULT request: %+v", got)
	}
	if got := e.approvals("kind='RESULT' AND output_id=? AND state='APPROVED'", outs["c"]); len(got) != 0 {
		t.Fatalf("C's old approval must not be reused: %+v", got)
	}
	if got := e.approvals("kind='RESULT' AND output_id=? AND state IN ('PENDING','APPROVED')", outs["d"]); len(got) != 0 {
		t.Fatalf("D was rejected and must not be re-requested: %+v", got)
	}
	if o := e.output(outs["a"]); o.SupersededBy == "" {
		t.Fatal("A output must be superseded")
	}
}

// TestReviseCLIAndIPCAgree: the CLI sends the plan.Revision envelope over IPC,
// so both paths return the same error code and plan digest.
func TestReviseCLIAndIPCAgree(t *testing.T) {
	e := newEnv(t, baseConfig)
	res := e.applyPlan(abcPlan)
	if _, err := e.app.StartRun(e.ctx, e.cmd, res.RunID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.app.PauseRun(e.ctx, e.cmd, res.RunID); err != nil {
		t.Fatal(err)
	}
	srv := ipc.NewServer(e.app).Handler()
	call := func(gen, planJSON string) (int, map[string]any) {
		body, _ := json.Marshal(plan.Revision{RunID: res.RunID, ExpectedRunGeneration: gen,
			Plan: json.RawMessage(strings.ReplaceAll(planJSON, "PROJECT", e.project))})
		req := httptest.NewRequest(http.MethodPost, "/ipc/v1/plans/revise", bytes.NewReader(body))
		req.Header.Set(ipc.HeaderProtocol, ipc.ProtocolVersion)
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		var m map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &m)
		return w.Code, m
	}

	// Same invalid input: same error code on both paths.
	_, direct := e.revise(res.RunID, "9", revisedABC("1", "a v2", "C"))
	code, body := call("9", revisedABC("1", "a v2", "C"))
	ipcErr, _ := body["error"].(map[string]any)
	if direct == nil || ipcErr == nil || ipcErr["code"] != domain.AsError(direct).Code || code != domain.HTTPStatus(domain.AsError(direct).Code) {
		t.Fatalf("error mismatch: direct=%v ipc=%d %v", direct, code, body)
	}

	// Same accepted input: the IPC digest equals the plan's own digest.
	want, err := plan.Parse([]byte(strings.ReplaceAll(revisedABC("1", "a v2", "C"), "PROJECT", e.project)))
	if err != nil {
		t.Fatal(err)
	}
	code, body = call("1", revisedABC("1", "a v2", "C"))
	if code != http.StatusOK {
		t.Fatalf("ipc revise: %d %v", code, body)
	}
	v, err := e.app.ValidatePlan(e.ctx, json.RawMessage(strings.ReplaceAll(revisedABC("1", "a v2", "C"), "PROJECT", e.project)))
	if err != nil {
		t.Fatal(err)
	}
	want.Normalize("refs/heads/integration")
	if body["plan_digest"] != v.PlanDigest || body["plan_digest"] != want.Digest() || body["run_generation"] != "2" {
		t.Fatalf("digest mismatch: ipc=%v validate=%s", body["plan_digest"], v.PlanDigest)
	}
}

func sortedCopy(s []string) []string {
	c := append([]string(nil), s...)
	for i := range c {
		for j := i + 1; j < len(c); j++ {
			if c[j] < c[i] {
				c[i], c[j] = c[j], c[i]
			}
		}
	}
	return c
}

func mustMarshal(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
