// Package tmux adapts a dedicated tmux server (explicit -L socket owned by
// Samsara) for managed workers (01 §8). It never attaches to the user's
// default tmux server for new workers, never builds shell strings from issue
// text, and never infers Claude readiness or completion from pane text.
package tmux

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Mineru98/samsara/runtime/internal/backend"
	"github.com/Mineru98/samsara/runtime/internal/domain"
	"github.com/Mineru98/samsara/runtime/internal/procinfo"
)

// SessionPrefix marks sessions owned by Samsara.
const SessionPrefix = "samsara-"

// Adapter implements Backend, WorkerLauncher, PromptSubmitter,
// SessionController and Focuser.
type Adapter struct {
	Binary string // default "tmux"
	Socket string // -L socket name; default samsara-<uid>
	Clock  domain.Clock
}

// New returns an adapter on the per-user dedicated socket.
func New(clock domain.Clock) *Adapter {
	return &Adapter{Binary: "tmux", Socket: fmt.Sprintf("samsara-%d", os.Getuid()), Clock: clock}
}

// Name implements backend.Backend.
func (a *Adapter) Name() string { return domain.BackendTmux }

func (a *Adapter) now() time.Time {
	if a.Clock != nil {
		return a.Clock.Now()
	}
	return time.Now().UTC()
}

func (a *Adapter) run(ctx context.Context, stdin []byte, args ...string) (string, string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, a.Binary, append([]string{"-L", a.Socket}, args...)...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	err := cmd.Run()
	return out.String(), errb.String(), err
}

// Capabilities is the tmux capability table.
func Capabilities() backend.Capabilities {
	return backend.Capabilities{
		CreateWorker:        backend.Capability{Level: backend.Supported},
		AttachSession:       backend.Capability{Level: backend.Conditional, ReasonCode: "EXPLICIT_SOCKET_AND_PANE"},
		SubmitPrompt:        backend.Capability{Level: backend.Conditional, ReasonCode: "HOOK_READINESS_AND_INPUT_OWNERSHIP"},
		ObserveProcess:      backend.Capability{Level: backend.Supported},
		ObserveAgent:        backend.Capability{Level: backend.Conditional, ReasonCode: "HOOK_EVENTS"},
		ReadOutput:          backend.Capability{Level: backend.Conditional, ReasonCode: "OPT_IN_CAPTURE"},
		RequestInterrupt:    backend.Capability{Level: backend.Supported},
		RestoreConversation: backend.Capability{Level: backend.Unsupported, ReasonCode: "SEPARATE_RESUME_LAUNCHER"},
	}
}

// Probe is read-only: it checks the binary version and never starts a server.
func (a *Adapter) Probe(ctx context.Context) (backend.ProbeResult, error) {
	r := backend.ProbeResult{Kind: domain.BackendTmux, Instance: a.Socket}
	if _, err := exec.LookPath(a.Binary); err != nil {
		r.Availability, r.Reasons = domain.BackendUnavailable, []string{"BINARY_NOT_FOUND"}
		return r, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, a.Binary, "-V").Output()
	if err != nil {
		r.Availability, r.Reasons = domain.BackendUnavailable, []string{"VERSION_FAILED"}
		return r, nil
	}
	r.Version = strings.TrimSpace(string(out))
	if v := parseVersion(r.Version); v < 3.0 {
		r.Availability, r.Reasons = domain.BackendUnavailable, []string{"TMUX_TOO_OLD: need >= 3.0 for -e and argv commands"}
		return r, nil
	}
	r.Availability, r.Capabilities = domain.BackendAvailable, Capabilities()
	return r, nil
}

func parseVersion(s string) float64 {
	f := strings.Fields(s)
	if len(f) < 2 {
		return 0
	}
	v := strings.TrimRightFunc(f[1], func(r rune) bool { return (r < '0' || r > '9') && r != '.' })
	x, _ := strconv.ParseFloat(v, 64)
	return x
}

// paneFormat uses '|' because tmux escapes control characters (tabs) in
// format output; the command is last so SplitN keeps any '|' inside it.
const paneFormat = "#{session_id}|#{session_name}|#{window_id}|#{pane_id}|#{pane_pid}|#{pane_dead}|#{pane_current_command}"

func (a *Adapter) parsePane(line string) (*backend.Ref, map[string]string) {
	f := strings.SplitN(strings.TrimSpace(line), "|", 7)
	if len(f) < 7 {
		return nil, nil
	}
	pid, _ := strconv.Atoi(f[4])
	id, _ := procinfo.Of(pid)
	ref := &backend.Ref{Kind: domain.BackendTmux, Instance: a.Socket,
		Fields:     map[string]string{"session_id": f[0], "session_name": f[1], "window_id": f[2], "pane_id": f[3]},
		Generation: f[4] + "@" + id.StartTime}
	return ref, map[string]string{"pane_pid": f[4], "pane_dead": f[5], "command": f[6]}
}

