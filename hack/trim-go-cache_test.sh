#!/usr/bin/env bash
# Tests for hack/trim-go-cache.sh against a fake cache: 1 MB entries last used
# 240, 120, 48, 13 and 4 hours ago and now, two of each, with limits in MB.
# Run: hack/trim-go-cache_test.sh
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
TRIM="$(cd "$(dirname "$0")" && pwd)/trim-go-cache.sh"
fail=0
check() { if [ "$2" = "$3" ]; then echo "ok   $1"; else echo "FAIL $1: got [$2] want [$3]"; fail=1; fi; }

# fake: a fresh cache in $T/cache.
fake() {
  rm -rf "$T/cache"
  mkdir -p "$T/cache/00" "$T/cache/01"
  echo "fake" >"$T/cache/README"
  touch -d '30 days ago' "$T/cache/README"
  local age
  for age in 240 120 48 13 4 0; do
    head -c 1048576 /dev/zero >"$T/cache/00/h$age-a"
    head -c 1048576 /dev/zero >"$T/cache/01/h$age-d"
    touch -d "$age hours ago" "$T/cache/00/h$age-a" "$T/cache/01/h$age-d"
  done
}
# left: the ages in hours of the entries still there.
left() { find "$T/cache" -mindepth 2 -type f -printf '%f\n' | sed 's/^h\([0-9]*\)-.*/\1/' | sort -nu | tr '\n' ' '; }
trim() { C4_GOCACHE_MAX_MB=$1 "$TRIM" "$T/cache"; }

fake; out=$(trim 13)
check "under the limit: keeps every entry" "$(left)" "0 4 13 48 120 240 "
check "under the limit: says nothing" "$out" ""

fake; out=$(trim 11)
check "a little over: only entries unused for 7 days go" "$(left)" "0 4 13 48 120 "
echo "$out" | grep -q "was 12 MB, limit 11 MB; dropped 2 MB of entries unused for 7+ days, now 10 MB"
check "a little over: says what it dropped" $? 0

fake; trim 9 >/dev/null
check "further over: entries unused for 3 days go too" "$(left)" "0 4 13 48 "

fake; trim 7 >/dev/null
check "far over: entries unused for 1 day go too" "$(left)" "0 4 13 "

fake; out=$(trim 5)
check "farther over: entries unused for 12 hours go too" "$(left)" "0 4 "
echo "$out" | grep -q "dropped 8 MB of entries unused for 12+ hours, now 4 MB"
check "farther over: says the hours" $? 0

fake; trim 3 >/dev/null
check "farthest over: entries unused for 3 hours go too" "$(left)" "0 "

fake; out=$(trim 1)
check "over even then: keeps entries used in the last 2 hours" "$(left)" "0 "
check "over even then: keeps top-level files" "$(cat "$T/cache/README")" "fake"
echo "$out" | grep -q "still over the limit"; check "over even then: says so" $? 0

check "missing directory: exit 0" "$(trim 1 >/dev/null; C4_GOCACHE_MAX_MB=1 "$TRIM" "$T/none"; echo $?)" 0

[ "$fail" = 0 ] && echo PASS || echo FAIL
exit "$fail"
