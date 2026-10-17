// Command swiftfetch-cli is the command-line front end of the SwiftFetch
// download engine (spec §15).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/raptordnj/swiftfetch/internal/config"
	"github.com/raptordnj/swiftfetch/internal/domain"
	"github.com/raptordnj/swiftfetch/internal/downloader"
	"github.com/raptordnj/swiftfetch/internal/events"
	"github.com/raptordnj/swiftfetch/internal/persistence"
	"github.com/raptordnj/swiftfetch/internal/ratelimit"
	"github.com/raptordnj/swiftfetch/internal/transport"
)

// Exit codes (spec §15).
const (
	exitOK       = 0
	exitFailed   = 1
	exitUsage    = 2
	exitPaused   = 3
	memoryLimit  = 32 << 20
	doublePress  = 2 * time.Second
	maxConnsFlag = 16
)

const usageText = `usage: swiftfetch-cli <command> [flags]

commands:
  get URL [-o DIR] [--conns auto|N] [--limit RATE] [--sha256 HEX] [--on-exists rename|overwrite|fail] [--conservative]
  explain URL
  list
  resume ID [--restart-if-changed]
  rm ID [--delete-partial]
`

func main() {
	if os.Getenv("GOMEMLIMIT") == "" {
		debug.SetMemoryLimit(memoryLimit)
	}
	env := map[string]string{}
	for _, k := range []string{"SWIFTFETCH_DATA_DIR"} {
		if v, ok := os.LookupEnv(k); ok {
			env[k] = v
		}
	}
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr, env))
}

// app is the wired engine plus its output streams.
type app struct {
	eng    *downloader.Engine
	db     *persistence.DB
	bus    *events.Bus
	cfg    config.Config
	stdout io.Writer
	stderr io.Writer
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer, env map[string]string) int {
	if len(args) == 0 {
		outp(stderr, usageText)
		return exitUsage
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "get", "explain", "list", "resume", "rm":
	case "-h", "--help", "help":
		outp(stdout, usageText)
		return exitOK
	default:
		outf(stderr, "unknown command %q\n%s", cmd, usageText)
		return exitUsage
	}

	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		outDir, conns, limit, sha, onExists string
		conservative, restart, delPartial   bool
	)
	switch cmd {
	case "get":
		fs.StringVar(&outDir, "o", "", "destination directory")
		fs.StringVar(&conns, "conns", "auto", "connections: auto or 1-16")
		fs.StringVar(&limit, "limit", "", "rate limit, e.g. 512K, 2M")
		fs.StringVar(&sha, "sha256", "", "expected SHA-256 (hex)")
		fs.StringVar(&onExists, "on-exists", "", "rename, overwrite or fail")
		fs.BoolVar(&conservative, "conservative", false, "conservative mode")
	case "resume":
		fs.BoolVar(&restart, "restart-if-changed", false, "restart from zero if the remote file changed")
	case "rm":
		fs.BoolVar(&delPartial, "delete-partial", false, "also delete the partial temp file")
	}
	pos, err := parseInterspersed(fs, rest)
	if err != nil {
		return exitUsage
	}
	wantPos := 1
	if cmd == "list" {
		wantPos = 0
	}
	if len(pos) != wantPos {
		outf(stderr, "%s: expected %d argument(s), got %d\n%s", cmd, wantPos, len(pos), usageText)
		return exitUsage
	}

	req := downloader.AddRequest{DestDir: outDir, ExpectedSHA256: sha, ExistingPolicy: domain.ExistingFilePolicy(onExists), Conservative: conservative}
	if cmd == "get" {
		if conns != "auto" {
			n, err := strconv.Atoi(conns)
			if err != nil || n < 1 || n > maxConnsFlag {
				outf(stderr, "invalid --conns %q: use auto or 1-%d\n", conns, maxConnsFlag)
				return exitUsage
			}
			req.Connections = n
			outf(stderr, "warning: forcing --conns %d can reduce throughput or trigger server throttling\n", n)
		}
		if limit != "" {
			if req.RateLimit, err = parseRate(limit); err != nil {
				outln(stderr, err)
				return exitUsage
			}
		}
	}

	cfg := config.Default()
	if d := env["SWIFTFETCH_DATA_DIR"]; d != "" {
		cfg.DataDir = d
	}
	cfg.RestartOnRemoteChange = restart
	if err := cfg.Validate(); err != nil {
		outln(stderr, "invalid configuration:", err)
		return exitUsage
	}

	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		outln(stderr, "cannot create data directory:", err)
		return exitFailed
	}
	db, err := persistence.Open(filepath.Join(cfg.DataDir, "swiftfetch.db"))
	if err != nil {
		outln(stderr, "cannot open database:", err)
		return exitFailed
	}
	defer func() { _ = db.Close() }()

	bus := events.NewBus(cfg.ProgressHz, domain.RealClock{})
	a := &app{
		db: db, bus: bus, cfg: cfg, stdout: stdout, stderr: stderr,
		eng: downloader.NewEngine(downloader.EngineDeps{
			Store: db, Config: cfg, Bus: bus, Clock: domain.RealClock{},
			Global: ratelimit.NewBandwidth(cfg.GlobalRateLimit),
			Conns:  ratelimit.NewSemaphore(cfg.GlobalMaxConnections),
		}),
	}

	switch cmd {
	case "get":
		req.URL = pos[0]
		return a.get(ctx, req)
	case "explain":
		return a.explain(ctx, pos[0], req)
	case "list":
		return a.list(ctx)
	case "resume":
		return a.resume(ctx, pos[0])
	default:
		return a.rm(ctx, pos[0], delPartial)
	}
}

