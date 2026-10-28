// Package ipc is the CLI/Hook ↔ daemon transport: HTTP/JSON over a
// user-private Unix domain socket (contract §10.1). Requests carry a
// protocol version, request id, deadline and optional idempotency key.
package ipc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/Mineru98/samsara/runtime/internal/app"
	"github.com/Mineru98/samsara/runtime/internal/domain"
	"github.com/Mineru98/samsara/runtime/internal/paths"
)

// ProtocolVersion of the IPC contract.
const ProtocolVersion = "1"

// MaxBody bounds request bodies.
const MaxBody = 8 << 20

// Headers.
const (
	HeaderProtocol    = "Samsara-Protocol"
	HeaderRequestID   = "Samsara-Request-Id"
	HeaderDeadlineMs  = "Samsara-Deadline-Ms"
	HeaderIdempotency = "Idempotency-Key"
	HeaderIfMatch     = "If-Match"
)

// --- server ---------------------------------------------------------------

// Server serves IPC routes for one App.
type Server struct {
	App         *app.App
	UIBootstrap func(ctx context.Context) (any, error)
	Stop        func()
	mux         *http.ServeMux
}

// NewServer builds the route table.
func NewServer(a *app.App) *Server {
	s := &Server{App: a, mux: http.NewServeMux()}
	s.routes()
	return s
}

// Handler returns the HTTP handler.
func (s *Server) Handler() http.Handler { return s }

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if pv := r.Header.Get(HeaderProtocol); pv != ProtocolVersion {
		writeErr(w, domain.Errf(domain.CodeProtocolMismatch, "IPC protocol %q unsupported (want %s)", pv, ProtocolVersion))
		return
	}
	if ms, err := strconv.Atoi(r.Header.Get(HeaderDeadlineMs)); err == nil && ms > 0 {
		ctx, cancel := context.WithTimeout(r.Context(), time.Duration(ms)*time.Millisecond)
		defer cancel()
		r = r.WithContext(ctx)
	}
	r.Body = http.MaxBytesReader(w, r.Body, MaxBody)
	s.mux.ServeHTTP(w, r)
}

func cmdFrom(r *http.Request) app.Cmd {
	return app.Cmd{Principal: app.Principal{ID: fmt.Sprintf("user:%d", os.Getuid()), Kind: "cli"},
		IdempotencyKey: r.Header.Get(HeaderIdempotency), IfMatch: r.Header.Get(HeaderIfMatch)}
}

func decode[T any](r *http.Request) (T, error) {
	var v T
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(&v); err != nil && !errors.Is(err, io.EOF) {
		return v, domain.Errf(domain.CodeInvalidRequest, "bad JSON body: %v", err)
	}
	return v, nil
}

func handle[T any](fn func(r *http.Request, req T) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErr(w, domain.Errf(domain.CodeInvalidRequest, "POST required"))
			return
		}
		req, err := decode[T](r)
		if err != nil {
			writeErr(w, err)
			return
		}
		res, err := fn(r, req)
		if err != nil {
			writeErrWith(w, err, res)
			return
		}
		writeJSON(w, http.StatusOK, res)
	}
}

