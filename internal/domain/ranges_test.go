package domain

import (
	"math"
	"math/rand/v2"
	"slices"
	"testing"
)

func TestNewRangeRejects(t *testing.T) {
	for _, c := range [][2]int64{{-1, 5}, {5, 4}, {-3, -1}} {
		if _, err := NewRange(c[0], c[1]); err == nil {
			t.Errorf("NewRange(%d,%d) = nil error, want error", c[0], c[1])
		}
	}
	r, err := NewRange(5, 5)
	if err != nil {
		t.Fatalf("NewRange(5,5) error: %v", err)
	}
	if !r.Empty() || r.Len() != 0 {
		t.Errorf("NewRange(5,5) = %+v, want empty", r)
	}
	r, err = NewRange(2, 9)
	if err != nil || r.Len() != 7 || r.Empty() {
		t.Errorf("NewRange(2,9) = %+v, %v", r, err)
	}
}

func TestRangeSetCoalesce(t *testing.T) {
	var s RangeSet
	s.Add(ByteRange{0, 10})
	s.Add(ByteRange{10, 20})
	if got, want := s.Ranges(), []ByteRange{{0, 20}}; !slices.Equal(got, want) {
		t.Fatalf("Ranges() = %v, want %v", got, want)
	}
	s.Add(ByteRange{30, 40})
	s.Add(ByteRange{5, 35})
	if got, want := s.Ranges(), []ByteRange{{0, 40}}; !slices.Equal(got, want) {
		t.Fatalf("Ranges() = %v, want %v", got, want)
	}
	s.Add(ByteRange{7, 7}) // empty: ignored
	if s.Len() != 40 {
		t.Fatalf("Len() = %d, want 40", s.Len())
	}
}

func TestRangeSetContainsAndCopy(t *testing.T) {
	var s RangeSet
	s.Add(ByteRange{0, 10})
	s.Add(ByteRange{20, 30})
	if !s.Contains(ByteRange{2, 8}) || !s.Contains(ByteRange{20, 30}) {
		t.Error("Contains false for covered range")
	}
	if s.Contains(ByteRange{5, 25}) || s.Contains(ByteRange{10, 11}) {
		t.Error("Contains true for uncovered range")
	}
	got := s.Ranges()
	got[0] = ByteRange{99, 100}
	if s.Ranges()[0] != (ByteRange{0, 10}) {
		t.Error("Ranges() did not return a copy")
	}
}

func TestMissing(t *testing.T) {
	var s RangeSet
	s.Add(ByteRange{0, 10})
	s.Add(ByteRange{20, 30})
	if got, want := s.Missing(40), []ByteRange{{10, 20}, {30, 40}}; !slices.Equal(got, want) {
		t.Errorf("Missing(40) = %v, want %v", got, want)
	}
	var e RangeSet
	if got := e.Missing(0); got != nil {
		t.Errorf("empty Missing(0) = %v, want nil", got)
	}
	if got, want := e.Missing(5), []ByteRange{{0, 5}}; !slices.Equal(got, want) {
		t.Errorf("empty Missing(5) = %v, want %v", got, want)
	}
	// Ranges beyond total are clipped.
	if got, want := s.Missing(25), []ByteRange{{10, 20}}; !slices.Equal(got, want) {
		t.Errorf("Missing(25) = %v, want %v", got, want)
	}
}

func TestAddInt64Overflow(t *testing.T) {
	if _, ok := AddInt64(math.MaxInt64, 1); ok {
		t.Error("AddInt64(MaxInt64, 1) ok = true, want false")
	}
	if _, ok := AddInt64(math.MinInt64, -1); ok {
		t.Error("AddInt64(MinInt64, -1) ok = true, want false")
	}
	if v, ok := AddInt64(2, 3); !ok || v != 5 {
		t.Errorf("AddInt64(2,3) = %d, %v", v, ok)
	}
}

func TestRangeSetLargeOffsets(t *testing.T) {
	const base = int64(5) << 30
	var s RangeSet
	s.Add(ByteRange{base, base + 10})
	s.Add(ByteRange{base + 10, base + 20})
	s.Add(ByteRange{base + 30, base + 40})
	s.Add(ByteRange{base + 5, base + 35})
	if got, want := s.Ranges(), []ByteRange{{base, base + 40}}; !slices.Equal(got, want) {
		t.Fatalf("Ranges() = %v, want %v", got, want)
	}
	if s.Len() != 40 {
		t.Errorf("Len() = %d, want 40", s.Len())
	}
	if got, want := s.Missing(base+50), []ByteRange{{0, base}, {base + 40, base + 50}}; !slices.Equal(got, want) {
		t.Errorf("Missing = %v, want %v", got, want)
	}
}

func TestRangeSetProperties(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2)) //nolint:gosec // deterministic seed for reproducible test
	for iter := 0; iter < 2000; iter++ {
		const total = 1000
		var s RangeSet
		bitmap := make([]bool, total)
		for k := 0; k < 20; k++ {
			a, b := r.Int64N(total), r.Int64N(total)
			if a > b {
				a, b = b, a
			}
			s.Add(ByteRange{a, b})
			for i := a; i < b; i++ {
				bitmap[i] = true
			}
		}
		rs := s.Ranges()
		var covered int64
		for i, x := range rs {
			if x.Empty() {
				t.Fatalf("iter %d: empty range %v", iter, x)
			}
			if i > 0 && rs[i-1].End >= x.Start {
				t.Fatalf("iter %d: ranges not sorted/disjoint/non-adjacent: %v", iter, rs)
			}
			covered += x.Len()
		}
		var want int64
		for _, v := range bitmap {
			if v {
				want++
			}
		}
		if covered != want || s.Len() != want {
			t.Fatalf("iter %d: Len=%d sum=%d bitmap=%d", iter, s.Len(), covered, want)
		}
		missing := make([]bool, total)
		for _, m := range s.Missing(total) {
			for i := m.Start; i < m.End; i++ {
				missing[i] = true
			}
		}
		for i := range bitmap {
			if missing[i] == bitmap[i] {
				t.Fatalf("iter %d: byte %d covered=%v missing=%v", iter, i, bitmap[i], missing[i])
			}
		}
	}
}
