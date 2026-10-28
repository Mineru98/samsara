package app

import (
	"context"
	"encoding/json"

	"github.com/Mineru98/samsara/runtime/internal/backend"
	"github.com/Mineru98/samsara/runtime/internal/domain"
	"github.com/Mineru98/samsara/runtime/internal/store/sqlite"
)

// RunCommandResult reports a run command.
type RunCommandResult struct {
	RunID      string `json:"run_id"`
	State      string `json:"state"`
	Version    string `json:"version"`
	ApprovalID string `json:"approval_id,omitempty"`
}

func planSpec(p *sqlite.Project, r *sqlite.Run) approvalSpec {
	return approvalSpec{Kind: domain.ApprovalPlan, ProjectID: p.ID, RunID: r.ID, RunGeneration: r.Generation, PolicyVersion: p.PolicyVersion,
		Subject: map[string]any{"plan_digest": r.PlanDigest, "graph_version": r.GraphVersion}}
}

// StartRun moves a PLANNED run to ACTIVE after re-checking activation, drain
// and the PLAN approval (PLN-06). It never creates workers itself; the
// scheduler does after this commit.
func (a *App) StartRun(ctx context.Context, cmd Cmd, runID string) (*RunCommandResult, error) {
	if err := a.requireReady(); err != nil {
		return nil, err
	}
	var out RunCommandResult
	var blocked error
	err := a.DB.Write(ctx, func(tx *sqlite.Tx) error {
		// run start is naturally idempotent (ACTIVE stays ACTIVE) and must
		// re-check the PLAN approval on every call, so responses are not
		// replayed from the command table.
		res, err := func() (*RunCommandResult, error) {
			r, err := sqlite.GetRun(ctx, tx, runID)
			if err != nil {
				return nil, err
			}
			if err := checkIfMatch(cmd, r.Version); err != nil {
				return nil, err
			}
			if r.State == domain.RunActive {
				return &RunCommandResult{RunID: r.ID, State: r.State, Version: domain.Dec(r.Version)}, nil
			}
			if r.State != domain.RunPlanned {
				return nil, domain.Errf(domain.CodeConflict, "run is %s", r.State)
			}
			p, err := sqlite.GetProject(ctx, tx, r.ProjectID)
			if err != nil {
				return nil, err
			}
			if p.Activation != domain.ActivationEnabled || p.RegistryState != domain.RegistryPresent {
				return nil, domain.Errf(domain.CodeProjectNotEnabled, "project is %s/%s", p.Activation, p.RegistryState)
			}
			cfg := projectConfig(p)
			ap, err := a.requestApproval(ctx, tx, planSpec(p, r))
			if err != nil {
				return nil, err
			}
			if cfg.Approval.ImplementationStart == "auto" {
				if err := a.approveAutomatically(ctx, tx, ap); err != nil {
					return nil, err
				}
				ap.State = domain.ApprovalApproved
			}
			if ap.State != domain.ApprovalApproved {
				// Keep the PENDING approval but report the blocked start. The
				// approval row must survive, so this is not a tx error.
				blocked = domain.Errf(domain.CodeApprovalRequired, "PLAN approval required before start").WithDetails("approval_id", ap.ID)
				return &RunCommandResult{RunID: r.ID, State: r.State, Version: domain.Dec(r.Version), ApprovalID: ap.ID}, nil
			}
			if _, err := tx.ExecContext(ctx, "UPDATE runs SET state='ACTIVE', started_at=?, version=version+1 WHERE id=?", tx.Now, r.ID); err != nil {
				return nil, err
			}
			tx.Touch(r.ProjectID, sqlite.KindRun, r.ID)
			tx.Touch(r.ProjectID, sqlite.KindProject, r.ProjectID)
			if err := a.touchRunIssues(ctx, tx, r.ProjectID, r.ID); err != nil {
				return nil, err
			}
			tx.Event(r.ProjectID, "run.started", "run", r.ID, r.Version+1)
			if err := tx.Audit(ctx, r.ProjectID, cmd.Principal.ID, "run.start", "run", r.ID, "", "", nil); err != nil {
				return nil, err
			}
			return &RunCommandResult{RunID: r.ID, State: domain.RunActive, Version: domain.Dec(r.Version + 1), ApprovalID: ap.ID}, nil
		}()
		if err != nil {
			return err
		}
		out = *res
		return nil
	})
	if err != nil {
		return nil, err
	}
	a.Wake()
	if blocked != nil {
		return &out, blocked
	}
	return &out, nil
}

