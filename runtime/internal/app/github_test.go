package app_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Mineru98/samsara/runtime/api/apitest"
	"github.com/Mineru98/samsara/runtime/internal/app"
	"github.com/Mineru98/samsara/runtime/internal/domain"
	"github.com/Mineru98/samsara/runtime/internal/github"
	"github.com/Mineru98/samsara/runtime/internal/store/sqlite"
)

// fakeGitHub answers by "METHOD path-without-query" and records calls.
type fakeGitHub struct {
	mu     sync.Mutex
	routes map[string]*github.Response
	calls  []string
}

func (f *fakeGitHub) Do(_ context.Context, method, path string, fields map[string]string) (*github.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	call := method + " " + path
	for k, v := range fields {
		call += " " + k + "=" + v
	}
	f.calls = append(f.calls, call)
	if r, ok := f.routes[method+" "+strings.SplitN(path, "?", 2)[0]]; ok {
		return r, nil
	}
	return &github.Response{Status: 404, Header: http.Header{}, Body: []byte(`{"message":"Not Found"}`)}, nil
}

func ghOK(body string) *github.Response {
	return &github.Response{Status: 200, Header: http.Header{}, Body: []byte(body)}
}

func healthyGitHub() *fakeGitHub {
	return &fakeGitHub{routes: map[string]*github.Response{
		"GET repos/o/r/issues": ghOK(`[
			{"number":1,"title":"bug","state":"open","html_url":"https://github.com/o/r/issues/1","updated_at":"2026-10-27T00:00:00Z"},
			{"number":2,"title":"fix bug","state":"open","html_url":"https://github.com/o/r/pull/2","updated_at":"2026-10-28T00:00:00Z","pull_request":{"merged_at":null}}]`),
		"GET repos/o/r/commits":                   ghOK(`[{"sha":"abc123","html_url":"https://github.com/o/r/commit/abc123","commit":{"message":"fix: bug","committer":{"date":"2026-10-28T00:10:00Z"},"author":{"name":"me"}}}]`),
		"GET repos/o/r/pulls/2":                   ghOK(`{"draft":false,"head":{"sha":"abc123","ref":"fix/1"}}`),
		"GET repos/o/r/commits/abc123/check-runs": ghOK(`{"check_runs":[{"id":7,"name":"test","status":"in_progress","conclusion":null,"html_url":"https://github.com/o/r/runs/7"}]}`),
		"GET repos/o/r/pulls/2/reviews":           ghOK(`[{"id":9,"state":"APPROVED","html_url":"h","commit_id":"abc123","submitted_at":"2026-10-28T00:20:00Z","user":{"login":"rev"}}]`),
	}}
}

func (e *env) history() map[string]any {
	e.t.Helper()
	h, err := e.app.GitHubHistory(e.ctx, e.project)
	if err != nil {
		e.t.Fatal(err)
	}
	apitest.Check(e.t, "entities.schema.json#/$defs/GitHubHistory", h)
	b, _ := json.Marshal(h)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return out
}

func (e *env) githubFreshness() string {
	e.t.Helper()
	s, err := e.app.ProjectSnapshot(e.ctx, e.project)
	if err != nil {
		e.t.Fatal(err)
	}
	return s.Freshness["github"]
}

func historyKinds(h map[string]any) map[string]int {
	out := map[string]int{}
	for _, it := range h["items"].([]any) {
		out[it.(map[string]any)["kind"].(string)]++
	}
	return out
}

