// Package domain holds the stable domain types and component interfaces of the
// UHPC wet-joint traffic-release service. Types map one-to-one onto the
// documented data model; interfaces describe the five business components plus
// the persistence/recovery boundary.
package domain

import (
	"example.com/uhpc-wet-joint-traffic-release/internal/fixedpoint"
	"example.com/uhpc-wet-joint-traffic-release/internal/geometry"
)

// LogicalTime is a monotonically increasing logical clock value. The domain
// never depends on wall-clock time, network, or random ordering.
type LogicalTime int64

// MaterialCategory enumerates the four conserved material pools.
type MaterialCategory string

const (
	MaterialPowder     MaterialCategory = "POWDER"      // 粉料
	MaterialWater      MaterialCategory = "WATER"       // 水
	MaterialAdmixture  MaterialCategory = "ADMIXTURE"   // 外加剂
	MaterialSteelFiber MaterialCategory = "STEEL_FIBER" // 钢纤维
)

// BridgeSpan identifies a bridge span, its engineering coordinate scale, the
// allowed recipe set, and the current rule digest.
type BridgeSpan struct {
	ID              string   `json:"id"`
	CoordinateScale int64    `json:"coordinate_scale"`
	AllowedRecipes  []string `json:"allowed_recipes"`
	RuleDigest      string   `json:"rule_digest"`
}

// SurfaceZone is a base-surface roughness measurement zone.
type SurfaceZone struct {
	ID       string `json:"id"`
	Required bool   `json:"required"`
}

// MixPlan is a single planned batch (盘次) in the locked pouring sequence.
// Component masses are integer grams.
type MixPlan struct {
	Batch     string `json:"batch"`
	Sequence  int    `json:"sequence"`
	Powder    int64  `json:"powder"`
	Water     int64  `json:"water"`
	Admixture int64  `json:"admixture"`
	Fiber     int64  `json:"fiber"`
}

// CuringSchedule is the locked curing timeline for a joint.
type CuringSchedule struct {
	DurationMinutes int64            `json:"duration_minutes"`
	MinTemperature  fixedpoint.Value `json:"min_temperature"`
	MinHumidity     fixedpoint.Value `json:"min_humidity"`
}

// RecipeRule is a locked UHPC recipe revision: allowable deviation, flow
// window, work window, and the strength/bond/shrinkage thresholds that gate
// traffic release.
type RecipeRule struct {
	Name            string           `json:"name"`
	AllowDeviation  fixedpoint.Value `json:"allow_deviation"` // 允许偏差，比例
	FlowMin         fixedpoint.Value `json:"flow_min"`
	FlowMax         fixedpoint.Value `json:"flow_max"`
	WorkWindow      int64            `json:"work_window"` // 施工期限（逻辑时间单位）
	MinStrength     fixedpoint.Value `json:"min_strength"`
	MinBondStrength fixedpoint.Value `json:"min_bond_strength"`
	MaxShrinkage    fixedpoint.Value `json:"max_shrinkage"`
}

// JointDesign is the full locked design description of a wet joint.
type JointDesign struct {
	JointNumber  string           `json:"joint_number"`
	SpanID       string           `json:"span_id"`
	Geometry     geometry.Design  `json:"geometry"`
	SurfaceZones []SurfaceZone    `json:"surface_zones"`
	Recipe       string           `json:"recipe"`
	MixPlans     []MixPlan        `json:"mix_plans"`
	Curing       CuringSchedule   `json:"curing"`
	Adjacency    [][]int          `json:"adjacency"`     // 邻接表
	SegmentZones map[int][]string `json:"segment_zones"` // segment -> zones
	LockVersion  int              `json:"lock_version"`
}

// LockSummary is the immutable digest produced by a successful design lock.
type LockSummary struct {
	JointNumber string `json:"joint_number"`
	Version     int    `json:"version"`
	RuleDigest  string `json:"rule_digest"`
	Digest      string `json:"digest"`
}

// MaterialLedgerEntry is one immutable movement in the integer-gram ledger.
type MaterialLedgerEntry struct {
	Category MaterialCategory `json:"category"`
	Batch    string           `json:"batch"`
	Delta    int64            `json:"delta"` // 正扣减/负退回，整数克
	Reason   string           `json:"reason"`
	Time     LogicalTime      `json:"time"`
}

// EquipmentLease is a timed, mutually exclusive lease on a resource.
type EquipmentLease struct {
	Category   string      `json:"category"`
	ResourceID string      `json:"resource_id"`
	Holder     string      `json:"holder"`
	Purpose    string      `json:"purpose"`
	Token      string      `json:"token"`
	Start      LogicalTime `json:"start"`
	Deadline   LogicalTime `json:"deadline"`
	Active     bool        `json:"active"`
}

// CallStatus is the state of an external instrument call.
type CallStatus string

const (
	CallPending CallStatus = "PENDING"
	CallSuccess CallStatus = "SUCCESS"
)

// DeviceCall is a bounded, deterministic external instrument call record.
type DeviceCall struct {
	Key        string           `json:"key"`
	Instrument string           `json:"instrument"`
	Attempt    int              `json:"attempt"`
	Failure    string           `json:"failure"`
	Reading    fixedpoint.Value `json:"reading"`
	Time       LogicalTime      `json:"time"`
	Status     CallStatus       `json:"status"`
}

// FillCell is one append-only pour cell addressed by segment and layer.
type FillCell struct {
	Segment    int         `json:"segment"`
	Layer      int         `json:"layer"`
	MixBatch   string      `json:"mix_batch"`
	Generation string      `json:"generation"`
	Time       LogicalTime `json:"time"`
	Compaction bool        `json:"compaction"`
}

