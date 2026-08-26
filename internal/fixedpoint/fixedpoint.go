// Package fixedpoint implements overflow-checked signed fixed-point arithmetic
// with explicit decimal scales. It underpins every quality calculation in the
// UHPC wet-joint domain (water/cement ratio, fibre content, flow, shrinkage,
// compressive and bond strength). Rounding is half-away-from-zero and all
// operations reject divide-by-zero, scale mismatch, and signed 64-bit overflow.
package fixedpoint

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
)

// Sentinel errors for fixed-point operations. Each maps to a stable domain
// code at the HTTP API boundary.
var (
	ErrIllegalScale  = errors.New("fixedpoint: scale must be in [0, MaxScale]")
	ErrScaleMismatch = errors.New("fixedpoint: scale mismatch")
	ErrOverflow      = errors.New("fixedpoint: overflow")
	ErrDivideByZero  = errors.New("fixedpoint: divide by zero")
)

// MaxScale is the largest supported number of decimal places. 10^MaxScale fits
// comfortably in a signed 64-bit integer, leaving headroom for multiplication.
const MaxScale = 15

// Value is a signed fixed-point number stored as an integer raw value with an
// explicit decimal scale. The represented real number is raw / 10^scale.
type Value struct {
	raw   int64
	scale int
}

// New constructs a fixed-point value from a raw integer and a decimal scale.
// scale must lie in [0, MaxScale].
func New(raw int64, scale int) (Value, error) {
	if scale < 0 || scale > MaxScale {
		return Value{}, ErrIllegalScale
	}
	return Value{raw: raw, scale: scale}, nil
}

// NewInt constructs a value with scale 0.
func NewInt(v int64) Value { return Value{raw: v, scale: 0} }

// Must panics on error; intended for constants and tests.
func Must(v Value, err error) Value {
	if err != nil {
		panic(err)
	}
	return v
}

// Raw returns the underlying integer.
func (v Value) Raw() int64 { return v.raw }

// Scale returns the number of decimal places.
func (v Value) Scale() int { return v.scale }

// Sign reports -1, 0 or 1.
func (v Value) Sign() int {
	switch {
	case v.raw > 0:
		return 1
	case v.raw < 0:
		return -1
	default:
		return 0
	}
}

// IsZero reports whether the value is zero.
func (v Value) IsZero() bool { return v.raw == 0 }

// Cmp compares v and o, requiring equal scales.
func (v Value) Cmp(o Value) (int, error) {
	if v.scale != o.scale {
		return 0, ErrScaleMismatch
	}
	switch {
	case v.raw < o.raw:
		return -1, nil
	case v.raw > o.raw:
		return 1, nil
	default:
		return 0, nil
	}
}

// Add returns v+o, requiring equal scales and checking for overflow.
func (v Value) Add(o Value) (Value, error) {
	if v.scale != o.scale {
		return Value{}, ErrScaleMismatch
	}
	r, err := addInt64(v.raw, o.raw)
	if err != nil {
		return Value{}, err
	}
	return Value{raw: r, scale: v.scale}, nil
}

// Sub returns v-o, requiring equal scales and checking for overflow.
func (v Value) Sub(o Value) (Value, error) {
	if v.scale != o.scale {
		return Value{}, ErrScaleMismatch
	}
	r, err := subInt64(v.raw, o.raw)
	if err != nil {
		return Value{}, err
	}
	return Value{raw: r, scale: v.scale}, nil
}

// Mul returns v*o at the same scale, rounding half-away-from-zero.
func (v Value) Mul(o Value) (Value, error) {
	if v.scale != o.scale {
		return Value{}, ErrScaleMismatch
	}
	r, err := mulDivRound(v.raw, o.raw, pow10(v.scale))
	if err != nil {
		return Value{}, err
	}
	return Value{raw: r, scale: v.scale}, nil
}

// Div returns v/o at the same scale, rounding half-away-from-zero. Division by
// zero is rejected.
func (v Value) Div(o Value) (Value, error) {
	if v.scale != o.scale {
		return Value{}, ErrScaleMismatch
	}
	r, err := mulDivRound(v.raw, pow10(v.scale), o.raw)
	if err != nil {
		return Value{}, err
	}
	return Value{raw: r, scale: v.scale}, nil
}

