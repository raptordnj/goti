package mobile

import (
	"context"
	"errors"
	"log/slog"
	"mime"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/raptordnj/goti/internal/domain"
	"github.com/raptordnj/goti/internal/ui/state"
)

const retryEvery = 30 * time.Second

// KV is the settings store (satisfied by *persistence.DB).
type KV interface {
	GetSetting(ctx context.Context, key string) (string, error)
	PutSetting(ctx context.Context, key, value string) error
}

// Publisher copies completed downloads into shared Downloads exactly once.
type Publisher struct {
	host    Host
	kv      KV
	log     *slog.Logger
	now     func() time.Time
	lastTry map[string]time.Time

	mu      sync.Mutex
	known   map[string]bool // task IDs known to be published
	missing map[string]bool // task IDs whose DestPath was missing (logged once)
}

// NewPublisher returns a Publisher.
func NewPublisher(h Host, kv KV, log *slog.Logger) *Publisher {
	if log == nil {
		log = slog.Default()
	}
	return &Publisher{host: h, kv: kv, log: log, now: time.Now, lastTry: map[string]time.Time{}, known: map[string]bool{}, missing: map[string]bool{}}
}

// Tick publishes any completed, unpublished tasks.
func (p *Publisher) Tick(ctx context.Context, s *state.Snapshot) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, t := range s.Tasks {
		if t.Section != state.SectionCompleted || t.DestPath == "" {
			continue
		}
		key := "android.published:" + t.ID
		if p.known[t.ID] {
			continue
		}
		v, err := p.kv.GetSetting(ctx, key)
		if err != nil && !errors.Is(err, domain.ErrNotFound) {
			p.log.Warn("read published marker", "task", t.ID, "err", err)
			continue
		}
		if err == nil && v != "" {
			p.known[t.ID] = true
			continue
		}
		if _, err := os.Stat(t.DestPath); err != nil {
			if !p.missing[t.ID] {
				p.missing[t.ID] = true
				p.log.Warn("completed file missing, not publishing", "task", t.ID, "err", err)
			}
			continue
		}
		now := p.now()
		if at, ok := p.lastTry[t.ID]; ok && now.Sub(at) < retryEvery {
			continue
		}
		p.lastTry[t.ID] = now
		mt := mime.TypeByExtension(filepath.Ext(t.DestPath))
		if mt == "" {
			mt = "application/octet-stream"
		}
		name := t.FileName
		if name == "" {
			name = filepath.Base(t.DestPath)
		}
		uri, err := p.host.PublishToDownloads(t.DestPath, name, mt)
		if err != nil || uri == "" {
			p.log.Warn("publish to Downloads failed", "task", t.ID, "err", err)
			continue
		}
		// uri-by-path first; the published marker is the commit point.
		if err := p.kv.PutSetting(ctx, "android.uri:"+t.DestPath, uri); err != nil {
			p.log.Warn("record uri by path", "task", t.ID, "err", err)
			continue
		}
		if err := p.kv.PutSetting(ctx, key, uri); err != nil {
			p.log.Warn("record published marker", "task", t.ID, "err", err)
			continue
		}
		p.known[t.ID] = true
		delete(p.lastTry, t.ID)
		if err := os.Remove(t.DestPath); err != nil {
			p.log.Warn("remove private copy", "task", t.ID, "err", err)
		}
	}
}

// URIFor returns the published content URI of destPath, or "".
func URIFor(ctx context.Context, kv KV, destPath string) string {
	v, _ := kv.GetSetting(ctx, "android.uri:"+destPath)
	return v
}

// Run ticks the controller (and pub, when non-nil) every d until ctx ends.
func Run(ctx context.Context, load func() *state.Snapshot, h Host, pub *Publisher, every time.Duration) {
	c := NewController(h)
	var inflight atomic.Bool
	tk := time.NewTicker(every)
	defer tk.Stop()
	for {
		s := load()
		c.Tick(s, time.Now())
		if pub != nil {
			// Own goroutine: a large copy must not stall notifications.
			if inflight.CompareAndSwap(false, true) {
				go func() {
					defer inflight.Store(false)
					pub.Tick(ctx, s)
				}()
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
		}
	}
}
