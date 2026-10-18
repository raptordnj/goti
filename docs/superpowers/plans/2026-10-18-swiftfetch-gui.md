# SwiftFetch GUI + Thin Queue Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A Gio desktop app `swiftfetch` with a thin download queue, persisted settings and a system tray, on top of the existing engine.

**Architecture:** Commands in, snapshots out: UI/tray send typed commands over a bounded channel to an app goroutine that drives `services.Queue` + `downloader.Engine`; a state goroutine folds `events.Bus` events into an immutable `Snapshot` published via `atomic.Pointer` and triggers `Window.Invalidate`.

**Tech Stack:** Go 1.27, gioui.org v0.10.3, gioui.org/x v0.10.3 (explorer), fyne.io/systray v1.12.2, modernc.org/sqlite (existing).

**Spec:** `docs/superpowers/specs/2026-10-18-swiftfetch-gui-design.md` (read it; section refs below are to it).

## Global Constraints

- Module `github.com/raptordnj/swiftfetch`; branch `feat/gui`; push after every task (`git push`).
- Dependency rules of spec §2 (Gio only in `internal/ui` non-state packages + `cmd/swiftfetch`; systray only in `internal/tray`).
- Command channel capacity 64; snapshot publish ≤ once per 100 ms; toasts max 3, 5 s each.
- Settings defaults: MaxSimultaneous 3 (1-10), ResumeOnStartup true, MaxConnections 8 (1-16), Theme system, CloseToTray true, ExistingPolicy rename.
- Lock-elsewhere skip 30 s; scheduler tick 5 s; command timeout 30 s; shutdown ≤ 10 s (existing engine).
- Frame loop does no I/O, no blocking channel ops, no locks held across layout.
- All text/background token pairs ≥ 4.5:1 contrast.
- Every package passes `go vet` and `go test -race -short`.
- Match existing code style: file header comment, doc comments on exported identifiers, `domain.NewError` for user-facing errors.

## Review Focus

1. Lowering MaxSimultaneous while N tasks run → no running task stopped; no new start until below cap (Task 3 test `TestQueueLowerSlots`).
2. Pause on a task between "scheduler picked it" and "Run registered" → task ends paused, not running (Task 3 `TestQueuePauseRace`, run with `-race -count=20`).
3. 10k progress events in a burst → state goroutine publishes ≤ ~11 snapshots/s and never blocks the bus (Task 5 `TestStateCoalesces`).
4. Very long / RTL / emoji filenames → middle ellipsis by rune, not byte (Task 6 `TestMiddleEllipsisRunes`).
5. Stored settings JSON from a newer version with unknown fields or out-of-range values → defaults for bad fields, no startup failure (Task 2 `TestSettingsLoadTolerant`).

---

### Task 1: Engine additions (spec §3)

**Files:** Modify `internal/downloader/engine.go`, `internal/downloader/engine_run.go`; Test `internal/downloader/engine_queue_test.go`

**Interfaces — Produces:**
- `var ErrLockedElsewhere = errors.New("task is locked by another process")`; `lockTask` returns a `domain.Error` wrapping both it and `storage.ErrLocked`.
- `func (e *Engine) Hold(ctx context.Context, id string) error`
- `func (e *Engine) Requeue(ctx context.Context, id string) error`
- `func (e *Engine) SetConfig(cfg config.Config)`; private `func (e *Engine) config() config.Config` replaces every `e.d.Config` read.
- `func (e *Engine) AllowRestart(ctx context.Context, id string) error`; `taskSettings.RestartOnChange bool \`json:"restart_on_change,omitempty"\``; `apply` sets `cfg.RestartOnRemoteChange = true` when set.
- `func (e *Engine) Running(id string) bool`

