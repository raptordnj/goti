package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	"github.com/raptordnj/swiftfetch/tests/faultserver"
)

const cliSeed = 7

func runCLI(t *testing.T, env map[string]string, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := runWith(context.Background(), args, nil, &out, &errb, env, system{sigs: make(chan os.Signal), exit: func(int) {}})
	return code, out.String(), errb.String()
}

func TestCLIGetAndList(t *testing.T) {
	const size = 300 << 10
	srv := faultserver.New(t, faultserver.Options{Size: size, Seed: cliSeed, ETag: "v1"})
	dir, data := t.TempDir(), t.TempDir()
	env := map[string]string{"SWIFTFETCH_DATA_DIR": data}
	url := srv.URL + "/file?token=secret"
	code, out, errs := runCLI(t, env, "get", url, "-o", dir)
	if code != 0 {
		t.Fatalf("get exit %d\nstdout: %s\nstderr: %s", code, out, errs)
	}
	got, err := os.ReadFile(filepath.Join(dir, "file"))
	if err != nil {
		t.Fatal(err)
	}
	want := faultserver.ExpectedSHA256(cliSeed, size)
	if sha256.Sum256(got) != want {
		t.Fatal("downloaded content mismatch")
	}
	code, out, errs = runCLI(t, env, "list")
	if code != 0 || !strings.Contains(out, "completed") {
		t.Fatalf("list exit %d out %q err %q", code, out, errs)
	}
	if strings.Contains(out, "secret") || !strings.Contains(out, "token=") {
		t.Fatalf("list URL not redacted: %q", out)
	}
}

func TestCLIExplain(t *testing.T) {
	srv := faultserver.New(t, faultserver.Options{Size: 64 << 20, Seed: cliSeed, ETag: "v1", IgnoreRanges: true})
	env := map[string]string{"SWIFTFETCH_DATA_DIR": t.TempDir()}
	code, out, errs := runCLI(t, env, "explain", srv.URL+"/file?token=secret")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if !strings.Contains(out, "single") || !strings.Contains(out, "ignored Range") {
		t.Fatalf("output missing strategy/reason: %q", out)
	}
	if strings.Contains(out, "secret") {
		t.Fatalf("explain leaked secret: %q", out)
	}
}

