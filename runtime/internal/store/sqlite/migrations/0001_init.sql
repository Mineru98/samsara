-- Samsara runtime schema v1 (03-contracts-and-data-model.md §5).
-- Times are unix milliseconds (INTEGER). Counters are INTEGER and rendered as
-- decimal strings on the wire.

CREATE TABLE runtime_metadata (
  id                  INTEGER PRIMARY KEY CHECK (id = 1),
  host_id             TEXT NOT NULL,
  database_generation TEXT NOT NULL,
  catalog_version     INTEGER NOT NULL DEFAULT 0,
  pruned_through_seq  INTEGER NOT NULL DEFAULT 0,
  created_at          INTEGER NOT NULL
);

CREATE TABLE runtime_epochs (
  epoch      TEXT PRIMARY KEY,
  pid        INTEGER NOT NULL,
  started_at INTEGER NOT NULL,
  stopped_at INTEGER
);

CREATE TABLE projects (
  id               TEXT PRIMARY KEY,
  root_path        TEXT NOT NULL UNIQUE,
  display_name     TEXT NOT NULL,
  activation       TEXT NOT NULL CHECK (activation IN ('UNCONFIGURED','ENABLED','DISABLED')),
  registry_state   TEXT NOT NULL DEFAULT 'PRESENT' CHECK (registry_state IN ('PRESENT','MISSING','ARCHIVED')),
  drain_state      TEXT NOT NULL DEFAULT 'NONE' CHECK (drain_state IN ('NONE','REQUESTED','DRAINING','COMPLETE','NEEDS_ATTENTION')),
  config_digest    TEXT NOT NULL DEFAULT '',
  config_json      TEXT NOT NULL DEFAULT '{}',
  policy_version   INTEGER NOT NULL DEFAULT 1,
  graph_version    INTEGER NOT NULL DEFAULT 0,
  project_version  INTEGER NOT NULL DEFAULT 0,
  version          INTEGER NOT NULL DEFAULT 1,
  created_at       INTEGER NOT NULL,
  updated_at       INTEGER NOT NULL,
  last_activity_at INTEGER NOT NULL
);

CREATE TABLE repositories (
  id              TEXT PRIMARY KEY,
  common_dir      TEXT NOT NULL UNIQUE,
  object_format   TEXT NOT NULL DEFAULT 'sha1',
  remote_identity TEXT NOT NULL DEFAULT '',
  created_at      INTEGER NOT NULL
);

CREATE TABLE project_repositories (
  project_id    TEXT NOT NULL REFERENCES projects(id),
  repository_id TEXT NOT NULL REFERENCES repositories(id),
  PRIMARY KEY (project_id, repository_id)
);

CREATE TABLE check_profiles (
  project_id  TEXT NOT NULL REFERENCES projects(id),
  name        TEXT NOT NULL,
  argv_json   TEXT NOT NULL,
  timeout_sec INTEGER NOT NULL DEFAULT 600,
  created_at  INTEGER NOT NULL,
  PRIMARY KEY (project_id, name)
);

CREATE TABLE issues (
  id                     TEXT PRIMARY KEY,
  project_id             TEXT NOT NULL REFERENCES projects(id),
  client_key             TEXT NOT NULL,
  title                  TEXT NOT NULL,
  acceptance_criteria    TEXT NOT NULL DEFAULT '[]',
  check_profile          TEXT NOT NULL DEFAULT '',
  required_capabilities  TEXT NOT NULL DEFAULT '[]',
  target_repository_id   TEXT REFERENCES repositories(id),
  target_ref             TEXT NOT NULL DEFAULT '',
  lifecycle              TEXT NOT NULL DEFAULT 'OPEN' CHECK (lifecycle IN ('OPEN','COMPLETED','CANCELLED')),
  version                INTEGER NOT NULL DEFAULT 1,
  created_at             INTEGER NOT NULL,
  updated_at             INTEGER NOT NULL
);
CREATE INDEX issues_by_project ON issues(project_id, created_at, id);

