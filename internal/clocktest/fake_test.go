package clocktest

import (
	"context"
	"testing"
	"time"
)

func waitWaiters(t *testing.T, c *FakeClock, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for c.Waiters() != n {
		if time.Now().After(deadline) {
			t.Fatalf("waiters = %d, want %d", c.Waiters(), n)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestFakeClockSleep(t *testing.T) {
	c := New(time.Unix(0, 0))
	done := make(chan error, 1)
	go func() { done <- c.Sleep(context.Background(), 5*time.Second) }()
	waitWaiters(t, c, 1)
	c.Advance(4 * time.Second)
	select {
	case err := <-done:
		t.Logf("err = %v", err)
		t.Fatal("returned early")
	case <-time.After(20 * time.Millisecond):
	}
	c.Advance(time.Second)
	if err := <-done; err != nil {
		t.Fatalf("err = %v", err)
	}
	if c.Waiters() != 0 {
		t.Fatalf("waiters = %d", c.Waiters())
	}
}

func TestFakeClockSleepCancel(t *testing.T) {
	c := New(time.Unix(0, 0))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Sleep(ctx, time.Hour) }()
	waitWaiters(t, c, 1)
	cancel()
	if err := <-done; err != context.Canceled {
		t.Fatalf("err = %v", err)
	}
	waitWaiters(t, c, 0)
}

func TestFakeClockAfterAndNow(t *testing.T) {
	start := time.Unix(100, 0)
	c := New(start)
	ch := c.After(time.Second)
	c.Advance(time.Second)
	got := <-ch
	if !got.Equal(start.Add(time.Second)) || !c.Now().Equal(got) {
		t.Fatalf("got %v now %v", got, c.Now())
	}
}
