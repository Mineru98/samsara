// Package herdr adapts herdr. The installed protocol must be pinned in the
// compatibility manifest before managed execution is enabled (01 §7, M0):
// until the live schema matches a pinned fixture that the manifest records as
// verified, every capability reports UNSUPPORTED (HERDR_COMPAT_UNVERIFIED or
// HERDR_SCHEMA_MISMATCH) and the resolver falls back to the next backend.
// Probing is read-only and never starts a herdr server.
package herdr

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Mineru98/samsara/runtime/internal/backend"
	"github.com/Mineru98/samsara/runtime/internal/domain"
	"github.com/Mineru98/samsara/runtime/internal/procinfo"
)

// WorkspacePrefix marks workspaces owned by Samsara; the rest of the label is
// the launch correlation.
const WorkspacePrefix = "samsara-"

// PinnedSchemas are the `herdr api schema --json` digests whose protocol this
// adapter implements. Each has a fixture under testdata/ (see
// TestPinnedSchemaFixtures). A new herdr release needs a new fixture and a
// contract suite run before it is added here.
var PinnedSchemas = map[string]string{
	"sha256:9e2af207e9aa8183d4aeca5fde9cc48e7909bb40cdbd7cf21608a6d3ea78075b": "testdata/schema-0.9.3-protocol22.json",
}

// Adapter implements Backend, WorkerLauncher, PromptSubmitter,
// SessionController and Focuser.
type Adapter struct {
	Binary string
	Client *Client
	// VerifiedSchemaSHA256 and VerifiedVersion come from the compatibility
	// manifest. Empty schema means no verified protocol.
	VerifiedSchemaSHA256 string
	VerifiedVersion      string
	Clock                domain.Clock
	// StartTimeout bounds the wait for the typed command to run (default 5s).
	StartTimeout time.Duration
	// ReadyTimeout bounds the wait for herdr to detect the agent as ready
	// before a prompt (default 20s).
	ReadyTimeout time.Duration
	// LiveSchema overrides `herdr api schema --json` (tests).
	LiveSchema func(ctx context.Context) ([]byte, error)

	mu          sync.Mutex
	stale       bool
	staleReason string
}

// New returns the adapter for the current user's herdr server.
func New(clock domain.Clock, verifiedSchema, verifiedVersion string) *Adapter {
	return &Adapter{Binary: "herdr", Client: &Client{Socket: DefaultSocketPath()}, VerifiedSchemaSHA256: verifiedSchema,
		VerifiedVersion: verifiedVersion, Clock: clock}
}

// Name implements backend.Backend.
func (a *Adapter) Name() string { return domain.BackendHerdr }

// DefersInitialPrompt implements backend.DefersInitialPrompt: the worker
// command is typed into a shell, so issue text never goes into it.
func (a *Adapter) DefersInitialPrompt() bool { return true }

func (a *Adapter) now() time.Time {
	if a.Clock != nil {
		return a.Clock.Now()
	}
	return time.Now().UTC()
}

func unsupported(code string) backend.Capability {
	return backend.Capability{Level: backend.Unsupported, ReasonCode: code}
}

// Capabilities is the table of a verified herdr protocol.
func Capabilities() backend.Capabilities {
	return backend.Capabilities{
		CreateWorker:        backend.Capability{Level: backend.Supported},
		AttachSession:       unsupported("HERDR_ATTACH_NOT_IMPLEMENTED"),
		SubmitPrompt:        backend.Capability{Level: backend.Conditional, ReasonCode: "AGENT_READY_AND_INPUT_OWNERSHIP"},
		ObserveProcess:      backend.Capability{Level: backend.Supported},
		ObserveAgent:        backend.Capability{Level: backend.Conditional, ReasonCode: "NATIVE_DETECTION_AND_HOOK_EVENTS"},
		ReadOutput:          unsupported("HERDR_READ_OUTPUT_NOT_IMPLEMENTED"),
		RequestInterrupt:    backend.Capability{Level: backend.Supported},
		RestoreConversation: unsupported("HERDR_RESTORE_OWNERSHIP_UNDEFINED"),
	}
}