// MulInt returns round(integer * value) as an int64 with overflow checking. It
// is used to translate a ratio (e.g. an allowable deviation) into an absolute
// integer-gram tolerance.
func (v Value) MulInt(integer int64) (int64, error) {
	return mulDivRound(integer, v.raw, pow10(v.scale))
}

// Rescale converts v to a new scale, rounding half-away-from-zero.
func (v Value) Rescale(scale int) (Value, error) {
	if scale < 0 || scale > MaxScale {
		return Value{}, ErrIllegalScale
	}
	if scale == v.scale {
		return v, nil
	}
	var r int64
	var err error
	if scale > v.scale {
		r, err = mulDivRound(v.raw, pow10(scale-v.scale), 1)
	} else {
		r, err = divRoundHalfAway(v.raw, pow10(v.scale-scale))
	}
	if err != nil {
		return Value{}, err
	}
	return Value{raw: r, scale: scale}, nil
}

func addInt64(a, b int64) (int64, error) {
	r := a + b
	if (b > 0 && r < a) || (b < 0 && r > a) {
		return 0, ErrOverflow
	}
	return r, nil
}

func subInt64(a, b int64) (int64, error) {
	r := a - b
	if (b < 0 && r < a) || (b > 0 && r > a) {
		return 0, ErrOverflow
	}
	return r, nil
}

func mulInt64(a, b int64) (int64, error) {
	if a == 0 || b == 0 {
		return 0, nil
	}
	if (a == math.MinInt64 && b == -1) || (b == math.MinInt64 && a == -1) {
		return 0, ErrOverflow
	}
	r := a * b
	if r/b != a {
		return 0, ErrOverflow
	}
	return r, nil
}

// mulDivRound computes round(num*mul/den) with checked multiplication and
// half-away-from-zero division.
func mulDivRound(num, mul, den int64) (int64, error) {
	p, err := mulInt64(num, mul)
	if err != nil {
		return 0, err
	}
	return divRoundHalfAway(p, den)
}

// divRoundHalfAway divides num by den and rounds half away from zero. It
// rejects division by zero and MinInt64/-1 overflow.
func divRoundHalfAway(num, den int64) (int64, error) {
	if den == 0 {
		return 0, ErrDivideByZero
	}
	if num == math.MinInt64 && den == -1 {
		return 0, ErrOverflow
	}
	q := num / den
	r := num % den
	if r == 0 {
		return q, nil
	}
	rMag := absU64(r)
	dMag := absU64(den)
	// Round half away from zero when 2*|r| >= |den|, i.e. |r| >= |den|-|r|.
	if rMag >= dMag-rMag {
		if (num > 0) == (den > 0) {
			q++
		} else {
			q--
		}
	}
	return q, nil
}

func absU64(v int64) uint64 {
	if v < 0 {
		return uint64(-(v + 1)) + 1
	}
	return uint64(v)
}

func pow10(n int) int64 {
	r := int64(1)
	for i := 0; i < n; i++ {
		r *= 10
	}
	return r
}

// MarshalJSON serializes a Value as {"raw":..., "scale":...} so snapshots can be
// persisted and recovered without losing precision.
func (v Value) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Raw   int64 `json:"raw"`
		Scale int   `json:"scale"`
	}{Raw: v.raw, Scale: v.scale})
}

// UnmarshalJSON restores a Value from its JSON form, rejecting an out-of-range
// scale.
func (v *Value) UnmarshalJSON(b []byte) error {
	var aux struct {
		Raw   int64 `json:"raw"`
		Scale int   `json:"scale"`
	}
	if err := json.Unmarshal(b, &aux); err != nil {
		return err
	}
	if aux.Scale < 0 || aux.Scale > MaxScale {
		return fmt.Errorf("fixedpoint: illegal scale %d", aux.Scale)
	}
	v.raw = aux.Raw
	v.scale = aux.Scale
	return nil
}
