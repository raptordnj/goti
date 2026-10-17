# SwiftFetch — Sub-project 1: Headless Core Engine — Design Spec

- **Date:** 2026-10-17
- **Status:** Draft for review
- **Module path:** `github.com/raptordnj/swiftfetch`
- **Toolchain:** Go 1.27 (installed: go1.27.2). `go.mod` declares `go 1.27`.

## 1. Context and scope

SwiftFetch is a cross-platform (Linux, Windows, macOS) desktop download manager in Go with a Gio UI. The full product brief is decomposed into four sub-projects, each with its own spec → plan → implementation cycle:

1. **Headless core engine (this spec)** — domain model, transport, inspection and strategy selection, single-stream and multipart downloaders, adaptive concurrency, storage/checkpointing/recovery, SQLite task persistence, a deterministic fault-injection test server, and a thin CLI.
2. **Queue service and command/event layer** — durable multi-task queue, priorities, global pause/resume, settings persistence, duplicate detection, the service API the GUI consumes.
3. **Gio desktop UI** — design system, screens, dialogs, details panel, platform integration (file pickers, open file/folder).
4. **Hardening and release** — benchmarks vs curl/aria2, stress/soak tests, CI matrix with native builds, packaging, docs.

Priorities, in order: (1) evidence-based strategy selection, (2) integrity/resumability/fault tolerance, (3) behaviour on slow/unstable networks, (4) efficient adaptive concurrency, then maintainability.

### Non-goals for sub-project 1

- Any GUI code or Gio dependency.
- Multi-task queue scheduling, priorities, reordering (sub-project 2). The engine runs one task per `Engine.Run` call; the CLI runs one download at a time. Global limiters (connections, bandwidth) are nonetheless designed as shared objects so sub-project 2 can run multiple engines.
- Protocols other than HTTP/HTTPS (no FTP, BitTorrent, HLS/DASH).
- Custom request headers, cookies, or stored credentials (URL userinfo is supported but not persisted in clear text logs/events; see §11).
- Encrypting persisted URLs with the OS keychain (documented limitation, §14).
- Scheduled downloads and network-change auto-pause (sub-project 2).

### Memory footprint (top priority, user-mandated 2026-10-17)

A low memory footprint of the program itself is a top priority and a hard constraint on every design choice.

- **Budgets (measured, CLI process RSS on linux/amd64):** idle after startup ≤ 20 MiB; one active 8-connection download ≤ 40 MiB; Go heap in use during a download ≤ 4 MiB above the pre-download baseline (buffers + segment state).
- **Buffers:** 64 KiB per connection (not 256 KiB), pooled; never more than one buffer per active connection.
- **Runtime:** the CLI sets a soft limit via `debug.SetMemoryLimit(32 MiB)` unless `GOMEMLIMIT` is set; no per-read allocations on the hot path (verified with `testing.AllocsPerRun`).
- **SQLite:** `cache_size=-512` (512 KiB), `mmap_size=0`, WAL autocheckpoint left at default.
- **Bounded state:** ≤ 4096 segments × small fixed struct; attempts capped at 200 rows in the DB and never loaded wholesale; probe bodies never buffered; full-file verification streams with a 64 KiB buffer.
- **Dependencies:** no new modules; any addition must justify its binary-size and RSS cost.

## 2. Architecture

```
cmd/swiftfetch-cli/      thin CLI wiring; no business logic
internal/domain/         Download, State + transition table, ByteRange/RangeSet, ErrorKind. No project deps.
internal/transport/      http.Client construction, dial-time address policy, redirect policy,
                         range request building, Content-Range / Content-Disposition parsing, redaction
internal/strategy/       Inspector (performs probes → Observations), Select (pure: Observations+Config → Decision),
                         Controller (pure adaptive-concurrency state machine fed Samples)
internal/downloader/     Engine (runs one task end to end), single-stream executor, multipart executor,
                         scheduler (segment ownership, endgame splitting), worker, retry/backoff, recovery
internal/storage/        temp-file creation and preallocation, positional writes, sync, segment hashing,
                         per-OS no-replace atomic finalize
internal/persistence/    SQLite implementation of downloader.Store; embedded migrations
internal/ratelimit/      shared token buckets (global + per-download) over golang.org/x/time/rate;
                         global connection semaphore
internal/events/         typed events; per-subscriber coalescing bounded queues
internal/config/         Config struct, defaults, validation
tests/faultserver/       deterministic misbehaving HTTP server used by integration tests
tests/integration/       end-to-end engine tests against faultserver
tests/faultinjection/    persistence/file-write failure injection and subprocess kill tests
```

### Dependency rules

