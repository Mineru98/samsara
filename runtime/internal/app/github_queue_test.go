package app_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Mineru98/samsara/runtime/internal/app"
	"github.com/Mineru98/samsara/runtime/internal/domain"
	"github.com/Mineru98/samsara/runtime/internal/github"
	"github.com/Mineru98/samsara/runtime/internal/store/sqlite"
)

const queueConfig = `version: 1
backend:
  mode: auto
  order: [herdr, tmux, standalone]
  allow_fallback: true
approval:
  implementation_start: auto
  result: manual
  integration: manual
git:
  target_ref: refs/heads/integration
  integration_authority: github_queue
github:
  sync: "off"
  repository: o/r
  merge_queue:
    policy_app_id: 4242
`

// verifiedRules is a target whose merge queue builds and merges one entry
// at a time and requires samsara/integration-policy from the pinned App.
const verifiedRules = `[
	{"type":"merge_queue","parameters":{"grouping_strategy":"ALLGREEN","max_entries_to_build":1,"max_entries_to_merge":1,"merge_method":"MERGE"}},
	{"type":"required_status_checks","parameters":{"required_status_checks":[{"context":"samsara/integration-policy","integration_id":4242}]}}]`

// queueEntry is the provider view of the candidate's pull request.
type queueEntry struct {
	ID, Head, Base string
}

// queueGitHub is a scripted GitHub: REST routes plus the GraphQL merge
// queue mutation and pull request query.
type queueGitHub struct {
	mu        sync.Mutex
	rules     string
	source    string
	prState   string // OPEN, CLOSED, MERGED
	prHead    string
	entry     *queueEntry
	mergeOID  string
	enqueueOK bool
	dequeued  []string
	dequeueNo bool // refuse dequeue and keep the entry (missing permission)
	calls     []string
}

func (f *queueGitHub) Do(_ context.Context, method, path string, fields map[string]string) (*github.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, method+" "+path)
	path = strings.SplitN(path, "?", 2)[0]
	ok := func(body string) (*github.Response, error) {
		return &github.Response{Status: 200, Header: http.Header{}, Body: []byte(body)}, nil
	}
	switch {
	case method == "GET" && path == "repos/o/r/rules/branches/integration":
		return ok(f.rules)
	case method == "GET" && path == "repos/o/r/commits/"+f.source+"/pulls":
		return ok(fmt.Sprintf(`[{"number":7,"node_id":"PR_7","state":%q,"head":{"sha":%q},"base":{"ref":"integration"}}]`, strings.ToLower(f.prState), f.prHead))
	case method == "POST" && path == "graphql" && strings.Contains(fields["query"], "dequeuePullRequest"):
		f.dequeued = append(f.dequeued, fields["id"])
		if f.dequeueNo {
			return ok(`{"errors":[{"type":"FORBIDDEN","message":"Resource not accessible by integration"}]}`)
		}
		f.entry = nil
		return ok(`{"data":{"dequeuePullRequest":{"mergeQueueEntry":{"id":"MQE_1"}}}}`)
	case method == "POST" && path == "graphql" && strings.Contains(fields["query"], "enqueuePullRequest"):
		if fields["pullRequestId"] != "PR_7" || fields["expectedHeadOid"] != f.source {
			return ok(`{"errors":[{"message":"bad enqueue request"}]}`)
		}
		if !f.enqueueOK {
			return ok(`{"errors":[{"type":"UNPROCESSABLE","message":"Pull request is not mergeable"}]}`)
		}
		f.entry = &queueEntry{ID: "MQE_1"}
		return ok(`{"data":{"enqueuePullRequest":{"mergeQueueEntry":{"id":"MQE_1","state":"QUEUED"}}}}`)
	case method == "POST" && path == "graphql" && strings.Contains(fields["query"], "mergeQueueEntry"):
		entry := "null"
		if f.entry != nil {
			head, base := "null", "null"
			if f.entry.Head != "" {
				head, base = fmt.Sprintf(`{"oid":%q}`, f.entry.Head), fmt.Sprintf(`{"oid":%q}`, f.entry.Base)
			}
			entry = fmt.Sprintf(`{"id":%q,"state":"AWAITING_CHECKS","headCommit":%s,"baseCommit":%s}`, f.entry.ID, head, base)
		}
		merge := "null"
		if f.mergeOID != "" {
			merge = fmt.Sprintf(`{"oid":%q}`, f.mergeOID)
		}
		return ok(fmt.Sprintf(`{"data":{"repository":{"pullRequest":{"number":7,"state":%q,"headRefOid":%q,"mergeCommit":%s,"mergeQueueEntry":%s}}}}`,
			f.prState, f.prHead, merge, entry))
	}
	return &github.Response{Status: 404, Header: http.Header{}, Body: []byte(`{"message":"Not Found"}`)}, nil
}

