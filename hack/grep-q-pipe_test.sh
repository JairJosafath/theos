#!/usr/bin/env bash
# Guard: no script under hack/ that runs with pipefail (and no make file, whose
# SHELL sets it) pipes a command into grep -q. grep -q exits on the first
# match; a producer still writing then gets SIGPIPE, and pipefail turns the
# match into a "no". Read the output into a variable first, or let grep read
# to the end (grep ... >/dev/null).
# Run: hack/grep-q-pipe_test.sh
HACK=$(cd "$(dirname "$0")" && pwd)
fail=0
while IFS= read -r f; do
  case $f in *.mk) ;; *) grep -q pipefail "$f" || continue ;; esac
  # A pipe, then grep with -q (alone or among other flags), --quiet or --silent.
  if hits=$(grep -nE '\|[[:space:]]*grep([[:space:]]+-[a-zA-Z]+)*[[:space:]]+(-[a-zA-Z]*q|--quiet|--silent)' "$f"); then
    echo "FAIL ${f#"$HACK"/}: pipes into grep -q under pipefail:"
    sed 's/^/    /' <<<"$hits"
    fail=1
  fi
done < <(find "$HACK" -type f \( -name '*.sh' -o -name '*.mk' \) ! -name '*_test.sh' | sort)
[ "$fail" = 0 ] && echo "ok   no hack/ script pipes into grep -q under pipefail"
exit $fail