func (s *Server) routes() {
	a := s.App
	m := s.mux
	m.HandleFunc("/ipc/v1/runtime", handle(func(r *http.Request, _ struct{ Probe bool }) (any, error) {
		return a.Info(r.Context(), true), nil
	}))
	m.HandleFunc("/ipc/v1/projects/register", handle(func(r *http.Request, req app.RegisterProjectRequest) (any, error) {
		return a.RegisterProject(r.Context(), cmdFrom(r), req)
	}))
	m.HandleFunc("/ipc/v1/projects/disable", handle(func(r *http.Request, req struct {
		ProjectID string `json:"project_id"`
	}) (any, error) {
		return a.DisableProject(r.Context(), cmdFrom(r), req.ProjectID)
	}))
	m.HandleFunc("/ipc/v1/projects/archive", handle(func(r *http.Request, req struct {
		ProjectID string `json:"project_id"`
	}) (any, error) {
		return map[string]bool{"ok": true}, a.ArchiveProject(r.Context(), cmdFrom(r), req.ProjectID)
	}))
	m.HandleFunc("/ipc/v1/projects/relink", handle(func(r *http.Request, req struct {
		ProjectID string `json:"project_id"`
		Path      string `json:"path"`
	}) (any, error) {
		return map[string]bool{"ok": true}, a.RelinkProject(r.Context(), cmdFrom(r), req.ProjectID, req.Path)
	}))
	m.HandleFunc("/ipc/v1/projects/list", handle(func(r *http.Request, req struct {
		IncludeArchived bool `json:"include_archived"`
	}) (any, error) {
		return a.ListProjectSummaries(r.Context(), req.IncludeArchived)
	}))
	m.HandleFunc("/ipc/v1/projects/snapshot", handle(func(r *http.Request, req struct {
		ProjectID string `json:"project_id"`
	}) (any, error) {
		return a.ProjectSnapshot(r.Context(), req.ProjectID)
	}))
	m.HandleFunc("/ipc/v1/profiles/register", handle(func(r *http.Request, req app.CheckProfileRequest) (any, error) {
		return map[string]bool{"ok": true}, a.RegisterCheckProfile(r.Context(), cmdFrom(r), req)
	}))
	m.HandleFunc("/ipc/v1/plans/validate", handle(func(r *http.Request, req json.RawMessage) (any, error) {
		return a.ValidatePlan(r.Context(), req)
	}))
	m.HandleFunc("/ipc/v1/plans/apply", handle(func(r *http.Request, req json.RawMessage) (any, error) {
		return a.ApplyPlan(r.Context(), cmdFrom(r), req)
	}))
	type runReq struct {
		RunID string `json:"run_id"`
	}
	m.HandleFunc("/ipc/v1/runs/start", handle(func(r *http.Request, req runReq) (any, error) {
		return a.StartRun(r.Context(), cmdFrom(r), req.RunID)
	}))
	m.HandleFunc("/ipc/v1/runs/pause", handle(func(r *http.Request, req runReq) (any, error) {
		return a.PauseRun(r.Context(), cmdFrom(r), req.RunID)
	}))
	m.HandleFunc("/ipc/v1/runs/resume", handle(func(r *http.Request, req runReq) (any, error) {
		return a.ResumeRun(r.Context(), cmdFrom(r), req.RunID)
	}))
	m.HandleFunc("/ipc/v1/runs/cancel", handle(func(r *http.Request, req runReq) (any, error) {
		return a.CancelRun(r.Context(), cmdFrom(r), req.RunID)
	}))
	m.HandleFunc("/ipc/v1/issues/retry", handle(func(r *http.Request, req struct {
		IssueID string `json:"issue_id"`
	}) (any, error) {
		return a.RetryIssue(r.Context(), cmdFrom(r), req.IssueID)
	}))
	m.HandleFunc("/ipc/v1/issues/get", handle(func(r *http.Request, req struct {
		IssueID string `json:"issue_id"`
	}) (any, error) {
		return a.GetIssueDetail(r.Context(), req.IssueID)
	}))
	m.HandleFunc("/ipc/v1/sessions/register", handle(func(r *http.Request, req app.HookIdentity) (any, error) {
		return a.RegisterSession(r.Context(), req)
	}))
	m.HandleFunc("/ipc/v1/sessions/attach", handle(func(r *http.Request, req app.AttachRequest) (any, error) {
		return a.AttachSession(r.Context(), cmdFrom(r), req)
	}))
	type sessReq struct {
		SessionID string `json:"session_id"`
	}
	m.HandleFunc("/ipc/v1/sessions/focus", handle(func(r *http.Request, req sessReq) (any, error) {
		return a.FocusSession(r.Context(), req.SessionID)
	}))
	m.HandleFunc("/ipc/v1/sessions/release-input", handle(func(r *http.Request, req sessReq) (any, error) {
		return a.ReleaseInput(r.Context(), cmdFrom(r), req.SessionID)
	}))
	m.HandleFunc("/ipc/v1/sessions/observe", handle(func(r *http.Request, req app.ObserveRequest) (any, error) {
		return map[string]bool{"ok": true}, a.ObserveSession(r.Context(), req)
	}))
	m.HandleFunc("/ipc/v1/sessions/finish", handle(func(r *http.Request, req app.FinishRequest) (any, error) {
		return map[string]bool{"ok": true}, a.FinishSession(r.Context(), req)
	}))
	m.HandleFunc("/ipc/v1/tools/check", handle(func(r *http.Request, req app.ToolCheckRequest) (any, error) {
		return a.CheckTool(r.Context(), req)
	}))
	m.HandleFunc("/ipc/v1/tools/result", handle(func(r *http.Request, req app.ObserveRequest) (any, error) {
		return map[string]bool{"ok": true}, a.ObserveSession(r.Context(), req)
	}))
	m.HandleFunc("/ipc/v1/leases/renew", handle(func(r *http.Request, req app.RenewRequest) (any, error) {
		return a.RenewLease(r.Context(), req)
	}))
	m.HandleFunc("/ipc/v1/outputs/submit", handle(func(r *http.Request, req app.OutputSubmitRequest) (any, error) {
		return a.SubmitOutput(r.Context(), cmdFrom(r), req)
	}))
	m.HandleFunc("/ipc/v1/dispatches/ack", handle(func(r *http.Request, req struct {
		DispatchID      string `json:"dispatch_id"`
		ClaudeSessionID string `json:"claude_session_id"`
	}) (any, error) {
		return map[string]bool{"ok": true}, a.AckDispatch(r.Context(), req.DispatchID, req.ClaudeSessionID)
	}))
	m.HandleFunc("/ipc/v1/approvals/list", handle(func(r *http.Request, req struct {
		ProjectID string `json:"project_id"`
		Cursor    string `json:"cursor"`
	}) (any, error) {
		return a.ListApprovalsPage(r.Context(), req.ProjectID, app.PageReq{Cursor: req.Cursor, Limit: 200})
	}))
	m.HandleFunc("/ipc/v1/approvals/decide", handle(func(r *http.Request, req struct {
		ApprovalID string `json:"approval_id"`
		app.DecisionRequest
	}) (any, error) {
		return a.DecideApproval(r.Context(), cmdFrom(r), req.ApprovalID, req.DecisionRequest)
	}))
	m.HandleFunc("/ipc/v1/merge/queues", handle(func(r *http.Request, req struct {
		ProjectID string `json:"project_id"`
	}) (any, error) {
		return a.MergeQueues(r.Context(), req.ProjectID)
	}))
	type candReq struct {
		CandidateID string `json:"candidate_id"`
	}
	m.HandleFunc("/ipc/v1/merge/enqueue", handle(func(r *http.Request, req candReq) (any, error) {
		return a.EnqueueCandidate(r.Context(), cmdFrom(r), req.CandidateID)
	}))
	m.HandleFunc("/ipc/v1/merge/cancel", handle(func(r *http.Request, req candReq) (any, error) {
		return a.CancelCandidate(r.Context(), cmdFrom(r), req.CandidateID)
	}))
	m.HandleFunc("/ipc/v1/merge/inspect", handle(func(r *http.Request, req candReq) (any, error) {
		return a.Lookup(r.Context(), "merge_candidates", req.CandidateID)
	}))
	m.HandleFunc("/ipc/v1/links/add", handle(func(r *http.Request, req app.LinkRequest) (any, error) {
		return map[string]bool{"ok": true}, a.AddExternalLink(r.Context(), cmdFrom(r), req)
	}))
	m.HandleFunc("/ipc/v1/lookup", handle(func(r *http.Request, req struct {
		Kind string `json:"kind"`
		ID   string `json:"id"`
	}) (any, error) {
		return a.Lookup(r.Context(), req.Kind, req.ID)
	}))
	m.HandleFunc("/ipc/v1/history", handle(func(r *http.Request, req struct {
		ProjectID string `json:"project_id"`
		Cursor    string `json:"cursor"`
		Tools     bool   `json:"include_tool_checks"`
	}) (any, error) {
		return a.History(r.Context(), req.ProjectID, app.PageReq{Cursor: req.Cursor, Limit: 100}, req.Tools)
	}))
	m.HandleFunc("/ipc/v1/ui/bootstrap", handle(func(r *http.Request, _ struct{}) (any, error) {
		if s.UIBootstrap == nil {
			return nil, domain.Errf(domain.CodeUnsupportedCapability, "UI server not running")
		}
		return s.UIBootstrap(r.Context())
	}))
	m.HandleFunc("/ipc/v1/daemon/stop", handle(func(r *http.Request, _ struct{}) (any, error) {
		if s.Stop == nil {
			return nil, domain.Errf(domain.CodeUnsupportedCapability, "stop not wired")
		}
		go s.Stop()
		return map[string]string{"status": "STOPPING"}, nil
	}))
	m.HandleFunc("/ipc/v1/backup", handle(func(r *http.Request, req struct {
		Dest string `json:"dest"`
	}) (any, error) {
		return map[string]string{"dest": req.Dest}, a.DB.Backup(r.Context(), req.Dest)
	}))
}

