# SwiftFetch GUI Polish Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the SwiftFetch Gio GUI premium, compact, minimal and smooth without changing app/state semantics.

**Architecture:** Refine `internal/ui/theme` and `internal/ui/components` in place, add `internal/ui/icons` (vector icons), `internal/ui/anim` (eased values) and a headless PNG snapshot harness; then restyle rows, screens and dialogs on top.

**Tech Stack:** Go 1.27, gioui.org v0.10.3 (incl. `gpu/headless`, `widget`, `io/semantic`, `io/pointer`), Inter 4.1 TTF (OFL).

**Spec:** `docs/superpowers/specs/2026-10-18-swiftfetch-gui-polish-design.md` (polish) on top of `docs/superpowers/specs/2026-10-18-swiftfetch-gui-design.md` (GUI).

## Global Constraints

- Branch `feat/gui`; `git push` after every task.
- Intents, CanX table, keyboard map and app/state behaviour unchanged unless a task says so.
- Tokens exactly as polish spec §3; component specs exactly as §4 (sizes in dp/sp verbatim).
- Frame loop: no I/O, no blocking; animations invalidate only while unsettled; ReduceMotion → instant.
- All UI strings covered by the embedded Inter fonts (coverage test).
- Contrast: Text/TextMuted on Bg/Surface/SurfaceSunken ≥ 4.5; OnAccent/Accent ≥ 4.5; semantic on Surface ≥ 4.5; TextFaint ≥ 3.0.
- Each UI task regenerates snapshot PNGs (from Task 3 on) and the reviewer looks at them.
- `go vet ./...`, `gofmt -l .` empty, `go test -race -short ./internal/... ./cmd/...` pass.

## Review Focus

1. Long filenames, 0-byte and unknown-size tasks, and 100 % done rows must not overflow, jitter or show "NaN"/negative values (Task 5 `TestRowEdgeFixtures` renders each without panic and with stable text-column width).
2. Overflow menu near the window bottom/right edge must stay on-screen (Task 5 `TestMenuClampsToWindow`).
3. Animations must stop requesting frames once settled (Task 2 `TestValueSettlesNoInvalidate`), else idle CPU burns.
4. Dark theme hover/selection overlays must stay visible on SurfaceSunken (Task 1 contrast test includes AccentSoft vs Surface ≥ 1.2 luminance ratio).
5. Narrow window (640×420) with dialog open: dialog shrinks to window − 32 dp and scrolls (Task 7 `TestDialogNarrow`).

---

### Task 1: Tokens, Inter font, palettes, ReduceMotion setting

**Files:** Modify `internal/ui/theme/theme.go`; Create `internal/ui/theme/fonts/{Inter-Regular.ttf,Inter-Medium.ttf,Inter-SemiBold.ttf,OFL.txt}`, `internal/ui/theme/fonts.go`; Modify `internal/ui/theme/contrast_test.go`, `internal/settings/settings.go` (+ test), all existing call sites of renamed tokens; glyph coverage test switches to Inter.

**Interfaces — Produces:** spacing `S0..S6` = 2,4,8,12,16,20,24 dp; type `Caption=11, Body=13, Title=15, Display=18` (unit.Sp) plus `type TextStyle struct{Size unit.Sp; Weight font.Weight}` values `StyleCaption, StyleBody, StyleBodyStrong, StyleTitle, StyleDisplay`; radii `RadiusControl=6, RadiusRow=8, RadiusPanel=12`; `Palette` fields `Bg, Surface, SurfaceSunken, Hairline, Text, TextMuted, TextFaint, Accent, AccentSoft, OnAccent, Success, Warning, Error, Focus` with values from polish spec §3; `func StateColor(p Palette, s domain.State, interrupted bool) color.NRGBA`; `theme.New(p)` uses an Inter collection (`fonts.go`: `//go:embed`, `opentype.ParseCollection`). `settings.Settings.ReduceMotion bool` (default false).

- [ ] Step 1: Tests — extended `TestContrast` (pairs in Global Constraints + AccentSoft-over-Surface vs Surface ≥ 1.2 luminance ratio), `TestStateColor` table, `TestInterLoads` (collection has 3 faces, weights 400/500/600), coverage test over all UI strings using Inter faces, settings default/JSON round-trip for ReduceMotion.
- [ ] Step 2: run → FAIL. Step 3: download Inter 4.1 static TTFs + OFL from the rsms/inter GitHub release into the fonts dir (verify SHA-256 printed in report); implement; update renamed token call sites mechanically (old S1..S6/TextSm..TextXl/RadiusSm/Md → nearest new token). Step 4: `go test -race ./internal/ui/... ./internal/settings` PASS.
- [ ] Step 5: Commit `feat(ui/theme): Inter typography, refined palettes and tokens`, push.

### Task 2: Icons and animation primitives

**Files:** Create `internal/ui/icons/icons.go` (+ test), `internal/ui/anim/anim.go` (+ test).

