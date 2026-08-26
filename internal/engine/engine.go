// Package engine is the application layer that coordinates the five business
// components of the UHPC wet-joint traffic-release domain over a single durable
// aggregate state. Every mutation validates domain rules, advances the logical
// clock, and atomically persists a digest-stamped snapshot; on restart the
// snapshot and event sequence are re-validated before the service becomes
// writable again.
package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"

	"example.com/uhpc-wet-joint-traffic-release/internal/codes"
	"example.com/uhpc-wet-joint-traffic-release/internal/domain"
	"example.com/uhpc-wet-joint-traffic-release/internal/store"
)

// snapshotStore is the persistence boundary the engine depends on. It is an
// interface so tests can inject a store that fails mid-commit.
type snapshotStore interface {
	Recover() (domain.Snapshot, error)
	Healthy() bool
	MarkUnhealthy()
	Save(eventType string, snap domain.Snapshot) error
	Path() string
}

// Engine coordinates the aggregate state and the durable snapshot store. It
// implements domain.Catalog, domain.JointAggregate, domain.MaterialLedger,
// domain.EvidenceRecorder and domain.Arbitrator.
type Engine struct {
	mu      sync.Mutex
	st      *state
	store   snapshotStore
	healthy bool
}

// New builds an Engine backed by the snapshot file at path. The state is empty
// until Recover is called.
func New(path string) *Engine {
	return &Engine{
		st:      newState(),
		store:   store.NewStore(path),
		healthy: true,
	}
}

// NewWithStore builds an Engine backed by an arbitrary snapshot store.
func NewWithStore(st snapshotStore) *Engine {
	return &Engine{st: newState(), store: st, healthy: true}
}

// NewInMemory builds an Engine with no durable backing store (still fully
// functional for tests and single-process use).
func NewInMemory() *Engine {
	return New("")
}

// Recover loads the durable snapshot and validates its digest and event
// sequence. On any inconsistency the engine enters a read-only fault state that
// returns RECOVERY_INTEGRITY_FAILED for subsequent writes.
func (e *Engine) Recover() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	snap, err := e.store.Recover()
	if err != nil {
		if errors.Is(err, store.ErrNoSnapshot) {
			e.healthy = true
			return nil
		}
		e.healthy = false
		return err
	}

	var st state
	if err := json.Unmarshal(snap.State, &st); err != nil {
		e.healthy = false
		e.store.MarkUnhealthy()
		return codes.New(codes.CodeRecoveryIntegrity, "snapshot state is corrupt: "+err.Error())
	}

	if err := validateSequence(st.Committed, st.Sequence); err != nil {
		e.healthy = false
		e.store.MarkUnhealthy()
		return err
	}

	digest, err := computeDigest(&st)
	if err != nil {
		e.healthy = false
		e.store.MarkUnhealthy()
		return err
	}
	if digest != snap.Digest {
		e.healthy = false
		e.store.MarkUnhealthy()
		return codes.New(codes.CodeRecoveryIntegrity, "snapshot digest mismatch")
	}

	e.st = &st
	e.healthy = true
	return nil
}

// Healthy reports whether the engine is writable after recovery.
func (e *Engine) Healthy() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.healthy
}

// StorePath returns the durable snapshot path ("" for in-memory engines).
func (e *Engine) StorePath() string { return e.store.Path() }

// snapshot builds the durable snapshot for the current state, stamping the
// digest. It must be called with e.mu held.
func snapshotLocked(st *state) (domain.Snapshot, error) {
	raw, err := json.Marshal(st)
	if err != nil {
		return domain.Snapshot{}, err
	}
	sum := sha256.Sum256(raw)
	return domain.Snapshot{
		SchemaVersion: 1,
		Sequences:     append([]int64(nil), st.Committed...),
		Digest:        hex.EncodeToString(sum[:]),
		State:         raw,
	}, nil
}

// computeDigest recomputes the digest for an already-deserialized state.
func computeDigest(st *state) (string, error) {
	raw, err := json.Marshal(st)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// validateSequence asserts that Committed is the contiguous prefix [1..N] where
// N == Sequence, i.e. no committed event is missing or duplicated.
func validateSequence(committed []int64, seq int64) error {
	if seq < 0 {
		return codes.New(codes.CodeRecoveryIntegrity, "negative sequence counter")
	}
	if int64(len(committed)) != seq {
		return codes.New(codes.CodeRecoveryIntegrity, "event sequence length mismatch")
	}
	for i, n := range committed {
		if n != int64(i+1) {
			return codes.New(codes.CodeRecoveryIntegrity, "event sequence is not contiguous")
		}
	}
	return nil
}

// mutate runs fn against the live state under the write lock, and if fn returns
// nil, commits the resulting state to the durable snapshot store. Any error
// leaves the state untouched (transactional rollback semantics).
func (e *Engine) mutate(eventType string, fn func(st *state) error) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.healthy {
		return codes.New(codes.CodeRecoveryIntegrity, "service is read-only after recovery fault")
	}

	// Clone state so a failed fn cannot partially mutate the live aggregate.
	working := cloneState(e.st)
	if err := fn(working); err != nil {
		return err
	}

	if e.store.Path() != "" {
		snap, err := snapshotLocked(working)
		if err != nil {
			return err
		}
		if err := e.store.Save(eventType, snap); err != nil {
			return err
		}
	}
	e.st = working
	return nil
}

// cloneState deep-copies the aggregate state so mutations can be rolled back.
func cloneState(src *state) *state {
	raw, _ := json.Marshal(src)
	var dst state
	_ = json.Unmarshal(raw, &dst)
	return &dst
}

// read runs fn against the live state under the read lock.
func (e *Engine) read(fn func(st *state) error) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return fn(e.st)
}
