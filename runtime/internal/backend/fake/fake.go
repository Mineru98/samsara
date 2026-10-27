// Package fake is a deterministic backend for invariant tests (05 §6.3). It
// records every call and can inject failures: errors before send, lost
// acknowledgements, unknown liveness and pane reuse. It never succeeds for a
// capability it does not advertise.
package fake

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Mineru98/samsara/runtime/internal/backend"
	"github.com/Mineru98/samsara/runtime/internal/domain"
)

// Call is one recorded adapter invocation.
type Call struct {
	Op          string
	OperationID string
	Correlation string
	DispatchID  string
	At          time.Time
	Outcome     backend.Outcome
}

// Session is a fake worker.
type Session struct {
	Ref      backend.Ref
	Spec     backend.LaunchSpec
	Liveness string
	Input    string
	Prompts  []string
}

// Backend is the fake adapter.
type Backend struct {
	mu    sync.Mutex
	kind  string
	Clock domain.Clock

	Caps         backend.Capabilities
	Availability string

	// Failure injection (one-shot unless noted).
	FailLaunchBeforeSend bool
	LoseLaunchAck        bool // performs the launch but reports UNKNOWN
	LosePromptAck        bool
	ObserveError         bool // persistent: observation channel broken

	Sessions map[string]*Session // by correlation
	Calls    []Call
	nextPane int
}

// New creates a fake with full capabilities under the given kind name.
func New(kind string, clock domain.Clock) *Backend {
	s := backend.Capability{Level: backend.Supported}
	return &Backend{kind: kind, Clock: clock, Availability: domain.BackendAvailable, Sessions: map[string]*Session{},
		Caps: backend.Capabilities{CreateWorker: s, AttachSession: s, SubmitPrompt: s, ObserveProcess: s, ObserveAgent: s,
			ReadOutput: backend.Capability{Level: backend.Unsupported, ReasonCode: "FAKE"}, RequestInterrupt: s,
			RestoreConversation: backend.Capability{Level: backend.Unsupported, ReasonCode: "FAKE"}}}
}

// Name implements backend.Backend.
func (b *Backend) Name() string { return b.kind }

func (b *Backend) record(c Call) {
	c.At = b.Clock.Now()
	b.Calls = append(b.Calls, c)
}

// CallsOf returns recorded calls of an op.
func (b *Backend) CallsOf(op string) []Call {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []Call
	for _, c := range b.Calls {
		if c.Op == op {
			out = append(out, c)
		}
	}
	return out
}

// Probe implements backend.Backend.
func (b *Backend) Probe(ctx context.Context) (backend.ProbeResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.record(Call{Op: "probe"})
	return backend.ProbeResult{Kind: b.kind, Availability: b.Availability, Version: "fake-1", Capabilities: b.Caps}, nil
}

// Launch implements backend.WorkerLauncher.
func (b *Backend) Launch(ctx context.Context, spec backend.LaunchSpec) (backend.LaunchReceipt, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.Caps.CreateWorker.Usable() {
		b.record(Call{Op: "launch", OperationID: spec.OperationID, Correlation: spec.Correlation, Outcome: backend.NotSupported})
		return backend.LaunchReceipt{Outcome: backend.NotSupported}, nil
	}
	if b.FailLaunchBeforeSend {
		b.FailLaunchBeforeSend = false
		b.record(Call{Op: "launch", OperationID: spec.OperationID, Correlation: spec.Correlation, Outcome: backend.NotSent})
		return backend.LaunchReceipt{Outcome: backend.NotSent, Detail: "injected failure before send"}, nil
	}
	if s, ok := b.Sessions[spec.Correlation]; ok {
		b.record(Call{Op: "launch", OperationID: spec.OperationID, Correlation: spec.Correlation, Outcome: backend.Delivered})
		ref := s.Ref
		return backend.LaunchReceipt{Outcome: backend.Delivered, Ref: &ref, Detail: "adopted"}, nil
	}
	b.nextPane++
	ref := backend.Ref{Kind: b.kind, Instance: "fake", Fields: map[string]string{"pane_id": fmt.Sprintf("%%%d", b.nextPane)}, Generation: fmt.Sprintf("g%d", b.nextPane)}
	b.Sessions[spec.Correlation] = &Session{Ref: ref, Spec: spec, Liveness: domain.LivenessAlive, Input: domain.InputReady}
	if b.LoseLaunchAck {
		b.LoseLaunchAck = false
		b.record(Call{Op: "launch", OperationID: spec.OperationID, Correlation: spec.Correlation, Outcome: backend.Ambiguous})
		return backend.LaunchReceipt{Outcome: backend.Ambiguous, Detail: "injected ack loss"}, nil
	}
	b.record(Call{Op: "launch", OperationID: spec.OperationID, Correlation: spec.Correlation, Outcome: backend.Delivered})
	return backend.LaunchReceipt{Outcome: backend.Delivered, Ref: &ref}, nil
}

