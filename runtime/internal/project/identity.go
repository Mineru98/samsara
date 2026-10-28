// Package project resolves project roots and holds the local trust registry.
package project

import (
	"bytes"
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Identity is the canonical location of a project checkout.
type Identity struct {
	Root           string // canonical project root (main worktree for linked worktrees)
	Toplevel       string // toplevel of the given cwd's worktree
	IsGit          bool
	CommonDir      string // canonical git common dir
	ObjectFormat   string
	RemoteIdentity string // normalised origin URL, informational only
}

// Canonical returns an absolute, symlink-resolved path.
func Canonical(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	r, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	return filepath.Clean(r), nil
}

// Resolve computes the identity of the directory containing cwd. Linked
// worktrees of one repository resolve to the same project root (01 §4.1).
func Resolve(ctx context.Context, cwd string) (*Identity, error) {
	c, err := Canonical(cwd)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := gitOut(ctx, c, "rev-parse", "--show-toplevel", "--git-common-dir")
	if err != nil {
		return &Identity{Root: c, Toplevel: c}, nil
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 2 {
		return &Identity{Root: c, Toplevel: c}, nil
	}
	top, err := Canonical(lines[0])
	if err != nil {
		return nil, err
	}
	common := lines[1]
	if !filepath.IsAbs(common) {
		common = filepath.Join(c, common)
	}
	common, err = Canonical(common)
	if err != nil {
		return nil, err
	}
	id := &Identity{Toplevel: top, IsGit: true, CommonDir: common, ObjectFormat: "sha1"}
	if filepath.Base(common) == ".git" {
		id.Root = filepath.Dir(common)
	} else {
		id.Root = top
	}
	if f, err := gitOut(ctx, c, "rev-parse", "--show-object-format"); err == nil && strings.TrimSpace(f) != "" {
		id.ObjectFormat = strings.TrimSpace(f)
	}
	if u, err := gitOut(ctx, c, "config", "--get", "remote.origin.url"); err == nil {
		id.RemoteIdentity = NormalizeRemote(strings.TrimSpace(u))
	}
	return id, nil
}

// NormalizeRemote maps SSH and HTTPS spellings of the same remote to one
// host/path form. It is informational: publication authority is never merged
// on remote URL alone (contract §2.1).
func NormalizeRemote(u string) string {
	u = strings.TrimSuffix(strings.TrimSpace(u), ".git")
	if u == "" {
		return ""
	}
	if i := strings.Index(u, "://"); i >= 0 {
		u = u[i+3:]
		if at := strings.Index(u, "@"); at >= 0 && at < strings.Index(u+"/", "/") {
			u = u[at+1:]
		}
		return strings.ToLower(u)
	}
	if at := strings.Index(u, "@"); at >= 0 {
		u = u[at+1:]
	}
	return strings.ToLower(strings.Replace(u, ":", "/", 1))
}

func gitOut(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return stdout.String(), nil
}

// Within reports whether path is dir or inside dir (both canonical).
func Within(path, dir string) bool {
	if path == dir {
		return true
	}
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel != "." && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel)
}
