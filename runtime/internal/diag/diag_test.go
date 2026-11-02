package diag

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var planted = []string{
	"ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789",
	"github_pat_11ABCDEFG0123456789_abcdefghijklmnop",
	"sk-ant-api03-SECRETSECRETSECRET",
	"Bearer eyJhbGciOiJIUzI1NiJ9.payload.sig",
	"rm -rf /tmp/user-tool-input",
	"please refactor the billing module (user prompt)",
	"bootstrap-code-123456",
	"hunter2pass",
	"lt_launch_ticket_xyz",
	"quotedsecretvalue",
}

func TestRedactRemovesPlantedSecrets(t *testing.T) {
	r := &Redactor{}
	in := map[string]any{
		"github":     map[string]any{"token": planted[0], "repository": "owner/name"},
		"env":        []any{"GH_TOKEN=" + planted[1]},
		"error":      "call failed: Authorization: " + planted[3],
		"key":        planted[2],
		"detail":     map[string]any{"tool_input": map[string]any{"command": planted[4]}, "tool": "Bash"},
		"prompt":     planted[5],
		"code":       planted[6],
		"activation": "ENABLED",
		"git_error":  "fatal: unable to access 'https://bob:hunter2pass@github.com/o/r.git/'",
		"env_ticket": map[string]any{"launch_ticket": planted[8]},
		"probe":      map[string]any{"bash_commands": []any{planted[4]}},
		"raw":        `{"token": "quotedsecretvalue"}`,
	}
	b, _ := json.Marshal(r.Value(in))
	out := string(b)
	for _, s := range planted {
		if strings.Contains(out, s) {
			t.Errorf("planted value %q survived: %s", s, out)
		}
	}
	for _, keep := range []string{"owner/name", "ENABLED", `"tool":"Bash"`} {
		if !strings.Contains(out, keep) {
			t.Errorf("non-sensitive %q removed: %s", keep, out)
		}
	}
	if r.Count < len(planted) {
		t.Errorf("count = %d", r.Count)
	}
}

func TestLogTailRedactsJSONAndText(t *testing.T) {
	p := filepath.Join(t.TempDir(), "daemon.log")
	lines := []string{
		`{"level":"INFO","msg":"daemon started","epoch":"e1"}`,
		`{"level":"WARN","msg":"gh failed","err":"token=` + planted[0] + `","prompt":"` + planted[5] + `"}`,
		`plain text with ` + planted[2],
	}
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := &Redactor{}
	tail := r.LogTail(p, 2)
	b, _ := json.Marshal(tail)
	if len(tail) != 2 || strings.Contains(string(b), "daemon started") {
		t.Fatalf("tail = %s", b)
	}
	for _, s := range planted {
		if strings.Contains(string(b), s) {
			t.Errorf("planted value %q survived: %s", s, b)
		}
	}
}
