package store

import (
	"os"
	"path/filepath"
	"testing"

	"example.com/uhpc-wet-joint-traffic-release/internal/codes"
	"example.com/uhpc-wet-joint-traffic-release/internal/domain"
)

// TestSaveRecoverRoundTrip verifies that a snapshot written to the embedded
// database survives a fresh store instance, proving the persistence boundary is
// real and durable.
func TestSaveRecoverRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")

	snap := domain.Snapshot{
		SchemaVersion: 1,
		Sequences:     []int64{1, 2, 3},
		Digest:        "abc123",
		State:         []byte(`{"materials":{}}`),
	}

	s := NewStore(path)
	if err := s.Save("stock", snap); err != nil {
		t.Fatalf("Save: %v", err)
	}
	defer s.Close()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("database file not written: %v", err)
	}

	// A second instance reads the persisted snapshot back from the database.
	s2 := NewStore(path)
	defer s2.Close()
	got, err := s2.Recover()
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if got.SchemaVersion != snap.SchemaVersion || got.Digest != snap.Digest {
		t.Fatalf("round-trip mismatch: got %+v, want %+v", got, snap)
	}
	if string(got.State) != string(snap.State) {
		t.Fatalf("state mismatch: got %q, want %q", got.State, snap.State)
	}
	if len(got.Sequences) != 3 || got.Sequences[2] != 3 {
		t.Fatalf("sequences mismatch: got %v, want [1 2 3]", got.Sequences)
	}
}

// TestSavePersistsEventInSameTransaction verifies that the committed event is
// written together with its snapshot so the event/snapshot boundary is atomic.
func TestSavePersistsEventInSameTransaction(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")

	s := NewStore(path)
	defer s.Close()
	snap := domain.Snapshot{
		SchemaVersion: 1,
		Sequences:     []int64{7},
		Digest:        "d7",
		State:         []byte(`{}`),
	}
	if err := s.Save("append-fill", snap); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if err := s.open(); err != nil {
		t.Fatalf("open: %v", err)
	}
	var seq int64
	var eventType, digest string
	if err := s.db.QueryRow("SELECT seq, event_type, digest FROM domain_event WHERE seq = 7").Scan(&seq, &eventType, &digest); err != nil {
		t.Fatalf("event not persisted with snapshot: %v", err)
	}
	if eventType != "append-fill" || digest != "d7" {
		t.Fatalf("event mismatch: type=%q digest=%q", eventType, digest)
	}
}

// TestRecoverFirstBoot verifies that a missing snapshot is a clean first boot,
// not a fault, and leaves the store writable.
func TestRecoverFirstBoot(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(filepath.Join(dir, "missing.db"))
	defer s.Close()

	if _, err := s.Recover(); err != ErrNoSnapshot {
		t.Fatalf("Recover = %v, want ErrNoSnapshot", err)
	}
	if !s.Healthy() {
		t.Fatalf("store should remain healthy after first boot")
	}
}

// TestRecoverCorruptDatabaseFault verifies that a corrupt database puts the
// store into the read-only RECOVERY_INTEGRITY_FAILED fault state.
func TestRecoverCorruptDatabaseFault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")
	if err := os.WriteFile(path, []byte("not a sqlite database"), 0o644); err != nil {
		t.Fatalf("write corrupt database: %v", err)
	}

	s := NewStore(path)
	defer s.Close()
	if _, err := s.Recover(); err == nil {
		t.Fatalf("Recover on corrupt database should fail")
	} else if codes.CodeOf(err) != codes.CodeRecoveryIntegrity {
		t.Fatalf("Recover error code = %q, want RECOVERY_INTEGRITY_FAILED", codes.CodeOf(err))
	}
	if s.Healthy() {
		t.Fatalf("store should be unhealthy after corrupt database")
	}
	if err := s.Save("stock", domain.Snapshot{}); err == nil {
		t.Fatalf("Save should be refused in the fault state")
	}
}
