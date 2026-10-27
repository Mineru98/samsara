package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Mineru98/samsara/runtime/internal/backend"
	"github.com/Mineru98/samsara/runtime/internal/domain"
	"github.com/Mineru98/samsara/runtime/internal/gitops"
	"github.com/Mineru98/samsara/runtime/internal/hooks"
	"github.com/Mineru98/samsara/runtime/internal/store/sqlite"
)

// outcome of one outbox handler run.
type outcome struct {
	status string        // DONE | RETRY | FAILED | UNKNOWN
	delay  time.Duration // for RETRY
	err    string
	result any
	// finish runs in the finalising transaction together with the outbox
	// row update so domain state and intent status commit atomically.
	finish func(tx *sqlite.Tx) error
}

func done(result any, finish func(tx *sqlite.Tx) error) outcome {
	return outcome{status: "DONE", result: result, finish: finish}
}
func retry(d time.Duration, err error) outcome {
	return outcome{status: "RETRY", delay: d, err: err.Error()}
}
func failed(err string, finish func(tx *sqlite.Tx) error) outcome {
	return outcome{status: "FAILED", err: err, finish: finish}
}
func unknown(err string, finish func(tx *sqlite.Tx) error) outcome {
	return outcome{status: "UNKNOWN", err: err, finish: finish}
}

type outboxRow struct {
	ID, OperationID, ProjectID, Kind, Payload string
	AttemptCount                              int
}

// resendSafe lists intents whose handler first inspects the external state,
// so re-running after an unknown outcome cannot duplicate the effect
// (14.2). prompt.submit and session.interrupt are never resent.
var resendSafe = map[string]bool{
	"worktree.create": true, "worker.launch": true, "output.verify": true, "merge.validate": true, "git.publish": true,
}

const maxOutboxAttempts = 8

// DispatchOutbox runs due intents. External calls happen outside any DB
// transaction (R-06).
func (a *App) DispatchOutbox(ctx context.Context) error {
	if !a.Ready() {
		return nil
	}
	var rows []outboxRow
	err := a.DB.Read(ctx, func(q sqlite.Q) error {
		rs, err := q.QueryContext(ctx, "SELECT id, operation_id, COALESCE(project_id,''), kind, payload, attempt_count FROM outbox WHERE state='PENDING' AND retry_at <= ? ORDER BY created_at, id LIMIT 32", a.nowMillis())
		if err != nil {
			return err
		}
		defer rs.Close()
		for rs.Next() {
			var r outboxRow
			if err := rs.Scan(&r.ID, &r.OperationID, &r.ProjectID, &r.Kind, &r.Payload, &r.AttemptCount); err != nil {
				return err
			}
			rows = append(rows, r)
		}
		return rs.Err()
	})
	if err != nil {
		return err
	}
	for _, r := range rows {
		claimed := false
		if err := a.DB.Write(ctx, func(tx *sqlite.Tx) error {
			res, err := tx.ExecContext(ctx, "UPDATE outbox SET state='RUNNING', attempt_count=attempt_count+1, updated_at=? WHERE id=? AND state='PENDING'", tx.Now, r.ID)
			if err != nil {
				return err
			}
			n, _ := res.RowsAffected()
			claimed = n == 1
			if claimed {
				_, err = tx.ExecContext(ctx, "UPDATE operations SET state='RUNNING', updated_at=? WHERE id=?", tx.Now, r.OperationID)
			}
			return err
		}); err != nil || !claimed {
			continue
		}
		o := a.runHandler(ctx, r)
		if err := a.finishOutbox(ctx, r, o); err != nil {
			a.Log.Error("outbox finish", "id", r.ID, "kind", r.Kind, "err", err)
		}
	}
	return nil
}

func (a *App) runHandler(ctx context.Context, r outboxRow) (o outcome) {
	defer func() {
		if p := recover(); p != nil {
			o = unknown(fmt.Sprintf("handler panic: %v", p), nil)
		}
	}()
	var payload map[string]string
	_ = json.Unmarshal([]byte(r.Payload), &payload)
	switch r.Kind {
	case "worktree.create":
		return a.handleWorktreeCreate(ctx, payload)
	case "worker.launch":
		return a.handleWorkerLaunch(ctx, r, payload)
	case "prompt.submit":
		return a.handlePromptSubmit(ctx, r, payload)
	case "session.interrupt":
		return a.handleInterrupt(ctx, payload)
	case "output.verify":
		return a.handleVerify(ctx, payload)
	case "merge.validate":
		return a.handleMergeValidate(ctx, payload)
	case "git.publish":
		return a.handlePublish(ctx, payload)
	}
	return failed("unknown outbox kind "+r.Kind, nil)
}