CREATE TABLE runs (
  id                  TEXT PRIMARY KEY,
  project_id          TEXT NOT NULL REFERENCES projects(id),
  generation          INTEGER NOT NULL DEFAULT 1,
  graph_version       INTEGER NOT NULL,
  state               TEXT NOT NULL CHECK (state IN ('PLANNED','ACTIVE','PAUSED','COMPLETED','FAILED','CANCELLED')),
  plan_schema_version TEXT NOT NULL,
  plan_digest         TEXT NOT NULL,
  plan_snapshot       TEXT NOT NULL,
  alias_mapping       TEXT NOT NULL,
  final_summary       TEXT,
  version             INTEGER NOT NULL DEFAULT 1,
  created_at          INTEGER NOT NULL,
  started_at          INTEGER,
  ended_at            INTEGER
);
CREATE INDEX runs_by_project ON runs(project_id, created_at, id);

CREATE TABLE run_issues (
  run_id           TEXT NOT NULL REFERENCES runs(id),
  issue_id         TEXT NOT NULL REFERENCES issues(id),
  chosen_output_id TEXT,
  PRIMARY KEY (run_id, issue_id)
);

CREATE TABLE dependency_edges (
  id                      TEXT PRIMARY KEY,
  project_id              TEXT NOT NULL REFERENCES projects(id),
  run_id                  TEXT NOT NULL REFERENCES runs(id),
  predecessor_issue_id    TEXT NOT NULL REFERENCES issues(id),
  successor_issue_id      TEXT NOT NULL REFERENCES issues(id),
  required_milestone      TEXT NOT NULL CHECK (required_milestone IN ('IMPLEMENTED','VERIFIED','RESULT_APPROVED','INTEGRATED')),
  downstream_phase        TEXT NOT NULL CHECK (downstream_phase IN ('EXECUTION','INTEGRATION')),
  required_run_generation INTEGER NOT NULL,
  required_output_id      TEXT,
  target_repository_id    TEXT REFERENCES repositories(id),
  target_ref              TEXT NOT NULL DEFAULT '',
  graph_version           INTEGER NOT NULL,
  invalidation_policy     TEXT NOT NULL DEFAULT 'PAUSE_DESCENDANTS',
  version                 INTEGER NOT NULL DEFAULT 1,
  CHECK (predecessor_issue_id <> successor_issue_id),
  CHECK (required_milestone <> 'INTEGRATED' OR (target_repository_id IS NOT NULL AND target_ref <> '')),
  UNIQUE (run_id, predecessor_issue_id, successor_issue_id)
);

CREATE TABLE worktrees (
  id            TEXT PRIMARY KEY,
  project_id    TEXT NOT NULL REFERENCES projects(id),
  repository_id TEXT REFERENCES repositories(id),
  attempt_id    TEXT,
  path          TEXT NOT NULL UNIQUE,
  branch_ref    TEXT NOT NULL DEFAULT '',
  base_sha      TEXT NOT NULL DEFAULT '',
  ownership     TEXT NOT NULL CHECK (ownership IN ('managed','external')),
  state         TEXT NOT NULL,
  state_reason  TEXT NOT NULL DEFAULT '',
  version       INTEGER NOT NULL DEFAULT 1,
  created_at    INTEGER NOT NULL,
  updated_at    INTEGER NOT NULL
);

CREATE TABLE execution_attempts (
  id                TEXT PRIMARY KEY,
  project_id        TEXT NOT NULL REFERENCES projects(id),
  run_id            TEXT NOT NULL REFERENCES runs(id),
  issue_id          TEXT NOT NULL REFERENCES issues(id),
  run_generation    INTEGER NOT NULL,
  worktree_id       TEXT REFERENCES worktrees(id),
  state             TEXT NOT NULL,
  state_reason      TEXT NOT NULL DEFAULT '',
  quarantine_reason TEXT NOT NULL DEFAULT '',
  resume_required   INTEGER NOT NULL DEFAULT 0,
  backend_kind      TEXT NOT NULL DEFAULT '',
  backend_reason    TEXT NOT NULL DEFAULT '',
  output_id         TEXT,
  version           INTEGER NOT NULL DEFAULT 1,
  created_at        INTEGER NOT NULL,
  updated_at        INTEGER NOT NULL
);
CREATE UNIQUE INDEX one_live_attempt_per_issue
  ON execution_attempts(run_id, issue_id)
  WHERE state IN ('PENDING','STARTING','RUNNING','PAUSING','PAUSED','CANCEL_REQUESTED');
