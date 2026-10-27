// Package app holds the application command handlers. Every mutation runs in
// one store transaction (state + event + audit + outbox). External effects
// (git, tmux, herdr, check commands) run only in the outbox dispatcher and
// reconcile loop, outside any transaction (R-06).
package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Mineru98/samsara/runtime/internal/backend"
	"github.com/Mineru98/samsara/runtime/internal/config"
	"github.com/Mineru98/samsara/runtime/internal/domain"
	"github.com/Mineru98/samsara/runtime/internal/gitops"
	"github.com/Mineru98/samsara/runtime/internal/paths"
	"github.com/Mineru98/samsara/runtime/internal/project"
	"github.com/Mineru98/samsara/runtime/internal/store/sqlite"
)

// HostMaxWorkers bounds concurrent workers across all projects.
const HostMaxWorkers = 16

// ClaudeSupport describes whether the installed Claude Code can enforce the
// policy hook fail-closed (onFailure: block, v2.1.295+).
type ClaudeSupport struct {
	Version          string `json:"version"`
	BlockOnFailure   bool   `json:"block_on_failure"`
	FailureInjection bool   `json:"failure_injection_verified"`
}

// App is the runtime application.
type App struct {
	DB       *sqlite.DB
	Clock    domain.Clock
	Layout   paths.Layout
	Backends *backend.Set
	Git      *gitops.Git
	Epoch    string
	SelfExe  string
	Log      *slog.Logger
	Claude   func() ClaudeSupport
	// RequireHookReadiness makes registration verify that the managed
	// policy hook is installed in the worker's worktree. Tests may disable.
	RequireHookReadiness bool

	ready      atomic.Bool
	wake       chan struct{}
	registryMu sync.Mutex
	targetMu   sync.Map // target_id -> *sync.Mutex (in-process serialisation)
	observedAt sync.Map // session_id -> time.Time
}

