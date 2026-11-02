package probe

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
