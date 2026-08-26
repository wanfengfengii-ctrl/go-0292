package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"example.com/uhpc-wet-joint-traffic-release/internal/domain"
	"example.com/uhpc-wet-joint-traffic-release/internal/fixedpoint"
	"example.com/uhpc-wet-joint-traffic-release/internal/geometry"
)

func do(t *testing.T, srv *Server, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		r = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, r)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	return rr
}

func fpv(raw int64, scale int) fixedpoint.Value {
	v, _ := fixedpoint.New(raw, scale)
	return v
}

func TestHTTPFullReleaseFlow(t *testing.T) {
	srv := New()

	// Register recipe then span.
	if rr := do(t, srv, "POST", "/v1/recipes", domain.RecipeRule{
		Name: "UHPC-1", AllowDeviation: fpv(2, 2), FlowMin: fpv(180, 1), FlowMax: fpv(260, 1),
		WorkWindow: 100, MinStrength: fpv(50, 0), MinBondStrength: fpv(2, 0), MaxShrinkage: fpv(500, 0),
	}); rr.Code != http.StatusCreated {
		t.Fatalf("recipe: got %d", rr.Code)
	}
	if rr := do(t, srv, "POST", "/v1/spans", domain.BridgeSpan{
		ID: "S1", CoordinateScale: 1000, AllowedRecipes: []string{"UHPC-1"}, RuleDigest: "v1",
	}); rr.Code != http.StatusCreated {
		t.Fatalf("span: got %d", rr.Code)
	}
	if rr := do(t, srv, "POST", "/v1/materials", map[string]any{"category": "POWDER", "batch": "B1", "grams": 100000}); rr.Code != http.StatusCreated {
		t.Fatalf("stock powder: got %d", rr.Code)
	}
	for _, m := range []map[string]any{
		{"category": "WATER", "batch": "B1", "grams": 20000},
		{"category": "ADMIXTURE", "batch": "B1", "grams": 1000},
		{"category": "STEEL_FIBER", "batch": "B1", "grams": 5000},
	} {
		if rr := do(t, srv, "POST", "/v1/materials", m); rr.Code != http.StatusCreated {
			t.Fatalf("stock %v: got %d", m, rr.Code)
		}
	}

	// Create + lock joint.
	design := domain.JointDesign{
		JointNumber: "J1", SpanID: "S1", Recipe: "UHPC-1", LockVersion: 1,
		Geometry: geometry.Design{
			Range:    geometry.Range{Start: 0, End: 999},
			Segments: []geometry.Segment{{Index: 0, Start: 0, End: 499}, {Index: 1, Start: 500, End: 999}},
			Layers:   2, Direction: geometry.DirectionAscending, Rebar: geometry.Rebar{Cover: 50},
		},
		SurfaceZones: []domain.SurfaceZone{{ID: "Z1", Required: true}},
		MixPlans:     []domain.MixPlan{{Batch: "B1", Sequence: 0, Powder: 100000, Water: 20000, Admixture: 1000, Fiber: 5000}},
		Curing:       domain.CuringSchedule{DurationMinutes: 60, MinTemperature: fpv(20, 0), MinHumidity: fpv(90, 0)},
		Adjacency:    [][]int{{1}, {0}},
		SegmentZones: map[int][]string{0: {"Z1"}, 1: {"Z1"}},
	}
	if rr := do(t, srv, "POST", "/v1/joints", design); rr.Code != http.StatusCreated {
		t.Fatalf("create joint: got %d body %s", rr.Code, rr.Body.String())
	}

	// Surface evidence.
	if rr := do(t, srv, "POST", "/v1/joints/J1/surface-evidence", domain.SurfaceEvidence{ZoneID: "Z1", Clean: true, PreWet: true, Time: 5}); rr.Code != http.StatusCreated {
		t.Fatalf("surface: got %d", rr.Code)
	}

	// Material preparation with lease.
	prep := map[string]any{
		"operation_id": "prep-1",
		"batch":        "B1",
		"grams":        map[string]int64{"POWDER": 100000, "WATER": 20000, "ADMIXTURE": 1000, "STEEL_FIBER": 5000},
		"leases":       []map[string]any{{"category": "POUR", "resource_id": "POUR-1", "holder": "crew-A", "purpose": "place", "deadline": 1000}},
	}
	if rr := do(t, srv, "POST", "/v1/joints/J1/material-preparations", prep); rr.Code != http.StatusCreated {
		t.Fatalf("prepare: got %d body %s", rr.Code, rr.Body.String())
	}

	// Mix + flow.
	mix := domain.MixRun{JointNumber: "J1", Batch: "B1", Sequence: 0, Powder: 100000, Water: 20000, Admixture: 1000, Fiber: 5000, Time: 20}
	if rr := do(t, srv, "POST", "/v1/joints/J1/mix-runs", mix); rr.Code != http.StatusCreated {
		t.Fatalf("mix: got %d body %s", rr.Code, rr.Body.String())
	}
	if rr := do(t, srv, "POST", "/v1/joints/J1/flow-tests", domain.FlowTest{Value: fpv(200, 1), Passed: true, Time: 30}); rr.Code != http.StatusCreated {
		t.Fatalf("flow: got %d", rr.Code)
	}

	// Fill all four cells.
	var view struct {
		Generation string `json:"generation"`
	}
	if err := json.Unmarshal(do(t, srv, "GET", "/v1/joints/J1", nil).Body.Bytes(), &view); err != nil {
		t.Fatalf("read generation: %v", err)
	}
	genID := view.Generation
	for i, tt := range []int{40, 50, 60, 70} {
		seg, layer := i%2, i/2
		cell := domain.FillCell{Segment: seg, Layer: layer, MixBatch: "B1", Generation: genID, Time: domain.LogicalTime(tt), Compaction: true}
		if rr := do(t, srv, "POST", "/v1/joints/J1/fills", cell); rr.Code != http.StatusCreated {
			t.Fatalf("fill %d: got %d body %s", i, rr.Code, rr.Body.String())
		}
	}

	// Curing, inspections, reviews, verdict.
	if rr := do(t, srv, "POST", "/v1/joints/J1/curing-samples", domain.CuringEvidence{Temperature: fpv(20, 0), Humidity: fpv(90, 0), Duration: 60, Time: 80}); rr.Code != http.StatusCreated {
		t.Fatalf("curing: got %d", rr.Code)
	}
	for _, ev := range []domain.InspectionEvidence{
		{Kind: "STRENGTH", Segment: 0, Generation: genID, Reading: fpv(60, 0), Passed: true, Time: 90},
		{Kind: "PULL_OFF", Segment: 0, Generation: genID, Reading: fpv(3, 0), Passed: true, Time: 91},
	} {
		if rr := do(t, srv, "POST", "/v1/joints/J1/inspections", ev); rr.Code != http.StatusCreated {
			t.Fatalf("inspection: got %d", rr.Code)
		}
	}
	for _, r := range []string{"eng-A", "eng-B"} {
		if rr := do(t, srv, "POST", "/v1/joints/J1/reviews", domain.Review{Reviewer: r, Qualified: true, Conclusion: "RELEASE"}); rr.Code != http.StatusCreated {
			t.Fatalf("review %s: got %d", r, rr.Code)
		}
	}
	rr := do(t, srv, "POST", "/v1/joints/J1/verdicts", domain.FinalVerdict{Type: domain.VerdictRelease})
	if rr.Code != http.StatusCreated {
		t.Fatalf("verdict: got %d body %s", rr.Code, rr.Body.String())
	}
	var verdict domain.FinalVerdict
	if err := json.Unmarshal(rr.Body.Bytes(), &verdict); err != nil {
		t.Fatalf("unmarshal verdict: %v", err)
	}
	if verdict.Credential == "" {
		t.Fatalf("verdict has no credential")
	}
}
