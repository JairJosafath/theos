#!/usr/bin/env bash
# Files a project's backlog into its Gitea repositories, as a planner would: one
# issue per Markdown file under DIR/<container>/, in file order, with labels, a
# milestone and dependencies from the file's front matter:
#
#   ---
#   title: Find the CEL expressions in Kubernetes YAML
#   labels: status/ready, kind/feature
#   milestone: M1 Diagnostics
#   depends: 01-locate-cel, other-container#03-schemas
#   ---
#   ## Context ...
#
# A dependency names another file by its name without .md, in the same folder
# or as <container>#<name>; it becomes a Gitea issue dependency ("blocked by").
# Safe to repeat: an issue whose title exists is left as it is, and existing
# dependencies are kept.
#
# Usage: GITEA_URL=... GITEA_TOKEN=... file-issues.sh PROJECT DIR
set -euo pipefail

PROJECT=${1:?usage: file-issues.sh PROJECT DIR}
DIR=${2:?usage: file-issues.sh PROJECT DIR}
: "${GITEA_URL:?set GITEA_URL}" "${GITEA_TOKEN:?set GITEA_TOKEN}"

log() { printf '%s backlog: %s\n' "$(date -u +%H:%M:%S)" "$*"; }
die() { log "ERROR: $*" >&2; exit 1; }

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
# The token stays off the command line: curl reads the header from a file.
(umask 077 && printf 'Authorization: token %s\n' "$GITEA_TOKEN" > "$WORK/auth")

# api METHOD PATH [JSON]: sets STATUS and BODY.
api() {
  local method=$1 path=$2 data=${3:-}
  local args=(-sS -o "$WORK/body" -w '%{http_code}' -X "$method"
    -H @"$WORK/auth" -H 'Accept: application/json')
  if [[ -n $data ]]; then
    args+=(-H 'Content-Type: application/json' --data "$data")
  fi
  STATUS=$(curl "${args[@]}" "$GITEA_URL/api/v1$path")
  BODY=$(cat "$WORK/body")
}

expect() {
  local what=$1 code
  shift
  for code; do [[ $STATUS == "$code" ]] && return 0; done
  die "$what: HTTP $STATUS: $BODY"
}

# front FILE KEY: a front matter value, or "".
front() {
  awk -v key="$2" 'NR == 1 && $0 == "---" { inside = 1; next }
    inside && $0 == "---" { exit }
    inside && index($0, key ":") == 1 { sub("^" key ":[ ]*", ""); print; exit }' "$1"
}

# body FILE: everything after the front matter.
body() {
  awk 'NR == 1 && $0 == "---" { inside = 1; next }
    inside && $0 == "---" { inside = 0; started = 1; next }
    started' "$1" | sed '/./,$!d'
}

# list "a, b": one item per line.
list() { tr ',' '\n' <<<"$1" | sed 's/^ *//; s/ *$//' | sed '/^$/d'; }

