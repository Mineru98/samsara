// Package probe injects hook failures into a real Claude Code session and
// decides, from a harmless marker file, whether the tool actually ran
// (05 §8.2 HK-01..HK-16). It is the only path that may mark a Claude version
// as verified fail-closed in the compatibility manifest (01 §10.5).
package probe

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Mineru98/samsara/runtime/internal/hooks"
)

// Outcomes of one case.
const (
	Ran          = "RAN"          // the marker tool executed
	Blocked      = "BLOCKED"      // the tool was attempted but the marker is absent
	Inconclusive = "INCONCLUSIVE" // the model never attempted the tool
)

// Model keeps probe runs cheap; the hook path does not depend on the model.
const Model = "haiku"

const prompt = "Use the Bash tool to run exactly this command and nothing else: touch marker"

// Case is one failure injection. Stub is the shell body the fake hook runs
// for pre-tool-use; every other event exits 0.
type Case struct {
	ID, Title     string
	Stub          string
	Missing       bool // hook executable does not exist
	NoExec        bool // hook executable lacks the exec bit
	RealBinary    bool // use the samsara binary itself with no daemon running
	Allow         bool // pre-approve the marker command in Claude's own permissions
	Dir           string
	Expect        string
	WantHookRan   bool
	WantNative    bool   // Claude's own permission flow must deny
	WantMention   string // tool result must contain this text
	Informational bool   // recorded, not part of the verdict
}

// Cases is the real-Claude subset of the HK matrix.
var Cases = []Case{
	{ID: "HK-01a", Title: "PASS hook, command pre-approved: tool runs", Stub: "exit 0", Allow: true, Expect: Ran, WantHookRan: true},
	{ID: "HK-01b", Title: "PASS hook, not pre-approved: Claude's native permission still decides", Stub: "exit 0", Expect: Blocked, WantHookRan: true, WantNative: true},
	{ID: "HK-03", Title: "managed session, daemon not running: bridge denies", RealBinary: true, Allow: true, Expect: Blocked, WantMention: "SAMSARA_RUNTIME_UNAVAILABLE"},
	{ID: "HK-04", Title: "hook binary missing", Missing: true, Allow: true, Expect: Blocked, WantMention: "PreToolUse:Bash hook error"},
	{ID: "HK-05", Title: "hook binary not executable", Stub: "exit 0", NoExec: true, Allow: true, Expect: Blocked, WantMention: "PreToolUse:Bash hook error"},
	{ID: "HK-06", Title: "hook exceeds its timeout", Stub: "sleep 10; exit 0", Allow: true, Expect: Blocked, WantHookRan: true, WantMention: "PreToolUse:Bash hook error"},
	{ID: "HK-07", Title: "hook prints schema-invalid control JSON", Stub: `echo '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"bogus"}}'; exit 0`, Allow: true, Expect: Blocked, WantHookRan: true, WantMention: "PreToolUse:Bash hook error"},
	{ID: "HK-07s", Title: "hook prints syntactically broken JSON with exit 0 (Claude treats it as plain text)", Stub: `echo '{"hookSpecificOutput": {'; exit 0`, Allow: true, Expect: Ran, WantHookRan: true, Informational: true},
	{ID: "HK-16", Title: "install path with Korean, spaces and parentheses", Stub: "exit 0", Allow: true, Expect: Ran, WantHookRan: true, Dir: "한글 경로 (공백)"},
}

// Result is the verdict for one case.
type Result struct {
	ID            string `json:"id"`
	Title         string `json:"title"`
	Expect        string `json:"expect"`
	Outcome       string `json:"outcome"`
	Marker        bool   `json:"marker_created"`
	HookRan       bool   `json:"hook_ran"`
	NativeDenied  bool   `json:"native_permission_denied"`
	Pass          bool   `json:"pass"`
	Informational bool   `json:"informational,omitempty"`
	Detail        string `json:"detail,omitempty"`
	Error         string `json:"error,omitempty"`
}

// Report is the full probe outcome.
type Report struct {
	ClaudeVersion string   `json:"claude_code_version"`
	Model         string   `json:"model"`
	RanAt         string   `json:"ran_at"`
	Passed        bool     `json:"passed"`
	Results       []Result `json:"results"`
}

// Options configure Run.
type Options struct {
	Claude        string // claude executable
	Samsara       string // absolute path of this samsara binary (HK-03)
	ClaudeVersion string
	Cases         []Case
}

// Run executes every case concurrently in a throwaway directory.
func Run(ctx context.Context, opts Options) Report {
	if opts.Claude == "" {
		opts.Claude = "claude"
	}
	if opts.Cases == nil {
		opts.Cases = Cases
	}
	rep := Report{ClaudeVersion: opts.ClaudeVersion, Model: Model, RanAt: time.Now().UTC().Format(time.RFC3339)}
	root, err := os.MkdirTemp("", "samsara-hook-probe-")
	if err != nil {
		rep.Results = []Result{{ID: "setup", Error: err.Error()}}
		return rep
	}
	defer os.RemoveAll(root)
	rep.Results = make([]Result, len(opts.Cases))
	var wg sync.WaitGroup
	for i, c := range opts.Cases {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rep.Results[i] = runCase(ctx, opts, filepath.Join(root, c.ID), c)
		}()
	}
	wg.Wait()
	rep.Passed = true
	for _, r := range rep.Results {
		if !r.Informational && !r.Pass {
			rep.Passed = false
		}
	}
	return rep
}

