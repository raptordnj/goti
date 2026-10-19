#!/usr/bin/env bash
# android-version-code.sh VERSION prints the Android versionCode: MAJOR*10000 + MINOR*100 + PATCH.
# A leading "v" and any "-prerelease" suffix are ignored.
set -euo pipefail
v=${1:-}
v=${v#v}
v=${v%%-*}
if [[ ! $v =~ ^([0-9]+)\.([0-9]+)\.([0-9]+)$ ]]; then
	echo "android-version-code.sh: invalid version '${1:-}'" >&2
	exit 1
fi
echo $((10#${BASH_REMATCH[1]} * 10000 + 10#${BASH_REMATCH[2]} * 100 + 10#${BASH_REMATCH[3]}))
