package downloader

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math/rand/v2"
	"net"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/raptordnj/goti/internal/clocktest"
	"github.com/raptordnj/goti/internal/domain"
)

func newBackoff() Backoff {
	return Backoff{Base: 500 * time.Millisecond, Max: 60 * time.Second, Factor: 2, Rand: rand.New(rand.NewPCG(1, 2))} //nolint:gosec // deterministic test seed
}

func TestDelayBounds(t *testing.T) {
	b := newBackoff()
	for a := 0; a <= 10; a++ {
		hi := min(60*time.Second, 500*time.Millisecond<<a)
		for i := 0; i < 1000; i++ {
			if d := b.Delay(a, 0); d < 0 || d > hi {
				t.Fatalf("attempt %d: %v outside [0,%v]", a, d, hi)
			}
		}
	}
}

func TestDelayRetryAfter(t *testing.T) {
	b := newBackoff()
	for i := 0; i < 1000; i++ {
		if d := b.Delay(3, 30*time.Second); d < 30*time.Second || d > 33*time.Second {
			t.Fatalf("got %v", d)
		}
		if d := b.Delay(3, time.Hour); d > 11*time.Minute || d < 10*time.Minute {
			t.Fatalf("got %v", d)
		}
	}
}

func TestClassify(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want domain.ErrorKind
	}{
		{"econnreset", fmt.Errorf("read: %w", syscall.ECONNRESET), domain.KindTransient},
		{"enospc", &fs.PathError{Op: "write", Path: "x", Err: syscall.ENOSPC}, domain.KindLocal},
		{"eacces", fmt.Errorf("w: %w", syscall.EACCES), domain.KindLocal},
		{"erofs", fmt.Errorf("w: %w", syscall.EROFS), domain.KindLocal},
		{"auth", &domain.Error{Kind: domain.KindAuth}, domain.KindAuth},
		{"wrapped auth", fmt.Errorf("x: %w", &domain.Error{Kind: domain.KindAuth}), domain.KindAuth},
		{"domain beats syscall", &domain.Error{Kind: domain.KindPermanent, Err: syscall.ECONNRESET}, domain.KindPermanent},
		{"canceled", context.Canceled, domain.KindCancelled},
		{"x509", x509.UnknownAuthorityError{}, domain.KindPermanent},
		{"x509 wrapped", fmt.Errorf("tls: %w", x509.HostnameError{}), domain.KindPermanent},
		{"unexpected eof", io.ErrUnexpectedEOF, domain.KindTransient},
		{"timeout", &net.OpError{Op: "read", Err: timeoutErr{}}, domain.KindTransient},
		{"unknown", errors.New("boom"), domain.KindTransient},
	}
	for _, c := range cases {
		if got := Classify(c.err); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

func TestClassifyOffline(t *testing.T) {
	for name, err := range map[string]error{
		"dns":     &net.DNSError{Err: "no such host", Name: "x"},
		"refused": fmt.Errorf("dial: %w", &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}),
		"netun":   fmt.Errorf("w: %w", syscall.ENETUNREACH),
		"hostun":  fmt.Errorf("w: %w", syscall.EHOSTUNREACH),
		"dialto":  &net.OpError{Op: "dial", Err: timeoutErr{}},
	} {
		if got := Classify(err); got != domain.KindOffline {
			t.Errorf("%s: got %v", name, got)
		}
	}
}

func TestRetryableOnlyTransient(t *testing.T) {
	for k := domain.KindTransient; k <= domain.KindLocal; k++ {
		want := k == domain.KindTransient || k == domain.KindOffline
		if Retryable(k) != want {
			t.Errorf("%v: got %v", k, Retryable(k))
		}
	}
}

func TestBudgetConcurrent(t *testing.T) {
	b := NewBudget(50)
	var n atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if b.Spend() {
				n.Add(1)
			}
		}()
	}
	wg.Wait()
	if n.Load() != 50 {
		t.Fatalf("got %d", n.Load())
	}
}

func TestBudgetProgressResets(t *testing.T) {
	b := NewBudget(50)
	for i := 0; i < 49; i++ {
		if !b.Spend() {
			t.Fatal("spend failed")
		}
	}
	b.Progress()
	for i := 0; i < 50; i++ {
		if !b.Spend() {
			t.Fatalf("spend %d failed after reset", i)
		}
	}
	if b.Spend() {
		t.Fatal("should be exhausted")
	}
}

func waitWaiters(t *testing.T, c *clocktest.FakeClock) {
	t.Helper()
	for i := 0; i < 5000 && c.Waiters() < 1; i++ {
		time.Sleep(time.Millisecond)
	}
	if c.Waiters() < 1 {
		t.Fatal("waiter never slept")
	}
}

