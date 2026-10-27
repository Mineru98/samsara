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
