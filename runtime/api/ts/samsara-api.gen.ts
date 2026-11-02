// Code generated from runtime/api/schemas by `go test ./api -run TestTypeScriptUpToDate -update`. DO NOT EDIT.
//
// int64 counters, versions and sequences are decimal strings (Decimal).
// Enum-typed response fields use Open<T>: a newer runtime may send a value
// this build does not know (03 §12.1). Narrow with the is<Enum> guards and
// render anything else as unknown instead of failing.

/** A value of T, or a value added by a newer runtime. */
export type Open<T extends string> = T | (string & {});

/** Returns v when it is one of values, otherwise undefined. */
export function known<T extends string>(values: readonly T[], v: string | null | undefined): T | undefined {
  return typeof v === "string" && (values as readonly string[]).includes(v) ? (v as T) : undefined;
}

// ---- common.schema.json ----

export const ApprovalKindValues = ["PLAN", "RESULT", "MERGE", "RECOVERY"] as const;
export type ApprovalKind = (typeof ApprovalKindValues)[number];
export function isApprovalKind(v: unknown): v is ApprovalKind {
  return known(ApprovalKindValues, typeof v === "string" ? v : undefined) !== undefined;
}

export const ApprovalStateValues = ["PENDING", "APPROVED", "REJECTED", "INVALIDATED", "CANCELLED"] as const;
export type ApprovalState = (typeof ApprovalStateValues)[number];
export function isApprovalState(v: unknown): v is ApprovalState {
  return known(ApprovalStateValues, typeof v === "string" ? v : undefined) !== undefined;
}

export const AttemptStateValues = ["PENDING", "STARTING", "RUNNING", "PAUSING", "PAUSED", "IMPLEMENTED", "VERIFYING", "VERIFIED", "FAILED", "CANCEL_REQUESTED", "CANCELLED", "LOST"] as const;
export type AttemptState = (typeof AttemptStateValues)[number];
export function isAttemptState(v: unknown): v is AttemptState {
  return known(AttemptStateValues, typeof v === "string" ? v : undefined) !== undefined;
}

export const BackendAvailabilityValues = ["AVAILABLE", "DEGRADED", "UNAVAILABLE"] as const;
export type BackendAvailability = (typeof BackendAvailabilityValues)[number];
export function isBackendAvailability(v: unknown): v is BackendAvailability {
  return known(BackendAvailabilityValues, typeof v === "string" ? v : undefined) !== undefined;
}

export const BackendKindValues = ["herdr", "tmux", "standalone", "fake"] as const;
export type BackendKind = (typeof BackendKindValues)[number];
export function isBackendKind(v: unknown): v is BackendKind {
  return known(BackendKindValues, typeof v === "string" ? v : undefined) !== undefined;
}

export const CandidateStateValues = ["WAITING", "VALIDATING", "READY", "PUBLISHING", "MERGED", "STALE_BASE", "CONFLICT", "FAILED", "CANCELLED"] as const;
export type CandidateState = (typeof CandidateStateValues)[number];
export function isCandidateState(v: unknown): v is CandidateState {
  return known(CandidateStateValues, typeof v === "string" ? v : undefined) !== undefined;
}

/** int64 counter, version or sequence rendered as a decimal string so JavaScript never loses precision. */
export type Decimal = string;

export const DownstreamPhaseValues = ["EXECUTION", "INTEGRATION"] as const;
export type DownstreamPhase = (typeof DownstreamPhaseValues)[number];
export function isDownstreamPhase(v: unknown): v is DownstreamPhase {
  return known(DownstreamPhaseValues, typeof v === "string" ? v : undefined) !== undefined;
}

export const DrainStateValues = ["NONE", "REQUESTED", "DRAINING", "COMPLETE", "NEEDS_ATTENTION"] as const;
export type DrainState = (typeof DrainStateValues)[number];
export function isDrainState(v: unknown): v is DrainState {
  return known(DrainStateValues, typeof v === "string" ? v : undefined) !== undefined;
}

/** Stable error identifier (03 §7.3). */
export const ErrorCodeValues = ["INVALID_REQUEST", "SESSION_EXPIRED", "ACTION_FORBIDDEN", "RESOURCE_NOT_FOUND", "DEPENDENCY_UNMET", "DELIVERY_UNKNOWN", "IDEMPOTENCY_KEY_REUSED", "CONFLICT", "VERSION_MISMATCH", "APPROVAL_SUBJECT_CHANGED", "APPROVAL_REQUIRED", "UNSUPPORTED_CAPABILITY", "RATE_LIMITED", "RUNTIME_NOT_READY", "STORAGE_UNAVAILABLE", "RUN_NOT_FINALIZED", "PROTOCOL_MISMATCH", "PROJECT_NOT_ENABLED", "PROJECT_NOT_TRUSTED", "CYCLE_DETECTED", "OWNERSHIP_CONFLICT", "TARGET_CHECKED_OUT", "STALE_FENCE", "INTERNAL"] as const;
export type ErrorCode = (typeof ErrorCodeValues)[number];
export function isErrorCode(v: unknown): v is ErrorCode {
  return known(ErrorCodeValues, typeof v === "string" ? v : undefined) !== undefined;
}

