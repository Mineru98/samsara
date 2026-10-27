// Package gitops runs git with argv arrays only (no shell), parses porcelain
// -z output, and implements the non-destructive worktree and integration
// primitives of 04-git-worktree-and-merge-plan.md. It never uses --force,
// reset --hard, clean, stash or force-push.
package gitops

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// Git runs git commands.
type Git struct {
	Binary  string
	Timeout time.Duration
}

// New returns a runner with a default timeout.
func New() *Git { return &Git{Binary: "git", Timeout: 2 * time.Minute} }

// Result of a git invocation.
type Result struct {
	Stdout, Stderr string
	ExitCode       int
}

// Run executes git -C dir args... and returns the result. A non-zero exit is
// reported in ExitCode, not as an error; errors are for failures to run.
func (g *Git) Run(ctx context.Context, dir string, env []string, args ...string) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, g.Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, g.Binary, append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C", "GIT_OPTIONAL_LOCKS=0")
	cmd.Env = append(cmd.Env, env...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	r := Result{Stdout: out.String(), Stderr: errb.String()}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		r.ExitCode = ee.ExitCode()
		return r, nil
	}
	if err != nil {
		return r, err
	}
	return r, nil
}

func (g *Git) must(ctx context.Context, dir string, args ...string) (string, error) {
	r, err := g.Run(ctx, dir, nil, args...)
	if err != nil {
		return "", err
	}
	if r.ExitCode != 0 {
		return "", fmt.Errorf("git %s: exit %d: %s", strings.Join(args, " "), r.ExitCode, strings.TrimSpace(r.Stderr))
	}
	return r.Stdout, nil
}

