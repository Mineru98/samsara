package github

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

// fakeClient answers by "METHOD path-without-query" and records calls.
type fakeClient struct {
	routes map[string]*Response
	calls  []string
}

func (f *fakeClient) Do(_ context.Context, method, path string, fields map[string]string) (*Response, error) {
	call := method + " " + path
	for k, v := range fields {
		call += " " + k + "=" + v
	}
	f.calls = append(f.calls, call)
	if r, ok := f.routes[method+" "+strings.SplitN(path, "?", 2)[0]]; ok {
		return r, nil
	}
	return &Response{Status: 404, Header: http.Header{}, Body: []byte(`{"message":"Not Found"}`)}, nil
}

func ok(body string) *Response {
	return &Response{Status: 200, Header: http.Header{}, Body: []byte(body)}
}

var now = time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)

func TestParseIncluded(t *testing.T) {
	out := "HTTP/2.0 403 Forbidden\r\nX-Ratelimit-Remaining: 0\r\nX-Ratelimit-Reset: 1793635762\r\n\r\n{\"message\":\"API rate limit exceeded\"}"
	r, ok := parseIncluded([]byte(out))
	if !ok || r.Status != 403 || r.Header.Get("X-RateLimit-Remaining") != "0" || !strings.Contains(string(r.Body), "rate limit") {
		t.Fatalf("parse: %+v %v", r, ok)
	}
	if _, ok := parseIncluded([]byte("gh: To get started with GitHub CLI, please run: gh auth login")); ok {
		t.Fatal("non-HTTP output must not parse")
	}
}

func TestClassify(t *testing.T) {
	h := func(kv ...string) http.Header {
		x := http.Header{}
		for i := 0; i < len(kv); i += 2 {
			x.Set(kv[i], kv[i+1])
		}
		return x
	}
	cases := []struct {
		r    *Response
		want string
	}{
		{&Response{Status: 403, Header: h("X-RateLimit-Remaining", "0", "X-RateLimit-Reset", "1793635762")}, StatusRateLimited},
		{&Response{Status: 429, Header: h("Retry-After", "120")}, StatusRateLimited},
		{&Response{Status: 403, Header: h("X-RateLimit-Remaining", "4000")}, StatusAuthRequired},
		{&Response{Status: 401, Header: h()}, StatusAuthRequired},
		{&Response{Status: 404, Header: h()}, StatusRepoNotFound},
		{&Response{Status: 502, Header: h()}, StatusProviderError},
	}
	for _, c := range cases {
		e := AsError(Classify(c.r, now))
		if e.Status != c.want {
			t.Fatalf("HTTP %d %v: got %s want %s", c.r.Status, c.r.Header, e.Status, c.want)
		}
	}
	e := AsError(Classify(&Response{Status: 429, Header: h("Retry-After", "120")}, now))
	if !e.RetryAt.Equal(now.Add(120 * time.Second)) {
		t.Fatalf("Retry-After: %v", e.RetryAt)
	}
	e = AsError(Classify(&Response{Status: 403, Header: h("X-RateLimit-Remaining", "0", "X-RateLimit-Reset", "1793635762")}, now))
	if e.RetryAt.Unix() != 1793635762 {
		t.Fatalf("reset: %v", e.RetryAt)
	}
	if Classify(ok("{}"), now) != nil {
		t.Fatal("2xx must not be an error")
	}
}

func TestRepoFromRemote(t *testing.T) {
	for in, want := range map[string]string{
		"https://github.com/Mineru98/samsara.git":     "Mineru98/samsara",
		"https://github.com/Mineru98/samsara\n":       "Mineru98/samsara",
		"https://x-access@github.com/o/r.git":         "o/r",
		"git@github.com:Mineru98/samsara.git":         "Mineru98/samsara",
		"ssh://git@github.com/Mineru98/my.repo.git":   "Mineru98/my.repo",
		"https://gitlab.com/Mineru98/samsara.git":     "",
		"https://github.example.com/Mineru98/samsara": "",
		"/home/me/repos/samsara":                      "",
	} {
		got, _ := RepoFromRemote(in)
		if got != want {
			t.Fatalf("%q -> %q want %q", in, got, want)
		}
	}
}

func TestRedact(t *testing.T) {
	s := Redact("auth failed for ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ012345 and github_pat_11ABCDEFG0123456789_abcdefghij")
	if strings.Contains(s, "ghp_ABC") || strings.Contains(s, "github_pat_11") {
		t.Fatalf("not redacted: %s", s)
	}
}