export interface ErrorDetail {
  code: Open<ErrorCode>;
  details?: Record<string, unknown>;
  message: string;
  retryable: boolean;
}

/** Body of every non-2xx HTTP response. */
export interface ErrorResponse {
  error: HTTPError;
}

export const FreshnessValues = ["LIVE", "PARTIAL", "STALE", "UNAVAILABLE"] as const;
export type Freshness = (typeof FreshnessValues)[number];
export function isFreshness(v: unknown): v is Freshness {
  return known(FreshnessValues, typeof v === "string" ? v : undefined) !== undefined;
}

/** HTTP error detail: the shared error plus the echoed X-Request-Id. */
export interface HTTPError {
  code: Open<ErrorCode>;
  details?: Record<string, unknown>;
  message: string;
  request_id: string;
  retryable: boolean;
}

export const InputStateValues = ["READY", "BUSY", "NATIVE_PERMISSION", "USER_OWNED", "UNKNOWN"] as const;
export type InputState = (typeof InputStateValues)[number];
export function isInputState(v: unknown): v is InputState {
  return known(InputStateValues, typeof v === "string" ? v : undefined) !== undefined;
}

export const IntegrationAuthorityValues = ["manual", "github_queue", "samsara_local"] as const;
export type IntegrationAuthority = (typeof IntegrationAuthorityValues)[number];
export function isIntegrationAuthority(v: unknown): v is IntegrationAuthority {
  return known(IntegrationAuthorityValues, typeof v === "string" ? v : undefined) !== undefined;
}

export const IssueLifecycleValues = ["OPEN", "COMPLETED", "CANCELLED"] as const;
export type IssueLifecycle = (typeof IssueLifecycleValues)[number];
export function isIssueLifecycle(v: unknown): v is IssueLifecycle {
  return known(IssueLifecycleValues, typeof v === "string" ? v : undefined) !== undefined;
}

export const LeaseStateValues = ["ACTIVE", "REVOKED", "EXPIRED", "RELEASED"] as const;
export type LeaseState = (typeof LeaseStateValues)[number];
export function isLeaseState(v: unknown): v is LeaseState {
  return known(LeaseStateValues, typeof v === "string" ? v : undefined) !== undefined;
}

export const LivenessValues = ["ALIVE", "EXITED", "UNKNOWN"] as const;
export type Liveness = (typeof LivenessValues)[number];
export function isLiveness(v: unknown): v is Liveness {
  return known(LivenessValues, typeof v === "string" ? v : undefined) !== undefined;
}

export const MilestoneValues = ["IMPLEMENTED", "VERIFIED", "RESULT_APPROVED", "INTEGRATED"] as const;
export type Milestone = (typeof MilestoneValues)[number];
export function isMilestone(v: unknown): v is Milestone {
  return known(MilestoneValues, typeof v === "string" ? v : undefined) !== undefined;
}

export const NativeQueueStateValues = ["NONE", "REQUESTED", "QUEUED", "VALIDATING", "MERGED", "REMOVED", "UNKNOWN"] as const;
export type NativeQueueState = (typeof NativeQueueStateValues)[number];
export function isNativeQueueState(v: unknown): v is NativeQueueState {
  return known(NativeQueueStateValues, typeof v === "string" ? v : undefined) !== undefined;
}

export const OperationStateValues = ["PENDING", "RUNNING", "SUCCEEDED", "FAILED", "UNKNOWN", "CANCELLED"] as const;
export type OperationState = (typeof OperationStateValues)[number];
export function isOperationState(v: unknown): v is OperationState {
  return known(OperationStateValues, typeof v === "string" ? v : undefined) !== undefined;
}

/** RFC 3339 instant, or an empty string when the moment has not happened. */
export type OptionalTimestamp = string;

export const OwnershipValues = ["managed", "external"] as const;
export type Ownership = (typeof OwnershipValues)[number];
export function isOwnership(v: unknown): v is Ownership {
  return known(OwnershipValues, typeof v === "string" ? v : undefined) !== undefined;
}

export const ProjectActivationValues = ["UNCONFIGURED", "ENABLED", "DISABLED"] as const;
export type ProjectActivation = (typeof ProjectActivationValues)[number];
export function isProjectActivation(v: unknown): v is ProjectActivation {
  return known(ProjectActivationValues, typeof v === "string" ? v : undefined) !== undefined;
}

export const RegistryStateValues = ["PRESENT", "MISSING", "ARCHIVED"] as const;
export type RegistryState = (typeof RegistryStateValues)[number];
export function isRegistryState(v: unknown): v is RegistryState {
  return known(RegistryStateValues, typeof v === "string" ? v : undefined) !== undefined;
}

