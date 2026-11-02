package herdr

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Mineru98/samsara/runtime/internal/backend"
	"github.com/Mineru98/samsara/runtime/internal/backend/fake"
	"github.com/Mineru98/samsara/runtime/internal/backend/standalone"
	"github.com/Mineru98/samsara/runtime/internal/domain"
)

// server is a herdr endpoint the contract suite runs against: the in-process
// fake or an isolated real herdr server.
type server interface {
	socket() string
	stop(t *testing.T)
	start(t *testing.T)
	restart(t *testing.T)
	createForeign(t *testing.T, label string) paneInfo
	// seen returns what reached the worker's terminal input.
	seen(t *testing.T, pane string) string
}

func (f *fakeServer) seen(t *testing.T, pane string) string {
	return strings.Join(f.prompts(pane), "\n")
}

// proxy sits between the adapter and the server. It can execute a request
// and drop its response (lost acknowledgement), inject events into open
// subscriptions and count calls.
type proxy struct {
	path, upstream string
	mu             sync.Mutex
	drop           map[string]int
	calls          map[string]int
	subs           map[net.Conn]*sync.Mutex
}

func newProxy(t *testing.T, upstream string) *proxy {
	dir, err := os.MkdirTemp("", "phd")
	if err != nil {
		t.Fatal(err)
	}
	p := &proxy{path: dir + "/p.sock", upstream: upstream, drop: map[string]int{}, calls: map[string]int{}, subs: map[net.Conn]*sync.Mutex{}}
	ln, err := net.Listen("unix", p.path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close(); os.RemoveAll(dir) })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go p.serve(c)
		}
	}()
	return p
}

func (p *proxy) dropNext(method string) {
	p.mu.Lock()
	p.drop[method]++
	p.mu.Unlock()
}

func (p *proxy) count(method string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls[method]
}

func (p *proxy) injectEventsLost() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	for c, wm := range p.subs {
		wm.Lock()
		c.Write([]byte(`{"event":"events_lost","data":{}}` + "\n"))
		wm.Unlock()
	}
	return len(p.subs)
}

func (p *proxy) serve(c net.Conn) {
	defer c.Close()
	up, err := net.Dial("unix", p.upstream)
	if err != nil {
		return
	}
	defer up.Close()
	rd := bufio.NewReader(c)
	line, err := rd.ReadBytes('\n')
	if err != nil {
		return
	}
	var req struct {
		Method string `json:"method"`
	}
	_ = json.Unmarshal(line, &req)
	p.mu.Lock()
	p.calls[req.Method]++
	dropping := p.drop[req.Method] > 0
	if dropping {
		p.drop[req.Method]--
	}
	p.mu.Unlock()
	if _, err := up.Write(line); err != nil {
		return
	}
	urd := bufio.NewReader(up)
	if req.Method != "events.subscribe" {
		resp, err := urd.ReadBytes('\n')
		if err != nil || dropping {
			return // executed upstream, acknowledgement lost
		}
		c.Write(resp)
		return
	}
	wm := &sync.Mutex{}
	p.mu.Lock()
	p.subs[c] = wm
	p.mu.Unlock()
	defer func() { p.mu.Lock(); delete(p.subs, c); p.mu.Unlock() }()
	go func() { io.Copy(io.Discard, rd); up.Close() }()
	for {
		l, err := urd.ReadBytes('\n')
		if err != nil {
			return
		}
		wm.Lock()
		_, err = c.Write(l)
		wm.Unlock()
		if err != nil {
			return
		}
	}
}

