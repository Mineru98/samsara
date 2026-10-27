// Package bridge is the `samsara hook <event>` entry point invoked by Claude
// Code. It decides offline (registry file only) whether a session is in
// scope; unmanaged sessions exit immediately without any socket connection,
// daemon start or backend probe (R-01). For managed sessions the PreToolUse
// gate fails closed. stdout carries only hook-contract JSON; diagnostics go
// to stderr.
package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"

	"github.com/Mineru98/samsara/runtime/internal/domain"
	"github.com/Mineru98/samsara/runtime/internal/procinfo"
	"github.com/Mineru98/samsara/runtime/internal/project"
)

// MaxInput bounds the hook stdin read.
const MaxInput = 4 << 20

// Caller performs IPC calls to the daemon.
type Caller interface {
	Call(ctx context.Context, path string, req, resp any) error
}

// Env is the bridge's view of its environment.
type Env struct {
	RegistryPath string
	Ticket       string // SAMSARA_LAUNCH_TICKET
	ManagedEnv   bool   // SAMSARA_MANAGED=1
	Caller       Caller
	// EnsureDaemon starts the per-user daemon (single instance) and waits
	// bounded for readiness. Only used for ENABLED+autostart projects.
	EnsureDaemon func(ctx context.Context) error
	Deadline     time.Duration
	ParentPID    int
}

// Input is the subset of Claude hook stdin used here.
type Input struct {
	SessionID        string          `json:"session_id"`
	Cwd              string          `json:"cwd"`
	HookEventName    string          `json:"hook_event_name"`
	Source           string          `json:"source"`
	ToolName         string          `json:"tool_name"`
	ToolInput        json.RawMessage `json:"tool_input"`
	ToolUseID        string          `json:"tool_use_id"`
	Prompt           string          `json:"prompt"`
	Message          string          `json:"message"`
	NotificationType string          `json:"notification_type"`
	AgentID          string          `json:"agent_id"`
	Reason           string          `json:"reason"`
}

var dispatchRe = regexp.MustCompile(`\[samsara-dispatch:([A-Za-z0-9_]+)\]`)

// Run executes one hook invocation and returns the process exit code.
func Run(sub string, stdin io.Reader, stdout, stderr io.Writer, env Env) int {
	if env.Deadline == 0 {
		env.Deadline = 2 * time.Second
	}
	raw, err := io.ReadAll(io.LimitReader(stdin, MaxInput+1))
	if err != nil || len(raw) > MaxInput {
		return failInput(sub, stdout, stderr, env, "hook input unreadable or too large")
	}
	var in Input
	if err := json.Unmarshal(raw, &in); err != nil {
		return failInput(sub, stdout, stderr, env, "hook input is not valid JSON")
	}
	cwd := in.Cwd
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	if c, err := project.Canonical(cwd); err == nil {
		cwd = c
	}
	reg, err := project.LoadRegistry(env.RegistryPath)
	if err != nil {
		reg = &project.Registry{}
		if env.Ticket != "" || env.ManagedEnv {
			return denyOrFail(sub, stdout, stderr, domain.ReasonRuntimeUnavailable, "registry unreadable")
		}
		fmt.Fprintln(stderr, "samsara: registry unreadable:", err)
		return 0
	}
	scope := reg.Classify(cwd, in.SessionID)
	managed := scope.Managed() || env.Ticket != "" || env.ManagedEnv
	if !managed && !scope.Relevant() {
		return 0 // inert: out of scope, no IPC, no probe (ACT-01/02)
	}
	ctx, cancel := context.WithTimeout(context.Background(), env.Deadline)
	defer cancel()
	ident := map[string]any{
		"protocol_version": "1", "request_id": domain.NewID("hookreq"), "event": hookEvent(sub),
		"claude_session_id": in.SessionID, "cwd": cwd, "ticket": env.Ticket, "managed_hint": managed, "source": in.Source,
	}
	if id, ok := procinfo.Of(env.ParentPID); ok {
		ident["process"] = id.Map()
	}
	switch sub {
	case "session-start":
		if env.EnsureDaemon != nil && scope.Entry != nil && scope.Entry.Activation == domain.ActivationEnabled && scope.Entry.Autostart {
			sctx, scancel := context.WithTimeout(context.Background(), 5*time.Second)
			if err := env.EnsureDaemon(sctx); err != nil {
				fmt.Fprintln(stderr, "samsara: daemon autostart failed:", err)
			}
			scancel()
		}
		var res struct {
			Managed bool   `json:"managed"`
			Context string `json:"context"`
			Reason  string `json:"reason"`
		}
		// Autostart (≤5s) + registration (≤4s) stay inside the 10s hook timeout.
		sctx, scancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer scancel()
		if err := env.Caller.Call(sctx, "/ipc/v1/sessions/register", ident, &res); err != nil {
			msg := "삼사라 런타임 연결 실패: 관리 작업의 도구 실행은 정책 확인 전까지 차단됩니다."
			if !managed {
				msg = "삼사라 런타임에 연결하지 못했습니다(관찰 기록 없음)."
			}
			fmt.Fprintln(stderr, "samsara:", err)
			return writeJSON(stdout, sessionStartOutput(msg))
		}
		text := res.Context
		if res.Reason != "" && res.Managed {
			text = fmt.Sprintf("삼사라 관리 등록이 거절되었습니다 (%s). 도구 실행은 차단됩니다.", res.Reason)
		}
		if text == "" {
			return 0
		}
		return writeJSON(stdout, sessionStartOutput(text))
	case "pre-tool-use":
		req := ident
		req["tool_name"] = in.ToolName
		req["tool_use_id"] = in.ToolUseID
		paths, background := toolScope(in.ToolName, in.ToolInput)
		req["affected_paths"] = paths
		req["background"] = background
		req["payload_digest"] = domain.DigestBytes(in.ToolInput)
		var dec struct {
			Decision   string `json:"decision"`
			ReasonCode string `json:"reason_code"`
			Message    string `json:"message"`
		}
		if err := env.Caller.Call(ctx, "/ipc/v1/tools/check", req, &dec); err != nil {
			if !managed {
				return 0 // ordinary session in an enabled project: observe-only
			}
			fmt.Fprintln(stderr, "samsara:", err)
			return writeJSON(stdout, denyOutput(domain.ReasonRuntimeUnavailable, "삼사라 런타임에 연결할 수 없어 관리 작업의 도구 실행을 차단합니다."))
		}
		switch dec.Decision {
		case domain.DecisionPass:
			return 0 // no permission override: Claude's own flow continues
		case domain.DecisionDeny:
			return writeJSON(stdout, denyOutput(dec.ReasonCode, dec.Message))
		}
		if managed {
			return writeJSON(stdout, denyOutput(domain.ReasonInvalidHookInput, "알 수 없는 정책 응답"))
		}
		return 0
	case "session-end":
		_ = env.Caller.Call(ctx, "/ipc/v1/sessions/finish", mergeMap(ident, map[string]any{"reason": in.Reason}), nil)
		return 0
	default:
		obs := mergeMap(ident, map[string]any{"tool_name": in.ToolName, "tool_use_id": in.ToolUseID,
			"failed": sub == "post-tool-use-failure", "notification_type": notificationType(in), "agent_id": in.AgentID})
		if sub == "user-prompt-submit" {
			if m := dispatchRe.FindStringSubmatch(in.Prompt); m != nil {
				obs["dispatch_id"] = m[1]
			}
		}
		if err := env.Caller.Call(ctx, "/ipc/v1/sessions/observe", obs, nil); err != nil {
			fmt.Fprintln(stderr, "samsara: observe:", err)
		}
		return 0
	}
}

