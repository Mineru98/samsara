// Package daemon owns the per-OS-user runtime process lifecycle: the
// single-instance lock, recovery before serving commands, the IPC socket,
// the loopback UI server and the reconcile loop (01 §15).
package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/Mineru98/samsara/runtime/internal/app"
	"github.com/Mineru98/samsara/runtime/internal/backend"
	"github.com/Mineru98/samsara/runtime/internal/backend/herdr"
	"github.com/Mineru98/samsara/runtime/internal/backend/standalone"
	"github.com/Mineru98/samsara/runtime/internal/backend/tmux"
	"github.com/Mineru98/samsara/runtime/internal/domain"
	"github.com/Mineru98/samsara/runtime/internal/hooks"
	"github.com/Mineru98/samsara/runtime/internal/paths"
	"github.com/Mineru98/samsara/runtime/internal/store/sqlite"
	"github.com/Mineru98/samsara/runtime/internal/transport/httpapi"
	"github.com/Mineru98/samsara/runtime/internal/transport/ipc"
)

// ErrAlreadyRunning is returned when another daemon holds the lock.
var ErrAlreadyRunning = errors.New("another samsara daemon already holds the lock")

// Lock is an exclusive advisory lock on a file (released on process exit,
// so a crashed daemon never leaves a stale lock; PID files are not used).
type Lock struct{ f *os.File }

// TryLock acquires the lock without blocking.
func TryLock(path string) (*Lock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrAlreadyRunning
		}
		return nil, err
	}
	_ = f.Truncate(0)
	_, _ = f.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0)
	return &Lock{f: f}, nil
}

// Unlock releases the lock.
func (l *Lock) Unlock() {
	if l != nil && l.f != nil {
		_ = syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
		l.f.Close()
	}
}

// Options configure Run.
type Options struct {
	Layout   paths.Layout
	UIPort   int
	Log      io.Writer
	Interval time.Duration
}

// DetectClaude reads the installed Claude Code version. Block-on-failure
// support is inferred from the documented minimum version only; the
// failure-injection verification flag stays false until doctor --exercise
// records it in the compatibility manifest.
func DetectClaude(manifest Manifest) app.ClaudeSupport {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "claude", "--version").Output()
	if err != nil {
		return app.ClaudeSupport{}
	}
	v, ok := hooks.ParseVersion(string(out))
	if !ok {
		return app.ClaudeSupport{Version: string(out)}
	}
	return app.ClaudeSupport{Version: v, BlockOnFailure: hooks.VersionAtLeast(v, hooks.MinBlockVersion),
		FailureInjection: manifest.PolicyHookFailureBlockVerified && manifest.ClaudeCodeVersion == v}
}

// Manifest is the compatibility manifest (01 §23.1).
type Manifest struct {
	VerifiedAt                     string `json:"verified_at"`
	Platform                       string `json:"platform"`
	SamsaraBuild                   string `json:"samsara_build"`
	ClaudeCodeVersion              string `json:"claude_code_version"`
	PolicyHookFailureBlockVerified bool   `json:"policy_hook_failure_block_verified"`
	HerdrCLIVersion                string `json:"herdr_cli_version"`
	HerdrServerVersion             string `json:"herdr_server_version"`
	HerdrSchemaSHA256              string `json:"herdr_schema_sha256"`
	TmuxVersion                    string `json:"tmux_version"`
	GitVersion                     string `json:"git_version"`
	SQLiteVersion                  string `json:"sqlite_version"`
	TestSuiteRevision              string `json:"test_suite_revision"`
	RealBackendSmokePassed         bool   `json:"real_backend_smoke_passed"`
	HerdrProtocolVerified          bool   `json:"herdr_protocol_verified"`
}

// ManifestPath is where the compatibility manifest is stored.
func ManifestPath(l paths.Layout) string { return filepath.Join(l.StateDir, "compatibility.json") }

// LoadManifest reads the manifest (zero value if absent).
func LoadManifest(l paths.Layout) Manifest {
	var m Manifest
	if b, err := os.ReadFile(ManifestPath(l)); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	return m
}