// Launch creates a detached session running argv (no shell) in spec.Dir.
// The session name is the correlation label so Lookup can find it after a
// lost acknowledgement.
func (a *Adapter) Launch(ctx context.Context, spec backend.LaunchSpec) (backend.LaunchReceipt, error) {
	if len(spec.Argv) == 0 {
		return backend.LaunchReceipt{Outcome: backend.NotSent, Detail: "empty argv"}, nil
	}
	name := SessionPrefix + spec.Correlation
	if existing, _ := a.Lookup(ctx, spec.Correlation); existing != nil {
		return backend.LaunchReceipt{Outcome: backend.Delivered, Ref: existing, Detail: "adopted existing session for correlation"}, nil
	}
	args := []string{"new-session", "-d", "-P", "-F", paneFormat, "-s", name, "-c", spec.Dir}
	keys := make([]string, 0, len(spec.Env))
	for k := range spec.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		args = append(args, "-e", k+"="+spec.Env[k])
	}
	args = append(args, "--")
	args = append(args, spec.Argv...)
	out, stderr, err := a.run(ctx, nil, args...)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) || isTimeout(err) {
			return backend.LaunchReceipt{Outcome: backend.Ambiguous, Detail: "tmux timed out: " + stderr}, nil
		}
		if strings.Contains(stderr, "duplicate session") {
			ref, _ := a.Lookup(ctx, spec.Correlation)
			if ref != nil {
				return backend.LaunchReceipt{Outcome: backend.Delivered, Ref: ref, Detail: "adopted duplicate session"}, nil
			}
			return backend.LaunchReceipt{Outcome: backend.Ambiguous, Detail: stderr}, nil
		}
		return backend.LaunchReceipt{Outcome: backend.NotSent, Detail: strings.TrimSpace(stderr)}, nil
	}
	ref, _ := a.parsePane(out)
	if ref == nil {
		return backend.LaunchReceipt{Outcome: backend.Ambiguous, Detail: "unparseable tmux output"}, nil
	}
	return backend.LaunchReceipt{Outcome: backend.Delivered, Ref: ref}, nil
}

func isTimeout(err error) bool {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return false
	}
	return strings.Contains(err.Error(), "killed") || strings.Contains(err.Error(), "deadline")
}

// Lookup finds the owned session created for a correlation label.
func (a *Adapter) Lookup(ctx context.Context, correlation string) (*backend.Ref, error) {
	out, stderr, err := a.run(ctx, nil, "list-panes", "-t", "="+SessionPrefix+correlation, "-F", paneFormat)
	if err != nil {
		if noSession(stderr) {
			return nil, nil
		}
		return nil, fmt.Errorf("tmux lookup: %s", strings.TrimSpace(stderr))
	}
	ref, _ := a.parsePane(strings.SplitN(out, "\n", 2)[0])
	return ref, nil
}

func noSession(stderr string) bool {
	return strings.Contains(stderr, "can't find") || strings.Contains(stderr, "no server running") ||
		strings.Contains(stderr, "error connecting") || strings.Contains(stderr, "No such file")
}

// Observe reports pane and process liveness. A vanished pane whose process
// still runs is UNKNOWN, never EXITED.
func (a *Adapter) Observe(ctx context.Context, ref backend.Ref) (backend.Observation, error) {
	obs := backend.Observation{Liveness: domain.LivenessUnknown, Confidence: "LOW", Source: "tmux", ObservedAt: a.now()}
	pane := ref.Fields["pane_id"]
	out, stderr, err := a.run(ctx, nil, "display-message", "-p", "-t", pane, paneFormat)
	if err != nil {
		if !noSession(stderr) {
			return obs, fmt.Errorf("tmux observe: %s", strings.TrimSpace(stderr))
		}
		// Pane is gone. Only declare EXITED when the recorded process is gone.
		if pid, start, ok := splitGeneration(ref.Generation); ok {
			switch procinfo.Check(procinfo.Identity{PID: pid, StartTime: start}) {
			case procinfo.Gone:
				obs.Liveness, obs.Confidence, obs.RawState = domain.LivenessExited, "HIGH", "pane_gone"
			case procinfo.Running:
				obs.RawState = "pane_gone_process_alive"
			}
		}
		return obs, nil
	}
	cur, extra := a.parsePane(out)
	if cur == nil {
		return obs, nil
	}
	obs.Generation = cur.Generation
	obs.RawState = extra["command"]
	if cur.Generation != ref.Generation || cur.Fields["session_id"] != ref.Fields["session_id"] {
		// Same pane id, different occupant: not our worker.
		obs.Liveness, obs.Confidence, obs.RawState = domain.LivenessExited, "MEDIUM", "pane_reused"
		return obs, nil
	}
	if extra["pane_dead"] == "1" {
		obs.Liveness, obs.Confidence = domain.LivenessExited, "HIGH"
		return obs, nil
	}
	obs.Liveness, obs.Confidence = domain.LivenessAlive, "HIGH"
	pid, _ := strconv.Atoi(extra["pane_pid"])
	if id, ok := procinfo.Of(pid); ok {
		obs.ProcessIdentity = id.Map()
	}
	return obs, nil
}

