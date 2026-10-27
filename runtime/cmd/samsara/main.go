// Command samsara is the CLI, daemon, hook bridge and worker launcher of the
// Samsara runtime (01 §19).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Mineru98/samsara/runtime/internal/app"
	"github.com/Mineru98/samsara/runtime/internal/backend/herdr"
	"github.com/Mineru98/samsara/runtime/internal/backend/tmux"
	"github.com/Mineru98/samsara/runtime/internal/config"
	"github.com/Mineru98/samsara/runtime/internal/daemon"
	"github.com/Mineru98/samsara/runtime/internal/domain"
	"github.com/Mineru98/samsara/runtime/internal/gitops"
	"github.com/Mineru98/samsara/runtime/internal/hooks"
	"github.com/Mineru98/samsara/runtime/internal/hooks/bridge"
	"github.com/Mineru98/samsara/runtime/internal/paths"
	"github.com/Mineru98/samsara/runtime/internal/plan"
	"github.com/Mineru98/samsara/runtime/internal/procinfo"
	"github.com/Mineru98/samsara/runtime/internal/project"
	"github.com/Mineru98/samsara/runtime/internal/store/sqlite"
	"github.com/Mineru98/samsara/runtime/internal/transport/ipc"
)

// version is set with -ldflags "-X main.version=...".
var version = "0.1.0-dev"

const usage = `samsara — local orchestration runtime for Claude Code work

Usage: samsara [--json] <command> [args]

Project:   init | enable | disable | status | project list|relink|archive
Plans:     plan validate|apply --file plan.json | profile add
Runs:      run start|pause|resume|cancel <run_id> | issue show|retry <issue_id>
Sessions:  session attach --issue <id> | session inspect|release-input|focus <id>
Approvals: approval list | approval decide <id> --approve|--reject|--revoke
Work:      output submit | worktree list|inspect | merge queue|inspect|enqueue|cancel
History:   history | link add
Runtime:   daemon start|stop|run|status | doctor [--exercise] [--write-manifest] | ui | backup --dest f | version
Internal:  hook <event> | launcher -- <argv...>
`

type cli struct {
	layout paths.Layout
	json   bool
	out    io.Writer
	errw   io.Writer
	exe    string
}

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	app.Version = version
	layout, err := paths.Resolve()
	if err != nil {
		fmt.Fprintln(os.Stderr, "samsara:", err)
		return domain.ExitError
	}
	exe, _ := os.Executable()
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	c := &cli{layout: layout, out: os.Stdout, errw: os.Stderr, exe: exe}
	for len(args) > 0 && strings.HasPrefix(args[0], "--") {
		switch args[0] {
		case "--json":
			c.json = true
		case "--help":
			fmt.Fprint(c.out, usage)
			return 0
		default:
			fmt.Fprintln(c.errw, "unknown flag", args[0])
			return domain.ExitInput
		}
		args = args[1:]
	}
	if len(args) == 0 {
		fmt.Fprint(c.errw, usage)
		return domain.ExitInput
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "hook":
		return c.hook(rest)
	case "launcher":
		return c.launcher(rest)
	case "version":
		return c.print(map[string]string{"version": version, "go": runtime.Version(), "os": runtime.GOOS + "/" + runtime.GOARCH})
	case "daemon":
		return c.daemon(rest)
	case "init":
		return c.initCmd(rest, true)
	case "enable":
		return c.initCmd(rest, false)
	case "disable":
		return c.disable(rest)
	case "status":
		return c.status(rest)
	case "doctor":
		return c.doctor(rest)
	case "project":
		return c.projectCmd(rest)
	case "profile":
		return c.profile(rest)
	case "plan":
		return c.planCmd(rest)
	case "run":
		return c.runCmd(rest)
	case "issue":
		return c.issueCmd(rest)
	case "session":
		return c.sessionCmd(rest)
	case "approval":
		return c.approvalCmd(rest)
	case "output":
		return c.outputCmd(rest)
	case "worktree":
		return c.worktreeCmd(rest)
	case "merge":
		return c.mergeCmd(rest)
	case "history":
		return c.history(rest)
	case "link":
		return c.link(rest)
	case "ui":
		return c.ui(rest)
	case "backup":
		return c.backup(rest)
	}
	fmt.Fprintf(c.errw, "unknown command %q\n\n%s", cmd, usage)
	return domain.ExitInput
}

