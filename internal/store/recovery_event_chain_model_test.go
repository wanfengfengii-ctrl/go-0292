package store_test

import (
	"database/sql"
	"path/filepath"
	"testing"

	"example.com/uhpc-wet-joint-traffic-release/internal/codes"
	"example.com/uhpc-wet-joint-traffic-release/internal/domain"
	"example.com/uhpc-wet-joint-traffic-release/internal/engine"
	"example.com/uhpc-wet-joint-traffic-release/internal/store"
	_ "modernc.org/sqlite"
)

func TestModel_RecoveryRejectsDivergentEventChain(t *testing.T) {
	tests := []struct {
		name    string
		corrupt string
	}{
		{
			name:    "missing event",
			corrupt: "DELETE FROM domain_event WHERE seq = 1",
		},
		{
			name:    "duplicate snapshot sequence",
			corrupt: `UPDATE snapshot SET sequences = '[1,1]' WHERE id = 1`,
		},
		{
			name:    "event digest drift",
			corrupt: `UPDATE domain_event SET digest = 'tampered' WHERE seq = 2`,
		},
		{
			name:    "noncontiguous event sequence",
			corrupt: "UPDATE domain_event SET seq = 4 WHERE seq = 2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state.db")
			writer := engine.New(path)
			if err := writer.Recover(); err != nil {
				t.Fatalf("first-boot Recover: %v", err)
			}
			if err := writer.Stock(domain.MaterialPowder, "P1", 100); err != nil {
				t.Fatalf("first Stock: %v", err)
			}
			if err := writer.Stock(domain.MaterialWater, "W1", 20); err != nil {
				t.Fatalf("second Stock: %v", err)
			}

			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatalf("open database for fault injection: %v", err)
			}
			if _, err := db.Exec(tt.corrupt); err != nil {
				_ = db.Close()
				t.Fatalf("inject recovery inconsistency: %v", err)
			}
			if err := db.Close(); err != nil {
				t.Fatalf("close fault-injection connection: %v", err)
			}

			durable := store.NewStore(path)
			t.Cleanup(func() { _ = durable.Close() })
			if _, err := durable.Recover(); codes.CodeOf(err) != codes.CodeRecoveryIntegrity {
				t.Fatalf("store Recover error = %v (code %q), want RECOVERY_INTEGRITY_FAILED", err, codes.CodeOf(err))
			}
			if durable.Healthy() {
				t.Fatal("store remained writable after recovery integrity failure")
			}
			if err := durable.Save("stock", domain.Snapshot{}); codes.CodeOf(err) != codes.CodeRecoveryIntegrity {
				t.Fatalf("store Save after fault = %v (code %q), want read-only RECOVERY_INTEGRITY_FAILED", err, codes.CodeOf(err))
			}

			restarted := engine.New(path)
			if err := restarted.Recover(); codes.CodeOf(err) != codes.CodeRecoveryIntegrity {
				t.Fatalf("engine Recover error = %v (code %q), want RECOVERY_INTEGRITY_FAILED", err, codes.CodeOf(err))
			}
			if restarted.Healthy() {
				t.Fatal("engine remained writable after recovery integrity failure")
			}
			if err := restarted.Stock(domain.MaterialSteelFiber, "F1", 5); codes.CodeOf(err) != codes.CodeRecoveryIntegrity {
				t.Fatalf("engine write after fault = %v (code %q), want read-only RECOVERY_INTEGRITY_FAILED", err, codes.CodeOf(err))
			}
		})
	}
}
