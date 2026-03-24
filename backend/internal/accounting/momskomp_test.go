package accounting

import (
	"math"
	"testing"
)

func TestSimplifiedCompensationZero(t *testing.T) {
	if got := SimplifiedCompensation(0); got != 0 {
		t.Errorf("SimplifiedCompensation(0) = %f, want 0", got)
	}
	if got := SimplifiedCompensation(-1000); got != 0 {
		t.Errorf("SimplifiedCompensation(-1000) = %f, want 0", got)
	}
}

func TestSimplifiedCompensationTier1Only(t *testing.T) {
	// 100,000 * 8% = 8,000
	got := SimplifiedCompensation(100_000)
	want := 8_000.0
	if math.Abs(got-want) > 0.01 {
		t.Errorf("SimplifiedCompensation(100000) = %f, want %f", got, want)
	}
}

func TestSimplifiedCompensationAtTier1Boundary(t *testing.T) {
	// 7,000,000 * 8% = 560,000
	got := SimplifiedCompensation(7_000_000)
	want := 560_000.0
	if math.Abs(got-want) > 0.01 {
		t.Errorf("SimplifiedCompensation(7000000) = %f, want %f", got, want)
	}
}

func TestSimplifiedCompensationTier2(t *testing.T) {
	// 10,000,000: tier1 = 7M * 8% = 560,000; tier2 = 3M * 6% = 180,000; total = 740,000
	got := SimplifiedCompensation(10_000_000)
	want := 740_000.0
	if math.Abs(got-want) > 0.01 {
		t.Errorf("SimplifiedCompensation(10000000) = %f, want %f", got, want)
	}
}

func TestSimplifiedCompensationSmallClub(t *testing.T) {
	// Typical small boat club: 500,000 NOK operating costs
	// 500,000 * 8% = 40,000
	got := SimplifiedCompensation(500_000)
	want := 40_000.0
	if math.Abs(got-want) > 0.01 {
		t.Errorf("SimplifiedCompensation(500000) = %f, want %f", got, want)
	}
}

func TestSimplifiedCompensationRounding(t *testing.T) {
	// 123,456.78 * 8% = 9876.5424 → rounded to 9876.54
	got := SimplifiedCompensation(123_456.78)
	want := 9876.54
	if math.Abs(got-want) > 0.01 {
		t.Errorf("SimplifiedCompensation(123456.78) = %f, want %f", got, want)
	}
}
