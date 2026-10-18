# Goti Rebrand Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Rename SwiftFetch to Goti (গতি, "Fast by design. Reliable by nature.") everywhere users see it, with safe data-dir migration.

**Architecture:** Mechanical rename of user-visible strings/binaries/ids plus a small data-dir migration in `internal/config`, a Bengali fallback face in the theme, and an About group in Settings.

**Tech Stack:** Go 1.27, Gio v0.10.3, Noto Sans Bengali (OFL).

**Spec:** `docs/superpowers/specs/2026-10-18-goti-rebrand-design.md`

## Global Constraints

- Values B1–B12 of the spec verbatim (name, Bengali, tagline punctuation, binaries, env var, data dirs, ids).
- GitHub repo is already `raptordnj/goti`; module path becomes `github.com/raptordnj/goti` (rewrite all imports; `go build ./...` must pass).
- Never delete user data; migration uses rename only.
- Branch `feat/gui`; push after the task; `go vet ./...`, gofmt, `go test -race -short ./internal/... ./cmd/...`, archtest green.

## Review Focus

1. Old data dir exists and new exists too → must use new, never merge/overwrite (`TestMigrateDataDir` case).
2. GUI already running from the old binary name while new one starts → instance lock lives in the (migrated) data dir; migration must not run while `gui.lock` in the old dir is held (skip migration + warn).
3. Bengali text rendered as tofu boxes (coverage + shaping test; reviewer inspects snapshot PNG).

---

### Task 1: Rebrand to Goti

**Files:** `go mod edit -module github.com/raptordnj/goti` + rewrite all imports (`gofmt -r` or sed over *.go), `git mv cmd/swiftfetch cmd/goti`, `git mv cmd/swiftfetch-cli cmd/goti-cli`; Modify `internal/config/config.go` (+ tests: dirs, `MigrateDataDir(old, new string, log *slog.Logger) (string, error)`, `ResolveDataDir(env map[string]string, log) string`), both mains (env precedence, migration call before opening the DB / taking locks), `internal/ui/**` strings (window title, header, empty state shows `গতি` + tagline, tray tooltip/menu "Show Goti"), `internal/ui/screens/settings.go` (About group B11), `internal/ui/theme/fonts.go` (+ `NotoSansBengali-{Regular,Medium}.ttf`, OFL text), snapshot fixtures, Makefile, README, `internal/archtest` paths, `scripts/check-brand.sh`.

- [ ] Step 1: Tests — `TestDefaultDataDirGoti`, `TestMigrateDataDir` (4 cases incl. old-locked skip), `TestDataDirEnvPrecedence`, `TestBengaliCovered` (runes of গতি covered; shaping yields no .notdef glyph), `TestAboutShowsBrand` (texts "Goti", "গতি", tagline, version), existing tests updated for renamed strings/paths.
- [ ] Step 2: FAIL → implement → PASS (`go test -race -short ./internal/... ./cmd/...`).
- [ ] Step 3: `scripts/check-brand.sh` (grep for `swiftfetch|SwiftFetch` outside docs/, go.mod/import paths, and the deprecated-env/migration constants) exits 0.
- [ ] Step 4: `make screenshots`; view main-empty light/dark and settings; confirm Goti + গতি render correctly.
- [ ] Step 5: Commit `feat: rebrand SwiftFetch to Goti (গতি)`, push.
