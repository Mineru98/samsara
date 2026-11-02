package app

import (
	"context"
	"time"

	"github.com/Mineru98/samsara/runtime/internal/domain"
	"github.com/Mineru98/samsara/runtime/internal/metrics"
	"github.com/Mineru98/samsara/runtime/internal/store/sqlite"
)

func newMetrics(db *sqlite.DB) *metrics.Runtime {
	m := metrics.New()
	db.WriteLatency = &m.SQLiteWrite
	return m
}

// recordDecision counts one PreToolUse policy decision and its latency.
func (a *App) recordDecision(d *ToolDecision, took time.Duration) {
	if a.Metrics == nil {
		return
	}
	a.Metrics.HookDecision.Observe(took)
	if d == nil {
		a.Metrics.HookDecisions.Inc("ERROR")
		return
	}
	a.Metrics.HookDecisions.Inc(d.Decision)
	if d.Decision == domain.DecisionDeny {
		a.Metrics.HookDenyReasons.Inc(d.ReasonCode)
	}
}

// MetricsView is the `samsara metrics` result. Memory values cover the
// current daemon epoch; gauges are read from the database now.
type MetricsView struct {
	Epoch        string              `json:"runtime_epoch"`
	Since        string              `json:"since"`
	CollectedAt  string              `json:"collected_at"`
	Hook         HookStats           `json:"hook"`
	SQLiteWriter metrics.SummaryView `json:"sqlite_writer_latency"`
	SSE          SSEStats            `json:"sse"`
	Gauges       Gauges              `json:"gauges"`
}

// HookStats are the PreToolUse decision metrics.
type HookStats struct {
	DecisionLatency metrics.SummaryView `json:"decision_latency"`
	Decisions       map[string]int64    `json:"decisions"`
	DenyByReason    map[string]int64    `json:"deny_by_reason"`
}

// SSEStats are the UI stream metrics.
type SSEStats struct {
	Connects       map[string]int64 `json:"connects"`
	Reconnects     map[string]int64 `json:"reconnects"`
	ResetsByReason map[string]int64 `json:"reset_required_by_reason"`
}

// Gauges are point-in-time counts read from the database.
type Gauges struct {
	ActiveAttempts      int64 `json:"active_attempts"`
	UnknownSessions     int64 `json:"unknown_sessions"`
	QuarantinedAttempts int64 `json:"quarantined_attempts"`
	OutboxPending       int64 `json:"outbox_pending"`
	OutboxLagMs         int64 `json:"outbox_lag_ms"`
	AmbiguousOperations int64 `json:"ambiguous_operations"`
	MergeQueueDepth     int64 `json:"merge_queue_depth"`
	EventRetentionDays  int   `json:"event_retention_days"`
}

// MetricsSnapshot returns the current metrics.
func (a *App) MetricsSnapshot(ctx context.Context) (*MetricsView, error) {
	m := a.Metrics
	if m == nil {
		m = metrics.New()
	}
	now := a.Clock.Now()
	v := &MetricsView{Epoch: a.Epoch, Since: m.Since.UTC().Format(time.RFC3339), CollectedAt: now.UTC().Format(time.RFC3339),
		Hook:         HookStats{DecisionLatency: m.HookDecision.View(), Decisions: m.HookDecisions.View(), DenyByReason: m.HookDenyReasons.View()},
		SQLiteWriter: m.SQLiteWrite.View(),
		SSE:          SSEStats{Connects: m.SSEConnects.View(), Reconnects: m.SSEReconnects.View(), ResetsByReason: m.SSEResets.View()},
	}
	g := &v.Gauges
	err := a.DB.Read(ctx, func(q sqlite.Q) error {
		counts := []struct {
			dst *int64
			sql string
		}{
			{&g.ActiveAttempts, "SELECT COUNT(*) FROM execution_attempts WHERE state IN ('STARTING','RUNNING','PAUSING')"},
			{&g.UnknownSessions, "SELECT COUNT(*) FROM sessions WHERE ended_at IS NULL AND liveness='UNKNOWN'"},
			{&g.QuarantinedAttempts, "SELECT COUNT(*) FROM execution_attempts WHERE state='RUNNING' AND quarantine_reason<>''"},
			{&g.OutboxPending, "SELECT COUNT(*) FROM outbox WHERE state IN ('PENDING','RUNNING')"},
			{&g.AmbiguousOperations, "SELECT COUNT(*) FROM operations WHERE state='UNKNOWN'"},
			{&g.MergeQueueDepth, "SELECT COUNT(*) FROM merge_candidates WHERE state IN ('WAITING','VALIDATING','READY','PUBLISHING')"},
		}
		for _, c := range counts {
			if err := q.QueryRowContext(ctx, c.sql).Scan(c.dst); err != nil {
				return err
			}
		}
		var oldest int64
		if err := q.QueryRowContext(ctx, "SELECT COALESCE(MIN(created_at),0) FROM outbox WHERE state IN ('PENDING','RUNNING')").Scan(&oldest); err != nil {
			return err
		}
		if oldest > 0 {
			g.OutboxLagMs = domain.Millis(now) - oldest
		}
		days, err := eventRetentionDays(ctx, q)
		g.EventRetentionDays = days
		return err
	})
	return v, err
}

// eventRetentionDays is the replay window for the shared event log: the
// longest observation.event_retention_days of the projects still in the
// catalog, so every project keeps at least what it configured. The event
// cursor watermark (pruned_through_seq) is global, which is why the window
// is not cut per project.
func eventRetentionDays(ctx context.Context, q sqlite.Q) (int, error) {
	ps, err := sqlite.ListProjects(ctx, q)
	if err != nil {
		return 0, err
	}
	days := 0
	for _, p := range ps {
		if p.RegistryState == domain.RegistryArchived {
			continue
		}
		if d := projectConfig(p).Observation.EventRetentionDays; d > days {
			days = d
		}
	}
	if days == 0 {
		days = defaultEventRetentionDays
	}
	return days, nil
}

const defaultEventRetentionDays = 7
