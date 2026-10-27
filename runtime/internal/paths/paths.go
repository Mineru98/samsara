// Package paths resolves the per-OS-user state, runtime and worktree
// directories. Everything is scoped to the current OS user (ADR 01); a
// directory owned by someone else is refused (ACT-13).
package paths

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// Layout is the set of directories and files used by one user runtime.
type Layout struct {
	StateDir     string // DB, registry, logs
	RuntimeDir   string // socket, locks, ui port
	WorktreeRoot string // managed worktrees
}

// Resolve computes the layout. SAMSARA_HOME overrides everything (tests,
// isolated installs).
func Resolve() (Layout, error) {
	if h := os.Getenv("SAMSARA_HOME"); h != "" {
		abs, err := filepath.Abs(h)
		if err != nil {
			return Layout{}, err
		}
		return Layout{StateDir: filepath.Join(abs, "state"), RuntimeDir: filepath.Join(abs, "run"), WorktreeRoot: filepath.Join(abs, "worktrees")}, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return Layout{}, err
	}
	l := Layout{
		StateDir:     filepath.Join(home, ".local", "state", "samsara"),
		WorktreeRoot: filepath.Join(home, ".local", "share", "samsara", "worktrees"),
		RuntimeDir:   filepath.Join(os.TempDir(), fmt.Sprintf("samsara-%d", os.Getuid())),
	}
	if x := os.Getenv("XDG_STATE_HOME"); x != "" {
		l.StateDir = filepath.Join(x, "samsara")
	}
	if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		l.WorktreeRoot = filepath.Join(x, "samsara", "worktrees")
	}
	if x := os.Getenv("XDG_RUNTIME_DIR"); x != "" {
		l.RuntimeDir = filepath.Join(x, "samsara")
	}
	return l, nil
}

// DBPath is the SQLite file.
func (l Layout) DBPath() string { return filepath.Join(l.StateDir, "samsara.db") }

// RegistryPath is the trust/activation registry read by the hook fast path.
func (l Layout) RegistryPath() string { return filepath.Join(l.StateDir, "registry.json") }

// maxSocketPath keeps below the sun_path limit (104 on macOS, 108 on Linux).
const maxSocketPath = 100

// SocketPath is the IPC Unix domain socket. When the runtime dir is too
// deep for sun_path, a short private directory keyed by the runtime dir is
// used instead (still 0700 and owner-checked).
func (l Layout) SocketPath() string {
	p := filepath.Join(l.RuntimeDir, "samsara.sock")
	if len(p) <= maxSocketPath {
		return p
	}
	sum := sha256.Sum256([]byte(l.RuntimeDir))
	return filepath.Join(l.shortSocketDir(sum[:]), "s.sock")
}

func (l Layout) shortSocketDir(sum []byte) string {
	return filepath.Join(os.TempDir(), fmt.Sprintf("samsara-%d-%s", os.Getuid(), hex.EncodeToString(sum[:6])))
}

// LockPath is the single-instance daemon lock.
func (l Layout) LockPath() string { return filepath.Join(l.RuntimeDir, "daemon.lock") }

// StartLockPath serialises autostart attempts.
func (l Layout) StartLockPath() string { return filepath.Join(l.RuntimeDir, "start.lock") }

// UIInfoPath records the loopback UI address.
func (l Layout) UIInfoPath() string { return filepath.Join(l.RuntimeDir, "ui.json") }

// LogPath is the daemon log.
func (l Layout) LogPath() string { return filepath.Join(l.StateDir, "daemon.log") }

// PromptDir holds launch prompt files for managed workers.
func (l Layout) PromptDir() string { return filepath.Join(l.StateDir, "prompts") }

// EnsurePrivateDir creates dir with 0700 and verifies it is owned by the
// current user and not group/world writable.
func EnsurePrivateDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return CheckPrivate(dir)
}

// CheckPrivate verifies ownership and permissions of an existing path.
func CheckPrivate(path string) error {
	fi, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is a symlink; refusing", path)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if ok && int(st.Uid) != os.Getuid() {
		return fmt.Errorf("%s is owned by uid %d, not the current user %d", path, st.Uid, os.Getuid())
	}
	if fi.IsDir() && fi.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("%s is group/world writable (%o)", path, fi.Mode().Perm())
	}
	return nil
}

// Ensure creates all directories of the layout.
func (l Layout) Ensure() error {
	for _, d := range []string{l.StateDir, l.RuntimeDir, l.WorktreeRoot, l.PromptDir(), filepath.Dir(l.SocketPath())} {
		if err := EnsurePrivateDir(d); err != nil {
			return err
		}
	}
	return nil
}
