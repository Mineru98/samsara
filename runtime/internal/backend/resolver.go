package backend

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Mineru98/samsara/runtime/internal/domain"
)

// Set holds the adapters available to the runtime.
type Set struct {
	mu       sync.Mutex
	backends map[string]Backend
	cache    map[string]ProbeResult
	ttl      time.Duration
	clock    domain.Clock
}

// NewSet registers adapters by name.
func NewSet(clock domain.Clock, bs ...Backend) *Set {
	s := &Set{backends: map[string]Backend{}, cache: map[string]ProbeResult{}, ttl: 30 * time.Second, clock: clock}
	for _, b := range bs {
		s.backends[b.Name()] = b
	}
	return s
}

// Get returns the adapter of a kind.
func (s *Set) Get(name string) (Backend, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.backends[name]
	return b, ok
}

// Replace swaps an adapter (tests).
func (s *Set) Replace(b Backend) {
	s.mu.Lock()
	s.backends[b.Name()] = b
	delete(s.cache, b.Name())
	s.mu.Unlock()
}

// Probe returns a cached read-only probe result.
func (s *Set) Probe(ctx context.Context, name string) ProbeResult {
	s.mu.Lock()
	b, ok := s.backends[name]
	if r, cached := s.cache[name]; cached && s.clock.Now().Sub(r.ProbedAt) < s.ttl {
		s.mu.Unlock()
		return r
	}
	s.mu.Unlock()
	if !ok {
		return ProbeResult{Kind: name, Availability: domain.BackendUnavailable, Reasons: []string{"ADAPTER_NOT_REGISTERED"}, ProbedAt: s.clock.Now()}
	}
	r, err := b.Probe(ctx)
	if err != nil {
		r = ProbeResult{Kind: name, Availability: domain.BackendUnavailable, Reasons: []string{"PROBE_FAILED: " + err.Error()}}
	}
	r.Kind = name
	r.ProbedAt = s.clock.Now()
	s.mu.Lock()
	s.cache[name] = r
	s.mu.Unlock()
	return r
}

// Invalidate drops cached probe results.
func (s *Set) Invalidate() {
	s.mu.Lock()
	s.cache = map[string]ProbeResult{}
	s.mu.Unlock()
}

// Policy is the backend part of the project config.
type Policy struct {
	Mode          string
	Order         []string
	AllowFallback bool
}

// Decision records why a backend was selected (diagnostics, 01 §4.3).
type Decision struct {
	Selected string            `json:"selected"`
	Mode     string            `json:"mode"`
	Manual   bool              `json:"manual"` // selected backend cannot create workers: attach required
	Rejected map[string]string `json:"rejected"`
}

// ResolveForLaunch decides where a NEW worker is created. Explicit modes are
// respected; fallback only happens when allowed. It never applies to existing
// sessions, which keep their binding (R-11).
func (s *Set) ResolveForLaunch(ctx context.Context, p Policy, required []string) (Decision, error) {
	d := Decision{Mode: p.Mode, Rejected: map[string]string{}}
	order := p.Order
	if len(order) == 0 {
		order = []string{domain.BackendHerdr, domain.BackendTmux, domain.BackendStandalone}
	}
	candidates := order
	if p.Mode != "auto" && p.Mode != "" {
		candidates = []string{p.Mode}
		if p.AllowFallback {
			for _, o := range order {
				if o != p.Mode {
					candidates = append(candidates, o)
				}
			}
		}
	}
	for _, name := range candidates {
		r := s.Probe(ctx, name)
		if r.Availability == domain.BackendUnavailable {
			d.Rejected[name] = joinReasons(r.Reasons, "UNAVAILABLE")
			continue
		}
		if missing := missingCaps(r.Capabilities, required); missing != "" {
			d.Rejected[name] = "MISSING_CAPABILITY:" + missing
			continue
		}
		if r.Capabilities.CreateWorker.Usable() {
			d.Selected = name
			return d, nil
		}
		if name == domain.BackendStandalone {
			d.Selected, d.Manual = name, true
			return d, nil
		}
		d.Rejected[name] = "CREATE_WORKER_" + string(r.Capabilities.CreateWorker.Level) + ":" + r.Capabilities.CreateWorker.ReasonCode
	}
	return d, domain.Errf(domain.CodeUnsupportedCapability, "no backend satisfies launch policy (mode=%s)", p.Mode).WithDetails("rejected", d.Rejected)
}

func missingCaps(c Capabilities, required []string) string {
	for _, name := range required {
		if name == "create_worker" {
			continue // handled separately so standalone can fall back to attach
		}
		cap, ok := c.ByName(name)
		if !ok || !cap.Usable() {
			return name
		}
	}
	return ""
}

func joinReasons(rs []string, def string) string {
	if len(rs) == 0 {
		return def
	}
	return fmt.Sprint(rs)
}
