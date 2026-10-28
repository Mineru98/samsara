package sqlite

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Mineru98/samsara/runtime/internal/domain"
)

// Wire projections. Counters are decimal strings, times RFC 3339 UTC.

type ProjectView struct {
	ID             string `json:"id"`
	Version        string `json:"version"`
	RootPath       string `json:"root_path"`
	DisplayName    string `json:"display_name"`
	Activation     string `json:"activation"`
	RegistryState  string `json:"registry_state"`
	DrainState     string `json:"drain_state"`
	PolicyVersion  string `json:"policy_version"`
	GraphVersion   string `json:"graph_version"`
	ProjectVersion string `json:"project_version"`
	LastActivityAt string `json:"last_activity_at"`
	CreatedAt      string `json:"created_at"`
}

type ProjectSummary struct {
	ProjectView
	Running            int    `json:"running"`
	Blocked            int    `json:"blocked"`
	ApprovalPending    int    `json:"approval_pending"`
	IntegrationPending int    `json:"integration_pending"`
	LatestRunID        string `json:"latest_run_id,omitempty"`
	LatestRunState     string `json:"latest_run_state,omitempty"`
}

type IssueView struct {
	ID                   string           `json:"id"`
	ProjectID            string           `json:"project_id"`
	Version              string           `json:"version"`
	ClientKey            string           `json:"client_key"`
	Title                string           `json:"title"`
	Lifecycle            string           `json:"lifecycle"`
	AcceptanceCriteria   []string         `json:"acceptance_criteria"`
	CheckProfile         string           `json:"check_profile"`
	RequiredCapabilities []string         `json:"required_capabilities"`
	RunID                string           `json:"run_id,omitempty"`
	CurrentAttemptID     string           `json:"current_attempt_id,omitempty"`
	ExecutionState       string           `json:"execution_state,omitempty"`
	IntegrationState     string           `json:"integration_state,omitempty"`
	DisplayStatus        string           `json:"display_status"`
	BlockingReasons      []BlockingReason `json:"blocking_reasons"`
	AvailableActions     []Action         `json:"available_actions"`
	ResumeRequired       bool             `json:"resume_required"`
}

type Action struct {
	Action     string `json:"action"`
	Enabled    bool   `json:"enabled"`
	ResourceID string `json:"resource_id,omitempty"`
	ReasonCode string `json:"reason_code,omitempty"`
}

type RunView struct {
	ID            string            `json:"id"`
	ProjectID     string            `json:"project_id"`
	Version       string            `json:"version"`
	State         string            `json:"state"`
	RunGeneration string            `json:"run_generation"`
	GraphVersion  string            `json:"graph_version"`
	PlanDigest    string            `json:"plan_digest"`
	AliasMapping  map[string]string `json:"alias_mapping"`
	CreatedAt     string            `json:"created_at"`
	StartedAt     string            `json:"started_at,omitempty"`
	EndedAt       string            `json:"ended_at,omitempty"`
}

type AttemptView struct {
	ID               string `json:"id"`
	ProjectID        string `json:"project_id"`
	RunID            string `json:"run_id"`
	IssueID          string `json:"issue_id"`
	Version          string `json:"version"`
	State            string `json:"state"`
	StateReason      string `json:"state_reason,omitempty"`
	QuarantineReason string `json:"quarantine_reason,omitempty"`
	RunGeneration    string `json:"run_generation"`
	WorktreeID       string `json:"worktree_id,omitempty"`
	BackendKind      string `json:"backend_kind,omitempty"`
	BackendReason    string `json:"backend_reason,omitempty"`
	OutputID         string `json:"output_id,omitempty"`
	ResumeRequired   bool   `json:"resume_required"`
	SessionID        string `json:"session_id,omitempty"`
	LeaseState       string `json:"lease_state,omitempty"`
	LeaseFence       string `json:"lease_fence,omitempty"`
	LeaseDeadlineAt  string `json:"lease_deadline_at,omitempty"`
	UpdatedAt        string `json:"updated_at"`
}

type SessionView struct {
	ID              string            `json:"id"`
	ProjectID       string            `json:"project_id"`
	Version         string            `json:"version"`
	ClaudeSessionID string            `json:"claude_session_id"`
	Managed         bool              `json:"managed"`
	DesiredState    string            `json:"desired_state"`
	Liveness        string            `json:"liveness"`
	InputState      string            `json:"input_state"`
	InputOwner      string            `json:"input_owner"`
	Source          string            `json:"source"`
	BackendRawState string            `json:"backend_raw_state,omitempty"`
	Confidence      string            `json:"confidence"`
	ObservedAt      string            `json:"observed_at"`
	LastHookAt      string            `json:"last_hook_at,omitempty"`
	EndedAt         string            `json:"ended_at,omitempty"`
	AttemptID       string            `json:"attempt_id,omitempty"`
	Backend         *BackendLocation  `json:"backend,omitempty"`
	Process         map[string]string `json:"process,omitempty"`
}

