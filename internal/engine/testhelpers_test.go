package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"

	"example.com/uhpc-wet-joint-traffic-release/internal/domain"
	"example.com/uhpc-wet-joint-traffic-release/internal/fixedpoint"
	"example.com/uhpc-wet-joint-traffic-release/internal/geometry"
)

// fp builds a fixed-point value, failing the test on error.
func fp(t *testing.T, raw int64, scale int) fixedpoint.Value {
	t.Helper()
	v, err := fixedpoint.New(raw, scale)
	if err != nil {
		t.Fatalf("fixedpoint.New(%d,%d): %v", raw, scale, err)
	}
	return v
}

// registerSpanRecipe registers the standard recipe then span, leaving the rule
// version at 1 so a design with LockVersion 1 is current.
func registerSpanRecipe(t *testing.T, e *Engine) {
	t.Helper()
	rule := domain.RecipeRule{
		Name:            "UHPC-1",
		AllowDeviation:  fp(t, 2, 2),
		FlowMin:         fp(t, 180, 1),
		FlowMax:         fp(t, 260, 1),
		WorkWindow:      100,
		MinStrength:     fp(t, 50, 0),
		MinBondStrength: fp(t, 2, 0),
		MaxShrinkage:    fp(t, 500, 0),
	}
	if err := e.RegisterRecipe(rule); err != nil {
		t.Fatalf("RegisterRecipe: %v", err)
	}
	span := domain.BridgeSpan{
		ID:              "S1",
		CoordinateScale: 1000,
		AllowedRecipes:  []string{"UHPC-1"},
		RuleDigest:      "v1",
	}
	if err := e.RegisterSpan(span); err != nil {
		t.Fatalf("RegisterSpan: %v", err)
	}
}

// standardDesign returns a two-segment, two-layer joint design with one mix plan.
func standardDesign() domain.JointDesign {
	return domain.JointDesign{
		JointNumber: "J1",
		SpanID:      "S1",
		Recipe:      "UHPC-1",
		LockVersion: 1,
		Geometry: geometry.Design{
			Range:     geometry.Range{Start: 0, End: 999},
			Segments:  []geometry.Segment{{Index: 0, Start: 0, End: 499}, {Index: 1, Start: 500, End: 999}},
			Layers:    2,
			Direction: geometry.DirectionAscending,
			Rebar:     geometry.Rebar{Cover: 50},
		},
		SurfaceZones: []domain.SurfaceZone{{ID: "Z1", Required: true}},
		MixPlans: []domain.MixPlan{
			{Batch: "B1", Sequence: 0, Powder: 100000, Water: 20000, Admixture: 1000, Fiber: 5000},
		},
		Curing: domain.CuringSchedule{
			DurationMinutes: 60,
			MinTemperature:  fpT(20, 0),
			MinHumidity:     fpT(90, 0),
		},
		Adjacency:    [][]int{{1}, {0}},
		SegmentZones: map[int][]string{0: {"Z1"}, 1: {"Z1"}},
	}
}

// fpT builds a fixed-point value, panicking on error (for literals).
func fpT(raw int64, scale int) fixedpoint.Value {
	v, err := fixedpoint.New(raw, scale)
	if err != nil {
		panic(err)
	}
	return v
}

// lockStandard registers the standard span/recipe and locks the standard joint.
func lockStandard(t *testing.T, e *Engine) {
	t.Helper()
	registerSpanRecipe(t, e)
	if _, err := e.Lock("J1", standardDesign()); err != nil {
		t.Fatalf("Lock: %v", err)
	}
}

// stock credits the standard material pool for batch B1.
func stock(t *testing.T, e *Engine, batch string, powder, water, admixture, fiber int64) {
	t.Helper()
	for cat, grams := range map[domain.MaterialCategory]int64{
		domain.MaterialPowder:     powder,
		domain.MaterialWater:      water,
		domain.MaterialAdmixture:  admixture,
		domain.MaterialSteelFiber: fiber,
	} {
		if err := e.Stock(cat, batch, grams); err != nil {
			t.Fatalf("Stock(%s,%s): %v", cat, batch, err)
		}
	}
}

// confirmSurface records confirming evidence for every required zone.
func confirmSurface(t *testing.T, e *Engine, jointID string, time domain.LogicalTime) {
	t.Helper()
	ev := domain.SurfaceEvidence{
		ZoneID:    "Z1",
		Roughness: fpT(6, 0),
		Clean:     true,
		PreWet:    true,
		Time:      time,
	}
	if err := e.RecordSurfaceEvidence(jointID, ev); err != nil {
		t.Fatalf("RecordSurfaceEvidence: %v", err)
	}
}

// preparePour atomically deducts standard doses and acquires a pouring lease.
func preparePour(t *testing.T, e *Engine, opID string, batch string, deadline domain.LogicalTime) domain.LeaseSet {
	t.Helper()
	req := domain.MaterialRequest{
		Batch: batch,
		Grams: map[domain.MaterialCategory]int64{
			domain.MaterialPowder:     100000,
			domain.MaterialWater:      20000,
			domain.MaterialAdmixture:  1000,
			domain.MaterialSteelFiber: 5000,
		},
		Leases: []domain.LeaseRequest{
			{Category: PourEquipment, ResourceID: "POUR-1", Holder: "crew-A", Purpose: "place", Deadline: deadline},
		},
	}
	set, err := e.Prepare(domain.OperationRecord{OperationID: opID, Digest: digestOf(req)}, req)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	return set
}

func digestOf(v any) string {
	raw, _ := json.Marshal(v)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
