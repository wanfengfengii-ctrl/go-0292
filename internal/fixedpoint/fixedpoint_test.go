package fixedpoint

import (
	"errors"
	"testing"
)

func mustValue(v Value, err error) Value {
	if err != nil {
		panic("unexpected error: " + err.Error())
	}
	return v
}

func TestAddSub(t *testing.T) {
	a := NewInt(1000)
	b := NewInt(250)
	if got := mustValue(a.Add(b)); got.Raw() != 1250 {
		t.Fatalf("Add: got %d, want 1250", got.Raw())
	}
	if got := mustValue(a.Sub(b)); got.Raw() != 750 {
		t.Fatalf("Sub: got %d, want 750", got.Raw())
	}
}

func TestMulRoundHalfAway(t *testing.T) {
	// 2.5 * 2.0 = 5.0 at scale 1
	a := mustValue(New(25, 1))
	b := mustValue(New(20, 1))
	got := mustValue(a.Mul(b))
	if got.Raw() != 50 || got.Scale() != 1 {
		t.Fatalf("Mul: got raw=%d scale=%d, want 50 scale 1", got.Raw(), got.Scale())
	}
}

func TestDivRoundHalfAway(t *testing.T) {
	// 1.0 / 2.0 = 0.5 at scale 1 -> exact
	a := mustValue(New(10, 1))
	b := mustValue(New(20, 1))
	if got := mustValue(a.Div(b)); got.Raw() != 5 {
		t.Fatalf("Div: got %d, want 5", got.Raw())
	}
	// 1 / 3 at scale 0 rounds half away from zero -> 0
	if got := mustValue(NewInt(1).Div(NewInt(3))); got.Raw() != 0 {
		t.Fatalf("Div 1/3: got %d, want 0", got.Raw())
	}
	// 2 / 3 at scale 0 -> 1 (0.667 rounds away)
	if got := mustValue(NewInt(2).Div(NewInt(3))); got.Raw() != 1 {
		t.Fatalf("Div 2/3: got %d, want 1", got.Raw())
	}
	// -1 / 2 at scale 0 -> -1 (half away from zero)
	if got := mustValue(NewInt(-1).Div(NewInt(2))); got.Raw() != -1 {
		t.Fatalf("Div -1/2: got %d, want -1", got.Raw())
	}
}

func TestRescaleRoundHalfAway(t *testing.T) {
	// 0.06 at scale 2 rescaled to scale 1 -> 0.1 (raw 1)
	v := mustValue(New(6, 2))
	got := mustValue(v.Rescale(1))
	if got.Raw() != 1 || got.Scale() != 1 {
		t.Fatalf("Rescale: got raw=%d scale=%d, want 1 scale 1", got.Raw(), got.Scale())
	}
	// negative half away: -0.06 -> -0.1
	vn := mustValue(New(-6, 2))
	gotn := mustValue(vn.Rescale(1))
	if gotn.Raw() != -1 {
		t.Fatalf("Rescale negative: got %d, want -1", gotn.Raw())
	}
}

func TestDivideByZero(t *testing.T) {
	if _, err := NewInt(1).Div(NewInt(0)); !errors.Is(err, ErrDivideByZero) {
		t.Fatalf("expected ErrDivideByZero, got %v", err)
	}
}

func TestScaleMismatch(t *testing.T) {
	a := NewInt(1)
	b := mustValue(New(1, 1))
	if _, err := a.Add(b); !errors.Is(err, ErrScaleMismatch) {
		t.Fatalf("expected ErrScaleMismatch, got %v", err)
	}
	if _, err := a.Div(b); !errors.Is(err, ErrScaleMismatch) {
		t.Fatalf("expected ErrScaleMismatch, got %v", err)
	}
}

func TestIllegalScale(t *testing.T) {
	if _, err := New(1, -1); !errors.Is(err, ErrIllegalScale) {
		t.Fatalf("expected ErrIllegalScale, got %v", err)
	}
	if _, err := New(1, MaxScale+1); !errors.Is(err, ErrIllegalScale) {
		t.Fatalf("expected ErrIllegalScale for > MaxScale, got %v", err)
	}
}

func TestOverflow(t *testing.T) {
	max := NewInt(1<<63 - 1)
	if _, err := max.Add(NewInt(1)); !errors.Is(err, ErrOverflow) {
		t.Fatalf("expected Add overflow, got %v", err)
	}
	// 2^32 * 2^32 = 2^64 overflows int64 product
	big := NewInt(1 << 32)
	if _, err := big.Mul(big); !errors.Is(err, ErrOverflow) {
		t.Fatalf("expected Mul overflow, got %v", err)
	}
}

func TestCmp(t *testing.T) {
	a := NewInt(5)
	b := NewInt(3)
	if c, _ := a.Cmp(b); c != 1 {
		t.Fatalf("Cmp: got %d, want 1", c)
	}
	if c, _ := b.Cmp(a); c != -1 {
		t.Fatalf("Cmp: got %d, want -1", c)
	}
	if c, _ := a.Cmp(a); c != 0 {
		t.Fatalf("Cmp equal: got %d, want 0", c)
	}
}

func TestWaterCementRatio(t *testing.T) {
	// water/cement ratio 0.38 as raw 38 at scale 2
	water := mustValue(New(380, 2))   // 3.80
	cement := mustValue(New(1000, 2)) // 10.00
	ratio := mustValue(water.Div(cement))
	if ratio.Raw() != 38 || ratio.Scale() != 2 {
		t.Fatalf("water/cement ratio: got raw=%d scale=%d, want 38 scale 2", ratio.Raw(), ratio.Scale())
	}
}
