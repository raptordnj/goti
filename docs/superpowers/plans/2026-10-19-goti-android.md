# Goti for Android Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship Goti as a signed Android APK, built and attached to the GitHub release on tag push, with downloads running in a foreground service with a sticky notification.

**Architecture:** `gogio -buildmode archive` turns `cmd/goti` (engine + Gio UI) into `goti.aar`; a Gradle/Kotlin host app in `android/` hosts `GioView`, the `dataSync` foreground service, share intents and MediaStore publishing. Go↔Kotlin talk through one Kotlin `Bridge` object (static methods called from Go via `git.wow.st/gmp/jni`; `external` natives exported from Go with cgo). All service/notification decisions are pure Go in `internal/mobile` and unit-tested on desktop.

**Tech Stack:** Go 1.27, Gio v0.10.3, gogio (pinned to the version installed in Task 1), `git.wow.st/gmp/jni v0.0.0-20210610011705-34026c7e22d0` (already in go.sum), Gradle 9.3.1 wrapper, AGP 9.1.0 (built-in Kotlin), compileSdk/targetSdk 35, minSdk 26, NDK 28.2.13676358 locally / r27+ in CI.

**Spec:** `docs/superpowers/specs/2026-10-19-goti-android-design.md`

## Global Constraints

- Application id `io.github.raptordnj.goti`; Kotlin package `io.github.raptordnj.goti`; Gradle namespace `io.github.raptordnj.goti`; the AAR is built with `-appid io.github.raptordnj.goti.gio` so its manifest namespace does not collide with the app's.
- `GOOS=android` also satisfies the `linux` build constraint: every `linux`-only desktop file (tray, xdg-open) must carry `!android`.
- Desktop builds and tests must stay green and unchanged in behaviour (`make test`, `make lint` if present).
- Notification channel id `downloads`, name "Downloads", `IMPORTANCE_LOW`. Copy (exact): title `Goti · N downloading`; text `<speed> · <eta> left` (eta omitted → `<speed>`); paused text `Paused · N downloads`; completion `Downloads complete · N files`; timeout `Downloads paused by Android — tap to resume`. Speed formatted with `platform.FormatSpeed`, eta with the same formatter the row meta line uses.
- Notification updates ≤ 1 per second; service stops after 10 min with every remaining task paused.
- Never publish an unsigned APK; never commit a keystore. Secrets: `ANDROID_KEYSTORE_B64`, `ANDROID_KEYSTORE_PASSWORD`, `ANDROID_KEY_ALIAS`, `ANDROID_KEY_PASSWORD`.
- Release asset name `Goti_<ver>_android_universal.apk` (ver without `v`); `versionCode = major*10000 + minor*100 + patch` (pre-release suffix ignored).
- Phone layout applies when window width < 600 dp, on every OS.

## Review Focus

1. **Activity destroyed (swipe away / rotation / back) while downloading** → downloads keep running; reopening shows a working window. Desktop code quits on `DestroyEvent` when no tray — on Android the window must be recreated, never `Quit`. Test: Task 1 `TestKeepAliveOnAndroidPolicy`; Task 8 emulator smoke backs out and reopens.
2. **Process killed and service restarted (`START_STICKY`, no activity)** → `Gio.init` runs Go `main` without a window; resume-on-startup restarts active tasks and the notification reappears. Test: Task 8 smoke `am kill` + service restart check.
3. **Completed file published twice or lost** (restart between copy and delete, publish error) → publish is idempotent per task, private copy deleted only after a successful publish whose size matches. Test: Task 3 `TestPublisherRetriesAndIsIdempotent`.
4. **Notification permission denied on Android 13+** → downloads still run; service still starts (notification is just hidden). Test: Task 5 manual-check line + Kotlin `startForeground` guarded; Task 8 smoke runs with permission revoked.
5. **Shared text that is not a bare URL** ("Check this https://x/y.zip !") → first http(s) URL extracted; no URL → app opens normally, no Add sheet. Test: Task 5 `ShareIntentsTest`.

---

### Task 1: Android build split, data dir and window keep-alive