type BackendLocation struct {
	BindingID  string          `json:"binding_id"`
	Kind       string          `json:"kind"`
	Instance   string          `json:"instance,omitempty"`
	Ref        json.RawMessage `json:"ref"`
	Generation string          `json:"generation"`
}

type WorktreeView struct {
	ID           string `json:"id"`
	ProjectID    string `json:"project_id"`
	Version      string `json:"version"`
	RepositoryID string `json:"repository_id,omitempty"`
	AttemptID    string `json:"attempt_id,omitempty"`
	Path         string `json:"path"`
	BranchRef    string `json:"branch_ref"`
	BaseSHA      string `json:"base_sha"`
	Ownership    string `json:"ownership"`
	State        string `json:"state"`
	StateReason  string `json:"state_reason,omitempty"`
}

type DependencyView struct {
	ID                    string `json:"dependency_id"`
	ProjectID             string `json:"project_id"`
	RunID                 string `json:"run_id"`
	PredecessorIssueID    string `json:"predecessor_issue_id"`
	SuccessorIssueID      string `json:"successor_issue_id"`
	RequiredMilestone     string `json:"required_milestone"`
	DownstreamPhase       string `json:"downstream_phase"`
	RequiredRunGeneration string `json:"required_run_generation"`
	RequiredOutputID      any    `json:"required_output_id"`
	TargetRepositoryID    string `json:"target_repository_id,omitempty"`
	TargetRef             string `json:"target_ref,omitempty"`
	GraphVersion          string `json:"graph_version"`
	InvalidationPolicy    string `json:"invalidation_policy"`
	Version               string `json:"version"`
	Met                   bool   `json:"met"`
}

type ApprovalView struct {
	ID                string          `json:"id"`
	ProjectID         string          `json:"project_id"`
	Version           string          `json:"version"`
	Kind              string          `json:"kind"`
	State             string          `json:"state"`
	StateReason       string          `json:"state_reason,omitempty"`
	SubjectDigest     string          `json:"subject_digest"`
	Subject           json.RawMessage `json:"subject"`
	RunID             string          `json:"run_id,omitempty"`
	IssueID           string          `json:"issue_id,omitempty"`
	OutputID          string          `json:"output_id,omitempty"`
	MergeCandidateID  string          `json:"merge_candidate_id,omitempty"`
	CandidateRevision string          `json:"candidate_revision,omitempty"`
	PolicyVersion     string          `json:"policy_version"`
	RequestedAt       string          `json:"requested_at"`
	DecidedAt         string          `json:"decided_at,omitempty"`
	DecisionActor     string          `json:"decision_actor,omitempty"`
}

type CandidateView struct {
	ID                     string           `json:"id"`
	ProjectID              string           `json:"project_id"`
	Version                string           `json:"version"`
	IssueID                string           `json:"issue_id"`
	RunID                  string           `json:"run_id"`
	AttemptID              string           `json:"attempt_id"`
	OutputID               string           `json:"output_id"`
	TargetID               string           `json:"target_id"`
	TargetRef              string           `json:"target_ref"`
	Authority              string           `json:"authority"`
	SourceSHA              string           `json:"source_sha"`
	BaseSHA                string           `json:"base_sha,omitempty"`
	CandidateSHA           string           `json:"candidate_sha,omitempty"`
	Revision               string           `json:"revision"`
	State                  string           `json:"state"`
	StateReason            string           `json:"state_reason,omitempty"`
	NativeQueueState       string           `json:"native_queue_state"`
	PublicationObservation string           `json:"publication_observation,omitempty"`
	Enqueued               bool             `json:"enqueued"`
	QueuePosition          string           `json:"queue_position"`
	ApprovalGate           *ApprovalGate    `json:"approval_gate"`
	BlockingReasons        []BlockingReason `json:"blocking_reasons"`
	EvidenceID             string           `json:"evidence_id,omitempty"`
}

type ApprovalGate struct {
	Required      bool     `json:"required"`
	State         any      `json:"state"`
	ApprovalIDs   []string `json:"approval_ids"`
	SubjectDigest string   `json:"subject_digest,omitempty"`
}

