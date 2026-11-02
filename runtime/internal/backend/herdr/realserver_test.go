package herdr

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// realServer runs an isolated `herdr server` under a private
// XDG_CONFIG_HOME, so the user's running herdr session is never touched.
type realServer struct {
	dir, claude string
	cmd         *exec.Cmd
	client      *Client
}

func newRealServer(t *testing.T) *realServer {
	if _, err := exec.LookPath("herdr"); err != nil {
		t.Skip("herdr not installed")
	}
	dir, err := os.MkdirTemp("/tmp", "shd") // short: unix socket paths are limited
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "bin")
	_ = os.MkdirAll(bin, 0o755)
	claude := filepath.Join(bin, "claude")
	script := "#!/bin/sh\necho \"FAKECLAUDE $*\"\nwhile IFS= read -r line; do echo \"GOT: $line\"; done\n"
	if err := os.WriteFile(claude, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	r := &realServer{dir: dir, claude: claude, client: &Client{Socket: filepath.Join(dir, "herdr", "herdr.sock"), Timeout: 5 * time.Second}}
	r.start(t)
	t.Cleanup(func() { r.stop(t); os.RemoveAll(dir) })
	return r
}

func (r *realServer) socket() string { return r.client.Socket }

func (r *realServer) start(t *testing.T) {
	cmd := exec.Command("herdr", "server")
	env := []string{"XDG_CONFIG_HOME=" + r.dir, "SHELL=/bin/sh", "PATH=" + filepath.Join(r.dir, "bin") + ":" + os.Getenv("PATH")}
	for _, kv := range os.Environ() {
		switch k, _, _ := cutEq(kv); k {
		case "XDG_CONFIG_HOME", "SHELL", "PATH", "HERDR_SOCKET_PATH", "HERDR_ENV", "HERDR_PANE_ID", "HERDR_TAB_ID", "HERDR_WORKSPACE_ID":
		default:
			env = append(env, kv)
		}
	}
	cmd.Env = env
	cmd.Stdout, cmd.Stderr = nil, nil
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	r.cmd = cmd
	deadline := time.Now().Add(10 * time.Second)
	for r.client.Call(context.Background(), "ping", nil, nil) != nil {
		if time.Now().After(deadline) {
			t.Fatal("isolated herdr server did not start")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func cutEq(kv string) (string, string, bool) {
	for i := 0; i < len(kv); i++ {
		if kv[i] == '=' {
			return kv[:i], kv[i+1:], true
		}
	}
	return kv, "", false
}

// stop shuts the server down through its API (it saves the session for
// native restore) and waits for the process.
func (r *realServer) stop(t *testing.T) {
	if r.cmd == nil {
		return
	}
	_ = r.client.Call(context.Background(), "server.stop", nil, nil)
	done := make(chan struct{})
	go func() { _ = r.cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		_ = r.cmd.Process.Kill()
		<-done
	}
	r.cmd = nil
}

func (r *realServer) restart(t *testing.T) {
	r.stop(t)
	r.start(t)
}

func (r *realServer) createForeign(t *testing.T, label string) paneInfo {
	var res struct {
		RootPane paneInfo `json:"root_pane"`
	}
	if err := r.client.Call(context.Background(), "workspace.create", map[string]any{"cwd": r.dir, "label": label}, &res); err != nil {
		t.Fatal(err)
	}
	return res.RootPane
}

func (r *realServer) seen(t *testing.T, pane string) string {
	var res struct {
		Read struct {
			Text string `json:"text"`
		} `json:"read"`
	}
	if err := r.client.Call(context.Background(), "pane.read", map[string]any{"pane_id": pane, "source": "recent", "lines": 50}, &res); err != nil {
		return ""
	}
	return res.Read.Text
}
