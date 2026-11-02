package probe

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Mineru98/samsara/runtime/internal/domain"
	"github.com/Mineru98/samsara/runtime/internal/hooks/bridge"
	"github.com/Mineru98/samsara/runtime/internal/paths"
	"github.com/Mineru98/samsara/runtime/internal/transport/ipc"
)

// Lines captured from `claude -p --output-format stream-json --verbose` 2.1.316.
const hookBlockedStream = `{"type":"system","subtype":"init","session_id":"s"}
{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"touch marker"}}]}}
{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","is_error":true,"content":"PreToolUse:Bash hook error: [/x/samsara hook pre-tool-use]: timed out; blocking because onFailure is \"block\""}]}}
not json
{"type":"result","subtype":"success","permission_denials":[]}
`

const nativeDeniedStream = `{"type":"assistant","message":{"content":[{"type":"text","text":"ok"},{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"touch marker"}}]}}
{"type":"system","subtype":"permission_denied","tool_name":"Bash","message":"touch needs approval"}
{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":[{"type":"text","text":"touch needs approval"}]}]}}
`

func TestParseStream(t *testing.T) {
	s := ParseStream(strings.NewReader(hookBlockedStream))
	if !s.Attempted || !s.Answered || s.NativeDenied || !strings.Contains(s.Detail, `onFailure is "block"`) {
		t.Fatalf("hook-blocked stream misread: %+v", s)
	}
	s = ParseStream(strings.NewReader(nativeDeniedStream))
	if !s.Attempted || !s.NativeDenied || s.Detail != "touch needs approval" {
		t.Fatalf("native-denied stream misread: %+v", s)
	}
	if s := ParseStream(strings.NewReader(`{"type":"assistant","message":{"content":[{"type":"text","text":"no"}]}}`)); s.Attempted {
		t.Fatal("text-only answer counted as a tool attempt")
	}
}

func TestDiedMidCallIsNotBlocked(t *testing.T) {
	s := ParseStream(strings.NewReader(`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash"}]}}`))
	if !s.Attempted || s.Answered {
		t.Fatalf("%+v", s)
	}
	if Outcome(s.Attempted && s.Answered, false) != Inconclusive {
		t.Fatal("a run killed before the tool result must not count as blocked")
	}
}

func TestHookCaseRejectsNativeDenial(t *testing.T) {
	for _, c := range Cases {
		if c.ID == "HK-04" && Judge(c, Result{Outcome: Blocked, NativeDenied: true, Detail: "touch needs approval"}) {
			t.Fatal("HK-04 passed on Claude's own permission denial instead of the hook block")
		}
	}
}

func TestOutcomeNeverPassesWithoutAttempt(t *testing.T) {
	if got := Outcome(false, false); got != Inconclusive {
		t.Fatalf("no attempt and no marker must be inconclusive, got %s", got)
	}
	if Outcome(true, false) != Blocked || Outcome(false, true) != Ran || Outcome(true, true) != Ran {
		t.Fatal("outcome table wrong")
	}
	hk04 := Case{Expect: Blocked}
	if Judge(hk04, Result{Outcome: Inconclusive}) {
		t.Fatal("an unattempted tool must not count as blocked")
	}
}

func TestJudgeRequirements(t *testing.T) {
	native := Case{Expect: Blocked, WantHookRan: true, WantNative: true}
	if Judge(native, Result{Outcome: Blocked, HookRan: true}) {
		t.Fatal("HK-01b needs Claude's own permission denial")
	}
	if !Judge(native, Result{Outcome: Blocked, HookRan: true, NativeDenied: true}) {
		t.Fatal("HK-01b should pass")
	}
	daemonDown := Case{Expect: Blocked, WantMention: "SAMSARA_RUNTIME_UNAVAILABLE"}
	if Judge(daemonDown, Result{Outcome: Blocked, Detail: "needs approval"}) {
		t.Fatal("HK-03 must be blocked by the bridge, not by something else")
	}
}

