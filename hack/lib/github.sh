#!/usr/bin/env bash
# GitHub helpers for work on the factory itself. That work is tracked as issues
# on JairJosafath/c4factory, one issue per piece of work, and sessions hand off
# through comments on them (the handoff skill). Product work, the apps the
# factory builds, stays in Gitea, where the Crews work it.
#
# Source it for the gh_* functions (hack/relay.sh, hack/evaluate.sh), or run it:
#
#   hack/lib/github.sh labels            create the labels below (safe to repeat)
#   hack/lib/github.sh pick              the issue to work next: NUMBER TITLE URL STATUS, tab-separated
#   hack/lib/github.sh show N            issue N, its newest handoff comment and the comments after it
#   hack/lib/github.sh take N [WHO]      swap N to status/in-progress and comment that WHO took it
#   hack/lib/github.sh handoff N FILE    post FILE as N's handoff comment; if GitHub can't be
#                                        reached, keep it in .relay/pending-handoff-N.md (exit 3)
#   hack/lib/github.sh flush             post the handoffs kept in .relay/ while GitHub was away
#   hack/lib/github.sh comment N FILE    post FILE as a comment on N
#   hack/lib/github.sh status N STATUS   swap N's status label: proposed, ready, in-progress, needs-human
#   hack/lib/github.sh close N FILE      comment FILE (what landed: SHAs, branch), then close N
#   hack/lib/github.sh create TITLE FILE LABEL...
#                                        file an issue, FILE as its body; prints its number. An open
#                                        issue with the same title is left as it is.
#
# FILE may be - for stdin. Pickup order: an open status/in-progress issue, else
# the oldest status/ready + priority/urgent one, else the oldest status/ready one.
# A handoff comment starts with the line <!-- relay-handoff -->.
#
# Auth: GH_TOKEN, else the git credential helper's github.com password, kept in
# the environment and never printed. Needs gh and jq. C4_GITHUB_REPO overrides
# the repository.

GH_LIB_ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
C4_GITHUB_REPO=${C4_GITHUB_REPO:-JairJosafath/c4factory}
GH_PENDING_DIR=${GH_PENDING_DIR:-$GH_LIB_ROOT/.relay}
GH_HANDOFF_MARKER='<!-- relay-handoff -->'
export GH_PROMPT_DISABLED=1 GH_NO_UPDATE_NOTIFIER=1

# name|color|description, the same words the factory uses in Gitea.
GH_LABELS=(
  'status/proposed|c5def5|Proposed; waits for the user to ready it'
  'status/ready|0e8a16|Ready: the next session may take it'
  'status/in-progress|fbca04|A session works on it; continue from its newest handoff comment'
  'status/needs-human|d93f0b|Blocked on the user; the newest comment says why'
  'priority/urgent|b60205|Picked before every other ready issue'
  'kind/bug|d73a4a|Something is broken'
  'kind/feature|a2eeef|New behaviour'
  'kind/chore|ededed|Upkeep: operations, releases, tooling'
  'kind/docs|0075ca|Documentation'
  'kind/improvement|84b6eb|Make something that works better'
  'source/evaluator|5319e7|Filed by hack/evaluate.sh, the factory evaluator'
)

# gh_token: export GH_TOKEN, from the git credential helper unless already set.
gh_token() {
  if [ -z "${GH_TOKEN:-}" ]; then
    GH_TOKEN=$(printf 'protocol=https\nhost=github.com\n\n' |
      GIT_TERMINAL_PROMPT=0 timeout 30 git credential fill 2>/dev/null |
      sed -n 's/^password=//p') || true
  fi
  if [ -z "${GH_TOKEN:-}" ]; then
    echo "github: no token: GH_TOKEN is unset and the git credential helper gave none" >&2
    return 1
  fi
  export GH_TOKEN
}

# gh_api ARGS...: gh api against GitHub with the token.
gh_api() {
  gh_token || return 1
  gh api -H 'Accept: application/vnd.github+json' "$@"
}

# gh_read FILE: FILE's contents, or stdin for -.
gh_read() {
  if [ "${1:--}" = - ]; then cat; else cat -- "$1"; fi
}

gh_labels() {
  local have line name color desc
  have=$(gh_api --paginate "repos/$C4_GITHUB_REPO/labels?per_page=100" --jq '.[].name') || return 1
  for line in "${GH_LABELS[@]}"; do
    IFS='|' read -r name color desc <<<"$line"
    if grep -qxF -- "$name" <<<"$have"; then
      echo "label $name: exists"
      continue
    fi
    gh_api --method POST "repos/$C4_GITHUB_REPO/labels" \
      -f name="$name" -f color="$color" -f description="$desc" >/dev/null || return 1
    echo "label $name: created"
  done
}

