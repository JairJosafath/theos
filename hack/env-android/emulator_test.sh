#!/usr/bin/env bash
# Tests for hack/env-android/emulator.sh, the android test environment's
# android-emulator, on a fake emulator and adb: start boots the AVD from /, in
# the background, once, and waits for Android; no KVM, an emulator that
# stops and a boot past the timeout fail, saying why.
# Run: hack/env-android/emulator_test.sh
T=$(mktemp -d)
SCRIPT="$(cd "$(dirname "$0")" && pwd)/emulator.sh"
fail=0
check() { if [ "$2" = "$3" ]; then echo "ok   $1"; else echo "FAIL $1: got [$2] want [$3]"; fail=1; fi; }
has() { grep -qF -- "$2" <<<"$1" && echo yes || echo no; }

# The fake emulator says where and how it ran, then boots after FAKE_BOOT_DELAY
# seconds, or stops at once with FAKE_EMULATOR_FAIL; the fake adb reports the
# boot once it happened.
mkdir -p "$T/bin"
cat >"$T/bin/emulator" <<EOF
#!/usr/bin/env bash
pwd >"$T/cwd"
echo "\$*" >"$T/args"
echo start >>"$T/starts"
if [ -n "\${FAKE_EMULATOR_FAIL:-}" ]; then echo "PANIC: Unknown AVD name" >&2; exit 1; fi
sleep "\${FAKE_BOOT_DELAY:-0.2}"
touch "$T/booted"
exec sleep 300
EOF
cat >"$T/bin/adb" <<EOF
#!/usr/bin/env bash
if [ "\$*" = "shell getprop sys.boot_completed" ] && [ -e "$T/booted" ]; then printf '1\r\n'; exit 0; fi
echo "adb: no devices/emulators found" >&2
exit 1
EOF
chmod +x "$T/bin/emulator" "$T/bin/adb"
touch "$T/kvm"
export PATH="$T/bin:$PATH" ANDROID_KVM="$T/kvm" ANDROID_EMULATOR_PID="$T/pid" ANDROID_EMULATOR_LOG="$T/log" \
  ANDROID_BOOT_POLL=0.1
# Stop whatever fake emulator a case left.
cleanup() {
  [ -s "$T/pid" ] && kill "$(<"$T/pid")" 2>/dev/null
  rm -rf "$T"
}
trap cleanup EXIT
reset() {
  [ -s "$T/pid" ] && kill "$(<"$T/pid")" 2>/dev/null
  rm -f "$T/pid" "$T/booted" "$T/starts" "$T/args" "$T/cwd" "$T/log"
}

out=$(cd "$T" && ANDROID_KVM="$T/no-kvm" bash "$SCRIPT" start 2>&1)
check "no KVM fails" "$?" 1
check "no KVM names the device to ask for" "$(has "$out" "devices.kubevirt.io/kvm")" yes
check "no KVM starts no emulator" "$([ -e "$T/starts" ] && echo started || echo none)" none

reset
out=$(cd "$T" && bash "$SCRIPT" start 2>&1)
check "start boots" "$?" 0
check "start waits for Android" "$(has "$out" "Android has booted on theos")" yes
check "the emulator runs from /, not the run's folder" "$(cat "$T/cwd")" /
check "the emulator runs the AVD headless" \
  "$(has "$(cat "$T/args")" "-avd theos -no-window -no-audio -no-boot-anim -no-snapshot")" yes
check "the emulator outlives the run" "$(kill -0 "$(<"$T/pid")" 2>/dev/null && echo running || echo gone)" running
bash "$SCRIPT" start >/dev/null 2>&1
check "a second start boots no second emulator" "$(wc -l <"$T/starts" | tr -d ' ')" 1
bash "$SCRIPT" status >/dev/null 2>&1
check "status once booted" "$?" 0
bash "$SCRIPT" stop
rm -f "$T/booted"
out=$(bash "$SCRIPT" status 2>&1)
check "status after stop fails" "$?" 1
check "status after stop says it is not running" "$(has "$out" "the emulator is not running")" yes

reset
out=$(FAKE_EMULATOR_FAIL=1 bash "$SCRIPT" start 2>&1)
check "an emulator that stops fails the start" "$?" 1
check "an emulator that stops shows its log" "$(has "$out" "PANIC: Unknown AVD name")" yes

reset
out=$(FAKE_BOOT_DELAY=30 ANDROID_BOOT_TIMEOUT=1 bash "$SCRIPT" start 2>&1)
check "a boot past the timeout fails" "$?" 1
check "a boot past the timeout says so" "$(has "$out" "Android did not boot in 1s")" yes

out=$(bash "$SCRIPT" reboot 2>&1)
check "an unknown command fails" "$?" 2
exit $fail
