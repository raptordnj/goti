# SwiftFetch Core Engine Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the headless SwiftFetch download engine (evidence-based strategy selection, adaptive multipart, crash-safe resume, SQLite persistence) plus a thin CLI, fully tested against a deterministic fault-injection server.

**Architecture:** Pure, deterministic decision code (`strategy.Select`, `strategy.Controller`, `domain` state table) is separated from effectful code (`transport`, `storage`, `persistence`, `downloader`). `downloader.Engine` orchestrates one task per `Run` call; `persistence` implements the `downloader.Store` interface using only `domain` types. A seeded fault server in `tests/faultserver` drives integration and crash tests.

**Tech Stack:** Go 1.27; `modernc.org/sqlite` v1.60.1; `golang.org/x/time` v0.16.0; `golang.org/x/sync` v0.24.0; `golang.org/x/sys` v0.49.0; `github.com/google/uuid` v1.6.0; `golangci-lint` v2.15.0 (tool, not a dependency).

**Spec:** `docs/superpowers/specs/2026-10-17-swiftfetch-core-engine-design.md` — executors read it alongside this plan; section references below (§N) point into it.

## Global Constraints

- Module path `github.com/raptordnj/swiftfetch`; `go.mod` declares `go 1.27`.
- No GUI code, no Gio import, no cgo (`CGO_ENABLED=0` must build).
- Third-party modules limited to the five listed in Tech Stack.
- `domain` imports nothing from the project; `persistence` imports only `domain`; nothing imports `cmd/`.
- All byte offsets/lengths are `int64`; ranges are half-open `[Start, End)`.
- No package-level mutable state; dependencies passed via constructors.
- Every URL in logs, events, stored error messages and CLI output goes through `transport.Redact`.
- TLS verification is never disabled.
- All channels and queues are bounded; publishing events never blocks the engine.
- Tests must pass under `go test -race ./...`; `gofmt`, `go vet`, `golangci-lint run` clean.
- Default config values exactly as spec §13.
- Commit after every task (conventional commit prefixes: `feat:`, `test:`, `chore:`, `docs:`).

## Review Focus

1. **Disk full (ENOSPC) or permission error during `WriteAt`/`Sync`** → task goes to `failed` with `ErrorKind Local` and a "free disk space / check permissions" action; never retried as transient; verified progress preserved. Pinned in Task 22.
2. **Signed URL expired when resuming** (403 on revalidation) → task `failed` with `Auth`, action "URL may have expired — supply a new URL", segments preserved in the DB. Pinned in Task 23.
3. **Ctrl-C during `inspecting`** (before a temp file exists) → task `paused`, later `resume` succeeds without "temp file missing" errors. Pinned in Task 23.
4. **Destination directory missing or not writable** → `Add` fails fast with `Local` error naming the directory; no task row left behind. Pinned in Task 23.
5. **Two tasks resolving to the same filename in the same directory** → distinct temp files (O_EXCL collision picks next suffix) and distinct final names under `rename` policy. Pinned in Task 23.

---

## File Structure

```
go.mod, go.sum, Makefile, .golangci.yml, README.md
cmd/swiftfetch-cli/main.go            arg parsing, wiring, progress printer, signals
cmd/swiftfetch-cli/progress.go        progress line rendering (pure, tested)
internal/domain/state.go              State, Transition
internal/domain/ranges.go             ByteRange, RangeSet
internal/domain/errors.go             ErrorKind, Error
internal/domain/task.go               Task, Validators, Strategy, ExistingFilePolicy, SegmentRecord, Checkpoint, Attempt
internal/domain/clock.go              Clock, RealClock
internal/clocktest/fake.go            FakeClock (test helper package, importable by tests only by convention)
internal/config/config.go             Config, Default(), Validate()
internal/transport/contentrange.go    ParseContentRange, RangeHeader
internal/transport/disposition.go     ParseContentDisposition
internal/transport/redact.go          Redact, IsSignedURL
internal/transport/client.go          NewClient, ClientOptions, address checks, redirect policy
internal/transport/validate.go        ValidateRangeResponse, ReadValidators
internal/storage/filename.go          SanitizeFilename, SafeJoin
internal/storage/tempfile.go          CreateTemp, OpenTemp, TempFile, HashRange
internal/storage/finalize.go          Finalize, nextFreeName
internal/storage/rename_linux.go      renameNoReplace (renameat2, link+unlink fallback)
internal/storage/rename_darwin.go     renameNoReplace (renamex_np RENAME_EXCL)
internal/storage/rename_windows.go    renameNoReplace (MoveFileEx)
internal/storage/rename_other.go      renameNoReplace (link+unlink) for remaining unix
internal/storage/sync_unix.go / sync_windows.go   syncDir
internal/ratelimit/bandwidth.go       Bandwidth, WaitAll
internal/ratelimit/semaphore.go       Semaphore
internal/events/events.go             Event types
internal/events/bus.go                Bus, Subscription
internal/strategy/observations.go     Observations, ProbeResult
internal/strategy/inspector.go        Inspector
internal/strategy/selector.go         Select, Decision, ResumeState, ChunkSize
internal/strategy/controller.go       Controller, Sample, Action
internal/persistence/sqlite.go        Open, DB methods
internal/persistence/migrations/0001_init.sql
internal/downloader/store.go          Store interface
internal/downloader/scheduler.go      Segment, Scheduler
internal/downloader/retry.go          Backoff, Classify
internal/downloader/sink.go           segment write path (rate limit → WriteAt → hash → Pos)
internal/downloader/single.go         runSingle
internal/downloader/multipart.go      runMultipart, worker loop, fill mode
internal/downloader/checkpoint.go     checkpointer
internal/downloader/recovery.go       recoverSegments
internal/downloader/engine.go         Engine, EngineDeps, AddRequest, Run, Pause, Cancel, Explain, Remove
tests/faultserver/server.go           Server, Options, content generator
tests/integration/*_test.go
tests/faultinjection/*_test.go
```

---

### Task 1: Module scaffold and state machine

**Files:**
- Create: `go.mod`, `Makefile`, `.golangci.yml`, `.gitignore`, `internal/domain/state.go`
- Test: `internal/domain/state_test.go`

**Interfaces:**
- Produces: `type State string` with constants `StateQueued="queued"`, `StateInspecting`, `StateDownloading`, `StatePaused`, `StateRetrying`, `StateVerifying`, `StateCompleted`, `StateFailed`, `StateCancelled`; `var AllStates []State`; `func Transition(from, to State) error`; `var ErrInvalidTransition = errors.New(...)`; `func (s State) Terminal() bool`; `func ParseState(string) (State, error)`.

- [ ] **Step 1:** `go mod init github.com/raptordnj/swiftfetch`, set `go 1.27`. `.gitignore`: `/bin/`, `*.swiftfetch-part`, `coverage.out`. Makefile targets: `test` (`go test ./...`), `race` (`go test -race ./...`), `lint` (`golangci-lint run`), `fuzz` (each Fuzz target `-fuzztime=30s`), `build` (`CGO_ENABLED=0 go build -o bin/swiftfetch-cli ./cmd/swiftfetch-cli`), `cross` (linux/windows/darwin × amd64/arm64 into `bin/`). `.golangci.yml` (v2 format) enabling `govet, staticcheck, errcheck, ineffassign, unused, gosec, revive`; exclude `gosec` G304 (file paths from config are intentional) with a comment saying why. Install linter: `go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0`.

- [ ] **Step 2: Write the failing test** — table of allowed pairs copied from spec §3.1; loop over all 81 `(from,to)` pairs:

```go
func TestTransitionTableExhaustive(t *testing.T) {
	allowed := map[State][]State{
		StateQueued:      {StateInspecting, StatePaused, StateCancelled},
		StateInspecting:  {StateDownloading, StateRetrying, StateFailed, StatePaused, StateCancelled},
		StateDownloading: {StateVerifying, StateRetrying, StatePaused, StateFailed, StateCancelled},
		StateRetrying:    {StateInspecting, StatePaused, StateFailed, StateCancelled},
		StatePaused:      {StateQueued, StateCancelled},
		StateVerifying:   {StateCompleted, StateDownloading, StateFailed, StateCancelled},
		StateFailed:      {StateQueued, StateCancelled},
	}
	for _, from := range AllStates {
		for _, to := range AllStates {
			err := Transition(from, to)
			want := slices.Contains(allowed[from], to)
			if want && err != nil { t.Errorf("%s->%s: unexpected %v", from, to, err) }
			if !want && !errors.Is(err, ErrInvalidTransition) { t.Errorf("%s->%s: want ErrInvalidTransition, got %v", from, to, err) }
		}
	}
}
```
Plus `TestTerminal` (only completed, cancelled) and `TestParseState` (round-trip all, reject `"bogus"`).

- [ ] **Step 3:** Run `go test ./internal/domain/` → FAIL (undefined).
- [ ] **Step 4:** Implement `internal/domain/state.go` with the table as a `map[State]map[State]bool`.
- [ ] **Step 5:** Run `go test ./internal/domain/ && go vet ./...` → PASS.
- [ ] **Step 6:** Commit `chore: scaffold module; feat(domain): state machine`.

---

### Task 2: ByteRange and RangeSet

**Files:**
- Create: `internal/domain/ranges.go`
- Test: `internal/domain/ranges_test.go`

**Interfaces:**
- Produces: `type ByteRange struct{ Start, End int64 }`; `func NewRange(start, end int64) (ByteRange, error)` (rejects negative, start>end); `func (r ByteRange) Len() int64`; `func (r ByteRange) Empty() bool`; `type RangeSet struct{...}` (zero value usable); `func (s *RangeSet) Add(r ByteRange)`; `func (s *RangeSet) Contains(r ByteRange) bool`; `func (s *RangeSet) Len() int64`; `func (s *RangeSet) Missing(total int64) []ByteRange`; `func (s *RangeSet) Ranges() []ByteRange` (copy); `func AddInt64(a, b int64) (int64, bool)` (overflow-checked).

