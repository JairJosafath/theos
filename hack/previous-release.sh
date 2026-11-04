#!/usr/bin/env bash
# Names the release before TAG: the highest vX.Y.Z tag below TAG that has a
# published GitHub release. A tag whose release was never published (its
# Release workflow failed) is not a release, and neither is a draft or a
# prerelease: no device is told to run one. Release passes the answer to
# hack/release-notes.sh as FROM, and Verify Release upgrades from it.
#
#   hack/previous-release.sh TAG            the previous release, or nothing when there is none
#   hack/previous-release.sh --skipped TAG  the vX.Y.Z tags between it and TAG: tagged, never released
#
# It reads REPO's releases (and for --skipped its tags) with gh, so a workflow
# gives it GH_TOKEN. A failed lookup fails; it never means "no release".
#
#   REPO   owner/name (default $GITHUB_REPOSITORY, else JairJosafath/theos)
set -euo pipefail

skipped=
if [ "${1:-}" = --skipped ]; then skipped=1; shift; fi
TAG=${1:-}
if ! [[ $TAG =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "usage: hack/previous-release.sh [--skipped] vX.Y.Z (got: ${TAG:-nothing})" >&2
  exit 2
fi
REPO=${REPO:-${GITHUB_REPOSITORY:-JairJosafath/theos}}

# list PATH FILTER: the vX.Y.Z names FILTER picks from every page of
# repos/REPO/PATH, one a line. A reply that isn't pages of lists fails too.
list() {
  local pages
  pages=$(gh api "repos/$REPO/$1" --paginate) || return 1
  jq -rs --arg what "repos/$REPO/$1" '
    if length > 0 and all(.[]; type == "array") then .[][]
    else error("\($what): not a list") end
    | '"$2"' | select(test("^v[0-9]+\\.[0-9]+\\.[0-9]+$"))' <<<"$pages"
}

releases=$(list releases 'select((.draft or .prerelease) | not) | .tag_name')
prev=$(printf '%s\n' "$releases" "$TAG" | sort -uV |
  awk -v t="$TAG" 'NF == 0 { next } $0 == t { print p; exit } { p = $0 }')
if [ -z "$skipped" ]; then
  [ -z "$prev" ] || echo "$prev"
  exit 0
fi
# Every tag above prev and below TAG has no release, or it would be prev.
tags=$(list tags .name)
printf '%s\n' "$tags" "$prev" "$TAG" | sort -uV |
  awk -v lo="$prev" -v hi="$TAG" 'BEGIN { on = lo == "" } NF == 0 { next }
    $0 == hi { exit } on { print } $0 == lo { on = 1 }'
