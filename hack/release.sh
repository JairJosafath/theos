#!/usr/bin/env bash
# Cuts a release in one command: the maintainer's way to tag one from a sweep,
# with no relay session (#523). The guide at a tag installs that tag's release
# (#370), so the tag's commit must name its version in two places:
# dist/chart/Chart.yaml's version, and docs/getting-started.md's VERSION and
# the release its prose names. This bumps both in a pull request of their own,
# merges it with a merge commit, and tags that commit; the pushed tag starts
# Release, which checks the same thing (--check) before it builds anything.
#
#   hack/release.sh X.Y.Z|patch|minor   bump, merge and tag vX.Y.Z (patch and
#                                       minor count up from the chart's version)
#   hack/release.sh --bump X.Y.Z        only bump the two files under ROOT
#   hack/release.sh --check vX.Y.Z      exit 1 unless the files under ROOT name X.Y.Z
#
# The release command works in a worktree of its own from origin/main, so the
# checkout it runs in is never touched. It refuses a version that isn't above
# the chart's, or whose tag origin already has. GOVERNANCE.md lets the
# maintainer run it: the bump is the only change it commits.
#
#   ROOT     the tree --bump and --check read (default the repository's root)
#   REMOTE   the remote to fetch main from and push to (default origin)
set -euo pipefail

HACK="$(cd "$(dirname "$0")" && pwd)"
ROOT=${ROOT:-$(dirname "$HACK")}
REMOTE=${REMOTE:-origin}
CHART=dist/chart/Chart.yaml
GUIDE=docs/getting-started.md

usage() {
  echo "usage: hack/release.sh X.Y.Z|patch|minor | --bump X.Y.Z | --check vX.Y.Z" >&2
  exit 2
}
semver() { [[ $1 =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; }
chart_version() { sed -n 's/^version: *//p' "$1/$CHART"; }

# check DIR VERSION: every place under DIR that names the release names VERSION.
check() {
  local dir=$1 want=$2 bad=0 got
  got=$(chart_version "$dir")
  if [ "$got" != "$want" ]; then
    echo "$CHART says version ${got:-nothing}, not $want" >&2
    bad=1
  fi
  got=$(sed -n 's/^VERSION=//p' "$dir/$GUIDE" | sort -u | tr '\n' ' ')
  if [ "$got" != "$want " ]; then
    echo "$GUIDE sets VERSION=${got:-nothing}, not $want" >&2
    bad=1
  fi
  got=$(sed -n 's/.*The commands use the release `\([^`]*\)`.*/\1/p' "$dir/$GUIDE")
  if [ "$got" != "$want" ]; then
    echo "$GUIDE says its commands use the release ${got:-nothing}, not $want" >&2
    bad=1
  fi
  return $bad
}

# bump DIR VERSION: names VERSION in DIR's chart and guide.
bump() {
  local dir=$1 to=$2
  sed -i "s/^version: .*/version: $to/" "$dir/$CHART"
  sed -i -e "s/^VERSION=.*/VERSION=$to/" \
    -e "s/The commands use the release \`[^\`]*\`/The commands use the release \`$to\`/" "$dir/$GUIDE"
  check "$dir" "$to"
}

# next FROM WHAT: the version after FROM; WHAT is patch, minor or X.Y.Z.
next() {
  local major minor patch
  IFS=. read -r major minor patch <<<"$1"
  case $2 in
  patch) echo "$major.$minor.$((patch + 1))" ;;
  minor) echo "$major.$((minor + 1)).0" ;;
  *) echo "$2" ;;
  esac
}

case ${1:-} in
--check)
  [[ ${2:-} =~ ^v([0-9]+\.[0-9]+\.[0-9]+)$ ]] || usage
  if ! check "$ROOT" "${BASH_REMATCH[1]}"; then
    echo "bump them on main (hack/release.sh ${BASH_REMATCH[1]}), then tag that commit" >&2
    exit 1
  fi
  exit 0
  ;;
--bump)
  semver "${2:-}" || usage
  bump "$ROOT" "$2"
  exit
  ;;
patch | minor) ;;
*) semver "${1:-}" || usage ;;
esac

git -C "$ROOT" fetch --quiet "$REMOTE" main --tags
WT=$(mktemp -d)
trap 'git -C "$ROOT" worktree remove --force "$WT" >/dev/null 2>&1 || true; rm -rf "$WT"' EXIT
git -C "$ROOT" worktree add --quiet --detach "$WT" "$REMOTE/main"

from=$(chart_version "$WT")
to=$(next "$from" "$1")
tag=v$to
if [ "$(printf '%s\n%s\n' "$from" "$to" | sort -V | tail -1)" != "$to" ] || [ "$from" = "$to" ]; then
  echo "$tag isn't above the chart's version, $from" >&2
  exit 1
fi
if git -C "$ROOT" ls-remote --exit-code --tags "$REMOTE" "refs/tags/$tag" >/dev/null; then
  echo "$REMOTE already has $tag" >&2
  exit 1
fi

branch=chore/release-$tag
bump "$WT" "$to"
git -C "$WT" commit --quiet -m "chore: release $tag" -- "$CHART" "$GUIDE"
git -C "$WT" push --quiet "$REMOTE" "HEAD:refs/heads/$branch"
url=$(cd "$WT" && gh pr create --base main --head "$branch" --title "chore: release $tag" \
  --body "Names $tag in \`$CHART\` and \`$GUIDE\`, so the guide at the tag installs that release (#370). Made by \`hack/release.sh\`.")
echo "opened $url"
(cd "$WT" && gh pr merge "$url" --merge)
git -C "$WT" push --quiet "$REMOTE" --delete "$branch"
sha=$(cd "$WT" && gh pr view "$url" --json mergeCommit -q .mergeCommit.oid)
if [ -z "$sha" ]; then
  echo "$url has no merge commit; nothing tagged" >&2
  exit 1
fi

git -C "$WT" fetch --quiet "$REMOTE" main
git -C "$WT" checkout --quiet --detach "$sha"
ROOT=$WT "$HACK/release.sh" --check "$tag"
git -C "$WT" push --quiet "$REMOTE" "$sha:refs/tags/$tag"
echo "tagged $sha as $tag; Release builds it"
