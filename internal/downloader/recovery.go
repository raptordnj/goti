// This file implements verified recovery of checkpointed segments (spec §9.3
// steps 1-3, invariant I2).

package downloader

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"

	"github.com/raptordnj/goti/internal/domain"
	"github.com/raptordnj/goti/internal/storage"
)

// recoverBufSize is the read buffer used to re-hash segments on recovery;
// one buffer is allocated per recoverSegments call.
const recoverBufSize = 256 << 10

// recoverSegments re-verifies checkpointed records against file and returns
// them with every unverifiable segment reset to Start (pending, no hash or
// hasher state). Done segments are re-hashed over [Start,End); in-progress
// segments over [Start,Pos) against the Sum of their restored hasher. A nil
// or missing file, or one whose size is not size, resets every segment.
// discardedBytes is the progress thrown away. Only I/O errors other than a
// missing or short file, and ctx ending, are returned as errors.
func recoverSegments(ctx context.Context, file *storage.TempFile, size int64, recs []domain.SegmentRecord) ([]domain.SegmentRecord, int64, error) {
	out := make([]domain.SegmentRecord, len(recs))
	copy(out, recs)
	var discarded int64
	reset := func(r *domain.SegmentRecord) {
		discarded += r.Pos - r.Start
		r.Pos, r.Done, r.Hash, r.HasherState = r.Start, false, nil, nil
	}
	if !fileHasSize(file, size) {
		for i := range out {
			reset(&out[i])
		}
		return out, discarded, nil
	}
	buf := make([]byte, recoverBufSize)
	for i := range out {
		if err := ctx.Err(); err != nil {
			return nil, 0, err
		}
		r := &out[i]
		if r.Pos <= r.Start && !r.Done {
			reset(r)
			continue
		}
		ok, err := verifyRecord(file, r, buf)
		if err != nil {
			return nil, 0, err
		}
		if !ok {
			reset(r)
		}
	}
	return out, discarded, nil
}

// verifyRecord reports whether r's progress matches the bytes on disk.
func verifyRecord(file *storage.TempFile, r *domain.SegmentRecord, buf []byte) (bool, error) {
	var want []byte
	end := r.Pos
	if r.Done {
		want, end = r.Hash, r.End
	} else {
		if len(r.HasherState) == 0 {
			return false, nil
		}
		h, err := storage.ResumeHasher(r.HasherState)
		if err != nil {
			return false, nil
		}
		want = h.Sum(nil)
	}
	got, err := storage.HashRangeBuf(file, domain.ByteRange{Start: r.Start, End: end}, buf)
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return bytes.Equal(got, want), nil
}

// fileHasSize reports whether file exists on disk with exactly size bytes.
func fileHasSize(file *storage.TempFile, size int64) bool {
	if file == nil {
		return false
	}
	fi, err := os.Stat(file.Path())
	return err == nil && fi.Size() == size
}
