#!/usr/bin/env bash
# Tests for hack/delete-merged-branches.sh (#118: "no dead branches are left
# hanging, once they are merged to main, they should be deleted"), with gh
# faked and git real, against a bare repository that stands in for GitHub's.
# The sweep deletes a branch main contains and prints its commit, and keeps
# the default branch, a protected one, one a ruleset forbids deleting, one
# the keep-list names, the head and the base of an open pull request and a
# branch ahead of main; a fork's open pull request doesn't keep this
# repository's branch of the same name. It reads every page of the branches,
# the open pull requests and a branch's rules. After a merge, the head branch
# of the merged pull request goes, also when a squash left main without its
# commit, and stays when it was pushed further, is the head or the base of
# another open pull request, is protected or belongs to a fork; a pull
# request that isn't merged, or merged elsewhere than main, deletes nothing.
# A branch pushed to after the run read it is kept, with its new commit, and
# no other ref whose name ends in the branch's is deleted in its place. A
# dry run deletes nothing, and only DRY_RUN=false or none deletes. A branch
# name reaches GitHub percent-encoded, git as one word, and the restore line
# quoted for a shell. The token is in no argument, no URL and no output, and
# no credential helper of the runner's sees it. What GitHub refuses or can't
# answer is kept, and the run fails unless a rejection explains it; a branch
# with no common ancestor with main is kept without failing it. The
# keep-list splits on CRLF line ends too, and the run prints the patterns it
# read. And the Branch hygiene
# workflow runs the script from main only, with the token it needs and no
# more, and hands it nothing from an event or a variable but through env.
# Run: hack/delete-merged-branches_test.sh
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
ROOT=$(cd "$(dirname "$0")/.." && pwd)
SCRIPT=$ROOT/hack/delete-merged-branches.sh
fail=0
check() { if [ "$2" = "$3" ]; then echo "ok   $1"; else echo "FAIL $1: got [$2] want [$3]"; fail=1; fi; }

# The stand-in for GitHub's repository is a bare one, $REMOTE: the script
# deletes from it with the real git. Its commits are real and empty, one per
# number, the same on every run. Its update hook refuses what $T/gh/refused
# names, as a rule of GitHub's would.
REMOTE=$T/remote.git
TOKEN=ghs_stand-in-token-0123456789
GIT=$(command -v git)
git init -q --bare "$REMOTE"
EMPTY=$(git -C "$REMOTE" hash-object -w -t tree /dev/null)
git -C "$REMOTE" remote add origin "$REMOTE"
sed "s|@T@|$T|g" >"$REMOTE/hooks/update" <<'HOOK'
#!/bin/sh
while read -r b msg; do
  [ "$1" = "refs/heads/$b" ] && { echo "$msg" >&2; exit 1; }
done <@T@/gh/refused
exit 0
HOOK
chmod +x "$REMOTE/hooks/update"
# sha N: the commit numbered N.
sha() {
  GIT_AUTHOR_NAME=t GIT_AUTHOR_EMAIL=t@example.invalid GIT_AUTHOR_DATE=2026-11-10T00:00:00Z \
    GIT_COMMITTER_NAME=t GIT_COMMITTER_EMAIL=t@example.invalid GIT_COMMITTER_DATE=2026-11-10T00:00:00Z \
    git -C "$REMOTE" commit-tree -m "$1" "$EMPTY"
}

# The stand-in gh answers `gh api` from the files in $T/gh, as GitHub's REST
# API does: a list one page at a time, and every page only with --paginate.
# It logs each path it is asked in $T/paths and each call that changes
# something in $T/calls; a DELETE deletes the branch by its name, whatever
# commit it is at.
#   default          the default branch's name
#   branches.json    the branches, one array per page
#   open.json        the open pull requests, one array per page
#   pulls.json       the pull requests of a commit
#   ahead/SHA        how many commits SHA is ahead of the default branch (0
#                    when absent), as JSON, so null is no count; "fail" makes
#                    the comparison fail, and "unrelated" is GitHub's answer
#                    for a commit with no common ancestor with that branch
#   ruled            the branches (as in the URL) a ruleset forbids deleting:
#                    the deletion rule is on the second page of their rules
#   refused          "BRANCH MESSAGE": the deletions the repository rejects
#   moved            "BRANCH SHA": the commit a branch moved to after the
#                    branches were listed; "BRANCH -": it was deleted
#   down             the URL paths GitHub fails to answer (a prefix each)
#   unguarded        when it exists, git push loses the leases that expect no
#                    ref, its guards: a git that deletes another ref
mkdir -p "$T/fakes" "$T/gh/ahead"
sed "s|@T@|$T|g" >"$T/fakes/gh" <<'FAKE'
#!/usr/bin/env bash
T=@T@
G=$T/gh
[ "$1" = api ] || { echo "gh $*: not faked" >&2; exit 1; }
shift
method=GET jq= path= paginate=
while [ $# -gt 0 ]; do
  case $1 in
    -H) shift ;;
    --paginate) paginate=1 ;;
    --method) method=$2; shift ;;
    --jq) jq=$2; shift ;;
    *) path=$1 ;;
  esac
  shift
