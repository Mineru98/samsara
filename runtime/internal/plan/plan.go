// Package plan parses and validates structured execution plans submitted by
// Skills (contract §4.6). Validation here is pure: it never touches the DB,
// creates workers or runs check commands.
package plan

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/Mineru98/samsara/runtime/internal/config"
	"github.com/Mineru98/samsara/runtime/internal/domain"
)

// SpecVersion is the only accepted plan schema version.
const SpecVersion = "1"

// Plan is the structured plan document.
type Plan struct {
	SpecVersion          string       `json:"spec_version"`
	ProjectID            string       `json:"project_id"`
	ExpectedGraphVersion string       `json:"expected_graph_version"`
	Issues               []Issue      `json:"issues"`
	Dependencies         []Dependency `json:"dependencies"`
}

// Issue is one planned work item addressed by its client_key alias.
type Issue struct {
	ClientKey            string   `json:"client_key"`
	Title                string   `json:"title"`
	AcceptanceCriteria   []string `json:"acceptance_criteria"`
	CheckProfile         string   `json:"check_profile,omitempty"`
	RequiredCapabilities []string `json:"required_capabilities,omitempty"`
	TargetRepositoryID   string   `json:"target_repository_id,omitempty"`
	TargetRef            string   `json:"target_ref,omitempty"`
}

// Dependency links two aliases.
type Dependency struct {
	PredecessorKey     string `json:"predecessor_key"`
	SuccessorKey       string `json:"successor_key"`
	RequiredMilestone  string `json:"required_milestone,omitempty"`
	DownstreamPhase    string `json:"downstream_phase,omitempty"`
	TargetRepositoryID string `json:"target_repository_id,omitempty"`
	TargetRef          string `json:"target_ref,omitempty"`
}

// Problem is one validation finding.
type Problem struct {
	Code    string `json:"code"`
	Path    string `json:"path"`
	Message string `json:"message"`
}

// KnownCapabilities lists capability names a plan may require.
var KnownCapabilities = map[string]bool{
	"create_worker": true, "attach_session": true, "submit_prompt": true, "observe_process": true,
	"observe_agent": true, "read_output": true, "request_interrupt": true, "restore_conversation": true,
}

// Parse decodes a plan strictly (unknown fields are errors).
func Parse(data []byte) (*Plan, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	p := &Plan{}
	if err := dec.Decode(p); err != nil {
		return nil, domain.Errf(domain.CodeInvalidRequest, "plan: %v", err)
	}
	if dec.Decode(&struct{}{}) != io.EOF {
		return nil, domain.Errf(domain.CodeInvalidRequest, "plan: trailing data after JSON document")
	}
	return p, nil
}

// Normalize fills documented defaults (INTEGRATED + EXECUTION, §4.1).
func (p *Plan) Normalize(defaultTargetRef string) {
	for i := range p.Dependencies {
		d := &p.Dependencies[i]
		if d.RequiredMilestone == "" {
			d.RequiredMilestone = domain.MilestoneIntegrated
		}
		if d.DownstreamPhase == "" {
			d.DownstreamPhase = domain.PhaseExecution
		}
		if d.RequiredMilestone == domain.MilestoneIntegrated && d.TargetRef == "" {
			d.TargetRef = defaultTargetRef
		}
	}
	for i := range p.Issues {
		if p.Issues[i].AcceptanceCriteria == nil {
			p.Issues[i].AcceptanceCriteria = []string{}
		}
		if p.Issues[i].RequiredCapabilities == nil {
			p.Issues[i].RequiredCapabilities = []string{}
		}
	}
}

