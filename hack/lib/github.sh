#!/usr/bin/env bash
# GitHub helpers for work on the factory itself. That work is tracked as issues
# on JairJosafath/c4factory, one issue per piece of work, and sessions hand off
# through comments on them (the handoff skill). Product work, the apps the
# factory builds, stays in Gitea, where the Crews work it.
#
# Source it for the gh_* functions (hack/relay.sh), or run it:
#
#   hack/lib/github.sh labels            create the labels below, or update their descriptions (safe to repeat)
#   hack/lib/github.sh pick              the issue to work next: NUMBER TITLE URL STATUS, tab-separated
#   hack/lib/github.sh blocked [--unworked]
#                                        what holds back each ready or in-progress issue pick skips: N waits on M
#                                        (M's status, its claim, what M waits on); --unworked: only Ms nobody works
#   hack/lib/github.sh cycles [--report] the open issues that wait on each other in a cycle, one cycle per line;
#                                        --report comments each issue in one once, the newest to needs-human
#   hack/lib/github.sh proposed [--since EPOCH]
#                                        the open status/proposed issues' numbers, oldest first, one per line;
#                                        with --since, N CHANGED CLOSED for the relay watch (see gh_proposed)
#   hack/lib/github.sh show N            issue N, its newest handoff comment and the comments after it
#   hack/lib/github.sh take N            claim N for this checkout and swap it to status/in-progress;
#                                        prints NAME BRANCH, tab-separated. Exit 4: someone else holds it,
#                                        or it needs the cluster and this device has none (C4_CLUSTER=none).
#                                        This checkout's claim on a closed issue is released first
#   hack/lib/github.sh release [REASON]  release this checkout's claim and cluster lease (default reason: released)
#   hack/lib/github.sh claims            the live and stale claims on open issues
#   hack/lib/github.sh beat              refresh this checkout's claim heartbeat and cluster lease, at most every 15
#                                        minutes. Exit 2: the claim was taken over; stop working
#   hack/lib/github.sh handoff N FILE    post FILE as N's handoff comment and release the claim and lease; if
#                                        GitHub can't be reached, keep it in .relay/pending-handoff-N.md (exit 3)
#   hack/lib/github.sh flush             post the handoffs kept in .relay/ while GitHub was away, releasing their claims
#   hack/lib/github.sh comment N FILE    post FILE as a comment on N
#   hack/lib/github.sh label N LABEL...  add labels to N
#   hack/lib/github.sh cluster acquire [--wait DURATION] [--fresh] [--bare]
#                                        lease one of this device's Kind clusters (C4_CLUSTERS, default 2) to this
#                                        checkout and print its name, creating it if it does not exist yet; wait up
#                                        to DURATION (default 30m) while other checkouts hold every one; exit 4 names them.
#                                        --fresh: prefer one not created yet, else delete and recreate the leased one
#                                        (only it); pass it when the issue asks for a fresh cluster or what is installed
#                                        isn't yours: a pool cluster belongs to nobody between leases.
#                                        --bare (implies --fresh): create it with make cluster only, no testenv Gitea
#                                        or seed; pass it to install a release (docs/getting-started.md) on it
#   hack/lib/github.sh cluster release   drop this checkout's cluster lease
#   hack/lib/github.sh cluster name      the cluster this checkout's make targets use: its lease's, else c4factory
#   hack/lib/github.sh cluster holder [NAME]
#                                        the live cluster leases (or NAME's); exit 1: none
#   hack/lib/github.sh cluster pool      each cluster in the pool: leased, free or not created yet
#   hack/lib/github.sh status N STATUS   swap N's status label: proposed, ready, in-progress, needs-human
#                                        (needs-human releases the claim and lease)
#   hack/lib/github.sh close N FILE      comment FILE (what landed: SHAs, branch), close N, release the claim and lease
#   hack/lib/github.sh duplicate N M     close N as a duplicate of M, saying so in a comment on N
#   hack/lib/github.sh create TITLE FILE LABEL...
#                                        file an issue, FILE as its body; prints its number. An open
#                                        issue with the same title is left as it is.
#   hack/lib/github.sh red WORKFLOW      whether WORKFLOW (its file, test.yml) is red on main: its two newest
#                                        finished runs there failed (cancelled and skipped runs don't count)
#                                        and none runs there now (it may be the fix).
#                                        Prints an issue body naming the runs, their commits, the failing jobs
#                                        and the newest run's last 40 failed log lines. Exit 1: not red
#   hack/lib/github.sh intake N          label issue N by where it came from: a "### Reported by" name in its
#                                        body gives user/<name> + source/user, else source/dev; adds
#                                        status/proposed when it has no status. Only for issues from the
#                                        owner, members and collaborators; safe to repeat
#
# FILE may be - for stdin. Pickup order, oldest first: a status/in-progress
# issue without a live claim, else a status/ready + priority/urgent one, else a
# status/ready one; issues with a live claim, or whose body says "Depends on #M"
# while #M is open, are skipped. A handoff comment starts with the line
# <!-- relay-handoff -->.
#
# The Kind cluster: issues whose work needs a real Kubernetes node (Agent
# Sandbox pods, kubelet, RBAC as deployed, Gitea Actions runners) carry
# needs/cluster. On a device
# without one (C4_CLUSTER=none) pick skips them and take refuses them. On a
# device with one, any checkout may work them, and the checkouts share a pool
# of Kind clusters through leases: a session does the work that needs no
# cluster first, runs `cluster acquire` right before its first step that
# touches a cluster, deploys its own branch (make deploy) to the cluster it got,
# verifies, and runs `cluster release`. A lease lives in the git common dir,
# beside every checkout, names the cluster, agent, issue and worktree, is
# refreshed by beat and is stale after C4_LEASE_TTL. release, handoff, close
# and needs-human drop it too.
# hack/lib/github_test.sh tests these rules against a stubbed GitHub.
#
# Claims (docs/plans/done/parallel-agents.md): taking an issue posts a comment that
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
  'status/proposed|c5def5|Proposed; the relay'"'"'s triage or the user readies it when it is sound'
  'status/ready|0e8a16|Ready: the next session may take it'
  'status/in-progress|fbca04|A session works on it; continue from its newest handoff comment'
  'status/needs-human|d93f0b|Blocked on the user; the newest comment says why'
  'priority/urgent|b60205|Picked before every other ready issue'
  'kind/bug|d73a4a|Something is broken'
  'kind/feature|a2eeef|New behaviour'
  'kind/chore|ededed|Upkeep: operations, releases, tooling'
  'kind/docs|0075ca|Documentation'
  'kind/improvement|84b6eb|Make something that works better'
  'source/dev|bfd4f2|Made while developing c4factory'
  'source/user|f9d0c4|Reported from a device that runs c4factory; user/<name> names it'
  'needs/cluster|1d76db|Needs a real Kubernetes node: Agent Sandbox pods, kubelet, RBAC as deployed, Gitea Actions runners'
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
  local have line name color desc was
  have=$(gh_api --paginate "repos/$C4_GITHUB_REPO/labels?per_page=100" --jq '.[] | [.name, .description // ""] | @tsv') || return 1
  for line in "${GH_LABELS[@]}"; do
    IFS='|' read -r name color desc <<<"$line"
    if grep -q -- "^$name"$'\t' <<<"$have"; then
      was=$(grep -- "^$name"$'\t' <<<"$have" | head -1 | cut -f2)
      if [ "$was" = "$desc" ]; then
        echo "label $name: exists"
      else
        gh_api --method PATCH "repos/$C4_GITHUB_REPO/labels/${name//\//%2F}" -f description="$desc" >/dev/null || return 1
        echo "label $name: description updated"
      fi
      continue
    fi
    gh_api --method POST "repos/$C4_GITHUB_REPO/labels" \
      -f name="$name" -f color="$color" -f description="$desc" >/dev/null || return 1
    echo "label $name: created"
  done
}

