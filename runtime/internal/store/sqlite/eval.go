package sqlite

import (
	"context"
	"sort"

	"github.com/Mineru98/samsara/runtime/internal/domain"
)

// BlockingReason explains why an issue cannot proceed (contract §8.2).
type BlockingReason struct {
	Code              string `json:"code"`
	SourceIssueID     string `json:"source_issue_id,omitempty"`
	RequiredMilestone string `json:"required_milestone,omitempty"`
	DownstreamPhase   string `json:"downstream_phase,omitempty"`
	ApprovalID        string `json:"approval_id,omitempty"`
	DependencyID      string `json:"dependency_id,omitempty"`
	Message           string `json:"message"`
}

// MilestoneResult is the evaluation of one dependency edge.
type MilestoneResult struct {
	Met      bool
	OutputID string
	Reason   *BlockingReason
}

// EvaluateEdge decides whether the predecessor reached the required milestone
// in the run's current generation. Backend observations (herdr done, Stop
// hooks, GitHub closed) are never inputs here (R-05).
func EvaluateEdge(ctx context.Context, q Q, e *Edge) (MilestoneResult, error) {
	run, err := GetRun(ctx, q, e.RunID)
	if err != nil {
		return MilestoneResult{}, err
	}
	gen := run.Generation
	if e.RequiredGeneration > gen {
		gen = e.RequiredGeneration
	}
	var out *Output
	if e.RequiredOutputID != "" {
		o, err := GetOutput(ctx, q, e.RequiredOutputID)
		if err != nil {
			return MilestoneResult{}, err
		}
		if o.SupersededBy == "" {
			out = o
		}
	} else {
		out, err = CurrentOutput(ctx, q, e.RunID, e.Predecessor, gen)
		if err != nil {
			return MilestoneResult{}, err
		}
	}
	reason := func(code, msg string) MilestoneResult {
		return MilestoneResult{Reason: &BlockingReason{Code: code, SourceIssueID: e.Predecessor, RequiredMilestone: e.Milestone,
			DownstreamPhase: e.Phase, DependencyID: e.ID, Message: msg}}
	}
	if out == nil {
		return reason("DEPENDENCY_NOT_IMPLEMENTED", "선행 이슈의 산출물이 아직 제출되지 않았습니다."), nil
	}
	if e.Milestone == domain.MilestoneImplemented {
		return MilestoneResult{Met: true, OutputID: out.ID}, nil
	}
	if out.Verification != "PASSED" {
		return reason("DEPENDENCY_NOT_VERIFIED", "선행 이슈의 산출물 검증이 완료되지 않았습니다."), nil
	}
	if e.Milestone == domain.MilestoneVerified {
		return MilestoneResult{Met: true, OutputID: out.ID}, nil
	}
	if e.Milestone == domain.MilestoneResultApprove {
		apps, err := ApprovalsWhere(ctx, q, "kind='RESULT' AND output_id=?", out.ID)
		if err != nil {
			return MilestoneResult{}, err
		}
		for _, a := range apps {
			if a.State == domain.ApprovalApproved {
				return MilestoneResult{Met: true, OutputID: out.ID}, nil
			}
		}
		r := reason("DEPENDENCY_APPROVAL_PENDING", "선행 이슈의 결과 승인이 필요합니다.")
		for _, a := range apps {
			if a.State == domain.ApprovalPending {
				r.Reason.ApprovalID = a.ID
				break
			}
		}
		return r, nil
	}
	// INTEGRATED
	t, err := FindTarget(ctx, q, e.TargetRepositoryID, e.TargetRef)
	if err != nil {
		return MilestoneResult{}, err
	}
	if t != nil {
		ev, err := EvidenceForOutput(ctx, q, out.ID, t.ID)
		if err != nil {
			return MilestoneResult{}, err
		}
		if ev != nil {
			return MilestoneResult{Met: true, OutputID: out.ID}, nil
		}
	}
	return reason("DEPENDENCY_NOT_INTEGRATED", "선행 이슈의 변경이 통합 대상에 반영되지 않았습니다."), nil
}

