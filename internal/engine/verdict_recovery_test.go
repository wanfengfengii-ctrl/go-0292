package engine

import (
	"path/filepath"
	"sync"
	"testing"

	"example.com/uhpc-wet-joint-traffic-release/internal/codes"
	"example.com/uhpc-wet-joint-traffic-release/internal/domain"
)

// pourAll fills every cell in the standard two-segment, two-layer joint.
func pourAll(t *testing.T, e *Engine, genID string) {
	t.Helper()
	for i, tt := range []domain.LogicalTime{40, 50, 60, 70} {
		seg, layer := i%2, i/2
		if _, err := e.AppendFill("J1", domain.FillCell{Segment: seg, Layer: layer, MixBatch: "B1", Generation: genID, Time: tt, Compaction: true}); err != nil {
			t.Fatalf("fill %d: %v", i, err)
		}
	}
}

// cureClose closes the curing timeline.
func cureClose(t *testing.T, e *Engine) {
	t.Helper()
	if err := e.RecordCuring("J1", domain.CuringEvidence{Temperature: fpT(20, 0), Humidity: fpT(90, 0), Duration: 60, Time: 80}); err != nil {
		t.Fatalf("curing: %v", err)
	}
}

// addInspections records passing strength and pull-off evidence.
func addInspections(t *testing.T, e *Engine, genID string) {
	t.Helper()
	if err := e.RecordInspection("J1", domain.InspectionEvidence{Kind: KindStrength, Segment: 0, Generation: genID, Reading: fpT(60, 0), Passed: true, Time: 90}); err != nil {
		t.Fatalf("strength: %v", err)
	}
	if err := e.RecordInspection("J1", domain.InspectionEvidence{Kind: KindPullOff, Segment: 0, Generation: genID, Reading: fpT(3, 0), Passed: true, Time: 91}); err != nil {
		t.Fatalf("pull-off: %v", err)
	}
}

func TestReleasePreconditions(t *testing.T) {
	e := NewInMemory()
	genID := readyToPour(t, e, 20, 1000)
	pourAll(t, e, genID)

	release := func() domain.FinalVerdict {
		v, err := e.Verdict("J1", domain.FinalVerdict{Type: domain.VerdictRelease})
		if err != nil {
			t.Fatalf("Verdict: %v", err)
		}
		return v
	}
	assertRejected := func(label string) {
		t.Helper()
		_, err := e.Verdict("J1", domain.FinalVerdict{Type: domain.VerdictRelease})
		if codes.CodeOf(err) != codes.CodePreconditionsNotMet {
			t.Fatalf("%s: got %v, want PRECONDITIONS_NOT_MET", label, err)
		}
	}

	// Missing curing closure.
	assertRejected("missing curing")
	cureClose(t, e)
	// Missing strength.
	assertRejected("missing strength")
	if err := e.RecordInspection("J1", domain.InspectionEvidence{Kind: KindStrength, Segment: 0, Generation: genID, Reading: fpT(60, 0), Passed: true, Time: 90}); err != nil {
		t.Fatalf("strength: %v", err)
	}
	// Missing pull-off.
	assertRejected("missing pull-off")
	if err := e.RecordInspection("J1", domain.InspectionEvidence{Kind: KindPullOff, Segment: 0, Generation: genID, Reading: fpT(3, 0), Passed: true, Time: 91}); err != nil {
		t.Fatalf("pull-off: %v", err)
	}
	// Missing second reviewer.
	if err := e.SubmitReview("J1", domain.Review{Reviewer: "eng-A", Qualified: true, Conclusion: "RELEASE"}); err != nil {
		t.Fatalf("review1: %v", err)
	}
	assertRejected("missing second reviewer")
	// Two qualified reviewers -> release with a credential.
	if err := e.SubmitReview("J1", domain.Review{Reviewer: "eng-B", Qualified: true, Conclusion: "RELEASE"}); err != nil {
		t.Fatalf("review2: %v", err)
	}
	v := release()
	if v.Credential == "" {
		t.Fatalf("release verdict has empty credential")
	}
}

