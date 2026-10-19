//go:build !android

package mobile

import (
	"context"
	"errors"
	"testing"

	"github.com/raptordnj/goti/internal/ui/state"
)

type uriFake struct {
	fakeHost
	opened, shared []string
	ok             bool
}

func (u *uriFake) OpenURI(uri, m string) bool  { u.opened = append(u.opened, uri+"|"+m); return u.ok }
func (u *uriFake) ShareURI(uri, m string) bool { u.shared = append(u.shared, uri+"|"+m); return u.ok }

func TestStartWithOpenersUsePublishedURI(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	kv := memKV{"android.uri:/d/a.zip": "content://x/1"}
	h := &uriFake{ok: true}
	first := startWith(Deps{Ctx: ctx, Load: func() *state.Snapshot { return &state.Snapshot{} }, KV: kv}, h)
	if first == nil {
		t.Fatal("no first-add hook")
	}
	open, share := testOpeners[0], testOpeners[1]
	if err := open("/d/a.zip"); err != nil || len(h.opened) != 1 || h.opened[0] != "content://x/1|application/zip" {
		t.Fatalf("open err=%v calls=%v", err, h.opened)
	}
	if err := share("/d/other.bin"); !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("unpublished share err=%v", err)
	}
	h.ok = false
	if err := share("/d/a.zip"); err == nil {
		t.Fatal("host refusal not reported")
	}
}