// Validate performs structural checks: schema, duplicate/dangling keys,
// enums, refs and cycles. Project-level checks (trust, profiles, targets)
// are done by the runtime with DB access.
func (p *Plan) Validate() []Problem {
	var ps []Problem
	add := func(code, path, f string, a ...any) {
		ps = append(ps, Problem{Code: code, Path: path, Message: fmt.Sprintf(f, a...)})
	}
	if p.SpecVersion != SpecVersion {
		add("UNSUPPORTED_SPEC_VERSION", "spec_version", "spec_version must be %q", SpecVersion)
	}
	if p.ProjectID == "" {
		add("REQUIRED", "project_id", "project_id is required")
	}
	if _, err := domain.ParseDec(p.ExpectedGraphVersion); err != nil || p.ExpectedGraphVersion == "" {
		add("INVALID_DECIMAL", "expected_graph_version", "expected_graph_version must be a decimal string")
	}
	if len(p.Issues) == 0 {
		add("REQUIRED", "issues", "at least one issue is required")
	}
	keys := map[string]int{}
	for i, is := range p.Issues {
		path := fmt.Sprintf("issues[%d]", i)
		if strings.TrimSpace(is.ClientKey) == "" {
			add("REQUIRED", path+".client_key", "client_key is required")
		} else if _, dup := keys[is.ClientKey]; dup {
			add("DUPLICATE_KEY", path+".client_key", "duplicate client_key %q", is.ClientKey)
		} else {
			keys[is.ClientKey] = i
		}
		if strings.TrimSpace(is.Title) == "" {
			add("REQUIRED", path+".title", "title is required")
		}
		for j, c := range is.RequiredCapabilities {
			if !KnownCapabilities[c] {
				add("UNKNOWN_CAPABILITY", fmt.Sprintf("%s.required_capabilities[%d]", path, j), "unknown capability %q", c)
			}
		}
		if is.CheckProfile != "" && strings.ContainsAny(is.CheckProfile, " \t\n;|&$`") {
			add("INVALID_CHECK_PROFILE", path+".check_profile", "check_profile must be a registered profile name, not a command")
		}
		if is.TargetRef != "" {
			if err := config.ValidateFullRef(is.TargetRef); err != nil {
				add("INVALID_REF", path+".target_ref", "%v", err)
			}
		}
	}
	edgeSeen := map[[2]string]bool{}
	adj := map[string][]string{}
	for i, d := range p.Dependencies {
		path := fmt.Sprintf("dependencies[%d]", i)
		_, okP := keys[d.PredecessorKey]
		_, okS := keys[d.SuccessorKey]
		if !okP {
			add("DANGLING_DEPENDENCY", path+".predecessor_key", "unknown predecessor_key %q", d.PredecessorKey)
		}
		if !okS {
			add("DANGLING_DEPENDENCY", path+".successor_key", "unknown successor_key %q", d.SuccessorKey)
		}
		if d.PredecessorKey == d.SuccessorKey && d.PredecessorKey != "" {
			add("SELF_DEPENDENCY", path, "an issue cannot depend on itself")
		}
		if !domain.ValidMilestone(d.RequiredMilestone) {
			add("INVALID_ENUM", path+".required_milestone", "required_milestone %q is invalid", d.RequiredMilestone)
		}
		if !domain.ValidPhase(d.DownstreamPhase) {
			add("INVALID_ENUM", path+".downstream_phase", "downstream_phase %q is invalid", d.DownstreamPhase)
		}
		if d.RequiredMilestone == domain.MilestoneIntegrated {
			if err := config.ValidateFullRef(d.TargetRef); err != nil {
				add("INVALID_REF", path+".target_ref", "INTEGRATED dependency needs a full target_ref: %v", err)
			}
		}
		k := [2]string{d.PredecessorKey, d.SuccessorKey}
		if edgeSeen[k] {
			add("DUPLICATE_DEPENDENCY", path, "duplicate dependency %s -> %s", d.PredecessorKey, d.SuccessorKey)
		}
		edgeSeen[k] = true
		if okP && okS {
			adj[d.PredecessorKey] = append(adj[d.PredecessorKey], d.SuccessorKey)
		}
	}
	if cyc := FindCycle(adj); len(cyc) > 0 {
		add(domain.CodeCycleDetected, "dependencies", "dependency cycle: %s", strings.Join(cyc, " -> "))
	}
	return ps
}

