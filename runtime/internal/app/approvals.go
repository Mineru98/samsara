package app

import (
	"context"
	"encoding/json"

	"github.com/Mineru98/samsara/runtime/internal/domain"
	"github.com/Mineru98/samsara/runtime/internal/store/sqlite"
)

// approvalSpec describes an approval subject. The digest binds the decision
// to exact revisions and policy (contract §4.3, R-10).
type approvalSpec struct {
	Kind          string
	ProjectID     string
	RunID         string
	RunGeneration int64
	IssueID       string
	OutputID      string
	CandidateID   string
	CandidateRev  int64
	PolicyVersion int64
	Subject       map[string]any
}

func (s approvalSpec) digest() string {
	subj := map[string]any{"kind": s.Kind, "project_id": s.ProjectID, "run_id": s.RunID, "run_generation": s.RunGeneration,
		"issue_id": s.IssueID, "output_id": s.OutputID, "merge_candidate_id": s.CandidateID, "candidate_revision": s.CandidateRev,
		"policy_version": s.PolicyVersion}
	for k, v := range s.Subject {
		subj[k] = v
	}
	return domain.Digest(subj)
}

// requestApproval returns an existing PENDING/APPROVED approval with the same
// digest or creates a PENDING one.
func (a *App) requestApproval(ctx context.Context, tx *sqlite.Tx, s approvalSpec) (*sqlite.Approval, error) {
	d := s.digest()
	ex, err := sqlite.ApprovalsWhere(ctx, tx, "kind=? AND subject_digest=? AND state IN ('PENDING','APPROVED')", s.Kind, d)
	if err != nil {
		return nil, err
	}
	if len(ex) > 0 {
		return ex[0], nil
	}
	subj, _ := json.Marshal(s.Subject)
	id := domain.NewID("apr")
	nullable := func(v string) any {
		if v == "" {
			return nil
		}
		return v
	}
	var rev any
	if s.CandidateRev > 0 {
		rev = s.CandidateRev
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO approvals(id,project_id,kind,state,subject,subject_digest,run_id,run_generation,issue_id,output_id,merge_candidate_id,candidate_revision,policy_version,requested_at)
		VALUES(?,?,?,'PENDING',?,?,?,?,?,?,?,?,?,?)`, id, s.ProjectID, s.Kind, string(subj), d, nullable(s.RunID), s.RunGeneration,
		nullable(s.IssueID), nullable(s.OutputID), nullable(s.CandidateID), rev, s.PolicyVersion, tx.Now); err != nil {
		return nil, err
	}
	tx.Touch(s.ProjectID, sqlite.KindApproval, id)
	if s.IssueID != "" {
		tx.Touch(s.ProjectID, sqlite.KindIssue, s.IssueID)
	}
	tx.Event(s.ProjectID, "approval.requested", "approval", id, 1)
	return sqlite.GetApproval(ctx, tx, id)
}

// approveAutomatically records a policy approval (approval.* = auto).
func (a *App) approveAutomatically(ctx context.Context, tx *sqlite.Tx, ap *sqlite.Approval) error {
	if ap.State == domain.ApprovalApproved {
		return nil
	}
	if _, err := tx.ExecContext(ctx, "UPDATE approvals SET state='APPROVED', decision_actor='policy:auto', decided_at=?, version=version+1 WHERE id=? AND state='PENDING'", tx.Now, ap.ID); err != nil {
		return err
	}
	tx.Touch(ap.ProjectID, sqlite.KindApproval, ap.ID)
	return a.afterApproval(ctx, tx, ap.ID)
}

// invalidateApproval marks PENDING/APPROVED approvals INVALIDATED.
func (a *App) invalidateApproval(ctx context.Context, tx *sqlite.Tx, ap *sqlite.Approval, reason string) error {
	if ap.State != domain.ApprovalPending && ap.State != domain.ApprovalApproved {
		return nil
	}
	if _, err := tx.ExecContext(ctx, "UPDATE approvals SET state='INVALIDATED', state_reason=?, version=version+1 WHERE id=?", reason, ap.ID); err != nil {
		return err
	}
	tx.Touch(ap.ProjectID, sqlite.KindApproval, ap.ID)
	if ap.IssueID != "" {
		tx.Touch(ap.ProjectID, sqlite.KindIssue, ap.IssueID)
	}
	return tx.Audit(ctx, ap.ProjectID, "runtime", "approval.invalidate", "approval", ap.ID, "", reason, nil)
}

// DecisionRequest is the body of POST /v1/approvals/{id}/decisions.
type DecisionRequest struct {
	Decision      string `json:"decision"` // APPROVE | REJECT | REVOKE
	SubjectDigest string `json:"subject_digest"`
	Comment       string `json:"comment"`
}

// CommandResult is the generic accepted-command response (contract §6.3).
type CommandResult struct {
	CommandID       string `json:"command_id"`
	OperationID     string `json:"operation_id,omitempty"`
	Status          string `json:"status"`
	ResourceVersion string `json:"resource_version"`
	ProjectVersion  string `json:"project_version"`
}

// DecideApproval records a decision against the exact subject digest. A
// decision on a changed subject is refused (APPROVAL_SUBJECT_CHANGED). The
// decision never issues a lease directly: the scheduler re-checks all
// conditions afterwards.
func (a *App) DecideApproval(ctx context.Context, cmd Cmd, approvalID string, req DecisionRequest) (*CommandResult, error) {
	if err := a.requireReady(); err != nil {
		return nil, err
	}
	var out CommandResult
	err := a.DB.Write(ctx, func(tx *sqlite.Tx) error {
		return a.idempotent(ctx, tx, "approval.decide:"+approvalID, cmd, req, &out, func() (any, error) {
			ap, err := sqlite.GetApproval(ctx, tx, approvalID)
			if err != nil {
				return nil, err
			}
			if err := checkIfMatch(cmd, ap.Version); err != nil {
				return nil, err
			}
			if req.SubjectDigest != "" && req.SubjectDigest != ap.SubjectDigest {
				return nil, domain.Errf(domain.CodeApprovalSubjectChanged, "approval subject changed").
					WithDetails("refresh_resource", "/v1/approvals/"+ap.ID)
			}
			if stale, reason, err := a.approvalStale(ctx, tx, ap); err != nil {
				return nil, err
			} else if stale {
				if err := a.invalidateApproval(ctx, tx, ap, reason); err != nil {
					return nil, err
				}
				return nil, domain.Errf(domain.CodeApprovalSubjectChanged, "approval subject is no longer current: %s", reason)
			}
			var newState string
			switch req.Decision {
			case "APPROVE":
				if ap.State != domain.ApprovalPending {
					return nil, domain.Errf(domain.CodeConflict, "approval is %s", ap.State)
				}
				newState = domain.ApprovalApproved
			case "REJECT":
				if ap.State != domain.ApprovalPending {
					return nil, domain.Errf(domain.CodeConflict, "approval is %s", ap.State)
				}
				newState = domain.ApprovalRejected
			case "REVOKE":
				if ap.State != domain.ApprovalApproved {
					return nil, domain.Errf(domain.CodeConflict, "only APPROVED approvals can be revoked")
				}
				newState = domain.ApprovalInvalidated
			default:
				return nil, domain.Errf(domain.CodeInvalidRequest, "decision must be APPROVE, REJECT or REVOKE")
			}
			if _, err := tx.ExecContext(ctx, "UPDATE approvals SET state=?, state_reason=?, decision_actor=?, decision_comment=?, decided_at=?, version=version+1 WHERE id=?",
				newState, "DECIDED_"+req.Decision, cmd.Principal.ID, req.Comment, tx.Now, ap.ID); err != nil {
				return nil, err
			}
			tx.Touch(ap.ProjectID, sqlite.KindApproval, ap.ID)
			if ap.IssueID != "" {
				tx.Touch(ap.ProjectID, sqlite.KindIssue, ap.IssueID)
			}
			tx.Event(ap.ProjectID, "approval.decided", "approval", ap.ID, ap.Version+1)
			if err := tx.Audit(ctx, ap.ProjectID, cmd.Principal.ID, "approval.decide", "approval", ap.ID, req.Decision, "", map[string]string{"comment": req.Comment, "subject_digest": ap.SubjectDigest}); err != nil {
				return nil, err
			}
			if newState == domain.ApprovalApproved {
				if err := a.afterApproval(ctx, tx, ap.ID); err != nil {
					return nil, err
				}
			}
			if ap.RunID != "" {
				if err := a.reevaluateRunningAttempts(ctx, tx, ap.RunID); err != nil {
					return nil, err
				}
				if err := a.touchRunIssues(ctx, tx, ap.ProjectID, ap.RunID); err != nil {
					return nil, err
				}
			}
			p, err := sqlite.GetProject(ctx, tx, ap.ProjectID)
			if err != nil {
				return nil, err
			}
			return &CommandResult{CommandID: domain.NewID("cmd"), Status: "ACCEPTED", ResourceVersion: domain.Dec(ap.Version + 1), ProjectVersion: domain.Dec(p.ProjectVersion + 1)}, nil
		})
	})
	if err != nil {
		return nil, err
	}
	a.Wake()
	return &out, nil
}

// approvalStale checks whether the approval subject still matches current
// revisions (output current, candidate revision current, run generation).
func (a *App) approvalStale(ctx context.Context, q sqlite.Q, ap *sqlite.Approval) (bool, string, error) {
	if ap.RunID != "" {
		r, err := sqlite.GetRun(ctx, q, ap.RunID)
		if err != nil {
			return false, "", err
		}
		if r.Generation != ap.RunGeneration {
			return true, "RUN_GENERATION_CHANGED", nil
		}
		if r.State == domain.RunCancelled || r.State == domain.RunFailed {
			return true, "RUN_" + r.State, nil
		}
	}
	if ap.OutputID != "" {
		o, err := sqlite.GetOutput(ctx, q, ap.OutputID)
		if err != nil {
			return false, "", err
		}
		if o.SupersededBy != "" {
			return true, "OUTPUT_SUPERSEDED", nil
		}
	}
	if ap.Kind == domain.ApprovalMerge && ap.CandidateID != "" {
		c, err := sqlite.GetCandidate(ctx, q, ap.CandidateID)
		if err != nil {
			return false, "", err
		}
		if c.Revision != ap.CandidateRevision {
			return true, "CANDIDATE_REVISION_CHANGED", nil
		}
		var subj map[string]any
		_ = json.Unmarshal([]byte(ap.Subject), &subj)
		if subj["candidate_sha"] != c.CandidateSHA || subj["base_sha"] != c.BaseSHA {
			return true, "CANDIDATE_CHANGED", nil
		}
	}
	return false, "", nil
}

// afterApproval runs the effects of an APPROVED decision in the same tx.
func (a *App) afterApproval(ctx context.Context, tx *sqlite.Tx, approvalID string) error {
	ap, err := sqlite.GetApproval(ctx, tx, approvalID)
	if err != nil {
		return err
	}
	switch ap.Kind {
	case domain.ApprovalResult:
		return a.createCandidate(ctx, tx, ap.OutputID)
	}
	return nil
}

// reevaluateRunningAttempts pauses RUNNING attempts of a run whose execution
// prerequisites are no longer met (F-REVOKE). Only affected attempts change;
// independent work continues (POL-02).
func (a *App) reevaluateRunningAttempts(ctx context.Context, tx *sqlite.Tx, runID string) error {
	atts, err := sqlite.AttemptsWhere(ctx, tx, "run_id=? AND state IN ('STARTING','RUNNING')", runID)
	if err != nil {
		return err
	}
	for _, att := range atts {
		bl, err := sqlite.ExecutionBlockers(ctx, tx, runID, att.IssueID)
		if err != nil {
			return err
		}
		if len(bl) == 0 {
			continue
		}
		if err := a.pauseAttempt(ctx, tx, att, "DEPENDENCY_INVALIDATED"); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) touchRunIssues(ctx context.Context, tx *sqlite.Tx, projectID, runID string) error {
	issues, err := sqlite.IssuesOfRun(ctx, tx, runID)
	if err != nil {
		return err
	}
	for _, i := range issues {
		tx.Touch(projectID, sqlite.KindIssue, i.ID)
	}
	edges, err := sqlite.EdgesOfRun(ctx, tx, runID)
	if err != nil {
		return err
	}
	for _, e := range edges {
		tx.Touch(projectID, sqlite.KindDependency, e.ID)
	}
	return nil
}
