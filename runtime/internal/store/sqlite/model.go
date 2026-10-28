package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/Mineru98/samsara/runtime/internal/domain"
)

// Row types mirror the tables. They are store-internal; wire projections are
// built in projection.go.

type Project struct {
	ID, RootPath, DisplayName, Activation, RegistryState, DrainState string
	ConfigDigest, ConfigJSON                                         string
	PolicyVersion, GraphVersion, ProjectVersion, Version             int64
	CreatedAt, UpdatedAt, LastActivityAt                             int64
}

const projectCols = `id,root_path,display_name,activation,registry_state,drain_state,config_digest,config_json,policy_version,graph_version,project_version,version,created_at,updated_at,last_activity_at`

func scanProject(s interface{ Scan(...any) error }) (*Project, error) {
	p := &Project{}
	err := s.Scan(&p.ID, &p.RootPath, &p.DisplayName, &p.Activation, &p.RegistryState, &p.DrainState, &p.ConfigDigest,
		&p.ConfigJSON, &p.PolicyVersion, &p.GraphVersion, &p.ProjectVersion, &p.Version, &p.CreatedAt, &p.UpdatedAt, &p.LastActivityAt)
	return p, err
}

// GetProject loads a project or RESOURCE_NOT_FOUND.
func GetProject(ctx context.Context, q Q, id string) (*Project, error) {
	p, err := scanProject(q.QueryRowContext(ctx, "SELECT "+projectCols+" FROM projects WHERE id=?", id))
	return orNil(p, err), notFound(err, "project", id)
}

// GetProjectByRoot loads a project by canonical root path.
func GetProjectByRoot(ctx context.Context, q Q, root string) (*Project, error) {
	p, err := scanProject(q.QueryRowContext(ctx, "SELECT "+projectCols+" FROM projects WHERE root_path=?", root))
	return orNil(p, err), notFound(err, "project", root)
}