func (a *App) finishOutbox(ctx context.Context, r outboxRow, o outcome) error {
	return a.DB.Write(ctx, func(tx *sqlite.Tx) error {
		state, opState := "DONE", domain.OpSucceeded
		retryAt := tx.Now
		switch o.status {
		case "RETRY":
			if r.AttemptCount+1 >= maxOutboxAttempts {
				state, opState = "FAILED", domain.OpFailed
			} else {
				state, opState = "PENDING", domain.OpPending
				retryAt = tx.Now + o.delay.Milliseconds()
			}
		case "FAILED":
			state, opState = "FAILED", domain.OpFailed
		case "UNKNOWN":
			state, opState = "UNKNOWN", domain.OpUnknown
		}
		if o.finish != nil && (o.status != "RETRY" || state == "FAILED") {
			if err := o.finish(tx); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, "UPDATE outbox SET state=?, retry_at=?, last_error=?, updated_at=? WHERE id=?", state, retryAt, o.err, tx.Now, r.ID); err != nil {
			return err
		}
		res, _ := json.Marshal(o.result)
		if o.result == nil {
			res = []byte("{}")
		}
		if _, err := tx.ExecContext(ctx, "UPDATE operations SET state=?, result=?, error=?, updated_at=? WHERE id=?", opState, string(res), o.err, tx.Now, r.OperationID); err != nil {
			return err
		}
		if r.ProjectID != "" {
			tx.Touch(r.ProjectID, sqlite.KindOperation, r.OperationID)
		}
		return nil
	})
}

// --- worktree.create -------------------------------------------------------

func (a *App) handleWorktreeCreate(ctx context.Context, p map[string]string) outcome {
	var w *sqlite.Worktree
	var att *sqlite.Attempt
	var repo *sqlite.Repository
	var issue *sqlite.Issue
	err := a.DB.Read(ctx, func(q sqlite.Q) error {
		var err error
		if w, err = sqlite.GetWorktree(ctx, q, p["worktree_id"]); err != nil {
			return err
		}
		if att, err = sqlite.GetAttempt(ctx, q, p["attempt_id"]); err != nil {
			return err
		}
		if issue, err = sqlite.GetIssue(ctx, q, att.IssueID); err != nil {
			return err
		}
		repo, err = sqlite.GetRepository(ctx, q, w.RepositoryID)
		return err
	})
	if err != nil {
		return failed(err.Error(), nil)
	}
	if w.State != domain.WorktreeCreating {
		return done(map[string]string{"state": w.State}, nil)
	}
	repoDir := repo.CommonDir
	if strings.HasSuffix(repoDir, "/.git") {
		repoDir = strings.TrimSuffix(repoDir, "/.git")
	}
	failWT := func(reason string) func(tx *sqlite.Tx) error {
		return func(tx *sqlite.Tx) error {
			if _, err := tx.ExecContext(ctx, "UPDATE worktrees SET state='QUARANTINED', state_reason=?, version=version+1, updated_at=? WHERE id=?", reason, tx.Now, w.ID); err != nil {
				return err
			}
			cur, err := sqlite.GetAttempt(ctx, tx, att.ID)
			if err != nil {
				return err
			}
			tx.Touch(w.ProjectID, sqlite.KindWorktree, w.ID)
			if cur.State == domain.AttemptStarting || cur.State == domain.AttemptPending {
				return a.setAttemptState(ctx, tx, cur, domain.AttemptFailed, reason)
			}
			return nil
		}
	}
	base, err := a.Git.ResolveRef(ctx, repoDir, issue.TargetRef)
	if err != nil {
		return retry(10*time.Second, err)
	}
	if base == "" {
		return failed("target ref "+issue.TargetRef+" does not exist", failWT("TARGET_REF_MISSING"))
	}
	// Adopt a worktree created before a crash only if git lists it at the
	// same path with the expected branch (04 §6.2).
	list, err := a.Git.ListWorktrees(ctx, repoDir)
	if err != nil {
		return retry(10*time.Second, err)
	}
	exists := false
	for _, li := range list {
		if li.Path == w.Path {
			if li.Branch != w.BranchRef {
				return failed("path exists with another branch", failWT(domain.CodeOwnershipConflict))
			}
			exists = true
			if b, err := a.Git.RevParse(ctx, w.Path, "HEAD"); err == nil {
				base = b
			}
		}
	}
	if !exists {
		if _, err := os.Stat(w.Path); err == nil {
			return failed("path exists outside git worktree list", failWT(domain.CodeOwnershipConflict))
		}
		if err := os.MkdirAll(dirOf(w.Path), 0o700); err != nil {
			return retry(10*time.Second, err)
		}
		if err := a.Git.AddWorktree(ctx, repoDir, w.Path, strings.TrimPrefix(w.BranchRef, "refs/heads/"), base); err != nil {
			return failed(err.Error(), failWT("WORKTREE_ADD_FAILED"))
		}
	}
	if a.SelfExe != "" {
		if err := hooks.Install(w.Path, a.SelfExe); err != nil {
			return failed(err.Error(), failWT("HOOK_INSTALL_FAILED"))
		}
	}
	return done(map[string]string{"path": w.Path, "base_sha": base}, func(tx *sqlite.Tx) error {
		if _, err := tx.ExecContext(ctx, "UPDATE worktrees SET state='READY', base_sha=?, version=version+1, updated_at=? WHERE id=? AND state='CREATING'", base, tx.Now, w.ID); err != nil {
			return err
		}
		tx.Touch(w.ProjectID, sqlite.KindWorktree, w.ID)
		tx.Event(w.ProjectID, "worktree.ready", "worktree", w.ID, w.Version+1)
		cur, err := sqlite.GetAttempt(ctx, tx, att.ID)
		if err != nil {
			return err
		}
		tx.Touch(w.ProjectID, sqlite.KindIssue, cur.IssueID)
		if cur.State == domain.AttemptStarting {
			return a.enqueueLaunch(ctx, tx, cur.ID)
		}
		return nil
	})
}

