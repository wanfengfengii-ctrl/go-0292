package engine_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"

	"example.com/uhpc-wet-joint-traffic-release/internal/codes"
	"example.com/uhpc-wet-joint-traffic-release/internal/domain"
	"example.com/uhpc-wet-joint-traffic-release/internal/engine"
)

func TestModel_PrepareRejectsOverlappingDuplicateResourceLeasesAtomically(t *testing.T) {
	tests := []struct {
		name   string
		leases []domain.LeaseRequest
	}{
		{
			name: "different holders with equal windows",
			leases: []domain.LeaseRequest{
				{Category: "MIXER", ResourceID: "MIXER-1", Holder: "crew-A", Purpose: "mix", Deadline: 1000},
				{Category: "MIXER", ResourceID: "MIXER-1", Holder: "crew-B", Purpose: "mix", Deadline: 1000},
			},
		},
		{
			name: "different holders with nested windows",
			leases: []domain.LeaseRequest{
				{Category: "MIXER", ResourceID: "MIXER-1", Holder: "crew-A", Purpose: "mix", Deadline: 800},
				{Category: "MIXER", ResourceID: "MIXER-1", Holder: "crew-B", Purpose: "mix", Deadline: 1200},
			},
		},
	}

	digest := func(req domain.MaterialRequest) string {
		raw, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("marshal request: %v", err)
		}
		sum := sha256.Sum256(raw)
		return hex.EncodeToString(sum[:])
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := engine.NewInMemory()
			if err := e.Stock(domain.MaterialPowder, "B1", 100); err != nil {
				t.Fatalf("Stock: %v", err)
			}

			req := domain.MaterialRequest{
				Batch:  "B1",
				Grams:  map[domain.MaterialCategory]int64{domain.MaterialPowder: 25},
				Leases: tt.leases,
			}
			op := domain.OperationRecord{OperationID: "prepare-J1", Digest: digest(req)}
			got, err := e.Prepare(op, req)
			if codes.CodeOf(err) != codes.CodeLeaseConflict {
				t.Fatalf("Prepare error = %v, want LEASE_CONFLICT", err)
			}
			if len(got.Leases) != 0 {
				t.Fatalf("Prepare returned %d leases on conflict, want none", len(got.Leases))
			}

			balance, err := e.Balance(domain.MaterialPowder, "B1")
			if err != nil {
				t.Fatalf("Balance: %v", err)
			}
			if balance != 100 {
				t.Fatalf("balance after conflict = %d, want 100", balance)
			}
			for _, holder := range []string{"crew-A", "crew-B"} {
				if err := e.ReleaseLease(holder, "MIXER-1"); codes.CodeOf(err) != codes.CodeLeaseNotHolder {
					t.Fatalf("ReleaseLease(%q) error = %v, want LEASE_NOT_HOLDER", holder, err)
				}
			}

			validReq := req
			validReq.Leases = []domain.LeaseRequest{tt.leases[0]}
			validOp := domain.OperationRecord{OperationID: op.OperationID, Digest: digest(validReq)}
			validSet, err := e.Prepare(validOp, validReq)
			if err != nil {
				t.Fatalf("valid retry with same operation ID: %v", err)
			}
			if len(validSet.Leases) != 1 || !validSet.Leases[0].Active || validSet.Leases[0].Token == "" {
				t.Fatalf("valid retry leases = %+v, want one active token", validSet.Leases)
			}
		})
	}
}