func TestConcurrentVerdictSingleWinner(t *testing.T) {
	e := NewInMemory()
	genID := readyToPour(t, e, 20, 1000)
	pourAll(t, e, genID)
	cureClose(t, e)
	addInspections(t, e, genID)
	for _, r := range []string{"eng-A", "eng-B"} {
		if err := e.SubmitReview("J1", domain.Review{Reviewer: r, Qualified: true, Conclusion: "RELEASE"}); err != nil {
			t.Fatalf("review %s: %v", r, err)
		}
	}

	types := []domain.VerdictType{domain.VerdictRelease, domain.VerdictQuarantine, domain.VerdictCancel}
	start := make(chan struct{})
	results := make([]domain.FinalVerdict, 3)
	errs := make([]error, 3)
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			v, err := e.Verdict("J1", domain.FinalVerdict{Type: types[i]})
			results[i] = v
			errs[i] = err
		}(i)
	}
	close(start)
	wg.Wait()

	successes := 0
	credentialCount := 0
	for i := 0; i < 3; i++ {
		if errs[i] == nil {
			successes++
			if results[i].Credential != "" {
				credentialCount++
			}
		} else if codes.CodeOf(errs[i]) != codes.CodeVerdictConflict {
			t.Fatalf("loser %d: got %v, want VERDICT_CONFLICT", i, errs[i])
		}
	}
	if successes != 1 {
		t.Fatalf("got %d successful verdicts, want exactly 1", successes)
	}
	if credentialCount > 1 {
		t.Fatalf("got %d credentials, want at most 1", credentialCount)
	}
}

func TestRecoveryRestoresState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snapshot.json")

	e1 := New(path)
	if err := e1.Recover(); err != nil {
		t.Fatalf("Recover (first boot): %v", err)
	}
	genID := readyToPour(t, e1, 20, 1000)
	pourAll(t, e1, genID)
	cureClose(t, e1)
	addInspections(t, e1, genID)
	if err := e1.SubmitReview("J1", domain.Review{Reviewer: "eng-A", Qualified: true, Conclusion: "RELEASE"}); err != nil {
		t.Fatalf("review1: %v", err)
	}
	if err := e1.SubmitReview("J1", domain.Review{Reviewer: "eng-B", Qualified: true, Conclusion: "RELEASE"}); err != nil {
		t.Fatalf("review2: %v", err)
	}
	verdict, err := e1.Verdict("J1", domain.FinalVerdict{Type: domain.VerdictRelease})
	if err != nil {
		t.Fatalf("Verdict: %v", err)
	}
	// A pending device retry record should also survive.
	if err := e1.RecordDeviceFailure(domain.DeviceCall{Key: "dev-1", Instrument: "SCALE", Attempt: 1, Failure: "REFUSED", Time: 1}); err != nil {
		t.Fatalf("device failure: %v", err)
	}

	// Restart: a fresh engine recovers the durable snapshot.
	e2 := New(path)
	if err := e2.Recover(); err != nil {
		t.Fatalf("Recover (restart): %v", err)
	}

	view, err := e2.GetJoint("J1")
	if err != nil {
		t.Fatalf("GetJoint after recovery: %v", err)
	}
	if !view.Prefix.Done {
		t.Fatalf("prefix not recovered: %+v", view.Prefix)
	}
	if view.Verdict == nil || view.Verdict.Credential != verdict.Credential {
		t.Fatalf("verdict not recovered: %+v", view.Verdict)
	}
	bal, _ := e2.Balance(domain.MaterialPowder, "B1")
	if bal != 0 {
		t.Fatalf("material balance not recovered: %d", bal)
	}
	call, ok, _ := e2.GetDeviceCall("dev-1")
	if !ok || call.Status != domain.CallPending {
		t.Fatalf("device call not recovered: %+v", call)
	}
	// Idempotent operation survives restart: replaying the same operation with
	// the same content returns the cached result without a second deduction.
	req := domain.MaterialRequest{
		Batch: "B1",
		Grams: map[domain.MaterialCategory]int64{
			domain.MaterialPowder:     100000,
			domain.MaterialWater:      20000,
			domain.MaterialAdmixture:  1000,
			domain.MaterialSteelFiber: 5000,
		},
		Leases: []domain.LeaseRequest{{Category: PourEquipment, ResourceID: "POUR-1", Holder: "crew-A", Purpose: "place", Deadline: 1000}},
	}
	if _, err := e2.Prepare(domain.OperationRecord{OperationID: "prep", Digest: digestOf(req)}, req); err != nil {
		t.Fatalf("recovered operation replay: %v", err)
	}
	bal, _ = e2.Balance(domain.MaterialPowder, "B1")
	if bal != 0 {
		t.Fatalf("material balance changed after idempotent replay: %d", bal)
	}
}