# gh_seconds VALUE NAME: VALUE (2h, 90m, 300s or 300) in seconds; NAME is
# what the error message calls it.
gh_seconds() {
  local t=$1
  [[ $t =~ ^[0-9]+[smh]?$ ]] || { echo "github: $2 must look like 2h, 90m, 300s or 300" >&2; return 1; }
  case $t in
    *h) echo $((${t%h} * 3600)) ;;
    *m) echo $((${t%m} * 60)) ;;
    *) echo "${t%s}" ;;
  esac
}

# gh_ttl: C4_LEASE_TTL in seconds.
gh_ttl() { gh_seconds "$C4_LEASE_TTL" C4_LEASE_TTL; }

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
  [ "${C4_CLUSTER:-}" != none ] || nocluster=true
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

# jq definitions over open issues: deps, the issues one says it depends on.
GH_DEPS_JQ='
  def deps: [(.body // "") | scan("[Dd]epends on #([0-9]+)") | .[0] | tonumber] | unique;
'

# gh_open_issues: the open issues, not pull requests, oldest first, as one JSON array.
gh_open_issues() {
  local pages
  pages=$(gh_api --paginate --slurp \
    "repos/$C4_GITHUB_REPO/issues?state=open&sort=created&direction=asc&per_page=100") || return 1
  jq -c '[.[][] | select(.pull_request | not)]' <<<"$pages"
}

# gh_blocked [--unworked]: what holds back each open status/ready or
# status/in-progress issue that pick skips because it waits on an open issue
# (and no live claim holds it): one line per issue it waits on, oldest first,
#   N waits on M (STATUS, CLAIM[, waits on #X, #Y])
# STATUS being M's status label (or "no status"), CLAIM "claimed by NAME" while
# a live claim holds M, else "no claim", and the last part the open issues M
# waits on itself. With --unworked, only the lines whose M nobody works (in
# progress with a live claim). Exit 2: GitHub could not be asked.
gh_blocked() {
  local ttl issues claims unworked=false
  case ${1:-} in
    --unworked) unworked=true ;;
    '') ;;
    *) echo "usage: hack/lib/github.sh blocked [--unworked]" >&2; return 1 ;;
  esac
  ttl=$(gh_ttl) || return 2
  issues=$(gh_open_issues) || return 2
  claims=$(gh_recent_claims "$ttl") || return 2
  jq -r --arg cm "$GH_CLAIM_MARKER" --argjson ttl "$ttl" --argjson claims "$claims" \
    --argjson unworked "$unworked" "$GH_CLAIM_JQ$GH_DEPS_JQ"'
    def has($l): .labels | any(.name == $l);
    def isopen($n): any(.[]; .number == $n);
    . as $all
    | def holder($n): [$claims[] | select(live and .issue == $n)] | last | if . then cname else null end;
      def waits($i): [$i | deps[] | select(. as $d | $all | isopen($d))];
      .[] | select(has("status/ready") or has("status/in-progress"))
      | select(holder(.number) == null)
      | .number as $n | waits(.)[] as $d
      | ($all[] | select(.number == $d)) as $m
      | ([$m.labels[].name | select(startswith("status/"))] | first // "no status") as $status
      | holder($d) as $h
      | select($unworked and $status == "status/in-progress" and $h != null | not)
      | waits($m) as $w
      | "\($n) waits on \($d) (\($status), \(if $h then "claimed by \($h)" else "no claim" end)"
        + (if $w == [] then "" else ", waits on \($w | map("#\(.)") | join(", "))" end) + ")"' <<<"$issues" || return 2
}

GH_CYCLE_MARKER='<!-- relay-cycle -->'

# gh_cycles [--report]: the open issues that wait on each other in a cycle
# (Depends on #M in their bodies), one cycle per line as #A #B ..., oldest
# first. With --report, each issue in a cycle gets one comment naming it (an
# issue that has that comment already is left alone) and, when one was posted
# or no issue in the cycle is in status/needs-human, the newest goes there;
# prints what it did.
# Exit 2: GitHub could not be asked.
gh_cycles() {
  local issues cycles cycle report=false n posted newest all body
  case ${1:-} in
    --report) report=true ;;
    '') ;;
    *) echo "usage: hack/lib/github.sh cycles [--report]" >&2; return 1 ;;
  esac
  issues=$(gh_open_issues) || return 2
  cycles=$(jq -r "$GH_DEPS_JQ"'
    [.[].number] as $open
    | (reduce .[] as $i ({}; .["\($i.number)"] = [$i | deps[] | select(. as $d | $open | any(. == $d))])) as $g
    | def reach($n): {seen: [], todo: $g["\($n)"]}
        | until(.todo == []; .todo[0] as $x | .todo |= .[1:]
            | if (.seen | any(. == $x)) then . else .seen += [$x] | .todo += $g["\($x)"] end)
        | .seen;
      [$open[] as $n | reach($n) as $r | select($r | any(. == $n))
        | [$r[] | select(. as $m | reach($m) | any(. == $n))] | sort] | unique
    | .[] | map("#\(.)") | join(" ")' <<<"$issues") || return 2
  if ! $report; then
    [ -z "$cycles" ] || echo "$cycles"
    return 0
  fi
  while IFS= read -r cycle; do
    [ -n "$cycle" ] || continue
    newest=${cycle##*#}
    body="$GH_CYCLE_MARKER"$'\n'"${cycle// /, } wait on each other in a cycle (\`Depends on #M\` in their bodies), so pick skips each of them for good. Drop one \`Depends on\` line to break it; #$newest, the newest, goes to status/needs-human until then."
    posted=
    for n in $cycle; do
      n=${n#\#}
      all=$(gh_comments "$n") || return 2
      jq -e --arg b "$body" 'any(.[]; .body == $b)' <<<"$all" >/dev/null && continue
      printf '%s\n' "$body" | gh_comment "$n" - >/dev/null || return 2
      posted+=" #$n"
    done
    # Also when an earlier round commented but failed to move the newest.
    [ -n "$posted" ] || ! jq -e --arg c "$cycle" '[$c | scan("[0-9]+") | tonumber] as $m
      | any(.[] | select(.number as $n | $m | any(. == $n)); .labels | any(.name == "status/needs-human"))' \
      <<<"$issues" >/dev/null || continue
    gh_set_status "$newest" needs-human >/dev/null || return 2
    echo "cycle $cycle: ${posted:+commented on${posted}; }#$newest to status/needs-human"
  done <<<"$cycles"
}

# gh_proposed [--since T]: the open status/proposed issues' numbers, oldest
# first, one per line, for the relay's triage. With --since T (epoch seconds,
# the last triage's start), each line is N CHANGED CLOSED for hack/relay-watch.sh:
# CHANGED is when N last changed in a way the triage didn't (its body, title,
# labels or a comment that isn't a triage's, <!-- relay-triage -->), CLOSED
# when the newest issue it waits on closed (Depends on #M in its body, or an #M
# in its newest triage comment); either is 0 when nothing after T says so.
# Exit 2: GitHub could not be asked.
gh_proposed() {
  local issues since= comments closed n triage='[]' t
  case ${1:-} in
    --since) [[ ${2:-} =~ ^[0-9]+$ ]] || { echo "github: proposed --since takes epoch seconds" >&2; return 1; }
      since=$(date -u -d "@$2" +%Y-%m-%dT%H:%M:%SZ) ;;
    '') ;;
    *) echo "usage: hack/lib/github.sh proposed [--since EPOCH]" >&2; return 1 ;;
  esac
  issues=$(gh_api --paginate --slurp \
    "repos/$C4_GITHUB_REPO/issues?state=open&sort=created&direction=asc&per_page=100") || return 2
  issues=$(jq -c '[.[][] | select(.pull_request | not) | select(.labels | any(.name == "status/proposed"))]' <<<"$issues") ||
    return 2
  if [ -z "$since" ]; then
    jq -r '.[].number' <<<"$issues"
    return
  fi
  comments=$(gh_api --paginate --slurp "repos/$C4_GITHUB_REPO/issues/comments?since=$since&per_page=100") || return 2
  closed=$(gh_api --paginate --slurp "repos/$C4_GITHUB_REPO/issues?state=closed&since=$since&per_page=100") || return 2
  closed=$(jq -c --arg s "$since" '[.[][] | select(.closed_at != null and .closed_at > $s) | {number, closed_at}]' <<<"$closed") ||
    return 2
  # Only when something closed since T: each issue's newest triage comment, for the #M it names.
  if [ "$closed" != '[]' ]; then
    for n in $(jq -r '.[].number' <<<"$issues"); do
      t=$(gh_comments "$n") || return 2
      triage=$(jq -c --argjson n "$n" --argjson c "$t" \
        '. + [{n: $n, body: ([$c[] | select(.body | startswith("<!-- relay-triage -->"))] | last | .body // "")}]' <<<"$triage")
    done
  fi
  jq -r --arg s "$since" --argjson comments "$comments" --argjson closed "$closed" --argjson triage "$triage" '
    def ep: fromdateiso8601;
    def mine: .body | startswith("<!-- relay-triage -->");
    [$comments[][] | select(.updated_at > $s) | . + {n: (.issue_url | sub(".*/"; "") | tonumber)}] as $since
    | .[] | .number as $n
    | [$since[] | select(.n == $n)] as $c
    # The triage comment bumps the issue'"'"'s updated_at; a change in its few
    # seconds is taken for the triage'"'"'s own.
    | ([$c[] | select(mine) | .updated_at | ep] | max) as $tc
    | ([$c[] | select(mine | not) | .updated_at | ep]
        + (if (.updated_at | ep) > (($tc // 0) + 10) then [.updated_at | ep] else [] end)
        | max // 0) as $changed
    | ([(.body // "") | scan("[Dd]epends on #([0-9]+)") | .[0] | tonumber]
        + [$triage[] | select(.n == $n) | .body | scan("#([0-9]+)") | .[0] | tonumber]) as $refs
    | ([$closed[] | select(.number as $m | $refs | any(. == $m)) | .closed_at | ep] | max // 0) as $done
    | "\($n) \($changed) \($done)"' <<<"$issues" || return 2
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
  [ "$want" != status/needs-human ] || { gh_lease_release; gh_release_for "$n" needs-human; }
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
# checkouts (in the git common dir), so two takes on the device don't race
# each other.
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
# label alone.
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
  if [ "${C4_CLUSTER:-}" = none ] && jq -e '.labels | any(.name == "needs/cluster")' <<<"$issue" >/dev/null; then
    echo "github: #$n needs the Kind cluster (needs/cluster) and this device has none (C4_CLUSTER=none)" >&2
    return 4
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

# gh_label N LABEL...: add LABELs to N.
gh_label() {
  local n=${1:?usage: label N LABEL...}
  shift
  [ $# -gt 0 ] || { echo "usage: label N LABEL..." >&2; return 64; }
  jq -n '{labels: $ARGS.positional}' --args "$@" |
    gh_api --method POST "repos/$C4_GITHUB_REPO/issues/$n/labels" --input - >/dev/null || return 1
  echo "labeled #$n: $*"
}

# The cluster pool: the device runs C4_CLUSTERS Kind clusters (default 2),
# c4factory, c4factory-2, ..., and its checkouts share them through leases. A
# deploy and its verification take a whole cluster (one operator Deployment,
# one set of cluster-scoped CRDs, a node that verifications stop), so a session
# leases one only around the steps that touch it, and the make targets in its
# checkout use that one (cluster name). Each cluster's lease is a JSON file in
# the git common dir, beside every checkout: {cluster, name, issue, worktree,
# beat}. It is live while its worktree exists and its beat is within
# C4_LEASE_TTL; a stale one is taken over. A checkout holds at most one.
# C4_CLUSTER_LEASE names the first cluster's file (the tests); the others sit
# beside it. A pool cluster that does not exist yet is created on its first
# lease (make cluster gitea seed; C4_MAKE overrides make, KIND kind). A pool
# cluster belongs to nobody between leases: what an earlier session installed
# stays on it, so acquire --fresh deletes and recreates the cluster it leased
# (make cluster-delete, then as above), and never one another checkout holds.
# acquire --bare does the same and creates it with make cluster only: nothing
# installed, for a release's chart, which brings its own Gitea.
GH_LEASE_POLL=${GH_LEASE_POLL:-15} # seconds between tries while acquire waits
GH_POOL_FIRST=c4factory

# gh_pool: the pool's cluster names, one per line.
gh_pool() {
  local n=${C4_CLUSTERS:-2} k
  [[ $n =~ ^[1-9][0-9]{0,1}$ ]] || { echo "github: C4_CLUSTERS must be a number from 1 to 99, not '$n'" >&2; return 64; }
  echo "$GH_POOL_FIRST"
  for ((k = 2; k <= n; k++)); do echo "$GH_POOL_FIRST-$k"; done
}

# gh_lease_base: the first cluster's lease file; the lock sits beside it.
gh_lease_base() {
  local dir
  [ -z "${C4_CLUSTER_LEASE:-}" ] || { echo "$C4_CLUSTER_LEASE"; return; }
  dir=$(git -C "$GH_LIB_ROOT" rev-parse --path-format=absolute --git-common-dir 2>/dev/null) || dir=$GH_PENDING_DIR
  echo "$dir/c4-cluster.lease"
}

# gh_lease_file NAME: the path of cluster NAME's lease.
gh_lease_file() {
  local base
  base=$(gh_lease_base)
  case ${1:?usage: gh_lease_file NAME} in
    "$GH_POOL_FIRST") echo "$base" ;;
    *) echo "${base%.lease}-$1.lease" ;;
  esac
}

# gh_lease_names: the clusters that have a lease file, live or not, one per line.
gh_lease_names() {
  local base f
  base=$(gh_lease_base)
  [ ! -s "$base" ] || echo "$GH_POOL_FIRST"
  for f in "${base%.lease}"-*.lease; do
    [ -s "$f" ] || continue
    f=${f##*/}
    f=${f#"$(basename "${base%.lease}")-"}
    echo "${f%.lease}"
  done
}

# gh_checkout: this checkout's root, the worktree a lease names (the relay's
# workers set GH_PENDING_DIR to their worktree's .relay).
gh_checkout() { realpath -m "$GH_PENDING_DIR/.."; }

# gh_lease_read NAME: set l_name, l_issue, l_wt and l_beat from cluster NAME's
# lease, live or not. Exit 1: there is none.
gh_lease_read() {
  local f
  f=$(gh_lease_file "$1")
  [ -s "$f" ] || return 1
  IFS=$'\x1f' read -r l_name l_issue l_wt l_beat < <(jq -r \
    '[.name // "", (.issue // "" | tostring), .worktree // "", (.beat // 0 | tostring)] | join("\u001f")' "$f" 2>/dev/null)
  [ -n "$l_wt" ]
}

# gh_lease_live NAME: gh_lease_read, and exit 1 unless the lease is live. A bad
# C4_LEASE_TTL counts as live: better wait than deploy over another session.
gh_lease_live() {
  local ttl
  gh_lease_read "$1" && [ -d "$l_wt" ] || return 1
  ttl=$(gh_ttl 2>/dev/null) || return 0
  [ $(($(date +%s) - ${l_beat:-0})) -lt "$ttl" ]
}

# gh_lease_mine: exit 0 when the lease read names this checkout.
gh_lease_mine() { [ "$(realpath -m "$l_wt")" = "$(gh_checkout)" ]; }

# gh_lease_desc: the lease read, as "otter-23 (#23, PATH), heartbeat 5m ago".
gh_lease_desc() {
  echo "$l_name (${l_issue:+#$l_issue, }$l_wt), heartbeat $((($(date +%s) - ${l_beat:-0}) / 60))m ago"
}

# gh_lease_name: the cluster whose lease names this checkout, live or not.
# Exit 1: this checkout holds none.
gh_lease_name() {
  local c l_name l_issue l_wt l_beat
  for c in $(gh_lease_names); do
    gh_lease_read "$c" && gh_lease_mine && { echo "$c"; return 0; }
  done
  return 1
}

# gh_lease_holder NAME: the checkout other than this one that holds a live
# lease on cluster NAME, as gh_lease_desc prints it. Exit 1: none does.
gh_lease_holder() {
  local l_name l_issue l_wt l_beat
  gh_lease_live "${1:?usage: gh_lease_holder NAME}" && ! gh_lease_mine || return 1
  gh_lease_desc
}

# gh_lease_locked CMD ARGS...: run CMD under the leases' lock, so acquires,
# releases and beats in different checkouts don't interleave. Not the device
# lock: take holds that while it releases a claim, and so the lease.
gh_lease_locked() {
  local fd rc
  exec {fd}>>"$(gh_lease_base).lock"
  flock -w 30 "$fd" || { echo "github: waited 30s for the cluster leases' lock" >&2; exec {fd}>&-; return 2; }
  "$@"
  rc=$?
  exec {fd}>&-
  return $rc
}

# gh_kind_clusters: the Kind clusters that exist on this device, one per line.
gh_kind_clusters() { "${KIND:-kind}" get clusters 2>/dev/null; }

# gh_lease_take [FRESH]: lease a pool cluster to this checkout and print its
# name: the one it already holds, else a free one that exists (with FRESH set, a
# free one that does not), else the first free one. With every cluster held by
# other checkouts, print them and exit 1. Run under gh_lease_locked.
gh_lease_take() {
  local fresh=${1:-} pool c f pick= free=() held=() exist name= issue= c_issue c_id c_name c_branch l_name l_issue l_wt l_beat
  pool=$(gh_pool) || return 64
  # The cluster this checkout holds, while it is still in the pool (C4_CLUSTERS may have shrunk).
  pick=$(gh_lease_name) && grep -qxF -- "$pick" <<<"$pool" || pick=
  if [ -z "$pick" ]; then
    [ -z "$(gh_lease_name)" ] || gh_lease_release_locked >/dev/null
    for c in $pool; do
      if gh_lease_live "$c"; then held+=("$c: $(gh_lease_desc)"); else free+=("$c"); fi
    done
    if [ ${#free[@]} -eq 0 ]; then
      f=$(printf '%s; ' "${held[@]}")
      echo "${f%; }"
      return 1
    fi
    exist=$(gh_kind_clusters)
    for c in "${free[@]}"; do
      if grep -qxF -- "$c" <<<"$exist"; then [ -n "$fresh" ] || { pick=$c; break; }; else [ -z "$fresh" ] || { pick=$c; break; }; fi
    done
    pick=${pick:-${free[0]}}
  fi
  if gh_claim_read; then name=$c_name issue=$c_issue; fi
  f=$(gh_lease_file "$pick")
  jq -n --arg c "$pick" --arg name "${name:-${USER:-$(id -un)}}" --arg issue "$issue" --arg wt "$(gh_checkout)" \
    --argjson beat "$(date +%s)" '{cluster: $c, name: $name, issue: ($issue | tonumber? // null), worktree: $wt, beat: $beat}' \
    >"$f.$$" && mv "$f.$$" "$f" && echo "$pick"
}

# gh_lease_acquire [--wait DURATION] [--fresh] [--bare]: lease a pool cluster
# to this checkout, waiting up to DURATION (default 30m) while other checkouts
# hold every one; create it if it does not exist yet. --fresh prefers one that
# does not exist yet, and deletes and recreates the leased one if it does.
# --bare implies --fresh and creates it without Gitea and the seed. Prints
# the cluster's name on stdout, the rest on stderr. Exit 4 naming the holders
# when the wait runs out.
gh_lease_acquire() {
  local wait=30m secs t0 out said= fresh= bare= l_name l_issue l_wt l_beat
  while [ $# -gt 0 ]; do
    case $1 in
      --wait) wait=${2:-}; shift 2 || shift ;;
      --wait=*) wait=${1#*=}; shift ;;
      --fresh) fresh=1; shift ;;
      --bare) fresh=1 bare=1; shift ;;
      *) echo "usage: cluster acquire [--wait DURATION] [--fresh] [--bare]" >&2; return 64 ;;
    esac
  done
  secs=$(gh_seconds "$wait" --wait) || return 64
  gh_ttl >/dev/null || return 64
  gh_pool >/dev/null || return 64
  t0=$(date +%s)
  while ! out=$(gh_lease_locked gh_lease_take $fresh); do
    [ -n "$out" ] || return 1
    if [ $(($(date +%s) - t0)) -ge "$secs" ]; then
      echo "github: every Kind cluster in the pool is in use ($out). Land what needs no cluster and hand off with only the cluster steps left" >&2
      return 4
    fi
    [ -n "$said" ] || echo "github: every Kind cluster in the pool is leased ($out); waiting up to $wait" >&2
    said=1
    sleep "$GH_LEASE_POLL"
  done
  if [ -n "$fresh" ] && gh_kind_clusters | grep -qxF -- "$out"; then
    # Only the cluster this checkout's lease names, checked once more.
    if ! gh_lease_read "$out" || ! gh_lease_mine; then
      echo "github: the lease on Kind cluster $out no longer names this checkout; not deleting it" >&2
      return 1
    fi
    echo "github: --fresh: deleting Kind cluster $out, which this checkout now leases (no other checkout holds it), to create it again (make cluster-delete KIND_CLUSTER=$out)" >&2
    if ! "${C4_MAKE:-make}" -C "$GH_LIB_ROOT" cluster-delete "KIND_CLUSTER=$out" >&2; then
      gh_lease_release >/dev/null
      echo "github: could not delete Kind cluster $out; released its lease" >&2
      return 1
    fi
  fi
  if [ -n "$bare" ] && ! gh_kind_clusters | grep -qxF -- "$out"; then
    echo "github: --bare: creating Kind cluster $out with nothing installed (make cluster KIND_CLUSTER=$out)" >&2
    if ! "${C4_MAKE:-make}" -C "$GH_LIB_ROOT" cluster "KIND_CLUSTER=$out" >&2; then
      gh_lease_release >/dev/null
      echo "github: could not create Kind cluster $out; released its lease" >&2
      return 1
    fi
  elif ! gh_kind_clusters | grep -qxF -- "$out"; then
    echo "github: Kind cluster $out does not exist yet; creating it with Gitea and the seeded templates (make cluster gitea seed KIND_CLUSTER=$out)" >&2
    if ! "${C4_MAKE:-make}" -C "$GH_LIB_ROOT" cluster gitea seed "KIND_CLUSTER=$out" >&2; then
      gh_lease_release >/dev/null
      echo "github: could not create Kind cluster $out; released its lease" >&2
      return 1
    fi
  fi
  gh_lease_read "$out"
  echo "github: Kind cluster $out is leased to $(gh_lease_desc). make in this checkout uses it; kubectl needs --context kind-$out. Release it when verification is done (hack/lib/github.sh cluster release)" >&2
  echo "$out"
}

# gh_lease_release: drop this checkout's lease, live or stale; other
# checkouts' are left alone.
gh_lease_release() { gh_lease_locked gh_lease_release_locked; }

gh_lease_release_locked() {
  local c l_name l_issue l_wt l_beat
  c=$(gh_lease_name) || return 0
  gh_lease_read "$c"
  rm -f "$(gh_lease_file "$c")"
  echo "released Kind cluster $c ($l_name${l_issue:+, #$l_issue})"
}

# gh_lease_beat: refresh this checkout's lease, at most every GH_BEAT_EVERY seconds.
gh_lease_beat() {
  local c l_name l_issue l_wt l_beat
  c=$(gh_lease_name) || return 0
  gh_lease_read "$c"
  [ $(($(date +%s) - ${l_beat:-0})) -ge "$GH_BEAT_EVERY" ] || return 0
  gh_lease_locked gh_lease_beat_locked
}

gh_lease_beat_locked() {
  local c f
  c=$(gh_lease_name) || return 0
  f=$(gh_lease_file "$c")
  jq --argjson t "$(date +%s)" '.beat = $t' "$f" >"$f.$$" && mv "$f.$$" "$f"
}

# gh_lease_pool: each pool cluster and whether it is leased, free or not
# created yet.
gh_lease_pool() {
  local c pool exist l_name l_issue l_wt l_beat
  pool=$(gh_pool) || return 64
  exist=$(gh_kind_clusters)
  for c in $pool; do
    if gh_lease_live "$c"; then
      echo "$c: leased to $(gh_lease_desc)$(gh_lease_mine && echo ' (this checkout)')"
    elif grep -qxF -- "$c" <<<"$exist"; then
      echo "$c: free"
    else
      echo "$c: free, not created yet (its first lease creates it)"
    fi
  done
}

# gh_cluster acquire [--wait DURATION] [--fresh] [--bare] | release | name | holder [NAME] | pool:
# the cluster pool. name prints the cluster this checkout's make targets use:
# its lease's, else c4factory. holder prints the live leases, or NAME's; exit
# 1 when there is none.
gh_cluster() {
  local c found= l_name l_issue l_wt l_beat
  case ${1:-} in
    acquire) shift; gh_lease_acquire "$@" ;;
    release) gh_lease_release ;;
    name) gh_lease_name || echo "$GH_POOL_FIRST" ;;
    pool) gh_lease_pool ;;
    holder)
      for c in ${2:-$(gh_lease_names)}; do
        gh_lease_live "$c" || continue
        echo "$c: $(gh_lease_desc)$(gh_lease_mine && echo ' (this checkout)')"
        found=1
      done
      [ -n "$found" ]
      ;;
    *) echo "usage: cluster acquire [--wait DURATION] [--fresh] [--bare] | release | name | holder [NAME] | pool" >&2; return 64 ;;
  esac
}

# gh_release [REASON]: release this checkout's claim, and its cluster lease;
# the claim's comment then says "**NAME** released this issue: REASON".
# Without a claim there is nothing more to do.
gh_release() {
  local reason=${1:-released} c_issue c_id c_name c_branch rc
  gh_lease_release
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

# gh_beat: refresh this checkout's cluster lease and claim heartbeat, at most every
# GH_BEAT_EVERY seconds. GitHub errors are ignored until the next beat. Exit 2:
# the claim was taken over, with what to do on stderr.
gh_beat() {
  local c_issue c_id c_name c_branch c rc last body
  gh_lease_beat
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

# gh_handoff N FILE: post FILE as N's handoff comment, the marker line first,
# and drop this checkout's cluster lease. If GitHub can't be reached, keep the text in .relay/pending-handoff-N.md and
# exit 3; gh_flush posts it later. A newer handoff replaces an unposted older
# one: each handoff is a full snapshot.
gh_handoff() {
  local n=${1:?usage: handoff N FILE} body pending url
  body=$(gh_read "${2:--}") || return 1
  [ -n "$body" ] || { echo "github: empty handoff" >&2; return 1; }
  [ "$(head -1 <<<"$body")" = "$GH_HANDOFF_MARKER" ] || body="$GH_HANDOFF_MARKER"$'\n'"$body"
  pending="$GH_PENDING_DIR/pending-handoff-$n.md"
  gh_lease_release >&2
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

# gh_flush: post every handoff kept in .relay/, printing each comment's URL,
# and release this checkout's claim on its issue as gh_handoff would have;
# exit 1 if any is still there.
gh_flush() {
  local f n url held rc=0
  for f in "$GH_PENDING_DIR"/pending-handoff-*.md; do
    [ -e "$f" ] || continue
    n=${f##*/pending-handoff-}
    n=${n%.md}
    if url=$(gh_comment "$n" "$f"); then
      rm -f "$f"
      echo "posted the pending handoff on #$n: $url"
      held=$(jq -r .issue "$GH_CLAIM_FILE" 2>/dev/null)
      gh_release_for "$n" "handed off"
      [ "$held" != "$n" ] || [ -e "$GH_CLAIM_FILE" ] || echo "released #$n: handed off"
    else
      echo "github: still could not post $f" >&2
      rc=1
    fi
  done
  return $rc
}

# gh_close N FILE: comment what landed, close N as completed, then release the
# claim and the cluster lease. Closing comes first: if it fails, the claim stays live, so pick does
# not offer the open, in-progress issue again as abandoned work.
gh_close() {
  local n=${1:?usage: close N FILE}
  gh_comment "$n" "${2:--}" || return 1
  gh_api --method PATCH "repos/$C4_GITHUB_REPO/issues/$n" \
    -f state=closed -f state_reason=completed --jq '"closed #\(.number)"' || return 1
  gh_lease_release
  gh_release_for "$n" finished
}

# gh_duplicate N M: comment on N that it duplicates M, and close N as not planned.
gh_duplicate() {
  local n=${1:?usage: duplicate N M} m=${2:?usage: duplicate N M}
  [[ $n =~ ^[0-9]+$ && $m =~ ^[0-9]+$ && $n != "$m" ]] || { echo "github: duplicate takes two issue numbers" >&2; return 1; }
  echo "Duplicate of #$m." | gh_comment "$n" - >/dev/null || return 1
  gh_api --method PATCH "repos/$C4_GITHUB_REPO/issues/$n" \
    -f state=closed -f state_reason=not_planned --jq '"closed #\(.number) as a duplicate of #'"$m"'"'
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
  local a has_source=
  for a in "$@"; do [[ $a == source/* ]] && has_source=1; done
  [ -n "$has_source" ] || set -- "$@" source/dev
  jq -n --arg t "$title" --arg b "$body" '{title: $t, body: $b, labels: $ARGS.positional}' --args "$@" |
    gh_api --method POST "repos/$C4_GITHUB_REPO/issues" --input - --jq .number
}

# gh_red WORKFLOW: print an issue body when WORKFLOW's two newest finished runs
# on main failed (see the usage above); exit 1 when it isn't red, 2 when GitHub
# can't say.
gh_red() {
  local wf=${1:?usage: red WORKFLOW} runs n id
  gh_token || return 2
  runs=$(gh run list --repo "$C4_GITHUB_REPO" --branch main --workflow "$wf" --limit 10 \
    --json databaseId,status,conclusion,headSha,displayTitle,url,createdAt,workflowName) || return 2
  # A run still going on main may be the fix: wait for it.
  [ "$(jq -r 'sort_by(.createdAt) | last | .status // "completed"' <<<"$runs")" = completed ] || return 1
  runs=$(jq -c '[.[] | select(.conclusion != "cancelled" and .conclusion != "skipped")] | sort_by(.createdAt) | reverse | .[:2]' <<<"$runs") ||
    return 2
  n=$(jq '[.[] | select(.conclusion == "failure" or .conclusion == "timed_out" or .conclusion == "startup_failure")] | length' <<<"$runs")
  [ "$n" = 2 ] || return 1
  echo "\`$(jq -r '.[0].workflowName' <<<"$runs")\` (\`$wf\`) failed on main twice in a row; every pull request built on main fails with it."
  echo
  echo "## Runs"
  echo
  for id in $(jq -r '.[].databaseId' <<<"$runs"); do
    jq -r --argjson id "$id" '.[] | select(.databaseId == $id) |
      "- [\(.databaseId)](\(.url)) at \(.createdAt), \(.conclusion), commit \(.headSha[:7]): \(.displayTitle)"' <<<"$runs"
    gh run view "$id" --repo "$C4_GITHUB_REPO" --json jobs \
      --jq '.jobs[] | select(.conclusion == "failure" or .conclusion == "timed_out") | "  - failed job: \(.name)"' || return 2
  done
  echo
  echo "## The newest run's failed log, last 40 lines"
  echo
  echo '```'
  gh run view "$(jq -r '.[0].databaseId' <<<"$runs")" --repo "$C4_GITHUB_REPO" --log-failed 2>&1 | tail -40
  echo '```'
  echo
  echo "Filed by hack/relay-watch.sh. The session that fixes main closes this issue; when the cause already has an open issue, close this one as its duplicate (\`hack/lib/github.sh duplicate N M\`)."
}

GH_USER_COLOR=fef2c0

# gh_reporter: the name under the "### Reported by" heading of the issue body
# on stdin, or nothing. Headings inside fenced code blocks don't count.
gh_reporter() {
  tr -d '\r' | awk '
    /^```/ { fence = !fence; next }
    fence { next }
    want {
      if ($0 ~ /^[ \t]*$/) next
      if ($0 !~ /^### / && $0 !~ /^_No response_[ \t]*$/) { sub(/^[ \t]+/, ""); sub(/[ \t]+$/, ""); print }
      exit
    }
    /^### Reported by *$/ { want = 1 }
  '
}

# gh_user_slug NAME: NAME as a label-safe slug, or nothing.
gh_user_slug() {
  local s
  s=$(printf '%s' "$1" | tr 'A-Z' 'a-z' | sed 's/[^a-z0-9][^a-z0-9]*/-/g; s/^-//; s/-$//')
  s=${s:0:40}
  printf '%s\n' "${s%-}"
}

# gh_intake N: label issue N by its source (see the usage above). Issues from
# anyone but the owner, members and collaborators are left alone.
gh_intake() {
  local n=${1:?usage: intake N} issue assoc body labels name slug user_label add=() del=() l
  issue=$(gh_api "repos/$C4_GITHUB_REPO/issues/$n") || return 1
  assoc=$(jq -r .author_association <<<"$issue")
  case $assoc in OWNER | MEMBER | COLLABORATOR) ;; *) return 0 ;; esac
  body=$(jq -r '.body // ""' <<<"$issue")
  labels=$(jq -r '.labels[].name' <<<"$issue")
  name=$(gh_reporter <<<"$body")
  slug=$(gh_user_slug "$name")
  if [ -n "$slug" ]; then
    user_label=user/$slug
    for l in $(grep -x 'user/.*' <<<"$labels" | grep -vxF -- "$user_label"); do del+=("$l"); done
    grep -qxF source/dev <<<"$labels" && del+=(source/dev)
    grep -qxF -- "$user_label" <<<"$labels" || add+=("$user_label")
    grep -qxF source/user <<<"$labels" || add+=(source/user)
  else
    grep -q '^source/' <<<"$labels" || add+=(source/dev)
  fi
  grep -q '^status/' <<<"$labels" || add+=(status/proposed)
  for l in "${del[@]}"; do
    gh_api --method DELETE "repos/$C4_GITHUB_REPO/issues/$n/labels/${l//\//%2F}" >/dev/null || return 1
  done
  [ ${#add[@]} -gt 0 ] || return 0
  if [ -n "${user_label:-}" ] && [[ " ${add[*]} " == *" $user_label "* ]] &&
    ! gh_api "repos/$C4_GITHUB_REPO/labels/${user_label//\//%2F}" >/dev/null 2>&1; then
    gh_api --method POST "repos/$C4_GITHUB_REPO/labels" -f name="$user_label" -f color="$GH_USER_COLOR" \
      -f description="Reported by the device $slug" >/dev/null || return 1
  fi
  jq -n '{labels: $ARGS.positional}' --args "${add[@]}" |
    gh_api --method POST "repos/$C4_GITHUB_REPO/issues/$n/labels" --input - >/dev/null
}

if [ "${BASH_SOURCE[0]}" = "$0" ]; then
  set -uo pipefail
  cmd=${1:-help}
  shift || true
  case $cmd in
    labels) gh_labels ;;
    pick) gh_pick ;;
    blocked) gh_blocked "$@" ;;
    cycles) gh_cycles "$@" ;;
    proposed) gh_proposed "$@" ;;
    show) gh_show "$@" ;;
    take) gh_take "$@" ;;
    release) gh_release "$@" ;;
    claims) gh_claims ;;
    beat) gh_beat ;;
    handoff) gh_handoff "$@" ;;
    flush) gh_flush ;;
    comment) gh_comment "$@" ;;
    label) gh_label "$@" ;;
    cluster) gh_cluster "$@" ;;
    status) gh_set_status "$@" ;;
    close) gh_close "$@" ;;
    duplicate) gh_duplicate "$@" ;;
    create) gh_create "$@" ;;
    intake) gh_intake "$@" ;;
    red) gh_red "$@" ;;
    *)
      sed -n '2,/^$/{s/^# \{0,1\}//;p}' "$0"
      [ "$cmd" = help ] || exit 64
      ;;
  esac
fi
