#!/usr/bin/env bash
# package-linux.sh VERSION ARCH builds goti and goti-cli and packages them into
# dist/goti_<VERSION>_linux_<ARCH>.{deb,rpm,pkg.tar.zst,tar.gz}.
# ARCH is amd64 or arm64 and must match the host (the GUI needs cgo).
set -euo pipefail
cd "$(dirname "$0")/.."
VERSION=${1:?usage: package-linux.sh VERSION ARCH}
ARCH=${2:?usage: package-linux.sh VERSION ARCH}
NFPM=github.com/goreleaser/nfpm/v2/cmd/nfpm@v2.48.0

dist=$PWD/dist
stage=$dist/stage
rm -rf "$stage"
mkdir -p "$stage"
ldflags="-s -w -X main.version=$VERSION"
CGO_ENABLED=1 GOOS=linux GOARCH=$ARCH go build -trimpath -ldflags "$ldflags" -o "$stage/goti" ./cmd/goti
CGO_ENABLED=0 GOOS=linux GOARCH=$ARCH go build -trimpath -ldflags "$ldflags" -o "$stage/goti-cli" ./cmd/goti-cli
cp packaging/linux/goti.desktop "$stage/"
sed -e "s/@VERSION@/$VERSION/" -e "s/@DATE@/$(date -u +%F)/" \
	packaging/linux/io.github.raptordnj.Goti.metainfo.xml >"$stage/io.github.raptordnj.Goti.metainfo.xml"

export VERSION ARCH
base=goti_${VERSION}_linux_${ARCH}
for p in deb rpm archlinux; do
	tmp=$dist/tmp-$p
	rm -rf "$tmp"
	mkdir -p "$tmp"
	go run "$NFPM" package -f packaging/nfpm.yaml -p "$p" -t "$tmp"
	f=$(ls "$tmp")
	case $p in
	deb) ext=deb ;;
	rpm) ext=rpm ;;
	archlinux) ext=pkg.tar.zst ;;
	esac
	mv "$tmp/$f" "$dist/$base.$ext"
	rmdir "$tmp"
done

# Generic tarball: binaries, desktop integration files and a user-local installer if present.
tar_root=$dist/tar/$base
rm -rf "$dist/tar"
mkdir -p "$tar_root/icons"
cp "$stage"/{goti,goti-cli,goti.desktop,io.github.raptordnj.Goti.metainfo.xml} "$tar_root/"
cp packaging/icons/goti-{16,32,48,64,128,256,512}.png "$tar_root/icons/"
[ -f scripts/install-linux.sh ] && cp scripts/install-linux.sh "$tar_root/"
tar -C "$dist/tar" -czf "$dist/$base.tar.gz" "$base"
rm -rf "$dist/tar" "$stage"
ls -l "$dist/$base".*