// --- output helpers -------------------------------------------------------

func (c *cli) print(v any) int {
	b, _ := json.MarshalIndent(v, "", "  ")
	fmt.Fprintln(c.out, string(b))
	return domain.ExitOK
}

func (c *cli) fail(err error) int {
	de := domain.AsError(err)
	if c.json {
		b, _ := json.MarshalIndent(map[string]any{"error": de}, "", "  ")
		fmt.Fprintln(c.out, string(b))
	} else {
		fmt.Fprintf(c.errw, "samsara: %s: %s\n", de.Code, de.Message)
		if len(de.Details) > 0 {
			b, _ := json.MarshalIndent(de.Details, "", "  ")
			fmt.Fprintln(c.errw, string(b))
		}
	}
	return domain.ExitCode(de.Code)
}

func (c *cli) client() *ipc.Client { return ipc.NewClient(c.layout.SocketPath()) }

// call ensures the daemon is running (explicit user commands may start it)
// and performs one IPC call.
func (c *cli) call(path string, req, resp any, opts ...ipc.CallOpts) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := daemon.EnsureRunning(ctx, c.layout, c.exe); err != nil {
		return &domain.Error{Code: domain.ReasonRuntimeUnavailable, Message: err.Error()}
	}
	o := ipc.CallOpts{}
	if len(opts) > 0 {
		o = opts[0]
	}
	return c.client().CallWith(ctx, path, req, resp, o)
}

func (c *cli) callPrint(path string, req any, opts ...ipc.CallOpts) int {
	var resp json.RawMessage
	err := c.call(path, req, &resp, opts...)
	if err != nil {
		if len(resp) > 0 && !c.json {
			fmt.Fprintln(c.out, string(resp))
		}
		return c.fail(err)
	}
	var v any
	_ = json.Unmarshal(resp, &v)
	return c.print(v)
}

func newFlags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	return fs
}

// projectID resolves --project or the project containing the cwd.
func (c *cli) projectID(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	cwd, _ := os.Getwd()
	cwd, _ = project.Canonical(cwd)
	reg, err := project.LoadRegistry(c.layout.RegistryPath())
	if err != nil {
		return "", err
	}
	sc := reg.Classify(cwd, "")
	if sc.Entry == nil {
		return "", domain.Errf(domain.CodeInvalidRequest, "no registered project contains %s; pass --project or run samsara init", cwd)
	}
	return sc.Entry.ProjectID, nil
}

// --- hook / launcher ------------------------------------------------------

func (c *cli) hook(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(c.errw, "usage: samsara hook <event>")
		return 0 // never break Claude for a usage error on unmanaged sessions
	}
	env := bridge.Env{
		RegistryPath: c.layout.RegistryPath(),
		Ticket:       os.Getenv("SAMSARA_LAUNCH_TICKET"),
		ManagedEnv:   os.Getenv("SAMSARA_MANAGED") == "1",
		Caller:       c.client(),
		ParentPID:    bridge.ParentPID(),
		EnsureDaemon: func(ctx context.Context) error { return daemon.EnsureRunning(ctx, c.layout, c.exe) },
	}
	return bridge.Run(args[0], os.Stdin, os.Stdout, os.Stderr, env)
}

// launcher runs the worker command as a child, renews the lease while the
// child lives (heartbeat independent of hook traffic, 01 §11.2) and reports
// the child's exit as process-exit evidence.
func (c *cli) launcher(args []string) int {
	if len(args) > 0 && args[0] == "--" {
		args = args[1:]
	}
	if len(args) == 0 {
		fmt.Fprintln(c.errw, "usage: samsara launcher -- <argv...>")
		return domain.ExitInput
	}
	ticket := os.Getenv("SAMSARA_LAUNCH_TICKET")
	attempt := os.Getenv("SAMSARA_ATTEMPT_ID")
	hb, _ := strconv.Atoi(os.Getenv("SAMSARA_HEARTBEAT_SECONDS"))
	if hb <= 0 {
		hb = 5
	}
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.Env = os.Environ()
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(c.errw, "samsara launcher:", err)
		c.reportExit(ticket, 127)
		return 127
	}
	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		for s := range sigs {
			_ = cmd.Process.Signal(s)
		}
	}()
	stop := make(chan struct{})
	go func() {
		t := time.NewTicker(time.Duration(hb) * time.Second)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				var res app.RenewResult
				_ = c.client().Call(ctx, "/ipc/v1/leases/renew", app.RenewRequest{Ticket: ticket, AttemptID: attempt}, &res)
				cancel()
			}
		}
	}()
	err := cmd.Wait()
	close(stop)
	code := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code = ee.ExitCode()
	} else if err != nil {
		code = 1
	}
	c.reportExit(ticket, code)
	return code
}

