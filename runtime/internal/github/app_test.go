package github

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const testToken = "ghs_installationtokenvalue0123456789"

// fakeAppAPI plays the App endpoints: it verifies the JWT, issues one
// installation token and keeps check runs per head SHA.
type fakeAppAPI struct {
	t      *testing.T
	pub    *rsa.PublicKey
	mu     sync.Mutex
	mints  int
	runs   []map[string]any
	calls  []string
	reject string // non-empty: POST check-runs answers 422 with this message
}

func (f *fakeAppAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, r.Method+" "+r.URL.Path)
	body, _ := io.ReadAll(r.Body)
	auth := r.Header.Get("Authorization")
	switch {
	case r.Method == "POST" && r.URL.Path == "/app/installations/9/access_tokens":
		if err := f.verifyJWT(strings.TrimPrefix(auth, "Bearer ")); err != nil {
			w.WriteHeader(401)
			fmt.Fprintf(w, `{"message":%q}`, err.Error())
			return
		}
		var req struct {
			Permissions map[string]string `json:"permissions"`
		}
		_ = json.Unmarshal(body, &req)
		if len(req.Permissions) != 1 || req.Permissions["checks"] != "write" {
			f.t.Errorf("token must be narrowed to checks:write, got %v", req.Permissions)
		}
		f.mints++
		w.WriteHeader(201)
		fmt.Fprintf(w, `{"token":%q,"expires_at":%q}`, testToken, time.Now().Add(time.Hour).UTC().Format(time.RFC3339))
		return
	}
	if auth != "token "+testToken {
		w.WriteHeader(401)
		fmt.Fprint(w, `{"message":"Bad credentials"}`)
		return
	}
	switch {
	case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/repos/o/r/commits/") && strings.HasSuffix(r.URL.Path, "/check-runs"):
		q := r.URL.Query()
		if q.Get("check_name") != PolicyCheckName || q.Get("app_id") != "77" || q.Get("filter") != "all" {
			f.t.Errorf("check run lookup query: %v", q)
		}
		head := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/repos/o/r/commits/"), "/check-runs")
		var out []map[string]any
		for _, run := range f.runs {
			if run["head_sha"] == head {
				out = append(out, run)
			}
		}
		b, _ := json.Marshal(map[string]any{"check_runs": out})
		w.Write(b)
	case r.Method == "POST" && r.URL.Path == "/repos/o/r/check-runs":
		if f.reject != "" {
			w.WriteHeader(422)
			fmt.Fprintf(w, `{"message":%q}`, f.reject)
			return
		}
		run := map[string]any{}
		_ = json.Unmarshal(body, &run)
		run["id"], run["app"] = float64(100+len(f.runs)), map[string]any{"id": float64(77)}
		f.runs = append(f.runs, run)
		b, _ := json.Marshal(run)
		w.WriteHeader(201)
		w.Write(b)
	case r.Method == "PATCH" && strings.HasPrefix(r.URL.Path, "/repos/o/r/check-runs/"):
		upd := map[string]any{}
		_ = json.Unmarshal(body, &upd)
		for _, run := range f.runs {
			if fmt.Sprint(run["id"]) == strings.TrimPrefix(r.URL.Path, "/repos/o/r/check-runs/") {
				for k, v := range upd {
					run[k] = v
				}
			}
		}
		w.Write([]byte(`{}`))
	default:
		w.WriteHeader(404)
		fmt.Fprint(w, `{"message":"Not Found"}`)
	}
}

func (f *fakeAppAPI) verifyJWT(tok string) error {
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return fmt.Errorf("malformed jwt")
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return err
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(f.pub, crypto.SHA256, sum[:], sig); err != nil {
		return fmt.Errorf("bad signature")
	}
	raw, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var c struct {
		Iss string `json:"iss"`
		Iat int64  `json:"iat"`
		Exp int64  `json:"exp"`
	}
	if err := json.Unmarshal(raw, &c); err != nil || c.Iss != "77" || c.Exp-c.Iat > 600 || c.Exp <= time.Now().Unix() {
		return fmt.Errorf("bad claims %s", raw)
	}
	return nil
}

func newTestKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func newFakeApp(t *testing.T) (*AppPublisher, *fakeAppAPI) {
	key := newTestKey(t)
	api := &fakeAppAPI{t: t, pub: &key.PublicKey}
	srv := httptest.NewServer(api)
	t.Cleanup(srv.Close)
	return NewAppPublisher(AppCredential{AppID: 77, InstallationID: 9, APIURL: srv.URL}, key), api
}

