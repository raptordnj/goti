# Goti release builds and CI — design

Date: 2026-10-18. Status: approved for planning (user: "build for all major linux distros including
debian, ubuntu, archlinux, omarchy and also for windows and macos using github actions"; decisions
delegated). Covers both binaries: `goti` (Gio GUI, cgo on Linux/macOS) and `goti-cli`
(pure Go).

## 1. Targets

| Platform | Runner | Arch | Artifacts |
|---|---|---|---|
| Linux | `ubuntu-22.04` (amd64), `ubuntu-22.04-arm` (arm64) | amd64, arm64 | `.deb` (Debian 12+, Ubuntu 22.04+), `.rpm` (Fedora/openSUSE), `.pkg.tar.zst` (Arch, Omarchy, Manjaro), `.tar.gz` (generic) |
| Windows | `windows-latest` | amd64 | **NSIS installer** `Goti_<ver>_windows_amd64_setup.exe` (primary) + portable `goti_<ver>_windows_amd64.zip`; GUI exe built with `-H windowsgui`, icon + version info embedded; CLI exe |
| macOS | `macos-14` (arm64), `macos-13` (amd64) | arm64, amd64 | `Goti_<ver>_macos_<arch>.dmg` with `Goti.app` (ad-hoc signed), plus CLI in a `.tar.gz` |

- glibc floor = Ubuntu 22.04's 2.35 (covers Debian 12, Ubuntu 22.04+, current Arch/Omarchy/Fedora).
- Omarchy is Arch-based: it uses the Arch package; README says `sudo pacman -U goti-*.pkg.tar.zst`.
- `packaging/arch/PKGBUILD` (builds from the release source tarball) is provided for AUR publication;
  publishing to AUR is out of scope.
- Runtime deps declared in packages: Debian/Ubuntu `libwayland-client0, libwayland-egl1,
  libwayland-cursor0, libxkbcommon0, libxkbcommon-x11-0, libx11-xcb1, libxcursor1, libxfixes3,
  libegl1, libvulkan1`; Arch `wayland libxkbcommon libxkbcommon-x11 libxcursor libxfixes libglvnd
  vulkan-icd-loader`; RPM `wayland libxkbcommon libxkbcommon-x11 libXcursor libXfixes mesa-libEGL
  vulkan-loader`.

## 2. Packaging assets (`packaging/`)

- `packaging/icons/goti-{16,32,48,64,128,256,512}.png` generated from one vector source by
  `go run ./packaging/cmd/genicons` (draws the app mark — accent rounded square + white down arrow —
  with the same geometry as the tray icon; writes PNGs, `goti.ico` (multi-size), and an
  `.iconset` dir for macOS `iconutil`). Generated files are committed so CI needs no extra tools.
- `packaging/linux/goti.desktop` (Name=Goti, Exec=goti %U, Icon=goti,
  Categories=Network;FileTransfer;, MimeType=x-scheme-handler/http;… not set — no URL handler),
  `packaging/linux/io.github.raptordnj.Goti.metainfo.xml` (AppStream).
- `packaging/nfpm.yaml`: one config for deb, rpm, archlinux; installs `/usr/bin/goti`,
  `/usr/bin/goti-cli`, desktop file, metainfo, hicolor icons; per-packager `depends` overrides
  as §1; version from `$VERSION`; maintainer, license MIT? — license field = repository license if a
  `LICENSE` file exists, else `Proprietary` (decided at implementation by reading the repo).
- `packaging/windows/winres.json` for `github.com/tc-hib/go-winres` (icon, version info, manifest with
  DPI awareness per-monitor v2); generated `.syso` produced in CI (`go run github.com/tc-hib/go-winres@v0.3.3 make`).