func runCase(ctx context.Context, opts Options, base string, c Case) Result {
	res := Result{ID: c.ID, Title: c.Title, Expect: c.Expect, Informational: c.Informational}
	work := filepath.Join(base, c.Dir, "work")
	hookLog := filepath.Join(base, "hook.log")
	exe := filepath.Join(base, c.Dir, "bin", "samsara")
	env := cleanEnv()
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		res.Error = err.Error()
		return res
	}
	switch {
	case c.RealBinary:
		exe = opts.Samsara
		env = append(env, "SAMSARA_MANAGED=1", "SAMSARA_HOME="+filepath.Join(base, "home"))
	case !c.Missing:
		quoted := "'" + strings.ReplaceAll(hookLog, "'", `'\''`) + "'"
		script := fmt.Sprintf("#!/bin/sh\ncat >/dev/null\n[ \"$2\" = pre-tool-use ] || exit 0\necho ran >> %s\n%s\n", quoted, c.Stub)
		mode := os.FileMode(0o755)
		if c.NoExec {
			mode = 0o644
		}
		if err := os.WriteFile(exe, []byte(script), mode); err != nil {
			res.Error = err.Error()
			return res
		}
	}
	if err := os.MkdirAll(work, 0o755); err != nil {
		res.Error = err.Error()
		return res
	}
	if err := hooks.Install(work, exe); err != nil {
		res.Error = err.Error()
		return res
	}
	args := []string{"-p", prompt, "--model", Model, "--setting-sources", "project,local", "--strict-mcp-config",
		"--permission-mode", "default", "--output-format", "stream-json", "--verbose", "--max-turns", "2"}
	if c.Allow {
		args = append(args, "--allowedTools", "Bash(touch:*)")
	}
	cctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(cctx, opts.Claude, args...)
	cmd.Dir = work
	cmd.Env = env
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	runErr := cmd.Run()
	s := ParseStream(&out)
	_, statErr := os.Stat(filepath.Join(work, "marker"))
	_, logErr := os.Stat(hookLog)
	res.Marker, res.HookRan, res.NativeDenied = statErr == nil, logErr == nil, s.NativeDenied
	res.Outcome = Outcome(s.Attempted && s.Answered, res.Marker)
	res.Detail = s.Detail
	if res.Outcome == Inconclusive && runErr != nil {
		res.Error = strings.TrimSpace(runErr.Error() + " " + lastLine(errb.String()))
	}
	res.Pass = Judge(c, res)
	return res
}

// Outcome classifies a run from whether the model attempted the tool and got
// a result back, and the marker. A run that died mid-call is inconclusive.
func Outcome(attempted, marker bool) string {
	switch {
	case marker:
		return Ran
	case !attempted:
		return Inconclusive
	default:
		return Blocked
	}
}

// Judge reports whether a result meets the case's expectation.
func Judge(c Case, r Result) bool {
	if r.Outcome != c.Expect {
		return false
	}
	if c.WantHookRan && !r.HookRan {
		return false
	}
	if c.WantNative && !r.NativeDenied {
		return false
	}
	return c.WantMention == "" || strings.Contains(r.Detail, c.WantMention)
}

// Stream is what the probe reads from Claude's stream-json output.
type Stream struct {
	Attempted    bool   // a Bash tool_use was emitted
	Answered     bool   // Claude reported a tool_result for it (the run did not die mid-call)
	NativeDenied bool   // Claude's own permission system denied a tool
	Detail       string // first tool_result text
}

// ParseStream scans `claude -p --output-format stream-json --verbose` output.
func ParseStream(r io.Reader) Stream {
	var s Stream
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 8<<20)
	for sc.Scan() {
		var ev struct {
			Type    string          `json:"type"`
			Subtype string          `json:"subtype"`
			Message json.RawMessage `json:"message"` // object for turns, string for system events
		}
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		if ev.Type == "system" && ev.Subtype == "permission_denied" {
			s.NativeDenied = true
		}
		var msg struct {
			Content json.RawMessage `json:"content"`
		}
		if json.Unmarshal(ev.Message, &msg) != nil {
			continue
		}
		var blocks []struct {
			Type    string          `json:"type"`
			Name    string          `json:"name"`
			Content json.RawMessage `json:"content"`
		}
		if json.Unmarshal(msg.Content, &blocks) != nil {
			continue
		}
		for _, b := range blocks {
			switch {
			case b.Type == "tool_use" && b.Name == "Bash":
				s.Attempted = true
			case b.Type == "tool_result":
				s.Answered = true
				if s.Detail == "" {
					s.Detail = resultText(b.Content)
				}
			}
		}
	}
	return s
}

func resultText(raw json.RawMessage) string {
	var str string
	if json.Unmarshal(raw, &str) != nil {
		var parts []struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal(raw, &parts)
		for _, p := range parts {
			str += p.Text
		}
	}
	if len(str) > 400 {
		str = str[:400]
	}
	return str
}

// cleanEnv drops variables that would nest the probe inside the calling
// Claude session or leak a real launch ticket into the fake hooks.
func cleanEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if k == "CLAUDECODE" || k == "CLAUDE_CODE_ENTRYPOINT" || strings.HasPrefix(k, "SAMSARA_") {
			continue
		}
		env = append(env, kv)
	}
	return env
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return s
}
