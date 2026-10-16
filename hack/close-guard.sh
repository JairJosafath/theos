#!/usr/bin/env bash
# PreToolUse hook on Bash (.claude/settings.json): before a session opens,
# edits or merges a pull request (gh pr create, edit, merge) or commits (git
# commit) in this repository, it runs hack/lib/github.sh closes-check over the
# text that would land: the command itself, where a --body, -m or heredoc
# puts it, and the files a --body-file, -F or --file names; for gh pr merge,
# also the pull request's title, body and commit messages on GitHub, whose
# Refs #N then count only in the body (a commit's Refs #N closes nothing). A
# closing keyword (closes, fixed, resolves, ...) for an issue the same text
# says Refs, or for an issue with a "Depends on #S" step still open, would
# close the parent of a plan before its steps land; the hook denies the
# command and names the line. When GitHub can't be asked, a merge is denied
# and the rest go on with a warning. Commands without a closing keyword cost
# no GitHub call; other commands cost one bash, with no subprocess.
set -uo pipefail

IFS= read -r -d '' input
# A loose match on the raw JSON with builtins only, as heavy-guard.sh does.
loose='gh.*pr.*(create|edit|merge)|git.*commit'
[[ $input =~ $loose ]] || exit 0

cmd=$(jq -r '.tool_input.command // empty' <<<"$input" 2>/dev/null) || exit 0
cwd=$(jq -r '.cwd // empty' <<<"$input" 2>/dev/null)
[ -n "$cwd" ] && cd "$cwd" 2>/dev/null
cmd=${cmd//$'\\\n'/ }
nl=$'\n'
pr='gh[[:blank:]]+pr[[:blank:]]+(create|edit|merge)'
commit='git([[:blank:]]+-C[[:blank:]]+[^[:space:]]+)?[[:blank:]]+commit'
# Where a command starts: the start of a line, or after ; & | ( or $(, then
# any VAR=value words. Text that only mentions one (echo "git commit") is not one.
sep="(^|[;&|(]|[$nl])[[:space:]]*([[:alnum:]_]+=[^[:space:]]*[[:space:]]+)*"
[[ $cmd =~ $sep($pr|$commit)([[:space:]]|$) ]] || exit 0
what=${BASH_REMATCH[3]}

# shellcheck source=hack/lib/github.sh
. "$(dirname "${BASH_SOURCE[0]}")/lib/github.sh"
# Only this repository's issues: another repository's #N is not ours to guard.
origin=$(git remote get-url origin 2>/dev/null) || exit 0
[[ ${origin,,} == *"${C4_GITHUB_REPO,,}"* ]] || exit 0

deny() {
  jq -n --arg m "$1" '{hookSpecificOutput: {hookEventName: "PreToolUse", permissionDecision: "deny", permissionDecisionReason: $m}}'
  exit 0
}

text=$cmd
while IFS= read -r f; do
  f=$(sed -E "s/^[^= ]+[= ]+//; s/^[\"']//; s/[\"']$//" <<<"$f")
  [ -n "$f" ] && [ "$f" != - ] && [ -f "$f" ] && text+=$'\n'$(cat -- "$f")
done < <(grep -oE "(--body-file|--file|-F)[= ]+(\"[^\"]*\"|'[^']*'|[^[:space:];&|]+)" <<<"$cmd")

merge= body=
if [[ $what == *merge ]]; then
  merge=1
  # The pull request is the first word after merge that is not a flag or a
  # flag's value: a number, a URL or a branch.
  arg= repo= skip=
  rest=
  [[ $cmd =~ gh[[:blank:]]+pr[[:blank:]]+merge([^$nl]*) ]] && rest=${BASH_REMATCH[1]}
  read -ra words <<<"$rest"
  for w in "${words[@]}"; do
    [[ $w =~ ^[\;\&\|\)] ]] && break
    end=
    [[ $w =~ [\;\&\|\)]$ ]] && end=1 w=${w%%[;&|)]*}
    w=${w//[\"\']/}
    if [ -n "$skip" ]; then
      [ "$skip" = repo ] && repo=$w
      skip=
    else
      case $w in
        -R | --repo) skip=repo ;;
        --repo=*) repo=${w#*=} ;;
        -b | --body | -F | --body-file | -t | --subject | -A | --author-email | --match-head-commit) skip=1 ;;
        -*) ;;
        *) [ -n "$arg" ] || arg=$w ;;
      esac
    fi
    [ -z "$end" ] || break
  done
  # Another repository's pull request is not ours to guard.
  [ -z "$repo" ] || [[ ${repo,,} == *"${C4_GITHUB_REPO,,}" ]] || exit 0
  if [[ $arg =~ ^[0-9]+$ || $arg =~ /pull/([0-9]+) ]]; then
    n=${BASH_REMATCH[1]:-$arg}
  else
    gh_token 2>/dev/null
    n=$(gh pr view ${arg:+"$arg"} --json number --jq .number 2>/dev/null)
  fi
  [[ ${n:-} =~ ^[0-9]+$ ]] || deny "close-guard: could not tell which pull request \`gh pr merge\` merges, so it could not check that merging it closes no plan's parent too early. Name the pull request: gh pr merge N."
  more=$(gh_pr_text "$n" 2>/dev/null) && body=$(gh_pr_body "$n" 2>/dev/null) ||
    deny "close-guard: could not read pull request #$n from GitHub to check that merging it closes no plan's parent too early; try again."
  text+=$'\n'$more
fi

# A merge's Refs #N count only in the pull request's body (gh_closes_check BODY).
out=$(gh_closes_check - ${merge:+"$body"} <<<"$text" 2>&1)
case $? in
  0) exit 0 ;;
  1) deny "$out" ;;
  *)
    [ -n "$merge" ] && deny "close-guard: GitHub could not be asked whether the issues this merge closes still have open steps; try again. $out"
    jq -n --arg m "close-guard: GitHub could not be asked whether the issues this text closes still have open steps; check before merging. $out" \
      '{hookSpecificOutput: {hookEventName: "PreToolUse", additionalContext: $m}}'
    ;;
esac
