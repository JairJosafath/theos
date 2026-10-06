#!/usr/bin/env bash
# Tests for hack/previous-release.sh, the release before a tag, against a fake
# gh that answers repos/o/r/releases and repos/o/r/tags from files in $T.
# Run: hack/previous-release_test.sh
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
PREV="$(cd "$(dirname "$0")" && pwd)/previous-release.sh"
fail=0
check() { if [ "$2" = "$3" ]; then echo "ok   $1"; else echo "FAIL $1: got [$2] want [$3]"; fail=1; fi; }

# The fake gh; GH_FAIL=releases or tags fails that lookup.
mkdir -p "$T/bin"
cat >"$T/bin/gh" <<'GH'
#!/usr/bin/env bash
case "$*" in
  "api repos/o/r/releases --paginate") [ "$GH_FAIL" = releases ] && exit 1; cat "$T/releases" ;;
  "api repos/o/r/tags --paginate") [ "$GH_FAIL" = tags ] && exit 1; cat "$T/tags" ;;
  *) echo "gh: unexpected $*" >&2; exit 1 ;;
esac
GH
chmod +x "$T/bin/gh"
export T GH_FAIL=

# releases TAG[:draft|:pre]...: one page of the repository's releases.
releases() {
  printf '%s\n' "$@" | jq -Rn '[inputs | select(. != "") | split(":")
    | {tag_name: .[0], draft: (.[1] == "draft"), prerelease: (.[1] == "pre")}]' >"$T/releases"
}
# tags NAME...: one page of the repository's tags.
tags() { printf '%s\n' "$@" | jq -Rn '[inputs | select(. != "") | {name: .}]' >"$T/tags"; }
# prev ARG...: the script's output in $out, its exit code in $rc, its stderr in $T/err.
prev() {
  out=$(env -u REPO GITHUB_REPOSITORY=o/r PATH="$T/bin:$PATH" "$PREV" "$@" 2>"$T/err")
  rc=$?
}

# v0.4.0 is tagged, but its Release failed: there is no release for it.
releases v0.3.0 v0.2.0 v0.1.0
tags v0.5.0 v0.4.0 v0.3.0 v0.2.0 v0.1.0
prev v0.5.0
check "a tag with no release is skipped" "$out" v0.3.0
prev --skipped v0.5.0
check "  --skipped names it" "$out" v0.4.0
prev v0.4.0
check "  checking the failed tag itself: the release before it" "$out" v0.3.0
prev --skipped v0.4.0
check "  nothing skipped below it" "$out" ""

# The release being made or checked is TAG; it's usually not published yet.
releases v0.3.0 v0.2.0
tags v0.4.0 v0.3.0 v0.2.0
prev v0.4.0
check "TAG not published yet: the release before it" "$out" v0.3.0
releases v0.4.0 v0.3.0 v0.2.0
prev v0.4.0
check "TAG published: still the release before it" "$out" v0.3.0
prev v0.3.0
check "a release above TAG doesn't count" "$out" v0.2.0

releases
tags v0.1.0
prev v0.1.0
check "the first release: nothing" "$out" ""
check "  and no error" "$rc" 0
prev --skipped v0.1.0
check "  nothing skipped" "$out" ""
tags v0.1.1 v0.1.0
prev v0.1.1
check "the first release after a failed one: nothing" "$out" ""
prev --skipped v0.1.1
check "  --skipped names the failed one" "$out" v0.1.0

releases v0.6.0:pre v0.5.0:pre v0.4.0:draft v0.3.0
tags v0.6.0 v0.5.0 v0.4.0 v0.3.0
prev v0.6.0
check "drafts and prereleases are left out" "$out" v0.3.0
prev --skipped v0.6.0
check "  --skipped names them" "$out" "$(printf 'v0.4.0\nv0.5.0')"

releases v0.10.0 v0.9.0 v0.2.0 v1.0.0-rc.1 latest
prev v0.11.0
check "versions sort as versions: v0.10.0 > v0.9.0" "$out" v0.10.0
prev v0.10.0
check "  the one before v0.10.0 is v0.9.0" "$out" v0.9.0
prev v1.0.0
check "  only vX.Y.Z names are releases" "$out" v0.10.0

# gh --paginate prints the pages one after the other: [...][...].
releases v0.9.0
jq -c . "$T/releases" >"$T/page1"
releases v0.10.0
jq -c . "$T/releases" >"$T/page2"
cat "$T/page1" "$T/page2" | tr -d '\n' >"$T/releases"
prev v0.11.0
check "every page counts" "$out" v0.10.0

GH_FAIL=releases
prev v0.5.0
check "a failed lookup fails" "$rc" 1
check "  and names no release" "$out" ""
GH_FAIL=
releases v0.3.0
GH_FAIL=tags
prev --skipped v0.5.0
check "a failed tag lookup fails --skipped" "$rc" 1
GH_FAIL=
: >"$T/releases"
prev v0.5.0
check "gh saying nothing fails" "$([ "$rc" != 0 ] && echo failed)" failed
echo '{"message":"Not Found"}' >"$T/releases"
prev v0.5.0
check "gh saying something else fails" "$([ "$rc" != 0 ] && echo failed)" failed
grep -q "repos/o/r/releases: not a list" "$T/err"
check "  and says what it read" $? 0

prev
check "no TAG: usage" "$rc" 2
prev main
check "TAG not vX.Y.Z: usage" "$rc" 2

exit $fail