// Run starts the daemon in the foreground until a signal or stop request.
func Run(opts Options) error {
	l := opts.Layout
	if err := l.Ensure(); err != nil {
		return err
	}
	lock, err := TryLock(l.LockPath())
	if err != nil {
		return err
	}
	defer lock.Unlock()
	if opts.Log == nil {
		opts.Log = os.Stderr
	}
	if opts.Interval == 0 {
		opts.Interval = 2 * time.Second
	}
	log := slog.New(slog.NewJSONHandler(opts.Log, nil))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	db, err := sqlite.Open(ctx, l.DBPath(), domain.SystemClock{})
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()
	manifest := LoadManifest(l)
	clock := domain.SystemClock{}
	verified := ""
	if manifest.HerdrProtocolVerified {
		verified = manifest.HerdrSchemaSHA256
	}
	set := backend.NewSet(clock, herdr.New(clock, verified), tmux.New(clock), &standalone.Adapter{Clock: clock})
	a := app.New(db, l, set, log)
	var claudeOnce sync.Once
	var claude app.ClaudeSupport
	a.Claude = func() app.ClaudeSupport {
		claudeOnce.Do(func() { claude = DetectClaude(manifest) })
		return claude
	}
	if err := a.Recover(ctx); err != nil {
		return fmt.Errorf("recovery: %w", err)
	}
	ipcSrv := ipc.NewServer(a)
	httpSrv := httpapi.New(a)
	stopOnce := sync.Once{}
	stop := func() { stopOnce.Do(cancel) }
	ipcSrv.SetHooks(httpSrv.NewBootstrapCode, stop)

	ul, err := httpSrv.Listen(opts.UIPort)
	if err != nil {
		return fmt.Errorf("ui listen: %w", err)
	}
	info, _ := json.Marshal(map[string]any{"port": httpSrv.Port, "pid": os.Getpid(), "epoch": a.Epoch})
	_ = os.WriteFile(l.UIInfoPath(), info, 0o600)
	il, err := ipc.Listen(l.SocketPath())
	if err != nil {
		return fmt.Errorf("ipc listen: %w", err)
	}
	hs1 := &http.Server{Handler: ipcSrv, ReadHeaderTimeout: 5 * time.Second}
	hs2 := &http.Server{Handler: httpSrv, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = hs1.Serve(il) }()
	go func() { _ = hs2.Serve(ul) }()
	log.Info("daemon started", "epoch", a.Epoch, "socket", l.SocketPath(), "ui_port", httpSrv.Port, "sqlite", db.EngineVersion)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		select {
		case <-sig:
			stop()
		case <-ctx.Done():
		}
	}()
	a.Loop(ctx, opts.Interval)

	// Graceful stop: no new assignments; DB transactions finish. Managed
	// workers keep running but their next policy check fails closed because
	// the daemon is gone (01 §15.2).
	sctx, scancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer scancel()
	_ = hs1.Shutdown(sctx)
	_ = hs2.Shutdown(sctx)
	_ = os.Remove(l.SocketPath())
	_ = os.Remove(l.UIInfoPath())
	_ = db.Write(sctx, func(tx *sqlite.Tx) error {
		_, err := tx.ExecContext(sctx, "UPDATE runtime_epochs SET stopped_at=? WHERE epoch=?", tx.Now, a.Epoch)
		return err
	})
	log.Info("daemon stopped", "epoch", a.Epoch)
	return nil
}

// Ping checks whether a daemon answers on the socket.
func Ping(ctx context.Context, l paths.Layout) bool {
	c := ipc.NewClient(l.SocketPath())
	ctx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	var info app.RuntimeInfo
	return c.Call(ctx, "/ipc/v1/runtime", map[string]any{}, &info) == nil
}

// EnsureRunning starts a detached daemon if none answers, serialising
// concurrent starters (ACT-05) and waiting bounded for readiness.
func EnsureRunning(ctx context.Context, l paths.Layout, exe string) error {
	if Ping(ctx, l) {
		return nil
	}
	if err := l.Ensure(); err != nil {
		return err
	}
	starter, err := TryLock(l.StartLockPath())
	if err == nil {
		defer starter.Unlock()
		if !Ping(ctx, l) {
			if err := spawn(l, exe); err != nil {
				return err
			}
		}
	}
	deadline := time.Now().Add(8 * time.Second)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	for time.Now().Before(deadline) {
		if Ping(ctx, l) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("daemon did not become ready (see %s)", l.LogPath())
}

func spawn(l paths.Layout, exe string) error {
	logf, err := os.OpenFile(l.LogPath(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()
	cmd := exec.Command(exe, "daemon", "run")
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Env = os.Environ()
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
