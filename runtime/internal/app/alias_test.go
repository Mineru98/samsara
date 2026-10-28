package app_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Mineru98/samsara/runtime/internal/app"
	"github.com/Mineru98/samsara/runtime/internal/domain"
	"github.com/Mineru98/samsara/runtime/internal/paths"
	"github.com/Mineru98/samsara/runtime/internal/project"
	"github.com/Mineru98/samsara/runtime/internal/store/sqlite"
)

// aliasedRoot returns a canonical directory and a symlink alias to it, the
// shape of macOS TMPDIR (/var/folders/... -> /private/var/folders/...).
func aliasedRoot(t *testing.T) (canon, alias string) {
	t.Helper()
	canon, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	alias = filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(canon, alias); err != nil {
		t.Fatal(err)
	}
	return canon, alias
}

func (e *env) worktreePath(issueID string) string {
	e.t.Helper()
	wt, err := e.app.Lookup(e.ctx, sqlite.KindWorktree, e.attempt(issueID).WorktreeID)
	if err != nil {
		e.t.Fatal(err)
	}
	return wt.(sqlite.WorktreeView).Path
}

// #74: SAMSARA_HOME/worktree root reached through a symlink alias must still
// let the worker register, while out-of-scope cwds and escapes stay denied.
func TestAliasedWorktreeRootManagedWorker(t *testing.T) {
	canon, alias := aliasedRoot(t)
	e := newEnvIn(t, baseConfig, alias)
	issue, wt := startSingle(t, e)

	stored := e.worktreePath(issue)
	if !strings.HasPrefix(stored, canon+string(filepath.Separator)) {
		t.Fatalf("worktree must be stored in canonical form under %s, got %s", canon, stored)
	}
	if d := e.check("claude-X", wt, "a.txt"); d.Decision != domain.DecisionPass {
		t.Fatalf("worktree write: %+v", d)
	}
	rel, err := filepath.Rel(canon, stored)
	if err != nil {
		t.Fatal(err)
	}
	if d := e.check("claude-X", filepath.Join(alias, rel), "a.txt"); d.Decision != domain.DecisionPass {
		t.Fatalf("aliased cwd of the same worktree: %+v", d)
	}
	if d := e.check("claude-X", e.repo); d.ReasonCode != domain.ReasonCwdOutOfScope {
		t.Fatalf("user checkout is outside the attempt worktree: %+v", d)
	}
	if d := e.check("claude-X", wt, filepath.Join(alias, "repo", "x.txt")); d.ReasonCode != domain.ReasonPathOutOfScope {
		t.Fatalf("aliased path outside the worktree: %+v", d)
	}
	if err := os.Symlink(e.repo, filepath.Join(stored, "escape")); err != nil {
		t.Fatal(err)
	}
	if d := e.check("claude-X", wt, "escape/x.txt"); d.ReasonCode != domain.ReasonPathOutOfScope {
		t.Fatalf("symlink escape from the worktree: %+v", d)
	}

	// The hook fast path classifies canonical worktree cwds as managed.
	if err := e.app.SyncRegistry(e.ctx); err != nil {
		t.Fatal(err)
	}
	reg, err := project.LoadRegistry(e.layout.RegistryPath())
	if err != nil {
		t.Fatal(err)
	}
	if !reg.Classify(stored, "").ManagedRoot {
		t.Fatalf("managed roots %v must contain %s", reg.ManagedRoots, stored)
	}
	if reg.Classify(e.repo, "").ManagedRoot {
		t.Fatalf("user checkout must not be a managed root: %v", reg.ManagedRoots)
	}
}

// #74: a Standalone session started through the alias is found and attached.
func TestAliasedWorktreeRootStandaloneAttach(t *testing.T) {
	canon, alias := aliasedRoot(t)
	cfg := strings.Replace(baseConfig, "mode: auto", "mode: standalone", 1)
	e := newEnvIn(t, cfg, alias)
	res := e.applyPlan(singlePlan)
	if _, err := e.app.StartRun(e.ctx, e.cmd, res.RunID); err != nil {
		t.Fatal(err)
	}
	e.reconcile(3)
	issue := res.IssueMapping["x"]
	stored := e.worktreePath(issue)
	rel, err := filepath.Rel(canon, stored)
	if err != nil || strings.HasPrefix(rel, "..") {
		t.Fatalf("worktree must be stored under %s, got %s", canon, stored)
	}
	aliasCwd := filepath.Join(alias, rel)
	if _, err := e.app.RegisterSession(e.ctx, app.HookIdentity{ClaudeSessionID: "user-1", Cwd: aliasCwd, Process: selfProcess()}); err != nil {
		t.Fatal(err)
	}
	ar, err := e.app.AttachSession(e.ctx, e.cmd, app.AttachRequest{ClaudeSessionID: "user-1", IssueID: issue})
	if err != nil || ar.LeaseID == "" {
		t.Fatalf("attach: %+v %v", ar, err)
	}
	if d := e.check("user-1", aliasCwd, "a.txt"); d.Decision != domain.DecisionPass {
		t.Fatalf("attached session allowed: %+v", d)
	}
	if d := e.check("user-1", e.repo); d.ReasonCode != domain.ReasonCwdOutOfScope {
		t.Fatalf("user checkout is outside the attempt worktree: %+v", d)
	}
}