- [ ] **Step 1: Write failing tests**: `TestNewRangeRejects` (`(-1,5)`, `(5,4)` error; `(5,5)` ok, empty); `TestRangeSetCoalesce` (`Add[0,10)`, `Add[10,20)` → `Ranges()==[{0,20}]`; `Add[30,40)`, `Add[5,35)` → `[{0,40}]`); `TestMissing` (set `{[0,10),[20,30)}`, total 40 → `[{10,20},{30,40}]`; empty set total 0 → `nil`); `TestAddInt64Overflow` (`AddInt64(math.MaxInt64, 1)` → `ok=false`); `TestRangeSetLargeOffsets` (ranges around `5<<30` behave identically); and the property test:

```go
func TestRangeSetProperties(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	for iter := 0; iter < 2000; iter++ {
		const total = 1000
		var s RangeSet
		bitmap := make([]bool, total)
		for k := 0; k < 20; k++ {
			a, b := r.Int64N(total), r.Int64N(total)
			if a > b { a, b = b, a }
			s.Add(ByteRange{a, b})
			for i := a; i < b; i++ { bitmap[i] = true }
		}
		// invariants: sorted, disjoint, non-adjacent; Len and Missing agree with bitmap
		...
	}
}
```
- [ ] **Step 2:** Run → FAIL. **Step 3:** Implement (sorted slice; `Add` merges overlapping/adjacent via binary search). **Step 4:** Run → PASS. **Step 5:** Commit `feat(domain): byte ranges and range set`.

---

### Task 3: Errors, task types, clock

**Files:**
- Create: `internal/domain/errors.go`, `internal/domain/task.go`, `internal/domain/clock.go`, `internal/clocktest/fake.go`
- Test: `internal/domain/errors_test.go`, `internal/clocktest/fake_test.go`

**Interfaces:**
- Produces (errors): `type ErrorKind int` with `KindTransient, KindPermanent, KindAuth, KindIntegrity, KindCancelled, KindRemoteChanged, KindPolicy, KindLocal` and `String()`; `type Error struct{ Kind ErrorKind; Msg string; Action string; RetryAfter time.Duration; Err error }` implementing `Error()` and `Unwrap()`; `func NewError(kind ErrorKind, msg, action string, cause error) *Error`; `func KindOf(err error) ErrorKind` (walks `errors.As`; `context.Canceled` → `KindCancelled`; unknown → `KindTransient`); sentinel causes `var ErrPaused, ErrCancelled = errors.New("paused"), errors.New("cancelled")`.
- Produces (task): `type Strategy string` (`StrategySingle="single"`, `StrategyMultipart="multipart"`, `StrategyConservative="conservative"`, `StrategyResume="resume"`, `StrategyUnsupported="unsupported"`); `type ExistingFilePolicy string` (`PolicyRename="rename"`, `PolicyOverwrite="overwrite"`, `PolicyFail="fail"`); `type NetworkPolicy string` (`NetAllowDirectPrivate="allow-direct-private"`, `NetAllowAll="allow-all"`, `NetBlockPrivate="block-private"`) — lives in `domain` so `transport` keeps depending only on `domain`; `type Validators struct{ ETag string; ETagStrong bool; LastModified string }` with `func (v Validators) Kind() ValidatorKind` and `func (v Validators) Matches(o Validators) bool` (strong ETags compare exactly; else Last-Modified equal and non-empty; none → false); `type ValidatorKind string` (`ValidatorStrongETag`, `ValidatorLastModified`, `ValidatorNone`); `type Task struct{ ID, OriginalURL, FinalURL, DestDir, FileName, TempPath, FinalPath string; Size int64; SizeKnown bool; Validators Validators; Strategy Strategy; DecisionJSON []byte; State State; LastErrorKind ErrorKind; LastErrorMsg string; ExpectedSHA256 string; ExistingPolicy ExistingFilePolicy; SettingsJSON []byte; VerifyResult string; Downloaded int64; CreatedAt, UpdatedAt time.Time }`; `type SegmentRecord struct{ Start, End, Pos int64; Done bool; Hash, HasherState []byte }`; `type Checkpoint struct{ Segments []SegmentRecord; Downloaded int64 }`; `type Attempt struct{ At time.Time; Kind ErrorKind; Message string; Delay time.Duration }`.
- Produces (clock): `type Clock interface{ Now() time.Time; After(d time.Duration) <-chan time.Time; Sleep(ctx context.Context, d time.Duration) error }`; `type RealClock struct{}`; `clocktest.New(start time.Time) *FakeClock` with the same methods plus `Advance(d time.Duration)` and `Waiters() int` (number of pending `After`/`Sleep` timers; lets tests advance only once a goroutine is blocked).

- [ ] **Step 1: Failing tests:** `TestKindOf` — wrapped `*Error{Kind: KindAuth}` via `fmt.Errorf("x: %w", e)` → `KindAuth`; `context.Canceled` → `KindCancelled`; `errors.New("x")` → `KindTransient`. `TestValidatorsMatches` table (strong==strong same → true; strong vs different → false; weak ETags with equal LM → true via LM; both empty → false). `TestFakeClockSleep` — goroutine `Sleep(ctx, 5s)`; poll until `Waiters()==1`; `Advance(4s)` → still blocked; `Advance(1s)` → returns nil; cancelled ctx → returns `ctx.Err()`.
- [ ] **Step 2:** Run → FAIL. **Step 3:** Implement. **Step 4:** Run → PASS (`-race`). **Step 5:** Commit `feat(domain): errors, task types, clock`.

---

### Task 4: Config defaults and validation

**Files:**
- Create: `internal/config/config.go`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Consumes: `domain.NetworkPolicy`, `domain.ExistingFilePolicy` (Task 3).
- Produces: `type Config struct` with fields named exactly as spec §13 rows: `MaxConnections, GlobalMaxConnections, InitialConnections int` (0 = auto); `AdaptiveConcurrency bool`; `MinMultipartSize, ChunkSizeMin, ChunkSizeMax int64`; `GlobalRateLimit, PerDownloadRateLimit int64`; `RetryBaseDelay, RetryMaxDelay time.Duration; RetryFactor float64`; `MaxSegmentAttempts, TaskRetryBudget int`; `StallTimeout, CheckpointInterval, ProbeTimeout time.Duration`; `CheckpointBytes int64`; `ExistingFilePolicy domain.ExistingFilePolicy`; `RestartOnRemoteChange, ConservativeMode, AllowHTTPSDowngrade bool`; `NetworkPolicy domain.NetworkPolicy`; `ProgressHz int`; `DownloadDir, DataDir string`. `func Default() Config`; `func (c Config) Validate() error` (returns `errors.Join` of `FieldError{Field, Msg}`); `func DefaultDownloadDir() string`; `func DefaultDataDir() string`.

- [ ] **Step 1: Failing tests:** `TestDefaultsMatchSpec` asserting each §13 default (`MaxConnections==8`, `GlobalMaxConnections==16`, `MinMultipartSize==8<<20`, `ChunkSizeMin==1<<20`, `ChunkSizeMax==64<<20`, `RetryBaseDelay==500ms`, `RetryMaxDelay==60s`, `RetryFactor==2`, `MaxSegmentAttempts==8`, `TaskRetryBudget==50`, `StallTimeout==30s`, `CheckpointInterval==2s`, `CheckpointBytes==16<<20`, `ProbeTimeout==15s`, `ExistingFilePolicy==PolicyRename`, `NetworkPolicy==domain.NetAllowDirectPrivate`, `ProgressHz==4`, booleans false except `AdaptiveConcurrency`); `TestDefaultValidates`; `TestValidateRejects` table — `MaxConnections: 0` and `17`, `GlobalMaxConnections: 65`, `MinMultipartSize: 1<<19`, `ChunkSizeMin > ChunkSizeMax`, negative rate limit, `StallTimeout: 4s` and `601s`, `CheckpointInterval: 400ms` and `31s`, `CheckpointBytes: 1<<19`, `ProbeTimeout: 0` and `121s`, `ProgressHz: 0` and `31`, `InitialConnections > MaxConnections`, unknown policy strings — each error mentions the field name. `TestDefaultDataDir` (with `XDG_DATA_HOME` set via `t.Setenv` on linux → `$XDG_DATA_HOME/swiftfetch`).
- [ ] **Step 2–4:** FAIL → implement (DataDir per OS per §13 using `runtime.GOOS`, `os.UserHomeDir`, `os.Getenv("LOCALAPPDATA")`) → PASS.
- [ ] **Step 5:** Commit `feat(config): defaults and validation`.

---

### Task 5: Content-Range and Content-Disposition parsing

**Files:**
- Create: `internal/transport/contentrange.go`, `internal/transport/disposition.go`
- Test: `internal/transport/contentrange_test.go`, `internal/transport/disposition_test.go`

**Interfaces:**
- Produces: `type ContentRange struct{ Range domain.ByteRange; Total int64; TotalKnown bool; Unsatisfied bool }`; `func ParseContentRange(h string) (ContentRange, error)` — accepts `bytes a-b/N`, `bytes a-b/*`, `bytes */N` (Unsatisfied); rejects b<a, b≥N, negatives, overflow, missing unit, extra garbage; `Range` is half-open (`End = b+1`). `func RangeHeader(r domain.ByteRange) string` → `"bytes=%d-%d"` with `End-1`; `func OpenRangeHeader(start int64) string` → `"bytes=%d-"`. `func ParseContentDisposition(h string) (name string, ok bool)` — prefers `filename*=UTF-8''…` (percent-decoded, charset UTF-8 or ISO-8859-1), else `filename="…"` or token; uses `mime.ParseMediaType`; returns raw (unsanitized) name.

- [ ] **Step 1: Failing tests:** table for ParseContentRange: `"bytes 0-0/100"` → `{0,1}`, total 100; `"bytes 5368709120-5368709121/5368709122"` → large ok; `"bytes 0-9/*"` → TotalKnown=false; `"bytes */0"` → Unsatisfied, Total 0; errors for `"bytes 9-0/100"`, `"bytes 0-100/100"`, `"bytes -1-5/10"`, `"0-5/10"`, `"bytes 0-5/10 junk"`, `"bytes 0-99999999999999999999/1"`. `TestRangeHeader` (`{0,1}` → `bytes=0-0`). Disposition: `attachment; filename="a b.txt"` → `a b.txt`; `attachment; filename*=UTF-8''%E2%82%AC%20rates.pdf; filename="x.pdf"` → `€ rates.pdf`; `inline` → ok=false; garbage → ok=false. Fuzz targets `FuzzParseContentRange` (never panics; if ok then `0 ≤ Start < End`, `!TotalKnown || End ≤ Total`) and `FuzzParseContentDisposition` (never panics).
- [ ] **Step 2–4:** FAIL → implement → PASS; run each fuzz target 30s.
- [ ] **Step 5:** Commit `feat(transport): range and disposition parsing`.

