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
	i.Dispatch("pause_all") // unbound: ignored, no panic
	i.Bind(func(app.Command) bool { n++; return true }, nil, nil)
	i.Dispatch("pause_all")
	if n != 1 {
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