func (f *queueGitHub) set(fn func(f *queueGitHub)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

// fakeChecks records policy check publications instead of calling the
// Checks API with an App credential.
type fakeChecks struct {
	mu   sync.Mutex
	runs []app.PolicyCheck
}

func (f *fakeChecks) PublishPolicyCheck(_ context.Context, c app.PolicyCheck) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runs = append(f.runs, c)
	return fmt.Sprintf("cr_%d", len(f.runs)), nil
}

func (f *fakeChecks) published() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, r := range f.runs {
		out = append(out, r.HeadSHA+":"+r.Status+":"+r.Conclusion)
	}
	return out
}

// queueCandidate runs one issue to an approved RESULT and returns its
// candidate on the github_queue target.
func queueCandidate(t *testing.T, e *env) *sqlite.Candidate {
	t.Helper()
	issueID, wt := startSingle(t, e)
	e.commitIn(wt, "feature.txt", "worker\n")
	e.submitAndApprove(issueID)
	cs := e.candidates(issueID)
	if len(cs) != 1 {
		t.Fatalf("candidates: %d", len(cs))
	}
	return cs[0]
}

// tick lets the poll interval pass and runs reconcile passes.
func (e *env) tick(n int) {
	for i := 0; i < n; i++ {
		e.clock.Advance(11 * time.Second)
		e.app.ReconcileOnce(e.ctx)
	}
}

func (e *env) mergeApprovals(candidateID string) []*sqlite.Approval {
	e.t.Helper()
	var aps []*sqlite.Approval
	_ = e.db.Read(e.ctx, func(q sqlite.Q) error {
		aps, _ = sqlite.ApprovalsWhere(e.ctx, q, "kind='MERGE' AND merge_candidate_id=?", candidateID)
		return nil
	})
	return aps
}

func pendingMerge(t *testing.T, e *env, candidateID string) *sqlite.Approval {
	t.Helper()
	for _, ap := range e.mergeApprovals(candidateID) {
		if ap.State == domain.ApprovalPending {
			return ap
		}
	}
	t.Fatalf("no pending MERGE approval for %s", candidateID)
	return nil
}