---

### Task 6: Redaction and signed-URL detection

**Files:**
- Create: `internal/transport/redact.go`
- Test: `internal/transport/redact_test.go`

**Interfaces:**
- Produces: `func Redact(raw string) string` (parse failure → `"<unparseable url>"`; strips userinfo; replaces values of sensitive keys with `REDACTED`; keeps scheme/host/path); `func IsSignedURL(u *url.URL) bool`; `var signedKeys` and `var sensitiveKeys` (case-insensitive) exactly the lists in spec §4.1 step 5 and §11.5.

- [ ] **Step 1: Failing tests:** `Redact("https://user:pw@h/p?X-Amz-Signature=abc&x=1")` → `"https://h/p?X-Amz-Signature=REDACTED&x=1"`; `access_token`, `api_key`, `password` redacted, case-insensitive (`X-AMZ-SIGNATURE`); `IsSignedURL` true for `?sig=..&se=..`, false for `?page=2`. `FuzzRedact`: for any input containing `password=<secret>` with secret of ≥6 `[a-z]` chars, output does not contain the secret; never panics.
- [ ] **Step 2–4:** FAIL → implement → PASS + fuzz 30s.
- [ ] **Step 5:** Commit `feat(transport): url redaction`.

---

### Task 7: Fault server

Built before the networking code that it tests.

**Files:**
- Create: `tests/faultserver/server.go`, `tests/faultserver/content.go`
- Test: `tests/faultserver/server_test.go`

**Interfaces:**
- Produces: `func Byte(seed uint64, i int64) byte` (deterministic content: low byte of a splitmix64 of `seed^i`); `func ExpectedSHA256(seed uint64, size int64) [32]byte`; `type Options struct` with fields:
  `Size int64; Seed uint64; UnknownLength bool; IgnoreRanges bool; MalformedContentRange bool; WrongTotal bool; ShortBodyBy int64; LongBodyBy int64; ETag string; WeakETag bool; LastModified time.Time; NoValidators bool; ChangeETagAfter int; RedirectChain int; RedirectTo string; ExpireAfter time.Duration; StatusOverride int; FailFirstN int; FailStatus int; RetryAfter time.Duration; BytesPerSecondPerConn int64; BytesPerSecondGlobal int64; JitterMax time.Duration; ResetAfterBytes int64; ResetEveryNth int; CorruptAt []int64; CloseAfterBytes int64; MaxConcurrent int; RequireAuth string; RangeIgnoredAfter int; WritePiece int` (bytes per write/flush, default 16 KiB).
  `type Server struct{ URL string; ... }`; `func New(t testing.TB, opts Options) *Server` (registers `t.Cleanup`); `func (s *Server) Requests() []RequestLog` where `RequestLog{Method, Range, IfRange string; Status int}`; `func (s *Server) ActiveMax() int` (peak concurrent requests); `func (s *Server) SetOptions(func(*Options))` (mutate between phases, under lock).
- Behaviour: `/file` serves content; `/redirect/N` chains to `/file`; ranges honour `If-Range` (strong-ETag mismatch → 200 full); `ChangeETagAfter` flips ETag (and content seed+1) after K requests; `MaxConcurrent` exceeded → 503 with `Retry-After`; throttling via per-connection and global `rate.Limiter` while writing in `WritePiece`-sized pieces with a flush after each; `ResetAfterBytes` hijacks and closes the connection; `ExpireAfter` → 403 after duration since start; `RequireAuth` expects `Authorization` header value else 401.

- [ ] **Step 1: Failing tests:** `TestFullBodyMatchesGenerator` (GET `/file`, Size 100_000 → SHA-256 equals `ExpectedSHA256`); `TestRangeResponse` (`Range: bytes=10-19` → 206, `Content-Range: bytes 10-19/100000`, body equals generator bytes 10..19); `TestIgnoreRanges` → 200; `TestChangeETagAfter` (ETag differs on request 3 when `ChangeETagAfter: 2`); `TestMaxConcurrent` (3 parallel slow requests with `MaxConcurrent: 2` → one 503); `TestThrottle` (`BytesPerSecondPerConn: 200_000`, 400 KB body takes ≥ 1.5 s); `TestFiveGiBSparse` (range at `5<<30 - 10` returns correct generator bytes without allocating the body).
- [ ] **Step 2–4:** FAIL → implement → PASS.
- [ ] **Step 5:** Commit `test: deterministic fault server`.

---

### Task 8: HTTP client, address policy, redirect policy, response validation

**Files:**
- Create: `internal/transport/client.go`, `internal/transport/validate.go`
- Test: `internal/transport/client_test.go`, `internal/transport/validate_test.go`

**Interfaces:**
- Consumes: `domain.NetworkPolicy`, `domain.Validators`, `domain.Error`, `ParseContentRange`, `Redact`.
- Produces: `type ClientOptions struct{ Policy domain.NetworkPolicy; AllowPrivate bool; AllowHTTPSDowngrade bool; MaxRedirects int; RoundTripper http.RoundTripper }`; `func NewClient(opts ClientOptions) *http.Client` (when `RoundTripper` nil: cloned default transport with `DialContext` using a `net.Dialer{Control: checkAddr}`, `MaxResponseHeaderBytes: 1<<20`, `ForceAttemptHTTP2: true`; `CheckRedirect` enforces `MaxRedirects` (default 10), downgrade rule, and strips `Authorization` on host change); `func HostIsPrivate(ctx context.Context, r *net.Resolver, u *url.URL) (bool, error)` (true if literal or any resolved IP is non-public); `func isNonPublic(ip netip.Addr) bool` (loopback, private, link-local unicast/multicast, unspecified, ULA, CGNAT `100.64.0.0/10`, IPv4-mapped forms); `func ReadValidators(h http.Header) domain.Validators`; `func ValidateRangeResponse(resp *http.Response, want domain.ByteRange, total int64, v domain.Validators) error` — returns `KindRemoteChanged` on total/validator mismatch, `KindIntegrity` on interval mismatch or 200, nil on exact match. Policy semantics: `NetAllowAll` never blocks; `NetBlockPrivate` always blocks non-public; `NetAllowDirectPrivate` blocks non-public unless `AllowPrivate` (caller sets it to `HostIsPrivate(original URL)`). Blocked dials return `*domain.Error{Kind: KindPolicy}`.

- [ ] **Step 1: Failing tests:** `TestIsNonPublic` table (`127.0.0.1`, `10.1.2.3`, `192.168.0.1`, `169.254.1.1`, `::1`, `fd00::1`, `100.64.0.1`, `::ffff:10.0.0.1` → true; `8.8.8.8`, `2606:4700::1` → false). `TestDialBlocksPrivateWhenNotAllowed` (fault server on 127.0.0.1; `NetAllowDirectPrivate, AllowPrivate:false` → GET fails, `domain.KindOf(err)==KindPolicy`); `TestDialAllowsDirectPrivate` (`AllowPrivate:true` → 200); `TestRedirectLimit` (`RedirectChain: 11` → error; 10 → ok); `TestHTTPSDowngradeBlocked` (TLS fault server via `httptest.NewTLSServer` redirecting to plain `http://` → `KindPolicy`); `TestAuthorizationStrippedCrossHost` (redirect from `127.0.0.1` to `localhost` host; second server records no `Authorization`). `TestValidateRangeResponse` table: exact 206 → nil; 200 → Integrity; wrong interval → Integrity; wrong total → RemoteChanged; ETag differs → RemoteChanged; LM-only validators equal → nil.
- [ ] **Step 2–4:** FAIL → implement → PASS.
- [ ] **Step 5:** Commit `feat(transport): client with address and redirect policy`.

---

### Task 9: Filename sanitization and safe join

**Files:**
- Create: `internal/storage/filename.go`
- Test: `internal/storage/filename_test.go`

**Interfaces:**
- Produces: `func SanitizeFilename(name string) string`; `func SafeJoin(dir, name string) (string, error)` (error if result escapes `dir`); `const maxNameBytes = 200`.

