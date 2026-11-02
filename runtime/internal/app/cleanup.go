package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Mineru98/samsara/runtime/internal/domain"
	"github.com/Mineru98/samsara/runtime/internal/hooks"
	"github.com/Mineru98/samsara/runtime/internal/project"
	"github.com/Mineru98/samsara/runtime/internal/store/sqlite"
)

// CleanupRequest asks for managed worktree cleanup (04 §6.3). Naming
// worktree ids is the user's explicit request and skips the retention
// period; without ids only worktrees past retention are considered.
type CleanupRequest struct {
	ProjectID    string   `json:"project_id"`
	WorktreeIDs  []string `json:"worktree_ids,omitempty"`
	DryRun       bool     `json:"dry_run"`
	DeleteBranch bool     `json:"delete_branch"`
}

// CleanupItem reports the decision and actions for one worktree.
type CleanupItem struct {
	WorktreeID       string   `json:"worktree_id"`
	Path             string   `json:"path"`
	BranchRef        string   `json:"branch_ref,omitempty"`
	State            string   `json:"state"`
	Eligible         bool     `json:"eligible"`
	Reasons          []string `json:"reasons"`
	WorktreeRemoved  bool     `json:"worktree_removed"`
	BranchRemoved    bool     `json:"branch_removed"`
	BranchKeptReason string   `json:"branch_kept_reason,omitempty"`
	Error            string   `json:"error,omitempty"`
}

// CleanupResult lists every evaluated worktree.
type CleanupResult struct {
	DryRun bool          `json:"dry_run"`
	Items  []CleanupItem `json:"items"`
}

type cleanupFacts struct {
	w          *sqlite.Worktree
	repoDir    string
	outputs    []string
	dbReasons  []string
	retainedOK bool
}

