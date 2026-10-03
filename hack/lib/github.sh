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
#   hack/lib/github.sh take N            claim N for this checkout and swap it to status/in-progress;
#                                        prints NAME BRANCH, tab-separated. Exit 4: someone else holds it,
#                                        or it needs the cluster and this device can't give it one now.
#                                        This checkout's claim on a closed issue is released first
#   hack/lib/github.sh release [REASON]  release this checkout's claim (default reason: released)
#   hack/lib/github.sh claims            the live and stale claims on open issues
#   hack/lib/github.sh beat              refresh this checkout's claim heartbeat, at most every 15
#                                        minutes. Exit 2: the claim was taken over; stop working
#   hack/lib/github.sh handoff N FILE    post FILE as N's handoff comment and release the claim; if
#                                        GitHub can't be reached, keep it in .relay/pending-handoff-N.md (exit 3)
#   hack/lib/github.sh flush             post the handoffs kept in .relay/ while GitHub was away
#   hack/lib/github.sh comment N FILE    post FILE as a comment on N
#   hack/lib/github.sh label N LABEL...  add labels to N. Exit 4 after adding needs/cluster: another
#                                        checkout on this device holds the cluster
#   hack/lib/github.sh status N STATUS   swap N's status label: proposed, ready, in-progress, needs-human
#                                        (needs-human releases the claim)
#   hack/lib/github.sh close N FILE      comment FILE (what landed: SHAs, branch), release the claim, close N
#   hack/lib/github.sh create TITLE FILE LABEL...
#                                        file an issue, FILE as its body; prints its number. An open
#                                        issue with the same title is left as it is.
#
# FILE may be - for stdin. Pickup order, oldest first: a status/in-progress
# issue without a live claim, else a status/ready + priority/urgent one, else a
# status/ready one; issues with a live claim, or whose body says "Depends on #M"
# while #M is open, are skipped. A handoff comment starts with the line
# <!-- relay-handoff -->.
#
# The Kind cluster: issues whose work needs it carry needs/cluster. On a device
# without one (C4_CLUSTER=none) pick skips them and take refuses them. On a
# device with one, a single checkout works a needs/cluster issue at a time:
# while another checkout (git worktree list) holds a claim on one, beaten within
# C4_LEASE_TTL, pick skips them and take refuses them.
# hack/lib/github_test.sh tests these rules against a stubbed GitHub.
#
# Claims (docs/plans/parallel-agents.md): taking an issue posts a comment that
# starts with <!-- c4-claim --> and names the agent (otter-23), host, branch and
# session. It is live while it says "works on this issue" and was edited within
# C4_LEASE_TTL (2h; 90m, 300s and plain seconds work too). The local half is
# .relay/claim; a checkout holds one claim at a time. C4_HOST names the host
# (default: hostname), C4_SESSION_ID the session (default: CLAUDE_CODE_SESSION_ID).
#
# Auth: GH_TOKEN, else the git credential helper's github.com password, kept in
# the environment and never printed. Needs gh and jq. C4_GITHUB_REPO overrides
# the repository.

GH_LIB_ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
C4_GITHUB_REPO=${C4_GITHUB_REPO:-JairJosafath/c4factory}
GH_PENDING_DIR=${GH_PENDING_DIR:-$GH_LIB_ROOT/.relay}
GH_HANDOFF_MARKER='<!-- relay-handoff -->'
GH_CLAIM_MARKER='<!-- c4-claim -->'
GH_CLAIM_FILE=${GH_CLAIM_FILE:-$GH_PENDING_DIR/claim}
C4_LEASE_TTL=${C4_LEASE_TTL:-2h}
GH_CLAIM_SETTLE=${GH_CLAIM_SETTLE:-5}  # seconds take waits before checking it won
GH_BEAT_EVERY=${GH_BEAT_EVERY:-900}    # seconds between heartbeats
export GH_PROMPT_DISABLED=1 GH_NO_UPDATE_NOTIFIER=1

