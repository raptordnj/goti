#!/usr/bin/env bash
# Smoke-tests a Goti debug APK on a running emulator/VM.
# Usage: scripts/android-smoke.sh <debug.apk>
# Env: ADB_SERIAL (default 127.0.0.1:6555, Genymotion), HOST_IP (host as seen
# from the device; default 10.0.3.2 for Genymotion, use 10.0.2.2 for an AVD),
# PORT (default 8765).
set -uo pipefail

APK=${1:?usage: $0 <debug.apk>}
ADB_SERIAL=${ADB_SERIAL:-127.0.0.1:6555}
HOST_IP=${HOST_IP:-10.0.3.2}
PORT=${PORT:-8765}
PKG=io.github.raptordnj.goti
ADD=io.github.raptordnj.goti.debug.ADD
adb() { command adb -s "$ADB_SERIAL" "$@"; }

fails=0
pass() { echo "PASS: $*"; }
fail() { echo "FAIL: $*"; fails=$((fails + 1)); }
# wait_for <seconds> <cmd...>: polls cmd every second until it succeeds.
wait_for() {
	local t=$1; shift
	for ((i = 0; i < t; i++)); do "$@" && return 0; sleep 1; done
	return 1
}

work=$(mktemp -d)
srv=""
cleanup() { [[ -n $srv ]] && kill "$srv" 2>/dev/null; rm -rf "$work"; }
trap cleanup EXIT

adb wait-for-device
head -c $((20 * 1024 * 1024)) /dev/urandom >"$work/blob.bin"
cp "$work/blob.bin" "$work/blob2.bin"
# Throttled server so a 20 MB download lasts long enough to observe the service.
cat >"$work/serve.py" <<'PY'
import http.server, sys, time
class H(http.server.SimpleHTTPRequestHandler):
    def copyfile(self, src, dst):
        while True:
            b = src.read(256 * 1024)
            if not b: break
            dst.write(b); time.sleep(0.2)
    def log_message(self, *a): pass
http.server.ThreadingHTTPServer.allow_reuse_address = True
http.server.test(HandlerClass=H, ServerClass=http.server.ThreadingHTTPServer, port=int(sys.argv[1]), bind="0.0.0.0")
PY
(cd "$work" && exec python3 serve.py "$PORT" >/dev/null 2>&1) &
srv=$!
sleep 1

adb shell am force-stop "$PKG" >/dev/null 2>&1
adb uninstall "$PKG" >/dev/null 2>&1 # fresh state: no earlier blob.bin downloads
if adb install -r "$APK" >/dev/null; then pass "install"; else fail "install"; exit 1; fi
adb shell pm revoke "$PKG" android.permission.POST_NOTIFICATIONS >/dev/null 2>&1
# Drop earlier runs' files and rows (MediaStore rows outlive an uninstall, and
# a name clash would publish "blob (1).bin" instead).
adb shell rm -f '/sdcard/Download/Goti/blob*' >/dev/null 2>&1
adb shell content delete --uri content://media/external/downloads --where "\"_display_name LIKE 'blob%'\"" >/dev/null 2>&1
if adb shell content query --uri content://media/external/downloads --projection _display_name | grep -q '_display_name=blob'; then
	fail "stale blob rows remain in MediaStore"; exit 1
fi

fg() { live_services | grep -A40 'DownloadService' | grep -q 'isForeground=true'; }
# live_services: dumpsys "active services" sections only (stale records of
# dead processes linger under "Destroying services" / "Last ANR service")
live_services() { adb shell dumpsys activity services "$PKG" | awk '/^  [A-Z][^ ].*:$/ { skip = !/active services/ } !skip'; }
svc_gone() { ! live_services | grep -q 'DownloadService'; }
# resumed: GotiActivity reached onResume (its main thread is not stuck in onCreate).
resumed() { adb shell dumpsys activity activities | grep -E 'topResumedActivity|mResumedActivity' | grep -q "$PKG/.GotiActivity"; }
pid() { adb shell pidof "$PKG" | tr -d '\r'; }
SIZE=$((20 * 1024 * 1024))
# published <name>: a full-size Downloads/Goti row with exactly that name.
published() { adb shell content query --uri content://media/external/downloads --projection _display_name:_size | tr -d '\r' | grep -q "_display_name=$1, _size=$SIZE\$"; }

adb shell am start -a "$ADD" -d "http://$HOST_IP:$PORT/blob.bin" "$PKG" >/dev/null 2>&1
if wait_for 60 fg; then pass "service foreground while downloading"; else fail "service foreground while downloading"; fi

adb shell input keyevent KEYCODE_BACK
sleep 2
p1=$(pid)
adb shell monkey -p "$PKG" -c android.intent.category.LAUNCHER 1 >/dev/null 2>&1
if wait_for 15 resumed; then pass "activity resumed after Back + relaunch"; else fail "activity resumed after Back + relaunch"; fi
if [[ -n $p1 && $(pid) == "$p1" ]]; then pass "process alive after Back + relaunch"; else fail "process alive after Back + relaunch (pid before=$p1 after=$(pid))"; fi

if wait_for 120 published blob.bin; then pass "blob.bin published to Downloads"; else fail "blob.bin published to Downloads"; fi
if wait_for 30 svc_gone; then pass "service stopped after completion"; else fail "service stopped after completion"; fi

# Second run: process death mid-download; sticky service must bring it back.
adb shell am start -a "$ADD" -d "http://$HOST_IP:$PORT/blob2.bin" "$PKG" >/dev/null 2>&1
if wait_for 60 fg; then
	old=$(pid)
	adb shell input keyevent KEYCODE_HOME
	sleep 1
	adb shell kill "$old" 2>/dev/null || adb shell su 0 kill "$old" 2>/dev/null || adb shell run-as "$PKG" kill "$old"
	back() { local p; p=$(pid); [[ -n $p && $p != "$old" ]] && fg; }
	if wait_for 60 back; then pass "service restarted in foreground after process kill"; else fail "service restarted in foreground after process kill"; fi
	if wait_for 120 published blob2.bin; then pass "blob2.bin published after restart"; else fail "blob2.bin published after restart"; fi
else
	fail "second download did not start"
fi

if ((fails)); then echo "SMOKE: $fails check(s) FAILED"; exit 1; fi
echo "SMOKE: all checks passed"
