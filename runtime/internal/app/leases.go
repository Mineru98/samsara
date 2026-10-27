package app

import (
	"context"

	"github.com/Mineru98/samsara/runtime/internal/domain"
	"github.com/Mineru98/samsara/runtime/internal/store/sqlite"
)

// resourceKey is the execution resource of an attempt: its worktree. One
// ACTIVE lease per worktree (R-02, R-04).
func resourceKey(worktreeID string) string { return "worktree:" + worktreeID }

// bumpFence durably increments a resource's fence and returns the new value.
func bumpFence(ctx context.Context, tx *sqlite.Tx, key string) (int64, error) {
	if _, err := tx.ExecContext(ctx, `INSERT INTO resource_generations(resource_key,current_fence) VALUES(?,1)
		ON CONFLICT(resource_key) DO UPDATE SET current_fence=current_fence+1`, key); err != nil {
		return 0, err
	}
	return sqlite.CurrentFence(ctx, tx, key)
}

// issueLease grants execution authority to an attempt. It fails if another
// ACTIVE lease holds the resource; it never steals a lease.
func (a *App) issueLease(ctx context.Context, tx *sqlite.Tx, att *sqlite.Attempt, sessionID, bindingID string, ttlMillis int64) (*sqlite.Lease, error) {
	if att.WorktreeID == "" {
		return nil, domain.Errf(domain.CodeConflict, "attempt %s has no worktree", att.ID)
	}
	key := resourceKey(att.WorktreeID)
	cur, err := sqlite.ActiveLease(ctx, tx, key)
	if err != nil {
		return nil, err
	}
	if cur != nil {
		if cur.OwnerAttemptID != att.ID {
			return nil, domain.Errf(domain.CodeOwnershipConflict, "worktree is leased by attempt %s", cur.OwnerAttemptID)
		}
		if err := a.endLease(ctx, tx, cur, domain.LeaseReleased, "REISSUED"); err != nil {
			return nil, err
		}
	}
	fence, err := bumpFence(ctx, tx, key)
	if err != nil {
		return nil, err
	}
	l := &sqlite.Lease{ID: domain.NewID("lease"), ProjectID: att.ProjectID, ResourceKey: key, OwnerAttemptID: att.ID, SessionID: sessionID,
		BindingID: bindingID, Fence: fence, State: domain.LeaseActive, RuntimeEpoch: a.Epoch, IssuedAt: tx.Now, RenewedAt: tx.Now, DeadlineAt: tx.Now + ttlMillis}
	var sid, bid any
	if sessionID != "" {
		sid = sessionID
	}
	if bindingID != "" {
		bid = bindingID
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO leases(id,project_id,resource_key,owner_attempt_id,session_id,binding_id,fence,state,runtime_epoch,issued_at,renewed_at,deadline_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, l.ID, l.ProjectID, key, att.ID, sid, bid, fence, domain.LeaseActive, a.Epoch, tx.Now, tx.Now, l.DeadlineAt); err != nil {
		return nil, err
	}
	tx.Touch(att.ProjectID, sqlite.KindAttempt, att.ID)
	if err := tx.Audit(ctx, att.ProjectID, "runtime", "lease.issue", "attempt", att.ID, "", "", map[string]any{"lease_id": l.ID, "fence": fence}); err != nil {
		return nil, err
	}
	return l, nil
}

// endLease moves an ACTIVE lease to a terminal state and bumps the fence so
// any request carrying the old fence is stale (INV-03).
func (a *App) endLease(ctx context.Context, tx *sqlite.Tx, l *sqlite.Lease, state, reason string) error {
	res, err := tx.ExecContext(ctx, "UPDATE leases SET state=?, state_reason=?, version=version+1 WHERE id=? AND state='ACTIVE'", state, reason, l.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil
	}
	if _, err := bumpFence(ctx, tx, l.ResourceKey); err != nil {
		return err
	}
	tx.Touch(l.ProjectID, sqlite.KindAttempt, l.OwnerAttemptID)
	return tx.Audit(ctx, l.ProjectID, "runtime", "lease."+state, "attempt", l.OwnerAttemptID, "", reason, map[string]any{"lease_id": l.ID, "fence": l.Fence})
}

// revokeAttemptLease ends the attempt's ACTIVE lease, if any.
func (a *App) revokeAttemptLease(ctx context.Context, tx *sqlite.Tx, att *sqlite.Attempt, state, reason string) error {
	l, err := sqlite.LatestLeaseForAttempt(ctx, tx, att.ID)
	if err != nil || l == nil || l.State != domain.LeaseActive {
		return err
	}
	return a.endLease(ctx, tx, l, state, reason)
}

// expireLeases moves overdue ACTIVE leases to EXPIRED. Expiry never means
// the process ended (R-03): the attempt is flagged for reconciliation.
func (a *App) expireLeases(ctx context.Context) error {
	return a.DB.Write(ctx, func(tx *sqlite.Tx) error {
		ls, err := sqlite.LeasesWhere(ctx, tx, "state='ACTIVE' AND deadline_at < ?", tx.Now)
		if err != nil {
			return err
		}
		for _, l := range ls {
			if err := a.endLease(ctx, tx, l, domain.LeaseExpired, "TTL"); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, "UPDATE execution_attempts SET quarantine_reason=CASE WHEN quarantine_reason='' THEN 'LEASE_EXPIRED' ELSE quarantine_reason END, version=version+1, updated_at=? WHERE id=? AND state='RUNNING'", tx.Now, l.OwnerAttemptID); err != nil {
				return err
			}
			tx.Event(l.ProjectID, "lease.expired", "lease", l.ID, l.Version+1)
		}
		return nil
	})
}

