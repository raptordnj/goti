# SwiftFetch GUI polish — design

Date: 2026-10-18. Status: approved for planning (user: "make the GUI most premium … elegant, polished,
very user friendly, smooth UX … compact and minimal"; decisions delegated: "do all autonomously").
Builds on `2026-10-18-swiftfetch-gui-design.md` (the GUI spec). Only the visual layer and interaction
feel change; app/queue/state semantics, intents and keyboard map stay as specified there.

## 1. Goals and success criteria

- **Premium & calm:** one typeface with real weights, vector icons, hairline structure instead of heavy
  boxes, one accent colour, consistent 4-dp rhythm. Nothing looks like a debug UI.
- **Compact & minimal:** a download fits on one 56-dp row; header is one 52-dp bar; secondary actions
  appear on hover/selection; no decorative chrome.
- **Smooth:** progress bars glide, dialogs/toasts fade+slide, hover/press states ease; no jank; frame loop
  stays I/O-free and animations only invalidate while running.
- **Reliable-looking:** clear state colour language, precise numbers (tabular figures), no layout jumps
  when values change, explicit empty/offline/error states, honest disabled states.
- **Verifiable:** a headless render harness produces PNG snapshots of every screen in light and dark
  so reviewers (and the user) can see the result without running the app.

Observed problems in the current build (screenshot 2026-10-18): empty state left-aligned mid-window,
text-only buttons, Go fonts, no hover feedback, no motion, flat full-width white body, header actions
of mixed weight.

## 2. Decisions

| # | Decision | Why |
|---|----------|-----|
| P1 | Refine existing `ui/theme` + `ui/components` in place (not material widgets, not a new kit). | Keeps intents/tests; smallest diff to a coherent system. |
| P2 | Embed **Inter 4.1** static TTF: Regular 400, Medium 500, SemiBold 600 (OFL-1.1, license file committed under `internal/ui/theme/fonts/`). Go fonts are dropped. Gio's shaper cannot enable OpenType `tnum`, so numeric values are laid out in fixed-width slots to avoid jitter. | Premium typography is the largest single quality win. |
| P3 | **Vector icons** drawn with `clip.Path` in a new `ui/icons` package (no icon font): plus, pause, play, x, retry, restart, folder, file, more, settings, check, alert, wifi-off, arrow-down, chevron-right; 16-dp grid, 1.5-dp stroke, round caps. | Crisp at any scale, no glyph-coverage risk. |
| P4 | **Motion** via a tiny `ui/anim` package: `Value` (eased float toward a target, critically damped, 180 ms default) and `Fade` (0→1 over 150 ms ease-out). Animations request redraw with `gtx.Execute(op.InvalidateCmd{})` only while not settled. Respects a `ReduceMotion` setting (default false) → instant. | Smoothness without a busy loop. |
| P5 | **Headless snapshot harness** (`internal/ui/snapshot`, test-only build tag `snapshot`) renders fixture snapshots with `gioui.org/gpu/headless` to PNG under `docs/screenshots/` (main empty, main busy, details, add, confirm, settings, toasts, offline — light & dark, 1100×720 and 720×560). Skips when no GPU device. | Lets review judge visuals; documents the UI in README. |

## 3. Design tokens (replace current values)

- **Spacing:** 2, 4, 8, 12, 16, 20, 24 dp (`S0..S6`). Row padding 12 h / 10 v.
- **Type (Inter):** Caption 11 sp/Medium (section labels, uppercase, +0.4 tracking emulated by spacing—
  skip tracking if unsupported), Body 13 sp/Regular, BodyStrong 13 sp/Medium, Title 15 sp/SemiBold,
  Display 18 sp/SemiBold (dialog titles). Line height 1.35.
- **Radii:** 6 (controls), 8 (rows, inputs), 12 (dialogs, panels), full (pills, progress).
- **Elevation:** two levels drawn as 2–3 stacked translucent rrects (shadow) — `Raised` (menus, toasts),
  `Overlay` (dialogs). Scrim 60 % black (dark) / 30 % (light).
- **Palette (light):** Bg #F7F8FA, Surface #FFFFFF, SurfaceSunken #F1F3F6, Hairline #E4E7EC,
  Text #111418, TextMuted #5E6673, TextFaint #8A919C (≥ 3:1, non-essential only), Accent #3461F5,
  AccentSoft = Accent @ 10 %, OnAccent #FFFFFF, Success #12805C, Warning #A15C07, Error #C8322B,
  Focus = Accent @ 55 %.
- **Palette (dark):** Bg #0E0F12, Surface #16181D, SurfaceSunken #1C1F25, Hairline #272B33,
  Text #ECEEF1, TextMuted #A0A7B3, TextFaint #6F7682, Accent #6C8CFF, OnAccent #0B1020,
  Success #3DD39A, Warning #F5B13D, Error #FF7A70, Focus = Accent @ 60 %.
- Contrast test extended: Text, TextMuted on Bg/Surface/SurfaceSunken ≥ 4.5; OnAccent/Accent ≥ 4.5;
  semantic colours on Surface ≥ 4.5; TextFaint ≥ 3.0 and never used for essential info.
- **State colour language:** Downloading = Accent, Queued/Inspecting = TextMuted, Paused/Interrupted =
  Warning, Completed = Success, Failed/Cancelled = Error. Shown as a 6-dp dot + progress colour.

## 4. Components

- **Button** kinds: Primary (accent fill), Secondary (Surface + hairline), Ghost (text only), Danger;
  sizes Md 32 dp / Sm 28 dp; optional leading icon; states hover (+4 % overlay), pressed (+8 %), focused
  (2-dp Focus ring, 2-dp offset), disabled (40 % opacity on fg+bg). State transitions eased (P4).
- **IconButton:** 28 dp square, ghost; hover bg SurfaceSunken; carries an accessible description
  (`semantic.DescriptionOp`).
- **ProgressBar:** 3 dp tall, full radius, track Hairline; fill eased with `anim.Value`; indeterminate =
  30 %-width segment sliding (1.2 s loop) — only animates while visible and ReduceMotion off.
- **TaskRow (56 dp):** [file-type tile 32 dp, rounded 8, SurfaceSunken bg, icon] · column {name
  (BodyStrong, middle-ellipsized to width), meta line (Caption-size 12 sp Muted): `● Downloading ·
  412 MB of 1.2 GB · 8.4 MB/s · 1 min left`} · progress bar spanning the text column under the meta line
  (hidden for completed) · trailing actions. Trailing area: primary state action always visible (pause /
  resume / retry / open); `more` button visible on hover, selection or keyboard focus; others in the
  overflow menu. Hover bg SurfaceSunken @ 60 %; selected bg AccentSoft + 2-dp accent bar at left edge.
  Numbers live in fixed-width slots so text does not jitter.
- **Overflow menu (new):** popover anchored to `more`, Raised elevation, items with icons: Open file,
  Show in folder, Copy link, Copy path, Cancel, Remove…; keyboard navigable; closes on Esc/click-outside.
- **SectionHeader:** Caption uppercase muted label + count pill; 28 dp tall; collapsible chevron for
  Completed and Failed (state kept in window, default expanded).
- **TextField:** 32 dp, radius 8, SurfaceSunken fill, hairline border → Accent border on focus, inline
  error text below in Error with alert icon.
- **Dialog:** 440 dp wide card, radius 12, Overlay elevation, scrim; enter = fade + 8-dp rise (150 ms);
  title Display, body Body; actions right-aligned (Secondary + Primary/Danger).
- **Toast:** bottom-centre stack, max 3, 360 dp wide, Raised, leading semantic icon, auto-dismiss 5 s
  with fade-out; dismiss icon button.
- **Banner (offline):** slim 32-dp bar under header, Warning tint, wifi-off icon, text "You're offline —
  downloads resume automatically".
- **Header (52 dp):** app mark (accent rounded square with arrow-down icon) + "Goti" Title;
  centre-left: aggregate pill "3 active · 8.4 MB/s" (hidden when idle); right: IconButton pause/resume
  all, IconButton settings, Primary Sm "Add" with plus icon. Hairline bottom border.
- **Empty state:** truly centred column: 48-dp icon tile, "No downloads yet" (Title), "Paste a link
  with Ctrl+V or click Add" (Body Muted), Primary "Add download".
- **Details panel:** 340 dp right pane (≥ 1000 dp) / bottom sheet (narrow) with Raised elevation;
  header with file tile, name (wrap 2 lines), state dot+text, close icon; key/value list in two
  columns (label Muted 12 sp, value Body, tabular slots), grouped: Transfer, Source, Integrity, History;
  actions row at bottom.
- **Settings:** grouped sections (Downloads, Network, Appearance, Behaviour) with row-style controls:
  numeric stepper (− value +) for MaxSimultaneous/MaxConnections, text fields for dir/limits with unit
  suffix, segmented control for Theme (System/Light/Dark), switches for ResumeOnStartup, CloseToTray,
  ReduceMotion (new setting, default false). New components: **Switch**, **Segmented**, **Stepper**.

`ReduceMotion` is a new `settings.Settings` field (JSON key `ReduceMotion`, default false; tolerant load
unchanged).

## 5. Interaction polish

- Hover cursors: pointer on clickables, text cursor on editors.
- Row click selects; double-click opens the file when completed, else opens details.
- After Add succeeds the dialog closes and the new row is selected + scrolled into view (state already
  publishes `AddResultSeq`; window matches by newest task).
- Destructive confirm buttons are Danger; Remove dialog shows file name and size.
- Optimistic pressed feedback: action icon swaps immediately (pause→play) while command is in flight,
  reconciled when the snapshot updates (no state lies: if the command fails, a toast explains and the
  icon reverts).
- Window: default size 960×640, min 640×420; remembers nothing else (YAGNI).

## 6. Testing

- Existing tests keep passing (intents, CanX, shortcuts); update text/glyph tests to Inter coverage and
  new strings.
- Unit: `anim` (easing converges, settles, ReduceMotion instant), `icons` (each icon lays out non-empty
  bounds), new components (Switch toggles intent, Segmented selection, Stepper clamps 1-10 / 1-16, menu
  item emits intent, Esc closes menu), contrast test with new pairs, Inter coverage for every UI string.
- Snapshot harness (P5) run by the plan's final task; PNGs committed to `docs/screenshots/` and linked
  in README. Visual review of PNGs is part of each UI task's review.
- Frame-cost check: laying out the busy fixture (50 rows) headless 100× stays under 2 ms/frame average
  on this machine (benchmark, not a hard CI gate).

## 7. Out of scope

Search/filter, drag-and-drop, multi-select (still deferred per GUI spec), custom window decorations,
per-OS native widgets, localisation.
