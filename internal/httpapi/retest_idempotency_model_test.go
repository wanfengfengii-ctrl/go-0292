package httpapi_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"example.com/uhpc-wet-joint-traffic-release/internal/domain"
	"example.com/uhpc-wet-joint-traffic-release/internal/engine"
	"example.com/uhpc-wet-joint-traffic-release/internal/fixedpoint"
	"example.com/uhpc-wet-joint-traffic-release/internal/geometry"
	"example.com/uhpc-wet-joint-traffic-release/internal/httpapi"
)

func TestModel_RetestSubmissionPreservesCompletedSet(t *testing.T) {
	cases := []struct {
		name             string
		anomalyKind      string
		wantSameSet      bool
		wantCurrentDone  bool
		wantReleaseReady bool
	}{
		{
			name:             "duplicate completed anomaly returns original terminal set",
			anomalyKind:      engine.KindStrength,
			wantSameSet:      true,
			wantCurrentDone:  true,
			wantReleaseReady: true,
		},
		{
			name:            "different anomaly creates an independent set",
			anomalyKind:     engine.KindPullOff,
			wantSameSet:     false,
			wantCurrentDone: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			value := func(raw int64, scale int) fixedpoint.Value {
				v, err := fixedpoint.New(raw, scale)
				if err != nil {
					t.Fatalf("fixedpoint.New(%d, %d): %v", raw, scale, err)
				}
				return v
			}
			must := func(label string, err error) {
				t.Helper()
				if err != nil {
					t.Fatalf("%s: %v", label, err)
				}
			}

			e := engine.NewInMemory()
			must("register recipe", e.RegisterRecipe(domain.RecipeRule{
				Name:            "UHPC-1",
				AllowDeviation:  value(2, 2),
				FlowMin:         value(180, 1),
				FlowMax:         value(260, 1),
				WorkWindow:      100,
				MinStrength:     value(50, 0),
				MinBondStrength: value(2, 0),
				MaxShrinkage:    value(500, 0),
			}))
			must("register span", e.RegisterSpan(domain.BridgeSpan{
				ID: "S1", CoordinateScale: 1000, AllowedRecipes: []string{"UHPC-1"}, RuleDigest: "v1",
			}))
			_, err := e.Lock("J1", domain.JointDesign{
				JointNumber: "J1", SpanID: "S1", Recipe: "UHPC-1", LockVersion: 1,
				Geometry: geometry.Design{
					Range: geometry.Range{Start: 0, End: 999},
					Segments: []geometry.Segment{
						{Index: 0, Start: 0, End: 499},
						{Index: 1, Start: 500, End: 999},
					},
					Layers: 1, Direction: geometry.DirectionAscending,
				},
				SurfaceZones: []domain.SurfaceZone{{ID: "Z1", Required: true}},
				MixPlans: []domain.MixPlan{{
					Batch: "B1", Sequence: 0, Powder: 100000, Water: 20000, Admixture: 1000, Fiber: 5000,
				}},
				Curing: domain.CuringSchedule{
					DurationMinutes: 60, MinTemperature: value(20, 0), MinHumidity: value(90, 0),
				},
				Adjacency:    [][]int{{1}, {0}},
				SegmentZones: map[int][]string{0: {"Z1"}, 1: {"Z1"}},
			})
			must("lock joint", err)

			for category, grams := range map[domain.MaterialCategory]int64{
				domain.MaterialPowder: 100000, domain.MaterialWater: 20000,
				domain.MaterialAdmixture: 1000, domain.MaterialSteelFiber: 5000,
			} {
				must("stock "+string(category), e.Stock(category, "B1", grams))
			}
			must("record surface", e.RecordSurfaceEvidence("J1", domain.SurfaceEvidence{
				ZoneID: "Z1", Clean: true, PreWet: true, Time: 5,
			}))
			req := domain.MaterialRequest{
				Batch: "B1",
				Grams: map[domain.MaterialCategory]int64{
					domain.MaterialPowder: 100000, domain.MaterialWater: 20000,
					domain.MaterialAdmixture: 1000, domain.MaterialSteelFiber: 5000,
				},
				Leases: []domain.LeaseRequest{{
					Category: engine.PourEquipment, ResourceID: "POUR-1", Holder: "crew", Purpose: "place", Deadline: 1000,
				}},
			}
			raw, err := json.Marshal(req)
			must("marshal material request", err)
			digest := sha256.Sum256(raw)
			_, err = e.Prepare(domain.OperationRecord{OperationID: "prep-1", Digest: hex.EncodeToString(digest[:])}, req)
			must("prepare material", err)
			must("record flow", e.RecordFlow("J1", domain.FlowTest{Value: value(200, 1), Passed: true, Time: 10}))
			materialGeneration, err := e.RecordMix("J1", domain.MixRun{
				JointNumber: "J1", Batch: "B1", Sequence: 0, Powder: 100000, Water: 20000,
				Admixture: 1000, Fiber: 5000, Time: 20,
			})
			must("record mix", err)
			for segment := 0; segment < 2; segment++ {
				_, err = e.AppendFill("J1", domain.FillCell{
					Segment: segment, Layer: 0, MixBatch: "B1", Generation: materialGeneration.ID,
					Time: domain.LogicalTime(30 + segment), Compaction: true,
				})
				must("append fill", err)
			}
			must("record curing", e.RecordCuring("J1", domain.CuringEvidence{
				Temperature: value(20, 0), Humidity: value(90, 0), Duration: 60, Time: 40,
			}))

			originalAnomaly := domain.Anomaly{
				JointNumber: "J1", Kind: engine.KindStrength, Segment: 0, Generation: materialGeneration.ID,
				Evidence: domain.InspectionEvidence{
					Kind: engine.KindStrength, Segment: 0, Generation: materialGeneration.ID,
					Reading: value(40, 0), Passed: false, Time: 41,
				},
			}
			originalSet, err := e.Retest(originalAnomaly)
			must("create retest", err)
			if !reflect.DeepEqual(originalSet.Segments, []int{0, 1}) {
				t.Fatalf("initial ordered retest segments = %v, want [0 1]", originalSet.Segments)
			}
			originalRemediation, err := e.ActivateGeneration(originalSet.ID)
			must("activate remediation", err)
			for segment := 0; segment < 2; segment++ {
				must("record remediation strength", e.RecordInspection("J1", domain.InspectionEvidence{
					Kind: engine.KindStrength, Segment: segment, Generation: originalRemediation.ID,
					Reading: value(60, 0), Passed: true, Time: domain.LogicalTime(50 + segment),
				}))
			}
			must("record remediation pull-off", e.RecordInspection("J1", domain.InspectionEvidence{
				Kind: engine.KindPullOff, Segment: 0, Generation: originalRemediation.ID,
				Reading: value(3, 0), Passed: true, Time: 52,
			}))
			completedSet, completedRemediation, hasRemediation, err := e.GetRetest(originalSet.ID)
			must("read completed retest", err)
			if !completedSet.Done || !hasRemediation || !completedRemediation.Complete {
				t.Fatalf("retest was not completed before resubmission: set=%+v remediation=%+v", completedSet, completedRemediation)
			}
			beforeJoint, err := e.GetJoint("J1")
			must("read joint before resubmission", err)

			srv := httpapi.NewWithEngine(e)
			postAnomaly := originalAnomaly
			postAnomaly.Kind = tc.anomalyKind
			postAnomaly.Evidence.Kind = tc.anomalyKind
			body, err := json.Marshal(postAnomaly)
			must("marshal anomaly", err)
			postReq := httptest.NewRequest(http.MethodPost, "/v1/joints/J1/retests", bytes.NewReader(body))
			postReq.Header.Set("Content-Type", "application/json")
			postResponse := httptest.NewRecorder()
			srv.ServeHTTP(postResponse, postReq)
			if postResponse.Code != http.StatusCreated {
				t.Fatalf("POST duplicate retest status = %d, body=%s", postResponse.Code, postResponse.Body.String())
			}
			var submittedSet domain.RetestSet
			must("decode submitted retest", json.Unmarshal(postResponse.Body.Bytes(), &submittedSet))

			if tc.wantSameSet {
				if !reflect.DeepEqual(submittedSet, completedSet) {
					t.Fatalf("duplicate POST returned %+v, want original completed set %+v", submittedSet, completedSet)
				}
			} else if submittedSet.ID == completedSet.ID {
				t.Fatalf("different anomaly reused retest ID %q", submittedSet.ID)
			}

			getResponse := httptest.NewRecorder()
			srv.ServeHTTP(getResponse, httptest.NewRequest(http.MethodGet, "/v1/retests/"+submittedSet.ID, nil))
			if getResponse.Code != http.StatusOK {
				t.Fatalf("GET retest status = %d, body=%s", getResponse.Code, getResponse.Body.String())
			}
			var got struct {
				Retest      domain.RetestSet              `json:"retest"`
				Remediation *domain.RemediationGeneration `json:"remediation"`
			}
			must("decode GET retest", json.Unmarshal(getResponse.Body.Bytes(), &got))
			if got.Retest.Done != tc.wantCurrentDone {
				t.Fatalf("GET retest done = %v, want %v", got.Retest.Done, tc.wantCurrentDone)
			}

			preservedSet, preservedRemediation, preservedHasRemediation, err := e.GetRetest(originalSet.ID)
			must("read original retest after resubmission", err)
			if !reflect.DeepEqual(preservedSet, completedSet) || !preservedHasRemediation || !reflect.DeepEqual(preservedRemediation, completedRemediation) {
				t.Fatalf("original completed lineage changed: set=%+v remediation=%+v", preservedSet, preservedRemediation)
			}
			afterJoint, err := e.GetJoint("J1")
			must("read joint after resubmission", err)
			if tc.wantSameSet && (afterJoint.RetestID != beforeJoint.RetestID || afterJoint.Generation != beforeJoint.Generation) {
				t.Fatalf("duplicate changed active lineage: before retest=%q generation=%q, after retest=%q generation=%q",
					beforeJoint.RetestID, beforeJoint.Generation, afterJoint.RetestID, afterJoint.Generation)
			}

			if tc.wantReleaseReady {
				must("submit first review", e.SubmitReview("J1", domain.Review{Reviewer: "eng-A", Qualified: true, Conclusion: "RELEASE"}))
				must("submit second review", e.SubmitReview("J1", domain.Review{Reviewer: "eng-B", Qualified: true, Conclusion: "RELEASE"}))
				verdict, err := e.Verdict("J1", domain.FinalVerdict{Type: domain.VerdictRelease})
				must("release after duplicate retest", err)
				if verdict.Credential == "" {
					t.Fatal("release after duplicate retest returned no credential")
				}
			}
		})
	}
}
