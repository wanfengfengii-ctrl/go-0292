package engine

import (
	"errors"
	"sync"
	"testing"

	"example.com/uhpc-wet-joint-traffic-release/internal/codes"
	"example.com/uhpc-wet-joint-traffic-release/internal/domain"
	"example.com/uhpc-wet-joint-traffic-release/internal/store"
)

// fakeStore is an in-memory snapshot store that can be forced to fail on Save,
// exercising transactional rollback on commit failure.
type fakeStore struct {
	snap     domain.Snapshot
	hasSnap  bool
	failSave bool
	bad      bool
}

func (f *fakeStore) Recover() (domain.Snapshot, error) {
	if !f.hasSnap {
		return domain.Snapshot{}, store.ErrNoSnapshot
	}
	return f.snap, nil
}
func (f *fakeStore) Healthy() bool  { return !f.bad }
func (f *fakeStore) MarkUnhealthy() { f.bad = true }
func (f *fakeStore) Path() string   { return "memory" }
func (f *fakeStore) Save(eventType string, s domain.Snapshot) error {
	if f.failSave {
		return errors.New("commit failure")
	}
	f.snap = s
	f.hasSnap = true
	return nil
}

func TestConcurrentPrepareSingleWinner(t *testing.T) {
	e := NewInMemory()
	registerSpanRecipe(t, e)
	if _, err := e.Lock("J1", standardDesign()); err != nil {
		t.Fatalf("Lock: %v", err)
	}
	stock(t, e, "B1", 100, 100, 100, 100)

	grams := map[domain.MaterialCategory]int64{
		domain.MaterialPowder:     60,
		domain.MaterialWater:      60,
		domain.MaterialAdmixture:  60,
		domain.MaterialSteelFiber: 60,
	}
	leaseReq := []domain.LeaseRequest{
		{Category: "MIXER", ResourceID: "MIXER-1", Holder: "crew-A", Purpose: "mix", Deadline: 1000},
	}
	mkReq := func() domain.MaterialRequest {
		return domain.MaterialRequest{Batch: "B1", Grams: grams, Leases: leaseReq}
	}

	start := make(chan struct{})
	results := make([]error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			req := mkReq()
			_, err := e.Prepare(domain.OperationRecord{OperationID: "op-" + string(rune('0'+i)), Digest: digestOf(req)}, req)
			results[i] = err
		}(i)
	}
	close(start)
	wg.Wait()

	successes, failures := 0, 0
	for _, err := range results {
		if err == nil {
			successes++
		} else {
			failures++
			if codes.CodeOf(err) != codes.CodeMaterialInsufficient {
				t.Fatalf("loser error = %v, want MATERIAL_INSUFFICIENT", err)
			}
		}
	}
	if successes != 1 || failures != 1 {
		t.Fatalf("got %d successes %d failures, want 1/1", successes, failures)
	}
	bal, _ := e.Balance(domain.MaterialPowder, "B1")
	if bal != 40 {
		t.Fatalf("powder balance = %d, want 40", bal)
	}
}

func TestPrepareCommitFailureRollsBack(t *testing.T) {
	fs := &fakeStore{}
	e := NewWithStore(fs)
	if err := e.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	registerSpanRecipe(t, e)
	if _, err := e.Lock("J1", standardDesign()); err != nil {
		t.Fatalf("Lock: %v", err)
	}
	stock(t, e, "B1", 100000, 20000, 1000, 5000)

	req := domain.MaterialRequest{
		Batch: "B1",
		Grams: map[domain.MaterialCategory]int64{
			domain.MaterialPowder:     100000,
			domain.MaterialWater:      20000,
			domain.MaterialAdmixture:  1000,
			domain.MaterialSteelFiber: 5000,
		},
		Leases: []domain.LeaseRequest{
			{Category: "MIXER", ResourceID: "MIXER-1", Holder: "crew-A", Purpose: "mix", Deadline: 1000},
		},
	}

	fs.failSave = true
	_, err := e.Prepare(domain.OperationRecord{OperationID: "op-commit-fail", Digest: digestOf(req)}, req)
	if err == nil {
		t.Fatalf("expected commit failure, got nil")
	}

	// Material must be fully restored and no lease may remain.
	bal, _ := e.Balance(domain.MaterialPowder, "B1")
	if bal != 100000 {
		t.Fatalf("powder balance = %d, want 100000 (rolled back)", bal)
	}
	if err := e.ReleaseLease("crew-A", "MIXER-1"); codes.CodeOf(err) != codes.CodeLeaseNotHolder {
		t.Fatalf("expected no lease after rollback, got %v", err)
	}
}

func TestPrepareIdempotency(t *testing.T) {
	e := NewInMemory()
	registerSpanRecipe(t, e)
	if _, err := e.Lock("J1", standardDesign()); err != nil {
		t.Fatalf("Lock: %v", err)
	}
	stock(t, e, "B1", 100000, 20000, 1000, 5000)

	req := domain.MaterialRequest{
		Batch: "B1",
		Grams: map[domain.MaterialCategory]int64{
			domain.MaterialPowder: 100000,
		},
		Leases: []domain.LeaseRequest{
			{Category: "MIXER", ResourceID: "MIXER-1", Holder: "crew-A", Purpose: "mix", Deadline: 1000},
		},
	}
	op := domain.OperationRecord{OperationID: "op-1", Digest: digestOf(req)}

	first, err := e.Prepare(op, req)
	if err != nil {
		t.Fatalf("first Prepare: %v", err)
	}

	// Same operation, same content: identical result, no double deduction.
	second, err := e.Prepare(op, req)
	if err != nil {
		t.Fatalf("idempotent replay: %v", err)
	}
	if len(second.Leases) != len(first.Leases) || second.Leases[0].Token != first.Leases[0].Token {
		t.Fatalf("idempotent replay returned different result")
	}
	bal, _ := e.Balance(domain.MaterialPowder, "B1")
	if bal != 0 {
		t.Fatalf("powder balance = %d, want 0 (single deduction)", bal)
	}

	// Same operation, different content: conflict.
	req2 := req
	req2.Grams = map[domain.MaterialCategory]int64{domain.MaterialPowder: 99999}
	_, err = e.Prepare(domain.OperationRecord{OperationID: "op-1", Digest: digestOf(req2)}, req2)
	if codes.CodeOf(err) != codes.CodeIdempotencyConflict {
		t.Fatalf("expected IDEMPOTENCY_CONFLICT, got %v", err)
	}
}
