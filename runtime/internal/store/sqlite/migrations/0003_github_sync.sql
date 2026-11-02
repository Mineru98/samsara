-- GitHub history sync (01 §17, 02 §12.3, 04 §15). external_links keeps one
-- row per external object; sync adds its observed state, origin and the time
-- it was last confirmed. No credential is ever stored here.
ALTER TABLE external_links ADD COLUMN state     TEXT NOT NULL DEFAULT '';
ALTER TABLE external_links ADD COLUMN source    TEXT NOT NULL DEFAULT 'manual';
ALTER TABLE external_links ADD COLUMN synced_at INTEGER;
ALTER TABLE external_links ADD COLUMN detail    TEXT NOT NULL DEFAULT '{}';

-- Per-project sync cursor and provider health. A failed or rate-limited
-- pass updates only this row; local work state is never touched.
CREATE TABLE github_sync_state (
  project_id      TEXT PRIMARY KEY REFERENCES projects(id),
  repository      TEXT NOT NULL DEFAULT '',
  status          TEXT NOT NULL,
  issues_cursor   TEXT NOT NULL DEFAULT '',
  commits_cursor  TEXT NOT NULL DEFAULT '',
  last_attempt_at INTEGER,
  last_success_at INTEGER,
  next_attempt_at INTEGER NOT NULL DEFAULT 0,
  failures        INTEGER NOT NULL DEFAULT 0,
  last_error      TEXT NOT NULL DEFAULT '',
  updated_at      INTEGER NOT NULL
);