# Agent names are WORD-N; the Nth agent on an issue gets the Nth unused word.
GH_NAMES=(otter heron lynx wren finch badger marten plover stoat ibis kestrel
  vole puffin newt egret ferret curlew shrew tern hare dipper robin gecko crane)

# jq definitions over comments ({id, updated_at, body}); need --arg cm and --argjson ttl.
GH_CLAIM_JQ='
  def claim: .body | sub("^\\s+"; "") | startswith($cm);
  def held: .body | test("\\*\\* works on this issue");
  def fresh: (.updated_at | fromdateiso8601) > (now - $ttl);
  def live: claim and held and fresh;
  def cname: [.body | capture("\\*\\*(?<n>[^*]+)\\*\\*") | .n] | first // "";
  def cfield($f): [.body | capture($f + " `(?<v>[^`]+)`") | .v] | first // "";
  def age: (now - (.updated_at | fromdateiso8601)) / 60 | floor | "\(.)m ago";
'

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
  'needs/cluster|1d76db|Needs the Kind cluster: deploys, factory-ops, Crews or e2e'
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

# gh_ttl: C4_LEASE_TTL in seconds.
gh_ttl() {
  local t=$C4_LEASE_TTL
  [[ $t =~ ^[0-9]+[smh]?$ ]] || { echo "github: C4_LEASE_TTL must look like 2h, 90m, 300s or 300" >&2; return 1; }
  case $t in
    *h) echo $((${t%h} * 3600)) ;;
    *m) echo $((${t%m} * 60)) ;;
    *) echo "${t%s}" ;;
  esac
}

# gh_recent_claims SECONDS: the claim comments on any issue edited in the last
# SECONDS, as one JSON array of {id, issue, updated_at, html_url, body}.
gh_recent_claims() {
  local since pages
  since=$(date -u -d "@$(($(date +%s) - $1))" +%Y-%m-%dT%H:%M:%SZ)
  pages=$(gh_api --paginate --slurp "repos/$C4_GITHUB_REPO/issues/comments?since=$since&per_page=100") || return 1
  jq -c --arg cm "$GH_CLAIM_MARKER" --argjson ttl 0 "$GH_CLAIM_JQ"'
    [.[][] | select(claim) | {id, issue: (.issue_url | sub(".*/"; "") | tonumber), updated_at, html_url, body}]' <<<"$pages"
}