type TargetView struct {
	ID            string `json:"id"`
	RepositoryID  string `json:"repository_id"`
	TargetRef     string `json:"target_ref"`
	Authority     string `json:"authority"`
	PolicyVersion string `json:"policy_version"`
	Version       string `json:"version"`
}

type OperationView struct {
	ID          string          `json:"id"`
	ProjectID   string          `json:"project_id,omitempty"`
	Kind        string          `json:"kind"`
	State       string          `json:"state"`
	ResourceKey string          `json:"resource_key,omitempty"`
	CommandID   string          `json:"command_id,omitempty"`
	Result      json.RawMessage `json:"result"`
	Error       string          `json:"error,omitempty"`
	CreatedAt   string          `json:"created_at"`
	UpdatedAt   string          `json:"updated_at"`
}

// LoadProjection loads the wire projection of an entity, or nil when it no
// longer exists.
func LoadProjection(ctx context.Context, q Q, kind, id string, now int64) (any, error) {
	var v any
	var err error
	switch kind {
	case KindProject:
		var p *Project
		p, err = GetProject(ctx, q, id)
		if err == nil {
			v = projectView(p)
		}
	case KindIssue:
		v, err = IssueProjection(ctx, q, id)
	case KindRun:
		var r *Run
		r, err = GetRun(ctx, q, id)
		if err == nil {
			v = RunProjection(r)
		}
	case KindAttempt:
		v, err = AttemptProjection(ctx, q, id)
	case KindSession:
		v, err = SessionProjection(ctx, q, id)
	case KindWorktree:
		var w *Worktree
		w, err = GetWorktree(ctx, q, id)
		if err == nil {
			v = WorktreeProjection(w)
		}
	case KindDependency:
		v, err = DependencyProjection(ctx, q, id)
	case KindGate:
		v, err = gateByID(ctx, q, id, now)
	case KindApproval:
		var a *Approval
		a, err = GetApproval(ctx, q, id)
		if err == nil {
			v = ApprovalProjection(a)
		}
	case KindCandidate:
		v, err = CandidateProjection(ctx, q, id)
	case KindTarget:
		var t *Target
		t, err = GetTarget(ctx, q, id)
		if err == nil {
			v = TargetProjection(t)
		}
	case KindOperation:
		v, err = OperationProjection(ctx, q, id)
	default:
		return nil, nil
	}
	if domain.IsCode(err, domain.CodeNotFound) {
		return nil, nil
	}
	return v, err
}

func projectView(p *Project) ProjectView {
	return ProjectView{ID: p.ID, Version: domain.Dec(p.Version), RootPath: p.RootPath, DisplayName: p.DisplayName,
		Activation: p.Activation, RegistryState: p.RegistryState, DrainState: p.DrainState,
		PolicyVersion: domain.Dec(p.PolicyVersion), GraphVersion: domain.Dec(p.GraphVersion),
		ProjectVersion: domain.Dec(p.ProjectVersion), LastActivityAt: domain.RFC3339(p.LastActivityAt), CreatedAt: domain.RFC3339(p.CreatedAt)}
}

// ProjectProjection exposes the project view.
func ProjectProjection(p *Project) ProjectView { return projectView(p) }

// LoadProjectSummary builds the sidebar summary for one project.
func LoadProjectSummary(ctx context.Context, q Q, projectID string, now int64) (*ProjectSummary, error) {
	p, err := GetProject(ctx, q, projectID)
	if err != nil {
		return nil, err
	}
	s := &ProjectSummary{ProjectView: projectView(p)}
	if err := q.QueryRowContext(ctx, "SELECT COUNT(*) FROM execution_attempts WHERE project_id=? AND state IN ('STARTING','RUNNING','PAUSING')", projectID).Scan(&s.Running); err != nil {
		return nil, err
	}
	if err := q.QueryRowContext(ctx, "SELECT COUNT(*) FROM approvals WHERE project_id=? AND state='PENDING'", projectID).Scan(&s.ApprovalPending); err != nil {
		return nil, err
	}
	if err := q.QueryRowContext(ctx, "SELECT COUNT(*) FROM merge_candidates WHERE project_id=? AND state NOT IN ('MERGED','CANCELLED','FAILED')", projectID).Scan(&s.IntegrationPending); err != nil {
		return nil, err
	}
	runs, err := ListRuns(ctx, q, projectID)
	if err != nil {
		return nil, err
	}
	if len(runs) > 0 {
		s.LatestRunID, s.LatestRunState = runs[0].ID, runs[0].State
		if runs[0].State == domain.RunActive || runs[0].State == domain.RunPaused {
			issues, err := IssuesOfRun(ctx, q, runs[0].ID)
			if err != nil {
				return nil, err
			}
			for _, i := range issues {
				iv, err := IssueProjection(ctx, q, i.ID)
				if err != nil {
					return nil, err
				}
				if iv.DisplayStatus == "BLOCKED" {
					s.Blocked++
				}
			}
		}
	}
	return s, nil
}

