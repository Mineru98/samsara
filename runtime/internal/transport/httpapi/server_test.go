package httpapi

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Mineru98/samsara/runtime/api"
	"github.com/Mineru98/samsara/runtime/api/apitest"
	"github.com/Mineru98/samsara/runtime/internal/app"
	"github.com/Mineru98/samsara/runtime/internal/backend"
	"github.com/Mineru98/samsara/runtime/internal/backend/standalone"
	"github.com/Mineru98/samsara/runtime/internal/domain"
	"github.com/Mineru98/samsara/runtime/internal/paths"
	"github.com/Mineru98/samsara/runtime/internal/store/sqlite"
)

func newServer(t *testing.T) (*Server, string, string) {
	t.Helper()
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	_ = os.MkdirAll(repo, 0o755)
	if out, err := exec.Command("git", "-C", repo, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	l := paths.Layout{StateDir: filepath.Join(dir, "s"), RuntimeDir: filepath.Join(dir, "r"), WorktreeRoot: filepath.Join(dir, "w")}
	_ = l.Ensure()
	db, err := sqlite.Open(context.Background(), l.DBPath(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	a := app.New(db, l, backend.NewSet(domain.SystemClock{}, &standalone.Adapter{}), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := a.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	res, err := a.RegisterProject(context.Background(), app.Cmd{Principal: app.Principal{ID: "t"}}, app.RegisterProjectRequest{Path: repo})
	if err != nil {
		t.Fatal(err)
	}
	s := New(a)
	ln, err := s.Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	hs := &http.Server{Handler: s}
	go func() { _ = hs.Serve(ln) }()
	t.Cleanup(func() { hs.Close() })
	boot, err := s.NewBootstrapCode(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	url := boot.(map[string]string)["url"]
	return s, res.ProjectID, url
}

func do(t *testing.T, method, url, tok, body string, hdr ...string) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	return res, b
}

func TestBootstrapAuthAndSSE(t *testing.T) {
	s, pid, url := newServer(t)
	base := strings.SplitN(url, "/#", 2)[0]
	code := strings.SplitN(url, "#bootstrap=", 2)[1]
	refErr := api.Ref("common.schema.json", "ErrorResponse")
	fx := apitest.NewRecorder(t, "http-errors", "HTTP error bodies from the loopback API: missing bearer, foreign origin, disagreeing cursors and a command without Idempotency-Key.", nil)
	r, b := do(t, "GET", base+"/v1/projects", "", "")
	if r.StatusCode != 401 {
		t.Fatalf("NET-01: unauthenticated = %d", r.StatusCode)
	}
	fx.AddRaw(refErr, "401 missing bearer token", b)
	if r, _ := do(t, "GET", base+"/v1/projects?token="+code, "", ""); r.StatusCode != 401 {
		t.Fatal("NET-02: query credentials must not work")
	}
	r, b = do(t, "POST", base+"/v1/ui/sessions", "", `{"code":"`+code+`"}`)
	if r.StatusCode != 200 {
		t.Fatalf("exchange: %d %s", r.StatusCode, b)
	}
	var tok struct{ Token string }
	_ = json.Unmarshal(b, &tok)
	if r, _ := do(t, "POST", base+"/v1/ui/sessions", "", `{"code":"`+code+`"}`); r.StatusCode != 401 {
		t.Fatal("one-time code reused")
	}
	if r, b := do(t, "GET", base+"/v1/projects", tok.Token, "", "Origin", "http://evil.example", "X-Request-Id", "req-1"); r.StatusCode != 403 {
		t.Fatal("foreign origin accepted")
	} else {
		fx.AddRaw(refErr, "403 foreign origin (X-Request-Id echoed)", b)
	}
	r, b = do(t, "GET", base+"/v1/projects/"+pid+"/snapshot", tok.Token, "")
	if r.StatusCode != 200 || r.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("snapshot: %d %s", r.StatusCode, b)
	}
	if err := api.ValidateStrict(api.Ref("snapshot.schema.json", "ProjectSnapshot"), b); err != nil {
		t.Fatalf("snapshot contract: %v", err)
	}
	for path, ref := range map[string]string{
		"/v1/runtime":          api.Ref("entities.schema.json", "RuntimeInfo"),
		"/v1/catalog/snapshot": api.Ref("snapshot.schema.json", "CatalogSnapshot"),
		"/v1/projects":         api.Ref("snapshot.schema.json", "ProjectList"),
	} {
		r, body := do(t, "GET", base+path, tok.Token, "")
		if r.StatusCode != 200 {
			t.Fatalf("%s: %d %s", path, r.StatusCode, body)
		}
		if err := api.ValidateStrict(ref, body); err != nil {
			t.Fatalf("%s contract: %v", path, err)
		}
	}
	var snap sqlite.Snapshot
	_ = json.Unmarshal(b, &snap)

	// Stream from the snapshot cursor, then commit a change.
	req, _ := http.NewRequest("GET", base+"/v1/projects/"+pid+"/events?after="+snap.SnapshotCursor, nil)
	req.Header.Set("Authorization", "Bearer "+tok.Token)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := http.DefaultClient.Do(req.WithContext(ctx))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if !strings.HasPrefix(res.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("content type %s", res.Header.Get("Content-Type"))
	}
	go func() {
		time.Sleep(200 * time.Millisecond)
		_ = s.App.ArchiveProject(context.Background(), app.Cmd{Principal: app.Principal{ID: "t"}}, pid) // fails (enabled) – no event
		_, _ = s.App.DisableProject(context.Background(), app.Cmd{Principal: app.Principal{ID: "t"}}, pid)
	}()
	sc := bufio.NewScanner(res.Body)
	var id, data string
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "id: ") {
			id = line[4:]
		}
		if strings.HasPrefix(line, "data: ") {
			data = line[6:]
			break
		}
	}
	if err := api.ValidateStrict(api.Ref("event.schema.json", "ProjectEvent"), []byte(data)); err != nil {
		t.Fatalf("event contract: %v", err)
	}
	var ev sqlite.EventEnvelope
	if err := json.Unmarshal([]byte(data), &ev); err != nil || id == "" {
		t.Fatalf("event: %q %v", data, err)
	}
	if ev.PreviousProjectVersion != snap.ProjectVersion || ev.Type != "project.disable_requested" {
		t.Fatalf("EVT-02: %+v (snapshot version %s)", ev, snap.ProjectVersion)
	}
	if r, b := do(t, "GET", base+"/v1/projects/"+pid+"/events?after=a", tok.Token, "", "Last-Event-ID", "b"); r.StatusCode != 400 {
		t.Fatal("EVT-14: disagreeing cursors must be 400")
	} else {
		fx.AddRaw(refErr, "400 after and Last-Event-ID disagree", b)
	}
	if r, b := do(t, "POST", base+"/v1/projects/"+pid+"/disable", tok.Token, ""); r.StatusCode != 400 {
		t.Fatal("commands require Idempotency-Key")
	} else {
		fx.AddRaw(refErr, "400 command without Idempotency-Key", b)
	}
	fx.Save()
	if r, _ := do(t, "DELETE", base+"/v1/ui/sessions/current", tok.Token, ""); r.StatusCode != 204 {
		t.Fatal("revoke failed")
	}
	if r, _ := do(t, "GET", base+"/v1/projects", tok.Token, ""); r.StatusCode != 401 {
		t.Fatal("revoked token still works")
	}
}

func TestGitHubHistoryAndRefreshContract(t *testing.T) {
	_, pid, bootstrapURL := newServer(t)
	base := strings.SplitN(bootstrapURL, "/#", 2)[0]
	code := strings.SplitN(bootstrapURL, "#bootstrap=", 2)[1]
	response, body := do(t, http.MethodPost, base+"/v1/ui/sessions", "", `{"code":"`+code+`"}`)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("bootstrap: %d %s", response.StatusCode, body)
	}
	var session struct{ Token string }
	if err := json.Unmarshal(body, &session); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method, path, schema string
		status               int
	}{
		{http.MethodGet, "/v1/projects/" + pid + "/github-history", "GitHubHistory", http.StatusOK},
		{http.MethodPost, "/v1/projects/" + pid + "/refresh", "RefreshProjectResult", http.StatusAccepted},
	} {
		response, body := do(t, tc.method, base+tc.path, session.Token, "", "Idempotency-Key", "contract-"+tc.schema)
		if response.StatusCode != tc.status {
			t.Fatalf("%s: %d %s", tc.path, response.StatusCode, body)
		}
		if err := api.ValidateStrict(api.Ref("entities.schema.json", tc.schema), body); err != nil {
			t.Fatalf("%s: %v; body: %s", tc.path, err, body)
		}
	}
}

// 01 §18.1: a stream resumed with Last-Event-ID counts as a reconnect and
// every reset_required is counted by reason.
func TestSSEReconnectAndResetMetrics(t *testing.T) {
	s, pid, url := newServer(t)
	base := strings.SplitN(url, "/#", 2)[0]
	code := strings.SplitN(url, "#bootstrap=", 2)[1]
	_, b := do(t, "POST", base+"/v1/ui/sessions", "", `{"code":"`+code+`"}`)
	var tok struct{ Token string }
	_ = json.Unmarshal(b, &tok)
	r, body := do(t, "GET", base+"/v1/projects/"+pid+"/events", tok.Token, "", "Last-Event-ID", "not-a-cursor")
	if r.StatusCode != 200 || !strings.Contains(string(body), "CURSOR_INVALID") {
		t.Fatalf("stream: %d %s", r.StatusCode, body)
	}
	m := s.App.Metrics
	if m.SSEConnects.View()["project"] != 1 || m.SSEReconnects.View()["project"] != 1 || m.SSEResets.View()["CURSOR_INVALID"] != 1 {
		t.Fatalf("connects %v reconnects %v resets %v", m.SSEConnects.View(), m.SSEReconnects.View(), m.SSEResets.View())
	}
}