/** Why an event cursor cannot be served; the client must reload the snapshot. */
export const ResetReasonValues = ["CURSOR_INVALID", "CURSOR_SCOPE_MISMATCH", "DATABASE_GENERATION_CHANGED", "CURSOR_EXPIRED"] as const;
export type ResetReason = (typeof ResetReasonValues)[number];
export function isResetReason(v: unknown): v is ResetReason {
  return known(ResetReasonValues, typeof v === "string" ? v : undefined) !== undefined;
}

export const RunStateValues = ["PLANNED", "ACTIVE", "PAUSED", "COMPLETED", "FAILED", "CANCELLED"] as const;
export type RunState = (typeof RunStateValues)[number];
export function isRunState(v: unknown): v is RunState {
  return known(RunStateValues, typeof v === "string" ? v : undefined) !== undefined;
}

export const SessionDesiredStateValues = ["ACTIVE", "PAUSED", "STOPPED"] as const;
export type SessionDesiredState = (typeof SessionDesiredStateValues)[number];
export function isSessionDesiredState(v: unknown): v is SessionDesiredState {
  return known(SessionDesiredStateValues, typeof v === "string" ? v : undefined) !== undefined;
}

/** RFC 3339 instant. */
export type Timestamp = string;

export const WorktreeStateValues = ["CREATING", "READY", "IN_USE", "QUIESCING", "RETAINED", "CLEANUP_ELIGIBLE", "REMOVED", "QUARANTINED"] as const;
export type WorktreeState = (typeof WorktreeStateValues)[number];
export function isWorktreeState(v: unknown): v is WorktreeState {
  return known(WorktreeStateValues, typeof v === "string" ? v : undefined) !== undefined;
}

// ---- config.schema.json ----

export const ApprovalModeValues = ["manual", "auto"] as const;
export type ApprovalMode = (typeof ApprovalModeValues)[number];
export function isApprovalMode(v: unknown): v is ApprovalMode {
  return known(ApprovalModeValues, typeof v === "string" ? v : undefined) !== undefined;
}

export const BackendNameValues = ["herdr", "tmux", "standalone"] as const;
export type BackendName = (typeof BackendNameValues)[number];
export function isBackendName(v: unknown): v is BackendName {
  return known(BackendNameValues, typeof v === "string" ? v : undefined) !== undefined;
}

export interface Config {
  approval?: {
    implementation_start?: Open<ApprovalMode>;
    integration?: Open<ApprovalMode>;
    result?: Open<ApprovalMode>;
  } | null;
  backend?: {
    allow_fallback?: boolean;
    mode?: Open<"auto" | "herdr" | "tmux" | "standalone">;
    order?: Open<BackendName>[];
  } | null;
  execution?: {
    /** At most half of lease_ttl_seconds (checked by the runtime). */
    heartbeat_seconds?: number;
    lease_ttl_seconds?: number;
    managed_worktree_required?: boolean;
    max_workers?: number;
    max_workers_per_issue?: 1;
    unknown_worker_policy?: "quarantine";
    worker_command?: string[];
  } | null;
  git?: {
    branch_prefix?: string;
    integration_authority?: Open<IntegrationAuthority>;
    merge_concurrency_per_target?: 1;
    target_ref?: string;
    worktree_retention_hours?: number;
  } | null;
  github?: {
    close_issues?: boolean;
    merge_queue?: {
      manifest_artifact?: string;
      manifest_workflow?: string;
      policy_app_id?: number;
    };
    repository?: string;
    sync?: Open<"auto" | "off">;
    sync_interval_seconds?: number;
  } | null;
  observation?: {
    collect_terminal_output?: boolean;
    event_retention_days?: number;
  } | null;
  runtime?: {
    autostart?: boolean;
    enabled?: boolean;
  } | null;
  /** Defaults to 1. */
  version?: 1;
}

// ---- entities.schema.json ----

/** Server-decided action availability; the UI never infers it (03 §13). */
export interface Action {
  action: string;
  enabled: boolean;
  reason_code?: string;
  resource_id?: string;
}

export interface ApprovalGate {
  approval_ids: string[];
  required: boolean;
  state: Open<ApprovalState> | null;
  subject_digest?: string;
}

export interface ApprovalView {
  candidate_revision?: Decimal;
  decided_at?: Timestamp;
  decision_actor?: string;
  id: string;
  issue_id?: string;
  kind: Open<ApprovalKind>;
  merge_candidate_id?: string;
  output_id?: string;
  policy_version: Decimal;
  project_id: string;
  requested_at: Timestamp;
  run_id?: string;
  state: Open<ApprovalState>;
  state_reason?: string;
  /** Canonical subject the decision binds to (base/source/policy). */
  subject: unknown;
  subject_digest: string;
  version: Decimal;
}