done
out() { if [ -n "$jq" ]; then jq -r "$jq"; else cat; fi; }
# pages FILE: every page with --paginate, the first one without.
pages() { if [ -n "$paginate" ]; then cat "$1"; else jq -c -n 'input' "$1"; fi; }
err() { echo "{\"message\":\"$2\",\"status\":\"$1\"}"; echo "gh: $2 (HTTP $1)" >&2; exit 1; }
path=${path#repos/o/r}
echo "$path" >>"$T/paths"
while read -r p; do
  [ -n "$p" ] && [[ $path == "$p"* ]] && err 502 "Bad Gateway"
done <"$G/down"
if [ "$method" = DELETE ]; then
  echo "DELETE $path" >>"$T/calls"
  name=${path#/git/refs/heads/}
  git -C "$T/remote.git" update-ref -d "refs/heads/$(printf '%b' "${name//%/\\x}")"
  exit 0
fi
case $path in
  '')
    [ -e "$G/default" ] || err 502 "Bad Gateway"
    jq -n --arg d "$(cat "$G/default")" '{default_branch: $d}' | out
    ;;
  '/pulls?state=open&per_page=100') pages "$G/open.json" | out ;;
  '/branches?per_page=100') pages "$G/branches.json" | out ;;
  /branches/*)
    b=$(jq -c --arg n "${path#/branches/}" '.[] | select((.name | split("/") | map(@uri) | join("/")) == $n)' "$G/branches.json")
    [ -n "$b" ] || err 404 "Branch not found"
    out <<<"$b"
    ;;
  /compare/*)
    sha=${path##*...}
    n=$(cat "$G/ahead/$sha" 2>/dev/null || echo 0)
    [ "$n" = fail ] && err 404 "Not Found"
    [ "$n" = unrelated ] && err 404 "No common ancestor between $(cat "$G/default") and $sha."
    echo "{\"ahead_by\":$n}" | out
    ;;
  /commits/*/pulls*) out <"$G/pulls.json" ;;
  /rules/branches/*'?per_page=100')
    b=${path#/rules/branches/}
    echo '[{"type":"pull_request"}]' | out
    if [ -n "$paginate" ] && grep -qxF -- "${b%\?per_page=100}" "$G/ruled"; then
      echo '[{"type":"deletion"},{"type":"non_fast_forward"}]' | out
    fi
    ;;
  *) err 404 "Not Found" ;;
esac
FAKE
# The stand-in git is the real one, and notes what it was run with: every
# argument in $T/git-args, and in $T/git-cred what the credential settings of
# a push would hand to github.com; it then tells those settings the push
# succeeded, as git does, which is when a helper that stores is handed the
# password. The runs' own global configuration, $T/gitconfig, has such a
# helper, as a runner may: it writes all it is asked and told to
# $T/helper-spy. That configuration also sends https://github.com/o/r.git,
# and the same on ghe.example.invalid, to the stand-in repository, and the
# runs allow git no transport but a local path: no case reaches the network.
sed -e "s|@T@|$T|g" -e "s|@GIT@|$GIT|g" >"$T/fakes/git" <<'FAKE'
#!/usr/bin/env bash
printf '%s\n' "$*" >>@T@/git-args
args=("$@") cfg=() push=
for ((i = 0; i < $#; i++)); do
  case ${args[i]} in
    -c) cfg+=(-c "${args[++i]}") ;;
    push) push=1 && break ;;
  esac
done
if [ -n "$push" ]; then
  cred=$(printf 'protocol=https\nhost=github.com\n\n' | @GIT@ "${cfg[@]}" credential fill 2>&1)
  printf '%s\n' "$cred" >>@T@/git-cred
  printf '%s\n\n' "$cred" | @GIT@ "${cfg[@]}" credential approve >/dev/null 2>&1
  if [ -e @T@/gh/unguarded ]; then
    for a in "$@"; do
      shift
      [[ $a == --force-with-lease=*: ]] || set -- "$@" "$a"
    done
  fi
fi
exec @GIT@ "$@"
FAKE
chmod +x "$T/fakes/gh" "$T/fakes/git"
git config --file "$T/gitconfig" credential.helper "!f() { { echo \"\$1\" && cat; } >>'$T/helper-spy'; }; f"
git config --file "$T/gitconfig" --add "url.$REMOTE.insteadOf" https://github.com/o/r.git
git config --file "$T/gitconfig" --add "url.$REMOTE.insteadOf" https://ghe.example.invalid/o/r.git

# branch NAME SHA [PROTECTED]: one branch as GitHub lists it.
branch() { jq -cn --arg n "$1" --arg s "$2" --argjson p "${3:-false}" '{name: $n, commit: {sha: $s}, protected: $p}'; }
# pull NUMBER HEAD_REF HEAD_SHA [REPO [BASE [MERGED]]]: one pull request, of a
# fork when REPO isn't o/r, to the branch BASE of this repository.
pull() {
  jq -cn --argjson n "$1" --arg ref "$2" --arg sha "$3" --arg repo "${4-o/r}" --arg base "${5:-main}" --arg merged "${6:-yes}" \
    '{number: $n, merged_at: (if $merged == "yes" then "2026-11-10T09:00:00Z" else null end), base: {ref: $base},
      head: {ref: $ref, sha: $sha, repo: (if $repo == "" then null else {full_name: $repo} end)}}'
}
# world: GitHub as every case starts from it: main, no pull request, no rule.
world() {
  rm -rf "$T/gh" && mkdir -p "$T/gh/ahead"
  echo main >"$T/gh/default"
  echo '[]' >"$T/gh/open.json"
  echo '[]' >"$T/gh/pulls.json"
  : >"$T/gh/ruled"
  : >"$T/gh/refused"
  : >"$T/gh/down"
  : >"$T/gh/moved"
  branch main "$(sha 1)" | jq -s . >"$T/gh/branches.json"
}
# paged: the lines read, as arrays of two: the pages of a list. They are cut
# with a slice: jq 1.8, the hosted runner's, dropped the private _nwise that
# older ones had.
paged() { jq -c -n '[inputs] | range(0; length; 2) as $i | .[$i:$i + 2]'; }
# branches BRANCH...: the repository's branches, main first, two to a page.
branches() { { branch main "$(sha 1)" && cat; } | paged >"$T/gh/branches.json"; }
# open PULL...: the open pull requests, two to a page.
open() { paged >"$T/gh/open.json"; }
# run ARGS...: the script against the stand-ins, from a directory that is no
# clone; its output in $T/out, the calls that changed something in $T/calls.
# The repository first gets the branches GitHub lists, and then what moved
# since: the push that lands while the script decides. DRY_RUN is empty, as
# on the workflow's schedule, unless the case sets it; DRY_RUN=unset and
# REMOTE_URL=unset leave the script without DRY_RUN and THEOS_GIT_REMOTE,
# and SERVER_URL is its GITHUB_SERVER_URL, which it otherwise doesn't have.
run() {
  local s n vars=()
  [ "${DRY_RUN:-}" = unset ] || vars+=(DRY_RUN="${DRY_RUN:-}")
  [ "${REMOTE_URL:-}" = unset ] || vars+=(THEOS_GIT_REMOTE="${REMOTE_URL:-$REMOTE}")
  [ -z "${SERVER_URL:-}" ] || vars+=(GITHUB_SERVER_URL="$SERVER_URL")
  : >"$T/calls"
  : >"$T/paths"
  : >"$T/git-args"
  : >"$T/git-cred"
  : >"$T/helper-spy"
  git -C "$REMOTE" for-each-ref --format='delete %(refname)' refs/heads | git -C "$REMOTE" update-ref --stdin
  jq -r '.[] | [.commit.sha, .name] | @tsv' "$T/gh/branches.json" | while IFS=$'\t' read -r s n; do
    git -C "$REMOTE" update-ref "refs/heads/$n" "$s"
  done
  while read -r n s; do
    case $s in
      '') ;;
      -) git -C "$REMOTE" update-ref -d "refs/heads/$n" ;;
      *) git -C "$REMOTE" update-ref "refs/heads/$n" "$s" ;;
    esac
  done <"$T/gh/moved"
  (cd "$T" && env -u DRY_RUN -u THEOS_GIT_REMOTE -u GITHUB_SERVER_URL PATH="$T/fakes:$PATH" GH_TOKEN="$TOKEN" \
    GIT_CONFIG_GLOBAL="$T/gitconfig" GIT_ALLOW_PROTOCOL=file THEOS_GITHUB_REPO=o/r THEOS_KEEP_BRANCHES="${KEEP:-}" \
    "${vars[@]}" "$SCRIPT" "$@" >"$T/out" 2>&1)
}
# deleted: the listed branches the repository no longer has.
deleted() {
  jq -r '.[].name' "$T/gh/branches.json" | while IFS= read -r n; do
    git -C "$REMOTE" show-ref --verify -q "refs/heads/$n" || printf '%s ' "$n"
  done | sed 's/ $//'
}
# at BRANCH: the commit the repository has the branch at.
at() { git -C "$REMOTE" rev-parse -q --verify "refs/heads/$1"; }
# step TEXT: the step of the workflow that holds TEXT, from its "- name:",
# without the comments.
step() {
  awk -v want="$1" '/^ *#/ { next } /^      - / { if (hit) exit; block = "" } { block = block $0 "\n" }
    index($0, want) { hit = 1 } END { if (hit) printf "%s", block }' "$WF"
}

# The sweep, the weekly and the manual run.
world
{
  branch claude/done "$(sha 2)"
  branch release "$(sha 3)" true
  branch claude/ahead "$(sha 4)"
  branch claude/open "$(sha 5)"
  branch patch-1 "$(sha 6)"
  branch keep/ruled "$(sha 7)"
  branch feat/a "$(sha 8)"
  branch long/lived/1 "$(sha 9)"
  branch dev "$(sha 10)"
  branch developer "$(sha 11)"
} | branches
echo 2 >"$T/gh/ahead/$(sha 4)"
# feat/b is stacked on feat/a, which main already contains.
{ pull 8 patch-1 "$(sha 6)" stranger/r && pull 40 feat/b "$(sha 12)" o/r feat/a && pull 7 claude/open "$(sha 5)"; } | open
echo keep/ruled >"$T/gh/ruled"
check "the stand-in lists the eleven branches two to a page, on any jq (#118)" \
  "$(jq -c 'map(.name)' "$T/gh/branches.json" | tr '\n' ' ')" \
  '["main","claude/done"] ["release","claude/ahead"] ["claude/open","patch-1"] ["keep/ruled","feat/a"] ["long/lived/1","dev"] ["developer"] '
check "and the three open pull requests two to a page" "$(jq -c 'map(.number)' "$T/gh/open.json" | tr '\n' ' ')" '[8,40] [7] '
KEEP=$'long/*\ndev' run sweep
check "a sweep exits 0" "$?" 0
check "a sweep deletes the branches main contains, and only them, on every page of the branches (#118)" "$(deleted)" \
  "claude/done patch-1 developer"
check "it prints the deleted branch with its commit and how to restore it" \
  "$(grep -c "^deleted claude/done $(sha 2) (restore: git push origin $(sha 2):refs/heads/claude/done)$" "$T/out")" 1
check "the default branch is kept" "$(grep -c "^kept main $(sha 1): the default branch$" "$T/out")" 1
check "a protected branch is kept" "$(grep -c "^kept release $(sha 3): a protected branch$" "$T/out")" 1
check "a branch ahead of main is kept, with the count" \
  "$(grep -c "^kept claude/ahead $(sha 4): 2 commit(s) main doesn't have$" "$T/out")" 1
check "the head of an open pull request is kept, with its number, on every page of the pull requests (#118)" \
  "$(grep -c "^kept claude/open $(sha 5): the head of the open pull request #7$" "$T/out")" 1
check "the base of an open pull request is kept, though main contains it: deleting it closes the pull request (#118)" \
  "$(grep -c "^kept feat/a $(sha 8): the base of the open pull request #40$" "$T/out")" 1
check "a branch a ruleset forbids deleting is kept, on every page of its rules (#118)" \
  "$(grep -c "^kept keep/ruled $(sha 7): a ruleset forbids deleting it$" "$T/out")" 1
check "a branch's rules are read 100 to a page (#118)" "$(grep -c '^/rules/branches/claude/done?per_page=100$' "$T/paths")" 1
check "a fork's open pull request doesn't keep this repository's branch of that name" \
  "$(grep -c "^deleted patch-1 " "$T/out")" 1
check "a branch a glob of the keep-list matches is kept, with the glob (#118)" \
  "$(grep -c "^kept long/lived/1 $(sha 9): THEOS_KEEP_BRANCHES names it (long/\*)$" "$T/out")" 1
check "a branch the keep-list names is kept, and one that only starts like it is not" \
  "$(grep -c "^kept dev $(sha 10): THEOS_KEEP_BRANCHES names it (dev)$" "$T/out")" 1
check "nothing is deleted by its name alone through the API (#118)" "$(cat "$T/calls")" ""

KEEP=$'long/*\ndev' DRY_RUN=true run sweep
check "a dry run exits 0" "$?" 0
check "a dry run deletes nothing" "$(deleted)" ""
check "a dry run says what it would delete" "$(grep '^would delete ' "$T/out" | cut -d' ' -f3 | tr '\n' ' ')" \
  "claude/done patch-1 developer "
check "a dry run still says what it keeps" "$(grep -c '^kept ' "$T/out")" 8
check "a dry run runs no git" "$(cat "$T/git-args")" ""

run sweep
check "the keep-list is empty unless it is set" "$(deleted)" "claude/done patch-1 long/lived/1 dev developer"

# A push that lands while the run decides.
world
{ branch raced "$(sha 2)" && branch calm "$(sha 3)" && branch went "$(sha 4)"; } | branches
printf 'raced %s\nwent -\n' "$(sha 60)" >"$T/gh/moved"
run sweep
check "a branch that moved in between doesn't fail the run" "$?" 0
check "a branch pushed to after the run read it is not deleted with that push (#118)" "$(at raced)" "$(sha 60)"
check "it is kept with the commit it is at now" \
  "$(grep -c "^kept raced $(sha 60): pushed to since this run read it at $(sha 2)$" "$T/out")" 1
check "and no restore line names a commit it had left" "$(grep -c "^deleted raced" "$T/out")" 0
check "the branch still where the run read it is deleted" "$(grep -c "^deleted calm $(sha 3) " "$T/out")" 1
check "a branch that went in between is no error" "$(grep -c '^gone went: already deleted$' "$T/out")" 1
check "the delete names the commit the branch must be at" \
  "$(grep -c -- " --force-with-lease=refs/heads/raced:$(sha 2) -- $REMOTE :refs/heads/raced$" "$T/git-args")" 1
check "nothing is deleted by its name alone through the API" "$(cat "$T/calls")" ""

# The token.
check "git gets the token from a credential helper, as GitHub's x-access-token" \
  "$(sort -u "$T/git-cred" | grep -c -x -e 'username=x-access-token' -e "password=$TOKEN")" 2
check "the token is in no argument of git" "$(grep -c -F "$TOKEN" "$T/git-args")" 0
check "nor in what the run prints" "$(grep -c -F "$TOKEN" "$T/out")" 0
check "a credential helper the runner has set up is neither asked for the token nor handed it to store (#118)" \
  "$(cat "$T/helper-spy")" ""

# Where git deletes when nothing says: the repository on github.com, or on
# the server the runner names.
world
branch a "$(sha 2)" | branches
REMOTE_URL='unset' run sweep
check "with no THEOS_GIT_REMOTE the delete goes to the repository on github.com, by a URL that holds no credential (#118)" \
  "$(grep -c -- " -- https://github.com/o/r.git :refs/heads/a$" "$T/git-args")" 1
check "and the branch is deleted there" "$(deleted)" a
check "the token is in no argument of git then either (#118)" "$(grep -c -F "$TOKEN" "$T/git-args")" 0
REMOTE_URL='unset' SERVER_URL=https://ghe.example.invalid run sweep
check "on another server, it goes to the repository on GITHUB_SERVER_URL (#118)" \
  "$(grep -c -- " -- https://ghe.example.invalid/o/r.git :refs/heads/a$" "$T/git-args") $(deleted)" "1 a"

# A ref whose name ends in the branch's. For the refs/heads/NAME of a push,
# git takes refs/tags/refs/heads/NAME or refs/heads/refs/heads/NAME as well,
# and three more: the one of them that is there once the branch itself is
# gone.
world
{
  branch lost "$(sha 2)"
  branch lost-a "$(sha 2)"
  branch lost-b "$(sha 2)"
  branch lost-c "$(sha 2)"
  branch went "$(sha 3)"
  branch refs/heads/went "$(sha 3)"
  branch both "$(sha 4)"
  branch calm "$(sha 5)"
} | branches
pull 9 refs/heads/went "$(sha 3)" | open
printf '%s -\n' lost lost-a lost-b lost-c went >"$T/gh/moved"
# strays: the refs that are no branch and named like a branch's full ref, at
# that branch's commit; the last one is named like a branch that is there.
STRAYS=(refs/refs/heads/lost-a refs/remotes/refs/heads/lost-b refs/remotes/refs/heads/lost-c/HEAD refs/tags/refs/heads/lost)
strays() {
  local r
  for r in "${STRAYS[@]}"; do
    git -C "$REMOTE" update-ref "$r" "$(sha 2)"
  done
  git -C "$REMOTE" update-ref refs/tags/refs/heads/both "$(sha 4)"
}
strays
run sweep
check "a ref named like a branch's doesn't fail the run" "$?" 0
check "no ref named like the full ref of a branch that went is deleted in its place, a tag least of all (#118)" \
  "$(git -C "$REMOTE" for-each-ref --format='%(refname)' "${STRAYS[@]}" | tr '\n' ' ')" "${STRAYS[*]} "
check "nor is a branch named like it, which the run printed as kept (#118)" \
  "$(at refs/heads/went) $(grep -c "^kept refs/heads/went $(sha 3): the head of the open pull request #9$" "$T/out")" "$(sha 3) 1"
check "the branches that went are gone, and no error" "$(grep -c -x 'gone \(lost\|lost-[abc]\|went\): already deleted' "$T/out")" 5
check "a branch git can't tell from a tag named like its full ref is kept, with the tag's name (#118)" \
  "$(grep -c "^kept both $(sha 4): git can't tell it from refs/tags/refs/heads/both, and deletes neither while both are there$" "$T/out")" 1
check "and both are still there" "$(at both) $(git -C "$REMOTE" rev-parse -q --verify refs/tags/refs/heads/both)" "$(sha 4) $(sha 4)"
check "the branch nothing is named like is deleted" "$(grep -c "^deleted calm $(sha 5) " "$T/out")" 1
strays
: >"$T/gh/unguarded"
run sweep
check "should git delete another ref than the branch all the same, the run fails (#118)" "$?" 1
check "it names the ref git deleted, by the line git printed for it, and how to bring it back (#118)" \
  "$(grep -c "^error lost $(sha 2): git deleted refs/tags/refs/heads/lost in its place (restore: git push origin $(sha 2):refs/tags/refs/heads/lost)$" "$T/out")" 1
check "and prints no branch as deleted that git didn't name" "$(grep -c '^deleted lost ' "$T/out")" 0
git -C "$REMOTE" for-each-ref --format='delete %(refname)' refs/tags refs/refs refs/remotes | git -C "$REMOTE" update-ref --stdin

# What GitHub answers about a branch that main can't be compared with.
world
{ branch gh-pages "$(sha 2)" && branch odd "$(sha 3)" && branch fine "$(sha 4)"; } | branches
echo unrelated >"$T/gh/ahead/$(sha 2)"
run sweep
check "a branch with no common ancestor with main doesn't fail the run, week after week (#118)" "$?" 0
check "it is kept, with the reason (#118)" \
  "$(grep -c "^kept gh-pages $(sha 2): no common ancestor with main, so main doesn't contain it$" "$T/out")" 1
check "and the run goes on with the others" "$(deleted)" "odd fine"
echo null >"$T/gh/ahead/$(sha 3)"
run sweep
check "a comparison that holds no count of commits fails the run (#118)" "$?" 1
check "and keeps the branch: no count is not a count of 0 (#118)" "$(deleted)" "fine"
check "it says what GitHub answered in the count's place" \
  "$(grep -c "^error odd $(sha 3): GitHub's comparison with main holds no count of commits (null), so it is kept$" "$T/out")" 1

# The keep-list as a repository variable holds it.
world
{ branch long/lived/1 "$(sha 2)" && branch dev "$(sha 3)" && branch developer "$(sha 4)"; } | branches
KEEP=$'long/*\r\ndev\r\n' run sweep
check "a keep-list saved with CRLF line ends keeps the branches of every pattern, not of the last one (#118)" "$(deleted)" developer
check "the run prints the patterns it read (#118)" \
  "$(grep -c '^THEOS_KEEP_BRANCHES: 2 pattern(s): \[long/\*\] \[dev\]$' "$T/out")" 1
KEEP='"long/* dev"' run sweep
check "a keep-list typed in quotes shows them in the patterns the run read, where they match nothing (#118)" \
  "$(grep -c '^THEOS_KEEP_BRANCHES: 2 pattern(s): \["long/\*\] \[dev"\]$' "$T/out") $(deleted)" "1 long/lived/1 dev developer"
run sweep
check "an empty keep-list is printed as no pattern" "$(grep -c '^THEOS_KEEP_BRANCHES: no pattern$' "$T/out")" 1

# Only DRY_RUN=false, or none, deletes.
world
branch a "$(sha 2)" | branches
for v in True TRUE 1 yes on 0 FALSE ' false'; do
  DRY_RUN=$v run sweep
  check "DRY_RUN=[$v] is a dry run: nothing but false deletes (#118)" "$(deleted) $(grep -c "^would delete a $(sha 2)$" "$T/out")" " 1"
done
DRY_RUN=True run sweep
check "the run says which value made it a dry run (#118)" \
  "$(grep -c '^dry run: nothing is deleted (DRY_RUN is True: only false, or none, deletes)$' "$T/out")" 1
DRY_RUN=true run sweep
check "and says no more than that for true" "$(grep -c '^dry run: nothing is deleted$' "$T/out")" 1
DRY_RUN=false run sweep
check "DRY_RUN=false deletes, as a manual run with the box unticked" "$(deleted)" a
DRY_RUN='unset' run sweep
check "no DRY_RUN deletes, as the step of a push, which names none" "$(deleted)" a
run sweep
check "an empty DRY_RUN deletes, as the schedule, which has no input" "$(deleted)" a

# GitHub's names are the same in any case.
world
{ branch cased "$(sha 2)" && branch plain "$(sha 3)"; } | branches
pull 5 cased "$(sha 2)" O/R | open
run sweep
check "the head of an open pull request is kept when GitHub spells the repository in another case (#118)" \
  "$(grep -c "^kept cased $(sha 2): the head of the open pull request #5$" "$T/out") $(deleted)" "1 plain"
echo '[]' >"$T/gh/open.json"
pull 6 cased "$(sha 2)" O/R | jq -s . >"$T/gh/pulls.json"
run merged "$(sha 99)"
check "and a merged head is this repository's, and deleted, in another case too" "$(deleted)" cased

# A branch is named by whoever pushed it.
world
# shellcheck disable=SC2016 # the name is GitHub's to hand over, not the shell's to expand
evil='x/$(touch${IFS}pwned);100%`touch${IFS}pwned`'
branch "$evil" "$(sha 2)" | branches
run sweep
# shellcheck disable=SC2016 # as above
check "a branch name reaches GitHub percent-encoded" \
  "$(grep -c -x -F '/rules/branches/x/%24%28touch%24%7BIFS%7Dpwned%29%3B100%25%60touch%24%7BIFS%7Dpwned%60?per_page=100' "$T/paths")" 1
check "and git as one word" "$(deleted)" "$evil"
check "and is never run" "$([ -e "$T/pwned" ] && echo run)" ""
restore=$(sed -n 's/^deleted .* (restore: \(.*\))$/\1/p' "$T/out")
(cd "$REMOTE" && bash -c "$restore" >/dev/null 2>&1)
check "the restore line, pasted into a shell, runs nothing of the name (#118)" \
  "$([ -e "$REMOTE/pwned" ] || [ -e "$T/pwned" ] && echo run)" ""
check "and brings the branch back at its commit" "$(at "$evil")" "$(sha 2)"

# What GitHub refuses or can't answer.
world
{ branch a "$(sha 2)" && branch b "$(sha 3)" && branch c "$(sha 4)"; } | branches
echo 'a GH013: Repository rule violations found' >"$T/gh/refused"
run sweep
check "a deletion GitHub rejects for a rule keeps the branch and doesn't fail the run" "$?" 0
check "it says GitHub refused" "$(grep -c "^kept a $(sha 2): GitHub refuses to delete it: (hook declined)$" "$T/out")" 1
check "and goes on with the others" "$(deleted)" "b c"
: >"$T/gh/refused"
REMOTE_URL=$T/nowhere.git run sweep
check "a push GitHub doesn't take fails the run" "$?" 1
check "it names the branch and git's answer" \
  "$(grep -c "^error a $(sha 2): couldn't delete it: fatal: '$T/nowhere.git' does not appear to be a git repository$" "$T/out")" 1
check "and deletes nothing" "$(deleted)" ""
echo fail >"$T/gh/ahead/$(sha 3)"
run sweep
check "a branch GitHub can't compare with main fails the run" "$?" 1
check "and is kept" "$(deleted)" "a c"
rm "$T/gh/ahead/$(sha 3)"
echo /rules/branches/b >"$T/gh/down"
run sweep
check "a branch whose rules GitHub can't show fails the run" "$?" 1
check "and is kept too" "$(deleted)" "a c"
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
  branch feat/stacked-on "$(sha 14)"
  branch feat/raced "$(sha 15)"
  branch feat/kept "$(sha 16)"
} | branches
echo 3 >"$T/gh/ahead/$(sha 3)"  # a squash: main has its changes, not its commit
echo 1 >"$T/gh/ahead/$(sha 40)" # a commit pushed after the merge
echo "feat/raced $(sha 61)" >"$T/gh/moved"
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
  pull 33 feat/stacked-on "$(sha 14)"
  pull 34 feat/raced "$(sha 15)"
  pull 35 feat/kept "$(sha 16)"
} | jq -s . >"$T/gh/pulls.json"
# A fork's pull request is stacked on feat/stacked-on; #31 is on the second page.
{ pull 32 topic "$(sha 17)" stranger/r feat/stacked-on && pull 36 other "$(sha 18)" && pull 31 feat/reused "$(sha 6)"; } | open
KEEP='feat/k?pt' run merged "$(sha 99)"
check "a merge exits 0" "$?" 0
check "a merge deletes the merged head branches, and only them (#118)" "$(deleted)" \
  "feat/merged feat/squashed feat/behind"
check "a squashed head still at its merged commit is deleted with that commit" \
  "$(grep -c "^deleted feat/squashed $(sha 3) " "$T/out")" 1
check "a head moved to a commit main contains is deleted" "$(grep -c "^deleted feat/behind $(sha 50) " "$T/out")" 1
check "a head pushed further after the merge is kept" \
  "$(grep -c "^kept feat/pushed-on $(sha 40): 1 commit(s) main doesn't have, pushed after the merge of $(sha 4 | cut -c 1-12)$" "$T/out")" 1
check "a head that another open pull request uses is kept, on every page of the pull requests" \
  "$(grep -c "^kept feat/reused $(sha 6): the head of the open pull request #31$" "$T/out")" 1
check "a merged head that an open pull request, a fork's too, is based on is kept (#118)" \
  "$(grep -c "^kept feat/stacked-on $(sha 14): the base of the open pull request #32$" "$T/out")" 1
check "a merged head pushed to after the run read it is kept with its new commit (#118)" \
  "$(grep -c "^kept feat/raced $(sha 61): pushed to since this run read it at $(sha 15)$" "$T/out")" 1
check "and is still there" "$(at feat/raced)" "$(sha 61)"
check "a merged head the keep-list names is kept (#118)" \
  "$(grep -c "^kept feat/kept $(sha 16): THEOS_KEEP_BRANCHES names it (feat/k?pt)$" "$T/out")" 1
check "a protected head is kept" "$(grep -c "^kept release $(sha 7): a protected branch$" "$T/out")" 1
check "a fork's head is kept, and this repository's branch of that name with it" \
  "$(grep -c "^kept patch-1: pull request #26 came from a fork (stranger/r)" "$T/out")" 1
check "a head whose fork is gone is kept" "$(grep -c "^kept feat/fork-gone: pull request #30 came from a fork (a deleted one)" "$T/out")" 1
check "a pull request that isn't merged deletes nothing" "$(grep -c 'feat/unmerged' "$T/out")" 0
check "a pull request merged to another branch deletes nothing" "$(grep -c 'feat/other-base' "$T/out")" 0
check "a head already deleted is no error" "$(grep -c '^gone feat/gone: already deleted (pull request #29)$' "$T/out")" 1
check "a branch no pull request names is left alone" "$(grep -c bystander "$T/out")" 0
: >"$T/gh/moved"

DRY_RUN=true run merged "$(sha 99)"
check "a dry run of a merge deletes nothing" "$(deleted)" ""
check "and says what it would delete" "$(grep '^would delete ' "$T/out" | cut -d' ' -f3 | tr '\n' ' ')" \
  "feat/merged feat/squashed feat/behind feat/raced feat/kept "

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
check "a run by hand is a dry run unless someone says otherwise (#118)" \
  "$(sed -n '/^      dry_run:$/,/^[^ ]/p' "$WF" | grep -E '^        (type|default): ' | tr -s ' ' | tr '\n' ';')" \
  " type: boolean; default: true;"
check "its example of the keep-list is as it is typed, without the quotes that would be part of a pattern (#118)" \
  "$(grep -c '"release/' "$WF") $(grep -c '(release/\* dev; a \* also$' "$WF")" "0 1"
check "it never runs on a pull request's event, so never a pull request's code" \
  "$(grep -c '^  pull_request' "$WF")" 0
check "the workflow grants nothing by default" "$(grep -c '^permissions: {}$' "$WF")" 1
check "its job may write contents and read pull requests, and no more" \
  "$(sed -n '/^    permissions:$/,/^    [a-z]/p' "$WF" | grep '^      ' | sed 's/ *#.*//' | tr -s ' ' | tr '\n' ';')" \
  " contents: write; pull-requests: read;"
