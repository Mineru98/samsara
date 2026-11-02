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

// F-GIT conflict classification: text, binary, rename and directory
// conflicts are told apart from real git merge-tree output (04 §12.1).
func TestMergeTreeConflictClassification(t *testing.T) {
	ctx := context.Background()
	g := New()
	repo := t.TempDir()
	run(t, repo, "init", "-q", "-b", "main")
	write(t, filepath.Join(repo, "text.txt"), "base\n")
	write(t, filepath.Join(repo, "img.bin"), "\x00\x01base")
	write(t, filepath.Join(repo, "old.txt"), "keep me\n")
	write(t, filepath.Join(repo, "node"), "file\n")
	write(t, filepath.Join(repo, "clean.txt"), "clean\n")
	run(t, repo, "add", ".")
	run(t, repo, "commit", "-q", "-m", "base")

	run(t, repo, "checkout", "-q", "-b", "src")
	write(t, filepath.Join(repo, "text.txt"), "source\n")
	write(t, filepath.Join(repo, "img.bin"), "\x00\x02source")
	run(t, repo, "mv", "old.txt", "renamed.txt")
	run(t, repo, "rm", "-q", "node")
	if err := os.MkdirAll(filepath.Join(repo, "node"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(repo, "node", "child.txt"), "dir\n")
	run(t, repo, "add", "-A")
	run(t, repo, "commit", "-q", "-m", "source")
	source := run(t, repo, "rev-parse", "HEAD")

	run(t, repo, "checkout", "-q", "main")
	write(t, filepath.Join(repo, "text.txt"), "target\n")
	write(t, filepath.Join(repo, "img.bin"), "\x00\x03target")
	run(t, repo, "rm", "-q", "old.txt")
	write(t, filepath.Join(repo, "node"), "changed file\n")
	write(t, filepath.Join(repo, "clean.txt"), "clean on target\n")
	run(t, repo, "add", "-A")
	run(t, repo, "commit", "-q", "-m", "target")
	target := run(t, repo, "rev-parse", "HEAD")

	mr, err := g.MergeTree(ctx, repo, target, source)
	if err != nil || !mr.Conflict {
		t.Fatalf("expected conflict: %+v %v", mr, err)
	}
	kinds := map[string]string{}
	for _, f := range mr.Files {
		kinds[f.Path] = f.Kind
		if len(f.Types) == 0 {
			t.Fatalf("%s carries no git conflict type", f.Path)
		}
	}
	want := map[string]string{"text.txt": ConflictText, "img.bin": ConflictBinary, "renamed.txt": ConflictRename}
	for p, k := range want {
		if kinds[p] != k {
			t.Fatalf("%s classified %q, want %q (all: %v)", p, kinds[p], k, kinds)
		}
	}
	dir := false
	for p, k := range kinds {
		if strings.HasPrefix(p, "node") && k == ConflictDirectory {
			dir = true
		}
	}
	if !dir {
		t.Fatalf("file/directory conflict not classified: %v", kinds)
	}
	if _, ok := kinds["clean.txt"]; ok {
		t.Fatalf("one-sided change must not be reported as a conflict: %v", kinds)
	}
	// A clean merge still yields a tree.
	if mr, err := g.MergeTree(ctx, repo, target, target); err != nil || mr.Conflict || !ValidOID(mr.Tree) {
		t.Fatalf("clean merge: %+v %v", mr, err)
	}
}

func TestRefsContainingAndDeleteRefCAS(t *testing.T) {
	ctx := context.Background()
	g := New()
	repo := t.TempDir()
	run(t, repo, "init", "-q", "-b", "main")
	write(t, filepath.Join(repo, "f"), "1\n")
	run(t, repo, "add", ".")
	run(t, repo, "commit", "-q", "-m", "one")
	run(t, repo, "branch", "topic")
	tip := run(t, repo, "rev-parse", "topic")
	refs, err := g.RefsContaining(ctx, repo, tip)
	if err != nil || len(refs) != 2 {
		t.Fatalf("refs containing: %v %v", refs, err)
	}
	if err := g.DeleteRefCAS(ctx, repo, "refs/heads/topic", strings.Repeat("1", 40), "t"); !errors.Is(err, ErrCASFailed) {
		t.Fatalf("CAS delete with a wrong old value must fail: %v", err)
	}
	if err := g.DeleteRefCAS(ctx, repo, "refs/heads/topic", tip, "t"); err != nil {
		t.Fatal(err)
	}
	if oid, _ := g.ResolveRef(ctx, repo, "refs/heads/topic"); oid != "" {
		t.Fatal("branch must be deleted")
	}
}
