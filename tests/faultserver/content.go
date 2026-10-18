// Package faultserver is a deterministic fault-injection HTTP server used as
// the test harness for Goti's networking code.
package faultserver

import "crypto/sha256"

// Byte returns the content byte at offset i of the resource generated from
// seed: the low byte of splitmix64(seed ^ i). Content is a pure function of
// (seed, i), so arbitrarily large resources need no storage.
func Byte(seed uint64, i int64) byte {
	z := seed ^ uint64(i) //nolint:gosec // bit pattern of the offset is intended
	z += 0x9e3779b97f4a7c15
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return byte(z ^ (z >> 31)) //nolint:gosec // truncation to the low byte is intended
}

// ChangedSeed returns the content seed served once ChangeETagAfter has
// triggered: seed ^ 0x9e3779b97f4a7c15, so the new representation differs at
// essentially every offset (seed+1 would only permute neighbouring bytes).
func ChangedSeed(seed uint64) uint64 { return seed ^ 0x9e3779b97f4a7c15 }

// fill writes the generated content starting at offset off into buf.
func fill(buf []byte, seed uint64, off int64) {
	for j := range buf {
		buf[j] = Byte(seed, off+int64(j))
	}
}

// ExpectedSHA256 returns the SHA-256 of the first size bytes generated from seed.
func ExpectedSHA256(seed uint64, size int64) [32]byte {
	h := sha256.New()
	buf := make([]byte, 64<<10)
	for off := int64(0); off < size; {
		n := min(int64(len(buf)), size-off)
		fill(buf[:n], seed, off)
		_, _ = h.Write(buf[:n])
		off += n
	}
	var sum [32]byte
	h.Sum(sum[:0])
	return sum
}