func (c *cli) reportExit(ticket string, code int) {
	if ticket == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = c.client().Call(ctx, "/ipc/v1/sessions/finish", app.FinishRequest{ProcessExit: true, ExitCode: code, LauncherAuth: ticket,
		HookIdentity: app.HookIdentity{ProtocolVersion: "1", Event: "LauncherExit"}}, nil)
}

// --- daemon ---------------------------------------------------------------

func (c *cli) daemon(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(c.errw, "usage: samsara daemon start|stop|run|status")
		return domain.ExitInput
	}
	switch args[0] {
	case "run":
		fs := newFlags("daemon run")
		port := fs.Int("ui-port", 0, "loopback UI port (0 = ephemeral)")
		if err := fs.Parse(args[1:]); err != nil {
			return domain.ExitInput
		}
		if p := os.Getenv("SAMSARA_UI_PORT"); p != "" && *port == 0 {
			*port, _ = strconv.Atoi(p)
		}
		if err := daemon.Run(daemon.Options{Layout: c.layout, UIPort: *port, Log: os.Stderr}); err != nil {
			if errors.Is(err, daemon.ErrAlreadyRunning) {
				fmt.Fprintln(c.errw, "samsara: daemon already running")
				return domain.ExitOK
			}
			fmt.Fprintln(c.errw, "samsara daemon:", err)
			return domain.ExitError
		}
		return domain.ExitOK
	case "start":
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := daemon.EnsureRunning(ctx, c.layout, c.exe); err != nil {
			return c.fail(&domain.Error{Code: domain.ReasonRuntimeUnavailable, Message: err.Error()})
		}
		return c.statusDaemon()
	case "stop":
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if !daemon.Ping(ctx, c.layout) {
			return c.print(map[string]string{"status": "NOT_RUNNING"})
		}
		if err := c.client().Call(ctx, "/ipc/v1/daemon/stop", map[string]any{}, nil); err != nil {
			return c.fail(err)
		}
		for i := 0; i < 50 && daemon.Ping(ctx, c.layout); i++ {
			time.Sleep(100 * time.Millisecond)
		}
		return c.print(map[string]string{"status": "STOPPED", "note": "managed workers keep running; their next tool call is blocked until the daemon returns"})
	case "status":
		return c.statusDaemon()
	}
	fmt.Fprintln(c.errw, "usage: samsara daemon start|stop|run|status")
	return domain.ExitInput
}

func (c *cli) statusDaemon() int {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var info app.RuntimeInfo
	if err := c.client().Call(ctx, "/ipc/v1/runtime", map[string]any{}, &info); err != nil {
		c.print(map[string]any{"daemon": "OFFLINE", "socket": c.layout.SocketPath()})
		return domain.ExitConnection
	}
	return c.print(map[string]any{"daemon": "HEALTHY", "runtime": info})
}

// --- project --------------------------------------------------------------

func (c *cli) initCmd(args []string, write bool) int {
	fs := newFlags("init")
	path := fs.String("path", ".", "project directory")
	noHooks := fs.Bool("no-hooks", false, "do not install Claude Code hook entries")
	if err := fs.Parse(args); err != nil {
		return domain.ExitInput
	}
	id, err := project.Resolve(context.Background(), *path)
	if err != nil {
		return c.fail(domain.Errf(domain.CodeInvalidRequest, "%v", err))
	}
	cfgPath := filepath.Join(id.Root, config.RelPath)
	created := false
	if _, err := os.Stat(cfgPath); errors.Is(err, os.ErrNotExist) {
		if !write {
			return c.fail(domain.Errf(domain.CodeInvalidRequest, "%s missing; run samsara init", cfgPath))
		}
		b, _ := config.Marshal(config.Default())
		if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
			return c.fail(err)
		}
		if err := os.WriteFile(cfgPath, b, 0o644); err != nil {
			return c.fail(err)
		}
		created = true
	} else if _, _, err := config.Load(id.Root); err != nil {
		return c.fail(err)
	}
	if !*noHooks {
		if err := hooks.Install(id.Root, c.exe); err != nil {
			return c.fail(err)
		}
	}
	var res app.RegisterProjectResult
	if err := c.call("/ipc/v1/projects/register", app.RegisterProjectRequest{Path: id.Root}, &res); err != nil {
		return c.fail(err)
	}
	return c.print(map[string]any{"project": res, "config_created": created, "config": cfgPath,
		"hooks_installed": !*noHooks, "hooks_file": filepath.Join(id.Root, hooks.SettingsRel)})
}

