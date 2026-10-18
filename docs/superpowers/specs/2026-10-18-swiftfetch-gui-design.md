# SwiftFetch GUI (Gio) + thin queue — design

Date: 2026-10-18. Status: approved for planning (user delegated decisions: "do all autonomously").
Builds on sub-project 1 (core engine, branch `feat/core-engine`, spec
`2026-10-17-swiftfetch-core-engine-design.md`). Product brief: `2026-10-17-swiftfetch-product-brief.md` §9, §13.

## 1. Context, scope and decisions

The engine runs one task per `Engine.Run` call and has no queue. A desktop GUI needs a queue
layer, so this sub-project ships **a thin queue + the Gio desktop app + a system-tray presence**.

Decisions taken with the user (2026-10-18):

| # | Decision |
|---|----------|
| D1 | Option A: thin queue inside this spec; full queue features deferred. |
| D2 | UI scope "complete core": main list with sections, add dialog, row actions, details panel, settings, dark/light, keyboard nav + focus, destructive confirmation, clipboard paste, empty/error/offline states. |
| D3 | Closing the window keeps downloads running in the **system tray** (`fyne.io/systray`). Fallback when no tray host is available: close = pause all + quit. |
| D4 | Architecture: **commands in, snapshots out** (typed command channel → app goroutine; state goroutine → immutable snapshot → `Invalidate`). |
| D5 | Single instance via a lock file; a second launch prints "already running" and exits (no IPC). |
| D6 | CLI keeps working on the same data dir; per-task cross-process locks already prevent double-driving. |

### In scope
- Thin queue: max simultaneous downloads, FIFO auto-start, pause/resume one or all, startup restore
  (optional auto-resume), retry, remove (optionally delete partial), restart-from-zero after remote change.
- Persisted settings (versioned migration `0002`).
- Gio app `cmd/swiftfetch`, system tray, file/folder open, clipboard, folder picker.

### Deferred (later "queue + polish" phase)
Priorities, manual reorder, history search/filter, duplicate detection/merging rules, drag-and-drop
URLs, multi-select + bulk actions, rich animation, second-launch activation (IPC), packaging/signing.

### Non-goals
No web UI, embedded browser or local server. No change to download semantics of the engine.

## 2. Architecture

```
cmd/swiftfetch/main.go         wiring; runs app.Main() on the main goroutine
internal/settings/             Settings type, defaults, validation, SQLite repo (migration 0002)
internal/services/queue.go     Queue: slots, FIFO start, pause/resume all, restore
internal/app/                  App: owns Engine, Queue, Settings; Command types; command loop
internal/ui/state/             View model, Snapshot builder; consumes events.Bus (no Gio)
internal/ui/theme/             Tokens: palettes (light/dark), spacing, type scale, radii, states
internal/ui/components/        Button, IconButton, ProgressBar, TaskRow, SectionHeader, TextField, Dialog, Toast
internal/ui/screens/           MainScreen, DetailsPanel, SettingsScreen
internal/ui/dialogs/           AddDialog, ConfirmDialog
internal/ui/window.go          Window loop: events → frame → layout(snapshot); emits commands
internal/platform/             OpenPath, RevealPath, InstanceLock, format helpers
internal/tray/                 systray adapter: menu → commands, status ← snapshot
```

### Dependency rules (enforced by `internal/archtest` import test)
- `gioui.org/...` only in `internal/ui/{theme,components,screens,dialogs}`, `internal/ui` (window.go) and `cmd/swiftfetch`.
- `fyne.io/systray` only in `internal/tray`.
- `internal/ui/state`, `internal/app`, `internal/services`, `internal/settings` import neither.
- UI packages never import `downloader`, `persistence`, `transport`, `net/http`.
- Engine packages never import `app`, `services`, `ui`.

### New third-party deps
`gioui.org v0.10.3`, `gioui.org/x v0.10.3` (explorer: folder picker via xdg-desktop-portal on Linux),
`fyne.io/systray v1.12.2` (pure Go on Linux/Windows; cgo on macOS).

## 3. Engine additions (small, backward compatible)

