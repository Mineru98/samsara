-- Merge group manifest verification and a failing policy check (04 §10.8).
-- A group records the verdict on its collector manifest; a rejected group
-- publishes samsara/integration-policy as failure. SQLite cannot widen a
-- CHECK constraint in place, so the table is rebuilt. No credential is
-- stored here.
CREATE TABLE provider_validation_groups_new (
  id                 TEXT PRIMARY KEY,
  project_id         TEXT NOT NULL REFERENCES projects(id),
  candidate_id       TEXT NOT NULL REFERENCES merge_candidates(id),
  candidate_revision INTEGER NOT NULL,
  entry_id           TEXT NOT NULL,
  head_sha           TEXT NOT NULL,
  base_sha           TEXT NOT NULL,
  members            TEXT NOT NULL,
  policy_digest      TEXT NOT NULL,
  check_state        TEXT NOT NULL CHECK (check_state IN ('IN_PROGRESS_REQUESTED','IN_PROGRESS','SUCCESS_REQUESTED','SUCCESS','FAILURE_REQUESTED','FAILED','SUPERSEDED')),
  check_run_id       TEXT NOT NULL DEFAULT '',
  manifest_state     TEXT NOT NULL DEFAULT 'PENDING' CHECK (manifest_state IN ('PENDING','VERIFIED','FAILED')),
  manifest_run_id    TEXT NOT NULL DEFAULT '',
  manifest_reason    TEXT NOT NULL DEFAULT '',
  created_at         INTEGER NOT NULL,
  updated_at         INTEGER NOT NULL,
  UNIQUE (candidate_id, candidate_revision)
);
INSERT INTO provider_validation_groups_new(id,project_id,candidate_id,candidate_revision,entry_id,head_sha,base_sha,members,policy_digest,check_state,check_run_id,created_at,updated_at)
  SELECT id,project_id,candidate_id,candidate_revision,entry_id,head_sha,base_sha,members,policy_digest,check_state,check_run_id,created_at,updated_at FROM provider_validation_groups;
DROP TABLE provider_validation_groups;
ALTER TABLE provider_validation_groups_new RENAME TO provider_validation_groups;
