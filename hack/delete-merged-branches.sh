#!/usr/bin/env bash
# Deletes the branches of the GitHub repository that main already holds (#118:
# "no dead branches are left hanging, once they are merged to main, they
# should be deleted"). The Branch hygiene workflow
# (.github/workflows/branch-hygiene.yml) runs it. It asks GitHub through gh
# and deletes with git push, from an empty scratch repository: it needs no
# clone.
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
#   THEOS_GITHUB_REPO     the repository, OWNER/NAME
#   GH_TOKEN              a token that may delete a branch (contents: write)
#                         and read pull requests. gh reads it, and git gets it
#                         from a credential helper that reads this variable:
#                         it is in no argument, no URL, no file and no output
#   DRY_RUN=true          say what would be deleted, delete nothing
#   THEOS_KEEP_BRANCHES   glob patterns, split on spaces and newlines, of
#                         branches that are never deleted (release/* dev);
#                         a * also matches a slash. Default: none
#   THEOS_GIT_REMOTE      where git deletes. Default: the repository on
#                         GITHUB_SERVER_URL, or on https://github.com
#
# A branch is kept, with the reason in a "kept" line, when it is the default
# branch, when GitHub calls it protected or a ruleset forbids deleting it,
# when THEOS_KEEP_BRANCHES names it, when it is the head of an open pull
# request of this repository or the base of any open pull request (deleting
# the base closes the pull request stacked on it), and when it has a commit
# the default branch doesn't have. The last one has one
# exception: after a merge, the branch still at the commit its pull request
# merged is deleted, since a squash or a rebase leaves main without that
# commit and with its changes. A branch pushed further after its merge is
# kept. The head of a pull request from a fork is the fork's branch, and a
# branch of this repository with the same name is left alone.
#
# A branch is deleted only while it is still at the commit this run decided
# on: the delete is git push --force-with-lease=refs/heads/BRANCH:SHA, which
# GitHub applies or refuses in one step. A branch pushed to in between is kept,
# with the commit it is at now.
#
# Every deleted branch is printed with its commit, the restore command quoted
# for a shell, since a branch name may hold ; $( ) and backticks:
#   deleted BRANCH SHA (restore: git push origin SHA:refs/heads/BRANCH)
# Exit 1 when GitHub can't answer or the push fails for another reason than a
# rejected branch; the branches it could decide on are still handled.
# hack/delete-merged-branches_test.sh tests it against a stand-in gh.
set -uo pipefail