export interface AttemptView {
  backend_kind?: Open<BackendKind>;
  backend_reason?: string;
  id: string;
  issue_id: string;
  lease_deadline_at?: Timestamp;
  lease_fence?: Decimal;
  lease_state?: Open<LeaseState>;
  output_id?: string;
  project_id: string;
  quarantine_reason?: string;
  resume_required: boolean;
  run_generation: Decimal;
  run_id: string;
  session_id?: string;
  state: Open<AttemptState>;
  state_reason?: string;
  updated_at: Timestamp;
  version: Decimal;
  worktree_id?: string;
}

export interface BackendLocation {
  binding_id: string;
  generation: string;
  instance?: string;
  kind: Open<BackendKind>;
  /** Adapter-specific opaque location. */
  ref: unknown;
}

export interface BlockingReason {
  approval_id?: string;
  /** Reason code such as DEPENDENCY_UNMET or DEPENDENCY_APPROVAL_PENDING. */
  code: string;
  dependency_id?: string;
  downstream_phase?: Open<DownstreamPhase>;
  message: string;
  required_milestone?: Open<Milestone>;
  source_issue_id?: string;
}

export interface CandidateView {
  approval_gate: ApprovalGate | null;
  attempt_id: string;
  authority: Open<IntegrationAuthority>;
  base_sha?: string;
  blocking_reasons: BlockingReason[];
  candidate_sha?: string;
  conflict?: ConflictView;
  enqueued: boolean;
  evidence_id?: string;
  id: string;
  issue_id: string;
  native_queue_state: Open<NativeQueueState>;
  output_id: string;
  project_id: string;
  publication_observation?: string;
  queue_position: Decimal;
  resolution_attempt_id?: string;
  revalidation_count?: Decimal;
  revision: Decimal;
  run_id: string;
  source_sha: string;
  starvation_risk?: boolean;
  state: Open<CandidateState>;
  state_reason?: string;
  superseded_by?: string;
  target_id: string;
  target_ref: string;
  version: Decimal;
  waiting_since?: string;
}

/** Backend capability support (01 §5). */
export interface Capability {
  constraints?: Record<string, string>;
  /** Empty when the backend was not probed (adapter missing, binary absent or probe error). */
  level: Open<"SUPPORTED" | "CONDITIONAL" | "UNSUPPORTED" | "">;
  reason_code?: string;
}

export interface ClaudeSupport {
  block_on_failure: boolean;
  failure_injection_verified: boolean;
  version: string;
}

export interface ConflictFile {
  kind: string;
  path: string;
  types: string[];
}

export interface ConflictView {
  files: ConflictFile[];
  kinds: string[];
  source: string;
}

export interface DependencyView {
  dependency_id: string;
  downstream_phase: Open<DownstreamPhase>;
  graph_version: Decimal;
  invalidation_policy: string;
  met: boolean;
  predecessor_issue_id: string;
  project_id: string;
  required_milestone: Open<Milestone>;
  required_output_id: string | null;
  required_run_generation: Decimal;
  run_id: string;
  successor_issue_id: string;
  target_ref?: string;
  target_repository_id?: string;
  version: Decimal;
}

export interface GateMember {
  dependency_id: string;
  issue_id: string;
  met: boolean;
  required_milestone: Open<Milestone>;
}

/** AND gate over two or more requirements of one successor phase. */
export interface GateView {
  downstream_phase: Open<DownstreamPhase>;
  id: string;
  members: GateMember[];
  membership_digest: string;
  membership_version: string;
  open: boolean;
  run_generation: Decimal;
  run_id: string;
  successor_issue_id: string;
}

export interface GitHubHistory {
  error: string;
  freshness: Open<Freshness>;
  items: GitHubHistoryItem[];
  last_attempt_at: string | null;
  last_synced_at: string | null;
  repository: string;
  retry_at: string | null;
  sync: string;
  truncated: boolean;
}

export interface GitHubHistoryItem {
  detail: unknown;
  external_key: string;
  id: string;
  issue_id: string;
  kind: string;
  observed_at: string;
  provider: string;
  source: string;
  state: string;
  synced_at: string | null;
  title: string;
  url: string;
}

export interface IssueDetail {
  attempts: AttemptView[];
  dependencies: DependencyView[];
  issue: IssueView;
  merge_candidates: CandidateView[];
  outputs: (Record<string, unknown>)[];
}

export interface IssueView {
  acceptance_criteria: string[];
  available_actions: Action[];
  blocking_reasons: BlockingReason[];
  check_profile: string;
  client_key: string;
  current_attempt_id?: string;
  display_status: string;
  execution_state?: Open<AttemptState>;
  id: string;
  integration_state?: Open<CandidateState>;
  lifecycle: Open<IssueLifecycle>;
  project_id: string;
  required_capabilities: string[];
  resume_required: boolean;
  run_id?: string;
  title: string;
  version: Decimal;
}