**Files:**
- Rename: `internal/tray/tray_darwin.go` → `internal/tray/tray_none.go` with `//go:build darwin || android` (update its comment: Android has no tray; the notification replaces it)
- Modify: `internal/tray/menu.go` (`//go:build !darwin && !android`), `internal/tray/tray_linux.go` (`//go:build (linux && !android) || freebsd || openbsd || netbsd`), `internal/platform/open_linux.go` (`//go:build linux && !android`)
- Create: `internal/platform/open_android.go`, `internal/platform/mobile.go`, `internal/platform/mobile_test.go`
- Modify: `cmd/goti/main.go`, `cmd/goti/main_test.go`
- Modify: `Makefile` (target `android-vet`), `internal/archtest` only if it lists allowed per-OS files

**Interfaces:**
- Produces:
  - `platform.Mobile bool` — const, true only under `android` (`mobile_android.go` / `mobile_other.go`, or a single file with build-tagged consts).
  - `var platform.AndroidOpen, platform.AndroidShare func(path string) error` (android only; nil → `errors.ErrUnsupported`). `OpenPath` calls `AndroidOpen`; `RevealPath` calls `AndroidShare` (Share replaces Show in folder).
  - `func keepAlive(mobile, trayAvailable bool) bool` in `cmd/goti` — true when closing the window must not quit (mobile, or close-to-tray with a tray).
  - `func androidDataDir(gioDataDir string) string` in `cmd/goti` → `filepath.Join(gioDataDir, "goti")`.
  - `make android-vet`: `GOOS=android GOARCH=arm64 CGO_ENABLED=1 CC=<NDK>/toolchains/llvm/prebuilt/linux-x86_64/bin/aarch64-linux-android26-clang go vet ./...` with `ANDROID_NDK_ROOT` defaulting to the newest dir under `$ANDROID_HOME/ndk`.

- [ ] **Step 1: Write failing tests** in `cmd/goti/main_test.go`:
  - `TestKeepAliveOnAndroidPolicy`: `keepAlive(true,false)==true`, `keepAlive(false,true)==true`, `keepAlive(false,false)==false`.
  - `TestAndroidDataDir`: `androidDataDir("/data/user/0/io.github.raptordnj.goti/files") == "/data/user/0/io.github.raptordnj.goti/files/goti"`.
  - `internal/platform/mobile_test.go` `TestMobileFalseOnDesktop`: `if Mobile { t.Fatal() }`.
- [ ] **Step 2:** `go test ./cmd/goti ./internal/platform` → FAIL (undefined).
- [ ] **Step 3: Implement.** In `main.go`: data dir on Android = `androidDataDir(gioapp.DataDir())` (skip `ResolveDataDir`/migration); pass `TrayAvailable: func() bool { return keepAlive(platform.Mobile, r.trayOK.Load()) }` to `ui.RunWindow`; in `OnClosed`, when `platform.Mobile`, call `r.showWindow()` after clearing `r.win` so the next activity finds a pending window. Skip `tray.Start` and signal forwarding when `platform.Mobile`.
- [ ] **Step 4:** `go test ./cmd/goti ./internal/platform ./internal/tray` PASS; `make android-vet` exits 0; `go build ./...` and `GOOS=windows go build ./...` and `GOOS=darwin go vet ./internal/tray` still pass.
- [ ] **Step 5: Commit** `feat(android): build split, data dir and window keep-alive`.

### Task 2: `internal/mobile` — notification model and service controller (pure Go)

**Files:**
- Create: `internal/mobile/notify.go`, `internal/mobile/controller.go`, `internal/mobile/host.go`, tests `notify_test.go`, `controller_test.go`

