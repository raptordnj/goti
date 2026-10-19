package mobile

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/raptordnj/goti/internal/domain"
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

type countKV struct {
	memKV
	gets   int
	putErr string // key prefix whose Put fails
	getErr error
}

func (c *countKV) GetSetting(ctx context.Context, k string) (string, error) {
	c.gets++
	if c.getErr != nil {
		return "", c.getErr
	}
	if v, ok := c.memKV[k]; ok {
		return v, nil
	}
	return "", domain.ErrNotFound
}

func (c *countKV) PutSetting(ctx context.Context, k, v string) error {
	if c.putErr != "" && strings.HasPrefix(k, c.putErr) {
		return errors.New("put failed")
	}
	return c.memKV.PutSetting(ctx, k, v)
}

func mkfile(t *testing.T) string {
	dest := filepath.Join(t.TempDir(), "a.zip")
	os.WriteFile(dest, []byte("x"), 0o600)
	return dest
}

func TestPublisherKeepsFileWhenPutFails(t *testing.T) {
	for _, prefix := range []string{"android.uri:", "android.published:"} {
		dest := mkfile(t)
		kv, now := &countKV{memKV: memKV{}, putErr: prefix}, t0
		newPub(&pubHost{}, kv, &now).Tick(context.Background(), doneSnap("1", dest))
		if _, err := os.Stat(dest); err != nil {
			t.Fatalf("%s: file removed", prefix)
		}
	}
}

func TestPublisherGetErrorSkips(t *testing.T) {
	dest := mkfile(t)
	h, kv, now := &pubHost{}, &countKV{memKV: memKV{}, getErr: errors.New("db down")}, t0
	newPub(h, kv, &now).Tick(context.Background(), doneSnap("1", dest))
	if h.calls2 != 0 {
		t.Fatal("published despite KV error")
	}
}

func TestPublisherNoKVReadsOnceKnown(t *testing.T) {
	dest := mkfile(t)
	h, kv, now := &pubHost{}, &countKV{memKV: memKV{}}, t0
	p := newPub(h, kv, &now)
	s := doneSnap("1", dest)
	p.Tick(context.Background(), s)
	g := kv.gets
	p.Tick(context.Background(), s)
	if kv.gets != g {
		t.Fatalf("extra gets %d", kv.gets-g)
	}
}

func TestPublisherSkipsMissingFile(t *testing.T) {
	h, now := &pubHost{}, t0
	newPub(h, memKV{}, &now).Tick(context.Background(), doneSnap("1", "/nonexistent/x.zip"))
	if h.calls2 != 0 {
		t.Fatal("published missing file")
	}
}

type blockHost struct {
	pubHost
	updates chan struct{}
	enter   chan struct{}
	release chan struct{}
}

func (b *blockHost) UpdateNotification(Notification) {
	select {
	case b.updates <- struct{}{}:
	default:
	}
}
func (b *blockHost) PublishToDownloads(string, string, string) (string, error) {
	b.enter <- struct{}{}
	<-b.release
	return "content://x/1", nil
}

func TestRunPublisherDoesNotBlockController(t *testing.T) {
	dest := mkfile(t)
	h := &blockHost{updates: make(chan struct{}, 100), enter: make(chan struct{}, 10), release: make(chan struct{})}
	now := t0
	pub := newPub(h, memKV{}, &now)
	var n atomic.Int32
	load := func() *state.Snapshot {
		s := doneSnap("1", dest)
		s.ActiveCount = int(n.Add(1)) // title changes -> every tick updates
		return s
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { Run(ctx, load, h, pub, time.Millisecond); close(done) }()
	<-h.enter // publisher is now blocked
	for i := 0; i < 3; i++ {
		select {
		case <-h.updates:
		case <-time.After(5 * time.Second):
			t.Fatal("controller stalled while publisher blocked")
		}
	}
	close(h.release)
	cancel()
	<-done
}

func TestRunTicksController(t *testing.T) {
	h := &startHost{started: make(chan struct{}, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		Run(ctx, func() *state.Snapshot { return snap(1, 0, 0, 0) }, h, nil, time.Millisecond)
		close(done)
	}()
	select {
	case <-h.started:
	case <-time.After(5 * time.Second):
		t.Fatal("no start")
	}
	cancel()
	<-done
}

type startHost struct {
	fakeHost
	started chan struct{}
}

func (s *startHost) StartService() {
	select {
	case s.started <- struct{}{}:
	default:
	}
}