# The labels the factory and the planner use. status/* are exclusive: an issue is
# in one state at a time. The Crew controller creates the status labels too.
LABELS='[
  {"name": "status/proposed", "color": "#c5def5", "exclusive": true, "description": "Filed, not yet triaged; agents don'"'"'t pick it up"},
  {"name": "status/ready", "color": "#0e8a16", "exclusive": true, "description": "Clear and ready: the Crew picks it up when its dependencies are closed"},
  {"name": "status/in-progress", "color": "#fbca04", "exclusive": true, "description": "A Task is working on it"},
  {"name": "status/needs-human", "color": "#d93f0b", "exclusive": true, "description": "Agents can'"'"'t go on; a comment says why"},
  {"name": "kind/feature", "color": "#1d76db", "exclusive": true, "description": "New behaviour for users"},
  {"name": "kind/bug", "color": "#b60205", "exclusive": true, "description": "Behaviour that is wrong"},
  {"name": "kind/docs", "color": "#5319e7", "exclusive": true, "description": "Documentation only"},
  {"name": "kind/chore", "color": "#bfdadc", "exclusive": true, "description": "Upkeep: dependencies, tooling"}
]'

# ensure_labels REPO: creates the labels the repository lacks; leaves IDs in $WORK/labels-REPO.
ensure_labels() {
  local repo=$1
  api GET "/repos/$PROJECT/$repo/labels?limit=100"
  expect "Listing labels of $repo" 200
  local existing=$BODY name
  while read -r label; do
    name=$(jq -r .name <<<"$label")
    if ! jq -e --arg n "$name" 'any(.[]; .name == $n)' <<<"$existing" >/dev/null; then
      api POST "/repos/$PROJECT/$repo/labels" "$label"
      expect "Creating label $name in $repo" 201
      log "Created label $name in $repo"
    fi
  done < <(jq -c '.[]' <<<"$LABELS")
  api GET "/repos/$PROJECT/$repo/labels?limit=100"
  expect "Listing labels of $repo" 200
  printf '%s' "$BODY" > "$WORK/labels-$repo"
}

# milestone REPO TITLE: prints the milestone's ID, creating it if needed.
milestone() {
  local repo=$1 title=$2 id
  api GET "/repos/$PROJECT/$repo/milestones?state=all&limit=100"
  expect "Listing milestones of $repo" 200
  id=$(jq -r --arg t "$title" 'map(select(.title == $t))[0].id // empty' <<<"$BODY")
  if [[ -z $id ]]; then
    api POST "/repos/$PROJECT/$repo/milestones" "$(jq -nc --arg t "$title" '{title: $t}')"
    expect "Creating milestone $title in $repo" 201
    id=$(jq -r .id <<<"$BODY")
    log "Created milestone $title in $repo" >&2
  fi
  printf '%s' "$id"
}

# existing REPO TITLE: prints the number of the issue with that title, or "".
existing() {
  local repo=$1 title=$2 page=1 n
  while :; do
    api GET "/repos/$PROJECT/$repo/issues?state=all&type=issues&limit=50&page=$page"
    expect "Listing issues of $repo" 200
    n=$(jq -r --arg t "$title" 'map(select(.title == $t))[0].number // empty' <<<"$BODY")
    if [[ -n $n || $(jq length <<<"$BODY") -lt 50 ]]; then
      printf '%s' "$n"
      return
    fi
    page=$((page + 1))
  done
}

declare -A NUMBER # container#name -> issue number

file_issues() {
  local repo=$1 file name title labels ms ids n
  ensure_labels "$repo"
  for file in "$DIR/$repo"/*.md; do
    name=$(basename "$file" .md)
    title=$(front "$file" title)
    [[ -n $title ]] || die "$file has no title"
    n=$(existing "$repo" "$title")
    if [[ -n $n ]]; then
      log "$repo#$n exists: $title"
      NUMBER[$repo#$name]=$n
      continue
    fi
    labels=$(front "$file" labels)
    ids=$(list "$labels" | jq -R . | jq -sc --slurpfile all "$WORK/labels-$repo" \
      '[.[] as $n | $all[0][] | select(.name == $n) | .id]')
    [[ $(jq length <<<"$ids") == $(list "$labels" | wc -l) ]] || die "$file: unknown label in '$labels'"
    ms=$(front "$file" milestone)
    local payload
    payload=$(jq -nc --arg t "$title" --rawfile b <(body "$file") --argjson l "$ids" \
      '{title: $t, body: $b, labels: $l}')
    if [[ -n $ms ]]; then
      payload=$(jq -c --argjson m "$(milestone "$repo" "$ms")" '. + {milestone: $m}' <<<"$payload")
    fi
    api POST "/repos/$PROJECT/$repo/issues" "$payload"
    expect "Filing $file" 201
    n=$(jq -r .number <<<"$BODY")
    NUMBER[$repo#$name]=$n
    log "Filed $repo#$n: $title"
  done
}

link_dependencies() {
  local repo=$1 file name dep dep_repo dep_name blocker n
  for file in "$DIR/$repo"/*.md; do
    name=$(basename "$file" .md)
    n=${NUMBER[$repo#$name]}
    api GET "/repos/$PROJECT/$repo/issues/$n/dependencies?limit=50"
    expect "Listing dependencies of $repo#$n" 200
    local have=$BODY
    while read -r dep; do
      [[ -n $dep ]] || continue
      if [[ $dep == *"#"* ]]; then dep_repo=${dep%%#*} dep_name=${dep#*#}; else dep_repo=$repo dep_name=$dep; fi
      blocker=${NUMBER[$dep_repo#$dep_name]:-}
      [[ -n $blocker ]] || die "$file depends on $dep, which is not in the backlog"
      if jq -e --arg r "$PROJECT/$dep_repo" --argjson b "$blocker" \
        'any(.[]; .repository.full_name == $r and .number == $b)' <<<"$have" >/dev/null; then
        continue
      fi
      api POST "/repos/$PROJECT/$repo/issues/$n/dependencies" \
        "$(jq -nc --arg o "$PROJECT" --arg r "$dep_repo" --argjson i "$blocker" '{owner: $o, repo: $r, index: $i}')"
      expect "Making $repo#$n depend on $dep_repo#$blocker" 201
      log "$repo#$n now depends on $dep_repo#$blocker"
    done < <(list "$(front "$file" depends)")
  done
}

main() {
  local repos=() repo
  for repo in "$DIR"/*/; do repos+=("$(basename "$repo")"); done
  for repo in "${repos[@]}"; do file_issues "$repo"; done
  for repo in "${repos[@]}"; do link_dependencies "$repo"; done
  log "Done: the backlog of $PROJECT is in Gitea"
}

main
