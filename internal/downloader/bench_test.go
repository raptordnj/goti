package downloader

import (
	"context"
	"os"
	"testing"

	"github.com/raptordnj/swiftfetch/internal/domain"
	"github.com/raptordnj/swiftfetch/tests/faultserver"
)

// BenchmarkSingleVsMultipart downloads 64 MiB over loopback from a server
// capped at 4 MB/s per connection, single-stream versus auto multipart.
// Loopback numbers only: they show the per-connection-cap effect, not
// real-network throughput.
func BenchmarkSingleVsMultipart(b *testing.B) {
	const size = 64 << 20
	for _, c := range []struct {
		name  string
		conns int
	}{{"single", 1}, {"multipart-auto", 0}} {
		b.Run(c.name, func(b *testing.B) {
			f := newEngFixture(b, faultserver.Options{Size: size, BytesPerSecondPerConn: 4_000_000}, nil)
			b.SetBytes(size)
			b.ResetTimer()
			for b.Loop() {
				task, err := f.e.Add(context.Background(), AddRequest{URL: f.url(), Connections: c.conns, ExistingPolicy: domain.PolicyOverwrite})
				if err != nil {
					b.Fatal(err)
				}
				if err := f.e.Run(context.Background(), task.ID); err != nil {
					b.Fatal(err)
				}
				done, err := f.store.GetTask(context.Background(), task.ID)
				if err != nil || done.State != domain.StateCompleted {
					b.Fatalf("task %+v (%v)", done, err)
				}
				_ = os.Remove(done.FinalPath)
			}
			b.ReportMetric(float64(size)*float64(b.N)/b.Elapsed().Seconds()/1e6, "MB/s")
		})
	}
}

// BenchmarkInspect measures probe overhead (Explain: inspection plus
// strategy selection) against a localhost server.
func BenchmarkInspect(b *testing.B) {
	f := newEngFixture(b, faultserver.Options{Size: 64 << 20}, nil)
	for b.Loop() {
		if _, _, err := f.e.Explain(context.Background(), f.url(), AddRequest{}); err != nil {
			b.Fatal(err)
		}
	}
}