func TestGitHubSyncRecordsHistoryWithProvenance(t *testing.T) {
	e := newEnv(t, baseConfig)
	git(t, e.repo, "remote", "add", "origin", "git@github.com:o/r.git")
	if got := e.githubFreshness(); got != domain.FreshUnavailable {
		t.Fatalf("before any sync freshness = %s", got)
	}
	// A manual link bound to a local issue keeps its binding through sync.
	res := e.applyPlan(`{"spec_version":"1","project_id":"PROJECT","expected_graph_version":"0","issues":[{"client_key":"a","title":"A","acceptance_criteria":["x"]}]}`)
	local := res.IssueMapping["a"]
	if err := e.app.AddExternalLink(e.ctx, e.cmd, app.LinkRequest{ProjectID: e.project, IssueID: local, Provider: "github", Kind: "issue", ExternalKey: "o/r#1", Title: "manual"}); err != nil {
		t.Fatal(err)
	}
	fg := healthyGitHub()
	e.app.GitHub = fg
	e.app.SyncGitHubOnce(e.ctx)

	h := e.history()
	if h["sync"] != github.StatusOK || h["repository"] != "o/r" || h["freshness"] != domain.FreshLive || h["last_synced_at"] == nil {
		t.Fatalf("sync header: %+v", h)
	}
	if k := historyKinds(h); k["issue"] != 1 || k["pull_request"] != 1 || k["commit"] != 1 || k["check"] != 1 || k["review"] != 1 {
		t.Fatalf("kinds: %v", k)
	}
	for _, x := range h["items"].([]any) {
		it := x.(map[string]any)
		if it["synced_at"] == nil {
			t.Fatalf("synced item without synced_at: %+v", it)
		}
		switch it["kind"] {
		case "issue":
			if it["issue_id"] != local || it["source"] != "manual" || it["state"] != "open" || it["title"] != "bug" {
				t.Fatalf("manual link must keep its binding and gain GitHub state: %+v", it)
			}
		case "pull_request":
			if it["source"] != "github_sync" || it["state"] != "open" {
				t.Fatalf("pr: %+v", it)
			}
		case "check":
			if it["state"] != "in_progress" {
				t.Fatalf("check: %+v", it)
			}
		}
	}
	if got := e.githubFreshness(); got != domain.FreshLive {
		t.Fatalf("snapshot github freshness = %s", got)
	}

	// Not due again until the interval passes; refresh makes it due now.
	fg.calls = nil
	e.app.SyncGitHubOnce(e.ctx)
	if len(fg.calls) != 0 {
		t.Fatalf("sync ran before it was due: %v", fg.calls)
	}
	if st, err := e.app.RefreshGitHub(e.ctx, e.project); err != nil || st != "SCHEDULED" {
		t.Fatalf("refresh: %s %v", st, err)
	}
	e.app.SyncGitHubOnce(e.ctx)
	if len(fg.calls) == 0 || !strings.Contains(fg.calls[0], "since=2026-10-28T00%3A00%3A00Z") {
		t.Fatalf("refresh must run an incremental pass: %v", fg.calls)
	}

	// Freshness degrades to STALE when the data ages past two intervals.
	e.clock.Advance(11 * time.Minute)
	if got := e.githubFreshness(); got != domain.FreshStale {
		t.Fatalf("aged freshness = %s", got)
	}
}