- `domain` imports nothing from the project.
- `transport` → `domain`. `strategy` → `domain`, `transport`. `storage` → `domain`.
- `downloader` → `domain`, `strategy`, `transport`, `storage`, `ratelimit`, `events`, `config`. It defines the `Store` interface it needs. All types in that interface's signatures live in `domain`, so `persistence` imports only `domain` and satisfies the interface implicitly; it never imports `downloader`.
- `cmd/swiftfetch-cli` wires concrete implementations via constructors. No package-level mutable state.

### Interfaces (only where substitution is needed)

- `downloader.Store` — SQLite in production, in-memory fake in unit tests, failure-injecting wrapper in fault tests.
- `domain.Clock` (`Now`, `NewTimer`, `After`) — real clock vs fake clock for backoff and controller tests.
- `http.RoundTripper` — injectable for fault injection; production uses a configured `*http.Transport`.

No other interfaces, factories or manager types are introduced without a concrete need.

### Third-party dependencies

| Module | Purpose |
|---|---|
| `modernc.org/sqlite` | Pure-Go SQLite driver (no cgo; easy cross-compilation) |
| `golang.org/x/time/rate` | Token-bucket bandwidth limiting |
| `golang.org/x/sync/errgroup` | Structured worker lifecycles |
| `golang.org/x/sys` | `renameat2`, `renamex_np`, `MoveFileEx`, `fallocate` |
| `github.com/google/uuid` | Task IDs |

Versions, maintenance status and licences are verified at plan time; nothing else is added without justification. SHA-256 comes from the standard library (BLAKE3 not needed).

## 3. Domain model

### 3.1 States and transitions

States: `queued`, `inspecting`, `downloading`, `paused`, `retrying`, `verifying`, `completed`, `failed`, `cancelled`.

| From | Allowed to |
|---|---|
| queued | inspecting, paused, cancelled |
| inspecting | downloading, retrying, failed, paused, cancelled |
| downloading | verifying, retrying, paused, failed, cancelled |
| retrying | inspecting, paused, failed, cancelled |
| paused | queued, cancelled |
| verifying | completed, downloading (re-fetch failed segments only), failed, cancelled |
| failed | queued (manual retry), cancelled |
| completed | — (terminal) |
| cancelled | — (terminal) |

- Transitions are applied only through `domain.Transition(from, to) error`; any pair not in the table returns `ErrInvalidTransition`. The table is tested exhaustively (all 81 pairs).
- Every resume (`paused → queued → inspecting`) and every retry re-enters `inspecting`, which performs revalidation (Strategy D).
- Removing a task's history is a persistence operation, not a state.

### 3.2 Ranges

- `ByteRange{Start, End int64}` — half-open `[Start, End)`. All offsets and lengths are `int64`; constructors reject negative values and `Start > End`. Arithmetic helpers check for overflow.
- `RangeSet` — sorted, non-overlapping, coalesced set of ranges with `Add`, `Contains`, `Missing(total) []ByteRange`, `Len() int64`. Property tests verify invariants under random operations.

### 3.3 Error kinds

`ErrorKind`: `Transient`, `Offline` (connectivity lost: DNS failure, network/host unreachable, connection refused, dial timeout), `Permanent`, `Auth`, `Integrity`, `Cancelled`, `RemoteChanged`, `Policy` (blocked by security policy), `Local` (disk/filesystem). Errors carry a kind, a redacted message, an optional `RetryAfter`, and a recommended user action string. `errors.Is/As` work through wrapping.

## 4. Inspection and probing

All probe requests are bounded: 15 s per request timeout, max 10 redirects, `MaxResponseHeaderBytes` 1 MiB, body reads capped at expected length + 1 byte, bodies always closed promptly.

### 4.1 Sequence

1. **Validate URL.** Scheme must be `http` or `https`; host required; total length ≤ 8 KiB.
2. **Probe 1 — `GET` with `Range: bytes=0-0`.** HEAD is not sent: one ranged GET yields the same metadata plus range evidence, and HEAD responses are never trusted for size or range support anyway.
   - **206**, `Content-Range: bytes 0-0/N`, body exactly 1 byte → range candidate; size `N` known.
   - **206** with malformed/mismatched `Content-Range`, unknown total (`/*`), or wrong body length → ranges *not* proven; record the anomaly.
   - **200** → server ignored the range. Close the body immediately without reading it. Size from `Content-Length` if present, else unknown.
   - **416** with `Content-Range: bytes */0` → empty resource (size 0).
   - **401/403** → `Auth`. **404/410** → `Permanent`.
   - **429/503** → `Transient` with `Retry-After`; the inspection retries under the retry policy.
