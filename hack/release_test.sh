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

# tree DIR VERSION: the chart and guide lines release.sh reads, naming VERSION,
# a CRD, and a baseline with a CRD since removed.
tree() {
  mkdir -p "$1/dist/chart" "$1/docs" "$1/config/crd/bases" "$1/test/compat/baseline"
  echo "kind: CustomResourceDefinition # $2" >"$1/config/crd/bases/things.yaml"
  echo "kind: CustomResourceDefinition # old" >"$1/test/compat/baseline/gone.yaml"
  printf '# Versions the last release served that this tree no longer serves.\n#\n#   v1alpha1  Removed the v1alpha1 API versions.\nv1alpha1  Removed the v1alpha1 API versions.\n' >"$1/test/compat/dropped.txt"
  printf 'apiVersion: v2\nname: theos\nversion: %s\nappVersion: "dev"\n' "$2" >"$1/dist/chart/Chart.yaml"
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
check "  the CRDs become the baseline" "$(ls "$T/bump/test/compat/baseline")" things.yaml
check "  bump empties dropped.txt's entries" "$(grep -vc '^#' "$T/bump/test/compat/dropped.txt")" 0
check "  and keeps its comments" "$(grep -c '^#' "$T/bump/test/compat/dropped.txt")" 3

# #53: the quickstart installs the release it shipped with, so --check holds
# it to the tag and --bump moves it; a tree without one is as before.
tree "$T/quick" 1.2.0
mkdir -p "$T/quick/hack"
printf '#!/usr/bin/env bash\nVERSION=${THEOS_VERSION:-1.1.0}\nVERSION_OF_SOMETHING_ELSE=9\n' >"$T/quick/hack/quickstart.sh"
ROOT=$T/quick "$RELEASE" --check v1.2.0 2>"$T/err"
check "--check: a quickstart that installs another release is refused (#53)" $? 1
check "  and named" "$(grep -c 'hack/quickstart.sh installs 1.1.0, not 1.2.0' "$T/err")" 1
ROOT=$T/quick "$RELEASE" --bump 1.3.0
check "--bump: moves the quickstart with the chart (#53)" "$(sed -n 2p "$T/quick/hack/quickstart.sh")" 'VERSION=${THEOS_VERSION:-1.3.0}'
check "  and leaves its other lines" "$(sed -n 3p "$T/quick/hack/quickstart.sh")" 'VERSION_OF_SOMETHING_ELSE=9'
ROOT=$T/quick "$RELEASE" --check v1.3.0
check "  and --check agrees" $? 0

# #284: a scaffolded GitHub check falls back to the toolchain images of the
# release model.ToolchainRelease names, so --check holds it to the tag and
# --bump moves it; a tree without one is as before.
tree "$T/toolchain" 1.2.0
mkdir -p "$T/toolchain/internal/model"
printf 'package model\n\n// ToolchainRelease is the release.\nconst ToolchainRelease = "1.1.0"\n\nconst Other = "1.1.0"\n' \
  >"$T/toolchain/internal/model/ci_images.go"
ROOT=$T/toolchain "$RELEASE" --check v1.2.0 2>"$T/err"
check "--check: a ToolchainRelease that names another release is refused (#284)" $? 1
check "  and named" "$(grep -c 'internal/model/ci_images.go says ToolchainRelease 1.1.0, not 1.2.0' "$T/err")" 1
ROOT=$T/toolchain "$RELEASE" --bump 1.3.0
check "--bump: moves ToolchainRelease with the chart (#284)" "$(grep '^const ToolchainRelease' "$T/toolchain/internal/model/ci_images.go")" 'const ToolchainRelease = "1.3.0"'
check "  and leaves the file's other lines" "$(grep -c '^const Other = "1.1.0"$' "$T/toolchain/internal/model/ci_images.go")" 1
ROOT=$T/toolchain "$RELEASE" --check v1.3.0
check "  and --check agrees" $? 0

# The release command: a bare origin with main at 1.2.0, a clone, and a fake gh
# whose pr merge merges the branch into origin's main with a merge commit.
git init --quiet --bare -b main "$T/origin.git"
git init --quiet -b main "$T/src"
tree "$T/src" 1.2.0
mkdir -p "$T/src/internal/model"
printf 'package model\n\nconst ToolchainRelease = "1.2.0"\n' >"$T/src/internal/model/ci_images.go"
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
"pr create") echo https://github.com/o/theos/pull/7 ;;
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
check "  main's ToolchainRelease names it too (#284)" "$(git -C "$T/origin.git" show main:internal/model/ci_images.go | grep -c '^const ToolchainRelease = "1.2.1"$')" 1
check "  main's baseline is its CRDs" "$(git -C "$T/origin.git" ls-tree --name-only main test/compat/baseline/)" test/compat/baseline/things.yaml
check "  main's dropped.txt has no entries" "$(git -C "$T/origin.git" show main:test/compat/dropped.txt | grep -vc '^#')" 0
check "  and keeps its comments" "$(git -C "$T/origin.git" show main:test/compat/dropped.txt | grep -c '^#')" 3
check "  merged with a merge commit" "$(grep -c 'pr merge https://github.com/o/theos/pull/7 --merge' "$T/gh.log")" 1
check "  the bump branch is gone" "$(git -C "$T/origin.git" branch --list 'chore/*' | wc -l)" 0
check "  the clone's checkout is untouched" "$(sed -n 's/^version: //p' "$T/clone/dist/chart/Chart.yaml")" 1.2.0
check "  no worktree is left" "$(git -C "$T/clone" worktree list | wc -l)" 1
# Where the repository deletes merged branches itself, the delete fails because
# the branch is already gone (#580), and the release still checks and tags.
git -C "$T/origin.git" tag -d v1.2.1 >/dev/null
git -C "$T/origin.git" update-ref refs/heads/main main~1
touch "$T/autodelete"
run patch
check "patch, GitHub deletes the merged branch: still tags" $? 0
check "  the tag is origin's main" "$(git -C "$T/origin.git" rev-parse v1.2.1^{commit})" "$(git -C "$T/origin.git" rev-parse main)"
check "  it says it tagged" "$(grep -c '^tagged .* as v1.2.1' "$T/out")" 1
check "  and asks no one to delete the branch" "$(grep -c 'delete it by hand' "$T/out")" 0
# A delete origin refuses while the branch stays: the release says so and tags.
git -C "$T/origin.git" tag -d v1.2.1 >/dev/null
git -C "$T/origin.git" update-ref refs/heads/main main~1
rm "$T/autodelete"
cat >"$T/origin.git/hooks/pre-receive" <<'HOOK'
#!/usr/bin/env bash
while read -r _ new ref; do
  [ "$ref" = refs/heads/chore/release-v1.2.1 ] && [ "$new" = 0000000000000000000000000000000000000000 ] && exit 1
done
exit 0
HOOK
chmod +x "$T/origin.git/hooks/pre-receive"
run patch
check "patch, origin refuses the branch delete: still tags" $? 0
check "  the tag is origin's main" "$(git -C "$T/origin.git" rev-parse v1.2.1^{commit})" "$(git -C "$T/origin.git" rev-parse main)"
check "  it says to delete the branch by hand" "$(grep -c "couldn't delete chore/release-v1.2.1 on origin" "$T/out")" 1
rm "$T/origin.git/hooks/pre-receive"
git -C "$T/origin.git" branch --quiet -D chore/release-v1.2.1
run 1.2.1
check "the version just released is refused" $? 1
git -C "$T/origin.git" tag v1.3.0 main
run minor
check "a tag origin already has is refused" $? 1
check "  and says so" "$(grep -c 'origin already has v1.3.0' "$T/out")" 1

exit $fail
