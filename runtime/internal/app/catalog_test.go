package app_test

import (
	"encoding/json"
	"testing"

	"github.com/Mineru98/samsara/runtime/internal/app"
	"github.com/Mineru98/samsara/runtime/internal/store/sqlite"
)

// The dashboard sidebar reads ProjectSummary counters from catalog events.
// Changes to runs, attempts and approvals must reach the catalog stream, not
// only changes to the project row itself (#69).
func TestCatalogFollowsSummaryCounters(t *testing.T) {
	e := newEnv(t, baseConfig)
	res := e.applyPlan(abcPlan)
	snap, err := e.app.GetCatalogSnapshot(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	cur, err := app.DecodeCursor(snap.SnapshotCursor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.app.StartRun(e.ctx, e.cmd, res.RunID); err != nil {
		t.Fatal(err)
	}
	e.reconcile(3)
	e.register(res.IssueMapping["a"], "claude-A")
	e.register(res.IssueMapping["c"], "claude-C")

	evs, reset, err := e.app.ReadCatalogEvents(e.ctx, cur, 200)
	if err != nil || reset != "" {
		t.Fatalf("read catalog: %v %q", err, reset)
	}
	if len(evs) == 0 {
		t.Fatal("starting a run produced no catalog event")
	}
	var last struct {
		Project sqlite.ProjectSummary `json:"project"`
	}
	if err := json.Unmarshal(evs[len(evs)-1].Payload, &last); err != nil {
		t.Fatal(err)
	}
	var want *sqlite.ProjectSummary
	if err := e.db.Read(e.ctx, func(q sqlite.Q) error {
		var err error
		want, err = sqlite.LoadProjectSummary(e.ctx, q, e.project, 0)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if last.Project.Running != want.Running || last.Project.Blocked != want.Blocked || last.Project.ApprovalPending != want.ApprovalPending || last.Project.LatestRunState != want.LatestRunState {
		t.Fatalf("catalog summary is stale: got running=%d blocked=%d approvals=%d run=%s, want running=%d blocked=%d approvals=%d run=%s",
			last.Project.Running, last.Project.Blocked, last.Project.ApprovalPending, last.Project.LatestRunState,
			want.Running, want.Blocked, want.ApprovalPending, want.LatestRunState)
	}
	if want.Running == 0 {
		t.Fatal("scenario should have running attempts")
	}
}
