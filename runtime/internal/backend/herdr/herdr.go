// Package herdr adapts herdr. The installed protocol must be pinned in the
// compatibility manifest before managed execution is enabled (01 §7, M0):
// until a schema fixture is verified, worker creation and prompting report
// UNSUPPORTED with HERDR_COMPAT_UNVERIFIED and the resolver falls back to the
// next backend. Probing is read-only and never starts a herdr server.
package herdr

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"time"

	"github.com/Mineru98/samsara/runtime/internal/backend"
	"github.com/Mineru98/samsara/runtime/internal/domain"
)

// Adapter implements backend.Backend.
type Adapter struct {
	Binary string
	// VerifiedSchemaSHA256 is the schema hash recorded in the compatibility
	// manifest. Empty means no verified protocol.
	VerifiedSchemaSHA256 string
	Clock                domain.Clock
}

// New returns the adapter.
func New(clock domain.Clock, verifiedSchema string) *Adapter {
	return &Adapter{Binary: "herdr", VerifiedSchemaSHA256: verifiedSchema, Clock: clock}
}

// Name implements backend.Backend.
func (a *Adapter) Name() string { return domain.BackendHerdr }

// Probe records the binary version and protocol schema hash.
func (a *Adapter) Probe(ctx context.Context) (backend.ProbeResult, error) {
	r := backend.ProbeResult{Kind: domain.BackendHerdr}
	if _, err := exec.LookPath(a.Binary); err != nil {
		r.Availability, r.Reasons = domain.BackendUnavailable, []string{"BINARY_NOT_FOUND"}
		return r, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, a.Binary, "--version").Output(); err == nil {
		r.Version = strings.TrimSpace(string(out))
	}
	var schema bytes.Buffer
	cmd := exec.CommandContext(ctx, a.Binary, "api", "schema", "--json")
	cmd.Stdout = &schema
	if err := cmd.Run(); err != nil {
		r.Availability, r.Reasons = domain.BackendUnavailable, []string{"SCHEMA_UNAVAILABLE"}
		return r, nil
	}
	r.SchemaSHA256 = domain.DigestBytes(schema.Bytes())
	un := func(code string) backend.Capability {
		return backend.Capability{Level: backend.Unsupported, ReasonCode: code}
	}
	verified := a.VerifiedSchemaSHA256 != "" && a.VerifiedSchemaSHA256 == r.SchemaSHA256
	reason := "HERDR_COMPAT_UNVERIFIED"
	if a.VerifiedSchemaSHA256 != "" && !verified {
		reason = "HERDR_SCHEMA_MISMATCH"
	}
	r.Capabilities = backend.Capabilities{
		CreateWorker: un(reason), AttachSession: un(reason), SubmitPrompt: un(reason), ObserveProcess: un(reason),
		ObserveAgent: un(reason), ReadOutput: un(reason), RequestInterrupt: un(reason), RestoreConversation: un(reason),
	}
	r.Availability = domain.BackendDegraded
	r.Reasons = []string{reason}
	return r, nil
}

// Observe cannot claim anything without a verified protocol.
func (a *Adapter) Observe(ctx context.Context, ref backend.Ref) (backend.Observation, error) {
	now := time.Now().UTC()
	if a.Clock != nil {
		now = a.Clock.Now()
	}
	return backend.Observation{Liveness: domain.LivenessUnknown, Confidence: "NONE", Source: "herdr.unverified", ObservedAt: now, Generation: ref.Generation}, nil
}