CREATE INDEX attempts_by_project ON execution_attempts(project_id, state);

CREATE TABLE launch_tickets (
  ticket_hash TEXT PRIMARY KEY,
  attempt_id  TEXT NOT NULL REFERENCES execution_attempts(id),
  project_id  TEXT NOT NULL REFERENCES projects(id),
  issued_at   INTEGER NOT NULL,
  expires_at  INTEGER NOT NULL,
  used_at     INTEGER
);

CREATE TABLE sessions (
  id                TEXT PRIMARY KEY,
  project_id        TEXT NOT NULL REFERENCES projects(id),
  claude_session_id TEXT NOT NULL,
  cwd               TEXT NOT NULL DEFAULT '',
  managed           INTEGER NOT NULL DEFAULT 0,
  desired_state     TEXT NOT NULL DEFAULT 'ACTIVE',
  liveness          TEXT NOT NULL DEFAULT 'UNKNOWN',
  input_state       TEXT NOT NULL DEFAULT 'UNKNOWN',
  input_owner       TEXT NOT NULL DEFAULT 'user',
  source            TEXT NOT NULL DEFAULT 'hook',
  backend_raw_state TEXT NOT NULL DEFAULT '',
  confidence        TEXT NOT NULL DEFAULT 'LOW',
  process_identity  TEXT NOT NULL DEFAULT '{}',
  observed_at       INTEGER NOT NULL,
  last_hook_at      INTEGER,
  version           INTEGER NOT NULL DEFAULT 1,
  created_at        INTEGER NOT NULL,
  ended_at          INTEGER
);
CREATE INDEX sessions_by_claude ON sessions(claude_session_id);
CREATE INDEX sessions_by_project ON sessions(project_id);

CREATE TABLE backend_bindings (
  id            TEXT PRIMARY KEY,
  session_id    TEXT REFERENCES sessions(id),
  attempt_id    TEXT REFERENCES execution_attempts(id),
  kind          TEXT NOT NULL,
  instance      TEXT NOT NULL DEFAULT '',
  opaque_ref    TEXT NOT NULL,
  ref_key       TEXT NOT NULL,
  generation    TEXT NOT NULL,
  created_at    INTEGER NOT NULL,
  superseded_at INTEGER,
  UNIQUE (kind, instance, ref_key, generation)
);

CREATE TABLE session_assignments (
  id          TEXT PRIMARY KEY,
  session_id  TEXT NOT NULL REFERENCES sessions(id),
  attempt_id  TEXT NOT NULL REFERENCES execution_attempts(id),
  binding_id  TEXT REFERENCES backend_bindings(id),
  attached_at INTEGER NOT NULL,
  released_at INTEGER
);
CREATE UNIQUE INDEX one_active_session_assignment
  ON session_assignments(session_id) WHERE released_at IS NULL;
CREATE UNIQUE INDEX one_active_attempt_assignment
  ON session_assignments(attempt_id) WHERE released_at IS NULL;

CREATE TABLE resource_generations (
  resource_key  TEXT PRIMARY KEY,
  current_fence INTEGER NOT NULL
);

CREATE TABLE leases (
  id               TEXT PRIMARY KEY,
  project_id       TEXT NOT NULL REFERENCES projects(id),
  resource_key     TEXT NOT NULL,
  owner_attempt_id TEXT NOT NULL REFERENCES execution_attempts(id),
  session_id       TEXT REFERENCES sessions(id),
  binding_id       TEXT REFERENCES backend_bindings(id),
  fence            INTEGER NOT NULL,
  state            TEXT NOT NULL CHECK (state IN ('ACTIVE','REVOKED','EXPIRED','RELEASED')),
  state_reason     TEXT NOT NULL DEFAULT '',
  runtime_epoch    TEXT NOT NULL,
  issued_at        INTEGER NOT NULL,
  renewed_at       INTEGER NOT NULL,
  deadline_at      INTEGER NOT NULL,
  version          INTEGER NOT NULL DEFAULT 1
);
CREATE UNIQUE INDEX one_active_lease ON leases(resource_key) WHERE state = 'ACTIVE';
CREATE INDEX leases_by_attempt ON leases(owner_attempt_id);