- [ ] **Step 1: Write failing tests** (use existing `memstore_test.go` store + fault server helpers already used by `engine_test.go`):
  - `TestHoldQueuedIdle`: Add → Hold → GetTask.State == paused; StateChanged{queued→paused} published.
  - `TestHoldRunning`: task blocked mid-download → Hold returns `ErrAlreadyRunning`.
  - `TestHoldPausedNoop` (nil, unchanged) and `TestHoldCompletedInvalid` (`errors.Is(err, domain.ErrInvalidTransition)`).
  - `TestRequeue`: paused→queued, failed→queued, queued→nil, crash state `downloading` (set via store) → nil and unchanged, completed → ErrInvalidTransition, running → ErrAlreadyRunning.
  - `TestSetConfigAffectsNewRuns`: SetConfig with MaxConnections 1 then Run → decision JSON shows ≤1 connection; a run started before keeps its config.
  - `TestAllowRestart`: remote-changed fault (ETag changes) → Run fails `KindRemoteChanged`; AllowRestart; Requeue; Run completes.
  - `TestLockedElsewhere`: second Engine on same DataDir while first runs → `errors.Is(err, ErrLockedElsewhere)`.
  - `TestRunning`: true during Run, false after.
- [ ] **Step 2:** `go test ./internal/downloader -run 'Hold|Requeue|SetConfig|AllowRestart|LockedElsewhere|TestRunning' -race` → FAIL (undefined).
- [ ] **Step 3:** Implement. Hold/Requeue/AllowRestart take the run guard with `acquire(id, nil)` (loop on nothing — return `ErrAlreadyRunning` if held), load task, transition via `newTaskRun(e,t).setState`, release. Config stored in `e.cfg` under `e.mu`.
- [ ] **Step 4:** `go test -race ./internal/downloader` → PASS (whole package, nothing regressed).
- [ ] **Step 5:** Commit `feat(downloader): queue hooks — Hold, Requeue, SetConfig, AllowRestart, Running` and push.

### Task 2: Settings (spec §4)

**Files:** Create `internal/settings/settings.go`, `internal/settings/repo.go`, `internal/persistence/migrations/0002_settings.sql`; Modify `internal/persistence/sqlite.go` (add `GetSetting(ctx, key) (string, error)` returning `domain.ErrNotFound`, `PutSetting(ctx, key, value string) error`); Test `internal/settings/settings_test.go`, `internal/persistence/sqlite_test.go`

**Interfaces — Produces:** `settings.Settings` (fields exactly as spec §4), `type Theme string` with `ThemeSystem="system"`, `ThemeLight="light"`, `ThemeDark="dark"`; `Default()`, `Validate() error`, `Apply(config.Config) config.Config` (sets DownloadDir, MaxConnections, GlobalRateLimit, PerDownloadRateLimit, ExistingFilePolicy); `type Repo struct`; `NewRepo(kv KV, log *slog.Logger) *Repo` with `type KV interface{ GetSetting(ctx, string) (string, error); PutSetting(ctx, string, string) error }`; `(*Repo) Load(ctx) Settings`, `(*Repo) Save(ctx, Settings) error` (Validate first).

- [ ] **Step 1: Tests:** `TestDefaultValid`; `TestValidateRanges` (MaxSimultaneous 0 and 11, MaxConnections 17, negative limits, bad Theme → each FieldError named); `TestApply`; `TestRepoRoundTrip` (real `persistence.Open` on temp dir); `TestSettingsLoadTolerant` (stored `{"MaxSimultaneous":99,"Theme":"dark","Future":1}` → MaxSimultaneous 3, Theme dark); `TestLoadMissingDefaults`; persistence `TestMigration2OnV1DB` (open a DB created with only 0001 applied, reopen → settings table exists, tasks intact).
- [ ] **Step 2:** run → FAIL.
- [ ] **Step 3:** Implement; JSON keys are Go field names. Tolerant load: unmarshal over `Default()`, then for each invalid field reset to default and `log.Warn`.
- [ ] **Step 4:** `go test -race ./internal/settings ./internal/persistence` → PASS.
- [ ] **Step 5:** Commit `feat(settings): persisted app settings with migration 0002`, push.

### Task 3: Queue service (spec §5) + CommandResult event