func TestCLIUsageError(t *testing.T) {
	env := map[string]string{"SWIFTFETCH_DATA_DIR": t.TempDir()}
	for _, args := range [][]string{{"get"}, {}, {"bogus"}, {"get", "http://x/y", "--conns", "99"}, {"get", "http://x/y", "--limit", "abc"}, {"rm"}} {
		if code, _, _ := runCLI(t, env, args...); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
}

func TestCLIConnsWarning(t *testing.T) {
	srv := faultserver.New(t, faultserver.Options{Size: 64 << 10, Seed: cliSeed, ETag: "v1"})
	env := map[string]string{"SWIFTFETCH_DATA_DIR": t.TempDir()}
	code, _, errs := runCLI(t, env, "get", srv.URL+"/file", "-o", t.TempDir(), "--conns", "4")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if !strings.Contains(errs, connsWarning) {
		t.Fatalf("missing warning: %q", errs)
	}
	code, _, errs = runCLI(t, env, "get", srv.URL+"/file", "-o", t.TempDir(), "--conns", "auto")
	if code != 0 || strings.Contains(errs, connsWarning) {
		t.Fatalf("auto should not warn: %d %q", code, errs)
	}
}

func TestCLIRm(t *testing.T) {
	srv := faultserver.New(t, faultserver.Options{Size: 10 << 10, Seed: cliSeed, ETag: "v1"})
	env := map[string]string{"SWIFTFETCH_DATA_DIR": t.TempDir()}
	if code, _, e := runCLI(t, env, "get", srv.URL+"/file", "-o", t.TempDir()); code != 0 {
		t.Fatal(e)
	}
	_, out, _ := runCLI(t, env, "list")
	id := strings.Fields(out)[0]
	if code, _, e := runCLI(t, env, "rm", id); code != 0 {
		t.Fatal(e)
	}
	if _, out, _ = runCLI(t, env, "list"); strings.Contains(out, "completed") {
		t.Fatalf("task still listed: %q", out)
	}
}

func TestCLIInvalidURLIsUsageError(t *testing.T) {
	env := map[string]string{"SWIFTFETCH_DATA_DIR": t.TempDir()}
	for _, args := range [][]string{{"get", "ftp://x/y"}, {"get", "notaurl"}, {"explain", "http://"}} {
		if code, _, _ := runCLI(t, env, args...); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
}

func TestConnsWarningAfterValidation(t *testing.T) {
	env := map[string]string{"SWIFTFETCH_DATA_DIR": t.TempDir()}
	_, _, errs := runCLI(t, env, "get", "notaurl", "--conns", "4")
	if strings.Contains(errs, connsWarning) {
		t.Fatalf("warning printed despite invalid URL: %q", errs)
	}
}

func TestApplyMemoryLimit(t *testing.T) {
	prev := debug.SetMemoryLimit(-1)
	defer debug.SetMemoryLimit(prev)
	debug.SetMemoryLimit(1 << 40)
	if applyMemoryLimit(map[string]string{"GOMEMLIMIT": "1GiB"}) || debug.SetMemoryLimit(-1) != 1<<40 {
		t.Fatal("limit applied despite GOMEMLIMIT")
	}
	if !applyMemoryLimit(map[string]string{}) || debug.SetMemoryLimit(-1) != 32<<20 {
		t.Fatal("limit not applied")
	}
}

// startSlowGet runs a rate-limited get with injectable signals.
func startSlowGet(t *testing.T, env map[string]string, srvURL, limit string) (sigs chan os.Signal, exits chan int, code chan int) {
	t.Helper()
	sigs, exits, code = make(chan os.Signal, 2), make(chan int, 4), make(chan int, 1)
	go func() {
		var o, e bytes.Buffer
		code <- runWith(context.Background(), []string{"get", srvURL + "/file", "-o", t.TempDir(), "--limit", limit}, nil, &o, &e, env,
			system{sigs: sigs, exit: func(c int) { exits <- c }})
	}()
	return
}

func TestCLISignals(t *testing.T) {
	srv := faultserver.New(t, faultserver.Options{Size: 2 << 20, Seed: cliSeed, ETag: "v1"})
	env := map[string]string{"SWIFTFETCH_DATA_DIR": t.TempDir()}

	t.Run("first press pauses with exit 3", func(t *testing.T) {
		sigs, exits, code := startSlowGet(t, env, srv.URL, "200K")
		time.Sleep(300 * time.Millisecond)
		sigs <- os.Interrupt
		select {
		case c := <-code:
			if c != 3 {
				t.Fatalf("exit %d, want 3", c)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("run did not stop")
		}
		select {
		case c := <-exits:
			t.Fatalf("immediate exit %d after one press", c)
		default:
		}
	})

	t.Run("press before run registers is not lost", func(t *testing.T) {
		sigs := make(chan os.Signal, 1)
		sigs <- os.Interrupt
		var o, e bytes.Buffer
		c := runWith(context.Background(), []string{"get", srv.URL + "/file", "-o", t.TempDir(), "--limit", "100K"}, nil, &o, &e, env,
			system{sigs: sigs, exit: func(int) {}})
		if c != 3 {
			t.Fatalf("exit %d, want 3", c)
		}
	})

	t.Run("second press exits immediately", func(t *testing.T) {
		sigs, exits, code := startSlowGet(t, env, srv.URL, "200K")
		time.Sleep(300 * time.Millisecond)
		sigs <- os.Interrupt
		sigs <- os.Interrupt
		select {
		case c := <-exits:
			if c != 3 {
				t.Fatalf("exit %d, want 3", c)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("no immediate exit")
		}
		<-code
	})
}

func TestWatchSignalsWindowResets(t *testing.T) {
	sigs := make(chan os.Signal, 2)
	exits := make(chan int, 2)
	var errb bytes.Buffer
	a := &app{stderr: &errb, sys: system{sigs: sigs, exit: func(c int) { exits <- c }}}
	a.eng = a.newEngine(a.cfg)
	done := make(chan struct{})
	defer close(done)
	var n int
	go a.watchSignals("x", func(error) { n++ }, done)
	sigs <- os.Interrupt
	time.Sleep(doublePress + 300*time.Millisecond)
	sigs <- os.Interrupt // new first press, must not exit
	select {
	case c := <-exits:
		t.Fatalf("exited %d on a press after the window", c)
	case <-time.After(300 * time.Millisecond):
	}
	sigs <- os.Interrupt
	select {
	case <-exits:
	case <-time.After(2 * time.Second):
		t.Fatal("second press in window did not exit")
	}
}

func TestCLIResumeRemoteChangedPrompt(t *testing.T) {
	srv := faultserver.New(t, faultserver.Options{Size: 16 << 20, Seed: cliSeed, ETag: "v1"})
	env := map[string]string{"SWIFTFETCH_DATA_DIR": t.TempDir()}
	pause := func() {
		sigs, _, code := startSlowGet(t, env, srv.URL, "4M")
		time.Sleep(1200 * time.Millisecond)
		sigs <- os.Interrupt
		if c := <-code; c != 3 {
			t.Fatalf("pause exit %d", c)
		}
	}
	pause()
	srv.SetOptions(func(o *faultserver.Options) { o.ETag = "v2" })
	_, out, _ := runCLI(t, env, "list")
	id := strings.Fields(out)[0]
	resume := func(in io.Reader) (int, string) {
		var o, e bytes.Buffer
		c := runWith(context.Background(), []string{"resume", id}, in, &o, &e, env, system{sigs: make(chan os.Signal), exit: func(int) {}})
		return c, e.String()
	}
	c, errs := resume(strings.NewReader("n\n"))
	if c != 1 || !strings.Contains(errs, "restart from zero? [y/N]") || !strings.Contains(errs, "--restart-if-changed") {
		t.Fatalf("decline: exit %d stderr %q", c, errs)
	}
	c, errs = resume(strings.NewReader("y\n"))
	if c != 0 {
		t.Fatalf("accept: exit %d stderr %q", c, errs)
	}
}