// RunProjection builds the run view.
func RunProjection(r *Run) RunView {
	m := map[string]string{}
	_ = json.Unmarshal([]byte(r.AliasMapping), &m)
	return RunView{ID: r.ID, ProjectID: r.ProjectID, Version: domain.Dec(r.Version), State: r.State,
		RunGeneration: domain.Dec(r.Generation), GraphVersion: domain.Dec(r.GraphVersion), PlanDigest: r.PlanDigest,
		AliasMapping: m, CreatedAt: domain.RFC3339(r.CreatedAt), StartedAt: domain.RFC3339(r.StartedAt), EndedAt: domain.RFC3339(r.EndedAt)}
}

// latestRunOfIssue returns the newest run containing the issue.
func latestRunOfIssue(ctx context.Context, q Q, issueID string) (*Run, error) {
	var runID string
	err := q.QueryRowContext(ctx, "SELECT r.id FROM runs r JOIN run_issues ri ON ri.run_id=r.id WHERE ri.issue_id=? ORDER BY r.created_at DESC LIMIT 1", issueID).Scan(&runID)
	if err != nil {
		return nil, nil
	}
	return GetRun(ctx, q, runID)
}

// IssueProjection computes the server-side issue projection including
// display status, blocking reasons and available actions (contract §8.2).
func IssueProjection(ctx context.Context, q Q, issueID string) (*IssueView, error) {
	i, err := GetIssue(ctx, q, issueID)
	if err != nil {
		return nil, err
	}
	v := &IssueView{ID: i.ID, ProjectID: i.ProjectID, Version: domain.Dec(i.Version), ClientKey: i.ClientKey, Title: i.Title,
		Lifecycle: i.Lifecycle, CheckProfile: i.CheckProfile, BlockingReasons: []BlockingReason{}, AvailableActions: []Action{}}
	_ = json.Unmarshal([]byte(i.AcceptanceCriteria), &v.AcceptanceCriteria)
	_ = json.Unmarshal([]byte(i.RequiredCapabilities), &v.RequiredCapabilities)
	run, err := latestRunOfIssue(ctx, q, i.ID)
	if err != nil {
		return nil, err
	}
	switch i.Lifecycle {
	case domain.IssueCompleted:
		v.DisplayStatus = "COMPLETED"
	case domain.IssueCancelled:
		v.DisplayStatus = "CANCELLED"
	}
	if run == nil {
		if v.DisplayStatus == "" {
			v.DisplayStatus = "UNPLANNED"
		}
		return v, nil
	}
	v.RunID = run.ID
	att, err := LatestAttempt(ctx, q, run.ID, i.ID)
	if err != nil {
		return nil, err
	}
	if att != nil {
		v.CurrentAttemptID, v.ExecutionState, v.ResumeRequired = att.ID, att.State, att.ResumeRequired
	}
	cands, err := CandidatesWhere(ctx, q, "issue_id=? AND run_id=?", i.ID, run.ID)
	if err != nil {
		return nil, err
	}
	if len(cands) > 0 {
		v.IntegrationState = cands[len(cands)-1].State
	}
	exec, err := ExecutionBlockers(ctx, q, run.ID, i.ID)
	if err != nil {
		return nil, err
	}
	pendingApprovals, err := ApprovalsWhere(ctx, q, "issue_id=? AND state='PENDING'", i.ID)
	if err != nil {
		return nil, err
	}
	started := att != nil && att.State != domain.AttemptPending
	if !started {
		v.BlockingReasons = append(v.BlockingReasons, exec...)
	} else if att != nil && (att.State == domain.AttemptPausing || att.State == domain.AttemptPaused) {
		v.BlockingReasons = append(v.BlockingReasons, exec...)
	}
	if att != nil && att.StateReason == "MANUAL_ATTACH_REQUIRED" && (att.State == domain.AttemptPending || att.State == domain.AttemptPaused) {
		// Runnable, but the user must continue in their own terminal (§6.3).
		v.ResumeRequired = true
		v.AvailableActions = append(v.AvailableActions, Action{Action: "ATTACH_SESSION", Enabled: len(exec) == 0, ResourceID: att.WorktreeID})
	} else if att != nil && att.State == domain.AttemptPending && att.StateReason != "" && att.StateReason != "QUEUED" {
		v.BlockingReasons = append(v.BlockingReasons, BlockingReason{Code: att.StateReason, Message: pendingMessage(att.StateReason)})
	}
	if att != nil && att.QuarantineReason != "" {
		v.BlockingReasons = append(v.BlockingReasons, BlockingReason{Code: att.QuarantineReason, Message: "세션 생존·소유권 확인 전까지 실행권을 보류합니다."})
	}
	if len(cands) > 0 {
		c := cands[len(cands)-1]
		if c.State == domain.CandidateWaiting || c.State == domain.CandidateStaleBase {
			ib, err := IntegrationBlockers(ctx, q, run.ID, i.ID)
			if err != nil {
				return nil, err
			}
			v.BlockingReasons = append(v.BlockingReasons, ib...)
		}
	}
	for _, a := range pendingApprovals {
		v.AvailableActions = append(v.AvailableActions, Action{Action: "OPEN_APPROVAL", Enabled: true, ResourceID: a.ID})
	}
	// Blocking approvals of predecessors are also openable from the successor.
	for _, b := range v.BlockingReasons {
		if b.ApprovalID != "" {
			v.AvailableActions = append(v.AvailableActions, Action{Action: "OPEN_APPROVAL", Enabled: true, ResourceID: b.ApprovalID})
		}
	}
	resume := Action{Action: "RESUME", Enabled: false}
	if att != nil && att.State == domain.AttemptPaused {
		if len(exec) == 0 && run.State == domain.RunActive {
			resume.Enabled = true
		} else if len(exec) > 0 {
			resume.ReasonCode = domain.CodeDependencyUnmet
		} else {
			resume.ReasonCode = domain.ReasonRunNotActive
		}
		v.AvailableActions = append(v.AvailableActions, resume)
	}
	if v.DisplayStatus == "" {
		v.DisplayStatus = displayStatus(run, att, cands, v.BlockingReasons, len(pendingApprovals) > 0)
	}
	return v, nil
}

