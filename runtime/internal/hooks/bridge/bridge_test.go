package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mineru98/samsara/runtime/internal/domain"
	"github.com/Mineru98/samsara/runtime/internal/project"
)

type spyCaller struct {
	calls []string
	resp  map[string]any
	err   error
}

func (s *spyCaller) Call(ctx context.Context, path string, req, resp any) error {
	s.calls = append(s.calls, path)
	if s.err != nil {
		return s.err
	}
	if resp != nil && s.resp != nil {
		b, _ := json.Marshal(s.resp)
		return json.Unmarshal(b, resp)
	}
	return nil
}

func registry(t *testing.T, root string, activation string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "registry.json")
	r := &project.Registry{Projects: []project.RegistryEntry{{ProjectID: "prj_1", Root: root, Activation: activation, Autostart: true}}}
	if err := r.Save(p); err != nil {
		t.Fatal(err)
	}
	return p
}

func input(cwd string) string {
	b, _ := json.Marshal(map[string]any{"session_id": "s1", "cwd": cwd, "tool_name": "Edit", "tool_input": map[string]string{"file_path": cwd + "/a"}})
	return string(b)
}

func TestInertForUnconfiguredAndDisabled(t *testing.T) {
	root := t.TempDir()
	root, _ = project.Canonical(root)
	for _, tc := range []struct{ name, reg string }{
		{"no registry", filepath.Join(t.TempDir(), "missing.json")},
		{"disabled", registry(t, root, domain.ActivationDisabled)},
	} {
		spy := &spyCaller{}
		started := false
		var out, errb bytes.Buffer
		code := Run("pre-tool-use", strings.NewReader(input(root)), &out, &errb, Env{RegistryPath: tc.reg, Caller: spy,
			EnsureDaemon: func(context.Context) error { started = true; return nil }})
		if code != 0 || out.Len() != 0 || len(spy.calls) != 0 || started {
			t.Fatalf("%s: ACT-01/02 inert path violated: code=%d out=%q calls=%v started=%v", tc.name, code, out.String(), spy.calls, started)
		}
		code = Run("session-start", strings.NewReader(input(root)), &out, &errb, Env{RegistryPath: tc.reg, Caller: spy,
			EnsureDaemon: func(context.Context) error { started = true; return nil }})
		if code != 0 || len(spy.calls) != 0 || started {
			t.Fatalf("%s: session-start must be inert", tc.name)
		}
	}
}

func TestManagedFailsClosed(t *testing.T) {
	root := t.TempDir()
	reg := registry(t, root, domain.ActivationEnabled)
	spy := &spyCaller{err: errors.New("connection refused")}
	var out, errb bytes.Buffer
	code := Run("pre-tool-use", strings.NewReader(input(root)), &out, &errb, Env{RegistryPath: reg, Caller: spy, Ticket: "t"})
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	var o map[string]map[string]string
	if err := json.Unmarshal(out.Bytes(), &o); err != nil {
		t.Fatalf("HK-08: stdout must be pure JSON: %q", out.String())
	}
	h := o["hookSpecificOutput"]
	if h["permissionDecision"] != "deny" || !strings.Contains(h["permissionDecisionReason"], domain.ReasonRuntimeUnavailable) {
		t.Fatalf("HK-03: daemon down must deny managed tools: %+v", h)
	}
	// HK-07: malformed input in a managed worker is denied too.
	out.Reset()
	Run("pre-tool-use", strings.NewReader("{not json"), &out, &errb, Env{RegistryPath: reg, Caller: spy, ManagedEnv: true})
	if !strings.Contains(out.String(), `"deny"`) {
		t.Fatalf("malformed input must deny: %q", out.String())
	}
}

func TestPassDoesNotOverridePermissions(t *testing.T) {
	root := t.TempDir()
	root, _ = project.Canonical(root)
	reg := registry(t, root, domain.ActivationEnabled)
	spy := &spyCaller{resp: map[string]any{"decision": "PASS", "reason_code": "OK"}}
	var out, errb bytes.Buffer
	if code := Run("pre-tool-use", strings.NewReader(input(root)), &out, &errb, Env{RegistryPath: reg, Caller: spy, Ticket: "t"}); code != 0 {
		t.Fatal(code)
	}
	if out.Len() != 0 {
		t.Fatalf("HK-01/INV-05: PASS must not emit a permission decision, got %q", out.String())
	}
	spy.resp = map[string]any{"decision": "DENY", "reason_code": "LEASE_REVOKED", "message": "회수됨"}
	Run("pre-tool-use", strings.NewReader(input(root)), &out, &errb, Env{RegistryPath: reg, Caller: spy, Ticket: "t"})
	if !strings.Contains(out.String(), "SAMSARA_LEASE_REVOKED") {
		t.Fatalf("deny output: %q", out.String())
	}
}

func TestEnabledUnmanagedDaemonDownPasses(t *testing.T) {
	root := t.TempDir()
	root, _ = project.Canonical(root)
	reg := registry(t, root, domain.ActivationEnabled)
	spy := &spyCaller{err: errors.New("down")}
	var out, errb bytes.Buffer
	Run("pre-tool-use", strings.NewReader(input(root)), &out, &errb, Env{RegistryPath: reg, Caller: spy})
	if out.Len() != 0 {
		t.Fatalf("an ordinary session in an enabled project is observation-only: %q", out.String())
	}
}

func TestToolScope(t *testing.T) {
	p, bg := toolScope("Bash", json.RawMessage(`{"command":"x","run_in_background":true}`))
	if !bg || len(p) != 0 {
		t.Fatal("background bash not detected")
	}
	p, _ = toolScope("Write", json.RawMessage(`{"file_path":"/a/../b/c.txt"}`))
	if len(p) != 1 || p[0] != "/b/c.txt" {
		t.Fatalf("paths: %v", p)
	}
}
