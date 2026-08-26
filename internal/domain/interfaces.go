package domain

import "example.com/uhpc-wet-joint-traffic-release/internal/fixedpoint"

// Catalog is the joint-design and UHPC material-rule directory. It registers
// bridge spans and recipe revisions and locks a joint design into an immutable
// summary after validating geometry, span/recipe matching, and rule freshness.
type Catalog interface {
	// RegisterSpan records a bridge span with its coordinate scale, allowed
	// recipes and current rule digest.
	RegisterSpan(span BridgeSpan) error
	// RegisterRecipe records a UHPC recipe revision with its thresholds.
	RegisterRecipe(rule RecipeRule) error
	// Lock validates and freezes a joint design, returning an immutable summary.
	Lock(jointID string, design JointDesign) (LockSummary, error)
}

// JointAggregate maintains the append-only fill cells, locked flow direction,
// continuous pour prefix, surface handover and curing timeline of a joint.
type JointAggregate interface {
	// RecordSurfaceEvidence appends base-surface evidence for one zone.
	RecordSurfaceEvidence(jointID string, ev SurfaceEvidence) error
	// SurfaceConfirmed reports whether every required zone is confirmed.
	SurfaceConfirmed(jointID string) (bool, error)
	// AppendFill appends the next fill cell and advances the continuous prefix.
	AppendFill(jointID string, cell FillCell) (PourPrefix, error)
	// Prefix returns the current continuous pour prefix.
	Prefix(jointID string) (PourPrefix, error)
	// RecordCuring appends a curing sample to the timeline.
	RecordCuring(jointID string, ev CuringEvidence) error
	// CuringClosed reports whether the curing timeline is closed.
	CuringClosed(jointID string) (bool, error)
}

// MaterialLedger maintains integer-gram batches, sampling and loss ledger,
// atomic material issue, timed exclusive equipment leases, idempotent operation
// records, and transactional rollback.
type MaterialLedger interface {
	// Stock credits grams into a category/batch pool.
	Stock(category MaterialCategory, batch string, grams int64) error
	// Prepare atomically deducts the requested grams and acquires the requested
	// leases; any conflict or deviation rolls back the whole transaction.
	Prepare(op OperationRecord, req MaterialRequest) (LeaseSet, error)
	// Balance returns the current available grams for a category and batch.
	Balance(category MaterialCategory, batch string) (int64, error)
	// ReleaseLease releases an active lease held by holder.
	ReleaseLease(holder, resourceID string) error
	// RenewLease extends the deadline of an active lease held by holder.
	RenewLease(holder, resourceID string, deadline LogicalTime) error
}

// EvidenceRecorder records non-mergeable mix generations, work windows, flow,
// device failures/results, and inspection evidence.
type EvidenceRecorder interface {
	// RecordMix records a completed mix run, producing a material generation.
	RecordMix(jointID string, run MixRun) (MaterialGeneration, error)
	// RecordFlow records a flow test result that gates pouring authorization.
	RecordFlow(jointID string, flow FlowTest) error
	// RecordDeviceFailure registers a bounded, deterministic retry record.
	RecordDeviceFailure(call DeviceCall) error
	// RecordDeviceResult consumes a successful device result exactly once.
	RecordDeviceResult(call DeviceCall) error
	// RecordInspection appends strength/pull-off/shrinkage/crack evidence.
	RecordInspection(jointID string, ev InspectionEvidence) error
}

// Arbitrator handles strength/pull-off/shrinkage/crack/appearance decisions,
// computes the unique retest closure, isolates stale generations, and executes
// dual review plus single-writer final verdict.
type Arbitrator interface {
	// Retest computes the unique ordered retest closure for an anomaly.
	Retest(anomaly Anomaly) (RetestSet, error)
	// ActivateGeneration activates a remediation generation over a retest set.
	ActivateGeneration(retestID string) (RemediationGeneration, error)
	// SubmitReview records one independent qualified reviewer conclusion.
	SubmitReview(jointID string, review Review) error
	// Verdict executes the single-writer final verdict barrier.
	Verdict(jointID string, verdict FinalVerdict) (FinalVerdict, error)
}

// Store is the persistence and restart-recovery boundary. It persists snapshots
// and validates the event sequence on startup.
type Store interface {
	// Recover loads the persisted snapshot and validates the event sequence,
	// leaving the store read-only on RECOVERY_INTEGRITY_FAILED.
	Recover() error
	// Healthy reports whether recovery left the store writable.
	Healthy() bool
}

// Value is a convenience alias so callers can construct fixed-point values
// without importing the fixedpoint package.
type Value = fixedpoint.Value