func pendingMessage(code string) string {
	switch code {
	case "MANUAL_ATTACH_REQUIRED":
		return "실행 가능합니다. 터미널에서 Claude 세션을 연결(samsara session attach)해 재개해야 합니다."
	case "BACKEND_UNAVAILABLE":
		return "요청한 실행 환경을 사용할 수 없습니다."
	case "CAPACITY":
		return "동시 실행 한도에 도달했습니다."
	}
	return code
}

func displayStatus(run *Run, att *Attempt, cands []*Candidate, blockers []BlockingReason, approvalPending bool) string {
	if att != nil && att.QuarantineReason != "" {
		return "ATTENTION"
	}
	if att != nil {
		switch att.State {
		case domain.AttemptStarting:
			return "STARTING"
		case domain.AttemptRunning:
			return "RUNNING"
		case domain.AttemptPausing, domain.AttemptPaused:
			return "PAUSED"
		case domain.AttemptCancelRequested:
			return "CANCELLING"
		case domain.AttemptImplemented, domain.AttemptVerifying:
			return "VERIFYING"
		case domain.AttemptFailed:
			return "FAILED"
		case domain.AttemptLost:
			return "ATTENTION"
		case domain.AttemptCancelled:
			return "CANCELLED"
		}
	}
	if len(cands) > 0 {
		c := cands[len(cands)-1]
		switch c.State {
		case domain.CandidateMerged:
			return "INTEGRATED"
		case domain.CandidateConflict, domain.CandidateFailed:
			return "ATTENTION"
		case domain.CandidateCancelled:
		default:
			if len(blockers) > 0 {
				return "BLOCKED"
			}
			return "INTEGRATING"
		}
	}
	if approvalPending {
		return "APPROVAL_PENDING"
	}
	if att != nil && att.State == domain.AttemptVerified {
		return "VERIFIED"
	}
	if len(blockers) > 0 {
		return "BLOCKED"
	}
	if run.State == domain.RunPlanned {
		return "PLANNED"
	}
	if att != nil && att.State == domain.AttemptPending {
		return "READY"
	}
	if run.State == domain.RunActive {
		return "READY"
	}
	return "IDLE"
}

