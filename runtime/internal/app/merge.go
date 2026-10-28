package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Mineru98/samsara/runtime/internal/domain"
	"github.com/Mineru98/samsara/runtime/internal/gitops"
	"github.com/Mineru98/samsara/runtime/internal/store/sqlite"
)

// createCandidate records a merge candidate for an approved output on the
// issue's integration target. Candidates are immutable in source; base
// re-validation increments the revision (contract §2).
func (a *App) createCandidate(ctx context.Context, tx *sqlite.Tx, outputID string) error {
	out, err := sqlite.GetOutput(ctx, tx, outputID)
	if err != nil {
		return err
	}
	issue, err := sqlite.GetIssue(ctx, tx, out.IssueID)
	if err != nil {
		return err
	}
	if issue.TargetRepositoryID == "" {
		return nil
	}
	t, err := sqlite.FindTarget(ctx, tx, issue.TargetRepositoryID, issue.TargetRef)
	if err != nil || t == nil {
		return err
	}
	ex, err := sqlite.CandidatesWhere(ctx, tx, "output_id=? AND target_id=? AND state <> 'CANCELLED'", out.ID, t.ID)
	if err != nil || len(ex) > 0 {
		return err
	}
	var pos int64
	_ = tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(queue_position),0)+1 FROM merge_candidates WHERE target_id=?", t.ID).Scan(&pos)
	id := domain.NewID("mc")
	reason := ""
	switch t.Authority {
	case domain.AuthorityManual:
		reason = "AWAITING_EXTERNAL_INTEGRATION"
	case domain.AuthorityGitHubQueue:
		reason = "GITHUB_QUEUE_NOT_VERIFIED"
	case domain.AuthoritySamsaraLocal:
		reason = "AWAITING_ENQUEUE"
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO merge_candidates(id,project_id,issue_id,run_id,attempt_id,output_id,target_id,source_sha,state,state_reason,queue_position,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,'WAITING',?,?,?,?)`, id, out.ProjectID, out.IssueID, out.RunID, out.AttemptID, out.ID, t.ID, out.SourceSHA, reason, pos, tx.Now, tx.Now); err != nil {
		return err
	}
	tx.Touch(out.ProjectID, sqlite.KindCandidate, id)
	tx.Touch(out.ProjectID, sqlite.KindIssue, out.IssueID)
	tx.Event(out.ProjectID, "merge_candidate.created", "merge_candidate", id, 1)
	return nil
}

// EnqueueCandidate explicitly requests integration by the target authority.
// Automatic integration stays off by default (ADR 17).
func (a *App) EnqueueCandidate(ctx context.Context, cmd Cmd, candidateID string) (*CommandResult, error) {
	return a.candidateCommand(ctx, cmd, "merge.enqueue", candidateID, func(tx *sqlite.Tx, c *sqlite.Candidate, t *sqlite.Target) error {
		switch t.Authority {
		case domain.AuthorityGitHubQueue:
			return domain.Errf(domain.CodeUnsupportedCapability, "github_queue integration requires a verified provider policy check; not available in this build")
		case domain.AuthorityManual:
			return domain.Errf(domain.CodeUnsupportedCapability, "target uses manual integration: merge outside Samsara; the runtime records evidence once the target contains the source")
		}
		if c.State != domain.CandidateWaiting && c.State != domain.CandidateStaleBase && c.State != domain.CandidateFailed {
			return domain.Errf(domain.CodeConflict, "candidate is %s", c.State)
		}
		state := c.State
		if c.State == domain.CandidateFailed {
			state = domain.CandidateWaiting
		}
		_, err := tx.ExecContext(ctx, "UPDATE merge_candidates SET enqueued=1, state=?, state_reason='QUEUED', version=version+1, updated_at=? WHERE id=?", state, tx.Now, c.ID)
		return err
	})
}

// CancelCandidate cancels a candidate that is not being published.
func (a *App) CancelCandidate(ctx context.Context, cmd Cmd, candidateID string) (*CommandResult, error) {
	return a.candidateCommand(ctx, cmd, "merge.cancel", candidateID, func(tx *sqlite.Tx, c *sqlite.Candidate, t *sqlite.Target) error {
		switch c.State {
		case domain.CandidatePublishing:
			return domain.Errf(domain.CodeConflict, "publication in flight; it cannot be recalled, wait for its observed result")
		case domain.CandidateMerged, domain.CandidateCancelled:
			return domain.Errf(domain.CodeConflict, "candidate is %s", c.State)
		}
		if _, err := tx.ExecContext(ctx, "UPDATE merge_candidates SET state='CANCELLED', state_reason='USER_CANCELLED', version=version+1, updated_at=? WHERE id=?", tx.Now, c.ID); err != nil {
			return err
		}
		aps, err := sqlite.ApprovalsWhere(ctx, tx, "merge_candidate_id=? AND state IN ('PENDING','APPROVED')", c.ID)
		if err != nil {
			return err
		}
		for _, ap := range aps {
			if err := a.invalidateApproval(ctx, tx, ap, "CANDIDATE_CANCELLED"); err != nil {
				return err
			}
		}
		return nil
	})
}

func (a *App) candidateCommand(ctx context.Context, cmd Cmd, scope, candidateID string, fn func(tx *sqlite.Tx, c *sqlite.Candidate, t *sqlite.Target) error) (*CommandResult, error) {
	if err := a.requireReady(); err != nil {
		return nil, err
	}
	var out CommandResult
	err := a.DB.Write(ctx, func(tx *sqlite.Tx) error {
		return a.idempotent(ctx, tx, scope+":"+candidateID, cmd, candidateID, &out, func() (any, error) {
			c, err := sqlite.GetCandidate(ctx, tx, candidateID)
			if err != nil {
				return nil, err
			}
			if err := checkIfMatch(cmd, c.Version); err != nil {
				return nil, err
			}
			t, err := sqlite.GetTarget(ctx, tx, c.TargetID)
			if err != nil {
				return nil, err
			}
			if err := fn(tx, c, t); err != nil {
				return nil, err
			}
			tx.Touch(c.ProjectID, sqlite.KindCandidate, c.ID)
			tx.Touch(c.ProjectID, sqlite.KindIssue, c.IssueID)
			tx.Event(c.ProjectID, scope, "merge_candidate", c.ID, c.Version+1)
			if err := tx.Audit(ctx, c.ProjectID, cmd.Principal.ID, scope, "merge_candidate", c.ID, "", "", nil); err != nil {
				return nil, err
			}
			p, err := sqlite.GetProject(ctx, tx, c.ProjectID)
			if err != nil {
				return nil, err
			}
			// Exactly one event per project per transaction: the committed
			// project_version is the current one plus one.
			return &CommandResult{CommandID: domain.NewID("cmd"), Status: "ACCEPTED", ResourceVersion: domain.Dec(c.Version + 1), ProjectVersion: domain.Dec(p.ProjectVersion + 1)}, nil
		})
	})
	if err != nil {
		return nil, err
	}
	a.Wake()
	return &out, nil
}

func (a *App) targetLock(id string) *sync.Mutex {
	m, _ := a.targetMu.LoadOrStore(id, &sync.Mutex{})
	return m.(*sync.Mutex)
}

// MergeTick advances integration per target. Local targets process one
// candidate at a time from validation to publication (04 §9.1). Manual
// targets only observe external integration.
func (a *App) MergeTick(ctx context.Context) error {
	if !a.Ready() {
		return nil
	}
	var targets []*sqlite.Target
	if err := a.DB.Read(ctx, func(q sqlite.Q) error {
		rows, err := q.QueryContext(ctx, "SELECT DISTINCT target_id FROM merge_candidates WHERE state NOT IN ('MERGED','CANCELLED')")
		if err != nil {
			return err
		}
		var ids []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		rows.Close()
		for _, id := range ids {
			t, err := sqlite.GetTarget(ctx, q, id)
			if err != nil {
				return err
			}
			targets = append(targets, t)
		}
		return nil
	}); err != nil {
		return err
	}
	for _, t := range targets {
		var err error
		switch t.Authority {
		case domain.AuthorityManual:
			err = a.observeManualTarget(ctx, t)
		case domain.AuthoritySamsaraLocal:
			err = a.advanceLocalTarget(ctx, t)
		}
		if err != nil {
			a.Log.Warn("merge tick", "target", t.ID, "err", err)
		}
	}
	return nil
}

// observeManualTarget records evidence when the user integrated the source
// into the target outside Samsara (04 §13.3).
func (a *App) observeManualTarget(ctx context.Context, t *sqlite.Target) error {
	var cands []*sqlite.Candidate
	var repo *sqlite.Repository
	if err := a.DB.Read(ctx, func(q sqlite.Q) error {
		var err error
		if cands, err = sqlite.CandidatesWhere(ctx, q, "target_id=? AND state='WAITING'", t.ID); err != nil {
			return err
		}
		repo, err = sqlite.GetRepository(ctx, q, t.RepositoryID)
		return err
	}); err != nil || len(cands) == 0 {
		return err
	}
	dir := repoDir(repo)
	targetOID, err := a.Git.ResolveRef(ctx, dir, t.TargetRef)
	if err != nil || targetOID == "" {
		return err
	}
	for _, c := range cands {
		in, err := a.Git.IsAncestor(ctx, dir, c.SourceSHA, targetOID)
		if err != nil || !in {
			continue
		}
		cid := c.ID
		if err := a.DB.Write(ctx, func(tx *sqlite.Tx) error {
			cur, err := sqlite.GetCandidate(ctx, tx, cid)
			if err != nil || cur.State != domain.CandidateWaiting {
				return err
			}
			return a.recordMerged(ctx, tx, cur, "", targetOID, "manual_observed", map[string]any{"observed_target": targetOID})
		}); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) advanceLocalTarget(ctx context.Context, t *sqlite.Target) error {
	mu := a.targetLock(t.ID)
	if !mu.TryLock() {
		return nil
	}
	defer mu.Unlock()
	var active, next *sqlite.Candidate
	var approvalRequired bool
	err := a.DB.Read(ctx, func(q sqlite.Q) error {
		cs, err := sqlite.CandidatesWhere(ctx, q, "target_id=? AND state IN ('VALIDATING','READY','PUBLISHING')", t.ID)
		if err != nil {
			return err
		}
		if len(cs) > 0 {
			active = cs[0]
			p, err := sqlite.GetProject(ctx, q, active.ProjectID)
			if err != nil {
				return err
			}
			approvalRequired = projectConfig(p).Approval.Integration != "auto"
			return nil
		}
		ws, err := sqlite.CandidatesWhere(ctx, q, "target_id=? AND enqueued=1 AND state IN ('WAITING','STALE_BASE')", t.ID)
		if err != nil {
			return err
		}
		for _, c := range ws {
			bl, err := sqlite.IntegrationBlockers(ctx, q, c.RunID, c.IssueID)
			if err != nil {
				return err
			}
			if len(bl) == 0 {
				next = c
				return nil
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if active == nil && next != nil {
		return a.DB.Write(ctx, func(tx *sqlite.Tx) error {
			c, err := sqlite.GetCandidate(ctx, tx, next.ID)
			if err != nil || (c.State != domain.CandidateWaiting && c.State != domain.CandidateStaleBase) {
				return err
			}
			rev := c.Revision
			if c.State == domain.CandidateStaleBase {
				rev++
			}
			if _, err := tx.ExecContext(ctx, "UPDATE merge_candidates SET state='VALIDATING', state_reason='', revision=?, version=version+1, updated_at=? WHERE id=?", rev, tx.Now, c.ID); err != nil {
				return err
			}
			if _, err := tx.Enqueue(ctx, sqlite.OutboxItem{ProjectID: c.ProjectID, Kind: "merge.validate", Payload: map[string]string{"candidate_id": c.ID, "revision": domain.Dec(rev)},
				DedupeKey: fmt.Sprintf("merge.validate:%s:%d", c.ID, rev), ResourceKey: "target:" + t.ID}); err != nil {
				return err
			}
			tx.Touch(c.ProjectID, sqlite.KindCandidate, c.ID)
			tx.Touch(c.ProjectID, sqlite.KindIssue, c.IssueID)
			tx.Event(c.ProjectID, "merge_candidate.validating", "merge_candidate", c.ID, c.Version+1)
			return nil
		})
	}
	if active == nil || active.State != domain.CandidateReady {
		return nil
	}
	return a.DB.Write(ctx, func(tx *sqlite.Tx) error {
		c, err := sqlite.GetCandidate(ctx, tx, active.ID)
		if err != nil || c.State != domain.CandidateReady {
			return err
		}
		p, err := sqlite.GetProject(ctx, tx, c.ProjectID)
		if err != nil {
			return err
		}
		if approvalRequired {
			ap, err := a.requestApproval(ctx, tx, mergeSpec(p, c, t))
			if err != nil {
				return err
			}
			if stale, reason, err := a.approvalStale(ctx, tx, ap); err != nil {
				return err
			} else if stale {
				return a.invalidateApproval(ctx, tx, ap, reason)
			}
			if ap.State != domain.ApprovalApproved {
				return nil
			}
		}
		// Linearisation point: publication intent + target fence in one tx.
		if _, err := tx.ExecContext(ctx, "UPDATE target_locks SET current_fence=current_fence+1, owner_epoch=?, candidate_id=? WHERE target_id=?", a.Epoch, c.ID, t.ID); err != nil {
			return err
		}
		var fence int64
		if err := tx.QueryRowContext(ctx, "SELECT current_fence FROM target_locks WHERE target_id=?", t.ID).Scan(&fence); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE merge_candidates SET state='PUBLISHING', publication_observation='REQUESTED', version=version+1, updated_at=? WHERE id=?", tx.Now, c.ID); err != nil {
			return err
		}
		if _, err := tx.Enqueue(ctx, sqlite.OutboxItem{ProjectID: c.ProjectID, Kind: "git.publish",
			Payload:   map[string]string{"candidate_id": c.ID, "revision": domain.Dec(c.Revision), "target_fence": domain.Dec(fence)},
			DedupeKey: fmt.Sprintf("git.publish:%s:%d", c.ID, c.Revision), ResourceKey: "target:" + t.ID}); err != nil {
			return err
		}
		tx.Touch(c.ProjectID, sqlite.KindCandidate, c.ID)
		tx.Event(c.ProjectID, "merge_candidate.publishing", "merge_candidate", c.ID, c.Version+1)
		return tx.Audit(ctx, c.ProjectID, "integration-controller", "merge.publish_intent", "merge_candidate", c.ID, "", "", map[string]any{"candidate_sha": c.CandidateSHA, "base_sha": c.BaseSHA, "fence": fence})
	})
}

func mergeSpec(p *sqlite.Project, c *sqlite.Candidate, t *sqlite.Target) approvalSpec {
	return approvalSpec{Kind: domain.ApprovalMerge, ProjectID: p.ID, RunID: c.RunID, IssueID: c.IssueID, OutputID: c.OutputID,
		CandidateID: c.ID, CandidateRev: c.Revision, PolicyVersion: p.PolicyVersion,
		Subject: map[string]any{"candidate_sha": c.CandidateSHA, "base_sha": c.BaseSHA, "source_sha": c.SourceSHA, "target_ref": t.TargetRef, "target_policy": t.PolicyVersion}}
}

// handleMergeValidate computes the merge of the immutable source onto the
// current target and checks it in a temporary worktree (04 §9.2–9.3).
func (a *App) handleMergeValidate(ctx context.Context, p map[string]string) outcome {
	var c *sqlite.Candidate
	var t *sqlite.Target
	var repo *sqlite.Repository
	var issue *sqlite.Issue
	var profile *sqlite.CheckProfile
	err := a.DB.Read(ctx, func(q sqlite.Q) error {
		var err error
		if c, err = sqlite.GetCandidate(ctx, q, p["candidate_id"]); err != nil {
			return err
		}
		if t, err = sqlite.GetTarget(ctx, q, c.TargetID); err != nil {
			return err
		}
		if repo, err = sqlite.GetRepository(ctx, q, t.RepositoryID); err != nil {
			return err
		}
		if issue, err = sqlite.GetIssue(ctx, q, c.IssueID); err != nil {
			return err
		}
		if issue.CheckProfile != "" {
			profile, err = sqlite.GetCheckProfile(ctx, q, c.ProjectID, issue.CheckProfile)
		}
		return err
	})
	if err != nil {
		return failed(err.Error(), nil)
	}
	if c.State != domain.CandidateValidating || domain.Dec(c.Revision) != p["revision"] {
		return done(map[string]string{"skipped": c.State}, nil)
	}
	dir := repoDir(repo)
	setState := func(state, reason string, upd map[string]string) func(tx *sqlite.Tx) error {
		return func(tx *sqlite.Tx) error {
			cur, err := sqlite.GetCandidate(ctx, tx, c.ID)
			if err != nil || cur.State != domain.CandidateValidating || cur.Revision != c.Revision {
				return err
			}
			if _, err := tx.ExecContext(ctx, "UPDATE merge_candidates SET state=?, state_reason=?, base_sha=COALESCE(?,base_sha), candidate_sha=COALESCE(?,candidate_sha), candidate_tree=COALESCE(?,candidate_tree), version=version+1, updated_at=? WHERE id=?",
				state, reason, nz(upd["base"]), nz(upd["sha"]), nz(upd["tree"]), tx.Now, c.ID); err != nil {
				return err
			}
			tx.Touch(c.ProjectID, sqlite.KindCandidate, c.ID)
			tx.Touch(c.ProjectID, sqlite.KindIssue, c.IssueID)
			tx.Event(c.ProjectID, "merge_candidate."+state, "merge_candidate", c.ID, cur.Version+1)
			return nil
		}
	}
	checkedOut, err := a.Git.RefCheckedOut(ctx, dir, t.TargetRef)
	if err != nil {
		return retry(15*time.Second, err)
	}
	if checkedOut {
		return failed("target is checked out", setState(domain.CandidateFailed, domain.CodeTargetCheckedOut, nil))
	}
	base, err := a.Git.ResolveRef(ctx, dir, t.TargetRef)
	if err != nil {
		return retry(15*time.Second, err)
	}
	if base == "" {
		return failed("target ref missing", setState(domain.CandidateFailed, "TARGET_REF_MISSING", nil))
	}
	mr, err := a.Git.MergeTree(ctx, dir, base, c.SourceSHA)
	if err != nil {
		return failed(err.Error(), setState(domain.CandidateFailed, "MERGE_TREE_ERROR", map[string]string{"base": base}))
	}
	if mr.Conflict {
		return done(map[string]string{"conflict": mr.Detail}, setState(domain.CandidateConflict, "MERGE_CONFLICT", map[string]string{"base": base}))
	}
	sha, err := a.Git.CommitTree(ctx, dir, mr.Tree, fmt.Sprintf("samsara: integrate %s (%s)\n\nmerge-candidate: %s rev %d", issue.ClientKey, issue.Title, c.ID, c.Revision), a.Clock.Now(), base, c.SourceSHA)
	if err != nil {
		return failed(err.Error(), setState(domain.CandidateFailed, "COMMIT_TREE_ERROR", map[string]string{"base": base}))
	}
	check := checkResult{Name: "none", Result: "PASSED", Summary: "no check profile configured", Started: a.Clock.Now(), Finished: a.Clock.Now()}
	if profile != nil {
		check = a.runCheck(ctx, dir, sha, "candidate-"+c.ID, profile)
	} else if issue.CheckProfile != "" {
		check = checkResult{Name: issue.CheckProfile, Result: "FAILED", Summary: "check profile no longer registered", Started: a.Clock.Now(), Finished: a.Clock.Now()}
	}
	upd := map[string]string{"base": base, "sha": sha, "tree": mr.Tree}
	return done(map[string]any{"candidate_sha": sha, "base_sha": base, "check": check.Result}, func(tx *sqlite.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO check_runs(id,project_id,subject_kind,subject_id,subject_revision,check_name,checked_sha,command_digest,source_actor,result,exit_code,summary,started_at,finished_at)
			VALUES(?,?,'merge_candidate',?,?,?,?,?,'samsara',?,?,?,?,?)`, domain.NewID("chk"), c.ProjectID, c.ID, c.Revision, check.Name, sha, check.CommandDigest,
			check.Result, check.ExitCode, check.Summary, domain.Millis(check.Started), domain.Millis(check.Finished)); err != nil {
			return err
		}
		if check.Result != "PASSED" {
			return setState(domain.CandidateFailed, "CHECK_"+check.Result, upd)(tx)
		}
		return setState(domain.CandidateReady, "", upd)(tx)
	})
}

