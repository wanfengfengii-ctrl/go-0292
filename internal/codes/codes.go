// Package codes defines the stable, machine-readable error codes shared across
// the UHPC wet-joint domain. Codes are fixed strings (not HTTP statuses) so
// they remain a stable contract between the HTTP API and its clients.
package codes

import "errors"

// Code is a stable, machine-readable domain error code.
type Code string

const (
	// Geometry validation codes.
	CodeGeometryGap        Code = "GEOMETRY_GAP"
	CodeGeometryOverlap    Code = "GEOMETRY_OVERLAP"
	CodeGeometryOverflow   Code = "GEOMETRY_OVERFLOW"
	CodeGeometryDegenerate Code = "GEOMETRY_DEGENERATE"
	CodeGeometryNegative   Code = "GEOMETRY_NEGATIVE"
	CodeRebarKeyConflict   Code = "REBAR_KEY_CONFLICT"

	// Design lock codes.
	CodeSpanRecipeMismatch Code = "SPAN_RECIPE_MISMATCH"
	CodeStaleRuleDigest    Code = "STALE_RULE_DIGEST"

	// Operational codes.
	CodeIdempotencyConflict Code = "IDEMPOTENCY_CONFLICT"
	CodeMaterialExpired     Code = "MATERIAL_EXPIRED"
	CodeRecoveryIntegrity   Code = "RECOVERY_INTEGRITY_FAILED"

	// Resource and lookup codes.
	CodeJointNotFound      Code = "JOINT_NOT_FOUND"
	CodeRetestNotFound     Code = "RETEST_NOT_FOUND"
	CodeNotLocked          Code = "NOT_LOCKED"
	CodeAlreadyLocked      Code = "ALREADY_LOCKED"
	CodeOperationNotFound  Code = "OPERATION_NOT_FOUND"
	CodeDeviceCallNotFound Code = "DEVICE_CALL_NOT_FOUND"

	// Surface handover codes.
	CodeSurfaceNotConfirmed Code = "SURFACE_NOT_CONFIRMED"
	CodeZoneAlreadyRecorded Code = "ZONE_ALREADY_RECORDED"

	// Material and lease codes.
	CodeMaterialInsufficient Code = "MATERIAL_INSUFFICIENT"
	CodeMaterialUnavailable  Code = "MATERIAL_UNAVAILABLE"
	CodeLeaseConflict        Code = "LEASE_CONFLICT"
	CodeLeaseExpired         Code = "LEASE_EXPIRED"
	CodeLeaseNotHolder       Code = "LEASE_NOT_HOLDER"

	// Mixing, flow and pouring codes.
	CodeMixInvalid       Code = "MIX_INVALID"
	CodeFlowFailed       Code = "FLOW_FAILED"
	CodeFlowMissing      Code = "FLOW_MISSING"
	CodePourOutOfOrder   Code = "POUR_OUT_OF_ORDER"
	CodePourDuplicate    Code = "POUR_DUPLICATE"
	CodePourTimeBackward Code = "POUR_TIME_BACKWARD"
	CodePourWrongBatch   Code = "POUR_WRONG_BATCH"
	CodeCuringNotClosed  Code = "CURING_NOT_CLOSED"
	CodeCuringOutOfOrder Code = "CURING_OUT_OF_ORDER"

	// Inspection, retest and remediation codes.
	CodeInspectionFailed  Code = "INSPECTION_FAILED"
	CodeRetestNotComplete Code = "RETEST_NOT_COMPLETE"
	CodeStaleGeneration   Code = "STALE_GENERATION"
	CodeDuplicateRetest   Code = "DUPLICATE_RETEST"

	// Review and verdict codes.
	CodeReviewConflict      Code = "REVIEW_CONFLICT"
	CodeNotQualified        Code = "NOT_QUALIFIED"
	CodeDuplicateReviewer   Code = "DUPLICATE_REVIEWER"
	CodeVerdictConflict     Code = "VERDICT_CONFLICT"
	CodePreconditionsNotMet Code = "PRECONDITIONS_NOT_MET"

	// Device call codes.
	CodeDeviceRetryExceeded Code = "DEVICE_RETRY_EXCEEDED"
	CodeDeviceOutOfOrder    Code = "DEVICE_OUT_OF_ORDER"
	CodeDeviceDuplicate     Code = "DEVICE_DUPLICATE"
	CodeDeviceFormat        Code = "DEVICE_FORMAT_ERROR"
)

// Error is a domain error carrying a stable code and a human-readable message.
type Error struct {
	Code    Code
	Message string
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	return string(e.Code) + ": " + e.Message
}

// New builds a domain error with the given stable code.
func New(code Code, msg string) *Error { return &Error{Code: code, Message: msg} }

// CodeOf extracts the stable code carried by err, or "" if none is present.
func CodeOf(err error) Code {
	if err == nil {
		return ""
	}
	var ce *Error
	if errors.As(err, &ce) {
		return ce.Code
	}
	return ""
}
