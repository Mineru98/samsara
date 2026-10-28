package domain

// Enumerations follow 03-contracts-and-data-model.md §3. Values are stored and
// transported verbatim; they are never merged into a single status string.

// Project activation (user decision).
const (
	ActivationUnconfigured = "UNCONFIGURED"
	ActivationEnabled      = "ENABLED"
	ActivationDisabled     = "DISABLED"
)

// Project registry state (disk presence / archival).
const (
	RegistryPresent  = "PRESENT"
	RegistryMissing  = "MISSING"
	RegistryArchived = "ARCHIVED"
)

// Project drain state (disable processing progress).
const (
	DrainNone           = "NONE"
	DrainRequested      = "REQUESTED"
	DrainDraining       = "DRAINING"
	DrainComplete       = "COMPLETE"
	DrainNeedsAttention = "NEEDS_ATTENTION"
)

// Run state.
const (
	RunPlanned   = "PLANNED"
	RunActive    = "ACTIVE"
	RunPaused    = "PAUSED"
	RunCompleted = "COMPLETED"
	RunFailed    = "FAILED"
	RunCancelled = "CANCELLED"
)

// Issue lifecycle.
const (
	IssueOpen      = "OPEN"
	IssueCompleted = "COMPLETED"
	IssueCancelled = "CANCELLED"
)

// Backend availability.
const (
	BackendAvailable   = "AVAILABLE"
	BackendDegraded    = "DEGRADED"
	BackendUnavailable = "UNAVAILABLE"
)

// Operation state.
const (
	OpPending   = "PENDING"
	OpRunning   = "RUNNING"
	OpSucceeded = "SUCCEEDED"
	OpFailed    = "FAILED"
	OpUnknown   = "UNKNOWN"
	OpCancelled = "CANCELLED"
)

// Execution attempt state (§3.2).
const (
	AttemptPending         = "PENDING"
	AttemptStarting        = "STARTING"
	AttemptRunning         = "RUNNING"
	AttemptPausing         = "PAUSING"
	AttemptPaused          = "PAUSED"
	AttemptImplemented     = "IMPLEMENTED"
	AttemptVerifying       = "VERIFYING"
	AttemptVerified        = "VERIFIED"
	AttemptFailed          = "FAILED"
	AttemptCancelRequested = "CANCEL_REQUESTED"
	AttemptCancelled       = "CANCELLED"
	AttemptLost            = "LOST"
)

// AttemptLive reports whether an attempt still occupies its issue's single
// execution slot.
func AttemptLive(state string) bool {
	switch state {
	case AttemptPending, AttemptStarting, AttemptRunning, AttemptPausing,
		AttemptPaused, AttemptCancelRequested:
		return true
	}
	return false
}

// AttemptTerminal reports whether no further transition is expected.
func AttemptTerminal(state string) bool {
	switch state {
	case AttemptFailed, AttemptCancelled, AttemptLost:
		return true
	}
	return false
}

// Session observation.
const (
	SessionDesiredActive  = "ACTIVE"
	SessionDesiredPaused  = "PAUSED"
	SessionDesiredStopped = "STOPPED"

	LivenessAlive   = "ALIVE"
	LivenessExited  = "EXITED"
	LivenessUnknown = "UNKNOWN"

	InputReady            = "READY"
	InputBusy             = "BUSY"
	InputNativePermission = "NATIVE_PERMISSION"
	InputUserOwned        = "USER_OWNED"
	InputUnknown          = "UNKNOWN"
)

// Lease state.
const (
	LeaseActive   = "ACTIVE"
	LeaseRevoked  = "REVOKED"
	LeaseExpired  = "EXPIRED"
	LeaseReleased = "RELEASED"
)

// Approval kind and state. There is deliberately no NATIVE_PERMISSION kind:
// Claude's own permission prompts are never Samsara approvals (§3.3).
const (
	ApprovalPlan     = "PLAN"
	ApprovalResult   = "RESULT"
	ApprovalMerge    = "MERGE"
	ApprovalRecovery = "RECOVERY"

	ApprovalPending     = "PENDING"
	ApprovalApproved    = "APPROVED"
	ApprovalRejected    = "REJECTED"
	ApprovalInvalidated = "INVALIDATED"
	ApprovalCancelled   = "CANCELLED"
)

// Merge candidate state (§3.4).
const (
	CandidateWaiting    = "WAITING"
	CandidateValidating = "VALIDATING"
	CandidateReady      = "READY"
	CandidatePublishing = "PUBLISHING"
	CandidateMerged     = "MERGED"
	CandidateStaleBase  = "STALE_BASE"
	CandidateConflict   = "CONFLICT"
	CandidateFailed     = "FAILED"
	CandidateCancelled  = "CANCELLED"
)

// Native GitHub queue sub-state.
const (
	NativeNone       = "NONE"
	NativeRequested  = "REQUESTED"
	NativeQueued     = "QUEUED"
	NativeValidating = "VALIDATING"
	NativeMerged     = "MERGED"
	NativeRemoved    = "REMOVED"
	NativeUnknown    = "UNKNOWN"
)

// Dependency milestones and phases (§4.1).
const (
	MilestoneImplemented   = "IMPLEMENTED"
	MilestoneVerified      = "VERIFIED"
	MilestoneResultApprove = "RESULT_APPROVED"
	MilestoneIntegrated    = "INTEGRATED"

	PhaseExecution   = "EXECUTION"
	PhaseIntegration = "INTEGRATION"
)

// ValidMilestone reports whether s is a contract milestone.
func ValidMilestone(s string) bool {
	switch s {
	case MilestoneImplemented, MilestoneVerified, MilestoneResultApprove, MilestoneIntegrated:
		return true
	}
	return false
}

// ValidPhase reports whether s is a contract downstream phase.
func ValidPhase(s string) bool { return s == PhaseExecution || s == PhaseIntegration }

// Integration authority per (repository, target_ref).
const (
	AuthorityManual       = "manual"
	AuthorityGitHubQueue  = "github_queue"
	AuthoritySamsaraLocal = "samsara_local"
)

// Worktree resource state (04 §6.3) and ownership.
const (
	WorktreeCreating        = "CREATING"
	WorktreeReady           = "READY"
	WorktreeInUse           = "IN_USE"
	WorktreeQuiescing       = "QUIESCING"
	WorktreeRetained        = "RETAINED"
	WorktreeCleanupEligible = "CLEANUP_ELIGIBLE"
	WorktreeRemoved         = "REMOVED"
	WorktreeQuarantined     = "QUARANTINED"

	OwnershipManaged  = "managed"
	OwnershipExternal = "external"
)

// Backend kinds.
const (
	BackendHerdr      = "herdr"
	BackendTmux       = "tmux"
	BackendStandalone = "standalone"
	BackendFake       = "fake"
)

// Hook policy decision (internal; never mapped to permissionDecision:"allow").
const (
	DecisionPass = "PASS"
	DecisionDeny = "DENY"
)

// Freshness labels used in snapshots.
const (
	FreshLive        = "LIVE"
	FreshPartial     = "PARTIAL"
	FreshStale       = "STALE"
	FreshUnavailable = "UNAVAILABLE"
)