3. **Probe 2** — only if probe 1 proved a range candidate *and* `N ≥ MinMultipartSize`. Request a 2-byte range at `floor(N/2)`, with `If-Range: <etag>` when a strong ETag exists. Ranges are proven only if: status 206, `Content-Range` exactly matches the requested interval, total equals `N`, body length is 2, and ETag/Last-Modified equal probe 1's values.
4. **Validator classification:** strong ETag > `Last-Modified` > none. Weak ETags (`W/"…"`) count as none for combining ranges, because `If-Range` requires a strong validator.
5. **Signed/expiring URL detection** by query keys (case-insensitive): `X-Amz-Signature`, `X-Amz-Credential`, `X-Goog-Signature`, `Signature`, `Expires`, `sig`, `se`, `sp`, `token`, `Policy`, `Key-Pair-Id`. Sets `SignedURL=true`; drives redaction and the "URL may expire" hint.
6. **Filename resolution:** `Content-Disposition` `filename*` (RFC 5987/6266) → `filename` → last non-empty segment of the final URL path (percent-decoded) → `download`. Always sanitized (§11.4).
7. **Dynamic/streamed heuristics:** unknown size, `Transfer-Encoding: chunked` without length, or `Cache-Control: no-store` with no validator → recorded as "likely dynamic".

### 4.2 Observations

`strategy.Observations` captures: original URL, final URL, redirect chain, status codes, HTTP protocol version, size and whether known, content type, ETag (and strength), Last-Modified, range-probe results (per probe: requested range, status, returned Content-Range, body length, validators), anomalies list, signed-URL flag, dynamic flag, measured probe RTTs, probe retries/timeouts, filename candidate and its source.

## 5. Strategy selection

`strategy.Select(obs Observations, cfg Config, resume *ResumeState) Decision` is a pure, deterministic function.

### 5.1 Rules (evaluated in order)

- **E — Unsupported/restricted:** `Auth`, `Permanent`, `Policy` errors, or retry budget exhausted during inspection. Decision includes the reason and a recommended action.
- **D — Resume:** `resume != nil`. Compare stored validator, size and URL identity with fresh observations.
  - Match (strong ETag equal, or Last-Modified equal and size equal when no ETag) and ranges still proven → resume using verified segments; sub-strategy B or C, or A-from-offset (single stream with `Range: bytes=pos-` plus `If-Range`) if previously single-stream.
  - Mismatch → `RemoteChanged`: never combine old bytes. Task moves to `paused` with reason "remote file changed". Restart from zero requires user confirmation, or happens automatically when `cfg.RestartOnRemoteChange = true` (default `false`).
  - No validator stored → resume is not provably safe; same handling as mismatch unless the user confirms.
- **A — Single stream:** size unknown, ranges not proven, `N < MinMultipartSize`, no usable validator, `cfg.MaxConnections == 1`, or dynamic resource.
- **C — Conservative:** ranges proven and size large enough, but probe RTT > 400 ms, or any probe timed out/was retried, or `cfg.ConservativeMode = true`. Initial concurrency 1, max 2, chunk minimum 4 MiB.
- **B — Adaptive multipart:** ranges proven, `N ≥ MinMultipartSize`, validator present. Initial concurrency 2, max `cfg.MaxConnections` (default 8, hard cap 16).

Manual override (`--conns N`) sets initial = max = N but still requires proven ranges; it never forces multipart on an unproven server.

### 5.2 Decision

```go
type Decision struct {
    Strategy          Strategy      // A, B, C, D(+sub), E
    Confidence        Confidence    // High: strong ETag + two consistent probes; Medium: Last-Modified only; Low: otherwise
    Size              int64
    SizeKnown         bool
    RangesProven      bool
    Validator         ValidatorKind // StrongETag, LastModified, None
    InitialConns      int
    MaxConns          int
    ChunkSize         int64
    Probes            []ProbeResult
    Reasons           []string      // human-readable, why enabled/disabled
    ChangeTriggers    []string      // e.g. "drop to conservative after repeated timeouts"
}
```

Decisions are persisted as JSON with the task and printed by `swiftfetch-cli explain`.

## 6. Multipart engine

### 6.1 Segments

- Initial chunk size: `clamp(N / (MaxConns × 4), 1 MiB, 64 MiB)` rounded down to a MiB multiple; strategy C uses min 4 MiB. Segment count ≤ 4096 (chunk size grows to satisfy this for very large files).
- `Segment{Start, End (atomic), Pos, Hasher sha256 state, Owner, Done}`.
- The scheduler owns all segments under one mutex. States: pending, owned(workerID), done. `Acquire(workerID)` returns the next pending segment; `Release(seg)` returns it to pending keeping `Pos` and hasher state; `Complete(seg)` marks it done with its final hash.
- A released segment resumes from `Pos` with the restored hasher, by whichever worker acquires it next.

### 6.2 Endgame splitting (work stealing)

When there are no pending segments, a worker is idle, and an owned segment's remaining bytes > 2 × `MinSplitSize` (1 MiB) with estimated remaining time > 2 × the median of the others:

