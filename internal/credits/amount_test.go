package credits

import (
	"errors"
	"math"
	"testing"
)

func TestParseAmountCanonical(t *testing.T) {
	cases := []struct {
		in   string
		want int64
	}{
		{"0", 0},
		{"7", 7},
		{"-7", -7},
		{"1000", 1000},
		{"40000000", 40_000_000},
		{"9223372036854775807", math.MaxInt64},
		{"-9223372036854775808", math.MinInt64},
	}
	for _, c := range cases {
		got, err := ParseAmount(c.in)
		if err != nil || got != c.want {
			t.Fatalf("ParseAmount(%q) = (%d, %v), want (%d, nil)", c.in, got, err, c.want)
		}
	}
}

func TestParseAmountRejectsNonCanonicalAndOutOfRange(t *testing.T) {
	bad := []string{
		"", "-", "+1", "-0", "01", "007", "1.5", "1e3", "1E3", " 1", "1 ",
		"\t1", "1\n", "0x10", "١٢", "9223372036854775808", "-9223372036854775809",
		"99999999999999999999999", "1_000", "１", "1,000",
	}
	for _, s := range bad {
		if got, err := ParseAmount(s); err == nil {
			t.Fatalf("ParseAmount(%q) = %d, want error", s, got)
		}
	}
}

func TestFormatAmountRoundTrip(t *testing.T) {
	values := []int64{0, 1, -1, 1000, -1000, math.MaxInt64, math.MinInt64}
	for _, v := range values {
		s := FormatAmount(v)
		got, err := ParseAmount(s)
		if err != nil || got != v {
			t.Fatalf("round trip %d: got (%q -> %d, %v)", v, s, got, err)
		}
		if s == "-0" || (len(s) > 1 && s[0] == '0') || len(s) > 0 && s[0] == '+' {
			t.Fatalf("FormatAmount(%d) = %q is not canonical", v, s)
		}
	}
}

func TestCheckedArithmeticBoundaries(t *testing.T) {
	if v, err := Add(math.MaxInt64-1, 1); err != nil || v != math.MaxInt64 {
		t.Fatalf("Add near max = %d, %v", v, err)
	}
	if _, err := Add(math.MaxInt64, 1); !errors.Is(err, ErrOverflow) {
		t.Fatalf("Add overflow = %v, want ErrOverflow", err)
	}
	if v, err := Add(math.MinInt64+1, -1); err != nil || v != math.MinInt64 {
		t.Fatalf("Add near min = %d, %v", v, err)
	}
	if _, err := Add(math.MinInt64, -1); !errors.Is(err, ErrOverflow) {
		t.Fatalf("Add underflow = %v, want ErrOverflow", err)
	}
	if v, err := Sub(math.MinInt64+1, 1); err != nil || v != math.MinInt64 {
		t.Fatalf("Sub near min = %d, %v", v, err)
	}
	if _, err := Sub(math.MinInt64, 1); !errors.Is(err, ErrOverflow) {
		t.Fatalf("Sub underflow = %v, want ErrOverflow", err)
	}
	if _, err := Sub(math.MaxInt64, math.MinInt64); !errors.Is(err, ErrOverflow) {
		t.Fatalf("Sub max-min = %v, want ErrOverflow", err)
	}
	// Sub with a negative subtrahend crossing the top boundary.
	if _, err := Sub(math.MaxInt64, -1); !errors.Is(err, ErrOverflow) {
		t.Fatalf("Sub(max,-1) = %v, want ErrOverflow", err)
	}
	if v, err := Mul(math.MaxInt64, 1); err != nil || v != math.MaxInt64 {
		t.Fatalf("Mul identity = %d, %v", v, err)
	}
	if _, err := Mul(math.MaxInt64, 2); !errors.Is(err, ErrOverflow) {
		t.Fatalf("Mul overflow = %v, want ErrOverflow", err)
	}
	if _, err := Mul(math.MinInt64, -1); !errors.Is(err, ErrOverflow) {
		t.Fatalf("Mul(min,-1) = %v, want ErrOverflow", err)
	}
	if v, err := Mul(0, math.MinInt64); err != nil || v != 0 {
		t.Fatalf("Mul zero = %d, %v", v, err)
	}
}
