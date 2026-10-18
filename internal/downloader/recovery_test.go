package downloader

import (
	"context"
	"crypto/sha256"
	"encoding"
	"math/rand/v2"
	"os"
	"testing"

	"github.com/raptordnj/goti/internal/domain"
	"github.com/raptordnj/goti/internal/storage"
)

const recSize = 8 << 20

// recoveryFile writes recSize random bytes to a temp file and returns it
// with its content.
func recoveryFile(t *testing.T) (*storage.TempFile, []byte) {
	t.Helper()
	data := make([]byte, recSize)
	_, _ = rand.NewChaCha8([32]byte{3, 4}).Read(data)
	tf, err := storage.CreateTemp(t.TempDir(), "r.bin", recSize, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tf.Close() })
	if _, err := tf.WriteAt(data, 0); err != nil {
		t.Fatal(err)
	}
	return tf, data
}

func doneRec(data []byte, start, end int64) domain.SegmentRecord {
	s := sha256.Sum256(data[start:end])
	return domain.SegmentRecord{Start: start, End: end, Pos: end, Done: true, Hash: s[:]}
}

func progressRec(t *testing.T, data []byte, start, end, pos int64) domain.SegmentRecord {
	t.Helper()
	h := sha256.New()
	h.Write(data[start:pos])
	st, err := h.(encoding.BinaryMarshaler).MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	return domain.SegmentRecord{Start: start, End: end, Pos: pos, HasherState: st}
}

func corrupt(t *testing.T, tf *storage.TempFile, data []byte, off int64) {
	t.Helper()
	if _, err := tf.WriteAt([]byte{data[off] ^ 0xff}, off); err != nil {
		t.Fatal(err)
	}
}

func TestRecoverTrustsOnlyVerified(t *testing.T) {
	const q = recSize / 4
	tf, data := recoveryFile(t)
	recs := []domain.SegmentRecord{
		doneRec(data, 0, q),
		doneRec(data, q, 2*q),
		progressRec(t, data, 2*q, 3*q, 2*q+q/2),
		progressRec(t, data, 3*q, 4*q, 3*q+q/2),
	}
	corrupt(t, tf, data, q+12345)
	corrupt(t, tf, data, 3*q+100)

	got, discarded, err := recoverSegments(context.Background(), tf, recSize, recs)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("%d records", len(got))
	}
	if !got[0].Done || got[0].Pos != q {
		t.Fatalf("seg0 %+v, want done", got[0])
	}
	if got[1].Done || got[1].Pos != q || got[1].Hash != nil || got[1].HasherState != nil {
		t.Fatalf("seg1 %+v, want reset to Start", got[1])
	}
	if got[2].Pos != 2*q+q/2 || got[2].HasherState == nil {
		t.Fatalf("seg2 %+v, want Pos kept", got[2])
	}
	if got[3].Pos != 3*q || got[3].HasherState != nil {
		t.Fatalf("seg3 %+v, want reset to Start", got[3])
	}
	if want := int64(q + q/2); discarded != want {
		t.Fatalf("discarded %d, want %d", discarded, want)
	}
	// The result restores into a scheduler.
	if _, err := RestoreScheduler(recSize, got); err != nil {
		t.Fatal(err)
	}
}

func TestRecoverMissingFileOrWrongSize(t *testing.T) {
	const q = recSize / 4
	tf, data := recoveryFile(t)
	recs := []domain.SegmentRecord{
		doneRec(data, 0, q),
		doneRec(data, q, 2*q),
		progressRec(t, data, 2*q, 3*q, 2*q+100),
		{Start: 3 * q, End: 4 * q, Pos: 3 * q},
	}
	want := int64(2*q + 100)
	check := func(name string, file *storage.TempFile, size int64) {
		t.Helper()
		got, discarded, err := recoverSegments(context.Background(), file, size, recs)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for i, r := range got {
			if r.Done || r.Pos != r.Start || r.Hash != nil || r.HasherState != nil || r.End != recs[i].End {
				t.Fatalf("%s: seg %d %+v not reset", name, i, r)
			}
		}
		if discarded != want {
			t.Fatalf("%s: discarded %d, want %d", name, discarded, want)
		}
	}
	check("nil file", nil, recSize)
	check("wrong size", tf, recSize+1)
	if err := os.Remove(tf.Path()); err != nil {
		t.Fatal(err)
	}
	check("removed file", tf, recSize)
}
