// Package geometry models the integer-millimetre design geometry of a UHPC wet
// joint: the closed slot range, longitudinal segments, pour layers, rebar cover
// and lap, and shear-key positions. It validates continuous, non-overlapping,
// non-degenerate segment coverage, rejects negative dimensions and rebar/shear
// key conflicts, and checks signed 64-bit overflow on coordinate scale
// conversion.
package geometry

import (
	"fmt"
	"sort"

	"example.com/uhpc-wet-joint-traffic-release/internal/codes"
)

// Direction is the locked pour direction across a joint.
type Direction string

const (
	DirectionAscending  Direction = "ASCENDING"  // 由小里程向大里程推进
	DirectionDescending Direction = "DESCENDING" // 由大里程向小里程推进
)

// Range is a closed interval of integer millimetres [Start, End]; End inclusive.
type Range struct {
	Start int64 `json:"start"`
	End   int64 `json:"end"`
}

// Segment is a longitudinal pour segment spanning a closed integer-millimetre
// interval [Start, End]; End inclusive.
type Segment struct {
	Index int   `json:"index"`
	Start int64 `json:"start"`
	End   int64 `json:"end"`
}

// Rebar describes rebar cover and lap requirements in integer millimetres.
type Rebar struct {
	Cover int64 `json:"cover"` // 保护层厚度
	Lap   int64 `json:"lap"`   // 搭接长度
}

// ShearKey is a positioned shear connector in integer millimetres.
type ShearKey struct {
	Position int64 `json:"position"`
}

// Design is the geometric description of a wet joint prior to locking.
type Design struct {
	Range     Range      `json:"range"`
	Segments  []Segment  `json:"segments"`
	Layers    int        `json:"layers"`
	Direction Direction  `json:"direction"`
	Rebar     Rebar      `json:"rebar"`
	ShearKeys []ShearKey `json:"shear_keys"`
}

// ValidationError is a single deterministic geometry failure.
type ValidationError struct {
	Code    codes.Code
	Message string
}

func (e ValidationError) Error() string { return string(e.Code) + ": " + e.Message }

// ErrOverflow is returned by ConvertToMillimeters when a coordinate scale
// conversion overflows signed 64-bit arithmetic.
var ErrOverflow = codes.New(codes.CodeGeometryOverflow, "coordinate scale conversion overflow")

// ConvertToMillimeters scales an engineering-coordinate value by scale to
// integer millimetres, checking for signed 64-bit overflow. scale must be
// non-negative.
func ConvertToMillimeters(value, scale int64) (int64, error) {
	if scale < 0 {
		return 0, codes.New(codes.CodeGeometryNegative, "coordinate scale must be non-negative")
	}
	if value == 0 || scale == 0 {
		return 0, nil
	}
	r := value * scale
	if r/scale != value {
		return 0, ErrOverflow
	}
	return r, nil
}

// Validate checks the design for continuous, non-overlapping, non-degenerate
// segment coverage, non-negative dimensions, and rebar/shear-key conflicts. It
// returns errors deterministically sorted by code then message.
func (d Design) Validate() []ValidationError {
	var errs []ValidationError

	// Design range.
	if d.Range.Start < 0 || d.Range.End < 0 {
		errs = append(errs, ValidationError{codes.CodeGeometryNegative, "range coordinates must be non-negative"})
	} else if d.Range.End <= d.Range.Start {
		errs = append(errs, ValidationError{codes.CodeGeometryDegenerate, "range must have positive length"})
	}

	// Rebar cover and lap.
	if d.Rebar.Cover < 0 || d.Rebar.Lap < 0 {
		errs = append(errs, ValidationError{codes.CodeGeometryNegative, "rebar cover and lap must be non-negative"})
	}

	// Segments: per-segment validity first, then coverage.
	segs := append([]Segment(nil), d.Segments...)
	sort.SliceStable(segs, func(i, j int) bool {
		if segs[i].Start != segs[j].Start {
			return segs[i].Start < segs[j].Start
		}
		if segs[i].End != segs[j].End {
			return segs[i].End < segs[j].End
		}
		return segs[i].Index < segs[j].Index
	})
	for _, s := range segs {
		if s.Start < 0 || s.End < 0 {
			errs = append(errs, ValidationError{codes.CodeGeometryNegative, fmt.Sprintf("segment %d coordinates must be non-negative", s.Index)})
		} else if s.End <= s.Start {
			errs = append(errs, ValidationError{codes.CodeGeometryDegenerate, fmt.Sprintf("segment %d must have positive length", s.Index)})
		}
	}

	if len(segs) == 0 {
		errs = append(errs, ValidationError{codes.CodeGeometryGap, "no segments defined"})
	} else {
		if segs[0].Start != d.Range.Start {
			errs = append(errs, ValidationError{codes.CodeGeometryGap, "segments do not start at range start"})
		}
		if segs[len(segs)-1].End != d.Range.End {
			errs = append(errs, ValidationError{codes.CodeGeometryGap, "segments do not end at range end"})
		}
		for i := 1; i < len(segs); i++ {
			prev, cur := segs[i-1], segs[i]
			if cur.Start <= prev.End {
				errs = append(errs, ValidationError{codes.CodeGeometryOverlap, fmt.Sprintf("segments %d and %d overlap", prev.Index, cur.Index)})
			} else if cur.Start > prev.End+1 {
				errs = append(errs, ValidationError{codes.CodeGeometryGap, fmt.Sprintf("gap between segments %d and %d", prev.Index, cur.Index)})
			}
		}
	}

	// Shear keys: position must lie inside the range, clear the rebar cover
	// zone, and be unique.
	seen := make(map[int64]bool)
	for _, k := range d.ShearKeys {
		if k.Position < d.Range.Start || k.Position > d.Range.End {
			errs = append(errs, ValidationError{codes.CodeRebarKeyConflict, "shear key outside joint range"})
			continue
		}
		if d.Rebar.Cover > 0 {
			if k.Position < d.Range.Start+d.Rebar.Cover || k.Position > d.Range.End-d.Rebar.Cover {
				errs = append(errs, ValidationError{codes.CodeRebarKeyConflict, "shear key conflicts with rebar cover zone"})
			}
		}
		if seen[k.Position] {
			errs = append(errs, ValidationError{codes.CodeRebarKeyConflict, "duplicate shear key position"})
		}
		seen[k.Position] = true
	}

	sort.Slice(errs, func(i, j int) bool {
		if errs[i].Code != errs[j].Code {
			return errs[i].Code < errs[j].Code
		}
		return errs[i].Message < errs[j].Message
	})
	return errs
}
