# Goti Release Builds and CI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** GitHub Actions CI plus release builds producing deb/rpm/Arch/tar.gz (Linux amd64+arm64), Windows zip, and macOS dmg (arm64+amd64) for both Goti binaries.

**Architecture:** Committed packaging assets (icons, desktop/metainfo, nfpm config, winres, macOS plist + script) driven by small shell scripts; `ci.yml` for checks on every push, `release.yml` matrix for packaging on tags/dispatch.

**Tech Stack:** GitHub Actions, nfpm v2 (deb/rpm/archlinux), go-winres, iconutil/hdiutil/codesign, Go 1.27, Gio cgo deps.

**Spec:** `docs/superpowers/specs/2026-10-18-swiftfetch-release-ci-design.md`

## Global Constraints

- Branch `feat/gui`; `git push` after each task. Never push tags or create releases (use `workflow_dispatch` for testing).
- Targets, runners, package names and runtime deps exactly as spec §1.
- Bundle id `io.github.raptordnj.Goti`; macOS min 11.0; Windows GUI built with `-H windowsgui`.
- Builds: `-trimpath -ldflags "-s -w -X main.version=<v>"`; GUI `CGO_ENABLED=1` (Linux/macOS), CLI `CGO_ENABLED=0`.
- Third-party actions pinned to commit SHAs; official `actions/*` by major tag.
- No secrets required; signing/notarization is documented follow-up only.

## Review Focus

1. Linux GUI binary linked against a glibc newer than 2.35 → fails on Debian 12/Ubuntu 22.04 (Task 3: CI step `objdump -T goti | grep -o 'GLIBC_[0-9.]*' | sort -V | tail -1` ≤ 2.35, fail otherwise).
2. Package installs file paths that collide with other packages or miss the icon cache (Task 1: nfpm contents test lists exact paths; postinstall runs `gtk-update-icon-cache -f -t /usr/share/icons/hicolor || true` and `update-desktop-database || true`).
3. macOS app opened from dmg reports "damaged" on arm64 due to missing signature (Task 2: `codesign --verify --deep --strict` step in workflow).
4. Windows exe opens a console window (Task 2: `-H windowsgui` asserted by checking PE subsystem via `go version -m`-independent check: PowerShell reads PE header subsystem == 2).
5. `--version` prints `dev` in release builds because ldflags target wrong symbol (Task 1 test: build with `-X main.version=1.2.3` and assert output).

---

### Task 1: Version flag, icons, Linux packaging (local)

**Files:** Modify `cmd/goti/main.go`, `cmd/goti-cli/main.go` (+ tests); Create `packaging/cmd/genicons/main.go`, `packaging/icons/*` (generated, committed), `packaging/linux/goti.desktop`, `packaging/linux/io.github.raptordnj.Goti.metainfo.xml`, `packaging/linux/postinstall.sh`, `packaging/nfpm.yaml`, `scripts/package-linux.sh`; Modify `Makefile` (`package-linux` target), `.gitignore` (`dist/`).

**Interfaces — Produces:** `var version = "dev"` in both mains; `--version` prints `goti <version>` / `goti-cli <version>` and exits 0. `scripts/package-linux.sh VERSION ARCH` → `dist/goti_<VERSION>_linux_<ARCH>.{deb,rpm,pkg.tar.zst,tar.gz}` (nfpm naming may differ — script renames to this scheme). nfpm run via `go run github.com/goreleaser/nfpm/v2/cmd/nfpm@v2.43.0` (pin; check latest v2 tag at implementation and record it).

