package app_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/Mineru98/samsara/runtime/internal/app"
	"github.com/Mineru98/samsara/runtime/internal/store/sqlite"
)

// insertIntent records one in-flight outbox intent with its operation.
func insertIntent(t *testing.T, e *env, id, kind string) {
	t.Helper()
	now := e.clock.Now().UnixMilli()
	if err := e.db.Write(e.ctx, func(tx *sqlite.Tx) error {
		if _, err := tx.ExecContext(e.ctx, "INSERT INTO operations(id,project_id,kind,state,created_at,updated_at) VALUES(?,?,?,?,?,?)", "op_"+id, e.project, kind, "PENDING", now, now); err != nil {
			return err
		}
		_, err := tx.ExecContext(e.ctx, "INSERT INTO outbox(id,operation_id,project_id,kind,payload,dedupe_key,state,retry_at,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)",
			"ob_"+id, "op_"+id, e.project, kind, "{}", "dk_"+id, "PENDING", now, now, now)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func stateOf(t *testing.T, db *sqlite.DB, table, id string) string {
	t.Helper()
	var s string
	if err := db.Read(t.Context(), func(q sqlite.Q) error {
		return q.QueryRowContext(t.Context(), "SELECT state FROM "+table+" WHERE id=?", id).Scan(&s)
	}); err != nil {
		t.Fatal(err)
	}
	return s
}

// DB-12/DB-13/EVT-11: a restored backup gets a new generation so cursors
// issued before the restore reset, and its in-flight intents are held as
// UNKNOWN instead of being re-sent.
func TestRestoredDatabaseResetsCursorsAndHoldsIntents(t *testing.T) {
	e := newEnv(t, baseConfig)
	insertIntent(t, e, "prompt", "prompt.submit")
	insertIntent(t, e, "publish", "git.publish")
	backup := filepath.Join(t.TempDir(), "backup.db")
	if err := e.db.Backup(e.ctx, backup); err != nil {
		t.Fatal(err)
	}
	// The UI keeps reading the live database after the backup was taken.
	e.applyPlan(singlePlan)
	snap, err := e.app.ProjectSnapshot(e.ctx, e.project)
	if err != nil {
		t.Fatal(err)
	}
	cur, _ := app.DecodeCursor(snap.SnapshotCursor)

	open := func(path string) *sqlite.DB {
		db, err := sqlite.Open(e.ctx, path, e.clock)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Close() })
		return db
	}
	// Control: the raw backup shares the old generation, so the cursor
	// would be served from a lineage that never had those events.
	raw := filepath.Join(t.TempDir(), "raw.db")
	if err := sqlite.BackupFile(e.ctx, backup, raw); err != nil {
		t.Fatal(err)
	}
	rawApp := app.New(open(raw), e.layout, e.backend, nil)
	if b, _ := rawApp.ReadProjectEvents(e.ctx, e.project, cur, 10); b.ResetRequired != "" {
		t.Fatalf("control: unprepared copy reset unexpectedly (%s)", b.ResetRequired)
	}

	restored := open(backup)
	if err := app.PrepareRestored(e.ctx, restored, backup); err != nil {
		t.Fatal(err)
	}
	if restored.DatabaseGeneration == e.db.DatabaseGeneration {
		t.Fatal("generation not rotated")
	}
	a := app.New(restored, e.layout, e.backend, nil)
	if b, _ := a.ReadProjectEvents(e.ctx, e.project, cur, 10); b.ResetRequired != "DATABASE_GENERATION_CHANGED" {
		t.Fatalf("EVT-11: old cursor after restore = %+v", b)
	}
	for _, id := range []string{"prompt", "publish"} {
		if s := stateOf(t, restored, "outbox", "ob_"+id); s != "UNKNOWN" {
			t.Errorf("outbox %s = %s, want UNKNOWN", id, s)
		}
		if s := stateOf(t, restored, "operations", "op_"+id); s != "UNKNOWN" {
			t.Errorf("operation %s = %s, want UNKNOWN", id, s)
		}
	}
	var audits int
	_ = restored.Read(e.ctx, func(q sqlite.Q) error {
		return q.QueryRowContext(e.ctx, "SELECT COUNT(*) FROM audit_entries WHERE action='runtime.restore'").Scan(&audits)
	})
	if audits != 1 {
		t.Fatalf("restore audit rows = %d", audits)
	}
	// The live database is untouched.
	if s := stateOf(t, e.db, "outbox", "ob_prompt"); s != "PENDING" {
		t.Fatalf("live outbox changed: %s", s)
	}
}

func countEvents(t *testing.T, e *env, before int64) int {
	t.Helper()
	var n int
	_ = e.db.Read(e.ctx, func(q sqlite.Q) error {
		return q.QueryRowContext(e.ctx, "SELECT COUNT(*) FROM events WHERE recorded_at < ?", before).Scan(&n)
	})
	return n
}

// observation.event_retention_days decides the replay window (was 7 days).
func TestEventRetentionFollowsConfig(t *testing.T) {
	for _, tc := range []struct {
		name     string
		cfg      string
		advance  time.Duration
		wantKept bool
	}{
		{"default 7 days prunes 10 day old events", baseConfig, 10 * 24 * time.Hour, false},
		{"30 days keeps 10 day old events", baseConfig + "observation:\n  event_retention_days: 30\n", 10 * 24 * time.Hour, true},
		{"30 days prunes 31 day old events", baseConfig + "observation:\n  event_retention_days: 30\n", 31 * 24 * time.Hour, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t, tc.cfg)
			old := e.clock.Now().UnixMilli() + 1
			if countEvents(t, e, old) < 1 {
				t.Fatal("setup: registration should record events")
			}
			e.clock.Advance(tc.advance)
			e.applyPlan(singlePlan) // newest event of the project is recent
			if err := e.app.PruneEvents(e.ctx); err != nil {
				t.Fatal(err)
			}
			if kept := countEvents(t, e, old) > 0; kept != tc.wantKept {
				t.Fatalf("old events kept = %v, want %v", kept, tc.wantKept)
			}
		})
	}
}

// 01 §18.1: hook decisions, deny reasons and writer latency are recorded.
func TestMetricsRecordHookDecisions(t *testing.T) {
	e := newEnv(t, baseConfig)
	d := e.check("claude-unknown", e.repo, filepath.Join(e.repo, "README.md"))
	m, err := e.app.MetricsSnapshot(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if m.Hook.DecisionLatency.Count != 1 || m.Hook.Decisions[d.Decision] != 1 {
		t.Fatalf("decision metrics = %+v (decision %s)", m.Hook, d.Decision)
	}
	if d.Decision == "DENY" && m.Hook.DenyByReason[d.ReasonCode] != 1 {
		t.Fatalf("deny reason %s not counted: %+v", d.ReasonCode, m.Hook.DenyByReason)
	}
	if m.SQLiteWriter.Count == 0 {
		t.Fatal("writer latency not recorded")
	}
	if m.Gauges.EventRetentionDays != 7 {
		t.Fatalf("retention gauge = %d", m.Gauges.EventRetentionDays)
	}
	insertIntent(t, e, "lag", "prompt.submit")
	e.clock.Advance(3 * time.Second)
	m, _ = e.app.MetricsSnapshot(e.ctx)
	if m.Gauges.OutboxPending != 1 || m.Gauges.OutboxLagMs != 3000 {
		t.Fatalf("outbox gauges = %+v", m.Gauges)
	}
}