// PauseRun stops new tool calls of the run's attempts by revoking leases.
// Attempts go to PAUSING until stop evidence arrives (01 §10.6).
func (a *App) PauseRun(ctx context.Context, cmd Cmd, runID string) (*RunCommandResult, error) {
	return a.runCommand(ctx, cmd, "run.pause", runID, func(tx *sqlite.Tx, r *sqlite.Run) error {
		if r.State != domain.RunActive {
			return domain.Errf(domain.CodeConflict, "run is %s", r.State)
		}
		return a.pauseRunTx(ctx, tx, r, "RUN_PAUSED")
	})
}

func (a *App) pauseRunTx(ctx context.Context, tx *sqlite.Tx, r *sqlite.Run, reason string) error {
	if _, err := tx.ExecContext(ctx, "UPDATE runs SET state='PAUSED', version=version+1 WHERE id=?", r.ID); err != nil {
		return err
	}
	atts, err := sqlite.AttemptsWhere(ctx, tx, "run_id=? AND state IN ('STARTING','RUNNING')", r.ID)
	if err != nil {
		return err
	}
	for _, att := range atts {
		if err := a.pauseAttempt(ctx, tx, att, reason); err != nil {
			return err
		}
	}
	tx.Touch(r.ProjectID, sqlite.KindRun, r.ID)
	tx.Touch(r.ProjectID, sqlite.KindProject, r.ProjectID)
	tx.Event(r.ProjectID, "run.paused", "run", r.ID, r.Version+1)
	return nil
}

// pauseAttempt revokes the lease and moves the attempt to PAUSING. An
// interrupt request is queued when the backend supports one; neither the deny
// nor the interrupt is treated as proof that the tool stopped.
func (a *App) pauseAttempt(ctx context.Context, tx *sqlite.Tx, att *sqlite.Attempt, reason string) error {
	if err := a.revokeAttemptLease(ctx, tx, att, domain.LeaseRevoked, reason); err != nil {
		return err
	}
	next := domain.AttemptPausing
	as, err := sqlite.ActiveAssignmentForAttempt(ctx, tx, att.ID)
	if err != nil {
		return err
	}
	if att.State == domain.AttemptStarting && as == nil {
		// Nothing registered yet: no tool can be in flight from this attempt.
		next = domain.AttemptPaused
	}
	if as != nil {
		if s, err := sqlite.GetSession(ctx, tx, as.SessionID); err == nil && (s.Liveness == domain.LivenessExited || s.InputState == domain.InputReady) {
			next = domain.AttemptPaused
		}
	}
	if _, err := tx.ExecContext(ctx, "UPDATE execution_attempts SET state=?, state_reason=?, version=version+1, updated_at=? WHERE id=?", next, reason, tx.Now, att.ID); err != nil {
		return err
	}
	tx.Touch(att.ProjectID, sqlite.KindAttempt, att.ID)
	tx.Touch(att.ProjectID, sqlite.KindIssue, att.IssueID)
	tx.Event(att.ProjectID, "attempt.pausing", "attempt", att.ID, att.Version+1)
	if as != nil && next == domain.AttemptPausing {
		if b, err := sqlite.CurrentBindingForAttempt(ctx, tx, att.ID); err == nil && b != nil {
			if _, err := tx.Enqueue(ctx, sqlite.OutboxItem{ProjectID: att.ProjectID, Kind: "session.interrupt",
				Payload: map[string]string{"attempt_id": att.ID, "binding_id": b.ID}, DedupeKey: "interrupt:" + att.ID + ":" + domain.Dec(att.Version+1),
				ResourceKey: "attempt:" + att.ID}); err != nil {
				return err
			}
		}
	}
	return tx.Audit(ctx, att.ProjectID, "runtime", "attempt.pause", "attempt", att.ID, "", reason, nil)
}

// ResumeRun re-activates the run; paused attempts are re-evaluated by the
// scheduler (dependency, quota, backend, liveness) before any new lease.
func (a *App) ResumeRun(ctx context.Context, cmd Cmd, runID string) (*RunCommandResult, error) {
	return a.runCommand(ctx, cmd, "run.resume", runID, func(tx *sqlite.Tx, r *sqlite.Run) error {
		if r.State != domain.RunPaused {
			return domain.Errf(domain.CodeConflict, "run is %s", r.State)
		}
		p, err := sqlite.GetProject(ctx, tx, r.ProjectID)
		if err != nil {
			return err
		}
		if p.Activation != domain.ActivationEnabled {
			return domain.Errf(domain.CodeProjectNotEnabled, "project is %s", p.Activation)
		}
		if _, err := tx.ExecContext(ctx, "UPDATE runs SET state='ACTIVE', version=version+1 WHERE id=?", r.ID); err != nil {
			return err
		}
		// Attempts paused by the run pause become eligible for resume.
		if _, err := tx.ExecContext(ctx, "UPDATE execution_attempts SET state_reason='RESUME_REQUESTED', version=version+1 WHERE run_id=? AND state='PAUSED' AND state_reason IN ('RUN_PAUSED','DISABLE_DRAINING')", r.ID); err != nil {
			return err
		}
		tx.Touch(r.ProjectID, sqlite.KindRun, r.ID)
		tx.Touch(r.ProjectID, sqlite.KindProject, r.ProjectID)
		tx.Event(r.ProjectID, "run.resumed", "run", r.ID, r.Version+1)
		return a.touchRunIssues(ctx, tx, r.ProjectID, r.ID)
	})
}