**Files:** Modify `internal/events/events.go` (add `CommandResult{Cmd, ID string; Err error}` with `TaskID()`); Create `internal/services/queue.go`; Test `internal/services/queue_test.go`

**Interfaces — Consumes:** Task 1 engine methods. **Produces:**
```go
type QueueEngine interface { // satisfied by *downloader.Engine
    Run(ctx context.Context, id string) error
    Pause(id string) bool
    Hold(ctx context.Context, id string) error
    Requeue(ctx context.Context, id string) error
    Running(id string) bool
    Shutdown(ctx context.Context) error
}
type QueueDeps struct { Engine QueueEngine; Store interface{ ListTasks(context.Context) ([]*domain.Task, error) }; Bus *events.Bus; Clock domain.Clock; Logger *slog.Logger; MaxSimultaneous int; ResumeOnStartup bool }
func NewQueue(d QueueDeps) *Queue
func (q *Queue) Start(ctx context.Context)   // spawns scheduler; returns immediately
func (q *Queue) Enqueue(id string)
func (q *Queue) Pause(ctx context.Context, id string) error
func (q *Queue) Resume(ctx context.Context, id string) error
func (q *Queue) PauseAll(ctx context.Context) error
func (q *Queue) ResumeAll(ctx context.Context) error
func (q *Queue) Paused() bool
func (q *Queue) SetMaxSimultaneous(n int)
func (q *Queue) Shutdown(ctx context.Context) error
func Interrupted(s domain.State) bool // inspecting|downloading|retrying|verifying
```

- [ ] **Step 1: Tests** with the real engine + `tests/faultserver` (slow-bandwidth mode so downloads overlap) and `clocktest.Fake` for the tick:
  - `TestQueueMaxSimultaneous`: 5 tasks, max 2 → never more than 2 `Running` (sample every 5 ms), all complete.
  - `TestQueueFIFO`: max 1 → completion order == CreatedAt order.
  - `TestQueueLowerSlots`: 3 running, SetMaxSimultaneous(1) → all 3 keep running; no 4th starts until 2 finish.
  - `TestQueuePauseResume`; `TestQueuePauseQueuedIdle` (state paused, never started).
  - `TestQueuePauseRace` (Pause immediately after Enqueue; `-count=20`) → final state paused.
  - `TestQueuePauseAllResumeAll`: all running+queued end paused; nothing starts while paused; ResumeAll completes all.
  - `TestQueueLockedElsewhere`: fake engine returning `downloader.ErrLockedElsewhere` → task skipped for 30 s (fake clock), Warning published, retried after.
  - `TestQueueRestore`: tasks in queued, paused, downloading (crash) — ResumeOnStartup true → queued+downloading complete, paused untouched; false → queued becomes paused, downloading untouched.
  - `TestQueueShutdown`: running tasks end paused; Start-ed scheduler exits.
- [ ] **Step 2:** run → FAIL.
- [ ] **Step 3:** Implement. Single scheduler goroutine; state (`running map[string]struct{}`, `wanted map[string]bool`, `skipUntil map[string]time.Time`, `slots`, `paused`) under `q.mu`; wake channel cap 1. Pause: mark `held[id]` before calling engine so a pick in flight is dropped; if `Running` → `Pause`, else `Hold` (ErrAlreadyRunning → retry `Pause`). Each started run: `go func(){ err := eng.Run(ctx,id); q.done(id, err) }()`.
- [ ] **Step 4:** `go test -race ./internal/services ./internal/events` → PASS; `-run PauseRace -count=20` PASS.
- [ ] **Step 5:** Commit `feat(services): thin download queue`, push.

### Task 4: App layer (spec §6)

**Files:** Create `internal/app/commands.go`, `internal/app/app.go`; Test `internal/app/app_test.go`

