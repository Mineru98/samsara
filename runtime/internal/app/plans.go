package app

import (
	"context"
	"encoding/json"

	"github.com/Mineru98/samsara/runtime/internal/domain"
	"github.com/Mineru98/samsara/runtime/internal/plan"
	"github.com/Mineru98/samsara/runtime/internal/store/sqlite"
)

// PlanValidation is the result of plan validate.
type PlanValidation struct {
	Valid             bool           `json:"valid"`
	PlanDigest        string         `json:"plan_digest"`
	ProjectValidated  bool           `json:"project_validated"`
	Problems          []plan.Problem `json:"problems"`
	CurrentGraphVer   string         `json:"current_graph_version,omitempty"`
	BlockingRunID     string         `json:"blocking_run_id,omitempty"`
	ResolvedTargetRef string         `json:"resolved_target_ref,omitempty"`
}

// ValidatePlan checks schema, references, cycles, check profiles and
// targets without any mutation (PLN-01).
func (a *App) ValidatePlan(ctx context.Context, raw json.RawMessage) (*PlanValidation, error) {
	p, err := plan.Parse(raw)
	if err != nil {
		return nil, err
	}
	res := &PlanValidation{ProjectValidated: true, Problems: []plan.Problem{}}
	err = a.DB.Read(ctx, func(q sqlite.Q) error {
		ps, _, err := a.validateAgainstProject(ctx, q, p)
		res.Problems = append(res.Problems, ps...)
		if err != nil {
			return err
		}
		if pr, err := sqlite.GetProject(ctx, q, p.ProjectID); err == nil {
			res.CurrentGraphVer = domain.Dec(pr.GraphVersion)
			res.ResolvedTargetRef = projectConfig(pr).Git.TargetRef
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	res.PlanDigest = p.Digest()
	res.Valid = len(res.Problems) == 0
	return res, nil
}

// validateAgainstProject normalises the plan with project defaults and runs
// structural + project checks.
func (a *App) validateAgainstProject(ctx context.Context, q sqlite.Q, p *plan.Plan) ([]plan.Problem, *sqlite.Project, error) {
	pr, err := sqlite.GetProject(ctx, q, p.ProjectID)
	if err != nil {
		if domain.IsCode(err, domain.CodeNotFound) {
			p.Normalize("refs/heads/main")
			ps := p.Validate()
			return append(ps, plan.Problem{Code: domain.CodeProjectNotTrusted, Path: "project_id", Message: "project is not registered locally (run samsara init)"}), nil, nil
		}
		return nil, nil, err
	}
	cfg := projectConfig(pr)
	p.Normalize(cfg.Git.TargetRef)
	ps := p.Validate()
	if pr.Activation != domain.ActivationEnabled {
		ps = append(ps, plan.Problem{Code: domain.CodeProjectNotEnabled, Path: "project_id", Message: "project is not enabled"})
	}
	repo, err := sqlite.RepositoryOfProject(ctx, q, pr.ID)
	if err != nil {
		return nil, nil, err
	}
	for i, is := range p.Issues {
		if is.CheckProfile != "" {
			cp, err := sqlite.GetCheckProfile(ctx, q, pr.ID, is.CheckProfile)
			if err != nil {
				return nil, nil, err
			}
			if cp == nil {
				ps = append(ps, plan.Problem{Code: "UNREGISTERED_CHECK_PROFILE", Path: issuePath(i) + ".check_profile",
					Message: "check_profile " + is.CheckProfile + " is not registered (samsara profile add)"})
			}
		}
		if is.TargetRepositoryID != "" && (repo == nil || is.TargetRepositoryID != repo.ID) {
			ps = append(ps, plan.Problem{Code: "UNREGISTERED_TARGET", Path: issuePath(i) + ".target_repository_id", Message: "target repository is not registered for this project"})
		}
	}
	for i, d := range p.Dependencies {
		if d.RequiredMilestone != domain.MilestoneIntegrated {
			continue
		}
		if repo == nil {
			ps = append(ps, plan.Problem{Code: "UNREGISTERED_TARGET", Path: depPath(i), Message: "INTEGRATED dependencies need a Git repository"})
			continue
		}
		if d.TargetRepositoryID != "" && d.TargetRepositoryID != repo.ID {
			ps = append(ps, plan.Problem{Code: "UNREGISTERED_TARGET", Path: depPath(i) + ".target_repository_id", Message: "target repository is not registered for this project"})
		}
		t, err := sqlite.FindTarget(ctx, q, repo.ID, d.TargetRef)
		if err != nil {
			return nil, nil, err
		}
		if t == nil {
			ps = append(ps, plan.Problem{Code: "UNREGISTERED_TARGET", Path: depPath(i) + ".target_ref", Message: "target " + d.TargetRef + " is not a registered integration target"})
		}
	}
	return ps, pr, nil
}

func issuePath(i int) string { return "issues[" + domain.Dec(int64(i)) + "]" }
func depPath(i int) string   { return "dependencies[" + domain.Dec(int64(i)) + "]" }

// PlanApplyResult is the accepted plan (a PLANNED run, no workers).
type PlanApplyResult struct {
	RunID         string            `json:"run_id"`
	RunState      string            `json:"run_state"`
	RunGeneration string            `json:"run_generation"`
	GraphVersion  string            `json:"graph_version"`
	PlanDigest    string            `json:"plan_digest"`
	IssueMapping  map[string]string `json:"issue_mapping"`
}

// ApplyPlan atomically records issues, edges and a PLANNED run. It never
// starts workers (PLN-02..04).
func (a *App) ApplyPlan(ctx context.Context, cmd Cmd, raw json.RawMessage) (*PlanApplyResult, error) {
	if err := a.requireReady(); err != nil {
		return nil, err
	}
	p, err := plan.Parse(raw)
	if err != nil {
		return nil, err
	}
	var out PlanApplyResult
	err = a.DB.Write(ctx, func(tx *sqlite.Tx) error {
		return a.idempotent(ctx, tx, "plan.apply:"+p.ProjectID, cmd, p, &out, func() (any, error) {
			ps, pr, err := a.validateAgainstProject(ctx, tx, p)
			if err != nil {
				return nil, err
			}
			if len(ps) > 0 {
				return nil, plan.Problems2Error(ps)
			}
			expected, _ := domain.ParseDec(p.ExpectedGraphVersion)
			if expected != pr.GraphVersion {
				return nil, domain.Errf(domain.CodeVersionMismatch, "graph version changed").
					WithDetails("expected_version", p.ExpectedGraphVersion, "current_version", domain.Dec(pr.GraphVersion))
			}
			var blocking string
			_ = tx.QueryRowContext(ctx, "SELECT id FROM runs WHERE project_id=? AND state IN ('PLANNED','ACTIVE','PAUSED') LIMIT 1", pr.ID).Scan(&blocking)
			if blocking != "" {
				return nil, domain.Errf(domain.CodeConflict, "project already has an unfinished run %s; cancel it or use pause+revise", blocking).WithDetails("run_id", blocking)
			}
			repo, err := sqlite.RepositoryOfProject(ctx, tx, pr.ID)
			if err != nil {
				return nil, err
			}
			graph := pr.GraphVersion + 1
			runID := domain.NewID("run")
			mapping := map[string]string{}
			for _, is := range p.Issues {
				mapping[is.ClientKey] = domain.NewID("issue")
			}
			mappingJSON, _ := json.Marshal(mapping)
			snap, _ := json.Marshal(p)
			if _, err := tx.ExecContext(ctx, `INSERT INTO runs(id,project_id,generation,graph_version,state,plan_schema_version,plan_digest,plan_snapshot,alias_mapping,created_at)
				VALUES(?,?,1,?,?,?,?,?,?,?)`, runID, pr.ID, graph, domain.RunPlanned, p.SpecVersion, p.Digest(), string(snap), string(mappingJSON), tx.Now); err != nil {
				return nil, err
			}
			cfg := projectConfig(pr)
			for _, is := range p.Issues {
				ac, _ := json.Marshal(is.AcceptanceCriteria)
				caps, _ := json.Marshal(is.RequiredCapabilities)
				target := is.TargetRef
				if target == "" {
					target = cfg.Git.TargetRef
				}
				var repoID any
				if repo != nil {
					repoID = repo.ID
				}
				iid := mapping[is.ClientKey]
				if _, err := tx.ExecContext(ctx, `INSERT INTO issues(id,project_id,client_key,title,acceptance_criteria,check_profile,required_capabilities,target_repository_id,target_ref,created_at,updated_at)
					VALUES(?,?,?,?,?,?,?,?,?,?,?)`, iid, pr.ID, is.ClientKey, is.Title, string(ac), is.CheckProfile, string(caps), repoID, target, tx.Now, tx.Now); err != nil {
					return nil, err
				}
				if _, err := tx.ExecContext(ctx, "INSERT INTO run_issues(run_id,issue_id) VALUES(?,?)", runID, iid); err != nil {
					return nil, err
				}
				tx.Touch(pr.ID, sqlite.KindIssue, iid)
			}
			for _, d := range p.Dependencies {
				eid := domain.NewID("dep")
				var repoID any
				if d.RequiredMilestone == domain.MilestoneIntegrated && repo != nil {
					repoID = repo.ID
				}
				if _, err := tx.ExecContext(ctx, `INSERT INTO dependency_edges(id,project_id,run_id,predecessor_issue_id,successor_issue_id,required_milestone,downstream_phase,required_run_generation,target_repository_id,target_ref,graph_version)
					VALUES(?,?,?,?,?,?,?,1,?,?,?)`, eid, pr.ID, runID, mapping[d.PredecessorKey], mapping[d.SuccessorKey], d.RequiredMilestone, d.DownstreamPhase, repoID, d.TargetRef, graph); err != nil {
					return nil, err
				}
				tx.Touch(pr.ID, sqlite.KindDependency, eid)
			}
			if _, err := tx.ExecContext(ctx, "UPDATE projects SET graph_version=?, version=version+1 WHERE id=?", graph, pr.ID); err != nil {
				return nil, err
			}
			tx.Touch(pr.ID, sqlite.KindRun, runID)
			tx.Touch(pr.ID, sqlite.KindProject, pr.ID)
			tx.Event(pr.ID, "plan.applied", "run", runID, 1)
			if err := tx.Audit(ctx, pr.ID, cmd.Principal.ID, "plan.apply", "run", runID, "", "", map[string]any{"plan_digest": p.Digest(), "issues": len(p.Issues)}); err != nil {
				return nil, err
			}
			return &PlanApplyResult{RunID: runID, RunState: domain.RunPlanned, RunGeneration: "1", GraphVersion: domain.Dec(graph),
				PlanDigest: p.Digest(), IssueMapping: mapping}, nil
		})
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// PlanReviseResult reports an accepted revision of a PAUSED run.
type PlanReviseResult struct {
	RunID                  string            `json:"run_id"`
	RunState               string            `json:"run_state"`
	RunGeneration          string            `json:"run_generation"`
	GraphVersion           string            `json:"graph_version"`
	PlanDigest             string            `json:"plan_digest"`
	ChangeKind             string            `json:"change_kind"`
	AffectedIssueIDs       []string          `json:"affected_issue_ids"`
	SupersededOutputIDs    []string          `json:"superseded_output_ids"`
	InvalidatedApprovalIDs []string          `json:"invalidated_approval_ids"`
	PlanApprovalID         string            `json:"plan_approval_id,omitempty"`
	IssueMapping           map[string]string `json:"issue_mapping"`
}

// liveAttemptStates are attempts that still hold (or may regain) execution.
const liveAttemptStates = "'PENDING','STARTING','RUNNING','PAUSING','PAUSED','CANCEL_REQUESTED'"

// RevisePlan changes the plan of a PAUSED, quiescent run (01 §12.4, 03 §4.6).
// A title-only change keeps the generation. A change of execution meaning
// bumps run_generation and graph_version and re-evaluates only the affected
// issues: their live attempts are replaced, their outputs are marked
// superseded and their candidates cancelled. Unaffected attempts keep their
// identity and move to the new generation. Every approval bound to the old
// generation is invalidated; outputs and integration evidence are never
// deleted.
func (a *App) RevisePlan(ctx context.Context, cmd Cmd, raw json.RawMessage) (*PlanReviseResult, error) {
	if err := a.requireReady(); err != nil {
		return nil, err
	}
	rev, p, err := plan.ParseRevision(raw)
	if err != nil {
		return nil, err
	}
	var out PlanReviseResult
	err = a.DB.Write(ctx, func(tx *sqlite.Tx) error {
		return a.idempotent(ctx, tx, "plan.revise:"+rev.RunID, cmd, json.RawMessage(raw), &out, func() (any, error) {
			return a.revisePlanTx(ctx, tx, cmd, rev, p)
		})
	})
	if err != nil {
		return nil, err
	}
	a.Wake()
	return &out, nil
}

func (a *App) revisePlanTx(ctx context.Context, tx *sqlite.Tx, cmd Cmd, rev *plan.Revision, p *plan.Plan) (*PlanReviseResult, error) {
	r, err := sqlite.GetRun(ctx, tx, rev.RunID)
	if err != nil {
		return nil, err
	}
	if err := checkIfMatch(cmd, r.Version); err != nil {
		return nil, err
	}
	if p.ProjectID != r.ProjectID {
		return nil, domain.Errf(domain.CodeInvalidRequest, "plan project_id does not match the run's project")
	}
	ps, pr, err := a.validateAgainstProject(ctx, tx, p)
	if err != nil {
		return nil, err
	}
	if len(ps) > 0 {
		return nil, plan.Problems2Error(ps)
	}
	if r.State != domain.RunPaused {
		return nil, domain.Errf(domain.CodeConflict, "run is %s; pause it before revise", r.State).WithDetails("run_id", r.ID, "run_state", r.State)
	}
	if exp, _ := domain.ParseDec(rev.ExpectedRunGeneration); exp != r.Generation {
		return nil, domain.Errf(domain.CodeVersionMismatch, "run generation changed").
			WithDetails("expected_run_generation", rev.ExpectedRunGeneration, "current_run_generation", domain.Dec(r.Generation))
	}
	if exp, _ := domain.ParseDec(p.ExpectedGraphVersion); exp != pr.GraphVersion {
		return nil, domain.Errf(domain.CodeVersionMismatch, "graph version changed").
			WithDetails("expected_version", p.ExpectedGraphVersion, "current_version", domain.Dec(pr.GraphVersion))
	}
	// Quiescence: no tool may be in flight and no verification or
	// integration may still be acting on an output this revision supersedes.
	busy, err := sqlite.AttemptsWhere(ctx, tx, "run_id=? AND state IN ('STARTING','RUNNING','PAUSING','CANCEL_REQUESTED','IMPLEMENTED','VERIFYING')", r.ID)
	if err != nil {
		return nil, err
	}
	if len(busy) > 0 {
		ids := make([]string, len(busy))
		for i, att := range busy {
			ids[i] = att.ID
		}
		return nil, domain.Errf(domain.CodeConflict, "run is not quiescent: %d attempt(s) are still running or verifying", len(busy)).
			WithDetails("reason", "RUN_NOT_QUIESCENT", "attempt_ids", ids)
	}
	inflight, err := sqlite.CandidatesWhere(ctx, tx, "run_id=? AND state IN ('VALIDATING','PUBLISHING')", r.ID)
	if err != nil {
		return nil, err
	}
	if len(inflight) > 0 {
		ids := make([]string, len(inflight))
		for i, c := range inflight {
			ids[i] = c.ID
		}
		return nil, domain.Errf(domain.CodeConflict, "run is not quiescent: %d merge candidate(s) are validating or publishing", len(inflight)).
			WithDetails("reason", "RUN_NOT_QUIESCENT", "merge_candidate_ids", ids)
	}
	old, err := plan.Parse([]byte(r.PlanSnapshot))
	if err != nil {
		return nil, err
	}
	delta := plan.Compare(old, p)
	if len(delta.Removed) > 0 {
		return nil, domain.Errf(domain.CodeInvalidRequest, "revise cannot remove issues: %v", delta.Removed).WithDetails("removed_client_keys", delta.Removed)
	}
	mapping := map[string]string{}
	if err := json.Unmarshal([]byte(r.AliasMapping), &mapping); err != nil {
		return nil, err
	}
	issueByKey := map[string]plan.Issue{}
	for _, is := range p.Issues {
		issueByKey[is.ClientKey] = is
	}
	for _, k := range delta.Retitled {
		if _, err := tx.ExecContext(ctx, "UPDATE issues SET title=?, version=version+1, updated_at=? WHERE id=?", issueByKey[k].Title, tx.Now, mapping[k]); err != nil {
			return nil, err
		}
		tx.Touch(r.ProjectID, sqlite.KindIssue, mapping[k])
	}
	res := &PlanReviseResult{RunID: r.ID, RunState: r.State, ChangeKind: delta.Kind(), PlanDigest: p.Digest(),
		AffectedIssueIDs: []string{}, SupersededOutputIDs: []string{}, InvalidatedApprovalIDs: []string{}, IssueMapping: mapping}
	snap, _ := json.Marshal(p)
	auditDetails := map[string]any{"change_kind": res.ChangeKind, "previous_plan_digest": r.PlanDigest, "plan_digest": res.PlanDigest,
		"previous_plan_snapshot": json.RawMessage(r.PlanSnapshot)}

	if res.ChangeKind == plan.ChangeMetadataOnly {
		if _, err := tx.ExecContext(ctx, "UPDATE runs SET plan_digest=?, plan_snapshot=?, version=version+1 WHERE id=?", res.PlanDigest, string(snap), r.ID); err != nil {
			return nil, err
		}
		res.RunGeneration, res.GraphVersion = domain.Dec(r.Generation), domain.Dec(r.GraphVersion)
		tx.Touch(r.ProjectID, sqlite.KindRun, r.ID)
		tx.Event(r.ProjectID, "plan.revised", "run", r.ID, r.Version+1)
		return res, tx.Audit(ctx, r.ProjectID, cmd.Principal.ID, "plan.revise", "run", r.ID, "", "", auditDetails)
	}

	gen, graph := r.Generation+1, pr.GraphVersion+1
	repo, err := sqlite.RepositoryOfProject(ctx, tx, pr.ID)
	if err != nil {
		return nil, err
	}
	var repoID any
	if repo != nil {
		repoID = repo.ID
	}
	cfg := projectConfig(pr)
	targetOf := func(is plan.Issue) string {
		if is.TargetRef != "" {
			return is.TargetRef
		}
		return cfg.Git.TargetRef
	}
	for _, k := range delta.Added {
		is := issueByKey[k]
		iid := domain.NewID("issue")
		mapping[k] = iid
		ac, _ := json.Marshal(is.AcceptanceCriteria)
		caps, _ := json.Marshal(is.RequiredCapabilities)
		if _, err := tx.ExecContext(ctx, `INSERT INTO issues(id,project_id,client_key,title,acceptance_criteria,check_profile,required_capabilities,target_repository_id,target_ref,created_at,updated_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,?)`, iid, pr.ID, is.ClientKey, is.Title, string(ac), is.CheckProfile, string(caps), repoID, targetOf(is), tx.Now, tx.Now); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO run_issues(run_id,issue_id) VALUES(?,?)", r.ID, iid); err != nil {
			return nil, err
		}
		tx.Touch(pr.ID, sqlite.KindIssue, iid)
	}
	for _, k := range delta.Changed {
		is := issueByKey[k]
		ac, _ := json.Marshal(is.AcceptanceCriteria)
		caps, _ := json.Marshal(is.RequiredCapabilities)
		if _, err := tx.ExecContext(ctx, "UPDATE issues SET acceptance_criteria=?, check_profile=?, required_capabilities=?, target_ref=?, version=version+1, updated_at=? WHERE id=?",
			string(ac), is.CheckProfile, string(caps), targetOf(is), tx.Now, mapping[k]); err != nil {
			return nil, err
		}
	}

	// Edges are bound to the run; rewrite them for the new generation.
	edges, err := sqlite.EdgesOfRun(ctx, tx, r.ID)
	if err != nil {
		return nil, err
	}
	existing := map[[2]string]*sqlite.Edge{}
	for _, e := range edges {
		existing[[2]string{e.Predecessor, e.Successor}] = e
	}
	for _, d := range p.Dependencies {
		pred, succ := mapping[d.PredecessorKey], mapping[d.SuccessorKey]
		var edgeRepo any
		if d.RequiredMilestone == domain.MilestoneIntegrated && repo != nil {
			edgeRepo = repo.ID
		}
		k := [2]string{pred, succ}
		if e := existing[k]; e != nil {
			delete(existing, k)
			if _, err := tx.ExecContext(ctx, `UPDATE dependency_edges SET required_milestone=?, downstream_phase=?, target_repository_id=?, target_ref=?,
				required_run_generation=?, graph_version=?, version=version+1 WHERE id=?`, d.RequiredMilestone, d.DownstreamPhase, edgeRepo, d.TargetRef, gen, graph, e.ID); err != nil {
				return nil, err
			}
			tx.Touch(pr.ID, sqlite.KindDependency, e.ID)
			continue
		}
		eid := domain.NewID("dep")
		if _, err := tx.ExecContext(ctx, `INSERT INTO dependency_edges(id,project_id,run_id,predecessor_issue_id,successor_issue_id,required_milestone,downstream_phase,required_run_generation,target_repository_id,target_ref,graph_version)
			VALUES(?,?,?,?,?,?,?,?,?,?,?)`, eid, pr.ID, r.ID, pred, succ, d.RequiredMilestone, d.DownstreamPhase, gen, edgeRepo, d.TargetRef, graph); err != nil {
			return nil, err
		}
		tx.Touch(pr.ID, sqlite.KindDependency, eid)
	}
	for _, e := range existing {
		if _, err := tx.ExecContext(ctx, "DELETE FROM dependency_edges WHERE id=?", e.ID); err != nil {
			return nil, err
		}
		tx.Touch(pr.ID, sqlite.KindDependency, e.ID)
	}

	if _, err := tx.ExecContext(ctx, "UPDATE runs SET generation=?, graph_version=?, plan_digest=?, plan_snapshot=?, alias_mapping=?, version=version+1 WHERE id=?",
		gen, graph, res.PlanDigest, string(snap), mustJSON(mapping), r.ID); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE projects SET graph_version=?, version=version+1 WHERE id=?", graph, pr.ID); err != nil {
		return nil, err
	}

	affected := map[string]bool{}
	for _, k := range plan.Affected(p, delta) {
		affected[mapping[k]] = true
		res.AffectedIssueIDs = append(res.AffectedIssueIDs, mapping[k])
	}
	for _, is := range old.Issues {
		iid := mapping[is.ClientKey]
		if !affected[iid] {
			// Unaffected work keeps its attempt (and worktree) and moves to
			// the new generation, so resume re-leases it (POL-02).
			if _, err := tx.ExecContext(ctx, "UPDATE execution_attempts SET run_generation=?, version=version+1, updated_at=? WHERE run_id=? AND issue_id=? AND state IN ("+liveAttemptStates+")",
				gen, tx.Now, r.ID, iid); err != nil {
				return nil, err
			}
			continue
		}
		if err := a.reviseAffectedIssue(ctx, tx, r, iid, gen, res); err != nil {
			return nil, err
		}
	}

	// Every approval of the old generation is void (POL-05, POL-07).
	aps, err := sqlite.ApprovalsWhere(ctx, tx, "run_id=? AND state IN ('PENDING','APPROVED') AND COALESCE(run_generation,0) <> ?", r.ID, gen)
	if err != nil {
		return nil, err
	}
	decided := map[string]bool{} // outputs whose RESULT was pending or approved
	for _, ap := range aps {
		if err := a.invalidateApproval(ctx, tx, ap, "PLAN_REVISED"); err != nil {
			return nil, err
		}
		res.InvalidatedApprovalIDs = append(res.InvalidatedApprovalIDs, ap.ID)
		if ap.Kind == domain.ApprovalResult && ap.OutputID != "" {
			decided[ap.OutputID] = true
		}
	}

	nr, err := sqlite.GetRun(ctx, tx, r.ID)
	if err != nil {
		return nil, err
	}
	ap, err := a.ensurePlanApproval(ctx, tx, pr, nr)
	if err != nil {
		return nil, err
	}
	res.PlanApprovalID = ap.ID
	// Verified results of unaffected issues need a RESULT decision bound to
	// the new generation. Results the user rejected or revoked stay so.
	for _, is := range old.Issues {
		iid := mapping[is.ClientKey]
		if affected[iid] {
			continue
		}
		if err := a.rerequestResultApproval(ctx, tx, pr, nr, iid, decided); err != nil {
			return nil, err
		}
	}

	res.RunGeneration, res.GraphVersion = domain.Dec(gen), domain.Dec(graph)
	tx.Touch(r.ProjectID, sqlite.KindRun, r.ID)
	tx.Touch(r.ProjectID, sqlite.KindProject, r.ProjectID)
	if err := a.touchRunIssues(ctx, tx, r.ProjectID, r.ID); err != nil {
		return nil, err
	}
	tx.Event(r.ProjectID, "plan.revised", "run", r.ID, r.Version+1)
	auditDetails["run_generation"] = gen
	auditDetails["affected_issue_ids"] = res.AffectedIssueIDs
	auditDetails["superseded_output_ids"] = res.SupersededOutputIDs
	return res, tx.Audit(ctx, r.ProjectID, cmd.Principal.ID, "plan.revise", "run", r.ID, "", "", auditDetails)
}

// reviseAffectedIssue retires the issue's old-generation work without
// deleting it and queues a fresh attempt in the new generation.
func (a *App) reviseAffectedIssue(ctx context.Context, tx *sqlite.Tx, r *sqlite.Run, issueID string, gen int64, res *PlanReviseResult) error {
	latest, err := sqlite.LatestAttempt(ctx, tx, r.ID, issueID)
	if err != nil {
		return err
	}
	if latest != nil && latest.QuarantineReason != "" {
		return domain.Errf(domain.CodeConflict, "attempt %s is quarantined (%s); resolve it before revise", latest.ID, latest.QuarantineReason).
			WithDetails("attempt_id", latest.ID, "quarantine_reason", latest.QuarantineReason)
	}
	next := "" // the attempt that carries the issue into the new generation
	switch {
	case latest == nil:
	case latest.State == domain.AttemptPending && latest.WorktreeID == "":
		// Nothing ran yet: the queued attempt simply moves to the new plan.
		if _, err := tx.ExecContext(ctx, "UPDATE execution_attempts SET run_generation=?, version=version+1, updated_at=? WHERE id=?", gen, tx.Now, latest.ID); err != nil {
			return err
		}
		next = latest.ID
	default:
		// The cancelled attempt and candidates point at the attempt that
		// replaces them, so history reads as one line (04 §12).
		next = domain.NewID("att")
		if latest.State == domain.AttemptPending || latest.State == domain.AttemptPaused {
			if err := a.revokeAttemptLease(ctx, tx, latest, domain.LeaseRevoked, "PLAN_REVISED"); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, "UPDATE execution_attempts SET state='CANCELLED', state_reason='PLAN_REVISED', superseded_by=?, version=version+1, updated_at=? WHERE id=?", next, tx.Now, latest.ID); err != nil {
				return err
			}
			if err := a.releaseAttemptBindings(ctx, tx, latest); err != nil {
				return err
			}
			tx.Touch(r.ProjectID, sqlite.KindAttempt, latest.ID)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO execution_attempts(id,project_id,run_id,issue_id,run_generation,state,state_reason,created_at,updated_at)
			VALUES(?,?,?,?,?,'PENDING','QUEUED',?,?)`, next, r.ProjectID, r.ID, issueID, gen, tx.Now, tx.Now); err != nil {
			return err
		}
		tx.Touch(r.ProjectID, sqlite.KindAttempt, next)
	}
	rows, err := tx.QueryContext(ctx, "SELECT id FROM output_artifacts WHERE run_id=? AND issue_id=? AND superseded_by IS NULL ORDER BY created_at, id", r.ID, issueID)
	if err != nil {
		return err
	}
	var outs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		outs = append(outs, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	marker := "PLAN_REVISED:" + domain.Dec(gen)
	for _, id := range outs {
		if _, err := tx.ExecContext(ctx, "UPDATE output_artifacts SET superseded_by=? WHERE id=?", marker, id); err != nil {
			return err
		}
		res.SupersededOutputIDs = append(res.SupersededOutputIDs, id)
	}
	cancelled, err := sqlite.CandidatesWhere(ctx, tx, "run_id=? AND issue_id=? AND state IN ('WAITING','READY','STALE_BASE','CONFLICT','FAILED')", r.ID, issueID)
	if err != nil {
		return err
	}
	for _, c := range cancelled {
		if _, err := tx.ExecContext(ctx, "UPDATE merge_candidates SET state='CANCELLED', state_reason='PLAN_REVISED', superseded_by=NULLIF(?,''), version=version+1, updated_at=? WHERE id=?",
			next, tx.Now, c.ID); err != nil {
			return err
		}
		tx.Touch(r.ProjectID, sqlite.KindCandidate, c.ID)
	}
	if _, err := tx.ExecContext(ctx, "UPDATE issues SET lifecycle='OPEN', version=version+1, updated_at=? WHERE id=? AND lifecycle='COMPLETED'", tx.Now, issueID); err != nil {
		return err
	}
	tx.Touch(r.ProjectID, sqlite.KindIssue, issueID)
	return nil
}

// rerequestResultApproval asks for a RESULT decision on the issue's carried
// forward, verified output in the run's current generation.
func (a *App) rerequestResultApproval(ctx context.Context, tx *sqlite.Tx, p *sqlite.Project, r *sqlite.Run, issueID string, decided map[string]bool) error {
	o, err := sqlite.CurrentOutput(ctx, tx, r.ID, issueID, r.Generation)
	if err != nil || o == nil || o.Verification != "PASSED" || !decided[o.ID] {
		return err
	}
	rows, err := tx.QueryContext(ctx, "SELECT check_name FROM check_runs WHERE subject_kind='output' AND subject_id=? AND result='PASSED' ORDER BY started_at, id", o.ID)
	if err != nil {
		return err
	}
	checks := []string{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			return err
		}
		checks = append(checks, n)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	ap, err := a.requestApproval(ctx, tx, approvalSpec{Kind: domain.ApprovalResult, ProjectID: p.ID, RunID: r.ID, RunGeneration: r.Generation,
		IssueID: issueID, OutputID: o.ID, PolicyVersion: p.PolicyVersion,
		Subject: map[string]any{"source_sha": o.SourceSHA, "manifest_digest": o.ManifestDigest, "checks": checks}})
	if err != nil {
		return err
	}
	if projectConfig(p).Approval.Result == "auto" {
		return a.approveAutomatically(ctx, tx, ap)
	}
	return nil
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
