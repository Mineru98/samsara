// Package httpapi is the loopback HTTP API and SSE feed for the dashboard
// (contract §7–9, §11). It binds to 127.0.0.1 only, checks Host/Origin,
// authenticates with an in-memory bearer obtained from a one-time bootstrap
// code, and never exposes SQLite, lease secrets or raw tool input.
package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Mineru98/samsara/runtime/internal/app"
	"github.com/Mineru98/samsara/runtime/internal/domain"
	"github.com/Mineru98/samsara/runtime/internal/store/sqlite"
)

// Server is the HTTP API.
type Server struct {
	App      *app.App
	Hub      *Hub
	Port     int
	TokenTTL time.Duration
	mux      *http.ServeMux
}

// New builds the server and installs the DB notifier into the hub.
func New(a *app.App) *Server {
	s := &Server{App: a, Hub: NewHub(), TokenTTL: 12 * time.Hour, mux: http.NewServeMux()}
	a.DB.SetNotifier(s.Hub.Notify)
	s.routes()
	return s
}

// Listen binds a loopback listener (port 0 = ephemeral).
func (s *Server) Listen(port int) (net.Listener, error) {
	l, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return nil, err
	}
	s.Port = l.Addr().(*net.TCPAddr).Port
	return l, nil
}

func (s *Server) allowedOrigins() map[string]bool {
	p := strconv.Itoa(s.Port)
	return map[string]bool{"http://127.0.0.1:" + p: true, "http://localhost:" + p: true}
}

// ServeHTTP applies the endpoint defences before routing.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'self'; connect-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; frame-ancestors 'none'")
	host := r.Host
	p := strconv.Itoa(s.Port)
	if host != "127.0.0.1:"+p && host != "localhost:"+p {
		writeErr(w, r, domain.Errf(domain.CodeActionForbidden, "host not allowed"))
		return
	}
	if o := r.Header.Get("Origin"); o != "" && !s.allowedOrigins()[o] {
		writeErr(w, r, domain.Errf(domain.CodeActionForbidden, "origin not allowed"))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	s.mux.ServeHTTP(w, r)
}

type ctxKey int

const principalKey ctxKey = 1

// auth wraps a handler with bearer authentication.
func (s *Server) auth(next func(w http.ResponseWriter, r *http.Request, p app.Principal)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, err := s.authenticate(r)
		if err != nil {
			writeErr(w, r, err)
			return
		}
		next(w, r, p)
	}
}

func (s *Server) authenticate(r *http.Request) (app.Principal, error) {
	authz := r.Header.Get("Authorization")
	if !strings.HasPrefix(authz, "Bearer ") {
		return app.Principal{}, domain.Errf(domain.CodeSessionExpired, "missing bearer token")
	}
	tok := strings.TrimPrefix(authz, "Bearer ")
	var id string
	var exp int64
	var revoked sql.NullInt64
	err := s.App.DB.Read(r.Context(), func(q sqlite.Q) error {
		return q.QueryRowContext(r.Context(), "SELECT id, expires_at, revoked_at FROM ui_auth_sessions WHERE token_hash=?", domain.HashSecret(tok)).Scan(&id, &exp, &revoked)
	})
	if err != nil || revoked.Valid || exp < domain.Millis(s.App.Clock.Now()) {
		return app.Principal{}, domain.Errf(domain.CodeSessionExpired, "session expired or revoked")
	}
	return app.Principal{ID: "ui:" + id, Kind: "ui"}, nil
}

// NewBootstrapCode issues a one-time code (60 s) and returns the launch URL.
func (s *Server) NewBootstrapCode(ctx context.Context) (any, error) {
	code := domain.NewSecret()
	err := s.App.DB.Write(ctx, func(tx *sqlite.Tx) error {
		_, err := tx.ExecContext(ctx, "INSERT INTO ui_bootstrap_codes(code_hash,expires_at) VALUES(?,?)", domain.HashSecret(code), tx.Now+60*1000)
		return err
	})
	if err != nil {
		return nil, err
	}
	return map[string]string{"url": fmt.Sprintf("http://127.0.0.1:%d/#bootstrap=%s", s.Port, code), "expires_in": "60s"}, nil
}