// storeLegacyAlias rewrites the worktree row to the aliased form a runtime
// before #74 recorded, and returns that path.
func (e *env) storeLegacyAlias(issueID, canon, alias string) string {
	e.t.Helper()
	rel, err := filepath.Rel(canon, e.worktreePath(issueID))
	if err != nil {
		e.t.Fatal(err)
	}
	legacy := filepath.Join(alias, rel)
	wtID := e.attempt(issueID).WorktreeID
	if err := e.db.Write(e.ctx, func(tx *sqlite.Tx) error {
		_, err := tx.ExecContext(e.ctx, "UPDATE worktrees SET path=? WHERE id=?", legacy, wtID)
		tx.Touch(e.project, sqlite.KindWorktree, wtID)
		return err
	}); err != nil {
		e.t.Fatal(err)
	}
	if got := e.worktreePath(issueID); got != legacy {
		e.t.Fatalf("legacy row not stored: %s", got)
	}
	return legacy
}

// #74: a DB written before the fix holds aliased worktree paths. After a
// restart the worker is re-authorised and the registry carries the
// canonical root, while out-of-scope cwds and escapes stay denied.
func TestLegacyAliasedWorktreeRowManagedAfterRestart(t *testing.T) {
	canon, alias := aliasedRoot(t)
	e := newEnvIn(t, baseConfig, alias)
	issue, wt := startSingle(t, e)
	stored := e.worktreePath(issue)
	e.storeLegacyAlias(issue, canon, alias)

	e.app = e.newApp(e.app.SelfExe)
	if got := e.worktreePath(issue); got != stored {
		t.Fatalf("recovery must restore the canonical path %s, got %s", stored, got)
	}
	e.reconcile(2)
	if d := e.check("claude-X", wt, "a.txt"); d.Decision != domain.DecisionPass {
		t.Fatalf("worker re-authorised after restart: %+v", d)
	}
	if d := e.check("claude-X", e.repo); d.ReasonCode != domain.ReasonCwdOutOfScope {
		t.Fatalf("user checkout is outside the attempt worktree: %+v", d)
	}
	if err := os.Symlink(e.repo, filepath.Join(stored, "escape")); err != nil {
		t.Fatal(err)
	}
	if d := e.check("claude-X", wt, "escape/x.txt"); d.ReasonCode != domain.ReasonPathOutOfScope {
		t.Fatalf("symlink escape from the worktree: %+v", d)
	}
	reg, err := project.LoadRegistry(e.layout.RegistryPath())
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(canon, "wt"); len(reg.ManagedRoots) != 1 || reg.ManagedRoots[0] != want {
		t.Fatalf("managed roots %v, want [%s]", reg.ManagedRoots, want)
	}
}

// #74: a Standalone worktree recorded in aliased form is found and attached
// after a restart.
func TestLegacyAliasedWorktreeRowStandaloneAttachAfterRestart(t *testing.T) {
	canon, alias := aliasedRoot(t)
	cfg := strings.Replace(baseConfig, "mode: auto", "mode: standalone", 1)
	e := newEnvIn(t, cfg, alias)
	res := e.applyPlan(singlePlan)
	if _, err := e.app.StartRun(e.ctx, e.cmd, res.RunID); err != nil {
		t.Fatal(err)
	}
	e.reconcile(3)
	issue := res.IssueMapping["x"]
	stored := e.worktreePath(issue)
	legacy := e.storeLegacyAlias(issue, canon, alias)

	e.app = e.newApp(e.app.SelfExe)
	if got := e.worktreePath(issue); got != stored {
		t.Fatalf("recovery must restore the canonical path %s, got %s", stored, got)
	}
	if _, err := e.app.RegisterSession(e.ctx, app.HookIdentity{ClaudeSessionID: "user-1", Cwd: legacy, Process: selfProcess()}); err != nil {
		t.Fatal(err)
	}
	ar, err := e.app.AttachSession(e.ctx, e.cmd, app.AttachRequest{ClaudeSessionID: "user-1", IssueID: issue})
	if err != nil || ar.LeaseID == "" {
		t.Fatalf("attach: %+v %v", ar, err)
	}
	if d := e.check("user-1", e.repo); d.ReasonCode != domain.ReasonCwdOutOfScope {
		t.Fatalf("user checkout is outside the attempt worktree: %+v", d)
	}
}

// #74: only the parents of the worktree root are resolved; a root that is
// itself a symlink is neither followed nor accepted by Ensure.
func TestSymlinkedWorktreeRootStaysRefused(t *testing.T) {
	canon, alias := aliasedRoot(t)
	if err := os.MkdirAll(filepath.Join(canon, "elsewhere"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(canon, "elsewhere"), filepath.Join(canon, "wt")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(canon, "state"), 0o700); err != nil {
		t.Fatal(err)
	}
	layout := paths.Layout{StateDir: filepath.Join(alias, "state"), RuntimeDir: filepath.Join(alias, "run"), WorktreeRoot: filepath.Join(alias, "wt")}
	if err := layout.Ensure(); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlinked worktree root must be refused, got %v", err)
	}
	db, err := sqlite.Open(context.Background(), layout.DBPath(), domain.NewFakeClock(time.Date(2026, 10, 28, 1, 0, 0, 0, time.UTC)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	a := app.New(db, layout, nil, nil)
	if want := filepath.Join(canon, "wt"); a.Layout.WorktreeRoot != want {
		t.Fatalf("root must resolve parents only: got %s, want %s", a.Layout.WorktreeRoot, want)
	}
}
