// Package ops implements the install, upgrade, restore and version-pruning
// procedures of 05 §17. Every procedure that touches the database holds the
// daemon instance lock for its whole duration, so no daemon (old or new)
// can write the database while it is being backed up, migrated or replaced.
package ops

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Mineru98/samsara/runtime/internal/app"
	"github.com/Mineru98/samsara/runtime/internal/daemon"
	"github.com/Mineru98/samsara/runtime/internal/hooks"
	"github.com/Mineru98/samsara/runtime/internal/paths"
	"github.com/Mineru98/samsara/runtime/internal/project"
	"github.com/Mineru98/samsara/runtime/internal/store/sqlite"
	"github.com/Mineru98/samsara/runtime/internal/transport/ipc"
)

// BinaryName is the installed executable name.
const BinaryName = "samsara"

// VersionsDir holds one directory per installed version.
func VersionsDir(l paths.Layout) string { return filepath.Join(l.InstallDir(), "versions") }

// CurrentLink is the stable path that always points at the current version.
func CurrentLink(l paths.Layout) string { return filepath.Join(l.InstallDir(), "bin", BinaryName) }

// VersionBinary is where version v is installed.
func VersionBinary(l paths.Layout, v string) string {
	return filepath.Join(VersionsDir(l), v, BinaryName)
}

// InstallResult reports an install.
type InstallResult struct {
	Version string `json:"version"`
	Binary  string `json:"binary"`
	Link    string `json:"link"`
	Reused  bool   `json:"reused,omitempty"`
}