// AttemptProjection builds the attempt view with lease summary.
func AttemptProjection(ctx context.Context, q Q, id string) (*AttemptView, error) {
	a, err := GetAttempt(ctx, q, id)
	if err != nil {
		return nil, err
	}
	v := &AttemptView{ID: a.ID, ProjectID: a.ProjectID, RunID: a.RunID, IssueID: a.IssueID, Version: domain.Dec(a.Version),
		State: a.State, StateReason: a.StateReason, QuarantineReason: a.QuarantineReason, RunGeneration: domain.Dec(a.RunGeneration),
		WorktreeID: a.WorktreeID, BackendKind: a.BackendKind, BackendReason: a.BackendReason, OutputID: a.OutputID,
		ResumeRequired: a.ResumeRequired, UpdatedAt: domain.RFC3339(a.UpdatedAt)}
	l, err := LatestLeaseForAttempt(ctx, q, a.ID)
	if err != nil {
		return nil, err
	}
	if l != nil {
		v.LeaseState, v.LeaseFence, v.LeaseDeadlineAt = l.State, domain.Dec(l.Fence), domain.RFC3339(l.DeadlineAt)
	}
	as, err := ActiveAssignmentForAttempt(ctx, q, a.ID)
	if err != nil {
		return nil, err
	}
	if as != nil {
		v.SessionID = as.SessionID
	}
	return v, nil
}

// SessionProjection builds the session view with backend location.
func SessionProjection(ctx context.Context, q Q, id string) (*SessionView, error) {
	s, err := GetSession(ctx, q, id)
	if err != nil {
		return nil, err
	}
	v := &SessionView{ID: s.ID, ProjectID: s.ProjectID, Version: domain.Dec(s.Version), ClaudeSessionID: s.ClaudeSessionID,
		Managed: s.Managed, DesiredState: s.DesiredState, Liveness: s.Liveness, InputState: s.InputState, InputOwner: s.InputOwner,
		Source: s.Source, BackendRawState: s.BackendRawState, Confidence: s.Confidence, ObservedAt: domain.RFC3339(s.ObservedAt),
		LastHookAt: domain.RFC3339(s.LastHookAt), EndedAt: domain.RFC3339(s.EndedAt)}
	as, err := ActiveAssignmentForSession(ctx, q, s.ID)
	if err != nil {
		return nil, err
	}
	if as != nil {
		v.AttemptID = as.AttemptID
	}
	b, err := CurrentBindingForSession(ctx, q, s.ID)
	if err != nil {
		return nil, err
	}
	if b != nil {
		v.Backend = &BackendLocation{BindingID: b.ID, Kind: b.Kind, Instance: b.Instance, Ref: json.RawMessage(b.OpaqueRef), Generation: b.Generation}
	}
	return v, nil
}

// WorktreeProjection builds the worktree view.
func WorktreeProjection(w *Worktree) WorktreeView {
	return WorktreeView{ID: w.ID, ProjectID: w.ProjectID, Version: domain.Dec(w.Version), RepositoryID: w.RepositoryID,
		AttemptID: w.AttemptID, Path: w.Path, BranchRef: w.BranchRef, BaseSHA: w.BaseSHA, Ownership: w.Ownership,
		State: w.State, StateReason: w.StateReason}
}

// DependencyProjection builds the dependency view with current evaluation.
func DependencyProjection(ctx context.Context, q Q, id string) (*DependencyView, error) {
	e, err := GetEdge(ctx, q, id)
	if err != nil {
		return nil, err
	}
	r, err := EvaluateEdge(ctx, q, e)
	if err != nil {
		return nil, err
	}
	var req any
	if e.RequiredOutputID != "" {
		req = e.RequiredOutputID
	}
	return &DependencyView{ID: e.ID, ProjectID: e.ProjectID, RunID: e.RunID, PredecessorIssueID: e.Predecessor,
		SuccessorIssueID: e.Successor, RequiredMilestone: e.Milestone, DownstreamPhase: e.Phase,
		RequiredRunGeneration: domain.Dec(e.RequiredGeneration), RequiredOutputID: req, TargetRepositoryID: e.TargetRepositoryID,
		TargetRef: e.TargetRef, GraphVersion: domain.Dec(e.GraphVersion), InvalidationPolicy: e.Invalidation,
		Version: domain.Dec(e.Version), Met: r.Met}, nil
}

// ApprovalProjection builds the approval view.
func ApprovalProjection(a *Approval) ApprovalView {
	v := ApprovalView{ID: a.ID, ProjectID: a.ProjectID, Version: domain.Dec(a.Version), Kind: a.Kind, State: a.State,
		StateReason: a.StateReason, SubjectDigest: a.SubjectDigest, Subject: json.RawMessage(a.Subject), RunID: a.RunID,
		IssueID: a.IssueID, OutputID: a.OutputID, MergeCandidateID: a.CandidateID, PolicyVersion: domain.Dec(a.PolicyVersion),
		RequestedAt: domain.RFC3339(a.RequestedAt), DecidedAt: domain.RFC3339(a.DecidedAt), DecisionActor: a.DecisionActor}
	if a.CandidateRevision > 0 {
		v.CandidateRevision = domain.Dec(a.CandidateRevision)
	}
	return v
}