**Interfaces — Produces:** `type Icon func(gtx layout.Context, size unit.Dp, c color.NRGBA) layout.Dimensions`; vars `Plus, Pause, Play, Close, Retry, Restart, Folder, File, Archive, Video, Audio, Image, Doc, Disk, More, Settings, Check, Alert, WifiOff, ArrowDown, ChevronRight, ChevronDown` (16-dp design grid, 1.5-dp stroke via `clip.Stroke`, round caps). `func ForFile(name string) Icon`. `anim.Value{}` with `Set(target float32)`, `Get(gtx layout.Context) float32` (critically damped toward target, ~180 ms to 99 %, requests `op.InvalidateCmd` via `gtx.Execute` while unsettled), `Snap(v)`; `anim.Fade` with `Show(bool)`, `Alpha(gtx) float32` (150 ms ease-out); package var `Reduce func() bool` consulted by both (true → instant).

- [ ] Step 1: Tests — `TestIconsBounds` (each icon at 16/24 dp returns dims == size and records ops), `TestForFile`, `TestValueConverges` (fake gtx.Now steps: within 1 % after 250 ms), `TestValueSettlesNoInvalidate` (after settle, no InvalidateCmd executed — use a counting `input.Router`/source or wrap Execute via a test hook), `TestReduceInstant`, `TestFade`.
- [ ] Steps 2-4: FAIL → implement → `go test -race ./internal/ui/icons ./internal/ui/anim` PASS.
- [ ] Step 5: Commit `feat(ui): vector icons and eased animation primitives`, push.

### Task 3: Headless snapshot harness

**Files:** Create `internal/ui/snapshot/fixtures.go` (fixture `*state.Snapshot`s: Empty, Busy (8 tasks across all sections incl. long name, unknown size, retrying, failed, interrupted, completed), Offline, WithToasts), `internal/ui/snapshot/render_test.go` (build tag `snapshot`), Makefile target `screenshots`.

**Interfaces — Produces:** `func Render(w, h int, th *theme.Theme, lay func(gtx layout.Context)) (*image.RGBA, error)` using `gioui.org/gpu/headless` (`headless.NewWindow`, `Frame`, `Screenshot`); `go test -tags snapshot ./internal/ui/snapshot -run TestScreenshots -update` writes PNGs to `docs/screenshots/<screen>-<theme>-<w>x<h>.png` for: main-empty, main-busy, details, add, confirm-remove, settings, toasts, offline × light/dark × {1100×720, 720×560}. Without `-update` it only renders (smoke). Skips with `t.Skip` when `headless.NewWindow` errors (no GPU).

- [ ] Step 1: write render test + fixtures (uses the current screens).
- [ ] Step 2: `go test -tags snapshot ./internal/ui/snapshot -run TestScreenshots -update` → PNGs written; open two of them (Read tool) to confirm real content. Record GPU path used (EGL/Vulkan) in report.
- [ ] Step 3: Commit `test(ui): headless snapshot harness and baseline screenshots`, push.

### Task 4: Core components restyle + new controls

**Files:** Modify `internal/ui/components/{button.go,field.go,progress.go,dialog.go,toast.go,focus.go}`; Create `internal/ui/components/{switch.go,segmented.go,stepper.go,banner.go,elevation.go,menu.go}`; tests in `components_test.go` (+ new files).

**Interfaces — Produces:** `Button(th, c, label string, kind ButtonKind, opts ...ButtonOpt)` with `WithIcon(icons.Icon)`, `Small()`; `IconButton(th, c, icon icons.Icon, desc string)` (28 dp); `ProgressBar(th, v *anim.Value, frac float32, col color.NRGBA)` (frac<0 indeterminate); `Switch(th, b *widget.Bool, label string)`; `Segmented(th, e *widget.Enum, options [][2]string /*key,label*/)`; `Stepper(th, s *StepperState, min, max int)` with `type StepperState struct{ Dec, Inc widget.Clickable; Value int }`; `Banner(th, icon icons.Icon, text string, col color.NRGBA)`; `Raised`/`Overlay` shadow helpers in elevation.go; `Menu` popover: `type MenuState struct{ Open bool; Anchor image.Point; Items []MenuItem; list widget.List; clicks []widget.Clickable }`, `type MenuItem struct{ Key, Label string; Icon icons.Icon; Danger bool }`, `func (m *MenuState) Layout(gtx, th) (picked string)` clamped to window bounds; Esc/click-outside closes. Dialog: `DialogShell(th, fade *anim.Fade, title string, body, actions layout.Widget)` with scrim, 440-dp card, rise animation. ToastStack bottom-centre with icons and fade.

- [ ] Step 1: Tests — `TestSwitchToggles`, `TestSegmentedSelects`, `TestStepperClamps` (1..10), `TestMenuPick`, `TestMenuEscCloses`, `TestMenuClampsToWindow` (anchor at bottom-right → menu fully inside), `TestButtonDisabledDims`, existing component tests updated.
- [ ] Steps 2-4: FAIL → implement → `go test -race ./internal/ui/...` PASS; regenerate screenshots (`make screenshots`) — expect changed buttons/fields.
- [ ] Step 5: Commit `feat(ui/components): premium controls, menus, elevation and motion`, push.

### Task 5: TaskRow, section headers, overflow menu wiring

