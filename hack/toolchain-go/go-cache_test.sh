#!/usr/bin/env bash
# Tests for hack/toolchain-go/go-cache.sh, with a fake warm cache and du.
# Run: hack/toolchain-go/go-cache_test.sh
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
SCRIPT="$(cd "$(dirname "$0")" && pwd)/go-cache.sh"
fail=0
check() { if [ "$2" = "$3" ]; then echo "ok   $1"; else echo "FAIL $1: got [$2] want [$3]"; fail=1; fi; }
has() { [ -e "$1" ] && echo yes || echo no; }

# A fake du reports the sizes in GB that `sizes` queued, one per call and the
# last one from then on, so the bound is tested without gigabytes.
mkdir -p "$T/bin"
cat >"$T/bin/du" <<'EOF'
#!/bin/sh
gb=$(head -n1 "$SIZES")
[ "$(wc -l <"$SIZES")" -gt 1 ] && sed -i 1d "$SIZES"
printf '%s\t%s\n' $((gb * 1024 * 1024)) "$2"
EOF
chmod +x "$T/bin/du"
export SIZES=$T/sizes
sizes() { printf '%s\n' "$@" >"$SIZES"; }
run() { PATH="$T/bin:$PATH" C4_GO_CACHE_SEED=$T/seed bash "$SCRIPT" "$T/cache" 4 >"$T/out" 2>&1; }

mkdir -p "$T/seed/go-build/ab" "$T/seed/golangci-lint/cd"
echo warm >"$T/seed/go-build/ab/warm-d"
echo lint >"$T/seed/golangci-lint/cd/lint-d"
echo readme >"$T/seed/go-build/README"

sizes 1
run
check "first run exits 0" "$?" 0
check "seeds the build cache" "$(cat "$T/cache/build/ab/warm-d")" warm
check "seeds the lint cache" "$(cat "$T/cache/golangci-lint/cd/lint-d")" lint
check "marks it seeded" "$(has "$T/cache/.seeded")" yes

echo mine >"$T/cache/build/ab/warm-d"
echo new >"$T/cache/build/ab/new-d"
run
check "seeds only once" "$(cat "$T/cache/build/ab/warm-d")" mine

mkdir -p "$T/cache/build/ef"
echo old >"$T/cache/build/ef/old-d"
touch -d '2 days ago' "$T/cache/build/ef/old-d"
# Over the bound: the size before, then after dropping the old entries.
sizes 5 3
run
check "over the bound, drops entries unused for a day" "$(has "$T/cache/build/ef/old-d")" no
check "keeps the ones used since" "$(has "$T/cache/build/ab/new-d")" yes
check "under the bound again, doesn't empty it" "$(grep -c 'emptied' "$T/out")" 0

# Still over the bound after the old entries go: everything goes, then a reseed.
sizes 5 5 5 1
run
check "still over: drops recent entries too" "$(has "$T/cache/build/ab/new-d")" no
check "keeps the entry directories" "$(has "$T/cache/build/ab")" yes
check "keeps files outside entry directories" "$(has "$T/cache/build/README")" yes
check "reseeds after emptying" "$(cat "$T/cache/build/ab/warm-d")" warm
check "says what it did" "$(grep -c 'emptied' "$T/out")" 1

sizes 1
rm -rf "$T/seed"
rm -f "$T/cache/.seeded"
run
check "a missing seed is not an error" "$?" 0

exit $fail
