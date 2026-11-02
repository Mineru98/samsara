package herdr

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
)

// fakeServer speaks the subset of herdr protocol 22 the adapter uses, with
// the response shapes recorded from herdr 0.9.3. It lets the contract suite
// run where herdr is not installed.
type fakeServer struct {
	t    *testing.T
	path string

	mu         sync.Mutex
	ln         net.Listener
	conns      map[net.Conn]bool
	workspaces map[string]*fakeWorkspace
	panes      map[string]*fakePane
	order      []string
	nextWS     int
	nextTerm   int
}

type fakeWorkspace struct {
	id, label string
	panes     []string
}

type fakePane struct {
	id, ws, tab, terminal string
	typed                 string
	launched              bool
	status                string
	prompts               []string
}

func newFakeServer(t *testing.T) *fakeServer {
	dir, err := os.MkdirTemp("", "fhd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	f := &fakeServer{t: t, path: dir + "/herdr.sock", workspaces: map[string]*fakeWorkspace{}, panes: map[string]*fakePane{}}
	f.start(t)
	t.Cleanup(func() { f.stop(t) })
	return f
}

func (f *fakeServer) socket() string { return f.path }

func (f *fakeServer) start(t *testing.T) {
	ln, err := net.Listen("unix", f.path)
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.ln, f.conns = ln, map[net.Conn]bool{}
	f.mu.Unlock()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			f.mu.Lock()
			f.conns[c] = true
			f.mu.Unlock()
			go f.serve(c)
		}
	}()
}

func (f *fakeServer) stop(t *testing.T) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ln == nil {
		return
	}
	f.ln.Close()
	for c := range f.conns {
		c.Close()
	}
	f.ln = nil
	os.Remove(f.path)
}

// restart simulates a server restart with native restore: every pane comes
// back under the same id with a new terminal and nothing running in it.
func (f *fakeServer) restart(t *testing.T) {
	f.stop(t)
	f.mu.Lock()
	for _, p := range f.panes {
		f.nextTerm++
		p.terminal = fmt.Sprintf("term_fake%d", f.nextTerm)
		p.launched, p.status, p.typed = false, "unknown", ""
	}
	f.mu.Unlock()
	f.start(t)
}

func (f *fakeServer) setAgent(t *testing.T, pane, status string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.panes[pane].status = status
}

func (f *fakeServer) createForeign(t *testing.T, label string) paneInfo {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.createLocked(label)
}

func (f *fakeServer) prompts(pane string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.panes[pane].prompts...)
}

func (f *fakeServer) createLocked(label string) paneInfo {
	f.nextWS++
	f.nextTerm++
	ws := fmt.Sprintf("w%d", f.nextWS)
	p := &fakePane{id: ws + ":p1", ws: ws, tab: ws + ":t1", terminal: fmt.Sprintf("term_fake%d", f.nextTerm), status: "unknown"}
	f.workspaces[ws] = &fakeWorkspace{id: ws, label: label, panes: []string{p.id}}
	f.panes[p.id] = p
	f.order = append(f.order, ws)
	return f.info(p)
}

func (f *fakeServer) info(p *fakePane) paneInfo {
	pi := paneInfo{PaneID: p.id, TabID: p.tab, WorkspaceID: p.ws, TerminalID: p.terminal, AgentStatus: p.status}
	if p.launched {
		pi.Agent = "claude"
	}
	return pi
}