// PourPrefix is the current continuous pour prefix position.
type PourPrefix struct {
	Segment int  `json:"segment"`
	Layer   int  `json:"layer"`
	Done    bool `json:"done"`
}

// SurfaceEvidence records base-surface roughness/cleanliness/pre-wetting proof.
type SurfaceEvidence struct {
	ZoneID    string           `json:"zone_id"`
	Roughness fixedpoint.Value `json:"roughness"`
	Clean     bool             `json:"clean"`
	PreWet    bool             `json:"pre_wet"`
	Time      LogicalTime      `json:"time"`
}

// CuringEvidence is one curing timeline sample.
type CuringEvidence struct {
	Temperature fixedpoint.Value `json:"temperature"`
	Humidity    fixedpoint.Value `json:"humidity"`
	Duration    int64            `json:"duration"`
	Time        LogicalTime      `json:"time"`
}

// InspectionEvidence records a strength/pull-off/shrinkage/crack reading.
type InspectionEvidence struct {
	Kind       string           `json:"kind"` // STRENGTH / PULL_OFF / SHRINKAGE / CRACK / APPEARANCE
	Subject    string           `json:"subject"`
	Segment    int              `json:"segment"`
	Generation string           `json:"generation"`
	Reading    fixedpoint.Value `json:"reading"`
	Passed     bool             `json:"passed"`
	Time       LogicalTime      `json:"time"`
}

// Anomaly identifies a single abnormal inspection fact.
type Anomaly struct {
	JointNumber string             `json:"joint_number"`
	Kind        string             `json:"kind"`
	Segment     int                `json:"segment"`
	Generation  string             `json:"generation"`
	Evidence    InspectionEvidence `json:"evidence"`
}

// RetestSet is the unique ordered retest closure for an anomaly.
type RetestSet struct {
	ID       string  `json:"id"`
	Anomaly  Anomaly `json:"anomaly"`
	Segments []int   `json:"segments"` // 有序影响范围
	Done     bool    `json:"done"`
}

// RemediationGeneration is a new generation covering a retest set.
type RemediationGeneration struct {
	ID       string `json:"id"`
	RetestID string `json:"retest_id"`
	Previous string `json:"previous"`
	Segments []int  `json:"segments"`
	Complete bool   `json:"complete"`
}

// Review is one independent qualified reviewer conclusion.
type Review struct {
	Reviewer   string `json:"reviewer"`
	Qualified  bool   `json:"qualified"`
	Conclusion string `json:"conclusion"`
	Digest     string `json:"digest"`
}

// VerdictType is the final traffic-release outcome.
type VerdictType string

const (
	VerdictRelease    VerdictType = "RELEASE"    // 允许通车
	VerdictQuarantine VerdictType = "QUARANTINE" // 质量隔离
	VerdictCancel     VerdictType = "CANCEL"     // 取消
)

// FinalVerdict is the single-writer final outcome and, on release, the unique
// traffic credential.
type FinalVerdict struct {
	JointNumber    string      `json:"joint_number"`
	Type           VerdictType `json:"type"`
	BarrierVersion int         `json:"barrier_version"`
	Credential     string      `json:"credential"` // 唯一通车凭据
}

// OperationRecord binds an operation ID to a request digest and committed
// response, enabling idempotent retry.
type OperationRecord struct {
	OperationID string `json:"operation_id"`
	Digest      string `json:"digest"`
	Response    string `json:"response"`
	Sequence    int64  `json:"sequence"`
}

// MaterialRequest is the set of grams and leases requested by one operation.
// All gram deductions apply to a single supply Batch.
type MaterialRequest struct {
	Batch  string                     `json:"batch"`
	Grams  map[MaterialCategory]int64 `json:"grams"`
	Leases []LeaseRequest             `json:"leases"`
}

// LeaseRequest is a single timed resource lease request.
type LeaseRequest struct {
	Category   string      `json:"category"`
	ResourceID string      `json:"resource_id"`
	Holder     string      `json:"holder"`
	Purpose    string      `json:"purpose"`
	Deadline   LogicalTime `json:"deadline"`
}

// LeaseSet is the granted set of leases for one atomic preparation.
type LeaseSet struct {
	Leases []EquipmentLease `json:"leases"`
}

// MixRun is a completed batch mixing record with actual integer-gram dosages.
type MixRun struct {
	JointNumber string      `json:"joint_number"`
	Batch       string      `json:"batch"`
	Sequence    int         `json:"sequence"`
	Powder      int64       `json:"powder"`
	Water       int64       `json:"water"`
	Admixture   int64       `json:"admixture"`
	Fiber       int64       `json:"fiber"`
	Time        LogicalTime `json:"time"`
}

// MaterialGeneration is a non-mergeable generation produced by a valid mix.
type MaterialGeneration struct {
	ID          string      `json:"id"`
	JointNumber string      `json:"joint_number"`
	Batch       string      `json:"batch"`
	Deadline    LogicalTime `json:"deadline"`
	Valid       bool        `json:"valid"`
}

// FlowTest is the flowability result that gates pouring authorization.
type FlowTest struct {
	Value  fixedpoint.Value `json:"value"`
	Passed bool             `json:"passed"`
	Time   LogicalTime      `json:"time"`
}

// Snapshot is the recoverable aggregate state, persisted by the store.
type Snapshot struct {
	SchemaVersion int     `json:"schema_version"`
	Sequences     []int64 `json:"sequences"`
	Digest        string  `json:"digest"`
	State         []byte  `json:"state"`
}