func TestSyncReadsAllKindsAndAdvancesCursors(t *testing.T) {
	f := &fakeClient{routes: map[string]*Response{
		"GET repos/o/r/issues": ok(`[
			{"number":1,"title":"bug","state":"open","html_url":"u1","updated_at":"2026-11-01T00:00:00Z"},
			{"number":2,"title":"fix bug","state":"closed","html_url":"u2","updated_at":"2026-11-02T00:00:00Z","pull_request":{"merged_at":"2026-11-02T00:00:00Z"}}]`),
		"GET repos/o/r/commits":                ok(`[{"sha":"abc","html_url":"uc","commit":{"message":"fix: bug\n\nbody","committer":{"date":"2026-11-02T01:00:00Z"},"author":{"name":"me"}}}]`),
		"GET repos/o/r/pulls/2":                ok(`{"draft":false,"head":{"sha":"abc","ref":"fix/1"}}`),
		"GET repos/o/r/commits/abc/check-runs": ok(`{"check_runs":[{"id":7,"name":"test","status":"completed","conclusion":"success","html_url":"uk"}]}`),
		"GET repos/o/r/pulls/2/reviews":        ok(`[{"id":9,"state":"APPROVED","html_url":"ur","commit_id":"abc","submitted_at":"2026-11-02T00:30:00Z","user":{"login":"rev"}}]`),
	}}
	res, err := Sync(context.Background(), f, "o/r", Cursors{}, nil, DefaultLimits, now)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]Item{}
	for _, it := range res.Items {
		kinds[it.Kind] = it
	}
	if kinds["issue"].Key != "o/r#1" || kinds["pull_request"].State != "merged" || kinds["commit"].Title != "fix: bug" ||
		kinds["check"].State != "success" || kinds["review"].State != "APPROVED" || kinds["pull_request"].Detail["head_sha"] != "abc" {
		t.Fatalf("items: %+v", res.Items)
	}
	if res.Cursors.Issues != "2026-11-02T00:00:00Z" || res.Cursors.Commits != "2026-11-02T01:00:00Z" {
		t.Fatalf("cursors: %+v", res.Cursors)
	}
	// The next pass asks only for what changed since the cursors.
	f.calls = nil
	if _, err := Sync(context.Background(), f, "o/r", res.Cursors, nil, DefaultLimits, now); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.calls[0], "since=2026-11-02T00%3A00%3A00Z") || !strings.Contains(f.calls[1], "since=2026-11-01T01%3A00%3A00Z") {
		t.Fatalf("incremental calls: %v", f.calls)
	}
}

func TestSyncAbortsOnProviderFailure(t *testing.T) {
	f := &fakeClient{routes: map[string]*Response{
		"GET repos/o/r/issues": {Status: 403, Header: http.Header{"X-Ratelimit-Remaining": {"0"}, "X-Ratelimit-Reset": {"1793635762"}}, Body: []byte(`{}`)},
	}}
	res, err := Sync(context.Background(), f, "o/r", Cursors{Issues: "x"}, nil, DefaultLimits, now)
	if res != nil || AsError(err).Status != StatusRateLimited {
		t.Fatalf("got %v %v", res, err)
	}
}

func TestCloseIssueReadsBeforeWriting(t *testing.T) {
	f := &fakeClient{routes: map[string]*Response{"GET repos/o/r/issues/5": ok(`{"state":"closed"}`)}}
	already, err := CloseIssue(context.Background(), f, "o/r#5", now)
	if err != nil || !already || len(f.calls) != 1 {
		t.Fatalf("closed issue must not be patched: %v %v %v", already, err, f.calls)
	}
	f = &fakeClient{routes: map[string]*Response{"GET repos/o/r/issues/5": ok(`{"state":"open"}`), "PATCH repos/o/r/issues/5": ok(`{"state":"closed"}`)}}
	already, err = CloseIssue(context.Background(), f, "o/r#5", now)
	if err != nil || already || len(f.calls) != 2 || !strings.Contains(f.calls[1], "state=closed") {
		t.Fatalf("open issue must be patched: %v %v %v", already, err, f.calls)
	}
	f = &fakeClient{routes: map[string]*Response{"GET repos/o/r/issues/5": ok(`{"state":"open","pull_request":{}}`)}}
	if _, err := CloseIssue(context.Background(), f, "o/r#5", now); err == nil || len(f.calls) != 1 {
		t.Fatalf("a pull request must not be closed: %v %v", err, f.calls)
	}
}

func TestSyncPagesCommitsAndSkipsOnlyFailingPRDetails(t *testing.T) {
	var page []string
	for i := 0; i < 100; i++ {
		page = append(page, `{"sha":"s`+strings.Repeat("x", i%3)+`","commit":{"message":"m","committer":{"date":"2026-11-01T00:00:00Z"}}}`)
	}
	f := &fakeClient{routes: map[string]*Response{
		"GET repos/o/r/issues":  ok(`[{"number":3,"title":"p","state":"open","updated_at":"2026-11-01T00:00:00Z","pull_request":{"merged_at":null}}]`),
		"GET repos/o/r/commits": ok("[" + strings.Join(page, ",") + "]"),
		// pulls/3 and pulls/4 are missing (404); pulls/5 is fine.
		"GET repos/o/r/pulls/5":               ok(`{"head":{"sha":"h5"}}`),
		"GET repos/o/r/commits/h5/check-runs": ok(`{"check_runs":[{"id":1,"name":"ci","status":"completed","conclusion":"success"}]}`),
		"GET repos/o/r/pulls/5/reviews":       ok(`[]`),
	}}
	res, err := Sync(context.Background(), f, "o/r", Cursors{}, []int{4, 5}, Limits{IssuePages: 1, CommitPages: 2, PRDetails: 5}, now)
	if err != nil {
		t.Fatalf("a per-PR failure must not fail the pass: %v", err)
	}
	pages := 0
	for _, c := range f.calls {
		if strings.HasPrefix(c, "GET repos/o/r/commits?") {
			pages++
		}
	}
	if pages != 2 {
		t.Fatalf("full commit pages must be followed: %d calls %v", pages, f.calls)
	}
	if len(res.Warnings) != 2 {
		t.Fatalf("warnings: %v", res.Warnings)
	}
	found := false
	for _, it := range res.Items {
		if it.Kind == "check" && it.State == "success" {
			found = true
		}
	}
	if !found {
		t.Fatal("an already known open PR must have its checks re-read")
	}
	// A rate limit on a detail call still aborts the pass.
	f.routes["GET repos/o/r/pulls/5"] = &Response{Status: 429, Header: http.Header{"Retry-After": {"5"}}}
	if _, err := Sync(context.Background(), f, "o/r", Cursors{}, []int{5}, Limits{IssuePages: 1, CommitPages: 1, PRDetails: 5}, now); AsError(err).Status != StatusRateLimited {
		t.Fatalf("rate limit on details: %v", err)
	}
}