func splitGeneration(g string) (int, string, bool) {
	i := strings.IndexByte(g, '@')
	if i < 0 {
		return 0, "", false
	}
	pid, err := strconv.Atoi(g[:i])
	return pid, g[i+1:], err == nil
}

// Submit pastes literal text through a named buffer and presses Enter. A
// successful paste only means tmux accepted the bytes; Claude's receipt is
// confirmed separately through the dispatch acknowledgement.
func (a *Adapter) Submit(ctx context.Context, req backend.PromptRequest) (backend.PromptReceipt, error) {
	if err := a.verifyOwned(ctx, req.Ref); err != nil {
		return backend.PromptReceipt{Outcome: backend.PolicySkipped, Detail: err.Error()}, nil
	}
	buf := "samsara-" + req.DispatchID
	if _, stderr, err := a.run(ctx, []byte(req.Text), "load-buffer", "-b", buf, "-"); err != nil {
		return backend.PromptReceipt{Outcome: backend.NotSent, Detail: stderr}, nil
	}
	if _, stderr, err := a.run(ctx, nil, "paste-buffer", "-d", "-p", "-b", buf, "-t", req.Ref.Fields["pane_id"]); err != nil {
		return backend.PromptReceipt{Outcome: backend.Ambiguous, Detail: stderr}, nil
	}
	if _, stderr, err := a.run(ctx, nil, "send-keys", "-t", req.Ref.Fields["pane_id"], "Enter"); err != nil {
		return backend.PromptReceipt{Outcome: backend.Ambiguous, Detail: stderr}, nil
	}
	return backend.PromptReceipt{Outcome: backend.Delivered}, nil
}

func (a *Adapter) verifyOwned(ctx context.Context, ref backend.Ref) error {
	obs, err := a.Observe(ctx, ref)
	if err != nil {
		return err
	}
	if obs.Liveness != domain.LivenessAlive {
		return fmt.Errorf("pane occupant not verified (%s)", obs.RawState)
	}
	if !strings.HasPrefix(ref.Fields["session_name"], SessionPrefix) {
		return fmt.Errorf("session is not owned by samsara")
	}
	return nil
}

// RequestInterrupt sends Escape (Claude Code's interrupt key) to an owned,
// verified pane. Delivered does not mean the tool stopped.
func (a *Adapter) RequestInterrupt(ctx context.Context, ref backend.Ref) (backend.ActionReceipt, error) {
	if err := a.verifyOwned(ctx, ref); err != nil {
		return backend.ActionReceipt{Outcome: backend.PolicySkipped, Detail: err.Error()}, nil
	}
	if _, stderr, err := a.run(ctx, nil, "send-keys", "-t", ref.Fields["pane_id"], "Escape"); err != nil {
		return backend.ActionReceipt{Outcome: backend.Ambiguous, Detail: stderr}, nil
	}
	return backend.ActionReceipt{Outcome: backend.Delivered}, nil
}

// CloseOwnedSession kills an owned session after verifying the occupant.
func (a *Adapter) CloseOwnedSession(ctx context.Context, ref backend.Ref) (backend.ActionReceipt, error) {
	if err := a.verifyOwned(ctx, ref); err != nil {
		return backend.ActionReceipt{Outcome: backend.PolicySkipped, Detail: err.Error()}, nil
	}
	if _, stderr, err := a.run(ctx, nil, "kill-session", "-t", ref.Fields["session_id"]); err != nil {
		return backend.ActionReceipt{Outcome: backend.Ambiguous, Detail: stderr}, nil
	}
	return backend.ActionReceipt{Outcome: backend.Delivered}, nil
}

// FocusInstruction returns the attach command (no key injection).
func (a *Adapter) FocusInstruction(ref backend.Ref) (string, error) {
	return fmt.Sprintf("tmux -L %s attach -t %s", a.Socket, ref.Fields["session_id"]), nil
}
