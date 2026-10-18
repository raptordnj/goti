# Rebrand SwiftFetch → Goti — design

Date: 2026-10-18. Requested by the user: name **Goti**, Bengali wordmark **গতি**, tagline
**"Fast by design. Reliable by nature."** Decisions delegated ("do all autonomously").

## Decisions

| # | Item | New value |
|---|------|-----------|
| B1 | Product / window title / tray / dialogs / README | `Goti` |
| B2 | Bengali wordmark | `গতি` (shown in empty state, About, README, installer welcome, metainfo) |
| B3 | Tagline | `Fast by design. Reliable by nature.` (exact punctuation) |
| B4 | Binaries & cmd dirs | `cmd/goti` → `goti` (GUI; `Goti.exe` on Windows, `Goti.app` on macOS), `cmd/goti-cli` → `goti-cli` |
| B5 | Env var | `GOTI_DATA_DIR`; `SWIFTFETCH_DATA_DIR` still honoured as a fallback (logged as deprecated) |
| B6 | Data dir | Windows `%LOCALAPPDATA%\Goti`, macOS `~/Library/Application Support/Goti`, Linux `$XDG_DATA_HOME/goti` or `~/.local/share/goti` |
| B7 | Migration | On start (GUI and CLI), if the new data dir does not exist and the old SwiftFetch dir does, `os.Rename` old → new (log "migrated data from …"); if rename fails (cross-device / locked), keep using the old dir for this run and log a warning. Never copy-and-delete. |
| B8 | Ids | bundle/AppStream id `io.github.raptordnj.Goti`; desktop file `goti.desktop`; icons `goti.png`; lock file stays `gui.lock` inside the data dir; Windows uninstall key `Goti` |
| B9 | Tray tooltip | `Goti — Fast by design. Reliable by nature.`; status line unchanged |
| B10 | Bengali font | Gio shaper needs a face with Bengali glyphs: embed **Noto Sans Bengali** Regular + Medium (OFL-1.1, from the official notofonts GitHub release) in `internal/ui/theme/fonts/` and add it to the collection after Inter so Latin text keeps Inter. Coverage test asserts every rune in `গতি` is covered and that Bengali shaping produces ≥ 1 glyph cluster per syllable without `.notdef`. |
| B11 | About | Settings screen gets an "About" group: app mark, `Goti` (Title), `গতি` (Body Muted), tagline, version (`main.version`), data dir path. |
| B12 | Repo & module | GitHub repo renamed to `raptordnj/goti` (done 2026-10-18 at the user's request; GitHub redirects the old URL). Go module path becomes `github.com/raptordnj/goti`; every import updated (`go mod edit -module` + rewrite imports). |

## Scope

Rename every user-visible string, binary, package, id and path; keep code identifiers that are not
user-visible unless they contain the old brand in an exported API name (rename those too for
consistency, e.g. package docs). Specs/plans written earlier keep their history; README, Makefile,
packaging plan/spec and polish spec are updated to the new name.

## Testing

- `TestDefaultDataDir` per OS expectations with new names; `TestMigrateDataDir` (old exists/new missing →
  renamed; both exist → new used, old untouched; rename failure → old used + warning); env precedence
  `GOTI_DATA_DIR` > `SWIFTFETCH_DATA_DIR` > default.
- `grep -rni swiftfetch` over non-docs, non-module-path code returns only the deprecated env fallback
  and migration constants (test or CI check script `scripts/check-brand.sh`).
- Snapshot PNGs regenerated show "Goti" and the Bengali wordmark rendered (reviewer looks at them).
