package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"net/http"
	"path/filepath"
	"reflect"
	"testing"

	"example.com/uhpc-wet-joint-traffic-release/internal/codes"
	"example.com/uhpc-wet-joint-traffic-release/internal/domain"
	"example.com/uhpc-wet-joint-traffic-release/internal/engine"
	"example.com/uhpc-wet-joint-traffic-release/internal/geometry"
	"example.com/uhpc-wet-joint-traffic-release/internal/store"
)

func TestModel_FlowTestTransaction(t *testing.T) {
	cases := []struct {
		name          string
		flow          domain.FlowTest
		flowStatus    int
		fillStatus    int
		fillCode      codes.Code
		snapshotMoves bool
	}{
		{
			name:       "reported failure does not authorize filling",
			flow:       domain.FlowTest{Value: fpv(200, 1), Passed: false, Time: 30},
			flowStatus: http.StatusUnprocessableEntity,
			fillStatus: http.StatusUnprocessableEntity,
			fillCode:   codes.CodeFlowMissing,
		},
		{
			name:       "out of recipe window rolls back",
			flow:       domain.FlowTest{Value: fpv(170, 1), Passed: true, Time: 31},
			flowStatus: http.StatusUnprocessableEntity,
			fillStatus: http.StatusUnprocessableEntity,
			fillCode:   codes.CodeFlowMissing,
		},
		{
			name:       "fixed point rescale failure rolls back",
			flow:       domain.FlowTest{Value: fpv(math.MaxInt64, 0), Passed: true, Time: 32},
			flowStatus: http.StatusUnprocessableEntity,
			fillStatus: http.StatusUnprocessableEntity,
			fillCode:   codes.CodeFlowMissing,
		},
		{
			name:          "passing in-window result authorizes filling",
			flow:          domain.FlowTest{Value: fpv(200, 1), Passed: true, Time: 30},
			flowStatus:    http.StatusCreated,
			fillStatus:    http.StatusCreated,
			snapshotMoves: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state.db")
			eng := engine.New(path)
			rule := domain.RecipeRule{
				Name: "UHPC-1", AllowDeviation: fpv(2, 2), FlowMin: fpv(180, 1), FlowMax: fpv(260, 1),
				WorkWindow: 100, MinStrength: fpv(50, 0), MinBondStrength: fpv(2, 0), MaxShrinkage: fpv(500, 0),
			}
			if err := eng.RegisterRecipe(rule); err != nil {
				t.Fatalf("RegisterRecipe: %v", err)
			}
			if err := eng.RegisterSpan(domain.BridgeSpan{
				ID: "S1", CoordinateScale: 1000, AllowedRecipes: []string{"UHPC-1"}, RuleDigest: "v1",
			}); err != nil {
				t.Fatalf("RegisterSpan: %v", err)
			}
			design := domain.JointDesign{
				JointNumber: "J1", SpanID: "S1", Recipe: "UHPC-1", LockVersion: 1,
				Geometry: geometry.Design{
					Range: geometry.Range{Start: 0, End: 999},
					Segments: []geometry.Segment{
						{Index: 0, Start: 0, End: 499},
						{Index: 1, Start: 500, End: 999},
					},
					Layers: 2, Direction: geometry.DirectionAscending, Rebar: geometry.Rebar{Cover: 50},
				},
				SurfaceZones: []domain.SurfaceZone{{ID: "Z1", Required: true}},
				MixPlans: []domain.MixPlan{{
					Batch: "B1", Sequence: 0, Powder: 100000, Water: 20000, Admixture: 1000, Fiber: 5000,
				}},
				Curing: domain.CuringSchedule{
					DurationMinutes: 60, MinTemperature: fpv(20, 0), MinHumidity: fpv(90, 0),
				},
				Adjacency: [][]int{{1}, {0}}, SegmentZones: map[int][]string{0: {"Z1"}, 1: {"Z1"}},
			}
			if _, err := eng.Lock("J1", design); err != nil {
				t.Fatalf("Lock: %v", err)
			}
			for category, grams := range map[domain.MaterialCategory]int64{
				domain.MaterialPowder: 100000, domain.MaterialWater: 20000,
				domain.MaterialAdmixture: 1000, domain.MaterialSteelFiber: 5000,
			} {
				if err := eng.Stock(category, "B1", grams); err != nil {
					t.Fatalf("Stock(%s): %v", category, err)
				}
			}
			if err := eng.RecordSurfaceEvidence("J1", domain.SurfaceEvidence{
				ZoneID: "Z1", Clean: true, PreWet: true, Time: 5,
			}); err != nil {
				t.Fatalf("RecordSurfaceEvidence: %v", err)
			}
			request := domain.MaterialRequest{
				Batch: "B1",
				Grams: map[domain.MaterialCategory]int64{
					domain.MaterialPowder: 100000, domain.MaterialWater: 20000,
					domain.MaterialAdmixture: 1000, domain.MaterialSteelFiber: 5000,
				},
				Leases: []domain.LeaseRequest{{
					Category: engine.PourEquipment, ResourceID: "POUR-1", Holder: "crew-A", Purpose: "place", Deadline: 1000,
				}},
			}
			rawRequest, err := json.Marshal(request)
			if err != nil {
				t.Fatalf("marshal material request: %v", err)
			}
			digest := sha256.Sum256(rawRequest)
			if _, err := eng.Prepare(domain.OperationRecord{
				OperationID: "prep-1", Digest: hex.EncodeToString(digest[:]),
			}, request); err != nil {
				t.Fatalf("Prepare: %v", err)
			}
			generation, err := eng.RecordMix("J1", domain.MixRun{
				JointNumber: "J1", Batch: "B1", Sequence: 0,
				Powder: 100000, Water: 20000, Admixture: 1000, Fiber: 5000, Time: 20,
			})
			if err != nil {
				t.Fatalf("RecordMix: %v", err)
			}

			audit := store.NewStore(path)
			before, err := audit.Recover()
			if err != nil {
				t.Fatalf("recover snapshot before flow test: %v", err)
			}
			if err := audit.Close(); err != nil {
				t.Fatalf("close audit store: %v", err)
			}

			srv := NewWithEngine(eng)
			flowResponse := do(t, srv, http.MethodPost, "/v1/joints/J1/flow-tests", tc.flow)
			if flowResponse.Code != tc.flowStatus {
				t.Fatalf("flow status = %d body %s, want %d", flowResponse.Code, flowResponse.Body.String(), tc.flowStatus)
			}
			if tc.flowStatus != http.StatusCreated {
				var body struct {
					Code codes.Code `json:"code"`
				}
				if err := json.Unmarshal(flowResponse.Body.Bytes(), &body); err != nil {
					t.Fatalf("decode flow error: %v", err)
				}
				if body.Code != codes.CodeFlowFailed {
					t.Fatalf("flow code = %q, want %q", body.Code, codes.CodeFlowFailed)
				}
			}

			audit = store.NewStore(path)
			after, err := audit.Recover()
			if err != nil {
				t.Fatalf("recover snapshot after flow test: %v", err)
			}
			if err := audit.Close(); err != nil {
				t.Fatalf("close audit store: %v", err)
			}
			moved := !reflect.DeepEqual(before, after)
			if moved != tc.snapshotMoves {
				t.Fatalf("durable snapshot changed = %v, want %v", moved, tc.snapshotMoves)
			}

			fillResponse := do(t, srv, http.MethodPost, "/v1/joints/J1/fills", domain.FillCell{
				Segment: 0, Layer: 0, MixBatch: "B1", Generation: generation.ID, Time: 40, Compaction: true,
			})
			if fillResponse.Code != tc.fillStatus {
				t.Fatalf("fill status = %d body %s, want %d", fillResponse.Code, fillResponse.Body.String(), tc.fillStatus)
			}
			if tc.fillCode != "" {
				var body struct {
					Code codes.Code `json:"code"`
				}
				if err := json.Unmarshal(fillResponse.Body.Bytes(), &body); err != nil {
					t.Fatalf("decode fill error: %v", err)
				}
				if body.Code != tc.fillCode {
					t.Fatalf("fill code = %q, want %q", body.Code, tc.fillCode)
				}
			}

			viewResponse := do(t, srv, http.MethodGet, "/v1/joints/J1", nil)
			if viewResponse.Code != http.StatusOK {
				t.Fatalf("joint view status = %d body %s", viewResponse.Code, viewResponse.Body.String())
			}
			var view struct {
				Fills  []domain.FillCell `json:"fills"`
				Prefix domain.PourPrefix `json:"prefix"`
			}
			if err := json.Unmarshal(viewResponse.Body.Bytes(), &view); err != nil {
				t.Fatalf("decode joint view: %v", err)
			}
			wantFills := 0
			wantPrefix := domain.PourPrefix{Segment: 0, Layer: 0}
			if tc.fillStatus == http.StatusCreated {
				wantFills = 1
				wantPrefix = domain.PourPrefix{Segment: 1, Layer: 0}
			}
			if len(view.Fills) != wantFills || view.Prefix != wantPrefix {
				t.Fatalf("joint after fill has %d fills and prefix %+v, want %d and %+v", len(view.Fills), view.Prefix, wantFills, wantPrefix)
			}
		})
	}
}
