// Package standalone is the capability-limited mode for ordinary Claude Code
// sessions: attach, hook policy and observation only. It never creates
// terminals or injects keystrokes (01 §9).
package standalone

import (
	"context"
	"time"

	"github.com/Mineru98/samsara/runtime/internal/backend"
	"github.com/Mineru98/samsara/runtime/internal/domain"
	"github.com/Mineru98/samsara/runtime/internal/procinfo"
)

// Adapter implements backend.Backend only.
type Adapter struct{ Clock domain.Clock }

// Name implements backend.Backend.
func (a *Adapter) Name() string { return domain.BackendStandalone }

// Capabilities is the fixed Standalone MVP table.
func Capabilities() backend.Capabilities {
	un := func(r string) backend.Capability {
		return backend.Capability{Level: backend.Unsupported, ReasonCode: r}
	}
	return backend.Capabilities{
		CreateWorker:        un("STANDALONE_NO_PTY"),
		AttachSession:       backend.Capability{Level: backend.Supported},
		SubmitPrompt:        un("STANDALONE_NO_INPUT_INJECTION"),
		ObserveProcess:      backend.Capability{Level: backend.Conditional, ReasonCode: "HOOK_REPORTED_PID"},
		ObserveAgent:        backend.Capability{Level: backend.Supported, ReasonCode: "HOOK_EVENTS"},
		ReadOutput:          un("STANDALONE_NO_OUTPUT"),
		RequestInterrupt:    un("STANDALONE_NO_INPUT_INJECTION"),
		RestoreConversation: un("USER_RESUMES"),
	}
}

// Probe implements backend.Backend. Standalone is always available.
func (a *Adapter) Probe(ctx context.Context) (backend.ProbeResult, error) {
	return backend.ProbeResult{Kind: domain.BackendStandalone, Availability: domain.BackendAvailable, Capabilities: Capabilities()}, nil
}

// Observe checks the Claude process recorded at attach time.
func (a *Adapter) Observe(ctx context.Context, ref backend.Ref) (backend.Observation, error) {
	now := time.Now().UTC()
	if a.Clock != nil {
		now = a.Clock.Now()
	}
	obs := backend.Observation{Liveness: domain.LivenessUnknown, Confidence: "LOW", Source: "standalone.process", ObservedAt: now, Generation: ref.Generation}
	id, ok := procinfo.FromMap(ref.Fields)
	if !ok {
		return obs, nil
	}
	switch procinfo.Check(id) {
	case procinfo.Running:
		obs.Liveness, obs.Confidence = domain.LivenessAlive, "MEDIUM"
	case procinfo.Gone:
		obs.Liveness, obs.Confidence = domain.LivenessExited, "MEDIUM"
	}
	obs.ProcessIdentity = id.Map()
	return obs, nil
}

// FocusInstruction tells the user where the session lives.
func (a *Adapter) FocusInstruction(ref backend.Ref) (string, error) {
	return "Standalone 세션은 자동으로 열 수 없습니다. 해당 Claude Code를 실행한 터미널로 돌아가 계속 입력하세요.", nil
}