func TestGitHubFailuresLeaveLocalStateAlone(t *testing.T) {
	e := newEnv(t, baseConfig)
	git(t, e.repo, "remote", "add", "origin", "https://github.com/o/r.git")
	res := e.applyPlan(`{"spec_version":"1","project_id":"PROJECT","expected_graph_version":"0","issues":[{"client_key":"a","title":"A","acceptance_criteria":["x"]}]}`)
	e.app.GitHub = healthyGitHub()
	e.app.SyncGitHubOnce(e.ctx)
	before := e.history()
	issueBefore := e.issueView(res.IssueMapping["a"])

	// Rate limit: 403 with an exhausted budget and a reset time.
	reset := e.clock.Now().Add(20 * time.Minute)
	limited := &fakeGitHub{routes: map[string]*github.Response{"GET repos/o/r/issues": {Status: 403,
		Header: http.Header{"X-Ratelimit-Remaining": {"0"}, "X-Ratelimit-Reset": {domain.Dec(reset.Unix())}},
		Body:   []byte(`{"message":"API rate limit exceeded for token ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ0123"}`)}}}
	e.app.GitHub = limited
	if _, err := e.app.RefreshGitHub(e.ctx, e.project); err != nil {
		t.Fatal(err)
	}
	e.app.SyncGitHubOnce(e.ctx)
	h := e.history()
	if h["sync"] != github.StatusRateLimited || h["retry_at"] != domain.RFC3339(reset.UnixMilli()) {
		t.Fatalf("rate limit: %+v", h)
	}
	if len(h["items"].([]any)) != len(before["items"].([]any)) {
		t.Fatal("a failed sync must not drop recorded history")
	}
	if strings.Contains(h["error"].(string), "ghp_ABC") {
		t.Fatalf("token-shaped text must be redacted: %s", h["error"])
	}
	// Refresh during the window does not hammer the provider.
	if st, _ := e.app.RefreshGitHub(e.ctx, e.project); st != github.StatusRateLimited {
		t.Fatalf("refresh during rate limit = %s", st)
	}
	n := len(limited.calls)
	e.app.SyncGitHubOnce(e.ctx)
	if len(limited.calls) != n {
		t.Fatal("sync ran inside the rate-limit window")
	}
	if got := e.githubFreshness(); got != domain.FreshStale {
		t.Fatalf("freshness after failure = %s", got)
	}

	// GitHub 404 is a provider status, not a missing project.
	e.clock.Advance(21 * time.Minute)
	e.app.GitHub = &fakeGitHub{routes: map[string]*github.Response{}}
	e.app.SyncGitHubOnce(e.ctx)
	if h := e.history(); h["sync"] != github.StatusRepoNotFound {
		t.Fatalf("404: %+v", h)
	}
	if _, err := e.app.GitHubHistory(e.ctx, "prj_missing"); !domain.IsCode(err, domain.CodeNotFound) {
		t.Fatalf("unknown project must stay RESOURCE_NOT_FOUND, got %v", err)
	}

	// Local work state is untouched by any of this.
	after := e.issueView(res.IssueMapping["a"])
	if after.Version != issueBefore.Version || after.DisplayStatus != issueBefore.DisplayStatus {
		t.Fatalf("issue changed: %+v -> %+v", issueBefore, after)
	}
	var token int
	_ = e.db.Read(e.ctx, func(q sqlite.Q) error {
		return q.QueryRowContext(e.ctx, "SELECT COUNT(*) FROM github_sync_state WHERE last_error LIKE '%ghp_%'").Scan(&token)
	})
	if token != 0 {
		t.Fatal("token stored in github_sync_state")
	}
}

func TestGitHubSyncNotConfiguredOrDisabled(t *testing.T) {
	e := newEnv(t, baseConfig) // no origin remote
	fg := healthyGitHub()
	e.app.GitHub = fg
	e.app.SyncGitHubOnce(e.ctx)
	if h := e.history(); h["sync"] != github.StatusNotConfigured || h["freshness"] != domain.FreshUnavailable || len(fg.calls) != 0 {
		t.Fatalf("no remote: %+v %v", h, fg.calls)
	}
	off := newEnv(t, baseConfig+"github:\n  sync: \"off\"\n")
	git(t, off.repo, "remote", "add", "origin", "https://github.com/o/r.git")
	off.app.GitHub = fg
	off.app.SyncGitHubOnce(off.ctx)
	if h := off.history(); h["sync"] != github.StatusDisabled || len(fg.calls) != 0 {
		t.Fatalf("sync off: %+v %v", h, fg.calls)
	}
	if st, _ := off.app.RefreshGitHub(off.ctx, off.project); st != github.StatusDisabled {
		t.Fatalf("refresh with sync off = %s", st)
	}
}

const onePlan = `{"spec_version":"1","project_id":"PROJECT","expected_graph_version":"0","issues":[{"client_key":"a","title":"A","acceptance_criteria":["a works"]}]}`

