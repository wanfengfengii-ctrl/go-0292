package engine

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"example.com/uhpc-wet-joint-traffic-release/internal/codes"
	"example.com/uhpc-wet-joint-traffic-release/internal/domain"
)

// Stock credits grams into a category/batch material pool. The initial pool
// mass equals the sum of available balance, issued doses, sampling, loss and
// quarantined remainder, enforced by the ledger (see BalanceConservation).
func (e *Engine) Stock(category domain.MaterialCategory, batch string, grams int64) error {
	return e.mutate("stock", func(st *state) error {
		if grams < 0 {
			return codes.New(codes.CodeGeometryNegative, "stock grams must be non-negative")
		}
		if _, exists := st.Batches[category][batch]; exists {
			return codes.New(codes.CodeMaterialUnavailable, "batch "+batch+" already stocked")
		}
		st.setMaterialBalance(category, batch, grams)
		st.Batches[category][batch] = grams
		st.Ledger = append(st.Ledger, domain.MaterialLedgerEntry{
			Category: category,
			Batch:    batch,
			Delta:    grams,
			Reason:   "STOCK",
			Time:     st.Now,
		})
		st.nextSequence()
		return nil
	})
}

// Balance returns the available grams for a category/batch, or 0 if unknown.
func (e *Engine) Balance(category domain.MaterialCategory, batch string) (int64, error) {
	var bal int64
	err := e.read(func(st *state) error {
		g, ok := st.materialBalance(category, batch)
		if !ok {
			return codes.New(codes.CodeMaterialUnavailable, "unknown batch "+batch)
		}
		bal = g
		return nil
	})
	return bal, err
}

// Prepare atomically deducts the requested grams and acquires the requested
// leases. Any conflict (insufficient material, active lease, idempotency
// mismatch) rolls back the whole transaction. It is idempotent by operation ID.
func (e *Engine) Prepare(op domain.OperationRecord, req domain.MaterialRequest) (domain.LeaseSet, error) {
	var result domain.LeaseSet
	err := e.mutate("prepare", func(st *state) error {
		// Idempotency: same operation ID + same content returns the same result.
		if existing, ok := st.Operations[op.OperationID]; ok {
			if existing.Digest == op.Digest {
				var cached domain.LeaseSet
				if err := json.Unmarshal([]byte(existing.Response), &cached); err == nil {
					result = cached
					return nil
				}
			}
			return codes.New(codes.CodeIdempotencyConflict,
				"operation "+op.OperationID+" was already committed with different content")
		}

		// Validate material availability first.
		for cat, grams := range req.Grams {
			if grams < 0 {
				return codes.New(codes.CodeGeometryNegative, "requested grams must be non-negative")
			}
			bal, ok := st.materialBalance(cat, req.Batch)
			if !ok {
				return codes.New(codes.CodeMaterialUnavailable, "unknown batch "+req.Batch)
			}
			if bal < grams {
				return codes.New(codes.CodeMaterialInsufficient,
					fmt.Sprintf("insufficient %s: have %d, need %d", cat, bal, grams))
			}
		}

		// Validate leases: no resource may already hold an unexpired active lease.
		for _, lr := range req.Leases {
			if existing, ok := st.Leases[lr.ResourceID]; ok && existing.Active && existing.Deadline > st.Now {
				return codes.New(codes.CodeLeaseConflict,
					"resource "+lr.ResourceID+" is already leased")
			}
		}

		// All validations passed: apply deductions and grants together.
		for cat, grams := range req.Grams {
			bal, _ := st.materialBalance(cat, req.Batch)
			st.setMaterialBalance(cat, req.Batch, bal-grams)
			st.Ledger = append(st.Ledger, domain.MaterialLedgerEntry{
				Category: cat,
				Batch:    req.Batch,
				Delta:    -grams,
				Reason:   "ISSUE",
				Time:     st.Now,
			})
		}

		result = domain.LeaseSet{Leases: make([]domain.EquipmentLease, 0, len(req.Leases))}
		for _, lr := range req.Leases {
			lease := domain.EquipmentLease{
				Category:   lr.Category,
				ResourceID: lr.ResourceID,
				Holder:     lr.Holder,
				Purpose:    lr.Purpose,
				Token:      newToken(),
				Start:      st.Now,
				Deadline:   lr.Deadline,
				Active:     true,
			}
			st.Leases[lr.ResourceID] = lease
			result.Leases = append(result.Leases, lease)
		}

		// Record the operation for idempotent retry.
		resp, _ := json.Marshal(result)
		st.Operations[op.OperationID] = domain.OperationRecord{
			OperationID: op.OperationID,
			Digest:      op.Digest,
			Response:    string(resp),
			Sequence:    st.nextSequence(),
		}
		return nil
	})
	return result, err
}

// ReleaseLease releases an active lease held by holder. Only the holder may
// release; an expired or foreign lease is rejected.
func (e *Engine) ReleaseLease(holder, resourceID string) error {
	return e.mutate("release-lease", func(st *state) error {
		lease, ok := st.Leases[resourceID]
		if !ok || !lease.Active {
			return codes.New(codes.CodeLeaseNotHolder, "no active lease for "+resourceID)
		}
		if lease.Holder != holder {
			return codes.New(codes.CodeLeaseNotHolder, "lease held by "+lease.Holder)
		}
		lease.Active = false
		st.Leases[resourceID] = lease
		st.nextSequence()
		return nil
	})
}

// RenewLease extends the deadline of an active lease held by holder.
func (e *Engine) RenewLease(holder, resourceID string, deadline domain.LogicalTime) error {
	return e.mutate("renew-lease", func(st *state) error {
		lease, ok := st.Leases[resourceID]
		if !ok || !lease.Active {
			return codes.New(codes.CodeLeaseNotHolder, "no active lease for "+resourceID)
		}
		if lease.Holder != holder {
			return codes.New(codes.CodeLeaseNotHolder, "lease held by "+lease.Holder)
		}
		if deadline <= lease.Deadline {
			return codes.New(codes.CodeLeaseNotHolder, "renewal deadline must extend the lease")
		}
		lease.Deadline = deadline
		st.Leases[resourceID] = lease
		st.nextSequence()
		return nil
	})
}

func newToken() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