CREATE TABLE output_artifacts (
  id              TEXT PRIMARY KEY,
  project_id      TEXT NOT NULL REFERENCES projects(id),
  run_id          TEXT NOT NULL REFERENCES runs(id),
  issue_id        TEXT NOT NULL REFERENCES issues(id),
  attempt_id      TEXT NOT NULL REFERENCES execution_attempts(id),
  run_generation  INTEGER NOT NULL,
  source_sha      TEXT NOT NULL,
  base_sha        TEXT NOT NULL DEFAULT '',
  branch_ref      TEXT NOT NULL DEFAULT '',
  manifest        TEXT NOT NULL,
  manifest_digest TEXT NOT NULL,
  provenance      TEXT NOT NULL,
  fence           INTEGER NOT NULL,
  verification    TEXT NOT NULL DEFAULT 'PENDING',
  superseded_by   TEXT,
  created_at      INTEGER NOT NULL
);
CREATE TRIGGER output_artifacts_immutable
  BEFORE UPDATE OF id, project_id, run_id, issue_id, attempt_id, run_generation, source_sha,
                   base_sha, branch_ref, manifest, manifest_digest, provenance, fence, created_at
  ON output_artifacts
  BEGIN SELECT RAISE(ABORT, 'output_artifacts are immutable'); END;
CREATE TRIGGER output_artifacts_no_delete BEFORE DELETE ON output_artifacts
  BEGIN SELECT RAISE(ABORT, 'output_artifacts are immutable'); END;

CREATE TABLE approvals (
  id                 TEXT PRIMARY KEY,
  project_id         TEXT NOT NULL REFERENCES projects(id),
  kind               TEXT NOT NULL CHECK (kind IN ('PLAN','RESULT','MERGE','RECOVERY')),
  state              TEXT NOT NULL CHECK (state IN ('PENDING','APPROVED','REJECTED','INVALIDATED','CANCELLED')),
  subject            TEXT NOT NULL,
  subject_digest     TEXT NOT NULL,
  run_id             TEXT REFERENCES runs(id),
  run_generation     INTEGER,
  issue_id           TEXT REFERENCES issues(id),
  output_id          TEXT REFERENCES output_artifacts(id),
  merge_candidate_id TEXT,
  candidate_revision INTEGER,
  policy_version     INTEGER NOT NULL,
  state_reason       TEXT NOT NULL DEFAULT '',
  decision_actor     TEXT NOT NULL DEFAULT '',
  decision_comment   TEXT NOT NULL DEFAULT '',
  requested_at       INTEGER NOT NULL,
  decided_at         INTEGER,
  version            INTEGER NOT NULL DEFAULT 1
);
CREATE UNIQUE INDEX one_pending_approval_per_subject
  ON approvals(kind, subject_digest) WHERE state = 'PENDING';
CREATE INDEX approvals_by_project ON approvals(project_id, state);

CREATE TABLE integration_targets (
  id                TEXT PRIMARY KEY,
  repository_id     TEXT NOT NULL REFERENCES repositories(id),
  target_ref        TEXT NOT NULL,
  authority         TEXT NOT NULL CHECK (authority IN ('manual','github_queue','samsara_local')),
  integration_repo  TEXT NOT NULL DEFAULT '',
  policy_version    INTEGER NOT NULL DEFAULT 1,
  version           INTEGER NOT NULL DEFAULT 1,
  created_at        INTEGER NOT NULL,
  UNIQUE (repository_id, target_ref)
);

CREATE TABLE project_targets (
  project_id TEXT NOT NULL REFERENCES projects(id),
  target_id  TEXT NOT NULL REFERENCES integration_targets(id),
  PRIMARY KEY (project_id, target_id)
);