// ExecutionBlockers returns unmet EXECUTION-phase edges of an issue.
func ExecutionBlockers(ctx context.Context, q Q, runID, issueID string) ([]BlockingReason, error) {
	return blockersForPhase(ctx, q, runID, issueID, domain.PhaseExecution)
}

// IntegrationBlockers returns unmet INTEGRATION-phase edges of an issue.
func IntegrationBlockers(ctx context.Context, q Q, runID, issueID string) ([]BlockingReason, error) {
	return blockersForPhase(ctx, q, runID, issueID, domain.PhaseIntegration)
}

func blockersForPhase(ctx context.Context, q Q, runID, issueID, phase string) ([]BlockingReason, error) {
	edges, err := EdgesInto(ctx, q, runID, issueID)
	if err != nil {
		return nil, err
	}
	var out []BlockingReason
	for _, e := range edges {
		if e.Phase != phase {
			continue
		}
		r, err := EvaluateEdge(ctx, q, e)
		if err != nil {
			return nil, err
		}
		if !r.Met {
			out = append(out, *r.Reason)
		}
	}
	return out, nil
}

// Gate is the projection of all requirements of one successor/phase (§4.2).
type Gate struct {
	ID                string       `json:"id"`
	RunID             string       `json:"run_id"`
	RunGeneration     string       `json:"run_generation"`
	SuccessorIssueID  string       `json:"successor_issue_id"`
	DownstreamPhase   string       `json:"downstream_phase"`
	Members           []GateMember `json:"members"`
	Open              bool         `json:"open"`
	MembershipVersion string       `json:"membership_version"`
	MembershipDigest  string       `json:"membership_digest"`
}

// GateMember is one requirement of a gate.
type GateMember struct {
	DependencyID      string `json:"dependency_id"`
	IssueID           string `json:"issue_id"`
	RequiredMilestone string `json:"required_milestone"`
	Met               bool   `json:"met"`
}

// GatesForRun computes gates for successors with two or more requirements in
// the same phase (a single edge is shown as a plain edge).
func GatesForRun(ctx context.Context, q Q, runID string, now int64) ([]Gate, error) {
	run, err := GetRun(ctx, q, runID)
	if err != nil {
		return nil, err
	}
	edges, err := EdgesOfRun(ctx, q, runID)
	if err != nil {
		return nil, err
	}
	type key struct{ succ, phase string }
	groups := map[key][]*Edge{}
	for _, e := range edges {
		k := key{e.Successor, e.Phase}
		groups[k] = append(groups[k], e)
	}
	var out []Gate
	for k, es := range groups {
		if len(es) < 2 {
			continue
		}
		sort.Slice(es, func(i, j int) bool { return es[i].Predecessor < es[j].Predecessor })
		reqs := make([]map[string]string, 0, len(es))
		g := Gate{RunID: runID, RunGeneration: domain.Dec(run.Generation), SuccessorIssueID: k.succ, DownstreamPhase: k.phase, Open: true}
		var maxGraph int64
		for _, e := range es {
			r, err := EvaluateEdge(ctx, q, e)
			if err != nil {
				return nil, err
			}
			g.Members = append(g.Members, GateMember{DependencyID: e.ID, IssueID: e.Predecessor, RequiredMilestone: e.Milestone, Met: r.Met})
			g.Open = g.Open && r.Met
			reqs = append(reqs, map[string]string{"p": e.Predecessor, "m": e.Milestone, "t": e.TargetRef})
			if e.GraphVersion > maxGraph {
				maxGraph = e.GraphVersion
			}
		}
		g.MembershipDigest = domain.Digest(reqs)
		g.MembershipVersion = domain.Dec(maxGraph)
		g.ID = "gate_" + domain.Digest(map[string]any{"run": runID, "gen": run.Generation, "succ": k.succ, "phase": k.phase, "reqs": reqs})[7:23]
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
