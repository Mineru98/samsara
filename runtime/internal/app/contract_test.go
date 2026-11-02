package app_test

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/Mineru98/samsara/runtime/api"
	"github.com/Mineru98/samsara/runtime/api/apitest"
	"github.com/Mineru98/samsara/runtime/internal/app"
	"github.com/Mineru98/samsara/runtime/internal/store/sqlite"
)

var (
	refSnapshot = api.Ref("snapshot.schema.json", "ProjectSnapshot")
	refEvent    = api.Ref("event.schema.json", "ProjectEvent")
	refError    = api.Ref("common.schema.json", "ErrorDetail")
	refPage     = api.Ref("snapshot.schema.json", "Page")
)

// fixture starts a contract fixture whose temp paths are made stable.
func (e *env) fixture(name, description string) *apitest.Recorder {
	repl := map[string]string{e.dir: "/fixture"}
	if real, err := filepath.EvalSymlinks(e.dir); err == nil {
		repl[real] = "/fixture"
	}
	return apitest.NewRecorder(e.t, name, description, repl)
}

// snapshot reads the dashboard snapshot and checks it against the contract.
func (e *env) snapshot() *sqlite.Snapshot {
	e.t.Helper()
	s, err := e.app.ProjectSnapshot(e.ctx, e.project)
	if err != nil {
		e.t.Fatal(err)
	}
	apitest.Check(e.t, refSnapshot, s)
	return s
}

// events replays every project event and checks each envelope.
func (e *env) events() []sqlite.EventEnvelope {
	e.t.Helper()
	b, err := e.app.ReadProjectEvents(e.ctx, e.project, app.Cursor{Scope: e.project, DBGen: e.db.DatabaseGeneration}, 1000)
	if err != nil || b.ResetRequired != "" {
		e.t.Fatalf("events: %v %s", err, b.ResetRequired)
	}
	for _, ev := range b.Events {
		apitest.Check(e.t, refEvent, ev)
	}
	return b.Events
}

// configJSON decodes a policy file the way the runtime reads it and renders
// it as JSON for the config schema.
func configJSON(t *testing.T, doc string) []byte {
	t.Helper()
	var v map[string]any
	if err := yaml.Unmarshal([]byte(doc), &v); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
