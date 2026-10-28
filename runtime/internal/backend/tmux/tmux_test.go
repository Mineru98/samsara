package tmux

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/Mineru98/samsara/runtime/internal/backend"
	"github.com/Mineru98/samsara/runtime/internal/domain"
)

func TestTmuxLaunchObserveLookupClose(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	a := New(nil)
	a.Socket = fmt.Sprintf("samsara-test-%d", os.Getpid())
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", a.Socket, "kill-server").Run() })
	ctx := context.Background()
	p, _ := a.Probe(ctx)
	if p.Availability != domain.BackendAvailable || !p.Capabilities.CreateWorker.Usable() {
		t.Fatalf("probe: %+v", p)
	}
	dir := t.TempDir()
	spec := backend.LaunchSpec{Dir: dir, Argv: []string{"sh", "-c", "echo \"$SAMSARA_X\" > out.txt; sleep 30"}, Env: map[string]string{"SAMSARA_X": "hello world; $(rm -rf /)"}, Correlation: "corr1"}
	rc, err := a.Launch(ctx, spec)
	if err != nil || rc.Outcome != backend.Delivered || rc.Ref == nil {
		t.Fatalf("launch: %+v %v", rc, err)
	}
	// Lost-ack recovery: the same correlation is adopted, not duplicated.
	rc2, _ := a.Launch(ctx, spec)
	if rc2.Ref == nil || rc2.Ref.Key() != rc.Ref.Key() {
		t.Fatalf("relaunch must adopt: %+v", rc2)
	}
	ref, err := a.Lookup(ctx, "corr1")
	if err != nil || ref == nil || ref.Generation != rc.Ref.Generation {
		t.Fatalf("lookup: %+v %v", ref, err)
	}
	time.Sleep(300 * time.Millisecond)
	b, _ := os.ReadFile(dir + "/out.txt")
	if string(b) != "hello world; $(rm -rf /)\n" {
		t.Fatalf("env must be passed literally, got %q", b)
	}
	obs, err := a.Observe(ctx, *rc.Ref)
	if err != nil || obs.Liveness != domain.LivenessAlive {
		t.Fatalf("observe: %+v %v", obs, err)
	}
	// A different generation at the same pane is not our worker (BE-07).
	stale := *rc.Ref
	stale.Generation = "1@0"
	if o, _ := a.Observe(ctx, stale); o.Liveness == domain.LivenessAlive {
		t.Fatalf("stale generation must not be ALIVE: %+v", o)
	}
	if cl, _ := a.CloseOwnedSession(ctx, *rc.Ref); cl.Outcome != backend.Delivered {
		t.Fatalf("close: %+v", cl)
	}
	// Until the process is really gone the adapter must say UNKNOWN, never
	// EXITED; poll for the confirmed exit.
	deadline := time.Now().Add(5 * time.Second)
	for {
		obs, _ = a.Observe(ctx, *rc.Ref)
		if obs.Liveness == domain.LivenessExited {
			break
		}
		if obs.Liveness != domain.LivenessUnknown || time.Now().After(deadline) {
			t.Fatalf("after close: %+v", obs)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
