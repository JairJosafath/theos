#!/usr/bin/env bash
# android-emulator, in the android test environment's image: boots the
# image's AVD once per lease, in the background, and waits until Android has
# booted, so the runs that follow find a device on adb:
#
#   theos-env run android -- android-emulator start
#   theos-env run android -- ./gradlew connectedDebugAndroidTest
#
# theos-env keeps what a run started in the background running for the rest
# of the lease, and replaces /work on each run (docs/plans/test-environments.md,
# T4), so the emulator starts from /, its output in a log.
#
#   android-emulator [start]   boot the emulator unless it runs, then wait for Android
#   android-emulator status    exit 0 once Android has booted
#   android-emulator stop      stop the emulator
#
# ANDROID_AVD (theos) names the AVD, ANDROID_EMULATOR_ARGS adds the
# emulator's flags, ANDROID_BOOT_TIMEOUT (600) is how many seconds the boot
# may take, and ANDROID_EMULATOR_LOG (/tmp/android-emulator.log) is its
# output. ANDROID_KVM, ANDROID_EMULATOR_PID and ANDROID_BOOT_POLL are for the
# tests (emulator_test.sh).
set -euo pipefail

avd=${ANDROID_AVD:-theos}
log=${ANDROID_EMULATOR_LOG:-/tmp/android-emulator.log}
pidfile=${ANDROID_EMULATOR_PID:-/tmp/android-emulator.pid}
kvm=${ANDROID_KVM:-/dev/kvm}
timeout=${ANDROID_BOOT_TIMEOUT:-600}
poll=${ANDROID_BOOT_POLL:-2}

running() { [[ -s $pidfile ]] && kill -0 "$(<"$pidfile")" 2>/dev/null; }
booted() { [[ $(adb shell getprop sys.boot_completed 2>/dev/null | tr -d '\r') == 1 ]]; }

start() {
  if ! running; then
    if [[ ! -r $kvm || ! -w $kvm ]]; then
      echo "No $kvm here, which the emulator needs: give the TestEnvironment the cluster's KVM device in local.devices, such as devices.kubevirt.io/kvm: 1" >&2
      exit 1
    fi
    # Its own session, so the end of this run's command doesn't stop it.
    # shellcheck disable=SC2086 # the extra flags are words
    (cd / && exec setsid emulator -avd "$avd" -no-window -no-audio -no-boot-anim -no-snapshot \
      -gpu swiftshader_indirect ${ANDROID_EMULATOR_ARGS:-} >"$log" 2>&1 </dev/null) &
    echo $! >"$pidfile"
    echo "Booting $avd; its output is in $log"
  fi
  local deadline=$((SECONDS + timeout))
  until booted; do
    if ! running; then
      echo "The emulator stopped before Android booted; the end of $log:" >&2
      tail -n 30 "$log" >&2 || true
      exit 1
    fi
    if ((SECONDS >= deadline)); then
      echo "Android did not boot in ${timeout}s; the emulator still runs. The end of $log:" >&2
      tail -n 30 "$log" >&2 || true
      exit 1
    fi
    sleep "$poll"
  done
  echo "Android has booted on $avd"
}

case ${1:-start} in
  start) start ;;
  status)
    if booted; then echo "Android has booted on $avd"; else
      echo "Android has not booted$(running && echo ' yet' || echo ': the emulator is not running')" >&2
      exit 1
    fi
    ;;
  stop)
    if running; then kill "$(<"$pidfile")"; fi
    rm -f "$pidfile"
    ;;
  *)
    echo "usage: android-emulator [start|status|stop]" >&2
    exit 2
    ;;
esac