func (c *cli) disable(args []string) int {
	fs := newFlags("disable")
	pid := fs.String("project", "", "project id")
	if err := fs.Parse(args); err != nil {
		return domain.ExitInput
	}
	id, err := c.projectID(*pid)
	if err != nil {
		return c.fail(err)
	}
	return c.callPrint("/ipc/v1/projects/disable", map[string]string{"project_id": id}, ipc.CallOpts{IdempotencyKey: domain.NewID("cli")})
}

func (c *cli) status(args []string) int {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var info app.RuntimeInfo
	if err := c.client().Call(ctx, "/ipc/v1/runtime", map[string]any{}, &info); err != nil {
		reg, _ := project.LoadRegistry(c.layout.RegistryPath())
		c.print(map[string]any{"daemon": "OFFLINE", "registered_projects": reg.Projects})
		return domain.ExitConnection
	}
	var projects json.RawMessage
	_ = c.client().Call(ctx, "/ipc/v1/projects/list", map[string]any{}, &projects)
	var ps any
	_ = json.Unmarshal(projects, &ps)
	return c.print(map[string]any{"daemon": "HEALTHY", "runtime": info, "projects": ps})
}

func (c *cli) projectCmd(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(c.errw, "usage: samsara project list|relink|archive|snapshot")
		return domain.ExitInput
	}
	fs := newFlags("project")
	pid := fs.String("project", "", "project id")
	path := fs.String("path", "", "new path (relink)")
	all := fs.Bool("all", false, "include archived")
	if err := fs.Parse(args[1:]); err != nil {
		return domain.ExitInput
	}
	switch args[0] {
	case "list":
		return c.callPrint("/ipc/v1/projects/list", map[string]bool{"include_archived": *all})
	case "relink":
		return c.callPrint("/ipc/v1/projects/relink", map[string]string{"project_id": *pid, "path": *path})
	case "archive":
		id, err := c.projectID(*pid)
		if err != nil {
			return c.fail(err)
		}
		return c.callPrint("/ipc/v1/projects/archive", map[string]string{"project_id": id})
	case "snapshot":
		id, err := c.projectID(*pid)
		if err != nil {
			return c.fail(err)
		}
		return c.callPrint("/ipc/v1/projects/snapshot", map[string]string{"project_id": id})
	}
	return domain.ExitInput
}

func (c *cli) profile(args []string) int {
	if len(args) == 0 || args[0] != "add" {
		fmt.Fprintln(c.errw, "usage: samsara profile add --name <n> [--timeout 600] -- <argv...>")
		return domain.ExitInput
	}
	fs := newFlags("profile add")
	pid := fs.String("project", "", "project id")
	name := fs.String("name", "", "profile name")
	timeout := fs.Int64("timeout", 600, "seconds")
	if err := fs.Parse(args[1:]); err != nil {
		return domain.ExitInput
	}
	id, err := c.projectID(*pid)
	if err != nil {
		return c.fail(err)
	}
	return c.callPrint("/ipc/v1/profiles/register", app.CheckProfileRequest{ProjectID: id, Name: *name, Argv: fs.Args(), TimeoutSec: *timeout})
}

// --- plan / run / issue ---------------------------------------------------

