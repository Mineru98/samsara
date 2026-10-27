package app

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/Mineru98/samsara/runtime/internal/backend"
	"github.com/Mineru98/samsara/runtime/internal/config"
	"github.com/Mineru98/samsara/runtime/internal/domain"
	"github.com/Mineru98/samsara/runtime/internal/store/sqlite"
)

type schedCandidate struct {
	run      *sqlite.Run
	project  *sqlite.Project
	issue    *sqlite.Issue
	existing *sqlite.Attempt // PENDING/QUEUED attempt to start, or nil
	reuseWT  string
}

// ScheduleTick evaluates ACTIVE runs and starts ready work within project
// and host quotas (01 §12.3). Approval waits block only the dependent
// closure: independent issues keep running (INV-06).
func (a *App) ScheduleTick(ctx context.Context) error {
	if !a.Ready() {
		return nil
	}
	var cands []schedCandidate
	running := map[string]int{}
	hostRunning := 0
	var paused []*sqlite.Attempt
	err := a.DB.Read(ctx, func(q sqlite.Q) error {
		rows, err := q.QueryContext(ctx, "SELECT project_id, COUNT(*) FROM execution_attempts WHERE state IN ('STARTING','RUNNING','PAUSING') GROUP BY project_id")
		if err != nil {
			return err
		}
		for rows.Next() {
			var pid string
			var n int
			if err := rows.Scan(&pid, &n); err != nil {
				rows.Close()
				return err
			}
			running[pid] = n
			hostRunning += n
		}
		rows.Close()
		runs, err := sqlite.RunsInState(ctx, q, domain.RunActive)
		if err != nil {
			return err
		}
		for _, r := range runs {
			p, err := sqlite.GetProject(ctx, q, r.ProjectID)
			if err != nil {
				return err
			}
			if p.Activation != domain.ActivationEnabled || p.RegistryState != domain.RegistryPresent {
				continue
			}
			issues, err := sqlite.IssuesOfRun(ctx, q, r.ID)
			if err != nil {
				return err
			}
			for _, is := range issues {
				if is.Lifecycle != domain.IssueOpen {
					continue
				}
				att, err := sqlite.LatestAttempt(ctx, q, r.ID, is.ID)
				if err != nil {
					return err
				}
				if att != nil && !restartablePending(att) {
					if att.State == domain.AttemptPaused && (att.StateReason == "DEPENDENCY_INVALIDATED" || att.StateReason == "RESUME_REQUESTED") {
						paused = append(paused, att)
					}
					continue
				}
				bl, err := sqlite.ExecutionBlockers(ctx, q, r.ID, is.ID)
				if err != nil {
					return err
				}
				if len(bl) > 0 {
					continue
				}
				c := schedCandidate{run: r, project: p, issue: is, existing: att}
				if att != nil {
					c.reuseWT = att.WorktreeID
				}
				cands = append(cands, c)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	// Fair order: projects with fewer running workers first, then FIFO.
	sort.SliceStable(cands, func(i, j int) bool {
		return running[cands[i].project.ID] < running[cands[j].project.ID]
	})
	for _, c := range cands {
		cfg := projectConfig(c.project)
		if hostRunning >= HostMaxWorkers || running[c.project.ID] >= cfg.Execution.MaxWorkers {
			continue
		}
		var caps []string
		_ = json.Unmarshal([]byte(c.issue.RequiredCapabilities), &caps)
		dec, rerr := a.Backends.ResolveForLaunch(ctx, backend.Policy{Mode: cfg.Backend.Mode, Order: cfg.Backend.Order, AllowFallback: cfg.Backend.AllowFallback}, caps)
		claude := a.Claude()
		started, err := a.startAttempt(ctx, c, cfg, dec, rerr, claude)
		if err != nil {
			a.Log.Warn("schedule", "issue", c.issue.ID, "err", err)
			continue
		}
		if started {
			running[c.project.ID]++
			hostRunning++
		}
	}
	for _, att := range paused {
		if err := a.tryResumeAttempt(ctx, att); err != nil {
			a.Log.Warn("resume", "attempt", att.ID, "err", err)
		}
	}
	return nil
}

// restartablePending reports whether a PENDING attempt waits for the
// scheduler (queued, or blocked by an environment condition that may clear).
func restartablePending(att *sqlite.Attempt) bool {
	if att.State != domain.AttemptPending {
		return false
	}
	switch att.StateReason {
	case "QUEUED", "BACKEND_UNAVAILABLE", "POLICY_GATE_UNSUPPORTED_CLAUDE_VERSION", "GIT_REPOSITORY_REQUIRED":
		return true
	}
	return false
}

var slugRe = regexp.MustCompile(`[^a-z0-9-]+`)

func slug(s string, max int) string {
	s = slugRe.ReplaceAllString(strings.ToLower(s), "-")
	s = strings.Trim(s, "-")
	if len(s) > max {
		s = strings.Trim(s[:max], "-")
	}
	if s == "" {
		s = "x"
	}
	return s
}

func shortID(id string) string {
	if i := strings.IndexByte(id, '_'); i >= 0 && len(id) > i+9 {
		return id[i+1 : i+9]
	}
	return id
}

// startAttempt creates (or advances) the attempt and its worktree intent in
// one transaction. Worker creation itself happens in the outbox.
func (a *App) startAttempt(ctx context.Context, c schedCandidate, cfg *config.Config, dec backend.Decision, resolveErr error, claude ClaudeSupport) (bool, error) {
	started := false
	err := a.DB.Write(ctx, func(tx *sqlite.Tx) error {
		// Re-check inside the writer: state may have changed since the read.
		r, err := sqlite.GetRun(ctx, tx, c.run.ID)
		if err != nil || r.State != domain.RunActive {
			return err
		}
		latest, err := sqlite.LatestAttempt(ctx, tx, r.ID, c.issue.ID)
		if err != nil {
			return err
		}
		if latest != nil && (c.existing == nil || latest.ID != c.existing.ID || latest.State != domain.AttemptPending) {
			return nil
		}
		bl, err := sqlite.ExecutionBlockers(ctx, tx, r.ID, c.issue.ID)
		if err != nil || len(bl) > 0 {
			return err
		}
		repo, err := sqlite.RepositoryOfProject(ctx, tx, c.project.ID)
		if err != nil {
			return err
		}
		att := c.existing
		if att == nil {
			att = &sqlite.Attempt{ID: domain.NewID("att"), ProjectID: c.project.ID, RunID: r.ID, IssueID: c.issue.ID, RunGeneration: r.Generation}
			if _, err := tx.ExecContext(ctx, `INSERT INTO execution_attempts(id,project_id,run_id,issue_id,run_generation,state,state_reason,created_at,updated_at)
				VALUES(?,?,?,?,?,'PENDING','QUEUED',?,?)`, att.ID, att.ProjectID, att.RunID, att.IssueID, att.RunGeneration, tx.Now, tx.Now); err != nil {
				return err
			}
		}
		state, reason := domain.AttemptStarting, ""
		switch {
		case resolveErr != nil:
			state, reason = domain.AttemptPending, "BACKEND_UNAVAILABLE"
		case dec.Manual:
			state, reason = domain.AttemptPending, "MANUAL_ATTACH_REQUIRED"
		case a.RequireHookReadiness && !claude.BlockOnFailure:
			state, reason = domain.AttemptPending, "POLICY_GATE_UNSUPPORTED_CLAUDE_VERSION"
		}
		if repo == nil {
			state, reason = domain.AttemptPending, "GIT_REPOSITORY_REQUIRED"
		}
		if c.existing != nil && state == c.existing.State && reason == c.existing.StateReason {
			return nil // unchanged blocked condition: no churn
		}
		decJSON, _ := json.Marshal(dec)
		if _, err := tx.ExecContext(ctx, "UPDATE execution_attempts SET state=?, state_reason=?, backend_kind=?, backend_reason=?, version=version+1, updated_at=? WHERE id=?",
			state, reason, dec.Selected, string(decJSON), tx.Now, att.ID); err != nil {
			return err
		}
		tx.Touch(c.project.ID, sqlite.KindAttempt, att.ID)
		tx.Touch(c.project.ID, sqlite.KindIssue, c.issue.ID)
		if repo == nil || reason == "BACKEND_UNAVAILABLE" || reason == "POLICY_GATE_UNSUPPORTED_CLAUDE_VERSION" {
			tx.Event(c.project.ID, "attempt.blocked", "attempt", att.ID, att.Version+1)
			return nil
		}
		if c.reuseWT == "" {
			wtID := domain.NewID("wt")
			path := filepath.Join(a.Layout.WorktreeRoot, shortID(c.project.ID), att.ID)
			branch := fmt.Sprintf("refs/heads/%s/%s/%s/%s", cfg.Git.BranchPrefix, shortID(c.project.ID), slug(c.issue.ClientKey, 40), shortID(att.ID))
			if err := config.ValidateFullRef(branch); err != nil {
				return domain.Errf(domain.CodeInvalidRequest, "branch name: %v", err)
			}
			if _, err := tx.ExecContext(ctx, "INSERT INTO worktrees(id,project_id,repository_id,attempt_id,path,branch_ref,ownership,state,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)",
				wtID, c.project.ID, repo.ID, att.ID, path, branch, domain.OwnershipManaged, domain.WorktreeCreating, tx.Now, tx.Now); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, "UPDATE execution_attempts SET worktree_id=? WHERE id=?", wtID, att.ID); err != nil {
				return err
			}
			if _, err := tx.Enqueue(ctx, sqlite.OutboxItem{ProjectID: c.project.ID, Kind: "worktree.create", Payload: map[string]string{"attempt_id": att.ID, "worktree_id": wtID},
				DedupeKey: "worktree.create:" + wtID, ResourceKey: "worktree:" + wtID}); err != nil {
				return err
			}
			tx.Touch(c.project.ID, sqlite.KindWorktree, wtID)
		} else {
			if _, err := tx.ExecContext(ctx, "UPDATE worktrees SET attempt_id=?, state='READY', version=version+1, updated_at=? WHERE id=?", att.ID, tx.Now, c.reuseWT); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, "UPDATE execution_attempts SET worktree_id=? WHERE id=?", c.reuseWT, att.ID); err != nil {
				return err
			}
			if state == domain.AttemptStarting {
				if err := a.enqueueLaunch(ctx, tx, att.ID); err != nil {
					return err
				}
			}
			tx.Touch(c.project.ID, sqlite.KindWorktree, c.reuseWT)
		}
		tx.Event(c.project.ID, "attempt.scheduled", "attempt", att.ID, att.Version+1)
		started = state == domain.AttemptStarting
		return tx.Audit(ctx, c.project.ID, "scheduler", "attempt.schedule", "attempt", att.ID, state, reason, map[string]any{"backend": dec.Selected, "rejected": dec.Rejected})
	})
	return started, err
}

// enqueueLaunch issues a one-time launch ticket and queues worker creation.
func (a *App) enqueueLaunch(ctx context.Context, tx *sqlite.Tx, attemptID string) error {
	ticket := domain.NewSecret()
	if _, err := tx.ExecContext(ctx, "INSERT INTO launch_tickets(ticket_hash,attempt_id,project_id,issued_at,expires_at) SELECT ?, id, project_id, ?, ? FROM execution_attempts WHERE id=?",
		domain.HashSecret(ticket), tx.Now, tx.Now+15*60*1000, attemptID); err != nil {
		return err
	}
	var pid string
	if err := tx.QueryRowContext(ctx, "SELECT project_id FROM execution_attempts WHERE id=?", attemptID).Scan(&pid); err != nil {
		return err
	}
	var n int
	_ = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM launch_tickets WHERE attempt_id=?", attemptID).Scan(&n)
	_, err := tx.Enqueue(ctx, sqlite.OutboxItem{ProjectID: pid, Kind: "worker.launch", Payload: map[string]string{"attempt_id": attemptID, "ticket": ticket},
		DedupeKey: fmt.Sprintf("worker.launch:%s:%d", attemptID, n), ResourceKey: "attempt:" + attemptID, Correlation: attemptID})
	return err
}

// tryResumeAttempt resumes a PAUSED attempt when its prerequisites hold
// again (POL-03). A live bound session gets a fresh lease (new fence) and
// resume_required; an exited session needs a relaunch or a manual attach.
func (a *App) tryResumeAttempt(ctx context.Context, att *sqlite.Attempt) error {
	var binding *sqlite.Binding
	_ = a.DB.Read(ctx, func(q sqlite.Q) error {
		binding, _ = sqlite.CurrentBindingForAttempt(ctx, q, att.ID)
		return nil
	})
	alive := false
	if binding != nil {
		if obs, err := a.observeBinding(ctx, binding); err == nil && obs.Liveness == domain.LivenessAlive && (obs.Generation == "" || obs.Generation == binding.Generation) {
			alive = true
		}
	}
	canPrompt := binding != nil && a.supports(ctx, binding.Kind, func(c backend.Capabilities) backend.Capability { return c.SubmitPrompt })
	canCreate := a.supports(ctx, att.BackendKind, func(c backend.Capabilities) backend.Capability { return c.CreateWorker })
	err := a.DB.Write(ctx, func(tx *sqlite.Tx) error {
		cur, err := sqlite.GetAttempt(ctx, tx, att.ID)
		if err != nil || cur.State != domain.AttemptPaused {
			return err
		}
		p, err := sqlite.GetProject(ctx, tx, cur.ProjectID)
		if err != nil {
			return err
		}
		if a.policyBlock(ctx, tx, p, cur) != "" {
			return nil
		}
		as, err := sqlite.ActiveAssignmentForAttempt(ctx, tx, cur.ID)
		if err != nil {
			return err
		}
		if as != nil && alive {
			cfg := projectConfig(p)
			if _, err := a.issueLease(ctx, tx, cur, as.SessionID, as.BindingID, a.leaseTTL(cfg).Milliseconds()); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, "UPDATE execution_attempts SET state='RUNNING', state_reason='RESUMED', resume_required=1, quarantine_reason='', version=version+1, updated_at=? WHERE id=?", tx.Now, cur.ID); err != nil {
				return err
			}
			tx.Touch(p.ID, sqlite.KindAttempt, cur.ID)
			tx.Touch(p.ID, sqlite.KindIssue, cur.IssueID)
			tx.Event(p.ID, "attempt.resumed", "attempt", cur.ID, cur.Version+1)
			s, err := sqlite.GetSession(ctx, tx, as.SessionID)
			if err != nil {
				return err
			}
			// Automated input only when the binding supports it, the session
			// is idle, and automation still owns input (01 §7.3).
			if canPrompt && s.InputOwner == "automation" && s.InputState == domain.InputReady {
				return a.enqueuePrompt(ctx, tx, cur, s.ID, "resume", "삼사라: 선행 조건이 다시 충족되었습니다. 중단된 작업을 이어서 진행하세요.")
			}
			return nil
		}
		if as == nil || binding == nil {
			if canCreate {
				if _, err := tx.ExecContext(ctx, "UPDATE execution_attempts SET state='STARTING', state_reason='RELAUNCH', version=version+1, updated_at=? WHERE id=?", tx.Now, cur.ID); err != nil {
					return err
				}
				tx.Touch(p.ID, sqlite.KindAttempt, cur.ID)
				tx.Touch(p.ID, sqlite.KindIssue, cur.IssueID)
				return a.enqueueLaunch(ctx, tx, cur.ID)
			}
			if _, err := tx.ExecContext(ctx, "UPDATE execution_attempts SET state_reason='MANUAL_ATTACH_REQUIRED', resume_required=1, version=version+1, updated_at=? WHERE id=?", tx.Now, cur.ID); err != nil {
				return err
			}
			tx.Touch(p.ID, sqlite.KindAttempt, cur.ID)
			tx.Touch(p.ID, sqlite.KindIssue, cur.IssueID)
		}
		return nil
	})
	return err
}

// enqueuePrompt records a dispatch and queues the automated input.
func (a *App) enqueuePrompt(ctx context.Context, tx *sqlite.Tx, att *sqlite.Attempt, sessionID, purpose, text string) error {
	dispatchID := domain.NewID("dsp")
	opID, err := tx.Enqueue(ctx, sqlite.OutboxItem{ProjectID: att.ProjectID, Kind: "prompt.submit",
		Payload:   map[string]string{"attempt_id": att.ID, "session_id": sessionID, "dispatch_id": dispatchID, "text": text + " [samsara-dispatch:" + dispatchID + "]"},
		DedupeKey: "prompt:" + dispatchID, ResourceKey: "attempt:" + att.ID})
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO dispatches(id,attempt_id,session_id,operation_id,purpose,state,created_at) VALUES(?,?,?,?,?,'QUEUED',?)",
		dispatchID, att.ID, sessionID, opID, purpose, tx.Now)
	return err
}