# gh_pick: the issue to work next, as NUMBER TITLE URL STATUS (tab-separated),
# STATUS being in-progress, ready-urgent or ready. Exit 1: nothing to pick;
# exit 2: GitHub could not be asked.
gh_pick() {
  local ttl issues claims row nocluster=false
  ttl=$(gh_ttl) || return 2
  if [ "${C4_CLUSTER:-}" = none ] || [ -n "$(gh_cluster_holder)" ]; then nocluster=true; fi
  issues=$(gh_api --paginate --slurp \
    "repos/$C4_GITHUB_REPO/issues?state=open&sort=created&direction=asc&per_page=100") || return 2
  claims=$(gh_recent_claims "$ttl") || return 2
  row=$(jq -r --arg cm "$GH_CLAIM_MARKER" --argjson ttl "$ttl" --argjson claims "$claims" \
    --argjson nocluster "$nocluster" "$GH_CLAIM_JQ"'
    def has($l): .labels | any(. == $l);
    [.[][]] as $all
    | [$all[].number] as $open
    | [$claims[] | select(live) | .issue] as $taken
    | [$all[] | select(.pull_request | not)
        | select(.number as $n | $taken | any(. == $n) | not)
        | select([(.body // "") | scan("[Dd]epends on #([0-9]+)") | .[0] | tonumber]
            | any(. as $d | $open | any(. == $d)) | not)
        | {number, title, html_url, labels: [.labels[].name]}
        | select($nocluster and has("needs/cluster") | not)] as $free
    | [($free[] | select(has("status/in-progress")) | . + {how: "in-progress"}),
       ($free[] | select(has("status/ready") and has("priority/urgent")) | . + {how: "ready-urgent"}),
       ($free[] | select(has("status/ready")) | . + {how: "ready"})]
    | first // empty | [.number, .title, .html_url, .how] | @tsv' <<<"$issues") || return 2
  [ -n "$row" ] || return 1
  echo "$row"
}

# gh_comments N: issue N's comments, oldest first, as one JSON array.
gh_comments() {
  local pages
  pages=$(gh_api --paginate --slurp "repos/$C4_GITHUB_REPO/issues/$1/comments?per_page=100") || return 1
  jq -c '[.[][] | {id, created_at, updated_at, html_url, user: .user.login, body}]' <<<"$pages"
}

# gh_latest_handoff N: the newest handoff comment on N as JSON, or nothing.
gh_latest_handoff() {
  local all
  all=$(gh_comments "$1") || return 1
  jq -c --arg m "$GH_HANDOFF_MARKER" \
    '[.[] | select(.body | sub("^\\s+"; "") | startswith($m))] | last // empty' <<<"$all"
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
  if ! grep -qxF -- "$want" <<<"$labels"; then
    jq -n --arg l "$want" '{labels: [$l]}' |
      gh_api --method POST "repos/$C4_GITHUB_REPO/issues/$n/labels" --input - >/dev/null || return 1
  fi
  [ "$want" != status/needs-human ] || gh_release_for "$n" needs-human
}

# gh_edit_comment ID BODY: replace comment ID's body.
gh_edit_comment() {
  jq -n --arg b "$2" '{body: $b}' |
    gh_api --method PATCH "repos/$C4_GITHUB_REPO/issues/comments/$1" --input - >/dev/null
}

# gh_claim_comment ID: comment ID as JSON {id, updated_at, body}, or nothing
# when it was deleted. Exit 1: GitHub could not be asked.
gh_claim_comment() {
  local out
  if out=$(gh_api "repos/$C4_GITHUB_REPO/issues/comments/$1" --jq '{id, updated_at, body}' 2>&1); then
    echo "$out"
    return 0
  fi
  grep -q 'HTTP 404' <<<"$out" && return 0
  echo "$out" >&2
  return 1
}

# gh_claim_read: set c_issue, c_id, c_name and c_branch from .relay/claim.
# Exit 1: this checkout holds no claim.
gh_claim_read() {
  [ -s "$GH_CLAIM_FILE" ] || return 1
  IFS=$'\t' read -r c_issue c_id c_name c_branch < <(jq -r '[.issue, .id, .name, .branch] | @tsv' "$GH_CLAIM_FILE")
  [ -n "$c_id" ]
}

# gh_claim_held: print this checkout's claim comment if it still says c_name
# works on the issue (past the TTL too: nobody has taken it over yet). Exit 1:
# released, taken over or deleted; exit 2: GitHub could not be asked.
gh_claim_held() {
  local c
  c=$(gh_claim_comment "$c_id") || return 2
  [ -n "$c" ] || return 1
  jq -e --arg cm "$GH_CLAIM_MARKER" --argjson ttl 0 --arg name "$c_name" \
    "$GH_CLAIM_JQ"'held and cname == $name' <<<"$c" >/dev/null || return 1
  echo "$c"
}

# gh_claim_body NAME BRANCH: a live claim comment's body.
gh_claim_body() {
  local session=${C4_SESSION_ID:-${CLAUDE_CODE_SESSION_ID:-}} line
  line="**$1** works on this issue · host \`${C4_HOST:-$(hostname)}\` · branch \`$2\`"
  [ -z "$session" ] || line+=" · \`claude --resume $session\`"
  printf '%s\n%s\nHeartbeat: %s\n' "$GH_CLAIM_MARKER" "$line" "$(date -u +%Y-%m-%dT%H:%MZ)"
}

# gh_branch_name: the branch for the issue JSON on stdin, <type>/<N>-<slug>, the
# type from its kind/* label (bug is fix, feature and improvement are feat).
gh_branch_name() {
  jq -r '
    ([.labels[].name | select(startswith("kind/")) | ltrimstr("kind/")] | first // "") as $k
    | ({bug: "fix", feature: "feat", improvement: "feat", chore: "chore", docs: "docs"}[$k] // "feat") as $t
    | ([.title | ascii_downcase | scan("[a-z0-9]+")]
      | reduce .[] as $w ({s: "", done: false};
          if .done then .
          elif .s == "" then .s = $w
          elif (.s | length) + 1 + ($w | length) <= 40 then .s += "-" + $w
          else .done = true end)
      | .s) as $slug
    | "\($t)/\(.number)-\(if $slug == "" then "work" else $slug end)"'
}

# gh_device_locked CMD ARGS...: run CMD under a lock shared by the device's
# checkouts (in the git common dir), so two of them can't both pass the
# one-cluster-issue check before either has written its claim or label.
gh_device_locked() {
  local dir fd rc
  dir=$(git -C "$GH_LIB_ROOT" rev-parse --path-format=absolute --git-common-dir 2>/dev/null) ||
    { "$@"; return; }
  exec {fd}>>"$dir/c4-take.lock"
  flock -w 120 "$fd" || echo "github: waited 2 minutes for $dir/c4-take.lock; going on without it" >&2
  "$@"
  rc=$?
  exec {fd}>&-
  return $rc
}

# gh_take N: claim N for this checkout, under the device lock.
gh_take() { gh_device_locked gh_take_locked "$@"; }

# gh_take_locked N: claim N for this checkout and print NAME BRANCH (tab-separated).
# A claim this checkout already holds on N is printed again; one it holds on a
# closed issue is released first. Exit 4: someone
# else holds N, or won the race to it; exit 1: this checkout holds another
# issue, or GitHub could not be asked. GH_TAKE_KEEP_STATUS=1 leaves N's status
# label alone (the evaluator's claim on "Factory evaluations").
gh_take_locked() {
  local n=${1:?usage: take N} ttl c_issue c_id c_name c_branch rc state issue all holder used w name branch stale id winner
  ttl=$(gh_ttl) || return 1
  if gh_claim_read; then
    gh_claim_held >/dev/null
    rc=$?
    [ "$rc" = 2 ] && return 1
    if [ "$rc" = 0 ]; then
      if [ "$c_issue" = "$n" ]; then
        printf '%s\t%s\n' "$c_name" "$c_branch"
        return 0
      fi
      # A merged pull request saying "Closes #N" closes N but leaves its claim.
      state=$(gh_api "repos/$C4_GITHUB_REPO/issues/$c_issue" --jq .state) || return 1
      if [ "$state" != closed ]; then
        echo "github: this checkout holds #$c_issue as $c_name; release it (hack/lib/github.sh release) or take #$n from another worktree" >&2
        return 1
      fi
      gh_release finished >&2 || return 1
    fi
    rm -f "$GH_CLAIM_FILE"
  fi

  issue=$(gh_api "repos/$C4_GITHUB_REPO/issues/$n") || return 1
  if jq -e '.labels | any(.name == "needs/cluster")' <<<"$issue" >/dev/null; then
    if [ "${C4_CLUSTER:-}" = none ]; then
      echo "github: #$n needs the Kind cluster (needs/cluster) and this device has none (C4_CLUSTER=none)" >&2
      return 4
    fi
    holder=$(gh_cluster_holder)
    if [ -n "$holder" ]; then
      echo "github: #$n needs the Kind cluster, and $holder already works a needs/cluster issue on this device" >&2
      return 4
    fi
  fi
  all=$(gh_comments "$n") || return 1
  local q=(--arg cm "$GH_CLAIM_MARKER" --argjson ttl "$ttl")

  # 1. Someone else holds it.
  holder=$(jq -r "${q[@]}" "$GH_CLAIM_JQ"'
    [.[] | select(live)] | first // empty
    | "\(cname) on host \(cfield("host")), heartbeat \(age)"' <<<"$all")
  if [ -n "$holder" ]; then
    echo "github: #$n is claimed by $holder" >&2
    return 4
  fi

  # The next unused name, and the branch of the newest claim (else a new one).
  used=$(jq -r "${q[@]}" "$GH_CLAIM_JQ"'.[] | select(claim) | cname' <<<"$all")
  name=
  for w in "${GH_NAMES[@]}"; do
    grep -qxF -- "$w-$n" <<<"$used" || { name=$w-$n; break; }
  done
  [ -n "$name" ] || name=agent$(($(grep -c . <<<"$used") + 1))-$n
  branch=$(jq -r "${q[@]}" "$GH_CLAIM_JQ"'
    [.[] | select(claim) | cfield("branch") | select(. != "")] | last // empty' <<<"$all")
  [ -n "$branch" ] || branch=$(gh_branch_name <<<"$issue")

  # 2. Expire the claims past the TTL.
  while IFS=$'\t' read -r id w stale; do
    [ -n "$id" ] || continue
    gh_edit_comment "$id" "$(printf '%s\n**%s** expired, taken over by %s · branch `%s`' \
      "$GH_CLAIM_MARKER" "$w" "$name" "$stale")" || return 1
  done < <(jq -r "${q[@]}" "$GH_CLAIM_JQ"'
    .[] | select(claim and held and (fresh | not)) | [.id, cname, cfield("branch")] | @tsv' <<<"$all")

  # 3. Claim, wait, and keep it only if it is the oldest live claim.
  id=$(gh_claim_body "$name" "$branch" | jq -Rs '{body: .}' |
    gh_api --method POST "repos/$C4_GITHUB_REPO/issues/$n/comments" --input - --jq .id) || return 1
  sleep "$GH_CLAIM_SETTLE"
  if ! all=$(gh_comments "$n"); then
    gh_api --method DELETE "repos/$C4_GITHUB_REPO/issues/comments/$id" >/dev/null
    return 1
  fi
  winner=$(jq -r "${q[@]}" "$GH_CLAIM_JQ"'
    [.[] | select(live)] | min_by(.id) // empty | "\(.id)\t\(cname)"' <<<"$all")
  if [ "${winner%%$'\t'*}" != "$id" ]; then
    gh_api --method DELETE "repos/$C4_GITHUB_REPO/issues/comments/$id" >/dev/null
    echo "github: #$n was claimed at the same time by ${winner#*$'\t'}, who won" >&2
    return 4
  fi

  # 4. The local half, and the status.
  mkdir -p "$GH_PENDING_DIR"
  [ -e "$GH_PENDING_DIR/.gitignore" ] || echo '*' >"$GH_PENDING_DIR/.gitignore"
  jq -n --argjson n "$n" --argjson id "$id" --arg name "$name" --arg branch "$branch" \
    --argjson labels "$(jq -c '[.labels[].name]' <<<"$issue")" --argjson beat "$(date +%s)" \
    '{issue: $n, id: $id, name: $name, branch: $branch, labels: $labels, beat: $beat}' >"$GH_CLAIM_FILE"
  [ -n "${GH_TAKE_KEEP_STATUS:-}" ] ||
    gh_set_status "$n" in-progress || echo "github: claimed #$n but could not label it status/in-progress" >&2
  printf '%s\t%s\n' "$name" "$branch"
}

# gh_cluster_holder: another checkout on this device (git worktree list) whose
# claim is on a needs/cluster issue and was beaten within C4_LEASE_TTL, as
# "otter-23 (#23, PATH)"; nothing when there is none. The labels come from
# GitHub, else from the claim file.
gh_cluster_holder() {
  local ttl mine wt f issue name beat labels
  ttl=$(gh_ttl) || return 0
  mine=$(realpath -m "$GH_CLAIM_FILE")
  while IFS= read -r wt; do
    f=$wt/.relay/claim
    [ -s "$f" ] && [ "$(realpath -m "$f")" != "$mine" ] || continue
    IFS=$'\t' read -r issue name beat < <(jq -r '[.issue, .name, (.beat // 0)] | @tsv' "$f" 2>/dev/null)
    [ -n "$issue" ] && [ $(($(date +%s) - ${beat:-0})) -lt "$ttl" ] || continue
    labels=$(gh_api "repos/$C4_GITHUB_REPO/issues/$issue/labels?per_page=100" --jq '.[].name' 2>/dev/null) ||
      labels=$(jq -r '.labels[]?' "$f")
    grep -qxF needs/cluster <<<"$labels" || continue
    echo "$name (#$issue, $wt)"
    return 0
  done < <(git -C "$GH_LIB_ROOT" worktree list --porcelain 2>/dev/null | sed -n 's/^worktree //p')
}

# gh_label N LABEL...: add LABELs to N, under the device lock. Exit 4 after
# adding needs/cluster when another checkout on this device holds the cluster:
# land what needs no cluster and hand off.
gh_label() { gh_device_locked gh_label_locked "$@"; }

gh_label_locked() {
  local n=${1:?usage: label N LABEL...} holder
  shift
  [ $# -gt 0 ] || { echo "usage: label N LABEL..." >&2; return 64; }
  jq -n '{labels: $ARGS.positional}' --args "$@" |
    gh_api --method POST "repos/$C4_GITHUB_REPO/issues/$n/labels" --input - >/dev/null || return 1
  echo "labeled #$n: $*"
  # This checkout's own issue: keep the claim's labels current, the fallback
  # gh_cluster_holder reads when GitHub can't be asked.
  if [ -s "$GH_CLAIM_FILE" ] && [ "$(jq -r .issue "$GH_CLAIM_FILE")" = "$n" ]; then
    jq --args '.labels = ((.labels // []) + $ARGS.positional | unique)' "$@" <"$GH_CLAIM_FILE" >"$GH_CLAIM_FILE.tmp" &&
      mv "$GH_CLAIM_FILE.tmp" "$GH_CLAIM_FILE"
  fi
  printf '%s\n' "$@" | grep -qxF needs/cluster && [ "${C4_CLUSTER:-}" != none ] || return 0
  holder=$(gh_cluster_holder)
  [ -n "$holder" ] || return 0
  echo "github: this device's Kind cluster is in use: $holder works a needs/cluster issue; land what needs no cluster and hand off #$n" >&2
  return 4
}

# gh_release [REASON]: release this checkout's claim; its comment then says
# "**NAME** released this issue: REASON". Without a claim there is nothing to do.
gh_release() {
  local reason=${1:-released} c_issue c_id c_name c_branch rc
  gh_claim_read || return 0
  gh_claim_held >/dev/null
  rc=$?
  [ "$rc" = 2 ] && return 1
  if [ "$rc" = 0 ]; then
    gh_edit_comment "$c_id" "$(printf '%s\n**%s** released this issue: %s · branch `%s`' \
      "$GH_CLAIM_MARKER" "$c_name" "$reason" "$c_branch")" || return 1
    echo "released #$c_issue ($c_name): $reason"
  else
    echo "github: $c_name's claim on #$c_issue was already released or taken over" >&2
  fi
  rm -f "$GH_CLAIM_FILE"
}

# gh_release_for N REASON: release this checkout's claim if it is on N. A
# failure only warns: the claim then expires after C4_LEASE_TTL.
gh_release_for() {
  local c_issue c_id c_name c_branch
  gh_claim_read && [ "$c_issue" = "$1" ] || return 0
  gh_release "$2" >/dev/null ||
    echo "github: could not release the claim on #$1; it expires after $C4_LEASE_TTL" >&2
}

# gh_beat: refresh this checkout's claim heartbeat, at most every
# GH_BEAT_EVERY seconds. GitHub errors are ignored until the next beat. Exit 2:
# the claim was taken over, with what to do on stderr.
gh_beat() {
  local c_issue c_id c_name c_branch c rc last body
  gh_claim_read || return 0
  last=$(jq -r '.beat // 0' "$GH_CLAIM_FILE")
  [ $(($(date +%s) - last)) -ge "$GH_BEAT_EVERY" ] || return 0
  c=$(gh_claim_held 2>/dev/null)
  rc=$?
  [ "$rc" = 2 ] && return 0
  if [ "$rc" = 1 ]; then
    rm -f "$GH_CLAIM_FILE"
    cat >&2 <<EOF
github: $c_name no longer holds #$c_issue: another agent took it over (or it was released). Stop working on it:
push what you have to $c_branch-$c_name (git push origin HEAD:refs/heads/$c_branch-$c_name),
comment that branch on #$c_issue (hack/lib/github.sh comment $c_issue -), and stop.
EOF
    return 2
  fi
  body=$(jq -r .body <<<"$c" | sed "s/^Heartbeat: .*/Heartbeat: $(date -u +%Y-%m-%dT%H:%MZ)/")
  gh_edit_comment "$c_id" "$body" 2>/dev/null || return 0
  jq --argjson t "$(date +%s)" '.beat = $t' "$GH_CLAIM_FILE" >"$GH_CLAIM_FILE.tmp" &&
    mv "$GH_CLAIM_FILE.tmp" "$GH_CLAIM_FILE"
}

# gh_claims: the newest unreleased claim on each open issue, edited in the last
# 7 days: ISSUE NAME live|stale HOST HEARTBEAT BRANCH, tab-separated.
gh_claims() {
  local ttl claims open
  ttl=$(gh_ttl) || return 1
  claims=$(gh_recent_claims $((7 * 86400))) || return 1
  open=$(gh_api --paginate --slurp "repos/$C4_GITHUB_REPO/issues?state=open&per_page=100" |
    jq -c '[.[][].number]') || return 1
  jq -r --arg cm "$GH_CLAIM_MARKER" --argjson ttl "$ttl" --argjson open "$open" "$GH_CLAIM_JQ"'
    [group_by(.issue)[] | max_by(.id) | select(.issue as $i | $open | any(. == $i)) | select(held)]
    | sort_by(.issue)[]
    | ["#\(.issue)", cname, (if fresh then "live" else "stale" end), cfield("host"), "heartbeat \(age)", cfield("branch")]
    | @tsv' <<<"$claims"
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
    gh_release_for "$n" "handed off"
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

# gh_close N FILE: comment what landed, close N as completed, then release the
# claim. Closing comes first: if it fails, the claim stays live, so pick does
# not offer the open, in-progress issue again as abandoned work.
gh_close() {
  local n=${1:?usage: close N FILE}
  gh_comment "$n" "${2:--}" || return 1
  gh_api --method PATCH "repos/$C4_GITHUB_REPO/issues/$n" \
    -f state=closed -f state_reason=completed --jq '"closed #\(.number)"' || return 1
  gh_release_for "$n" finished
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
    release) gh_release "$@" ;;
    claims) gh_claims ;;
    beat) gh_beat ;;
    handoff) gh_handoff "$@" ;;
    flush) gh_flush ;;
    comment) gh_comment "$@" ;;
    label) gh_label "$@" ;;
    status) gh_set_status "$@" ;;
    close) gh_close "$@" ;;
    create) gh_create "$@" ;;
    *)
      sed -n '2,/^$/{s/^# \{0,1\}//;p}' "$0"
      [ "$cmd" = help ] || exit 64
      ;;
  esac
fi