// 04 §10.2 / §10.9: without a verified merge queue, a pinned required
// policy check and a check publisher, the candidate stays WAITING with the
// concrete reason and enqueue is refused.
func TestGitHubQueueUnsupportedStaysWaiting(t *testing.T) {
	cases := []struct {
		name, rules string
		publisher   bool
		reason      string
	}{
		{"no queue", `[]`, true, "MERGE_QUEUE_DISABLED"},
		{"ambiguous rulesets", strings.Replace(verifiedRules, "[", `[{"type":"merge_queue","parameters":{"grouping_strategy":"ALLGREEN","max_entries_to_build":1,"max_entries_to_merge":1}},`, 1), true, "MERGE_QUEUE_RULES_AMBIGUOUS"},
		{"headgreen", strings.Replace(verifiedRules, "ALLGREEN", "HEADGREEN", 1), true, "MERGE_QUEUE_HEADGREEN_UNSUPPORTED"},
		{"group size", strings.Replace(verifiedRules, `"max_entries_to_build":1`, `"max_entries_to_build":5`, 1), true, "MERGE_QUEUE_GROUP_SIZE_UNSUPPORTED"},
		{"no policy check", `[{"type":"merge_queue","parameters":{"grouping_strategy":"ALLGREEN","max_entries_to_build":1,"max_entries_to_merge":1}}]`, true, "POLICY_CHECK_NOT_REQUIRED"},
		{"unpinned source", strings.Replace(verifiedRules, `,"integration_id":4242`, ``, 1), true, "POLICY_CHECK_SOURCE_UNPINNED"},
		{"no publisher", verifiedRules, false, "POLICY_PUBLISHER_UNAVAILABLE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t, queueConfig)
			gh := &queueGitHub{rules: tc.rules, prState: "OPEN", enqueueOK: true}
			e.app.GitHub = gh
			if tc.publisher {
				e.app.PolicyChecks = &fakeChecks{}
			}
			c := queueCandidate(t, e)
			e.tick(2)
			cur, _ := e.candidate(c.ID)
			if cur.State != domain.CandidateWaiting || cur.StateReason != tc.reason {
				t.Fatalf("want WAITING(%s), got %s(%s)", tc.reason, cur.State, cur.StateReason)
			}
			if _, err := e.app.EnqueueCandidate(e.ctx, e.cmd, c.ID); !domain.IsCode(err, domain.CodeUnsupportedCapability) || !strings.Contains(err.Error(), tc.reason) {
				t.Fatalf("enqueue must be refused with %s: %v", tc.reason, err)
			}
		})
	}
}