- [ ] **Step 1: Failing tests** (table): `"../../etc/passwd"` → `"passwd"` (take last element after splitting on `/` and `\`) ; `"a/b\\c.txt"` → `"c.txt"`; `"..."` → `"download"`; `".bashrc"` → `"bashrc"`; `"CON"` → `"CON_"`; `"con.txt"` → `"con_.txt"`; `"LPT9.log"` → `"LPT9_.log"`; `"a<b>:c|d?e*.txt"` → `"a_b__c_d_e_.txt"`; `"name. . "` → `"name"`; `"\x00\x1fx\u0085y"` → `"xy"`; `""` → `"download"`; a 300-byte name of `"é"` repeated + `".tar.gz"` → ≤200 bytes, valid UTF-8, ends with `".tar.gz"`. `TestSafeJoin` (`SafeJoin("/d", "x")` ok; `SafeJoin("/d", "..")` error). `FuzzSanitizeFilename`: output non-empty, valid UTF-8, ≤200 bytes, contains no `/`, `\`, NUL, and `SafeJoin(t.TempDir(), out)` succeeds.
- [ ] **Step 2–4:** FAIL → implement → PASS + fuzz 30s.
- [ ] **Step 5:** Commit `feat(storage): filename sanitization`.

---

### Task 10: Temp file, positional writes, range hashing

**Files:**
- Create: `internal/storage/tempfile.go`
- Test: `internal/storage/tempfile_test.go`

**Interfaces:**
- Produces: `type TempFile struct` wrapping `*os.File` with `WriteAt(p []byte, off int64) (int, error)`, `ReadAt`, `Sync() error`, `Close() error`, `Path() string`; `func CreateTemp(dir, base string, size int64, sizeKnown bool) (*TempFile, error)` — path `<dir>/<base>.swiftfetch-part`, `O_CREATE|O_EXCL|O_RDWR`, mode 0644; on `EEXIST` tries `<base> (1).swiftfetch-part` … up to 1000; truncates to `size` when known; on Linux best-effort `unix.Fallocate(fd, FALLOC_FL_KEEP_SIZE, 0, size)` (ignore error) in `prealloc_linux.go`, no-op `prealloc_other.go`; `func OpenTemp(path string) (*TempFile, error)` (no create); `func HashRange(r io.ReaderAt, br domain.ByteRange) ([]byte, error)` (SHA-256 over the range, 1 MiB buffer); `func ResumeHasher(state []byte) (hash.Hash, error)` (new SHA-256, `UnmarshalBinary(state)`).
- Failures from the OS are wrapped as `*domain.Error{Kind: KindLocal, Action: "check free disk space and write permission for <dir>"}`.

- [ ] **Step 1: Failing tests:** `TestCreateTempPreallocates` (size 10 MiB → `Stat().Size()==10<<20`); `TestCreateTempExclusive` (two creates with same base → second path ends `" (1).swiftfetch-part"`); `TestConcurrentWriteAt` (16 goroutines each write a distinct 64 KiB pattern block at its offset under `-race`; read back equals expected); `TestHashRange` (equals `sha256.Sum256` of the slice); `TestResumeHasher` (hash first half, `MarshalBinary`, `ResumeHasher`, write second half → `Sum` equals full hash); `TestCreateTempReadOnlyDir` (chmod 0500 dir, skip on Windows/root → `KindOf(err)==KindLocal`).
- [ ] **Step 2–4:** FAIL → implement → PASS.
- [ ] **Step 5:** Commit `feat(storage): temp files and range hashing`.

---

### Task 11: No-replace finalization

**Files:**
- Create: `internal/storage/finalize.go`, `rename_linux.go`, `rename_darwin.go`, `rename_windows.go`, `rename_other.go` (`//go:build unix && !linux && !darwin`), `sync_unix.go`, `sync_windows.go`
- Test: `internal/storage/finalize_test.go`

**Interfaces:**
- Produces: `func Finalize(tempPath, destPath string, policy domain.ExistingFilePolicy) (finalPath string, err error)`; `func NextFreeName(path string, n int) string` (`/d/a.tar.gz`, 1 → `/d/a (1).tar.gz` — treat `.tar.gz`, `.tar.xz`, `.tar.bz2`, `.tar.zst` as one extension); internal `renameNoReplace(old, new string) error` returning an error satisfying `errors.Is(err, fs.ErrExist)` on conflict; `syncDir(dir string) error` (no-op on Windows).
- Algorithm: `PolicyOverwrite` → `os.Rename` (Windows: `MoveFileEx(REPLACE_EXISTING|WRITE_THROUGH)`). `PolicyFail` → `renameNoReplace` once; conflict → `*domain.Error{Kind: KindLocal, Msg: "destination exists"}`. `PolicyRename` → try `dest`, then `NextFreeName(dest, 1..1000)` until no conflict. Linux: `unix.Renameat2(AT_FDCWD, old, AT_FDCWD, new, RENAME_NOREPLACE)`; on `ENOSYS`/`EINVAL` fall back to `os.Link` + `os.Remove(old)`. Darwin: `unix.RenamexNp(old, new, unix.RENAME_EXCL)`. Then `syncDir(filepath.Dir(final))`.

- [ ] **Step 1: Failing tests:** `TestFinalizeNoConflict`; `TestFinalizeRenamePolicy` (existing `a.txt` with content "keep" → final `a (1).txt`, original still "keep"); `TestFinalizeTarGz` (`x.tar.gz` exists → `x (1).tar.gz`); `TestFinalizeFailPolicy` (error, both files intact); `TestFinalizeOverwrite` (replaced); `TestFinalizeRaceNoOverwrite` (20 goroutines finalize different temp files to the same dest with `PolicyRename` → 20 distinct final files, all contents intact).
- [ ] **Step 2–4:** FAIL → implement → PASS. Also run `GOOS=windows go vet ./internal/storage/ && GOOS=darwin go vet ./internal/storage/ && GOOS=freebsd go vet ./internal/storage/`.
- [ ] **Step 5:** Commit `feat(storage): no-replace atomic finalization`.

---

### Task 12: Rate limiting and connection semaphore

**Files:**
- Create: `internal/ratelimit/bandwidth.go`, `internal/ratelimit/semaphore.go`
- Test: `internal/ratelimit/ratelimit_test.go`

**Interfaces:**
- Produces: `type Bandwidth struct`; `func NewBandwidth(bytesPerSec int64) *Bandwidth` (0 = unlimited; burst 64 KiB); `func (b *Bandwidth) SetLimit(bytesPerSec int64)`; `func (b *Bandwidth) WaitN(ctx context.Context, n int) error` (splits `n` into ≤ burst pieces; nil receiver = unlimited); `func WaitAll(ctx context.Context, n int, ls ...*Bandwidth) error`; `type Semaphore struct`; `func NewSemaphore(n int) *Semaphore`; `func (s *Semaphore) Acquire(ctx context.Context) error`; `func (s *Semaphore) TryAcquire() bool`; `func (s *Semaphore) Release()`; `func (s *Semaphore) InUse() int`.

- [ ] **Step 1: Failing tests:** `TestBandwidthSharedAcrossWorkers` (limit 1 MB/s, 4 goroutines each `WaitN` 512 KiB total in 32 KiB calls through the *same* limiter → elapsed ≥ 1.8 s, proving the limit is not multiplied); `TestWaitAllUsesStricter` (global 2 MB/s, per-download 500 KB/s, 1 MB → ≥ 1.8 s); `TestUnlimited` (0 → < 50 ms for 100 MB); `TestWaitNLargerThanBurst` (n = 1 MiB with limit 1 MiB/s does not error); `TestSemaphore` (cap 2: two acquire, third blocks until release; ctx cancel → error; `InUse` correct).
- [ ] **Step 2–4:** FAIL → implement over `golang.org/x/time/rate` → PASS.
- [ ] **Step 5:** Commit `feat(ratelimit): shared bandwidth limits and connection semaphore`.

---

### Task 13: Event bus with coalescing

**Files:**
- Create: `internal/events/events.go`, `internal/events/bus.go`
- Test: `internal/events/bus_test.go`

**Interfaces:**
- Consumes: `domain.State`, `domain.ErrorKind`, `domain.Clock`.
- Produces: `type Event interface{ TaskID() string }` implemented by `StateChanged{ID string; From, To domain.State; Reason string}`, `Progress{ID string; Downloaded, Total int64; TotalKnown bool; SpeedBps float64; ActiveConns int; ETA *time.Duration}`, `DecisionMade{ID string; Summary string; DecisionJSON []byte}`, `RetryScheduled{ID string; Attempt int; Kind domain.ErrorKind; Delay time.Duration; Reason string}`, `StrategyChanged{ID string; From, To domain.Strategy; Reason string}`, `Warning{ID, Msg string}`, `Resync{}` (`TaskID()==""`). `func NewBus(progressHz int, clock domain.Clock) *Bus`; `func (b *Bus) Publish(e Event)` (never blocks); `func (b *Bus) Subscribe() *Subscription`; `func (s *Subscription) Next(ctx context.Context) (Event, error)`; `func (s *Subscription) Close()`; `const queueCap = 256`.
- Semantics: non-progress events FIFO; progress stored latest-wins per task; `Next` returns a queued non-progress event if any, else the progress of a task whose last delivery was ≥ `1/progressHz` ago, else blocks (waking on publish or clock). Overflow of the 256 queue → clear queue and pending progress, enqueue one `Resync`.

- [ ] **Step 1: Failing tests (FakeClock):** `TestProgressCoalesced` (publish 100 progress events for task A within one tick → one `Next` yields only the last; second `Next` blocks until `Advance(250ms)`); `TestStateEventsNotDropped` (100 `StateChanged` → all 100 in order); `TestOverflowResync` (300 `StateChanged` with no reader → first `Next` is `Resync`); `TestPublishNeverBlocks` (no subscriber reading, 10_000 publishes complete < 1s); `TestStateBeforeProgress` (interleaved → state event delivered first); `TestCloseUnblocksNext`.
- [ ] **Step 2–4:** FAIL → implement → PASS (`-race`).
- [ ] **Step 5:** Commit `feat(events): bounded coalescing event bus`.

---

### Task 14: Inspector (probing)

**Files:**
- Create: `internal/strategy/observations.go`, `internal/strategy/inspector.go`
- Test: `internal/strategy/inspector_test.go`

**Interfaces:**
- Consumes: `transport.*` (Task 5, 6, 8), `domain.*`, fault server.
- Produces: `type ProbeResult struct{ Requested domain.ByteRange; Status int; ContentRange string; BodyLen int64; Validators domain.Validators; RTT time.Duration; Err string }`; `type Observations struct{ OriginalURL, FinalURL string; Redirects []string (redacted); Proto string; Status int; Size int64; SizeKnown bool; ContentType string; Validators domain.Validators; Probes []ProbeResult; RangesProven bool; Anomalies []string; SignedURL bool; Dynamic bool; ProbeRetries int; ProbeTimeouts int; FileName string; FileNameSource string; Err *domain.Error }`; `type Inspector struct{ Client *http.Client; Cfg config.Config; Clock domain.Clock }`; `func (in *Inspector) Inspect(ctx context.Context, rawURL string, known *domain.Validators) (Observations, error)` — `known` (resume) sets `If-Range` on probe 2 when strong; returns `error` only for invalid URL or ctx cancellation; HTTP-level failures land in `Observations.Err` with kinds per spec §4.1. Retries 429/503 per `Retry-After` using `Clock.Sleep`, up to 3 attempts, counting `ProbeRetries`.
- Algorithm: exactly spec §4.1 steps 1–7. Body reads via `io.LimitReader(body, want+1)`; always `resp.Body.Close()`. For a 200 response do not read the body.

- [ ] **Step 1: Failing tests** (each against a fault server):
  - `TestInspectRangesProven` (Size 20 MiB, strong ETag) → `RangesProven`, `Size==20<<20`, 2 probes, probe 2 range `{10<<20, 10<<20+2}`, `Validators.ETagStrong`.
  - `TestInspectIgnoreRanges` → `!RangesProven`, size from Content-Length, exactly 1 request logged, anomaly mentions "ignored Range".
  - `TestInspectMalformedContentRange` / `WrongTotal` / `LongBodyBy:1` → `!RangesProven`, anomaly recorded.
  - `TestInspectSmallFileSkipsProbe2` (Size 1 MiB) → 1 range probe.
  - `TestInspectEmptyFile` (Size 0) → `SizeKnown && Size==0`.
  - `TestInspectUnknownLength` → `!SizeKnown`, `Dynamic`.
  - `TestInspectAuth` (`StatusOverride: 401` and `403`) → `Err.Kind==KindAuth`; `404`/`410` → `KindPermanent`.
  - `TestInspectRetryAfter` (`FailFirstN: 2, FailStatus: 503, RetryAfter: 1s`; FakeClock advanced by test) → succeeds, `ProbeRetries==2`.
  - `TestInspectRedirects` (`/redirect/3`) → `FinalURL` ends `/file`, 3 redirects.
  - `TestInspectFilename` (Content-Disposition `filename*` → used, source `"content-disposition"`; none → URL path segment; path `/` → `"download"`).
  - `TestInspectSignedURL` (`?X-Amz-Signature=s`) → `SignedURL`, `Redirects`/`FinalURL` contain no `s`.
  - `TestInspectInconsistentETag` (`ChangeETagAfter: 1`) → `!RangesProven`, anomaly "validator changed between probes".
- [ ] **Step 2–4:** FAIL → implement → PASS.
- [ ] **Step 5:** Commit `feat(strategy): bounded resource inspection`.

---

### Task 15: Strategy selector

**Files:**
- Create: `internal/strategy/selector.go`
- Test: `internal/strategy/selector_test.go`

**Interfaces:**
- Consumes: `Observations` (Task 14), `config.Config`, `domain.*`.
- Produces: `type Confidence string` (`ConfidenceHigh`, `ConfidenceMedium`, `ConfidenceLow`); `type ResumeState struct{ Validators domain.Validators; Size int64; SizeKnown bool; Previous domain.Strategy }`; `type Decision struct` with fields exactly as spec §5.2 (`Strategy domain.Strategy; Resume bool; RemoteChanged bool; Confidence; Size int64; SizeKnown, RangesProven bool; Validator domain.ValidatorKind; InitialConns, MaxConns int; ChunkSize int64; Probes []ProbeResult; Reasons, ChangeTriggers []string; Err *domain.Error`), JSON-tagged snake_case; `func Select(obs Observations, cfg config.Config, resume *ResumeState) Decision`; `func ChunkSize(size int64, maxConns int, conservative bool, cfg config.Config) int64` (spec §6.1: `clamp(size/(maxConns*4), ChunkSizeMin, ChunkSizeMax)` rounded down to MiB; conservative min 4 MiB; grow so `ceil(size/chunk) ≤ 4096`); `func (d Decision) Summary() string` (one line, e.g. `"multipart (high confidence): 2→8 connections, 4 MiB chunks"`). When `resume != nil` and validators match, `Strategy` is the underlying B/C/A and `Resume=true`. RTT threshold constant `conservativeRTT = 400*time.Millisecond`. A `--conns N` override arrives as `cfg.InitialConnections == cfg.MaxConnections == N` with `cfg.AdaptiveConcurrency=false`.

- [ ] **Step 1: Failing tests** — table-driven `TestSelect` with hand-built `Observations`, one row per rule in spec §5.1, asserting `Strategy`, `InitialConns`, `MaxConns`, `Confidence`, and that `Reasons` is non-empty:
  - Err Auth → `StrategyUnsupported`, `Err` preserved.
  - unknown size → single, conns 1/1.
  - ranges not proven → single.
  - size 7 MiB proven → single ("below multipart threshold").
  - proven, 100 MiB, no validator → single.
  - proven, 100 MiB, strong ETag → multipart, 2/8, High.
  - proven, 100 MiB, LM only → multipart, Medium.
  - proven + probe RTT 500 ms → conservative, 1/2, chunk ≥ 4 MiB.
  - proven + `ProbeRetries:1` → conservative.
  - `cfg.ConservativeMode` → conservative.
  - `cfg.MaxConnections=1` → single.
  - override N=6, proven → 6/6; override N=6, not proven → single.
  - resume matching → `Resume`, multipart; resume ETag mismatch → `RemoteChanged`, `Err.Kind==KindRemoteChanged`; resume with stored `ValidatorNone` → `RemoteChanged` ("cannot verify").
  - `TestSelectDeterministic` (same input twice → `reflect.DeepEqual`).
  - `TestChunkSize` (100 MiB/8 conns → 3 MiB; 10 GiB/8 → 64 MiB; 1 TiB → ≥ 256 MiB and ≤ 4096 chunks; conservative 20 MiB → 4 MiB).
- [ ] **Step 2–4:** FAIL → implement → PASS.
- [ ] **Step 5:** Commit `feat(strategy): deterministic strategy selection`.

---

### Task 16: Adaptive concurrency controller

**Files:**
- Create: `internal/strategy/controller.go`
- Test: `internal/strategy/controller_test.go`, `internal/strategy/linksim_test.go`

**Interfaces:**
- Produces: `type Sample struct{ Window time.Duration; UsefulBytes int64; Conns int; Errors int; Throttled int; RetryAfter time.Duration; TimeoutsResets int; WriteBlockedFrac float64 }`; `type ActionKind int` (`Hold`, `Increase`, `Decrease`, `SwitchConservative`, `Backoff`); `type Action struct{ Kind ActionKind; Target int; Delay time.Duration }`; `type ControllerConfig struct{ Initial, Max int; Adaptive bool }`; `func NewController(c ControllerConfig) *Controller`; `func (c *Controller) Observe(s Sample) Action`; `func (c *Controller) Current() int`; `func (c *Controller) EWMA() float64`.
- Constants (spec §8): `alpha=0.3`, `stableWindowsBeforeProbe=2`, `probeWindows=2`, `minGain=0.10`, `cooldownAfterRevert=6`, `cooldownAfterThrottle=12`, `timeoutWindowsToConservative=3`, `stableWindowsToRecover=10`, `diskBlockedLimit=0.5`, `maxRetryAfter=10*time.Minute`. `Adaptive=false` → always `Hold` at `Initial` except throttling (still halves — safety overrides manual).
- Note: the window sizing (≥2 s and ≥256 KiB, max 10 s) is done by the caller (Task 21); the controller trusts each `Sample`.

- [ ] **Step 1: Failing tests:**
  - Unit: `TestIncreaseRequiresStableWindows` (one good window → Hold; two → Increase to 3); `TestRevertWithoutGain` (after increase, two windows with +5% → Decrease back to 2, then 6 Holds even with good samples); `TestKeepOnGain` (+30% → stays 3, later probes 4); `TestThrottleHalves` (at 8, `Throttled:1, RetryAfter: 20s` → `Backoff{Target:4, Delay:20s}`; RetryAfter 1h → capped 10 min); `TestTimeoutsDecrease` and three consecutive → `SwitchConservative` (Target ≤ 2); `TestDiskBoundNeverIncreases`; `TestNeverExceedsMax`, `TestNeverBelowOne`; `TestManualHolds`.
  - Simulation (`linksim_test.go`): a model `throughput(n) ` fed into the controller for 200 windows: per-conn cap 1 MB/s, link 20 MB/s → final `Current() ≥ 6`; link-capped 5 MB/s with per-conn 10 MB/s → `Current() ≤ 2` for ≥ 80% of windows; throttling model (n > 3 → 429) → never stays above 3 for more than 2 consecutive windows; per-window noise ±10% with seeded RNG → no oscillation more than 1 change per 4 windows on average.
- [ ] **Step 2–4:** FAIL → implement → PASS.
- [ ] **Step 5:** Commit `feat(strategy): adaptive concurrency controller`.

---

### Task 17: SQLite persistence

**Files:**
- Create: `internal/downloader/store.go`, `internal/persistence/sqlite.go`, `internal/persistence/migrations/0001_init.sql`
- Test: `internal/persistence/sqlite_test.go`

**Interfaces:**
- Produces (in `downloader/store.go`, depends only on `domain`):
```go
type Store interface {
	CreateTask(ctx context.Context, t *domain.Task) error
	GetTask(ctx context.Context, id string) (*domain.Task, error) // ErrNotFound
	UpdateTask(ctx context.Context, t *domain.Task) error
	ListTasks(ctx context.Context) ([]*domain.Task, error)        // newest first
	DeleteTask(ctx context.Context, id string) error              // cascades segments, attempts
	SaveCheckpoint(ctx context.Context, taskID string, cp domain.Checkpoint) error // one tx: replace segments, set downloaded
	LoadSegments(ctx context.Context, taskID string) ([]domain.SegmentRecord, error) // ordered by start
	ClearSegments(ctx context.Context, taskID string) error
	AddAttempt(ctx context.Context, taskID string, a domain.Attempt) error // prunes to 200 newest
	ListAttempts(ctx context.Context, taskID string) ([]domain.Attempt, error)
}
```
  and `var ErrNotFound = errors.New("not found")` in `domain/errors.go` (add there so persistence can return it).
- Produces (persistence): `func Open(path string) (*DB, error)` (creates dir 0700, file 0600, pragmas from spec §14, runs migrations); `func (db *DB) Close() error`; `*DB` satisfies `downloader.Store` (asserted in the test file, not the package, to keep the import direction). `const SchemaVersion = 1`; opening a DB whose `schema_version` > `SchemaVersion` → error containing "newer".
- Schema exactly spec §14; `_pragma` DSN params for modernc: `file:<path>?_pragma=journal_mode(WAL)&_pragma=synchronous(FULL)&_pragma=foreign_keys(ON)&_pragma=busy_timeout(5000)`.

- [ ] **Step 1: Failing tests:** `TestTaskRoundTrip` (all fields incl. `DecisionJSON`, `Validators`, times at second precision); `TestCheckpointReplacesSegments` (save 3 segs, then 2 → load returns 2, `Downloaded` updated); `TestCheckpointAtomic` (cancelled ctx mid-call → previous checkpoint intact); `TestAttemptsPruned` (250 adds → 200 newest); `TestDeleteCascades`; `TestFilePermissions` (unix: file mode `0600`, dir `0700`); `TestRefuseNewerSchema` (manually set version 99 → `Open` error contains "newer"); `TestReopenPersists`; `var _ downloader.Store = (*persistence.DB)(nil)` compile check in `sqlite_test.go` (package `persistence_test`).
- [ ] **Step 2–4:** FAIL → implement → PASS.
- [ ] **Step 5:** Commit `feat(persistence): sqlite task store with migrations`.

---

### Task 18: Segment scheduler with endgame splitting

**Files:**
- Create: `internal/downloader/scheduler.go`
- Test: `internal/downloader/scheduler_test.go`

**Interfaces:**
- Consumes: `domain.ByteRange`, `domain.SegmentRecord`, `storage.ResumeHasher`.
- Produces: `type Segment struct{ Start int64; ... }` with methods `End() int64`, `Pos() int64`, `Advance(n int64)` (owner only), `Hasher() hash.Hash`, `Remaining() int64`; `func NewScheduler(total int64, chunk int64) *Scheduler` (grid of fresh segments); `func NewOpenScheduler() *Scheduler` (unknown size: one segment `[0, math.MaxInt64)`; `Finish(n int64)` sets its `End` and the total to `n` at EOF); `func (s *Scheduler) Total() int64`; `func RestoreScheduler(total int64, recs []domain.SegmentRecord) (*Scheduler, error)` (validates coverage `[0,total)` and disjointness, else error); `func (s *Scheduler) Acquire(worker int) (*Segment, bool)` (lowest-start pending); `Release(seg *Segment)`; `Complete(seg *Segment)` (stores `Hasher().Sum(nil)`, requires `Pos()==End()`); `TrySplit(worker int, minSplit, bufSize int64, rate func(*Segment) float64) (*Segment, bool)` (spec §6.2 rule; the new segment is returned already owned by `worker`); `Records() []domain.SegmentRecord` (snapshot with marshaled hasher states for in-progress segments); `AllDone() bool`; `DoneBytes() int64`; `Pending() int`.
- Concurrency: one `sync.Mutex`; `end` and `pos` are `atomic.Int64` so the owner's hot write path reads `End()` without the lock. Split point algorithm:

```go
// S = max(pos+bufSize, start+(end-start)/2), aligned up to 64 KiB; refuse if S >= end-minSplit.
```

- [ ] **Step 1: Failing tests:** `TestGridCoversExactly` (total `5<<30+7`, chunk 64 MiB → union `[0,total)`, last segment short); `TestAcquireExclusive` (two workers never get the same segment); `TestReleaseKeepsPos`; `TestCompleteRequiresFull`; `TestSplitAboveWrittenPos` (pos at 900 KiB of a 4 MiB segment, buf 256 KiB → `S ≥ pos+256 KiB`, owner's `End()==S`, new `[S, 4 MiB)`); `TestSplitRefusesSmall`; `TestRestoreRejectsGaps/Overlaps`; `TestOpenSchedulerFinish` (advance 1234, `Finish(1234)` → `AllDone` after `Complete`, `Total()==1234`); `TestRecordsRoundTrip` (records → `RestoreScheduler` → identical coverage, hasher continues correctly). Property test `TestSchedulerRandomOps` (seeded, 500 iterations × 200 random ops among acquire/advance/release/split/complete across 8 workers; after every op assert disjoint + union exactly `[0,total)` + `DoneBytes()` equals sum of done lengths; at end drive to completion and assert `AllDone`) run under `-race` with workers as goroutines in a second variant.
- [ ] **Step 2–4:** FAIL → implement → PASS.
- [ ] **Step 5:** Commit `feat(downloader): segment scheduler with endgame splitting`.

---

### Task 19: Retry, backoff, classification

**Files:**
- Create: `internal/downloader/retry.go`
- Test: `internal/downloader/retry_test.go`

**Interfaces:**
- Produces: `type Backoff struct{ Base, Max time.Duration; Factor float64; Rand *rand.Rand }`; `func (b Backoff) Delay(attempt int, retryAfter time.Duration) time.Duration` (full jitter: uniform `[0, min(Max, Base*Factor^attempt)]`; if `retryAfter>0` return `min(retryAfter, 10*time.Minute)` plus up to 10% jitter); `func Classify(err error) domain.ErrorKind` (`domain.KindOf`, plus: `net.Error` timeouts, `syscall.ECONNRESET`, `io.ErrUnexpectedEOF` → Transient; `x509` errors → Permanent; `syscall.ENOSPC`, `EACCES`, `EROFS` → Local); `func Retryable(k domain.ErrorKind) bool` (only Transient); `type Budget struct` with `func NewBudget(n int) *Budget` and `Spend() bool` (concurrency-safe).

- [ ] **Step 1: Failing tests:** `TestDelayBounds` (seeded; 1000 draws per attempt 0..10 all within `[0, min(60s, 500ms·2^a)]`); `TestDelayRetryAfter` (30s → in `[30s, 33s]`; 1h → ≤ 11 min); `TestClassify` table (ECONNRESET wrapped → Transient; ENOSPC wrapped in `*fs.PathError` → Local; `*domain.Error{KindAuth}` → Auth; `context.Canceled` → Cancelled; `x509.UnknownAuthorityError{}` → Permanent); `TestRetryableOnlyTransient`; `TestBudgetConcurrent` (100 goroutines spending budget 50 → exactly 50 true).
- [ ] **Step 2–4:** FAIL → implement → PASS.
- [ ] **Step 5:** Commit `feat(downloader): retry policy and error classification`.

---

### Task 20: Write path and single-stream executor

**Files:**
- Create: `internal/downloader/sink.go`, `internal/downloader/single.go`, `internal/downloader/run.go`
- Test: `internal/downloader/single_test.go`

**Interfaces:**
- Consumes: `Scheduler`/`Segment` (18), `storage.TempFile` (10), `ratelimit` (12), `transport` (5, 8), `Backoff`/`Classify` (19), `clocktest` (3).
- Produces (`run.go`, shared by single and multipart): 
```go
type runEnv struct {
	task      *domain.Task
	decision  strategy.Decision
	client    *http.Client
	file      *storage.TempFile
	sched     *Scheduler
	limits    []*ratelimit.Bandwidth // per-download, global
	conns     *ratelimit.Semaphore
	cfg       config.Config
	clock     domain.Clock
	backoff   Backoff
	budget    *Budget
	onBytes   func(n int64)          // progress + controller accounting (atomic)
	onRetry   func(a domain.Attempt) // event + store
	bufPool   *sync.Pool             // 256 KiB []byte
	writeAt   func(p []byte, off int64) (int, error) // defaults to file.WriteAt; tests inject faults
	windowMin time.Duration          // controller window minimum, default 2s; tests shorten it
}
```
  `func copySegment(ctx context.Context, env *runEnv, seg *Segment, body io.Reader) error` (in `sink.go`): loop read → truncate to `seg.End()-seg.Pos()` → `ratelimit.WaitAll` → `env.writeAt` → `seg.Hasher().Write` → `seg.Advance(n)` → `env.onBytes(n)`; returns nil when `Pos()==End()`, `io.ErrUnexpectedEOF` if body ends early, a stall error (`KindTransient`, msg "stalled") when no bytes for the adaptive stall timeout (spec §8.1 — implemented with a watchdog goroutine that cancels a per-request child context; the median gap is tracked per call).
  `func runSingle(ctx context.Context, env *runEnv) error` — one segment: `NewScheduler(Size, Size)` when size is known, `NewOpenScheduler()` (Task 18) when unknown, calling `Finish` at EOF. Fresh: plain GET. Resume with `Pos>0` and `RangesProven`: `Range: OpenRangeHeader(pos)` + `If-Range` (strong ETag or LM); a 200 response on resume → `KindRemoteChanged` unless validators match and then restart from 0 is *not* done here (return the error; Engine decides). Retries transient errors with backoff + budget, keeping `Pos`.

- [ ] **Step 1: Failing tests** (fault server + real `storage.TempFile` in `t.TempDir()`, real clock with tiny backoff `Base: 1ms`):
  - `TestSingleDownloadCorrect` (Size 3 MiB → file SHA-256 == `ExpectedSHA256`).
  - `TestSingleResumesAfterReset` (`ResetAfterBytes: 1<<20, ResetEveryNth: 1` for first 2 requests then off via `SetOptions`) → correct file; server log shows a request with `Range: bytes=1048576-` (or later) and `If-Range`.
  - `TestSingleTruncationRetried` (`CloseAfterBytes`) → correct.
  - `TestSingleUnknownLength` → correct, `sched.Total()` equals generated size.
  - `TestSingleEmptyFile` → zero-byte file, no body read error.
  - `TestSingleStall` (`JitterMax` huge on one request; `StallTimeout` 200 ms via cfg) → retried and completes; one attempt with msg "stalled".
  - `TestSingleSlowButProgressingNotStalled` (`BytesPerSecondPerConn: 50_000`, `WritePiece: 4096`, 300 KB, StallTimeout 200 ms → reads arrive every ~80 ms, so zero "stalled" attempts and completion).
  - `TestSingleBudgetExhausted` (`FailFirstN: 100, FailStatus: 503`, budget 3) → error kind Transient after 3 retries, `onRetry` called 3 times.
  - `TestSingleRateLimited` (per-download 1 MB/s, 2 MiB → ≥ 1.8 s).
- [ ] **Step 2–4:** FAIL → implement → PASS (`-race`).
- [ ] **Step 5:** Commit `feat(downloader): write path and single-stream executor`.

---

### Task 21: Multipart executor with adaptive controller and fill mode

**Files:**
- Create: `internal/downloader/multipart.go`
- Test: `internal/downloader/multipart_test.go`

**Interfaces:**
- Consumes: everything in Task 20's `runEnv`, `strategy.Controller` (16), `transport.ValidateRangeResponse` (8).
- Produces: `func runMultipart(ctx context.Context, env *runEnv, ctl *strategy.Controller, onStrategy func(from, to domain.Strategy, reason string)) error`.
- Behaviour:
  - Supervisor goroutine owns the worker set under an `errgroup.WithContext`. It starts `ctl.Current()` workers; every window (≥ 2 s and ≥ 256 KiB accumulated, max 10 s — measured with `env.clock`) builds a `strategy.Sample` from atomics updated by workers and applies the `Action`: `Increase` → start a worker if `env.conns.TryAcquire()`; `Decrease`/`SwitchConservative` → set a `retire` counter that workers consult at segment boundaries; `Backoff` → all workers pause new requests until `clock.Now()+Delay` (shared "notBefore" atomic).
  - Worker loop: spec §6.3. Before each request `env.conns.Acquire`; after response close `Release`. Request: `Range: RangeHeader([Pos, End))`, `If-Range` when strong ETag. Response handling per spec §6.4: `ValidateRangeResponse`; `KindRemoteChanged` → return error (aborts group); 200 → increment `rangeIgnored` counter, release segment, retry; when `rangeIgnored ≥ 3` → supervisor cancels workers and calls `runFill`.
  - `func runFill(ctx context.Context, env *runEnv) error`: one GET without Range; check `ReadValidators(resp.Header).Matches(task.Validators)` and `Content-Length == Size` else `KindRemoteChanged`; stream body; for each byte span intersecting a non-done segment at that segment's `Pos`, write+hash+advance; discard other bytes. Segments whose `Pos` is ahead of the stream position just skip. Emits `onStrategy(multipart, single, "server stopped honouring ranges; fill mode")`.
  - When no pending segments and a worker is idle → `sched.TrySplit(worker, 1<<20, 256<<10, rate)` where `rate` is the segment's recent bytes/sec tracked by its owner.
  - Each worker increments `writeBlocked` time around `WriteAt` (for `WriteBlockedFrac`).

- [ ] **Step 1: Failing tests** (fault server, `t.TempDir()`):
  - `TestMultipartCorrect` (Size 40 MiB, strong ETag, initial 2 max 8) → SHA-256 equal; server log shows only 206s, no overlapping ranges (parse each logged Range and assert union disjoint).
  - `TestMultipartScalesOnPerConnCap` (`BytesPerSecondPerConn: 2 MB/s`, Size 64 MiB, `env.windowMin = 200ms`) → `server.ActiveMax() ≥ 4`.
  - `TestMultipartHoldsOnGlobalCap` (`BytesPerSecondGlobal: 4 MB/s`, per-conn unlimited) → `ActiveMax() ≤ 3`.
  - `TestMultipart429Storm` (`MaxConcurrent: 3`, start at 6 via override) → completes correctly; at least one Backoff observed (`onRetry` with status 503/429).
  - `TestMultipartETagChangeAborts` (`ChangeETagAfter: 5`) → error kind `RemoteChanged`; returned before completion; no byte written from requests carrying the new ETag (assert: every completed segment's hash equals the generator hash of the original seed).
  - `TestMultipartRangeIgnoredFillMode` (`RangeIgnoredAfter: 4`) → completes correctly; `onStrategy` called with reason containing "fill mode".
  - `TestMultipartResets` (`ResetAfterBytes: 3<<20, ResetEveryNth: 3`) → correct.
  - `TestMultipartEndgameSplit` (one slow request via per-conn jitter on the last segment) → server log shows a Range starting mid-segment (split happened), file correct.
  - `TestMultipartNoGoroutineLeak` (cancel ctx mid-download → after return, `runtime.NumGoroutine()` returns to baseline ±2 within 2 s).
  - `TestMultipart5GiB` (`testing.Short()` skip; sparse 5 GiB, max 8) → correct SHA-256 (generator).
- [ ] **Step 2–4:** FAIL → implement → PASS (`-race`, excluding the 5 GiB test from `-race` runs via `testing.Short` + Makefile `race` uses `-short`).
- [ ] **Step 5:** Commit `feat(downloader): adaptive multipart executor with fill mode`.

---

### Task 22: Checkpointing and recovery

**Files:**
- Create: `internal/downloader/checkpoint.go`, `internal/downloader/recovery.go`
- Test: `internal/downloader/checkpoint_test.go`, `internal/downloader/recovery_test.go`, `internal/downloader/memstore_test.go` (in-memory `Store` fake for package tests)

**Interfaces:**
- Consumes: `Store` (17), `Scheduler.Records` (18), `storage.TempFile`, `storage.HashRange`, `storage.ResumeHasher`.
- Produces: `type checkpointer struct`; `func newCheckpointer(store Store, taskID string, file *storage.TempFile, sched *Scheduler, clock domain.Clock, interval time.Duration, bytes int64) *checkpointer`; `func (c *checkpointer) MaybeCheckpoint(ctx context.Context, downloadedSinceLast int64) error` (fires when interval elapsed or bytes reached); `func (c *checkpointer) Checkpoint(ctx context.Context) error` (always: `file.Sync()` **then** `store.SaveCheckpoint(Records())`); `func recoverSegments(ctx context.Context, file *storage.TempFile, size int64, recs []domain.SegmentRecord) (recovered []domain.SegmentRecord, discardedBytes int64, err error)` per spec §9.3 steps 2–3 (done → re-hash full; in-progress → restore hasher, re-hash `[Start,Pos)`, compare `Sum`; mismatches reset).
- Ordering invariant I1 is enforced inside `Checkpoint`; a `Sync` error aborts without touching the store and is returned as `KindLocal`.

- [ ] **Step 1: Failing tests:**
  - `TestCheckpointOrdering` — fake file whose `Sync` records a sequence number and a fake store that records the sequence number at `SaveCheckpoint`; assert sync < save on every call; with `Sync` returning error, store never called and error `KindLocal`.
  - `TestMaybeCheckpointTriggers` (FakeClock: 1.9 s no trigger, 2 s trigger; 16 MiB bytes trigger).
  - `TestRecoverTrustsOnlyVerified` — write a 8 MiB file, segments: done-correct, done-with-corrupted-byte, in-progress-correct (pos mid), in-progress-corrupted → recovered: 1st done, 2nd reset to Start, 3rd keeps Pos, 4th reset; `discardedBytes` equals reset amounts.
  - `TestRecoverMissingFileOrWrongSize` → all segments reset (caller handles), error nil, `discardedBytes` = total done.
  - **Review Focus #1:** `TestWriteENOSPCFailsLocal` — set `env.writeAt` (Task 20) to return `&fs.PathError{Op: "write", Err: syscall.ENOSPC}` after 1 MiB, with a checkpointer firing every 256 KiB; run `runSingle` → error `KindLocal`, zero retry attempts recorded, and the last saved checkpoint covers ≥ 768 KiB (bytes synced before the failure are kept).
- [ ] **Step 2–4:** FAIL → implement → PASS.
- [ ] **Step 5:** Commit `feat(downloader): crash-safe checkpointing and verified recovery`.

---

### Task 23: Engine orchestration

**Files:**
- Create: `internal/downloader/engine.go`
- Test: `internal/downloader/engine_test.go`

**Interfaces:**
- Consumes: all previous tasks.
- Produces:
```go
type EngineDeps struct {
	Store   Store
	Config  config.Config
	Bus     *events.Bus
	Clock   domain.Clock
	Global  *ratelimit.Bandwidth
	Conns   *ratelimit.Semaphore
	Logger  *slog.Logger
	NewClient func(allowPrivate bool) *http.Client // production: transport.NewClient with cfg policy
	Resolver  *net.Resolver                        // nil → net.DefaultResolver
}
type AddRequest struct {
	URL            string
	DestDir        string // "" → cfg.DownloadDir
	FileName       string // "" → from inspection
	ExpectedSHA256 string // hex, optional; validated 64 hex chars
	ExistingPolicy domain.ExistingFilePolicy // "" → cfg
	Connections    int    // 0 = auto
	RateLimit      int64  // 0 = cfg.PerDownloadRateLimit
	Conservative   bool
}
func NewEngine(d EngineDeps) *Engine
func (e *Engine) Add(ctx context.Context, r AddRequest) (*domain.Task, error) // validates, checks dest dir writable, persists queued
func (e *Engine) Run(ctx context.Context, id string) error                    // full lifecycle; returns nil on completed
func (e *Engine) Pause(id string) bool                                        // cancels running ctx with cause domain.ErrPaused
func (e *Engine) Cancel(ctx context.Context, id string) error                 // running or not → cancelled; keeps temp file unless removed
func (e *Engine) Remove(ctx context.Context, id string, deletePartial bool) error // never deletes completed files
func (e *Engine) Explain(ctx context.Context, url string, r AddRequest) (strategy.Decision, strategy.Observations, error)
func (e *Engine) Shutdown(ctx context.Context) error                          // pause all, wait ≤10s, refuse new Run
var ErrAlreadyRunning = errors.New("task already running")
var ErrShuttingDown  = errors.New("engine shutting down")
```
- `Run` flow: guard (map[id]cancel under mutex → `ErrAlreadyRunning`) → state `queued→inspecting` (from paused: `paused→queued→inspecting`; from failed: `failed→queued→inspecting`) → `Inspector.Inspect` (with stored validators when segments exist) → `Select` (ResumeState when segments exist) → publish `DecisionMade`, persist decision → on `RemoteChanged`: if `cfg.RestartOnRemoteChange` clear segments + delete temp and continue fresh, else state `paused` with reason, return `*domain.Error{KindRemoteChanged}` → open/create temp (resume: `OpenTemp` + `recoverSegments`; missing temp → fresh with `Warning`) → state `downloading` → `runSingle`/`runMultipart` with a progress ticker publishing `Progress` (ETA only when `SizeKnown` and speed EWMA > 0 for ≥ 3 s) and `checkpointer.MaybeCheckpoint` → errors: cause `ErrPaused` → final checkpoint, state `paused`, return `domain.ErrPaused`; `ErrCancelled` → checkpoint, `cancelled`; transient with budget left → `retrying` → backoff → loop to `inspecting`; else checkpoint + `failed` with kind/msg/action → success: `verifying` → full-file SHA-256 if `ExpectedSHA256` (mismatch → `failed` `KindIntegrity`) → close → `storage.Finalize` → sync → `completed` with `FinalPath`, `VerifyResult` (`"sha256 verified"` or `"segment-consistency verified; no trusted checksum supplied"`) → `ClearSegments`.
- Every state change goes through `domain.Transition` and is persisted, logged (redacted), and published.
- Temp file name: `storage.CreateTemp(destDir, SanitizeFilename(name), …)`; final destination `SafeJoin(destDir, sanitized)`.

- [ ] **Step 1: Failing tests** (memstore or real SQLite in `t.TempDir()`, fault server):
  - `TestRunCompletes` (multipart-eligible 20 MiB) → state completed, file at `FinalPath` correct, no `.swiftfetch-part` left, segments cleared, events include `DecisionMade` and `StateChanged` sequence `queued→inspecting→downloading→verifying→completed`.
  - `TestRunChecksumMismatch` (`CorruptAt: [12345]`, correct `ExpectedSHA256` of uncorrupted content) → `failed`, `KindIntegrity`, temp file kept.
  - `TestRunChecksumMatch` → `VerifyResult=="sha256 verified"`.
  - `TestPauseResume` (throttled 10 MiB; `Pause` at ~30% → `Run` returns `ErrPaused`, state paused; second `Run` → completes; server log shows resumed ranges, total bytes served < 1.4 × size).
  - `TestRunGuard` (two concurrent `Run` same id → one `ErrAlreadyRunning`).
  - `TestPauseResumeRace` (50 iterations: random pause delay 0–50 ms then Run again; `-race`; final file correct; `server.ActiveMax()` never exceeds `MaxConnections`).
  - `TestRemoteChangedOnResume` (pause, flip ETag via `SetOptions`, Run → `KindRemoteChanged`, state paused, segments still present); with `RestartOnRemoteChange=true` → completes with new content hash.
  - `TestCancel` → cancelled, temp file still present; `Remove(id, true)` deletes temp and task.
  - `TestRemoveNeverDeletesCompleted`.
  - `TestExistingDestinationPreserved` (dest has `file.bin` → final `file (1).bin`, original untouched).
  - `TestShutdown` (two running tasks → `Shutdown` → both paused within 10 s; subsequent `Run` → `ErrShuttingDown`).
  - **Review Focus #2:** `TestResumeExpiredSignedURL` (pause, `SetOptions(ExpireAfter: 0)` → Run → `failed`, `KindAuth`, `Action` contains "expired", `LoadSegments` non-empty).
  - **Review Focus #3:** `TestPauseDuringInspecting` (`JitterMax` on probe so inspection is slow; Pause while state inspecting → paused; Run → completes, no Warning about missing temp file).
  - **Review Focus #4:** `TestAddMissingDestDir` and `TestAddReadOnlyDestDir` → `KindLocal`, message contains dir; `ListTasks` empty.
  - **Review Focus #5:** `TestSameNameConcurrentTasks` (two tasks, same URL path name, same dir, run concurrently → two distinct temp files during run, final `name` and `name (1)`, both correct).
  - `TestRetryingStateOnTransient` (`FailFirstN: 2, FailStatus: 503` on download requests after inspection → state sequence includes `retrying`, completes, attempts recorded with redacted messages).
- [ ] **Step 2–4:** FAIL → implement → PASS (`-race -short`).
- [ ] **Step 5:** Commit `feat(downloader): engine lifecycle orchestration`.

---

### Task 24: CLI

**Files:**
- Create: `cmd/swiftfetch-cli/main.go`, `cmd/swiftfetch-cli/progress.go`
- Test: `cmd/swiftfetch-cli/progress_test.go`, `cmd/swiftfetch-cli/main_test.go`

**Interfaces:**
- Consumes: `downloader.Engine` and deps, `persistence.Open`, `config.Default`, `events.Bus`.
- Produces: subcommands and flags exactly spec §15; `func run(ctx context.Context, args []string, stdout, stderr io.Writer, env map[string]string) int` (testable entry; `main` calls it with `os.Args[1:]`); `func renderProgress(p events.Progress, name string, strategy domain.Strategy) string` (`name  42.0%  420.0 MiB / 1.0 GiB  12.3 MiB/s  ETA 0:48  conns 4  multipart`; ETA `"—"` when nil; unknown total → no percent); `func parseRate(s string) (int64, error)` (binary suffixes: `K`=1024, `M`=1024², `G`=1024³; bare number = bytes/s). `SWIFTFETCH_DATA_DIR` env overrides DataDir (used by tests). Exit codes 0/1/2/3. Signal handling: first SIGINT → `engine.Pause`, second within 2 s → exit 3 immediately. `--conns N` prints the warning from spec §13 to stderr.

- [ ] **Step 1: Failing tests:** `TestRenderProgress` table (known/unknown total, nil ETA, long names truncated to 40 runes with `…`); `TestParseRate` (`"512K"`, `"2M"`, `"1G"`, `"100"`, errors on `"abc"`, `"-1"`); `TestCLIGetAndList` (fault server; `run(["get", url, "-o", dir])` → 0, file correct; `run(["list"])` output contains `completed` and a redacted URL); `TestCLIExplain` (ignore-ranges server → output contains `single` and `ignored Range`); `TestCLIUsageError` (`run(["get"])` → 2); `TestCLIConnsWarning`.
- [ ] **Step 2–4:** FAIL → implement → PASS. Run `make build cross` → six binaries in `bin/`.
- [ ] **Step 5:** Commit `feat(cli): swiftfetch-cli`.

---

### Task 25: Integration matrix and explain golden tests

**Files:**
- Create: `tests/integration/matrix_test.go`, `tests/integration/explain_test.go`, `tests/integration/testdata/explain/*.golden`

**Interfaces:**
- Consumes: `downloader.Engine` via public constructor, real SQLite, fault server.

- [ ] **Step 1: Write tests:** `TestMatrix` — one subtest per fault-server mode from spec §18.1 with the expected terminal outcome:

| Mode | Expect |
|---|---|
| correct ranges 50 MiB | completed, multipart |
| ignore ranges | completed, single |
| malformed Content-Range | completed, single |
| wrong total | completed, single |
| short body (ShortBodyBy) | completed after retries |
| weak ETag only | completed, single |
| no validators | completed, single |
| LM only 50 MiB | completed, multipart, medium |
| ETag change mid-download | paused, RemoteChanged, no final file |
| redirect chain 5 | completed |
| redirect to private with AllowPrivate=false | failed, Policy |
| expire after 200 ms on throttled download | failed, Auth |
| 401 / 403 / 404 / 410 | failed, Auth/Auth/Permanent/Permanent |
| 416 empty | completed, 0 bytes |
| 429 + Retry-After 1s ×2 | completed, retrying observed |
| 503 MaxConcurrent 2 | completed, ActiveMax ≤ 3 |
| variable bandwidth (jitter) | completed |
| resets every 3rd request | completed |
| mid-stream truncation | completed |
| corrupted bytes, no checksum | completed, VerifyResult "no trusted checksum" |
| corrupted bytes, checksum | failed, Integrity |
| premature close | completed |
| unknown length | completed, single |
| 5 GiB sparse (skip in -short) | completed |

  Every completed case asserts SHA-256 == `faultserver.ExpectedSHA256(seed, size)`. `TestExplainGolden` — for 8 representative modes, `Engine.Explain` → `Decision` JSON with RTTs zeroed, compared against `testdata/explain/<mode>.golden`; `-update` flag regenerates.
- [ ] **Step 2:** Run `go test ./tests/integration/ -short -race` → fix any defects found via superpowers:systematic-debugging (regression test in the owning package first).
- [ ] **Step 3:** Commit `test: integration matrix and explain golden files`.

---

### Task 26: Fault injection and crash test

**Files:**
- Create: `tests/faultinjection/store_faults_test.go`, `tests/faultinjection/crash_test.go`, `tests/faultinjection/faultystore.go`

**Interfaces:**
- Consumes: `downloader.Store`, `persistence.Open`, `Engine`.
- Produces: `type FaultyStore struct{ downloader.Store; FailSaveCheckpointAfter int; FailUpdateTaskAfter int }` returning an injected error on the Nth call.

- [ ] **Step 1: Write tests:**
  - `TestCheckpointFailureThenRecover` — `FailSaveCheckpointAfter: 3` → Run returns error; new Engine on the *underlying* store + same files → Run completes, file correct.
  - `TestUpdateTaskFailure` — state persistence error surfaces as failure, later Run recovers.
  - `TestCrashRecovery` — parent starts a fault server (Size 32 MiB, `BytesPerSecondGlobal: 8 MB/s`), DB in `t.TempDir()`, runs `exec.Command(os.Args[0], "-test.run=TestCrashChild")` with env `SWIFTFETCH_CRASH_CHILD=1`, URL, DB path, dest dir; kills the child (`Process.Kill`) after a seeded random delay in `[300ms, 3500ms]`; then in-process opens the DB, finds the task, asserts no segment is marked done whose bytes don't hash (call `recoverSegments` and compare to records — **all recorded done segments must verify**, proving I1), runs `Engine.Run` → completed and SHA-256 correct. Repeat 20 iterations with seeds 1..20 (`-short` runs 3). `TestCrashChild` returns immediately unless the env var is set; otherwise adds and runs the download.
- [ ] **Step 2:** Run `go test ./tests/faultinjection/ -race -count=1` → PASS (20/20 non-short).
- [ ] **Step 3:** Commit `test: fault injection and crash recovery`.

---

### Task 27: Memory bound, benchmarks, docs, final verification

**Files:**
- Create: `tests/integration/memory_test.go`, `internal/downloader/bench_test.go`, `README.md`
- Modify: `Makefile` (add `bench`)

- [ ] **Step 1: Memory test:** `TestMemoryBound` (skip in `-short`): 1 GiB fault-server download, max 8 conns; sample `runtime.ReadMemStats().HeapInuse` every 50 ms; assert peak ≤ `8×256 KiB + 16 MiB` above the pre-download baseline. Run → PASS (if FAIL, profile with `-memprofile` and fix the cause, not the bound).
- [ ] **Step 2: Benchmarks:** `BenchmarkSingleVsMultipart` (per-conn cap 4 MB/s, 64 MiB; sub-benchmarks single, multipart-auto) reporting `MB/s` via `b.ReportMetric`; `BenchmarkInspect` (probe overhead against localhost). `make bench` runs them; README records the machine (`uname -a`, CPU model, Go version) and results under "Local smoke benchmarks" with the explicit caveat that these are loopback numbers, not real-network claims.
- [ ] **Step 3: README:** build/install, CLI usage with examples, how strategy selection works (summary of §4–§5 with `explain` output example), durability trade-offs (§9.3 paragraph), security defaults (§11), configuration defaults table, known limitations (§20 verbatim list), development commands.
- [ ] **Step 4: Final verification** (superpowers:verification-before-completion): run and record output of `gofmt -l .` (empty), `go vet ./...`, `golangci-lint run`, `go test -race -short ./...`, `go test ./...` (full incl. 5 GiB, crash 20×, memory), `make cross`. Check every acceptance criterion in spec §19 against the evidence and list any not met.
- [ ] **Step 5:** Commit `docs: README; test: memory bound and benchmarks`.
