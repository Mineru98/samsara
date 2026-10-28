package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/Mineru98/samsara/runtime/internal/app"
	"github.com/Mineru98/samsara/runtime/internal/domain"
)

// Hub fans out post-commit wake-ups. Each subscriber has a 1-slot channel:
// a slow client never blocks the writer and never loses data, because the
// stream always re-reads durable events from the DB (contract §9.6).
type Hub struct {
	mu      sync.Mutex
	subs    map[string]map[chan struct{}]bool // project id or "catalog"
	maxSubs int
}

// NewHub returns an empty hub.
func NewHub() *Hub { return &Hub{subs: map[string]map[chan struct{}]bool{}, maxSubs: 64} }

// Notify implements sqlite.Notifier.
func (h *Hub) Notify(projects []string, catalog bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	wake := func(key string) {
		for ch := range h.subs[key] {
			select {
			case ch <- struct{}{}:
			default:
			}
		}
	}
	for _, p := range projects {
		wake(p)
	}
	if catalog {
		wake("catalog")
	}
}

// Subscribe registers a wake-up channel.
func (h *Hub) Subscribe(key string) (chan struct{}, func(), error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, m := range h.subs {
		n += len(m)
	}
	if n >= h.maxSubs {
		return nil, nil, domain.Errf(domain.CodeRateLimited, "too many open streams")
	}
	ch := make(chan struct{}, 1)
	if h.subs[key] == nil {
		h.subs[key] = map[chan struct{}]bool{}
	}
	h.subs[key][ch] = true
	return ch, func() {
		h.mu.Lock()
		delete(h.subs[key], ch)
		h.mu.Unlock()
	}, nil
}

// HeartbeatInterval between comment frames.
var HeartbeatInterval = 15 * time.Second

func sseHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Type", "text/event-stream; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
}

func writeEvent(w http.ResponseWriter, id, typ string, data any) error {
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	if id != "" {
		if _, err := fmt.Fprintf(w, "id: %s\n", id); err != nil {
			return err
		}
	}
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", typ, b)
	return err
}

// cursorParam implements "after" vs Last-Event-ID (both allowed, must agree).
func cursorParam(r *http.Request) (string, error) {
	after := r.URL.Query().Get("after")
	lei := r.Header.Get("Last-Event-ID")
	if after != "" && lei != "" && after != lei {
		return "", domain.Errf(domain.CodeInvalidRequest, "after and Last-Event-ID disagree")
	}
	if after == "" {
		after = lei
	}
	if after == "" {
		return "", domain.Errf(domain.CodeInvalidRequest, "cursor required: start from the snapshot_cursor of a snapshot")
	}
	return after, nil
}

func (s *Server) projectEvents(w http.ResponseWriter, r *http.Request, p app.Principal) {
	projectID := r.PathValue("project_id")
	raw, err := cursorParam(r)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	if _, err := s.App.Lookup(r.Context(), "projects", projectID); err != nil {
		writeErr(w, r, err)
		return
	}
	ch, unsub, err := s.Hub.Subscribe(projectID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	defer unsub()
	fl, _ := w.(http.Flusher)
	sseHeaders(w)
	cur, derr := app.DecodeCursor(raw)
	if derr != nil {
		_ = writeEvent(w, "", "reset_required", map[string]string{"reason": "CURSOR_INVALID"})
		return
	}
	hb := time.NewTicker(HeartbeatInterval)
	defer hb.Stop()
	for {
		batch, err := s.App.ReadProjectEvents(r.Context(), projectID, cur, 200)
		if err != nil {
			return
		}
		if batch.ResetRequired != "" {
			_ = writeEvent(w, "", "reset_required", map[string]string{"reason": batch.ResetRequired})
			if fl != nil {
				fl.Flush()
			}
			return
		}
		for _, e := range batch.Events {
			seq, _ := domain.ParseDec(e.Seq)
			next := app.Cursor{Scope: cur.Scope, DBGen: cur.DBGen, Seq: seq}
			if err := writeEvent(w, app.EncodeCursor(next), "project.change", e); err != nil {
				return
			}
			cur = next
		}
		if fl != nil {
			fl.Flush()
		}
		if len(batch.Events) == 200 {
			continue
		}
		select {
		case <-r.Context().Done():
			return
		case <-ch:
		case <-hb.C:
			if _, err := s.authenticate(r); err != nil {
				return // token expired: stream ends, client re-authenticates
			}
			if _, err := fmt.Fprint(w, ": heartbeat\n\n"); err != nil {
				return
			}
			if fl != nil {
				fl.Flush()
			}
		}
	}
}

func (s *Server) catalogEvents(w http.ResponseWriter, r *http.Request, p app.Principal) {
	raw, err := cursorParam(r)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	ch, unsub, err := s.Hub.Subscribe("catalog")
	if err != nil {
		writeErr(w, r, err)
		return
	}
	defer unsub()
	fl, _ := w.(http.Flusher)
	sseHeaders(w)
	cur, derr := app.DecodeCursor(raw)
	if derr != nil {
		_ = writeEvent(w, "", "reset_required", map[string]string{"reason": "CURSOR_INVALID"})
		return
	}
	hb := time.NewTicker(HeartbeatInterval)
	defer hb.Stop()
	for {
		evs, reset, err := s.App.ReadCatalogEvents(r.Context(), cur, 200)
		if err != nil {
			return
		}
		if reset != "" {
			_ = writeEvent(w, "", "reset_required", map[string]string{"reason": reset})
			if fl != nil {
				fl.Flush()
			}
			return
		}
		for _, e := range evs {
			seq, _ := domain.ParseDec(e.Seq)
			next := app.Cursor{Scope: "catalog", DBGen: cur.DBGen, Seq: seq}
			if err := writeEvent(w, app.EncodeCursor(next), "catalog.change", e); err != nil {
				return
			}
			cur = next
		}
		if fl != nil {
			fl.Flush()
		}
		if len(evs) == 200 {
			continue
		}
		select {
		case <-r.Context().Done():
			return
		case <-ch:
		case <-hb.C:
			if _, err := s.authenticate(r); err != nil {
				return
			}
			if _, err := fmt.Fprint(w, ": heartbeat\n\n"); err != nil {
				return
			}
			if fl != nil {
				fl.Flush()
			}
		}
	}
}