// UIBootstrap and Stop are wired by the daemon.
func (s *Server) SetHooks(ui func(ctx context.Context) (any, error), stop func()) {
	s.UIBootstrap, s.Stop = ui, stop
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// ErrorBody is the wire error (contract §7.3).
type ErrorBody struct {
	Error  *domain.Error `json:"error"`
	Result any           `json:"result,omitempty"`
}

func writeErr(w http.ResponseWriter, err error) { writeErrWith(w, err, nil) }

func writeErrWith(w http.ResponseWriter, err error, res any) {
	de := domain.AsError(err)
	writeJSON(w, domain.HTTPStatus(de.Code), ErrorBody{Error: de, Result: res})
}

// Listen creates the user-private socket. A stale socket file is replaced
// only by the holder of the daemon lock (caller's responsibility).
func Listen(path string) (net.Listener, error) {
	_ = os.Remove(path)
	l, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		l.Close()
		return nil, err
	}
	return &peerCheckListener{Listener: l}, nil
}

// peerCheckListener rejects connections from other OS users.
type peerCheckListener struct{ net.Listener }

func (l *peerCheckListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		if uc, ok := c.(*net.UnixConn); ok {
			if uid, ok := peerUID(uc); ok && uid != os.Getuid() {
				c.Close()
				continue
			}
		}
		return c, nil
	}
}

