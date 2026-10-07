#!/usr/bin/env bash
# Tests for hack/release.sh: --check refuses a tag the chart or the guide
# doesn't name, which is what Release runs before it builds; --bump names a
# version in both; and the release command bumps, merges and tags against a
# bare origin and a fake gh that merges like GitHub does.
# Run: hack/release_test.sh
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
HACK="$(cd "$(dirname "$0")" && pwd)"
RELEASE=$HACK/release.sh
fail=0
check() { if [ "$2" = "$3" ]; then echo "ok   $1"; else echo "FAIL $1: got [$2] want [$3]"; fail=1; fi; }

# tree DIR VERSION: the chart and guide lines release.sh reads, naming VERSION.
tree() {
  mkdir -p "$1/dist/chart" "$1/docs"
  printf 'apiVersion: v2\nname: c4-factory\nversion: %s\nappVersion: "dev"\n' "$2" >"$1/dist/chart/Chart.yaml"
  printf 'The commands use the release `%s`; for a later one, change `VERSION`.\n\n```bash\nVERSION=%s\n```\n\nVERSION=%s\n' \
    "$2" "$2" "$2" >"$1/docs/getting-started.md"
}

tree "$T/ok" 1.2.0
ROOT=$T/ok "$RELEASE" --check v1.2.0 2>"$T/err"
check "--check: a tag the chart and the guide name passes" $? 0
ROOT=$T/ok "$RELEASE" --check v1.2.1 2>"$T/err"
check "--check: a tag neither names is refused" $? 1
check "  the chart's version is named" "$(grep -c 'Chart.yaml says version 1.2.0, not 1.2.1' "$T/err")" 1
check "  it says how to fix it" "$(grep -c 'hack/release.sh 1.2.1' "$T/err")" 1

tree "$T/guide" 1.2.1
sed -i '0,/^VERSION=.*/s//VERSION=1.2.0/' "$T/guide/docs/getting-started.md"
ROOT=$T/guide "$RELEASE" --check v1.2.1 2>"$T/err"
check "--check: the chart names it, one guide VERSION doesn't: refused" $? 1
check "  the guide's VERSION is named" "$(grep -c 'sets VERSION=1.2.0 1.2.1' "$T/err")" 1

tree "$T/prose" 1.2.1
sed -i 's/release `1.2.1`/release `1.2.0`/' "$T/prose/docs/getting-started.md"
ROOT=$T/prose "$RELEASE" --check v1.2.1 2>"$T/err"
check "--check: the guide's prose names another release: refused" $? 1

ROOT=$T/ok "$RELEASE" --check 1.2.0 2>/dev/null
check "--check: a tag without its v is a usage error" $? 2

cp -r "$T/ok" "$T/bump"
ROOT=$T/bump "$RELEASE" --bump 1.3.0
check "--bump: names the version" $? 0
ROOT=$T/bump "$RELEASE" --check v1.3.0
check "  and --check agrees" $? 0
check "  the rest of the chart is kept" "$(grep -c 'appVersion: "dev"' "$T/bump/dist/chart/Chart.yaml")" 1

# The release command: a bare origin with main at 1.2.0, a clone, and a fake gh
# whose pr merge merges the branch into origin's main with a merge commit.
git init --quiet --bare -b main "$T/origin.git"
git init --quiet -b main "$T/src"
tree "$T/src" 1.2.0
git -C "$T/src" add -A
git -C "$T/src" -c user.name=t -c user.email=t@t commit --quiet -m init
git -C "$T/src" push --quiet "$T/origin.git" main
git clone --quiet "$T/origin.git" "$T/clone"
cp "$RELEASE" "$T/clone/release.sh"
mkdir "$T/bin"
cat >"$T/bin/gh" <<EOF
#!/usr/bin/env bash
echo "gh \$*" >>"$T/gh.log"
case "\$1 \$2" in
"pr create") echo https://github.com/o/c4factory/pull/7 ;;
"pr merge")
  rm -rf "$T/merge"
  git clone --quiet "$T/origin.git" "$T/merge"
  git -C "$T/merge" -c user.name=t -c user.email=t@t merge --quiet --no-ff -m merged origin/chore/release-v1.2.1
  git -C "$T/merge" push --quiet origin main
  if [ -e "$T/autodelete" ]; then git -C "$T/origin.git" branch --quiet -D chore/release-v1.2.1; fi
  ;;
"pr view") git -C "$T/origin.git" rev-parse main ;;
esac
EOF
chmod +x "$T/bin/gh"
export GIT_AUTHOR_NAME=t GIT_AUTHOR_EMAIL=t@t GIT_COMMITTER_NAME=t GIT_COMMITTER_EMAIL=t@t

run() { (cd "$T/clone" && PATH="$T/bin:$PATH" ROOT=$T/clone ./release.sh "$@") >"$T/out" 2>&1; }
run 1.2.0
check "a version that isn't above the chart's is refused" $? 1
run patch
check "patch: bumps, merges and tags" $? 0
check "  the tag is origin's main, the merge commit" "$(git -C "$T/origin.git" rev-parse v1.2.1^{commit})" "$(git -C "$T/origin.git" rev-parse main)"
check "  main names the release" "$(git -C "$T/origin.git" show main:dist/chart/Chart.yaml | grep -c '^version: 1.2.1$')" 1
check "  merged with a merge commit" "$(grep -c 'pr merge https://github.com/o/c4factory/pull/7 --merge' "$T/gh.log")" 1
check "  the bump branch is gone" "$(git -C "$T/origin.git" branch --list 'chore/*' | wc -l)" 0
check "  the clone's checkout is untouched" "$(sed -n 's/^version: //p' "$T/clone/dist/chart/Chart.yaml")" 1.2.0
check "  no worktree is left" "$(git -C "$T/clone" worktree list | wc -l)" 1
# Where the repository deletes merged branches itself, the release still tags.
git -C "$T/origin.git" tag -d v1.2.1 >/dev/null
git -C "$T/origin.git" update-ref refs/heads/main main~1
touch "$T/autodelete"
run patch
check "patch, GitHub deletes the merged branch: still tags" $? 0
check "  the tag is origin's main" "$(git -C "$T/origin.git" rev-parse v1.2.1^{commit})" "$(git -C "$T/origin.git" rev-parse main)"
run 1.2.1
check "the version just released is refused" $? 1
git -C "$T/origin.git" tag v1.3.0 main
run minor
check "a tag origin already has is refused" $? 1
check "  and says so" "$(grep -c 'origin already has v1.3.0' "$T/out")" 1

exit $fail