- Split point `S = max(Pos + BufferSize, Start + (End - Start)/2)` (aligned to 64 KiB). If `S ≥ End - MinSplitSize`, don't split.
- The scheduler atomically sets the owner's `End = S` and creates a new pending segment `[S, oldEnd)`.
- Workers check `End` before every write and truncate the write to `End`; reaching `End` closes the HTTP body early. Since `S > Pos + BufferSize`, no already-in-flight buffer crosses `S`.
- Invariant (property-tested): the union of all segments is exactly `[0, N)` and segments are pairwise disjoint at all times.

### 6.3 Worker loop

For each acquired segment: wait for connection semaphore → build request `Range: bytes=Pos-(End-1)` with `If-Range` when strong ETag → validate response (§6.4) → stream into a pooled 64 KiB buffer → for each read: wait on per-download and global rate limiters → `WriteAt(buf, offset)` → update hasher → advance `Pos` → report bytes to the sample accumulator. On error: classify; release segment; apply backoff; retry under budget.

### 6.4 Response validation

- Status must be 206; `Content-Range` start/end must equal the request; total must equal `N`; ETag/Last-Modified must equal the Decision's validators.
- Validator or total mismatch → `RemoteChanged`: the whole multipart run aborts immediately (no mixing) and the task follows §5.1 D-mismatch handling.
- Mismatched `Content-Range` interval with matching validators → `Integrity` for that segment; the body is discarded and the segment retried.
- 200 to a ranged request (server stopped honouring ranges): close the body and retry the segment. After 3 such responses within the task, switch to **fill mode**: concurrency 1, one `GET` without `Range`; validate its ETag/Last-Modified/`Content-Length` against the stored values (mismatch → `RemoteChanged`); stream the full body, writing and hashing only bytes that fall in missing ranges and discarding the rest. This wastes bandwidth but never discards verified progress or mixes representations.
- Body shorter than `Content-Length` / requested length → `Transient`; resume from `Pos`.
- Body longer than requested → stop reading at `End`, treat as `Integrity` anomaly logged once, keep the correctly-bounded bytes only if the range header matched.

### 6.5 Storage

- Temp file: `<destdir>/<name>.swiftfetch-part`, in the destination directory so finalization is a same-filesystem rename.
- Created with `O_CREATE|O_EXCL` (never reuses an unknown file), mode `0644`, preallocated with `Truncate(N)`; on Linux additionally `fallocate(FALLOC_FL_KEEP_SIZE)` where supported (failure is non-fatal).
- All writes are `WriteAt`; there is no shared file offset. Windows `WriteAt` via `os.File` uses overlapped positional writes and is covered by the same tests in CI.
- Memory bound: `connections × 64 KiB` buffers + small per-segment state.
- Files > 4 GiB: covered by a sparse-file test (fault server serves a deterministic synthetic 5 GiB body generated from offset, without storing it).

## 7. Single-stream engine