func (s *Server) exchangeCode(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Code == "" {
		writeErr(w, r, domain.Errf(domain.CodeInvalidRequest, "code required"))
		return
	}
	tok := domain.NewSecret()
	var exp int64
	err := s.App.DB.Write(r.Context(), func(tx *sqlite.Tx) error {
		res, err := tx.ExecContext(r.Context(), "UPDATE ui_bootstrap_codes SET used_at=? WHERE code_hash=? AND used_at IS NULL AND expires_at >= ?", tx.Now, domain.HashSecret(body.Code), tx.Now)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return domain.Errf(domain.CodeSessionExpired, "bootstrap code invalid, used or expired")
		}
		exp = tx.Now + s.TokenTTL.Milliseconds()
		_, err = tx.ExecContext(r.Context(), "INSERT INTO ui_auth_sessions(id,token_hash,principal,created_at,expires_at) VALUES(?,?,?,?,?)",
			domain.NewID("uis"), domain.HashSecret(tok), "local-user", tx.Now, exp)
		return err
	})
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"token": tok, "expires_at": domain.RFC3339(exp)})
}

func (s *Server) revokeCurrent(w http.ResponseWriter, r *http.Request, p app.Principal) {
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	_ = s.App.DB.Write(r.Context(), func(tx *sqlite.Tx) error {
		_, err := tx.ExecContext(r.Context(), "UPDATE ui_auth_sessions SET revoked_at=? WHERE token_hash=?", tx.Now, domain.HashSecret(tok))
		return err
	})
	w.WriteHeader(http.StatusNoContent)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, r *http.Request, err error) {
	de := domain.AsError(err)
	body := map[string]any{"code": de.Code, "message": de.Message, "retryable": de.Retryable, "request_id": r.Header.Get("X-Request-Id")}
	if de.Details != nil {
		body["details"] = de.Details
	}
	if de.Code == domain.CodeRateLimited {
		w.Header().Set("Retry-After", "1")
	}
	writeJSON(w, domain.HTTPStatus(de.Code), map[string]any{"error": body})
}

// etagOf derives a strong ETag "<id>:v<version>" from a projection.
func etagOf(v any) string {
	b, _ := json.Marshal(v)
	var m struct {
		ID      string `json:"id"`
		Version string `json:"version"`
	}
	if json.Unmarshal(b, &m) != nil || m.ID == "" || m.Version == "" {
		return ""
	}
	return `"` + m.ID + ":v" + m.Version + `"`
}

func (s *Server) cmd(r *http.Request, p app.Principal) (app.Cmd, error) {
	key := r.Header.Get("Idempotency-Key")
	if key == "" {
		return app.Cmd{}, domain.Errf(domain.CodeInvalidRequest, "Idempotency-Key header required for commands")
	}
	c := app.Cmd{Principal: p, IdempotencyKey: key}
	if im := r.Header.Get("If-Match"); im != "" {
		im = strings.Trim(im, `"`)
		if i := strings.LastIndex(im, ":v"); i >= 0 {
			c.IfMatch = im[i+2:]
		} else {
			c.IfMatch = im
		}
	}
	return c, nil
}

// get serves one entity projection with its ETag.
func (s *Server) get(kind string) http.HandlerFunc {
	return s.auth(func(w http.ResponseWriter, r *http.Request, p app.Principal) {
		id := r.PathValue("id")
		v, err := s.App.Lookup(r.Context(), kind, id)
		if err != nil {
			writeErr(w, r, err)
			return
		}
		if et := etagOf(v); et != "" {
			w.Header().Set("ETag", et)
		}
		writeJSON(w, http.StatusOK, v)
	})
}

