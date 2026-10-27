package app

import (
	"context"
	"os"
	"time"

	"github.com/Mineru98/samsara/runtime/internal/domain"
	"github.com/Mineru98/samsara/runtime/internal/store/sqlite"
)

// Recover runs once at daemon start before commands are accepted
// (01 §15.1). Leases of the previous epoch are not honoured: they expire and
// their attempts wait for verified re-authorisation; in-flight intents become
// UNKNOWN or are re-checked, never blindly re-executed.
func (a *App) Recover(ctx context.Context) error {
	err := a.DB.Write(ctx, func(tx *sqlite.Tx) error {
		if _, err := tx.ExecContext(ctx, "UPDATE runtime_epochs SET stopped_at=? WHERE stopped_at IS NULL", tx.Now); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO runtime_epochs(epoch,pid,started_at) VALUES(?,?,?)", a.Epoch, os.Getpid(), tx.Now); err != nil {
			return err
		}
		ls, err := sqlite.LeasesWhere(ctx, tx, "state='ACTIVE' AND runtime_epoch<>?", a.Epoch)
		if err != nil {
			return err
		}
		for _, l := range ls {
			if err := a.endLease(ctx, tx, l, domain.LeaseExpired, domain.ReasonEpochChanged); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, "UPDATE execution_attempts SET quarantine_reason='RECOVERY_PENDING', version=version+1, updated_at=? WHERE id=? AND state='RUNNING'", tx.Now, l.OwnerAttemptID); err != nil {
				return err
			}
			tx.Event(l.ProjectID, "lease.expired", "lease", l.ID, l.Version+1)
		}
		// Observations from before the restart are stale.
		ss, err := sqlite.SessionsWhere(ctx, tx, "ended_at IS NULL AND liveness='ALIVE'")
		if err != nil {
			return err
		}
		for _, s := range ss {
			if _, err := tx.ExecContext(ctx, "UPDATE sessions SET liveness='UNKNOWN', confidence='LOW', version=version+1 WHERE id=?", s.ID); err != nil {
				return err
			}
			tx.Touch(s.ProjectID, sqlite.KindSession, s.ID)
		}
		// Intents caught mid-flight.
		rows, err := tx.QueryContext(ctx, "SELECT id, operation_id, kind FROM outbox WHERE state='RUNNING'")
		if err != nil {
			return err
		}
		type ob struct{ id, op, kind string }
		var obs []ob
		for rows.Next() {
			var o ob
			if err := rows.Scan(&o.id, &o.op, &o.kind); err != nil {
				rows.Close()
				return err
			}
			obs = append(obs, o)
		}
		rows.Close()
		for _, o := range obs {
			state, opState := "UNKNOWN", domain.OpUnknown
			if resendSafe[o.kind] {
				state, opState = "PENDING", domain.OpPending
			}
			if _, err := tx.ExecContext(ctx, "UPDATE outbox SET state=?, last_error='interrupted by runtime restart', updated_at=? WHERE id=?", state, tx.Now, o.id); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, "UPDATE operations SET state=?, updated_at=? WHERE id=?", opState, tx.Now, o.op); err != nil {
				return err
			}
			if o.kind == "prompt.submit" {
				if _, err := tx.ExecContext(ctx, "UPDATE dispatches SET state='UNKNOWN' WHERE operation_id=? AND state IN ('QUEUED','SENT')", o.op); err != nil {
					return err
				}
			}
		}
		if _, err := tx.ExecContext(ctx, "UPDATE merge_candidates SET publication_observation='UNKNOWN' WHERE state='PUBLISHING'"); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}
	a.ready.Store(true)
	if err := a.SyncRegistry(ctx); err != nil {
		a.Log.Warn("registry sync", "err", err)
	}
	a.Wake()
	return nil
}

