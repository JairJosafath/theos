#!/usr/bin/env bash
# PreToolUse hook on Bash (.claude/settings.json): keeps the parent of a plan
# open while one of its "Depends on #S" steps is.
#
# By hand: a command that closes an issue at once (gh issue close N, a gh api
# call on issues/N that sets state=closed with a field or a JSON --input, or
# hack/lib/github.sh duplicate N M) is denied while N has a step still open,
# naming the step (hack/lib/github.sh close and duplicate refuse by
# themselves too). Steps the same command closes count as closed. When the
# hook can't tell which issue it closes (gh issue close "$n") or GitHub can't
# be asked, it is denied.
#
# At merge: before a session opens, edits or merges a pull request (gh pr
# create, edit, merge) or commits (git commit) in this repository, it runs
# hack/lib/github.sh closes-check over the text that would land: the command
# itself, where a --body, -m or heredoc puts it, and the files a --body-file,
# -F or --file names; for gh pr merge, also the pull request's title, body
# and commit messages on GitHub, whose Refs #N then count only in the body (a
# commit's Refs #N closes nothing). A closing keyword (closes, fixed,
# resolves, ...) for an issue the same text says Refs, or for an issue with a
# "Depends on #S" step still open, would close the parent of a plan before its
# steps land; the hook denies the command and names the line. When GitHub
# can't be asked, a merge is denied and the rest go on with a warning.
# A command that closes nothing costs no GitHub call, and one without any of
# these words costs one bash, with no subprocess.
set -uo pipefail

# In an agent sandbox (IS_SANDBOX=1, hack/agent/Dockerfile) the hook does
# nothing, before it reads its input: the sandbox has no heavy-job slots, no
# cluster lease, no claim and no main to pull (#1894).
[ "${IS_SANDBOX:-}" = 1 ] && exit 0

IFS= read -r -d '' input
# A loose match on the raw JSON with builtins only, as heavy-guard.sh does.
loose='gh.*pr.*(create|edit|merge)|git.*commit|gh.*issue.*close|gh.*api.*issues|github\.sh.*duplicate'
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
closer="gh[[:blank:]]+(issue[[:blank:]]+close|api)|((ba)?sh[[:blank:]]+)?[^[:space:];&|()]*github\.sh[[:blank:]]+duplicate"
what= byhand=
[[ $cmd =~ $sep($pr|$commit)([[:space:]]|$) ]] && what=${BASH_REMATCH[3]}
[[ $cmd =~ $closer ]] && byhand=1
[ -n "$what$byhand" ] || exit 0

# shellcheck source=hack/lib/github.sh
. "$(dirname "${BASH_SOURCE[0]}")/lib/github.sh"
# Only this repository's issues: another repository's #N is not ours to guard.
origin=$(git remote get-url origin 2>/dev/null) || exit 0
[[ ${origin,,} == *"${C4_GITHUB_REPO,,}"* ]] || exit 0

deny() {
  jq -n --arg m "$1" '{hookSpecificOutput: {hookEventName: "PreToolUse", permissionDecision: "deny", permissionDecisionReason: $m}}'
  exit 0
}