// A fake claude that always attempts the tool and never runs it: every
// blocking case passes, every case expecting the tool to run fails, and the
// managed settings are written with the per-case hook path.
func TestRunWithFakeClaude(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "claude")
	script := "#!/bin/sh\ngrep -q '\"onFailure\": \"block\"' .claude/settings.local.json || exit 9\n" +
		`echo '{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash"}]}}'` + "\n" +
		`echo '{"type":"user","message":{"content":[{"type":"tool_result","content":"PreToolUse:Bash hook error: failed"}]}}'` + "\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	cases := []Case{
		{ID: "HK-04", Missing: true, Expect: Blocked, WantMention: "PreToolUse:Bash hook error"},
		{ID: "HK-16", Stub: "exit 0", Expect: Ran, Dir: "한글 경로 (공백)"},
		{ID: "HK-07s", Stub: "exit 0", Expect: Ran, Informational: true},
	}
	rep := Run(context.Background(), Options{Claude: fake, ClaudeVersion: "2.1.316", Cases: cases})
	if len(rep.Results) != 3 || rep.Passed {
		t.Fatalf("unexpected report: %+v", rep)
	}
	if r := rep.Results[0]; !r.Pass || r.Outcome != Blocked {
		t.Fatalf("HK-04: %+v", r)
	}
	if r := rep.Results[1]; r.Pass || r.Outcome != Blocked {
		t.Fatalf("HK-16 must fail when the tool never ran: %+v", r)
	}
	rep = Run(context.Background(), Options{Claude: fake, Cases: cases[:1]})
	if !rep.Passed {
		t.Fatalf("informational and passing cases only: %+v", rep)
	}
}

// Every P0 item of 05 §8.2 (HK-01..HK-15) has a real-Claude case or a named
// fake-layer substitute, and every named test exists.
func TestEveryP0HookItemIsCovered(t *testing.T) {
	covered := map[string]bool{}
	for _, c := range Cases {
		covered[c.ID[:5]] = true
	}
	dirs := map[string]string{"app": "../../app", "bridge": "../bridge", "hooks": ".."}
	for _, sub := range Substitutes {
		covered[sub.ID] = true
		if sub.Reason == "" || len(sub.Tests) == 0 {
			t.Fatalf("%s: substitute needs a reason and tests", sub.ID)
		}
		for _, ref := range sub.Tests {
			pkg, name, _ := strings.Cut(ref, ".")
			files, _ := filepath.Glob(filepath.Join(dirs[pkg], "*_test.go"))
			re := regexp.MustCompile(`(?m)^func ` + name + `\(t \*testing\.T\)`)
			found := false
			for _, f := range files {
				b, _ := os.ReadFile(f)
				found = found || re.Match(b)
			}
			if !found {
				t.Fatalf("%s cites missing test %s", sub.ID, ref)
			}
		}
	}
	for i := 1; i <= 15; i++ {
		if id := fmt.Sprintf("HK-%02d", i); !covered[id] {
			t.Fatalf("%s has neither a probe case nor a substitute", id)
		}
	}
}

func TestCheckedInOrder(t *testing.T) {
	a, b := Check{Tool: "Agent", Session: "s"}, Check{Tool: "Bash", Session: "s"}
	if !checkedInOrder([]Check{a, {Tool: "Read", Session: "s"}, b}, []string{"Agent", "Bash"}) {
		t.Fatal("subsequence from one session should match")
	}
	if checkedInOrder([]Check{b, a}, []string{"Agent", "Bash"}) {
		t.Fatal("order matters")
	}
	if checkedInOrder([]Check{a, {Tool: "Bash", Session: "other"}}, []string{"Agent", "Bash"}) {
		t.Fatal("HK-13: a check from another Claude session is a hidden worker")
	}
	if Judge(Case{Expect: Blocked, WantChecks: []string{"Bash"}}, Result{Outcome: Blocked}) {
		t.Fatal("a block the daemon never decided must not pass")
	}
}

