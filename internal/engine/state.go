package engine

import "example.com/uhpc-wet-joint-traffic-release/internal/domain"

// jointState is the current-generation aggregate view of one locked joint.
type jointState struct {
	SpanID       string
	Design       domain.JointDesign
	Summary      domain.LockSummary
	Generation   string // active material generation ID (empty until first valid mix)
	Prefix       domain.PourPrefix
	Fills        []domain.FillCell
	Surface      map[string]domain.SurfaceEvidence
	Curing       []domain.CuringEvidence
	CuringClosed bool
	Flow         *domain.FlowTest
	Inspections  []domain.InspectionEvidence
	Archived     []domain.InspectionEvidence
	RetestID     string // active retest set ID, if any
	MixCount     int
}

// state is the complete durable aggregate state of the service.
type state struct {
	Spans        map[string]domain.BridgeSpan
	Recipes      map[string]domain.RecipeRule
	Joints       map[string]*jointState
	Materials    map[domain.MaterialCategory]map[string]int64 // available grams
	Batches      map[domain.MaterialCategory]map[string]int64 // initial grams
	Ledger       []domain.MaterialLedgerEntry
	Leases       map[string]domain.EquipmentLease // keyed by resource ID
	Generations  map[string]domain.MaterialGeneration
	DeviceCalls  map[string]domain.DeviceCall
	Retests      map[string]domain.RetestSet
	Remediations map[string]domain.RemediationGeneration
	Reviews      map[string][]domain.Review
	Verdicts     map[string]domain.FinalVerdict
	Operations   map[string]domain.OperationRecord
	Sequence     int64
	Committed    []int64
	RuleVersion  int
	Now          domain.LogicalTime
}

// advanceNow moves the observed logical clock forward to t, if t is later.
func (s *state) advanceNow(t domain.LogicalTime) {
	if t > s.Now {
		s.Now = t
	}
}

func newState() *state {
	return &state{
		Spans:        make(map[string]domain.BridgeSpan),
		Recipes:      make(map[string]domain.RecipeRule),
		Joints:       make(map[string]*jointState),
		Materials:    make(map[domain.MaterialCategory]map[string]int64),
		Batches:      make(map[domain.MaterialCategory]map[string]int64),
		Leases:       make(map[string]domain.EquipmentLease),
		Generations:  make(map[string]domain.MaterialGeneration),
		DeviceCalls:  make(map[string]domain.DeviceCall),
		Retests:      make(map[string]domain.RetestSet),
		Remediations: make(map[string]domain.RemediationGeneration),
		Reviews:      make(map[string][]domain.Review),
		Verdicts:     make(map[string]domain.FinalVerdict),
		Operations:   make(map[string]domain.OperationRecord),
	}
}

// nextSequence allocates the next committed sequence number and records it in
// the committed event list so recovery can verify contiguity.
func (s *state) nextSequence() int64 {
	s.Sequence++
	s.Committed = append(s.Committed, s.Sequence)
	return s.Sequence
}

// materialBalance returns the available grams for a category/batch, and whether
// the batch exists.
func (s *state) materialBalance(cat domain.MaterialCategory, batch string) (int64, bool) {
	m, ok := s.Materials[cat]
	if !ok {
		return 0, false
	}
	g, ok := m[batch]
	return g, ok
}

// setMaterialBalance sets the available grams, creating buckets as needed.
func (s *state) setMaterialBalance(cat domain.MaterialCategory, batch string, grams int64) {
	if s.Materials[cat] == nil {
		s.Materials[cat] = make(map[string]int64)
	}
	if s.Batches[cat] == nil {
		s.Batches[cat] = make(map[string]int64)
	}
	s.Materials[cat][batch] = grams
}
