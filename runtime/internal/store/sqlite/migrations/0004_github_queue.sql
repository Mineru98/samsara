-- GitHub native merge queue authority (04 §10, 03 §3.4).
-- The target keeps the last provider compatibility probe (04 §10.2); a
-- candidate keeps the pull request it is delivered through and the provider
-- group it is currently validated in. Every group tuple GitHub builds is a
-- separate row: its policy check and MERGE approval never carry over to
-- another group (04 §10.7, §10.9). No credential is stored here.
ALTER TABLE integration_targets ADD COLUMN provider_capability TEXT NOT NULL DEFAULT '{}';
ALTER TABLE integration_targets ADD COLUMN provider_checked_at INTEGER NOT NULL DEFAULT 0;

ALTER TABLE merge_candidates ADD COLUMN provider_pr            INTEGER NOT NULL DEFAULT 0;
ALTER TABLE merge_candidates ADD COLUMN provider_pr_node       TEXT NOT NULL DEFAULT '';
ALTER TABLE merge_candidates ADD COLUMN provider_validation_id TEXT;

CREATE TABLE provider_validation_groups (
  id                 TEXT PRIMARY KEY,
  project_id         TEXT NOT NULL REFERENCES projects(id),
  candidate_id       TEXT NOT NULL REFERENCES merge_candidates(id),
  candidate_revision INTEGER NOT NULL,
  entry_id           TEXT NOT NULL,
  head_sha           TEXT NOT NULL,
  base_sha           TEXT NOT NULL,
  members            TEXT NOT NULL,
  policy_digest      TEXT NOT NULL,
  check_state        TEXT NOT NULL CHECK (check_state IN ('IN_PROGRESS_REQUESTED','IN_PROGRESS','SUCCESS_REQUESTED','SUCCESS','SUPERSEDED')),
  check_run_id       TEXT NOT NULL DEFAULT '',
  created_at         INTEGER NOT NULL,
  updated_at         INTEGER NOT NULL,
  UNIQUE (candidate_id, candidate_revision)
);