func (s *Server) routes() {
	m := s.mux
	a := s.App
	m.HandleFunc("GET /v1/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "ready": a.Ready()})
	})
	m.HandleFunc("POST /v1/ui/sessions", s.exchangeCode)
	m.HandleFunc("DELETE /v1/ui/sessions/current", s.auth(s.revokeCurrent))
	m.HandleFunc("GET /v1/runtime", s.auth(func(w http.ResponseWriter, r *http.Request, p app.Principal) {
		writeJSON(w, http.StatusOK, a.Info(r.Context(), true))
	}))
	m.HandleFunc("GET /v1/projects", s.auth(func(w http.ResponseWriter, r *http.Request, p app.Principal) {
		ps, err := a.ListProjectSummaries(r.Context(), r.URL.Query().Get("include_archived") == "true")
		respond(w, r, map[string]any{"items": nonNil(ps)}, err)
	}))
	m.HandleFunc("GET /v1/catalog/snapshot", s.auth(func(w http.ResponseWriter, r *http.Request, p app.Principal) {
		cs, err := a.GetCatalogSnapshot(r.Context())
		respond(w, r, cs, err)
	}))
	m.HandleFunc("GET /v1/catalog/events", s.auth(s.catalogEvents))
	m.HandleFunc("GET /v1/projects/{project_id}", s.auth(func(w http.ResponseWriter, r *http.Request, p app.Principal) {
		v, err := a.Lookup(r.Context(), sqlite.KindProject, r.PathValue("project_id"))
		if err == nil {
			if et := etagOf(v); et != "" {
				w.Header().Set("ETag", et)
			}
		}
		respond(w, r, v, err)
	}))
	m.HandleFunc("GET /v1/projects/{project_id}/snapshot", s.auth(func(w http.ResponseWriter, r *http.Request, p app.Principal) {
		snap, err := a.ProjectSnapshot(r.Context(), r.PathValue("project_id"))
		respond(w, r, snap, err)
	}))
	m.HandleFunc("GET /v1/projects/{project_id}/events", s.auth(s.projectEvents))
	page := func(fn func(context.Context, string, app.PageReq) (*app.Page, error)) http.HandlerFunc {
		return s.auth(func(w http.ResponseWriter, r *http.Request, p app.Principal) {
			lim, _ := strconv.Atoi(r.URL.Query().Get("limit"))
			pg, err := fn(r.Context(), r.PathValue("project_id"), app.PageReq{Cursor: r.URL.Query().Get("cursor"), Limit: lim})
			respond(w, r, pg, err)
		})
	}
	m.HandleFunc("GET /v1/projects/{project_id}/issues", page(a.ListIssuesPage))
	m.HandleFunc("GET /v1/projects/{project_id}/runs", page(a.ListRunsPage))
	m.HandleFunc("GET /v1/projects/{project_id}/approvals", page(a.ListApprovalsPage))
	m.HandleFunc("GET /v1/projects/{project_id}/history", page(func(ctx context.Context, pid string, pr app.PageReq) (*app.Page, error) {
		return a.History(ctx, pid, pr, false)
	}))
	m.HandleFunc("GET /v1/projects/{project_id}/github-history", s.auth(func(w http.ResponseWriter, r *http.Request, p app.Principal) {
		v, err := a.GitHubHistory(r.Context(), r.PathValue("project_id"))
		respond(w, r, v, err)
	}))
	m.HandleFunc("GET /v1/projects/{project_id}/merge-queues", s.auth(func(w http.ResponseWriter, r *http.Request, p app.Principal) {
		v, err := a.MergeQueues(r.Context(), r.PathValue("project_id"))
		respond(w, r, map[string]any{"items": nonNil(v)}, err)
	}))
	m.HandleFunc("GET /v1/issues/{id}", s.auth(func(w http.ResponseWriter, r *http.Request, p app.Principal) {
		v, err := a.GetIssueDetail(r.Context(), r.PathValue("id"))
		if err == nil {
			if et := etagOf(v.Issue); et != "" {
				w.Header().Set("ETag", et)
			}
		}
		respond(w, r, v, err)
	}))
	m.HandleFunc("GET /v1/runs/{id}", s.get(sqlite.KindRun))
	m.HandleFunc("GET /v1/runs/{id}/snapshot", s.auth(func(w http.ResponseWriter, r *http.Request, p app.Principal) {
		v, err := a.RunSnapshot(r.Context(), r.PathValue("id"))
		respond(w, r, v, err)
	}))
	m.HandleFunc("GET /v1/attempts/{id}", s.get(sqlite.KindAttempt))
	m.HandleFunc("GET /v1/sessions/{id}", s.get(sqlite.KindSession))
	m.HandleFunc("GET /v1/worktrees/{id}", s.get(sqlite.KindWorktree))
	m.HandleFunc("GET /v1/approvals/{id}", s.get(sqlite.KindApproval))
	m.HandleFunc("GET /v1/merge-candidates/{id}", s.get(sqlite.KindCandidate))
	m.HandleFunc("GET /v1/operations/{id}", s.get(sqlite.KindOperation))

	command := func(fn func(r *http.Request, c app.Cmd) (any, error)) http.HandlerFunc {
		return s.auth(func(w http.ResponseWriter, r *http.Request, p app.Principal) {
			if !a.Ready() {
				writeErr(w, r, domain.Errf(domain.CodeRuntimeNotReady, "recovering"))
				return
			}
			c, err := s.cmd(r, p)
			if err != nil {
				writeErr(w, r, err)
				return
			}
			v, err := fn(r, c)
			if err != nil {
				writeErr(w, r, err)
				return
			}
			writeJSON(w, http.StatusAccepted, v)
		})
	}
	m.HandleFunc("POST /v1/approvals/{id}/decisions", command(func(r *http.Request, c app.Cmd) (any, error) {
		var body app.DecisionRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			return nil, domain.Errf(domain.CodeInvalidRequest, "bad body")
		}
		return a.DecideApproval(r.Context(), c, r.PathValue("id"), body)
	}))
	m.HandleFunc("POST /v1/merge-candidates/{id}/enqueue", command(func(r *http.Request, c app.Cmd) (any, error) {
		return a.EnqueueCandidate(r.Context(), c, r.PathValue("id"))
	}))
	m.HandleFunc("POST /v1/merge-candidates/{id}/cancel", command(func(r *http.Request, c app.Cmd) (any, error) {
		return a.CancelCandidate(r.Context(), c, r.PathValue("id"))
	}))
	m.HandleFunc("POST /v1/runs/{id}/pause", command(func(r *http.Request, c app.Cmd) (any, error) {
		return a.PauseRun(r.Context(), c, r.PathValue("id"))
	}))
	m.HandleFunc("POST /v1/runs/{id}/resume", command(func(r *http.Request, c app.Cmd) (any, error) {
		return a.ResumeRun(r.Context(), c, r.PathValue("id"))
	}))
	m.HandleFunc("POST /v1/runs/{id}/cancel", command(func(r *http.Request, c app.Cmd) (any, error) {
		return a.CancelRun(r.Context(), c, r.PathValue("id"))
	}))
	m.HandleFunc("POST /v1/projects/{project_id}/disable", command(func(r *http.Request, c app.Cmd) (any, error) {
		return a.DisableProject(r.Context(), c, r.PathValue("project_id"))
	}))
	m.HandleFunc("POST /v1/projects/{project_id}/refresh", command(func(r *http.Request, c app.Cmd) (any, error) {
		if _, err := a.Lookup(r.Context(), sqlite.KindProject, r.PathValue("project_id")); err != nil {
			return nil, err
		}
		a.Backends.Invalidate()
		a.Wake()
		return map[string]string{"status": "ACCEPTED"}, nil
	}))
	m.HandleFunc("POST /v1/sessions/{id}/focus", command(func(r *http.Request, c app.Cmd) (any, error) {
		return a.FocusSession(r.Context(), r.PathValue("id"))
	}))
	m.HandleFunc("POST /v1/sessions/{id}/release-input", command(func(r *http.Request, c app.Cmd) (any, error) {
		return a.ReleaseInput(r.Context(), c, r.PathValue("id"))
	}))
	m.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(placeholderPage))
	})
}

const placeholderPage = `<!doctype html><html lang="ko"><head><meta charset="utf-8"><title>Samsara runtime</title></head>
<body><h1>Samsara runtime</h1><p>대시보드 UI 번들은 이 런타임 빌드에 포함되어 있지 않습니다. API: <code>/v1/health</code>, <code>/v1/projects</code>, SSE <code>/v1/projects/{id}/events</code>.</p></body></html>`

func respond(w http.ResponseWriter, r *http.Request, v any, err error) {
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