func (c *cli) planCmd(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(c.errw, "usage: samsara plan validate|apply --file plan.json")
		return domain.ExitInput
	}
	fs := newFlags("plan")
	file := fs.String("file", "", "plan JSON file ('-' for stdin)")
	key := fs.String("idempotency-key", "", "idempotency key (apply)")
	if err := fs.Parse(args[1:]); err != nil {
		return domain.ExitInput
	}
	var raw []byte
	var err error
	if *file == "-" {
		raw, err = io.ReadAll(io.LimitReader(os.Stdin, ipc.MaxBody))
	} else {
		raw, err = os.ReadFile(*file)
	}
	if err != nil {
		return c.fail(domain.Errf(domain.CodeInvalidRequest, "%v", err))
	}
	switch args[0] {
	case "validate":
		// Without a daemon only the offline schema check is possible; say so.
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if !daemon.Ping(ctx, c.layout) {
			p, err := plan.Parse(raw)
			if err != nil {
				return c.fail(err)
			}
			p.Normalize("refs/heads/main")
			ps := p.Validate()
			c.print(app.PlanValidation{Valid: len(ps) == 0, PlanDigest: p.Digest(), ProjectValidated: false, Problems: append([]plan.Problem{}, ps...)})
			if len(ps) > 0 {
				return domain.ExitInput
			}
			return domain.ExitOK
		}
		var res app.PlanValidation
		if err := c.client().Call(ctx, "/ipc/v1/plans/validate", json.RawMessage(raw), &res); err != nil {
			return c.fail(err)
		}
		c.print(res)
		if !res.Valid {
			return domain.ExitInput
		}
		return domain.ExitOK
	case "apply":
		k := *key
		if k == "" {
			k = "plan:" + domain.DigestBytes(raw)
		}
		return c.callPrint("/ipc/v1/plans/apply", json.RawMessage(raw), ipc.CallOpts{IdempotencyKey: k})
	}
	return domain.ExitInput
}

func (c *cli) runCmd(args []string) int {
	if len(args) < 2 {
		fmt.Fprintln(c.errw, "usage: samsara run start|pause|resume|cancel <run_id>")
		return domain.ExitInput
	}
	switch args[0] {
	case "start", "pause", "resume", "cancel":
		opts := ipc.CallOpts{}
		if args[0] != "start" {
			opts.IdempotencyKey = domain.NewID("cli")
		}
		return c.callPrint("/ipc/v1/runs/"+args[0], map[string]string{"run_id": args[1]}, opts)
	}
	return domain.ExitInput
}

func (c *cli) issueCmd(args []string) int {
	if len(args) < 2 {
		fmt.Fprintln(c.errw, "usage: samsara issue show|retry <issue_id>")
		return domain.ExitInput
	}
	switch args[0] {
	case "show":
		return c.callPrint("/ipc/v1/issues/get", map[string]string{"issue_id": args[1]})
	case "retry":
		return c.callPrint("/ipc/v1/issues/retry", map[string]string{"issue_id": args[1]})
	}
	return domain.ExitInput
}

// --- sessions / outputs ---------------------------------------------------

func ancestors() []int { return procinfo.Ancestors(os.Getpid()) }

func (c *cli) sessionCmd(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(c.errw, "usage: samsara session attach --issue <id> | inspect|release-input|focus <session_id>")
		return domain.ExitInput
	}
	switch args[0] {
	case "attach":
		fs := newFlags("session attach")
		issue := fs.String("issue", "", "issue id")
		sid := fs.String("claude-session-id", "", "Claude session id (default: the Claude process running this command)")
		if err := fs.Parse(args[1:]); err != nil {
			return domain.ExitInput
		}
		return c.callPrint("/ipc/v1/sessions/attach", app.AttachRequest{IssueID: *issue, ClaudeSessionID: *sid, AncestorPIDs: ancestors()})
	case "inspect":
		if len(args) < 2 {
			return domain.ExitInput
		}
		return c.callPrint("/ipc/v1/lookup", map[string]string{"kind": sqlite.KindSession, "id": args[1]})
	case "focus":
		if len(args) < 2 {
			return domain.ExitInput
		}
		return c.callPrint("/ipc/v1/sessions/focus", map[string]string{"session_id": args[1]})
	case "release-input":
		if len(args) < 2 {
			return domain.ExitInput
		}
		return c.callPrint("/ipc/v1/sessions/release-input", map[string]string{"session_id": args[1]}, ipc.CallOpts{IdempotencyKey: domain.NewID("cli")})
	}
	return domain.ExitInput
}