// 03 §3.4, 04 §10.7–10.10: WAITING → PUBLISHING enqueue, native queue
// state tracking, a new MERGE approval and fresh policy check for every
// group tuple (INV-09), and MERGED evidence from the provider. The target
// is never published locally (INV-10).
func TestGitHubQueueEnqueueGroupApprovalMerged(t *testing.T) {
	e := newEnv(t, queueConfig)
	gh := &queueGitHub{rules: verifiedRules, prState: "OPEN", enqueueOK: true}
	checks := &fakeChecks{}
	e.app.GitHub, e.app.PolicyChecks = gh, checks
	c := queueCandidate(t, e)
	gh.set(func(f *queueGitHub) { f.source, f.prHead = c.SourceSHA, c.SourceSHA })
	localTarget := git(t, e.repo, "rev-parse", "refs/heads/integration")

	e.tick(1)
	if cur, _ := e.candidate(c.ID); cur.State != domain.CandidateWaiting || cur.StateReason != "AWAITING_ENQUEUE" {
		t.Fatalf("verified target: want WAITING(AWAITING_ENQUEUE), got %s(%s)", cur.State, cur.StateReason)
	}
	if _, err := e.app.EnqueueCandidate(e.ctx, e.cmd, c.ID); err != nil {
		t.Fatal(err)
	}
	cur, _ := e.candidate(c.ID)
	if cur.State != domain.CandidatePublishing || cur.NativeQueueState != domain.NativeRequested {
		t.Fatalf("want PUBLISHING/REQUESTED, got %s/%s", cur.State, cur.NativeQueueState)
	}
	e.tick(1)
	if cur, _ = e.candidate(c.ID); cur.NativeQueueState != domain.NativeQueued {
		t.Fatalf("want QUEUED after enqueue, got %s", cur.NativeQueueState)
	}

	// Group 1 forms: a fresh in_progress check and a pending MERGE approval
	// bound to its head/base.
	gh.set(func(f *queueGitHub) { f.entry = &queueEntry{ID: "MQE_1", Head: "g1", Base: "b1"} })
	e.tick(2)
	cur, _ = e.candidate(c.ID)
	if cur.NativeQueueState != domain.NativeValidating || cur.CandidateSHA != "g1" || cur.BaseSHA != "b1" {
		t.Fatalf("group 1: %s head %s base %s", cur.NativeQueueState, cur.CandidateSHA, cur.BaseSHA)
	}
	first := pendingMerge(t, e, c.ID)
	if got := checks.published(); len(got) != 1 || got[0] != "g1:in_progress:" {
		t.Fatalf("group 1 checks: %v", got)
	}

	// INV-09: the queue rebuilt the group on a new base before approval.
	gh.set(func(f *queueGitHub) { f.entry = &queueEntry{ID: "MQE_1", Head: "g2", Base: "b2"} })
	e.tick(2)
	for _, ap := range e.mergeApprovals(c.ID) {
		if ap.ID == first.ID && ap.State != domain.ApprovalInvalidated {
			t.Fatalf("approval for the old group must be invalidated, got %s", ap.State)
		}
	}
	if _, err := e.app.DecideApproval(e.ctx, e.cmd, first.ID, app.DecisionRequest{Decision: "APPROVE"}); err == nil {
		t.Fatal("an approval of a replaced group must not be accepted")
	}
	second := pendingMerge(t, e, c.ID)
	if _, err := e.app.DecideApproval(e.ctx, e.cmd, second.ID, app.DecisionRequest{Decision: "APPROVE"}); err != nil {
		t.Fatal(err)
	}
	e.tick(2)
	if got := checks.published(); len(got) != 3 || got[1] != "g2:in_progress:" || got[2] != "g2:completed:success" {
		t.Fatalf("group 2 checks: %v", got)
	}

	// 04 §10.9: the same head SHA in a re-created entry is a new group: the
	// earlier success is reset and a new approval is required.
	gh.set(func(f *queueGitHub) { f.entry = &queueEntry{ID: "MQE_2", Head: "g2", Base: "b2"} })
	e.tick(2)
	if got := checks.published(); len(got) != 4 || got[3] != "g2:in_progress:" {
		t.Fatalf("same-SHA regroup must reset the check first: %v", got)
	}
	third := pendingMerge(t, e, c.ID)
	if third.ID == second.ID {
		t.Fatal("same-SHA regroup must not reuse the previous MERGE approval")
	}
	if _, err := e.app.DecideApproval(e.ctx, e.cmd, third.ID, app.DecisionRequest{Decision: "APPROVE"}); err != nil {
		t.Fatal(err)
	}
	e.tick(2)

	// GitHub merges the pull request: evidence is recorded from the provider.
	gh.set(func(f *queueGitHub) { f.entry, f.prState, f.mergeOID = nil, "MERGED", "m1" })
	e.tick(2)
	cur, _ = e.candidate(c.ID)
	if cur.State != domain.CandidateMerged || cur.NativeQueueState != domain.NativeMerged {
		t.Fatalf("want MERGED/MERGED, got %s/%s (%s)", cur.State, cur.NativeQueueState, cur.StateReason)
	}
	var method, after string
	var publishes int
	_ = e.db.Read(e.ctx, func(q sqlite.Q) error {
		_ = q.QueryRowContext(e.ctx, "SELECT method,target_after FROM integration_evidence WHERE candidate_id=?", c.ID).Scan(&method, &after)
		return q.QueryRowContext(e.ctx, "SELECT COUNT(*) FROM outbox WHERE kind='git.publish'").Scan(&publishes)
	})
	if method != "github_queue" || after != "m1" {
		t.Fatalf("evidence: %s %s", method, after)
	}
	if publishes != 0 || git(t, e.repo, "rev-parse", "refs/heads/integration") != localTarget {
		t.Fatalf("github_queue target must never be published locally (publishes %d)", publishes)
	}
}

