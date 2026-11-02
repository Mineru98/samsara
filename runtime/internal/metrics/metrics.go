// Package metrics keeps the in-process runtime metrics of 01 §18.1. Values
// live in memory only: they describe the current daemon epoch and are read
// through `samsara metrics` and the diagnostics export.
package metrics

import (
	"sort"
	"sync"
	"time"
)

// window is how many recent samples a Summary keeps for its percentiles.
const window = 1024

// Summary records durations: an exact count, sum and max over the epoch and
// percentiles over the most recent samples.
type Summary struct {
	mu    sync.Mutex
	count int64
	sum   time.Duration
	max   time.Duration
	ring  []time.Duration
	next  int
}

// Observe records one duration.
func (s *Summary) Observe(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.count++
	s.sum += d
	if d > s.max {
		s.max = d
	}
	if len(s.ring) < window {
		s.ring = append(s.ring, d)
		return
	}
	s.ring[s.next] = d
	s.next = (s.next + 1) % window
}

// SummaryView is the read-only form of a Summary, in milliseconds.
type SummaryView struct {
	Count  int64   `json:"count"`
	MeanMs float64 `json:"mean_ms"`
	P50Ms  float64 `json:"p50_ms"`
	P95Ms  float64 `json:"p95_ms"`
	P99Ms  float64 `json:"p99_ms"`
	MaxMs  float64 `json:"max_ms"`
	Window int     `json:"window"`
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

// View returns the current values.
func (s *Summary) View() SummaryView {
	s.mu.Lock()
	samples := append([]time.Duration(nil), s.ring...)
	v := SummaryView{Count: s.count, MaxMs: ms(s.max), Window: len(samples)}
	if s.count > 0 {
		v.MeanMs = ms(s.sum / time.Duration(s.count))
	}
	s.mu.Unlock()
	if len(samples) == 0 {
		return v
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	pick := func(p float64) float64 {
		i := int(p*float64(len(samples))+0.5) - 1
		if i < 0 {
			i = 0
		}
		if i >= len(samples) {
			i = len(samples) - 1
		}
		return ms(samples[i])
	}
	v.P50Ms, v.P95Ms, v.P99Ms = pick(0.50), pick(0.95), pick(0.99)
	return v
}

// Counter counts events by label.
type Counter struct {
	mu sync.Mutex
	m  map[string]int64
}

// Inc adds one to label.
func (c *Counter) Inc(label string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[string]int64{}
	}
	c.m[label]++
}

// View returns a copy of the counts.
func (c *Counter) View() map[string]int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]int64, len(c.m))
	for k, v := range c.m {
		out[k] = v
	}
	return out
}

// Runtime groups the metrics recorded by the daemon process.
type Runtime struct {
	Since           time.Time
	HookDecision    Summary // PreToolUse policy evaluation (tool run time excluded)
	HookDecisions   Counter // by decision
	HookDenyReasons Counter // by reason_code
	SQLiteWrite     Summary // writer lock wait + transaction
	SSEConnects     Counter // by scope kind: project | catalog
	SSEReconnects   Counter // connects that resumed from a cursor
	SSEResets       Counter // reset_required by reason (replay gap)
}

// New returns an empty metrics set.
func New() *Runtime { return &Runtime{Since: time.Now()} }