func dirOf(p string) string {
	i := strings.LastIndexByte(p, '/')
	if i <= 0 {
		return "/"
	}
	return p[:i]
}

// --- worker.launch ---------------------------------------------------------

func (a *App) handleWorkerLaunch(ctx context.Context, r outboxRow, p map[string]string) outcome {
	var att *sqlite.Attempt
	var w *sqlite.Worktree
	var issue *sqlite.Issue
	var proj *sqlite.Project
	err := a.DB.Read(ctx, func(q sqlite.Q) error {
		var err error
		if att, err = sqlite.GetAttempt(ctx, q, p["attempt_id"]); err != nil {
			return err
		}
		if w, err = sqlite.GetWorktree(ctx, q, att.WorktreeID); err != nil {
			return err
		}
		if issue, err = sqlite.GetIssue(ctx, q, att.IssueID); err != nil {
			return err
		}
		proj, err = sqlite.GetProject(ctx, q, att.ProjectID)
		return err
	})
	if err != nil {
		return failed(err.Error(), nil)
	}
	be, ok := a.Backends.Get(att.BackendKind)
	launcher, ok2 := be.(backend.WorkerLauncher)
	if !ok || !ok2 {
		return failed("backend "+att.BackendKind+" cannot launch", a.failAttempt(att.ID, "BACKEND_CANNOT_LAUNCH"))
	}
	if att.State != domain.AttemptStarting {
		// The worker may have registered (and even finished) before the
		// launch receipt was recorded: adopt the location, never relaunch.
		ref, err := launcher.Lookup(ctx, att.ID)
		if err != nil {
			return retry(10*time.Second, err)
		}
		if ref == nil {
			return done(map[string]string{"skipped": "attempt " + att.State}, scrubTicket(r.ID))
		}
		rr := *ref
		return done(map[string]any{"adopted": rr.Fields}, func(tx *sqlite.Tx) error {
			if err := scrubTicket(r.ID)(tx); err != nil {
				return err
			}
			return a.recordBinding(ctx, tx, att, rr)
		})
	}
	cfg := projectConfig(proj)
	prompt := workerPrompt(issue, att)
	argv := []string{a.SelfExe, "launcher", "--"}
	argv = append(argv, cfg.Execution.WorkerCommand...)
	argv = append(argv, prompt)
	env := map[string]string{"SAMSARA_LAUNCH_TICKET": p["ticket"], "SAMSARA_MANAGED": "1", "SAMSARA_ATTEMPT_ID": att.ID,
		"SAMSARA_HEARTBEAT_SECONDS": domain.Dec(int64(cfg.Execution.HeartbeatSeconds))}
	if h := os.Getenv("SAMSARA_HOME"); h != "" {
		env["SAMSARA_HOME"] = h
	}
	spec := backend.LaunchSpec{OperationID: r.OperationID, AttemptID: att.ID, ProjectID: att.ProjectID, Dir: w.Path, Argv: argv, Env: env, Correlation: att.ID}
	rc, err := launcher.Launch(ctx, spec)
	if err != nil {
		rc = backend.LaunchReceipt{Outcome: backend.Ambiguous, Detail: err.Error()}
	}
	switch rc.Outcome {
	case backend.Delivered:
		ref := *rc.Ref
		return done(map[string]any{"ref": ref.Fields, "generation": ref.Generation}, func(tx *sqlite.Tx) error {
			if err := scrubTicket(r.ID)(tx); err != nil {
				return err
			}
			if err := a.recordBinding(ctx, tx, att, ref); err != nil {
				return err
			}
			tx.Touch(att.ProjectID, sqlite.KindAttempt, att.ID)
			tx.Event(att.ProjectID, "worker.launched", "attempt", att.ID, att.Version)
			return tx.Audit(ctx, att.ProjectID, "runtime", "worker.launch", "attempt", att.ID, string(rc.Outcome), "", map[string]any{"backend": ref.Kind, "ref": ref.Fields})
		})
	case backend.NotSent:
		return retry(15*time.Second, errors.New(rc.Detail))
	case backend.NotSupported, backend.PolicySkipped:
		return failed(rc.Detail, a.failAttempt(att.ID, "LAUNCH_"+string(rc.Outcome)))
	default:
		// Unknown: never relaunch blindly. Reconcile resolves it via Lookup.
		return unknown(rc.Detail, nil)
	}
}

