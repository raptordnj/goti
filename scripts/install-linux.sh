#!/usr/bin/env bash
# install-linux.sh [--prefix DIR] [--uninstall]
# Installs Goti from an unpacked release tarball for the current user (default
# prefix ~/.local): bin/goti, bin/goti-cli, the desktop entry, hicolor icons and
# AppStream metainfo. Every installed path is recorded in
# DIR/share/goti/install-manifest; --uninstall removes exactly those paths.
set -euo pipefail
prefix=$HOME/.local
uninstall=0
while [ $# -gt 0 ]; do
	case $1 in
	--prefix) prefix=${2:?--prefix needs a directory}; shift 2 ;;
	--prefix=*) prefix=${1#--prefix=}; shift ;;
	--uninstall) uninstall=1; shift ;;
	-h | --help) sed -n '2,6p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
	*) echo "install-linux.sh: unknown argument: $1" >&2; exit 2 ;;
	esac
done
case $prefix in
*[\\\"\`\$%]*) echo "install-linux.sh: prefix must not contain \\ \" \` \$ or %" >&2; exit 2 ;;
esac
mkdir -p "$prefix"
prefix=$(cd "$prefix" && pwd)
manifest=$prefix/share/goti/install-manifest

# Cache refreshes are best effort.
# shellcheck disable=SC2015
refresh() {
	command -v update-desktop-database >/dev/null && update-desktop-database -q "$prefix/share/applications" 2>/dev/null || true
	command -v gtk-update-icon-cache >/dev/null && gtk-update-icon-cache -q -t "$prefix/share/icons/hicolor" 2>/dev/null || true
}

if [ "$uninstall" = 1 ]; then
	if [ ! -f "$manifest" ]; then
		echo "install-linux.sh: no Goti install found under $prefix" >&2
		exit 1
	fi
	while IFS= read -r f; do
		case $f in "$prefix"/*) ;; *) continue ;; esac
		rm -f -- "$f"
		# Prune directories left empty, but never the prefix itself.
		d=$(dirname "$f")
		while [ "$d" != "$prefix" ] && rmdir -- "$d" 2>/dev/null; do d=$(dirname "$d"); done
	done <"$manifest"
	rm -f -- "$manifest"
	rmdir -- "$prefix/share/goti" 2>/dev/null || true
	refresh
	echo "Goti removed from $prefix (your downloads and settings are kept)."
	exit 0
fi

src=$(cd "$(dirname "$0")" && pwd)
for f in goti goti-cli goti.desktop io.github.raptordnj.Goti.metainfo.xml; do
	[ -f "$src/$f" ] || { echo "install-linux.sh: $src/$f missing; run from the unpacked tarball" >&2; exit 1; }
done

mkdir -p "$prefix/share/goti"
: >"$manifest.new"
put() { # put MODE SRC DST
	install -D -m "$1" "$2" "$3"
	echo "$3" >>"$manifest.new"
}
put 0755 "$src/goti" "$prefix/bin/goti"
put 0755 "$src/goti-cli" "$prefix/bin/goti-cli"
for s in 16 32 48 64 128 256 512; do
	[ -f "$src/icons/goti-$s.png" ] && put 0644 "$src/icons/goti-$s.png" "$prefix/share/icons/hicolor/${s}x${s}/apps/goti.png"
done
put 0644 "$src/io.github.raptordnj.Goti.metainfo.xml" "$prefix/share/metainfo/io.github.raptordnj.Goti.metainfo.xml"
[ -f "$src/LICENSE" ] && put 0644 "$src/LICENSE" "$prefix/share/licenses/goti/LICENSE"
# Point Exec at the installed binary so the menu entry works even when
# $prefix/bin is not on PATH. Quoted per the Desktop Entry spec.
desktop=$prefix/share/applications/goti.desktop
mkdir -p "$(dirname "$desktop")"
exec_path=$prefix/bin/goti
while IFS= read -r line || [ -n "$line" ]; do
	case $line in
	Exec=*) printf 'Exec="%s"\n' "$exec_path" ;;
	*) printf '%s\n' "$line" ;;
	esac
done <"$src/goti.desktop" >"$desktop"
chmod 0644 "$desktop"
echo "$desktop" >>"$manifest.new"
# Merge with a previous manifest so an upgrade still uninstalls old leftovers.
[ -f "$manifest" ] && cat "$manifest" >>"$manifest.new"
sort -u "$manifest.new" >"$manifest"
rm -f "$manifest.new"
refresh
echo "Goti installed to $prefix."
case ":$PATH:" in *":$prefix/bin:"*) ;; *) echo "Note: $prefix/bin is not on PATH; goti-cli needs it there to run by name." ;; esac
