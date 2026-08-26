package engine

import (
	"testing"

	"example.com/uhpc-wet-joint-traffic-release/internal/codes"
	"example.com/uhpc-wet-joint-traffic-release/internal/domain"
)

// readyToPour locks a joint, confirms the surface, prepares materials and a
// pouring lease, records a valid mix and a passing flow test, returning the
// active generation ID.
func readyToPour(t *testing.T, e *Engine, mixTime, leaseDeadline domain.LogicalTime) string {
	t.Helper()
	lockStandard(t, e)
	stock(t, e, "B1", 100000, 20000, 1000, 5000)
	confirmSurface(t, e, "J1", 5)
	preparePour(t, e, "prep", "B1", leaseDeadline)

	run := domain.MixRun{
		JointNumber: "J1", Batch: "B1", Sequence: 0,
		Powder: 100000, Water: 20000, Admixture: 1000, Fiber: 5000,
		Time: mixTime,
	}
	gen, err := e.RecordMix("J1", run)
	if err != nil {
		t.Fatalf("RecordMix: %v", err)
	}
	if err := e.RecordFlow("J1", domain.FlowTest{Value: fpT(200, 1), Passed: true, Time: mixTime + 5}); err != nil {
		t.Fatalf("RecordFlow: %v", err)
	}
	return gen.ID
}

func TestSequentialFillPrefix(t *testing.T) {
	e := NewInMemory()
	genID := readyToPour(t, e, 20, 1000)

	steps := []struct {
		seg, layer int
		wantSeg    int
		wantLayer  int
		wantDone   bool
	}{
		{0, 0, 1, 0, false},
		{1, 0, 0, 1, false},
		{0, 1, 1, 1, false},
		{1, 1, 1, 1, true},
	}
	time := domain.LogicalTime(40)
	for i, s := range steps {
		prefix, err := e.AppendFill("J1", domain.FillCell{
			Segment: s.seg, Layer: s.layer, MixBatch: "B1", Generation: genID,
			Time: time, Compaction: true,
		})
		if err != nil {
			t.Fatalf("step %d AppendFill: %v", i, err)
		}
		if prefix.Segment != s.wantSeg || prefix.Layer != s.wantLayer || prefix.Done != s.wantDone {
			t.Fatalf("step %d prefix = %+v, want seg=%d layer=%d done=%v", i, prefix, s.wantSeg, s.wantLayer, s.wantDone)
		}
		time += 10
	}
}

func TestPourOrderViolations(t *testing.T) {
	// Jump ahead (skip segment 0).
	e := NewInMemory()
	genID := readyToPour(t, e, 20, 1000)
	_, err := e.AppendFill("J1", domain.FillCell{Segment: 1, Layer: 0, MixBatch: "B1", Generation: genID, Time: 40, Compaction: true})
	if codes.CodeOf(err) != codes.CodePourOutOfOrder {
		t.Fatalf("jump: got %v, want POUR_OUT_OF_ORDER", err)
	}
	prefix, _ := e.Prefix("J1")
	if prefix.Segment != 0 || prefix.Layer != 0 {
		t.Fatalf("prefix after jump = %+v, want segment 0 layer 0", prefix)
	}

	// Wrong batch for the correct cell.
	e2 := NewInMemory()
	genID2 := readyToPour(t, e2, 20, 1000)
	_, err = e2.AppendFill("J1", domain.FillCell{Segment: 0, Layer: 0, MixBatch: "WRONG", Generation: genID2, Time: 40, Compaction: true})
	if codes.CodeOf(err) != codes.CodePourWrongBatch {
		t.Fatalf("wrong batch: got %v, want POUR_WRONG_BATCH", err)
	}

	// Logical time moving backwards.
	e3 := NewInMemory()
	genID3 := readyToPour(t, e3, 20, 1000)
	if _, err := e3.AppendFill("J1", domain.FillCell{Segment: 0, Layer: 0, MixBatch: "B1", Generation: genID3, Time: 40, Compaction: true}); err != nil {
		t.Fatalf("first fill: %v", err)
	}
	_, err = e3.AppendFill("J1", domain.FillCell{Segment: 1, Layer: 0, MixBatch: "B1", Generation: genID3, Time: 30, Compaction: true})
	if codes.CodeOf(err) != codes.CodePourTimeBackward {
		t.Fatalf("time backward: got %v, want POUR_TIME_BACKWARD", err)
	}

	// Duplicate fill after the prefix is complete.
	e4 := NewInMemory()
	genID4 := readyToPour(t, e4, 20, 1000)
	times := []domain.LogicalTime{40, 50, 60, 70}
	for i, tt := range times {
		seg, layer := i%2, i/2 // layer-major: (0,0),(1,0),(0,1),(1,1)
		if _, err := e4.AppendFill("J1", domain.FillCell{Segment: seg, Layer: layer, MixBatch: "B1", Generation: genID4, Time: tt, Compaction: true}); err != nil {
			t.Fatalf("fill %d: %v", i, err)
		}
	}
	_, err = e4.AppendFill("J1", domain.FillCell{Segment: 0, Layer: 0, MixBatch: "B1", Generation: genID4, Time: 80, Compaction: true})
	if codes.CodeOf(err) != codes.CodePourDuplicate {
		t.Fatalf("duplicate: got %v, want POUR_DUPLICATE", err)
	}
}

func TestMaterialDeadlineExpiry(t *testing.T) {
	e := NewInMemory()
	// mix at time 20 -> deadline 120.
	genID := readyToPour(t, e, 20, 1000)

	// Just before the deadline: succeeds.
	if _, err := e.AppendFill("J1", domain.FillCell{Segment: 0, Layer: 0, MixBatch: "B1", Generation: genID, Time: 119, Compaction: true}); err != nil {
		t.Fatalf("fill before deadline: %v", err)
	}
	// At the deadline boundary: rejected.
	_, err := e.AppendFill("J1", domain.FillCell{Segment: 1, Layer: 0, MixBatch: "B1", Generation: genID, Time: 120, Compaction: true})
	if codes.CodeOf(err) != codes.CodeMaterialExpired {
		t.Fatalf("fill at deadline: got %v, want MATERIAL_EXPIRED", err)
	}
}

func TestLeaseExpiryBlocksPour(t *testing.T) {
	e := NewInMemory()
	// lease deadline 100, generation deadline 110 (mix time 10 + 100).
	genID := readyToPour(t, e, 10, 100)

	if _, err := e.AppendFill("J1", domain.FillCell{Segment: 0, Layer: 0, MixBatch: "B1", Generation: genID, Time: 90, Compaction: true}); err != nil {
		t.Fatalf("fill within lease: %v", err)
	}
	_, err := e.AppendFill("J1", domain.FillCell{Segment: 1, Layer: 0, MixBatch: "B1", Generation: genID, Time: 100, Compaction: true})
	if codes.CodeOf(err) != codes.CodeLeaseExpired {
		t.Fatalf("fill after lease expiry: got %v, want LEASE_EXPIRED", err)
	}
}
