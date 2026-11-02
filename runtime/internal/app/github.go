package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Mineru98/samsara/runtime/internal/config"
	"github.com/Mineru98/samsara/runtime/internal/domain"
	"github.com/Mineru98/samsara/runtime/internal/github"
	"github.com/Mineru98/samsara/runtime/internal/store/sqlite"
)

// githubState is the persisted sync position and provider health.
type githubState struct {
	Repository, Status, IssuesCursor, CommitsCursor, LastError string
	LastAttempt, LastSuccess                                   sql.NullInt64
	NextAttempt, Failures                                      int64
}

func loadGitHubState(ctx context.Context, q sqlite.Q, projectID string) (*githubState, error) {
	s := &githubState{}
	err := q.QueryRowContext(ctx, `SELECT repository,status,issues_cursor,commits_cursor,last_error,last_attempt_at,last_success_at,next_attempt_at,failures
		FROM github_sync_state WHERE project_id=?`, projectID).
		Scan(&s.Repository, &s.Status, &s.IssuesCursor, &s.CommitsCursor, &s.LastError, &s.LastAttempt, &s.LastSuccess, &s.NextAttempt, &s.Failures)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return s, err
}

func saveGitHubState(ctx context.Context, tx *sqlite.Tx, projectID string, s *githubState) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO github_sync_state(project_id,repository,status,issues_cursor,commits_cursor,last_attempt_at,last_success_at,next_attempt_at,failures,last_error,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(project_id) DO UPDATE SET repository=excluded.repository, status=excluded.status, issues_cursor=excluded.issues_cursor,
			commits_cursor=excluded.commits_cursor, last_attempt_at=excluded.last_attempt_at, last_success_at=excluded.last_success_at,
			next_attempt_at=excluded.next_attempt_at, failures=excluded.failures, last_error=excluded.last_error, updated_at=excluded.updated_at`,
		projectID, s.Repository, s.Status, s.IssuesCursor, s.CommitsCursor, s.LastAttempt, s.LastSuccess, s.NextAttempt, s.Failures, github.Redact(s.LastError), tx.Now)
	return err
}

// githubFreshness maps sync health to the snapshot freshness label: LIVE
// within two intervals of a successful pass, STALE once data exists but is
// old or the last pass failed, UNAVAILABLE when nothing was ever synced.
func githubFreshness(s *githubState, cfg *config.Config, now int64) string {
	if s == nil || !s.LastSuccess.Valid {
		return domain.FreshUnavailable
	}
	if s.Status == github.StatusOK && now-s.LastSuccess.Int64 <= 2*int64(cfg.GitHub.SyncIntervalSeconds)*1000 {
		return domain.FreshLive
	}
	return domain.FreshStale
}

// GitHubLoop runs sync passes apart from the reconcile loop so a slow
// provider never delays lease expiry or scheduling.
func (a *App) GitHubLoop(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		a.SyncGitHubOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-a.ghWake:
		}
	}
}

// SyncGitHubOnce syncs every enabled project whose next attempt is due.
func (a *App) SyncGitHubOnce(ctx context.Context) {
	if !a.Ready() {
		return
	}
	var ps []*sqlite.Project
	if err := a.DB.Read(ctx, func(q sqlite.Q) error {
		var err error
		ps, err = sqlite.ListProjects(ctx, q)
		return err
	}); err != nil {
		a.Log.Warn("github sync", "err", err)
		return
	}
	for _, p := range ps {
		if ctx.Err() != nil {
			return
		}
		if p.Activation != domain.ActivationEnabled {
			continue
		}
		if err := a.syncGitHubProject(ctx, p); err != nil {
			a.Log.Warn("github sync", "project", p.ID, "err", github.Redact(err.Error()))
		}
	}
}

func (a *App) syncGitHubProject(ctx context.Context, p *sqlite.Project) error {
	cfg := projectConfig(p)
	now := a.nowMillis()
	var st *githubState
	if err := a.DB.Read(ctx, func(q sqlite.Q) error {
		var err error
		st, err = loadGitHubState(ctx, q, p.ID)
		return err
	}); err != nil {
		return err
	}
	if st == nil {
		st = &githubState{}
	}
	interval := int64(cfg.GitHub.SyncIntervalSeconds) * 1000
	if cfg.GitHub.Sync == "off" {
		if st.Status == github.StatusDisabled {
			return nil
		}
		st.Status, st.LastError, st.NextAttempt = github.StatusDisabled, "", 0
		return a.DB.Write(ctx, func(tx *sqlite.Tx) error { return saveGitHubState(ctx, tx, p.ID, st) })
	}
	if st.NextAttempt > now {
		return nil
	}
	// A refresh that lands while this pass is in flight must survive the
	// write-back below.
	a.ghRefresh.Delete(p.ID)
	repo := cfg.GitHub.Repository
	if repo == "" {
		if r, err := a.Git.Run(ctx, p.RootPath, nil, "remote", "get-url", "origin"); err == nil {
			repo, _ = github.RepoFromRemote(r.Stdout)
		}
	}
	if repo == "" {
		st.Status, st.LastError, st.NextAttempt = github.StatusNotConfigured, "no github.com origin remote and no github.repository", now+interval
		return a.DB.Write(ctx, func(tx *sqlite.Tx) error { return saveGitHubState(ctx, tx, p.ID, st) })
	}
	if repo != st.Repository {
		// A different repository starts from scratch.
		st.Repository, st.IssuesCursor, st.CommitsCursor = repo, "", ""
	}
	var openPRs []int
	if err := a.DB.Read(ctx, func(q sqlite.Q) error {
		rows, err := q.QueryContext(ctx, `SELECT CAST(json_extract(detail,'$.number') AS INTEGER) FROM external_links
			WHERE project_id=? AND provider='github' AND kind='pull_request' AND state='open' AND external_key LIKE ? AND json_extract(detail,'$.number') IS NOT NULL
			ORDER BY synced_at LIMIT ?`, p.ID, repo+"#%", github.DefaultLimits.PRDetails)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var n int
			if err := rows.Scan(&n); err != nil {
				return err
			}
			openPRs = append(openPRs, n)
		}
		return rows.Err()
	}); err != nil {
		return err
	}
	res, err := github.Sync(ctx, a.GitHub, repo, github.Cursors{Issues: st.IssuesCursor, Commits: st.CommitsCursor}, openPRs, github.DefaultLimits, a.Clock.Now())
	now = a.nowMillis()
	_, refreshed := a.ghRefresh.LoadAndDelete(p.ID)
	st.LastAttempt = sql.NullInt64{Int64: now, Valid: true}
	if err != nil {
		ge := github.AsError(err)
		st.Status, st.LastError, st.Failures = ge.Status, ge.Message, st.Failures+1
		st.NextAttempt = now + githubBackoff(st.Failures, interval)
		if ge.Status == github.StatusRateLimited {
			st.NextAttempt = domain.Millis(ge.RetryAt)
		} else if refreshed {
			st.NextAttempt = 0
		}
		// Only the sync row changes: links already recorded and every local
		// work entity stay as they were.
		return a.DB.Write(ctx, func(tx *sqlite.Tx) error {
			if err := saveGitHubState(ctx, tx, p.ID, st); err != nil {
				return err
			}
			tx.Event(p.ID, "github.sync.failed", "project", p.ID, 0)
			return nil
		})
	}
	st.Status, st.LastError, st.Failures = github.StatusOK, strings.Join(res.Warnings, "; "), 0
	if len(res.Warnings) > 0 {
		a.Log.Warn("github sync partial", "project", p.ID, "warnings", github.Redact(st.LastError))
	}
	st.IssuesCursor, st.CommitsCursor = res.Cursors.Issues, res.Cursors.Commits
	st.LastSuccess = sql.NullInt64{Int64: now, Valid: true}
	st.NextAttempt = now + interval
	if refreshed {
		st.NextAttempt = 0
	}
	return a.DB.Write(ctx, func(tx *sqlite.Tx) error {
		for _, it := range res.Items {
			detail, _ := json.Marshal(it.Detail)
			// A link recorded by `samsara link add` keeps its local issue
			// binding; sync only refreshes what GitHub reports.
			if _, err := tx.ExecContext(ctx, `INSERT INTO external_links(id,project_id,issue_id,provider,kind,external_key,url,title,observed_at,state,source,synced_at,detail)
				VALUES(?,?,NULL,'github',?,?,?,?,?,?,'github_sync',?,?)
				ON CONFLICT(project_id,provider,kind,external_key) DO UPDATE SET url=excluded.url, title=excluded.title, state=excluded.state,
					synced_at=excluded.synced_at, detail=excluded.detail`,
				domain.NewID("lnk"), p.ID, it.Kind, it.Key, it.URL, it.Title, tx.Now, it.State, tx.Now, string(detail)); err != nil {
				return err
			}
		}
		if err := saveGitHubState(ctx, tx, p.ID, st); err != nil {
			return err
		}
		tx.Event(p.ID, "github.synced", "project", p.ID, 0)
		return nil
	})
}

// githubBackoff doubles from 30 s per consecutive failure, capped at the
// sync interval.
func githubBackoff(failures, interval int64) int64 {
	d := int64(30_000)
	for i := int64(1); i < failures && d < interval; i++ {
		d *= 2
	}
	if d > interval {
		d = interval
	}
	return d
}

// RefreshGitHub makes the project's next sync due now. A rate-limit window
// is kept: refresh is an explicit request, not a way around the provider.
func (a *App) RefreshGitHub(ctx context.Context, projectID string) (string, error) {
	status := "SCHEDULED"
	err := a.DB.Write(ctx, func(tx *sqlite.Tx) error {
		p, err := sqlite.GetProject(ctx, tx, projectID)
		if err != nil {
			return err
		}
		if p.Activation != domain.ActivationEnabled || projectConfig(p).GitHub.Sync == "off" {
			status = github.StatusDisabled
			return nil
		}
		st, err := loadGitHubState(ctx, tx, projectID)
		if err != nil || st == nil {
			return err
		}
		if st.Status == github.StatusRateLimited && st.NextAttempt > tx.Now {
			status = github.StatusRateLimited
			return nil
		}
		_, err = tx.ExecContext(ctx, "UPDATE github_sync_state SET next_attempt_at=0, updated_at=? WHERE project_id=?", tx.Now, projectID)
		return err
	})
	if err == nil && status == "SCHEDULED" {
		a.ghRefresh.Store(projectID, true)
		select {
		case a.ghWake <- struct{}{}:
		default:
		}
	}
	return status, err
}

// enqueueGitHubClose records the opt-in write that closes the GitHub issues
// linked to a completed local issue (03 §4.5). It runs in the completion
// transaction; the call itself happens in the outbox.
func (a *App) enqueueGitHubClose(ctx context.Context, tx *sqlite.Tx, projectID, issueID string) error {
	p, err := sqlite.GetProject(ctx, tx, projectID)
	if err != nil {
		return err
	}
	if !projectConfig(p).GitHub.CloseIssues {
		return nil
	}
	rows, err := tx.QueryContext(ctx, "SELECT id, external_key FROM external_links WHERE project_id=? AND issue_id=? AND provider='github' AND kind='issue'", projectID, issueID)
	if err != nil {
		return err
	}
	type link struct{ id, key string }
	var ls []link
	for rows.Next() {
		var l link
		if err := rows.Scan(&l.id, &l.key); err != nil {
			rows.Close()
			return err
		}
		ls = append(ls, l)
	}
	rows.Close()
	for _, l := range ls {
		if _, err := tx.Enqueue(ctx, sqlite.OutboxItem{ProjectID: projectID, Kind: "github.issue.close",
			Payload:   map[string]string{"link_id": l.id, "issue_id": issueID, "external_key": l.key},
			DedupeKey: "github.issue.close:" + l.id, ResourceKey: "github:" + l.key, Correlation: issueID}); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) handleGitHubIssueClose(ctx context.Context, r outboxRow, p map[string]string) outcome {
	already, err := github.CloseIssue(ctx, a.GitHub, p["external_key"], a.Clock.Now())
	if err != nil {
		ge := github.AsError(err)
		switch ge.Status {
		case github.StatusRateLimited:
			return retry(ge.RetryAt.Sub(a.Clock.Now()).Truncate(time.Second)+time.Second, ge)
		case github.StatusProviderError:
			return retry(30*time.Second, ge)
		case github.StatusAuthRequired, github.StatusNotConfigured:
			// gh may be logged in again later; wait instead of dropping
			// the opted-in close.
			return retry(10*time.Minute, ge)
		}
		return failed(ge.Error(), nil)
	}
	return done(map[string]any{"already_closed": already}, func(tx *sqlite.Tx) error {
		if _, err := tx.ExecContext(ctx, "UPDATE external_links SET state='closed', synced_at=? WHERE id=?", tx.Now, p["link_id"]); err != nil {
			return err
		}
		return tx.Audit(ctx, r.ProjectID, "system:github", "github.issue.close", "issue", p["issue_id"], "", "",
			map[string]any{"external_key": p["external_key"], "already_closed": already})
	})
}
