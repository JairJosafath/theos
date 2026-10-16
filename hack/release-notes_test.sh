#!/usr/bin/env bash
# Tests for hack/release-notes.sh, the release notes draft: the categories and
# the left-out rules against hack/testdata/release-notes-prs.jsonl, and the
# pull requests read from a temporary git repository with a fake gh.
# Run: hack/release-notes_test.sh
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
HACK="$(cd "$(dirname "$0")" && pwd)"
NOTES=$HACK/release-notes.sh
fail=0
check() { if [ "$2" = "$3" ]; then echo "ok   $1"; else echo "FAIL $1: got [$2] want [$3]"; fail=1; fi; }
# has NAME LINE: the draft in $T/out has LINE.
has() { grep -qxF -- "$2" "$T/out"; check "$1" $? 0; }
hasnt() { grep -qF -- "$2" "$T/out"; check "$1" $? 1; }
# section NAME: the lines of section NAME in $T/out.
section() { sed -n "/^### $1\$/,/^\(###\|Images:\)/p" "$T/out" | grep '^- '; }

VERSION=1.2.0 REGISTRY=ghcr.io/o/c4factory REPO=o/c4factory \
  "$NOTES" --prs "$HACK/testdata/release-notes-prs.jsonl" v1.1.0 >"$T/out" 2>&1
check "drafts from a fixture" $? 0

check "sections in Keep a Changelog order" "$(grep '^### ' "$T/out" | tr '\n' ' ')" "### Added ### Changed ### Fixed ### Security "
check "feat: Added, without the prefix; a title with none takes the branch's type" \
  "$(section Added)" "$(printf -- '- a Brief kind (#1)\n- Briefs step 2: the planner (#2)\n- AWS: the Go Lambda Framework aws-lambda-go (#15)')"
check "fix: Fixed" "$(section Fixed)" "- the UI keeps its session (#3)"
check "breaking (! or BREAKING CHANGE) and other types: Changed" "$(section Changed)" \
  "$(printf -- '- **Breaking:** Containers name their System (#4)\n- **Breaking:** one reconcile loop (#5)\n- cache the templates (#8)')"
check "a security label or title: Security" "$(section Security)" \
  "$(printf -- '- escape the repository name (#6)\n- a security header on the UI (#7)')"
for n in 9 10 11 12; do hasnt "chore, ci, test and docs are left out (#$n)" "(#$n)"; done
hasnt "only hack/, .claude/, .relay/ and docs/dev/: left out" "(#13)"
hasnt "a security fix to hack/ only: left out" "(#14)"
has "an AWS: title on a feat/ branch: Added, the title whole (#1270)" "- AWS: the Go Lambda Framework aws-lambda-go (#15)"
hasnt "an Envtest: title on a chore/ branch: left out (#1270)" "(#16)"
hasnt "a plan touching only docs/plans/ on a feat/ branch: left out (#1270)" "(#17)"
hasnt "a plan touching the design, architecture, AGENTS.md and evidence too: left out (#1270)" "(#18)"
has "the image list ends it" 'Images: `ghcr.io/o/c4factory/<name>:1.2.0` for operator, ui, seed, the toolchains'
has "the install command names the version" "helm install c4-factory oci://ghcr.io/o/c4factory/charts/c4-factory --version 1.2.0 \\"
has "the guide link names the tag" "[docs/getting-started.md](https://github.com/o/c4factory/blob/v1.2.0/docs/getting-started.md)"

# The install snippet is the guide's install without the pull secret: the
# bash blocks of its section 1 up to the helm install's, but the registry
# login's, and the helm install's imagePullSecrets left out; compared one
# command per line, with its continuations joined.
commands() { sed -e :a -e '/\\$/N; s/ *\\\n */ /; ta' | sed 's/^ *//; s/  */ /g'; }
VERSION=1.2.0 "$NOTES" --prs "$HACK/testdata/release-notes-prs.jsonl" v1.1.0 >"$T/out" 2>&1
sed -n '/^Install/,/^\[docs/{/^```bash$/,/^```$/{/^```/d;p}}' "$T/out" | commands >"$T/snippet"
sed -n '/^## 1\. /,/^## 2\. /p' "$HACK/../docs/getting-started.md" |
  awk '/^```bash$/ {b = ""; on = 1; next} /^```$/ && on {on = 0; if (b !~ /registry login/) printf "%s", b; if (b ~ /helm install/) exit; next} on {b = b $0 "\n"}' |
  commands | sed "s/ --set '[^']*imagePullSecrets[^']*'//g; s/\"\$VERSION\"/1.2.0/" >"$T/guide"
check "the install snippet runs the guide's install, without the pull secret" \
  "$(diff "$T/guide" "$T/snippet")" ""
hasnt "  makes no project namespace by hand" "kubectl create namespace webshop"
has "  says the chart makes them" "\`gitea.projects\`, for each of which the chart makes a namespace:"

