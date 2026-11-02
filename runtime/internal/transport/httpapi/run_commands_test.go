package httpapi

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Mineru98/samsara/runtime/api"
	"github.com/Mineru98/samsara/runtime/internal/app"
	"github.com/Mineru98/samsara/runtime/internal/domain"
)

// TestRunStartAndReviseCommands covers the dashboard run commands (#110):
// available_actions per state, start behind the PLAN approval, the editable
// plan, revise and the rejection of a stale run generation.
func TestRunStartAndReviseCommands(t *testing.T) {
	s, pid, url := newServer(t)
	base := strings.SplitN(url, "/#", 2)[0]
	code := strings.SplitN(url, "#bootstrap=", 2)[1]
	_, b := do(t, "POST", base+"/v1/ui/sessions", "", `{"code":"`+code+`"}`)
	var tok struct{ Token string }
	_ = json.Unmarshal(b, &tok)
	ctx := context.Background()
	cmd := app.Cmd{Principal: app.Principal{ID: "t"}}
	applied, err := s.App.ApplyPlan(ctx, cmd, json.RawMessage(`{"spec_version":"1","project_id":"`+pid+`","expected_graph_version":"0",
		"issues":[{"client_key":"a","title":"A","acceptance_criteria":["x"]},{"client_key":"b","title":"B","acceptance_criteria":["y"]}],
		"dependencies":[{"predecessor_key":"a","successor_key":"b"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	runURL := base + "/v1/runs/" + applied.RunID
	var run struct {
		Version          string `json:"version"`
		State            string `json:"state"`
		AvailableActions []struct {
			Action string `json:"action"`
		} `json:"available_actions"`
	}
	getRun := func() {
		t.Helper()
		r, body := do(t, "GET", runURL, tok.Token, "")
		if r.StatusCode != 200 {
			t.Fatalf("get run: %d %s", r.StatusCode, body)
		}
		if err := api.ValidateStrict(api.Ref("entities.schema.json", "RunView"), body); err != nil {
			t.Fatalf("RunView contract: %v", err)
		}
		run.AvailableActions = nil
		_ = json.Unmarshal(body, &run)
	}
	actions := func() string {
		var out []string
		for _, a := range run.AvailableActions {
			out = append(out, a.Action)
		}
		return strings.Join(out, ",")
	}
	postKey := func(path, ifMatch, body, key string) (int, []byte) {
		t.Helper()
		hdr := []string{"Idempotency-Key", key, "Content-Type", "application/json"}
		if ifMatch != "" {
			hdr = append(hdr, "If-Match", `"`+ifMatch+`"`)
		}
		r, rb := do(t, "POST", runURL+path, tok.Token, body, hdr...)
		return r.StatusCode, rb
	}
	post := func(path, ifMatch, body string) (int, []byte) {
		t.Helper()
		return postKey(path, ifMatch, body, domain.NewID("k"))
	}
	errOf := func(b []byte) (string, map[string]any) {
		var e struct {
			Error struct {
				Code    string         `json:"code"`
				Details map[string]any `json:"details"`
			} `json:"error"`
		}
		_ = json.Unmarshal(b, &e)
		return e.Error.Code, e.Error.Details
	}

	getRun()
	if run.State != "PLANNED" || actions() != "START_RUN" {
		t.Fatalf("PLANNED actions = %q", actions())
	}
	// Manual PLAN approval: start is refused with the approval to open.
	st, rb := post("/start", applied.RunID+":v"+run.Version, "")
	if c, d := errOf(rb); st != 409 || c != domain.CodeApprovalRequired || d["approval_id"] == nil {
		t.Fatalf("start before approval: %d %s", st, rb)
	}
	_, d := errOf(rb)
	if _, err := s.App.DecideApproval(ctx, cmd, d["approval_id"].(string), app.DecisionRequest{Decision: "APPROVE"}); err != nil {
		t.Fatal(err)
	}
	if st, rb := post("/start", applied.RunID+":v"+run.Version, ""); st != 202 {
		t.Fatalf("start: %d %s", st, rb)
	} else if err := api.ValidateStrict(api.Ref("ipc.schema.json", "RunCommandResult"), rb); err != nil {
		t.Fatalf("start contract: %v", err)
	}
	getRun()
	if run.State != "ACTIVE" || actions() != "PAUSE_RUN" {
		t.Fatalf("ACTIVE actions = %q", actions())
	}
	if st, rb := post("/pause", applied.RunID+":v"+run.Version, ""); st != 202 {
		t.Fatalf("pause: %d %s", st, rb)
	}
	getRun()
	if run.State != "PAUSED" || actions() != "RESUME_RUN,REVISE_PLAN" {
		t.Fatalf("PAUSED actions = %q", actions())
	}

	// The editable plan carries the current graph version.
	r, pb := do(t, "GET", runURL+"/plan", tok.Token, "")
	if r.StatusCode != 200 {
		t.Fatalf("plan: %d %s", r.StatusCode, pb)
	}
	if err := api.ValidateStrict(api.Ref("ipc.schema.json", "RunPlan"), pb); err != nil {
		t.Fatalf("RunPlan contract: %v", err)
	}
	var rp struct {
		RunGeneration string         `json:"run_generation"`
		GraphVersion  string         `json:"graph_version"`
		Plan          map[string]any `json:"plan"`
	}
	_ = json.Unmarshal(pb, &rp)
	if rp.RunGeneration != "1" || rp.Plan["expected_graph_version"] != rp.GraphVersion {
		t.Fatalf("plan view: %s", pb)
	}
	// Add an issue: execution meaning changes, the generation moves to 2.
	rp.Plan["issues"] = append(rp.Plan["issues"].([]any), map[string]any{"client_key": "c", "title": "C", "acceptance_criteria": []string{"z"}})
	revised, _ := json.Marshal(map[string]any{"expected_run_generation": rp.RunGeneration, "plan": rp.Plan})
	reviseKey := domain.NewID("k")
	st, rb = postKey("/revise", applied.RunID+":v"+run.Version, string(revised), reviseKey)
	if st != 202 {
		t.Fatalf("revise: %d %s", st, rb)
	}
	// A retry of the same intent (same key, same body) replays the stored
	// result instead of revising again.
	if st2, rb2 := postKey("/revise", applied.RunID+":v"+run.Version, string(revised), reviseKey); st2 != 202 || string(rb2) != string(rb) {
		t.Fatalf("revise replay: %d %s (first %s)", st2, rb2, rb)
	}
	if err := api.ValidateStrict(api.Ref("ipc.schema.json", "PlanReviseResult"), rb); err != nil {
		t.Fatalf("revise contract: %v", err)
	}
	var res app.PlanReviseResult
	_ = json.Unmarshal(rb, &res)
	if res.RunGeneration != "2" || res.ChangeKind != "EXECUTION" || !strings.HasPrefix(res.PlanDigest, "sha256:") {
		t.Fatalf("revise result: %s", rb)
	}
	// Same edit again from the stale generation (and stale If-Match, as the
	// dashboard sends it): refused as a generation change, never applied twice.
	for _, im := range []string{"", applied.RunID + ":v" + run.Version} {
		st, rb = post("/revise", im, string(revised))
		if c, d := errOf(rb); st != 412 || c != domain.CodeVersionMismatch || d["current_run_generation"] != "2" {
			t.Fatalf("stale revise (If-Match %q): %d %s", im, st, rb)
		}
	}
	if st, rb := post("/revise", "", `{"run_id":"x","expected_run_generation":"2","plan":{}}`); st != 400 {
		t.Fatalf("revise body with run_id must be rejected: %d %s", st, rb)
	}

	// Resume after an execution change needs the new generation's PLAN
	// approval: refused with its id, then accepted once approved.
	getRun()
	st, rb = post("/resume", applied.RunID+":v"+run.Version, "")
	c, d := errOf(rb)
	if st != 409 || c != domain.CodeApprovalRequired || d["approval_id"] != res.PlanApprovalID || res.PlanApprovalID == "" {
		t.Fatalf("resume before approval: %d %s (plan_approval_id %q)", st, rb, res.PlanApprovalID)
	}
	if _, err := s.App.DecideApproval(ctx, cmd, res.PlanApprovalID, app.DecisionRequest{Decision: "APPROVE"}); err != nil {
		t.Fatal(err)
	}
	if st, rb := post("/resume", applied.RunID+":v"+run.Version, ""); st != 202 {
		t.Fatalf("resume: %d %s", st, rb)
	}
	getRun()
	if run.State != "ACTIVE" || actions() != "PAUSE_RUN" {
		t.Fatalf("resumed run: %s %q", run.State, actions())
	}
}
