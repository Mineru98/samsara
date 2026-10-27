package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/Mineru98/samsara/runtime/internal/domain"
	"github.com/Mineru98/samsara/runtime/internal/store/sqlite"
)

// Page is a cursor-paginated list.
type Page struct {
	Items      []any  `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
}

// PageReq holds pagination input (default 50, max 200).
type PageReq struct {
	Cursor string
	Limit  int
}

func (p PageReq) limit() int {
	if p.Limit <= 0 {
		return 50
	}
	if p.Limit > 200 {
		return 200
	}
	return p.Limit
}

// pageCursor encodes (created_at, id) for deterministic keyset pagination.
func encodePageCursor(ts int64, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("%d|%s", ts, id)))
}

func decodePageCursor(c string) (int64, string, error) {
	if c == "" {
		return 0, "", nil
	}
	b, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return 0, "", domain.Errf(domain.CodeInvalidRequest, "bad cursor")
	}
	parts := strings.SplitN(string(b), "|", 2)
	if len(parts) != 2 {
		return 0, "", domain.Errf(domain.CodeInvalidRequest, "bad cursor")
	}
	ts, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, "", domain.Errf(domain.CodeInvalidRequest, "bad cursor")
	}
	return ts, parts[1], nil
}

// ListProjectSummaries returns every project with sidebar summaries.
func (a *App) ListProjectSummaries(ctx context.Context, includeArchived bool) ([]*sqlite.ProjectSummary, error) {
	var out []*sqlite.ProjectSummary
	err := a.DB.Read(ctx, func(q sqlite.Q) error {
		ps, err := sqlite.ListProjects(ctx, q)
		if err != nil {
			return err
		}
		for _, p := range ps {
			if p.RegistryState == domain.RegistryArchived && !includeArchived {
				continue
			}
			s, err := sqlite.LoadProjectSummary(ctx, q, p.ID, a.nowMillis())
			if err != nil {
				return err
			}
			out = append(out, s)
		}
		return nil
	})
	return out, err
}

// CatalogSnapshot is the atomic sidebar snapshot.
type CatalogSnapshot struct {
	SchemaVersion      string                   `json:"schema_version"`
	CatalogVersion     string                   `json:"catalog_version"`
	DatabaseGeneration string                   `json:"database_generation"`
	SnapshotCursor     string                   `json:"snapshot_cursor"`
	Projects           []*sqlite.ProjectSummary `json:"projects"`
}

// GetCatalogSnapshot reads the catalog and its cursor in one transaction.
func (a *App) GetCatalogSnapshot(ctx context.Context) (*CatalogSnapshot, error) {
	cs := &CatalogSnapshot{SchemaVersion: "1", DatabaseGeneration: a.DB.DatabaseGeneration, Projects: []*sqlite.ProjectSummary{}}
	err := a.DB.Read(ctx, func(q sqlite.Q) error {
		var cv, seq int64
		if err := q.QueryRowContext(ctx, "SELECT catalog_version FROM runtime_metadata WHERE id=1").Scan(&cv); err != nil {
			return err
		}
		if err := q.QueryRowContext(ctx, "SELECT COALESCE(MAX(seq),0) FROM catalog_events").Scan(&seq); err != nil {
			return err
		}
		cs.CatalogVersion = domain.Dec(cv)
		cs.SnapshotCursor = EncodeCursor(Cursor{Scope: "catalog", DBGen: a.DB.DatabaseGeneration, Seq: seq})
		ps, err := sqlite.ListProjects(ctx, q)
		if err != nil {
			return err
		}
		for _, p := range ps {
			s, err := sqlite.LoadProjectSummary(ctx, q, p.ID, a.nowMillis())
			if err != nil {
				return err
			}
			cs.Projects = append(cs.Projects, s)
		}
		return nil
	})
	return cs, err
}

// Cursor is the opaque SSE replay position.
type Cursor struct {
	Scope string // project id or "catalog"
	DBGen string
	Seq   int64
}

// EncodeCursor renders a cursor; it is not an authentication token.
func EncodeCursor(c Cursor) string {
	return base64.RawURLEncoding.EncodeToString([]byte(c.Scope + "|" + c.DBGen + "|" + domain.Dec(c.Seq)))
}

// DecodeCursor parses an opaque cursor.
func DecodeCursor(s string) (Cursor, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return Cursor{}, err
	}
	parts := strings.SplitN(string(b), "|", 3)
	if len(parts) != 3 {
		return Cursor{}, fmt.Errorf("malformed cursor")
	}
	seq, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		return Cursor{}, err
	}
	return Cursor{Scope: parts[0], DBGen: parts[1], Seq: seq}, nil
}

// ProjectSnapshot builds the atomic dashboard snapshot (EVT-01).
func (a *App) ProjectSnapshot(ctx context.Context, projectID string) (*sqlite.Snapshot, error) {
	var snap *sqlite.Snapshot
	err := a.DB.Read(ctx, func(q sqlite.Q) error {
		s, seq, err := sqlite.BuildSnapshot(ctx, q, projectID, a.nowMillis())
		if err != nil {
			return err
		}
		s.RuntimeEpoch = a.Epoch
		s.DatabaseGeneration = a.DB.DatabaseGeneration
		s.SnapshotCursor = EncodeCursor(Cursor{Scope: projectID, DBGen: a.DB.DatabaseGeneration, Seq: seq})
		s.Freshness = map[string]string{"runtime": domain.FreshLive, "backend_observation": a.observationFreshness(ctx, q, projectID), "github": domain.FreshUnavailable}
		snap = s
		return nil
	})
	return snap, err
}

func (a *App) observationFreshness(ctx context.Context, q sqlite.Q, projectID string) string {
	var total, unknown int
	_ = q.QueryRowContext(ctx, "SELECT COUNT(*), COALESCE(SUM(CASE WHEN liveness='UNKNOWN' THEN 1 ELSE 0 END),0) FROM sessions WHERE project_id=? AND managed=1 AND ended_at IS NULL", projectID).Scan(&total, &unknown)
	switch {
	case total == 0:
		return domain.FreshLive
	case unknown == 0:
		return domain.FreshLive
	case unknown == total:
		return domain.FreshStale
	}
	return domain.FreshPartial
}

// EventBatch is a replay page for SSE.
type EventBatch struct {
	Events        []sqlite.EventEnvelope
	ResetRequired string // reason, when the cursor cannot be served
}

// ReadProjectEvents returns events after a cursor (EVT-02/03/10/11/13).
func (a *App) ReadProjectEvents(ctx context.Context, projectID string, cur Cursor, limit int) (*EventBatch, error) {
	if cur.Scope != projectID {
		return &EventBatch{ResetRequired: "CURSOR_SCOPE_MISMATCH"}, nil
	}
	if cur.DBGen != a.DB.DatabaseGeneration {
		return &EventBatch{ResetRequired: "DATABASE_GENERATION_CHANGED"}, nil
	}
	b := &EventBatch{}
	err := a.DB.Read(ctx, func(q sqlite.Q) error {
		var pruned int64
		if err := q.QueryRowContext(ctx, "SELECT pruned_through_seq FROM runtime_metadata WHERE id=1").Scan(&pruned); err != nil {
			return err
		}
		if cur.Seq < pruned {
			var gap int
			_ = q.QueryRowContext(ctx, "SELECT COUNT(*) FROM events WHERE project_id=? AND seq > ? AND seq <= ?", projectID, cur.Seq, pruned).Scan(&gap)
			var first int64
			_ = q.QueryRowContext(ctx, "SELECT COALESCE(MIN(seq),0) FROM events WHERE project_id=? AND seq > ?", projectID, cur.Seq).Scan(&first)
			if first == 0 || first <= pruned {
				b.ResetRequired = "CURSOR_EXPIRED"
				return nil
			}
		}
		rows, err := q.QueryContext(ctx, `SELECT seq,event_id,project_id,previous_project_version,project_version,type,aggregate_type,aggregate_id,aggregate_version,occurred_at,recorded_at,payload
			FROM events WHERE project_id=? AND seq > ? ORDER BY seq LIMIT ?`, projectID, cur.Seq, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var e sqlite.EventEnvelope
			var seq, prev, pv, av, occ, rec int64
			var payload string
			if err := rows.Scan(&seq, &e.EventID, &e.ProjectID, &prev, &pv, &e.Type, &e.AggregateType, &e.AggregateID, &av, &occ, &rec, &payload); err != nil {
				return err
			}
			e.SchemaVersion = "1"
			e.Seq, e.PreviousProjectVersion, e.ProjectVersion, e.AggregateVersion = domain.Dec(seq), domain.Dec(prev), domain.Dec(pv), domain.Dec(av)
			e.OccurredAt, e.RecordedAt = domain.RFC3339(occ), domain.RFC3339(rec)
			e.Payload = json.RawMessage(payload)
			b.Events = append(b.Events, e)
		}
		return rows.Err()
	})
	return b, err
}

// CatalogEvent is a catalog stream item.
type CatalogEvent struct {
	SchemaVersion  string          `json:"schema_version"`
	Seq            string          `json:"seq"`
	CatalogVersion string          `json:"catalog_version"`
	ProjectID      string          `json:"project_id"`
	RecordedAt     string          `json:"recorded_at"`
	Payload        json.RawMessage `json:"payload"`
}

// ReadCatalogEvents returns catalog events after a cursor.
func (a *App) ReadCatalogEvents(ctx context.Context, cur Cursor, limit int) ([]CatalogEvent, string, error) {
	if cur.Scope != "catalog" {
		return nil, "CURSOR_SCOPE_MISMATCH", nil
	}
	if cur.DBGen != a.DB.DatabaseGeneration {
		return nil, "DATABASE_GENERATION_CHANGED", nil
	}
	var out []CatalogEvent
	reset := ""
	err := a.DB.Read(ctx, func(q sqlite.Q) error {
		var minSeq int64
		_ = q.QueryRowContext(ctx, "SELECT COALESCE(MIN(seq),0) FROM catalog_events").Scan(&minSeq)
		if minSeq > cur.Seq+1 && cur.Seq > 0 {
			reset = "CURSOR_EXPIRED"
			return nil
		}
		rows, err := q.QueryContext(ctx, "SELECT seq, catalog_version, project_id, recorded_at, payload FROM catalog_events WHERE seq > ? ORDER BY seq LIMIT ?", cur.Seq, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var seq, cv, rec int64
			var e CatalogEvent
			var payload string
			if err := rows.Scan(&seq, &cv, &e.ProjectID, &rec, &payload); err != nil {
				return err
			}
			e.SchemaVersion, e.Seq, e.CatalogVersion, e.RecordedAt, e.Payload = "1", domain.Dec(seq), domain.Dec(cv), domain.RFC3339(rec), json.RawMessage(payload)
			out = append(out, e)
		}
		return rows.Err()
	})
	return out, reset, err
}

// Lookup loads one entity projection by kind and enforces existence.
func (a *App) Lookup(ctx context.Context, kind, id string) (any, error) {
	var v any
	err := a.DB.Read(ctx, func(q sqlite.Q) error {
		var err error
		v, err = sqlite.LoadProjection(ctx, q, kind, id, a.nowMillis())
		if err == nil && v == nil {
			err = domain.Errf(domain.CodeNotFound, "%s %s not found", kind, id)
		}
		return err
	})
	return v, err
}

// IssueDetail is the issue view plus dependency, attempts, outputs.
type IssueDetail struct {
	Issue        *sqlite.IssueView        `json:"issue"`
	Dependencies []*sqlite.DependencyView `json:"dependencies"`
	Attempts     []*sqlite.AttemptView    `json:"attempts"`
	Outputs      []map[string]any         `json:"outputs"`
	Candidates   []*sqlite.CandidateView  `json:"merge_candidates"`
}

// GetIssueDetail loads the issue detail.
func (a *App) GetIssueDetail(ctx context.Context, issueID string) (*IssueDetail, error) {
	d := &IssueDetail{Dependencies: []*sqlite.DependencyView{}, Attempts: []*sqlite.AttemptView{}, Outputs: []map[string]any{}, Candidates: []*sqlite.CandidateView{}}
	err := a.DB.Read(ctx, func(q sqlite.Q) error {
		var err error
		if d.Issue, err = sqlite.IssueProjection(ctx, q, issueID); err != nil {
			return err
		}
		rows, err := q.QueryContext(ctx, "SELECT id FROM dependency_edges WHERE predecessor_issue_id=? OR successor_issue_id=? ORDER BY id", issueID, issueID)
		if err != nil {
			return err
		}
		var ids []string
		for rows.Next() {
			var id string
			_ = rows.Scan(&id)
			ids = append(ids, id)
		}
		rows.Close()
		for _, id := range ids {
			dv, err := sqlite.DependencyProjection(ctx, q, id)
			if err != nil {
				return err
			}
			d.Dependencies = append(d.Dependencies, dv)
		}
		atts, err := sqlite.AttemptsWhere(ctx, q, "issue_id=?", issueID)
		if err != nil {
			return err
		}
		for _, at := range atts {
			av, err := sqlite.AttemptProjection(ctx, q, at.ID)
			if err != nil {
				return err
			}
			d.Attempts = append(d.Attempts, av)
		}
		orows, err := q.QueryContext(ctx, "SELECT id, attempt_id, source_sha, base_sha, verification, COALESCE(superseded_by,''), manifest_digest, created_at FROM output_artifacts WHERE issue_id=? ORDER BY created_at", issueID)
		if err != nil {
			return err
		}
		for orows.Next() {
			var id, att, src, base, ver, sup, dig string
			var created int64
			if err := orows.Scan(&id, &att, &src, &base, &ver, &sup, &dig, &created); err != nil {
				orows.Close()
				return err
			}
			d.Outputs = append(d.Outputs, map[string]any{"id": id, "attempt_id": att, "source_sha": src, "base_sha": base, "verification": ver,
				"superseded_by": sup, "manifest_digest": dig, "created_at": domain.RFC3339(created)})
		}
		orows.Close()
		cs, err := sqlite.CandidatesWhere(ctx, q, "issue_id=?", issueID)
		if err != nil {
			return err
		}
		for _, c := range cs {
			cv, err := sqlite.CandidateProjection(ctx, q, c.ID)
			if err != nil {
				return err
			}
			d.Candidates = append(d.Candidates, cv)
		}
		return nil
	})
	return d, err
}

// ListIssuesPage lists project issues with keyset pagination.
func (a *App) ListIssuesPage(ctx context.Context, projectID string, pr PageReq) (*Page, error) {
	return a.pageQuery(ctx, projectID, pr, "SELECT id, created_at FROM issues WHERE project_id=? AND (created_at > ? OR (created_at = ? AND id > ?)) ORDER BY created_at, id LIMIT ?", sqlite.KindIssue)
}

// ListRunsPage lists project runs.
func (a *App) ListRunsPage(ctx context.Context, projectID string, pr PageReq) (*Page, error) {
	return a.pageQuery(ctx, projectID, pr, "SELECT id, created_at FROM runs WHERE project_id=? AND (created_at > ? OR (created_at = ? AND id > ?)) ORDER BY created_at, id LIMIT ?", sqlite.KindRun)
}

// ListApprovalsPage lists project approvals.
func (a *App) ListApprovalsPage(ctx context.Context, projectID string, pr PageReq) (*Page, error) {
	return a.pageQuery(ctx, projectID, pr, "SELECT id, requested_at FROM approvals WHERE project_id=? AND (requested_at > ? OR (requested_at = ? AND id > ?)) ORDER BY requested_at, id LIMIT ?", sqlite.KindApproval)
}

func (a *App) pageQuery(ctx context.Context, projectID string, pr PageReq, query, kind string) (*Page, error) {
	ts, id, err := decodePageCursor(pr.Cursor)
	if err != nil {
		return nil, err
	}
	if pr.Cursor == "" {
		ts, id = -1, ""
	}
	p := &Page{Items: []any{}}
	err = a.DB.Read(ctx, func(q sqlite.Q) error {
		if _, err := sqlite.GetProject(ctx, q, projectID); err != nil {
			return err
		}
		lim := pr.limit()
		rows, err := q.QueryContext(ctx, query, projectID, ts, ts, id, lim+1)
		if err != nil {
			return err
		}
		type row struct {
			id string
			ts int64
		}
		var rs []row
		for rows.Next() {
			var r row
			if err := rows.Scan(&r.id, &r.ts); err != nil {
				rows.Close()
				return err
			}
			rs = append(rs, r)
		}
		rows.Close()
		if len(rs) > lim {
			rs = rs[:lim]
			last := rs[len(rs)-1]
			p.NextCursor = encodePageCursor(last.ts, last.id)
		}
		for _, r := range rs {
			v, err := sqlite.LoadProjection(ctx, q, kind, r.id, a.nowMillis())
			if err != nil {
				return err
			}
			if v != nil {
				p.Items = append(p.Items, v)
			}
		}
		return nil
	})
	return p, err
}

// HistoryEntry is one audit record.
type HistoryEntry struct {
	ID          string          `json:"id"`
	Actor       string          `json:"actor"`
	Action      string          `json:"action"`
	SubjectType string          `json:"subject_type"`
	SubjectID   string          `json:"subject_id"`
	Decision    string          `json:"decision,omitempty"`
	ReasonCode  string          `json:"reason_code,omitempty"`
	Detail      json.RawMessage `json:"detail"`
	CreatedAt   string          `json:"created_at"`
}

// History pages the durable audit log (newest first). Tool checks can be
// excluded to keep the business history readable.
func (a *App) History(ctx context.Context, projectID string, pr PageReq, includeToolChecks bool) (*Page, error) {
	ts, id, err := decodePageCursor(pr.Cursor)
	if err != nil {
		return nil, err
	}
	if pr.Cursor == "" {
		ts, id = 1<<62, ""
	}
	p := &Page{Items: []any{}}
	err = a.DB.Read(ctx, func(q sqlite.Q) error {
		if _, err := sqlite.GetProject(ctx, q, projectID); err != nil {
			return err
		}
		filter := ""
		if !includeToolChecks {
			filter = " AND action <> 'tool.check'"
		}
		lim := pr.limit()
		rows, err := q.QueryContext(ctx, "SELECT id,actor,action,subject_type,subject_id,decision,reason_code,detail,created_at FROM audit_entries WHERE project_id=?"+filter+
			" AND (created_at < ? OR (created_at = ? AND id < ?) OR ?='') ORDER BY created_at DESC, id DESC LIMIT ?", projectID, ts, ts, id, id, lim+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		var es []HistoryEntry
		var tss []int64
		for rows.Next() {
			var e HistoryEntry
			var detail string
			var created int64
			if err := rows.Scan(&e.ID, &e.Actor, &e.Action, &e.SubjectType, &e.SubjectID, &e.Decision, &e.ReasonCode, &detail, &created); err != nil {
				return err
			}
			e.Detail, e.CreatedAt = json.RawMessage(detail), domain.RFC3339(created)
			es = append(es, e)
			tss = append(tss, created)
		}
		if len(es) > lim {
			es, tss = es[:lim], tss[:lim]
			p.NextCursor = encodePageCursor(tss[len(tss)-1], es[len(es)-1].ID)
		}
		for _, e := range es {
			p.Items = append(p.Items, e)
		}
		return rows.Err()
	})
	return p, err
}

// GitHubHistory lists recorded external links. Live GitHub sync is not
// part of this build; freshness says so explicitly.
func (a *App) GitHubHistory(ctx context.Context, projectID string) (map[string]any, error) {
	items := []map[string]any{}
	err := a.DB.Read(ctx, func(q sqlite.Q) error {
		if _, err := sqlite.GetProject(ctx, q, projectID); err != nil {
			return err
		}
		rows, err := q.QueryContext(ctx, "SELECT id, COALESCE(issue_id,''), provider, kind, external_key, url, title, observed_at FROM external_links WHERE project_id=? ORDER BY observed_at DESC", projectID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id, issue, prov, kind, key, url, title string
			var obs int64
			if err := rows.Scan(&id, &issue, &prov, &kind, &key, &url, &title, &obs); err != nil {
				return err
			}
			items = append(items, map[string]any{"id": id, "issue_id": issue, "provider": prov, "kind": kind, "external_key": key, "url": url, "title": title, "observed_at": domain.RFC3339(obs)})
		}
		return rows.Err()
	})
	return map[string]any{"items": items, "freshness": domain.FreshUnavailable, "sync": "NOT_CONFIGURED"}, err
}

// LinkRequest records an external issue/PR/commit link for an issue.
type LinkRequest struct {
	ProjectID   string `json:"project_id"`
	IssueID     string `json:"issue_id,omitempty"`
	Provider    string `json:"provider"`
	Kind        string `json:"kind"`
	ExternalKey string `json:"external_key"`
	URL         string `json:"url,omitempty"`
	Title       string `json:"title,omitempty"`
}

// AddExternalLink stores provenance between local work and GitHub objects.
func (a *App) AddExternalLink(ctx context.Context, cmd Cmd, req LinkRequest) error {
	switch req.Kind {
	case "issue", "pull_request", "commit":
	default:
		return domain.Errf(domain.CodeInvalidRequest, "kind must be issue, pull_request or commit")
	}
	if req.Provider == "" || req.ExternalKey == "" {
		return domain.Errf(domain.CodeInvalidRequest, "provider and external_key are required")
	}
	return a.DB.Write(ctx, func(tx *sqlite.Tx) error {
		if _, err := sqlite.GetProject(ctx, tx, req.ProjectID); err != nil {
			return err
		}
		var issue any
		if req.IssueID != "" {
			i, err := sqlite.GetIssue(ctx, tx, req.IssueID)
			if err != nil {
				return err
			}
			if i.ProjectID != req.ProjectID {
				return domain.Errf(domain.CodeActionForbidden, "issue belongs to another project")
			}
			issue = i.ID
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO external_links(id,project_id,issue_id,provider,kind,external_key,url,title,observed_at) VALUES(?,?,?,?,?,?,?,?,?)
			ON CONFLICT(project_id,provider,kind,external_key) DO UPDATE SET issue_id=excluded.issue_id, url=excluded.url, title=excluded.title, observed_at=excluded.observed_at`,
			domain.NewID("lnk"), req.ProjectID, issue, req.Provider, req.Kind, req.ExternalKey, req.URL, req.Title, tx.Now); err != nil {
			return err
		}
		tx.Event(req.ProjectID, "external_link.recorded", "project", req.ProjectID, 0)
		return tx.Audit(ctx, req.ProjectID, cmd.Principal.ID, "external_link.add", "issue", req.IssueID, "", "", req)
	})
}