// FindCycle returns one cycle (as a closed path) in a directed graph, or nil.
func FindCycle(adj map[string][]string) []string {
	const (
		white = 0
		grey  = 1
		black = 2
	)
	color := map[string]int{}
	parent := map[string]string{}
	nodes := make([]string, 0, len(adj))
	for n := range adj {
		nodes = append(nodes, n)
	}
	sort.Strings(nodes)
	var cycle []string
	var visit func(string) bool
	visit = func(u string) bool {
		color[u] = grey
		next := append([]string(nil), adj[u]...)
		sort.Strings(next)
		for _, v := range next {
			if color[v] == grey {
				cycle = []string{v}
				for x := u; x != v; x = parent[x] {
					cycle = append(cycle, x)
				}
				cycle = append(cycle, v)
				for i, j := 0, len(cycle)-1; i < j; i, j = i+1, j-1 {
					cycle[i], cycle[j] = cycle[j], cycle[i]
				}
				return true
			}
			if color[v] == white {
				parent[v] = u
				if visit(v) {
					return true
				}
			}
		}
		color[u] = black
		return false
	}
	for _, n := range nodes {
		if color[n] == white && visit(n) {
			return cycle
		}
	}
	return nil
}

// Digest is the canonical plan digest used for idempotency and PLAN
// approval binding. expected_graph_version is excluded because it is a
// precondition, not plan content.
func (p *Plan) Digest() string {
	c := *p
	c.ExpectedGraphVersion = ""
	return domain.Digest(c)
}

// Problems2Error converts findings into one INVALID_REQUEST error.
func Problems2Error(ps []Problem) error {
	if len(ps) == 0 {
		return nil
	}
	code := domain.CodeInvalidRequest
	for _, p := range ps {
		if p.Code == domain.CodeCycleDetected {
			code = domain.CodeCycleDetected
		}
	}
	msgs := make([]string, len(ps))
	for i, p := range ps {
		msgs[i] = p.Path + ": " + p.Message
	}
	return (&domain.Error{Code: code, Message: "plan validation failed: " + strings.Join(msgs, "; ")}).WithDetails("problems", ps)
}

// SemanticDigest is the digest of the plan's execution meaning: titles are
// display metadata and excluded, so a title-only revision keeps the PLAN
// approval subject (01 §12.4).
func (p *Plan) SemanticDigest() string {
	// Keyed by client_key so reordering the document is not a change.
	issues := map[string]Issue{}
	for _, is := range p.Issues {
		is.Title = ""
		issues[is.ClientKey] = is
	}
	deps := map[string]Dependency{}
	for _, d := range p.Dependencies {
		deps[d.PredecessorKey+"\x00"+d.SuccessorKey] = d
	}
	return domain.Digest(map[string]any{"spec_version": p.SpecVersion, "project_id": p.ProjectID, "issues": issues, "dependencies": deps})
}

// Change kinds of a revision.
const (
	ChangeMetadataOnly = "METADATA_ONLY"
	ChangeExecution    = "EXECUTION"
)

// Revision is the body of plan revise: the full new plan for a PAUSED run.
type Revision struct {
	RunID                 string          `json:"run_id"`
	ExpectedRunGeneration string          `json:"expected_run_generation"`
	Plan                  json.RawMessage `json:"plan"`
}