// RetryIssue queues a new attempt after a FAILED, LOST or CANCELLED one. A
// retained worktree is reused only when the previous session is confirmed
// gone (R-04); otherwise a fresh worktree is created.
func (a *App) RetryIssue(ctx context.Context, cmd Cmd, issueID string) (*sqlite.AttemptView, error) {
	var attID string
	err := a.DB.Write(ctx, func(tx *sqlite.Tx) error {
		var runID string
		if err := tx.QueryRowContext(ctx, "SELECT r.id FROM runs r JOIN run_issues ri ON ri.run_id=r.id WHERE ri.issue_id=? AND r.state IN ('ACTIVE','PAUSED') ORDER BY r.created_at DESC LIMIT 1", issueID).Scan(&runID); err != nil {
			return domain.Errf(domain.CodeConflict, "issue has no ACTIVE or PAUSED run")
		}
		r, err := sqlite.GetRun(ctx, tx, runID)
		if err != nil {
			return err
		}
		prev, err := sqlite.LatestAttempt(ctx, tx, runID, issueID)
		if err != nil {
			return err
		}
		if prev == nil || !domain.AttemptTerminal(prev.State) {
			return domain.Errf(domain.CodeConflict, "retry requires a FAILED, LOST or CANCELLED latest attempt")
		}
		if prev.QuarantineReason != "" {
			return domain.Errf(domain.CodeConflict, "previous attempt is quarantined (%s); resolve it first", prev.QuarantineReason)
		}
		reuse := ""
		if prev.WorktreeID != "" {
			w, err := sqlite.GetWorktree(ctx, tx, prev.WorktreeID)
			if err != nil {
				return err
			}
			var live int
			_ = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM session_assignments sa JOIN sessions s ON s.id=sa.session_id WHERE sa.attempt_id=? AND s.liveness<>'EXITED'", prev.ID).Scan(&live)
			if w.State == domain.WorktreeRetained && live == 0 {
				reuse = w.ID
			}
		}
		attID = domain.NewID("att")
		var wt any
		if reuse != "" {
			wt = reuse
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO execution_attempts(id,project_id,run_id,issue_id,run_generation,worktree_id,state,state_reason,created_at,updated_at)
			VALUES(?,?,?,?,?,?,'PENDING','QUEUED',?,?)`, attID, r.ProjectID, r.ID, issueID, r.Generation, wt, tx.Now, tx.Now); err != nil {
			return err
		}
		tx.Touch(r.ProjectID, sqlite.KindAttempt, attID)
		tx.Touch(r.ProjectID, sqlite.KindIssue, issueID)
		tx.Event(r.ProjectID, "attempt.retry_queued", "attempt", attID, 1)
		return tx.Audit(ctx, r.ProjectID, cmd.Principal.ID, "issue.retry", "issue", issueID, "", "", map[string]string{"previous_attempt": prev.ID, "reuse_worktree": reuse})
	})
	if err != nil {
		return nil, err
	}
	a.Wake()
	var v *sqlite.AttemptView
	err = a.DB.Read(ctx, func(q sqlite.Q) error {
		v, err = sqlite.AttemptProjection(ctx, q, attID)
		return err
	})
	return v, err
}
