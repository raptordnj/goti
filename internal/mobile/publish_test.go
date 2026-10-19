package mobile

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/raptordnj/goti/internal/ui/state"
)

type memKV map[string]string

func (m memKV) GetSetting(_ context.Context, k string) (string, error) { return m[k], nil }
func (m memKV) PutSetting(_ context.Context, k, v string) error        { m[k] = v; return nil }

type pubHost struct {
	fakeHost
	calls2 int
	errs   []error
	mimes  []string
}

func (p *pubHost) PublishToDownloads(path, name, mime string) (string, error) {
	p.calls2++
	p.mimes = append(p.mimes, mime)
	if len(p.errs) > 0 {
		e := p.errs[0]
		p.errs = p.errs[1:]
		if e != nil {
			return "", e
		}
	}
	return "content://media/external/downloads/7", nil
}

func doneSnap(id, dest string) *state.Snapshot {
	return &state.Snapshot{Tasks: []state.TaskView{{ID: id, DestPath: dest, FileName: filepath.Base(dest), Section: state.SectionCompleted}}}
}

func newPub(h Host, kv KV, now *time.Time) *Publisher {
	p := NewPublisher(h, kv, slog.Default())
	p.now = func() time.Time { return *now }
	return p
}

func TestPublisherPublishesOnceAndRemovesPrivateCopy(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "a.zip")
	os.WriteFile(dest, []byte("x"), 0o600)
	h, kv, now := &pubHost{}, memKV{}, t0
	p := newPub(h, kv, &now)
	s := doneSnap("1", dest)
	p.Tick(context.Background(), s)
	p.Tick(context.Background(), s)
	if h.calls2 != 1 {
		t.Fatalf("calls %d", h.calls2)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatal("file kept")
	}
	if u := URIFor(context.Background(), kv, dest); u != "content://media/external/downloads/7" {
		t.Fatalf("uri %q", u)
	}
	if URIFor(context.Background(), kv, "/nope") != "" {
		t.Fatal("expected empty")
	}
}

func TestPublisherRetriesAndIsIdempotent(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "a.zip")
	os.WriteFile(dest, []byte("x"), 0o600)
	h, kv, now := &pubHost{errs: []error{errors.New("boom")}}, memKV{}, t0
	p := newPub(h, kv, &now)
	s := doneSnap("1", dest)
	ctx := context.Background()
	p.Tick(ctx, s)
	if _, err := os.Stat(dest); err != nil || len(kv) != 0 {
		t.Fatal("state changed on error")
	}
	now = t0.Add(10 * time.Second)
	p.Tick(ctx, s)
	if h.calls2 != 1 {
		t.Fatalf("retried too soon: %d", h.calls2)
	}
	now = t0.Add(31 * time.Second)
	p.Tick(ctx, s)
	if h.calls2 != 2 || len(kv) != 2 {
		t.Fatalf("calls %d kv %v", h.calls2, kv)
	}
	p2 := newPub(h, kv, &now)
	p2.Tick(ctx, s)
	if h.calls2 != 2 {
		t.Fatal("republished")
	}
}

func TestPublisherMime(t *testing.T) {
	for name, want := range map[string]string{"a.zip": "application/zip", "a.unknownext": "application/octet-stream"} {
		dest := filepath.Join(t.TempDir(), name)
		os.WriteFile(dest, []byte("x"), 0o600)
		h, now := &pubHost{}, t0
		newPub(h, memKV{}, &now).Tick(context.Background(), doneSnap("1", dest))
		if len(h.mimes) != 1 || h.mimes[0] != want {
			t.Errorf("%s: %v want %s", name, h.mimes, want)
		}
	}
}

func TestRunTicksControllerAndPublisher(t *testing.T) {
	h := &fakeHost{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		Run(ctx, func() *state.Snapshot { return snap(1, 0, 0, 0) }, h, nil, 5*time.Millisecond)
		close(done)
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done
	if h.count("start") != 1 {
		t.Fatalf("%v", h.calls)
	}
}