func (c *cli) outputCmd(args []string) int {
	if len(args) == 0 || args[0] != "submit" {
		fmt.Fprintln(c.errw, "usage: samsara output submit [--summary s] [--fence f] [--attempt id] [--pr url]")
		return domain.ExitInput
	}
	fs := newFlags("output submit")
	summary := fs.String("summary", "", "summary")
	fence := fs.String("fence", "", "expected fence")
	attempt := fs.String("attempt", "", "attempt id")
	pr := fs.String("pr", "", "pull request URL")
	sid := fs.String("claude-session-id", "", "Claude session id")
	if err := fs.Parse(args[1:]); err != nil {
		return domain.ExitInput
	}
	return c.callPrint("/ipc/v1/outputs/submit", app.OutputSubmitRequest{Ticket: os.Getenv("SAMSARA_LAUNCH_TICKET"), ClaudeSessionID: *sid,
		AncestorPIDs: ancestors(), AttemptID: *attempt, Fence: *fence, Summary: *summary, PullRequest: *pr})
}

// --- approvals / worktrees / merge ----------------------------------------

func (c *cli) approvalCmd(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(c.errw, "usage: samsara approval list | decide <id> --approve|--reject|--revoke")
		return domain.ExitInput
	}
	switch args[0] {
	case "list":
		fs := newFlags("approval list")
		pid := fs.String("project", "", "project id")
		if err := fs.Parse(args[1:]); err != nil {
			return domain.ExitInput
		}
		id, err := c.projectID(*pid)
		if err != nil {
			return c.fail(err)
		}
		return c.callPrint("/ipc/v1/approvals/list", map[string]string{"project_id": id})
	case "decide":
		if len(args) < 2 {
			return domain.ExitInput
		}
		fs := newFlags("approval decide")
		approve := fs.Bool("approve", false, "approve")
		reject := fs.Bool("reject", false, "reject")
		revoke := fs.Bool("revoke", false, "revoke an approved decision")
		digest := fs.String("digest", "", "expected subject digest")
		comment := fs.String("comment", "", "comment")
		ifMatch := fs.String("if-match", "", "expected approval version")
		if err := fs.Parse(args[2:]); err != nil {
			return domain.ExitInput
		}
		decision := ""
		switch {
		case *approve:
			decision = "APPROVE"
		case *reject:
			decision = "REJECT"
		case *revoke:
			decision = "REVOKE"
		}
		return c.callPrint("/ipc/v1/approvals/decide", map[string]string{"approval_id": args[1], "decision": decision, "subject_digest": *digest, "comment": *comment},
			ipc.CallOpts{IdempotencyKey: domain.NewID("cli"), IfMatch: *ifMatch})
	}
	return domain.ExitInput
}

func (c *cli) worktreeCmd(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(c.errw, "usage: samsara worktree list | inspect <id>")
		return domain.ExitInput
	}
	switch args[0] {
	case "list":
		fs := newFlags("worktree list")
		pid := fs.String("project", "", "project id")
		if err := fs.Parse(args[1:]); err != nil {
			return domain.ExitInput
		}
		id, err := c.projectID(*pid)
		if err != nil {
			return c.fail(err)
		}
		var snap struct {
			Entities map[string]json.RawMessage `json:"entities"`
		}
		if err := c.call("/ipc/v1/projects/snapshot", map[string]string{"project_id": id}, &snap); err != nil {
			return c.fail(err)
		}
		var v any
		_ = json.Unmarshal(snap.Entities[sqlite.KindWorktree], &v)
		return c.print(v)
	case "inspect":
		if len(args) < 2 {
			return domain.ExitInput
		}
		return c.callPrint("/ipc/v1/lookup", map[string]string{"kind": sqlite.KindWorktree, "id": args[1]})
	}
	return domain.ExitInput
}