// completeIssue drives a single issue to COMPLETED through the local queue.
func (e *env) completeIssue(issueID, runID string) {
	e.t.Helper()
	if _, err := e.app.StartRun(e.ctx, e.cmd, runID); err != nil {
		e.t.Fatal(err)
	}
	e.reconcile(3)
	e.register(issueID, "claude-"+issueID)
	wt := e.fake.SessionByAttempt(e.attempt(issueID).ID).Spec.Dir
	e.commitIn(wt, "a.txt", "A\n")
	if _, err := e.app.SubmitOutput(e.ctx, e.cmd, app.OutputSubmitRequest{Ticket: e.fake.SessionByAttempt(e.attempt(issueID).ID).Spec.Env["SAMSARA_LAUNCH_TICKET"]}); err != nil {
		e.t.Fatal(err)
	}
	e.reconcile(2)
	var approval, cand string
	_ = e.db.Read(e.ctx, func(q sqlite.Q) error {
		aps, _ := sqlite.ApprovalsWhere(e.ctx, q, "kind='RESULT' AND issue_id=? AND state='PENDING'", issueID)
		if len(aps) > 0 {
			approval = aps[0].ID
		}
		return nil
	})
	if _, err := e.app.DecideApproval(e.ctx, e.cmd, approval, app.DecisionRequest{Decision: "APPROVE"}); err != nil {
		e.t.Fatal(err)
	}
	e.reconcile(2)
	_ = e.db.Read(e.ctx, func(q sqlite.Q) error {
		cs, _ := sqlite.CandidatesWhere(e.ctx, q, "issue_id=?", issueID)
		if len(cs) > 0 {
			cand = cs[0].ID
		}
		return nil
	})
	if _, err := e.app.EnqueueCandidate(e.ctx, e.cmd, cand); err != nil {
		e.t.Fatal(err)
	}
	e.reconcile(4)
	if v := e.issueView(issueID); v.Lifecycle != domain.IssueCompleted {
		e.t.Fatalf("issue not completed: %s", v.Lifecycle)
	}
}

func TestGitHubIssueCloseIsOptIn(t *testing.T) {
	for _, tc := range []struct {
		name       string
		cfg        string
		wantPatch  bool
		ghState    string
		wantClosed bool
	}{
		{"default off", baseConfig + "github:\n  sync: \"off\"\n", false, "open", false},
		{"opt in", baseConfig + "github:\n  sync: \"off\"\n  close_issues: true\n", true, "open", true},
		{"opt in already closed", baseConfig + "github:\n  sync: \"off\"\n  close_issues: true\n", false, "closed", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t, tc.cfg)
			fg := &fakeGitHub{routes: map[string]*github.Response{
				"GET repos/o/r/issues/5":   ghOK(`{"state":"` + tc.ghState + `"}`),
				"PATCH repos/o/r/issues/5": ghOK(`{"state":"closed"}`),
			}}
			e.app.GitHub = fg
			res := e.applyPlan(onePlan)
			A := res.IssueMapping["a"]
			if err := e.app.AddExternalLink(e.ctx, e.cmd, app.LinkRequest{ProjectID: e.project, IssueID: A, Provider: "github", Kind: "issue", ExternalKey: "o/r#5"}); err != nil {
				t.Fatal(err)
			}
			e.completeIssue(A, res.RunID)
			e.reconcile(2)
			patched := false
			for _, c := range fg.calls {
				if strings.HasPrefix(c, "PATCH repos/o/r/issues/5") {
					patched = true
				}
			}
			if patched != tc.wantPatch {
				t.Fatalf("patched=%v want %v (calls %v)", patched, tc.wantPatch, fg.calls)
			}
			var state string
			_ = e.db.Read(e.ctx, func(q sqlite.Q) error {
				return q.QueryRowContext(e.ctx, "SELECT state FROM external_links WHERE external_key='o/r#5'").Scan(&state)
			})
			if (state == "closed") != tc.wantClosed {
				t.Fatalf("link state %q", state)
			}
		})
	}
}

