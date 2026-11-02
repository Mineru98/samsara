package httpapi

import (
	"net/http"
	"strings"
	"testing"
	"testing/fstest"
)

func TestStaticDashboard(t *testing.T) {
	s, _, url := newServer(t)
	base := strings.SplitN(url, "/#", 2)[0]

	// Without a UI build the runtime keeps the placeholder at / only.
	s.UI = nil
	if r, b := do(t, "GET", base+"/", "", ""); r.StatusCode != 200 || !strings.Contains(string(b), "Samsara runtime") {
		t.Fatalf("placeholder: %d %s", r.StatusCode, b)
	}
	if r, _ := do(t, "GET", base+"/projects/x", "", ""); r.StatusCode != 404 {
		t.Fatalf("deep link without UI = %d", r.StatusCode)
	}

	s.UI = fstest.MapFS{
		"index.html":          {Data: []byte(`<!doctype html><div id="root"></div><script type="module" src="/assets/index-abc.js"></script>`)},
		"assets/index-abc.js": {Data: []byte(`console.log(1)`)},
	}
	r, b := do(t, "GET", base+"/", "", "")
	if r.StatusCode != 200 || !strings.Contains(string(b), `id="root"`) {
		t.Fatalf("index: %d %s", r.StatusCode, b)
	}
	csp := r.Header.Get("Content-Security-Policy")
	if !strings.Contains(csp, "script-src 'self'") || !strings.Contains(csp, "connect-src 'self'") || strings.Contains(csp, "unsafe") {
		t.Fatalf("CSP must stay self-only: %q", csp)
	}
	// Deep links load the SPA shell (02 §3).
	if r, b := do(t, "GET", base+"/projects/prj_1?view=list", "", ""); r.StatusCode != 200 || !strings.Contains(string(b), `id="root"`) {
		t.Fatalf("deep link: %d %s", r.StatusCode, b)
	}
	r, b = do(t, "GET", base+"/assets/index-abc.js", "", "")
	if r.StatusCode != 200 || string(b) != "console.log(1)" || !strings.Contains(r.Header.Get("Content-Type"), "javascript") {
		t.Fatalf("asset: %d %q %s", r.StatusCode, r.Header.Get("Content-Type"), b)
	}
	if r.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("asset lost the defence headers")
	}
	// Missing assets and unknown API paths never fall back to HTML.
	if r, _ := do(t, "GET", base+"/assets/missing.js", "", ""); r.StatusCode != 404 {
		t.Fatalf("missing asset = %d", r.StatusCode)
	}
	if r, b := do(t, "GET", base+"/v1/nope", "", ""); r.StatusCode != 404 || !strings.Contains(string(b), "RESOURCE_NOT_FOUND") {
		t.Fatalf("unknown api = %d %s", r.StatusCode, b)
	}
	// Host checks still apply to static files (DNS rebinding).
	req, _ := http.NewRequest("GET", base+"/", nil)
	req.Host = "evil.example"
	if r, err := http.DefaultClient.Do(req); err != nil || r.StatusCode != 403 {
		t.Fatalf("foreign host = %v %v", r, err)
	} else {
		r.Body.Close()
	}
	// Serving the shell needs no bearer, but the API still does.
	if r, _ := do(t, "GET", base+"/v1/projects", "", ""); r.StatusCode != 401 {
		t.Fatal("API became public")
	}
}