# Every install text that applies Agent Sandbox's sandbox.yaml patches its
# controller's leader election off next, as config/agent-sandbox does (#959).
patch="kubectl -n agent-sandbox-system patch deployment agent-sandbox-controller --type=json"
off='"value":"--leader-elect=false"'
grep -qx -- "      value: --leader-elect=false" "$HACK/../config/agent-sandbox/kustomization.yaml"
check "config/agent-sandbox turns leader election off" $? 0
cp "$T/out" "$T/release-notes"
for f in "$T/release-notes" docs/getting-started.md dist/chart/README.md dist/chart/templates/NOTES.txt; do
  case $f in /*) ;; *) f=$HACK/../$f ;; esac
  case $(grep -A2 -F '/sandbox.yaml' "$f") in *"$patch"*"$off"*) got=yes ;; *) got=no ;; esac
  check "the leader-election patch follows sandbox.yaml in ${f##*/}" "$got" yes
done

grep -E '"number":(9|10|11|12|13|14|16|17|18),' "$HACK/testdata/release-notes-prs.jsonl" >"$T/none.jsonl"
VERSION=1.2.0 "$NOTES" --prs "$T/none.jsonl" v1.1.0 >"$T/out" 2>&1
check "a range with nothing to list: drafts" $? 0
check "  no sections" "$(grep -c '^### ' "$T/out")" 0
has "  says nothing changed" "Nothing a device runs changed since v1.1.0."
has "  still ends with the images" 'Images: `ghcr.io/jairjosafath/c4factory/<name>:1.2.0` for operator, ui, seed, the toolchains'

# A repository with two tags and pull requests merged between them; gh knows
# #3's labels and nothing else.
mkdir -p "$T/bin" "$T/repo"
cat >"$T/bin/gh" <<'GH'
#!/usr/bin/env bash
case "$*" in *"pr view 3 "*) echo '["kind/security"]' ;; *) exit 1 ;; esac
GH
chmod +x "$T/bin/gh"
cd "$T/repo" || exit 1
git init -q -b main . && git config user.email t@t && git config user.name t
commit() { mkdir -p "$(dirname "$1")"; echo "$RANDOM" >>"$1"; git add -A && git commit -qm "$2"; }
# merge N BRANCH TITLE FILE [MESSAGE]: pull request #N from BRANCH, one commit to FILE.
merge() {
  git checkout -qb "$2" main && commit "$4" "${5:-$3}" && git checkout -q main
  git merge -q --no-ff "$2" -m "Merge pull request #$1 from o/$2" -m "$3"
}
commit README.md init && git tag v0.1.0
merge 1 feat/1-old "feat(1): before the range" api/a.go
git tag v0.2.0
merge 2 feat/2-new "feat(2): a new kind" api/b.go
merge 3 fix/3-esc "fix(3): escape names" internal/scaffold/n.go
merge 4 refactor/4-api "refactor(4): the API" api/c.go "refactor: the API

BREAKING CHANGE: fields renamed"
merge 5 feat/5-relay "feat(5): the relay" hack/relay.sh
commit direct.txt "a commit straight to main"
git tag v0.3.0
merge 6 fix/6-after "fix(6): after the tag" api/d.go

PATH="$T/bin:$PATH" "$NOTES" >"$T/out" 2>&1
check "from git, previous tag to HEAD: drafts" $? 0
has "  lists what merged after the newest tag" "- after the tag (#6)"
hasnt "  not what came before it" "(#2)"
has "  the version is a placeholder off a tag" "helm install c4-factory oci://ghcr.io/jairjosafath/c4factory/charts/c4-factory --version X.Y.Z \\"

PATH="$T/bin:$PATH" "$NOTES" v0.3.0 >"$T/out" 2>&1
check "from git, a tag to the one before it: drafts" $? 0
check "  each pull request in its section" "$(grep -E '^(###|- )' "$T/out")" "$(printf -- '### Added\n- a new kind (#2)\n### Changed\n- **Breaking:** the API (#4)\n### Security\n- escape names (#3)')"
hasnt "  not before the previous tag" "(#1)"
hasnt "  not after the tag" "(#6)"
has "  the version comes from the tag" "helm install c4-factory oci://ghcr.io/jairjosafath/c4factory/charts/c4-factory --version 0.3.0 \\"

# The Release workflow passes the previous published release as FROM: v0.2.0's
# release failed, so v0.3.0's notes run from v0.1.0.
PATH="$T/bin:$PATH" "$NOTES" v0.3.0 v0.1.0 >"$T/out" 2>&1
check "from git, FROM across a tag with no release: drafts" $? 0
has "  lists what merged before that tag" "- before the range (#1)"
has "  and after it" "- a new kind (#2)"

PATH="$T/bin:$PATH" "$NOTES" v0.3.0 "" >"$T/out" 2>&1
check "from git, FROM empty (no release before): drafts" $? 0
has "  lists the whole history" "- before the range (#1)"
has "  up to the tag" "- a new kind (#2)"
hasnt "  not after it" "(#6)"

exit $fail