// Lookup implements backend.WorkerLauncher.
func (b *Backend) Lookup(ctx context.Context, correlation string) (*backend.Ref, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.record(Call{Op: "lookup", Correlation: correlation})
	if s, ok := b.Sessions[correlation]; ok {
		r := s.Ref
		return &r, nil
	}
	return nil, nil
}

// Observe implements backend.Backend.
func (b *Backend) Observe(ctx context.Context, ref backend.Ref) (backend.Observation, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.record(Call{Op: "observe"})
	if b.ObserveError {
		return backend.Observation{}, fmt.Errorf("injected observation failure")
	}
	for _, s := range b.Sessions {
		if s.Ref.Key() == ref.Key() {
			if s.Ref.Generation != ref.Generation {
				return backend.Observation{Liveness: domain.LivenessExited, RawState: "pane_reused", Confidence: "MEDIUM", Source: "fake", ObservedAt: b.Clock.Now(), Generation: s.Ref.Generation}, nil
			}
			return backend.Observation{Liveness: s.Liveness, InputState: s.Input, Confidence: "HIGH", Source: "fake", ObservedAt: b.Clock.Now(), Generation: s.Ref.Generation}, nil
		}
	}
	return backend.Observation{Liveness: domain.LivenessExited, Confidence: "HIGH", Source: "fake", ObservedAt: b.Clock.Now()}, nil
}

// Submit implements backend.PromptSubmitter.
func (b *Backend) Submit(ctx context.Context, req backend.PromptRequest) (backend.PromptReceipt, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.Caps.SubmitPrompt.Usable() {
		b.record(Call{Op: "submit", DispatchID: req.DispatchID, Outcome: backend.NotSupported})
		return backend.PromptReceipt{Outcome: backend.NotSupported}, nil
	}
	for _, s := range b.Sessions {
		if s.Ref.Key() == req.Ref.Key() {
			s.Prompts = append(s.Prompts, req.Text)
			if b.LosePromptAck {
				b.LosePromptAck = false
				b.record(Call{Op: "submit", DispatchID: req.DispatchID, Outcome: backend.Ambiguous})
				return backend.PromptReceipt{Outcome: backend.Ambiguous}, nil
			}
			b.record(Call{Op: "submit", DispatchID: req.DispatchID, Outcome: backend.Delivered})
			return backend.PromptReceipt{Outcome: backend.Delivered}, nil
		}
	}
	b.record(Call{Op: "submit", DispatchID: req.DispatchID, Outcome: backend.NotSent})
	return backend.PromptReceipt{Outcome: backend.NotSent, Detail: "no such session"}, nil
}

// RequestInterrupt implements backend.SessionController.
func (b *Backend) RequestInterrupt(ctx context.Context, ref backend.Ref) (backend.ActionReceipt, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.record(Call{Op: "interrupt", Outcome: backend.Delivered})
	return backend.ActionReceipt{Outcome: backend.Delivered}, nil
}

// CloseOwnedSession implements backend.SessionController.
func (b *Backend) CloseOwnedSession(ctx context.Context, ref backend.Ref) (backend.ActionReceipt, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.record(Call{Op: "close", Outcome: backend.Delivered})
	for _, s := range b.Sessions {
		if s.Ref.Key() == ref.Key() {
			s.Liveness = domain.LivenessExited
		}
	}
	return backend.ActionReceipt{Outcome: backend.Delivered}, nil
}

// SetLiveness changes a session's observed liveness.
func (b *Backend) SetLiveness(correlation, liveness string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if s, ok := b.Sessions[correlation]; ok {
		s.Liveness = liveness
	}
}

// Reincarnate simulates a backend restart reusing the same pane id with a
// new generation (BE-07).
func (b *Backend) Reincarnate(correlation string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if s, ok := b.Sessions[correlation]; ok {
		s.Ref.Generation += "'"
	}
}

// SessionByAttempt returns the fake session launched for an attempt.
func (b *Backend) SessionByAttempt(attemptID string) *Session {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, s := range b.Sessions {
		if s.Spec.AttemptID == attemptID {
			return s
		}
	}
	return nil
}
