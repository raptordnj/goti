package events

import (
	"context"
	"testing"
	"time"

	"github.com/raptordnj/swiftfetch/internal/clocktest"
	"github.com/raptordnj/swiftfetch/internal/domain"
)

func newTest(t *testing.T) (*Bus, *Subscription, *clocktest.FakeClock) {
	t.Helper()
	clk := clocktest.New(time.Unix(1000, 0))
	b := NewBus(4, clk)
	s := b.Subscribe()
	t.Cleanup(s.Close)
	return b, s, clk
}

func next(t *testing.T, s *Subscription) Event {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	e, err := s.Next(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func waitWaiters(t *testing.T, clk *clocktest.FakeClock) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for clk.Waiters() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("Next never blocked on the clock")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestProgressCoalesced(t *testing.T) {
	b, s, clk := newTest(t)
	for i := int64(1); i <= 100; i++ {
		b.Publish(Progress{ID: "A", Downloaded: i})
	}
	if p := next(t, s).(Progress); p.Downloaded != 100 {
		t.Fatalf("got %d, want 100", p.Downloaded)
	}
	b.Publish(Progress{ID: "A", Downloaded: 101})
	got := make(chan Event, 1)
	go func() {
		e, _ := s.Next(context.Background())
		got <- e
	}()
	waitWaiters(t, clk)
	select {
	case <-got:
		t.Fatal("delivered before tick")
	case <-time.After(30 * time.Millisecond):
	}
	// Publishing while blocked must not leak extra timers.
	for i := int64(102); i < 110; i++ {
		b.Publish(Progress{ID: "A", Downloaded: i})
		time.Sleep(time.Millisecond)
	}
	if w := clk.Waiters(); w != 1 {
		t.Fatalf("waiters=%d, want 1", w)
	}
	clk.Advance(250 * time.Millisecond)
	select {
	case e := <-got:
		if e.(Progress).Downloaded != 109 {
			t.Fatalf("got %+v", e)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("not delivered after Advance")
	}
}

func TestStateEventsNotDropped(t *testing.T) {
	b, s, _ := newTest(t)
	for i := range 100 {
		b.Publish(StateChanged{ID: "A", Reason: string(rune('a' + i%26)), From: domain.State("x")})
	}
	for i := range 100 {
		e := next(t, s).(StateChanged)
		if e.Reason != string(rune('a'+i%26)) {
			t.Fatalf("out of order at %d", i)
		}
	}
}

func TestOverflowResync(t *testing.T) {
	b, s, _ := newTest(t)
	b.Publish(Progress{ID: "A"})
	for range 300 {
		b.Publish(StateChanged{ID: "A"})
	}
	e := next(t, s)
	if _, ok := e.(Resync); !ok {
		t.Fatalf("got %T, want Resync", e)
	}
	if e.TaskID() != "" {
		t.Fatal("Resync TaskID should be empty")
	}
	// Events published after the overflow follow the Resync; the earlier
	// pending progress was discarded.
	for range 300 - queueCap - 1 {
		if _, ok := next(t, s).(StateChanged); !ok {
			t.Fatal("want StateChanged after Resync")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if e, err := s.Next(ctx); err == nil {
		t.Fatalf("unexpected extra event %T", e)
	}
}

func TestPublishNeverBlocks(t *testing.T) {
	b, _, _ := newTest(t)
	start := time.Now()
	for i := range 10_000 {
		if i%2 == 0 {
			b.Publish(StateChanged{ID: "A"})
		} else {
			b.Publish(Progress{ID: "A"})
		}
	}
	if el := time.Since(start); el > time.Second {
		t.Fatalf("took %v", el)
	}
}

func TestStateBeforeProgress(t *testing.T) {
	b, s, _ := newTest(t)
	b.Publish(Progress{ID: "A", Downloaded: 1})
	b.Publish(StateChanged{ID: "A", Reason: "s1"})
	b.Publish(Progress{ID: "B", Downloaded: 2})
	b.Publish(StateChanged{ID: "B", Reason: "s2"})
	if e, ok := next(t, s).(StateChanged); !ok || e.Reason != "s1" {
		t.Fatalf("want s1 first, got %+v", e)
	}
	if e, ok := next(t, s).(StateChanged); !ok || e.Reason != "s2" {
		t.Fatalf("want s2 second, got %+v", e)
	}
	if _, ok := next(t, s).(Progress); !ok {
		t.Fatal("want progress")
	}
	if _, ok := next(t, s).(Progress); !ok {
		t.Fatal("want progress")
	}
}

func TestCloseUnblocksNext(t *testing.T) {
	_, s, _ := newTest(t)
	errc := make(chan error, 1)
	go func() {
		_, err := s.Next(context.Background())
		errc <- err
	}()
	time.Sleep(20 * time.Millisecond)
	s.Close()
	select {
	case err := <-errc:
		if err != ErrClosed {
			t.Fatalf("err=%v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Next not unblocked")
	}
}

func TestNextContextCancel(t *testing.T) {
	_, s, _ := newTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := s.Next(ctx); err == nil {
		t.Fatal("expected ctx error")
	}
}

func TestCommandResultDelivered(t *testing.T) {
	b, s, _ := newTest(t)
	errBoom := context.Canceled
	b.Publish(CommandResult{Cmd: "pause", ID: "t1", Err: errBoom})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	ev, err := s.Next(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cr, ok := ev.(CommandResult)
	if !ok || cr.TaskID() != "t1" || cr.Cmd != "pause" || cr.Err != errBoom {
		t.Fatalf("got %#v", ev)
	}
}
