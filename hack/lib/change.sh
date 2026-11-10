# shellcheck shell=bash
# The change make check and make test select by: everything that differs from
# the merge base of HEAD with TEST_BASE, so commits, staged and unstaged
# changes and untracked files that git doesn't ignore. hack/check.sh and
# hack/test-packages.sh source this file, in the checkout's top directory.
#
#   change_base    sets CHANGE_FROM to the base's name and CHANGE_BASE to the
#                  merge base. When everything is to be checked it sets
#                  CHANGE_ALL to the reason instead and leaves CHANGE_BASE
#                  empty: TEST_ALL or FORCE is set, the branch is main, or
#                  there is no merge base (a shallow clone, a tarball)
#   change_files   prints the files that differ from CHANGE_BASE, one a line
#
#   TEST_BASE   the branch or commit to diff against (default origin/main)
#   TEST_ALL    non-empty: everything
#   FORCE       non-empty: everything

change_base() {
  local branch
  CHANGE_FROM=${TEST_BASE:-origin/main}
  CHANGE_BASE=
  CHANGE_ALL=
  if [ -n "${TEST_ALL:-}" ]; then
    CHANGE_ALL="TEST_ALL is set"
  elif [ -n "${FORCE:-}" ]; then
    CHANGE_ALL="FORCE is set"
  elif branch=$(git rev-parse --abbrev-ref HEAD 2>/dev/null) && [ "$branch" = main ]; then
    CHANGE_ALL="the branch is main"
  elif ! CHANGE_BASE=$(git merge-base "$CHANGE_FROM" HEAD 2>/dev/null); then
    CHANGE_BASE=
    CHANGE_ALL="no merge base with $CHANGE_FROM"
  fi
}

change_files() {
  {
    git diff --name-only --no-renames "$CHANGE_BASE" --
    git ls-files --others --exclude-standard
  } | sort -u
}
