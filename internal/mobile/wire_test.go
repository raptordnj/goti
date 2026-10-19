package mobile

import (
	"testing"

	"github.com/raptordnj/goti/internal/app"
)

func TestInboxActions(t *testing.T) {
	var sent []app.Command
	send := func(c app.Command) bool { sent = append(sent, c); return true }
	i := &Inbox{}
	i.Action("pause_all", send)
	i.Action("resume_all", send)
	i.Action("bogus", send)
	if len(sent) != 2 {
		t.Fatalf("sent %#v", sent)
	}
	if _, ok := sent[0].(app.PauseAll); !ok {
		t.Errorf("first = %#v", sent[0])
	}
	if _, ok := sent[1].(app.ResumeAll); !ok {
		t.Errorf("second = %#v", sent[1])
	}
}

func TestInboxSharedURLTakenOnce(t *testing.T) {
	i := &Inbox{}
	if got := i.TakeSharedURL(); got != "" {
		t.Fatalf("empty inbox = %q", got)
	}
	woke := 0
	i.Bind(nil, func() { woke++ }, nil)
	i.SetSharedURL("https://x.y/a.zip")
	if woke != 1 {
		t.Fatalf("invalidate calls = %d", woke)
	}
	if got := i.TakeSharedURL(); got != "https://x.y/a.zip" {
		t.Fatalf("take = %q", got)
	}
	if got := i.TakeSharedURL(); got != "" {
		t.Fatalf("second take = %q", got)
	}
}

func TestInboxBoundSend(t *testing.T) {
	var n int
	i := &Inbox{}
	i.Dispatch("pause_all") // unbound: queued, delivered on Bind
	i.Bind(func(app.Command) bool { n++; return true }, nil, nil)
	i.Dispatch("pause_all")
	if n != 2 {
		t.Fatalf("n = %d", n)
	}
}

func TestOnceFunc(t *testing.T) {
	n := 0
	f := Once(func() { n++ })
	f()
	f()
	if n != 1 {
		t.Fatalf("n = %d", n)
	}
}

func TestInboxQueuesUntilBound(t *testing.T) {
	i := &Inbox{}
	i.Dispatch("pause_all")
	i.Dispatch("resume_all")
	i.Dispatch("bogus")
	for n := 0; n < 10; n++ {
		i.Dispatch("pause_all")
	}
	var sent []app.Command
	i.Bind(func(c app.Command) bool { sent = append(sent, c); return true }, nil, nil)
	if len(sent) != maxPending {
		t.Fatalf("flushed %d, want %d", len(sent), maxPending)
	}
	for _, c := range sent {
		if _, ok := c.(app.PauseAll); !ok {
			t.Fatalf("kept wrong (old) actions: %#v", sent)
		}
	}
	sent = nil
	i.Bind(func(c app.Command) bool { sent = append(sent, c); return true }, nil, nil)
	if len(sent) != 0 {
		t.Fatalf("re-flushed %#v", sent)
	}
}

func TestInboxQueueKeepsOrder(t *testing.T) {
	i := &Inbox{}
	i.Dispatch("pause_all") // e.g. service timeout before Go bound
	i.Dispatch("resume_all")
	var sent []app.Command
	i.Bind(func(c app.Command) bool { sent = append(sent, c); return true }, nil, nil)
	if len(sent) != 2 {
		t.Fatalf("sent %#v", sent)
	}
	if _, ok := sent[0].(app.PauseAll); !ok {
		t.Fatalf("order %#v", sent)
	}
	if _, ok := sent[1].(app.ResumeAll); !ok {
		t.Fatalf("order %#v", sent)
	}
}

func TestInboxAddNow(t *testing.T) {
	var sent []app.Command
	i := &Inbox{}
	i.AddNow("http://h/early.bin") // unbound: queued until Bind
	i.Bind(func(c app.Command) bool { sent = append(sent, c); return true }, nil, nil)
	i.AddNow("http://h/blob.bin")
	i.AddNow("")
	want := []string{"http://h/early.bin", "http://h/blob.bin"}
	if len(sent) != len(want) {
		t.Fatalf("sent %#v", sent)
	}
	for k, w := range want {
		a, ok := sent[k].(app.AddDownload)
		if !ok || a.URL != w || a.DestDir != "" {
			t.Errorf("sent[%d] = %#v, want AddDownload{URL:%q}", k, sent[k], w)
		}
	}
	if got := i.TakeSharedURL(); got != "" {
		t.Errorf("AddNow must not open the Add sheet, shared = %q", got)
	}
}
