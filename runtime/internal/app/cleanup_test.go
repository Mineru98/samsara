package app_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Mineru98/samsara/runtime/api/apitest"
	"github.com/Mineru98/samsara/runtime/internal/app"
	"github.com/Mineru98/samsara/runtime/internal/domain"
	"github.com/Mineru98/samsara/runtime/internal/store/sqlite"
)

// finishedWorktree runs one issue to a submitted, verified output and
// returns its RETAINED managed worktree. exitWorker confirms process exit.
func finishedWorktree(t *testing.T, e *env, exitWorker bool) (issueID, wt, wtID string) {
	t.Helper()
	issueID, wt = startSingle(t, e)
	e.commitIn(wt, "x.txt", "X\n")
	ticket := e.fake.SessionByAttempt(e.attempt(issueID).ID).Spec.Env["SAMSARA_LAUNCH_TICKET"]
	if _, err := e.app.SubmitOutput(e.ctx, e.cmd, app.OutputSubmitRequest{Ticket: ticket}); err != nil {
		t.Fatal(err)
	}
	e.reconcile(2)
	if exitWorker {
		e.exitWorker(issueID)
	}
	wtID = e.attempt(issueID).WorktreeID
	if w := e.worktree(wtID); w.State != domain.WorktreeRetained {
		t.Fatalf("worktree after submit = %s", w.State)
	}
	return issueID, wt, wtID
}

// exitWorker reports the launcher-observed exit of the issue's worker.
func (e *env) exitWorker(issueID string) {
	e.t.Helper()
	ticket := e.fake.SessionByAttempt(e.attempt(issueID).ID).Spec.Env["SAMSARA_LAUNCH_TICKET"]
	if err := e.app.FinishSession(e.ctx, app.FinishRequest{ProcessExit: true, LauncherAuth: ticket}); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) worktree(id string) *sqlite.Worktree {
	e.t.Helper()
	var w *sqlite.Worktree
	if err := e.db.Read(e.ctx, func(q sqlite.Q) error {
		var err error
		w, err = sqlite.GetWorktree(e.ctx, q, id)
		return err
	}); err != nil {
		e.t.Fatal(err)
	}
	return w
}

func (e *env) cleanup(req app.CleanupRequest) *app.CleanupItem {
	e.t.Helper()
	req.ProjectID = e.project
	res, err := e.app.CleanupWorktrees(e.ctx, e.cmd, req)
	if err != nil {
		e.t.Fatal(err)
	}
	apitest.Check(e.t, "ipc.schema.json#/$defs/CleanupRequest", req)
	apitest.Check(e.t, "ipc.schema.json#/$defs/CleanupResult", res)
	if len(res.Items) != 1 {
		e.t.Fatalf("expected one item, got %+v", res.Items)
	}
	return &res.Items[0]
}