check "the job runs on the default branch's ref only, which a tag named main is not (#118)" \
  "$(grep -c "^    if: github.event_name == 'schedule' || github.ref == format('refs/heads/{0}', github.event.repository.default_branch)$" "$WF")" 1
check "no condition trusts a ref's short name, which a tag shares with a branch" "$(grep -c 'ref_name' "$WF")" 0
check "the clone keeps no credentials" "$(grep -c '^          persist-credentials: false$' "$WF")" 1
check "every action is pinned to a commit" "$(grep '^ *uses: ' "$WF" | grep -vc '@[0-9a-f]\{40\} # v')" 0
check "no run: step holds a \${{ }} expression: the event reaches the script through env" \
  "$(awk '/^ +run: / || inrun { if (/\$\{\{/) n++ } /^ +run: \|/ { inrun = 1; next } inrun && /^ +[a-z-]+: / { inrun = 0 } END { print n + 0 }' "$WF")" 0
# shellcheck disable=SC2016 # the workflow's text
{
  merged=$(step 'hack/delete-merged-branches.sh merged "$SHA"')
  sweep=$(step 'hack/delete-merged-branches.sh sweep')
  check "a push runs the merged mode with the pushed commit, and nothing else does (#118)" \
    "$(grep -E '^        (if|run): ' <<<"$merged" | tr -s ' ' | tr '\n' ';')" \
    " if: github.event_name == 'push'; run: hack/delete-merged-branches.sh merged \"\$SHA\";"
  check "a schedule or a manual run sweeps, and a push doesn't (#118)" \
    "$(grep -E '^        (if|run): ' <<<"$sweep" | tr -s ' ' | tr '\n' ';')" \
    " if: github.event_name != 'push'; run: hack/delete-merged-branches.sh sweep;"
  check "the sweep is a dry run when the manual run's input says so, and deletes on the schedule, which has no input (#118)" \
    "$(grep -c '^          DRY_RUN: \${{ inputs.dry_run }}$' <<<"$sweep")" 1
  check "a push always deletes: no dry run reaches the merged mode" "$(grep -c 'DRY_RUN' <<<"$merged")" 0
  check "both modes get the keep-list from the repository variable, through env (#118)" \
    "$(grep -c '^          THEOS_KEEP_BRANCHES: \${{ vars.KEEP_BRANCHES }}$' <<<"$merged"$'\n'"$sweep")" 2
  check "and the job's token, the repository and the pushed commit" \
    "$(grep -c -e '^          GH_TOKEN: \${{ github.token }}$' -e '^          THEOS_GITHUB_REPO: \${{ github.repository }}$' -e '^          SHA: \${{ github.sha }}$' <<<"$merged"$'\n'"$sweep")" 5
}

exit $fail