// Loop runs reconcile passes until ctx ends. Each pass is idempotent.
func (a *App) Loop(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	lastSlow := time.Time{}
	for {
		a.ReconcileOnce(ctx)
		if time.Since(lastSlow) > time.Minute {
			lastSlow = time.Now()
			if err := a.markMissingProjects(ctx); err != nil {
				a.Log.Warn("missing projects", "err", err)
			}
			if err := a.PruneEvents(ctx); err != nil {
				a.Log.Warn("prune", "err", err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-a.wake:
		}
	}
}

// ReconcileOnce performs one full reconcile pass (also used by tests).
func (a *App) ReconcileOnce(ctx context.Context) {
	steps := []struct {
		name string
		fn   func(context.Context) error
	}{
		{"expire", a.expireLeases},
		{"observe", a.observeSessions},
		{"outbox-unknown", a.reconcileUnknownOutbox},
		{"outbox", a.DispatchOutbox},
		{"schedule", a.ScheduleTick},
		{"outbox", a.DispatchOutbox},
		{"publishing", a.reconcilePublishing},
		{"merge", a.MergeTick},
		{"drain", a.progressDrains},
	}
	for _, s := range steps {
		if ctx.Err() != nil {
			return
		}
		if err := s.fn(ctx); err != nil {
			a.Log.Warn("reconcile step failed", "step", s.name, "err", err)
		}
	}
}

// observeSessions asks each managed session's own backend about liveness.
// A failed observation makes liveness UNKNOWN; it never triggers failover to
// another backend (INV-04, BE-06).
func (a *App) observeSessions(ctx context.Context) error {
	type target struct {
		s *sqlite.Session
		b *sqlite.Binding
	}
	var ts []target
	var starting []*sqlite.Attempt
	if err := a.DB.Read(ctx, func(q sqlite.Q) error {
		ss, err := sqlite.SessionsWhere(ctx, q, "managed=1 AND ended_at IS NULL")
		if err != nil {
			return err
		}
		for _, s := range ss {
			b, err := sqlite.CurrentBindingForSession(ctx, q, s.ID)
			if err != nil {
				return err
			}
			if b != nil {
				ts = append(ts, target{s, b})
			}
		}
		starting, err = sqlite.AttemptsWhere(ctx, q, "state='STARTING' AND updated_at < ?", a.nowMillis()-3*60*1000)
		return err
	}); err != nil {
		return err
	}
	now := a.Clock.Now()
	for _, t := range ts {
		if last, ok := a.observedAt.Load(t.s.ID); ok && now.Sub(last.(time.Time)) < 5*time.Second {
			continue
		}
		a.observedAt.Store(t.s.ID, now)
		obs, err := a.observeBinding(ctx, t.b)
		sid := t.s.ID
		_ = a.DB.Write(ctx, func(tx *sqlite.Tx) error {
			s, err2 := sqlite.GetSession(ctx, tx, sid)
			if err2 != nil || s.EndedAt != 0 {
				return err2
			}
			if err != nil {
				if s.Liveness == domain.LivenessUnknown {
					return nil
				}
				_, e := tx.ExecContext(ctx, "UPDATE sessions SET liveness='UNKNOWN', confidence='LOW', backend_raw_state='observation_failed', version=version+1 WHERE id=?", s.ID)
				tx.Touch(s.ProjectID, sqlite.KindSession, s.ID)
				return e
			}
			if obs.Liveness == domain.LivenessExited && obs.Confidence != "LOW" {
				return a.sessionExited(ctx, tx, s, "OBSERVED_EXIT:"+obs.RawState)
			}
			if obs.Liveness != s.Liveness || obs.Confidence != s.Confidence {
				if _, e := tx.ExecContext(ctx, "UPDATE sessions SET liveness=?, confidence=?, backend_raw_state=?, observed_at=?, version=version+1 WHERE id=?",
					obs.Liveness, obs.Confidence, obs.RawState, tx.Now, s.ID); e != nil {
					return e
				}
				tx.Touch(s.ProjectID, sqlite.KindSession, s.ID)
			}
			return nil
		})
	}
	// Workers that never registered: confirm their fate through the binding.
	for _, att := range starting {
		var b *sqlite.Binding
		_ = a.DB.Read(ctx, func(q sqlite.Q) error {
			b, _ = sqlite.CurrentBindingForAttempt(ctx, q, att.ID)
			return nil
		})
		if b == nil {
			continue
		}
		obs, err := a.observeBinding(ctx, b)
		aid := att.ID
		_ = a.DB.Write(ctx, func(tx *sqlite.Tx) error {
			cur, e := sqlite.GetAttempt(ctx, tx, aid)
			if e != nil || cur.State != domain.AttemptStarting {
				return e
			}
			switch {
			case err == nil && obs.Liveness == domain.LivenessExited:
				return a.setAttemptState(ctx, tx, cur, domain.AttemptFailed, "WORKER_EXITED_BEFORE_REGISTRATION")
			case cur.QuarantineReason == "" && tx.Now-cur.UpdatedAt > 10*60*1000:
				_, e := tx.ExecContext(ctx, "UPDATE execution_attempts SET quarantine_reason='REGISTRATION_TIMEOUT', version=version+1 WHERE id=?", cur.ID)
				tx.Touch(cur.ProjectID, sqlite.KindAttempt, cur.ID)
				tx.Touch(cur.ProjectID, sqlite.KindIssue, cur.IssueID)
				return e
			}
			return nil
		})
	}
	return nil
}

// reconcileUnknownOutbox re-queues UNKNOWN intents whose handler inspects
// external state first. Others (prompts, interrupts) stay UNKNOWN for a
// human decision (INV-08).
func (a *App) reconcileUnknownOutbox(ctx context.Context) error {
	return a.DB.Write(ctx, func(tx *sqlite.Tx) error {
		rows, err := tx.QueryContext(ctx, "SELECT id, kind, attempt_count, updated_at FROM outbox WHERE state='UNKNOWN'")
		if err != nil {
			return err
		}
		type ob struct {
			id, kind  string
			n         int
			updatedAt int64
		}
		var obs []ob
		for rows.Next() {
			var o ob
			if err := rows.Scan(&o.id, &o.kind, &o.n, &o.updatedAt); err != nil {
				rows.Close()
				return err
			}
			obs = append(obs, o)
		}
		rows.Close()
		for _, o := range obs {
			if !resendSafe[o.kind] || o.n >= maxOutboxAttempts || tx.Now-o.updatedAt < 20*1000 {
				continue
			}
			if _, err := tx.ExecContext(ctx, "UPDATE outbox SET state='PENDING', retry_at=?, updated_at=? WHERE id=?", tx.Now, tx.Now, o.id); err != nil {
				return err
			}
		}
		return nil
	})
}

// PruneEvents removes replay events older than the retention window. Audit
// and integration evidence are kept separately (DB-14).
func (a *App) PruneEvents(ctx context.Context) error {
	return a.DB.Write(ctx, func(tx *sqlite.Tx) error {
		cutoff := tx.Now - 7*24*3600*1000
		var maxSeq int64
		if err := tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(seq),0) FROM events WHERE recorded_at < ?", cutoff).Scan(&maxSeq); err != nil || maxSeq == 0 {
			return err
		}
		// Keep the newest event of every project so project_version chains stay checkable.
		if _, err := tx.ExecContext(ctx, `DELETE FROM events WHERE seq <= ? AND seq NOT IN (SELECT MAX(seq) FROM events GROUP BY project_id)`, maxSeq); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM catalog_events WHERE recorded_at < ?", cutoff); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "UPDATE runtime_metadata SET pruned_through_seq=MAX(pruned_through_seq, ?) WHERE id=1", maxSeq)
		return err
	})
}
