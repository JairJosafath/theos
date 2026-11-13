#!/usr/bin/env bash
# Checks that every Go file of theos starts with the license header of
# hack/boilerplate.go.txt (#47): the copyright line of The theos Authors and
# the Apache-2.0 notice, within the file's first 20 lines (a generated file
# puts its build constraint first). Prints each file without it and exits 1.
# The Go files under templates/ become other people's applications, which
# carry the license their Project chose (docs/plans/license.md), so they are
# left alone. hack/check-license-headers_test.sh runs it in make check.
#
#   hack/check-license-headers.sh [ROOT]   the tracked *.go under ROOT (default: the repository)
set -euo pipefail

ROOT=${1:-$(cd "$(dirname "$0")/.." && pwd)}
cd "$ROOT"

fail=0
while IFS= read -r f; do
  case $f in templates/*) continue ;; esac
  top=$(head -20 "$f")
  if [[ $top != *"Copyright "[0-9][0-9][0-9][0-9]" The theos Authors."* ]] ||
    [[ $top != *'Licensed under the Apache License, Version 2.0 (the "License");'* ]]; then
    echo "$f: no license header; start it with hack/boilerplate.go.txt, YEAR as the year"
    fail=1
  fi
done < <(git ls-files -- '*.go')
exit $fail