// Install copies self into versions/<version>/ and atomically points the
// current link at it. Other versions are never touched: running hooks and
// workers may still execute them (ACT-12).
func Install(l paths.Layout, self, version string) (*InstallResult, error) {
	if version == "" || strings.ContainsAny(version, `/\`) || version == "." || version == ".." {
		return nil, fmt.Errorf("invalid version %q", version)
	}
	dst := VersionBinary(l, version)
	res := &InstallResult{Version: version, Binary: dst, Link: CurrentLink(l)}
	if err := paths.EnsurePrivateDir(filepath.Dir(dst)); err != nil {
		return nil, err
	}
	if same, _ := sameFile(self, dst); same {
		res.Reused = true
	} else if err := copyExecutable(self, dst); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(res.Link), 0o755); err != nil {
		return nil, err
	}
	tmp := res.Link + ".tmp"
	_ = os.Remove(tmp)
	if err := os.Symlink(dst, tmp); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, res.Link); err != nil {
		_ = os.Remove(tmp)
		return nil, err
	}
	return res, nil
}

func sameFile(a, b string) (bool, error) {
	fa, err := os.Stat(a)
	if err != nil {
		return false, err
	}
	fb, err := os.Stat(b)
	if err != nil {
		return false, err
	}
	return os.SameFile(fa, fb), nil
}

// copyExecutable writes src to dst through a temporary file and rename, so
// a process executing an older dst never sees a half-written file.
func copyExecutable(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

// UpgradeOptions configure Upgrade.
type UpgradeOptions struct {
	Self    string // the new binary (normally os.Executable)
	Version string // the new binary's version
	// StopTimeout bounds the wait for the old daemon to release its lock.
	StopTimeout time.Duration
	// Start starts the new daemon even if none was running before.
	Start bool
}

// UpgradeResult reports each step of an upgrade.
type UpgradeResult struct {
	FromVersion      string         `json:"from_version,omitempty"`
	ToVersion        string         `json:"to_version"`
	DaemonWasRunning bool           `json:"daemon_was_running"`
	Backup           string         `json:"backup,omitempty"`
	SchemaBefore     int            `json:"schema_before"`
	SchemaAfter      int            `json:"schema_after"`
	Integrity        string         `json:"integrity"`
	Install          *InstallResult `json:"install,omitempty"`
	DaemonStarted    bool           `json:"daemon_started"`
	DaemonVersion    string         `json:"daemon_version,omitempty"`
	HooksRetargeted  []string       `json:"hooks_retargeted"`
	Steps            []string       `json:"steps"`
}

// Upgrade performs 05 §17.2: graceful stop of the running daemon, a
// consistent backup of the unmigrated database, migration by this binary,
// an integrity check, installation of this binary and a restart. When a
// step fails nothing after it runs, the daemon is not restarted, and the
// error names the backup to restore from.
func Upgrade(ctx context.Context, l paths.Layout, o UpgradeOptions) (*UpgradeResult, error) {
	if o.StopTimeout == 0 {
		o.StopTimeout = 15 * time.Second
	}
	res := &UpgradeResult{ToVersion: o.Version}
	if err := l.Ensure(); err != nil {
		return res, err
	}
	// The start lock is held until the new daemon answers, so a hook or CLI
	// call in between cannot start the old binary (daemon.EnsureRunning
	// only spawns while holding this lock).
	starter, err := waitFor(ctx, l.StartLockPath(), o.StopTimeout)
	if err != nil {
		return res, fmt.Errorf("start lock: %w", err)
	}
	defer starter.Unlock()
	if daemon.Ping(ctx, l) {
		res.DaemonWasRunning = true
		var info app.RuntimeInfo
		c := ipc.NewClient(l.SocketPath())
		if err := c.Call(ctx, "/ipc/v1/runtime", map[string]any{}, &info); err == nil {
			res.FromVersion = info.Version
		}
		if err := c.Call(ctx, "/ipc/v1/daemon/stop", map[string]any{}, nil); err != nil {
			return res, fmt.Errorf("stop daemon: %w", err)
		}
		res.Steps = append(res.Steps, "daemon stopped")
	}
	lock, err := waitFor(ctx, l.LockPath(), o.StopTimeout)
	if err != nil {
		return res, fmt.Errorf("daemon lock: %w", err)
	}
	locked := true
	unlock := func() {
		if locked {
			lock.Unlock()
			locked = false
		}
	}
	defer unlock()

	dbPath := l.DBPath()
	if _, err := os.Stat(dbPath); err == nil {
		fi, err := sqlite.Inspect(ctx, dbPath)
		if err != nil {
			return res, err
		}
		res.SchemaBefore = fi.SchemaVersion
		if len(fi.Problems) > 0 {
			return res, fmt.Errorf("database is not upgradable by %s: %s", o.Version, strings.Join(fi.Problems, "; "))
		}
		res.Backup = filepath.Join(l.BackupDir(), fmt.Sprintf("pre-upgrade-%s-to-%s-%s.db", nameOr(res.FromVersion, "unknown"), o.Version, stamp()))
		if err := sqlite.BackupFile(ctx, dbPath, res.Backup); err != nil {
			return res, fmt.Errorf("backup: %w", err)
		}
		_ = os.Chmod(res.Backup, 0o600)
		res.Steps = append(res.Steps, "backup written")
	}
	db, err := sqlite.Open(ctx, dbPath, nil)
	if err != nil {
		return res, fmt.Errorf("migrate: %w%s", err, restoreHint(res.Backup))
	}
	problems, err := db.CheckIntegrity(ctx)
	db.Close()
	if err != nil || len(problems) > 0 {
		return res, fmt.Errorf("integrity check failed: %v %s%s", err, strings.Join(problems, "; "), restoreHint(res.Backup))
	}
	res.SchemaAfter = sqlite.SchemaVersion
	res.Integrity = "ok"
	res.Steps = append(res.Steps, fmt.Sprintf("migrated schema %d -> %d, integrity ok", res.SchemaBefore, res.SchemaAfter))

	inst, err := Install(l, o.Self, o.Version)
	if err != nil {
		return res, fmt.Errorf("install: %w%s", err, restoreHint(res.Backup))
	}
	res.Install = inst
	res.Steps = append(res.Steps, "installed "+inst.Binary)
	// Hooks that run an installed version now run the new one: an older
	// binary cannot open the migrated database. The old binaries stay on
	// disk for sessions that already loaded their hooks (ACT-12).
	res.HooksRetargeted, err = RetargetHooks(l, inst.Binary)
	if err != nil {
		return res, fmt.Errorf("retarget hooks: %w", err)
	}
	unlock()

	if !res.DaemonWasRunning && !o.Start {
		return res, nil
	}
	if err := daemon.Spawn(l, inst.Binary); err != nil {
		return res, fmt.Errorf("start new daemon: %w", err)
	}
	for deadline := time.Now().Add(10 * time.Second); !daemon.Ping(ctx, l); {
		if time.Now().After(deadline) {
			return res, fmt.Errorf("new daemon did not become ready (see %s)", l.LogPath())
		}
		time.Sleep(100 * time.Millisecond)
	}
	res.DaemonStarted = true
	var info app.RuntimeInfo
	if err := ipc.NewClient(l.SocketPath()).Call(ctx, "/ipc/v1/runtime", map[string]any{}, &info); err == nil {
		res.DaemonVersion = info.Version
	}
	if res.DaemonVersion != o.Version {
		return res, fmt.Errorf("new daemon reports version %q, want %q", res.DaemonVersion, o.Version)
	}
	res.Steps = append(res.Steps, "daemon "+res.DaemonVersion+" started")
	return res, nil
}

func nameOr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func stamp() string { return time.Now().UTC().Format("20060102T150405.000Z") }

func restoreHint(backup string) string {
	if backup == "" {
		return ""
	}
	return "; the previous database is intact in " + backup + " (samsara restore --from " + backup + ")"
}

// waitFor acquires a lock file, waiting for its holder (a stopping daemon
// or a concurrent starter) to release it.
func waitFor(ctx context.Context, path string, timeout time.Duration) (*daemon.Lock, error) {
	deadline := time.Now().Add(timeout)
	for {
		lock, err := daemon.TryLock(path)
		if err == nil {
			return lock, nil
		}
		if !errors.Is(err, daemon.ErrAlreadyRunning) || time.Now().After(deadline) {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// RestoreResult reports a restore.
type RestoreResult struct {
	From               string `json:"from"`
	PreRestoreBackup   string `json:"pre_restore_backup,omitempty"`
	BackupSchema       int    `json:"backup_schema"`
	PreviousGeneration string `json:"previous_generation,omitempty"`
	DatabaseGeneration string `json:"database_generation"`
	Integrity          string `json:"integrity"`
	Note               string `json:"note"`
}

// ErrDaemonRunning is returned when a procedure needs the daemon stopped.
var ErrDaemonRunning = errors.New("the daemon is running; stop it first with `samsara daemon stop`")

// Restore replaces the database with a verified backup (05 §17.3). The
// current database is kept as a pre-restore backup. The restored file gets
// a new database_generation and its in-flight intents become UNKNOWN
// (app.PrepareRestored); the next daemon start reconciles backend, Git and
// GitHub facts before new work.
func Restore(ctx context.Context, l paths.Layout, from string) (*RestoreResult, error) {
	res := &RestoreResult{From: from}
	if daemon.Ping(ctx, l) {
		return res, ErrDaemonRunning
	}
	if err := l.Ensure(); err != nil {
		return res, err
	}
	lock, err := daemon.TryLock(l.LockPath())
	if err != nil {
		if errors.Is(err, daemon.ErrAlreadyRunning) {
			return res, ErrDaemonRunning
		}
		return res, err
	}
	defer lock.Unlock()

	fi, err := sqlite.Inspect(ctx, from)
	if err != nil {
		return res, err
	}
	res.BackupSchema = fi.SchemaVersion
	if len(fi.Problems) > 0 {
		return res, fmt.Errorf("backup %s cannot be restored by this binary: %s", from, strings.Join(fi.Problems, "; "))
	}
	dbPath := l.DBPath()
	if _, err := os.Stat(dbPath); err == nil {
		if cur, err := sqlite.Inspect(ctx, dbPath); err == nil {
			res.PreviousGeneration = cur.DatabaseGeneration
		}
		res.PreRestoreBackup = filepath.Join(l.BackupDir(), "pre-restore-"+stamp()+".db")
		if err := sqlite.BackupFile(ctx, dbPath, res.PreRestoreBackup); err != nil {
			return res, fmt.Errorf("pre-restore backup: %w", err)
		}
		_ = os.Chmod(res.PreRestoreBackup, 0o600)
	}
	// The copy is migrated, prepared and checked before it replaces
	// anything: an interruption leaves the current database untouched, and
	// a file with the backup's live intents is never put in place.
	tmp := dbPath + ".restore-tmp"
	removeDB(tmp)
	defer removeDB(tmp)
	if err := sqlite.BackupFile(ctx, from, tmp); err != nil {
		return res, fmt.Errorf("copy backup: %w", err)
	}
	_ = os.Chmod(tmp, 0o600)
	db, err := sqlite.Open(ctx, tmp, nil)
	if err != nil {
		return res, fmt.Errorf("open backup copy: %w", err)
	}
	gen, err := prepare(ctx, db, from)
	if cerr := db.Close(); err == nil && cerr != nil {
		err = cerr
	}
	if err != nil {
		return res, err
	}
	if fi, err := os.Stat(tmp + "-wal"); err == nil && fi.Size() > 0 {
		return res, fmt.Errorf("prepared copy was not checkpointed (%s-wal is not empty); the current database is unchanged", tmp)
	}
	// Close checkpointed the copy's WAL; the swap is a single rename, and
	// the old database's -wal/-shm must go with it so SQLite never replays
	// them onto the restored file.
	if err := os.Rename(tmp, dbPath); err != nil {
		return res, fmt.Errorf("replace database: %w (the current database is unchanged)", err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.Remove(dbPath + suffix); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return res, fmt.Errorf("remove stale %s: %w%s", suffix, err, restoreHint(res.PreRestoreBackup))
		}
	}
	res.Integrity = "ok"
	res.DatabaseGeneration = gen
	res.Note = "in-flight intents of the backup are UNKNOWN and PUBLISHING candidates re-read their target; the next daemon start reconciles backend, Git and GitHub before new work"
	return res, nil
}

func prepare(ctx context.Context, db *sqlite.DB, from string) (string, error) {
	if err := app.PrepareRestored(ctx, db, from); err != nil {
		return "", fmt.Errorf("prepare restored database: %w", err)
	}
	problems, err := db.CheckIntegrity(ctx)
	if err != nil || len(problems) > 0 {
		return "", fmt.Errorf("integrity check failed: %v %s", err, strings.Join(problems, "; "))
	}
	return db.DatabaseGeneration, nil
}

func removeDB(p string) {
	for _, suffix := range []string{"", "-wal", "-shm"} {
		_ = os.Remove(p + suffix)
	}
}

// RetargetHooks points every Samsara hook that runs an installed version
// (registered projects and managed worktrees) at bin. Hooks of binaries
// outside the install directory, such as a developer build, are left alone.
func RetargetHooks(l paths.Layout, bin string) ([]string, error) {
	reg, err := project.LoadRegistry(l.RegistryPath())
	if err != nil {
		return nil, err
	}
	users, err := hookBinaries(l, reg)
	if err != nil {
		return nil, err
	}
	dirs := map[string]bool{}
	versions := VersionsDir(l)
	resolved, _ := filepath.EvalSymlinks(versions)
	for cmd, ds := range users {
		if cmd == bin || !(project.Within(cmd, versions) || (resolved != "" && project.Within(cmd, resolved))) {
			continue
		}
		for _, d := range ds {
			dirs[d] = true
		}
	}
	out := []string{}
	for d := range dirs {
		if err := hooks.Install(d, bin); err != nil {
			return out, fmt.Errorf("%s: %w", d, err)
		}
		out = append(out, d)
	}
	sort.Strings(out)
	return out, nil
}

// PruneResult reports a version prune.
type PruneResult struct {
	Current  string            `json:"current"`
	Removed  []string          `json:"removed"`
	Retained map[string]string `json:"retained"` // version -> reason
}

// PruneVersions removes installed versions other than current. A version
// is kept while a hook entry of a registered project or a managed worktree
// still executes it, and nothing is removed while managed sessions are
// alive (ACT-12: the guard binary must outlive the sessions it guards).
func PruneVersions(l paths.Layout, current string) (*PruneResult, error) {
	res := &PruneResult{Current: current, Removed: []string{}, Retained: map[string]string{}}
	entries, err := os.ReadDir(VersionsDir(l))
	if errors.Is(err, fs.ErrNotExist) {
		return res, nil
	}
	if err != nil {
		return nil, err
	}
	reg, err := project.LoadRegistry(l.RegistryPath())
	if err != nil {
		return nil, err
	}
	inUse, err := hookBinaries(l, reg)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		v := e.Name()
		if !e.IsDir() || v == current {
			continue
		}
		bin := VersionBinary(l, v)
		users := inUse[bin]
		if r, err := filepath.EvalSymlinks(bin); err == nil && r != bin {
			users = append(users, inUse[r]...)
		}
		switch {
		case len(reg.ManagedSessions) > 0:
			res.Retained[v] = fmt.Sprintf("%d managed session(s) are alive", len(reg.ManagedSessions))
		case len(users) > 0:
			res.Retained[v] = "hook entries still execute it: " + strings.Join(users, ", ")
		default:
			if err := os.RemoveAll(filepath.Join(VersionsDir(l), v)); err != nil {
				return nil, err
			}
			res.Removed = append(res.Removed, v)
		}
	}
	return res, nil
}

// hookBinaries maps each Samsara hook executable to the directories whose
// settings run it: registered project roots and managed worktrees.
func hookBinaries(l paths.Layout, reg *project.Registry) (map[string][]string, error) {
	dirs := map[string]bool{}
	for _, p := range reg.Projects {
		dirs[p.Root] = true
	}
	// Managed worktrees: <root>/<project>/<issue>/<attempt> or shallower.
	_ = filepath.WalkDir(l.WorktreeRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(l.WorktreeRoot, p)
		if d.Name() == ".git" || strings.Count(rel, string(filepath.Separator)) > 3 {
			return filepath.SkipDir
		}
		if _, err := os.Stat(filepath.Join(p, hooks.SettingsRel)); err == nil {
			dirs[p] = true
			return filepath.SkipDir
		}
		return nil
	})
	out := map[string][]string{}
	for dir := range dirs {
		cmds, err := hooks.CommandPaths(dir)
		if err != nil {
			continue // unreadable settings are not ours to judge
		}
		for _, c := range cmds {
			out[c] = append(out[c], dir)
		}
	}
	return out, nil
}
