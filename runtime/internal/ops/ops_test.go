package ops_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mineru98/samsara/runtime/internal/daemon"
	"github.com/Mineru98/samsara/runtime/internal/hooks"
	"github.com/Mineru98/samsara/runtime/internal/ops"
	"github.com/Mineru98/samsara/runtime/internal/paths"
	"github.com/Mineru98/samsara/runtime/internal/project"
	"github.com/Mineru98/samsara/runtime/internal/store/sqlite"
	_ "modernc.org/sqlite"
)

func layout(t *testing.T) paths.Layout {
	t.Helper()
	l := paths.ForHome(t.TempDir())
	if err := l.Ensure(); err != nil {
		t.Fatal(err)
	}
	return l
}

func fakeBinary(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "samsara")
	if err := os.WriteFile(p, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func createDB(t *testing.T, l paths.Layout) string {
	t.Helper()
	db, err := sqlite.Open(context.Background(), l.DBPath(), nil)
	if err != nil {
		t.Fatal(err)
	}
	gen := db.DatabaseGeneration
	db.Close()
	return gen
}

func TestInstallKeepsOtherVersions(t *testing.T) {
	l := layout(t)
	v1, err := ops.Install(l, fakeBinary(t, "v1"), "0.1.0")
	if err != nil {
		t.Fatal(err)
	}
	v2, err := ops.Install(l, fakeBinary(t, "v2"), "0.2.0")
	if err != nil {
		t.Fatal(err)
	}
	if target, _ := os.Readlink(v2.Link); target != v2.Binary {
		t.Fatalf("link -> %s, want %s", target, v2.Binary)
	}
	if b, _ := os.ReadFile(v1.Binary); string(b) != "v1" {
		t.Fatal("installing v2 changed the v1 binary")
	}
	if _, err := ops.Install(l, fakeBinary(t, "x"), "../evil"); err == nil {
		t.Fatal("path-like version accepted")
	}
}

// 05 §17.2: backup of the unmigrated DB, migration, integrity, install.
func TestUpgradeBacksUpMigratesAndInstalls(t *testing.T) {
	l := layout(t)
	gen := createDB(t, l)
	res, err := ops.Upgrade(context.Background(), l, ops.UpgradeOptions{Self: fakeBinary(t, "v2"), Version: "0.2.0"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Integrity != "ok" || res.SchemaAfter != sqlite.SchemaVersion || res.DaemonStarted {
		t.Fatalf("result = %+v", res)
	}
	fi, err := sqlite.Inspect(context.Background(), res.Backup)
	if err != nil || len(fi.Problems) > 0 || fi.DatabaseGeneration != gen {
		t.Fatalf("backup %s: %+v %v", res.Backup, fi, err)
	}
	if st, _ := os.Stat(res.Backup); st.Mode().Perm() != 0o600 {
		t.Fatalf("backup mode %v", st.Mode())
	}
	if b, _ := os.ReadFile(ops.VersionBinary(l, "0.2.0")); string(b) != "v2" {
		t.Fatal("new binary not installed")
	}
}

// markNewerSchema makes the file look migrated by a future binary.
func markNewerSchema(t *testing.T, path string) {
	t.Helper()
	d, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := d.Exec("INSERT INTO schema_migrations(version,name,checksum,applied_at) VALUES(99,'0099_future.sql','x',0)"); err != nil {
		t.Fatal(err)
	}
}

// 05 §17.2/17.3: a database written by a newer binary is never migrated or
// replaced by an older one.
func TestUpgradeRefusesNewerSchemaWithoutChanges(t *testing.T) {
	l := layout(t)
	createDB(t, l)
	markNewerSchema(t, l.DBPath())
	res, err := ops.Upgrade(context.Background(), l, ops.UpgradeOptions{Self: fakeBinary(t, "old"), Version: "0.0.9"})
	if err == nil || !strings.Contains(err.Error(), "newer than this binary") {
		t.Fatalf("err = %v", err)
	}
	if res.Backup != "" || res.Install != nil {
		t.Fatalf("upgrade went ahead: %+v", res)
	}
	if _, err := os.Stat(ops.VersionBinary(l, "0.0.9")); err == nil {
		t.Fatal("binary installed despite refusal")
	}
}

func TestRestoreRotatesGenerationAndKeepsCurrentDB(t *testing.T) {
	l := layout(t)
	ctx := context.Background()
	gen := createDB(t, l)
	backup := filepath.Join(t.TempDir(), "b.db")
	db, err := sqlite.Open(ctx, l.DBPath(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Backup(ctx, backup); err != nil {
		t.Fatal(err)
	}
	db.Close()

	res, err := ops.Restore(ctx, l, backup)
	if err != nil {
		t.Fatal(err)
	}
	if res.DatabaseGeneration == "" || res.DatabaseGeneration == gen || res.PreviousGeneration != gen || res.Integrity != "ok" {
		t.Fatalf("result = %+v", res)
	}
	fi, err := sqlite.Inspect(ctx, l.DBPath())
	if err != nil || fi.DatabaseGeneration != res.DatabaseGeneration {
		t.Fatalf("restored db: %+v %v", fi, err)
	}
	if pre, err := sqlite.Inspect(ctx, res.PreRestoreBackup); err != nil || pre.DatabaseGeneration != gen {
		t.Fatalf("pre-restore backup: %+v %v", pre, err)
	}
	if _, err := os.Stat(l.DBPath() + ".restore-tmp"); err == nil {
		t.Fatal("temporary file left behind")
	}
}

func TestRestoreRefusals(t *testing.T) {
	ctx := context.Background()
	t.Run("daemon lock held", func(t *testing.T) {
		l := layout(t)
		createDB(t, l)
		lock, err := daemon.TryLock(l.LockPath())
		if err != nil {
			t.Fatal(err)
		}
		defer lock.Unlock()
		if _, err := ops.Restore(ctx, l, l.DBPath()); !errors.Is(err, ops.ErrDaemonRunning) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("backup from a newer binary", func(t *testing.T) {
		l := layout(t)
		gen := createDB(t, l)
		other := layout(t)
		createDB(t, other)
		markNewerSchema(t, other.DBPath())
		if _, err := ops.Restore(ctx, l, other.DBPath()); err == nil {
			t.Fatal("newer backup restored")
		}
		if fi, _ := sqlite.Inspect(ctx, l.DBPath()); fi.DatabaseGeneration != gen {
			t.Fatal("current database changed by a refused restore")
		}
	})
	t.Run("not a database", func(t *testing.T) {
		l := layout(t)
		bogus := fakeBinary(t, "not sqlite")
		if _, err := ops.Restore(ctx, l, bogus); err == nil {
			t.Fatal("non-database restored")
		}
	})
}

// ACT-12: the guard binary stays while managed sessions live or hooks use it.
func TestPruneVersionsKeepsGuardBinaries(t *testing.T) {
	l := layout(t)
	for _, v := range []string{"0.1.0", "0.2.0", "0.3.0"} {
		if _, err := ops.Install(l, fakeBinary(t, v), v); err != nil {
			t.Fatal(err)
		}
	}
	root := t.TempDir()
	if err := hooks.Install(root, ops.VersionBinary(l, "0.1.0")); err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(l.WorktreeRoot, "prj_1", "iss_1", "att_1")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := hooks.Install(wt, ops.VersionBinary(l, "0.2.0")); err != nil {
		t.Fatal(err)
	}
	reg := &project.Registry{Projects: []project.RegistryEntry{{ProjectID: "prj_1", Root: root, Activation: "ENABLED"}}, ManagedSessions: []string{"h1"}}
	if err := reg.Save(l.RegistryPath()); err != nil {
		t.Fatal(err)
	}
	res, err := ops.PruneVersions(l, "0.3.0")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Removed) != 0 || !strings.Contains(res.Retained["0.1.0"], "managed session") {
		t.Fatalf("with live sessions: %+v", res)
	}

	reg.ManagedSessions = nil
	if err := reg.Save(l.RegistryPath()); err != nil {
		t.Fatal(err)
	}
	res, err = ops.PruneVersions(l, "0.3.0")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Removed) != 0 || !strings.Contains(res.Retained["0.1.0"], root) || !strings.Contains(res.Retained["0.2.0"], wt) {
		t.Fatalf("hooks in use: %+v", res)
	}

	// Once the worktree is gone its version can go; the project hook keeps 0.1.0.
	if err := os.RemoveAll(filepath.Join(l.WorktreeRoot, "prj_1")); err != nil {
		t.Fatal(err)
	}
	res, err = ops.PruneVersions(l, "0.3.0")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Removed) != 1 || res.Removed[0] != "0.2.0" || res.Retained["0.1.0"] == "" {
		t.Fatalf("after worktree removal: %+v", res)
	}
	if _, err := os.Stat(ops.VersionBinary(l, "0.3.0")); err != nil {
		t.Fatal("current version removed")
	}
}

// An older binary cannot open a migrated database, so hooks that ran an
// installed version must run the new one after an upgrade; hooks of a
// binary outside the install directory are not ours to move.
func TestUpgradeRetargetsInstalledHooks(t *testing.T) {
	l := layout(t)
	createDB(t, l)
	old, err := ops.Install(l, fakeBinary(t, "v1"), "0.1.0")
	if err != nil {
		t.Fatal(err)
	}
	installed, dev := t.TempDir(), t.TempDir()
	devBin := fakeBinary(t, "dev")
	if err := hooks.Install(installed, old.Binary); err != nil {
		t.Fatal(err)
	}
	if err := hooks.Install(dev, devBin); err != nil {
		t.Fatal(err)
	}
	reg := &project.Registry{Projects: []project.RegistryEntry{{ProjectID: "a", Root: installed}, {ProjectID: "b", Root: dev}}}
	if err := reg.Save(l.RegistryPath()); err != nil {
		t.Fatal(err)
	}
	res, err := ops.Upgrade(context.Background(), l, ops.UpgradeOptions{Self: fakeBinary(t, "v2"), Version: "0.2.0"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.HooksRetargeted) != 1 || res.HooksRetargeted[0] != installed {
		t.Fatalf("retargeted = %v", res.HooksRetargeted)
	}
	if got, _ := hooks.CommandPaths(installed); len(got) != 1 || got[0] != ops.VersionBinary(l, "0.2.0") {
		t.Fatalf("installed project hooks = %v", got)
	}
	if got, _ := hooks.CommandPaths(dev); len(got) != 1 || got[0] != devBin {
		t.Fatalf("developer build hooks moved: %v", got)
	}
	if _, err := os.Stat(old.Binary); err != nil {
		t.Fatal("ACT-12: old binary removed by upgrade")
	}
}
