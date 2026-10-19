#!/usr/bin/env bash
# Tests for android-version-code.sh.
cd "$(dirname "$0")"
fail=0
check() {
	local in=$1 want=$2 got
	got=$(./android-version-code.sh "$in" 2>/dev/null)
	if [ "$got" != "$want" ]; then echo "FAIL: $in -> '$got' (want '$want')"; fail=1; fi
}
check 0.2.0 200
check 1.2.3 10203
check v0.2.0 200
check 0.2.0-rc1 200
if ./android-version-code.sh garbage >/dev/null 2>&1; then echo "FAIL: garbage should exit 1"; fail=1; fi
[ $fail = 0 ] && echo "ok"
exit $fail
