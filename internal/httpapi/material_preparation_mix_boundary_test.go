package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"path/filepath"
	"reflect"
	"testing"

	"example.com/uhpc-wet-joint-traffic-release/internal/domain"
	"example.com/uhpc-wet-joint-traffic-release/internal/engine"
	"example.com/uhpc-wet-joint-traffic-release/internal/geometry"
	"example.com/uhpc-wet-joint-traffic-release/internal/store"
)

func TestModel_MixRunRequiresMatchingMaterialPreparation(t *testing.T) {
	type jointView struct {
		Generation string            `json:"generation"`
		Prefix     domain.PourPrefix `json:"prefix"`
		Fills      []domain.FillCell `json:"fills"`
	}
	type persistedState struct {
		Generations map[string]domain.MaterialGeneration
	}
	type testCase struct {
		name          string
		prepareJoint  string
		prepareBatch  string
		emptyGrams    bool
		preparePowder int64
		retryPrepare  bool
		firstMix      bool
		powder        int64
		wantAccepted  bool
	}

	fullGrams := func() map[string]int64 {
		return map[string]int64{
			"POWDER": 100000, "WATER": 20000, "ADMIXTURE": 1000, "STEEL_FIBER": 5000,
		}
	}
	design := func(joint string) domain.JointDesign {
		return domain.JointDesign{
			JointNumber: joint, SpanID: "S1", Recipe: "UHPC-1", LockVersion: 1,
			Geometry: geometry.Design{
				Range: geometry.Range{Start: 0, End: 999},
				Segments: []geometry.Segment{
					{Index: 0, Start: 0, End: 499},
					{Index: 1, Start: 500, End: 999},
				},
				Layers: 1, Direction: geometry.DirectionAscending, Rebar: geometry.Rebar{Cover: 50},
			},
			SurfaceZones: []domain.SurfaceZone{{ID: "Z1", Required: true}},
			MixPlans: []domain.MixPlan{
				{Batch: "B1", Sequence: 0, Powder: 100000, Water: 20000, Admixture: 1000, Fiber: 5000},
				{Batch: "B1", Sequence: 1, Powder: 100000, Water: 20000, Admixture: 1000, Fiber: 5000},
			},
			Curing: domain.CuringSchedule{
				DurationMinutes: 60, MinTemperature: fpv(20, 0), MinHumidity: fpv(90, 0),
			},
			Adjacency: [][]int{{1}, {0}}, SegmentZones: map[int][]string{0: {"Z1"}, 1: {"Z1"}},
		}
	}
	readView := func(t *testing.T, srv *Server) jointView {
		t.Helper()
		rr := do(t, srv, http.MethodGet, "/v1/joints/J1", nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("get joint: status=%d body=%s", rr.Code, rr.Body.String())
		}
		var view jointView
		if err := json.Unmarshal(rr.Body.Bytes(), &view); err != nil {
			t.Fatalf("decode joint view: %v", err)
		}
		return view
	}
	readSnapshot := func(t *testing.T, path string) domain.Snapshot {
		t.Helper()
		s := store.NewStore(path)
		snap, err := s.Recover()
		if err != nil {
			t.Fatalf("recover persisted snapshot: %v", err)
		}
		if err := s.Close(); err != nil {
			t.Fatalf("close snapshot reader: %v", err)
		}
		return snap
	}
	generationCount := func(t *testing.T, snap domain.Snapshot) int {
		t.Helper()
		var state persistedState
		if err := json.Unmarshal(snap.State, &state); err != nil {
			t.Fatalf("decode persisted state: %v", err)
		}
		return len(state.Generations)
	}

	tests := []testCase{
		{name: "missing preparation", powder: 100000},
		{name: "empty grams preparation", prepareJoint: "J1", prepareBatch: "B1", emptyGrams: true, powder: 100000},
		{name: "preparation belongs to another joint", prepareJoint: "J2", prepareBatch: "B1", powder: 100000},
		{name: "preparation uses another material batch", prepareJoint: "J1", prepareBatch: "B2", powder: 100000},
		{name: "preparation grams do not match mix", prepareJoint: "J1", prepareBatch: "B1", preparePowder: 99000, powder: 100000},
		{name: "preparation belongs to prior mix sequence", prepareJoint: "J1", prepareBatch: "B1", firstMix: true, powder: 100000},
		{name: "prepared dose does not excuse dosage deviation", prepareJoint: "J1", prepareBatch: "B1", powder: 103000},
		{name: "matching preparation with idempotent retry", prepareJoint: "J1", prepareBatch: "B1", retryPrepare: true, powder: 100000, wantAccepted: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "state.db")
			eng := engine.New(dbPath)
			srv := NewWithEngine(eng)

			if rr := do(t, srv, http.MethodPost, "/v1/recipes", domain.RecipeRule{
				Name: "UHPC-1", AllowDeviation: fpv(2, 2), FlowMin: fpv(180, 1), FlowMax: fpv(260, 1),
				WorkWindow: 100, MinStrength: fpv(50, 0), MinBondStrength: fpv(2, 0), MaxShrinkage: fpv(500, 0),
			}); rr.Code != http.StatusCreated {
				t.Fatalf("register recipe: status=%d body=%s", rr.Code, rr.Body.String())
			}
			if rr := do(t, srv, http.MethodPost, "/v1/spans", domain.BridgeSpan{
				ID: "S1", CoordinateScale: 1000, AllowedRecipes: []string{"UHPC-1"}, RuleDigest: "v1",
			}); rr.Code != http.StatusCreated {
				t.Fatalf("register span: status=%d body=%s", rr.Code, rr.Body.String())
			}
			for _, batch := range []string{"B1", "B2"} {
				for category, grams := range map[string]int64{
					"POWDER": 300000, "WATER": 60000, "ADMIXTURE": 3000, "STEEL_FIBER": 15000,
				} {
					rr := do(t, srv, http.MethodPost, "/v1/materials", map[string]any{
						"category": category, "batch": batch, "grams": grams,
					})
					if rr.Code != http.StatusCreated {
						t.Fatalf("stock %s/%s: status=%d body=%s", category, batch, rr.Code, rr.Body.String())
					}
				}
			}
			for _, joint := range []string{"J1", "J2"} {
				if rr := do(t, srv, http.MethodPost, "/v1/joints", design(joint)); rr.Code != http.StatusCreated {
					t.Fatalf("lock %s: status=%d body=%s", joint, rr.Code, rr.Body.String())
				}
				if rr := do(t, srv, http.MethodPost, "/v1/joints/"+joint+"/surface-evidence", domain.SurfaceEvidence{
					ZoneID: "Z1", Clean: true, PreWet: true, Time: 5,
				}); rr.Code != http.StatusCreated {
					t.Fatalf("surface %s: status=%d body=%s", joint, rr.Code, rr.Body.String())
				}
			}
			if rr := do(t, srv, http.MethodPost, "/v1/joints/J1/flow-tests", domain.FlowTest{
				Value: fpv(200, 1), Passed: true, Time: 10,
			}); rr.Code != http.StatusCreated {
				t.Fatalf("flow: status=%d body=%s", rr.Code, rr.Body.String())
			}

			if tc.prepareJoint != "" {
				grams := fullGrams()
				if tc.emptyGrams {
					grams = map[string]int64{}
				} else if tc.preparePowder != 0 {
					grams["POWDER"] = tc.preparePowder
				}
				prep := map[string]any{
					"operation_id": "prep-1", "batch": tc.prepareBatch, "grams": grams,
					"leases": []map[string]any{{
						"category": "POUR", "resource_id": "POUR-1", "holder": "crew-A", "purpose": "place", "deadline": 1000,
					}},
				}
				path := "/v1/joints/" + tc.prepareJoint + "/material-preparations"
				if rr := do(t, srv, http.MethodPost, path, prep); rr.Code != http.StatusCreated {
					t.Fatalf("prepare: status=%d body=%s", rr.Code, rr.Body.String())
				}
				if tc.retryPrepare {
					if rr := do(t, srv, http.MethodPost, path, prep); rr.Code != http.StatusCreated {
						t.Fatalf("idempotent prepare retry: status=%d body=%s", rr.Code, rr.Body.String())
					}
					if balance, err := eng.Balance(domain.MaterialPowder, "B1"); err != nil || balance != 200000 {
						t.Fatalf("idempotent retry powder balance=(%d,%v), want (200000,nil)", balance, err)
					}
				}
			}

			sequence := 0
			if tc.firstMix {
				first := domain.MixRun{
					JointNumber: "J1", Batch: "B1", Sequence: 0,
					Powder: 100000, Water: 20000, Admixture: 1000, Fiber: 5000, Time: 20,
				}
				if rr := do(t, srv, http.MethodPost, "/v1/joints/J1/mix-runs", first); rr.Code != http.StatusCreated {
					t.Fatalf("first prepared mix: status=%d body=%s", rr.Code, rr.Body.String())
				}
				sequence = 1
			}

			beforeView := readView(t, srv)
			beforeSnap := readSnapshot(t, dbPath)
			mix := domain.MixRun{
				JointNumber: "J1", Batch: "B1", Sequence: sequence,
				Powder: tc.powder, Water: 20000, Admixture: 1000, Fiber: 5000, Time: 30,
			}
			mixResponse := do(t, srv, http.MethodPost, "/v1/joints/J1/mix-runs", mix)
			var generated domain.MaterialGeneration
			if mixResponse.Code == http.StatusCreated {
				if err := json.Unmarshal(mixResponse.Body.Bytes(), &generated); err != nil {
					t.Fatalf("decode generated material: %v", err)
				}
			}

			fillResponse := do(t, srv, http.MethodPost, "/v1/joints/J1/fills", domain.FillCell{
				Segment: 0, Layer: 0, MixBatch: "B1", Generation: generated.ID, Time: 40, Compaction: true,
			})
			afterView := readView(t, srv)
			afterSnap := readSnapshot(t, dbPath)

			if tc.wantAccepted {
				if mixResponse.Code != http.StatusCreated {
					t.Fatalf("matching prepared mix: status=%d body=%s", mixResponse.Code, mixResponse.Body.String())
				}
				if fillResponse.Code != http.StatusCreated {
					t.Fatalf("fill with prepared generation and lease: status=%d body=%s", fillResponse.Code, fillResponse.Body.String())
				}
				if generated.ID == "" || afterView.Generation != generated.ID || len(afterView.Fills) != len(beforeView.Fills)+1 {
					t.Fatalf("accepted mix/fill view=%+v generation=%+v", afterView, generated)
				}
				if generationCount(t, afterSnap) != generationCount(t, beforeSnap)+1 || bytes.Equal(afterSnap.State, beforeSnap.State) {
					t.Fatalf("accepted mix did not persist exactly one new generation")
				}
				return
			}

			if mixResponse.Code >= 200 && mixResponse.Code < 300 {
				t.Errorf("unmatched mix unexpectedly accepted: status=%d body=%s", mixResponse.Code, mixResponse.Body.String())
			}
			if fillResponse.Code >= 200 && fillResponse.Code < 300 {
				t.Errorf("fill advanced using an unmatched mix: status=%d body=%s", fillResponse.Code, fillResponse.Body.String())
			}
			if !reflect.DeepEqual(afterView, beforeView) {
				t.Errorf("rejected mix/fill changed joint view: before=%+v after=%+v", beforeView, afterView)
			}
			if afterSnap.Digest != beforeSnap.Digest || !bytes.Equal(afterSnap.State, beforeSnap.State) ||
				generationCount(t, afterSnap) != generationCount(t, beforeSnap) {
				t.Errorf("rejected mix/fill changed persisted snapshot or generation registry")
			}
		})
	}
}