- [ ] Step 1: Tests — `TestVersionFlag` in both cmd packages (build with `-ldflags -X main.version=1.2.3` via `go build` in a temp dir, run `--version`, assert exact output).
- [ ] Step 2: FAIL → implement version flag → PASS.
- [ ] Step 3: genicons draws the mark (reuse geometry from `internal/tray` icon generator if present; otherwise accent #3461F5 rounded square r=22 %, white down arrow), writes PNG sizes, multi-size ICO, `goti.iconset/` (icon_16x16.png … icon_512x512@2x.png). Run it; commit outputs.
- [ ] Step 4: desktop file, metainfo, postinstall, nfpm.yaml, package script; run `scripts/package-linux.sh 0.0.0-test amd64`; verify with `bsdtar -tf` each package lists `/usr/bin/goti`, `/usr/bin/goti-cli`, desktop, metainfo, icons; `pacman -Qip dist/*.pkg.tar.zst` shows deps; `desktop-file-validate` if installed. Paste listings in report.
- [ ] Step 5: Commit `build: version flag, app icons and Linux packaging`, push.

### Task 2: Windows NSIS installer, plus macOS packaging assets

**Files:** Create `packaging/windows/winres.json`, `packaging/windows/installer.nsi`, `scripts/package-windows.ps1`, `scripts/package-windows.sh` (Linux cross-build + `makensis`, for local verification), `scripts/install-linux.sh`, `packaging/macos/Info.plist.tmpl`, `packaging/macos/build-app.sh`, `scripts/package-macos.sh`.

**Interfaces — Produces:** `scripts/package-windows.{ps1,sh}` VERSION → `dist/Goti_V_windows_amd64_setup.exe` (NSIS per spec §2 installer.nsi bullet, verbatim behaviours) and `dist/goti_V_windows_amd64.zip` containing `Goti.exe` (GUI, `-H windowsgui`, go-winres `.syso` for icon/version/manifest) and `goti-cli.exe`; asserts PE subsystem == 2 (GUI). `scripts/install-linux.sh [--uninstall] [--prefix ~/.local]` installs binaries, desktop entry and hicolor icons from the tarball dir and refreshes caches (`|| true`).
- Local verification for the installer (this machine is Arch): install `nsis` if available (`pacman -Si nsis`; if not installable without sudo, report and rely on CI), run `scripts/package-windows.sh 0.0.0-test`, then list installer contents with `7z l` / `bsdtar -tf` if possible and confirm the expected files; run `scripts/install-linux.sh --prefix <scratch>` against a locally built tarball and confirm desktop entry + icons land and `desktop-file-validate` passes; `--uninstall` removes them. `scripts/package-macos.sh V ARCH` → `dist/Goti_V_macos_ARCH.dmg` (app built, `.icns` via `iconutil`, `codesign --force --deep -s -`, `codesign --verify --deep --strict`, `hdiutil create -format UDZO`) and `dist/goti-cli_V_macos_ARCH.tar.gz`.

- [ ] Step 1: Write assets/scripts. Locally verifiable parts: `shellcheck` on `.sh` if installed; `bash -n`; render `Info.plist` with a test version and validate XML with `xmllint --noout` if available; `go run github.com/tc-hib/go-winres@<pinned> make --arch amd64` in a temp copy works on Linux (it is pure Go) and produces `rsrc_windows_amd64.syso`; cross-build `GOOS=windows go build -ldflags -H=windowsgui ./cmd/goti` succeeds locally (Gio Windows needs no cgo).
- [ ] Step 2: Commit `build: Windows and macOS packaging scripts`, push. (Real execution happens in Task 3 CI.)

### Task 3: GitHub Actions workflows and CI verification

**Files:** Create `.github/workflows/ci.yml`, `.github/workflows/release.yml`; Modify `README.md` (Install section per platform incl. Omarchy, build-from-source, release process `git tag vX.Y.Z && git push --tags` described for the user).

**Interfaces:** per spec §3. Linux job: `apt-get install -y gcc pkg-config libwayland-dev libx11-dev libx11-xcb-dev libxkbcommon-x11-dev libgles2-mesa-dev libegl1-mesa-dev libffi-dev libxcursor-dev libvulkan-dev libxfixes-dev`; glibc floor check (Review Focus 1). Windows job uses `pwsh scripts/package-windows.ps1`; macOS jobs `scripts/package-macos.sh`. Artifacts uploaded per job; release job only on `refs/tags/v*`.

- [ ] Step 1: Write workflows; `actionlint` via `go run github.com/rhysd/actionlint/cmd/actionlint@<pinned>` passes locally.
- [ ] Step 2: Commit `ci: checks and multi-platform release workflow`, push → watch `gh run watch` for ci.yml on the pushed commit; fix until green on Linux, Windows, macOS (each fix its own commit).
- [ ] Step 3: `gh workflow run release.yml --ref feat/gui -f version=0.0.0-ci`; watch; fix until every matrix job is green. `gh run download <id> -D <scratchpad>/artifacts`; list contents of each artifact (zip/tar/dmg via `bsdtar -tf`/`7z l` where possible); record sizes and SHA-256s in the report.
- [ ] Step 4: README Install section; commit `docs: installation per platform and release process`, push.
