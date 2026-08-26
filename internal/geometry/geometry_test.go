package geometry

import (
	"errors"
	"testing"

	"example.com/uhpc-wet-joint-traffic-release/internal/codes"
)

func hasCode(errs []ValidationError, c codes.Code) bool {
	for _, e := range errs {
		if e.Code == c {
			return true
		}
	}
	return false
}

func TestContinuousCoverageSuccess(t *testing.T) {
	d := Design{
		Range: Range{Start: 0, End: 999},
		Segments: []Segment{
			{Index: 0, Start: 0, End: 499},
			{Index: 1, Start: 500, End: 999},
		},
		Layers:    2,
		Direction: DirectionAscending,
	}
	if errs := d.Validate(); len(errs) != 0 {
		t.Fatalf("expected valid design, got %v", errs)
	}
}

func TestGapDetected(t *testing.T) {
	d := Design{
		Range: Range{Start: 0, End: 999},
		Segments: []Segment{
			{Index: 0, Start: 0, End: 499},
			{Index: 1, Start: 600, End: 999},
		},
	}
	if errs := d.Validate(); !hasCode(errs, codes.CodeGeometryGap) {
		t.Fatalf("expected GEOMETRY_GAP, got %v", errs)
	}
}

func TestOverlapDetected(t *testing.T) {
	d := Design{
		Range: Range{Start: 0, End: 999},
		Segments: []Segment{
			{Index: 0, Start: 0, End: 500},
			{Index: 1, Start: 500, End: 999},
		},
	}
	if errs := d.Validate(); !hasCode(errs, codes.CodeGeometryOverlap) {
		t.Fatalf("expected GEOMETRY_OVERLAP, got %v", errs)
	}
}

func TestDegenerateAndNegativeRejected(t *testing.T) {
	// Degenerate segment (zero length).
	d := Design{
		Range:    Range{Start: 0, End: 10},
		Segments: []Segment{{Index: 0, Start: 5, End: 5}},
	}
	if errs := d.Validate(); !hasCode(errs, codes.CodeGeometryDegenerate) {
		t.Fatalf("expected GEOMETRY_DEGENERATE, got %v", errs)
	}
	// Negative coordinate.
	dn := Design{
		Range:    Range{Start: -1, End: 10},
		Segments: []Segment{{Index: 0, Start: -1, End: 10}},
	}
	if errs := dn.Validate(); !hasCode(errs, codes.CodeGeometryNegative) {
		t.Fatalf("expected GEOMETRY_NEGATIVE, got %v", errs)
	}
}

func TestRebarShearKeyConflict(t *testing.T) {
	d := Design{
		Range: Range{Start: 0, End: 100},
		Segments: []Segment{
			{Index: 0, Start: 0, End: 100},
		},
		Rebar:     Rebar{Cover: 10},
		ShearKeys: []ShearKey{{Position: 5}}, // inside cover zone
	}
	if errs := d.Validate(); !hasCode(errs, codes.CodeRebarKeyConflict) {
		t.Fatalf("expected REBAR_KEY_CONFLICT, got %v", errs)
	}
}

func TestSortedErrorOrder(t *testing.T) {
	d := Design{
		Range: Range{Start: 0, End: 999},
		Segments: []Segment{
			{Index: 1, Start: 600, End: 999}, // creates both gap and later ordering
			{Index: 0, Start: 0, End: 400},
		},
		Rebar: Rebar{Cover: -1}, // negative rebar
	}
	errs := d.Validate()
	for i := 1; i < len(errs); i++ {
		if errs[i-1].Code > errs[i].Code {
			t.Fatalf("errors not sorted by code: %v", errs)
		}
	}
}

func TestConvertToMillimetersOverflow(t *testing.T) {
	// near int64 boundary: value * scale overflows.
	_, err := ConvertToMillimeters(1<<62, 8)
	if !errors.Is(err, ErrOverflow) {
		t.Fatalf("expected overflow, got %v", err)
	}
	if codes.CodeOf(err) != codes.CodeGeometryOverflow {
		t.Fatalf("expected GEOMETRY_OVERFLOW code, got %q", codes.CodeOf(err))
	}
}

func TestConvertToMillimetersOK(t *testing.T) {
	got, err := ConvertToMillimeters(125, 10) // 1250 mm
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 1250 {
		t.Fatalf("got %d, want 1250", got)
	}
}
