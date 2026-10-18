#!/usr/bin/env bash
# build-app.sh VERSION BINARY OUTDIR assembles OUTDIR/Goti.app from a built goti
# binary: Info.plist from Info.plist.tmpl, goti.icns via iconutil, LICENSE in
# Contents/Resources, then an ad-hoc signature (codesign -s -) that is verified.
# macOS only (iconutil, codesign).
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../.." && pwd)
VERSION=${1:?usage: build-app.sh VERSION BINARY OUTDIR}
BINARY=${2:?usage: build-app.sh VERSION BINARY OUTDIR}
OUTDIR=${3:?usage: build-app.sh VERSION BINARY OUTDIR}

# CFBundleShortVersionString must be numeric: strip "v" and any suffix.
short=${VERSION#v}
short=${short%%[-+]*}

app=$OUTDIR/Goti.app
rm -rf "$app"
mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"
install -m 0755 "$BINARY" "$app/Contents/MacOS/Goti"
sed -e "s|@VERSION@|$VERSION|" -e "s|@SHORT_VERSION@|$short|" \
	"$here/Info.plist.tmpl" >"$app/Contents/Info.plist"
plutil -lint "$app/Contents/Info.plist"
iconutil -c icns -o "$app/Contents/Resources/goti.icns" "$root/packaging/icons/goti.iconset"
cp "$root/LICENSE" "$app/Contents/Resources/LICENSE"
printf 'APPL????' >"$app/Contents/PkgInfo"
codesign --force --deep -s - "$app"
codesign --verify --deep --strict --verbose=2 "$app"
echo "$app"
