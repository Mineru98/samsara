package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"

	"github.com/Mineru98/samsara/runtime/internal/domain"
)

// Entity kinds used in event change sets and snapshots.
const (
	KindProject     = "projects"
	KindIssue       = "issues"
	KindRun         = "runs"
	KindAttempt     = "attempts"
	KindSession     = "sessions"
	KindWorktree    = "worktrees"
	KindDependency  = "dependencies"
	KindGate        = "gates"
	KindApproval    = "approvals"
	KindCandidate   = "merge_candidates"
	KindTarget      = "integration_targets"
	KindLease       = "leases"
	KindOutput      = "outputs"
	KindOperation   = "operations"
	KindIntegration = "integration_evidence"
)

type eventDesc struct {
	typ, aggType, aggID string
	aggVersion          int64
}

type projectChanges struct {
	desc     *eventDesc
	upserts  map[string]map[string]bool
	deletes  map[string]map[string]bool
	catalog  bool
	occurred int64
}

// Tx is a writer transaction that accumulates touched entities per project.
// At commit time exactly one atomic `changes` event is appended per touched
// project (contract §9.1), carrying full projections of every touched entity.
type Tx struct {
	*sql.Tx
	db      *DB
	Now     int64
	changes map[string]*projectChanges
}

func newTx(db *DB, t *sql.Tx) *Tx {
	return &Tx{Tx: t, db: db, Now: domain.Millis(db.Clock.Now()), changes: map[string]*projectChanges{}}
}

// Clock exposes the store clock to services running in the transaction.
func (t *Tx) Clock() domain.Clock { return t.db.Clock }

// Epoch returns the clock's now in millis (fixed for the transaction).
func (t *Tx) NowMillis() int64 { return t.Now }

func (t *Tx) pc(projectID string) *projectChanges {
	c := t.changes[projectID]
	if c == nil {
		c = &projectChanges{upserts: map[string]map[string]bool{}, deletes: map[string]map[string]bool{}, occurred: t.Now}
		t.changes[projectID] = c
	}
	return c
}

// Touch marks an entity as changed so its projection is upserted.
func (t *Tx) Touch(projectID, kind, id string) {
	if projectID == "" || id == "" {
		return
	}
	c := t.pc(projectID)
	if c.upserts[kind] == nil {
		c.upserts[kind] = map[string]bool{}
	}
	c.upserts[kind][id] = true
	if kind == KindProject {
		c.catalog = true
	}
}

// Delete marks an entity as removed from the live projection.
func (t *Tx) Delete(projectID, kind, id string) {
	c := t.pc(projectID)
	if c.deletes[kind] == nil {
		c.deletes[kind] = map[string]bool{}
	}
	c.deletes[kind][id] = true
}

// Event names the domain event for a project. The first descriptor set in a
// transaction wins so the most significant action names the event.
func (t *Tx) Event(projectID, typ, aggType, aggID string, aggVersion int64) {
	c := t.pc(projectID)
	if c.desc == nil {
		c.desc = &eventDesc{typ: typ, aggType: aggType, aggID: aggID, aggVersion: aggVersion}
	}
}

// Audit appends an append-only audit row in the same transaction.
func (t *Tx) Audit(ctx context.Context, projectID, actor, action, subjectType, subjectID, decision, reason string, detail any) error {
	b, _ := json.Marshal(detail)
	if detail == nil {
		b = []byte("{}")
	}
	var pid any
	if projectID != "" {
		pid = projectID
	}
	_, err := t.ExecContext(ctx, `INSERT INTO audit_entries(id,project_id,actor,action,subject_type,subject_id,decision,reason_code,detail,created_at)
		VALUES(?,?,?,?,?,?,?,?,?,?)`, domain.NewID("aud"), pid, actor, action, subjectType, subjectID, decision, reason, string(b), t.Now)
	return err
}

// OutboxItem describes an external intent recorded with the state change.
type OutboxItem struct {
	ProjectID   string
	Kind        string
	Payload     any
	DedupeKey   string
	ResourceKey string
	CommandID   string
	Correlation string
	DelayMillis int64
}

// Enqueue inserts an operation + outbox row. A duplicate dedupe key returns
// the existing operation id without inserting (effect dedupe).
func (t *Tx) Enqueue(ctx context.Context, it OutboxItem) (string, error) {
	var existing string
	err := t.QueryRowContext(ctx, "SELECT operation_id FROM outbox WHERE dedupe_key=?", it.DedupeKey).Scan(&existing)
	if err == nil {
		return existing, nil
	}
	if err != sql.ErrNoRows {
		return "", err
	}
	opID := domain.NewID("op")
	var pid any
	if it.ProjectID != "" {
		pid = it.ProjectID
	}
	var cmd any
	if it.CommandID != "" {
		cmd = it.CommandID
	}
	if _, err := t.ExecContext(ctx, `INSERT INTO operations(id,project_id,kind,state,resource_key,command_id,correlation,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?)`, opID, pid, it.Kind, domain.OpPending, it.ResourceKey, cmd, it.Correlation, t.Now, t.Now); err != nil {
		return "", err
	}
	payload, _ := json.Marshal(it.Payload)
	if _, err := t.ExecContext(ctx, `INSERT INTO outbox(id,operation_id,project_id,kind,payload,dedupe_key,state,retry_at,created_at,updated_at)
		VALUES(?,?,?,?,?,?,'PENDING',?,?,?)`, domain.NewID("obx"), opID, pid, it.Kind, string(payload), it.DedupeKey,
		t.Now+it.DelayMillis, t.Now, t.Now); err != nil {
		return "", err
	}
	if it.ProjectID != "" {
		t.Touch(it.ProjectID, KindOperation, opID)
	}
	return opID, nil
}

