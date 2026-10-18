package ratelimit

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestBandwidthSharedAcrossWorkers(t *testing.T) {
	b := NewBandwidth(1 << 20)
	start := time.Now()
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 16 {
				if err := b.WaitN(context.Background(), 32<<10); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	wg.Wait()
	if el := time.Since(start); el < 1800*time.Millisecond {
		t.Fatalf("elapsed %v, limit was multiplied", el)
	}
}

func TestWaitAllUsesStricter(t *testing.T) {
	global := NewBandwidth(2_000_000)
	per := NewBandwidth(500_000)
	start := time.Now()
	for range 31 {
		if err := WaitAll(context.Background(), 32_000, global, per); err != nil {
			t.Fatal(err)
		}
	}
	if err := WaitAll(context.Background(), 8_000, global, nil, per); err != nil {
		t.Fatal(err)
	}
	if el := time.Since(start); el < 1800*time.Millisecond {
		t.Fatalf("elapsed %v", el)
	}
}

func TestUnlimited(t *testing.T) {
	for _, b := range []*Bandwidth{NewBandwidth(0), nil} {
		start := time.Now()
		for range 100 {
			if err := b.WaitN(context.Background(), 1<<20); err != nil {
				t.Fatal(err)
			}
		}
		if el := time.Since(start); el > 50*time.Millisecond {
			t.Fatalf("unlimited took %v", el)
		}
	}
}

func TestSetLimit(t *testing.T) {
	b := NewBandwidth(1 << 10)
	b.SetLimit(0)
	start := time.Now()
	if err := b.WaitN(context.Background(), 10<<20); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 50*time.Millisecond {
		t.Fatal("SetLimit(0) did not unlimit")
	}
}

func TestWaitNLargerThanBurst(t *testing.T) {
	b := NewBandwidth(1 << 20)
	if err := b.WaitN(context.Background(), 1<<20); err != nil {
		t.Fatal(err)
	}
}

func TestWaitNCancel(t *testing.T) {
	b := NewBandwidth(1 << 10)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := b.WaitN(ctx, 1<<20); err == nil {
		t.Fatal("expected error")
	}
}

func TestSemaphore(t *testing.T) {
	s := NewSemaphore(2)
	ctx := context.Background()
	if err := s.Acquire(ctx); err != nil {
		t.Fatal(err)
	}
	if !s.TryAcquire() {
		t.Fatal("second should succeed")
	}
	if s.TryAcquire() {
		t.Fatal("third TryAcquire should fail")
	}
	if s.InUse() != 2 {
		t.Fatalf("InUse=%d", s.InUse())
	}
	done := make(chan error, 1)
	go func() { done <- s.Acquire(ctx) }()
	select {
	case err := <-done:
		_ = err
		t.Fatal("third acquire should block")
	case <-time.After(50 * time.Millisecond):
	}
	s.Release()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	cctx, cancel := context.WithCancel(ctx)
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	if err := s.Acquire(cctx); err == nil {
		t.Fatal("expected ctx error")
	}
	if s.InUse() != 2 {
		t.Fatalf("InUse=%d", s.InUse())
	}
	s.Release()
	s.Release()
	if s.InUse() != 0 {
		t.Fatalf("InUse=%d", s.InUse())
	}
}