// recordBinding stores the backend location of an attempt's worker and links
// it to the session/assignment/lease if the worker already registered.
func (a *App) recordBinding(ctx context.Context, tx *sqlite.Tx, att *sqlite.Attempt, ref backend.Ref) error {
	var id string
	_ = tx.QueryRowContext(ctx, "SELECT id FROM backend_bindings WHERE kind=? AND instance=? AND ref_key=? AND generation=?", ref.Kind, ref.Instance, ref.Key(), ref.Generation).Scan(&id)
	if id == "" {
		if _, err := tx.ExecContext(ctx, "UPDATE backend_bindings SET superseded_at=? WHERE attempt_id=? AND superseded_at IS NULL", tx.Now, att.ID); err != nil {
			return err
		}
		id = domain.NewID("bnd")
		if _, err := tx.ExecContext(ctx, "INSERT INTO backend_bindings(id,attempt_id,kind,instance,opaque_ref,ref_key,generation,created_at) VALUES(?,?,?,?,?,?,?,?)",
			id, att.ID, ref.Kind, ref.Instance, ref.JSON(), ref.Key(), ref.Generation, tx.Now); err != nil {
			return err
		}
	}
	var sessionID string
	_ = tx.QueryRowContext(ctx, "SELECT session_id FROM session_assignments WHERE attempt_id=? ORDER BY attached_at DESC LIMIT 1", att.ID).Scan(&sessionID)
	if sessionID == "" {
		return nil
	}
	if _, err := tx.ExecContext(ctx, "UPDATE backend_bindings SET session_id=? WHERE id=? AND session_id IS NULL", sessionID, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE session_assignments SET binding_id=? WHERE attempt_id=? AND binding_id IS NULL", id, att.ID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE leases SET binding_id=? WHERE owner_attempt_id=? AND binding_id IS NULL", id, att.ID); err != nil {
		return err
	}
	tx.Touch(att.ProjectID, sqlite.KindSession, sessionID)
	return nil
}

func scrubTicket(outboxID string) func(tx *sqlite.Tx) error {
	return func(tx *sqlite.Tx) error {
		_, err := tx.ExecContext(context.Background(), "UPDATE outbox SET payload=json_remove(payload,'$.ticket') WHERE id=?", outboxID)
		return err
	}
}

func (a *App) failAttempt(attemptID, reason string) func(tx *sqlite.Tx) error {
	return func(tx *sqlite.Tx) error {
		att, err := sqlite.GetAttempt(context.Background(), tx, attemptID)
		if err != nil {
			return err
		}
		if att.State != domain.AttemptStarting && att.State != domain.AttemptPending {
			return nil
		}
		return a.setAttemptState(context.Background(), tx, att, domain.AttemptFailed, reason)
	}
}

