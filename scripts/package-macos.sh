#!/usr/bin/env bash
# package-macos.sh VERSION ARCH (amd64 or arm64; must match the host, the GUI
# needs cgo) writes dist/Goti_<VERSION>_macos_<ARCH>.dmg (Goti.app, an
# Applications link and LICENSE) and dist/goti-cli_<VERSION>_macos_<ARCH>.tar.gz.
set -euo pipefail
cd "$(dirname "$0")/.."
VERSION=${1:?usage: package-macos.sh VERSION ARCH}
ARCH=${2:?usage: package-macos.sh VERSION ARCH}
case $ARCH in
amd64 | arm64) ;;
*)
	echo "package-macos.sh: unsupported ARCH '$ARCH' (want amd64 or arm64)" >&2
	exit 2
	;;
esac

dist=$PWD/dist
stage=$dist/stage-macos
rm -rf "$stage"
mkdir -p "$stage/bin" "$stage/dmg" "$stage/cli"
ldflags="-s -w -X main.version=$VERSION"
export MACOSX_DEPLOYMENT_TARGET=11.0
CGO_ENABLED=1 GOOS=darwin GOARCH=$ARCH go build -trimpath -ldflags "$ldflags" -o "$stage/bin/goti" ./cmd/goti
CGO_ENABLED=0 GOOS=darwin GOARCH=$ARCH go build -trimpath -ldflags "$ldflags" -o "$stage/cli/goti-cli" ./cmd/goti-cli

packaging/macos/build-app.sh "$VERSION" "$stage/bin/goti" "$stage/dmg"
ln -s /Applications "$stage/dmg/Applications"
cp LICENSE "$stage/dmg/LICENSE"

dmg=$dist/Goti_${VERSION}_macos_${ARCH}.dmg
rm -f "$dmg"
hdiutil create -volname "Goti" -srcfolder "$stage/dmg" -ov -format UDZO "$dmg"

codesign --force -s - "$stage/cli/goti-cli"
cp LICENSE "$stage/cli/LICENSE"
tar -C "$stage/cli" -czf "$dist/goti-cli_${VERSION}_macos_${ARCH}.tar.gz" goti-cli LICENSE
rm -rf "$stage"
ls -l "$dmg" "$dist/goti-cli_${VERSION}_macos_${ARCH}.tar.gz"