REPO=${THEOS_GITHUB_REPO:?set THEOS_GITHUB_REPO to OWNER/NAME}
DRY=${DRY_RUN:-false}
REMOTE=${THEOS_GIT_REMOTE:-${GITHUB_SERVER_URL:-https://github.com}/$REPO.git}
KEEP=()
read -rd '' -a KEEP <<<"${THEOS_KEEP_BRANCHES:-}" || true
SCRATCH=
trap '[ -z "$SCRATCH" ] || rm -rf "$SCRATCH"' EXIT
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

# remote ARGS...: git against the repository, from an empty scratch repository
# so that no clone's configuration or hook takes part. The token comes from a
# credential helper that reads GH_TOKEN when git asks: the command line and
# the URL hold no credential, and nothing is written anywhere.
remote() {
  # shellcheck disable=SC2016 # the helper is git's to run, and GH_TOKEN its shell's to expand
  GIT_TERMINAL_PROMPT=0 git -C "$SCRATCH" -c push.negotiate=false -c credential.helper= \
    -c credential.helper='!f() { test "$1" = get && printf "username=x-access-token\npassword=%s\n" "$GH_TOKEN"; }; f' "$@"
}
# why OUTPUT: git's or gh's own word for a failure, on one line.
why() {
  local line
  line=$(grep -m 1 -E '^(fatal|error|remote): ' <<<"$1") || line=$(tail -n 1 <<<"$1")
  echo "$line"
}

# remove BRANCH SHA: delete the branch if it is still at SHA, or say it would
# be deleted in a dry run. The push carries SHA as the commit the branch must
# be at (--force-with-lease), so a push that landed since this run read the
# branch is never deleted with it: the branch is kept, with its new commit.
# GitHub rejecting the deletion means a rule this token can't read protects
# the branch: it is kept. A branch gone in between (GitHub's own
# "Automatically delete head branches" got there first) is no error.
remove() {
  local name=$1 sha=$2 out flag='' summary='' f r s now=''
  if [ "$DRY" = true ]; then
    echo "would delete $name $sha"
    return 0
  fi
  if [ -z "$SCRATCH" ]; then
    if ! SCRATCH=$(mktemp -d) || ! out=$(git init -q --bare --template= "$SCRATCH" 2>&1); then
      oops "$name $sha: couldn't make the scratch repository to delete from, so it is kept: $(why "${out:-}")"
      [ -z "$SCRATCH" ] || rm -rf "$SCRATCH"
      SCRATCH=
      return 0
    fi
  fi
  out=$(remote push --porcelain "--force-with-lease=refs/heads/$name:$sha" -- "$REMOTE" ":refs/heads/$name" 2>&1)
  # The porcelain line of the one ref pushed: FLAG, [(delete)]:REF, SUMMARY.
  while IFS=$'\t' read -r f r s; do
    [ "${r#(delete)}" = ":refs/heads/$name" ] && flag=$f summary=$s
  done <<<"$out"
  case "$flag $summary" in
    '- [deleted]')
      printf 'deleted %s %s (restore: git push origin %q)\n' "$name" "$sha" "$sha:refs/heads/$name"
      ;;
    '! [rejected] (stale info)')
      # Not at SHA any more: pushed to, or gone.
      if ! out=$(remote ls-remote "$REMOTE" "refs/heads/$name" 2>&1); then
        oops "$name $sha: it is no longer at that commit, and couldn't be read again, so it is kept: $(why "$out")"
        return 0
      fi
      while read -r s r; do
        [ "$r" = "refs/heads/$name" ] && now=$s
      done <<<"$out"
      if [ -z "$now" ]; then
        echo "gone $name: already deleted"
      else
        kept "$name" "$now" "pushed to since this run read it at $sha"
      fi
      ;;
    '! [remote rejected] '*) kept "$name" "$sha" "GitHub refuses to delete it: ${summary#\[remote rejected\] }" ;;
    *) oops "$name $sha: couldn't delete it: $(why "$out")" ;;
  esac
}

# consider BRANCH SHA PROTECTED [MERGED]: keep the branch at SHA or delete it.
# MERGED is the commit its pull request merged, when one just did.
consider() {
  local name=$1 sha=$2 protected=$3 merged=${4:-} ahead rules pattern
  if [ "$name" = "$DEFAULT" ]; then
    kept "$name" "$sha" "the default branch"
    return 0
  fi
  if [ "$protected" = true ]; then
    kept "$name" "$sha" "a protected branch"
    return 0
  fi
  for pattern in "${KEEP[@]}"; do
    # shellcheck disable=SC2053 # the pattern is a glob: that is the setting
    if [[ $name == $pattern ]]; then
      kept "$name" "$sha" "THEOS_KEEP_BRANCHES names it ($pattern)"
      return 0
    fi
  done
  if [ -n "${OPEN[$name]:-}" ]; then
    kept "$name" "$sha" "the head of the open pull request #${OPEN[$name]}"
    return 0
  fi
  if [ -n "${BASE[$name]:-}" ]; then
    kept "$name" "$sha" "the base of the open pull request #${BASE[$name]}"
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
  # Every page of the rules, 100 to a page: gh hands over one array per page.
  if ! rules=$(api --paginate "repos/$REPO/rules/branches/$(enc "$name")?per_page=100" 2>&1) ||
    ! rules=$(jq -s '[.[][] | select(.type == "deletion")] | length' <<<"$rules" 2>&1); then
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
# The open pull requests: the heads in this repository (a fork's head is the
# fork's branch) and every base, branch -> number. Without the list nothing is
# deleted.
declare -A OPEN=() BASE=()
if ! pulls=$(api --paginate "repos/$REPO/pulls?state=open&per_page=100" |
  jq -r --arg repo "$(lower "$REPO")" '.[]
    | (select((.head.repo.full_name // "" | ascii_downcase) == $repo) | ["head", .number, .head.ref]),
      ["base", .number, .base.ref]
    | @tsv'); then
  echo "error couldn't list the open pull requests of $REPO" >&2
  exit 1
fi
while IFS=$'\t' read -r end n ref; do
  case $end in
    head) OPEN[$ref]=$n ;;
    base) BASE[$ref]=$n ;;
  esac
done <<<"$pulls"

[ "$DRY" = true ] && echo "dry run: nothing is deleted"
if [ "$mode" = merged ]; then
  merged "$2"
else
  sweep
fi
exit $failed