func nz(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// handlePublish performs the CAS publication. It re-checks approval, target
// fence and base first; on an unknown outcome it reads the target before
// deciding anything (04 §13.1).
func (a *App) handlePublish(ctx context.Context, p map[string]string) outcome {
	var c *sqlite.Candidate
	var t *sqlite.Target
	var repo *sqlite.Repository
	var fence int64
	var proj *sqlite.Project
	err := a.DB.Read(ctx, func(q sqlite.Q) error {
		var err error
		if c, err = sqlite.GetCandidate(ctx, q, p["candidate_id"]); err != nil {
			return err
		}
		if t, err = sqlite.GetTarget(ctx, q, c.TargetID); err != nil {
			return err
		}
		if repo, err = sqlite.GetRepository(ctx, q, t.RepositoryID); err != nil {
			return err
		}
		if proj, err = sqlite.GetProject(ctx, q, c.ProjectID); err != nil {
			return err
		}
		return q.QueryRowContext(ctx, "SELECT current_fence FROM target_locks WHERE target_id=?", t.ID).Scan(&fence)
	})
	if err != nil {
		return failed(err.Error(), nil)
	}
	if c.State != domain.CandidatePublishing {
		return done(map[string]string{"skipped": c.State}, nil)
	}
	dir := repoDir(repo)
	back := func(state, reason string) func(tx *sqlite.Tx) error {
		return func(tx *sqlite.Tx) error {
			cur, err := sqlite.GetCandidate(ctx, tx, c.ID)
			if err != nil || cur.State != domain.CandidatePublishing {
				return err
			}
			if _, err := tx.ExecContext(ctx, "UPDATE merge_candidates SET state=?, state_reason=?, publication_observation=?, version=version+1, updated_at=? WHERE id=?", state, reason, reason, tx.Now, c.ID); err != nil {
				return err
			}
			aps, err := sqlite.ApprovalsWhere(ctx, tx, "kind='MERGE' AND merge_candidate_id=? AND state IN ('PENDING','APPROVED')", c.ID)
			if err != nil {
				return err
			}
			if state == domain.CandidateStaleBase {
				for _, ap := range aps {
					if err := a.invalidateApproval(ctx, tx, ap, "BASE_CHANGED"); err != nil {
						return err
					}
				}
			}
			tx.Touch(c.ProjectID, sqlite.KindCandidate, c.ID)
			tx.Touch(c.ProjectID, sqlite.KindIssue, c.IssueID)
			tx.Event(c.ProjectID, "merge_candidate."+state, "merge_candidate", c.ID, cur.Version+1)
			return nil
		}
	}
	if domain.Dec(fence) != p["target_fence"] {
		return failed("stale target fence", back(domain.CandidateFailed, domain.CodeStaleFence))
	}
	current, err := a.Git.ResolveRef(ctx, dir, t.TargetRef)
	if err != nil {
		return retry(15*time.Second, err)
	}
	if current == c.CandidateSHA {
		// Already published (e.g. response lost before crash).
		return done(map[string]string{"observed": current}, a.mergedFinish(c, current, "samsara_local_cas"))
	}
	if current != c.BaseSHA {
		return done(map[string]string{"observed": current}, back(domain.CandidateStaleBase, "TARGET_MOVED"))
	}
	if projectConfig(proj).Approval.Integration != "auto" {
		ok := false
		_ = a.DB.Read(ctx, func(q sqlite.Q) error {
			aps, _ := sqlite.ApprovalsWhere(ctx, q, "kind='MERGE' AND merge_candidate_id=? AND candidate_revision=? AND state='APPROVED'", c.ID, c.Revision)
			for _, ap := range aps {
				if stale, _, _ := a.approvalStale(ctx, q, ap); !stale {
					ok = true
				}
			}
			return nil
		})
		if !ok {
			return failed("merge approval no longer valid", back(domain.CandidateReady, "MERGE_APPROVAL_REQUIRED"))
		}
	}
	if checked, err := a.Git.RefCheckedOut(ctx, dir, t.TargetRef); err != nil || checked {
		return failed("target checked out", back(domain.CandidateFailed, domain.CodeTargetCheckedOut))
	}
	err = a.Git.UpdateRefCAS(ctx, dir, t.TargetRef, c.CandidateSHA, c.BaseSHA, "samsara: publish "+c.ID)
	if errors.Is(err, gitops.ErrCASFailed) {
		return done(nil, back(domain.CandidateStaleBase, "CAS_FAILED"))
	}
	after, rerr := a.Git.ResolveRef(ctx, dir, t.TargetRef)
	if err != nil || rerr != nil {
		// Outcome unknown: keep PUBLISHING; reconcile re-reads the target.
		msg := fmt.Sprint(err, rerr)
		return unknown(msg, func(tx *sqlite.Tx) error {
			_, e := tx.ExecContext(ctx, "UPDATE merge_candidates SET publication_observation='UNKNOWN', version=version+1, updated_at=? WHERE id=?", tx.Now, c.ID)
			tx.Touch(c.ProjectID, sqlite.KindCandidate, c.ID)
			return e
		})
	}
	if after != c.CandidateSHA {
		return done(map[string]string{"observed": after}, back(domain.CandidateStaleBase, "TARGET_MOVED_AFTER_CAS"))
	}
	return done(map[string]string{"observed": after}, a.mergedFinish(c, c.BaseSHA, "samsara_local_cas"))
}

func (a *App) mergedFinish(c *sqlite.Candidate, before, method string) func(tx *sqlite.Tx) error {
	return func(tx *sqlite.Tx) error {
		cur, err := sqlite.GetCandidate(context.Background(), tx, c.ID)
		if err != nil || cur.State == domain.CandidateMerged {
			return err
		}
		return a.recordMerged(context.Background(), tx, cur, before, cur.CandidateSHA, method, map[string]any{"candidate_revision": cur.Revision})
	}
}

// recordMerged writes immutable integration evidence and completes what the
// evidence allows (contract §4.5).
func (a *App) recordMerged(ctx context.Context, tx *sqlite.Tx, c *sqlite.Candidate, before, after, method string, provider map[string]any) error {
	pe, _ := json.Marshal(provider)
	if _, err := tx.ExecContext(ctx, `INSERT INTO integration_evidence(id,project_id,candidate_id,target_id,output_id,issue_id,target_before,target_after,method,provider_evidence,created_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?)`, domain.NewID("ev"), c.ProjectID, c.ID, c.TargetID, c.OutputID, c.IssueID, before, after, method, string(pe), tx.Now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE merge_candidates SET state='MERGED', state_reason='', publication_observation='CONFIRMED', version=version+1, updated_at=? WHERE id=?", tx.Now, c.ID); err != nil {
		return err
	}
	tx.Touch(c.ProjectID, sqlite.KindCandidate, c.ID)
	tx.Event(c.ProjectID, "merge_candidate.merged", "merge_candidate", c.ID, c.Version+1)
	if err := tx.Audit(ctx, c.ProjectID, "integration-controller", "merge.merged", "merge_candidate", c.ID, "", method, map[string]string{"target_after": after}); err != nil {
		return err
	}
	// Other local candidates validated against the old base are now stale.
	if _, err := tx.ExecContext(ctx, "UPDATE merge_candidates SET state='STALE_BASE', state_reason='BASE_ADVANCED', version=version+1, updated_at=? WHERE target_id=? AND id<>? AND state='READY'", tx.Now, c.TargetID, c.ID); err != nil {
		return err
	}
	if err := a.maybeCompleteIssue(ctx, tx, c.RunID, c.IssueID); err != nil {
		return err
	}
	if err := a.touchRunIssues(ctx, tx, c.ProjectID, c.RunID); err != nil {
		return err
	}
	a.Wake()
	return nil
}

// maybeCompleteIssue applies the completion rule of contract §4.5.
func (a *App) maybeCompleteIssue(ctx context.Context, tx *sqlite.Tx, runID, issueID string) error {
	r, err := sqlite.GetRun(ctx, tx, runID)
	if err != nil {
		return err
	}
	out, err := sqlite.CurrentOutput(ctx, tx, runID, issueID, r.Generation)
	if err != nil || out == nil || out.Verification != "PASSED" {
		return err
	}
	apps, err := sqlite.ApprovalsWhere(ctx, tx, "kind='RESULT' AND output_id=? AND state='APPROVED'", out.ID)
	if err != nil || len(apps) == 0 {
		return err
	}
	var ev int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM integration_evidence WHERE output_id=?", out.ID).Scan(&ev); err != nil || ev == 0 {
		return err
	}
	res, err := tx.ExecContext(ctx, "UPDATE issues SET lifecycle='COMPLETED', version=version+1, updated_at=? WHERE id=? AND lifecycle='OPEN'", tx.Now, issueID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil
	}
	tx.Touch(out.ProjectID, sqlite.KindIssue, issueID)
	tx.Event(out.ProjectID, "issue.completed", "issue", issueID, 0)
	return a.maybeCompleteRun(ctx, tx, runID)
}