// CancelRun requests cancellation. Live attempts move to CANCEL_REQUESTED
// and become CANCELLED only once the session is confirmed stopped.
func (a *App) CancelRun(ctx context.Context, cmd Cmd, runID string) (*RunCommandResult, error) {
	return a.runCommand(ctx, cmd, "run.cancel", runID, func(tx *sqlite.Tx, r *sqlite.Run) error {
		switch r.State {
		case domain.RunCompleted, domain.RunCancelled, domain.RunFailed:
			return domain.Errf(domain.CodeConflict, "run is %s", r.State)
		}
		atts, err := sqlite.AttemptsWhere(ctx, tx, "run_id=? AND state IN ('PENDING','STARTING','RUNNING','PAUSING','PAUSED')", r.ID)
		if err != nil {
			return err
		}
		for _, att := range atts {
			if err := a.revokeAttemptLease(ctx, tx, att, domain.LeaseRevoked, "RUN_CANCELLED"); err != nil {
				return err
			}
			next := domain.AttemptCancelRequested
			as, _ := sqlite.ActiveAssignmentForAttempt(ctx, tx, att.ID)
			if att.State == domain.AttemptPending || (as == nil && att.State != domain.AttemptRunning) {
				next = domain.AttemptCancelled
			}
			if _, err := tx.ExecContext(ctx, "UPDATE execution_attempts SET state=?, state_reason='RUN_CANCELLED', version=version+1, updated_at=? WHERE id=?", next, tx.Now, att.ID); err != nil {
				return err
			}
			if next == domain.AttemptCancelled {
				if err := a.releaseAttemptBindings(ctx, tx, att); err != nil {
					return err
				}
			}
			tx.Touch(r.ProjectID, sqlite.KindAttempt, att.ID)
		}
		if _, err := tx.ExecContext(ctx, "UPDATE merge_candidates SET state='CANCELLED', state_reason='RUN_CANCELLED', version=version+1, updated_at=? WHERE run_id=? AND state IN ('WAITING','READY','STALE_BASE','CONFLICT','FAILED')", tx.Now, r.ID); err != nil {
			return err
		}
		aps, err := sqlite.ApprovalsWhere(ctx, tx, "run_id=? AND state='PENDING'", r.ID)
		if err != nil {
			return err
		}
		for _, ap := range aps {
			if _, err := tx.ExecContext(ctx, "UPDATE approvals SET state='CANCELLED', state_reason='RUN_CANCELLED', version=version+1 WHERE id=?", ap.ID); err != nil {
				return err
			}
			tx.Touch(r.ProjectID, sqlite.KindApproval, ap.ID)
		}
		if _, err := tx.ExecContext(ctx, "UPDATE runs SET state='CANCELLED', ended_at=?, version=version+1 WHERE id=?", tx.Now, r.ID); err != nil {
			return err
		}
		if err := a.freezeRun(ctx, tx, r.ID); err != nil {
			return err
		}
		tx.Touch(r.ProjectID, sqlite.KindRun, r.ID)
		tx.Touch(r.ProjectID, sqlite.KindProject, r.ProjectID)
		tx.Event(r.ProjectID, "run.cancelled", "run", r.ID, r.Version+1)
		return a.touchRunIssues(ctx, tx, r.ProjectID, r.ID)
	})
}

