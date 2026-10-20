#!/usr/bin/env bash
# package-android.sh VERSION OUTDIR builds and signs the release APK and copies it to
# OUTDIR/Goti_<VERSION>_android_universal.apk, plus the Go/Gio library as
# OUTDIR/goti_<VERSION>_android.aar.
# Requires ANDROID_KEYSTORE_B64 (base64 keystore), ANDROID_KEYSTORE_PASSWORD, ANDROID_KEY_ALIAS,
# ANDROID_KEY_PASSWORD, ANDROID_HOME, gogio on PATH and apksigner (build-tools) available.
set -euo pipefail
cd "$(dirname "$0")/.."
VERSION=${1:?usage: package-android.sh VERSION OUTDIR}
OUTDIR=${2:?usage: package-android.sh VERSION OUTDIR}
VERSION=${VERSION#v}
: "${ANDROID_KEYSTORE_B64:?Release signing secrets missing}"

ks=$(mktemp)
trap 'rm -f "$ks"' EXIT
printf '%s' "$ANDROID_KEYSTORE_B64" | base64 -d >"$ks"
export ANDROID_KEYSTORE_FILE=$ks

make apk VERSION="$VERSION"

apk=android/app/build/outputs/apk/release/app-release.apk
apksigner=$(command -v apksigner || true)
if [ -z "$apksigner" ]; then
	apksigner=$(ls -d "$ANDROID_HOME"/build-tools/*/apksigner 2>/dev/null | sort -V | tail -1)
fi
[ -x "$apksigner" ] || { echo "package-android.sh: apksigner not found" >&2; exit 1; }
"$apksigner" verify --print-certs "$apk" || { echo "package-android.sh: APK signature verification failed" >&2; exit 1; }

mkdir -p "$OUTDIR"
cp "$apk" "$OUTDIR/Goti_${VERSION}_android_universal.apk"
cp android/app/libs/goti.aar "$OUTDIR/goti_${VERSION}_android.aar"