func hasReason(item *app.CleanupItem, r string) bool {
	for _, x := range item.Reasons {
		if x == r {
			return true
		}
	}
	return false
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// F-GIT clean: dry-run changes nothing; the explicit request removes the
// worktree (and the runtime's own hook file) but keeps an unmerged branch.
func TestCleanupCleanWorktreeKeepsUnmergedBranch(t *testing.T) {
	e := newEnv(t, baseConfig)
	_, wt, wtID := finishedWorktree(t, e, true)
	w := e.worktree(wtID)

	dry := e.cleanup(app.CleanupRequest{WorktreeIDs: []string{wtID}, DryRun: true, DeleteBranch: true})
	if !dry.Eligible || dry.WorktreeRemoved || !exists(wt) || e.worktree(wtID).State != domain.WorktreeRetained {
		t.Fatalf("dry-run must only report: %+v", dry)
	}
	item := e.cleanup(app.CleanupRequest{WorktreeIDs: []string{wtID}, DeleteBranch: true})
	if !item.WorktreeRemoved || item.State != domain.WorktreeRemoved || exists(wt) {
		t.Fatalf("clean worktree must be removed: %+v", item)
	}
	if e.worktree(wtID).State != domain.WorktreeRemoved {
		t.Fatal("record must be REMOVED")
	}
	if item.BranchRemoved || item.BranchKeptReason != "UNMERGED_COMMITS" {
		t.Fatalf("unmerged branch must be kept: %+v", item)
	}
	if git(t, e.repo, "rev-parse", "--verify", w.BranchRef) == "" {
		t.Fatal("branch must still exist")
	}
}

// F-GIT dirty, untracked, rename and binary: the worktree is preserved and
// nothing is cleaned, reset or force-removed (INV-12).
func TestCleanupPreservesDirtyUntrackedRenameBinary(t *testing.T) {
	cases := map[string]struct {
		mutate func(t *testing.T, wt string)
		reason string
	}{
		"dirty": {func(t *testing.T, wt string) {
			if err := os.WriteFile(filepath.Join(wt, "README.md"), []byte("edited\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}, "DIRTY_TRACKED"},
		"untracked": {func(t *testing.T, wt string) {
			if err := os.WriteFile(filepath.Join(wt, "notes.txt"), []byte("mine\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}, "UNTRACKED_FILES"},
		"rename": {func(t *testing.T, wt string) { git(t, wt, "mv", "README.md", "DOC.md") }, "DIRTY_TRACKED"},
		"binary": {func(t *testing.T, wt string) {
			if err := os.WriteFile(filepath.Join(wt, "blob.bin"), []byte{0, 1, 2, 255}, 0o644); err != nil {
				t.Fatal(err)
			}
		}, "UNTRACKED_FILES"},
		// git worktree remove would delete ignored files without --force.
		"ignored": {func(t *testing.T, wt string) {
			exclude := git(t, wt, "rev-parse", "--git-path", "info/exclude")
			if !filepath.IsAbs(exclude) {
				exclude = filepath.Join(wt, exclude)
			}
			_ = os.MkdirAll(filepath.Dir(exclude), 0o755)
			if err := os.WriteFile(exclude, []byte(".env\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(wt, ".env"), []byte("SECRET=1\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}, "IGNORED_FILES"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, baseConfig)
			_, wt, wtID := finishedWorktree(t, e, true)
			tc.mutate(t, wt)
			before := git(t, wt, "status", "--porcelain=v1", "--untracked-files=all")
			item := e.cleanup(app.CleanupRequest{WorktreeIDs: []string{wtID}, DeleteBranch: true})
			if item.Eligible || item.WorktreeRemoved || !hasReason(item, tc.reason) {
				t.Fatalf("%s must be preserved with %s: %+v", name, tc.reason, item)
			}
			if !exists(wt) || e.worktree(wtID).State != domain.WorktreeRetained {
				t.Fatal("worktree must stay on disk and RETAINED")
			}
			if after := git(t, wt, "status", "--porcelain=v1", "--untracked-files=all"); after != before || (name == "ignored" && !exists(filepath.Join(wt, ".env"))) {
				t.Fatalf("worktree content changed:\n%s\n---\n%s", before, after)
			}
		})
	}
}

// F-GIT squash: a squash integration does not contain the source commit, so
// the branch is the last reference and is kept; a real merge lets it go.
func TestCleanupBranchAfterSquashAndMerge(t *testing.T) {
	for _, squash := range []bool{true, false} {
		e := newEnv(t, baseConfig)
		_, wt, wtID := finishedWorktree(t, e, true)
		head := git(t, wt, "rev-parse", "HEAD")
		integ := git(t, e.repo, "rev-parse", "refs/heads/integration")
		if squash {
			c := exec.Command("git", "-C", e.repo, "commit-tree", head+"^{tree}", "-p", integ, "-m", "squash")
			c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
			out, err := c.Output()
			if err != nil {
				t.Fatal(err)
			}
			git(t, e.repo, "update-ref", "refs/heads/integration", string(out[:len(out)-1]), integ)
		} else {
			git(t, e.repo, "update-ref", "refs/heads/integration", head, integ)
		}
		branch := e.worktree(wtID).BranchRef
		item := e.cleanup(app.CleanupRequest{WorktreeIDs: []string{wtID}, DeleteBranch: true})
		if !item.WorktreeRemoved {
			t.Fatalf("squash=%v: worktree must be removed: %+v", squash, item)
		}
		_, err := exec.Command("git", "-C", e.repo, "rev-parse", "--verify", "--quiet", branch).Output()
		if squash && (item.BranchRemoved || item.BranchKeptReason != "UNMERGED_COMMITS" || err != nil) {
			t.Fatalf("squash: branch must be kept: %+v %v", item, err)
		}
		if !squash && (!item.BranchRemoved || err == nil) {
			t.Fatalf("merged: branch must be deleted: %+v", item)
		}
	}
}

// Liveness: an ALIVE or UNKNOWN (SessionEnd without exit proof) session
// keeps the worktree; so does an unexpired retention period.
func TestCleanupRequiresConfirmedExitAndRetention(t *testing.T) {
	e := newEnv(t, baseConfig)
	issueID, wt, wtID := finishedWorktree(t, e, false)
	if item := e.cleanup(app.CleanupRequest{WorktreeIDs: []string{wtID}}); item.Eligible || !hasReason(item, "SESSION_ALIVE") {
		t.Fatalf("alive session must block: %+v", item)
	}
	if err := e.app.FinishSession(e.ctx, app.FinishRequest{HookIdentity: app.HookIdentity{ClaudeSessionID: "claude-X", Event: "SessionEnd"}}); err != nil {
		t.Fatal(err)
	}
	if item := e.cleanup(app.CleanupRequest{WorktreeIDs: []string{wtID}}); item.Eligible || !hasReason(item, "SESSION_LIVENESS_UNKNOWN") {
		t.Fatalf("unknown liveness must block: %+v", item)
	}
	e.exitWorker(issueID)
	// Policy sweep (no ids): retention not elapsed yet.
	if item := e.cleanup(app.CleanupRequest{}); item.Eligible || !hasReason(item, "RETENTION_NOT_ELAPSED") {
		t.Fatalf("retention must block a policy sweep: %+v", item)
	}
	e.clock.Advance(169 * time.Hour)
	if item := e.cleanup(app.CleanupRequest{}); !item.WorktreeRemoved || exists(wt) {
		t.Fatalf("after retention the sweep removes it: %+v", item)
	}
	// Removed worktrees are not evaluated again by a sweep.
	res, err := e.app.CleanupWorktrees(e.ctx, e.cmd, app.CleanupRequest{ProjectID: e.project})
	if err != nil || len(res.Items) != 0 {
		t.Fatalf("removed worktree listed again: %+v %v", res, err)
	}
}