func (a *App) runCommand(ctx context.Context, cmd Cmd, scope, runID string, fn func(tx *sqlite.Tx, r *sqlite.Run) error) (*RunCommandResult, error) {
	if err := a.requireReady(); err != nil {
		return nil, err
	}
	var out RunCommandResult
	err := a.DB.Write(ctx, func(tx *sqlite.Tx) error {
		return a.idempotent(ctx, tx, scope+":"+runID, cmd, runID, &out, func() (any, error) {
			r, err := sqlite.GetRun(ctx, tx, runID)
			if err != nil {
				return nil, err
			}
			if err := checkIfMatch(cmd, r.Version); err != nil {
				return nil, err
			}
			if err := fn(tx, r); err != nil {
				return nil, err
			}
			if err := tx.Audit(ctx, r.ProjectID, cmd.Principal.ID, scope, "run", r.ID, "", "", nil); err != nil {
				return nil, err
			}
			nr, err := sqlite.GetRun(ctx, tx, runID)
			if err != nil {
				return nil, err
			}
			return &RunCommandResult{RunID: nr.ID, State: nr.State, Version: domain.Dec(nr.Version)}, nil
		})
	})
	if err != nil {
		return nil, err
	}
	a.Wake()
	return &out, nil
}

// freezeRun stores the historical summary of a finished run so later graph
// changes never rewrite the past (contract §8.1).
func (a *App) freezeRun(ctx context.Context, tx *sqlite.Tx, runID string) error {
	r, err := sqlite.GetRun(ctx, tx, runID)
	if err != nil {
		return err
	}
	issues, err := sqlite.IssuesOfRun(ctx, tx, runID)
	if err != nil {
		return err
	}
	type frozenIssue struct {
		ID, ClientKey, Title, Lifecycle, FinalAttemptState, OutputID, SourceSHA, Verification, IntegrationState, EvidenceID string
	}
	var fis []frozenIssue
	for _, i := range issues {
		fi := frozenIssue{ID: i.ID, ClientKey: i.ClientKey, Title: i.Title, Lifecycle: i.Lifecycle}
		if att, _ := sqlite.LatestAttempt(ctx, tx, runID, i.ID); att != nil {
			fi.FinalAttemptState = att.State
		}
		if o, _ := sqlite.CurrentOutput(ctx, tx, runID, i.ID, r.Generation); o != nil {
			fi.OutputID, fi.SourceSHA, fi.Verification = o.ID, o.SourceSHA, o.Verification
			var ev string
			_ = tx.QueryRowContext(ctx, "SELECT id FROM integration_evidence WHERE output_id=? LIMIT 1", o.ID).Scan(&ev)
			fi.EvidenceID = ev
		}
		cs, _ := sqlite.CandidatesWhere(ctx, tx, "run_id=? AND issue_id=?", runID, i.ID)
		if len(cs) > 0 {
			fi.IntegrationState = cs[len(cs)-1].State
		}
		fis = append(fis, fi)
	}
	edges, _ := sqlite.EdgesOfRun(ctx, tx, runID)
	type frozenEdge struct{ Predecessor, Successor, Milestone, Phase, TargetRef string }
	var fes []frozenEdge
	for _, e := range edges {
		fes = append(fes, frozenEdge{e.Predecessor, e.Successor, e.Milestone, e.Phase, e.TargetRef})
	}
	b, _ := json.Marshal(map[string]any{"issues": fis, "dependencies": fes, "final_state": r.State, "final_version": r.Version})
	_, err = tx.ExecContext(ctx, "UPDATE runs SET final_summary=? WHERE id=?", string(b), runID)
	return err
}

// maybeCompleteRun completes a run when every issue is COMPLETED.
func (a *App) maybeCompleteRun(ctx context.Context, tx *sqlite.Tx, runID string) error {
	r, err := sqlite.GetRun(ctx, tx, runID)
	if err != nil || (r.State != domain.RunActive && r.State != domain.RunPaused) {
		return err
	}
	var open int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM issues i JOIN run_issues ri ON ri.issue_id=i.id WHERE ri.run_id=? AND i.lifecycle <> 'COMPLETED'", runID).Scan(&open); err != nil {
		return err
	}
	if open > 0 {
		return nil
	}
	if _, err := tx.ExecContext(ctx, "UPDATE runs SET state='COMPLETED', ended_at=?, version=version+1 WHERE id=?", tx.Now, runID); err != nil {
		return err
	}
	if err := a.freezeRun(ctx, tx, runID); err != nil {
		return err
	}
	tx.Touch(r.ProjectID, sqlite.KindRun, runID)
	tx.Touch(r.ProjectID, sqlite.KindProject, r.ProjectID)
	tx.Event(r.ProjectID, "run.completed", "run", runID, r.Version+1)
	return nil
}

// supports reports whether a backend kind advertises a capability.
func (a *App) supports(ctx context.Context, kind string, pick func(backend.Capabilities) backend.Capability) bool {
	if kind == "" {
		return false
	}
	r := a.Backends.Probe(ctx, kind)
	return pick(r.Capabilities).Usable()
}
