package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/raptordnj/goti/internal/downloader"
	"github.com/raptordnj/goti/tests/faultserver"
)

var update = flag.Bool("update", false, "rewrite testdata/explain/*.golden")

// TestExplainGolden pins the Decision JSON for representative modes.
// Probe RTTs are the only nondeterministic fields and are zeroed; nothing
// else in a Decision depends on timing or on the server's port (it carries
// no URLs), and every Reason derived from RTT only fires above 400 ms,
// which a loopback probe never reaches.
func TestExplainGolden(t *testing.T) {
	modes := []struct {
		name string
		opts faultserver.Options
	}{
		{"correct-ranges", faultserver.Options{Size: 50 * mib, ETag: "v1"}},
		{"ignore-ranges", faultserver.Options{Size: big, ETag: "v1", IgnoreRanges: true}},
		{"malformed-content-range", faultserver.Options{Size: big, ETag: "v1", MalformedContentRange: true}},
		{"wrong-total", faultserver.Options{Size: big, ETag: "v1", WrongTotal: true}},
		{"weak-etag", faultserver.Options{Size: big, ETag: "v1", WeakETag: true}},
		{"last-modified-only", faultserver.Options{Size: 50 * mib, LastModified: lastMod}},
		{"unknown-length", faultserver.Options{Size: big, ETag: "v1", UnknownLength: true}},
		{"empty", faultserver.Options{Size: 0, ETag: "v1"}},
	}
	for _, m := range modes {
		t.Run(m.name, func(t *testing.T) {
			h := newHarness(t, m.opts, harnessOpts{})
			d, _, err := h.eng.Explain(context.Background(), h.fileURL(), downloader.AddRequest{})
			if err != nil {
				t.Fatal(err)
			}
			for i := range d.Probes {
				d.Probes[i].RTT = 0
			}
			got, err := json.MarshalIndent(d, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, '\n')
			path := filepath.Join("testdata", "explain", m.name+".golden")
			if *update {
				if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, got, 0o600); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path) //nolint:gosec // test data path
			if err != nil {
				t.Fatalf("%v (run with -update to create)", err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("%s mismatch (run with -update to accept):\ngot:\n%s\nwant:\n%s", path, got, want)
			}
		})
	}
}
