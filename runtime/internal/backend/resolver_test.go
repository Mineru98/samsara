package backend_test

import (
	"context"
	"testing"
	"time"

	"github.com/Mineru98/samsara/runtime/internal/backend"
	"github.com/Mineru98/samsara/runtime/internal/backend/fake"
	"github.com/Mineru98/samsara/runtime/internal/backend/standalone"
	"github.com/Mineru98/samsara/runtime/internal/domain"
)

func set(herdrUp, tmuxUp bool) *backend.Set {
	c := domain.NewFakeClock(time.Unix(0, 0))
	h, t := fake.New(domain.BackendHerdr, c), fake.New(domain.BackendTmux, c)
	if !herdrUp {
		h.Availability = domain.BackendUnavailable
	}
	if !tmuxUp {
		t.Availability = domain.BackendUnavailable
	}
	return backend.NewSet(c, h, t, &standalone.Adapter{Clock: c})
}

var auto = backend.Policy{Mode: "auto", Order: []string{"herdr", "tmux", "standalone"}, AllowFallback: true}

func TestResolverSelection(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name          string
		herdr, tmux   bool
		policy        backend.Policy
		want          string
		manual, isErr bool
	}{
		{"BE-01 both available", true, true, auto, "herdr", false, false},
		{"BE-02 herdr down", false, true, auto, "tmux", false, false},
		{"BE-03 none", false, false, auto, "standalone", true, false},
		{"BE-04 explicit tmux", true, true, backend.Policy{Mode: "tmux", Order: auto.Order}, "tmux", false, false},
		{"explicit tmux unavailable, no fallback", true, false, backend.Policy{Mode: "tmux", Order: auto.Order}, "", false, true},
		{"explicit tmux unavailable, fallback", true, false, backend.Policy{Mode: "tmux", Order: auto.Order, AllowFallback: true}, "herdr", false, false},
	}
	for _, c := range cases {
		d, err := set(c.herdr, c.tmux).ResolveForLaunch(ctx, c.policy, nil)
		if c.isErr {
			if !domain.IsCode(err, domain.CodeUnsupportedCapability) {
				t.Fatalf("%s: expected UNSUPPORTED_CAPABILITY, got %v", c.name, err)
			}
			continue
		}
		if err != nil || d.Selected != c.want || d.Manual != c.manual {
			t.Fatalf("%s: got %+v %v", c.name, d, err)
		}
	}
}

func TestStandaloneNeverFakesCapabilities(t *testing.T) {
	r, _ := (&standalone.Adapter{}).Probe(context.Background())
	if r.Capabilities.CreateWorker.Usable() || r.Capabilities.SubmitPrompt.Usable() || !r.Capabilities.AttachSession.Usable() {
		t.Fatalf("standalone capabilities: %+v", r.Capabilities)
	}
	// A plan requiring submit_prompt cannot fall back to standalone.
	if _, err := set(false, false).ResolveForLaunch(context.Background(), auto, []string{"submit_prompt"}); err == nil {
		t.Fatal("missing capability must not resolve")
	}
}