# gh_open_issues LABELS: open issues (no pull requests) carrying every label in
# the comma-separated LABELS, oldest first, as NUMBER TITLE URL lines (tab-separated).
gh_open_issues() {
  gh_api --paginate \
    "repos/$C4_GITHUB_REPO/issues?state=open&labels=$1&sort=created&direction=asc&per_page=100" \
    --jq '.[] | select(.pull_request | not) | [.number, .title, .html_url] | @tsv'
}

# gh_pick: the issue to work next, as NUMBER TITLE URL STATUS (tab-separated),
# STATUS being in-progress, ready-urgent or ready. Exit 1: nothing to pick;
# exit 2: GitHub could not be asked.
gh_pick() {
  local labels rows n
  for labels in status/in-progress status/ready,priority/urgent status/ready; do
    rows=$(gh_open_issues "$labels") || return 2
    [ -n "$rows" ] || continue
    n=$(wc -l <<<"$rows")
    if [ "$labels" = status/in-progress ] && [ "$n" -gt 1 ]; then
      echo "github: $n issues are in progress; taking the oldest" >&2
    fi
    case $labels in
      status/in-progress) printf '%s\tin-progress\n' "$(head -1 <<<"$rows")" ;;
      status/ready,*) printf '%s\tready-urgent\n' "$(head -1 <<<"$rows")" ;;
      *) printf '%s\tready\n' "$(head -1 <<<"$rows")" ;;
    esac
    return 0
  done
  return 1
}

# gh_comments N: issue N's comments, oldest first, as one JSON array.
gh_comments() {
  local pages
  pages=$(gh_api --paginate --slurp "repos/$C4_GITHUB_REPO/issues/$1/comments?per_page=100") || return 1
  jq -c '[.[][] | {id, created_at, html_url, user: .user.login, body}]' <<<"$pages"
}

# gh_latest_handoff N: the newest handoff comment on N as JSON, or nothing.
gh_latest_handoff() {
  local all
  all=$(gh_comments "$1") || return 1
  jq -c --arg m "$GH_HANDOFF_MARKER" \
    '[.[] | select(.body | sub("^\\s+"; "") | startswith($m))] | last // empty' <<<"$all"
}

# gh_status_of N: N's status label without the status/ prefix, or nothing.
gh_status_of() {
  gh_api "repos/$C4_GITHUB_REPO/issues/$1" \
    --jq '[.labels[].name | select(startswith("status/")) | ltrimstr("status/")] | first // empty'
}

gh_show() {
  local n=${1:?usage: show N} issue all
  issue=$(gh_api "repos/$C4_GITHUB_REPO/issues/$n") || return 1
  all=$(gh_comments "$n") || return 1
  jq -r '"#\(.number) \(.title)\n\(.html_url)\n\(.state); labels: \(.labels | map(.name) | join(", "))\n\n\(.body // "")"' <<<"$issue"
  jq -r --arg m "$GH_HANDOFF_MARKER" '
    def handoff: .body | sub("^\\s+"; "") | startswith($m);
    ([to_entries[] | select(.value | handoff) | .key] | last) as $i
    | if $i == null then
        "\n--- no handoff comment yet; \(length) other comment(s) ---",
        (.[] | "\n--- comment by \(.user), \(.created_at) ---\n\(.body)")
      else
        "\n--- newest handoff comment, \(.[$i].created_at), \(.[$i].html_url) ---\n\(.[$i].body)",
        (.[$i + 1:][] | "\n--- comment after it, by \(.user), \(.created_at) ---\n\(.body)")
      end' <<<"$all"
}

# gh_comment N FILE: post FILE as a comment on N; prints the comment's URL.
gh_comment() {
  local n=${1:?usage: comment N FILE} body
  body=$(gh_read "${2:--}") || return 1
  [ -n "$body" ] || { echo "github: empty comment" >&2; return 1; }
  jq -n --arg b "$body" '{body: $b}' |
    gh_api --method POST "repos/$C4_GITHUB_REPO/issues/$n/comments" --input - --jq .html_url
}