func hookEvent(sub string) string {
	switch sub {
	case "session-start":
		return "SessionStart"
	case "user-prompt-submit":
		return "UserPromptSubmit"
	case "pre-tool-use":
		return "PreToolUse"
	case "post-tool-use":
		return "PostToolUse"
	case "post-tool-use-failure":
		return "PostToolUseFailure"
	case "notification":
		return "Notification"
	case "stop":
		return "Stop"
	case "session-end":
		return "SessionEnd"
	case "subagent-start":
		return "SubagentStart"
	case "subagent-stop":
		return "SubagentStop"
	}
	return sub
}

func notificationType(in Input) string {
	if in.NotificationType != "" {
		return in.NotificationType
	}
	return ""
}

// toolScope extracts the paths a tool will touch and whether it would run
// in the background. It never evaluates tool input as a command.
func toolScope(tool string, input json.RawMessage) ([]string, bool) {
	var m map[string]any
	if json.Unmarshal(input, &m) != nil {
		return nil, false
	}
	var paths []string
	for _, k := range []string{"file_path", "notebook_path", "path"} {
		if s, ok := m[k].(string); ok && s != "" {
			paths = append(paths, filepath.Clean(s))
		}
	}
	bg := false
	if v, ok := m["run_in_background"].(bool); ok && v && tool == "Bash" {
		bg = true
	}
	return paths, bg
}

func mergeMap(a, b map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

func sessionStartOutput(text string) map[string]any {
	return map[string]any{"hookSpecificOutput": map[string]any{"hookEventName": "SessionStart", "additionalContext": text}}
}

func denyOutput(code, msg string) map[string]any {
	if msg == "" {
		msg = "삼사라 정책에 따라 차단되었습니다."
	}
	return map[string]any{"hookSpecificOutput": map[string]any{
		"hookEventName":            "PreToolUse",
		"permissionDecision":       "deny",
		"permissionDecisionReason": "SAMSARA_" + code + ": " + msg,
	}}
}

func writeJSON(w io.Writer, v any) int {
	b, err := json.Marshal(v)
	if err != nil {
		return 2
	}
	if _, err := w.Write(append(b, '\n')); err != nil {
		return 2
	}
	return 0
}

// failInput handles unparseable input: managed gates deny, others pass.
func failInput(sub string, stdout, stderr io.Writer, env Env, msg string) int {
	fmt.Fprintln(stderr, "samsara:", msg)
	if env.Ticket != "" || env.ManagedEnv {
		return denyOrFail(sub, stdout, stderr, domain.ReasonInvalidHookInput, msg)
	}
	return 0
}

func denyOrFail(sub string, stdout, stderr io.Writer, code, msg string) int {
	if sub == "pre-tool-use" {
		return writeJSON(stdout, denyOutput(code, msg))
	}
	fmt.Fprintln(stderr, "samsara:", code, msg)
	return 0
}

// ErrNoDaemon is returned by callers when the socket is absent.
var ErrNoDaemon = errors.New("samsara daemon is not running")

// ParentPID returns the hook's parent (the Claude process when exec-form
// args are used without a shell).
func ParentPID() int {
	if s := os.Getenv("SAMSARA_TEST_PARENT_PID"); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			return n
		}
	}
	return os.Getppid()
}