// parseInterspersed parses flags that may appear before or after positional
// arguments.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			return pos, nil
		}
		pos = append(pos, args[0])
		args = args[1:]
	}
}

func (a *app) fail(err error) int {
	var de *domain.Error
	if errors.As(err, &de) && de.Action != "" {
		outf(a.stderr, "error: %s (%s)\n", de.Msg, de.Action)
	} else {
		outln(a.stderr, "error:", err)
	}
	return exitFailed
}

func (a *app) get(ctx context.Context, req downloader.AddRequest) int {
	t, err := a.eng.Add(ctx, req)
	if err != nil {
		return a.usageOrFail(err)
	}
	return a.runTask(ctx, t.ID)
}

// usageOrFail maps policy errors from Add (bad flags/values) to exit 2.
func (a *app) usageOrFail(err error) int {
	if domain.KindOf(err) == domain.KindPolicy {
		outln(a.stderr, "error:", err)
		return exitUsage
	}
	return a.fail(err)
}

func (a *app) resume(ctx context.Context, id string) int {
	if _, err := a.db.GetTask(ctx, id); err != nil {
		return a.fail(err)
	}
	return a.runTask(ctx, id)
}

// runTask runs id with progress rendering and Ctrl-C handling.
func (a *app) runTask(ctx context.Context, id string) int {
	sub := a.bus.Subscribe()
	rctx, stop := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		a.renderLoop(rctx, sub, id)
	}()

	sigs := make(chan os.Signal, 2)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	sigDone := make(chan struct{})
	go func() {
		select {
		case <-sigs:
		case <-sigDone:
			return
		}
		a.eng.Pause(id)
		select {
		case <-sigs:
			outln(a.stderr, "\ninterrupted; state will be recovered on next run")
			os.Exit(exitPaused)
		case <-time.After(doublePress):
		case <-sigDone:
		}
	}()

	err := a.eng.Run(ctx, id)
	signal.Stop(sigs)
	close(sigDone)
	stop()
	sub.Close()
	wg.Wait()
	outln(a.stderr)

	switch {
	case err == nil:
		if t, gerr := a.db.GetTask(ctx, id); gerr == nil {
			outf(a.stdout, "completed: %s\n", t.FinalPath)
		}
		return exitOK
	case errors.Is(err, domain.ErrPaused), errors.Is(err, domain.ErrCancelled):
		outf(a.stdout, "paused; resume with: swiftfetch-cli resume %s\n", id)
		return exitPaused
	default:
		if domain.KindOf(err) == domain.KindRemoteChanged && !a.cfg.RestartOnRemoteChange {
			outln(a.stderr, "the remote file changed; rerun with --restart-if-changed to start over")
		}
		return a.fail(err)
	}
}

