package domain

import (
	"errors"
	"fmt"
	"net/http"
)

// Error codes. They are stable identifiers shared by HTTP, IPC, CLI and Hook
// reasons (contract §7.3). Messages are human readable and may change.
const (
	CodeInvalidRequest         = "INVALID_REQUEST"
	CodeSessionExpired         = "SESSION_EXPIRED"
	CodeActionForbidden        = "ACTION_FORBIDDEN"
	CodeNotFound               = "RESOURCE_NOT_FOUND"
	CodeDependencyUnmet        = "DEPENDENCY_UNMET"
	CodeDeliveryUnknown        = "DELIVERY_UNKNOWN"
	CodeIdempotencyKeyReused   = "IDEMPOTENCY_KEY_REUSED"
	CodeConflict               = "CONFLICT"
	CodeVersionMismatch        = "VERSION_MISMATCH"
	CodeApprovalSubjectChanged = "APPROVAL_SUBJECT_CHANGED"
	CodeApprovalRequired       = "APPROVAL_REQUIRED"
	CodeUnsupportedCapability  = "UNSUPPORTED_CAPABILITY"
	CodeRateLimited            = "RATE_LIMITED"
	CodeRuntimeNotReady        = "RUNTIME_NOT_READY"
	CodeStorageUnavailable     = "STORAGE_UNAVAILABLE"
	CodeRunNotFinalized        = "RUN_NOT_FINALIZED"
	CodeProtocolMismatch       = "PROTOCOL_MISMATCH"
	CodeProjectNotEnabled      = "PROJECT_NOT_ENABLED"
	CodeProjectNotTrusted      = "PROJECT_NOT_TRUSTED"
	CodeCycleDetected          = "CYCLE_DETECTED"
	CodeOwnershipConflict      = "OWNERSHIP_CONFLICT"
	CodeTargetCheckedOut       = "TARGET_CHECKED_OUT"
	CodeStaleFence             = "STALE_FENCE"
	CodeInternal               = "INTERNAL"
)

// Hook / policy reason codes (subset of the error namespace used in DENY).
const (
	ReasonUnmanaged             = "UNMANAGED_SESSION"
	ReasonSessionNotRegistered  = "SESSION_NOT_REGISTERED"
	ReasonNoAssignment          = "NO_ASSIGNMENT"
	ReasonAttemptNotRunning     = "ATTEMPT_NOT_RUNNING"
	ReasonLeaseRevoked          = "LEASE_REVOKED"
	ReasonLeaseExpired          = "LEASE_EXPIRED"
	ReasonLeaseMissing          = "LEASE_MISSING"
	ReasonEpochChanged          = "RUNTIME_EPOCH_CHANGED"
	ReasonClockAnomaly          = "CLOCK_ANOMALY"
	ReasonStaleFence            = "STALE_FENCE"
	ReasonDisableDraining       = "DISABLE_DRAINING"
	ReasonProjectNotEnabled     = "PROJECT_NOT_ENABLED"
	ReasonRunNotActive          = "RUN_NOT_ACTIVE"
	ReasonGenerationChanged     = "RUN_GENERATION_CHANGED"
	ReasonDependencyUnmet       = "DEPENDENCY_UNMET"
	ReasonCwdOutOfScope         = "CWD_OUT_OF_SCOPE"
	ReasonPathOutOfScope        = "PATH_OUT_OF_SCOPE"
	ReasonBackgroundUnsupported = "BACKGROUND_MUTATION_UNSUPPORTED"
	ReasonRuntimeUnavailable    = "RUNTIME_UNAVAILABLE"
	ReasonInvalidHookInput      = "INVALID_HOOK_INPUT"
	ReasonDeadlineExceeded      = "DEADLINE_EXCEEDED"
	ReasonQuarantined           = "QUARANTINED"
)

// Error is the structured error used across layers.
type Error struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	Retryable bool           `json:"retryable"`
	Details   map[string]any `json:"details,omitempty"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// Errf builds a domain error.
func Errf(code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// WithDetails attaches structured details.
func (e *Error) WithDetails(kv ...any) *Error {
	if e.Details == nil {
		e.Details = map[string]any{}
	}
	for i := 0; i+1 < len(kv); i += 2 {
		e.Details[fmt.Sprint(kv[i])] = kv[i+1]
	}
	return e
}

// AsError extracts a *Error, wrapping unknown errors as INTERNAL.
func AsError(err error) *Error {
	if err == nil {
		return nil
	}
	var de *Error
	if errors.As(err, &de) {
		return de
	}
	return &Error{Code: CodeInternal, Message: err.Error()}
}

// IsCode reports whether err carries the given code.
func IsCode(err error, code string) bool {
	var de *Error
	return errors.As(err, &de) && de.Code == code
}

// HTTPStatus maps an error code to the HTTP status of contract §7.3.
func HTTPStatus(code string) int {
	switch code {
	case CodeInvalidRequest, CodeCycleDetected, CodeProtocolMismatch:
		return http.StatusBadRequest
	case CodeSessionExpired:
		return http.StatusUnauthorized
	case CodeActionForbidden, CodeProjectNotEnabled, CodeProjectNotTrusted, CodeTargetCheckedOut:
		return http.StatusForbidden
	case CodeNotFound:
		return http.StatusNotFound
	case CodeDependencyUnmet, CodeDeliveryUnknown, CodeIdempotencyKeyReused, CodeConflict,
		CodeApprovalRequired, CodeRunNotFinalized, CodeOwnershipConflict, CodeStaleFence:
		return http.StatusConflict
	case CodeVersionMismatch, CodeApprovalSubjectChanged:
		return http.StatusPreconditionFailed
	case CodeUnsupportedCapability:
		return http.StatusUnprocessableEntity
	case CodeRateLimited:
		return http.StatusTooManyRequests
	case CodeRuntimeNotReady, CodeStorageUnavailable:
		return http.StatusServiceUnavailable
	}
	return http.StatusInternalServerError
}

// CLI exit codes (01 §19): distinct for blocked, input, connection, safety.
const (
	ExitOK         = 0
	ExitError      = 1
	ExitInput      = 2
	ExitConnection = 3
	ExitBlocked    = 4
	ExitRefused    = 5
)

// ExitCode maps an error code to a CLI exit code.
func ExitCode(code string) int {
	switch code {
	case "":
		return ExitOK
	case CodeInvalidRequest, CodeCycleDetected, CodeProtocolMismatch, CodeNotFound:
		return ExitInput
	case CodeRuntimeNotReady, CodeStorageUnavailable, ReasonRuntimeUnavailable:
		return ExitConnection
	case CodeDependencyUnmet, CodeApprovalRequired, CodeRunNotFinalized:
		return ExitBlocked
	case CodeActionForbidden, CodeProjectNotEnabled, CodeProjectNotTrusted, CodeConflict,
		CodeIdempotencyKeyReused, CodeVersionMismatch, CodeApprovalSubjectChanged,
		CodeOwnershipConflict, CodeTargetCheckedOut, CodeStaleFence, CodeUnsupportedCapability,
		CodeDeliveryUnknown:
		return ExitRefused
	}
	return ExitError
}
