package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Mineru98/samsara/runtime/internal/backend"
	"github.com/Mineru98/samsara/runtime/internal/domain"
	"github.com/Mineru98/samsara/runtime/internal/hooks"
	"github.com/Mineru98/samsara/runtime/internal/procinfo"
	"github.com/Mineru98/samsara/runtime/internal/project"
	"github.com/Mineru98/samsara/runtime/internal/store/sqlite"
)

// HookIdentity is the common part of every hook IPC request.
type HookIdentity struct {
	ProtocolVersion string            `json:"protocol_version"`
	RequestID       string            `json:"request_id"`
	Event           string            `json:"event"`
	ClaudeSessionID string            `json:"claude_session_id"`
	Cwd             string            `json:"cwd"`
	Ticket          string            `json:"ticket,omitempty"`
	ManagedHint     bool              `json:"managed_hint"`
	Process         map[string]string `json:"process,omitempty"` // procinfo identity of the Claude process
	Source          string            `json:"source,omitempty"`  // SessionStart source (startup|resume|clear|compact)
}

// RegisterResult is returned to the SessionStart bridge.
type RegisterResult struct {
	Managed   bool   `json:"managed"`
	SessionID string `json:"session_id,omitempty"`
	AttemptID string `json:"attempt_id,omitempty"`
	LeaseID   string `json:"lease_id,omitempty"`
	Fence     string `json:"fence,omitempty"`
	Context   string `json:"context,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

// attemptForTicket resolves a launch ticket to its attempt and current binding.
func (a *App) attemptForTicket(ctx context.Context, ticket string) (*sqlite.Attempt, *sqlite.Binding, error) {
	if ticket == "" {
		return nil, nil, domain.Errf(domain.CodeActionForbidden, "missing launch ticket")
	}
	var att *sqlite.Attempt
	var b *sqlite.Binding
	err := a.DB.Read(ctx, func(q sqlite.Q) error {
		var attemptID string
		err := q.QueryRowContext(ctx, "SELECT attempt_id FROM launch_tickets WHERE ticket_hash=?", domain.HashSecret(ticket)).Scan(&attemptID)
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Errf(domain.CodeActionForbidden, "unknown launch ticket")
		}
		if err != nil {
			return err
		}
		if att, err = sqlite.GetAttempt(ctx, q, attemptID); err != nil {
			return err
		}
		b, err = sqlite.CurrentBindingForAttempt(ctx, q, attemptID)
		return err
	})
	return att, b, err
}

// observeBinding asks the binding's own backend (never another one, R-11).
func (a *App) observeBinding(ctx context.Context, b *sqlite.Binding) (backend.Observation, error) {
	be, ok := a.Backends.Get(b.Kind)
	if !ok {
		return backend.Observation{Liveness: domain.LivenessUnknown}, fmt.Errorf("backend %s not available", b.Kind)
	}
	ref := bindingRef(b)
	return be.Observe(ctx, ref)
}

func bindingRef(b *sqlite.Binding) backend.Ref {
	fields := map[string]string{}
	_ = json.Unmarshal([]byte(b.OpaqueRef), &fields)
	return backend.Ref{Kind: b.Kind, Instance: b.Instance, Fields: fields, Generation: b.Generation}
}

// findProjectForCwd returns the project owning cwd (registered root or a
// worktree record), or nil.
func findProjectForCwd(ctx context.Context, q sqlite.Q, cwd string) (*sqlite.Project, *sqlite.Worktree, error) {
	ws, err := sqlite.ListWorktreesAll(ctx, q)
	if err != nil {
		return nil, nil, err
	}
	var bestW *sqlite.Worktree
	for _, w := range ws {
		if w.State != domain.WorktreeRemoved && project.Within(cwd, w.Path) && (bestW == nil || len(w.Path) > len(bestW.Path)) {
			bestW = w
		}
	}
	if bestW != nil {
		p, err := sqlite.GetProject(ctx, q, bestW.ProjectID)
		return p, bestW, err
	}
	ps, err := sqlite.ListProjects(ctx, q)
	if err != nil {
		return nil, nil, err
	}
	var best *sqlite.Project
	for _, p := range ps {
		if project.Within(cwd, p.RootPath) && (best == nil || len(p.RootPath) > len(best.RootPath)) {
			best = p
		}
	}
	return best, nil, nil
}

func canonicalOrClean(p string) string {
	if c, err := project.Canonical(p); err == nil {
		return c
	}
	return filepath.Clean(p)
}

// RegisterSession handles SessionStart (contract §10.2). With a launch
// ticket it completes a managed worker registration and grants the lease
// only after readiness checks. Without a ticket it records an observation
// session; it never turns an ordinary session into a worker.
func (a *App) RegisterSession(ctx context.Context, req HookIdentity) (*RegisterResult, error) {
	if err := a.requireReady(); err != nil {
		return nil, err
	}
	if req.ClaudeSessionID == "" {
		return nil, domain.Errf(domain.CodeInvalidRequest, "claude_session_id required")
	}
	req.Cwd = canonicalOrClean(req.Cwd)
	if req.Ticket != "" {
		return a.registerManaged(ctx, req)
	}
	res := &RegisterResult{}
	err := a.DB.Write(ctx, func(tx *sqlite.Tx) error {
		p, _, err := findProjectForCwd(ctx, tx, req.Cwd)
		if err != nil {
			return err
		}
		if p == nil {
			res.Reason = domain.ReasonUnmanaged
			return nil
		}
		s, err := a.upsertSession(ctx, tx, p.ID, req, false)
		if err != nil {
			return err
		}
		res.SessionID = s.ID
		if s.Managed {
			res.Managed = true
			if as, _ := sqlite.ActiveAssignmentForSession(ctx, tx, s.ID); as != nil {
				res.AttemptID = as.AttemptID
				if err := a.checkGenerationChange(ctx, tx, s, req); err != nil {
					return err
				}
			}
			res.Context = "삼사라 관리 세션입니다. 도구 실행 전 실행권을 검사합니다."
		} else if p.Activation == domain.ActivationEnabled {
			res.Context = "삼사라가 활성화된 프로젝트입니다. 이 세션은 관찰만 하며, 작업 연결은 `samsara session attach --issue <id>`로 명시적으로 합니다."
		}
		return nil
	})
	return res, err
}

// upsertSession creates or refreshes the session for a Claude session id.
func (a *App) upsertSession(ctx context.Context, tx *sqlite.Tx, projectID string, req HookIdentity, managed bool) (*sqlite.Session, error) {
	pid, _ := json.Marshal(req.Process)
	s, err := sqlite.FindSessionByClaudeID(ctx, tx, req.ClaudeSessionID)
	if err != nil {
		return nil, err
	}
	if s == nil {
		id := domain.NewID("ses")
		if _, err := tx.ExecContext(ctx, `INSERT INTO sessions(id,project_id,claude_session_id,cwd,managed,liveness,input_state,source,confidence,process_identity,observed_at,last_hook_at,created_at)
			VALUES(?,?,?,?,?,'ALIVE','UNKNOWN','hook','MEDIUM',?,?,?,?)`, id, projectID, req.ClaudeSessionID, req.Cwd, managed, string(pid), tx.Now, tx.Now, tx.Now); err != nil {
			return nil, err
		}
		tx.Touch(projectID, sqlite.KindSession, id)
		tx.Event(projectID, "session.registered", "session", id, 1)
		return sqlite.GetSession(ctx, tx, id)
	}
	if _, err := tx.ExecContext(ctx, "UPDATE sessions SET cwd=?, liveness='ALIVE', last_hook_at=?, observed_at=?, managed=MAX(managed,?), version=version+1 WHERE id=?",
		req.Cwd, tx.Now, tx.Now, managed, s.ID); err != nil {
		return nil, err
	}
	tx.Touch(s.ProjectID, sqlite.KindSession, s.ID)
	return sqlite.GetSession(ctx, tx, s.ID)
}

// checkGenerationChange revokes authority when the same Claude session id
// re-appears in a different process (resume in a new process): the old
// credential is never inherited (01 §11.2).
func (a *App) checkGenerationChange(ctx context.Context, tx *sqlite.Tx, s *sqlite.Session, req HookIdentity) error {
	if len(req.Process) == 0 {
		return nil
	}
	var old map[string]string
	_ = json.Unmarshal([]byte(s.ProcessIdentity), &old)
	if len(old) == 0 || (old["pid"] == req.Process["pid"] && old["start_time"] == req.Process["start_time"]) {
		return nil
	}
	pid, _ := json.Marshal(req.Process)
	if _, err := tx.ExecContext(ctx, "UPDATE sessions SET process_identity=?, version=version+1 WHERE id=?", string(pid), s.ID); err != nil {
		return err
	}
	as, err := sqlite.ActiveAssignmentForSession(ctx, tx, s.ID)
	if err != nil || as == nil {
		return err
	}
	att, err := sqlite.GetAttempt(ctx, tx, as.AttemptID)
	if err != nil {
		return err
	}
	if err := a.revokeAttemptLease(ctx, tx, att, domain.LeaseRevoked, "PROCESS_GENERATION_CHANGED"); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE execution_attempts SET quarantine_reason='PROCESS_GENERATION_CHANGED', version=version+1, updated_at=? WHERE id=?", tx.Now, att.ID); err != nil {
		return err
	}
	tx.Touch(att.ProjectID, sqlite.KindAttempt, att.ID)
	tx.Touch(att.ProjectID, sqlite.KindIssue, att.IssueID)
	return nil
}

func (a *App) registerManaged(ctx context.Context, req HookIdentity) (*RegisterResult, error) {
	res := &RegisterResult{Managed: true}
	claude := a.Claude()
	var syncReg bool
	err := a.DB.Write(ctx, func(tx *sqlite.Tx) error {
		var attemptID string
		var expires, used sql.NullInt64
		err := tx.QueryRowContext(ctx, "SELECT attempt_id, expires_at, used_at FROM launch_tickets WHERE ticket_hash=?", domain.HashSecret(req.Ticket)).Scan(&attemptID, &expires, &used)
		if errors.Is(err, sql.ErrNoRows) {
			res.Reason = "UNKNOWN_TICKET"
			return nil
		}
		if err != nil {
			return err
		}
		att, err := sqlite.GetAttempt(ctx, tx, attemptID)
		if err != nil {
			return err
		}
		res.AttemptID = att.ID
		if used.Valid {
			// SessionStart repeats on /clear or compaction in the same process.
			s, err := sqlite.FindSessionByClaudeID(ctx, tx, req.ClaudeSessionID)
			if err != nil {
				return err
			}
			as, _ := sqlite.ActiveAssignmentForAttempt(ctx, tx, att.ID)
			if s == nil || as == nil || as.SessionID != s.ID {
				res.Reason = "TICKET_ALREADY_USED"
				return tx.Audit(ctx, att.ProjectID, "hook", "session.register", "attempt", att.ID, domain.DecisionDeny, "TICKET_ALREADY_USED", nil)
			}
			res.SessionID = s.ID
			if err := a.checkGenerationChange(ctx, tx, s, req); err != nil {
				return err
			}
			res.Context = workerContext(att)
			return nil
		}
		if expires.Valid && tx.Now > expires.Int64 {
			res.Reason = "TICKET_EXPIRED"
			return nil
		}
		if att.State != domain.AttemptStarting {
			res.Reason = "ATTEMPT_" + att.State
			return nil
		}
		w, err := sqlite.GetWorktree(ctx, tx, att.WorktreeID)
		if err != nil {
			return err
		}
		if !project.Within(req.Cwd, w.Path) {
			res.Reason = domain.ReasonCwdOutOfScope
			return nil
		}
		p, err := sqlite.GetProject(ctx, tx, att.ProjectID)
		if err != nil {
			return err
		}
		s, err := a.upsertSession(ctx, tx, p.ID, req, true)
		if err != nil {
			return err
		}
		res.SessionID = s.ID
		pid, _ := json.Marshal(req.Process)
		if _, err := tx.ExecContext(ctx, "UPDATE sessions SET process_identity=?, input_owner='automation', source='launcher' WHERE id=?", string(pid), s.ID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE launch_tickets SET used_at=? WHERE ticket_hash=?", tx.Now, domain.HashSecret(req.Ticket)); err != nil {
			return err
		}
		b, err := sqlite.CurrentBindingForAttempt(ctx, tx, att.ID)
		if err != nil {
			return err
		}
		var bindingID any
		if b != nil {
			if _, err := tx.ExecContext(ctx, "UPDATE backend_bindings SET session_id=? WHERE id=?", s.ID, b.ID); err != nil {
				return err
			}
			bindingID = b.ID
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO session_assignments(id,session_id,attempt_id,binding_id,attached_at) VALUES(?,?,?,?,?)",
			domain.NewID("asg"), s.ID, att.ID, bindingID, tx.Now); err != nil {
			return err
		}
		syncReg = true
		// Readiness: policy gate must be enforceable before any authority.
		if reason := a.readinessBlock(claude, w.Path); reason != "" {
			res.Reason = reason
			if _, err := tx.ExecContext(ctx, "UPDATE execution_attempts SET state='FAILED', state_reason=?, version=version+1, updated_at=? WHERE id=?", reason, tx.Now, att.ID); err != nil {
				return err
			}
			tx.Touch(p.ID, sqlite.KindAttempt, att.ID)
			tx.Touch(p.ID, sqlite.KindIssue, att.IssueID)
			return tx.Audit(ctx, p.ID, "hook", "session.register", "attempt", att.ID, domain.DecisionDeny, reason, nil)
		}
		if reason := a.policyBlock(ctx, tx, p, att); reason != "" {
			res.Reason = reason
			if _, err := tx.ExecContext(ctx, "UPDATE execution_attempts SET state='PAUSED', state_reason=?, version=version+1, updated_at=? WHERE id=?", reason, tx.Now, att.ID); err != nil {
				return err
			}
			tx.Touch(p.ID, sqlite.KindAttempt, att.ID)
			tx.Touch(p.ID, sqlite.KindIssue, att.IssueID)
			return nil
		}
		bid := ""
		if b != nil {
			bid = b.ID
		}
		l, err := a.issueLease(ctx, tx, att, s.ID, bid, a.leaseTTL(projectConfig(p)).Milliseconds())
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE execution_attempts SET state='RUNNING', state_reason='', version=version+1, updated_at=? WHERE id=?", tx.Now, att.ID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE worktrees SET state='IN_USE', version=version+1, updated_at=? WHERE id=?", tx.Now, w.ID); err != nil {
			return err
		}
		tx.Touch(p.ID, sqlite.KindWorktree, w.ID)
		tx.Touch(p.ID, sqlite.KindIssue, att.IssueID)
		tx.Event(p.ID, "attempt.running", "attempt", att.ID, att.Version+1)
		res.LeaseID, res.Fence = l.ID, domain.Dec(l.Fence)
		res.Context = workerContext(att)
		return tx.Audit(ctx, p.ID, "hook", "session.register", "attempt", att.ID, domain.DecisionPass, "", map[string]string{"session_id": s.ID})
	})
	if err == nil && syncReg {
		err = a.SyncRegistry(ctx)
	}
	return res, err
}

// readinessBlock verifies that the managed policy hook can fail closed.
func (a *App) readinessBlock(claude ClaudeSupport, dir string) string {
	if !a.RequireHookReadiness {
		return ""
	}
	if !claude.BlockOnFailure {
		return "POLICY_GATE_UNSUPPORTED_CLAUDE_VERSION"
	}
	ok, err := hooks.PolicyHookInstalled(dir)
	if err != nil || !ok {
		return "POLICY_HOOK_NOT_INSTALLED"
	}
	return ""
}

func workerContext(att *sqlite.Attempt) string {
	return fmt.Sprintf("삼사라 관리 worker입니다 (attempt %s). 도구 실행마다 실행권을 검사합니다. 구현을 commit한 뒤 `samsara output submit`으로 결과를 제출하세요. 승인·통합은 런타임이 별도로 판정합니다.", att.ID)
}

// AttachRequest attaches an existing (Standalone) Claude session to an issue.
type AttachRequest struct {
	ClaudeSessionID string `json:"claude_session_id,omitempty"`
	AncestorPIDs    []int  `json:"ancestor_pids,omitempty"`
	IssueID         string `json:"issue_id"`
}

// AttachSession binds a user-started Claude session to an issue after an
// explicit user request (contract §10.2, second path).
func (a *App) AttachSession(ctx context.Context, cmd Cmd, req AttachRequest) (*RegisterResult, error) {
	if err := a.requireReady(); err != nil {
		return nil, err
	}
	claude := a.Claude()
	res := &RegisterResult{Managed: true}
	err := a.DB.Write(ctx, func(tx *sqlite.Tx) error {
		s, err := findSessionForAttach(ctx, tx, req)
		if err != nil {
			return err
		}
		p, err := sqlite.GetProject(ctx, tx, s.ProjectID)
		if err != nil {
			return err
		}
		if p.Activation != domain.ActivationEnabled {
			return domain.Errf(domain.CodeProjectNotEnabled, "project is %s", p.Activation)
		}
		if as, _ := sqlite.ActiveAssignmentForSession(ctx, tx, s.ID); as != nil {
			return domain.Errf(domain.CodeConflict, "session already assigned to attempt %s", as.AttemptID)
		}
		issue, err := sqlite.GetIssue(ctx, tx, req.IssueID)
		if err != nil {
			return err
		}
		if issue.ProjectID != p.ID {
			return domain.Errf(domain.CodeActionForbidden, "issue belongs to another project")
		}
		var run *sqlite.Run
		if err := func() error {
			var runID string
			if err := tx.QueryRowContext(ctx, "SELECT r.id FROM runs r JOIN run_issues ri ON ri.run_id=r.id WHERE ri.issue_id=? AND r.state='ACTIVE'", issue.ID).Scan(&runID); err != nil {
				return domain.Errf(domain.CodeConflict, "issue has no ACTIVE run")
			}
			run, err = sqlite.GetRun(ctx, tx, runID)
			return err
		}(); err != nil {
			return err
		}
		bl, err := sqlite.ExecutionBlockers(ctx, tx, run.ID, issue.ID)
		if err != nil {
			return err
		}
		if len(bl) > 0 {
			return domain.Errf(domain.CodeDependencyUnmet, "%s", bl[0].Message).WithDetails("blocking_reasons", bl)
		}
		cfg := projectConfig(p)
		att, err := sqlite.LatestAttempt(ctx, tx, run.ID, issue.ID)
		if err != nil {
			return err
		}
		var w *sqlite.Worktree
		switch {
		case att != nil && (att.State == domain.AttemptPending || (att.State == domain.AttemptPaused && att.StateReason == "MANUAL_ATTACH_REQUIRED")):
			if att.WorktreeID == "" {
				return domain.Errf(domain.CodeConflict, "attempt worktree is not ready yet")
			}
			if w, err = sqlite.GetWorktree(ctx, tx, att.WorktreeID); err != nil {
				return err
			}
			if w.State != domain.WorktreeReady && w.State != domain.WorktreeRetained {
				return domain.Errf(domain.CodeConflict, "worktree is %s", w.State)
			}
		case att != nil && domain.AttemptLive(att.State):
			return domain.Errf(domain.CodeConflict, "issue already has live attempt %s (%s)", att.ID, att.State)
		default:
			if cfg.Execution.ManagedWorktreeRequired {
				return domain.Errf(domain.CodeActionForbidden, "managed worktree required: wait for the scheduler to prepare it, then start Claude in that worktree")
			}
			top := s.Cwd
			if w, err = sqlite.GetWorktreeByPath(ctx, tx, top); err != nil {
				return err
			}
			if w == nil {
				w = &sqlite.Worktree{ID: domain.NewID("wt"), Path: top}
				if _, err := tx.ExecContext(ctx, "INSERT INTO worktrees(id,project_id,path,ownership,state,created_at,updated_at) VALUES(?,?,?,?,?,?,?)",
					w.ID, p.ID, top, domain.OwnershipExternal, domain.WorktreeReady, tx.Now, tx.Now); err != nil {
					return err
				}
			}
			att = &sqlite.Attempt{ID: domain.NewID("att"), ProjectID: p.ID, RunID: run.ID, IssueID: issue.ID, WorktreeID: w.ID, RunGeneration: run.Generation}
			if _, err := tx.ExecContext(ctx, `INSERT INTO execution_attempts(id,project_id,run_id,issue_id,run_generation,worktree_id,state,backend_kind,created_at,updated_at)
				VALUES(?,?,?,?,?,?,'PENDING','standalone',?,?)`, att.ID, p.ID, run.ID, issue.ID, run.Generation, w.ID, tx.Now, tx.Now); err != nil {
				return err
			}
		}
		if !project.Within(s.Cwd, w.Path) {
			return domain.Errf(domain.CodeActionForbidden, "session cwd %s is outside the attempt worktree %s", s.Cwd, w.Path)
		}
		if reason := a.readinessBlock(claude, w.Path); reason != "" {
			if r2 := a.readinessBlock(claude, p.RootPath); r2 != "" {
				return domain.Errf(domain.CodeUnsupportedCapability, "policy gate not ready: %s", reason)
			}
		}
		var proc map[string]string
		_ = json.Unmarshal([]byte(s.ProcessIdentity), &proc)
		ref := backend.Ref{Kind: domain.BackendStandalone, Fields: proc, Generation: proc["pid"] + "@" + proc["start_time"]}
		bid := domain.NewID("bnd")
		if _, err := tx.ExecContext(ctx, "INSERT INTO backend_bindings(id,session_id,attempt_id,kind,instance,opaque_ref,ref_key,generation,created_at) VALUES(?,?,?,?,?,?,?,?,?)",
			bid, s.ID, att.ID, domain.BackendStandalone, "", ref.JSON(), ref.Key(), ref.Generation, tx.Now); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO session_assignments(id,session_id,attempt_id,binding_id,attached_at) VALUES(?,?,?,?,?)",
			domain.NewID("asg"), s.ID, att.ID, bid, tx.Now); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE sessions SET managed=1, input_owner='user', version=version+1 WHERE id=?", s.ID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE execution_attempts SET state='STARTING', backend_kind='standalone', version=version+1, updated_at=? WHERE id=?", tx.Now, att.ID); err != nil {
			return err
		}
		att, err = sqlite.GetAttempt(ctx, tx, att.ID)
		if err != nil {
			return err
		}
		l, err := a.issueLease(ctx, tx, att, s.ID, bid, a.leaseTTL(cfg).Milliseconds())
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE execution_attempts SET state='RUNNING', state_reason='', resume_required=0, version=version+1, updated_at=? WHERE id=?", tx.Now, att.ID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE worktrees SET state='IN_USE', attempt_id=?, version=version+1, updated_at=? WHERE id=?", att.ID, tx.Now, w.ID); err != nil {
			return err
		}
		tx.Touch(p.ID, sqlite.KindSession, s.ID)
		tx.Touch(p.ID, sqlite.KindWorktree, w.ID)
		tx.Touch(p.ID, sqlite.KindIssue, issue.ID)
		tx.Touch(p.ID, sqlite.KindAttempt, att.ID)
		tx.Event(p.ID, "session.attached", "session", s.ID, s.Version+1)
		res.SessionID, res.AttemptID, res.LeaseID, res.Fence = s.ID, att.ID, l.ID, domain.Dec(l.Fence)
		res.Context = workerContext(att)
		return tx.Audit(ctx, p.ID, cmd.Principal.ID, "session.attach", "session", s.ID, "", "", map[string]string{"attempt_id": att.ID})
	})
	if err != nil {
		return nil, err
	}
	a.Wake()
	return res, a.SyncRegistry(ctx)
}

func findSessionForAttach(ctx context.Context, q sqlite.Q, req AttachRequest) (*sqlite.Session, error) {
	if req.ClaudeSessionID != "" {
		s, err := sqlite.FindSessionByClaudeID(ctx, q, req.ClaudeSessionID)
		if err != nil {
			return nil, err
		}
		if s == nil {
			return nil, domain.Errf(domain.CodeNotFound, "no registered session %s (SessionStart hook must run first)", req.ClaudeSessionID)
		}
		return s, nil
	}
	return findSessionByAncestors(ctx, q, req.AncestorPIDs)
}

// findSessionByAncestors finds the live Claude session whose process is an
// ancestor of the calling CLI process, verifying start time (no PID reuse).
func findSessionByAncestors(ctx context.Context, q sqlite.Q, pids []int) (*sqlite.Session, error) {
	ss, err := sqlite.SessionsWhere(ctx, q, "ended_at IS NULL")
	if err != nil {
		return nil, err
	}
	for _, pid := range pids {
		for _, s := range ss {
			var proc map[string]string
			_ = json.Unmarshal([]byte(s.ProcessIdentity), &proc)
			if proc["pid"] != strconv.Itoa(pid) {
				continue
			}
			if id, ok := procinfo.FromMap(proc); ok && procinfo.Check(id) == procinfo.Running {
				return s, nil
			}
		}
	}
	return nil, domain.Errf(domain.CodeNotFound, "could not find the calling Claude session; pass --claude-session-id")
}

// ToolCheckRequest is the PreToolUse policy request (contract §10.3).
type ToolCheckRequest struct {
	HookIdentity
	AttemptID     string   `json:"attempt_id,omitempty"`
	LeaseID       string   `json:"lease_id,omitempty"`
	Fence         string   `json:"fence,omitempty"`
	RuntimeEpoch  string   `json:"runtime_epoch,omitempty"`
	ToolUseID     string   `json:"tool_use_id,omitempty"`
	ToolName      string   `json:"tool_name"`
	AffectedPaths []string `json:"affected_paths,omitempty"`
	PayloadDigest string   `json:"payload_digest,omitempty"`
	Background    bool     `json:"background,omitempty"`
}

// ToolDecision is the policy result (contract §10.4). PASS never maps to a
// permission override in the bridge.
type ToolDecision struct {
	Decision       string `json:"decision"`
	ReasonCode     string `json:"reason_code"`
	Message        string `json:"message"`
	ProjectVersion string `json:"project_version,omitempty"`
	CurrentFence   string `json:"current_fence,omitempty"`
	RetryAfterMs   int    `json:"retry_after_ms,omitempty"`
}

var denyMessages = map[string]string{
	domain.ReasonSessionNotRegistered:  "삼사라 관리 세션 등록이 확인되지 않았습니다.",
	domain.ReasonNoAssignment:          "이 세션에 배정된 작업이 없습니다.",
	domain.ReasonAttemptNotRunning:     "작업 시도가 실행 상태가 아닙니다.",
	domain.ReasonLeaseRevoked:          "작업 실행권이 회수되었습니다.",
	domain.ReasonLeaseExpired:          "작업 실행권이 만료되었습니다.",
	domain.ReasonLeaseMissing:          "작업 실행권이 없습니다.",
	domain.ReasonEpochChanged:          "런타임 재시작 후 실행권 재확인이 필요합니다.",
	domain.ReasonClockAnomaly:          "시계 이상으로 실행권을 확인할 수 없습니다.",
	domain.ReasonStaleFence:            "오래된 실행권입니다.",
	domain.ReasonDisableDraining:       "프로젝트 비활성화(drain) 중입니다.",
	domain.ReasonProjectNotEnabled:     "프로젝트가 활성화되어 있지 않습니다.",
	domain.ReasonRunNotActive:          "실행 계획이 활성 상태가 아닙니다.",
	domain.ReasonGenerationChanged:     "실행 계획 세대가 바뀌었습니다.",
	domain.ReasonDependencyUnmet:       "선행 조건이 충족되지 않았습니다.",
	domain.ReasonCwdOutOfScope:         "작업 경로가 배정된 worktree 밖입니다.",
	domain.ReasonPathOutOfScope:        "대상 파일이 배정된 worktree 밖입니다.",
	domain.ReasonBackgroundUnsupported: "관리 worker의 background 실행은 지원하지 않습니다.",
	domain.ReasonQuarantined:           "세션 생존·소유권 확인 전까지 실행권을 보류합니다.",
}

func deny(code string) *ToolDecision {
	return &ToolDecision{Decision: domain.DecisionDeny, ReasonCode: code, Message: denyMessages[code], RetryAfterMs: 1000}
}

// CheckTool evaluates one tool call against the authoritative state in a
// single transaction. It never replays a previous PASS (POL-17): every
// request is re-evaluated. A PASS only means "Samsara does not object".
func (a *App) CheckTool(ctx context.Context, req ToolCheckRequest) (*ToolDecision, error) {
	if !a.Ready() {
		return deny(domain.ReasonEpochChanged), nil
	}
	req.Cwd = canonicalOrClean(req.Cwd)
	// Liveness verification for possible re-authorisation happens outside the
	// transaction (external observation, R-06).
	var verifiedBinding string
	var preBinding *sqlite.Binding
	_ = a.DB.Read(ctx, func(q sqlite.Q) error {
		s, _ := sqlite.FindSessionByClaudeID(ctx, q, req.ClaudeSessionID)
		if s == nil {
			return nil
		}
		as, _ := sqlite.ActiveAssignmentForSession(ctx, q, s.ID)
		if as == nil || as.BindingID == "" {
			return nil
		}
		l, _ := sqlite.LatestLeaseForAttempt(ctx, q, as.AttemptID)
		if l != nil && l.State == domain.LeaseExpired {
			preBinding, _ = sqlite.GetBinding(ctx, q, as.BindingID)
		}
		return nil
	})
	if preBinding != nil {
		if obs, err := a.observeBinding(ctx, preBinding); err == nil && obs.Liveness == domain.LivenessAlive && (obs.Generation == "" || obs.Generation == preBinding.Generation) {
			verifiedBinding = preBinding.ID
		}
	}
	var dec *ToolDecision
	err := a.DB.Write(ctx, func(tx *sqlite.Tx) error {
		var err error
		dec, err = a.checkToolTx(ctx, tx, req, verifiedBinding)
		return err
	})
	if err != nil {
		return deny(domain.ReasonRuntimeUnavailable), err
	}
	return dec, nil
}

func (a *App) checkToolTx(ctx context.Context, tx *sqlite.Tx, req ToolCheckRequest, verifiedBinding string) (*ToolDecision, error) {
	audit := func(projectID, subjectID string, d *ToolDecision) (*ToolDecision, error) {
		detail := map[string]any{"tool": req.ToolName, "tool_use_id": req.ToolUseID, "payload_digest": req.PayloadDigest}
		if err := tx.Audit(ctx, projectID, "hook", "tool.check", "session", subjectID, d.Decision, d.ReasonCode, detail); err != nil {
			return nil, err
		}
		return d, nil
	}
	s, err := sqlite.FindSessionByClaudeID(ctx, tx, req.ClaudeSessionID)
	if err != nil {
		return nil, err
	}
	if s == nil || !s.Managed {
		if req.ManagedHint || req.Ticket != "" {
			return audit("", req.ClaudeSessionID, deny(domain.ReasonSessionNotRegistered))
		}
		return &ToolDecision{Decision: domain.DecisionPass, ReasonCode: domain.ReasonUnmanaged}, nil
	}
	if _, err := tx.ExecContext(ctx, "UPDATE sessions SET last_hook_at=?, input_state='BUSY', liveness='ALIVE' WHERE id=?", tx.Now, s.ID); err != nil {
		return nil, err
	}
	as, err := sqlite.ActiveAssignmentForSession(ctx, tx, s.ID)
	if err != nil {
		return nil, err
	}
	if as == nil {
		return audit(s.ProjectID, s.ID, deny(domain.ReasonNoAssignment))
	}
	att, err := sqlite.GetAttempt(ctx, tx, as.AttemptID)
	if err != nil {
		return nil, err
	}
	p, err := sqlite.GetProject(ctx, tx, att.ProjectID)
	if err != nil {
		return nil, err
	}
	if p.ID != s.ProjectID {
		return audit(s.ProjectID, s.ID, deny(domain.ReasonSessionNotRegistered))
	}
	if att.State != domain.AttemptRunning {
		return audit(p.ID, s.ID, deny(domain.ReasonAttemptNotRunning))
	}
	key := resourceKey(att.WorktreeID)
	l, err := sqlite.LatestLeaseForAttempt(ctx, tx, att.ID)
	if err != nil {
		return nil, err
	}
	if l == nil {
		return audit(p.ID, s.ID, deny(domain.ReasonLeaseMissing))
	}
	cfg := projectConfig(p)
	expiredLike := l.State == domain.LeaseExpired || (l.State == domain.LeaseActive && (l.RuntimeEpoch != a.Epoch || tx.Now > l.DeadlineAt))
	if expiredLike {
		// Re-authorise only when the same binding was verified alive with the
		// same generation; otherwise quarantine (INV-04).
		if verifiedBinding == "" || verifiedBinding != l.BindingID || a.policyBlock(ctx, tx, p, att) != "" {
			if l.State == domain.LeaseActive {
				if err := a.endLease(ctx, tx, l, domain.LeaseExpired, "TTL"); err != nil {
					return nil, err
				}
			}
			code := domain.ReasonLeaseExpired
			if l.RuntimeEpoch != a.Epoch {
				code = domain.ReasonEpochChanged
			}
			return audit(p.ID, s.ID, deny(code))
		}
		if l.State == domain.LeaseActive {
			if err := a.endLease(ctx, tx, l, domain.LeaseExpired, "TTL"); err != nil {
				return nil, err
			}
		}
		nl, err := a.issueLease(ctx, tx, att, l.SessionID, l.BindingID, a.leaseTTL(cfg).Milliseconds())
		if err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE execution_attempts SET quarantine_reason='', version=version+1, updated_at=? WHERE id=?", tx.Now, att.ID); err != nil {
			return nil, err
		}
		tx.Touch(p.ID, sqlite.KindIssue, att.IssueID)
		l = nl
	}
	if l.State != domain.LeaseActive {
		if l.State == domain.LeaseRevoked {
			return audit(p.ID, s.ID, deny(domain.ReasonLeaseRevoked))
		}
		return audit(p.ID, s.ID, deny(domain.ReasonLeaseMissing))
	}
	if tx.Now < l.IssuedAt {
		return audit(p.ID, s.ID, deny(domain.ReasonClockAnomaly))
	}
	cur, err := sqlite.CurrentFence(ctx, tx, key)
	if err != nil {
		return nil, err
	}
	if cur != l.Fence {
		return audit(p.ID, s.ID, deny(domain.ReasonStaleFence))
	}
	if req.Fence != "" && req.Fence != domain.Dec(cur) {
		return audit(p.ID, s.ID, deny(domain.ReasonStaleFence))
	}
	if req.AttemptID != "" && req.AttemptID != att.ID {
		return audit(p.ID, s.ID, deny(domain.ReasonStaleFence))
	}
	if reason := a.policyBlock(ctx, tx, p, att); reason != "" {
		return audit(p.ID, s.ID, deny(reason))
	}
	w, err := sqlite.GetWorktree(ctx, tx, att.WorktreeID)
	if err != nil {
		return nil, err
	}
	if !project.Within(req.Cwd, w.Path) {
		return audit(p.ID, s.ID, deny(domain.ReasonCwdOutOfScope))
	}
	for _, ap := range req.AffectedPaths {
		abs := ap
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(req.Cwd, abs)
		}
		abs = resolveExistingPrefix(filepath.Clean(abs))
		if !project.Within(abs, w.Path) {
			return audit(p.ID, s.ID, deny(domain.ReasonPathOutOfScope))
		}
	}
	if req.Background {
		return audit(p.ID, s.ID, deny(domain.ReasonBackgroundUnsupported))
	}
	if att.ResumeRequired {
		if _, err := tx.ExecContext(ctx, "UPDATE execution_attempts SET resume_required=0, version=version+1 WHERE id=?", att.ID); err != nil {
			return nil, err
		}
		tx.Touch(p.ID, sqlite.KindAttempt, att.ID)
		tx.Touch(p.ID, sqlite.KindIssue, att.IssueID)
	}
	return audit(p.ID, s.ID, &ToolDecision{Decision: domain.DecisionPass, ReasonCode: "OK", ProjectVersion: domain.Dec(p.ProjectVersion), CurrentFence: domain.Dec(cur)})
}

// resolveExistingPrefix resolves symlinks of the longest existing prefix so
// a symlink inside the worktree cannot point a write outside it.
func resolveExistingPrefix(p string) string {
	rest := ""
	cur := p
	for i := 0; i < 64; i++ {
		if r, err := filepath.EvalSymlinks(cur); err == nil {
			if rest == "" {
				return r
			}
			return filepath.Join(r, rest)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return p
		}
		if rest == "" {
			rest = filepath.Base(cur)
		} else {
			rest = filepath.Join(filepath.Base(cur), rest)
		}
		cur = parent
	}
	return p
}

// ObserveRequest carries non-gating hook observations.
type ObserveRequest struct {
	HookIdentity
	ToolName         string `json:"tool_name,omitempty"`
	ToolUseID        string `json:"tool_use_id,omitempty"`
	Failed           bool   `json:"failed,omitempty"`
	NotificationType string `json:"notification_type,omitempty"`
	DispatchID       string `json:"dispatch_id,omitempty"`
	AgentID          string `json:"agent_id,omitempty"`
}

// ObserveSession records hook observations (UserPromptSubmit, PostToolUse,
// Notification, Stop, Subagent*). Observations never grant authority and
// never complete an issue (R-05, POL-14).
func (a *App) ObserveSession(ctx context.Context, req ObserveRequest) error {
	if !a.Ready() {
		return domain.Errf(domain.CodeRuntimeNotReady, "recovering")
	}
	err := a.DB.Write(ctx, func(tx *sqlite.Tx) error {
		s, err := sqlite.FindSessionByClaudeID(ctx, tx, req.ClaudeSessionID)
		if err != nil || s == nil {
			return err
		}
		input, owner := s.InputState, s.InputOwner
		switch req.Event {
		case "UserPromptSubmit":
			input = domain.InputBusy
			if req.DispatchID != "" {
				if err := a.ackDispatchTx(ctx, tx, req.DispatchID, s.ID); err != nil {
					return err
				}
			} else if s.Managed && owner == "automation" {
				owner = "user" // human took over input (BE-08)
			}
		case "PostToolUse", "PostToolUseFailure", "PreToolUse":
			input = domain.InputBusy
		case "Notification":
			switch req.NotificationType {
			case "permission_prompt":
				input = domain.InputNativePermission
			case "idle_prompt":
				input = domain.InputReady
			}
		case "Stop":
			input = domain.InputReady
		case "SubagentStart", "SubagentStop":
		}
		if _, err := tx.ExecContext(ctx, "UPDATE sessions SET input_state=?, input_owner=?, last_hook_at=?, observed_at=?, liveness='ALIVE', backend_raw_state=?, version=version+1 WHERE id=?",
			input, owner, tx.Now, tx.Now, req.Event, s.ID); err != nil {
			return err
		}
		tx.Touch(s.ProjectID, sqlite.KindSession, s.ID)
		if req.Event == "PostToolUse" || req.Event == "PostToolUseFailure" || strings.HasPrefix(req.Event, "Subagent") {
			if err := tx.Audit(ctx, s.ProjectID, "hook", strings.ToLower(req.Event), "session", s.ID, "", "",
				map[string]any{"tool": req.ToolName, "tool_use_id": req.ToolUseID, "failed": req.Failed, "agent_id": req.AgentID}); err != nil {
				return err
			}
		}
		as, err := sqlite.ActiveAssignmentForSession(ctx, tx, s.ID)
		if err != nil || as == nil {
			return err
		}
		att, err := sqlite.GetAttempt(ctx, tx, as.AttemptID)
		if err != nil {
			return err
		}
		if req.Event == "UserPromptSubmit" && att.ResumeRequired {
			if _, err := tx.ExecContext(ctx, "UPDATE execution_attempts SET resume_required=0, version=version+1 WHERE id=?", att.ID); err != nil {
				return err
			}
			tx.Touch(att.ProjectID, sqlite.KindIssue, att.IssueID)
		}
		// Stop means the response ended: the in-flight tool finished, which is
		// the stop evidence PAUSING needs. It is never completion evidence.
		if req.Event == "Stop" {
			switch att.State {
			case domain.AttemptPausing:
				return a.setAttemptState(ctx, tx, att, domain.AttemptPaused, att.StateReason)
			case domain.AttemptCancelRequested:
				if err := a.setAttemptState(ctx, tx, att, domain.AttemptCancelled, att.StateReason); err != nil {
					return err
				}
				return a.releaseAttemptBindings(ctx, tx, att)
			}
		}
		return nil
	})
	if err == nil {
		a.Wake()
	}
	return err
}

func (a *App) setAttemptState(ctx context.Context, tx *sqlite.Tx, att *sqlite.Attempt, state, reason string) error {
	if _, err := tx.ExecContext(ctx, "UPDATE execution_attempts SET state=?, state_reason=?, version=version+1, updated_at=? WHERE id=?", state, reason, tx.Now, att.ID); err != nil {
		return err
	}
	tx.Touch(att.ProjectID, sqlite.KindAttempt, att.ID)
	tx.Touch(att.ProjectID, sqlite.KindIssue, att.IssueID)
	tx.Event(att.ProjectID, "attempt."+strings.ToLower(state), "attempt", att.ID, att.Version+1)
	return nil
}

// releaseAttemptBindings releases assignments of a finished attempt and the
// worktree (retained, never deleted).
func (a *App) releaseAttemptBindings(ctx context.Context, tx *sqlite.Tx, att *sqlite.Attempt) error {
	if err := a.revokeAttemptLease(ctx, tx, att, domain.LeaseReleased, "ATTEMPT_FINISHED"); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE session_assignments SET released_at=? WHERE attempt_id=? AND released_at IS NULL", tx.Now, att.ID); err != nil {
		return err
	}
	if att.WorktreeID != "" {
		if _, err := tx.ExecContext(ctx, "UPDATE worktrees SET state='RETAINED', version=version+1, updated_at=? WHERE id=? AND state IN ('IN_USE','READY','QUIESCING')", tx.Now, att.WorktreeID); err != nil {
			return err
		}
		tx.Touch(att.ProjectID, sqlite.KindWorktree, att.WorktreeID)
	}
	return nil
}

// FinishRequest reports Stop/SessionEnd or launcher exit.
type FinishRequest struct {
	HookIdentity
	Reason       string `json:"reason,omitempty"`
	ProcessExit  bool   `json:"process_exit"` // launcher saw the child exit
	ExitCode     int    `json:"exit_code"`
	LauncherAuth string `json:"launcher_ticket,omitempty"`
}

// FinishSession handles SessionEnd and launcher exit. SessionEnd alone is
// not proof that the process stopped; liveness becomes UNKNOWN until the
// reconcile loop confirms it. A launcher-observed child exit is proof.
func (a *App) FinishSession(ctx context.Context, req FinishRequest) error {
	if !a.Ready() {
		return domain.Errf(domain.CodeRuntimeNotReady, "recovering")
	}
	var targetSession string
	if req.ProcessExit && req.LauncherAuth != "" {
		att, _, err := a.attemptForTicket(ctx, req.LauncherAuth)
		if err != nil {
			return err
		}
		_ = a.DB.Read(ctx, func(q sqlite.Q) error {
			// The assignment may already be released (output submitted).
			return q.QueryRowContext(ctx, "SELECT session_id FROM session_assignments WHERE attempt_id=? ORDER BY attached_at DESC LIMIT 1", att.ID).Scan(&targetSession)
		})
		if targetSession == "" {
			// Never registered: the launch failed before SessionStart.
			return a.DB.Write(ctx, func(tx *sqlite.Tx) error {
				att, err := sqlite.GetAttempt(ctx, tx, att.ID)
				if err != nil || att.State != domain.AttemptStarting {
					return err
				}
				return a.setAttemptState(ctx, tx, att, domain.AttemptFailed, "WORKER_EXITED_BEFORE_REGISTRATION")
			})
		}
	}
	err := a.DB.Write(ctx, func(tx *sqlite.Tx) error {
		var s *sqlite.Session
		var err error
		if targetSession != "" {
			s, err = sqlite.GetSession(ctx, tx, targetSession)
		} else {
			s, err = sqlite.FindSessionByClaudeID(ctx, tx, req.ClaudeSessionID)
		}
		if err != nil || s == nil {
			return err
		}
		if !req.ProcessExit {
			if _, err := tx.ExecContext(ctx, "UPDATE sessions SET liveness='UNKNOWN', backend_raw_state='SessionEnd', last_hook_at=?, version=version+1 WHERE id=?", tx.Now, s.ID); err != nil {
				return err
			}
			tx.Touch(s.ProjectID, sqlite.KindSession, s.ID)
			return nil
		}
		return a.sessionExited(ctx, tx, s, fmt.Sprintf("PROCESS_EXIT_%d", req.ExitCode))
	})
	if err == nil {
		a.Wake()
		_ = a.SyncRegistry(ctx)
	}
	return err
}

// sessionExited applies confirmed process exit to the session and attempt.
func (a *App) sessionExited(ctx context.Context, tx *sqlite.Tx, s *sqlite.Session, reason string) error {
	if _, err := tx.ExecContext(ctx, "UPDATE sessions SET liveness='EXITED', managed=0, input_state='UNKNOWN', ended_at=COALESCE(ended_at,?), backend_raw_state=?, observed_at=?, version=version+1 WHERE id=?",
		tx.Now, reason, tx.Now, s.ID); err != nil {
		return err
	}
	tx.Touch(s.ProjectID, sqlite.KindSession, s.ID)
	tx.Event(s.ProjectID, "session.exited", "session", s.ID, s.Version+1)
	as, err := sqlite.ActiveAssignmentForSession(ctx, tx, s.ID)
	if err != nil || as == nil {
		return err
	}
	att, err := sqlite.GetAttempt(ctx, tx, as.AttemptID)
	if err != nil {
		return err
	}
	switch att.State {
	case domain.AttemptRunning, domain.AttemptStarting:
		if err := a.setAttemptState(ctx, tx, att, domain.AttemptLost, reason); err != nil {
			return err
		}
	case domain.AttemptPausing:
		if err := a.setAttemptState(ctx, tx, att, domain.AttemptPaused, att.StateReason); err != nil {
			return err
		}
	case domain.AttemptCancelRequested:
		if err := a.setAttemptState(ctx, tx, att, domain.AttemptCancelled, att.StateReason); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, "UPDATE execution_attempts SET quarantine_reason='', version=version+1 WHERE id=?", att.ID); err != nil {
		return err
	}
	return a.releaseAttemptBindings(ctx, tx, att)
}

// AckDispatch records that the worker received an automated input.
func (a *App) AckDispatch(ctx context.Context, dispatchID, claudeSessionID string) error {
	return a.DB.Write(ctx, func(tx *sqlite.Tx) error {
		s, err := sqlite.FindSessionByClaudeID(ctx, tx, claudeSessionID)
		if err != nil || s == nil {
			return err
		}
		return a.ackDispatchTx(ctx, tx, dispatchID, s.ID)
	})
}

func (a *App) ackDispatchTx(ctx context.Context, tx *sqlite.Tx, dispatchID, sessionID string) error {
	var opID sql.NullString
	var attemptID string
	err := tx.QueryRowContext(ctx, "SELECT operation_id, attempt_id FROM dispatches WHERE id=?", dispatchID).Scan(&opID, &attemptID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE dispatches SET state='ACKED', acked_at=?, session_id=? WHERE id=?", tx.Now, sessionID, dispatchID); err != nil {
		return err
	}
	if opID.Valid {
		if _, err := tx.ExecContext(ctx, "UPDATE operations SET state='SUCCEEDED', updated_at=? WHERE id=?", tx.Now, opID.String); err != nil {
			return err
		}
	}
	return nil
}

// workerPromptPath is where the launch prompt file for an attempt lives.
func (a *App) workerPromptPath(attemptID string) string {
	return filepath.Join(a.Layout.PromptDir(), attemptID+".txt")
}

func writePrivateFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// FocusSession returns how to open the session's location. It never injects
// keys; unsupported backends say so (BE-14).
func (a *App) FocusSession(ctx context.Context, sessionID string) (map[string]string, error) {
	var b *sqlite.Binding
	if err := a.DB.Read(ctx, func(q sqlite.Q) error {
		if _, err := sqlite.GetSession(ctx, q, sessionID); err != nil {
			return err
		}
		var err error
		b, err = sqlite.CurrentBindingForSession(ctx, q, sessionID)
		return err
	}); err != nil {
		return nil, err
	}
	if b == nil {
		return nil, domain.Errf(domain.CodeUnsupportedCapability, "session has no backend location")
	}
	be, _ := a.Backends.Get(b.Kind)
	f, ok := be.(backend.Focuser)
	if !ok {
		return nil, domain.Errf(domain.CodeUnsupportedCapability, "%s cannot focus sessions", b.Kind)
	}
	ins, err := f.FocusInstruction(bindingRef(b))
	if err != nil {
		return nil, err
	}
	return map[string]string{"backend": b.Kind, "instruction": ins}, nil
}

// ReleaseInput hands input ownership back to automation after a human
// takeover. Automated input still re-checks every other condition.
func (a *App) ReleaseInput(ctx context.Context, cmd Cmd, sessionID string) (*CommandResult, error) {
	var out CommandResult
	err := a.DB.Write(ctx, func(tx *sqlite.Tx) error {
		return a.idempotent(ctx, tx, "session.release_input:"+sessionID, cmd, sessionID, &out, func() (any, error) {
			s, err := sqlite.GetSession(ctx, tx, sessionID)
			if err != nil {
				return nil, err
			}
			if err := checkIfMatch(cmd, s.Version); err != nil {
				return nil, err
			}
			if !s.Managed {
				return nil, domain.Errf(domain.CodeConflict, "session is not managed")
			}
			if _, err := tx.ExecContext(ctx, "UPDATE sessions SET input_owner='automation', version=version+1 WHERE id=?", s.ID); err != nil {
				return nil, err
			}
			tx.Touch(s.ProjectID, sqlite.KindSession, s.ID)
			tx.Event(s.ProjectID, "session.input_released", "session", s.ID, s.Version+1)
			if err := tx.Audit(ctx, s.ProjectID, cmd.Principal.ID, "session.release_input", "session", s.ID, "", "", nil); err != nil {
				return nil, err
			}
			return &CommandResult{CommandID: domain.NewID("cmd"), Status: "ACCEPTED", ResourceVersion: domain.Dec(s.Version + 1)}, nil
		})
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}
