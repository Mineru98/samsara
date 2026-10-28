// Package sqlite is the durable store. The daemon is the only writer (R-13);
// every domain mutation runs through DB.Write so that state, project_version,
// event, audit and outbox rows commit atomically (R-07).
package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/Mineru98/samsara/runtime/internal/domain"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// MinSQLiteVersion is the verified engine baseline (01 §14, WAL-reset fix).
const MinSQLiteVersion = "3.51.3"

// SchemaVersion is the highest migration this binary knows.
const SchemaVersion = 1

// Q is satisfied by *sql.Tx and *sql.DB.
type Q interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Notifier is told which projects (and whether the catalog) changed after a
// commit. It is only a wake-up hint for SSE; streams always re-read the DB.
type Notifier func(projectIDs []string, catalog bool)

// DB wraps one writer connection and a read pool.
type DB struct {
	path   string
	w      *sql.DB
	r      *sql.DB
	wmu    sync.Mutex
	Clock  domain.Clock
	notify Notifier

	HostID             string
	DatabaseGeneration string
	EngineVersion      string
}

// Open opens (creating if needed) the database, verifies the engine version
// and applies migrations. It never recreates or downgrades an existing DB.
func Open(ctx context.Context, path string, clock domain.Clock) (*DB, error) {
	if clock == nil {
		clock = domain.SystemClock{}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	pragmas := []string{"foreign_keys(1)", "busy_timeout(5000)", "journal_mode(WAL)", "synchronous(FULL)"}
	q := url.Values{}
	for _, p := range pragmas {
		q.Add("_pragma", p)
	}
	q.Set("_txlock", "immediate")
	dsn := "file:" + path + "?" + q.Encode()
	w, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	w.SetMaxOpenConns(1)
	rq := url.Values{}
	for _, p := range []string{"foreign_keys(1)", "busy_timeout(5000)", "query_only(1)"} {
		rq.Add("_pragma", p)
	}
	r, err := sql.Open("sqlite", "file:"+path+"?"+rq.Encode())
	if err != nil {
		w.Close()
		return nil, err
	}
	r.SetMaxOpenConns(8)
	db := &DB{path: path, w: w, r: r, Clock: clock}
	if err := db.w.QueryRowContext(ctx, "SELECT sqlite_version()").Scan(&db.EngineVersion); err != nil {
		db.Close()
		return nil, fmt.Errorf("sqlite: %w", err)
	}
	if CompareVersions(db.EngineVersion, MinSQLiteVersion) < 0 {
		db.Close()
		return nil, fmt.Errorf("sqlite engine %s is older than required %s", db.EngineVersion, MinSQLiteVersion)
	}
	if err := db.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	if err := db.loadMetadata(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// Close releases both pools.
func (db *DB) Close() error {
	err1 := db.w.Close()
	err2 := db.r.Close()
	return errors.Join(err1, err2)
}

// Path returns the database file path.
func (db *DB) Path() string { return db.path }

// SetNotifier installs the post-commit wake-up hook.
func (db *DB) SetNotifier(n Notifier) { db.notify = n }

type migration struct {
	version  int
	name     string
	sql      string
	checksum string
}

func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return nil, err
	}
	var out []migration
	for _, e := range entries {
		name := e.Name()
		v, err := strconv.Atoi(strings.SplitN(name, "_", 2)[0])
		if err != nil {
			return nil, fmt.Errorf("bad migration name %q", name)
		}
		b, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(b)
		out = append(out, migration{version: v, name: name, sql: string(b), checksum: hex.EncodeToString(sum[:])})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	return out, nil
}

func (db *DB) migrate(ctx context.Context) error {
	if _, err := db.w.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY, name TEXT NOT NULL, checksum TEXT NOT NULL, applied_at INTEGER NOT NULL)`); err != nil {
		return err
	}
	applied := map[int]string{}
	rows, err := db.w.QueryContext(ctx, "SELECT version, checksum FROM schema_migrations")
	if err != nil {
		return err
	}
	maxApplied := 0
	for rows.Next() {
		var v int
		var c string
		if err := rows.Scan(&v, &c); err != nil {
			rows.Close()
			return err
		}
		applied[v] = c
		if v > maxApplied {
			maxApplied = v
		}
	}
	rows.Close()
	if maxApplied > SchemaVersion {
		return fmt.Errorf("database schema %d is newer than this binary (%d); refusing to write", maxApplied, SchemaVersion)
	}
	migs, err := loadMigrations()
	if err != nil {
		return err
	}
	for _, m := range migs {
		if c, ok := applied[m.version]; ok {
			if c != m.checksum {
				return fmt.Errorf("migration %s checksum mismatch (db %s, binary %s)", m.name, c, m.checksum)
			}
			continue
		}
		tx, err := db.w.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, m.sql); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %s: %w", m.name, err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version,name,checksum,applied_at) VALUES(?,?,?,?)",
			m.version, m.name, m.checksum, domain.Millis(db.Clock.Now())); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func (db *DB) loadMetadata(ctx context.Context) error {
	err := db.w.QueryRowContext(ctx, "SELECT host_id, database_generation FROM runtime_metadata WHERE id=1").
		Scan(&db.HostID, &db.DatabaseGeneration)
	if errors.Is(err, sql.ErrNoRows) {
		db.HostID = domain.NewID("host")
		db.DatabaseGeneration = domain.NewID("dbgen")
		_, err = db.w.ExecContext(ctx, "INSERT INTO runtime_metadata(id,host_id,database_generation,created_at) VALUES(1,?,?,?)",
			db.HostID, db.DatabaseGeneration, domain.Millis(db.Clock.Now()))
	}
	return err
}

// RotateDatabaseGeneration marks the event lineage as new, e.g. after a
// restore, so that every old UI cursor becomes reset_required (12.3).
func (db *DB) RotateDatabaseGeneration(ctx context.Context) error {
	gen := domain.NewID("dbgen")
	if _, err := db.w.ExecContext(ctx, "UPDATE runtime_metadata SET database_generation=? WHERE id=1", gen); err != nil {
		return err
	}
	db.DatabaseGeneration = gen
	return nil
}

// Read runs fn inside one read transaction so all reads share a snapshot.
func (db *DB) Read(ctx context.Context, fn func(q Q) error) error {
	tx, err := db.r.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return storageErr(err)
	}
	defer tx.Rollback()
	return fn(tx)
}

// Write runs fn in the single writer transaction and flushes the accumulated
// project events before commit.
func (db *DB) Write(ctx context.Context, fn func(tx *Tx) error) error {
	db.wmu.Lock()
	defer db.wmu.Unlock()
	sqlTx, err := db.w.BeginTx(ctx, nil)
	if err != nil {
		return storageErr(err)
	}
	tx := newTx(db, sqlTx)
	if err := fn(tx); err != nil {
		sqlTx.Rollback()
		return err
	}
	projects, catalog, err := tx.flush(ctx)
	if err != nil {
		sqlTx.Rollback()
		return err
	}
	if err := sqlTx.Commit(); err != nil {
		return storageErr(err)
	}
	if db.notify != nil && (len(projects) > 0 || catalog) {
		db.notify(projects, catalog)
	}
	return nil
}

// Backup writes a consistent online copy using VACUUM INTO.
func (db *DB) Backup(ctx context.Context, dest string) error {
	db.wmu.Lock()
	defer db.wmu.Unlock()
	if _, err := os.Stat(dest); err == nil {
		return fmt.Errorf("backup destination %s already exists", dest)
	}
	_, err := db.w.ExecContext(ctx, "VACUUM INTO ?", dest)
	return err
}

func storageErr(err error) error {
	if err == nil {
		return nil
	}
	var de *domain.Error
	if errors.As(err, &de) {
		return err
	}
	return &domain.Error{Code: domain.CodeStorageUnavailable, Message: err.Error(), Retryable: true}
}

// CompareVersions compares dotted numeric versions.
func CompareVersions(a, b string) int {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}
