#!/usr/bin/env bash
# package-windows.sh VERSION cross-builds Goti for Windows amd64 on Linux/macOS and
# writes dist/goti_<VERSION>_windows_amd64.zip and, when makensis (NSIS 3) is on
# PATH, dist/Goti_<VERSION>_windows_amd64_setup.exe. CI uses package-windows.ps1.
set -euo pipefail
cd "$(dirname "$0")/.."
VERSION=${1:?usage: package-windows.sh VERSION}
ARCH=amd64
WINRES=github.com/tc-hib/go-winres@v0.3.3

# Numeric a.b.c.d for Windows version resources: drop a leading "v" and any
# pre-release/build suffix, keep up to four numeric parts, pad with zeros.
numver() {
	local v=${1#v}
	v=${v%%[-+]*}
	local IFS=.
	# shellcheck disable=SC2206
	local p=($v) out=() i
	for i in 0 1 2 3; do
		local n=${p[$i]:-0}
		[[ $n =~ ^[0-9]+$ ]] || n=0
		out+=("$((10#$n))")
	done
	echo "${out[*]}"
}
VERSION_NUM=$(numver "$VERSION")

dist=$PWD/dist
stage=$dist/stage-windows
rm -rf "$stage"
mkdir -p "$stage"

syso=cmd/goti/rsrc_windows_$ARCH.syso
trap 'rm -f "$syso"' EXIT
go run "$WINRES" make --in packaging/windows/winres.json --out cmd/goti/rsrc --arch "$ARCH" \
	--product-version "$VERSION_NUM" --file-version "$VERSION_NUM"

ldflags="-s -w -X main.version=$VERSION"
CGO_ENABLED=0 GOOS=windows GOARCH=$ARCH go build -trimpath -ldflags "$ldflags -H windowsgui" -o "$stage/Goti.exe" ./cmd/goti
CGO_ENABLED=0 GOOS=windows GOARCH=$ARCH go build -trimpath -ldflags "$ldflags" -o "$stage/goti-cli.exe" ./cmd/goti-cli
go run ./packaging/cmd/pesubsystem -want 2 "$stage/Goti.exe"
go run ./packaging/cmd/pesubsystem -want 3 "$stage/goti-cli.exe"

zipfile=$dist/goti_${VERSION}_windows_${ARCH}.zip
rm -f "$zipfile"
cp LICENSE "$stage/LICENSE.txt"
(cd "$stage" && zip -q -9 "$zipfile" Goti.exe goti-cli.exe LICENSE.txt)

if command -v makensis >/dev/null; then
	makensis -V3 -WX -INPUTCHARSET UTF8 -DVERSION="$VERSION" -DVERSION_NUM="$VERSION_NUM" -DARCH="$ARCH" \
		-DSRCDIR="$stage" -DOUTFILE="$dist/Goti_${VERSION}_windows_${ARCH}_setup.exe" \
		packaging/windows/installer.nsi
else
	echo "package-windows.sh: makensis not found; skipping the installer" >&2
fi
rm -rf "$stage"
ls -l "$dist"/*"_${VERSION}_windows_${ARCH}"*