// RenewRequest is sent by the owned worker launcher heartbeat.
type RenewRequest struct {
	Ticket    string `json:"ticket"`
	AttemptID string `json:"attempt_id"`
}

// RenewResult reports the current lease after renewal.
type RenewResult struct {
	Renewed  bool   `json:"renewed"`
	LeaseID  string `json:"lease_id,omitempty"`
	Fence    string `json:"fence,omitempty"`
	Deadline string `json:"deadline_at,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// RenewLease extends an ACTIVE lease of the ticket's attempt. A REVOKED
// lease is never revived. An EXPIRED lease (TTL or restart) is re-issued with
// a new fence only when the same backend binding still observes the same
// live process generation (01 §11.2).
func (a *App) RenewLease(ctx context.Context, req RenewRequest) (*RenewResult, error) {
	if err := a.requireReady(); err != nil {
		return nil, err
	}
	att, binding, err := a.attemptForTicket(ctx, req.Ticket)
	if err != nil {
		return nil, err
	}
	if req.AttemptID != "" && req.AttemptID != att.ID {
		return nil, domain.Errf(domain.CodeActionForbidden, "ticket does not belong to attempt")
	}
	verified := false
	var obsGen string
	if binding != nil {
		obs, err := a.observeBinding(ctx, binding)
		if err == nil && obs.Liveness == domain.LivenessAlive && obs.Generation == binding.Generation {
			verified = true
			obsGen = obs.Generation
		}
	}
	res := &RenewResult{}
	err = a.DB.Write(ctx, func(tx *sqlite.Tx) error {
		att, err := sqlite.GetAttempt(ctx, tx, att.ID)
		if err != nil {
			return err
		}
		p, err := sqlite.GetProject(ctx, tx, att.ProjectID)
		if err != nil {
			return err
		}
		cfg := projectConfig(p)
		l, err := sqlite.LatestLeaseForAttempt(ctx, tx, att.ID)
		if err != nil {
			return err
		}
		if l == nil {
			res.Reason = "NOT_REGISTERED"
			return nil
		}
		if att.State != domain.AttemptRunning {
			res.Reason = domain.ReasonAttemptNotRunning
			return nil
		}
		if reason := a.policyBlock(ctx, tx, p, att); reason != "" {
			res.Reason = reason
			return nil
		}
		switch {
		case l.State == domain.LeaseActive && l.RuntimeEpoch == a.Epoch && l.DeadlineAt >= tx.Now && tx.Now >= l.IssuedAt:
			dl := tx.Now + a.leaseTTL(cfg).Milliseconds()
			if _, err := tx.ExecContext(ctx, "UPDATE leases SET renewed_at=?, deadline_at=?, version=version+1 WHERE id=?", tx.Now, dl, l.ID); err != nil {
				return err
			}
			res.Renewed, res.LeaseID, res.Fence, res.Deadline = true, l.ID, domain.Dec(l.Fence), domain.RFC3339(dl)
		case l.State == domain.LeaseExpired || (l.State == domain.LeaseActive && (l.RuntimeEpoch != a.Epoch || l.DeadlineAt < tx.Now)):
			if !verified || binding == nil || l.BindingID != binding.ID || obsGen != binding.Generation {
				res.Reason = domain.ReasonQuarantined
				return nil
			}
			if l.State == domain.LeaseActive {
				if err := a.endLease(ctx, tx, l, domain.LeaseExpired, "TTL"); err != nil {
					return err
				}
			}
			nl, err := a.issueLease(ctx, tx, att, l.SessionID, l.BindingID, a.leaseTTL(cfg).Milliseconds())
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, "UPDATE execution_attempts SET quarantine_reason='', version=version+1, updated_at=? WHERE id=?", tx.Now, att.ID); err != nil {
				return err
			}
			tx.Event(att.ProjectID, "lease.reissued", "lease", nl.ID, 1)
			res.Renewed, res.LeaseID, res.Fence, res.Deadline = true, nl.ID, domain.Dec(nl.Fence), domain.RFC3339(nl.DeadlineAt)
		default:
			res.Reason = "LEASE_" + l.State
		}
		return nil
	})
	return res, err
}

// policyBlock returns a reason when the project/run/dependency state no
// longer allows execution of the attempt.
func (a *App) policyBlock(ctx context.Context, q sqlite.Q, p *sqlite.Project, att *sqlite.Attempt) string {
	if p.Activation != domain.ActivationEnabled {
		if p.DrainState == domain.DrainDraining || p.DrainState == domain.DrainRequested || p.DrainState == domain.DrainNeedsAttention {
			return domain.ReasonDisableDraining
		}
		return domain.ReasonProjectNotEnabled
	}
	run, err := sqlite.GetRun(ctx, q, att.RunID)
	if err != nil || run.State != domain.RunActive {
		return domain.ReasonRunNotActive
	}
	if run.Generation != att.RunGeneration {
		return domain.ReasonGenerationChanged
	}
	bl, err := sqlite.ExecutionBlockers(ctx, q, att.RunID, att.IssueID)
	if err != nil || len(bl) > 0 {
		return domain.ReasonDependencyUnmet
	}
	return ""
}
