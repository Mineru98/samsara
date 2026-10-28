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
	if r, _ := do(t, "GET", base+"/v1/projects", "", ""); r.StatusCode != 401 {
		t.Fatalf("NET-01: unauthenticated = %d", r.StatusCode)
	}
	if r, _ := do(t, "GET", base+"/v1/projects?token="+code, "", ""); r.StatusCode != 401 {
		t.Fatal("NET-02: query credentials must not work")
	}
	r, b := do(t, "POST", base+"/v1/ui/sessions", "", `{"code":"`+code+`"}`)
	if r.StatusCode != 200 {
		t.Fatalf("exchange: %d %s", r.StatusCode, b)
	}
	var tok struct{ Token string }
	_ = json.Unmarshal(b, &tok)
	if r, _ := do(t, "POST", base+"/v1/ui/sessions", "", `{"code":"`+code+`"}`); r.StatusCode != 401 {
		t.Fatal("one-time code reused")
	}
	if r, _ := do(t, "GET", base+"/v1/projects", tok.Token, "", "Origin", "http://evil.example"); r.StatusCode != 403 {
		t.Fatal("foreign origin accepted")
	}
	r, b = do(t, "GET", base+"/v1/projects/"+pid+"/snapshot", tok.Token, "")
	if r.StatusCode != 200 || r.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("snapshot: %d %s", r.StatusCode, b)
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
	var ev sqlite.EventEnvelope
	if err := json.Unmarshal([]byte(data), &ev); err != nil || id == "" {
		t.Fatalf("event: %q %v", data, err)
	}
	if ev.PreviousProjectVersion != snap.ProjectVersion || ev.Type != "project.disable_requested" {
		t.Fatalf("EVT-02: %+v (snapshot version %s)", ev, snap.ProjectVersion)
	}
	if r, _ := do(t, "GET", base+"/v1/projects/"+pid+"/events?after=a", tok.Token, "", "Last-Event-ID", "b"); r.StatusCode != 400 {
		t.Fatal("EVT-14: disagreeing cursors must be 400")
	}
	if r, _ := do(t, "POST", base+"/v1/projects/"+pid+"/disable", tok.Token, ""); r.StatusCode != 400 {
		t.Fatal("commands require Idempotency-Key")
	}
	if r, _ := do(t, "DELETE", base+"/v1/ui/sessions/current", tok.Token, ""); r.StatusCode != 204 {
		t.Fatal("revoke failed")
	}
	if r, _ := do(t, "GET", base+"/v1/projects", tok.Token, ""); r.StatusCode != 401 {
		t.Fatal("revoked token still works")
	}
}