// The fake daemon speaks the real IPC client protocol, and the real bridge
// turns its scripted decisions into the hook JSON Claude reads.
func TestFakeDaemonDrivesRealBridge(t *testing.T) {
	sock := paths.ForHome(t.TempDir()).SocketPath()
	f, err := StartFakeDaemon(sock, []string{domain.DecisionPass, domain.ReasonLeaseRevoked})
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	env := bridge.Env{RegistryPath: filepath.Join(t.TempDir(), "registry.json"), ManagedEnv: true, Caller: ipc.NewClient(sock)}
	in := `{"session_id":"s1","cwd":"/tmp","tool_name":"Bash","tool_input":{"command":"touch marker"}}`
	var out, errb bytes.Buffer
	if code := bridge.Run("pre-tool-use", strings.NewReader(in), &out, &errb, env); code != 0 || out.Len() != 0 {
		t.Fatalf("PASS must leave stdout empty: %d %q %s", code, out.String(), errb.String())
	}
	if code := bridge.Run("session-start", strings.NewReader(in), &out, &errb, env); code != 0 || out.Len() != 0 {
		t.Fatalf("other paths answer an empty result: %d %q", code, out.String())
	}
	bridge.Run("pre-tool-use", strings.NewReader(in), &out, &errb, env)
	var o struct {
		H map[string]string `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(out.Bytes(), &o); err != nil || o.H["permissionDecision"] != "deny" || !strings.Contains(o.H["permissionDecisionReason"], "SAMSARA_LEASE_REVOKED") {
		t.Fatalf("second check must be denied: %q", out.String())
	}
	if got := f.Checks(); len(got) != 2 || got[1] != (Check{Tool: "Bash", Session: "s1"}) {
		t.Fatalf("checks: %+v", got)
	}
}

func TestWrapperFailsSessionStartAndExecsBinary(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "real samsara")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho \"real:$*\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(dir, "it's.log")
	wrap := filepath.Join(dir, "samsara")
	if err := os.WriteFile(wrap, []byte(Wrapper(bin, log, true)), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(wrap, "hook", "pre-tool-use").Output()
	if err != nil || string(out) != "real:hook pre-tool-use\n" {
		t.Fatalf("pre-tool-use must reach the real binary: %q %v", out, err)
	}
	if _, err := os.Stat(log); err != nil {
		t.Fatal("pre-tool-use not recorded")
	}
	cmd := exec.Command(wrap, "hook", "session-start")
	cmd.Stdin = strings.NewReader("{}")
	if out, err := cmd.Output(); err == nil || len(out) != 0 {
		t.Fatalf("HK-10: SessionStart must fail before the bridge: %q %v", out, err)
	}
	if out, _ := exec.Command(filepath.Join(dir, "samsara"), "hook", "stop").Output(); string(out) != "real:hook stop\n" {
		t.Fatalf("other events pass through: %q", out)
	}
	_ = os.WriteFile(wrap, []byte(Wrapper(bin, log, false)), 0o755)
	if out, _ := exec.Command(wrap, "hook", "session-start").Output(); string(out) != "real:hook session-start\n" {
		t.Fatalf("without injection SessionStart passes through: %q", out)
	}
}

// Lines shaped like 2.1.316 output when a subagent runs Bash (HK-13).
const subagentStream = `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"a1","name":"Agent","input":{"prompt":"x"}}]}}
{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"a1","content":"Async agent launched"}]}}
{"type":"assistant","parent_tool_use_id":"a1","message":{"content":[{"type":"tool_use","id":"b1","name":"Bash","input":{"command":"touch marker"}}]}}
{"type":"user","parent_tool_use_id":"a1","message":{"content":[{"type":"tool_result","tool_use_id":"b1","content":"PreToolUse:Bash hook error: SAMSARA_LEASE_REVOKED: x"}]}}
`

func TestSubagentAndCommandsAreJudged(t *testing.T) {
	s := ParseStream(strings.NewReader(subagentStream))
	if !s.Subagent || len(s.Commands) != 1 || s.Commands[0] != "touch marker" || !strings.Contains(s.Detail, "SAMSARA_LEASE_REVOKED") {
		t.Fatalf("subagent stream misread: %+v", s)
	}
	if s := ParseStream(strings.NewReader(hookBlockedStream)); s.Subagent {
		t.Fatal("a parent Bash call is not a subagent call")
	}
	hk13 := Case{Expect: Blocked, WantSubagent: true}
	if Judge(hk13, Result{Outcome: Blocked}) {
		t.Fatal("HK-13 must not pass on a Bash call the parent made itself")
	}
	hk15 := Case{Expect: Blocked, WantCommands: []string{"sleep 5", "touch marker"}}
	if Judge(hk15, Result{Outcome: Blocked, Commands: []string{"sleep 5", "sleep 5"}}) {
		t.Fatal("HK-15 must see the marker command denied, not a retried sleep")
	}
	if !Judge(hk15, Result{Outcome: Blocked, Commands: []string{"sleep 5", "touch marker"}}) {
		t.Fatal("HK-15 should pass")
	}
}

// Only Bash checks consume the scripted decisions, so a model that calls
// another tool first cannot shift which call is denied.
func TestFakeDaemonScriptsBashOnly(t *testing.T) {
	sock := paths.ForHome(t.TempDir()).SocketPath()
	f, err := StartFakeDaemon(sock, []string{domain.ReasonLeaseRevoked})
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	c := ipc.NewClient(sock)
	var d struct {
		Decision string `json:"decision"`
	}
	for _, tool := range []string{"Agent", "Read", "Bash"} {
		if err := c.Call(context.Background(), "/ipc/v1/tools/check", map[string]string{"tool_name": tool, "claude_session_id": "s"}, &d); err != nil {
			t.Fatal(err)
		}
		if want := map[bool]string{true: domain.DecisionDeny, false: domain.DecisionPass}[tool == "Bash"]; d.Decision != want {
			t.Fatalf("%s: got %s want %s", tool, d.Decision, want)
		}
	}
}