**Interfaces:**
- Consumes: `state.Snapshot` (`ActiveCount`, `Counts[state.SectionQueued]`, `Counts[state.SectionPaused]`, `Counts[state.SectionCompleted]`, `TotalSpeedBps`, `QueuePaused`, `Tasks[i].Downloaded/Total/TotalKnown/Section`), `platform.FormatSpeed`.
- Produces:
  ```go
  type Notification struct {
      Title, Text string
      Percent     int  // 0..100, -1 = indeterminate
      Paused      bool // drives Pause all ↔ Resume all action
  }
  func NotificationFor(s *state.Snapshot) Notification
  type Host interface {
      StartService()
      UpdateNotification(n Notification)
      StopService(completed int) // completed>0 → "Downloads complete · N files"
      PublishToDownloads(path, name, mime string) (uri string, err error)
      RequestNotificationPermission()
  }
  type Controller struct{ /* host, clock state */ }
  func NewController(h Host) *Controller
  func (c *Controller) Tick(s *state.Snapshot, now time.Time)
  func Run(ctx context.Context, load func() *state.Snapshot, h Host, every time.Duration) // ticks Controller (every=500ms in prod)
  ```
- Rules: `pending = ActiveCount + Counts[Queued]`; `paused = Counts[Paused]`. Not running + pending>0 → `StartService` then update. Running: pending>0 → update (throttled to 1/s unless Paused flag or title changed); pending==0 && paused>0 → update with paused notification, start 10-min timer, then `StopService(completedSinceStart)`; pending==0 && paused==0 → `StopService(completedSinceStart)`. `completedSinceStart` = `Counts[Completed]` now minus value when service started (floor 0).
- Notification: running → Title `Goti · N downloading` (N = ActiveCount; when ActiveCount==0 but queued>0 use `Goti · N queued`), Text speed + ` · ` + eta + ` left` where eta = Σ(remaining)/TotalSpeedBps over active tasks only when all active totals known and speed>0; Percent = Σdownloaded·100/Σtotal over active tasks when all known, else -1. Paused → Title `Goti`, Text `Paused · N downloads`, Percent -1, Paused true.

- [ ] **Step 1: Failing tests** (fake host recording calls, injected `now`):
  - `TestNotificationRunning`: 2 active, totals 100 MB each, downloaded 50 MB each, speed 1 MiB/s → Title `Goti · 2 downloading`, Percent 50, Text starts with `platform.FormatSpeed(1<<20)+" · "` and ends `" left"`.
  - `TestNotificationUnknownTotal`: one active with `TotalKnown=false` → Percent -1, Text == `platform.FormatSpeed(speed)`.
  - `TestNotificationPaused`: 0 active, 3 paused → `{Title:"Goti", Text:"Paused · 3 downloads", Percent:-1, Paused:true}`.
  - `TestControllerStartsOnPending`, `TestControllerThrottlesUpdates` (10 ticks within 1 s → 1 update after the first), `TestControllerStopsWhenIdle` (StopService(2) after 2 completions), `TestControllerPausedTimeout` (paused at t0: no stop at t0+9m59s, stop at t0+10m), `TestControllerRestartsAfterStop`.
- [ ] **Step 2:** `go test ./internal/mobile` FAIL.
- [ ] **Step 3:** Implement. Package must not import gioui.org or jni (archtest-style test `TestMobileImportsPure` may assert this).
- [ ] **Step 4:** `go test -race ./internal/mobile` PASS.
- [ ] **Step 5: Commit** `feat(mobile): notification model and service controller`.

### Task 3: Publish completed downloads to shared Downloads

**Files:**
- Create: `internal/mobile/publish.go`, `internal/mobile/publish_test.go`

**Interfaces:**
- Consumes: `Host.PublishToDownloads`, a KV with `GetSetting/PutSetting(ctx, key, value string)` (same shape as `services.QueueDeps.KV`; `*persistence.DB` satisfies it).
- Produces:
  ```go
  type Publisher struct{ /* host, kv, log */ }
  func NewPublisher(h Host, kv KV, log *slog.Logger) *Publisher
  func (p *Publisher) Tick(ctx context.Context, s *state.Snapshot) // called from Run after Controller.Tick
  func URIFor(ctx context.Context, kv KV, destPath string) string    // "" when unpublished
  ```