// --- client ---------------------------------------------------------------

// Client calls the daemon over the socket.
type Client struct {
	Socket string
	hc     *http.Client
}

// NewClient returns a client for a socket path.
func NewClient(socket string) *Client {
	tr := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		// Never talk to a socket owned by another OS user (ACT-13).
		if err := paths.CheckPrivate(filepath.Dir(socket)); err != nil {
			return nil, err
		}
		if fi, err := os.Lstat(socket); err == nil {
			if st, ok := fi.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Getuid() {
				return nil, fmt.Errorf("socket %s is owned by uid %d", socket, st.Uid)
			}
		}
		var d net.Dialer
		return d.DialContext(ctx, "unix", socket)
	}, DisableKeepAlives: true}
	return &Client{Socket: socket, hc: &http.Client{Transport: tr}}
}

// CallOpts are per-call options.
type CallOpts struct {
	IdempotencyKey string
	IfMatch        string
}

// Call implements bridge.Caller.
func (c *Client) Call(ctx context.Context, path string, req, resp any) error {
	return c.CallWith(ctx, path, req, resp, CallOpts{})
}

// CallWith performs a POST with options. Daemon errors are returned as
// *domain.Error; connection failures as RUNTIME_UNAVAILABLE.
func (c *Client) CallWith(ctx context.Context, path string, req, resp any, o CallOpts) error {
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://samsara"+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	hr.Header.Set(HeaderProtocol, ProtocolVersion)
	hr.Header.Set(HeaderRequestID, domain.NewID("req"))
	hr.Header.Set("Content-Type", "application/json")
	if dl, ok := ctx.Deadline(); ok {
		ms := time.Until(dl).Milliseconds() - 100
		if ms < 50 {
			ms = 50
		}
		hr.Header.Set(HeaderDeadlineMs, strconv.FormatInt(ms, 10))
	}
	if o.IdempotencyKey != "" {
		hr.Header.Set(HeaderIdempotency, o.IdempotencyKey)
	}
	if o.IfMatch != "" {
		hr.Header.Set(HeaderIfMatch, o.IfMatch)
	}
	res, err := c.hc.Do(hr)
	if err != nil {
		return &domain.Error{Code: domain.ReasonRuntimeUnavailable, Message: err.Error(), Retryable: true}
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, MaxBody))
	if err != nil {
		return &domain.Error{Code: domain.ReasonRuntimeUnavailable, Message: err.Error(), Retryable: true}
	}
	if res.StatusCode != http.StatusOK {
		var eb struct {
			Error  *domain.Error   `json:"error"`
			Result json.RawMessage `json:"result"`
		}
		if json.Unmarshal(b, &eb) == nil && eb.Error != nil {
			if resp != nil && len(eb.Result) > 0 && string(eb.Result) != "null" {
				_ = json.Unmarshal(eb.Result, resp)
			}
			return eb.Error
		}
		return &domain.Error{Code: domain.CodeInternal, Message: fmt.Sprintf("HTTP %d: %s", res.StatusCode, string(b))}
	}
	if resp == nil {
		return nil
	}
	if raw, ok := resp.(*json.RawMessage); ok {
		*raw = b
		return nil
	}
	return json.Unmarshal(b, resp)
}