func (c *cli) mergeCmd(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(c.errw, "usage: samsara merge queue | inspect|enqueue|cancel <candidate_id>")
		return domain.ExitInput
	}
	switch args[0] {
	case "queue":
		fs := newFlags("merge queue")
		pid := fs.String("project", "", "project id")
		if err := fs.Parse(args[1:]); err != nil {
			return domain.ExitInput
		}
		id, err := c.projectID(*pid)
		if err != nil {
			return c.fail(err)
		}
		return c.callPrint("/ipc/v1/merge/queues", map[string]string{"project_id": id})
	case "inspect", "enqueue", "cancel":
		if len(args) < 2 {
			return domain.ExitInput
		}
		opts := ipc.CallOpts{}
		if args[0] != "inspect" {
			opts.IdempotencyKey = domain.NewID("cli")
		}
		return c.callPrint("/ipc/v1/merge/"+args[0], map[string]string{"candidate_id": args[1]}, opts)
	}
	return domain.ExitInput
}

func (c *cli) history(args []string) int {
	fs := newFlags("history")
	pid := fs.String("project", "", "project id")
	tools := fs.Bool("tools", false, "include tool checks")
	cursor := fs.String("cursor", "", "page cursor")
	if err := fs.Parse(args); err != nil {
		return domain.ExitInput
	}
	id, err := c.projectID(*pid)
	if err != nil {
		return c.fail(err)
	}
	return c.callPrint("/ipc/v1/history", map[string]any{"project_id": id, "include_tool_checks": *tools, "cursor": *cursor})
}

func (c *cli) link(args []string) int {
	if len(args) == 0 || args[0] != "add" {
		fmt.Fprintln(c.errw, "usage: samsara link add --kind issue|pull_request|commit --key <k> [--issue id] [--url u] [--title t]")
		return domain.ExitInput
	}
	fs := newFlags("link add")
	pid := fs.String("project", "", "project id")
	issue := fs.String("issue", "", "issue id")
	kind := fs.String("kind", "", "issue|pull_request|commit")
	key := fs.String("key", "", "external key (e.g. owner/repo#12 or SHA)")
	url := fs.String("url", "", "url")
	title := fs.String("title", "", "title")
	provider := fs.String("provider", "github", "provider")
	if err := fs.Parse(args[1:]); err != nil {
		return domain.ExitInput
	}
	id, err := c.projectID(*pid)
	if err != nil {
		return c.fail(err)
	}
	return c.callPrint("/ipc/v1/links/add", app.LinkRequest{ProjectID: id, IssueID: *issue, Provider: *provider, Kind: *kind, ExternalKey: *key, URL: *url, Title: *title})
}

func (c *cli) ui(args []string) int {
	fs := newFlags("ui")
	open := fs.Bool("open", false, "open the browser")
	if err := fs.Parse(args); err != nil {
		return domain.ExitInput
	}
	var res map[string]string
	if err := c.call("/ipc/v1/ui/bootstrap", map[string]any{}, &res); err != nil {
		return c.fail(err)
	}
	if *open {
		opener := "xdg-open"
		if runtime.GOOS == "darwin" {
			opener = "open"
		}
		_ = exec.Command(opener, res["url"]).Start()
	}
	// The one-time code is printed only to the terminal of the user who asked.
	return c.print(res)
}

func (c *cli) backup(args []string) int {
	fs := newFlags("backup")
	dest := fs.String("dest", "", "destination file (must not exist)")
	if err := fs.Parse(args); err != nil || *dest == "" {
		return domain.ExitInput
	}
	abs, _ := filepath.Abs(*dest)
	return c.callPrint("/ipc/v1/backup", map[string]string{"dest": abs})
}

// --- doctor ---------------------------------------------------------------