- `packaging/windows/installer.nsi` (NSIS 3, Modern UI 2; user priority 2026-10-18): per-machine
  install to `$PROGRAMFILES64\Goti` (admin, `RequestExecutionLevel admin`); pages Welcome,
  License (if LICENSE exists), Directory, Components (Desktop shortcut — optional, default on;
  "Add goti-cli to PATH" — optional, default off), Install, Finish (Run Goti checkbox);
  Start Menu shortcut "Goti"; uninstaller `Uninstall.exe` registered under
  `HKLM\Software\Microsoft\Windows\CurrentVersion\Uninstall\Goti` (DisplayName, DisplayVersion,
  Publisher "Goti", DisplayIcon, InstallLocation, UninstallString, QuietUninstallString,
  EstimatedSize, NoModify/NoRepair=1); upgrade-in-place detects a previous install via that key and
  runs its uninstaller silently first; refuses to proceed while Goti.exe is running (asks the
  user to quit it); uninstall removes program files, shortcuts, PATH entry (EnVar plugin not used —
  PATH edited via registry + `WM_SETTINGCHANGE` broadcast) and keeps user data in `%LOCALAPPDATA%\Goti` (stated
  on the uninstall page). Installer and uninstaller carry the app icon and version info. Built with
  `makensis -DVERSION=<v> -DARCH=amd64 -DSRCDIR=<dir>`; runs on Linux too (`nsis` package) for local
  verification.
- Linux desktop entry is installed by every Linux package and by `scripts/install-linux.sh`
  (user-local install from the tarball into `~/.local/{bin,share/applications,share/icons/hicolor}`,
  with `--uninstall`), so the generic tarball also integrates with the desktop menu.
- `packaging/macos/Info.plist.tmpl` (CFBundleIdentifier `io.github.raptordnj.Goti`,
  LSMinimumSystemVersion 11.0, NSHighResolutionCapable true) and `packaging/macos/build-app.sh`
  (assemble `.app`, `iconutil` → `.icns`, `codesign --force --deep -s -`, `hdiutil create` dmg).
- `scripts/package-linux.sh <version> <arch>`: builds both binaries (`CGO_ENABLED=1` GUI,
  `CGO_ENABLED=0` CLI, `-trimpath -ldflags "-s -w -X main.version=<v>"`), runs nfpm for deb/rpm/
  archlinux, makes the tarball into `dist/`.
- Version: `main.version` var added to both `cmd/*/main.go` (default `dev`), printed by
  `goti-cli --version` and `goti --version`.

## 3. Workflows (`.github/workflows/`)

- `ci.yml` — on push (all branches) and pull_request: ubuntu-22.04 job installs GUI build deps,
  runs `gofmt -l` check, `go vet ./...`, `go test -race -short ./...`; plus build-only jobs on
  windows-latest and macos-14 (`go build ./...`) to prove the GUI compiles natively. Go version
  from `go.mod` (`actions/setup-go` with `go-version-file`), module cache enabled. Concurrency group
  cancels superseded runs.
- `release.yml` — on tags `v*` and `workflow_dispatch` (input `version`, default `0.0.0-dev`):
  matrix jobs per §1 producing artifacts uploaded with `actions/upload-artifact@v4`; a final
  `release` job (tags only) downloads all artifacts, writes `SHA256SUMS`, and creates a GitHub
  Release via `softprops/action-gh-release@v2` (draft=false, prerelease when tag contains `-`).
- Pinned action versions (major tags of official actions; third-party pinned to a commit SHA).
- The long integration suite (`go test -timeout 30m ./...`) runs only on `workflow_dispatch` of
  ci.yml with input `full=true` (too slow for every push; private-repo minutes).

## 4. Verification

- Local: `scripts/package-linux.sh 0.0.0-test amd64` produces deb/rpm/pkg.tar.zst/tar.gz; inspect
  contents (`bsdtar -tf`, `dpkg-deb -c` if available via `bsdtar` fallback); `pacman -Qip` the Arch
  package; desktop-file-validate if installed.
- CI: push the branch → `ci.yml` green on all three OSes; run `release.yml` via
  `gh workflow run release.yml -f version=0.0.0-ci` → all matrix jobs green, artifacts downloadable;
  download the Linux tarball and Windows zip with `gh run download` and list their contents.
- Macs/Windows binaries are not executed here (no hardware); CI build success + artifact contents are
  the evidence. Notarization and Authenticode signing need secrets → documented as follow-up.

## 5. Out of scope

Code signing/notarization with real certificates, AUR/Flathub/winget/Homebrew publication,
auto-update, Windows arm64, Linux 32-bit.

## Amendment 2026-10-18 (user): tag-only CI

"CI only works on tag release and release contains multi platform builds": `ci.yml` is removed and
`release.yml` triggers only on `v*` tag pushes (no `workflow_dispatch`, no branch/PR runs). Its
`test` job (gofmt, vet, `go test -race -short ./...`) gates the linux/windows/macos build jobs; the
release job publishes all platform artifacts plus `SHA256SUMS` in one GitHub Release.
