# SwiftFetch — Original Product Brief (verbatim summary of record)

> Source: the user's full product brief, re-shared 2026-10-18. This file preserves it so later sub-projects
> (2: queue service, 3: Gio UI, 4: hardening/release) can be specified from it. The core-engine spec
> (2026-10-17-swiftfetch-core-engine-design.md) covers sub-project 1 and decomposes the brief.

## Role & vision
Production-quality, cross-platform (Linux/Windows/macOS) desktop download manager in **Go + Gio UI**.
Priorities in order: (1) intelligent strategy selection, (2) integrity/resumability/fault tolerance,
(3) low-bandwidth/unstable networks, (4) efficient adaptive multipart, (5) smooth accessible beautiful UX,
(6) maintainable, modular, well-tested code. Optimize real-world completion time, not peak throughput.

## Stack
Latest stable Go; Gio UI (gioui.org) as the real desktop UI — no web frontend/embedded browser/local server.
Stdlib networking; golang.org/x/sync/errgroup; golang.org/x/time/rate; modernc.org/sqlite; google/uuid;
SHA-256 (optional blake3). gofmt, go vet, golangci-lint, -race, unit/integration/stress/fault-injection
tests, cross-compilation + native CI builds, reproducible release builds.

## Strategy engine (§4 of brief) — implemented in sub-project 1
Resource inspection (final URL, redirects, filename, length, Accept-Ranges, Content-Range, ETag,
Last-Modified, proto, auth, content type, dynamic/streamed, signed URLs, range consistency). Prove
multipart with bounded range probes (206 + Content-Range + body length; second probe). Strategies
A single, B adaptive multipart, C conservative, D resume, E unsupported. Structured, deterministic
decision output shown in the UI details panel.

## Adaptive concurrency, multipart, fault tolerance, low bandwidth (§5–§8) — sub-project 1
EWMA + hysteresis, reversible changes, 429/503/Retry-After, bounded requests, per-download and global
limits, work stealing, exact-once range accounting, 64-bit offsets, preallocated temp + positional
writes, durable SQLite state machine (queued, inspecting, downloading, paused, retrying, verifying,
completed, failed, cancelled), checkpointing with sync-before-record, safe atomic finalization without
silent overwrite, strong-validator resume, signed-URL refresh preserving task identity when revalidated,
bounded backoff+jitter, error classification, global bandwidth limits, stall vs slow detection.

## UI and UX (§9) — sub-project 3 (Gio)
Minimal, calm, modern; restrained palette; dark+light themes; accessible contrast + keyboard nav;
responsive layouts; design system (spacing, typography, colors, radii, interaction states); subtle
animation; no clutter.
Main screen: header, prominent add-download, URL input/dialog, search+filter, sections for active,
queued, paused, completed, failed; counts and aggregate stats; settings access.
Row: filename+type, status, downloaded/total, %, speed, ETA only when meaningful, pause/resume, cancel,
context menu, "inspecting / validating strategy" indicator.
Details view: original/final URL (redacted), destination, size, current/avg speed, connections, strategy,
range support, chunks done/remaining, retry history, last error + action, integrity status, resume
capability, diagnostics.
Interactions: drag-and-drop URLs, keyboard shortcuts, clipboard entry, duplicate detection, filename/
destination conflict resolution, pause/resume/cancel/retry/remove, open folder, open file, copy URL/path,
destination file picker, multi-select + bulk actions, destructive-action confirmation, success/warning/
failure feedback, empty/loading/offline/error states, long filenames, focus indicators + tab order.
Never block the Gio event loop; bounded command/event channel; coalesce progress updates. No fake buttons.

## Architecture (§10–§11)
Suggested layout includes internal/app (application, lifecycle, commands), internal/services
(download_service, queue_service), internal/platform (files, integration), internal/ui (app, theme,
components, screens, dialogs, state), cmd/swiftfetch/main.go. Domain/engine never import Gio; UI never
does networking/persistence directly; constructor injection; context propagation; typed commands/events;
no god objects/giant files/needless abstractions. Structured goroutine lifecycles, bounded queues,
backpressure, pause≠cancel, graceful shutdown, safe recovery, bounded UI event delivery.

## Security (§12)
TLS verification always; safe redirects; private/loopback protection with DNS-rebinding consideration and
user-configurable policy; bounded probes/headers/filenames; path traversal prevention; credential
redaction; secure persistence of sensitive data; never execute downloads; no silent overwrite.

## Queue and persistence (§13) — sub-project 2
Add one or more URLs; queue; max simultaneous downloads; pause/resume individual and whole queue;
priorities; reorder; retry failed; restore on startup; search/filter history; remove history independent
of files (optionally delete partial data); destination conflicts; persisted settings; versioned schema +
migrations; duplicate handling never merges tasks with different auth/validators/destinations.

## Testing (§14), benchmarks (§15), cross-platform (§16)
Deterministic fault server (ranges ignored, malformed Content-Range, wrong length, inconsistent ETags,
weak/missing validators, redirects, expiry, 401/403/404/416/429/503, Retry-After, slow/variable
bandwidth, resets, truncation, delays, corruption, premature close, unknown length, empty, large).
Test strategy, probing, fallback, scheduling, concurrent writes, retry, pause/resume, restart recovery,
validator changes, checkpoint consistency, finalization, checksum, sanitization, queue persistence,
shutdown, cancellation races, UI event coalescing, cleanup. Property tests. Benchmarks (incl. vs curl/
aria2, honest methodology, no fabricated numbers). Native builds per OS, file pickers, open-file, data/
config dirs, packaging (Linux AppImage or similar, Windows exe, macOS signing/notarization notes); CI
native builds — cross-compiling is not proof GUI integration works.

## Configuration (§17)
Max simultaneous downloads; initial/max connections; adaptive; multipart min size; chunk policy;
global/per-download bandwidth; retry policy; stall detection; checkpoint interval; download dir;
existing-file behaviour; auto recovery; theme; startup behaviour; diagnostic logging. Auto by default;
manual connection override with warning; validated.

## Observability (§18)
Structured logs of lifecycle, decisions, retries, recovery; no per-read logs; diagnostics view/export
(version, Go/OS, sanitized metadata, strategy rationale, retries, integrity result, errors) with
redaction of auth headers, cookies, signed params, sensitive paths.

## Workflow (§19) and milestones (§20)
Superpowers methodology: plan → test-first → incremental integration (always buildable) →
root-cause debugging with regression tests → delivery checks. Milestones: 1 architecture/spec,
2 single-stream, 3 inspection/strategy, 4 multipart/adaptive, 5 durability/recovery, 6 Gio UI,
7 hardening, 8 release readiness. Report at each milestone: what was built, decisions, files, tests and
actual results, limitations, next step.

## Definition of done (§21)
Functional Gio app; verified single-stream; evidence-based multipart; correct fallback; bounded adaptive
concurrency; safe pause/resume; crash recovery; no mixed representations; correct bytes; safe finalize
and conflicts; persistent queue and settings; responsive UI during transfers; tested cancel/shutdown;
passing tests + race detection; per-OS build/test guidance; no placeholder features; documented limits.
