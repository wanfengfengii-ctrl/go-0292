package engine

import (
	"testing"

	"example.com/uhpc-wet-joint-traffic-release/internal/domain"
	"example.com/uhpc-wet-joint-traffic-release/internal/geometry"
)

// fourSegmentDesign builds a 4-segment, 1-layer joint where segments 0/1 share
// batch B1 (generation 1) and segments 2/3 share batch B2 (generation 2).
// Segments 0 and 3 share surface zone ZA, segments 1 and 2 are zone-isolated.
func fourSegmentDesign() domain.JointDesign {
	return domain.JointDesign{
		JointNumber: "J4", SpanID: "S1", Recipe: "UHPC-1", LockVersion: 1,
		Geometry: geometry.Design{
			Range: geometry.Range{Start: 0, End: 1999},
			Segments: []geometry.Segment{
				{Index: 0, Start: 0, End: 499},
				{Index: 1, Start: 500, End: 999},
				{Index: 2, Start: 1000, End: 1499},
				{Index: 3, Start: 1500, End: 1999},
			},
			Layers:    1,
			Direction: geometry.DirectionAscending,
		},
		SurfaceZones: []domain.SurfaceZone{{ID: "ZA", Required: true}, {ID: "ZB", Required: true}},
		MixPlans: []domain.MixPlan{
			{Batch: "B1", Sequence: 0, Powder: 100000, Water: 20000, Admixture: 1000, Fiber: 5000},
			{Batch: "B2", Sequence: 1, Powder: 100000, Water: 20000, Admixture: 1000, Fiber: 5000},
		},
		Curing: domain.CuringSchedule{DurationMinutes: 60, MinTemperature: fpT(20, 0), MinHumidity: fpT(90, 0)},
		Adjacency: [][]int{
			{1}, // segment 0 adjacent to 1
			{0}, // segment 1 adjacent to 0
			{},  // segment 2 isolated
			{},  // segment 3 isolated
		},
		SegmentZones: map[int][]string{
			0: {"ZA"},
			1: {"ZB"},
			2: {"ZC"},
			3: {"ZA"},
		},
	}
}

// setupFourSegment registers, locks, stocks and produces two generations: gen1
// covers segments 0 and 1, gen2 covers segments 2 and 3.
func setupFourSegment(t *testing.T, e *Engine) (gen1, gen2 domain.MaterialGeneration) {
	t.Helper()
	registerSpanRecipe(t, e)
	if _, err := e.Lock("J4", fourSegmentDesign()); err != nil {
		t.Fatalf("Lock: %v", err)
	}
	stock(t, e, "B1", 100000, 20000, 1000, 5000)
	stock(t, e, "B2", 100000, 20000, 1000, 5000)
	for _, z := range []string{"ZA", "ZB"} {
		if err := e.RecordSurfaceEvidence("J4", domain.SurfaceEvidence{ZoneID: z, Clean: true, PreWet: true, Time: 5}); err != nil {
			t.Fatalf("surface %s: %v", z, err)
		}
	}
	preparePour(t, e, "prep", "B1", 1000)
	if err := e.RecordFlow("J4", domain.FlowTest{Value: fpT(200, 1), Passed: true, Time: 10}); err != nil {
		t.Fatalf("flow: %v", err)
	}

	gen1, err := e.RecordMix("J4", domain.MixRun{JointNumber: "J4", Batch: "B1", Sequence: 0, Powder: 100000, Water: 20000, Admixture: 1000, Fiber: 5000, Time: 20})
	if err != nil {
		t.Fatalf("mix1: %v", err)
	}
	for _, seg := range []int{0, 1} {
		if _, err := e.AppendFill("J4", domain.FillCell{Segment: seg, Layer: 0, MixBatch: "B1", Generation: gen1.ID, Time: 25, Compaction: true}); err != nil {
			t.Fatalf("fill seg %d: %v", seg, err)
		}
	}
	gen2, err = e.RecordMix("J4", domain.MixRun{JointNumber: "J4", Batch: "B2", Sequence: 1, Powder: 100000, Water: 20000, Admixture: 1000, Fiber: 5000, Time: 30})
	if err != nil {
		t.Fatalf("mix2: %v", err)
	}
	for _, seg := range []int{2, 3} {
		if _, err := e.AppendFill("J4", domain.FillCell{Segment: seg, Layer: 0, MixBatch: "B2", Generation: gen2.ID, Time: 35, Compaction: true}); err != nil {
			t.Fatalf("fill seg %d: %v", seg, err)
		}
	}
	return gen1, gen2
}

func TestRetestClosureUniqueAndOrdered(t *testing.T) {
	e := NewInMemory()
	gen1, _ := setupFourSegment(t, e)

	a := domain.Anomaly{JointNumber: "J4", Kind: KindStrength, Segment: 0, Generation: gen1.ID}
	set, err := e.Retest(a)
	if err != nil {
		t.Fatalf("Retest: %v", err)
	}
	// Adjacency {0,1}; same-generation (gen1) {0,1}; shared zone ZA {0,3}.
	want := []int{0, 1, 3}
	if len(set.Segments) != len(want) {
		t.Fatalf("closure = %v, want %v", set.Segments, want)
	}
	for i := range want {
		if set.Segments[i] != want[i] {
			t.Fatalf("closure = %v, want %v (must be deterministically ordered)", set.Segments, want)
		}
	}

	// The same anomaly fact yields exactly one set.
	again, err := e.Retest(a)
	if err != nil {
		t.Fatalf("Retest again: %v", err)
	}
	if again.ID != set.ID {
		t.Fatalf("retest ID changed for same anomaly: %q vs %q", again.ID, set.ID)
	}
}

func TestStaleGenerationArchived(t *testing.T) {
	e := NewInMemory()
	gen1, _ := setupFourSegment(t, e)

	a := domain.Anomaly{JointNumber: "J4", Kind: KindStrength, Segment: 0, Generation: gen1.ID}
	set, err := e.Retest(a)
	if err != nil {
		t.Fatalf("Retest: %v", err)
	}
	rem, err := e.ActivateGeneration(set.ID)
	if err != nil {
		t.Fatalf("ActivateGeneration: %v", err)
	}

	// Late strength receipt from the old generation is archived, not applied.
	if err := e.RecordInspection("J4", domain.InspectionEvidence{Kind: KindStrength, Segment: 0, Generation: gen1.ID, Reading: fpT(60, 0), Passed: true, Time: 40}); err != nil {
		t.Fatalf("stale inspection: %v", err)
	}
	rs, _, _, err := e.GetRetest(set.ID)
	if err != nil {
		t.Fatalf("GetRetest: %v", err)
	}
	if rs.Done {
		t.Fatalf("retest marked done after stale evidence")
	}

	// Current-generation passing evidence completes the retest.
	for _, seg := range set.Segments {
		if err := e.RecordInspection("J4", domain.InspectionEvidence{Kind: KindStrength, Segment: seg, Generation: rem.ID, Reading: fpT(60, 0), Passed: true, Time: 50}); err != nil {
			t.Fatalf("current inspection seg %d: %v", seg, err)
		}
	}
	rs, _, _, err = e.GetRetest(set.ID)
	if err != nil {
		t.Fatalf("GetRetest after completion: %v", err)
	}
	if !rs.Done {
		t.Fatalf("retest not marked done after current-generation evidence")
	}
}