/** External effect. UNKNOWN means the outcome is not known and is never rewritten to success or failure automatically. */
export interface OperationView {
  command_id?: string;
  created_at: Timestamp;
  error?: string;
  id: string;
  kind: string;
  project_id?: string;
  resource_key?: string;
  /** Operation result JSON, or null. */
  result: unknown;
  state: Open<OperationState>;
  updated_at: Timestamp;
}

export interface ProbeResult {
  availability: Open<BackendAvailability>;
  capabilities: Record<string, Capability>;
  instance?: string;
  kind: Open<BackendKind>;
  probed_at: string;
  reasons?: string[];
  schema_sha256?: string;
  version?: string;
}

/** ProjectView plus sidebar counters. */
export interface ProjectSummary {
  activation: Open<ProjectActivation>;
  approval_pending: number;
  blocked: number;
  created_at: Timestamp;
  display_name: string;
  drain_state: Open<DrainState>;
  graph_version: Decimal;
  id: string;
  integration_pending: number;
  last_activity_at: OptionalTimestamp;
  latest_run_id?: string;
  latest_run_state?: Open<RunState>;
  policy_version: Decimal;
  project_version: Decimal;
  registry_state: Open<RegistryState>;
  root_path: string;
  running: number;
  version: Decimal;
}

export interface ProjectView {
  activation: Open<ProjectActivation>;
  created_at: Timestamp;
  display_name: string;
  drain_state: Open<DrainState>;
  graph_version: Decimal;
  id: string;
  last_activity_at: OptionalTimestamp;
  policy_version: Decimal;
  project_version: Decimal;
  registry_state: Open<RegistryState>;
  root_path: string;
  version: Decimal;
}

export interface RefreshProjectResult {
  github: string;
  status: "ACCEPTED";
}

export interface RunView {
  alias_mapping: Record<string, string> | null;
  created_at: Timestamp;
  ended_at?: Timestamp;
  graph_version: Decimal;
  id: string;
  plan_digest: string;
  project_id: string;
  run_generation: Decimal;
  started_at?: Timestamp;
  state: Open<RunState>;
  version: Decimal;
}

export interface RuntimeInfo {
  backends: Record<string, ProbeResult>;
  claude: ClaudeSupport;
  database_generation: string;
  host_id: string;
  pid: number;
  ready: boolean;
  runtime_epoch: string;
  schema_version: Decimal;
  sqlite_version: string;
  version: string;
}

export interface SessionView {
  attempt_id?: string;
  backend?: BackendLocation;
  backend_raw_state?: string;
  claude_session_id: string;
  confidence: string;
  desired_state: Open<SessionDesiredState>;
  ended_at?: Timestamp;
  id: string;
  input_owner: string;
  input_state: Open<InputState>;
  last_hook_at?: Timestamp;
  liveness: Open<Liveness>;
  managed: boolean;
  observed_at: OptionalTimestamp;
  process?: Record<string, string>;
  project_id: string;
  source: string;
  version: Decimal;
}

export interface TargetView {
  authority: Open<IntegrationAuthority>;
  id: string;
  policy_version: Decimal;
  repository_id: string;
  target_ref: string;
  version: Decimal;
}

export interface WorktreeView {
  attempt_id?: string;
  base_sha: string;
  branch_ref: string;
  id: string;
  ownership: Open<Ownership>;
  path: string;
  project_id: string;
  repository_id?: string;
  state: Open<WorktreeState>;
  state_reason?: string;
  version: Decimal;
}

// ---- event.schema.json ----

export interface CatalogEvent {
  catalog_version: Decimal;
  payload: CatalogEventPayload;
  project_id: string;
  recorded_at: Timestamp;
  schema_version: "1";
  seq: Decimal;
}

export interface CatalogEventPayload {
  project: ProjectSummary;
}

/** Projections to upsert and ids to delete, keyed by entity kind. */
export interface ChangeSet {
  deletes: Record<string, string[]>;
  upserts: {
    approvals?: ApprovalView[];
    attempts?: AttemptView[];
    dependencies?: DependencyView[];
    gates?: GateView[];
    integration_targets?: TargetView[];
    issues?: IssueView[];
    merge_candidates?: CandidateView[];
    operations?: OperationView[];
    projects?: ProjectView[];
    runs?: RunView[];
    sessions?: SessionView[];
    worktrees?: WorktreeView[];
  };
}

/** Project event envelope. seq is global and may have gaps within one project. */
export interface ProjectEvent {
  aggregate_id: string;
  aggregate_type: string;
  aggregate_version: Decimal;
  event_id: string;
  occurred_at: Timestamp;
  payload: ProjectEventPayload;
  previous_project_version: Decimal;
  project_id: string;
  project_version: Decimal;
  recorded_at: Timestamp;
  schema_version: "1";
  seq: Decimal;
  type: string;
}

export interface ProjectEventPayload {
  changes: ChangeSet;
}