func (a *Adapter) liveSchema(ctx context.Context) ([]byte, error) {
	if a.LiveSchema != nil {
		return a.LiveSchema(ctx)
	}
	var schema bytes.Buffer
	cmd := exec.CommandContext(ctx, a.Binary, "api", "schema", "--json")
	cmd.Stdout = &schema
	err := cmd.Run()
	return schema.Bytes(), err
}

// Probe records the binary version and protocol schema hash and pings the
// server. Capabilities are granted only for a verified, pinned protocol.
func (a *Adapter) Probe(ctx context.Context) (backend.ProbeResult, error) {
	r := backend.ProbeResult{Kind: domain.BackendHerdr, Instance: a.Client.Socket}
	if a.LiveSchema == nil {
		if _, err := exec.LookPath(a.Binary); err != nil {
			r.Availability, r.Reasons = domain.BackendUnavailable, []string{"BINARY_NOT_FOUND"}
			return r, nil
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if a.LiveSchema == nil {
		if out, err := exec.CommandContext(ctx, a.Binary, "--version").Output(); err == nil {
			r.Version = strings.TrimSpace(string(out))
		}
	}
	schema, err := a.liveSchema(ctx)
	if err != nil {
		r.Availability, r.Reasons = domain.BackendUnavailable, []string{"SCHEMA_UNAVAILABLE"}
		return r, nil
	}
	r.SchemaSHA256 = domain.DigestBytes(schema)
	reason := ""
	switch {
	case a.VerifiedSchemaSHA256 == "":
		reason = "HERDR_COMPAT_UNVERIFIED"
	case a.VerifiedSchemaSHA256 != r.SchemaSHA256 || PinnedSchemas[r.SchemaSHA256] == "":
		reason = "HERDR_SCHEMA_MISMATCH"
	case a.VerifiedVersion != "" && r.Version != "" && a.VerifiedVersion != r.Version:
		reason = "HERDR_VERSION_MISMATCH"
	}
	if reason != "" {
		un := unsupported(reason)
		r.Capabilities = backend.Capabilities{CreateWorker: un, AttachSession: un, SubmitPrompt: un, ObserveProcess: un,
			ObserveAgent: un, ReadOutput: un, RequestInterrupt: un, RestoreConversation: un}
		r.Availability, r.Reasons = domain.BackendDegraded, []string{reason}
		return r, nil
	}
	var pong struct {
		Version  string `json:"version"`
		Protocol int    `json:"protocol"`
	}
	if err := a.Client.Call(ctx, "ping", nil, &pong); err != nil {
		// Installed and verified, but no reachable server (BE-02). Probing
		// never starts one.
		r.Availability, r.Reasons = domain.BackendUnavailable, []string{"HERDR_SERVER_UNREACHABLE"}
		return r, nil
	}
	if r.Version != "" && !strings.HasSuffix(r.Version, " "+pong.Version) {
		un := unsupported("HERDR_SERVER_VERSION_MISMATCH")
		r.Capabilities = backend.Capabilities{CreateWorker: un, AttachSession: un, SubmitPrompt: un, ObserveProcess: un,
			ObserveAgent: un, ReadOutput: un, RequestInterrupt: un, RestoreConversation: un}
		r.Availability, r.Reasons = domain.BackendDegraded, []string{"HERDR_SERVER_VERSION_MISMATCH: server " + pong.Version}
		return r, nil
	}
	r.Availability, r.Capabilities = domain.BackendAvailable, Capabilities()
	if stale, why := a.Stale(); stale {
		r.Reasons = []string{"EVENTS_STALE: " + why}
	}
	return r, nil
}

// --- wire records ------------------------------------------------------------

type paneInfo struct {
	PaneID      string `json:"pane_id"`
	TabID       string `json:"tab_id"`
	WorkspaceID string `json:"workspace_id"`
	TerminalID  string `json:"terminal_id"`
	Agent       string `json:"agent"`
	AgentStatus string `json:"agent_status"`
}

type workspaceInfo struct {
	WorkspaceID string `json:"workspace_id"`
	Label       string `json:"label"`
}

type processInfo struct {
	ShellPID            int `json:"shell_pid"`
	ForegroundProcesses []struct {
		PID  int    `json:"pid"`
		Name string `json:"name"`
	} `json:"foreground_processes"`
}

type agentInfo struct {
	AgentStatus      string `json:"agent_status"`
	LaunchPending    bool   `json:"launch_pending"`
	InteractiveReady bool   `json:"interactive_ready"`
}

func (a *Adapter) processInfo(ctx context.Context, paneID string) (processInfo, error) {
	var res struct {
		ProcessInfo processInfo `json:"process_info"`
	}
	err := a.Client.Call(ctx, "pane.process_info", map[string]string{"pane_id": paneID}, &res)
	return res.ProcessInfo, err
}

// ref builds the location of a pane. The generation is the herdr terminal id
// plus the shell process identity: a restored or re-created pane gets a new
// terminal even when its pane id repeats (BE-07, 01 §7.5).
func (a *Adapter) ref(ctx context.Context, label string, p paneInfo) (*backend.Ref, processInfo, error) {
	pi, err := a.processInfo(ctx, p.PaneID)
	if err != nil {
		return nil, pi, err
	}
	gen := p.TerminalID
	if id, ok := procinfo.Of(pi.ShellPID); ok {
		gen += "/" + strconv.Itoa(id.PID) + "@" + id.StartTime
	}
	return &backend.Ref{Kind: domain.BackendHerdr, Instance: a.Client.Socket,
		Fields:     map[string]string{"workspace_id": p.WorkspaceID, "workspace_label": label, "tab_id": p.TabID, "pane_id": p.PaneID},
		Generation: gen}, pi, nil
}

// sameGeneration compares terminal ids, and process identities when both
// sides could read one (a failed /proc read must not look like a new process).
func sameGeneration(recorded, current string) bool {
	rt, rp, rs, rok := splitGeneration(recorded)
	ct, cp, cs, cok := splitGeneration(current)
	if rt != ct {
		return false
	}
	return !rok || !cok || (rp == cp && rs == cs)
}

func splitGeneration(g string) (terminal string, pid int, start string, ok bool) {
	terminal, rest, found := strings.Cut(g, "/")
	if !found {
		return terminal, 0, "", false
	}
	p, s, found := strings.Cut(rest, "@")
	n, err := strconv.Atoi(p)
	return terminal, n, s, found && err == nil
}

// --- launch ------------------------------------------------------------------

// Launch creates a worker in two checkpointed steps (01 §7.2): an owned
// workspace whose root shell pane runs in spec.Dir with spec.Env, then the
// launcher command typed into that shell. The command is built only from
// spec.Argv (runtime configuration), each word single-quoted; the initial
// prompt is never part of it. A retry resumes from the recorded checkpoint
// and never creates a second workspace for the same correlation (BE-10).
func (a *Adapter) Launch(ctx context.Context, spec backend.LaunchSpec) (backend.LaunchReceipt, error) {
	if len(spec.Argv) == 0 {
		return backend.LaunchReceipt{Outcome: backend.NotSent, Detail: "empty argv"}, nil
	}
	line, err := commandLine(spec.Argv)
	if err != nil {
		return backend.LaunchReceipt{Outcome: backend.NotSent, Detail: err.Error()}, nil
	}
	checkpoint := func(k, v string) {
		if spec.Checkpoint != nil {
			_ = spec.Checkpoint(k, v)
		}
	}
	label := WorkspacePrefix + spec.Correlation
	ref, err := a.Lookup(ctx, spec.Correlation)
	if err != nil {
		return backend.LaunchReceipt{Outcome: backend.Ambiguous, Detail: "lookup before create: " + err.Error()}, nil
	}
	var pi processInfo
	retype := ref != nil
	if ref == nil {
		// Step 1: shell pane in an owned workspace.
		var res struct {
			RootPane paneInfo `json:"root_pane"`
		}
		env := spec.Env
		if env == nil {
			env = map[string]string{} // herdr rejects a null env
		}
		err := a.Client.Call(ctx, "workspace.create", map[string]any{"cwd": spec.Dir, "env": env, "label": label, "focus": false}, &res)
		if err != nil {
			if wasSent(err) {
				return backend.LaunchReceipt{Outcome: backend.Ambiguous, Detail: err.Error()}, nil
			}
			return backend.LaunchReceipt{Outcome: backend.NotSent, Detail: err.Error()}, nil
		}
		if ref, pi, err = a.ref(ctx, label, res.RootPane); err != nil {
			return backend.LaunchReceipt{Outcome: backend.Ambiguous, Detail: "workspace created, process info lost: " + err.Error()}, nil
		}
		checkpoint("workspace_id", res.RootPane.WorkspaceID)
		checkpoint("pane_id", res.RootPane.PaneID)
		checkpoint("generation", ref.Generation)
	} else {
		if spec.Checkpoints["command_sent"] == "1" {
			return backend.LaunchReceipt{Outcome: backend.Delivered, Ref: ref, Detail: "adopted launched workspace"}, nil
		}
		if pi, err = a.processInfo(ctx, ref.Fields["pane_id"]); err != nil {
			return backend.LaunchReceipt{Outcome: backend.Ambiguous, Detail: err.Error()}, nil
		}
		if busy(pi) {
			// Something already runs in the owned shell: the command was
			// typed before the checkpoint was lost. Never type it twice.
			checkpoint("command_sent", "1")
			return backend.LaunchReceipt{Outcome: backend.Delivered, Ref: ref, Detail: "adopted workspace with running command"}, nil
		}
	}
	// Step 2: start the launcher in the shell.
	pane := ref.Fields["pane_id"]
	if retype {
		// An earlier attempt may have left a partial line: clear it so the
		// command is never typed twice into one line.
		if err := a.Client.Call(ctx, "pane.send_keys", map[string]any{"pane_id": pane, "keys": []string{"C-c"}}, nil); err != nil {
			return backend.LaunchReceipt{Outcome: actionOutcome(err), Ref: ref, Detail: "clear input line: " + err.Error()}, nil
		}
	}
	if err := a.Client.Call(ctx, "pane.send_text", map[string]string{"pane_id": pane, "text": line}, nil); err != nil {
		if wasSent(err) {
			return backend.LaunchReceipt{Outcome: backend.Ambiguous, Ref: ref, Detail: err.Error()}, nil
		}
		return backend.LaunchReceipt{Outcome: backend.NotSent, Detail: "workspace ready, command not sent: " + err.Error()}, nil
	}
	if err := a.Client.Call(ctx, "pane.send_keys", map[string]any{"pane_id": pane, "keys": []string{"Enter"}}, nil); err != nil {
		return backend.LaunchReceipt{Outcome: backend.Ambiguous, Ref: ref, Detail: "command typed, Enter not confirmed: " + err.Error()}, nil
	}
	// The shell may not have been ready (an rc prompt can swallow the line):
	// report success only once the command is seen running.
	wait := a.StartTimeout
	if wait == 0 {
		wait = 5 * time.Second
	}
	for deadline := time.Now().Add(wait); ; {
		pi, err := a.processInfo(ctx, pane)
		if err == nil && busy(pi) {
			break
		}
		if time.Now().After(deadline) {
			return backend.LaunchReceipt{Outcome: backend.Ambiguous, Ref: ref, Detail: "command typed but not observed running"}, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	checkpoint("command_sent", "1")
	return backend.LaunchReceipt{Outcome: backend.Delivered, Ref: ref}, nil
}

// busy reports whether the pane runs something other than its idle shell.
// The launcher line uses exec, so a launched pane keeps the shell pid but no
// longer runs a shell.
func busy(pi processInfo) bool {
	for _, p := range pi.ForegroundProcesses {
		if p.PID != pi.ShellPID || !shells[p.Name] {
			return true
		}
	}
	return false
}

var shells = map[string]bool{"sh": true, "bash": true, "zsh": true, "fish": true, "dash": true, "ksh": true, "mksh": true, "tcsh": true, "csh": true, "nu": true}

// commandLine renders `exec 'argv0' 'argv1' ...` for POSIX shells, zsh and
// fish alike. Control characters are refused rather than escaped.
func commandLine(argv []string) (string, error) {
	var b strings.Builder
	b.WriteString("exec")
	for _, w := range argv {
		for _, r := range w {
			if r < 0x20 || r == 0x7f {
				return "", fmt.Errorf("argv word %q contains a control character", w)
			}
		}
		b.WriteString(" '" + strings.ReplaceAll(w, "'", `'\''`) + "'")
	}
	return b.String(), nil
}

// Lookup finds the owned workspace created for a correlation label.
func (a *Adapter) Lookup(ctx context.Context, correlation string) (*backend.Ref, error) {
	label := WorkspacePrefix + correlation
	var ws struct {
		Workspaces []workspaceInfo `json:"workspaces"`
	}
	if err := a.Client.Call(ctx, "workspace.list", nil, &ws); err != nil {
		return nil, err
	}
	for _, w := range ws.Workspaces {
		if w.Label != label {
			continue
		}
		var panes struct {
			Panes []paneInfo `json:"panes"`
		}
		if err := a.Client.Call(ctx, "pane.list", map[string]string{"workspace_id": w.WorkspaceID}, &panes); err != nil {
			return nil, err
		}
		if len(panes.Panes) == 0 {
			// Owned but empty: never create a second workspace beside it.
			return nil, fmt.Errorf("owned workspace %s has no pane", w.WorkspaceID)
		}
		ref, _, err := a.ref(ctx, label, panes.Panes[0])
		return ref, err
	}
	return nil, nil
}

// --- observation -------------------------------------------------------------

// Observe reads the pane authoritatively. A vanished pane whose shell still
// runs is UNKNOWN, never EXITED; an unreachable server is an error so the
// session stays UNKNOWN and keeps its binding (BE-06).
func (a *Adapter) Observe(ctx context.Context, ref backend.Ref) (backend.Observation, error) {
	src := "herdr"
	if stale, _ := a.Stale(); stale {
		src = "herdr.reread"
	}
	obs := backend.Observation{Liveness: domain.LivenessUnknown, Confidence: "LOW", Source: src, ObservedAt: a.now()}
	var res struct {
		Pane paneInfo `json:"pane"`
	}
	err := a.Client.Call(ctx, "pane.get", map[string]string{"pane_id": ref.Fields["pane_id"]}, &res)
	if IsAPIError(err, "pane_not_found") {
		if _, pid, start, ok := splitGeneration(ref.Generation); ok {
			switch procinfo.Check(procinfo.Identity{PID: pid, StartTime: start}) {
			case procinfo.Gone:
				obs.Liveness, obs.Confidence, obs.RawState = domain.LivenessExited, "HIGH", "pane_gone"
			case procinfo.Running:
				obs.RawState = "pane_gone_process_alive"
			}
		}
		return obs, nil
	}
	if err != nil {
		return obs, fmt.Errorf("herdr observe: %w", err)
	}
	cur, pi, err := a.ref(ctx, ref.Fields["workspace_label"], res.Pane)
	if IsAPIError(err, "pane_not_found") {
		return obs, nil
	}
	if err != nil {
		return obs, fmt.Errorf("herdr observe: %w", err)
	}
	obs.Generation = cur.Generation
	obs.RawState = res.Pane.AgentStatus
	if !sameGeneration(ref.Generation, cur.Generation) || cur.Fields["workspace_id"] != ref.Fields["workspace_id"] {
		// Same pane id, different terminal: restored or reused, not our
		// worker incarnation (BE-07, 01 §7.5).
		obs.Liveness, obs.Confidence, obs.RawState = domain.LivenessExited, "MEDIUM", "pane_reused"
		return obs, nil
	}
	obs.Liveness, obs.Confidence = domain.LivenessAlive, "HIGH"
	switch res.Pane.AgentStatus {
	case "working":
		obs.InputState = domain.InputBusy
	case "blocked":
		obs.InputState = domain.InputNativePermission
	}
	if id, ok := procinfo.Of(pi.ShellPID); ok {
		obs.ProcessIdentity = id.Map()
	}
	return obs, nil
}

func (a *Adapter) verifyOwned(ctx context.Context, ref backend.Ref) error {
	if !strings.HasPrefix(ref.Fields["workspace_label"], WorkspacePrefix) {
		return fmt.Errorf("workspace is not owned by samsara")
	}
	obs, err := a.Observe(ctx, ref)
	if err != nil {
		return err
	}
	if obs.Liveness != domain.LivenessAlive {
		return fmt.Errorf("pane occupant not verified (%s)", obs.RawState)
	}
	return nil
}

// --- input -------------------------------------------------------------------

// Submit sends automated input through agent.prompt after herdr reports the
// agent ready. A prompt response means herdr accepted the text, never that
// Claude received or completed it (01 §7.3); a blocked agent (native
// permission or question screen) is never answered.
func (a *Adapter) Submit(ctx context.Context, req backend.PromptRequest) (backend.PromptReceipt, error) {
	if err := a.verifyOwned(ctx, req.Ref); err != nil {
		return backend.PromptReceipt{Outcome: backend.PolicySkipped, Detail: err.Error()}, nil
	}
	pane := req.Ref.Fields["pane_id"]
	wait := a.ReadyTimeout
	if wait == 0 {
		wait = 20 * time.Second
	}
	deadline := time.Now().Add(wait)
	for {
		var res struct {
			Agent agentInfo `json:"agent"`
		}
		err := a.Client.Call(ctx, "agent.get", map[string]string{"target": pane}, &res)
		switch {
		case err == nil && res.Agent.AgentStatus == "blocked":
			return backend.PromptReceipt{Outcome: backend.PolicySkipped, Detail: "agent is blocked on a native prompt"}, nil
		case err == nil && res.Agent.AgentStatus == "working":
			return backend.PromptReceipt{Outcome: backend.PolicySkipped, Detail: "agent is working: input not owned by automation"}, nil
		case err == nil && !res.Agent.LaunchPending && (res.Agent.AgentStatus == "idle" || res.Agent.AgentStatus == "done"):
		case err != nil && !IsAPIError(err, "agent_not_found"):
			return backend.PromptReceipt{Outcome: backend.NotSent, Detail: err.Error()}, nil
		default:
			if time.Now().After(deadline) {
				return backend.PromptReceipt{Outcome: backend.NotSent, Detail: "agent not ready"}, nil
			}
			select {
			case <-ctx.Done():
				return backend.PromptReceipt{Outcome: backend.NotSent, Detail: ctx.Err().Error()}, nil
			case <-time.After(300 * time.Millisecond):
			}
			continue
		}
		break
	}
	err := a.Client.Call(ctx, "agent.prompt", map[string]string{"target": pane, "text": req.Text}, nil)
	switch {
	case err == nil:
		return backend.PromptReceipt{Outcome: backend.Delivered}, nil
	case wasSent(err):
		// Sent, acknowledgement lost: never resend blindly (BE-09).
		return backend.PromptReceipt{Outcome: backend.Ambiguous, Detail: err.Error()}, nil
	default:
		return backend.PromptReceipt{Outcome: backend.NotSent, Detail: err.Error()}, nil
	}
}

// RequestInterrupt sends Escape (Claude Code's interrupt key) to an owned,
// verified pane. Delivered does not mean the tool stopped.
func (a *Adapter) RequestInterrupt(ctx context.Context, ref backend.Ref) (backend.ActionReceipt, error) {
	if err := a.verifyOwned(ctx, ref); err != nil {
		return backend.ActionReceipt{Outcome: backend.PolicySkipped, Detail: err.Error()}, nil
	}
	if err := a.Client.Call(ctx, "pane.send_keys", map[string]any{"pane_id": ref.Fields["pane_id"], "keys": []string{"Escape"}}, nil); err != nil {
		return backend.ActionReceipt{Outcome: actionOutcome(err), Detail: err.Error()}, nil
	}
	return backend.ActionReceipt{Outcome: backend.Delivered}, nil
}

// CloseOwnedSession closes the owned workspace after verifying the occupant.
func (a *Adapter) CloseOwnedSession(ctx context.Context, ref backend.Ref) (backend.ActionReceipt, error) {
	if err := a.verifyOwned(ctx, ref); err != nil {
		return backend.ActionReceipt{Outcome: backend.PolicySkipped, Detail: err.Error()}, nil
	}
	if err := a.Client.Call(ctx, "workspace.close", map[string]string{"workspace_id": ref.Fields["workspace_id"]}, nil); err != nil {
		return backend.ActionReceipt{Outcome: actionOutcome(err), Detail: err.Error()}, nil
	}
	return backend.ActionReceipt{Outcome: backend.Delivered}, nil
}

func actionOutcome(err error) backend.Outcome {
	if wasSent(err) {
		return backend.Ambiguous
	}
	return backend.NotSent
}

// FocusInstruction returns the command that opens the workspace (no key
// injection).
func (a *Adapter) FocusInstruction(ref backend.Ref) (string, error) {
	return "herdr workspace focus " + ref.Fields["workspace_id"], nil
}

// --- subscription --------------------------------------------------------------

// Stale reports whether the event stream lost continuity since the last
// successful subscription; observations are then authoritative rereads.
func (a *Adapter) Stale() (bool, string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.stale, a.staleReason
}

func (a *Adapter) setStale(stale bool, reason string) {
	a.mu.Lock()
	a.stale, a.staleReason = stale, reason
	a.mu.Unlock()
}

// errEventsLost ends a subscription so it is reopened from scratch.
var errEventsLost = errors.New("events_lost")

// Watch keeps a subscription open until ctx ends (01 §7.4). Events are only
// invalidation signals: onChange asks the runtime to reconcile, which reads
// authoritatively. events_lost, a closed connection or an undecodable event
// mark the cache stale; after resubscribing, onChange triggers the reread.
// herdr history is never treated as Samsara's durable event log.
func (a *Adapter) Watch(ctx context.Context, onChange func()) {
	subs := []map[string]string{{"type": "pane.exited"}, {"type": "pane.closed"}, {"type": "pane.agent_detected"}, {"type": "workspace.closed"}}
	backoff := time.Second
	for ctx.Err() == nil {
		err := a.Client.Subscribe(ctx, subs, func(ev Event) error {
			switch ev.Event {
			case "subscription_started":
				backoff = time.Second
				a.setStale(false, "")
			case "events_lost":
				return errEventsLost
			}
			onChange()
			return nil
		})
		if ctx.Err() != nil {
			return
		}
		a.setStale(true, err.Error())
		onChange()
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}
