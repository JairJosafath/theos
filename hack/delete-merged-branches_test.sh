#!/usr/bin/env bash
# Tests for hack/delete-merged-branches.sh (#118: "no dead branches are left
# hanging, once they are merged to main, they should be deleted"), with gh
# faked. The sweep deletes a branch main contains and prints its commit, and
# keeps the default branch, a protected one, one a ruleset forbids deleting,
# the head of an open pull request and a branch ahead of main; a fork's open
# pull request doesn't keep this repository's branch of the same name. After
# a merge, the head branch of the merged pull request goes, also when a
# squash left main without its commit, and stays when it was pushed further,
# is the head of another open pull request, is protected or belongs to a
# fork; a pull request that isn't merged, or merged elsewhere than main,
# deletes nothing. A dry run deletes nothing. A branch name reaches GitHub
# percent-encoded and never the shell. What GitHub refuses or can't answer is
# kept, and the run fails unless a rule explains the refusal. And the Branch
# hygiene workflow runs the script from main only, with the token it needs
# and no more, and hands it nothing from an event but through env.
# Run: hack/delete-merged-branches_test.sh
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
ROOT=$(cd "$(dirname "$0")/.." && pwd)
SCRIPT=$ROOT/hack/delete-merged-branches.sh
fail=0
check() { if [ "$2" = "$3" ]; then echo "ok   $1"; else echo "FAIL $1: got [$2] want [$3]"; fail=1; fi; }

# The stand-in gh answers `gh api` from the files in $T/gh, as GitHub's REST
# API does, and logs each call that changes something in $T/calls:
#   default          the default branch's name
#   branches.json    the branches, one array per page
#   open.json        the open pull requests
#   pulls.json       the pull requests of a commit
#   ahead/SHA        how many commits SHA is ahead of the default branch (0
#                    when absent); "fail" makes the comparison fail
#   ruled            the branches (as in the URL) a ruleset forbids deleting
#   refused          "BRANCH CODE MESSAGE": the deletions GitHub refuses
#   down             the URL paths GitHub fails to answer (a prefix each)
mkdir -p "$T/fakes" "$T/gh/ahead"
cat >"$T/fakes/gh" <<FAKE
#!/usr/bin/env bash
G=$T/gh
[ "\$1" = api ] || { echo "gh \$*: not faked" >&2; exit 1; }
shift
method=GET jq= path=
while [ \$# -gt 0 ]; do
  case \$1 in
    -H) shift ;;
    --paginate) ;;
    --method) method=\$2; shift ;;
    --jq) jq=\$2; shift ;;
    *) path=\$1 ;;
  esac
  shift
