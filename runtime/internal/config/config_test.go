package config

import (
	"strings"
	"testing"
)

func TestParseStrictAndDefaults(t *testing.T) {
	c, err := Parse([]byte("version: 1\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Backend.Mode != "auto" || strings.Join(c.Backend.Order, ",") != "herdr,tmux,standalone" || c.Git.IntegrationAuthority != "manual" {
		t.Fatalf("defaults: %+v", c)
	}
	bad := map[string]string{
		"unknown field":  "version: 1\nfoo: 1\n",
		"negative":       "version: 1\nexecution:\n  max_workers: -1\n",
		"backend":        "version: 1\nbackend:\n  mode: screen\n",
		"short ref":      "version: 1\ngit:\n  target_ref: main\n",
		"injection ref":  "version: 1\ngit:\n  target_ref: refs/heads/--upload-pack\n",
		"authority":      "version: 1\ngit:\n  integration_authority: yolo\n",
		"heartbeat":      "version: 1\nexecution:\n  lease_ttl_seconds: 10\n  heartbeat_seconds: 9\n",
		"approval value": "version: 1\napproval:\n  result: maybe\n",
	}
	for name, y := range bad {
		if _, err := Parse([]byte(y)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}