export interface ResetRequired {
  reason: Open<ResetReason>;
}

// ---- ipc.schema.json ----

export interface AttachRequest {
  ancestor_pids?: number[];
  claude_session_id?: string;
  issue_id: string;
}

export interface BackupRequest {
  dest: string;
}

export interface BackupResult {
  dest: string;
}

export interface CandidateRef {
  candidate_id: string;
}

export interface CheckProfileRequest {
  argv: string[];
  name: string;
  project_id: string;
  timeout_sec?: number;
}

export interface CleanupItem {
  branch_kept_reason?: string;
  branch_ref?: string;
  branch_removed: boolean;
  eligible: boolean;
  error?: string;
  path: string;
  reasons: string[] | null;
  state: string;
  worktree_id: string;
  worktree_removed: boolean;
}

export interface CleanupRequest {
  delete_branch?: boolean;
  dry_run?: boolean;
  project_id: string;
  worktree_ids?: string[];
}

export interface CleanupResult {
  dry_run: boolean;
  items: CleanupItem[];
}

/** Accepted command. The effect is observed through events, not this response. */
export interface CommandResult {
  command_id: string;
  operation_id?: string;
  project_version: Decimal;
  resource_version: Decimal;
  status: string;
}

export interface DecisionRequest {
  comment?: string;
  decision: Open<"APPROVE" | "REJECT" | "REVOKE">;
  subject_digest?: string;
}

export interface DispatchAckRequest {
  claude_session_id?: string;
  dispatch_id: string;
}

export type Empty = Record<string, unknown>;

export interface FinishRequest {
  claude_session_id: string;
  cwd?: string;
  event?: string;
  exit_code?: number;
  launcher_ticket?: string;
  managed_hint?: boolean;
  process?: Record<string, string>;
  process_exit?: boolean;
  protocol_version?: string;
  reason?: string;
  request_id?: string;
  source?: string;
  ticket?: string;
}

export type FocusResult = Record<string, string>;

export interface HistoryRequest {
  cursor?: string;
  include_tool_checks?: boolean;
  project_id: string;
}

export interface HookIdentity {
  claude_session_id: string;
  cwd?: string;
  event?: string;
  managed_hint?: boolean;
  process?: Record<string, string>;
  protocol_version?: string;
  request_id?: string;
  source?: string;
  ticket?: string;
}

export interface IPCDecisionRequest {
  approval_id: string;
  comment?: string;
  decision: Open<"APPROVE" | "REJECT" | "REVOKE">;
  subject_digest?: string;
}

/** Body of a non-2xx IPC response. result carries partial data when the handler returned some. */
export interface IPCError {
  error: ErrorDetail;
  result?: unknown;
}

