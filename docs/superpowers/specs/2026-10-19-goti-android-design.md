# Goti for Android — design

Date: 2026-10-19. Requested by the user: an Android APK of Goti, released from GitHub Actions, with
downloads running in a foreground service with a sticky notification. Audience for v1: sideloaders
installing the APK from GitHub releases (no Play Store yet). Approach chosen: **Gradle host app +
Gio as a library** (option 1 of 3).

## 1. Architecture

- One Android process. `GotiActivity` hosts the Gio window (Gio's `GioView`). `DownloadService` is a
  Kotlin foreground service, `foregroundServiceType="dataSync"`.
- The Go side (engine, queue, DB, App command loop) starts once per process from a new
  `internal/mobile` package and outlives the activity while the service is alive. Swiping the app
  away removes the window; downloads continue.
- **JNI bridge (narrow):**
  - Go → Kotlin: `startService()`, `updateNotification(title, text, percent)` (≤ 1/s; percent −1 =
    indeterminate), `stopService()`, `publishToDownloads(path, name, mime)`.
  - Kotlin → Go: `onAction("pause_all" | "resume_all")`, `onSharedURL(url)`, `onLowMemory()`.
  - All Kotlin → Go calls become App commands on the existing command channel.
- **Storage:** DB and partial files in `filesDir/goti`. On completion the file is copied into shared
  Downloads via MediaStore; the private copy is deleted only after the copy's size matches. Output
  folder setting hidden on Android.
- **Go changes:** `config.DefaultDataDir` Android branch; tray and single-instance lock excluded with
  `!android` build tags; `platform` Android implementation (open file / share via Intents). Engine and
  queue unchanged.

## 2. Notification and service lifecycle

- Channel "Downloads", `IMPORTANCE_LOW` (silent, no heads-up). `setOngoing(true)` while the service
  runs.
- Content: title `Goti · N downloading`; text `<speed> · <eta> left`; aggregate progress bar
  (indeterminate when any total is unknown). Actions: **Pause all** / **Resume all** (toggles),
  **Open**; tapping the body opens the app.
- All paused: notification shows `Paused · N downloads` and the service stays; after 10 min fully
  paused the service stops.
- **Start** when any task becomes active or queued (Add, shared link, resume-on-startup).
- **Stop** when no task is active or queued, or after the 10-min all-paused timeout. On stop after
  completions, post a dismissible `Downloads complete · N files` notification.
- Process killed: `START_STICKY`; on restart Go reopens the DB and resume-on-startup resumes tasks
  that were active.
- Android 14+ `onTimeout` (dataSync quota): pause all as interrupted and notify
  `Downloads paused by Android — tap to resume`.
- Permissions: `INTERNET`, `FOREGROUND_SERVICE`, `FOREGROUND_SERVICE_DATA_SYNC`,
  `POST_NOTIFICATIONS` (requested on first Add on Android 13+; downloads still work if denied),
  `WAKE_LOCK` (partial, held only while bytes are moving), `ACCESS_NETWORK_STATE`. No storage
  permission. No battery-optimisation exemption in v1.
- Connectivity callback feeds the existing offline detection.

## 3. Phone UI (width < 600 dp; also improves narrow desktop windows)

- Header: app mark + "Goti" left, settings right; Add becomes a bottom-right FAB; aggregate pill below
  the header.
- Rows ≥ 64 dp touch height; primary action always visible; long-press or ⋮ opens the menu (Open,
  Share, Copy link, Cancel, Remove…). "Show in folder" → "Share" on Android.
- Details: full-screen page with back arrow.
- Add: full-width bottom sheet; prefilled from clipboard when it holds a URL, and from a shared link.
- Settings: hide output folder, close-to-tray and tray notice; keep the rest.
- System Back closes menu → dialog → details → leaves the app (downloads continue). Respect system bar
  and IME insets. Sizes in sp follow system font scale.

## 4. Build, release, signing, testing

- `android/`: Gradle project (Kotlin, AGP 8.x), `compileSdk`/`targetSdk` 35, `minSdk` 26, app id
  `io.github.raptordnj.goti`.
- `make android-aar`: `gogio -target android -buildmode archive -arch arm64,arm,amd64 ./cmd/goti` →
  `android/app/libs/goti.aar`. `make apk`: AAR + `./gradlew assembleRelease`.
- One universal APK. `versionName` from the tag; `versionCode = major*10000 + minor*100 + patch`.
- CI: new `android` job in `release.yml` (tag-only), gated by the test job: setup-java 17, setup-go,
  Android SDK + NDK r27, gogio at a pinned version. Output `Goti_<ver>_android_universal.apk`, added
  to the release and `SHA256SUMS`.
- **Signing:** keystore and passwords come from repo secrets `ANDROID_KEYSTORE_B64`,
  `ANDROID_KEYSTORE_PASSWORD`, `ANDROID_KEY_ALIAS`, `ANDROID_KEY_PASSWORD` (already set 2026-10-19;
  the key lives outside the repo at `~/goti-android-signing/`). Missing secrets fail the job; an
  unsigned APK is never published. The keystore is never committed.
- **Tests:**
  - Go: notification text, service start/stop rules (incl. 10-min paused timeout), versionCode,
    Android data dir — all JNI-free.
  - Kotlin JVM unit tests: notification builder, action intents, share-intent URL extraction.
  - Instrumented smoke on an API 34 emulator in CI: launch, share a local test server URL, wait for
    completion, assert the file exists in MediaStore Downloads. If the emulator proves too flaky in
    CI, this becomes a documented manual check (recorded as a ruling).
  - Phone snapshot fixtures (360×780, light + dark) for the README.

## Out of scope (v1)

Play Store, AAB, F-Droid, tablet layout, Wi-Fi-only, per-ABI APK splits.