// ParseRevision decodes a revise request strictly and parses its plan.
func ParseRevision(data []byte) (*Revision, *Plan, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	r := &Revision{}
	if err := dec.Decode(r); err != nil {
		return nil, nil, domain.Errf(domain.CodeInvalidRequest, "revision: %v", err)
	}
	if dec.Decode(&struct{}{}) != io.EOF {
		return nil, nil, domain.Errf(domain.CodeInvalidRequest, "revision: trailing data after JSON document")
	}
	if r.RunID == "" {
		return nil, nil, domain.Errf(domain.CodeInvalidRequest, "revision: run_id is required")
	}
	if _, err := domain.ParseDec(r.ExpectedRunGeneration); err != nil || r.ExpectedRunGeneration == "" {
		return nil, nil, domain.Errf(domain.CodeInvalidRequest, "revision: expected_run_generation must be a decimal string")
	}
	if len(r.Plan) == 0 {
		return nil, nil, domain.Errf(domain.CodeInvalidRequest, "revision: plan is required")
	}
	p, err := Parse(r.Plan)
	if err != nil {
		return nil, nil, err
	}
	return r, p, nil
}

// Delta classifies the difference between the current and the revised plan
// by client_key. Both plans must be normalized.
type Delta struct {
	Added    []string // new issues
	Removed  []string // issues missing from the revision
	Changed  []string // issues whose execution fields changed
	Retitled []string // issues whose title changed
	// EdgeChanged lists successors whose incoming dependency set changed.
	EdgeChanged []string
}

// Kind reports whether the revision changes execution meaning.
func (d Delta) Kind() string {
	if len(d.Added)+len(d.Removed)+len(d.Changed)+len(d.EdgeChanged) == 0 {
		return ChangeMetadataOnly
	}
	return ChangeExecution
}

// Compare computes the revision delta from old to new.
func Compare(old, new *Plan) Delta {
	var d Delta
	oldIssues := map[string]Issue{}
	for _, is := range old.Issues {
		oldIssues[is.ClientKey] = is
	}
	newKeys := map[string]bool{}
	for _, is := range new.Issues {
		newKeys[is.ClientKey] = true
		o, ok := oldIssues[is.ClientKey]
		if !ok {
			d.Added = append(d.Added, is.ClientKey)
			continue
		}
		if o.Title != is.Title {
			d.Retitled = append(d.Retitled, is.ClientKey)
		}
		o.Title, is.Title = "", ""
		if domain.Digest(o) != domain.Digest(is) {
			d.Changed = append(d.Changed, is.ClientKey)
		}
	}
	for _, is := range old.Issues {
		if !newKeys[is.ClientKey] {
			d.Removed = append(d.Removed, is.ClientKey)
		}
	}
	edges := func(p *Plan) map[string]map[string]string {
		m := map[string]map[string]string{}
		for _, e := range p.Dependencies {
			if m[e.SuccessorKey] == nil {
				m[e.SuccessorKey] = map[string]string{}
			}
			m[e.SuccessorKey][e.PredecessorKey] = domain.Digest(e)
		}
		return m
	}
	oe, ne := edges(old), edges(new)
	succ := map[string]bool{}
	for s := range oe {
		succ[s] = true
	}
	for s := range ne {
		succ[s] = true
	}
	for s := range succ {
		if domain.Digest(oe[s]) != domain.Digest(ne[s]) && newKeys[s] {
			d.EdgeChanged = append(d.EdgeChanged, s)
		}
	}
	sort.Strings(d.EdgeChanged)
	return d
}

// Affected returns the client keys whose prerequisites changed: changed
// issues, successors of changed edges and all their descendants in the new
// plan (invalidation policy PAUSE_DESCENDANTS). Added issues are excluded.
func Affected(new *Plan, d Delta) []string {
	adj := map[string][]string{}
	for _, e := range new.Dependencies {
		adj[e.PredecessorKey] = append(adj[e.PredecessorKey], e.SuccessorKey)
	}
	added := map[string]bool{}
	for _, k := range d.Added {
		added[k] = true
	}
	seen := map[string]bool{}
	queue := append(append([]string{}, d.Changed...), d.EdgeChanged...)
	for len(queue) > 0 {
		k := queue[0]
		queue = queue[1:]
		if seen[k] {
			continue
		}
		seen[k] = true
		queue = append(queue, adj[k]...)
	}
	var out []string
	for k := range seen {
		if !added[k] {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}
