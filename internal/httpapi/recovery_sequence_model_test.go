package httpapi_test

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"example.com/uhpc-wet-joint-traffic-release/internal/codes"
	"example.com/uhpc-wet-joint-traffic-release/internal/domain"
	"example.com/uhpc-wet-joint-traffic-release/internal/engine"
	"example.com/uhpc-wet-joint-traffic-release/internal/httpapi"
	"example.com/uhpc-wet-joint-traffic-release/internal/store"
)

func TestModel_RecoveryRequiresSnapshotAndStateSequencesToMatch(t *testing.T) {
	tests := []struct {
		name              string
		seedSnapshot      bool
		snapshotSequences []int64
		wantHealthy       bool
	}{
		{name: "first boot", wantHealthy: true},
		{name: "matching restart", seedSnapshot: true, wantHealthy: true},
		{name: "length divergence", seedSnapshot: true, snapshotSequences: []int64{1, 2}, wantHealthy: false},
		{name: "order divergence", seedSnapshot: true, snapshotSequences: []int64{2, 1, 3}, wantHealthy: false},
		{name: "content divergence", seedSnapshot: true, snapshotSequences: []int64{1, 2, 4}, wantHealthy: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state.db")
			seedStore := store.NewStore(path)
			seedEngine := engine.NewWithStore(seedStore)
			if err := seedEngine.Recover(); err != nil {
				t.Fatalf("first-boot Recover: %v", err)
			}
			if tc.seedSnapshot {
				for i, category := range []domain.MaterialCategory{
					domain.MaterialPowder,
					domain.MaterialWater,
					domain.MaterialAdmixture,
				} {
					if err := seedEngine.Stock(category, "seed", int64(i+1)); err != nil {
						t.Fatalf("seed stock %d: %v", i, err)
					}
				}
			}
			if err := seedStore.Close(); err != nil {
				t.Fatalf("close seed store: %v", err)
			}

			if tc.snapshotSequences != nil {
				raw, err := json.Marshal(tc.snapshotSequences)
				if err != nil {
					t.Fatalf("marshal divergent sequences: %v", err)
				}
				db, err := sql.Open("sqlite", path)
				if err != nil {
					t.Fatalf("open snapshot database: %v", err)
				}
				if _, err := db.Exec("UPDATE snapshot SET sequences = ? WHERE id = 1", string(raw)); err != nil {
					_ = db.Close()
					t.Fatalf("diverge snapshot sequences: %v", err)
				}
				if err := db.Close(); err != nil {
					t.Fatalf("close snapshot database: %v", err)
				}
			}

			recoveredStore := store.NewStore(path)
			t.Cleanup(func() { _ = recoveredStore.Close() })
			recoveredEngine := engine.NewWithStore(recoveredStore)
			err := recoveredEngine.Recover()
			if tc.wantHealthy {
				if err != nil {
					t.Fatalf("Recover: %v", err)
				}
			} else if codes.CodeOf(err) != codes.CodeRecoveryIntegrity {
				t.Errorf("Recover code = %q, want %q (err=%v)", codes.CodeOf(err), codes.CodeRecoveryIntegrity, err)
			}

			if got := recoveredEngine.Healthy(); got != tc.wantHealthy {
				t.Errorf("Engine.Healthy = %v, want %v", got, tc.wantHealthy)
			}
			if got := recoveredStore.Healthy(); got != tc.wantHealthy {
				t.Errorf("Store.Healthy = %v, want %v", got, tc.wantHealthy)
			}

			req := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
			rr := httptest.NewRecorder()
			httpapi.NewWithEngine(recoveredEngine).ServeHTTP(rr, req)
			wantStatus := http.StatusOK
			if !tc.wantHealthy {
				wantStatus = http.StatusServiceUnavailable
			}
			if rr.Code != wantStatus {
				t.Errorf("GET /v1/health status = %d, want %d", rr.Code, wantStatus)
			}

			materialErr := recoveredEngine.Stock(domain.MaterialSteelFiber, "after-recovery", 1)
			deviceErr := recoveredEngine.RecordDeviceFailure(domain.DeviceCall{
				Key: "after-recovery", Instrument: "SCALE", Attempt: 1, Failure: "TIMEOUT", Time: 1,
			})
			if tc.wantHealthy {
				if materialErr != nil || deviceErr != nil {
					t.Errorf("healthy recovery rejected writes: material=%v device=%v", materialErr, deviceErr)
				}
			} else {
				if codes.CodeOf(materialErr) != codes.CodeRecoveryIntegrity {
					t.Errorf("material write code = %q, want %q", codes.CodeOf(materialErr), codes.CodeRecoveryIntegrity)
				}
				if codes.CodeOf(deviceErr) != codes.CodeRecoveryIntegrity {
					t.Errorf("device write code = %q, want %q", codes.CodeOf(deviceErr), codes.CodeRecoveryIntegrity)
				}
			}
		})
	}
}