// renderLoop redraws the progress line for task id.
func (a *app) renderLoop(ctx context.Context, sub *events.Subscription, id string) {
	name, strat := "", domain.Strategy("")
	for {
		ev, err := sub.Next(ctx)
		if err != nil {
			return
		}
		if ev.TaskID() != id {
			continue
		}
		switch e := ev.(type) {
		case events.DecisionMade:
			var d struct {
				Strategy domain.Strategy `json:"strategy"`
			}
			if json.Unmarshal(e.DecisionJSON, &d) == nil {
				strat = d.Strategy
			}
		case events.Warning:
			outf(a.stderr, "\nwarning: %s\n", e.Msg)
		case events.Progress:
			if name == "" {
				if t, err := a.db.GetTask(ctx, id); err == nil {
					name = t.FileName
				}
			}
			outf(a.stderr, "\r\x1b[2K%s", renderProgress(e, name, strat))
		}
	}
}

func (a *app) explain(ctx context.Context, rawURL string, req downloader.AddRequest) int {
	d, obs, err := a.eng.Explain(ctx, rawURL, req)
	if err != nil {
		return a.usageOrFail(err)
	}
	w := a.stdout
	outf(w, "URL:        %s\n", transport.Redact(rawURL))
	if obs.FinalURL != "" {
		outf(w, "Final URL:  %s\n", transport.Redact(obs.FinalURL))
	}
	outf(w, "Strategy:   %s\nConfidence: %s\n", d.Strategy, d.Confidence)
	if d.SizeKnown {
		outf(w, "Size:       %s\n", humanBytes(d.Size))
	} else {
		outln(w, "Size:       unknown")
	}
	outf(w, "Validator:  %s\nConnections: initial %d, max %d\n", d.Validator, d.InitialConns, d.MaxConns)
	outln(w, "Reasons:")
	for _, r := range d.Reasons {
		outf(w, "  - %s\n", r)
	}
	if len(obs.Anomalies) > 0 {
		outln(w, "Anomalies:")
		for _, an := range obs.Anomalies {
			outf(w, "  - %s\n", an)
		}
	}
	if len(d.Probes) > 0 {
		outln(w, "Probes:")
		for _, p := range d.Probes {
			line := fmt.Sprintf("  - bytes %d-%d: status %d, %d bytes, %s", p.Requested.Start, p.Requested.End, p.Status, p.BodyLen, p.RTT.Round(time.Millisecond))
			if p.ContentRange != "" {
				line += ", Content-Range " + p.ContentRange
			}
			if p.Err != "" {
				line += ", error: " + p.Err
			}
			outln(w, line)
		}
	}
	return exitOK
}

func (a *app) list(ctx context.Context) int {
	tasks, err := a.db.ListTasks(ctx)
	if err != nil {
		return a.fail(err)
	}
	for _, t := range tasks {
		prog := humanBytes(t.Downloaded)
		if t.SizeKnown && t.Size > 0 {
			prog = fmt.Sprintf("%.1f%% (%s / %s)", float64(t.Downloaded)*100/float64(t.Size), humanBytes(t.Downloaded), humanBytes(t.Size))
		}
		name := t.FileName
		if name == "" {
			name = "-"
		}
		outf(a.stdout, "%s  %-11s  %s  %s  %s\n", t.ID, t.State, prog, truncateName(name), transport.Redact(t.OriginalURL))
	}
	return exitOK
}

func (a *app) rm(ctx context.Context, id string, deletePartial bool) int {
	if err := a.eng.Remove(ctx, id, deletePartial); err != nil {
		return a.fail(err)
	}
	outf(a.stdout, "removed %s\n", id)
	return exitOK
}

// Output helpers: write errors to the terminal are not actionable.
func outf(w io.Writer, format string, a ...any) { _, _ = fmt.Fprintf(w, format, a...) }
func outln(w io.Writer, a ...any)               { _, _ = fmt.Fprintln(w, a...) }
func outp(w io.Writer, a ...any)                { _, _ = fmt.Fprint(w, a...) }