/** IPC route catalog: POST <path> with body `request`; 200 body is `response`, any other status is IPCError. Headers: Samsara-Protocol: 1 (required), Samsara-Request-Id, Samsara-Deadline-Ms, Idempotency-Key, If-Match. */
export interface IpcRoutes {
  "/ipc/v1/approvals/decide": {
    request: IPCDecisionRequest;
    response: CommandResult;
  };
  "/ipc/v1/approvals/list": {
    request: ProjectCursor;
    response: Page;
  };
  "/ipc/v1/backup": {
    request: BackupRequest;
    response: BackupResult;
  };
  "/ipc/v1/daemon/stop": {
    request: Empty;
    response: StopResult;
  };
  "/ipc/v1/dispatches/ack": {
    request: DispatchAckRequest;
    response: OK;
  };
  "/ipc/v1/history": {
    request: HistoryRequest;
    response: Page;
  };
  "/ipc/v1/issues/get": {
    request: IssueRef;
    response: IssueDetail;
  };
  "/ipc/v1/issues/retry": {
    request: IssueRef;
    response: AttemptView;
  };
  "/ipc/v1/leases/renew": {
    request: RenewRequest;
    response: RenewResult;
  };
  "/ipc/v1/links/add": {
    request: LinkRequest;
    response: OK;
  };
  "/ipc/v1/lookup": {
    request: LookupRequest;
    response: unknown;
  };
  "/ipc/v1/merge/cancel": {
    request: CandidateRef;
    response: CommandResult;
  };
  "/ipc/v1/merge/enqueue": {
    request: CandidateRef;
    response: CommandResult;
  };
  "/ipc/v1/merge/inspect": {
    request: CandidateRef;
    response: CandidateView;
  };
  "/ipc/v1/merge/prioritize": {
    request: PrioritizeRequest;
    response: CommandResult;
  };
  "/ipc/v1/merge/queues": {
    request: ProjectRef;
    response: unknown;
  };
  "/ipc/v1/merge/resolve": {
    request: CandidateRef;
    response: CommandResult;
  };
  "/ipc/v1/outputs/submit": {
    request: OutputSubmitRequest;
    response: OutputSubmitResult;
  };
  "/ipc/v1/plans/apply": {
    request: Plan;
    response: PlanApplyResult;
  };
  "/ipc/v1/plans/revise": {
    request: PlanReviseRequest;
    response: PlanReviseResult;
  };
  "/ipc/v1/plans/validate": {
    request: Plan;
    response: PlanValidation;
  };
  "/ipc/v1/profiles/register": {
    request: CheckProfileRequest;
    response: OK;
  };
  "/ipc/v1/projects/archive": {
    request: ProjectRef;
    response: OK;
  };
  "/ipc/v1/projects/disable": {
    request: ProjectRef;
    response: ProjectView;
  };
  "/ipc/v1/projects/list": {
    request: ListProjectsRequest;
    response: ProjectSummary[] | null;
  };
  "/ipc/v1/projects/register": {
    request: RegisterProjectRequest;
    response: RegisterProjectResult;
  };
  "/ipc/v1/projects/relink": {
    request: RelinkProjectRequest;
    response: OK;
  };
  "/ipc/v1/projects/snapshot": {
    request: ProjectRef;
    response: ProjectSnapshot;
  };
  "/ipc/v1/runs/cancel": {
    request: RunRef;
    response: RunCommandResult;
  };
  "/ipc/v1/runs/pause": {
    request: RunRef;
    response: RunCommandResult;
  };
  "/ipc/v1/runs/resume": {
    request: RunRef;
    response: RunCommandResult;
  };
  "/ipc/v1/runs/start": {
    request: RunRef;
    response: RunCommandResult;
  };
  "/ipc/v1/runtime": {
    request: Empty;
    response: RuntimeInfo;
  };
  "/ipc/v1/sessions/attach": {
    request: AttachRequest;
    response: RegisterResult;
  };
  "/ipc/v1/sessions/finish": {
    request: FinishRequest;
    response: OK;
  };
  "/ipc/v1/sessions/focus": {
    request: SessionRef;
    response: FocusResult;
  };
  "/ipc/v1/sessions/observe": {
    request: ObserveRequest;
    response: OK;
  };
  "/ipc/v1/sessions/register": {
    request: HookIdentity;
    response: RegisterResult;
  };
  "/ipc/v1/sessions/release-input": {
    request: SessionRef;
    response: CommandResult;
  };
  "/ipc/v1/tools/check": {
    request: ToolCheckRequest;
    response: ToolDecision;
  };
  "/ipc/v1/tools/result": {
    request: ObserveRequest;
    response: OK;
  };
  "/ipc/v1/ui/bootstrap": {
    request: Empty;
    response: UIBootstrap;
  };
  "/ipc/v1/worktrees/cleanup": {
    request: CleanupRequest;
    response: CleanupResult;
  };
}

export interface IssueRef {
  issue_id: string;
}

export interface LinkRequest {
  external_key: string;
  issue_id?: string;
  kind: string;
  project_id: string;
  provider: string;
  title?: string;
  url?: string;
}

export interface ListProjectsRequest {
  include_archived?: boolean;
}

export interface LookupRequest {
  id: string;
  kind: string;
}

export interface OK {
  ok: true;
}

export interface ObserveRequest {
  agent_id?: string;
  claude_session_id: string;
  cwd?: string;
  dispatch_id?: string;
  event?: string;
  failed?: boolean;
  managed_hint?: boolean;
  notification_type?: string;
  process?: Record<string, string>;
  protocol_version?: string;
  request_id?: string;
  source?: string;
  ticket?: string;
  tool_name?: string;
  tool_use_id?: string;
}

export interface OutputSubmitRequest {
  ancestor_pids?: number[];
  attempt_id?: string;
  claude_session_id?: string;
  fence?: string;
  pull_request?: string;
  summary?: string;
  ticket?: string;
}

export interface OutputSubmitResult {
  attempt_id: string;
  manifest_digest: string;
  output_id: string;
  source_sha: string;
  uncommitted_paths: string[] | null;
  verify_operation_id: string;
}

export interface PlanApplyResult {
  graph_version: Decimal;
  issue_mapping: Record<string, string>;
  plan_digest: string;
  run_generation: Decimal;
  run_id: string;
  run_state: Open<RunState>;
}

export interface PlanReviseRequest {
  expected_run_generation: Decimal;
  plan: Plan;
  run_id: string;
}

export interface PlanReviseResult {
  affected_issue_ids: string[];
  change_kind: string;
  graph_version: Decimal;
  invalidated_approval_ids: string[];
  issue_mapping: Record<string, string>;
  plan_approval_id?: string;
  plan_digest: string;
  run_generation: Decimal;
  run_id: string;
  run_state: Open<RunState>;
  superseded_output_ids: string[];
}

export interface PlanValidation {
  blocking_run_id?: string;
  current_graph_version?: Decimal;
  plan_digest: string;
  problems: PlanProblem[];
  project_validated: boolean;
  resolved_target_ref?: string;
  valid: boolean;
}

