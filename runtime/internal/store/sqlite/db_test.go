package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Mineru98/samsara/runtime/internal/domain"
)

func open(t *testing.T) (*DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "s.db")
	db, err := Open(context.Background(), path, domain.NewFakeClock(time.Unix(1_800_000_000, 0)))
	if err != nil {
		t.Fatal(err)
	}
	return db, path
}

func seedProject(t *testing.T, db *DB) string {
	t.Helper()
	id := domain.NewID("prj")
	err := db.Write(context.Background(), func(tx *Tx) error {
		_, err := tx.ExecContext(context.Background(), `INSERT INTO projects(id,root_path,display_name,activation,created_at,updated_at,last_activity_at) VALUES(?,?,?,?,?,?,?)`,
			id, "/p/"+id, "p", "ENABLED", tx.Now, tx.Now, tx.Now)
		tx.Touch(id, KindProject, id)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestMigrationsAndEngine(t *testing.T) {
	db, path := open(t)
	if CompareVersions(db.EngineVersion, MinSQLiteVersion) < 0 {
		t.Fatalf("engine %s < %s", db.EngineVersion, MinSQLiteVersion)
	}
	gen := db.DatabaseGeneration
	db.Close()
	// DB-02: reopening re-applies nothing and keeps the lineage.
	db2, err := Open(context.Background(), path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	if db2.DatabaseGeneration != gen {
		t.Fatal("database_generation must survive a restart")
	}
	var n int
	_ = db2.w.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&n)
	if n != SchemaVersion {
		t.Fatalf("migrations recorded: %d", n)
	}
}

func TestNewerSchemaRefused(t *testing.T) {
	db, path := open(t)
	_, _ = db.w.Exec("INSERT INTO schema_migrations(version,name,checksum,applied_at) VALUES(999,'future','x',0)")
	db.Close()
	if _, err := Open(context.Background(), path, nil); err == nil || !strings.Contains(err.Error(), "newer") {
		t.Fatalf("an older binary must refuse a newer schema, got %v", err)
	}
}

func TestAtomicWriteRollsBackEverything(t *testing.T) {
	db, _ := open(t)
	pid := seedProject(t, db)
	ctx := context.Background()
	boom := errors.New("boom")
	err := db.Write(ctx, func(tx *Tx) error {
		if _, err := tx.Enqueue(ctx, OutboxItem{ProjectID: pid, Kind: "x", Payload: map[string]int{}, DedupeKey: "k"}); err != nil {
			return err
		}
		if err := tx.Audit(ctx, pid, "t", "a", "s", "id", "", "", nil); err != nil {
			return err
		}
		tx.Touch(pid, KindProject, pid)
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatal(err)
	}
	// DB-05: state, event, audit and outbox are all absent.
	for _, tbl := range []string{"outbox", "operations", "audit_entries"} {
		var n int
		_ = db.r.QueryRow("SELECT COUNT(*) FROM " + tbl).Scan(&n)
		if n != 0 {
			t.Fatalf("%s has %d rows after rollback", tbl, n)
		}
	}
	var ev int
	_ = db.r.QueryRow("SELECT COUNT(*) FROM events WHERE project_id=?", pid).Scan(&ev)
	if ev != 1 { // only the seeding event
		t.Fatalf("events after rollback: %d", ev)
	}
}

func TestOneEventPerTransactionAndVersionChain(t *testing.T) {
	db, _ := open(t)
	pid := seedProject(t, db)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		_ = db.Write(ctx, func(tx *Tx) error {
			tx.Touch(pid, KindProject, pid)
			tx.Touch(pid, KindProject, pid)
			return nil
		})
	}
	rows, _ := db.r.Query("SELECT previous_project_version, project_version FROM events WHERE project_id=? ORDER BY seq", pid)
	defer rows.Close()
	var prev int64 = 0
	n := 0
	for rows.Next() {
		var p, v int64
		_ = rows.Scan(&p, &v)
		if p != prev || v != p+1 {
			t.Fatalf("chain broken: %d -> %d after %d", p, v, prev)
		}
		prev = v
		n++
	}
	if n != 4 {
		t.Fatalf("expected 4 events (1 per tx), got %d", n)
	}
}

func TestConstraintsAndImmutability(t *testing.T) {
	db, _ := open(t)
	pid := seedProject(t, db)
	ctx := context.Background()
	err := db.Write(ctx, func(tx *Tx) error {
		_, err := tx.ExecContext(ctx, "INSERT INTO issues(id,project_id,client_key,title,created_at,updated_at) VALUES('i1','nope','k','t',0,0)")
		return err
	})
	if err == nil {
		t.Fatal("DB-04: foreign key must be enforced")
	}
	// One ACTIVE lease per resource.
	err = db.Write(ctx, func(tx *Tx) error {
		stmts := []string{
			"INSERT INTO runs(id,project_id,graph_version,state,plan_schema_version,plan_digest,plan_snapshot,alias_mapping,created_at) VALUES('r1','" + pid + "',1,'ACTIVE','1','d','{}','{}',0)",
			"INSERT INTO issues(id,project_id,client_key,title,created_at,updated_at) VALUES('i1','" + pid + "','k','t',0,0)",
			"INSERT INTO execution_attempts(id,project_id,run_id,issue_id,run_generation,state,created_at,updated_at) VALUES('a1','" + pid + "','r1','i1',1,'RUNNING',0,0)",
			"INSERT INTO leases(id,project_id,resource_key,owner_attempt_id,fence,state,runtime_epoch,issued_at,renewed_at,deadline_at) VALUES('l1','" + pid + "','w','a1',1,'ACTIVE','e',0,0,1)",
		}
		for _, s := range stmts {
			if _, err := tx.ExecContext(ctx, s); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	err = db.Write(ctx, func(tx *Tx) error {
		_, err := tx.ExecContext(ctx, "INSERT INTO leases(id,project_id,resource_key,owner_attempt_id,fence,state,runtime_epoch,issued_at,renewed_at,deadline_at) VALUES('l2','"+pid+"','w','a1',2,'ACTIVE','e',0,0,1)")
		return err
	})
	if err == nil {
		t.Fatal("INV-02: second ACTIVE lease on one resource accepted")
	}
	// A second live attempt for the same issue in a run is refused.
	err = db.Write(ctx, func(tx *Tx) error {
		_, err := tx.ExecContext(ctx, "INSERT INTO execution_attempts(id,project_id,run_id,issue_id,run_generation,state,created_at,updated_at) VALUES('a2','"+pid+"','r1','i1',1,'STARTING',0,0)")
		return err
	})
	if err == nil {
		t.Fatal("R-02: two live attempts for one issue accepted")
	}
	// Outputs, evidence and audit cannot be rewritten.
	err = db.Write(ctx, func(tx *Tx) error {
		if _, err := tx.ExecContext(ctx, "INSERT INTO output_artifacts(id,project_id,run_id,issue_id,attempt_id,run_generation,source_sha,manifest,manifest_digest,provenance,fence,created_at) VALUES('o1','"+pid+"','r1','i1','a1',1,'s','{}','d','{}',1,0)"); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "UPDATE output_artifacts SET source_sha='other' WHERE id='o1'")
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("output must be immutable, got %v", err)
	}
	_ = db.Write(ctx, func(tx *Tx) error { return tx.Audit(ctx, pid, "a", "b", "c", "d", "", "", nil) })
	err = db.Write(ctx, func(tx *Tx) error {
		_, err := tx.ExecContext(ctx, "DELETE FROM audit_entries")
		return err
	})
	if err == nil {
		t.Fatal("audit must be append-only")
	}
}

func TestOutboxDedupe(t *testing.T) {
	db, _ := open(t)
	pid := seedProject(t, db)
	ctx := context.Background()
	var a, b string
	_ = db.Write(ctx, func(tx *Tx) error {
		a, _ = tx.Enqueue(ctx, OutboxItem{ProjectID: pid, Kind: "k", DedupeKey: "same"})
		b, _ = tx.Enqueue(ctx, OutboxItem{ProjectID: pid, Kind: "k", DedupeKey: "same"})
		return nil
	})
	if a == "" || a != b {
		t.Fatalf("dedupe key must return the same operation: %s %s", a, b)
	}
}

func TestBackupIsConsistent(t *testing.T) {
	db, _ := open(t)
	seedProject(t, db)
	dest := filepath.Join(t.TempDir(), "b.db")
	if err := db.Backup(context.Background(), dest); err != nil {
		t.Fatal(err)
	}
	b, err := Open(context.Background(), dest, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	var n int
	_ = b.r.QueryRow("SELECT COUNT(*) FROM projects").Scan(&n)
	if n != 1 {
		t.Fatalf("backup projects: %d", n)
	}
	if err := db.Backup(context.Background(), dest); err == nil {
		t.Fatal("backup must not overwrite an existing file")
	}
}