**Interfaces — Produces:** command types exactly as spec §6 (each with unexported `isCommand()`); `RemoveTask{ID string; DeletePartial bool}`; `type Deps struct{ Engine *downloader.Engine; Queue *services.Queue; Settings *settings.Repo; Bandwidth *ratelimit.Bandwidth; Bus *events.Bus; Logger *slog.Logger; BaseConfig config.Config }`; `New(Deps) *App`; `(*App) Send(Command) bool`; `(*App) Run(ctx) error` (returns nil after Quit handled); `(*App) Settings() settings.Settings`. Every command publishes `events.CommandResult{Cmd: "<TypeName>", ID, Err}`. AddDownload → `Engine.Add` then `Queue.Enqueue`. SaveSettings → Repo.Save, `Bandwidth.SetLimit`, `Queue.SetMaxSimultaneous`, `Engine.SetConfig(s.Apply(BaseConfig))`. RetryTask = Queue.Resume; RestartTask = AllowRestart + Queue.Resume.

- [ ] **Step 1: Tests:** `TestAppAddCompletes` (faultserver; subscribe bus, expect CommandResult{Cmd:"AddDownload",Err:nil} then StateChanged to completed); `TestAppSendFull` (65th Send without Run → false); `TestAppRemoveDeletesPartial`; `TestAppSaveSettingsLive` (rate limit applied: Bandwidth limit observable; invalid settings → CommandResult.Err with FieldError); `TestAppQuit` (Run returns, running tasks paused); `TestAppCommandPanicRecovered` (inject panicking fake via unexported hook → error CommandResult, loop continues).
- [ ] **Step 2:** FAIL. **Step 3:** Implement (30 s `context.WithTimeout` per command; `defer recover`). **Step 4:** `go test -race ./internal/app` PASS.
- [ ] **Step 5:** Commit `feat(app): command loop over queue, engine and settings`, push.

### Task 5: UI state (spec §7)

**Files:** Create `internal/ui/state/model.go` (TaskView, Section, Snapshot, Toast), `internal/ui/state/actions.go` (CanX table), `internal/ui/state/apply.go` (pure `apply`/`build`), `internal/ui/state/run.go` (goroutine, Store); Tests alongside.

**Interfaces — Produces:** types from spec §7; `type Toast struct{ Kind ToastKind; Text string; At time.Time }` with `ToastInfo/ToastSuccess/ToastError`; `type Deps struct{ Bus *events.Bus; Store interface{ ListTasks(ctx) ([]*domain.Task, error); GetTask(ctx,string) (*domain.Task, error); ListAttempts(ctx,string) ([]domain.Attempt, error) }; Running func(id string) bool; QueuePaused func() bool; Settings func() settings.Settings; Clock domain.Clock; Invalidate func() }`; `func Run(ctx context.Context, d Deps, s *Store)`; `(*Store) Load() *Snapshot` (never nil: zero Snapshot before first load). Status text strings exactly as spec §8 TaskRow list, produced by `func StatusText(v TaskView, now time.Time) string`.

- [ ] **Step 1: Tests:** `TestApplyStateChanged`, `TestApplyProgress` (Percent; -1 when !TotalKnown), `TestApplyRetry` (Retries capped at 10 newest), `TestResyncReloads`, `TestSectionsOrder` (Completed newest first, others CreatedAt asc), `TestInterruptedMapping` (downloading + !Running → Interrupted, Paused section, CanResume), `TestActionsTable` (every state × CanX matches spec), `TestStatusText` (all spec strings, "Retrying in 4 s"), `TestToastsExpire` (fake clock: max 3, drop after 5 s), `TestOfflineFlag` (Reason "waiting for network"), `TestStateCoalesces` (10k Progress events in 1 s fake time → ≤ 11 Invalidate calls; Publish never blocks), `TestLoadNeverNil`.
- [ ] **Step 2:** FAIL. **Step 3:** Implement; URL via `transport.Redact` is done in the store layer already (FinalURL redacted) — use `transport.Redact(t.OriginalURL)` from `internal/transport` (state may import transport; UI component packages may not). **Step 4:** `go test -race ./internal/ui/state` PASS.
- [ ] **Step 5:** Commit `feat(ui/state): event-folded view model and snapshot store`, push.