// MergeQueues lists candidates per target of a project.
func (a *App) MergeQueues(ctx context.Context, projectID string) ([]map[string]any, error) {
	var out []map[string]any
	err := a.DB.Read(ctx, func(q sqlite.Q) error {
		ts, err := sqlite.TargetsOfProject(ctx, q, projectID)
		if err != nil {
			return err
		}
		for _, t := range ts {
			cs, err := sqlite.CandidatesWhere(ctx, q, "target_id=?", t.ID)
			if err != nil {
				return err
			}
			items := []any{}
			for _, c := range cs {
				cv, err := sqlite.CandidateProjection(ctx, q, c.ID)
				if err != nil {
					return err
				}
				items = append(items, cv)
			}
			out = append(out, map[string]any{"target": sqlite.TargetProjection(t), "candidates": items})
		}
		return nil
	})
	return out, err
}

// RunSnapshot returns the frozen historical view of a finished run.
func (a *App) RunSnapshot(ctx context.Context, runID string) (map[string]any, error) {
	var out map[string]any
	err := a.DB.Read(ctx, func(q sqlite.Q) error {
		r, err := sqlite.GetRun(ctx, q, runID)
		if err != nil {
			return err
		}
		switch r.State {
		case domain.RunCompleted, domain.RunCancelled, domain.RunFailed:
		default:
			return domain.Errf(domain.CodeRunNotFinalized, "run %s is %s; use the live project snapshot", r.ID, r.State)
		}
		var summary any
		_ = json.Unmarshal([]byte(r.FinalSummary), &summary)
		var plan any
		_ = json.Unmarshal([]byte(r.PlanSnapshot), &plan)
		out = map[string]any{"schema_version": "1", "view_kind": "HISTORICAL", "run_id": r.ID, "project_id": r.ProjectID,
			"run_generation": domain.Dec(r.Generation), "ended_at": domain.RFC3339(r.EndedAt), "final_version": domain.Dec(r.Version),
			"final_state": r.State, "plan": plan, "summary": summary}
		return nil
	})
	return out, err
}

// ProjectIDOf returns the owning project id of an entity for scope checks.
func (a *App) ProjectIDOf(ctx context.Context, table, id string) (string, error) {
	allowed := map[string]bool{"issues": true, "runs": true, "execution_attempts": true, "sessions": true, "worktrees": true,
		"approvals": true, "merge_candidates": true, "operations": true}
	if !allowed[table] {
		return "", domain.Errf(domain.CodeInternal, "bad table")
	}
	var pid string
	err := a.DB.Read(ctx, func(q sqlite.Q) error {
		return q.QueryRowContext(ctx, "SELECT COALESCE(project_id,'') FROM "+table+" WHERE id=?", id).Scan(&pid)
	})
	if err != nil {
		return "", domain.Errf(domain.CodeNotFound, "%s not found", id)
	}
	return pid, nil
}