CREATE TABLE merge_candidates (
  id                      TEXT PRIMARY KEY,
  project_id              TEXT NOT NULL REFERENCES projects(id),
  issue_id                TEXT NOT NULL REFERENCES issues(id),
  run_id                  TEXT NOT NULL REFERENCES runs(id),
  attempt_id              TEXT NOT NULL REFERENCES execution_attempts(id),
  output_id               TEXT NOT NULL REFERENCES output_artifacts(id),
  target_id               TEXT NOT NULL REFERENCES integration_targets(id),
  source_sha              TEXT NOT NULL,
  base_sha                TEXT NOT NULL DEFAULT '',
  candidate_sha           TEXT NOT NULL DEFAULT '',
  candidate_tree          TEXT NOT NULL DEFAULT '',
  revision                INTEGER NOT NULL DEFAULT 1,
  state                   TEXT NOT NULL,
  state_reason            TEXT NOT NULL DEFAULT '',
  native_queue_state      TEXT NOT NULL DEFAULT 'NONE',
  publication_observation TEXT NOT NULL DEFAULT '',
  enqueued                INTEGER NOT NULL DEFAULT 0,
  queue_position          INTEGER NOT NULL,
  version                 INTEGER NOT NULL DEFAULT 1,
  created_at              INTEGER NOT NULL,
  updated_at              INTEGER NOT NULL
);
CREATE UNIQUE INDEX one_publishing_local_candidate
  ON merge_candidates(target_id) WHERE state IN ('VALIDATING','PUBLISHING');
CREATE INDEX candidates_by_target ON merge_candidates(target_id, state, queue_position);

CREATE TABLE target_locks (
  target_id     TEXT PRIMARY KEY REFERENCES integration_targets(id),
  current_fence INTEGER NOT NULL,
  owner_epoch   TEXT NOT NULL DEFAULT '',
  candidate_id  TEXT
);

CREATE TABLE check_runs (
  id                 TEXT PRIMARY KEY,
  project_id         TEXT NOT NULL REFERENCES projects(id),
  subject_kind       TEXT NOT NULL,
  subject_id         TEXT NOT NULL,
  subject_revision   INTEGER NOT NULL DEFAULT 0,
  check_name         TEXT NOT NULL,
  checked_sha        TEXT NOT NULL,
  command_digest     TEXT NOT NULL,
  source_actor       TEXT NOT NULL,
  result             TEXT NOT NULL,
  exit_code          INTEGER,
  summary            TEXT NOT NULL DEFAULT '',
  started_at         INTEGER NOT NULL,
  finished_at        INTEGER
);

CREATE TABLE integration_evidence (
  id                TEXT PRIMARY KEY,
  project_id        TEXT NOT NULL REFERENCES projects(id),
  candidate_id      TEXT NOT NULL REFERENCES merge_candidates(id),
  target_id         TEXT NOT NULL REFERENCES integration_targets(id),
  output_id         TEXT NOT NULL REFERENCES output_artifacts(id),
  issue_id          TEXT NOT NULL REFERENCES issues(id),
  target_before     TEXT NOT NULL DEFAULT '',
  target_after      TEXT NOT NULL,
  method            TEXT NOT NULL,
  provider_evidence TEXT NOT NULL DEFAULT '{}',
  created_at        INTEGER NOT NULL
);
CREATE TRIGGER integration_evidence_immutable BEFORE UPDATE ON integration_evidence
  BEGIN SELECT RAISE(ABORT, 'integration_evidence is immutable'); END;
CREATE TRIGGER integration_evidence_no_delete BEFORE DELETE ON integration_evidence
  BEGIN SELECT RAISE(ABORT, 'integration_evidence is immutable'); END;

CREATE TABLE external_links (
  id           TEXT PRIMARY KEY,
  project_id   TEXT NOT NULL REFERENCES projects(id),
  issue_id     TEXT REFERENCES issues(id),
  provider     TEXT NOT NULL,
  kind         TEXT NOT NULL,
  external_key TEXT NOT NULL,
  url          TEXT NOT NULL DEFAULT '',
  title        TEXT NOT NULL DEFAULT '',
  observed_at  INTEGER NOT NULL,
  UNIQUE (project_id, provider, kind, external_key)
);

CREATE TABLE commands (
  id              TEXT PRIMARY KEY,
  scope           TEXT NOT NULL,
  idempotency_key TEXT NOT NULL,
  principal       TEXT NOT NULL,
  request_hash    TEXT NOT NULL,
  response        TEXT NOT NULL,
  created_at      INTEGER NOT NULL,
  UNIQUE (scope, idempotency_key)
);

