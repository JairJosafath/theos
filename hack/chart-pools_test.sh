#!/usr/bin/env bash
# Tests for hack/chart-pools.sh: a render whose every GitProvider carries a
# base and a go runner passes; one short of a label fails naming the file,
# the counts and the label, also when the label has no match at all, where
# grep -c exits 1 and set -e once ended the script before the message
# (#1730, a #1698 follow-up).
# Run: hack/chart-pools_test.sh
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
HACK=$(cd "$(dirname "$0")" && pwd)
fail=0
check() { if [ "$2" = "$3" ]; then echo "ok   $1"; else echo "FAIL $1: got [$2] want [$3]"; fail=1; fi; }

# provider FILE LABELS...: appends a GitProvider with a runner per label.
provider() {
	local f=$1
	shift
	{
		echo "---"
		echo "apiVersion: theos.factory.io/v1beta1"
		echo "kind: GitProvider"
		echo "spec:"
		echo "  ci:"
		echo "    giteaActions:"
		echo "      runners:"
		for l in "$@"; do
			echo "      - label: $l"
			echo "        image: ghcr.io/theos/runner-$l:v1"
		done
	} >>"$f"
}
run() { "$HACK/chart-pools.sh" "$@" >"$T/out" 2>&1; }

: >"$T/two.yaml"
provider "$T/two.yaml" base go
provider "$T/two.yaml" go base
run "$T/two.yaml"
check "two providers with both labels pass" $? 0
check "a pass says so" "$(grep -c 'Every GitProvider the chart renders carries a base and a go runner pool' "$T/out")" 1

: >"$T/short.yaml"
provider "$T/short.yaml" base go
provider "$T/short.yaml" base
run "$T/two.yaml" "$T/short.yaml"
check "a provider without a go runner fails" $? 1
check "it names the file, the counts and the label" \
	"$(grep -c 'The chart (short.yaml) renders 2 GitProviders but 1 go runners' "$T/out")" 1

# #1730: no provider carries the label; grep -c counts 0 and exits 1.
: >"$T/none.yaml"
provider "$T/none.yaml" base
run "$T/none.yaml"
check "a label with no match fails (#1730)" $? 1
check "it still names the label (#1730)" \
	"$(grep -c 'The chart (none.yaml) renders 1 GitProviders but 0 go runners' "$T/out")" 1

# A render without GitProviders is a problem the caller reports: zero runners
# match zero providers here.
: >"$T/empty.yaml"
run "$T/empty.yaml"
check "no provider and no runner pass" $? 0

run
check "no file is a usage error" $? 2

[ "$fail" = 0 ] && echo "ok   hack/chart-pools.sh"
exit $fail