func (f *fakeServer) serve(c net.Conn) {
	defer c.Close()
	line, err := bufio.NewReader(c).ReadBytes('\n')
	if err != nil {
		return
	}
	var req struct {
		ID     string          `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(line, &req); err != nil {
		return
	}
	reply := func(result any) {
		b, _ := json.Marshal(map[string]any{"id": req.ID, "result": result})
		c.Write(append(b, '\n'))
	}
	fail := func(code, msg string) {
		b, _ := json.Marshal(map[string]any{"id": req.ID, "error": map[string]string{"code": code, "message": msg}})
		c.Write(append(b, '\n'))
	}
	var p map[string]any
	_ = json.Unmarshal(req.Params, &p)
	str := func(k string) string { s, _ := p[k].(string); return s }
	if req.Method == "events.subscribe" {
		reply(map[string]string{"type": "subscription_started"})
		buf := make([]byte, 1)
		c.Read(buf) // held open until the server stops or the client leaves
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	pane := f.panes[str("pane_id")]
	if t := str("target"); t != "" {
		pane = f.panes[t]
	}
	switch req.Method {
	case "ping":
		reply(map[string]any{"type": "pong", "version": "0.9.3", "protocol": 22})
	case "workspace.create":
		if v, ok := p["env"]; ok && v == nil {
			fail("invalid_request", "invalid type: null, expected a map")
			return
		}
		reply(map[string]any{"type": "workspace_created", "root_pane": f.createLocked(str("label"))})
	case "workspace.list":
		var out []map[string]string
		for _, id := range f.order {
			if w := f.workspaces[id]; w != nil {
				out = append(out, map[string]string{"workspace_id": w.id, "label": w.label})
			}
		}
		reply(map[string]any{"type": "workspace_list", "workspaces": out})
	case "workspace.close":
		w := f.workspaces[str("workspace_id")]
		if w == nil {
			fail("workspace_not_found", "no workspace")
			return
		}
		for _, id := range w.panes {
			delete(f.panes, id)
		}
		delete(f.workspaces, w.id)
		reply(map[string]string{"type": "ok"})
	case "pane.list":
		var out []paneInfo
		if w := f.workspaces[str("workspace_id")]; w != nil {
			for _, id := range w.panes {
				out = append(out, f.info(f.panes[id]))
			}
		}
		reply(map[string]any{"type": "pane_list", "panes": out})
	case "pane.get", "pane.process_info", "pane.send_text", "pane.send_keys", "agent.get", "agent.prompt", "pane.report_agent":
		if pane == nil {
			if strings.HasPrefix(req.Method, "agent.") {
				fail("agent_not_found", "agent target not found")
			} else {
				fail("pane_not_found", "pane not found")
			}
			return
		}
		f.paneMethod(req.Method, pane, p, reply, fail)
	default:
		fail("unknown_method", req.Method)
	}
}

func (f *fakeServer) paneMethod(method string, pane *fakePane, p map[string]any, reply func(any), fail func(string, string)) {
	name := "sh"
	if pane.launched {
		name = "claude"
	}
	switch method {
	case "pane.get":
		reply(map[string]any{"type": "pane_info", "pane": f.info(pane)})
	case "pane.process_info":
		reply(map[string]any{"type": "pane_process_info", "process_info": map[string]any{"pane_id": pane.id, "shell_pid": os.Getpid(),
			"foreground_processes": []map[string]any{{"pid": os.Getpid(), "name": name}}}})
	case "pane.send_text":
		pane.typed += p["text"].(string)
		reply(map[string]string{"type": "ok"})
	case "pane.send_keys":
		keys, _ := p["keys"].([]any)
		switch {
		case len(keys) == 1 && keys[0] == "C-c":
			pane.typed = ""
		case len(keys) == 1 && keys[0] == "Enter" && strings.HasPrefix(pane.typed, "exec ") && !strings.Contains(pane.typed[1:], "exec "):
			pane.launched, pane.typed = true, ""
			pane.prompts = append(pane.prompts, "LAUNCHED")
		case len(keys) == 1 && keys[0] == "Enter":
			pane.prompts = append(pane.prompts, "BROKEN LINE: "+pane.typed)
			pane.typed = ""
		}
		reply(map[string]string{"type": "ok"})
	case "pane.report_agent":
		pane.status = p["state"].(string)
		reply(map[string]string{"type": "ok"})
	case "agent.get":
		if !pane.launched {
			fail("agent_not_found", "agent target not found")
			return
		}
		reply(map[string]any{"type": "agent_info", "agent": map[string]any{"agent": "claude", "agent_status": pane.status, "pane_id": pane.id}})
	case "agent.prompt":
		if !pane.launched || (pane.status != "idle" && pane.status != "done") {
			fail("agent_not_ready", "agent is not ready")
			return
		}
		pane.prompts = append(pane.prompts, p["text"].(string))
		reply(map[string]any{"type": "agent_prompted"})
	}
}