CREATE TABLE operations (
  id           TEXT PRIMARY KEY,
  project_id   TEXT REFERENCES projects(id),
  kind         TEXT NOT NULL,
  state        TEXT NOT NULL CHECK (state IN ('PENDING','RUNNING','SUCCEEDED','FAILED','UNKNOWN','CANCELLED')),
  resource_key TEXT NOT NULL DEFAULT '',
  command_id   TEXT,
  correlation  TEXT NOT NULL DEFAULT '',
  result       TEXT NOT NULL DEFAULT '{}',
  error        TEXT NOT NULL DEFAULT '',
  created_at   INTEGER NOT NULL,
  updated_at   INTEGER NOT NULL
);

CREATE TABLE outbox (
  id            TEXT PRIMARY KEY,
  operation_id  TEXT NOT NULL REFERENCES operations(id),
  project_id    TEXT REFERENCES projects(id),
  kind          TEXT NOT NULL,
  payload       TEXT NOT NULL,
  dedupe_key    TEXT NOT NULL UNIQUE,
  state         TEXT NOT NULL CHECK (state IN ('PENDING','RUNNING','DONE','FAILED','UNKNOWN','CANCELLED')),
  attempt_count INTEGER NOT NULL DEFAULT 0,
  retry_at      INTEGER NOT NULL,
  last_error    TEXT NOT NULL DEFAULT '',
  created_at    INTEGER NOT NULL,
  updated_at    INTEGER NOT NULL
);
CREATE INDEX pending_outbox ON outbox(state, retry_at);

CREATE TABLE dispatches (
  id           TEXT PRIMARY KEY,
  attempt_id   TEXT NOT NULL REFERENCES execution_attempts(id),
  session_id   TEXT REFERENCES sessions(id),
  operation_id TEXT REFERENCES operations(id),
  purpose      TEXT NOT NULL,
  state        TEXT NOT NULL,
  created_at   INTEGER NOT NULL,
  acked_at     INTEGER
);

CREATE TABLE events (
  seq                      INTEGER PRIMARY KEY AUTOINCREMENT,
  event_id                 TEXT NOT NULL UNIQUE,
  project_id               TEXT NOT NULL REFERENCES projects(id),
  previous_project_version INTEGER NOT NULL,
  project_version          INTEGER NOT NULL,
  type                     TEXT NOT NULL,
  aggregate_type           TEXT NOT NULL,
  aggregate_id             TEXT NOT NULL,
  aggregate_version        INTEGER NOT NULL,
  occurred_at              INTEGER NOT NULL,
  recorded_at              INTEGER NOT NULL,
  payload                  TEXT NOT NULL
);
CREATE UNIQUE INDEX one_project_version_event ON events(project_id, project_version);
CREATE INDEX project_event_replay ON events(project_id, seq);

CREATE TABLE catalog_events (
  seq             INTEGER PRIMARY KEY AUTOINCREMENT,
  catalog_version INTEGER NOT NULL UNIQUE,
  project_id      TEXT NOT NULL,
  payload         TEXT NOT NULL,
  recorded_at     INTEGER NOT NULL
);

CREATE TABLE audit_entries (
  id           TEXT PRIMARY KEY,
  project_id   TEXT,
  actor        TEXT NOT NULL,
  action       TEXT NOT NULL,
  subject_type TEXT NOT NULL,
  subject_id   TEXT NOT NULL,
  decision     TEXT NOT NULL DEFAULT '',
  reason_code  TEXT NOT NULL DEFAULT '',
  detail       TEXT NOT NULL DEFAULT '{}',
  created_at   INTEGER NOT NULL
);
CREATE INDEX audit_by_project ON audit_entries(project_id, created_at, id);
CREATE TRIGGER audit_append_only_update BEFORE UPDATE ON audit_entries
  BEGIN SELECT RAISE(ABORT, 'audit_entries are append-only'); END;
CREATE TRIGGER audit_append_only_delete BEFORE DELETE ON audit_entries
  BEGIN SELECT RAISE(ABORT, 'audit_entries are append-only'); END;

CREATE TABLE ui_auth_sessions (
  id         TEXT PRIMARY KEY,
  token_hash TEXT NOT NULL UNIQUE,
  principal  TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL,
  revoked_at INTEGER
);

CREATE TABLE ui_bootstrap_codes (
  code_hash  TEXT PRIMARY KEY,
  expires_at INTEGER NOT NULL,
  used_at    INTEGER
);
