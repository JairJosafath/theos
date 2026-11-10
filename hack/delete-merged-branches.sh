#!/usr/bin/env bash
# Deletes the branches of the GitHub repository that main already holds (#118:
# "no dead branches are left hanging, once they are merged to main, they
# should be deleted"). The Branch hygiene workflow
# (.github/workflows/branch-hygiene.yml) runs it; it asks GitHub through gh
# and needs no clone.
#
#   hack/delete-merged-branches.sh merged SHA   the head branch of each pull
#                                               request merged to the default
#                                               branch that the commit SHA
#                                               brought there: what a push to
#                                               main runs
#   hack/delete-merged-branches.sh sweep        every branch whose tip the
#                                               default branch contains: the
#                                               weekly and the manual run
#
#   THEOS_GITHUB_REPO   the repository, OWNER/NAME
#   GH_TOKEN            a token that may delete a branch (contents: write) and
#                       read pull requests
#   DRY_RUN=true        say what would be deleted, delete nothing
#
# A branch is kept, with the reason in a "kept" line, when it is the default
# branch, when GitHub calls it protected or a ruleset forbids deleting it,
# when it is the head of an open pull request of this repository, and when it
# has a commit the default branch doesn't have. The last one has one
# exception: after a merge, the branch still at the commit its pull request
# merged is deleted, since a squash or a rebase leaves main without that
# commit and with its changes. A branch pushed further after its merge is
# kept. The head of a pull request from a fork is the fork's branch, and a
# branch of this repository with the same name is left alone.
#
# Every deleted branch is printed with its commit:
#   deleted BRANCH SHA (restore: git push origin SHA:refs/heads/BRANCH)
# Exit 1 when GitHub can't answer or refuses a deletion it gave no rule for;
# the branches it could decide on are still handled.
# hack/delete-merged-branches_test.sh tests it against a stand-in gh.
set -uo pipefail

REPO=${THEOS_GITHUB_REPO:?set THEOS_GITHUB_REPO to OWNER/NAME}
DRY=${DRY_RUN:-false}
failed=0

api() { gh api -H 'Accept: application/vnd.github+json' "$@"; }
# enc NAME: a branch name for a URL path, each part between its slashes
# percent-encoded. A branch is named by whoever pushed it: it never reaches a
# URL or a shell word unquoted.
enc() { jq -rn --arg s "$1" '$s | split("/") | map(@uri) | join("/")'; }
lower() { tr '[:upper:]' '[:lower:]' <<<"$1"; }
kept() { echo "kept $1 $2: $3"; }
oops() {
  echo "error $*" >&2
  failed=1
}

# remove BRANCH SHA: delete the branch, or say it would be in a dry run. GitHub
# refusing with a 422 means a rule this token can't read protects the branch:
# it is kept. A branch gone in between (GitHub's own "Automatically delete head
# branches" got there first) is no error.
remove() {
  local name=$1 sha=$2 out
  if [ "$DRY" = true ]; then
    echo "would delete $name $sha"
    return 0
  fi
  if out=$(api --method DELETE "repos/$REPO/git/refs/heads/$(enc "$name")" 2>&1); then
    echo "deleted $name $sha (restore: git push origin $sha:refs/heads/$name)"
    return 0
  fi
  case $out in
    *'Reference does not exist'*) echo "gone $name: already deleted" ;;
    *'HTTP 422'*) kept "$name" "$sha" "GitHub refuses to delete it: $(tail -n 1 <<<"$out")" ;;
    *) oops "$name $sha: couldn't delete it: $(tail -n 1 <<<"$out")" ;;
  esac
}