- `GET` without `Range` (fresh) or with `Range: bytes=Pos-` + `If-Range` (resume when ranges proven).
- On resume, if the server answers 200 to a ranged request → representation may differ or ranges ignored: restart only per §5.1 D handling.
- Unknown size: stream until EOF; preallocation skipped; the whole stream is one segment with `End` updated as bytes arrive; resume is impossible without ranges, so an interrupted unknown-size download restarts (communicated in the Decision's reasons).
- Same checkpointing, hashing, rate limiting and finalization as multipart.

## 8. Adaptive concurrency controller

`strategy.Controller` is a pure state machine: `Observe(Sample) Action`, where `Sample` is aggregated per window and `Action` is `Hold | Increase | Decrease(n) | SwitchConservative | Backoff(d)`.

- Window ≥ 2 s and ≥ 256 KiB of data (whichever comes later, max 10 s). `Sample` = useful bytes (bytes actually written to disk and accounted), active conns, errors by kind, 429/503 count with `Retry-After`, timeouts/resets, fraction of worker time blocked in `WriteAt`.
- Throughput smoothed by EWMA (α = 0.3). The controller remembers the best EWMA observed at each connection count.
- **Increase:** after 2 consecutive stable windows (no errors) and `N < Max`, try `N+1`. After 2 windows at `N+1`, keep it only if EWMA improved ≥ 10%; otherwise revert to `N` and set a cooldown of 6 windows before probing again. Time elapsing alone never increases N.
- **429/503:** `N = max(1, N/2)`, task-level backoff honouring `Retry-After` (capped at 10 min), cooldown 12 windows.
- **Timeouts/resets ≥ 2 in a window:** `N-1`. Three consecutive such windows → `SwitchConservative` (max 2, larger chunks). Recovery back to B requires 10 stable windows.
- **Disk-bound:** if write-blocked fraction > 50%, never increase.
- Decrease is applied by retiring workers at their next segment boundary (no mid-transfer kill). Increase spawns a worker only if the global connection semaphore permits.
- Signatures (documented and tested via simulation): per-connection-capped links → throughput scales with N → controller climbs; bandwidth-capped links → no gain → controller holds low; throttling → 429s → controller halves.

### 8.1 Harsh-network survival

Every download must survive harsh networks (high loss, high latency, very low bandwidth, half-open connections, outages) without failing or losing verified progress.

- **Progress-aware retry budget:** `TaskRetryBudget` and `MaxSegmentAttempts` count only retries without progress. Both reset after ≥ 1 MiB of new verified bytes or a completed segment. A flaky link that keeps making progress never exhausts them.
- **Waiting for network:** `Offline` errors do not spend any budget. The task stays in `retrying` with reason "waiting for network". It runs a cheap connectivity check (TCP dial to the origin host:port), starting at 5 s and backing off ×2 to 60 s, emitting a `RetryScheduled` event every check. On success it resumes via `inspecting`. After `OfflineWaitLimit` (default 24 h; 0 = forever) of continuous offline time it fails with `Offline` and the action "network unavailable — resume when connected"; verified progress is kept.
- **Timeouts suited to slow links:** no overall per-request timeout for downloads; dial 30 s, TLS handshake 30 s, response headers 60 s, TCP keepalive 15 s, HTTP/2 `ReadIdleTimeout` 30 s and `PingTimeout` 15 s (detects half-open connections). Body transfer is governed only by the adaptive stall detector (§8.2).
- Inspection uses the same offline handling, so adding a URL while offline waits instead of failing.

### 8.2 Stall detection

A worker is stalled only if no bytes arrive for `max(StallTimeout (30 s), 10 × median inter-read gap of that worker)`. A stalled request is cancelled, its segment released, and it retries via backoff. Slow-but-progressing transfers are never classified as stalled.

## 9. Checkpointing, recovery and durability invariants

### 9.1 Checkpoint

Triggered every `CheckpointInterval` (2 s) or `CheckpointBytes` (16 MiB) since the last one, and on every state transition and pause.

1. `file.Sync()` (durability boundary for data).
2. One SQLite transaction: upsert all segments — done segments with final SHA-256; in-progress segments with `Pos` and marshaled hasher state (`crypto/sha256` digest implements `encoding.BinaryMarshaler`); update task progress counters.

### 9.2 Invariants

- **I1:** A segment (or a `Pos` within it) is recorded in SQLite only after the bytes up to that point have been `Sync`ed.
- **I2:** Bytes are never reused unless their hash verifies on recovery.
- **I3:** Bytes from different remote representations are never combined: every response is validated against the task's stored validators, and resume requires matching validators.
- **I4:** The destination path is never overwritten unless the task's existing-file policy is `overwrite`.
- **I5:** Every byte in `[0, N)` is covered by exactly one segment at all times.
- **I6:** The task is marked `completed` only after the final file is in place and its parent directory synced (Unix).

### 9.3 Recovery (on `resume` or startup of an interrupted task)

1. Load task and segments. If the temp file is missing or its size ≠ `N` → discard all segment progress (restart within the task, after the D revalidation).
2. For each done segment: re-hash `[Start, End)` from disk; mismatch → mark pending with `Pos = Start`.
3. For each in-progress segment: restore hasher state, re-hash `[Start, Pos)` from disk, compare with `Sum()` of the restored state; mismatch → `Pos = Start`, fresh hasher.
4. Revalidate remotely (Strategy D). Proceed only on match.

Trade-off (documented in the README): checkpoint interval bounds lost work to ≈ interval × throughput; recovery re-reads verified data from disk once (disk-read cost ≈ downloaded size), which is far cheaper than re-downloading and is the price of I2. `CheckpointInterval` is configurable (0.5–30 s).

## 10. Finalization

1. Assert all segments done and union == `[0, N)` (or EOF reached for unknown size).
2. `Sync`; `Stat` size == `N`.
3. If `ExpectedSHA256` provided: full-file hash and compare → mismatch is `Integrity` (task → `failed`, temp file kept for inspection). Otherwise result is labelled "segment-consistency verified; no trusted checksum supplied" — never "authentic".
4. Close the file.
5. Resolve destination conflicts per policy (decided at task creation, re-checked now): `rename` (default; `name (1).ext`, `name (2).ext`, …), `overwrite` (explicit only), `fail`.
6. No-replace rename into place: Linux `renameat2(RENAME_NOREPLACE)`, falling back to `link`+`unlink` when unsupported (e.g. some network filesystems); macOS `renamex_np(RENAME_EXCL)`; Windows `MoveFileEx` without `MOVEFILE_REPLACE_EXISTING` (with `MOVEFILE_WRITE_THROUGH`). `overwrite` uses plain `os.Rename` (Unix) / `MoveFileEx` with `REPLACE_EXISTING` (Windows). On `EEXIST` with `rename` policy, pick the next suffix and retry (bounded to 1000).
7. Unix: fsync the parent directory.
8. Persist `completed` with final path and verification result; delete segment rows.

## 11. Transport and security

### 11.1 Threat model

Untrusted URLs (pasted by the user, possibly from hostile pages) and untrusted servers. Concerns: SSRF into the local network via redirects/DNS rebinding, path traversal via server-supplied filenames, credential leakage into logs/diagnostics/redirect targets, resource exhaustion via huge headers or endless redirects, silent overwrite of user files. Out of scope: malware scanning of downloaded content (files are never executed).

### 11.2 Address policy

Configurable `NetworkPolicy`: `AllowDirectPrivate` (default), `AllowAll`, `BlockPrivate`.

- Default: loopback, private (RFC 1918, ULA), link-local, and unspecified addresses are permitted only if the user-supplied URL's host itself is such an address or resolves to one. If the original host is public, any connection (including after redirects) to a non-public address is refused with a `Policy` error.
- Enforced in `net.Dialer.Control` against the actual resolved IP for every connection, which defeats DNS rebinding between probe and download.

### 11.3 Redirects

Max 10. `https → http` downgrade refused by default (`Policy`). `Authorization` header and URL userinfo are dropped on cross-host redirects. The full chain is recorded (redacted) in Observations.

### 11.4 Filename and path safety

Sanitization: Unicode NFC is not applied (no extra dependency); strip `/`, `\`, NUL and C0/C1 control characters; strip leading dots and leading/trailing whitespace; strip trailing dots and spaces (Windows); rename Windows reserved device names (`CON`, `PRN`, `AUX`, `NUL`, `COM1-9`, `LPT1-9`, with any extension) by suffixing `_`; replace `<>:"|?*` with `_`; cap at 200 bytes without splitting a UTF-8 sequence, preserving the extension where possible; empty result → `download`. The final path is `filepath.Join(destDir, name)` followed by a containment check (`filepath.Rel` must not start with `..`).

### 11.5 Redaction

A single `transport.Redact` is used for every log line, event, stored error, and diagnostic: removes userinfo; replaces values of signed/sensitive query keys (§4.1 step 5 list plus `access_token`, `api_key`, `key`, `password`, `auth`) with `REDACTED`; never emits `Authorization`/`Cookie` headers. Fuzz-tested to never panic and to never output a sensitive value present in input.

### 11.6 TLS

System roots, verification always on, no option to disable. TLS errors are `Permanent` with an explanatory message.

## 12. Events

- Event types: `StateChanged`, `Progress{Downloaded, Total, SpeedEWMA, ActiveConns, ETA (nil when unknown)}`, `DecisionMade`, `RetryScheduled{Attempt, Kind, Delay, Reason}`, `StrategyChanged`, `Warning`, `Resync`.
- `events.Bus` with per-subscriber queues. Progress is coalesced latest-wins per task, emitted at most `ProgressHz` (default 4) times per second. Non-progress events go through a bounded queue (256). If it overflows, the queue is cleared and a single `Resync` is delivered; the consumer then reads a snapshot from the engine. Publishing never blocks the engine.
- ETA is reported only when size is known and EWMA speed > 0 over at least 3 s.

## 13. Configuration defaults

| Setting | Default | Valid range |
|---|---|---|
| MaxConnections (per download) | 8 | 1–16 |
| GlobalMaxConnections | 16 | 1–64 |
| InitialConnections | auto (2 for B, 1 for C) | 1–MaxConnections |
| AdaptiveConcurrency | true | — |
| MinMultipartSize | 8 MiB | ≥ 1 MiB |
| ChunkSizeMin / Max | 1 MiB / 64 MiB | min ≤ max |
| GlobalRateLimit / PerDownloadRateLimit | 0 (unlimited) | ≥ 0 bytes/s |
| RetryBaseDelay / Max / Factor | 500 ms / 60 s / 2 | — |
| MaxSegmentAttempts / TaskRetryBudget | 8 / 50 | ≥ 1 |
| StallTimeout | 30 s | 5–600 s |
| CheckpointInterval / CheckpointBytes | 2 s / 16 MiB | 0.5–30 s / ≥ 1 MiB |
| ProbeTimeout | 15 s | 1–120 s |
| OfflineWaitLimit | 24 h | 0 (forever) or ≥ 1 min |
| ExistingFilePolicy | rename | rename, overwrite, fail |
| RestartOnRemoteChange | false | — |
| ConservativeMode | false | — |
| NetworkPolicy | AllowDirectPrivate | — |
| AllowHTTPSDowngrade | false | — |
| ProgressHz | 4 | 1–30 |
| DownloadDir | OS downloads dir (`$XDG_DOWNLOAD_DIR` or `~/Downloads`) | existing dir |
| DataDir | `$XDG_DATA_HOME/swiftfetch` / `%LOCALAPPDATA%\SwiftFetch` / `~/Library/Application Support/SwiftFetch` | — |

`config.Validate` rejects out-of-range values with field-specific errors; the CLI refuses to start on invalid config. Forcing more connections via `--conns` prints a warning that it can reduce throughput or trigger throttling.

## 14. Persistence (SQLite)

- Driver `modernc.org/sqlite`. Pragmas: `journal_mode=WAL`, `synchronous=FULL`, `foreign_keys=ON`, `busy_timeout=5000`. DB at `<DataDir>/swiftfetch.db`, file mode `0600`, directory `0700`.
- Migrations embedded via `embed`, applied in order inside transactions, tracked in `schema_version`. Opening a DB with a newer schema than the binary supports fails with a clear error (no downgrade).
- Tables:
  - `tasks(id TEXT PK, original_url, final_url, dest_dir, file_name, temp_path, size INTEGER, size_known INTEGER, etag, etag_strong INTEGER, last_modified, strategy, decision_json, state, last_error_kind, last_error_msg, expected_sha256, existing_policy, settings_json, final_path, verify_result, created_at, updated_at)`
  - `segments(task_id FK, start INTEGER, end INTEGER, pos INTEGER, done INTEGER, hash BLOB, hasher_state BLOB, PRIMARY KEY(task_id, start))`
  - `attempts(id INTEGER PK, task_id FK, at, kind, message, delay_ms)` — messages stored redacted; capped at 200 rows per task (oldest pruned).
  - `schema_version(version INTEGER)`
- **Known limitation:** URLs (including signed query parameters) are stored in clear text, protected only by file permissions. OS-keychain encryption is future work. Diagnostics and events always use redacted forms.

## 15. CLI (`swiftfetch-cli`)

- `get URL [-o DIR] [--conns auto|N] [--limit RATE] [--sha256 HEX] [--on-exists rename|overwrite|fail] [--conservative]`
- `explain URL` — runs inspection and prints the Decision (strategy, confidence, reasons, probes); downloads nothing beyond probe bytes.
- `list` — tasks with state, progress, redacted URL.
- `resume ID` — recovery + Strategy D; prompts if the remote changed (`--restart-if-changed` to auto-confirm).
- `rm ID [--delete-partial]` — removes task history; deletes the temp file only with the flag; never deletes completed files.
- Progress line redrawn ≤ 4 Hz: name, percent, downloaded/total, speed, ETA (or "—"), connections, strategy.
- Ctrl-C: first press → graceful pause (workers cancelled, final checkpoint, state `paused`); second press within 2 s → immediate exit (state recovered on next run).
- Exit codes: 0 success, 1 failed, 2 usage/config error, 3 paused/interrupted.

## 16. Concurrency and lifecycle

- `Engine.Run(ctx, taskID)` owns one task. Workers run under an `errgroup` derived from a per-task context; pause and cancel are distinct: pause cancels the context with cause `ErrPaused` and checkpoints; cancel cancels with `ErrCancelled` and moves to `cancelled`.
- A per-task run guard prevents a second concurrent `Run` for the same task (prevents duplicate workers on rapid pause/resume).
- All channels are bounded. Shared counters use `sync/atomic`; segment state is protected by the scheduler mutex.
- Shutdown: stop accepting new runs, pause all running tasks, wait for final checkpoints (bounded by 10 s), close DB.
- Leak checks: tests assert goroutine count returns to baseline after cancellation (`runtime.NumGoroutine` with a settle loop).

## 17. Logging

`log/slog` with a JSON or text handler. Logged: lifecycle transitions, decisions (summary), retries, strategy changes, recovery actions, finalize result. Never logged: per-read events, raw URLs, headers. All URL-bearing attributes pass through `Redact`.

## 18. Testing strategy

TDD for all of `domain`, `transport` parsers, `strategy`, `downloader`, `storage`, `persistence`.

### 18.1 Fault server (`tests/faultserver`)

An `httptest.Server`-based handler with deterministic content (byte at offset `i` = function of `(seed, i)`, so large files need no storage) and per-test configurable behaviours:

correct ranges; ignore ranges (200); malformed `Content-Range`; wrong total in `Content-Range`; lying `Content-Length` (short/long); ETag changes after K requests; weak ETag; no validators; Last-Modified only; redirect chains (incl. to private address and https→http); expiring URLs (403 after T); 401, 403, 404, 410, 416; 429 and 503 with `Retry-After`; fixed and variable bandwidth (token bucket per connection and global); per-connection throughput cap; connection resets after B bytes; mid-stream truncation; randomized chunk delays; corrupted bytes at given offsets; premature server close; chunked/unknown length; empty file; 5 GiB synthetic file; max-concurrent-connection limit (excess get 503); random connection drops (probability per written piece); blackhole (accept, send headers and part of the body, then hang); per-write latency (simulating 1–2 s RTT); bandwidth down to 8 KB/s; outage windows (listener refuses connections for N seconds, then recovers); injected DNS failures (via a test resolver/dialer hook).

### 18.2 Test inventory

- **Unit:** state table (all pairs); RangeSet properties; segment scheduler properties (random acquire/release/split/complete sequences — disjoint, complete, exactly-once); selector table tests covering every rule; controller simulations with fake clock and modelled links (per-conn cap, bandwidth cap, throttling, flaky); backoff/jitter bounds; config validation; filename sanitization; redaction.
- **Fuzz:** `Content-Range`, `Content-Disposition`, filename sanitizer, redactor.
- **Integration:** engine downloads against every fault-server mode; each completed file's SHA-256 compared with the generator's expected hash; mode-specific assertions (e.g. ETag change → `RemoteChanged`, never a mixed file; ignore-ranges → strategy A; 429 storm → concurrency decreases and download completes).
- **Pause/resume and cancellation races:** randomized pause/resume/cancel timings in loops under `-race`; assert no duplicate workers, correct final bytes, no goroutine leaks.
- **Fault injection:** wrapper `Store` failing the Nth transaction; wrapper file failing the Nth `WriteAt`/`Sync`; assert recovery never trusts unverified bytes.
- **Crash test:** test binary re-execs itself as a child that downloads from a fault server; parent `SIGKILL`s it at a random byte offset (20 randomized runs; Windows uses `Process.Kill`); restart recovery; final file byte-identical; assert that recovered segments were hash-verified.
- **Finalization:** no-replace rename behaviour per OS (existing destination preserved), suffix selection, overwrite opt-in.
- **Harsh-network soak:** single-stream and multipart, 5 seeds each: 20% drops, 800 ms latency, 64 KB/s, one 15 s outage and one blackhole. Every run must finish byte-identical with no task failure and bounded memory. A short variant runs in `-short`; the full soak runs otherwise.
- **Memory bound:** 1 GiB download; assert peak heap (`runtime.ReadMemStats` sampling) stays under a fixed bound (connections × buffer + 16 MiB).
- **Benchmarks (smoke only in this sub-project):** single vs multipart on local fault server with per-connection caps; inspection overhead. Full comparative benchmarking is sub-project 4.

All tests run with `go test -race ./...`. `go vet` and `golangci-lint` (installed via `go install`, config in `.golangci.yml` enabling `govet`, `staticcheck`, `errcheck`, `ineffassign`, `unused`, `gosec`, `revive` with documented exclusions) must be clean. A `Makefile` provides `test`, `race`, `lint`, `fuzz`, `build`, `cross` (cross-compile CLI for linux/windows/darwin × amd64/arm64).

## 19. Acceptance criteria (sub-project 1)

1. All unit, fuzz-seed, integration and fault-injection tests pass, including under `-race`.
2. `explain` yields the expected strategy and reasons for every fault-server mode (golden tests).
3. Crash test passes 20/20 randomized kill points on Linux.
4. Downloads are byte-correct with ignored ranges, ETag change mid-transfer (task stops with `RemoteChanged`, no mixed output), 429 storms, resets, truncation, and corrupted responses (corruption is detected when an expected SHA-256 is supplied; otherwise documented as undetectable by design).
5. Controller simulations show: climbs on per-connection-capped links, holds on bandwidth-capped links, halves under 429.
6. The harsh-network soak passes for all seeds; offline periods never spend retry budget; outages shorter than `OfflineWaitLimit` never fail a task.
7. Peak heap stays within the memory bound for a 1 GiB download.
8. Existing destination files are never overwritten without `overwrite` policy.
9. `go vet`, `gofmt`, `golangci-lint` clean; CLI cross-compiles for all six OS/arch targets.
10. README section documents usage, durability trade-offs, and known limitations.

## 20. Known limitations (to be documented)

- Corruption that preserves length and headers is detectable only with a user-supplied expected checksum; per-segment hashes protect against *local* disk corruption and crash damage, not against a server sending wrong bytes.
- Unknown-size downloads cannot be resumed after interruption.
- Persisted URLs are plaintext (file-permission protected).
- `link`+`unlink` fallback finalization is not atomic against a concurrent creator on exotic filesystems; it still never overwrites.
- Windows and macOS correctness is verified by CI (sub-project 4); in this sub-project they are cross-compiled only.
