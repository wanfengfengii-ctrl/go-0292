// Package store provides the persistence and restart-recovery boundary of the
// UHPC wet-joint service. The durable aggregate snapshot and its committed
// transaction events are written atomically to an embedded SQLite database in a
// single transaction, so a crash mid-write never corrupts the durable state.
// Recovery re-reads the snapshot and validates the schema and event sequence,
// leaving the store read-only if anything is inconsistent.
package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	_ "modernc.org/sqlite" // register the pure-Go SQLite driver

	"example.com/uhpc-wet-joint-traffic-release/internal/codes"
	"example.com/uhpc-wet-joint-traffic-release/internal/domain"
)

// ErrNoSnapshot indicates the store has no durable snapshot yet (first boot).
var ErrNoSnapshot = errors.New("store: no snapshot present")

// Store is a SQLite-backed durable snapshot store. It lazily opens and migrates
// the database on first access so construction never fails.
type Store struct {
	mu      sync.Mutex
	path    string
	db      *sql.DB
	healthy bool
}

// NewStore builds a store rooted at path. The database file and directory are
// created on first Save or Recover, not at construction, so a read-only first
// boot still works. An empty path means no durable backing store (in-memory).
func NewStore(path string) *Store {
	return &Store{path: path, healthy: true}
}

// Path returns the durable database path ("" for in-memory stores).
func (s *Store) Path() string { return s.path }

// Close releases the underlying database connection, if open.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil
	}
	err := s.db.Close()
	s.db = nil
	return err
}

// open lazily opens and migrates the database. It must be called with s.mu held.
func (s *Store) open() error {
	if s.path == "" {
		return nil
	}
	if s.db != nil {
		return nil
	}

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("store: create dir: %w", err)
	}

	db, err := sql.Open("sqlite", s.path)
	if err != nil {
		return fmt.Errorf("store: open database: %w", err)
	}
	// The engine serializes all access under its own mutex; a single pooled
	// connection avoids SQLite "database is locked" under concurrent readers.
	db.SetMaxOpenConns(1)

	if err := applyMigrations(db); err != nil {
		_ = db.Close()
		return err
	}
	s.db = db
	return nil
}

// Recover loads the latest snapshot from the database. It returns ErrNoSnapshot
// when no snapshot exists (first boot) and leaves the store healthy.
func (s *Store) Recover() (domain.Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.path == "" {
		return domain.Snapshot{}, ErrNoSnapshot
	}

	if err := s.open(); err != nil {
		s.healthy = false
		return domain.Snapshot{}, codes.New(codes.CodeRecoveryIntegrity, "cannot open store: "+err.Error())
	}

	var (
		schemaVersion int
		sequencesJSON string
		digest        string
		state         []byte
	)
	err := s.db.QueryRow(
		"SELECT schema_version, sequences, digest, state FROM snapshot WHERE id = 1",
	).Scan(&schemaVersion, &sequencesJSON, &digest, &state)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Snapshot{}, ErrNoSnapshot
	}
	if err != nil {
		s.healthy = false
		return domain.Snapshot{}, codes.New(codes.CodeRecoveryIntegrity, "snapshot read failed: "+err.Error())
	}

	var sequences []int64
	if err := json.Unmarshal([]byte(sequencesJSON), &sequences); err != nil {
		s.healthy = false
		return domain.Snapshot{}, codes.New(codes.CodeRecoveryIntegrity, "snapshot sequences are corrupt: "+err.Error())
	}

	s.healthy = true
	return domain.Snapshot{
		SchemaVersion: schemaVersion,
		Sequences:     sequences,
		Digest:        digest,
		State:         state,
	}, nil
}

// Healthy reports whether the store is writable.
func (s *Store) Healthy() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.healthy
}

// MarkUnhealthy forces the store into a read-only fault state.
func (s *Store) MarkUnhealthy() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.healthy = false
}

// Save atomically persists a snapshot and its newest committed event in one
// transaction. The caller (engine) is responsible for stamping snap.Digest
// before Save.
func (s *Store) Save(eventType string, snap domain.Snapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.healthy {
		return codes.New(codes.CodeRecoveryIntegrity, "store is read-only after recovery fault")
	}
	if s.path == "" {
		return nil
	}
	if err := s.open(); err != nil {
		return codes.New(codes.CodeRecoveryIntegrity, "cannot open store: "+err.Error())
	}

	sequencesJSON, err := json.Marshal(snap.Sequences)
	if err != nil {
		return fmt.Errorf("store: marshal sequences: %w", err)
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("store: begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(`
		INSERT INTO snapshot (id, schema_version, sequences, digest, state)
		VALUES (1, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			schema_version = excluded.schema_version,
			sequences      = excluded.sequences,
			digest         = excluded.digest,
			state          = excluded.state`,
		snap.SchemaVersion, string(sequencesJSON), snap.Digest, snap.State,
	); err != nil {
		return fmt.Errorf("store: write snapshot: %w", err)
	}

	// The newest committed event is written in the same transaction as its
	// snapshot, preserving the event/snapshot atomicity boundary. An idempotent
	// replay does not advance the sequence, so an already-recorded event is
	// ignored rather than overwritten.
	if len(snap.Sequences) > 0 {
		if _, err := tx.Exec(
			"INSERT OR IGNORE INTO domain_event (seq, event_type, digest) VALUES (?, ?, ?)",
			snap.Sequences[len(snap.Sequences)-1], eventType, snap.Digest,
		); err != nil {
			return fmt.Errorf("store: write event: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit transaction: %w", err)
	}
	return nil
}