// ListProjects returns every registered project, including disabled, missing
// and archived ones (history stays visible).
func ListProjects(ctx context.Context, q Q) ([]*Project, error) {
	rows, err := q.QueryContext(ctx, "SELECT "+projectCols+" FROM projects ORDER BY last_activity_at DESC, id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Project
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

type Issue struct {
	ID, ProjectID, ClientKey, Title, AcceptanceCriteria, CheckProfile, RequiredCapabilities string
	TargetRepositoryID, TargetRef, Lifecycle                                                string
	Version, CreatedAt, UpdatedAt                                                           int64
}

const issueCols = `id,project_id,client_key,title,acceptance_criteria,check_profile,required_capabilities,COALESCE(target_repository_id,''),target_ref,lifecycle,version,created_at,updated_at`

func scanIssue(s interface{ Scan(...any) error }) (*Issue, error) {
	i := &Issue{}
	err := s.Scan(&i.ID, &i.ProjectID, &i.ClientKey, &i.Title, &i.AcceptanceCriteria, &i.CheckProfile, &i.RequiredCapabilities,
		&i.TargetRepositoryID, &i.TargetRef, &i.Lifecycle, &i.Version, &i.CreatedAt, &i.UpdatedAt)
	return i, err
}

func GetIssue(ctx context.Context, q Q, id string) (*Issue, error) {
	i, err := scanIssue(q.QueryRowContext(ctx, "SELECT "+issueCols+" FROM issues WHERE id=?", id))
	return orNil(i, err), notFound(err, "issue", id)
}

func ListIssues(ctx context.Context, q Q, projectID string) ([]*Issue, error) {
	rows, err := q.QueryContext(ctx, "SELECT "+issueCols+" FROM issues WHERE project_id=? ORDER BY created_at, id", projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Issue
	for rows.Next() {
		i, err := scanIssue(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// IssuesOfRun lists the issues attached to a run.
func IssuesOfRun(ctx context.Context, q Q, runID string) ([]*Issue, error) {
	rows, err := q.QueryContext(ctx, "SELECT "+prefixCols("i.", issueCols)+" FROM issues i JOIN run_issues ri ON ri.issue_id=i.id WHERE ri.run_id=? ORDER BY i.created_at, i.id", runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Issue
	for rows.Next() {
		i, err := scanIssue(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

type Run struct {
	ID, ProjectID, State, PlanSchemaVersion, PlanDigest, PlanSnapshot, AliasMapping string
	FinalSummary                                                                    string
	Generation, GraphVersion, Version, CreatedAt, StartedAt, EndedAt                int64
}

const runCols = `id,project_id,state,plan_schema_version,plan_digest,plan_snapshot,alias_mapping,COALESCE(final_summary,''),generation,graph_version,version,created_at,COALESCE(started_at,0),COALESCE(ended_at,0)`

func scanRun(s interface{ Scan(...any) error }) (*Run, error) {
	r := &Run{}
	err := s.Scan(&r.ID, &r.ProjectID, &r.State, &r.PlanSchemaVersion, &r.PlanDigest, &r.PlanSnapshot, &r.AliasMapping,
		&r.FinalSummary, &r.Generation, &r.GraphVersion, &r.Version, &r.CreatedAt, &r.StartedAt, &r.EndedAt)
	return r, err
}

func GetRun(ctx context.Context, q Q, id string) (*Run, error) {
	r, err := scanRun(q.QueryRowContext(ctx, "SELECT "+runCols+" FROM runs WHERE id=?", id))
	return orNil(r, err), notFound(err, "run", id)
}

func ListRuns(ctx context.Context, q Q, projectID string) ([]*Run, error) {
	rows, err := q.QueryContext(ctx, "SELECT "+runCols+" FROM runs WHERE project_id=? ORDER BY created_at DESC, id", projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Run
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RunsInState lists runs (all projects) in one of the given states.
func RunsInState(ctx context.Context, q Q, states ...string) ([]*Run, error) {
	query := "SELECT " + runCols + " FROM runs WHERE state IN (" + placeholders(len(states)) + ") ORDER BY created_at, id"
	rows, err := q.QueryContext(ctx, query, anySlice(states)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Run
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

type Edge struct {
	ID, ProjectID, RunID, Predecessor, Successor, Milestone, Phase string
	RequiredOutputID, TargetRepositoryID, TargetRef, Invalidation  string
	RequiredGeneration, GraphVersion, Version                      int64
}

const edgeCols = `id,project_id,run_id,predecessor_issue_id,successor_issue_id,required_milestone,downstream_phase,COALESCE(required_output_id,''),COALESCE(target_repository_id,''),target_ref,invalidation_policy,required_run_generation,graph_version,version`

func scanEdge(s interface{ Scan(...any) error }) (*Edge, error) {
	e := &Edge{}
	err := s.Scan(&e.ID, &e.ProjectID, &e.RunID, &e.Predecessor, &e.Successor, &e.Milestone, &e.Phase, &e.RequiredOutputID,
		&e.TargetRepositoryID, &e.TargetRef, &e.Invalidation, &e.RequiredGeneration, &e.GraphVersion, &e.Version)
	return e, err
}

func GetEdge(ctx context.Context, q Q, id string) (*Edge, error) {
	e, err := scanEdge(q.QueryRowContext(ctx, "SELECT "+edgeCols+" FROM dependency_edges WHERE id=?", id))
	return orNil(e, err), notFound(err, "dependency", id)
}

// EdgesOfRun returns all edges of a run.
func EdgesOfRun(ctx context.Context, q Q, runID string) ([]*Edge, error) {
	return queryEdges(ctx, q, "SELECT "+edgeCols+" FROM dependency_edges WHERE run_id=? ORDER BY id", runID)
}

// EdgesInto returns edges whose successor is issueID within a run.
func EdgesInto(ctx context.Context, q Q, runID, issueID string) ([]*Edge, error) {
	return queryEdges(ctx, q, "SELECT "+edgeCols+" FROM dependency_edges WHERE run_id=? AND successor_issue_id=? ORDER BY id", runID, issueID)
}

// EdgesFrom returns edges whose predecessor is issueID within a run.
func EdgesFrom(ctx context.Context, q Q, runID, issueID string) ([]*Edge, error) {
	return queryEdges(ctx, q, "SELECT "+edgeCols+" FROM dependency_edges WHERE run_id=? AND predecessor_issue_id=? ORDER BY id", runID, issueID)
}

func queryEdges(ctx context.Context, q Q, query string, args ...any) ([]*Edge, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Edge
	for rows.Next() {
		e, err := scanEdge(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

type Attempt struct {
	ID, ProjectID, RunID, IssueID, WorktreeID, State, StateReason, QuarantineReason string
	BackendKind, BackendReason, OutputID                                            string
	RunGeneration, Version, CreatedAt, UpdatedAt                                    int64
	ResumeRequired                                                                  bool
}

const attemptCols = `id,project_id,run_id,issue_id,COALESCE(worktree_id,''),state,state_reason,quarantine_reason,backend_kind,backend_reason,COALESCE(output_id,''),run_generation,version,created_at,updated_at,resume_required`

func scanAttempt(s interface{ Scan(...any) error }) (*Attempt, error) {
	a := &Attempt{}
	err := s.Scan(&a.ID, &a.ProjectID, &a.RunID, &a.IssueID, &a.WorktreeID, &a.State, &a.StateReason, &a.QuarantineReason,
		&a.BackendKind, &a.BackendReason, &a.OutputID, &a.RunGeneration, &a.Version, &a.CreatedAt, &a.UpdatedAt, &a.ResumeRequired)
	return a, err
}

func GetAttempt(ctx context.Context, q Q, id string) (*Attempt, error) {
	a, err := scanAttempt(q.QueryRowContext(ctx, "SELECT "+attemptCols+" FROM execution_attempts WHERE id=?", id))
	return orNil(a, err), notFound(err, "attempt", id)
}

// LatestAttempt returns the newest attempt of an issue in a run, or nil.
func LatestAttempt(ctx context.Context, q Q, runID, issueID string) (*Attempt, error) {
	a, err := scanAttempt(q.QueryRowContext(ctx, "SELECT "+attemptCols+" FROM execution_attempts WHERE run_id=? AND issue_id=? ORDER BY created_at DESC, id DESC LIMIT 1", runID, issueID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return a, err
}

// AttemptsWhere runs a filtered attempt query.
func AttemptsWhere(ctx context.Context, q Q, where string, args ...any) ([]*Attempt, error) {
	rows, err := q.QueryContext(ctx, "SELECT "+attemptCols+" FROM execution_attempts WHERE "+where+" ORDER BY created_at, id", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Attempt
	for rows.Next() {
		a, err := scanAttempt(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

type Worktree struct {
	ID, ProjectID, RepositoryID, AttemptID, Path, BranchRef, BaseSHA, Ownership, State, StateReason string
	Version, CreatedAt, UpdatedAt                                                                   int64
}

const worktreeCols = `id,project_id,COALESCE(repository_id,''),COALESCE(attempt_id,''),path,branch_ref,base_sha,ownership,state,state_reason,version,created_at,updated_at`

func scanWorktree(s interface{ Scan(...any) error }) (*Worktree, error) {
	w := &Worktree{}
	err := s.Scan(&w.ID, &w.ProjectID, &w.RepositoryID, &w.AttemptID, &w.Path, &w.BranchRef, &w.BaseSHA, &w.Ownership,
		&w.State, &w.StateReason, &w.Version, &w.CreatedAt, &w.UpdatedAt)
	return w, err
}

func GetWorktree(ctx context.Context, q Q, id string) (*Worktree, error) {
	w, err := scanWorktree(q.QueryRowContext(ctx, "SELECT "+worktreeCols+" FROM worktrees WHERE id=?", id))
	return orNil(w, err), notFound(err, "worktree", id)
}

func GetWorktreeByPath(ctx context.Context, q Q, path string) (*Worktree, error) {
	w, err := scanWorktree(q.QueryRowContext(ctx, "SELECT "+worktreeCols+" FROM worktrees WHERE path=?", path))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return w, err
}

func ListWorktrees(ctx context.Context, q Q, projectID string) ([]*Worktree, error) {
	rows, err := q.QueryContext(ctx, "SELECT "+worktreeCols+" FROM worktrees WHERE project_id=? ORDER BY created_at, id", projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Worktree
	for rows.Next() {
		w, err := scanWorktree(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// ListWorktreesAll returns every worktree record (all projects).
func ListWorktreesAll(ctx context.Context, q Q) ([]*Worktree, error) {
	rows, err := q.QueryContext(ctx, "SELECT "+worktreeCols+" FROM worktrees ORDER BY created_at, id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Worktree
	for rows.Next() {
		w, err := scanWorktree(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

type Session struct {
	ID, ProjectID, ClaudeSessionID, Cwd, DesiredState, Liveness, InputState, InputOwner string
	Source, BackendRawState, Confidence, ProcessIdentity                                string
	Managed                                                                             bool
	ObservedAt, LastHookAt, Version, CreatedAt, EndedAt                                 int64
}

const sessionCols = `id,project_id,claude_session_id,cwd,desired_state,liveness,input_state,input_owner,source,backend_raw_state,confidence,process_identity,managed,observed_at,COALESCE(last_hook_at,0),version,created_at,COALESCE(ended_at,0)`

func scanSession(s interface{ Scan(...any) error }) (*Session, error) {
	x := &Session{}
	err := s.Scan(&x.ID, &x.ProjectID, &x.ClaudeSessionID, &x.Cwd, &x.DesiredState, &x.Liveness, &x.InputState, &x.InputOwner,
		&x.Source, &x.BackendRawState, &x.Confidence, &x.ProcessIdentity, &x.Managed, &x.ObservedAt, &x.LastHookAt, &x.Version,
		&x.CreatedAt, &x.EndedAt)
	return x, err
}

func GetSession(ctx context.Context, q Q, id string) (*Session, error) {
	s, err := scanSession(q.QueryRowContext(ctx, "SELECT "+sessionCols+" FROM sessions WHERE id=?", id))
	return orNil(s, err), notFound(err, "session", id)
}

// FindSessionByClaudeID returns the newest non-ended session for a Claude
// session id, or nil.
func FindSessionByClaudeID(ctx context.Context, q Q, claudeID string) (*Session, error) {
	s, err := scanSession(q.QueryRowContext(ctx, "SELECT "+sessionCols+" FROM sessions WHERE claude_session_id=? AND ended_at IS NULL ORDER BY created_at DESC, id DESC LIMIT 1", claudeID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return s, err
}

// SessionsWhere runs a filtered session query.
func SessionsWhere(ctx context.Context, q Q, where string, args ...any) ([]*Session, error) {
	rows, err := q.QueryContext(ctx, "SELECT "+sessionCols+" FROM sessions WHERE "+where+" ORDER BY created_at, id", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Session
	for rows.Next() {
		s, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

type Binding struct {
	ID, SessionID, AttemptID, Kind, Instance, OpaqueRef, RefKey, Generation string
	CreatedAt, SupersededAt                                                 int64
}

const bindingCols = `id,COALESCE(session_id,''),COALESCE(attempt_id,''),kind,instance,opaque_ref,ref_key,generation,created_at,COALESCE(superseded_at,0)`

func scanBinding(s interface{ Scan(...any) error }) (*Binding, error) {
	b := &Binding{}
	err := s.Scan(&b.ID, &b.SessionID, &b.AttemptID, &b.Kind, &b.Instance, &b.OpaqueRef, &b.RefKey, &b.Generation, &b.CreatedAt, &b.SupersededAt)
	return b, err
}

func GetBinding(ctx context.Context, q Q, id string) (*Binding, error) {
	b, err := scanBinding(q.QueryRowContext(ctx, "SELECT "+bindingCols+" FROM backend_bindings WHERE id=?", id))
	return orNil(b, err), notFound(err, "binding", id)
}

// CurrentBindingForAttempt returns the newest unsuperseded binding.
func CurrentBindingForAttempt(ctx context.Context, q Q, attemptID string) (*Binding, error) {
	b, err := scanBinding(q.QueryRowContext(ctx, "SELECT "+bindingCols+" FROM backend_bindings WHERE attempt_id=? AND superseded_at IS NULL ORDER BY created_at DESC LIMIT 1", attemptID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return b, err
}

// CurrentBindingForSession returns the newest unsuperseded binding.
func CurrentBindingForSession(ctx context.Context, q Q, sessionID string) (*Binding, error) {
	b, err := scanBinding(q.QueryRowContext(ctx, "SELECT "+bindingCols+" FROM backend_bindings WHERE session_id=? AND superseded_at IS NULL ORDER BY created_at DESC LIMIT 1", sessionID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return b, err
}

type Assignment struct {
	ID, SessionID, AttemptID, BindingID string
	AttachedAt, ReleasedAt              int64
}

// ActiveAssignmentForSession returns the unreleased assignment or nil.
func ActiveAssignmentForSession(ctx context.Context, q Q, sessionID string) (*Assignment, error) {
	a := &Assignment{}
	err := q.QueryRowContext(ctx, "SELECT id,session_id,attempt_id,COALESCE(binding_id,''),attached_at,COALESCE(released_at,0) FROM session_assignments WHERE session_id=? AND released_at IS NULL", sessionID).
		Scan(&a.ID, &a.SessionID, &a.AttemptID, &a.BindingID, &a.AttachedAt, &a.ReleasedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return a, err
}

// ActiveAssignmentForAttempt returns the unreleased assignment or nil.
func ActiveAssignmentForAttempt(ctx context.Context, q Q, attemptID string) (*Assignment, error) {
	a := &Assignment{}
	err := q.QueryRowContext(ctx, "SELECT id,session_id,attempt_id,COALESCE(binding_id,''),attached_at,COALESCE(released_at,0) FROM session_assignments WHERE attempt_id=? AND released_at IS NULL", attemptID).
		Scan(&a.ID, &a.SessionID, &a.AttemptID, &a.BindingID, &a.AttachedAt, &a.ReleasedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return a, err
}

type Lease struct {
	ID, ProjectID, ResourceKey, OwnerAttemptID, SessionID, BindingID, State, StateReason, RuntimeEpoch string
	Fence, IssuedAt, RenewedAt, DeadlineAt, Version                                                    int64
}

const leaseCols = `id,project_id,resource_key,owner_attempt_id,COALESCE(session_id,''),COALESCE(binding_id,''),state,state_reason,runtime_epoch,fence,issued_at,renewed_at,deadline_at,version`

func scanLease(s interface{ Scan(...any) error }) (*Lease, error) {
	l := &Lease{}
	err := s.Scan(&l.ID, &l.ProjectID, &l.ResourceKey, &l.OwnerAttemptID, &l.SessionID, &l.BindingID, &l.State, &l.StateReason,
		&l.RuntimeEpoch, &l.Fence, &l.IssuedAt, &l.RenewedAt, &l.DeadlineAt, &l.Version)
	return l, err
}

func GetLease(ctx context.Context, q Q, id string) (*Lease, error) {
	l, err := scanLease(q.QueryRowContext(ctx, "SELECT "+leaseCols+" FROM leases WHERE id=?", id))
	return orNil(l, err), notFound(err, "lease", id)
}

// ActiveLease returns the ACTIVE lease of a resource, or nil.
func ActiveLease(ctx context.Context, q Q, resourceKey string) (*Lease, error) {
	l, err := scanLease(q.QueryRowContext(ctx, "SELECT "+leaseCols+" FROM leases WHERE resource_key=? AND state='ACTIVE'", resourceKey))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return l, err
}

// LatestLeaseForAttempt returns the newest lease of an attempt, or nil.
func LatestLeaseForAttempt(ctx context.Context, q Q, attemptID string) (*Lease, error) {
	l, err := scanLease(q.QueryRowContext(ctx, "SELECT "+leaseCols+" FROM leases WHERE owner_attempt_id=? ORDER BY fence DESC LIMIT 1", attemptID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return l, err
}

// LeasesWhere runs a filtered lease query.
func LeasesWhere(ctx context.Context, q Q, where string, args ...any) ([]*Lease, error) {
	rows, err := q.QueryContext(ctx, "SELECT "+leaseCols+" FROM leases WHERE "+where+" ORDER BY issued_at, id", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Lease
	for rows.Next() {
		l, err := scanLease(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// CurrentFence returns the durable fence of a resource (0 if never issued).
func CurrentFence(ctx context.Context, q Q, resourceKey string) (int64, error) {
	var f int64
	err := q.QueryRowContext(ctx, "SELECT current_fence FROM resource_generations WHERE resource_key=?", resourceKey).Scan(&f)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return f, err
}

type Output struct {
	ID, ProjectID, RunID, IssueID, AttemptID, SourceSHA, BaseSHA, BranchRef string
	Manifest, ManifestDigest, Provenance, Verification, SupersededBy        string
	RunGeneration, Fence, CreatedAt                                         int64
}

const outputCols = `id,project_id,run_id,issue_id,attempt_id,source_sha,base_sha,branch_ref,manifest,manifest_digest,provenance,verification,COALESCE(superseded_by,''),run_generation,fence,created_at`

func scanOutput(s interface{ Scan(...any) error }) (*Output, error) {
	o := &Output{}
	err := s.Scan(&o.ID, &o.ProjectID, &o.RunID, &o.IssueID, &o.AttemptID, &o.SourceSHA, &o.BaseSHA, &o.BranchRef, &o.Manifest,
		&o.ManifestDigest, &o.Provenance, &o.Verification, &o.SupersededBy, &o.RunGeneration, &o.Fence, &o.CreatedAt)
	return o, err
}

func GetOutput(ctx context.Context, q Q, id string) (*Output, error) {
	o, err := scanOutput(q.QueryRowContext(ctx, "SELECT "+outputCols+" FROM output_artifacts WHERE id=?", id))
	return orNil(o, err), notFound(err, "output", id)
}

// CurrentOutput returns the newest non-superseded output of an issue in the
// given run generation, or nil.
func CurrentOutput(ctx context.Context, q Q, runID, issueID string, generation int64) (*Output, error) {
	o, err := scanOutput(q.QueryRowContext(ctx, "SELECT "+outputCols+" FROM output_artifacts WHERE run_id=? AND issue_id=? AND run_generation=? AND superseded_by IS NULL ORDER BY created_at DESC, id DESC LIMIT 1", runID, issueID, generation))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return o, err
}

type Approval struct {
	ID, ProjectID, Kind, State, Subject, SubjectDigest, RunID, IssueID, OutputID, CandidateID string
	StateReason, DecisionActor, DecisionComment                                               string
	RunGeneration, CandidateRevision, PolicyVersion, RequestedAt, DecidedAt, Version          int64
}

const approvalCols = `id,project_id,kind,state,subject,subject_digest,COALESCE(run_id,''),COALESCE(issue_id,''),COALESCE(output_id,''),COALESCE(merge_candidate_id,''),state_reason,decision_actor,decision_comment,COALESCE(run_generation,0),COALESCE(candidate_revision,0),policy_version,requested_at,COALESCE(decided_at,0),version`

func scanApproval(s interface{ Scan(...any) error }) (*Approval, error) {
	a := &Approval{}
	err := s.Scan(&a.ID, &a.ProjectID, &a.Kind, &a.State, &a.Subject, &a.SubjectDigest, &a.RunID, &a.IssueID, &a.OutputID,
		&a.CandidateID, &a.StateReason, &a.DecisionActor, &a.DecisionComment, &a.RunGeneration, &a.CandidateRevision,
		&a.PolicyVersion, &a.RequestedAt, &a.DecidedAt, &a.Version)
	return a, err
}

func GetApproval(ctx context.Context, q Q, id string) (*Approval, error) {
	a, err := scanApproval(q.QueryRowContext(ctx, "SELECT "+approvalCols+" FROM approvals WHERE id=?", id))
	return orNil(a, err), notFound(err, "approval", id)
}

// ApprovalsWhere runs a filtered approval query (newest first).
func ApprovalsWhere(ctx context.Context, q Q, where string, args ...any) ([]*Approval, error) {
	rows, err := q.QueryContext(ctx, "SELECT "+approvalCols+" FROM approvals WHERE "+where+" ORDER BY requested_at DESC, id DESC", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Approval
	for rows.Next() {
		a, err := scanApproval(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

type Target struct {
	ID, RepositoryID, TargetRef, Authority, IntegrationRepo string
	PolicyVersion, Version, CreatedAt                       int64
}

const targetCols = `id,repository_id,target_ref,authority,integration_repo,policy_version,version,created_at`

func scanTarget(s interface{ Scan(...any) error }) (*Target, error) {
	t := &Target{}
	err := s.Scan(&t.ID, &t.RepositoryID, &t.TargetRef, &t.Authority, &t.IntegrationRepo, &t.PolicyVersion, &t.Version, &t.CreatedAt)
	return t, err
}

func GetTarget(ctx context.Context, q Q, id string) (*Target, error) {
	t, err := scanTarget(q.QueryRowContext(ctx, "SELECT "+targetCols+" FROM integration_targets WHERE id=?", id))
	return orNil(t, err), notFound(err, "integration_target", id)
}

// FindTarget returns the target for (repository, ref) or nil.
func FindTarget(ctx context.Context, q Q, repoID, ref string) (*Target, error) {
	t, err := scanTarget(q.QueryRowContext(ctx, "SELECT "+targetCols+" FROM integration_targets WHERE repository_id=? AND target_ref=?", repoID, ref))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return t, err
}

// TargetsOfProject lists targets linked to a project.
func TargetsOfProject(ctx context.Context, q Q, projectID string) ([]*Target, error) {
	rows, err := q.QueryContext(ctx, "SELECT "+prefixCols("t.", targetCols)+" FROM integration_targets t JOIN project_targets pt ON pt.target_id=t.id WHERE pt.project_id=? ORDER BY t.target_ref", projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Target
	for rows.Next() {
		t, err := scanTarget(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

type Candidate struct {
	ID, ProjectID, IssueID, RunID, AttemptID, OutputID, TargetID, SourceSHA, BaseSHA, CandidateSHA string
	CandidateTree, State, StateReason, NativeQueueState, PublicationObservation                    string
	Revision, QueuePosition, Version, CreatedAt, UpdatedAt                                         int64
	Enqueued                                                                                       bool
}

const candidateCols = `id,project_id,issue_id,run_id,attempt_id,output_id,target_id,source_sha,base_sha,candidate_sha,candidate_tree,state,state_reason,native_queue_state,publication_observation,revision,queue_position,version,created_at,updated_at,enqueued`

func scanCandidate(s interface{ Scan(...any) error }) (*Candidate, error) {
	c := &Candidate{}
	err := s.Scan(&c.ID, &c.ProjectID, &c.IssueID, &c.RunID, &c.AttemptID, &c.OutputID, &c.TargetID, &c.SourceSHA, &c.BaseSHA,
		&c.CandidateSHA, &c.CandidateTree, &c.State, &c.StateReason, &c.NativeQueueState, &c.PublicationObservation,
		&c.Revision, &c.QueuePosition, &c.Version, &c.CreatedAt, &c.UpdatedAt, &c.Enqueued)
	return c, err
}

func GetCandidate(ctx context.Context, q Q, id string) (*Candidate, error) {
	c, err := scanCandidate(q.QueryRowContext(ctx, "SELECT "+candidateCols+" FROM merge_candidates WHERE id=?", id))
	return orNil(c, err), notFound(err, "merge_candidate", id)
}

// CandidatesWhere runs a filtered candidate query in queue order.
func CandidatesWhere(ctx context.Context, q Q, where string, args ...any) ([]*Candidate, error) {
	rows, err := q.QueryContext(ctx, "SELECT "+candidateCols+" FROM merge_candidates WHERE "+where+" ORDER BY queue_position, id", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Candidate
	for rows.Next() {
		c, err := scanCandidate(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

type Evidence struct {
	ID, ProjectID, CandidateID, TargetID, OutputID, IssueID, TargetBefore, TargetAfter, Method, ProviderEvidence string
	CreatedAt                                                                                                    int64
}

// EvidenceForOutput returns integration evidence of an output on a target.
func EvidenceForOutput(ctx context.Context, q Q, outputID, targetID string) (*Evidence, error) {
	e := &Evidence{}
	err := q.QueryRowContext(ctx, `SELECT id,project_id,candidate_id,target_id,output_id,issue_id,target_before,target_after,method,provider_evidence,created_at
		FROM integration_evidence WHERE output_id=? AND target_id=? ORDER BY created_at LIMIT 1`, outputID, targetID).
		Scan(&e.ID, &e.ProjectID, &e.CandidateID, &e.TargetID, &e.OutputID, &e.IssueID, &e.TargetBefore, &e.TargetAfter, &e.Method, &e.ProviderEvidence, &e.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return e, err
}

// Repository row.
type Repository struct {
	ID, CommonDir, ObjectFormat, RemoteIdentity string
	CreatedAt                                   int64
}

func GetRepository(ctx context.Context, q Q, id string) (*Repository, error) {
	r := &Repository{}
	err := q.QueryRowContext(ctx, "SELECT id,common_dir,object_format,remote_identity,created_at FROM repositories WHERE id=?", id).
		Scan(&r.ID, &r.CommonDir, &r.ObjectFormat, &r.RemoteIdentity, &r.CreatedAt)
	return orNil(r, err), notFound(err, "repository", id)
}

// RepositoryOfProject returns the primary repository of a project, or nil.
func RepositoryOfProject(ctx context.Context, q Q, projectID string) (*Repository, error) {
	r := &Repository{}
	err := q.QueryRowContext(ctx, `SELECT r.id,r.common_dir,r.object_format,r.remote_identity,r.created_at FROM repositories r
		JOIN project_repositories pr ON pr.repository_id=r.id WHERE pr.project_id=? ORDER BY r.created_at LIMIT 1`, projectID).
		Scan(&r.ID, &r.CommonDir, &r.ObjectFormat, &r.RemoteIdentity, &r.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return r, err
}

// CheckProfile row.
type CheckProfile struct {
	ProjectID, Name string
	Argv            []string
	TimeoutSec      int64
}

// GetCheckProfile loads a trusted check profile or nil.
func GetCheckProfile(ctx context.Context, q Q, projectID, name string) (*CheckProfile, error) {
	var argv string
	cp := &CheckProfile{ProjectID: projectID, Name: name}
	err := q.QueryRowContext(ctx, "SELECT argv_json, timeout_sec FROM check_profiles WHERE project_id=? AND name=?", projectID, name).Scan(&argv, &cp.TimeoutSec)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(argv), &cp.Argv); err != nil {
		return nil, err
	}
	return cp, nil
}

// ListCheckProfiles returns profile names of a project.
func ListCheckProfiles(ctx context.Context, q Q, projectID string) ([]*CheckProfile, error) {
	rows, err := q.QueryContext(ctx, "SELECT name, argv_json, timeout_sec FROM check_profiles WHERE project_id=? ORDER BY name", projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*CheckProfile
	for rows.Next() {
		var argv string
		cp := &CheckProfile{ProjectID: projectID}
		if err := rows.Scan(&cp.Name, &argv, &cp.TimeoutSec); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(argv), &cp.Argv)
		out = append(out, cp)
	}
	return out, rows.Err()
}

func orNil[T any](v *T, err error) *T {
	if err != nil {
		return nil
	}
	return v
}

func notFound(err error, kind, id string) error {
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Errf(domain.CodeNotFound, "%s %s not found", kind, id).WithDetails("resource", kind)
	}
	return err
}

func placeholders(n int) string {
	if n == 0 {
		return "NULL"
	}
	b := make([]byte, 0, n*2)
	for i := 0; i < n; i++ {
		if i > 0 {
			b = append(b, ',')
		}
		b = append(b, '?')
	}
	return string(b)
}

func anySlice(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

func prefixCols(prefix, cols string) string {
	out := []byte{}
	start := 0
	depth := 0
	for i := 0; i <= len(cols); i++ {
		if i < len(cols) {
			switch cols[i] {
			case '(':
				depth++
				continue
			case ')':
				depth--
				continue
			case ',':
				if depth > 0 {
					continue
				}
			default:
				continue
			}
		}
		col := cols[start:i]
		if len(out) > 0 {
			out = append(out, ',')
		}
		if len(col) > 9 && col[:9] == "COALESCE(" {
			out = append(out, "COALESCE("+prefix+col[9:]...)
		} else {
			out = append(out, prefix+col...)
		}
		start = i + 1
	}
	return string(out)
}