// hookGitHub runs a callback on the first call, to act while a pass is in flight.
type hookGitHub struct {
	*fakeGitHub
	once   sync.Once
	before func()
}

func (h *hookGitHub) Do(ctx context.Context, method, path string, fields map[string]string) (*github.Response, error) {
	h.once.Do(h.before)
	return h.fakeGitHub.Do(ctx, method, path, fields)
}

func TestGitHubRefreshDuringPassIsKept(t *testing.T) {
	e := newEnv(t, baseConfig)
	git(t, e.repo, "remote", "add", "origin", "https://github.com/o/r.git")
	fg := healthyGitHub()
	e.app.GitHub = &hookGitHub{fakeGitHub: fg, before: func() {
		if st, err := e.app.RefreshGitHub(e.ctx, e.project); err != nil || st != "SCHEDULED" {
			t.Errorf("refresh in flight: %s %v", st, err)
		}
	}}
	e.app.SyncGitHubOnce(e.ctx)
	n := len(fg.calls)
	e.app.SyncGitHubOnce(e.ctx)
	if len(fg.calls) == n {
		t.Fatal("a refresh requested during a pass was lost")
	}
}

func TestGitHubLinkKeyMustBeIssueRef(t *testing.T) {
	e := newEnv(t, baseConfig)
	for _, key := range []string{"#12", "https://github.com/o/r/issues/12", "o/r"} {
		err := e.app.AddExternalLink(e.ctx, e.cmd, app.LinkRequest{ProjectID: e.project, Provider: "github", Kind: "issue", ExternalKey: key})
		if !domain.IsCode(err, domain.CodeInvalidRequest) {
			t.Fatalf("%q accepted: %v", key, err)
		}
	}
	if err := e.app.AddExternalLink(e.ctx, e.cmd, app.LinkRequest{ProjectID: e.project, Provider: "github", Kind: "commit", ExternalKey: "abc123"}); err != nil {
		t.Fatal(err)
	}
}

func TestGitHubIssueCloseWaitsForLogin(t *testing.T) {
	e := newEnv(t, baseConfig+"github:\n  sync: \"off\"\n  close_issues: true\n")
	e.app.GitHub = &fakeGitHub{routes: map[string]*github.Response{"GET repos/o/r/issues/5": {Status: 401, Header: http.Header{}, Body: []byte(`{}`)}}}
	res := e.applyPlan(onePlan)
	A := res.IssueMapping["a"]
	if err := e.app.AddExternalLink(e.ctx, e.cmd, app.LinkRequest{ProjectID: e.project, IssueID: A, Provider: "github", Kind: "issue", ExternalKey: "o/r#5"}); err != nil {
		t.Fatal(err)
	}
	e.completeIssue(A, res.RunID)
	e.reconcile(2)
	var state string
	_ = e.db.Read(e.ctx, func(q sqlite.Q) error {
		return q.QueryRowContext(e.ctx, "SELECT state FROM outbox WHERE kind='github.issue.close'").Scan(&state)
	})
	if state != "PENDING" {
		t.Fatalf("close must be retried after an auth failure, outbox state %q", state)
	}
	fg := &fakeGitHub{routes: map[string]*github.Response{"GET repos/o/r/issues/5": ghOK(`{"state":"open"}`), "PATCH repos/o/r/issues/5": ghOK(`{}`)}}
	e.app.GitHub = fg
	e.clock.Advance(11 * time.Minute)
	e.reconcile(1)
	_ = e.db.Read(e.ctx, func(q sqlite.Q) error {
		return q.QueryRowContext(e.ctx, "SELECT state FROM outbox WHERE kind='github.issue.close'").Scan(&state)
	})
	if state != "DONE" || len(fg.calls) != 2 {
		t.Fatalf("after login the close must go through: %q %v", state, fg.calls)
	}
}