// 04 §10.8 step 7: the check is created once per group and a retried
// publication updates the run this App already made instead of adding one.
func TestAppPublisherCreatesThenUpdatesSameGroup(t *testing.T) {
	p, api := newFakeApp(t)
	ctx := context.Background()
	id1, err := p.PublishPolicyCheck(ctx, PolicyCheck{Repository: "o/r", HeadSHA: "g1", ExternalID: "pvg_1:d", Status: "in_progress", Summary: "pending"})
	if err != nil {
		t.Fatal(err)
	}
	id2, err := p.PublishPolicyCheck(ctx, PolicyCheck{Repository: "o/r", HeadSHA: "g1", ExternalID: "pvg_1:d", Status: "completed", Conclusion: "success", Summary: "granted"})
	if err != nil {
		t.Fatal(err)
	}
	if id1 != "100" || id2 != id1 || len(api.runs) != 1 {
		t.Fatalf("same group must reuse its run: %s %s runs=%d", id1, id2, len(api.runs))
	}
	if r := api.runs[0]; r["name"] != PolicyCheckName || r["status"] != "completed" || r["conclusion"] != "success" || r["external_id"] != "pvg_1:d" {
		t.Fatalf("run: %v", r)
	}
	// 04 §10.9: a new group on the same head SHA gets a fresh run.
	id3, err := p.PublishPolicyCheck(ctx, PolicyCheck{Repository: "o/r", HeadSHA: "g1", ExternalID: "pvg_2:e", Status: "in_progress", Summary: "pending"})
	if err != nil || id3 == id1 || len(api.runs) != 2 {
		t.Fatalf("new group must get a new run: %s %v", id3, err)
	}
	if api.mints != 1 {
		t.Fatalf("installation token must be cached, minted %d times", api.mints)
	}
	if _, ok := api.runs[1]["conclusion"]; ok {
		t.Fatal("an in_progress run carries no conclusion")
	}
}

// The installation token never leaves memory: errors are redacted and the
// token is re-minted only near expiry.
func TestAppPublisherNeverLeaksToken(t *testing.T) {
	p, api := newFakeApp(t)
	api.reject = "validation failed for " + testToken
	_, err := p.PublishPolicyCheck(context.Background(), PolicyCheck{Repository: "o/r", HeadSHA: "g1", ExternalID: "x", Status: "in_progress"})
	if err == nil || strings.Contains(err.Error(), testToken) || !strings.Contains(err.Error(), "[REDACTED]") {
		t.Fatalf("error must be redacted: %v", err)
	}
	if AsError(err).HTTP != 422 {
		t.Fatalf("classified: %v", err)
	}
	now := time.Now()
	p.Now = func() time.Time { return now.Add(59*time.Minute + 30*time.Second) }
	if err := p.Verify(context.Background()); err != nil {
		t.Fatal(err)
	}
	if api.mints != 2 {
		t.Fatalf("a token about to expire must be refreshed, minted %d", api.mints)
	}
}

func TestAppPublisherWrongKeyIsAuthRequired(t *testing.T) {
	p, _ := newFakeApp(t)
	p.key = newTestKey(t)
	if err := p.Verify(context.Background()); AsError(err).Status != StatusAuthRequired {
		t.Fatalf("a key of another App must be refused: %v", err)
	}
}

// The credential file and the key must be private to the user.
func TestLoadAppPublisher(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if p, err := LoadAppPublisher(dir); p != nil || err != nil {
		t.Fatalf("no credential: %v %v", p, err)
	}
	key := newTestKey(t)
	keyPath := filepath.Join(dir, "app.pem")
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), 0o600); err != nil {
		t.Fatal(err)
	}
	credPath := filepath.Join(dir, AppCredentialFile)
	write := func(body string, mode os.FileMode) {
		t.Helper()
		_ = os.Remove(credPath)
		if err := os.WriteFile(credPath, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
		_ = os.Chmod(credPath, mode)
	}
	good := fmt.Sprintf(`{"app_id":77,"installation_id":9,"private_key_path":%q}`, keyPath)
	write(good, 0o600)
	p, err := LoadAppPublisher(dir)
	if err != nil || p == nil || p.AppID() != 77 || p.InstallationID() != 9 {
		t.Fatalf("load: %v %v", p, err)
	}
	for name, tc := range map[string]struct {
		body string
		mode os.FileMode
	}{
		"group readable": {good, 0o640},
		"unknown field":  {strings.Replace(good, `"app_id"`, `"token":"x","app_id"`, 1), 0o600},
		"no app":         {strings.Replace(good, `"app_id":77`, `"app_id":0`, 1), 0o600},
		"relative key":   {`{"app_id":77,"installation_id":9,"private_key_path":"app.pem"}`, 0o600},
	} {
		write(tc.body, tc.mode)
		if p, err := LoadAppPublisher(dir); err == nil || p != nil {
			t.Fatalf("%s: must be refused", name)
		}
	}
	// A symlinked key could be swapped after the check; it is refused.
	link := filepath.Join(dir, "link.pem")
	if err := os.Symlink(keyPath, link); err != nil {
		t.Fatal(err)
	}
	write(strings.Replace(good, keyPath, link, 1), 0o600)
	if _, err := LoadAppPublisher(dir); err == nil {
		t.Fatal("a symlinked key must be refused")
	}
	write(good, 0o600)
	_ = os.Chmod(dir, 0o770)
	if _, err := LoadAppPublisher(dir); err == nil {
		t.Fatal("a group-writable credential directory must be refused")
	}
	_ = os.Chmod(dir, 0o700)
	_ = os.Chmod(keyPath, 0o644)
	if _, err := LoadAppPublisher(dir); err == nil || !strings.Contains(err.Error(), "0600") {
		t.Fatalf("a readable private key must be refused: %v", err)
	}
}
