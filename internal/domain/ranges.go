package domain

import (
	"errors"
	"sort"
)

// ByteRange is a half-open byte interval [Start, End).
type ByteRange struct {
	Start, End int64
}

// NewRange validates and returns the range [start, end).
func NewRange(start, end int64) (ByteRange, error) {
	if start < 0 || end < 0 {
		return ByteRange{}, errors.New("domain: negative range bound")
	}
	if start > end {
		return ByteRange{}, errors.New("domain: range start after end")
	}
	return ByteRange{Start: start, End: end}, nil
}

// Len returns the number of bytes in the range.
func (r ByteRange) Len() int64 { return r.End - r.Start }

// Empty reports whether the range contains no bytes.
func (r ByteRange) Empty() bool { return r.End <= r.Start }

// AddInt64 returns a+b and whether the addition did not overflow.
func AddInt64(a, b int64) (int64, bool) {
	c := a + b
	if (c > a) != (b > 0) {
		return 0, false
	}
	return c, true
}

// RangeSet is a set of bytes stored as sorted, disjoint, non-adjacent ranges.
// The zero value is an empty set ready to use.
type RangeSet struct {
	rs []ByteRange
}

// Add inserts r, merging it with any overlapping or adjacent ranges.
func (s *RangeSet) Add(r ByteRange) {
	if r.Empty() {
		return
	}
	// First range that could touch r: End >= r.Start.
	lo := sort.Search(len(s.rs), func(i int) bool { return s.rs[i].End >= r.Start })
	// First range strictly after r: Start > r.End.
	hi := lo + sort.Search(len(s.rs)-lo, func(i int) bool { return s.rs[lo+i].Start > r.End })
	if lo < hi {
		r.Start = min(r.Start, s.rs[lo].Start)
		r.End = max(r.End, s.rs[hi-1].End)
	}
	if hi == lo {
		s.rs = append(s.rs, ByteRange{})
		copy(s.rs[lo+1:], s.rs[lo:])
		s.rs[lo] = r
		return
	}
	s.rs[lo] = r
	s.rs = append(s.rs[:lo+1], s.rs[hi:]...)
}

// Contains reports whether every byte of r is in the set. An empty r is
// trivially contained.
func (s *RangeSet) Contains(r ByteRange) bool {
	if r.Empty() {
		return true
	}
	i := sort.Search(len(s.rs), func(i int) bool { return s.rs[i].End > r.Start })
	return i < len(s.rs) && s.rs[i].Start <= r.Start && s.rs[i].End >= r.End
}

// Len returns the total number of bytes in the set.
func (s *RangeSet) Len() int64 {
	var n int64
	for _, r := range s.rs {
		n += r.Len()
	}
	return n
}

// Missing returns the ranges within [0, total) not in the set, or nil if none.
func (s *RangeSet) Missing(total int64) []ByteRange {
	var out []ByteRange
	var pos int64
	for _, r := range s.rs {
		if r.Start >= total {
			break
		}
		if r.Start > pos {
			out = append(out, ByteRange{pos, r.Start})
		}
		pos = max(pos, r.End)
	}
	if pos < total {
		out = append(out, ByteRange{pos, total})
	}
	return out
}

// Ranges returns a copy of the set's ranges in ascending order.
func (s *RangeSet) Ranges() []ByteRange {
	return append([]ByteRange(nil), s.rs...)
}