var oidRe = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`)

// ValidOID reports whether s is a full SHA-1 or SHA-256 object id.
func ValidOID(s string) bool { return oidRe.MatchString(s) }

// ZeroOID returns the all-zero OID for an object format.
func ZeroOID(format string) string {
	if format == "sha256" {
		return strings.Repeat("0", 64)
	}
	return strings.Repeat("0", 40)
}

// RevParse resolves a revision to a full OID.
func (g *Git) RevParse(ctx context.Context, dir, rev string) (string, error) {
	out, err := g.must(ctx, dir, "rev-parse", "--verify", "--end-of-options", rev+"^{commit}")
	if err != nil {
		return "", err
	}
	oid := strings.TrimSpace(out)
	if !ValidOID(oid) {
		return "", fmt.Errorf("unexpected rev-parse output %q", oid)
	}
	return oid, nil
}

// ResolveRef returns the OID of a full ref, or "" when it does not exist.
func (g *Git) ResolveRef(ctx context.Context, dir, ref string) (string, error) {
	r, err := g.Run(ctx, dir, nil, "rev-parse", "--verify", "--quiet", "--end-of-options", ref)
	if err != nil {
		return "", err
	}
	if r.ExitCode != 0 {
		return "", nil
	}
	return strings.TrimSpace(r.Stdout), nil
}

// WorktreeInfo is one entry of `worktree list --porcelain -z`.
type WorktreeInfo struct {
	Path     string
	HEAD     string
	Branch   string // full ref, empty when detached
	Detached bool
	Bare     bool
	Locked   bool
	Prunable bool
}

// ListWorktrees parses the NUL-separated porcelain listing.
func (g *Git) ListWorktrees(ctx context.Context, repo string) ([]WorktreeInfo, error) {
	out, err := g.must(ctx, repo, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return nil, err
	}
	return ParseWorktreeList(out), nil
}

// ParseWorktreeList parses porcelain -z output: attributes are NUL
// terminated and records end with an empty attribute.
func ParseWorktreeList(out string) []WorktreeInfo {
	var res []WorktreeInfo
	var cur *WorktreeInfo
	for _, f := range strings.Split(out, "\x00") {
		if f == "" {
			if cur != nil {
				res = append(res, *cur)
				cur = nil
			}
			continue
		}
		k, v, _ := strings.Cut(f, " ")
		switch k {
		case "worktree":
			cur = &WorktreeInfo{Path: v}
		case "HEAD":
			if cur != nil {
				cur.HEAD = v
			}
		case "branch":
			if cur != nil {
				cur.Branch = v
			}
		case "detached":
			if cur != nil {
				cur.Detached = true
			}
		case "bare":
			if cur != nil {
				cur.Bare = true
			}
		case "locked":
			if cur != nil {
				cur.Locked = true
			}
		case "prunable":
			if cur != nil {
				cur.Prunable = true
			}
		}
	}
	if cur != nil {
		res = append(res, *cur)
	}
	return res
}

// AddWorktree creates a new branch at base in a new linked worktree.
func (g *Git) AddWorktree(ctx context.Context, repo, path, branch, base string) error {
	if !ValidOID(base) {
		return fmt.Errorf("base must be a full OID")
	}
	_, err := g.must(ctx, repo, "worktree", "add", "-b", branch, "--end-of-options", path, base)
	return err
}

// AddDetachedWorktree checks out sha detached at path (candidate/verify).
func (g *Git) AddDetachedWorktree(ctx context.Context, repo, path, sha string) error {
	if !ValidOID(sha) {
		return fmt.Errorf("sha must be a full OID")
	}
	_, err := g.must(ctx, repo, "worktree", "add", "--detach", "--end-of-options", path, sha)
	return err
}

// RemoveWorktree removes a worktree without --force; git refuses dirty trees.
func (g *Git) RemoveWorktree(ctx context.Context, repo, path string) error {
	_, err := g.must(ctx, repo, "worktree", "remove", "--end-of-options", path)
	return err
}

// StatusEntry is a porcelain v2 record (only the kind and path).
type StatusEntry struct {
	Kind string // "1" ordinary, "2" rename, "u" unmerged, "?" untracked, "!" ignored
	Path string
}

// Status returns porcelain v2 entries including untracked files.
func (g *Git) Status(ctx context.Context, dir string) ([]StatusEntry, error) {
	out, err := g.must(ctx, dir, "status", "--porcelain=v2", "-z", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	return ParseStatusV2(out), nil
}

// ParseStatusV2 parses NUL separated porcelain v2.
func ParseStatusV2(out string) []StatusEntry {
	var res []StatusEntry
	fields := strings.Split(out, "\x00")
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		if f == "" || f[0] == '#' {
			continue
		}
		switch f[0] {
		case '1':
			p := strings.SplitN(f, " ", 9)
			if len(p) == 9 {
				res = append(res, StatusEntry{Kind: "1", Path: p[8]})
			}
		case '2':
			p := strings.SplitN(f, " ", 10)
			if len(p) == 10 {
				res = append(res, StatusEntry{Kind: "2", Path: p[9]})
			}
			i++ // original path follows
		case 'u':
			p := strings.SplitN(f, " ", 11)
			if len(p) == 11 {
				res = append(res, StatusEntry{Kind: "u", Path: p[10]})
			}
		case '?', '!':
			res = append(res, StatusEntry{Kind: string(f[0]), Path: f[2:]})
		}
	}
	return res
}

// MergeResult of `merge-tree --write-tree`.
type MergeResult struct {
	Tree     string
	Conflict bool
	Detail   string
}

// MergeTree computes the merge of base and source without touching any
// index or working tree. Exit 0 = clean, 1 = conflict, other = error
// (04 §9.3). Only single invocations are used.
func (g *Git) MergeTree(ctx context.Context, repo, base, source string) (MergeResult, error) {
	r, err := g.Run(ctx, repo, nil, "merge-tree", "--write-tree", "--no-messages", base, source)
	if err != nil {
		return MergeResult{}, err
	}
	switch r.ExitCode {
	case 0:
		tree := strings.TrimSpace(strings.SplitN(r.Stdout, "\n", 2)[0])
		if !ValidOID(tree) {
			return MergeResult{}, fmt.Errorf("merge-tree returned no tree OID")
		}
		return MergeResult{Tree: tree}, nil
	case 1:
		return MergeResult{Conflict: true, Detail: strings.TrimSpace(r.Stdout)}, nil
	}
	return MergeResult{}, fmt.Errorf("merge-tree exit %d: %s", r.ExitCode, strings.TrimSpace(r.Stderr))
}

// CommitTree creates a merge commit of tree with parents, with a fixed
// integration identity so the tested commit is the published commit.
func (g *Git) CommitTree(ctx context.Context, repo, tree, message string, when time.Time, parents ...string) (string, error) {
	args := []string{"commit-tree", tree}
	for _, p := range parents {
		args = append(args, "-p", p)
	}
	args = append(args, "-m", message)
	date := when.UTC().Format(time.RFC3339)
	env := []string{"GIT_AUTHOR_NAME=Samsara Integration", "GIT_AUTHOR_EMAIL=samsara@localhost", "GIT_AUTHOR_DATE=" + date,
		"GIT_COMMITTER_NAME=Samsara Integration", "GIT_COMMITTER_EMAIL=samsara@localhost", "GIT_COMMITTER_DATE=" + date}
	r, err := g.Run(ctx, repo, env, args...)
	if err != nil {
		return "", err
	}
	if r.ExitCode != 0 {
		return "", fmt.Errorf("commit-tree: %s", strings.TrimSpace(r.Stderr))
	}
	oid := strings.TrimSpace(r.Stdout)
	if !ValidOID(oid) {
		return "", fmt.Errorf("commit-tree returned %q", oid)
	}
	return oid, nil
}

// ErrCASFailed means the ref did not hold the expected old value.
var ErrCASFailed = errors.New("compare-and-swap failed: target moved")

// UpdateRefCAS moves ref to newOID only if it currently equals oldOID
// (04 §9.4). Never forces.
func (g *Git) UpdateRefCAS(ctx context.Context, repo, ref, newOID, oldOID, reason string) error {
	r, err := g.Run(ctx, repo, nil, "update-ref", "-m", reason, ref, newOID, oldOID)
	if err != nil {
		return err
	}
	if r.ExitCode != 0 {
		if strings.Contains(r.Stderr, "but expected") || strings.Contains(r.Stderr, "is at") || strings.Contains(r.Stderr, "cannot lock ref") {
			return ErrCASFailed
		}
		return fmt.Errorf("update-ref: %s", strings.TrimSpace(r.Stderr))
	}
	return nil
}

// IsAncestor reports whether a is an ancestor of (or equal to) b.
func (g *Git) IsAncestor(ctx context.Context, repo, a, b string) (bool, error) {
	r, err := g.Run(ctx, repo, nil, "merge-base", "--is-ancestor", a, b)
	if err != nil {
		return false, err
	}
	switch r.ExitCode {
	case 0:
		return true, nil
	case 1:
		return false, nil
	}
	return false, fmt.Errorf("merge-base: %s", strings.TrimSpace(r.Stderr))
}

// RefCheckedOut reports whether a full branch ref is checked out in any
// worktree of repo (local CAS targets must not be, 04 §5.3).
func (g *Git) RefCheckedOut(ctx context.Context, repo, ref string) (bool, error) {
	wts, err := g.ListWorktrees(ctx, repo)
	if err != nil {
		return false, err
	}
	for _, w := range wts {
		if w.Branch == ref {
			return true, nil
		}
	}
	return false, nil
}

// ChangedFiles lists files changed between base and head.
func (g *Git) ChangedFiles(ctx context.Context, repo, base, head string) ([]string, error) {
	out, err := g.must(ctx, repo, "diff", "--name-only", "-z", base, head)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, f := range strings.Split(out, "\x00") {
		if f != "" {
			files = append(files, f)
		}
	}
	return files, nil
}

// Version returns `git --version`.
func (g *Git) Version(ctx context.Context) string {
	out, err := exec.CommandContext(ctx, g.Binary, "--version").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