done
out() { if [ -n "\$jq" ]; then jq -r "\$jq"; else cat; fi; }
err() { echo "{\"message\":\"\$2\",\"status\":\"\$1\"}"; echo "gh: \$2 (HTTP \$1)" >&2; exit 1; }
path=\${path#repos/o/r}
while read -r p; do
  [ -n "\$p" ] && [[ \$path == "\$p"* ]] && err 502 "Bad Gateway"
done <"\$G/down"
if [ "\$method" = DELETE ]; then
  echo "DELETE \$path" >>"$T/calls"
  while read -r b code msg; do
    [ "\$path" = "/git/refs/heads/\$b" ] && err "\$code" "\$msg"
  done <"\$G/refused"
  exit 0
fi
case \$path in
  '')
    [ -e "\$G/default" ] || err 502 "Bad Gateway"
    jq -n --arg d "\$(cat "\$G/default")" '{default_branch: \$d}' | out
    ;;
  '/pulls?state=open&per_page=100') out <"\$G/open.json" ;;
  '/branches?per_page=100') out <"\$G/branches.json" ;;
  /branches/*)
    b=\$(jq -c --arg n "\${path#/branches/}" '.[] | select((.name | split("/") | map(@uri) | join("/")) == \$n)' "\$G/branches.json")
    [ -n "\$b" ] || err 404 "Branch not found"
    out <<<"\$b"
    ;;
  /compare/*)
    sha=\${path##*...}
    n=\$(cat "\$G/ahead/\$sha" 2>/dev/null || echo 0)
    [ "\$n" = fail ] && err 404 "No common ancestor"
    echo "{\"ahead_by\":\$n}" | out
    ;;
  /commits/*/pulls*) out <"\$G/pulls.json" ;;
  /rules/branches/*)
    if grep -qxF -- "\${path#/rules/branches/}" "\$G/ruled"; then
      echo '[{"type":"deletion"},{"type":"non_fast_forward"}]' | out
    else
      echo '[{"type":"pull_request"}]' | out
    fi
    ;;
  *) err 404 "Not Found" ;;
esac
FAKE
chmod +x "$T/fakes/gh"

# branch NAME SHA [PROTECTED]: one branch as GitHub lists it.
branch() { jq -cn --arg n "$1" --arg s "$2" --argjson p "${3:-false}" '{name: $n, commit: {sha: $s}, protected: $p}'; }
# pull NUMBER HEAD_REF HEAD_SHA [REPO [BASE [MERGED]]]: one pull request.
pull() {
  jq -cn --argjson n "$1" --arg ref "$2" --arg sha "$3" --arg repo "${4-o/r}" --arg base "${5:-main}" --arg merged "${6:-yes}" \
    '{number: $n, merged_at: (if $merged == "yes" then "2026-11-10T09:00:00Z" else null end), base: {ref: $base},
      head: {ref: $ref, sha: $sha, repo: (if $repo == "" then null else {full_name: $repo} end)}}'
}
sha() { printf '%040d' "$1"; }
# world: GitHub as every case starts from it: main, no pull request, no rule.
world() {
  rm -rf "$T/gh" && mkdir -p "$T/gh/ahead"
  echo main >"$T/gh/default"
  echo '[]' >"$T/gh/open.json"
  echo '[]' >"$T/gh/pulls.json"
  : >"$T/gh/ruled"
  : >"$T/gh/refused"
  : >"$T/gh/down"
  branch main "$(sha 1)" | jq -s . >"$T/gh/branches.json"
}
# branches BRANCH...: the repository's branches, main first, two to a page.
# The pages are cut with a slice: jq 1.8, the hosted runner's, dropped the
# private _nwise that older ones had.
branches() {
  { branch main "$(sha 1)" && cat; } |
    jq -c -n '[inputs] | range(0; length; 2) as $i | .[$i:$i + 2]' >"$T/gh/branches.json"
}
# run ARGS...: the script against the stand-in; its output in $T/out, the
# calls that changed something in $T/calls.
run() {
  : >"$T/calls"
  (cd "$T" && env PATH="$T/fakes:$PATH" THEOS_GITHUB_REPO=o/r DRY_RUN="${DRY_RUN:-}" "$SCRIPT" "$@" >"$T/out" 2>&1)
}
deleted() { sed -n 's/^DELETE //p' "$T/calls" | tr '\n' ' ' | sed 's/ $//'; }

# The sweep, the weekly and the manual run.
world
{
  branch claude/done "$(sha 2)"
  branch release "$(sha 3)" true
  branch claude/ahead "$(sha 4)"
  branch claude/open "$(sha 5)"
  branch patch-1 "$(sha 6)"
  branch keep/ruled "$(sha 7)"
} | branches
echo 2 >"$T/gh/ahead/$(sha 4)"
{ pull 7 claude/open "$(sha 5)" && pull 8 patch-1 "$(sha 6)" stranger/r; } | jq -s . >"$T/gh/open.json"
echo keep/ruled >"$T/gh/ruled"
check "the stand-in lists the seven branches two to a page, on any jq (#118)" \
  "$(jq -c 'map(.name)' "$T/gh/branches.json" | tr '\n' ' ')" \
  '["main","claude/done"] ["release","claude/ahead"] ["claude/open","patch-1"] ["keep/ruled"] '
run sweep
check "a sweep exits 0" "$?" 0
check "a sweep deletes the branches main contains, and only them (#118)" "$(deleted)" \
  "/git/refs/heads/claude/done /git/refs/heads/patch-1"
check "it prints the deleted branch with its commit and how to restore it" \
  "$(grep -c "^deleted claude/done $(sha 2) (restore: git push origin $(sha 2):refs/heads/claude/done)$" "$T/out")" 1
check "the default branch is kept" "$(grep -c "^kept main $(sha 1): the default branch$" "$T/out")" 1
check "a protected branch is kept" "$(grep -c "^kept release $(sha 3): a protected branch$" "$T/out")" 1
check "a branch ahead of main is kept, with the count" \
  "$(grep -c "^kept claude/ahead $(sha 4): 2 commit(s) main doesn't have$" "$T/out")" 1
check "the head of an open pull request is kept, with its number" \
  "$(grep -c "^kept claude/open $(sha 5): the head of the open pull request #7$" "$T/out")" 1
check "a branch a ruleset forbids deleting is kept" \
  "$(grep -c "^kept keep/ruled $(sha 7): a ruleset forbids deleting it$" "$T/out")" 1
check "a fork's open pull request doesn't keep this repository's branch of that name" \
  "$(grep -c "^deleted patch-1 " "$T/out")" 1

DRY_RUN=true run sweep
check "a dry run exits 0" "$?" 0
check "a dry run deletes nothing" "$(deleted)" ""
check "a dry run says what it would delete" "$(grep '^would delete ' "$T/out" | cut -d' ' -f3 | tr '\n' ' ')" \
  "claude/done patch-1 "
check "a dry run still says what it keeps" "$(grep -c '^kept ' "$T/out")" 5

# A branch is named by whoever pushed it.
world
# shellcheck disable=SC2016 # the name is GitHub's to hand over, not the shell's to expand
evil='x/$(touch${IFS}pwned);100%'
branch "$evil" "$(sha 2)" | branches
run sweep
check "a branch name reaches GitHub percent-encoded" "$(deleted)" \
  '/git/refs/heads/x/%24%28touch%24%7BIFS%7Dpwned%29%3B100%25'
check "and is never run" "$([ -e "$T/pwned" ] && echo run)" ""

# What GitHub refuses or can't answer.
world
{ branch a "$(sha 2)" && branch b "$(sha 3)" && branch c "$(sha 4)"; } | branches
echo 'a 422 Repository rule violations found' >"$T/gh/refused"
run sweep
check "a deletion GitHub refuses for a rule (422) keeps the branch and doesn't fail the run" "$?" 0
check "it says GitHub refused" "$(grep -c "^kept a $(sha 2): GitHub refuses to delete it: gh: Repository rule violations found (HTTP 422)$" "$T/out")" 1
check "and goes on with the others" "$(grep -c '^deleted [bc] ' "$T/out")" 2
echo 'a 422 Reference does not exist' >"$T/gh/refused"
run sweep
check "a branch that went in between is no error" "$?" 0
check "it says the branch is gone" "$(grep -c '^gone a: already deleted$' "$T/out")" 1
echo 'a 403 Resource not accessible by integration' >"$T/gh/refused"
run sweep
check "a deletion refused without a rule fails the run" "$?" 1
check "it names the branch and GitHub's answer" \
  "$(grep -c "^error a $(sha 2): couldn't delete it: gh: Resource not accessible by integration (HTTP 403)$" "$T/out")" 1
check "and still deletes the others" "$(grep -c '^deleted [bc] ' "$T/out")" 2
: >"$T/gh/refused"
echo fail >"$T/gh/ahead/$(sha 3)"
run sweep
check "a branch GitHub can't compare with main fails the run" "$?" 1
check "and is kept" "$(deleted)" "/git/refs/heads/a /git/refs/heads/c"
rm "$T/gh/ahead/$(sha 3)"
echo /rules/branches/b >"$T/gh/down"
run sweep
check "a branch whose rules GitHub can't show fails the run" "$?" 1
check "and is kept too" "$(deleted)" "/git/refs/heads/a /git/refs/heads/c"
echo '/pulls?state=open' >"$T/gh/down"
run sweep
check "without the open pull requests the run fails" "$?" 1
check "and deletes nothing" "$(deleted)" ""
echo '/branches?' >"$T/gh/down"
run sweep
check "without the branches the run fails" "$?" 1

# After a merge to main: what a push to main runs.
world
{
  branch feat/merged "$(sha 2)"
  branch feat/squashed "$(sha 3)"
  branch feat/pushed-on "$(sha 40)"
  branch feat/behind "$(sha 50)"
  branch feat/reused "$(sha 6)"
  branch release "$(sha 7)" true
  branch patch-1 "$(sha 8)"
  branch feat/unmerged "$(sha 9)"
  branch feat/other-base "$(sha 10)"
  branch bystander "$(sha 11)"
} | branches
echo 3 >"$T/gh/ahead/$(sha 3)"  # a squash: main has its changes, not its commit
echo 1 >"$T/gh/ahead/$(sha 40)" # a commit pushed after the merge
{
  pull 20 feat/merged "$(sha 2)"
  pull 21 feat/squashed "$(sha 3)"
  pull 22 feat/pushed-on "$(sha 4)"
  pull 23 feat/behind "$(sha 5)"
  pull 24 feat/reused "$(sha 6)"
  pull 25 release "$(sha 7)"
  pull 26 patch-1 "$(sha 8)" stranger/r
  pull 27 feat/unmerged "$(sha 9)" o/r main no
  pull 28 feat/other-base "$(sha 10)" o/r release-1
  pull 29 feat/gone "$(sha 12)"
  pull 30 feat/fork-gone "$(sha 13)" ""
} | jq -s . >"$T/gh/pulls.json"
pull 31 feat/reused "$(sha 6)" | jq -s . >"$T/gh/open.json"
run merged "$(sha 99)"
check "a merge exits 0" "$?" 0
check "a merge deletes the merged head branches, and only them (#118)" "$(deleted)" \
  "/git/refs/heads/feat/merged /git/refs/heads/feat/squashed /git/refs/heads/feat/behind"
check "a squashed head still at its merged commit is deleted with that commit" \
  "$(grep -c "^deleted feat/squashed $(sha 3) " "$T/out")" 1
check "a head moved to a commit main contains is deleted" "$(grep -c "^deleted feat/behind $(sha 50) " "$T/out")" 1
check "a head pushed further after the merge is kept" \
  "$(grep -c "^kept feat/pushed-on $(sha 40): 1 commit(s) main doesn't have, pushed after the merge of 000000000000$" "$T/out")" 1
check "a head that another open pull request uses is kept" \
  "$(grep -c "^kept feat/reused $(sha 6): the head of the open pull request #31$" "$T/out")" 1
check "a protected head is kept" "$(grep -c "^kept release $(sha 7): a protected branch$" "$T/out")" 1
check "a fork's head is kept, and this repository's branch of that name with it" \
  "$(grep -c "^kept patch-1: pull request #26 came from a fork (stranger/r)" "$T/out")" 1
check "a head whose fork is gone is kept" "$(grep -c "^kept feat/fork-gone: pull request #30 came from a fork (a deleted one)" "$T/out")" 1
check "a pull request that isn't merged deletes nothing" "$(grep -c 'feat/unmerged' "$T/out")" 0
check "a pull request merged to another branch deletes nothing" "$(grep -c 'feat/other-base' "$T/out")" 0
check "a head already deleted is no error" "$(grep -c '^gone feat/gone: already deleted (pull request #29)$' "$T/out")" 1
check "a branch no pull request names is left alone" "$(grep -c bystander "$T/out")" 0

DRY_RUN=true run merged "$(sha 99)"
check "a dry run of a merge deletes nothing" "$(deleted)" ""
check "and says what it would delete" "$(grep -c '^would delete feat/' "$T/out")" 3

echo '[]' >"$T/gh/pulls.json"
run merged "$(sha 99)"
check "a push with no pull request exits 0" "$?" 0
check "and says so" "$(grep -c "^no merged pull request brought $(sha 99) to main$" "$T/out")" 1
check "and deletes nothing" "$(deleted)" ""
echo "/commits/$(sha 99)/pulls" >"$T/gh/down"
run merged "$(sha 99)"
check "a commit whose pull requests GitHub can't list fails the run" "$?" 1
: >"$T/gh/down"

run merged 'main; rm -rf /'
check "merged refuses what is no commit id" "$?" 2
run
check "no mode is a usage error" "$?" 2
rm "$T/gh/default"
run sweep
check "without the default branch the run fails" "$?" 1

# The workflow that runs it (.github/workflows/branch-hygiene.yml).
WF=$ROOT/.github/workflows/branch-hygiene.yml
check "Branch hygiene runs on pushes to main" "$(grep -A1 '^  push:$' "$WF" | grep -c '^    branches: \[main\]$')" 1
check "and weekly" "$(grep -A1 '^  schedule:$' "$WF" | grep -c "^    - cron: '[0-9]* [0-9]* \* \* [0-6]'$")" 1
check "and by hand, with a dry_run input" "$(grep -A3 '^  workflow_dispatch:$' "$WF" | grep -c '^      dry_run:$')" 1
check "it never runs on a pull request's event, so never a pull request's code" \
  "$(grep -c '^  pull_request' "$WF")" 0
check "the workflow grants nothing by default" "$(grep -c '^permissions: {}$' "$WF")" 1
check "its job may write contents and read pull requests, and no more" \
  "$(sed -n '/^    permissions:$/,/^    [a-z]/p' "$WF" | grep '^      ' | sed 's/ *#.*//' | tr -s ' ' | tr '\n' ';')" \
  " contents: write; pull-requests: read;"
check "the job runs on the default branch only, so the script is main's" \
  "$(grep -c "^    if: github.event_name == 'schedule' || github.ref_name == github.event.repository.default_branch$" "$WF")" 1
check "the clone keeps no credentials" "$(grep -c '^          persist-credentials: false$' "$WF")" 1
check "every action is pinned to a commit" "$(grep '^ *uses: ' "$WF" | grep -vc '@[0-9a-f]\{40\} # v')" 0
check "no run: step holds a \${{ }} expression: the event reaches the script through env" \
  "$(awk '/^ +run: / || inrun { if (/\$\{\{/) n++ } /^ +run: \|/ { inrun = 1; next } inrun && /^ +[a-z-]+: / { inrun = 0 } END { print n + 0 }' "$WF")" 0
# shellcheck disable=SC2016 # the workflow's text
check "a push runs the merged mode with the pushed commit" \
  "$(grep -c 'hack/delete-merged-branches.sh merged "\$SHA"' "$WF")" 1
check "a schedule or a manual run sweeps" "$(grep -c 'hack/delete-merged-branches.sh sweep$' "$WF")" 1

exit $fail
