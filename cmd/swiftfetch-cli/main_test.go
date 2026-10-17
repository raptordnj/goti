package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/raptordnj/swiftfetch/tests/faultserver"
)

const cliSeed = 7

func runCLI(t *testing.T, env map[string]string, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(context.Background(), args, &out, &errb, env)
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
	if !strings.Contains(errs, "reduce throughput") {
		t.Fatalf("missing warning: %q", errs)
	}
	code, _, errs = runCLI(t, env, "get", srv.URL+"/file", "-o", t.TempDir(), "--conns", "auto")
	if code != 0 || strings.Contains(errs, "reduce throughput") {
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
