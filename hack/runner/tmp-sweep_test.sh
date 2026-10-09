#!/usr/bin/env bash
# Tests for hack/runner/tmp-sweep.sh.
# Run: hack/runner/tmp-sweep_test.sh
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
SCRIPT="$(cd "$(dirname "$0")" && pwd)/tmp-sweep.sh"
fail=0
check() { if [ "$2" = "$3" ]; then echo "ok   $1"; else echo "FAIL $1: got [$2] want [$3]"; fail=1; fi; }
has() { [ -e "$1" ] && echo yes || echo no; }

# Nothing is a minute old yet: a sweep of what is older than 60 minutes keeps it all.
mkdir -p "$T/tmp/node-compile-cache/v24" "$T/tmp/go-build1"
echo old >"$T/tmp/node-compile-cache/v24/entry"
echo old >"$T/tmp/go-build1/_pkg_.a"
# tar sets mtime back; the sweep must not take that for an old file.
touch -d '2 days ago' "$T/tmp/go-build1/_pkg_.a"
bash "$SCRIPT" "$T/tmp" 60
check "keeps what is newer than the age" "$(has "$T/tmp/node-compile-cache/v24/entry")" yes
check "keeps a new file with an old mtime" "$(has "$T/tmp/go-build1/_pkg_.a")" yes

# Older than 0 minutes is everything written before now.
sleep 1
bash "$SCRIPT" "$T/tmp" 0
check "removes old files" "$(has "$T/tmp/node-compile-cache/v24/entry")" no
check "removes the emptied directories" "$(has "$T/tmp/node-compile-cache")" no
check "keeps the swept directory" "$(has "$T/tmp")" yes

check "a missing directory is no failure" "$(bash "$SCRIPT" "$T/none" 0; echo $?)" 0
exit $fail