// reconcilePublishing resolves PUBLISHING candidates whose outcome is
// unknown (crash or lost response) by reading the target.
func (a *App) reconcilePublishing(ctx context.Context) error {
	var cands []*sqlite.Candidate
	if err := a.DB.Read(ctx, func(q sqlite.Q) error {
		var err error
		cands, err = sqlite.CandidatesWhere(ctx, q, "state='PUBLISHING' AND publication_observation='UNKNOWN'")
		return err
	}); err != nil {
		return err
	}
	for _, c := range cands {
		var t *sqlite.Target
		var repo *sqlite.Repository
		if err := a.DB.Read(ctx, func(q sqlite.Q) error {
			var err error
			if t, err = sqlite.GetTarget(ctx, q, c.TargetID); err != nil {
				return err
			}
			repo, err = sqlite.GetRepository(ctx, q, t.RepositoryID)
			return err
		}); err != nil {
			return err
		}
		cur, err := a.Git.ResolveRef(ctx, repoDir(repo), t.TargetRef)
		if err != nil {
			continue
		}
		cc := c
		_ = a.DB.Write(ctx, func(tx *sqlite.Tx) error {
			now, err := sqlite.GetCandidate(ctx, tx, cc.ID)
			if err != nil || now.State != domain.CandidatePublishing {
				return err
			}
			switch cur {
			case now.CandidateSHA:
				return a.recordMerged(ctx, tx, now, now.BaseSHA, cur, "samsara_local_cas", map[string]any{"reconciled": true})
			case now.BaseSHA:
				// Not published: safe to go back to READY (approval re-checked).
				_, err := tx.ExecContext(ctx, "UPDATE merge_candidates SET state='READY', publication_observation='NOT_PUBLISHED', version=version+1, updated_at=? WHERE id=?", tx.Now, now.ID)
				tx.Touch(now.ProjectID, sqlite.KindCandidate, now.ID)
				return err
			default:
				_, err := tx.ExecContext(ctx, "UPDATE merge_candidates SET state='STALE_BASE', state_reason='TARGET_MOVED', publication_observation='NOT_PUBLISHED', version=version+1, updated_at=? WHERE id=?", tx.Now, now.ID)
				tx.Touch(now.ProjectID, sqlite.KindCandidate, now.ID)
				return err
			}
		})
	}
	return nil
}