// CleanupWorktrees removes managed worktrees that satisfy every eligibility
// rule. Worktree removal and branch removal are separate steps; a refused
// `git worktree remove` stops that worktree with the reason recorded. It
// never uses --force, clean, reset or stash (INV-12).
func (a *App) CleanupWorktrees(ctx context.Context, cmd Cmd, req CleanupRequest) (*CleanupResult, error) {
	if err := a.requireReady(); err != nil {
		return nil, err
	}
	explicit := len(req.WorktreeIDs) > 0
	var facts []*cleanupFacts
	err := a.DB.Read(ctx, func(q sqlite.Q) error {
		p, err := sqlite.GetProject(ctx, q, req.ProjectID)
		if err != nil {
			return err
		}
		retention := time.Duration(projectConfig(p).Git.WorktreeRetentionHours) * time.Hour
		var ws []*sqlite.Worktree
		if explicit {
			for _, id := range req.WorktreeIDs {
				w, err := sqlite.GetWorktree(ctx, q, id)
				if err != nil {
					return err
				}
				if w.ProjectID != p.ID {
					return domain.Errf(domain.CodeNotFound, "worktree %s is not in project %s", id, p.ID)
				}
				ws = append(ws, w)
			}
		} else if ws, err = sqlite.ListWorktrees(ctx, q, p.ID); err != nil {
			return err
		}
		for _, w := range ws {
			if w.State == domain.WorktreeRemoved && !explicit {
				continue
			}
			f := &cleanupFacts{w: w}
			if f.dbReasons, f.outputs, err = a.cleanupDBReasons(ctx, q, w); err != nil {
				return err
			}
			if !explicit && a.Clock.Now().Sub(time.UnixMilli(w.UpdatedAt)) < retention {
				f.dbReasons = append(f.dbReasons, "RETENTION_NOT_ELAPSED")
			}
			if w.RepositoryID != "" {
				repo, err := sqlite.GetRepository(ctx, q, w.RepositoryID)
				if err != nil {
					return err
				}
				f.repoDir = repoDir(repo)
			} else {
				f.dbReasons = append(f.dbReasons, "NO_REPOSITORY")
			}
			facts = append(facts, f)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	res := &CleanupResult{DryRun: req.DryRun, Items: []CleanupItem{}}
	for _, f := range facts {
		item := CleanupItem{WorktreeID: f.w.ID, Path: f.w.Path, BranchRef: f.w.BranchRef, State: f.w.State, Reasons: f.dbReasons}
		ownHook, gone := false, false
		if len(item.Reasons) == 0 {
			item.Reasons, ownHook, gone = a.cleanupGitReasons(ctx, f)
		}
		item.Eligible = len(item.Reasons) == 0
		switch {
		case item.Eligible && gone && !req.DryRun:
			// A previous removal whose confirmation was lost: git no longer
			// lists it and the path is absent, so record the fact.
			a.confirmRemoved(ctx, cmd, f, &item)
		case item.Eligible && !req.DryRun:
			a.removeWorktree(ctx, cmd, f, ownHook, req.DeleteBranch, explicit, &item)
		}
		if item.Reasons == nil {
			item.Reasons = []string{}
		}
		res.Items = append(res.Items, item)
	}
	if !req.DryRun {
		a.Wake()
	}
	return res, nil
}

// cleanupDBReasons evaluates the recorded conditions: ownership, resource
// state, live or unknown sessions, and attached verification/integration.
func (a *App) cleanupDBReasons(ctx context.Context, q sqlite.Q, w *sqlite.Worktree) ([]string, []string, error) {
	var reasons []string
	if w.Ownership != domain.OwnershipManaged {
		reasons = append(reasons, "NOT_MANAGED")
	}
	switch w.State {
	case domain.WorktreeRetained, domain.WorktreeCleanupEligible:
	default:
		reasons = append(reasons, "STATE_"+w.State)
	}
	rows, err := q.QueryContext(ctx, "SELECT state FROM execution_attempts WHERE worktree_id=?", w.ID)
	if err != nil {
		return nil, nil, err
	}
	var states []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			rows.Close()
			return nil, nil, err
		}
		states = append(states, s)
	}
	rows.Close()
	for _, s := range states {
		if domain.AttemptLive(s) {
			reasons = append(reasons, "ATTEMPT_ACTIVE")
			break
		}
		if s == domain.AttemptImplemented || s == domain.AttemptVerifying {
			reasons = append(reasons, "VERIFICATION_ACTIVE")
			break
		}
	}
	// A session is a blocker unless it is confirmed EXITED: ALIVE and
	// UNKNOWN liveness both keep the worktree (04 §6.3, INV-12).
	srows, err := q.QueryContext(ctx, `SELECT s.liveness, s.cwd, EXISTS(SELECT 1 FROM session_assignments sa JOIN execution_attempts e ON e.id=sa.attempt_id
		WHERE sa.session_id=s.id AND e.worktree_id=?) FROM sessions s WHERE s.project_id=? AND s.liveness<>'EXITED'`, w.ID, w.ProjectID)
	if err != nil {
		return nil, nil, err
	}
	alive, unknown := false, false
	for srows.Next() {
		var live, cwd string
		var assigned bool
		if err := srows.Scan(&live, &cwd, &assigned); err != nil {
			srows.Close()
			return nil, nil, err
		}
		if !assigned && !project.Within(cwd, w.Path) {
			continue
		}
		if live == domain.LivenessAlive {
			alive = true
		} else {
			unknown = true
		}
	}
	srows.Close()
	if alive {
		reasons = append(reasons, "SESSION_ALIVE")
	}
	if unknown {
		reasons = append(reasons, "SESSION_LIVENESS_UNKNOWN")
	}
	var open int
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM merge_candidates mc JOIN execution_attempts e ON e.id=mc.attempt_id
		WHERE e.worktree_id=? AND mc.state NOT IN ('MERGED','CANCELLED')`, w.ID).Scan(&open); err != nil {
		return nil, nil, err
	}
	if open > 0 {
		reasons = append(reasons, "MERGE_CANDIDATE_ACTIVE")
	}
	orows, err := q.QueryContext(ctx, "SELECT o.source_sha FROM output_artifacts o JOIN execution_attempts e ON e.id=o.attempt_id WHERE e.worktree_id=?", w.ID)
	if err != nil {
		return nil, nil, err
	}
	var outs []string
	for orows.Next() {
		var s string
		if err := orows.Scan(&s); err != nil {
			orows.Close()
			return nil, nil, err
		}
		outs = append(outs, s)
	}
	orows.Close()
	return reasons, outs, nil
}

// cleanupGitReasons inspects the actual worktree: it must be listed by git,
// clean including untracked files (the runtime's own hook settings file
// aside), and every commit it produced must stay reachable from a ref after
// the worktree is gone.
func (a *App) cleanupGitReasons(ctx context.Context, f *cleanupFacts) (reasons []string, ownHook, gone bool) {
	listed := false
	if list, err := a.Git.ListWorktrees(ctx, f.repoDir); err != nil {
		return []string{"GIT_UNAVAILABLE"}, false, false
	} else {
		for _, li := range list {
			if a.canonicalWorktreePath(li.Path) == f.w.Path {
				listed = true
				if li.Locked {
					reasons = append(reasons, "WORKTREE_LOCKED")
				}
			}
		}
	}
	if !listed {
		if _, err := os.Stat(f.w.Path); errors.Is(err, os.ErrNotExist) && f.w.State == domain.WorktreeCleanupEligible {
			return nil, false, true
		}
		return []string{"WORKTREE_NOT_LISTED"}, false, false
	}
	status, err := a.Git.StatusWithIgnored(ctx, f.w.Path)
	if err != nil {
		return []string{"STATUS_UNREADABLE"}, false, false
	}
	tracked, untracked, ignored := false, false, false
	for _, e := range status {
		if e.Path == hooks.SettingsRel && (e.Kind == "?" || e.Kind == "!") {
			if ok, err := hooks.OnlyOurs(f.w.Path); err == nil && ok {
				ownHook = true
				continue
			}
		}
		switch e.Kind {
		case "?":
			untracked = true
		case "!":
			ignored = true
		default:
			tracked = true
		}
	}
	if tracked {
		reasons = append(reasons, "DIRTY_TRACKED")
	}
	if untracked {
		reasons = append(reasons, "UNTRACKED_FILES")
	}
	// git worktree remove deletes ignored files (.env, local data) too.
	if ignored {
		reasons = append(reasons, "IGNORED_FILES")
	}
	head, err := a.Git.RevParse(ctx, f.w.Path, "HEAD")
	if err != nil {
		return append(reasons, "HEAD_UNREADABLE"), ownHook, false
	}
	for _, oid := range append([]string{head}, f.outputs...) {
		refs, err := a.Git.RefsContaining(ctx, f.repoDir, oid)
		if err != nil || len(refs) == 0 {
			reasons = append(reasons, "UNREFERENCED_COMMITS")
			break
		}
	}
	return reasons, ownHook, false
}

func (a *App) removeWorktree(ctx context.Context, cmd Cmd, f *cleanupFacts, ownHook, deleteBranch, explicit bool, item *CleanupItem) {
	w := f.w
	why := "RETENTION_ELAPSED"
	if explicit {
		why = "USER_REQUESTED"
	}
	setState := a.cleanupSetState(ctx, cmd, w)
	// Claim the transition only from the state the decision was based on.
	err := a.DB.Write(ctx, func(tx *sqlite.Tx) error {
		cur, err := sqlite.GetWorktree(ctx, tx, w.ID)
		if err != nil {
			return err
		}
		if cur.Version != w.Version || (cur.State != domain.WorktreeRetained && cur.State != domain.WorktreeCleanupEligible) {
			return domain.Errf(domain.CodeConflict, "worktree changed during cleanup (%s)", cur.State)
		}
		// Attempts, sessions and candidates do not bump the worktree
		// version: re-evaluate them in the claiming transaction.
		if again, _, err := a.cleanupDBReasons(ctx, tx, cur); err != nil {
			return err
		} else if len(again) > 0 {
			return domain.Errf(domain.CodeConflict, "worktree became ineligible during cleanup: %s", strings.Join(again, ","))
		}
		if _, err := tx.ExecContext(ctx, "UPDATE worktrees SET state='CLEANUP_ELIGIBLE', state_reason=?, version=version+1, updated_at=? WHERE id=?", why, tx.Now, w.ID); err != nil {
			return err
		}
		tx.Touch(w.ProjectID, sqlite.KindWorktree, w.ID)
		tx.Event(w.ProjectID, "worktree.cleanup_eligible", "worktree", w.ID, cur.Version+1)
		return tx.Audit(ctx, w.ProjectID, cmd.Principal.ID, "worktree.cleanup", "worktree", w.ID, domain.WorktreeCleanupEligible, why, nil)
	})
	if err != nil {
		item.Eligible, item.Error = false, err.Error()
		return
	}
	item.State = domain.WorktreeCleanupEligible
	settings := filepath.Join(w.Path, hooks.SettingsRel)
	if ownHook {
		_ = os.Remove(settings)
		_ = os.Remove(filepath.Dir(settings)) // only if now empty
	}
	if err := a.Git.RemoveWorktree(ctx, f.repoDir, w.Path); err != nil {
		// git refused (e.g. a file appeared since the check): keep it.
		if ownHook && a.SelfExe != "" {
			_ = hooks.Install(w.Path, a.SelfExe)
		}
		item.Error = err.Error()
		if e := setState(domain.WorktreeRetained, "REMOVE_REFUSED", map[string]any{"git": err.Error()}); e == nil {
			item.State = domain.WorktreeRetained
		}
		return
	}
	stillListed := false
	if list, err := a.Git.ListWorktrees(ctx, f.repoDir); err == nil {
		for _, li := range list {
			if a.canonicalWorktreePath(li.Path) == w.Path {
				stillListed = true
			}
		}
	} else {
		stillListed = true
	}
	if _, err := os.Stat(w.Path); stillListed || !errors.Is(err, os.ErrNotExist) {
		item.Error = "removal not confirmed by git worktree list and path"
		_ = setState(domain.WorktreeCleanupEligible, "REMOVAL_UNCONFIRMED", nil)
		return
	}
	if err := setState(domain.WorktreeRemoved, why, map[string]any{"branch_ref": w.BranchRef}); err != nil {
		item.Error = err.Error()
		return
	}
	item.State, item.WorktreeRemoved = domain.WorktreeRemoved, true
	if !deleteBranch || w.BranchRef == "" {
		return
	}
	item.BranchKeptReason = a.deleteBranch(ctx, cmd, f)
	item.BranchRemoved = item.BranchKeptReason == ""
}

// cleanupSetState moves a claimed (CLEANUP_ELIGIBLE) worktree on; it never
// overwrites a state another writer has set meanwhile.
func (a *App) cleanupSetState(ctx context.Context, cmd Cmd, w *sqlite.Worktree) func(state, reason string, detail map[string]any) error {
	return func(state, reason string, detail map[string]any) error {
		return a.DB.Write(ctx, func(tx *sqlite.Tx) error {
			cur, err := sqlite.GetWorktree(ctx, tx, w.ID)
			if err != nil {
				return err
			}
			if cur.State != domain.WorktreeCleanupEligible {
				return domain.Errf(domain.CodeConflict, "worktree is %s, not CLEANUP_ELIGIBLE", cur.State)
			}
			if _, err := tx.ExecContext(ctx, "UPDATE worktrees SET state=?, state_reason=?, version=version+1, updated_at=? WHERE id=? AND state='CLEANUP_ELIGIBLE'", state, reason, tx.Now, w.ID); err != nil {
				return err
			}
			tx.Touch(w.ProjectID, sqlite.KindWorktree, w.ID)
			tx.Event(w.ProjectID, "worktree."+strings.ToLower(state), "worktree", w.ID, cur.Version+1)
			return tx.Audit(ctx, w.ProjectID, cmd.Principal.ID, "worktree.cleanup", "worktree", w.ID, state, reason, detail)
		})
	}
}

// confirmRemoved records REMOVED for a claimed worktree that git no longer
// lists and whose path is gone.
func (a *App) confirmRemoved(ctx context.Context, cmd Cmd, f *cleanupFacts, item *CleanupItem) {
	if err := a.cleanupSetState(ctx, cmd, f.w)(domain.WorktreeRemoved, "OBSERVED_ABSENT", nil); err != nil {
		item.Error = err.Error()
		return
	}
	item.State, item.WorktreeRemoved = domain.WorktreeRemoved, true
}

// deleteBranch removes the worktree's branch only when its tip stays
// reachable from another ref, so no unintegrated commit loses its last
// reference (squash/rebase integrations keep the branch). It returns the
// reason the branch was kept, or "" when it was deleted.
func (a *App) deleteBranch(ctx context.Context, cmd Cmd, f *cleanupFacts) string {
	ref := f.w.BranchRef
	tip, err := a.Git.ResolveRef(ctx, f.repoDir, ref)
	if err != nil {
		return "GIT_UNAVAILABLE"
	}
	if tip == "" {
		return "BRANCH_MISSING"
	}
	if out, err := a.Git.RefCheckedOut(ctx, f.repoDir, ref); err != nil || out {
		return "BRANCH_CHECKED_OUT"
	}
	refs, err := a.Git.RefsContaining(ctx, f.repoDir, tip)
	if err != nil {
		return "GIT_UNAVAILABLE"
	}
	kept := false
	for _, r := range refs {
		if r != ref {
			kept = true
		}
	}
	if !kept {
		return "UNMERGED_COMMITS"
	}
	if err := a.Git.DeleteRefCAS(ctx, f.repoDir, ref, tip, "samsara: cleanup "+f.w.ID); err != nil {
		return "DELETE_REFUSED"
	}
	_ = a.DB.Write(ctx, func(tx *sqlite.Tx) error {
		return tx.Audit(ctx, f.w.ProjectID, cmd.Principal.ID, "worktree.branch_delete", "worktree", f.w.ID, "DELETED", "", map[string]string{"branch_ref": ref, "tip": tip})
	})
	return ""
}
