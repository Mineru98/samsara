package gitops

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	c := exec.Command("git", append([]string{"-C", dir}, args...)...)
	c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, p, s string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestParsers(t *testing.T) {
	wl := ParseWorktreeList("worktree /a b\x00HEAD 1111\x00branch refs/heads/main\x00\x00worktree /c\nd\x00HEAD 2222\x00detached\x00\x00")
	if len(wl) != 2 || wl[0].Path != "/a b" || wl[0].Branch != "refs/heads/main" || wl[1].Path != "/c\nd" || !wl[1].Detached {
		t.Fatalf("worktree parse: %+v", wl)
	}
	st := ParseStatusV2("1 .M N... 100644 100644 100644 aaa bbb dir/file name.txt\x002 R. N... 100644 100644 100644 aaa bbb R100 new name\x00old name\x00? untracked file\x00")
	if len(st) != 3 || st[0].Path != "dir/file name.txt" || st[1].Path != "new name" || st[2].Kind != "?" || st[2].Path != "untracked file" {
		t.Fatalf("status parse: %+v", st)
	}
}

func TestWorktreeMergeTreeAndCAS(t *testing.T) {
	ctx := context.Background()
	g := New()
	repo := t.TempDir()
	run(t, repo, "init", "-q", "-b", "main")
	write(t, filepath.Join(repo, "f.txt"), "base\n")
	run(t, repo, "add", ".")
	run(t, repo, "commit", "-q", "-m", "base")
	run(t, repo, "branch", "integration")
	base, err := g.RevParse(ctx, repo, "HEAD")
	if err != nil || !ValidOID(base) {
		t.Fatal(err)
	}
	wt := filepath.Join(t.TempDir(), "w1")
	if err := g.AddWorktree(ctx, repo, wt, "samsara/x/a", base); err != nil {
		t.Fatal(err)
	}
	list, _ := g.ListWorktrees(ctx, repo)
	found := false
	for _, w := range list {
		if w.Branch == "refs/heads/samsara/x/a" {
			found = true
		}
	}
	if !found {
		t.Fatalf("worktree not listed: %+v", list)
	}
	write(t, filepath.Join(wt, "a.txt"), "a\n")
	run(t, wt, "add", ".")
	run(t, wt, "commit", "-q", "-m", "a")
	srcA, _ := g.RevParse(ctx, wt, "HEAD")
	// Dirty worktrees are never removed (GIT-09).
	write(t, filepath.Join(wt, "dirty.txt"), "x")
	if err := g.RemoveWorktree(ctx, repo, wt); err == nil {
		t.Fatal("dirty worktree removed")
	}
	if _, err := os.Stat(filepath.Join(wt, "dirty.txt")); err != nil {
		t.Fatal("dirty file lost")
	}
	mr, err := g.MergeTree(ctx, repo, base, srcA)
	if err != nil || mr.Conflict || !ValidOID(mr.Tree) {
		t.Fatalf("clean merge: %+v %v", mr, err)
	}
	cand, err := g.CommitTree(ctx, repo, mr.Tree, "integrate", time.Unix(0, 0), base, srcA)
	if err != nil {
		t.Fatal(err)
	}
	// CAS with a wrong expected value fails without moving the ref.
	if err := g.UpdateRefCAS(ctx, repo, "refs/heads/integration", cand, srcA, "t"); !errors.Is(err, ErrCASFailed) {
		t.Fatalf("CAS must fail on mismatch, got %v", err)
	}
	if err := g.UpdateRefCAS(ctx, repo, "refs/heads/integration", cand, base, "t"); err != nil {
		t.Fatal(err)
	}
	if got, _ := g.ResolveRef(ctx, repo, "refs/heads/integration"); got != cand {
		t.Fatalf("integration ref %s", got)
	}
	in, _ := g.IsAncestor(ctx, repo, srcA, cand)
	if !in {
		t.Fatal("source must be contained in candidate")
	}
	if co, _ := g.RefCheckedOut(ctx, repo, "refs/heads/main"); !co {
		t.Fatal("main is checked out in the main worktree")
	}
	if co, _ := g.RefCheckedOut(ctx, repo, "refs/heads/integration"); co {
		t.Fatal("integration is not checked out")
	}
	// A conflicting change on another branch.
	wt2 := filepath.Join(t.TempDir(), "w2")
	if err := g.AddWorktree(ctx, repo, wt2, "samsara/x/b", base); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(wt2, "f.txt"), "other\n")
	run(t, wt2, "commit", "-qam", "b")
	write(t, filepath.Join(wt, "f.txt"), "mine\n")
	run(t, wt, "add", "f.txt")
	run(t, wt, "commit", "-qm", "c")
	s1, _ := g.RevParse(ctx, wt, "HEAD")
	s2, _ := g.RevParse(ctx, wt2, "HEAD")
	mr, err = g.MergeTree(ctx, repo, s1, s2)
	if err != nil || !mr.Conflict {
		t.Fatalf("conflict expected: %+v %v", mr, err)
	}
}