func fixtureSchema(t *testing.T) []byte {
	b, err := os.ReadFile(filepath.Join("testdata", "schema-0.9.3-protocol22.json"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

type suite struct {
	srv     server
	px      *proxy
	ad      *Adapter
	worker  []string
	isReal  bool
	schema  []byte
	version string
}

func (s *suite) launch(t *testing.T, corr string, cps map[string]string) backend.LaunchReceipt {
	t.Helper()
	rc, err := s.ad.Launch(context.Background(), backend.LaunchSpec{Dir: os.TempDir(), Argv: s.worker, Correlation: corr,
		Env: map[string]string{"SAMSARA_ATTEMPT_ID": corr}, Checkpoints: map[string]string{},
		Checkpoint: func(k, v string) error {
			if cps != nil {
				cps[k] = v
			}
			return nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	return rc
}

func (s *suite) mustLaunch(t *testing.T, corr string) backend.Ref {
	t.Helper()
	rc := s.launch(t, corr, nil)
	if rc.Outcome != backend.Delivered || rc.Ref == nil {
		t.Fatalf("launch: %s %s", rc.Outcome, rc.Detail)
	}
	return *rc.Ref
}

// started is what the server shows once the worker command runs.
func (s *suite) started() string {
	if s.isReal {
		return "FAKECLAUDE"
	}
	return "LAUNCHED"
}

func (s *suite) setAgent(t *testing.T, ref backend.Ref, state string) {
	t.Helper()
	// Wait until herdr sees the agent process, then report its state the
	// way the Claude integration hook does.
	deadline := time.Now().Add(10 * time.Second)
	for {
		err := s.ad.Client.Call(context.Background(), "agent.get", map[string]string{"target": ref.Fields["pane_id"]}, nil)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("agent never detected: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := s.ad.Client.Call(context.Background(), "pane.report_agent", map[string]string{"pane_id": ref.Fields["pane_id"],
		"source": "samsara-contract", "agent": "claude", "state": state}, nil); err != nil {
		t.Fatal(err)
	}
}

func (s *suite) set(tmuxUp bool) *backend.Set {
	c := domain.NewFakeClock(time.Unix(0, 0))
	tm := fake.New(domain.BackendTmux, c)
	if !tmuxUp {
		tm.Availability = domain.BackendUnavailable
	}
	return backend.NewSet(c, s.ad, tm, &standalone.Adapter{Clock: c})
}

var autoPolicy = backend.Policy{Mode: "auto", Order: []string{"herdr", "tmux", "standalone"}, AllowFallback: true}

func newSuite(t *testing.T, srv server, real bool, worker []string) *suite {
	s := &suite{srv: srv, isReal: real, worker: worker, schema: fixtureSchema(t)}
	s.px = newProxy(t, srv.socket())
	s.ad = &Adapter{Binary: "herdr", Client: &Client{Socket: s.px.path, Timeout: 5 * time.Second}, Clock: domain.SystemClock{},
		VerifiedSchemaSHA256: domain.DigestBytes(s.schema), ReadyTimeout: 5 * time.Second}
	if !real {
		s.ad.LiveSchema = func(context.Context) ([]byte, error) { return s.schema, nil }
	} else {
		out, _ := exec.Command("herdr", "--version").Output()
		s.ad.VerifiedVersion = strings.TrimSpace(string(out))
	}
	return s
}

// runContract runs BE-01..BE-11 against one server. The adapter-level
// expectation of each case follows 05 §9; the same cases run against the
// in-process fake (always) and a real herdr (SAMSARA_HERDR_IT=1).
func runContract(t *testing.T, s *suite) {
	ctx := context.Background()
	steps := []struct {
		name string
		fn   func(t *testing.T)
	}{
		{"BE-01 verified herdr is selected in auto mode", func(t *testing.T) {
			r, _ := s.ad.Probe(ctx)
			if r.Availability != domain.BackendAvailable || !r.Capabilities.CreateWorker.Usable() || !r.Capabilities.SubmitPrompt.Usable() {
				t.Fatalf("probe: %+v", r)
			}
			d, err := s.set(true).ResolveForLaunch(ctx, autoPolicy, []string{"submit_prompt", "observe_process"})
			if err != nil || d.Selected != domain.BackendHerdr {
				t.Fatalf("selected %q err %v rejected %v", d.Selected, err, d.Rejected)
			}
		}},
		{"BE-03 no herdr binary and no tmux: standalone, create false", func(t *testing.T) {
			missing := &Adapter{Binary: "samsara-no-such-herdr", Client: &Client{Socket: s.px.path}, VerifiedSchemaSHA256: s.ad.VerifiedSchemaSHA256}
			r, _ := missing.Probe(ctx)
			if r.Availability != domain.BackendUnavailable || r.Capabilities.CreateWorker.Usable() {
				t.Fatalf("probe: %+v", r)
			}
			c := domain.NewFakeClock(time.Unix(0, 0))
			tm := fake.New(domain.BackendTmux, c)
			tm.Availability = domain.BackendUnavailable
			d, err := backend.NewSet(c, missing, tm, &standalone.Adapter{Clock: c}).ResolveForLaunch(ctx, autoPolicy, nil)
			if err != nil || d.Selected != domain.BackendStandalone || !d.Manual {
				t.Fatalf("got %+v err %v", d, err)
			}
		}},
		{"BE-04 explicit tmux is respected", func(t *testing.T) {
			d, err := s.set(true).ResolveForLaunch(ctx, backend.Policy{Mode: "tmux", Order: autoPolicy.Order}, nil)
			if err != nil || d.Selected != domain.BackendTmux {
				t.Fatalf("got %+v err %v", d, err)
			}
		}},
		{"BE-05 a session herdr does not own is never adopted or driven", func(t *testing.T) {
			p := s.srv.createForeign(t, "user-workspace")
			ref, _, err := s.ad.ref(ctx, "user-workspace", p)
			if err != nil {
				t.Fatal(err)
			}
			if got, _ := s.ad.Lookup(ctx, "user-workspace"); got != nil {
				t.Fatalf("adopted foreign workspace %+v", got)
			}
			if rc, _ := s.ad.Submit(ctx, backend.PromptRequest{Ref: *ref, Text: "x"}); rc.Outcome != backend.PolicySkipped {
				t.Fatalf("submit to foreign pane: %s", rc.Outcome)
			}
			if rc, _ := s.ad.CloseOwnedSession(ctx, *ref); rc.Outcome != backend.PolicySkipped {
				t.Fatalf("close foreign pane: %s", rc.Outcome)
			}
		}},
		{"BE-08 working or blocked agent is not given automated input", func(t *testing.T) {
			ref := s.mustLaunch(t, "be08")
			for _, st := range []string{"working", "blocked"} {
				s.setAgent(t, ref, st)
				if rc, _ := s.ad.Submit(ctx, backend.PromptRequest{Ref: ref, Text: "must-not-arrive-" + st}); rc.Outcome != backend.PolicySkipped {
					t.Fatalf("%s: %s %s", st, rc.Outcome, rc.Detail)
				}
			}
			s.setAgent(t, ref, "idle")
			if rc, _ := s.ad.Submit(ctx, backend.PromptRequest{Ref: ref, Text: "ready-prompt"}); rc.Outcome != backend.Delivered {
				t.Fatalf("idle: %s %s", rc.Outcome, rc.Detail)
			}
			seen := waitSeen(t, s.srv, ref, "ready-prompt")
			if strings.Contains(seen, "must-not-arrive") {
				t.Fatalf("blocked input arrived: %q", seen)
			}
		}},
		{"BE-09 prompt ACK lost is UNKNOWN, not NOT_SENT", func(t *testing.T) {
			ref := s.mustLaunch(t, "be09")
			s.setAgent(t, ref, "idle")
			s.px.dropNext("agent.prompt")
			rc, _ := s.ad.Submit(ctx, backend.PromptRequest{Ref: ref, Text: "ack-lost-prompt"})
			if rc.Outcome != backend.Ambiguous {
				t.Fatalf("got %s %s", rc.Outcome, rc.Detail)
			}
			waitSeen(t, s.srv, ref, "ack-lost-prompt") // it did run: resending would duplicate
		}},
		{"BE-10 launch ACK lost: retry adopts, one workspace", func(t *testing.T) {
			s.px.dropNext("workspace.create")
			if rc := s.launch(t, "be10", nil); rc.Outcome != backend.Ambiguous {
				t.Fatalf("first launch: %s %s", rc.Outcome, rc.Detail)
			}
			cps := map[string]string{}
			rc := s.launch(t, "be10", cps)
			if rc.Outcome != backend.Delivered || rc.Ref == nil {
				t.Fatalf("retry: %s %s", rc.Outcome, rc.Detail)
			}
			if cps["command_sent"] != "1" {
				t.Fatalf("retry did not start the worker in the adopted pane: %v", cps)
			}
			if rc := s.launch(t, "be10", map[string]string{}); rc.Outcome != backend.Delivered || rc.Ref.Key() != (*rc.Ref).Key() {
				t.Fatalf("third launch: %s", rc.Outcome)
			}
			if n := countLabel(t, s.ad, WorkspacePrefix+"be10"); n != 1 {
				t.Fatalf("workspaces for correlation: %d", n)
			}
			s.setAgent(t, *rc.Ref, "idle") // the worker command runs exactly once in that pane
		}},
		{"BE-10 command ACK lost: retry clears the line, worker starts once", func(t *testing.T) {
			s.px.dropNext("pane.send_text")
			if rc := s.launch(t, "be10txt", nil); rc.Outcome != backend.Ambiguous {
				t.Fatalf("first launch: %s %s", rc.Outcome, rc.Detail)
			}
			rc := s.launch(t, "be10txt", map[string]string{})
			if rc.Outcome != backend.Delivered {
				t.Fatalf("retry: %s %s", rc.Outcome, rc.Detail)
			}
			seen := waitSeen(t, s.srv, *rc.Ref, s.started())
			if strings.Count(seen, s.started()) != 1 || strings.Contains(seen, "BROKEN") || strings.Contains(seen, "'exec") {
				t.Fatalf("worker command not started exactly once: %q", seen)
			}
		}},
		{"BE-10 checkpoints record both launch steps", func(t *testing.T) {
			cps := map[string]string{}
			if rc := s.launch(t, "be10cp", cps); rc.Outcome != backend.Delivered {
				t.Fatalf("launch: %s", rc.Outcome)
			}
			for _, k := range []string{"workspace_id", "pane_id", "generation", "command_sent"} {
				if cps[k] == "" {
					t.Fatalf("missing checkpoint %s in %v", k, cps)
				}
			}
		}},
		{"launch without env (doctor exercise worker)", func(t *testing.T) {
			rc, err := s.ad.Launch(ctx, backend.LaunchSpec{Dir: os.TempDir(), Argv: s.worker, Correlation: "noenv"})
			if err != nil || rc.Outcome != backend.Delivered {
				t.Fatalf("launch: %s %s %v", rc.Outcome, rc.Detail, err)
			}
			if obs, err := s.ad.Observe(ctx, *rc.Ref); err != nil || obs.Liveness != domain.LivenessAlive {
				t.Fatalf("observe: %+v %v", obs, err)
			}
			if cl, _ := s.ad.CloseOwnedSession(ctx, *rc.Ref); cl.Outcome != backend.Delivered {
				t.Fatalf("close: %s %s", cl.Outcome, cl.Detail)
			}
		}},
		{"BE-11 events_lost: stale, resubscribe, authoritative reread", func(t *testing.T) {
			ref := s.mustLaunch(t, "be11")
			wctx, cancel := context.WithCancel(ctx)
			defer cancel()
			var mu sync.Mutex
			var trail []bool
			go s.ad.Watch(wctx, func() {
				st, _ := s.ad.Stale()
				mu.Lock()
				trail = append(trail, st)
				mu.Unlock()
			})
			waitFor(t, func() bool { return s.px.injectEventsLost() > 0 })
			waitFor(t, func() bool { st, _ := s.ad.Stale(); return st })
			if obs, err := s.ad.Observe(ctx, ref); err != nil || obs.Source != "herdr.reread" || obs.Liveness != domain.LivenessAlive {
				t.Fatalf("stale observe: %+v %v", obs, err)
			}
			if r, _ := s.ad.Probe(ctx); len(r.Reasons) == 0 || !strings.HasPrefix(r.Reasons[0], "EVENTS_STALE") {
				t.Fatalf("probe does not surface staleness: %v", r.Reasons)
			}
			waitFor(t, func() bool { st, _ := s.ad.Stale(); return !st })
			before := s.px.count("pane.get")
			obs, err := s.ad.Observe(ctx, ref)
			if err != nil || obs.Liveness != domain.LivenessAlive || obs.Source != "herdr" || s.px.count("pane.get") != before+1 {
				t.Fatalf("reread: %+v %v", obs, err)
			}
			mu.Lock()
			defer mu.Unlock()
			if !containsSeq(trail, true, false) {
				t.Fatalf("reconcile was not triggered on stale then on resubscribe: %v", trail)
			}
		}},
		{"BE-02/BE-06 server down: worker UNKNOWN, new workers go to tmux", func(t *testing.T) {
			ref := s.mustLaunch(t, "be06")
			s.srv.stop(t)
			obs, err := s.ad.Observe(ctx, ref)
			if err == nil || obs.Liveness != domain.LivenessUnknown {
				t.Fatalf("observe while down: %+v %v", obs, err)
			}
			r, _ := s.ad.Probe(ctx)
			if r.Availability != domain.BackendUnavailable {
				t.Fatalf("probe while down: %+v", r)
			}
			d, err := s.set(true).ResolveForLaunch(ctx, autoPolicy, nil)
			if err != nil || d.Selected != domain.BackendTmux || d.Rejected[domain.BackendHerdr] == "" {
				t.Fatalf("got %+v err %v", d, err)
			}
			s.srv.start(t)
		}},
		{"BE-07 restart or restore: same pane id is not the old worker", func(t *testing.T) {
			ref := s.mustLaunch(t, "be07")
			s.srv.restart(t)
			waitFor(t, func() bool { return s.ad.Client.Call(ctx, "ping", nil, nil) == nil })
			obs, err := s.ad.Observe(ctx, ref)
			if err != nil || obs.Liveness == domain.LivenessAlive {
				t.Fatalf("old generation treated as alive: %+v %v", obs, err)
			}
			if rc, _ := s.ad.Submit(ctx, backend.PromptRequest{Ref: ref, Text: "x"}); rc.Outcome != backend.PolicySkipped {
				t.Fatalf("prompt to restored pane: %s", rc.Outcome)
			}
		}},
	}
	for _, st := range steps {
		if !t.Run(st.name, st.fn) {
			return
		}
	}
}

func containsSeq(xs []bool, a, b bool) bool {
	for i, x := range xs {
		if x == a {
			for _, y := range xs[i+1:] {
				if y == b {
					return true
				}
			}
		}
	}
	return false
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func waitSeen(t *testing.T, srv server, ref backend.Ref, text string) string {
	t.Helper()
	var seen string
	waitFor(t, func() bool { seen = srv.seen(t, ref.Fields["pane_id"]); return strings.Contains(seen, text) })
	return seen
}

func countLabel(t *testing.T, a *Adapter, label string) int {
	var ws struct {
		Workspaces []workspaceInfo `json:"workspaces"`
	}
	if err := a.Client.Call(context.Background(), "workspace.list", nil, &ws); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, w := range ws.Workspaces {
		if w.Label == label {
			n++
		}
	}
	return n
}

func TestContractFake(t *testing.T) {
	runContract(t, newSuite(t, newFakeServer(t), false, []string{"claude"}))
}

// TestContractRealHerdr runs the suite against an isolated herdr server
// (its own XDG_CONFIG_HOME, /bin/sh panes, a stand-in `claude` that echoes
// its input). It never touches the user's herdr session.
func TestContractRealHerdr(t *testing.T) {
	if os.Getenv("SAMSARA_HERDR_IT") != "1" {
		t.Skip("set SAMSARA_HERDR_IT=1 to run against the installed herdr")
	}
	r := newRealServer(t)
	s := newSuite(t, r, true, []string{r.claude})
	if out, _ := exec.Command("herdr", "api", "schema", "--json").Output(); domain.DigestBytes(out) != s.ad.VerifiedSchemaSHA256 {
		t.Fatalf("installed herdr schema %s is not the pinned fixture", domain.DigestBytes(out))
	}
	runContract(t, s)
}