# gh_set_status N STATUS: make status/STATUS N's only status label.
gh_set_status() {
  local n=${1:?usage: status N STATUS} want labels l
  case ${2:-} in
    proposed | ready | in-progress | needs-human) want=status/$2 ;;
    *) echo "github: status must be proposed, ready, in-progress or needs-human" >&2; return 1 ;;
  esac
  labels=$(gh_api "repos/$C4_GITHUB_REPO/issues/$n/labels?per_page=100" --jq '.[].name') || return 1
  while IFS= read -r l; do
    [[ $l == status/* && $l != "$want" ]] || continue
    gh_api --method DELETE "repos/$C4_GITHUB_REPO/issues/$n/labels/${l//\//%2F}" >/dev/null || return 1
  done <<<"$labels"
  grep -qxF -- "$want" <<<"$labels" && return 0
  jq -n --arg l "$want" '{labels: [$l]}' |
    gh_api --method POST "repos/$C4_GITHUB_REPO/issues/$n/labels" --input - >/dev/null
}

# gh_take N [WHO]: swap N to status/in-progress and comment that WHO took it.
gh_take() {
  local n=${1:?usage: take N [WHO]} who=${2:-A session} prev
  prev=$(gh_status_of "$n") || return 1
  gh_set_status "$n" in-progress || return 1
  if [ "$prev" = in-progress ]; then
    printf '%s continues this issue from its newest handoff comment.\n' "$who" | gh_comment "$n" - >/dev/null
  else
    printf '%s took this issue (it was %s).\n' "$who" "${prev:-unlabelled}" | gh_comment "$n" - >/dev/null
  fi
}

# gh_handoff N FILE: post FILE as N's handoff comment, the marker line first.
# If GitHub can't be reached, keep the text in .relay/pending-handoff-N.md and
# exit 3; gh_flush posts it later. A newer handoff replaces an unposted older
# one: each handoff is a full snapshot.
gh_handoff() {
  local n=${1:?usage: handoff N FILE} body pending url
  body=$(gh_read "${2:--}") || return 1
  [ -n "$body" ] || { echo "github: empty handoff" >&2; return 1; }
  [ "$(head -1 <<<"$body")" = "$GH_HANDOFF_MARKER" ] || body="$GH_HANDOFF_MARKER"$'\n'"$body"
  pending="$GH_PENDING_DIR/pending-handoff-$n.md"
  if url=$(printf '%s\n' "$body" | gh_comment "$n" -); then
    rm -f "$pending"
    echo "$url"
    return 0
  fi
  mkdir -p "$GH_PENDING_DIR"
  [ -e "$GH_PENDING_DIR/.gitignore" ] || echo '*' >"$GH_PENDING_DIR/.gitignore"
  printf '%s\n' "$body" >"$pending"
  echo "github: could not post the handoff; kept it in $pending for the next session (hack/lib/github.sh flush)" >&2
  return 3
}

# gh_flush: post every handoff kept in .relay/; exit 1 if any is still there.
gh_flush() {
  local f n rc=0
  for f in "$GH_PENDING_DIR"/pending-handoff-*.md; do
    [ -e "$f" ] || continue
    n=${f##*/pending-handoff-}
    n=${n%.md}
    if gh_comment "$n" "$f" >/dev/null; then
      rm -f "$f"
      echo "posted the pending handoff on #$n"
    else
      echo "github: still could not post $f" >&2
      rc=1
    fi
  done
  return $rc
}

# gh_close N FILE: comment what landed, then close N as completed.
gh_close() {
  local n=${1:?usage: close N FILE}
  gh_comment "$n" "${2:--}" || return 1
  gh_api --method PATCH "repos/$C4_GITHUB_REPO/issues/$n" \
    -f state=closed -f state_reason=completed --jq '"closed #\(.number)"'
}

# gh_create TITLE FILE LABEL...: file an issue and print its number. An open
# issue with exactly this title is left as it is, and its number printed.
gh_create() {
  local title=${1:?usage: create TITLE FILE LABEL...} file=${2:?usage: create TITLE FILE LABEL...} body found
  shift 2
  found=$(gh_api --paginate "repos/$C4_GITHUB_REPO/issues?state=open&per_page=100" \
    --jq ".[] | select((.pull_request | not) and .title == $(jq -n --arg t "$title" '$t')) | .number" |
    head -1) || return 1
  if [ -n "$found" ]; then
    echo "github: an open issue with this title exists: #$found" >&2
    echo "$found"
    return 0
  fi
  body=$(gh_read "$file") || return 1
  jq -n --arg t "$title" --arg b "$body" '{title: $t, body: $b, labels: $ARGS.positional}' --args "$@" |
    gh_api --method POST "repos/$C4_GITHUB_REPO/issues" --input - --jq .number
}

if [ "${BASH_SOURCE[0]}" = "$0" ]; then
  set -uo pipefail
  cmd=${1:-help}
  shift || true
  case $cmd in
    labels) gh_labels ;;
    pick) gh_pick ;;
    show) gh_show "$@" ;;
    take) gh_take "$@" ;;
    handoff) gh_handoff "$@" ;;
    flush) gh_flush ;;
    comment) gh_comment "$@" ;;
    status) gh_set_status "$@" ;;
    close) gh_close "$@" ;;
    create) gh_create "$@" ;;
    *)
      sed -n '2,/^$/{s/^# \{0,1\}//;p}' "$0"
      [ "$cmd" = help ] || exit 64
      ;;
  esac
fi