func (c *cli) doctor(args []string) int {
	fs := newFlags("doctor")
	exercise := fs.Bool("exercise", false, "create and destroy a throwaway tmux session to verify control")
	write := fs.Bool("write-manifest", false, "write the compatibility manifest")
	if err := fs.Parse(args); err != nil {
		return domain.ExitInput
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	report := map[string]any{"platform": runtime.GOOS + "/" + runtime.GOARCH, "samsara": version}
	problems := []string{}
	// SQLite engine linked into this binary (not the sqlite3 CLI).
	tmpdb := filepath.Join(os.TempDir(), fmt.Sprintf("samsara-doctor-%d.db", os.Getpid()))
	if db, err := sqlite.Open(ctx, tmpdb, nil); err == nil {
		report["sqlite_version"] = db.EngineVersion
		db.Close()
	} else {
		problems = append(problems, "sqlite: "+err.Error())
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		_ = os.Remove(tmpdb + suffix)
	}
	cl := daemon.DetectClaude(daemon.LoadManifest(c.layout))
	report["claude"] = cl
	if !cl.BlockOnFailure {
		problems = append(problems, "claude: version "+cl.Version+" lacks onFailure:block (need >= "+hooks.MinBlockVersion+"); managed execution disabled, observe-only")
	}
	tm := tmux.New(nil)
	tprobe, _ := tm.Probe(ctx)
	report["tmux"] = tprobe
	hd := herdr.New(nil, daemon.LoadManifest(c.layout).HerdrSchemaSHA256)
	hprobe, _ := hd.Probe(ctx)
	report["herdr"] = hprobe
	g := gitops.New()
	report["git"] = g.Version(ctx)
	cwd, _ := os.Getwd()
	if id, err := project.Resolve(ctx, cwd); err == nil {
		pr := map[string]any{"root": id.Root, "is_git": id.IsGit}
		if cfg, _, err := config.Load(id.Root); err != nil {
			pr["config_error"] = err.Error()
			problems = append(problems, "config: "+err.Error())
		} else if cfg != nil {
			pr["config"] = "valid"
		} else {
			pr["config"] = "absent"
		}
		ok, err := hooks.PolicyHookInstalled(id.Root)
		pr["policy_hook_installed"] = ok
		if err != nil {
			pr["hook_settings_error"] = err.Error()
		}
		reg, _ := project.LoadRegistry(c.layout.RegistryPath())
		sc := reg.Classify(id.Root, "")
		if sc.Entry != nil {
			pr["activation"] = sc.Entry.Activation
			pr["project_id"] = sc.Entry.ProjectID
		} else {
			pr["activation"] = domain.ActivationUnconfigured
		}
		report["project"] = pr
	}
	report["daemon_running"] = daemon.Ping(ctx, c.layout)
	if *exercise {
		report["tmux_exercise"] = exerciseTmux(ctx, tm)
	}
	report["problems"] = problems
	if *write {
		m := daemon.LoadManifest(c.layout)
		m.VerifiedAt = time.Now().UTC().Format("2006-01-02")
		m.Platform = runtime.GOOS + "/" + runtime.GOARCH
		m.SamsaraBuild = version
		m.ClaudeCodeVersion = cl.Version
		m.TmuxVersion = tprobe.Version
		m.HerdrCLIVersion = hprobe.Version
		if hprobe.SchemaSHA256 != "" {
			m.HerdrSchemaSHA256 = hprobe.SchemaSHA256
		}
		m.GitVersion = g.Version(ctx)
		if v, ok := report["sqlite_version"].(string); ok {
			m.SQLiteVersion = v
		}
		if ex, ok := report["tmux_exercise"].(map[string]any); ok && ex["ok"] == true {
			m.RealBackendSmokePassed = true
		}
		_ = c.layout.Ensure()
		b, _ := json.MarshalIndent(m, "", "  ")
		if err := os.WriteFile(daemon.ManifestPath(c.layout), b, 0o600); err != nil {
			problems = append(problems, "manifest: "+err.Error())
		}
		report["manifest"] = daemon.ManifestPath(c.layout)
		report["manifest_note"] = "policy_hook_failure_block_verified and herdr_protocol_verified are set only after manual failure-injection / protocol fixture verification"
	}
	c.print(report)
	if len(problems) > 0 {
		return domain.ExitBlocked
	}
	return domain.ExitOK
}

func exerciseTmux(ctx context.Context, tm *tmux.Adapter) map[string]any {
	res := map[string]any{"ok": false}
	tm.Socket = fmt.Sprintf("samsara-doctor-%d", os.Getpid())
	corr := "doctor-" + domain.NewID("x")[2:10]
	rc, err := tm.Launch(ctx, launchSpec(corr))
	if err != nil || rc.Ref == nil {
		res["error"] = fmt.Sprint(err, rc.Detail)
		return res
	}
	obs, _ := tm.Observe(ctx, *rc.Ref)
	res["observed"] = obs.Liveness
	cl, _ := tm.CloseOwnedSession(ctx, *rc.Ref)
	res["close"] = cl.Outcome
	_ = exec.Command("tmux", "-L", tm.Socket, "kill-server").Run()
	res["ok"] = obs.Liveness == domain.LivenessAlive && string(cl.Outcome) == "DELIVERED"
	return res
}