// CandidateProjection builds the merge candidate view.
func CandidateProjection(ctx context.Context, q Q, id string) (*CandidateView, error) {
	c, err := GetCandidate(ctx, q, id)
	if err != nil {
		return nil, err
	}
	t, err := GetTarget(ctx, q, c.TargetID)
	if err != nil {
		return nil, err
	}
	v := &CandidateView{ID: c.ID, ProjectID: c.ProjectID, Version: domain.Dec(c.Version), IssueID: c.IssueID, RunID: c.RunID,
		AttemptID: c.AttemptID, OutputID: c.OutputID, TargetID: c.TargetID, TargetRef: t.TargetRef, Authority: t.Authority,
		SourceSHA: c.SourceSHA, BaseSHA: c.BaseSHA, CandidateSHA: c.CandidateSHA, Revision: domain.Dec(c.Revision),
		State: c.State, StateReason: c.StateReason, NativeQueueState: c.NativeQueueState,
		PublicationObservation: c.PublicationObservation, Enqueued: c.Enqueued, QueuePosition: domain.Dec(c.QueuePosition),
		BlockingReasons: []BlockingReason{}}
	apps, err := ApprovalsWhere(ctx, q, "kind='MERGE' AND merge_candidate_id=? AND candidate_revision=?", c.ID, c.Revision)
	if err != nil {
		return nil, err
	}
	var p *Project
	if p, err = GetProject(ctx, q, c.ProjectID); err != nil {
		return nil, err
	}
	required := mergeApprovalRequired(p)
	if required {
		g := &ApprovalGate{Required: true, ApprovalIDs: []string{}}
		if len(apps) > 0 {
			g.State, g.SubjectDigest = apps[0].State, apps[0].SubjectDigest
			for _, a := range apps {
				g.ApprovalIDs = append(g.ApprovalIDs, a.ID)
			}
		}
		v.ApprovalGate = g
	}
	ib, err := IntegrationBlockers(ctx, q, c.RunID, c.IssueID)
	if err != nil {
		return nil, err
	}
	v.BlockingReasons = append(v.BlockingReasons, ib...)
	if c.State == domain.CandidateMerged {
		var evID string
		_ = q.QueryRowContext(ctx, "SELECT id FROM integration_evidence WHERE candidate_id=? LIMIT 1", c.ID).Scan(&evID)
		v.EvidenceID = evID
	}
	return v, nil
}

func mergeApprovalRequired(p *Project) bool {
	var cfg struct {
		Approval struct {
			Integration string `json:"integration"`
		} `json:"approval"`
	}
	_ = json.Unmarshal([]byte(p.ConfigJSON), &cfg)
	return cfg.Approval.Integration != "auto"
}

// TargetProjection builds the integration target view.
func TargetProjection(t *Target) TargetView {
	return TargetView{ID: t.ID, RepositoryID: t.RepositoryID, TargetRef: t.TargetRef, Authority: t.Authority,
		PolicyVersion: domain.Dec(t.PolicyVersion), Version: domain.Dec(t.Version)}
}

// OperationProjection builds an operation view.
func OperationProjection(ctx context.Context, q Q, id string) (*OperationView, error) {
	v := &OperationView{}
	var pid, cmd, result string
	var created, updated int64
	err := q.QueryRowContext(ctx, "SELECT id,COALESCE(project_id,''),kind,state,resource_key,COALESCE(command_id,''),result,error,created_at,updated_at FROM operations WHERE id=?", id).
		Scan(&v.ID, &pid, &v.Kind, &v.State, &v.ResourceKey, &cmd, &result, &v.Error, &created, &updated)
	if err != nil {
		return nil, notFound(err, "operation", id)
	}
	v.ProjectID, v.CommandID, v.Result = pid, cmd, json.RawMessage(result)
	v.CreatedAt, v.UpdatedAt = domain.RFC3339(created), domain.RFC3339(updated)
	return v, nil
}

func gateByID(ctx context.Context, q Q, id string, now int64) (any, error) {
	// Gate ids are derived; find by scanning active runs of any project.
	rows, err := q.QueryContext(ctx, "SELECT id FROM runs ORDER BY created_at DESC")
	if err != nil {
		return nil, err
	}
	var runIDs []string
	for rows.Next() {
		var r string
		if err := rows.Scan(&r); err != nil {
			rows.Close()
			return nil, err
		}
		runIDs = append(runIDs, r)
	}
	rows.Close()
	for _, r := range runIDs {
		gs, err := GatesForRun(ctx, q, r, now)
		if err != nil {
			return nil, err
		}
		for _, g := range gs {
			if g.ID == id {
				return g, nil
			}
		}
	}
	return nil, nil
}