1. `Engine.Hold(ctx, id) error` — for an **idle** task in `queued`, transition to `paused`
   (under the run guard). Running task → `ErrAlreadyRunning` (caller uses `Pause`). Other states: no-op nil
   for `paused`; `domain.ErrInvalidTransition` otherwise.
2. `Engine.SetConfig(cfg config.Config)` — replaces the config used by **runs started afterwards**
   (guarded by `e.mu`; `newTaskRun`, `Add`, `Explain` read it via `e.config()`). Running tasks keep theirs.
3. Per-task `restart_on_change` in `taskSettings`; `Engine.AllowRestart(ctx, id) error` sets it and
   persists. A run applies it as `cfg.RestartOnRemoteChange = true`. CLI behaviour is unchanged.
4. `ErrLockedElsewhere` sentinel: `lockTask` wraps `storage.ErrLocked` so callers can
   `errors.Is(err, downloader.ErrLockedElsewhere)`.
5. `Engine.Running(id) bool` — whether this process has an active Run for id.
6. `Engine.Requeue(ctx, id) error` — for an **idle** task in `paused` or `failed`, transition to
   `queued` under the run guard; `queued` → nil; running → `ErrAlreadyRunning`; crash-interrupted
   states (`inspecting/downloading/retrying/verifying` with no live run) → nil without change
   (`Run`'s `enter` settles them); other states → `domain.ErrInvalidTransition`.

## 4. Settings (`internal/settings`)

```go
type Settings struct {
    MaxSimultaneous   int    // 1-10, default 3
    ResumeOnStartup   bool   // default true
    DownloadDir       string // default config.DefaultDownloadDir()
    MaxConnections    int    // 1-16, default 8
    GlobalRateLimit   int64  // bytes/s, 0 = unlimited
    PerDownloadLimit  int64  // bytes/s, 0 = unlimited
    ExistingPolicy    domain.ExistingFilePolicy // default rename
    Theme             Theme  // system | light | dark, default system
    CloseToTray       bool   // default true
    TrayNoticeShown   bool   // first close-to-tray notice already shown
}
func Default() Settings
func (s Settings) Validate() error              // errors.Join of FieldError, like config
func (s Settings) Apply(c config.Config) config.Config
```

Persistence: migration `internal/persistence/migrations/0002_settings.sql` creates
`settings(key TEXT PRIMARY KEY, value TEXT NOT NULL)`; the repo stores one row `key='app'` with
JSON `value`. Unknown JSON fields are ignored; missing fields take defaults (forward compatible).
Invalid stored values → defaults for those fields + a logged warning (never a startup failure).

Live effect on save: `GlobalRateLimit` → `Bandwidth.SetLimit` immediately; `MaxSimultaneous` → queue
immediately (no running task is stopped when lowered; new starts wait); everything else → `Engine.SetConfig`
(new runs). The settings screen says "applies to downloads started after saving" under engine fields.

## 5. Queue (`internal/services`)

```go
type Queue struct { /* eng, store, clock, logger, bus; mu; slots int; running map[id]cancel; paused bool */ }
func NewQueue(d QueueDeps) *Queue
func (q *Queue) Start(ctx context.Context)            // runs scheduler goroutine until ctx done
func (q *Queue) Enqueue(id string)                     // wake scheduler
func (q *Queue) Pause(ctx, id) error                   // running → Engine.Pause; queued idle → Engine.Hold
func (q *Queue) Resume(ctx, id) error                  // paused/failed → queued (Engine.Requeue semantics, see below)
func (q *Queue) PauseAll(ctx) error / ResumeAll(ctx) error
func (q *Queue) SetMaxSimultaneous(n int)
func (q *Queue) Shutdown(ctx) error                    // stop scheduling; Engine.Shutdown
```

- **Order:** FIFO by `CreatedAt` (ties by ID) among tasks in state `queued` not running here.
- **Scheduler loop:** wakes on Enqueue, run completion, settings change, or a 5 s tick (picks up tasks
  the CLI added). Starts `go eng.Run(ctx, id)` while `len(running) < slots`. On return it frees the slot
  and wakes. Run's error is not shown here — the state goroutine reads it from events/store.
- **Lock held elsewhere** (`ErrLockedElsewhere`): skip that task for 30 s (`skipUntil` map), keep it
  queued, publish `events.Warning{ID, "running in another SwiftFetch process"}`.
- **Resume** of a paused/failed/interrupted task: `Engine.Requeue(ctx,id)` (§3 item 6), then `Enqueue`.
  The scheduler also runs "interrupted" tasks it was asked to resume (tracked in a `wanted` set),
  since their DB state is not `queued`.
- **PauseAll:** sets `paused=true` (scheduler starts nothing), Hold every idle queued task, Pause every
  running task. Persisted? No — on next start the tasks are already `paused` in the DB.
  **ResumeAll:** `paused=false`, Requeue every paused task, wake.
- **Restore on startup:** "interrupted" = DB state `inspecting/downloading/retrying/verifying` with no
  live run in this process (left by a crash or a killed CLI). With `ResumeOnStartup` (default): all
  `queued` and interrupted tasks are scheduled (interrupted ones added to `wanted`; `Run`'s `enter`
  settles them); `paused` stay paused. Without it: `queued` tasks are `Hold`-ed to `paused`;
  interrupted ones are not scheduled and show as **Interrupted** (Paused section; actions Resume /
  Cancel / Remove). Their DB state is settled when the user resumes (via `Run`).
- **Resume after a normal quit (amendment, final review):** a quit pauses running tasks, so on
  `Queue.Shutdown` (before the engine shuts down) the queue records the IDs of tasks that are running
  or `queued` and not paused by the user, as a JSON array under the settings KV key
  `resume_on_start`. On the next start, `restore` reads that list: with `ResumeOnStartup` it
  `Requeue`s each ID (in addition to the crash-interrupted handling above) so the scheduler starts
  them; without it they stay `paused`. The key is cleared (`[]`) either way. User-paused tasks are
  never in the list.
- **Retry** = Resume on a failed task. **Restart from zero** (remote changed) = `AllowRestart` + Resume.
- **Cancel/Remove** go straight to the engine (they already stop a running Run), then wake.


## 6. App layer (`internal/app`)

```go
type Command interface{ isCommand() }
type AddDownload struct{ URL, DestDir, FileName string }   // reply via Result
type PauseTask, ResumeTask, CancelTask, RetryTask, RestartTask struct{ ID string }
type RemoveTask struct{ ID string; DeletePartial bool }
type PauseAll, ResumeAll struct{}
type SaveSettings struct{ S settings.Settings }
type Quit struct{}

type App struct { /* queue, eng, settingsRepo, bus, cmds chan Command (cap 64) */ }
func New(d Deps) (*App, error)
func (a *App) Send(c Command) bool        // non-blocking; false when full (UI shows "busy, try again")
func (a *App) Run(ctx context.Context) error  // command loop; returns after Quit + graceful shutdown
```

Each command runs in the app goroutine with a 30 s timeout; long work (Add's writability check, Remove
with deletion) is fine there. Outcome → `events.Bus` as a new event type
`events.CommandResult{Cmd string; TaskID string; Err error}` (non-progress, so never coalesced) which the
state goroutine turns into a toast. `Quit`: Queue.Shutdown (engine pauses + checkpoints, ≤10 s), close DB,
then `os.Exit(0)` from main after `App.Run` returns.

## 7. UI state (`internal/ui/state`)

```go
type Section int // Active, Queued, Paused (incl. Interrupted), Completed, Failed (incl. Cancelled)
type TaskView struct {
    ID, FileName, DestPath, URL /* redacted */ string
    State domain.State; Interrupted bool; Section Section
    Downloaded, Total int64; TotalKnown bool; Percent float64 // -1 when unknown
    SpeedBps float64; ETA *time.Duration; Conns int
    Strategy, DecisionSummary, LastError, LastErrorAction, VerifyResult string
    Retries []AttemptView; CanPause, CanResume, CanCancel, CanRetry, CanRestart, CanOpen bool
}
type Snapshot struct {
    Tasks []TaskView           // ordered: Section, then CreatedAt (Completed: newest first)
    Counts [numSections]int; TotalSpeedBps float64; ActiveCount int
    Settings settings.Settings; QueuePaused bool
    Toasts []Toast              // newest last, max 3, each expires after 5 s
    Offline bool                // any active task in "waiting for network"
    Err string                  // fatal load error (shown as error state)
}
type Store struct{ cur atomic.Pointer[Snapshot] }
func (s *Store) Load() *Snapshot
func Run(ctx, deps) // goroutine: initial load from DB → loop sub.Next → apply → publish(Snapshot) → invalidate()
```

- **Apply rules:** `StateChanged` updates state (and reloads that task row from the DB for paths,
  errors, verify result); `Progress` updates numbers; `DecisionMade` summary; `RetryScheduled`
  appends attempt; `Resync` → full reload; `CommandResult` → toast (and reload the task).
- **Publication:** at most once per 100 ms (coalesce bursts; the bus already rate-limits progress to
  `ProgressHz`), then `invalidate()` (a `func()` the window provides; `Window.Invalidate` is goroutine-safe).
- `CanX` flags derive from state via one table (`actions.go`) so buttons are never "fake": a button
  shows only when its command is valid.
- Pure functions `apply(model, ev)` and `build(model) *Snapshot` are unit-tested without Gio.

## 8. Window, screens and components (Gio)

**Window loop** (`ui.Window`): owns `*app.Window`, theme, widget state (clickables, editors, list
positions, focus). On `app.FrameEvent`: `snap := store.Load()`, layout, `e.Frame(gtx.Ops)`. Never
blocks: commands go through `App.Send`; file pickers run in a goroutine and report back through a
small `chan` read at frame time + `Invalidate`. On `app.DestroyEvent`: if tray available and
`CloseToTray` → window loop returns, app keeps running (tray "Show" creates a new `ui.Window`);
otherwise → `Send(Quit)`.

**Main screen layout** (responsive, min 640×420; ≥ 1000 dp wide shows details as right pane,
narrower shows it as a full-width sheet):
- Header: app name, aggregate "N active · 2.4 MB/s", **Add download** (primary), Pause all/Resume all, Settings.
- Offline banner when `Snapshot.Offline`.
- Scrollable list (`widget.List`, virtualized) with section headers + counts; sections with 0 tasks hidden;
  all empty → empty state ("No downloads yet" + Add button + "Ctrl+V to paste a link").
- **TaskRow:** file-type glyph + filename (ellipsized middle), status text ("Inspecting · validating strategy",
  "Downloading", "Waiting for network", "Retrying in 4 s", "Paused", "Interrupted", "Verifying", "Completed",
  "Failed: <short error>"), downloaded/total, progress bar (indeterminate when total unknown), %,
  speed, ETA (only when `ETA != nil`), action buttons per `CanX`, overflow menu (Open file, Open folder,
  Copy URL, Copy path, Remove…). Selecting a row opens details.
- **DetailsPanel:** URL (redacted), destination, size, current speed, connections, strategy + decision
  summary, range support, chunks done/remaining (from Progress/segments count), retry history (last 10),
  last error + suggested action, integrity (`VerifyResult`), resume capability, buttons.
- **AddDialog:** URL field (prefilled from clipboard when it holds an http(s) URL), destination folder
  (default from settings, "Change…" opens folder picker), optional filename, Add/Cancel; inline validation
  (scheme, host) before sending; errors from `CommandResult` shown inline.
- **ConfirmDialog:** for Cancel and Remove; Remove offers "Also delete partial data" checkbox (unfinished only).
- **SettingsScreen:** fields of §4 with validation messages; Save/Cancel; theme switch applies live.
- **Toasts:** bottom-right, success/warning/error variants, auto-dismiss 5 s, dismiss button.

**Keyboard:** Tab/Shift+Tab focus traversal with visible focus ring; Ctrl+N add; Ctrl+V (outside text
fields) opens Add prefilled; Space pause/resume selected; Delete remove selected (confirm); Enter open
details; Esc closes dialog/details; Ctrl+, settings; Ctrl+Q quit; ↑/↓ move selection.

**Theme tokens** (`ui/theme`): spacing 4/8/12/16/24/32 dp; type scale 12/14/16/20 sp; radii 6/10 dp;
palettes light+dark with neutral surfaces and one accent (blue), semantic success/warning/error;
all text/background pairs ≥ 4.5:1 contrast (tested by a contrast unit test over the token pairs);
interaction states hover/pressed/focused/disabled derived from tokens. "System" theme follows the
OS where Gio exposes it (falls back to light).

## 9. Tray (`internal/tray`)

`tray.Start(deps) (available bool)` using `systray.RunWithExternalLoop` (Gio owns the main loop).
Menu: "Show SwiftFetch", status line (disabled item, refreshed ≤ 1 Hz from snapshot),
"Pause all"/"Resume all" (one toggling item), separator, "Quit". Clicks → `App.Send`. Icon embedded
PNG/ICO. `available=false` when registration fails (e.g. no StatusNotifierWatcher on D-Bus) → close quits.
First close-to-tray notice: Gio cannot veto a close, so the notice is shown **before** the first close
can happen — when the first download starts and `TrayNoticeShown` is false, a one-time toast says
"Closing the window keeps downloads running in the tray (Settings › Close to tray)", then
`TrayNoticeShown` is saved.
macOS: run-loop integration validated by a manual CI check (documented; cannot be tested on Linux here).

## 10. Platform (`internal/platform`)

`OpenPath(p)` (xdg-open / `cmd /c start` / `open`), `RevealPath(p)` (open containing folder),
`InstanceLock(dataDir) (*storage.FileLock, error)` using `storage.TryLock(dataDir/"gui.lock")`,
`FormatBytes`, `FormatSpeed`, `FormatETA`, `MiddleEllipsis(s, n)`. Commands launched with `exec.Command`
args (never a shell string with user data); paths validated to be the task's FinalPath/dest dir.

## 11. Startup / shutdown

`main`: parse `--data-dir` (env `SWIFTFETCH_DATA_DIR`), take instance lock (fail → print + exit 1),
open DB, load settings, build bus/engine/queue/app, start state goroutine, tray, `go app.Run`,
create first window, `app.Main()`. Quit path: `Quit` command → queue+engine shutdown → DB close →
`os.Exit(0)`. SIGINT/SIGTERM → `Send(Quit)`.

## 12. Error handling

- Command failures → `CommandResult.Err` → error toast with `domain.Error` message + action.
- Task failures → row status "Failed: …" + details last error/action; Retry button when retryable.
- DB load failure at startup → error state screen with message and Quit.
- Folder picker unavailable (`explorer.ErrNotAvailable`/portal missing) → keep the text field editable; toast.
- Panic in command loop: recovered per command, logged, error toast; state goroutine panic → logged, restarted once.

## 13. Testing

- `settings`: defaults, validation, JSON round-trip, migration applies on an existing v1 DB.
- Engine additions: Hold/Requeue/SetConfig/AllowRestart/Running/ErrLockedElsewhere unit tests incl. races (`-race`).
- `services.Queue`: fake clock + faultserver; max simultaneous honoured, FIFO, PauseAll/ResumeAll,
  lowering slots, lock-elsewhere skip, restore with ResumeOnStartup on/off, shutdown leaves tasks paused.
- `app`: commands end-to-end against faultserver (add→complete, pause/resume, remove with delete), full channel.
- `ui/state`: table tests for `apply`/`build`, Resync reload, coalescing, actions table, section ordering.
- `ui/theme`: contrast test. `platform`: formatters, ellipsis.
- UI smoke: headless Gio layout test rendering MainScreen/Details/Add/Settings with fixture snapshots
  into an `op.Ops` via `layout.Context` (no window) — asserts no panic and that buttons exist only per `CanX`.
- `archtest`: import rules of §2.
- Manual checklist (README): tray on KDE/GNOME+AppIndicator, folder picker, open file, keyboard nav.

## 14. Acceptance criteria

1. `go build ./cmd/swiftfetch` on Linux; `go vet`, `go test -race -short ./...` pass.
2. Add URL → appears Queued/Active → completes; at most `MaxSimultaneous` run concurrently.
3. Pause/resume one and all; close window → downloads continue in tray; Show restores window; Quit
   pauses + checkpoints; relaunch restores (auto-resume per setting).
4. Settings persist across restarts; theme switches live; rate limit applies immediately.
5. UI never blocks: progress of 10 parallel downloads renders smoothly (frame loop does no I/O).
6. CLI and GUI coexist on one data dir without double-driving a task.