### Task 6: Platform helpers (spec §10)

**Files:** Create `internal/platform/open.go` (+ `open_linux.go`, `open_windows.go`, `open_darwin.go`), `internal/platform/format.go`, `internal/platform/instance.go`; Test `internal/platform/format_test.go`, `instance_test.go`

**Interfaces — Produces:** `OpenPath(p string) error`, `RevealPath(p string) error` (via package var `startCmd func(name string, args ...string) error` for tests), `InstanceLock(dataDir string) (*storage.FileLock, error)` (ErrLocked → `ErrAlreadyRunning = errors.New("SwiftFetch is already running")`), `FormatBytes(int64) string` ("0 B", "1.5 KiB", "2.4 MiB"), `FormatSpeed(float64) string` ("2.4 MiB/s"), `FormatETA(time.Duration) string` ("45 s", "3 min", "1 h 05 min"), `MiddleEllipsis(s string, n int) string`.

- [ ] **Step 1: Tests:** table tests for formatters; `TestMiddleEllipsisRunes` ("日本語のとても長いファイル名.zip", emoji, n smaller than extension); `TestOpenPathArgs` (startCmd captured: linux `xdg-open <p>`, no shell); `TestInstanceLockTwice` (second → ErrAlreadyRunning).
- [ ] **Steps 2-4:** FAIL → implement → `go test -race ./internal/platform` PASS.
- [ ] **Step 5:** Commit `feat(platform): open, format and single-instance helpers`, push.

### Task 7: Theme + components (spec §8 tokens)

**Files:** `go get gioui.org@v0.10.3 gioui.org/x@v0.10.3`; Create `internal/ui/theme/theme.go` (tokens, `Light()`, `Dark()`, `type Theme struct{ Palette; Shaper *text.Shaper; ... }`), `internal/ui/components/{button.go,progress.go,row.go,section.go,field.go,dialog.go,toast.go,focus.go}`; Tests `internal/ui/theme/contrast_test.go`, `internal/ui/components/components_test.go`

**Interfaces — Produces:** `theme.Palette{Bg, Surface, SurfaceAlt, Text, TextMuted, Accent, OnAccent, Border, Success, Warning, Error, Focus color.NRGBA}`; spacing consts `S1..S6 = 4,8,12,16,24,32` (unit.Dp); type sizes `TextSm=12, TextMd=14, TextLg=16, TextXl=20` (unit.Sp); radii `RadiusSm=6, RadiusMd=10`. Components are functions `func Button(th *theme.Theme, c *widget.Clickable, label string, kind ButtonKind) layout.Widget` (kinds Primary/Secondary/Danger/Ghost), `IconButton`, `ProgressBar(th, frac float32 /* <0 = indeterminate */)`, `TaskRow(th *theme.Theme, v state.TaskView, w *RowWidgets, now time.Time) layout.Widget`, `SectionHeader(th, title string, count int)`, `TextField(th, e *widget.Editor, label, hint, errText string)`, `DialogShell(th, title string, body, actions layout.Widget)`, `ToastStack(th, []state.Toast, dismiss []widget.Clickable)`. Focus ring drawn when `gtx.Focused(tag)`.

- [ ] **Step 1: Tests:** `TestContrast` (WCAG relative luminance; pairs Text/Bg, Text/Surface, TextMuted/Surface, OnAccent/Accent, Error/Surface in both palettes ≥ 4.5); `TestComponentsLayout` (headless: `gtx := layout.Context{Ops: new(op.Ops), Constraints: layout.Exact(image.Pt(800,600)), Metric: unit.Metric{PxPerDp:1,PxPerSp:1}}`; lay out each component incl. TaskRow for every state fixture → no panic, non-zero dims); `TestTaskRowButtonsMatchCan` (RowWidgets records which buttons were laid out; equals CanX flags).
- [ ] **Steps 2-4:** FAIL → implement → `go test -race ./internal/ui/...` PASS.
- [ ] **Step 5:** Commit `feat(ui): theme tokens and core components`, push.