func workerPrompt(issue *sqlite.Issue, att *sqlite.Attempt) string {
	var ac []string
	_ = json.Unmarshal([]byte(issue.AcceptanceCriteria), &ac)
	var b strings.Builder
	fmt.Fprintf(&b, "삼사라 작업: %s\n\n", issue.Title)
	if len(ac) > 0 {
		b.WriteString("수용 기준:\n")
		for _, c := range ac {
			b.WriteString("- " + c + "\n")
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "이 worktree 안에서만 구현하세요. 완료되면 변경을 commit하고 `samsara output submit`으로 결과를 제출하세요. (attempt %s)", att.ID)
	return b.String()
}

// --- prompt.submit ---------------------------------------------------------

func (a *App) handlePromptSubmit(ctx context.Context, r outboxRow, p map[string]string) outcome {
	var b *sqlite.Binding
	var s *sqlite.Session
	var lease *sqlite.Lease
	err := a.DB.Read(ctx, func(q sqlite.Q) error {
		var err error
		if b, err = sqlite.CurrentBindingForAttempt(ctx, q, p["attempt_id"]); err != nil {
			return err
		}
		if s, err = sqlite.GetSession(ctx, q, p["session_id"]); err != nil {
			return err
		}
		lease, err = sqlite.LatestLeaseForAttempt(ctx, q, p["attempt_id"])
		return err
	})
	skip := func(reason string) outcome {
		return failed(reason, func(tx *sqlite.Tx) error {
			if _, err := tx.ExecContext(ctx, "UPDATE dispatches SET state='SKIPPED' WHERE id=?", p["dispatch_id"]); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctx, "UPDATE execution_attempts SET resume_required=1, version=version+1 WHERE id=?", p["attempt_id"])
			return err
		})
	}
	if err != nil || b == nil {
		return skip("no binding")
	}
	// Re-check the automated input conditions immediately before sending.
	if lease == nil || lease.State != domain.LeaseActive || s.InputOwner != "automation" || s.InputState != domain.InputReady || b.SessionID != s.ID {
		return skip("input conditions not met: owner=" + s.InputOwner + " input=" + s.InputState)
	}
	be, _ := a.Backends.Get(b.Kind)
	sub, ok := be.(backend.PromptSubmitter)
	if !ok {
		return skip("backend cannot submit prompts")
	}
	rc, err := sub.Submit(ctx, backend.PromptRequest{OperationID: r.OperationID, DispatchID: p["dispatch_id"], AttemptID: p["attempt_id"], Ref: bindingRef(b), Text: p["text"]})
	if err != nil {
		rc = backend.PromptReceipt{Outcome: backend.Ambiguous, Detail: err.Error()}
	}
	switch rc.Outcome {
	case backend.Delivered:
		// Delivered to the terminal; receipt by Claude is confirmed only by
		// the UserPromptSubmit acknowledgement of the dispatch id.
		return unknown("awaiting dispatch acknowledgement", func(tx *sqlite.Tx) error {
			_, err := tx.ExecContext(ctx, "UPDATE dispatches SET state='SENT' WHERE id=? AND state='QUEUED'", p["dispatch_id"])
			return err
		})
	case backend.NotSent, backend.NotSupported, backend.PolicySkipped:
		return skip(rc.Detail)
	default:
		return unknown(rc.Detail, func(tx *sqlite.Tx) error {
			if _, err := tx.ExecContext(ctx, "UPDATE dispatches SET state='UNKNOWN' WHERE id=?", p["dispatch_id"]); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctx, "UPDATE execution_attempts SET resume_required=1, version=version+1 WHERE id=?", p["attempt_id"])
			return err
		})
	}
}

// --- session.interrupt -----------------------------------------------------

func (a *App) handleInterrupt(ctx context.Context, p map[string]string) outcome {
	var b *sqlite.Binding
	if err := a.DB.Read(ctx, func(q sqlite.Q) error {
		var err error
		b, err = sqlite.GetBinding(ctx, q, p["binding_id"])
		return err
	}); err != nil {
		return failed(err.Error(), nil)
	}
	be, _ := a.Backends.Get(b.Kind)
	ctl, ok := be.(backend.SessionController)
	if !ok {
		return done(map[string]string{"interrupt": "UNSUPPORTED"}, nil)
	}
	rc, err := ctl.RequestInterrupt(ctx, bindingRef(b))
	if err != nil {
		return unknown(err.Error(), nil)
	}
	if rc.Outcome == backend.Ambiguous {
		return unknown(rc.Detail, nil)
	}
	return done(map[string]string{"interrupt": string(rc.Outcome), "detail": rc.Detail}, nil)
}

// --- shared helpers --------------------------------------------------------

func nullStr(s sql.NullString) string {
	if s.Valid {
		return s.String
	}
	return ""
}

// filterManagedFiles drops Samsara's own untracked settings from status.
func filterManagedFiles(es []gitops.StatusEntry) []gitops.StatusEntry {
	var out []gitops.StatusEntry
	for _, e := range es {
		if e.Path == hooks.SettingsRel {
			continue
		}
		out = append(out, e)
	}
	return out
}