// New builds the application. Call Recover before serving commands.
func New(db *sqlite.DB, layout paths.Layout, backends *backend.Set, log *slog.Logger) *App {
	if log == nil {
		log = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	exe, _ := os.Executable()
	return &App{DB: db, Clock: db.Clock, Layout: layout, Backends: backends, Git: gitops.New(), Log: log, SelfExe: exe,
		Epoch: domain.NewID("epoch"), wake: make(chan struct{}, 1), RequireHookReadiness: true,
		Claude: func() ClaudeSupport { return ClaudeSupport{} }}
}

// Ready reports whether recovery finished and commands may run.
func (a *App) Ready() bool { return a.ready.Load() }

// Wake nudges the reconcile loop.
func (a *App) Wake() {
	select {
	case a.wake <- struct{}{}:
	default:
	}
}

// WakeChan exposes the wake channel to the loop owner.
func (a *App) WakeChan() <-chan struct{} { return a.wake }

func (a *App) requireReady() error {
	if !a.ready.Load() {
		return domain.Errf(domain.CodeRuntimeNotReady, "runtime recovery in progress")
	}
	return nil
}

// Principal identifies the caller of a command.
type Principal struct {
	ID   string // "user:<uid>", "ui:<session>", "hook", "policy:auto"
	Kind string // cli | ui | hook | system
}

// Cmd carries the common command metadata.
type Cmd struct {
	Principal      Principal
	IdempotencyKey string
	IfMatch        string // expected resource version (decimal string) or ""
}

// idempotent wraps a mutating command so the same key + same request returns
// the stored response and the same key + different request is rejected.
func (a *App) idempotent(ctx context.Context, tx *sqlite.Tx, scope string, cmd Cmd, request any, out any, fn func() (any, error)) error {
	if cmd.IdempotencyKey == "" {
		res, err := fn()
		if err != nil {
			return err
		}
		return remarshal(res, out)
	}
	hash := domain.Digest(request)
	var stored, storedHash string
	err := tx.QueryRowContext(ctx, "SELECT request_hash, response FROM commands WHERE scope=? AND idempotency_key=?", scope, cmd.IdempotencyKey).Scan(&storedHash, &stored)
	if err == nil {
		if storedHash != hash {
			return domain.Errf(domain.CodeIdempotencyKeyReused, "idempotency key was used with a different request")
		}
		return json.Unmarshal([]byte(stored), out)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	res, err := fn()
	if err != nil {
		return err
	}
	b, err := json.Marshal(res)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO commands(id,scope,idempotency_key,principal,request_hash,response,created_at) VALUES(?,?,?,?,?,?,?)",
		domain.NewID("cmd"), scope, cmd.IdempotencyKey, cmd.Principal.ID, hash, string(b), tx.Now); err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

func remarshal(in, out any) error {
	b, err := json.Marshal(in)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

func checkIfMatch(cmd Cmd, current int64) error {
	if cmd.IfMatch == "" {
		return nil
	}
	if cmd.IfMatch != domain.Dec(current) {
		return domain.Errf(domain.CodeVersionMismatch, "resource changed").WithDetails("expected_version", cmd.IfMatch, "current_version", domain.Dec(current))
	}
	return nil
}

// projectConfig decodes the stored project config.
func projectConfig(p *sqlite.Project) *config.Config {
	c := config.Default()
	_ = json.Unmarshal([]byte(p.ConfigJSON), c)
	return c
}

func (a *App) nowMillis() int64 { return domain.Millis(a.Clock.Now()) }

func (a *App) leaseTTL(cfg *config.Config) time.Duration {
	return time.Duration(cfg.Execution.LeaseTTLSeconds) * time.Second
}

// SyncRegistry rewrites the hook registry from the DB. It is called after
// activation, drain or managed-session changes.
func (a *App) SyncRegistry(ctx context.Context) error {
	a.registryMu.Lock()
	defer a.registryMu.Unlock()
	reg := &project.Registry{ManagedRoots: []string{a.Layout.WorktreeRoot}}
	err := a.DB.Read(ctx, func(q sqlite.Q) error {
		ps, err := sqlite.ListProjects(ctx, q)
		if err != nil {
			return err
		}
		for _, p := range ps {
			cfg := projectConfig(p)
			reg.Projects = append(reg.Projects, project.RegistryEntry{ProjectID: p.ID, Root: p.RootPath, Activation: p.Activation,
				DrainActive: p.DrainState == domain.DrainRequested || p.DrainState == domain.DrainDraining || p.DrainState == domain.DrainNeedsAttention,
				Autostart:   cfg.Runtime.Autostart})
		}
		ss, err := sqlite.SessionsWhere(ctx, q, "managed=1 AND ended_at IS NULL")
		if err != nil {
			return err
		}
		for _, s := range ss {
			reg.ManagedSessions = append(reg.ManagedSessions, domain.HashSecret(s.ClaudeSessionID))
		}
		return nil
	})
	if err != nil {
		return err
	}
	return reg.Save(a.Layout.RegistryPath())
}

// RuntimeInfo is returned by GET /v1/runtime and `samsara status`.
type RuntimeInfo struct {
	Version            string                         `json:"version"`
	RuntimeEpoch       string                         `json:"runtime_epoch"`
	DatabaseGeneration string                         `json:"database_generation"`
	HostID             string                         `json:"host_id"`
	SchemaVersion      string                         `json:"schema_version"`
	SQLiteVersion      string                         `json:"sqlite_version"`
	Ready              bool                           `json:"ready"`
	PID                int                            `json:"pid"`
	Claude             ClaudeSupport                  `json:"claude"`
	Backends           map[string]backend.ProbeResult `json:"backends"`
}

// Version is the runtime build version (set by main).
var Version = "dev"

// Info reports runtime status. Backends are probed read-only.
func (a *App) Info(ctx context.Context, probe bool) RuntimeInfo {
	ri := RuntimeInfo{Version: Version, RuntimeEpoch: a.Epoch, DatabaseGeneration: a.DB.DatabaseGeneration, HostID: a.DB.HostID,
		SchemaVersion: domain.Dec(sqlite.SchemaVersion), SQLiteVersion: a.DB.EngineVersion, Ready: a.Ready(), PID: os.Getpid(),
		Claude: a.Claude(), Backends: map[string]backend.ProbeResult{}}
	if probe {
		for _, n := range []string{domain.BackendHerdr, domain.BackendTmux, domain.BackendStandalone} {
			ri.Backends[n] = a.Backends.Probe(ctx, n)
		}
	}
	return ri
}
