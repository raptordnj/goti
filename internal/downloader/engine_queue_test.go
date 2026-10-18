// These tests cover the queue hooks of the Engine: Hold, Requeue, SetConfig,
// AllowRestart, Running and the cross-process lock error.

package downloader

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/raptordnj/swiftfetch/internal/config"
	"github.com/raptordnj/swiftfetch/internal/domain"
	"github.com/raptordnj/swiftfetch/internal/events"
	"github.com/raptordnj/swiftfetch/tests/faultserver"
)

func slowFixture(t *testing.T, mod func(*config.Config)) *engFixture {
	return newEngFixture(t, faultserver.Options{Size: 8 << 20, BytesPerSecondGlobal: 2 << 20}, mod)
}

func (f *engFixture) setStored(t *testing.T, id string, s domain.State) {
	t.Helper()
	task := f.task(t, id)
	task.State = s
	if err := f.store.UpdateTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
}

func TestHoldQueuedIdle(t *testing.T) {
	f := slowFixture(t, nil)
	task := f.add(t, AddRequest{})
	if err := f.e.Hold(context.Background(), task.ID); err != nil {
		t.Fatal(err)
	}
	if got := f.task(t, task.ID).State; got != domain.StatePaused {
		t.Fatalf("state %s", got)
	}
	waitFor(t, 5*time.Second, "StateChanged", func() bool {
		for _, ev := range f.events() {
			if sc, ok := ev.(events.StateChanged); ok && sc.ID == task.ID && sc.From == domain.StateQueued && sc.To == domain.StatePaused {
				return true
			}
		}
		return false
	})
}

func TestHoldRunning(t *testing.T) {
	f := slowFixture(t, nil)
	task := f.add(t, AddRequest{})
	ch := f.startRun(task.ID)
	waitFor(t, 20*time.Second, "progress", func() bool { return f.downloaded(task.ID) > 0 })
	if err := f.e.Hold(context.Background(), task.ID); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("Hold: %v", err)
	}
	if err := f.e.Requeue(context.Background(), task.ID); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("Requeue: %v", err)
	}
	f.e.Pause(task.ID)
	_ = recvErr(t, ch)
}

func TestHoldPausedNoop(t *testing.T) {
	f := slowFixture(t, nil)
	task := f.add(t, AddRequest{})
	f.setStored(t, task.ID, domain.StatePaused)
	if err := f.e.Hold(context.Background(), task.ID); err != nil {
		t.Fatal(err)
	}
	if got := f.task(t, task.ID).State; got != domain.StatePaused {
		t.Fatalf("state %s", got)
	}
}

func TestHoldCompletedInvalid(t *testing.T) {
	f := slowFixture(t, nil)
	task := f.add(t, AddRequest{})
	f.setStored(t, task.ID, domain.StateCompleted)
	if err := f.e.Hold(context.Background(), task.ID); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("got %v", err)
	}
}

func TestRequeue(t *testing.T) {
	f := slowFixture(t, nil)
	ctx := context.Background()
	cases := []struct {
		from, want domain.State
		invalid    bool
	}{
		{domain.StatePaused, domain.StateQueued, false},
		{domain.StateFailed, domain.StateQueued, false},
		{domain.StateQueued, domain.StateQueued, false},
		{domain.StateDownloading, domain.StateDownloading, false},
		{domain.StateCompleted, domain.StateCompleted, true},
	}
	for _, c := range cases {
		task := f.add(t, AddRequest{})
		f.setStored(t, task.ID, c.from)
		err := f.e.Requeue(ctx, task.ID)
		if c.invalid != errors.Is(err, domain.ErrInvalidTransition) || (!c.invalid && err != nil) {
			t.Fatalf("%s: err %v", c.from, err)
		}
		if got := f.task(t, task.ID).State; got != c.want {
			t.Fatalf("%s: state %s, want %s", c.from, got, c.want)
		}
	}
}

func TestSetConfigAffectsNewRuns(t *testing.T) {
	f := newEngFixture(t, faultserver.Options{Size: 8 << 20}, func(c *config.Config) {
		c.MinMultipartSize = 1 << 20
		c.MaxConnections = 4
	})
	cfg := f.cfg
	cfg.MaxConnections, cfg.InitialConnections = 1, 1
	f.e.SetConfig(cfg)
	task := f.add(t, AddRequest{})
	if err := f.run(t, task.ID); err != nil {
		t.Fatal(err)
	}
	var d struct {
		MaxConns int `json:"max_conns"`
	}
	if err := json.Unmarshal(f.task(t, task.ID).DecisionJSON, &d); err != nil {
		t.Fatal(err)
	}
	if d.MaxConns > 1 {
		t.Fatalf("max_conns %d", d.MaxConns)
	}
	if m := f.srv.ActiveMax(); m > 1 {
		t.Fatalf("ActiveMax %d", m)
	}
}

func TestAllowRestart(t *testing.T) {
	const size = 10 << 20
	f := newEngFixture(t, faultserver.Options{Size: size, BytesPerSecondGlobal: 10 << 20}, nil)
	task := f.add(t, AddRequest{})
	if err := f.pauseAt(t, task.ID, 2<<20); !errors.Is(err, domain.ErrPaused) {
		t.Fatal(err)
	}
	f.srv.SetOptions(func(o *faultserver.Options) {
		o.ETag, o.Seed, o.BytesPerSecondGlobal = "v2", faultserver.ChangedSeed(engSeed), 0
	})
	_ = wantKind(t, f.run(t, task.ID), domain.KindRemoteChanged)
	ctx := context.Background()
	if err := f.e.AllowRestart(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.e.Requeue(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.run(t, task.ID); err != nil {
		t.Fatal(err)
	}
	checkContent(t, f.task(t, task.ID).FinalPath, faultserver.ChangedSeed(engSeed), size)
}

func TestLockedElsewhere(t *testing.T) {
	f := slowFixture(t, nil)
	task := f.add(t, AddRequest{})
	ch := f.startRun(task.ID)
	waitFor(t, 20*time.Second, "progress", func() bool { return f.downloaded(task.ID) > 0 })
	deps := f.e.d
	e2 := NewEngine(deps)
	err := e2.Run(context.Background(), task.ID)
	if !errors.Is(err, ErrLockedElsewhere) {
		t.Fatalf("got %v", err)
	}
	f.e.Pause(task.ID)
	_ = recvErr(t, ch)
}

func TestRunning(t *testing.T) {
	f := slowFixture(t, nil)
	task := f.add(t, AddRequest{})
	if f.e.Running(task.ID) {
		t.Fatal("running before Run")
	}
	ch := f.startRun(task.ID)
	waitFor(t, 20*time.Second, "running", func() bool { return f.e.Running(task.ID) })
	f.e.Pause(task.ID)
	_ = recvErr(t, ch)
	if f.e.Running(task.ID) {
		t.Fatal("running after Run")
	}
}