// 04 §10.8: a candidate dropped from the queue goes back to WAITING and
// loses its group-bound approvals; a rejected enqueue does the same.
func TestGitHubQueueRemovedAndRejected(t *testing.T) {
	e := newEnv(t, queueConfig)
	gh := &queueGitHub{rules: verifiedRules, prState: "OPEN", enqueueOK: false}
	e.app.GitHub, e.app.PolicyChecks = gh, &fakeChecks{}
	c := queueCandidate(t, e)
	gh.set(func(f *queueGitHub) { f.source, f.prHead = c.SourceSHA, c.SourceSHA })
	e.tick(1)
	if _, err := e.app.EnqueueCandidate(e.ctx, e.cmd, c.ID); err != nil {
		t.Fatal(err)
	}
	e.tick(1)
	cur, _ := e.candidate(c.ID)
	if cur.State != domain.CandidateWaiting || cur.StateReason != "NATIVE_ENQUEUE_REJECTED" || cur.Enqueued {
		t.Fatalf("rejected enqueue: %s(%s) enqueued=%v", cur.State, cur.StateReason, cur.Enqueued)
	}

	gh.set(func(f *queueGitHub) { f.enqueueOK = true })
	if _, err := e.app.EnqueueCandidate(e.ctx, e.cmd, c.ID); err != nil {
		t.Fatal(err)
	}
	gh.set(func(f *queueGitHub) {})
	e.tick(1)
	gh.set(func(f *queueGitHub) { f.entry = &queueEntry{ID: "MQE_1", Head: "g1", Base: "b1"} })
	e.tick(2)
	ap := pendingMerge(t, e, c.ID)
	gh.set(func(f *queueGitHub) { f.entry = nil })
	e.tick(2)
	cur, _ = e.candidate(c.ID)
	if cur.State != domain.CandidateWaiting || cur.StateReason != "NATIVE_QUEUE_REMOVED" || cur.NativeQueueState != domain.NativeRemoved || cur.Enqueued {
		t.Fatalf("removed: %s(%s) native %s enqueued=%v", cur.State, cur.StateReason, cur.NativeQueueState, cur.Enqueued)
	}
	for _, a := range e.mergeApprovals(c.ID) {
		if a.ID == ap.ID && a.State != domain.ApprovalInvalidated {
			t.Fatalf("group approval must be invalidated on removal: %s", a.State)
		}
	}
}

