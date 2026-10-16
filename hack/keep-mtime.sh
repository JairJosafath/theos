#!/usr/bin/env bash
# Runs a generator, then gives each FILE it rewrote with the same bytes its old
# mtime back. Go's test cache keys a package's result on the size and mtime of
# every file in the module the test opens; controller-gen rewrites every CRD,
# the role and the DeepCopy code on each run, so without this every make test
# reran the envtest suites that read them, whatever the change.
#
#   hack/keep-mtime.sh FILE... -- COMMAND [ARG...]
#
# A FILE that didn't exist, or that COMMAND changed or removed, keeps what
# COMMAND left. Exits with COMMAND's status.
set -uo pipefail

files=()
while [ $# -gt 0 ] && [ "$1" != -- ]; do
  files+=("$1")
  shift
done
if [ $# -lt 2 ]; then
  echo "usage: hack/keep-mtime.sh FILE... -- COMMAND [ARG...]" >&2
  exit 64
fi
shift

old=$(mktemp -d) || exit 1
trap 'rm -rf "$old"' EXIT
for i in "${!files[@]}"; do
  [ -f "${files[$i]}" ] && cp -p "${files[$i]}" "$old/$i"
done

"$@"
rc=$?

for i in "${!files[@]}"; do
  f=${files[$i]}
  if [ -f "$old/$i" ] && [ -f "$f" ] && cmp -s "$old/$i" "$f"; then
    touch -r "$old/$i" "$f"
  fi
done
exit $rc