// EventEnvelope is the persisted/streamed event (contract §9.1).
type EventEnvelope struct {
	SchemaVersion          string          `json:"schema_version"`
	EventID                string          `json:"event_id"`
	Seq                    string          `json:"seq"`
	ProjectID              string          `json:"project_id"`
	PreviousProjectVersion string          `json:"previous_project_version"`
	ProjectVersion         string          `json:"project_version"`
	Type                   string          `json:"type"`
	AggregateType          string          `json:"aggregate_type"`
	AggregateID            string          `json:"aggregate_id"`
	AggregateVersion       string          `json:"aggregate_version"`
	OccurredAt             string          `json:"occurred_at"`
	RecordedAt             string          `json:"recorded_at"`
	Payload                json.RawMessage `json:"payload"`
}

// ChangeSet is the atomic payload body.
type ChangeSet struct {
	Upserts map[string][]any    `json:"upserts"`
	Deletes map[string][]string `json:"deletes"`
}

func (t *Tx) flush(ctx context.Context) ([]string, bool, error) {
	ids := make([]string, 0, len(t.changes))
	for id := range t.changes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	catalog := false
	for _, pid := range ids {
		c := t.changes[pid]
		// Gate projections follow any issue/dependency/approval change in a run.
		if len(c.upserts[KindIssue]) > 0 || len(c.upserts[KindDependency]) > 0 {
			runs, err := runsForIssues(ctx, t, keys(c.upserts[KindIssue]))
			if err != nil {
				return nil, false, err
			}
			for _, rid := range runs {
				gates, err := GatesForRun(ctx, t, rid, t.Now)
				if err != nil {
					return nil, false, err
				}
				for _, g := range gates {
					if c.upserts[KindGate] == nil {
						c.upserts[KindGate] = map[string]bool{}
					}
					c.upserts[KindGate][g.ID] = true
				}
			}
		}
		cs := ChangeSet{Upserts: map[string][]any{}, Deletes: map[string][]string{}}
		for kind, set := range c.upserts {
			for _, id := range keys(set) {
				if c.deletes[kind][id] {
					continue
				}
				v, err := LoadProjection(ctx, t, kind, id, t.Now)
				if err != nil {
					return nil, false, err
				}
				if v != nil {
					cs.Upserts[kind] = append(cs.Upserts[kind], v)
				}
			}
		}
		for kind, set := range c.deletes {
			cs.Deletes[kind] = keys(set)
		}
		var prev int64
		if err := t.QueryRowContext(ctx, "SELECT project_version FROM projects WHERE id=?", pid).Scan(&prev); err != nil {
			return nil, false, err
		}
		next := prev + 1
		if _, err := t.ExecContext(ctx, "UPDATE projects SET project_version=?, last_activity_at=? WHERE id=?", next, t.Now, pid); err != nil {
			return nil, false, err
		}
		d := c.desc
		if d == nil {
			d = &eventDesc{typ: "project.changed", aggType: "project", aggID: pid}
		}
		payload, _ := json.Marshal(map[string]any{"changes": cs})
		if _, err := t.ExecContext(ctx, `INSERT INTO events(event_id,project_id,previous_project_version,project_version,type,aggregate_type,aggregate_id,aggregate_version,occurred_at,recorded_at,payload)
			VALUES(?,?,?,?,?,?,?,?,?,?,?)`, domain.NewID("evt"), pid, prev, next, d.typ, d.aggType, d.aggID, d.aggVersion, c.occurred, t.Now, string(payload)); err != nil {
			return nil, false, err
		}
		if c.catalog {
			catalog = true
			if err := t.appendCatalog(ctx, pid); err != nil {
				return nil, false, err
			}
		}
	}
	return ids, catalog, nil
}

func (t *Tx) appendCatalog(ctx context.Context, projectID string) error {
	var cv int64
	if err := t.QueryRowContext(ctx, "SELECT catalog_version FROM runtime_metadata WHERE id=1").Scan(&cv); err != nil {
		return err
	}
	cv++
	if _, err := t.ExecContext(ctx, "UPDATE runtime_metadata SET catalog_version=? WHERE id=1", cv); err != nil {
		return err
	}
	summary, err := LoadProjectSummary(ctx, t, projectID, t.Now)
	if err != nil {
		return err
	}
	b, _ := json.Marshal(map[string]any{"project": summary})
	_, err = t.ExecContext(ctx, "INSERT INTO catalog_events(catalog_version,project_id,payload,recorded_at) VALUES(?,?,?,?)",
		cv, projectID, string(b), t.Now)
	return err
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func runsForIssues(ctx context.Context, q Q, issueIDs []string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, iid := range issueIDs {
		rows, err := q.QueryContext(ctx, "SELECT run_id FROM run_issues WHERE issue_id=?", iid)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var r string
			if err := rows.Scan(&r); err != nil {
				rows.Close()
				return nil, err
			}
			if !seen[r] {
				seen[r] = true
				out = append(out, r)
			}
		}
		rows.Close()
	}
	sort.Strings(out)
	return out, nil
}
