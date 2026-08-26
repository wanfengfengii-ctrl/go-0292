package engine

import (
	"testing"

	"example.com/uhpc-wet-joint-traffic-release/internal/codes"
	"example.com/uhpc-wet-joint-traffic-release/internal/domain"
)

func TestModel_RecordMixAdvancesLogicalClockForLeaseExpiry(t *testing.T) {
	tests := []struct {
		name        string
		oldDeadline domain.LogicalTime
		wantCode    codes.Code
	}{
		{name: "lease ending before mix time is reusable", oldDeadline: 15},
		{name: "lease ending at mix time is reusable", oldDeadline: 20},
		{name: "lease ending after mix time still conflicts", oldDeadline: 21, wantCode: codes.CodeLeaseConflict},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := NewInMemory()
			lockStandard(t, e)
			stock(t, e, "B1", 200000, 40000, 2000, 10000)
			confirmSurface(t, e, "J1", 5)

			first := domain.MaterialRequest{
				Batch: "B1",
				Grams: map[domain.MaterialCategory]int64{
					domain.MaterialPowder:     100000,
					domain.MaterialWater:      20000,
					domain.MaterialAdmixture:  1000,
					domain.MaterialSteelFiber: 5000,
				},
				Leases: []domain.LeaseRequest{{
					Category: PourEquipment, ResourceID: "POUR-1", Holder: "crew-A",
					Purpose: "initial preparation", Deadline: tt.oldDeadline,
				}},
			}
			if _, err := e.Prepare(domain.OperationRecord{OperationID: "initial", Digest: digestOf(first)}, first); err != nil {
				t.Fatalf("initial Prepare: %v", err)
			}

			gen, err := e.RecordMix("J1", domain.MixRun{
				JointNumber: "J1", Batch: "B1", Sequence: 0,
				Powder: 100000, Water: 20000, Admixture: 1000, Fiber: 5000,
				Time: 20,
			})
			if err != nil {
				t.Fatalf("RecordMix: %v", err)
			}
			if gen.Deadline != 120 {
				t.Fatalf("generation deadline = %d, want 120", gen.Deadline)
			}

			second := domain.MaterialRequest{
				Batch: "B1",
				Grams: map[domain.MaterialCategory]int64{
					domain.MaterialPowder:     100000,
					domain.MaterialWater:      20000,
					domain.MaterialAdmixture:  1000,
					domain.MaterialSteelFiber: 5000,
				},
				Leases: []domain.LeaseRequest{{
					Category: PourEquipment, ResourceID: "POUR-1", Holder: "crew-B",
					Purpose: "formal pour", Deadline: 100,
				}},
			}
			got, err := e.Prepare(domain.OperationRecord{OperationID: "formal", Digest: digestOf(second)}, second)
			if code := codes.CodeOf(err); code != tt.wantCode {
				t.Fatalf("formal Prepare error code = %q (%v), want %q", code, err, tt.wantCode)
			}

			wantBalance := int64(0)
			if tt.wantCode == codes.CodeLeaseConflict {
				wantBalance = 100000
			} else {
				if len(got.Leases) != 1 {
					t.Fatalf("granted leases = %d, want 1", len(got.Leases))
				}
				if lease := got.Leases[0]; lease.Holder != "crew-B" || lease.Start != 20 {
					t.Fatalf("replacement lease = %+v, want holder crew-B starting at logical time 20", lease)
				}
			}
			balance, err := e.Balance(domain.MaterialPowder, "B1")
			if err != nil {
				t.Fatalf("Balance: %v", err)
			}
			if balance != wantBalance {
				t.Fatalf("powder balance = %d, want %d", balance, wantBalance)
			}
		})
	}
}
