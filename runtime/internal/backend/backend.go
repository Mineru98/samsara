// Package backend defines the ports through which the runtime creates,
// observes and talks to worker sessions (01 §5–6). Adapters convert external
// responses into these records; domain code never sees herdr/tmux JSON.
package backend

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"
)

// SupportLevel distinguishes partial from safe support.
type SupportLevel string

const (
	Supported   SupportLevel = "SUPPORTED"
	Conditional SupportLevel = "CONDITIONAL"
	Unsupported SupportLevel = "UNSUPPORTED"
)

// Capability is the support level of one feature with its reason.
type Capability struct {
	Level       SupportLevel      `json:"level"`
	ReasonCode  string            `json:"reason_code,omitempty"`
	Constraints map[string]string `json:"constraints,omitempty"`
}

// Usable reports whether the feature may be attempted.
func (c Capability) Usable() bool { return c.Level == Supported || c.Level == Conditional }

// Capabilities is the per-backend feature table.
type Capabilities struct {
	CreateWorker        Capability `json:"create_worker"`
	AttachSession       Capability `json:"attach_session"`
	SubmitPrompt        Capability `json:"submit_prompt"`
	ObserveProcess      Capability `json:"observe_process"`
	ObserveAgent        Capability `json:"observe_agent"`
	ReadOutput          Capability `json:"read_output"`
	RequestInterrupt    Capability `json:"request_interrupt"`
	RestoreConversation Capability `json:"restore_conversation"`
}

// ByName returns the capability with a plan capability name.
func (c Capabilities) ByName(name string) (Capability, bool) {
	switch name {
	case "create_worker":
		return c.CreateWorker, true
	case "attach_session":
		return c.AttachSession, true
	case "submit_prompt":
		return c.SubmitPrompt, true
	case "observe_process":
		return c.ObserveProcess, true
	case "observe_agent":
		return c.ObserveAgent, true
	case "read_output":
		return c.ReadOutput, true
	case "request_interrupt":
		return c.RequestInterrupt, true
	case "restore_conversation":
		return c.RestoreConversation, true
	}
	return Capability{}, false
}

// ProbeResult is a read-only probe outcome. Probing never starts servers or
// creates workspaces (01 §5.2).
type ProbeResult struct {
	Kind         string       `json:"kind"`
	Availability string       `json:"availability"`
	Version      string       `json:"version,omitempty"`
	Instance     string       `json:"instance,omitempty"`
	Capabilities Capabilities `json:"capabilities"`
	Reasons      []string     `json:"reasons,omitempty"`
	SchemaSHA256 string       `json:"schema_sha256,omitempty"`
	ProbedAt     time.Time    `json:"probed_at"`
}

// Ref locates a session inside a backend. Ref fields are adapter-specific
// opaque values; Generation distinguishes incarnations of the same location.
type Ref struct {
	Kind       string            `json:"kind"`
	Instance   string            `json:"instance,omitempty"`
	Fields     map[string]string `json:"fields"`
	Generation string            `json:"generation"`
}

// Key is a canonical string of the location (without generation).
func (r Ref) Key() string {
	ks := make([]string, 0, len(r.Fields))
	for k := range r.Fields {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	var b strings.Builder
	for _, k := range ks {
		b.WriteString(k + "=" + r.Fields[k] + ";")
	}
	return b.String()
}

// JSON renders the opaque fields.
func (r Ref) JSON() string {
	b, _ := json.Marshal(r.Fields)
	return string(b)
}

// Observation is a time-stamped, sourced view of a session.
type Observation struct {
	Liveness        string            `json:"liveness"`
	InputState      string            `json:"input_state,omitempty"`
	RawState        string            `json:"raw_state,omitempty"`
	Generation      string            `json:"generation,omitempty"`
	ProcessIdentity map[string]string `json:"process_identity,omitempty"`
	Confidence      string            `json:"confidence"`
	Source          string            `json:"source"`
	ObservedAt      time.Time         `json:"observed_at"`
}

// Outcome classifies an external action result (01 §6).
type Outcome string

const (
	NotSent       Outcome = "NOT_SENT"       // definitely not delivered
	Delivered     Outcome = "DELIVERED"      // delivered and backend responded
	Ambiguous     Outcome = "UNKNOWN"        // delivery or execution unknown
	PolicySkipped Outcome = "POLICY_SKIPPED" // intentionally not executed
	NotSupported  Outcome = "UNSUPPORTED"    // capability missing
)

// LaunchSpec describes a worker to create.
type LaunchSpec struct {
	OperationID string
	AttemptID   string
	ProjectID   string
	Dir         string
	Argv        []string
	Env         map[string]string
	Correlation string // unique, stable across retries of the same intent
}

// LaunchReceipt is the result of Launch.
type LaunchReceipt struct {
	Outcome Outcome
	Ref     *Ref
	Detail  string
}

// PromptRequest is an automated input.
type PromptRequest struct {
	OperationID string
	DispatchID  string
	AttemptID   string
	Ref         Ref
	Text        string
}

// PromptReceipt is the result of Submit.
type PromptReceipt struct {
	Outcome Outcome
	Detail  string
}

// ActionReceipt is the result of interrupt/close requests. Delivered means
// the request was accepted, never that the process has stopped.
type ActionReceipt struct {
	Outcome Outcome
	Detail  string
}

// Backend is the minimal port every adapter implements.
type Backend interface {
	Name() string
	Probe(ctx context.Context) (ProbeResult, error)
	Observe(ctx context.Context, ref Ref) (Observation, error)
}

// WorkerLauncher creates workers. Lookup finds a worker created for a
// correlation label so that a lost launch ACK never causes a duplicate
// worker (BE-10).
type WorkerLauncher interface {
	Launch(ctx context.Context, spec LaunchSpec) (LaunchReceipt, error)
	Lookup(ctx context.Context, correlation string) (*Ref, error)
}

// PromptSubmitter sends automated input.
type PromptSubmitter interface {
	Submit(ctx context.Context, req PromptRequest) (PromptReceipt, error)
}

// SessionController interrupts or closes owned sessions.
type SessionController interface {
	RequestInterrupt(ctx context.Context, ref Ref) (ActionReceipt, error)
	CloseOwnedSession(ctx context.Context, ref Ref) (ActionReceipt, error)
}

// Focuser returns a user-facing instruction to open the session location.
type Focuser interface {
	FocusInstruction(ref Ref) (string, error)
}
