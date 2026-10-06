#!/usr/bin/env bash
# Tests for hack/trim-go-cache.sh against a fake cache: 1 MB entries last used
# 10, 5 and 2 days ago and today, two of each, with limits in MB.
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
  for age in 10 5 2 0; do
    head -c 1048576 /dev/zero >"$T/cache/00/d$age-a"
    head -c 1048576 /dev/zero >"$T/cache/01/d$age-d"
    touch -d "$age days ago" "$T/cache/00/d$age-a" "$T/cache/01/d$age-d"
  done
}
# left: the ages of the entries still there.
left() { find "$T/cache" -mindepth 2 -type f -printf '%f\n' | sed 's/^d\([0-9]*\)-.*/\1/' | sort -nu | tr '\n' ' '; }
trim() { C4_GOCACHE_MAX_MB=$1 "$TRIM" "$T/cache"; }

fake; out=$(trim 9)
check "under the limit: keeps every entry" "$(left)" "0 2 5 10 "
check "under the limit: says nothing" "$out" ""

fake; out=$(trim 7)
check "a little over: only entries unused for 7 days go" "$(left)" "0 2 5 "
echo "$out" | grep -q "was 8 MB, limit 7 MB; dropped 2 MB of entries unused for 7+ days, now 6 MB"
check "a little over: says what it dropped" $? 0

fake; trim 5 >/dev/null
check "further over: entries unused for 3 days go too" "$(left)" "0 2 "

fake; trim 3 >/dev/null
check "far over: entries unused for 1 day go too" "$(left)" "0 "

fake; out=$(trim 1)
check "over even then: keeps entries used today" "$(left)" "0 "
check "over even then: keeps top-level files" "$(cat "$T/cache/README")" "fake"
echo "$out" | grep -q "still over the limit"; check "over even then: says so" $? 0

check "missing directory: exit 0" "$(trim 1 >/dev/null; C4_GOCACHE_MAX_MB=1 "$TRIM" "$T/none"; echo $?)" 0

[ "$fail" = 0 ] && echo PASS || echo FAIL
exit "$fail"