func TestOfflineWaiter(t *testing.T) {
	t.Run("recovers", func(t *testing.T) {
		clk := clocktest.New(time.Unix(0, 0))
		fails := 3
		w := &OfflineWaiter{Clock: clk, Limit: time.Hour, Check: func(context.Context) error {
			if fails > 0 {
				fails--
				return errors.New("down")
			}
			return nil
		}}
		var mu sync.Mutex
		var delays []time.Duration
		done := make(chan error, 1)
		go func() {
			done <- w.Wait(context.Background(), func(_ int, next time.Duration) {
				mu.Lock()
				delays = append(delays, next)
				mu.Unlock()
			})
		}()
		for _, d := range []time.Duration{5 * time.Second, 10 * time.Second, 20 * time.Second} {
			waitWaiters(t, clk)
			clk.Advance(d)
		}
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		want := []time.Duration{5 * time.Second, 10 * time.Second, 20 * time.Second}
		mu.Lock()
		defer mu.Unlock()
		if fmt.Sprint(delays) != fmt.Sprint(want) {
			t.Fatalf("delays %v", delays)
		}
	})
	t.Run("limit", func(t *testing.T) {
		clk := clocktest.New(time.Unix(0, 0))
		start := clk.Now()
		w := &OfflineWaiter{Clock: clk, Limit: time.Minute, Check: func(context.Context) error { return errors.New("down") }}
		done := make(chan error, 1)
		go func() { done <- w.Wait(context.Background(), nil) }()
		for {
			select {
			case err := <-done:
				if domain.KindOf(err) != domain.KindOffline {
					t.Fatalf("got %v", err)
				}
				if clk.Now().Sub(start) < time.Minute {
					t.Fatal("gave up early")
				}
				return
			default:
			}
			if clk.Waiters() > 0 {
				clk.Advance(5 * time.Second)
			}
			time.Sleep(time.Millisecond)
		}
	})
	t.Run("forever until cancel", func(t *testing.T) {
		clk := clocktest.New(time.Unix(0, 0))
		w := &OfflineWaiter{Clock: clk, Check: func(context.Context) error { return errors.New("down") }}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- w.Wait(ctx, nil) }()
		for i := 0; i < 30; i++ {
			waitWaiters(t, clk)
			clk.Advance(60 * time.Second)
			time.Sleep(time.Millisecond)
		}
		select {
		case err := <-done:
			t.Fatalf("returned early: %v", err)
		default:
		}
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("custom base and cap sequence", func(t *testing.T) {
		for _, c := range []struct {
			base time.Duration
			want []time.Duration
		}{
			{100 * time.Millisecond, []time.Duration{100 * time.Millisecond, 200 * time.Millisecond, 400 * time.Millisecond}},
			{0, []time.Duration{5 * time.Second, 10 * time.Second, 20 * time.Second, 40 * time.Second, 60 * time.Second, 60 * time.Second}},
		} {
			clk := clocktest.New(time.Unix(0, 0))
			var mu sync.Mutex
			var got []time.Duration
			w := &OfflineWaiter{Clock: clk, Base: c.base, Check: func(context.Context) error { return errors.New("down") }}
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() {
				done <- w.Wait(ctx, func(_ int, n time.Duration) { mu.Lock(); got = append(got, n); mu.Unlock() })
			}()
			for _, d := range c.want {
				waitWaiters(t, clk)
				clk.Advance(d)
				time.Sleep(time.Millisecond)
			}
			waitWaiters(t, clk)
			cancel()
			if err := <-done; !errors.Is(err, context.Canceled) {
				t.Errorf("got %v", err)
			}
			mu.Lock()
			if fmt.Sprint(got[:len(c.want)]) != fmt.Sprint(c.want) {
				t.Errorf("base %v: got %v want %v", c.base, got, c.want)
			}
			mu.Unlock()
		}
	})
	t.Run("sleep clamped to remaining limit", func(t *testing.T) {
		clk := clocktest.New(time.Unix(0, 0))
		start := clk.Now()
		w := &OfflineWaiter{Clock: clk, Limit: 50 * time.Second, Check: func(context.Context) error { return errors.New("down") }}
		done := make(chan error, 1)
		go func() { done <- w.Wait(context.Background(), nil) }()
		for {
			select {
			case err := <-done:
				if domain.KindOf(err) != domain.KindOffline {
					t.Fatalf("got %v", err)
				}
				if el := clk.Now().Sub(start); el != 50*time.Second {
					t.Fatalf("elapsed %v, want exactly 50s", el)
				}
				return
			default:
			}
			if clk.Waiters() > 0 {
				clk.Advance(time.Second)
			}
			time.Sleep(time.Millisecond)
		}
	})
}