# consider BRANCH SHA PROTECTED [MERGED]: keep the branch at SHA or delete it.
# MERGED is the commit its pull request merged, when one just did.
consider() {
  local name=$1 sha=$2 protected=$3 merged=${4:-} ahead rules
  if [ "$name" = "$DEFAULT" ]; then
    kept "$name" "$sha" "the default branch"
    return 0
  fi
  if [ "$protected" = true ]; then
    kept "$name" "$sha" "a protected branch"
    return 0
  fi
  if [ -n "${OPEN[$name]:-}" ]; then
    kept "$name" "$sha" "the head of the open pull request #${OPEN[$name]}"
    return 0
  fi
  if [ "$sha" != "$merged" ]; then
    if ! ahead=$(api "repos/$REPO/compare/$(enc "$DEFAULT")...$sha" --jq .ahead_by 2>&1); then
      oops "$name $sha: couldn't compare it with $DEFAULT, so it is kept: $(tail -n 1 <<<"$ahead")"
      return 0
    fi
    if [ "$ahead" != 0 ]; then
      kept "$name" "$sha" "$ahead commit(s) $DEFAULT doesn't have${merged:+, pushed after the merge of ${merged:0:12}}"
      return 0
    fi
  fi
  if ! rules=$(api "repos/$REPO/rules/branches/$(enc "$name")" --jq '[.[] | select(.type == "deletion")] | length' 2>&1); then
    oops "$name $sha: couldn't read its rules, so it is kept: $(tail -n 1 <<<"$rules")"
    return 0
  fi
  if [ "$rules" != 0 ]; then
    kept "$name" "$sha" "a ruleset forbids deleting it"
    return 0
  fi
  remove "$name" "$sha"
}

# merged SHA: the head branches of the merged pull requests SHA belongs to.
merged() {
  local sha=$1 prs n ref head repo branch tip protected
  if ! prs=$(api "repos/$REPO/commits/$sha/pulls?per_page=100" |
    jq -r --arg base "$DEFAULT" '.[] | select(.merged_at != null and .base.ref == $base)
      | [.number, .head.sha, (.head.repo.full_name // "a deleted one"), .head.ref] | @tsv'); then
    oops "couldn't list the pull requests of $sha"
    return 0
  fi
  if [ -z "$prs" ]; then
    echo "no merged pull request brought $sha to $DEFAULT"
    return 0
  fi
  while IFS=$'\t' read -r n head repo ref; do
    if [ "$(lower "$repo")" != "$(lower "$REPO")" ]; then
      echo "kept $ref: pull request #$n came from a fork ($repo), whose branch isn't this repository's"
      continue
    fi
    if ! branch=$(api "repos/$REPO/branches/$(enc "$ref")" --jq '[.commit.sha, .protected] | @tsv' 2>&1); then
      case $branch in
        *'HTTP 404'*) echo "gone $ref: already deleted (pull request #$n)" ;;
        *) oops "$ref: couldn't read the branch of pull request #$n: $(tail -n 1 <<<"$branch")" ;;
      esac
      continue
    fi
    IFS=$'\t' read -r tip protected <<<"$branch"
    consider "$ref" "$tip" "$protected" "$head"
  done <<<"$prs"
}

# sweep: every branch of the repository.
sweep() {
  local list name sha protected
  if ! list=$(api --paginate "repos/$REPO/branches?per_page=100" |
    jq -r '.[] | [.commit.sha, .protected, .name] | @tsv') || [ -z "$list" ]; then
    oops "couldn't list the branches"
    return 0
  fi
  while IFS=$'\t' read -r sha protected name; do
    consider "$name" "$sha" "$protected"
  done <<<"$list"
}

mode=${1:-}
case $mode in
  sweep) ;;
  merged)
    if ! [[ ${2:-} =~ ^[0-9a-f]{40,64}$ ]]; then
      echo "usage: $0 merged SHA (a full commit id)" >&2
      exit 2
    fi
    ;;
  *)
    echo "usage: $0 merged SHA | sweep" >&2
    exit 2
    ;;
esac

if ! DEFAULT=$(api "repos/$REPO" --jq .default_branch) || [ -z "$DEFAULT" ]; then
  echo "error couldn't read the default branch of $REPO" >&2
  exit 1
fi
# The open pull requests' heads in this repository: branch -> number. Without
# the list nothing is deleted.
declare -A OPEN=()
if ! heads=$(api --paginate "repos/$REPO/pulls?state=open&per_page=100" |
  jq -r --arg repo "$(lower "$REPO")" '.[] | select((.head.repo.full_name // "" | ascii_downcase) == $repo)
    | [.number, .head.ref] | @tsv'); then
  echo "error couldn't list the open pull requests of $REPO" >&2
  exit 1
fi
while IFS=$'\t' read -r n ref; do
  [ -n "$ref" ] && OPEN[$ref]=$n
done <<<"$heads"

[ "$DRY" = true ] && echo "dry run: nothing is deleted"
if [ "$mode" = merged ]; then
  merged "$2"
else
  sweep
fi
exit $failed
