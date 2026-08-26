package engine

import (
	"testing"

	"example.com/uhpc-wet-joint-traffic-release/internal/codes"
	"example.com/uhpc-wet-joint-traffic-release/internal/domain"
)

func TestModel_CuringScheduleGatesRelease(t *testing.T) {
	tests := []struct {
		name        string
		samples     []domain.CuringEvidence
		wantErrors  []bool
		wantClosed  bool
		wantRelease bool
	}{
		{
			name: "qualified monotonic samples close the schedule and permit release",
			samples: []domain.CuringEvidence{
				{Temperature: fpT(20, 0), Humidity: fpT(90, 0), Duration: 30, Time: 80},
				{Temperature: fpT(21, 0), Humidity: fpT(91, 0), Duration: 30, Time: 81},
			},
			wantErrors:  []bool{false, false},
			wantClosed:  true,
			wantRelease: true,
		},
		{
			name: "temperature below locked minimum does not contribute duration",
			samples: []domain.CuringEvidence{
				{Temperature: fpT(199, 1), Humidity: fpT(90, 0), Duration: 30, Time: 80},
				{Temperature: fpT(20, 0), Humidity: fpT(90, 0), Duration: 30, Time: 81},
			},
			wantErrors:  []bool{true, false},
			wantClosed:  false,
			wantRelease: false,
		},
		{
			name: "humidity below locked minimum does not contribute duration",
			samples: []domain.CuringEvidence{
				{Temperature: fpT(20, 0), Humidity: fpT(899, 1), Duration: 30, Time: 80},
				{Temperature: fpT(20, 0), Humidity: fpT(90, 0), Duration: 30, Time: 81},
			},
			wantErrors:  []bool{true, false},
			wantClosed:  false,
			wantRelease: false,
		},
		{
			name: "zero temperature and humidity cannot close curing",
			samples: []domain.CuringEvidence{
				{Temperature: fpT(0, 0), Humidity: fpT(0, 0), Duration: 60, Time: 80},
			},
			wantErrors:  []bool{true},
			wantClosed:  false,
			wantRelease: false,
		},
		{
			name: "zero duration is rejected",
			samples: []domain.CuringEvidence{
				{Temperature: fpT(20, 0), Humidity: fpT(90, 0), Duration: 0, Time: 80},
			},
			wantErrors:  []bool{true},
			wantClosed:  false,
			wantRelease: false,
		},
		{
			name: "negative duration is rejected",
			samples: []domain.CuringEvidence{
				{Temperature: fpT(20, 0), Humidity: fpT(90, 0), Duration: -1, Time: 80},
			},
			wantErrors:  []bool{true},
			wantClosed:  false,
			wantRelease: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := NewInMemory()
			genID := readyToPour(t, e, 20, 1000)
			pourAll(t, e, genID)

			for i, sample := range tc.samples {
				err := e.RecordCuring("J1", sample)
				if (err != nil) != tc.wantErrors[i] {
					t.Fatalf("RecordCuring sample %d error = %v, want error=%v", i, err, tc.wantErrors[i])
				}
			}

			closed, err := e.CuringClosed("J1")
			if err != nil {
				t.Fatalf("CuringClosed: %v", err)
			}
			if closed != tc.wantClosed {
				t.Fatalf("CuringClosed = %v, want %v", closed, tc.wantClosed)
			}

			addInspections(t, e, genID)
			for _, reviewer := range []string{"eng-A", "eng-B"} {
				if err := e.SubmitReview("J1", domain.Review{Reviewer: reviewer, Qualified: true, Conclusion: "RELEASE"}); err != nil {
					t.Fatalf("SubmitReview(%s): %v", reviewer, err)
				}
			}

			verdict, err := e.Verdict("J1", domain.FinalVerdict{Type: domain.VerdictRelease})
			if tc.wantRelease {
				if err != nil {
					t.Fatalf("Verdict(RELEASE): %v", err)
				}
				if verdict.Credential == "" {
					t.Fatal("Verdict(RELEASE) returned an empty credential")
				}
				return
			}
			if codes.CodeOf(err) != codes.CodePreconditionsNotMet {
				t.Fatalf("Verdict(RELEASE) error = %v, want PRECONDITIONS_NOT_MET", err)
			}
			if verdict.Credential != "" {
				t.Fatalf("rejected RELEASE returned credential %q", verdict.Credential)
			}
		})
	}
}