### Task 8: Screens and dialogs (spec §8)

**Files:** Create `internal/ui/screens/{main.go,details.go,settings.go,empty.go}`, `internal/ui/dialogs/{add.go,confirm.go}`; Test `internal/ui/screens/screens_test.go`, `internal/ui/dialogs/dialogs_test.go`

**Interfaces — Produces:** `type Intent interface{}` values emitted by screens instead of calling the app directly: `IntentCommand{Cmd app.Command}`, `IntentOpenAdd{Prefill string}`, `IntentOpenSettings{}`, `IntentSelect{ID string}`, `IntentConfirm{Kind ConfirmKind; ID string}`, `IntentPickFolder{}`, `IntentOpen{Path string; Reveal bool}`, `IntentCopy{Text string}` (package `internal/ui/screens`, file `intent.go`). `func (m *Main) Layout(gtx, th, snap *state.Snapshot, now time.Time) []Intent`; same for `Details`, `Settings`, `dialogs.Add`, `dialogs.Confirm`. Add validates URL (absolute http/https with host) and shows "Use an absolute http or https URL" inline. Responsive breakpoint 1000 dp (details pane vs sheet). Note: screens import `internal/app` only for command *types* — allowed (app imports no Gio).

- [ ] **Step 1: Tests (headless, fixture snapshots):** `TestMainEmptyState` (text "No downloads yet" present via a recording shaper or widget hook); `TestMainSectionsHidden` (zero-count sections not laid out); `TestMainClickPause` (simulate click on row's pause clickable via `widget.Clickable` press events using `gtx.Source` from `input.Router` → Intent `IntentCommand{app.PauseTask{ID}}`); `TestAddValidation` (bad URL → no intent, error shown; good → `IntentCommand{app.AddDownload{...}}`); `TestConfirmRemoveDeletePartial` (checkbox only when unfinished); `TestSettingsValidation` (MaxSimultaneous 0 → field error, no SaveSettings); `TestDetailsWideVsNarrow`.
- [ ] **Steps 2-4:** FAIL → implement → `go test -race ./internal/ui/...` PASS.
- [ ] **Step 5:** Commit `feat(ui): main, details, settings screens and dialogs`, push.

### Task 9: Window loop + `cmd/swiftfetch` (spec §8 window, §11)

**Files:** Create `internal/ui/window.go`, `internal/ui/keys.go`, `cmd/swiftfetch/main.go`; Test `internal/ui/keys_test.go`, `cmd/swiftfetch/main_test.go`

**Interfaces — Consumes:** everything above. **Produces:** `type WindowDeps struct{ App *app.App; Store *state.Store; Explorer factory; TrayAvailable func() bool; OnClosed func() }`; `func RunWindow(d WindowDeps) error` (creates `*app.Window` titled "SwiftFetch", min size 640×420, loops `w.Event()`; handles intents: commands → `App.Send` (false → toast "Busy, try again"), pickers in goroutine → result chan + Invalidate, clipboard via `clipboard.ReadCmd`/`WriteCmd`, open via platform); `func Shortcut(ev key.Event, ctx KeyContext) Intent` (pure; spec §8 keyboard table). `main`: flags `--data-dir`, env `SWIFTFETCH_DATA_DIR`, InstanceLock (fail → stderr "SwiftFetch is already running" exit 1), open DB, settings, bus (ProgressHz from config), engine (SetConfig from settings), queue, app, `state.Run`, tray (Task 10; stub `func() bool { return false }` until then), `go app.Run`, `go RunWindow`, SIGINT/SIGTERM → Send(Quit), after App.Run returns → db.Close, `os.Exit(0)`; `app.Main()` on main goroutine. Window close: `CloseToTray && TrayAvailable()` → just return; else Send(Quit).

- [ ] **Step 1: Tests:** `TestShortcuts` (table: Ctrl+N→IntentOpenAdd, Ctrl+V outside editor→IntentOpenAdd{prefill from clipboard flag}, Space→pause/resume selected, Delete→IntentConfirm remove, Esc, Ctrl+, , Ctrl+Q→Quit, ↑/↓); `TestMainWiringHeadless` (build everything except window/tray via exported `func build(dataDir string) (*runtime, error)` in main; add a faultserver URL through App; completes; Quit; second `build` on same dir while first holds lock → ErrAlreadyRunning).
- [ ] **Step 2:** FAIL. **Step 3:** Implement. **Step 4:** `go test -race ./internal/ui/... ./cmd/swiftfetch` PASS; `go build -o /tmp/claude-1000/swiftfetch ./cmd/swiftfetch` succeeds.
- [ ] **Step 5: Manual smoke** (Wayland session available): launch with temp `--data-dir`, add `https://speed.hetzner.de/100MB.bin`-like URL or a local faultserver URL, confirm progress, pause/resume, settings save, theme switch. Note results in commit message.
- [ ] **Step 6:** Commit `feat: swiftfetch GUI window and entrypoint`, push.

### Task 10: System tray (spec §9)

**Files:** `go get fyne.io/systray@v1.12.2`; Create `internal/tray/tray.go`, `internal/tray/icon.go` (embedded `icon.png`/`icon.ico` generated once, 32×32, accent colour arrow glyph — commit the files under `internal/tray/assets/`), `internal/tray/status.go`; Test `internal/tray/status_test.go`; Modify `cmd/swiftfetch/main.go`, `internal/ui/state/run.go` (one-time tray notice toast when first task becomes active and `!TrayNoticeShown`, then `App.Send(SaveSettings{...TrayNoticeShown:true})` via Deps callback `MarkTrayNotice func()`).

**Interfaces — Produces:** `type Deps struct{ Send func(app.Command) bool; Snapshot func() *state.Snapshot; Show func() }`; `func Start(d Deps) (available bool, stop func())` — uses `systray.RunWithExternalLoop`; availability detected by `onReady` firing within 2 s (Linux: also requires `org.kde.StatusNotifierWatcher` on session bus — check with godbus `NameHasOwner`; godbus is already an indirect dep via explorer). `func StatusLine(s *state.Snapshot) string` ("Idle", "3 active · 2.4 MiB/s", "Paused"). Menu updates ≤ 1 Hz from a ticker goroutine.

- [ ] **Step 1: Tests:** `TestStatusLine` table; `TestToggleLabel` ("Pause all" vs "Resume all" from QueuePaused); state `TestTrayNoticeOnce`.
- [ ] **Steps 2-4:** FAIL → implement → `go test -race ./internal/tray ./internal/ui/state` PASS; build passes.
- [ ] **Step 5: Manual smoke:** close window → tray icon remains, downloads continue; Show reopens; Quit exits and tasks are paused in `swiftfetch-cli list`. If no tray host, close quits.
- [ ] **Step 6:** Commit `feat(tray): system tray with close-to-tray`, push.

### Task 11: Architecture test, docs, final verification

**Files:** Create `internal/archtest/imports_test.go`; Modify `README.md` (GUI section: build deps per OS, run, shortcuts, tray notes incl. GNOME AppIndicator, manual checklist from spec §13), `Makefile` (`gui` target `go build ./cmd/swiftfetch`; `test` target gets `-timeout 30m` since the integration suite exceeds 10 min).

- [ ] **Step 1:** `TestImportRules` using `golang.org/x/tools/go/packages` *or* `go list -deps -json` via `exec` (prefer `go list`, no new dep): asserts every rule in spec §2.
- [ ] **Step 2:** `go test ./internal/archtest` PASS (fix violations in the offending package if any).
- [ ] **Step 3:** Full verification: `gofmt -l .` empty; `go vet ./...`; `go test -race -short ./...`; `go test -timeout 30m ./...` — all PASS.
- [ ] **Step 4:** Commit `test: architecture import rules; docs: GUI usage`, push.
