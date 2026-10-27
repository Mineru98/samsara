package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Mineru98/samsara/runtime/internal/domain"
	"github.com/Mineru98/samsara/runtime/internal/store/sqlite"
)

// OutputSubmitRequest is sent by `samsara output submit` from inside the
// worker. The caller is identified by its launch ticket (managed worker) or
// by its Claude ancestor process (Standalone).
type OutputSubmitRequest struct {
	Ticket          string `json:"ticket,omitempty"`
	ClaudeSessionID string `json:"claude_session_id,omitempty"`
	AncestorPIDs    []int  `json:"ancestor_pids,omitempty"`
	AttemptID       string `json:"attempt_id,omitempty"`
	Fence           string `json:"fence,omitempty"`
	Summary         string `json:"summary,omitempty"`
	PullRequest     string `json:"pull_request,omitempty"`
}

// OutputSubmitResult reports the accepted immutable output.
type OutputSubmitResult struct {
	OutputID       string   `json:"output_id"`
	AttemptID      string   `json:"attempt_id"`
	SourceSHA      string   `json:"source_sha"`
	ManifestDigest string   `json:"manifest_digest"`
	Uncommitted    []string `json:"uncommitted_paths"`
	OperationID    string   `json:"verify_operation_id"`
}

// SubmitOutput freezes the worktree HEAD as an immutable output. It requires
// the current execution lease and fence (INV-03); afterwards the lease is
// released and integration continues under the target controller's
// authority (01 §11.3).
func (a *App) SubmitOutput(ctx context.Context, cmd Cmd, req OutputSubmitRequest) (*OutputSubmitResult, error) {
	if err := a.requireReady(); err != nil {
		return nil, err
	}
	attemptID := req.AttemptID
	if req.Ticket != "" {
		att, _, err := a.attemptForTicket(ctx, req.Ticket)
		if err != nil {
			return nil, err
		}
		attemptID = att.ID
	} else if attemptID == "" {
		err := a.DB.Read(ctx, func(q sqlite.Q) error {
			s, err := findSessionForAttach(ctx, q, AttachRequest{ClaudeSessionID: req.ClaudeSessionID, AncestorPIDs: req.AncestorPIDs})
			if err != nil {
				return err
			}
			as, err := sqlite.ActiveAssignmentForSession(ctx, q, s.ID)
			if err != nil {
				return err
			}
			if as == nil {
				return domain.Errf(domain.CodeConflict, "session has no assigned attempt")
			}
			attemptID = as.AttemptID
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	var att *sqlite.Attempt
	var w *sqlite.Worktree
	if err := a.DB.Read(ctx, func(q sqlite.Q) error {
		var err error
		if att, err = sqlite.GetAttempt(ctx, q, attemptID); err != nil {
			return err
		}
		w, err = sqlite.GetWorktree(ctx, q, att.WorktreeID)
		return err
	}); err != nil {
		return nil, err
	}
	head, err := a.Git.RevParse(ctx, w.Path, "HEAD")
	if err != nil {
		return nil, domain.Errf(domain.CodeConflict, "cannot read worktree HEAD: %v", err)
	}
	status, err := a.Git.Status(ctx, w.Path)
	if err != nil {
		return nil, err
	}
	status = filterManagedFiles(status)
	dirty := []string{}
	for _, e := range status {
		dirty = append(dirty, e.Path)
	}
	base := w.BaseSHA
	changed := []string{}
	if base != "" {
		if head == base {
			return nil, domain.Errf(domain.CodeConflict, "no commits on top of base %s; commit the implementation first", base)
		}
		changed, _ = a.Git.ChangedFiles(ctx, w.Path, base, head)
	}
	branch := ""
	if r, err := a.Git.Run(ctx, w.Path, nil, "symbolic-ref", "-q", "HEAD"); err == nil && r.ExitCode == 0 {
		branch = strings.TrimSpace(r.Stdout)
	}
	manifest := map[string]any{"source_sha": head, "base_sha": base, "branch_ref": branch, "changed_files": changed,
		"uncommitted_paths": dirty, "summary": req.Summary, "pull_request": req.PullRequest}
	manifestJSON, _ := json.Marshal(manifest)
	res := &OutputSubmitResult{AttemptID: attemptID, SourceSHA: head, ManifestDigest: domain.Digest(manifest), Uncommitted: dirty}
	err = a.DB.Write(ctx, func(tx *sqlite.Tx) error {
		att, err := sqlite.GetAttempt(ctx, tx, attemptID)
		if err != nil {
			return err
		}
		if att.State != domain.AttemptRunning {
			return domain.Errf(domain.CodeStaleFence, "attempt is %s; only the current RUNNING attempt can submit", att.State)
		}
		l, err := sqlite.LatestLeaseForAttempt(ctx, tx, att.ID)
		if err != nil {
			return err
		}
		cur, err := sqlite.CurrentFence(ctx, tx, resourceKey(att.WorktreeID))
		if err != nil {
			return err
		}
		if l == nil || l.State != domain.LeaseActive || l.Fence != cur || l.RuntimeEpoch != a.Epoch || l.DeadlineAt < tx.Now {
			return domain.Errf(domain.CodeStaleFence, "execution lease is not current")
		}
		if req.Fence != "" && req.Fence != domain.Dec(cur) {
			return domain.Errf(domain.CodeStaleFence, "fence %s is stale (current %s)", req.Fence, domain.Dec(cur))
		}
		p, err := sqlite.GetProject(ctx, tx, att.ProjectID)
		if err != nil {
			return err
		}
		if reason := a.policyBlock(ctx, tx, p, att); reason != "" && reason != domain.ReasonDependencyUnmet {
			return domain.Errf(domain.CodeActionForbidden, "submission refused: %s", reason)
		}
		outID := domain.NewID("out")
		prov, _ := json.Marshal(map[string]any{"attempt_id": att.ID, "lease_id": l.ID, "fence": l.Fence, "session_id": l.SessionID,
			"principal": cmd.Principal.ID, "graph_version": p.GraphVersion, "policy_version": p.PolicyVersion})
		if _, err := tx.ExecContext(ctx, "UPDATE output_artifacts SET superseded_by=? WHERE run_id=? AND issue_id=? AND superseded_by IS NULL", outID, att.RunID, att.IssueID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO output_artifacts(id,project_id,run_id,issue_id,attempt_id,run_generation,source_sha,base_sha,branch_ref,manifest,manifest_digest,provenance,fence,created_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, outID, att.ProjectID, att.RunID, att.IssueID, att.ID, att.RunGeneration, head, base, branch,
			string(manifestJSON), res.ManifestDigest, string(prov), l.Fence, tx.Now); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE run_issues SET chosen_output_id=? WHERE run_id=? AND issue_id=?", outID, att.RunID, att.IssueID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE execution_attempts SET state='IMPLEMENTED', output_id=?, version=version+1, updated_at=? WHERE id=?", outID, tx.Now, att.ID); err != nil {
			return err
		}
		if err := a.releaseAttemptBindings(ctx, tx, att); err != nil {
			return err
		}
		opID, err := tx.Enqueue(ctx, sqlite.OutboxItem{ProjectID: att.ProjectID, Kind: "output.verify", Payload: map[string]string{"output_id": outID},
			DedupeKey: "verify:" + outID, ResourceKey: "output:" + outID})
		if err != nil {
			return err
		}
		res.OutputID, res.OperationID = outID, opID
		tx.Touch(att.ProjectID, sqlite.KindAttempt, att.ID)
		tx.Touch(att.ProjectID, sqlite.KindIssue, att.IssueID)
		if err := a.touchRunIssues(ctx, tx, att.ProjectID, att.RunID); err != nil {
			return err
		}
		tx.Event(att.ProjectID, "output.submitted", "attempt", att.ID, att.Version+1)
		return tx.Audit(ctx, att.ProjectID, cmd.Principal.ID, "output.submit", "attempt", att.ID, "", "", map[string]any{"output_id": outID, "source_sha": head})
	})
	if err != nil {
		return nil, err
	}
	a.Wake()
	_ = a.SyncRegistry(ctx)
	return res, nil
}

// handleVerify runs the issue's trusted check profile against the immutable
// source in a detached temporary worktree, never in the worker's tree.
func (a *App) handleVerify(ctx context.Context, p map[string]string) outcome {
	var out *sqlite.Output
	var issue *sqlite.Issue
	var profile *sqlite.CheckProfile
	var repo *sqlite.Repository
	err := a.DB.Read(ctx, func(q sqlite.Q) error {
		var err error
		if out, err = sqlite.GetOutput(ctx, q, p["output_id"]); err != nil {
			return err
		}
		if issue, err = sqlite.GetIssue(ctx, q, out.IssueID); err != nil {
			return err
		}
		if repo, err = sqlite.RepositoryOfProject(ctx, q, out.ProjectID); err != nil {
			return err
		}
		if issue.CheckProfile != "" {
			profile, err = sqlite.GetCheckProfile(ctx, q, out.ProjectID, issue.CheckProfile)
		}
		return err
	})
	if err != nil {
		return failed(err.Error(), nil)
	}
	if out.Verification != "PENDING" {
		return done(map[string]string{"verification": out.Verification}, nil)
	}
	// Mark VERIFYING first so observers see progress.
	_ = a.DB.Write(ctx, func(tx *sqlite.Tx) error {
		att, err := sqlite.GetAttempt(ctx, tx, out.AttemptID)
		if err != nil || att.State != domain.AttemptImplemented {
			return err
		}
		return a.setAttemptState(ctx, tx, att, domain.AttemptVerifying, "")
	})
	check := checkResult{Name: "none", Result: "PASSED", Summary: "no check profile configured", Started: a.Clock.Now()}
	if issue.CheckProfile != "" {
		if profile == nil {
			check = checkResult{Name: issue.CheckProfile, Result: "FAILED", Summary: "check profile no longer registered", Started: a.Clock.Now()}
		} else if repo == nil {
			check = checkResult{Name: issue.CheckProfile, Result: "FAILED", Summary: "no repository", Started: a.Clock.Now()}
		} else {
			check = a.runCheck(ctx, repoDir(repo), out.SourceSHA, "verify-"+out.ID, profile)
		}
	}
	return done(check, func(tx *sqlite.Tx) error {
		return a.recordVerification(ctx, tx, out, check)
	})
}

type checkResult struct {
	Name, Result, Summary, CommandDigest string
	ExitCode                             int
	Started, Finished                    time.Time
}

func repoDir(r *sqlite.Repository) string {
	return strings.TrimSuffix(r.CommonDir, "/.git")
}

// runCheck executes argv (never a shell string) with a timeout in a fresh
// detached worktree at sha and removes the worktree afterwards (no force).
func (a *App) runCheck(ctx context.Context, repo, sha, label string, profile *sqlite.CheckProfile) checkResult {
	cr := checkResult{Name: profile.Name, Started: a.Clock.Now(), CommandDigest: domain.Digest(profile.Argv)}
	dir := filepath.Join(a.Layout.StateDir, "checks", label)
	_ = os.RemoveAll(dir) // only our own scratch dir, never a user worktree
	_, _ = a.Git.Run(ctx, repo, nil, "worktree", "prune")
	if err := a.Git.AddDetachedWorktree(ctx, repo, dir, sha); err != nil {
		cr.Result, cr.Summary, cr.Finished = "ERROR", err.Error(), a.Clock.Now()
		return cr
	}
	defer func() {
		if err := a.Git.RemoveWorktree(context.Background(), repo, dir); err != nil {
			a.Log.Warn("check worktree retained", "dir", dir, "err", err)
		}
	}()
	cctx, cancel := context.WithTimeout(ctx, time.Duration(profile.TimeoutSec)*time.Second)
	defer cancel()
	c := exec.CommandContext(cctx, profile.Argv[0], profile.Argv[1:]...)
	c.Dir = dir
	c.Env = append(os.Environ(), "SAMSARA_CHECK=1", "SAMSARA_CHECKED_SHA="+sha)
	var buf bytes.Buffer
	c.Stdout, c.Stderr = &buf, &buf
	err := c.Run()
	cr.Finished = a.Clock.Now()
	tail := buf.String()
	if len(tail) > 4000 {
		tail = tail[len(tail)-4000:]
	}
	cr.Summary = tail
	switch {
	case err == nil:
		cr.Result = "PASSED"
	case cctx.Err() != nil:
		cr.Result, cr.Summary = "TIMEOUT", fmt.Sprintf("timeout after %ds\n%s", profile.TimeoutSec, tail)
	default:
		cr.Result = "FAILED"
		if ee, ok := err.(*exec.ExitError); ok {
			cr.ExitCode = ee.ExitCode()
		}
	}
	return cr
}

func (a *App) recordVerification(ctx context.Context, tx *sqlite.Tx, out *sqlite.Output, check checkResult) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO check_runs(id,project_id,subject_kind,subject_id,check_name,checked_sha,command_digest,source_actor,result,exit_code,summary,started_at,finished_at)
		VALUES(?,?,'output',?,?,?,?,'samsara',?,?,?,?,?)`, domain.NewID("chk"), out.ProjectID, out.ID, check.Name, out.SourceSHA, check.CommandDigest,
		check.Result, check.ExitCode, check.Summary, domain.Millis(check.Started), domain.Millis(check.Finished)); err != nil {
		return err
	}
	verdict := "PASSED"
	if check.Result != "PASSED" {
		verdict = "FAILED"
	}
	if _, err := tx.ExecContext(ctx, "UPDATE output_artifacts SET verification=? WHERE id=?", verdict, out.ID); err != nil {
		return err
	}
	att, err := sqlite.GetAttempt(ctx, tx, out.AttemptID)
	if err != nil {
		return err
	}
	if verdict == "FAILED" {
		if err := a.setAttemptState(ctx, tx, att, domain.AttemptFailed, "VERIFICATION_"+check.Result); err != nil {
			return err
		}
		return a.touchRunIssues(ctx, tx, out.ProjectID, out.RunID)
	}
	if err := a.setAttemptState(ctx, tx, att, domain.AttemptVerified, ""); err != nil {
		return err
	}
	p, err := sqlite.GetProject(ctx, tx, out.ProjectID)
	if err != nil {
		return err
	}
	r, err := sqlite.GetRun(ctx, tx, out.RunID)
	if err != nil {
		return err
	}
	ap, err := a.requestApproval(ctx, tx, approvalSpec{Kind: domain.ApprovalResult, ProjectID: p.ID, RunID: r.ID, RunGeneration: r.Generation,
		IssueID: out.IssueID, OutputID: out.ID, PolicyVersion: p.PolicyVersion,
		Subject: map[string]any{"source_sha": out.SourceSHA, "manifest_digest": out.ManifestDigest, "checks": []string{check.Name}}})
	if err != nil {
		return err
	}
	if projectConfig(p).Approval.Result == "auto" {
		if err := a.approveAutomatically(ctx, tx, ap); err != nil {
			return err
		}
	}
	if err := a.touchRunIssues(ctx, tx, out.ProjectID, out.RunID); err != nil {
		return err
	}
	return a.reevaluateRunningAttempts(ctx, tx, out.RunID)
}