// Snapshot is the atomic dashboard snapshot (contract §8.1).
type Snapshot struct {
	SchemaVersion      string            `json:"schema_version"`
	ProjectID          string            `json:"project_id"`
	ProjectVersion     string            `json:"project_version"`
	GraphVersion       string            `json:"graph_version"`
	RuntimeEpoch       string            `json:"runtime_epoch"`
	DatabaseGeneration string            `json:"database_generation"`
	SnapshotCursor     string            `json:"snapshot_cursor"`
	GeneratedAt        string            `json:"generated_at"`
	Freshness          map[string]string `json:"freshness"`
	Project            ProjectView       `json:"project"`
	Entities           map[string][]any  `json:"entities"`
	Summary            map[string]int    `json:"summary"`
}

// BuildSnapshot reads the live projection and the replay boundary within the
// caller's single read transaction.
func BuildSnapshot(ctx context.Context, q Q, projectID string, now int64) (*Snapshot, int64, error) {
	p, err := GetProject(ctx, q, projectID)
	if err != nil {
		return nil, 0, err
	}
	var seq int64
	if err := q.QueryRowContext(ctx, "SELECT COALESCE(MAX(seq),0) FROM events WHERE project_id=?", projectID).Scan(&seq); err != nil {
		return nil, 0, err
	}
	s := &Snapshot{SchemaVersion: "1", ProjectID: p.ID, ProjectVersion: domain.Dec(p.ProjectVersion), GraphVersion: domain.Dec(p.GraphVersion),
		GeneratedAt: domain.RFC3339(now), Project: projectView(p), Entities: map[string][]any{}}
	for _, k := range []string{KindIssue, KindRun, KindAttempt, KindSession, KindWorktree, KindDependency, KindGate, KindApproval, KindCandidate, KindTarget} {
		s.Entities[k] = []any{}
	}
	add := func(kind, query string, args ...any) error {
		rows, err := q.QueryContext(ctx, query, args...)
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
			v, err := LoadProjection(ctx, q, kind, id, now)
			if err != nil {
				return fmt.Errorf("%s %s: %w", kind, id, err)
			}
			if v != nil {
				s.Entities[kind] = append(s.Entities[kind], v)
			}
		}
		return nil
	}
	steps := []struct {
		kind, query string
	}{
		{KindIssue, "SELECT id FROM issues WHERE project_id=? ORDER BY created_at, id"},
		{KindRun, "SELECT id FROM runs WHERE project_id=? ORDER BY created_at, id"},
		{KindAttempt, "SELECT id FROM execution_attempts WHERE project_id=? ORDER BY created_at, id"},
		{KindSession, "SELECT id FROM sessions WHERE project_id=? AND (ended_at IS NULL OR ended_at > ?) ORDER BY created_at, id"},
		{KindWorktree, "SELECT id FROM worktrees WHERE project_id=? AND state <> 'REMOVED' ORDER BY created_at, id"},
		{KindDependency, "SELECT id FROM dependency_edges WHERE project_id=? ORDER BY id"},
		{KindApproval, "SELECT id FROM approvals WHERE project_id=? ORDER BY requested_at, id"},
		{KindCandidate, "SELECT id FROM merge_candidates WHERE project_id=? ORDER BY queue_position, id"},
	}
	for _, st := range steps {
		args := []any{projectID}
		if st.kind == KindSession {
			args = append(args, now-24*3600*1000)
		}
		if err := add(st.kind, st.query, args...); err != nil {
			return nil, 0, err
		}
	}
	targets, err := TargetsOfProject(ctx, q, projectID)
	if err != nil {
		return nil, 0, err
	}
	for _, t := range targets {
		s.Entities[KindTarget] = append(s.Entities[KindTarget], TargetProjection(t))
	}
	runs, err := ListRuns(ctx, q, projectID)
	if err != nil {
		return nil, 0, err
	}
	for _, r := range runs {
		gs, err := GatesForRun(ctx, q, r.ID, now)
		if err != nil {
			return nil, 0, err
		}
		for _, g := range gs {
			s.Entities[KindGate] = append(s.Entities[KindGate], g)
		}
	}
	sum, err := LoadProjectSummary(ctx, q, projectID, now)
	if err != nil {
		return nil, 0, err
	}
	s.Summary = map[string]int{"running": sum.Running, "blocked": sum.Blocked, "approval_pending": sum.ApprovalPending, "integration_pending": sum.IntegrationPending}
	return s, seq, nil
}