- KV keys: `android.published:<taskID>` = uri; `android.uri:<destPath>` = uri. A task is published once when its Section is Completed and no `android.published:` key exists. MIME = `mime.TypeByExtension(ext)` else `application/octet-stream`. After `PublishToDownloads` returns a non-empty uri and nil error, write both keys, then `os.Remove(destPath)` (log, don't fail, if removal errors). On error: log, retry on a later Tick no more than once per 30 s per task.

- [ ] **Step 1: Failing tests:** `TestPublisherPublishesOnceAndRemovesPrivateCopy` (temp file, fake host returns `content://media/external/downloads/7`; after two ticks host called once, file gone, `URIFor(dest)` returns the uri); `TestPublisherRetriesAndIsIdempotent` (first call errors → file kept, no keys; tick at +10 s → no call; tick at +31 s → call succeeds; new Publisher over same KV → no further call); `TestPublisherMime` (`a.zip` → `application/zip`, `a.unknownext` → `application/octet-stream`).
- [ ] **Step 2:** FAIL. **Step 3:** implement (inject clock via field for the 30 s rule). **Step 4:** `go test -race ./internal/mobile` PASS.
- [ ] **Step 5: Commit** `feat(mobile): publish completed files to Downloads`.

### Task 4: Go side of the JNI bridge and wiring into `cmd/goti`

**Files:**
- Create: `internal/mobile/host_android.go` (cgo + `git.wow.st/gmp/jni`), `internal/mobile/exports_android.go` (`//export` natives), `internal/mobile/wire.go` (build-tag-free wiring helpers), `internal/mobile/wire_test.go`
- Modify: `cmd/goti/main.go` (start `mobile.Run` + Publisher when `platform.Mobile`), `internal/ui/window.go` (`WindowDeps.TakeSharedURL func() string`), `go.mod` (promote `git.wow.st/gmp/jni` to a direct requirement)

**Interfaces:**
- Consumes: `gioapp.JavaVM()`, `gioapp.AppContext()`; `app.Command`s `PauseAll{}`, `ResumeAll{}`; the Kotlin `Bridge` (Task 5).
- Kotlin static methods called from Go (class `io/github/raptordnj/goti/Bridge`, all `@JvmStatic`; load the class through the app context's ClassLoader with `jni.ClassLoaderFor` + `jni.LoadClass` — `FindClass` fails on Go threads):
  - `startService(Landroid/content/Context;)V`
  - `updateNotification(Landroid/content/Context;Ljava/lang/String;Ljava/lang/String;IZ)V`
  - `stopService(Landroid/content/Context;I)V`
  - `publishToDownloads(Landroid/content/Context;Ljava/lang/String;Ljava/lang/String;Ljava/lang/String;)Ljava/lang/String;` (returns null/"" on failure)
  - `openUri(Landroid/content/Context;Ljava/lang/String;Ljava/lang/String;)Z`, `shareUri(Landroid/content/Context;Ljava/lang/String;Ljava/lang/String;)Z`
  - `requestNotificationPermission(Landroid/content/Context;)V`
- Go natives called from Kotlin (`external fun` in `Bridge`): `Java_io_github_raptordnj_goti_Bridge_nativeAction(env, cls, jstring)` with `"pause_all"|"resume_all"`; `..._nativeSharedUrl(env, cls, jstring)`; `..._nativeServiceTimeout(env, cls)` (→ `PauseAll{}` plus a toast-free log; the Kotlin side posts the timeout notification).
- Produces (testable, no build tag): `type Inbox struct` with `func (i *Inbox) Action(name string, send func(app.Command) bool)` mapping names to commands (unknown → ignored, logged), `func (i *Inbox) SetSharedURL(u string)`, `func (i *Inbox) TakeSharedURL() string` (returns once, then ""); package-level `var DefaultInbox` used by the exports.
- `platform.AndroidOpen = func(p) { uri := URIFor(kv, p); if uri=="" → open file:// via FileProvider is out of scope → return errors.ErrUnsupported; else Bridge.openUri(uri, mime) }`; `AndroidShare` same with `shareUri`.
- Add sheet trigger: window checks `TakeSharedURL()` each frame (and the bridge calls the window's Invalidate after `SetSharedURL`); non-empty → open Add dialog prefilled. First Add submit on Android calls `Host.RequestNotificationPermission()` once per process.

- [ ] **Step 1: Failing tests** `wire_test.go`: `TestInboxActions` (`pause_all`→`PauseAll{}`, `resume_all`→`ResumeAll{}`, `bogus`→nothing sent); `TestInboxSharedURLTakenOnce`.
- [ ] **Step 2:** FAIL. **Step 3:** implement; android files compile only under `android`.
- [ ] **Step 4:** `go test ./internal/mobile ./internal/ui/... ./cmd/goti` PASS; `make android-vet` PASS; `gogio -target android -buildmode archive -arch arm64 -appid io.github.raptordnj.goti.gio -minsdk 26 -o /tmp/…/goti.aar ./cmd/goti` succeeds.
- [ ] **Step 5: Commit** `feat(android): JNI bridge and mobile wiring`.

### Task 5: Kotlin host app (`android/`)

**Files:**
- Create: `android/settings.gradle.kts`, `android/build.gradle.kts`, `android/gradle.properties`, `android/gradle/wrapper/*` + `android/gradlew` (Gradle 9.3.1), `android/app/build.gradle.kts`, `android/app/src/main/AndroidManifest.xml`, `android/app/src/main/java/io/github/raptordnj/goti/{GotiActivity,DownloadService,Bridge,Notifications,ShareIntents,MediaStorePublisher}.kt`, `android/app/src/main/res/mipmap-*/ic_launcher.png` (generated from `packaging/icons` sources at 48/72/96/144/192), `android/app/src/main/res/values/strings.xml`, `android/app/src/debug/AndroidManifest.xml` (smoke-test alias, Task 8), `android/app/src/test/java/io/github/raptordnj/goti/{ShareIntentsTest,NotificationsTest}.kt`, `android/.gitignore` (`build/`, `.gradle/`, `local.properties`, `app/libs/*.aar`, `*.jks`)

**Interfaces:**
- Consumes: Go natives / static methods exactly as listed in Task 4; `org.gioui.GioView` and `org.gioui.Gio.init(Context)` from the AAR at `android/app/libs/goti.aar`.
- `app/build.gradle.kts`: namespace/applicationId `io.github.raptordnj.goti`, compileSdk 35, minSdk 26, targetSdk 35; `versionName = project.findProperty("versionName") ?: "0.0.0-dev"`, `versionCode = project.findProperty("versionCode")?.toInt() ?: 1`; release `signingConfig` from env `ANDROID_KEYSTORE_FILE`, `ANDROID_KEYSTORE_PASSWORD`, `ANDROID_KEY_ALIAS`, `ANDROID_KEY_PASSWORD` — when any is missing, `assembleRelease` fails with `Release signing secrets missing` (never falls back to debug signing). `testImplementation("junit:junit:4.13.2")`; JVM tests must not need Android framework classes (put pure logic in plain functions).
- Manifest: permissions `INTERNET`, `ACCESS_NETWORK_STATE`, `FOREGROUND_SERVICE`, `FOREGROUND_SERVICE_DATA_SYNC`, `POST_NOTIFICATIONS`, `WAKE_LOCK`, `WRITE_EXTERNAL_STORAGE` with `android:maxSdkVersion="28"` (ruling: MediaStore.Downloads needs API 29; API 26–28 write to the public Download dir). Activity `GotiActivity` (launcher, `launchMode="singleTask"`, `configChanges` same as Gio's default host: `orientation|screenSize|screenLayout|smallestScreenSize|keyboardHidden|keyboard|density|uiMode`, `windowSoftInputMode="adjustResize"`, intent-filter `SEND` `text/plain`). Service `DownloadService` `foregroundServiceType="dataSync"`, `exported="false"`.
- `GotiActivity`: mirrors `org.gioui.GioActivity` lifecycle (create GioView, start/stop/pause/resume/destroy, `configurationChanged`, `onBackPressed`→`view.backPressed()`, `onLowMemory`); on create/new intent with `ACTION_SEND` → `ShareIntents.extractUrl(text)` → `Bridge.nativeSharedUrl` if non-null.
- `ShareIntents.extractUrl(text: String?): String?` — first `https?://\S+` match with trailing `.,;:!?)]}'"` trimmed; null if none.
- `DownloadService`: `onCreate` calls `Gio.init(applicationContext)` (starts Go when restarted without activity); creates channel `downloads`; `onStartCommand` → `startForeground(ID=1, notification, FOREGROUND_SERVICE_TYPE_DATA_SYNC)` (catch `ForegroundServiceStartNotAllowedException` → log + `stopSelf()`), returns `START_STICKY`; partial wake lock `goti:download` held while running (released in `onDestroy`; timeout 6 h); `onTimeout(startId, fgsType)` (API 35) → `Bridge.nativeServiceTimeout()`, post timeout notification (id 3), `stopSelf()`.
- `Notifications`: pure helper `actionFor(paused: Boolean): String` → `"resume_all"` / `"pause_all"`, `labelFor(paused)` → `"Resume all"` / `"Pause all"`; builders for ongoing (id 1, `setOngoing(true)`, `setOnlyAlertOnce(true)`, small icon monochrome vector `ic_stat_goti`, progress or indeterminate when percent −1, actions toggle + `Open`, content intent opens `GotiActivity`), completion (id 2, `Downloads complete · N files`, autoCancel) and timeout (id 3). Action broadcasts go to a non-exported receiver inside `DownloadService` (or a `PendingIntent.getService` with action extra) that calls `Bridge.nativeAction`.
- `MediaStorePublisher.publish(ctx, path, name, mime): String?` — API ≥ 29: insert into `MediaStore.Downloads.EXTERNAL_CONTENT_URI` with `RELATIVE_PATH="Download/Goti"`, `IS_PENDING=1`, stream copy, verify copied byte count equals file length, `IS_PENDING=0`, return uri string; on failure delete the row and return null. API 26–28: copy to `Environment.getExternalStoragePublicDirectory(DIRECTORY_DOWNLOADS)/Goti/<name>` (unique name on clash), `MediaScannerConnection.scanFile`, return `Uri.fromFile(...)` string — `openUri` then uses a `FileProvider`-free `ACTION_VIEW` only for content:// (ruling: on 26–28 open/share returns false and the UI shows the existing "could not open" toast).
- Connectivity callback (spec §2): ruling — not added; the engine's existing network-error classification already drives the offline state and retries on Android, so a callback would duplicate it. Recorded deviation.
- `Bridge.requestNotificationPermission(ctx)`: API ≥ 33 and not granted → asks the current `GotiActivity` (weak ref) to `requestPermissions`; no-op otherwise.

- [ ] **Step 1: Failing JVM tests:** `ShareIntentsTest` — `"https://x.y/a.zip"`→same; `"Check this https://x.y/a.zip !"`→`"https://x.y/a.zip"`; `"see (http://h/p)."`→`"http://h/p"`; `"no link here"`→null; null→null. `NotificationsTest` — `actionFor(false)=="pause_all"`, `actionFor(true)=="resume_all"`, labels as above.
- [ ] **Step 2:** `cd android && ./gradlew testDebugUnitTest` FAIL.
- [ ] **Step 3:** Implement all files above.
- [ ] **Step 4:** `./gradlew testDebugUnitTest assembleDebug` PASS with a fresh AAR from Task 4 copied to `app/libs/goti.aar`; `assembleRelease` without env vars fails with `Release signing secrets missing`.
- [ ] **Step 5: Commit** `feat(android): Kotlin host app, foreground service and notifications`.

### Task 6: Phone layout (< 600 dp) and Android-only settings

**Files:**
- Modify: `internal/ui/screens/main.go`, `internal/ui/components/taskrow*.go`, `internal/ui/dialogs/add*.go`, details panel file, `internal/ui/screens/settings*.go`, `internal/ui/window.go` (Back handling, insets), `internal/ui/snapshot/*` (phone fixtures), `docs/screenshots/` (new `phone-*.png`)
- Test: matching `_test.go` files

**Interfaces:**
- Produces: `func screens.Compact(gtx layout.Context) bool` (= `gtx.Constraints.Max.X < gtx.Dp(600)`); settings rows hidden when `platform.Mobile`: output folder, close-to-tray, tray notice; row menu label "Share" instead of "Show in folder" when `platform.Mobile`.
- Compact rules (spec §3): header = mark + "Goti" left, settings icon right, no Add button; aggregate pill on its own line under the header when non-idle; FAB 56 dp circle, Primary, plus icon, 16 dp from bottom/right (above insets), sends the same intent as Add; rows ≥ 64 dp; long-press on a row opens its overflow menu; details = full-window page with back arrow; Add = full-width bottom sheet, URL field prefilled from clipboard if it parses as http(s) URL (only when sheet opens empty).
- Back (`key.NameBack` / Gio's back event): closes menu → dialog/sheet → details; otherwise not handled (system leaves the app).
- Insets: apply the window's system insets (Gio `app.FrameEvent.Insets`) as padding around the whole UI.

- [ ] **Step 1: Failing tests:** `TestCompactBreakpoint` (599 dp → true, 600 → false); `TestCompactHeaderHasNoAddButton` and `TestCompactFABSendsAdd` (render at 360×780, click FAB centre → Add dialog open intent); `TestRowMinHeightCompact` (≥ 64 dp); `TestBackClosesInOrder` (menu open → back closes menu; dialog open → closes dialog; details open → closes details; nothing open → back reported unhandled); `TestSettingsHidesDesktopRowsOnMobile` (render settings with an injected `mobile=true` flag — make the flag a parameter of the settings screen, fed from `platform.Mobile`, so desktop tests can cover it).
- [ ] **Step 2:** FAIL. **Step 3:** implement. Desktop ≥ 600 dp layout must be pixel-identical to before (existing snapshot/layout tests unchanged).
- [ ] **Step 4:** `go test ./internal/ui/...` PASS; `make screenshots` regenerates `docs/screenshots/phone-{main,busy,add,details,settings}-{light,dark}.png` at 360×780; look at them.
- [ ] **Step 5: Commit** `feat(ui): compact phone layout`.

### Task 7: Build scripts, CI release job, README

**Files:**
- Create: `scripts/package-android.sh`, `scripts/android-version-code.sh`, `scripts/android-version-code_test.sh`
- Modify: `Makefile` (`android-aar`, `apk`, `apk-debug`), `.github/workflows/release.yml` (job `android`, release job `needs` + asset glob), `README.md` (Android install + signing note), `.gitignore` (`android/app/libs/*.aar`)

**Interfaces:**
- `scripts/android-version-code.sh <ver>` prints the versionCode: `0.2.0`→`200`, `1.2.3`→`10203`, `v0.2.0`→`200`, `0.2.0-rc1`→`200`, `garbage`→ exit 1.
- `make android-aar`: `gogio -target android -buildmode archive -arch arm64,arm,amd64 -appid io.github.raptordnj.goti.gio -minsdk 26 -ldflags "-X main.version=$(VERSION)" -o android/app/libs/goti.aar ./cmd/goti`. `make apk-debug` = aar + `./gradlew assembleDebug`. `make apk` = aar + `./gradlew assembleRelease -PversionName=$(VERSION) -PversionCode=$(code)`.
- `scripts/package-android.sh <ver> <outdir>`: decodes `ANDROID_KEYSTORE_B64` to a temp file (deleted on exit via trap), exports `ANDROID_KEYSTORE_FILE`, runs `make apk`, runs `apksigner verify --print-certs` and fails unless verification passes, copies to `<outdir>/Goti_<ver>_android_universal.apk`.
- CI job `android` (`needs: test`, `runs-on: ubuntu-latest`): checkout, setup-go (same version source as other jobs), `actions/setup-java@v4` temurin 21, `android-actions/setup-android@v3`, `sdkmanager "platforms;android-35" "build-tools;35.0.0" "ndk;27.2.12479018"`, `go install gioui.org/cmd/gogio@<pinned pseudo-version>`, run `scripts/package-android.sh`, upload artifact. Fail fast with `::error::Release signing secrets missing` if `ANDROID_KEYSTORE_B64` is empty. Release job includes the APK and its line in `SHA256SUMS`.

- [ ] **Step 1:** write `android-version-code_test.sh` with the cases above; run → FAIL.
- [ ] **Step 2:** implement script; test PASS.
- [ ] **Step 3:** Makefile + package script; run `make apk-debug` locally → APK produced; run `scripts/package-android.sh 0.2.0 dist` with the local keystore env (`ANDROID_KEYSTORE_B64=$(base64 -w0 ~/goti-android-signing/goti-release.jks)` and passwords from `credentials.txt` — never echo them) → signed APK, `apksigner verify` passes.
- [ ] **Step 4:** workflow edit; `actionlint` if available, else `python -c 'import yaml…'` parse check.
- [ ] **Step 5: Commit** `ci(android): build and sign APK on release tags`.

### Task 8: Emulator smoke test (local) and release

**Files:**
- Create: `scripts/android-smoke.sh`, `android/app/src/debug/AndroidManifest.xml` (exported activity-alias `.DebugAdd` for action `io.github.raptordnj.goti.debug.ADD` with data scheme http — debug builds only; `GotiActivity` treats it like a share but adds immediately via `Bridge.nativeSharedUrl` + an `autoAdd` flag: `Inbox.SetSharedURL` variant `AddNow(u)` sending `app.AddDownload{URL:u}`)
- Modify: `internal/mobile/wire.go` (+`AddNow`, test `TestInboxAddNow`), `README.md` (manual checks list)

**Smoke script (`scripts/android-smoke.sh <debug.apk>`)** — requires a running emulator (`adb wait-for-device`): serves a 20 MB random file with `python3 -m http.server` on the host; `adb install -r`; `adb shell pm revoke … POST_NOTIFICATIONS` (Review Focus 4); `am start -a io.github.raptordnj.goti.debug.ADD -d http://10.0.2.2:<port>/blob.bin`; waits ≤ 60 s for `dumpsys activity services io.github.raptordnj.goti` to show `DownloadService` `isForeground=true` while downloading; `input keyevent KEYCODE_BACK` then relaunch (Review Focus 1) — app still alive (`pidof`); waits ≤ 120 s for the file in `content query --uri content://media/external/downloads --projection _display_name` matching `blob.bin`; checks the service stopped afterwards; second run: start a download, `am kill`/`am force-stop` is NOT used (it cancels sticky) — instead `adb shell kill <pid>` and wait ≤ 60 s for the process to return with the service foreground again (Review Focus 2). Prints PASS/FAIL per check; exit 1 on any FAIL.

- [ ] **Step 1:** `TestInboxAddNow` failing → implement → PASS.
- [ ] **Step 2:** start the local emulator: `avdmanager create avd -n goti34 -k "system-images;android-34;google_apis;x86_64"` (once), `emulator -avd goti34 -no-window -no-audio -no-snapshot &`; run `make apk-debug` and the smoke script → all PASS. Fix defects found (each fix with a test where the logic is Go or pure Kotlin).
- [ ] **Step 3:** take two emulator screenshots (`adb exec-out screencap -p`) into `docs/screenshots/android-{main,notification}.png`; link in README.
- [ ] **Step 4: Commit** `test(android): emulator smoke script and debug add intent`.
- [ ] **Step 5: Release** (controller, after final review): full `make test` green; merge `feat/android` → `main` `--no-ff`; push; tag `v0.2.0`; push tag; watch the release run; verify the release lists `Goti_0.2.0_android_universal.apk` and `SHA256SUMS` includes it; download it and `apksigner verify --print-certs` shows the SHA-256 in `credentials.txt`.

CI emulator test: ruling — not added to the tag pipeline in v1 (KVM on hosted runners works but adds 10+ min and flakiness to every release); the smoke script runs locally before tagging and is documented in README. Recorded as a deviation from spec §4.