export interface PrioritizeRequest {
  before?: string;
  candidate_id: string;
  reason: string;
}

export interface ProjectCursor {
  cursor?: string;
  project_id: string;
}

export interface ProjectRef {
  project_id: string;
}

export interface RegisterProjectRequest {
  path: string;
}

export interface RegisterProjectResult {
  activation: Open<ProjectActivation>;
  created: boolean;
  project_id: string;
  root: string;
  target_id?: string;
}

export interface RegisterResult {
  attempt_id?: string;
  context?: string;
  fence?: Decimal;
  lease_id?: string;
  managed: boolean;
  reason?: string;
  session_id?: string;
}

export interface RelinkProjectRequest {
  path: string;
  project_id: string;
}

export interface RenewRequest {
  attempt_id: string;
  ticket: string;
}

export interface RenewResult {
  deadline_at?: Timestamp;
  fence?: Decimal;
  lease_id?: string;
  reason?: string;
  renewed: boolean;
}

export interface RunCommandResult {
  approval_id?: string;
  run_id: string;
  state: Open<RunState>;
  version: Decimal;
}

export interface RunRef {
  run_id: string;
}

export interface SessionRef {
  session_id: string;
}

export interface StopResult {
  status: "STOPPING";
}

export interface ToolCheckRequest {
  affected_paths?: string[];
  attempt_id?: string;
  background?: boolean;
  claude_session_id: string;
  cwd?: string;
  event?: string;
  fence?: string;
  lease_id?: string;
  managed_hint?: boolean;
  payload_digest?: string;
  process?: Record<string, string>;
  protocol_version?: string;
  request_id?: string;
  runtime_epoch?: string;
  source?: string;
  ticket?: string;
  tool_name: string;
  tool_use_id?: string;
}

/** Hook policy decision. PASS never maps to permissionDecision allow. */
export interface ToolDecision {
  current_fence?: Decimal;
  decision: Open<"PASS" | "DENY">;
  message: string;
  project_version?: Decimal;
  reason_code: string;
  retry_after_ms?: number;
}

export interface UIBootstrap {
  expires_in: string;
  url: string;
}

// ---- plan.schema.json ----

export interface Plan {
  dependencies?: PlanDependency[];
  expected_graph_version: Decimal;
  issues: PlanIssue[];
  project_id: string;
  spec_version: "1";
}

export const PlanCapabilityValues = ["create_worker", "attach_session", "submit_prompt", "observe_process", "observe_agent", "read_output", "request_interrupt", "restore_conversation"] as const;
export type PlanCapability = (typeof PlanCapabilityValues)[number];
export function isPlanCapability(v: unknown): v is PlanCapability {
  return known(PlanCapabilityValues, typeof v === "string" ? v : undefined) !== undefined;
}

export interface PlanDependency {
  /** Default EXECUTION. */
  downstream_phase?: Open<DownstreamPhase>;
  predecessor_key: string;
  /** Default INTEGRATED. */
  required_milestone?: Open<Milestone>;
  successor_key: string;
  target_ref?: string;
  target_repository_id?: string;
}

export interface PlanIssue {
  acceptance_criteria?: string[];
  check_profile?: string;
  client_key: string;
  required_capabilities?: Open<PlanCapability>[];
  target_ref?: string;
  target_repository_id?: string;
  title: string;
}

export interface PlanProblem {
  code: string;
  message: string;
  path: string;
}

// ---- snapshot.schema.json ----

export interface CatalogSnapshot {
  catalog_version: Decimal;
  database_generation: string;
  projects: ProjectSummary[];
  schema_version: "1";
  snapshot_cursor: string;
}

/** Keyset page. Item shape depends on the endpoint. */
export interface Page {
  items: (Record<string, unknown>)[];
  next_cursor?: string;
}

export interface ProjectList {
  items: ProjectSummary[];
}

export interface ProjectSnapshot {
  database_generation: string;
  entities: SnapshotEntities;
  freshness: SnapshotFreshness;
  generated_at: Timestamp;
  graph_version: Decimal;
  project: ProjectView;
  project_id: string;
  project_version: Decimal;
  runtime_epoch: string;
  schema_version: "1";
  snapshot_cursor: string;
  summary: SnapshotSummary;
}

export interface SnapshotEntities {
  approvals: ApprovalView[];
  attempts: AttemptView[];
  dependencies: DependencyView[];
  gates: GateView[];
  integration_targets: TargetView[];
  issues: IssueView[];
  merge_candidates: CandidateView[];
  runs: RunView[];
  sessions: SessionView[];
  worktrees: WorktreeView[];
}

export interface SnapshotFreshness {
  backend_observation: Open<Freshness>;
  github: Open<Freshness>;
  runtime: Open<Freshness>;
}

export interface SnapshotSummary {
  approval_pending: number;
  blocked: number;
  integration_pending: number;
  running: number;
}