// 04 §10.3 / §10.10: an enqueue intent lost to a restart is resolved by
// reading the provider, and a merge GitHub performed without a published
// policy success is kept as a fact but never recorded as authorized.
func TestGitHubQueueLostIntentAndUnauthorizedMerge(t *testing.T) {
	e := newEnv(t, queueConfig)
	gh := &queueGitHub{rules: verifiedRules, prState: "OPEN", enqueueOK: true}
	e.app.GitHub, e.app.PolicyChecks = gh, &fakeChecks{}
	c := queueCandidate(t, e)
	gh.set(func(f *queueGitHub) { f.source, f.prHead = c.SourceSHA, c.SourceSHA })
	if _, err := e.app.EnqueueCandidate(e.ctx, e.cmd, c.ID); err != nil {
		t.Fatal(err)
	}
	// The daemon died while the intent was in flight; it was never sent.
	if err := e.db.Write(e.ctx, func(tx *sqlite.Tx) error {
		_, err := tx.ExecContext(e.ctx, "UPDATE outbox SET state='UNKNOWN' WHERE kind='github.queue.enqueue'")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	e.tick(1)
	if cur, _ := e.candidate(c.ID); cur.NativeQueueState != domain.NativeRequested {
		t.Fatalf("a fresh intent is left to its outbox item, got %s", cur.NativeQueueState)
	}
	e.clock.Advance(time.Minute)
	e.tick(1)
	cur, _ := e.candidate(c.ID)
	if cur.State != domain.CandidateWaiting || cur.StateReason != "NATIVE_QUEUE_REMOVED" {
		t.Fatalf("a lost intent must be resolved from the provider: %s(%s) native %s", cur.State, cur.StateReason, cur.NativeQueueState)
	}

	if _, err := e.app.EnqueueCandidate(e.ctx, e.cmd, c.ID); err != nil {
		t.Fatal(err)
	}
	e.tick(1)
	gh.set(func(f *queueGitHub) { f.entry, f.prState, f.mergeOID = nil, "MERGED", "m9" })
	e.tick(1)
	cur, _ = e.candidate(c.ID)
	if cur.State != domain.CandidateMerged || cur.StateReason != "MERGED_WITHOUT_POLICY_SUCCESS" {
		t.Fatalf("unauthorized merge: %s(%s)", cur.State, cur.StateReason)
	}
	var evidence string
	_ = e.db.Read(e.ctx, func(q sqlite.Q) error {
		return q.QueryRowContext(e.ctx, "SELECT provider_evidence FROM integration_evidence WHERE candidate_id=?", c.ID).Scan(&evidence)
	})
	if !strings.Contains(evidence, `"policy_authorized":false`) {
		t.Fatalf("evidence must say the merge was not authorized: %s", evidence)
	}
}

// #108: a candidate that only lost its pull request (closed, or its head
// moved away from the approved source) is enqueued again once an open pull
// request carries the source; the old group is not reused.
func TestGitHubQueueFailedCandidateReenqueue(t *testing.T) {
	e := newEnv(t, queueConfig)
	gh := &queueGitHub{rules: verifiedRules, prState: "OPEN", enqueueOK: true}
	e.app.GitHub, e.app.PolicyChecks = gh, &fakeChecks{}
	c := queueCandidate(t, e)
	gh.set(func(f *queueGitHub) { f.source, f.prHead = c.SourceSHA, c.SourceSHA })
	e.tick(1)

	for _, tc := range []struct {
		reason string
		lose   func(f *queueGitHub)
	}{
		{"PR_CLOSED", func(f *queueGitHub) { f.entry, f.prState = nil, "CLOSED" }},
		{"PR_HEAD_CHANGED", func(f *queueGitHub) { f.entry, f.prHead = nil, "other" }},
	} {
		if _, err := e.app.EnqueueCandidate(e.ctx, e.cmd, c.ID); err != nil {
			t.Fatalf("%s: enqueue: %v", tc.reason, err)
		}
		e.tick(1)
		gh.set(func(f *queueGitHub) { f.entry = &queueEntry{ID: "MQE_1", Head: "g-" + tc.reason, Base: "b1"} })
		e.tick(2)
		before, _ := e.candidate(c.ID)
		if before.ProviderValidationID == "" {
			t.Fatalf("%s: no provider group before the pull request was lost", tc.reason)
		}
		gh.set(tc.lose)
		e.tick(2)
		cur, _ := e.candidate(c.ID)
		if cur.State != domain.CandidateFailed || cur.StateReason != tc.reason {
			t.Fatalf("want FAILED(%s), got %s(%s)", tc.reason, cur.State, cur.StateReason)
		}
		// No open pull request carries the source: enqueue is refused and
		// the candidate stays FAILED.
		if _, err := e.app.EnqueueCandidate(e.ctx, e.cmd, c.ID); !domain.IsCode(err, domain.CodeConflict) {
			t.Fatalf("%s: enqueue without an open pull request must be refused: %v", tc.reason, err)
		}
		gh.set(func(f *queueGitHub) { f.prState, f.prHead = "OPEN", c.SourceSHA })
		if _, err := e.app.EnqueueCandidate(e.ctx, e.cmd, c.ID); err != nil {
			t.Fatalf("%s: re-enqueue: %v", tc.reason, err)
		}
		cur, _ = e.candidate(c.ID)
		if cur.State != domain.CandidatePublishing || cur.NativeQueueState != domain.NativeRequested || cur.ProviderValidationID != "" {
			t.Fatalf("%s: re-enqueue: %s/%s group %q", tc.reason, cur.State, cur.NativeQueueState, cur.ProviderValidationID)
		}
		e.tick(1)
		gh.set(func(f *queueGitHub) { f.entry = &queueEntry{ID: "MQE_2", Head: "h-" + tc.reason, Base: "b2"} })
		e.tick(2)
		if cur, _ = e.candidate(c.ID); cur.ProviderValidationID == "" || cur.ProviderValidationID == before.ProviderValidationID {
			t.Fatalf("%s: re-enqueued candidate must validate in a new group, got %q (old %q)", tc.reason, cur.ProviderValidationID, before.ProviderValidationID)
		}
		pendingMerge(t, e, c.ID)
		// Leave the queue so the next case starts from WAITING.
		gh.set(func(f *queueGitHub) { f.entry = nil })
		e.tick(2)
		if cur, _ = e.candidate(c.ID); cur.State != domain.CandidateWaiting {
			t.Fatalf("%s: want WAITING after removal, got %s(%s)", tc.reason, cur.State, cur.StateReason)
		}
	}
	if len(gh.dequeued) != 0 {
		t.Fatalf("a lost pull request is not dequeued by Samsara: %v", gh.dequeued)
	}
}

// #108: when the approval subject becomes permanently invalid, the pull
// request is taken out of the GitHub queue instead of blocking it, and the
// candidate is not re-enqueued. provider_validation_id is in the API view.
func TestGitHubQueueDequeueOnPermanentlyInvalidSubject(t *testing.T) {
	e := newEnv(t, queueConfig)
	gh := &queueGitHub{rules: verifiedRules, prState: "OPEN", enqueueOK: true}
	e.app.GitHub, e.app.PolicyChecks = gh, &fakeChecks{}
	c := queueCandidate(t, e)
	gh.set(func(f *queueGitHub) { f.source, f.prHead = c.SourceSHA, c.SourceSHA })
	e.tick(1)
	if _, err := e.app.EnqueueCandidate(e.ctx, e.cmd, c.ID); err != nil {
		t.Fatal(err)
	}
	e.tick(1)
	gh.set(func(f *queueGitHub) { f.entry = &queueEntry{ID: "MQE_1", Head: "g1", Base: "b1"} })
	e.tick(2)
	cur, _ := e.candidate(c.ID)
	var view *sqlite.CandidateView
	_ = e.db.Read(e.ctx, func(q sqlite.Q) error {
		var err error
		view, err = sqlite.CandidateProjection(e.ctx, q, c.ID)
		return err
	})
	if cur.ProviderValidationID == "" || view == nil || view.ProviderValidationID != cur.ProviderValidationID {
		t.Fatalf("provider_validation_id must be in the candidate view: db %q view %+v", cur.ProviderValidationID, view)
	}
	pendingMerge(t, e, c.ID)

	// The run is cancelled: every MERGE approval of this candidate is stale
	// for good.
	if err := e.db.Write(e.ctx, func(tx *sqlite.Tx) error {
		_, err := tx.ExecContext(e.ctx, "UPDATE runs SET state='CANCELLED' WHERE id=?", c.RunID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	e.tick(2)
	cur, _ = e.candidate(c.ID)
	if cur.State != domain.CandidateFailed || cur.StateReason != "RUN_CANCELLED" {
		t.Fatalf("want FAILED(RUN_CANCELLED), got %s(%s)", cur.State, cur.StateReason)
	}
	if len(gh.dequeued) != 1 || gh.dequeued[0] != "PR_7" {
		t.Fatalf("the pull request must be dequeued once: %v", gh.dequeued)
	}
	if cur.NativeQueueState != domain.NativeRemoved || cur.Enqueued || cur.ProviderValidationID != "" {
		t.Fatalf("after dequeue: native %s enqueued=%v group %q", cur.NativeQueueState, cur.Enqueued, cur.ProviderValidationID)
	}
	if _, err := e.app.EnqueueCandidate(e.ctx, e.cmd, c.ID); !domain.IsCode(err, domain.CodeConflict) {
		t.Fatalf("a permanently invalid candidate must not be re-enqueued: %v", err)
	}
	e.tick(2)
	if len(gh.dequeued) != 1 {
		t.Fatalf("dequeue must not repeat: %v", gh.dequeued)
	}
}

// #108: a refused dequeue is never recorded as a removal. Retries re-read
// the provider; when they run out the queue state is UNKNOWN. A pull
// request that left the queue is recorded as REMOVED only once observed.
func TestGitHubQueueDequeueRefused(t *testing.T) {
	cancelInGroup := func(t *testing.T, gh *queueGitHub) (*env, *sqlite.Candidate) {
		e := newEnv(t, queueConfig)
		e.app.GitHub, e.app.PolicyChecks = gh, &fakeChecks{}
		c := queueCandidate(t, e)
		gh.set(func(f *queueGitHub) { f.source, f.prHead = c.SourceSHA, c.SourceSHA })
		e.tick(1)
		if _, err := e.app.EnqueueCandidate(e.ctx, e.cmd, c.ID); err != nil {
			t.Fatal(err)
		}
		e.tick(1)
		gh.set(func(f *queueGitHub) { f.entry = &queueEntry{ID: "MQE_1", Head: "g1", Base: "b1"} })
		e.tick(2)
		pendingMerge(t, e, c.ID)
		if err := e.db.Write(e.ctx, func(tx *sqlite.Tx) error {
			_, err := tx.ExecContext(e.ctx, "UPDATE runs SET state='CANCELLED' WHERE id=?", c.RunID)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return e, c
	}

	t.Run("refused", func(t *testing.T) {
		gh := &queueGitHub{rules: verifiedRules, prState: "OPEN", enqueueOK: true, dequeueNo: true}
		e, c := cancelInGroup(t, gh)
		e.tick(2)
		if cur, _ := e.candidate(c.ID); cur.State != domain.CandidateFailed || cur.NativeQueueState == domain.NativeRemoved || !cur.Enqueued {
			t.Fatalf("a refused dequeue must not be recorded as removed: %s native %s enqueued=%v", cur.State, cur.NativeQueueState, cur.Enqueued)
		}
		e.tick(20)
		cur, _ := e.candidate(c.ID)
		if cur.NativeQueueState != domain.NativeUnknown || !cur.Enqueued {
			t.Fatalf("exhausted dequeue: want UNKNOWN, got native %s enqueued=%v", cur.NativeQueueState, cur.Enqueued)
		}
		if len(gh.dequeued) < 2 {
			t.Fatalf("a refused dequeue is retried: %v", gh.dequeued)
		}
	})

	t.Run("left the queue after a refusal", func(t *testing.T) {
		gh := &queueGitHub{rules: verifiedRules, prState: "OPEN", enqueueOK: true, dequeueNo: true}
		e, c := cancelInGroup(t, gh)
		e.tick(2)
		refused := len(gh.dequeued)
		if cur, _ := e.candidate(c.ID); cur.State != domain.CandidateFailed || cur.NativeQueueState == domain.NativeRemoved || refused == 0 {
			t.Fatalf("refused: %s native %s dequeues %d", cur.State, cur.NativeQueueState, refused)
		}
		// The entry leaves the queue on its own: the next attempt observes
		// it and records the removal without another dequeue call.
		gh.set(func(f *queueGitHub) { f.entry = nil })
		e.tick(2)
		cur, _ := e.candidate(c.ID)
		if cur.NativeQueueState != domain.NativeRemoved || cur.Enqueued || len(gh.dequeued) != refused {
			t.Fatalf("observed removal: native %s enqueued=%v dequeues %d→%d", cur.NativeQueueState, cur.Enqueued, refused, len(gh.dequeued))
		}
	})
}
