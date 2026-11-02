package herdr

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Mineru98/samsara/runtime/internal/domain"
)

// TestPinnedSchemaFixtures keeps PinnedSchemas and the committed fixtures in
// step: every pinned digest is the digest of its fixture file.
func TestPinnedSchemaFixtures(t *testing.T) {
	if len(PinnedSchemas) == 0 {
		t.Fatal("no pinned herdr schema")
	}
	for digest, path := range PinnedSchemas {
		b, err := os.ReadFile(filepath.FromSlash(path))
		if err != nil {
			t.Fatal(err)
		}
		if got := domain.DigestBytes(b); got != digest {
			t.Fatalf("%s: digest %s, pinned %s", path, got, digest)
		}
	}
}

func TestProbeReportsUnverifiedAndMismatch(t *testing.T) {
	srv := newFakeServer(t)
	fixture := fixtureSchema(t)
	other := append(append([]byte{}, fixture...), ' ')
	cases := []struct {
		name, verified string
		live           []byte
		want           string
	}{
		{"no manifest record", "", fixture, "HERDR_COMPAT_UNVERIFIED"},
		{"installed schema changed", domain.DigestBytes(fixture), other, "HERDR_SCHEMA_MISMATCH"},
		{"manifest verified an unpinned schema", domain.DigestBytes(other), other, "HERDR_SCHEMA_MISMATCH"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			live := c.live
			a := &Adapter{Client: &Client{Socket: srv.socket()}, VerifiedSchemaSHA256: c.verified,
				LiveSchema: func(context.Context) ([]byte, error) { return live, nil }}
			r, _ := a.Probe(context.Background())
			if r.Availability != domain.BackendDegraded || len(r.Reasons) != 1 || r.Reasons[0] != c.want {
				t.Fatalf("got %s %v", r.Availability, r.Reasons)
			}
			if r.Capabilities.CreateWorker.Usable() || r.Capabilities.SubmitPrompt.ReasonCode != c.want {
				t.Fatalf("capabilities not withheld: %+v", r.Capabilities)
			}
		})
	}
	t.Run("no installed version string to compare", func(t *testing.T) {
		a := &Adapter{Binary: "herdr", Client: &Client{Socket: srv.socket()}, VerifiedSchemaSHA256: domain.DigestBytes(fixture), VerifiedVersion: "herdr 0.0.1"}
		a.LiveSchema = func(context.Context) ([]byte, error) { return fixture, nil }
		if r, _ := a.Probe(context.Background()); r.Availability != domain.BackendAvailable {
			t.Fatalf("no installed version to compare must not fail: %+v", r)
		}
	})
}

func TestCommandLineQuoting(t *testing.T) {
	got, err := commandLine([]string{"/opt/samsara bin/samsara", "launcher", "--", "claude", "it's"})
	if err != nil {
		t.Fatal(err)
	}
	want := `exec '/opt/samsara bin/samsara' 'launcher' '--' 'claude' 'it'\''s'`
	if got != want {
		t.Fatalf("got %s", got)
	}
	if _, err := commandLine([]string{"claude", "line1\nrm -rf /"}); err == nil {
		t.Fatal("newline in argv must be refused")
	}
}
