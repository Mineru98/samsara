package hooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestInstallPreservesUserHooks(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	user := `{"permissions":{"allow":["Bash(ls)"]},"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"/usr/bin/my-guard"}]}]}}`
	if err := os.WriteFile(filepath.Join(dir, SettingsRel), []byte(user), 0o644); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "bin", "samsara")
	_ = os.MkdirAll(filepath.Dir(exe), 0o755)
	_ = os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755)
	for i := 0; i < 2; i++ { // idempotent re-install
		if err := Install(dir, exe); err != nil {
			t.Fatal(err)
		}
	}
	b, _ := os.ReadFile(filepath.Join(dir, SettingsRel))
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if m["permissions"] == nil {
		t.Fatal("ACT-11: unrelated settings lost")
	}
	groups := m["hooks"].(map[string]any)["PreToolUse"].([]any)
	if len(groups) != 2 {
		t.Fatalf("expected user group + one managed group, got %d", len(groups))
	}
	if g := groups[0].(map[string]any); g["matcher"] != "Bash" {
		t.Fatalf("user hook changed: %v", g)
	}
	ok, err := PolicyHookInstalled(dir)
	if err != nil || !ok {
		t.Fatalf("policy hook should be detected: %v %v", ok, err)
	}
	managed := groups[1].(map[string]any)["hooks"].([]any)[0].(map[string]any)
	if managed["onFailure"] != "block" || managed["command"] != exe {
		t.Fatalf("managed entry: %v", managed)
	}
	args := managed["args"].([]any)
	if args[0] != "hook" || args[1] != "pre-tool-use" {
		t.Fatalf("exec-form args expected: %v", args)
	}
	_ = os.Remove(exe)
	if ok, _ := PolicyHookInstalled(dir); ok {
		t.Fatal("missing executable must not count as installed")
	}
}

func TestVersionGate(t *testing.T) {
	v, ok := ParseVersion("2.1.311 (Claude Code)")
	if !ok || v != "2.1.311" || !VersionAtLeast(v, MinBlockVersion) {
		t.Fatalf("parse %q", v)
	}
	if VersionAtLeast("2.1.200", MinBlockVersion) {
		t.Fatal("old version accepted")
	}
}