**Files:** Modify `internal/ui/components/{row.go,section.go}`, `internal/ui/screens/main.go` (menu intents), tests.

**Interfaces:** Row per polish spec §4 (56 dp; tile + icon from `icons.ForFile`; name; meta line with state dot `theme.StateColor`; eased progress; primary action always visible; `more` visible on hover/selected/focused; menu items Open file, Show in folder, Copy link, Copy path, Cancel, Remove…, each emitting the existing intents and only when the corresponding CanX allows). `RowWidgets` gains `Hover gesture.Hover`, `Progress anim.Value`, `Menu components.MenuState`; keeps `Shown`/`ShownName`. `SectionHeader(th, title string, count int, collapsed *widget.Bool /*nil = not collapsible*/)`; Completed and Failed collapsible (state in `screens.Main`).

- [ ] Step 1: Tests — existing `TestTaskRowButtonsMatchCan` adapted (primary + menu items equal CanX), `TestRowEdgeFixtures` (long name, Total 0, unknown size, 100 %, negative speed guarded → no panic, name width constant across values), `TestSectionCollapse`, menu item → intent tests.
- [ ] Steps 2-4: FAIL → implement → tests PASS; `make screenshots`; inspect main-busy light+dark.
- [ ] Step 5: Commit `feat(ui): compact task rows with overflow menu and collapsible sections`, push.

### Task 6: Header, empty/offline states, main layout, details panel

**Files:** Modify `internal/ui/screens/{main.go,empty.go,details.go}`, tests.

**Interfaces:** Header 52 dp per §4 (app mark, title, aggregate pill hidden when idle, icon buttons pause/resume-all + settings, Primary Sm "Add" with plus); offline Banner text verbatim "You're offline — downloads resume automatically"; empty state centred with copy verbatim "No downloads yet" / "Paste a link with Ctrl+V or click Add"; details 340-dp pane / bottom sheet with groups Transfer, Source, Integrity, History and fixed-width value slots.

- [ ] Step 1: Tests — `TestEmptyStateCentered` (empty column centre within 2 px of window centre), `TestHeaderIdleHidesPill`, `TestOfflineBanner`, existing details tests adapted (labels/groups).
- [ ] Steps 2-4: FAIL → implement → PASS; `make screenshots`; inspect main-empty, main-busy, details, offline.
- [ ] Step 5: Commit `feat(ui): polished header, empty state and details panel`, push.

### Task 7: Dialogs and settings screen

**Files:** Modify `internal/ui/dialogs/{add.go,confirm.go}`, `internal/ui/screens/settings.go`, tests.

**Interfaces:** Add dialog: URL field with paste-detected hint, destination row (folder icon + path ellipsized + "Change…"), optional filename, Primary "Add" / Secondary "Cancel"; Confirm remove shows file name + size and a Switch "Also delete partial data" (unfinished only), Danger confirm; Settings grouped (Downloads: download dir, MaxSimultaneous stepper 1-10, ExistingPolicy segmented rename/overwrite/fail; Network: MaxConnections stepper 1-16, global & per-download limit fields with "KB/s" suffix (0 = unlimited); Appearance: Theme segmented System/Light/Dark (live preview), ReduceMotion switch; Behaviour: ResumeOnStartup, CloseToTray switches). Dialog width = min(440 dp, window − 32 dp) and body scrolls.

- [ ] Step 1: Tests — existing validation/intent tests adapted, `TestConfirmRemoveSwitch`, `TestSettingsSteppersClamp`, `TestSettingsReduceMotionSaved`, `TestDialogNarrow` (640×420: card fits, body scrollable).
- [ ] Steps 2-4: FAIL → implement → PASS; `make screenshots`; inspect add, confirm-remove, settings.
- [ ] Step 5: Commit `feat(ui): refined dialogs and grouped settings`, push.

### Task 8: Interaction polish, window defaults, docs

**Files:** Modify `internal/ui/window.go`, `internal/ui/input.go`, `internal/ui/screens/main.go`, `cmd/swiftfetch/main.go`, `README.md`; tests.

**Interfaces:** pointer cursors (`pointer.CursorPointer` on clickables, `CursorText` on editors); row double-click → `IntentOpen` when completed else `IntentSelect`+details; after successful AddDownload the newest task is selected and scrolled into view; optimistic primary-action icon (pending map keyed by task ID, cleared on next snapshot change of that task or on error toast); default window size 960×640; `anim.Reduce` wired to `App.Settings().ReduceMotion`.

- [ ] Step 1: Tests — `TestDoubleClickCompletedOpens`, `TestAddSelectsNewTask`, `TestOptimisticIconReverts` (failed command → icon back), `TestReduceMotionWired`.
- [ ] Steps 2-4: FAIL → implement → PASS; `make screenshots` final set; README "Screenshots" section linking light/dark main-busy + details; frame-cost benchmark `BenchmarkMainBusyLayout` reported (target < 2 ms/frame avg).
- [ ] Step 5: Full verification `gofmt -l .`, `go vet ./...`, `go test -race -short ./internal/... ./cmd/...`.
- [ ] Step 6: Commit `feat(ui): interaction polish, screenshots and docs`, push.