# shell_words TEXT: TEXT's shell words with their quotes dropped, and the
# separators between commands (; & | ( ) and newlines) as ";", each ended by
# a NUL. A quote that never closes is skipped.
shell_words() {
  local s=$1 w q="'" re
  re="^[[:blank:]]*(([^[:space:]\"$q;&|()\\]|\"([^\"\\]|\\\\.)*\"|$q[^$q]*$q|\\\\.)+|[;&|()]+|$nl)"
  while [ -n "$s" ]; do
    if [[ $s =~ $re ]]; then
      w=${BASH_REMATCH[1]} s=${s:${#BASH_REMATCH[0]}}
      if [[ $w == [\;\&\|\(\)]* || $w == "$nl" ]]; then w=';'; else w=${w//[\"\']/}; fi
      printf '%s\0' "$w"
    else
      s=${s:1}
    fi
  done
}

# no_heredocs TEXT: TEXT without its heredocs' bodies, which are text (a pull
# request's body, a commit message), not commands.
no_heredocs() {
  local line end= re="(^|[^<])<<-?[[:blank:]]*[\"']?\\\\?([[:alnum:]_]+)"
  while IFS= read -r line || [ -n "$line" ]; do
    if [ -n "$end" ]; then
      [ "${line#"${line%%[!$'\t']*}"}" != "$end" ] || end=
      continue
    fi
    printf '%s\n' "$line"
    [[ $line =~ $re ]] && end=${BASH_REMATCH[2]}
  done <<<"$1"
}

# ours REPO: REPO (OWNER/REPO, HOST/OWNER/REPO, gh api's {owner}/{repo}, a
# $VAR, or empty for the current one) is this repository.
ours() {
  [[ -z $1 || $1 == *[{:\$]* || ${1,,} == "${C4_GITHUB_REPO,,}" || ${1,,} == */"${C4_GITHUB_REPO,,}" ]]
}

# closed_by_hand WORD...: if the command WORD... closes one of this
# repository's issues at once, add its number to hand, or the command to
# unsure when its words don't say which issue.
hand=() unsure=()
closed_by_hand() {
  local all n= repo= ep= skip= closed= input= w body
  # Words before the command: VAR=value, and keywords (for ...; do gh ...).
  while [[ ${1:-} =~ ^[[:alnum:]_]+= || ${1:-} == @(do|then|else|elif|if|while|until|!|\{|time|command|exec|env|nohup) ]]; do shift; done
  [[ ${1:-} == @(bash|sh) && ${2:-} == *github.sh ]] && shift
  all="$*"
  if [[ ${1:-} == *github.sh && ${2:-} == duplicate ]]; then
    n=${3:-}
  elif [ "${1:-} ${2:-} ${3:-}" = "gh issue close" ]; then
    shift 3
    for w; do
      if [ -n "$skip" ]; then
        [ "$skip" = repo ] && repo=$w
        skip=
        continue
      fi
      case $w in
        -R | --repo) skip=repo ;;
        --repo=*) repo=${w#*=} ;;
        -c | --comment | -r | --reason | --duplicate-of) skip=1 ;;
        -*) ;;
        *) [ -n "$n" ] || n=${w#\#} ;;
      esac
    done
    [[ $n =~ github\.com/([^/]+/[^/]+)/issues/([0-9]+) ]] && repo=${BASH_REMATCH[1]} n=${BASH_REMATCH[2]}
  elif [ "${1:-} ${2:-}" = "gh api" ]; then
    shift 2
    for w; do
      if [ -n "$skip" ]; then
        case $skip in field) [ "$w" != state=closed ] || closed=1 ;; input) input=$w ;; esac
        skip=
        continue
      fi
      case $w in
        -f | -F | --field | --raw-field) skip=field ;;
        --field=* | --raw-field=*) [ "${w#*=}" != state=closed ] || closed=1 ;;
        -f* | -F*) [ "${w:2}" != state=closed ] || closed=1 ;;
        --input) skip=input ;;
        --input=*) input=${w#*=} ;;
        -X | --method | -H | --header | -q | --jq | -t | --template | -p | --preview | --hostname | --cache) skip=1 ;;
        -*) ;;
        *) [ -n "$ep" ] || ep=$w ;;
      esac
    done
    # A JSON body: the file --input names, or for --input - the command itself
    # (a heredoc or a pipe).
    if [ -z "$closed" ] && [ -n "$input" ]; then
      body=
      if [ "$input" = - ]; then body=$cmd; elif [ -f "$input" ]; then body=$(cat -- "$input"); fi
      [[ $body =~ \"state\"[[:space:]]*:[[:space:]]*\"closed\" ]] && closed=1
    fi
    [ -n "$closed" ] || return 0
    [[ $ep =~ ^(https://api\.github\.com/|/)?repos/(.+)/issues/([^/?]+)/?(\?.*)?$ ]] || return 0
    repo=${BASH_REMATCH[2]} n=${BASH_REMATCH[3]}
  else
    return 0
  fi
  [ -n "$n" ] || return 0
  ours "$repo" || return 0
  if [[ $n =~ ^[0-9]+$ ]]; then hand+=("$n"); else unsure+=("$all"); fi
}

if [ -n "$byhand" ]; then
  c=()
  while IFS= read -r -d '' w; do
    if [ "$w" = ';' ]; then
      closed_by_hand "${c[@]}"
      c=()
    else
      c+=("$w")
    fi
  done < <(shell_words "$(no_heredocs "$cmd")"; printf ';\0')
  [ ${#unsure[@]} -eq 0 ] ||
    deny "close-guard: could not tell which issue \`${unsure[0]}\` closes, so it could not check that no step of it is still open. Name the issue by its number."
  refused= lost=
  for n in $(printf '%s\n' "${hand[@]}" | sort -nu); do
    out=$(gh_close_check "$n" "${hand[@]}" 2>&1)
    case $? in
      0) ;;
      1) refused+=${refused:+$nl}$out ;;
      *) lost+=${lost:+ }$out ;;
    esac
  done
  [ -z "$refused" ] || deny "$refused"
  [ -z "$lost" ] || deny "close-guard: GitHub could not be asked whether the issues this command closes still have open steps; try again. $lost"
fi
[ -n "$what" ] || exit 0

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
