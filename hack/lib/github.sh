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
#                                        --report comments each issue in one once, the newest to needs-maintainer
#   hack/lib/github.sh proposed [--since EPOCH]
#                                        the open status/proposed issues' numbers, oldest first, one per line;
#                                        with --since, N CHANGED CLOSED for the relay watch (see gh_proposed)
#   hack/lib/github.sh escalated         the open status/needs-maintainer issues, oldest first: N SINCE, SINCE
#                                        being when N got that label, in epoch seconds (the relay watch reads it)
#   hack/lib/github.sh parents [--apply] [M]
#                                        the plan parents: open status/in-progress or status/waiting issues whose body
#                                        says Depends on #S and that have a merged pull request saying Refs #N (their
#                                        plan), one line each: N closes (every step closed) or N waits on #S...; with
#                                        M, only those that depend on M. --apply closes the first kind with a comment
#                                        naming the steps, and moves an in-progress one no live claim holds to
#                                        status/waiting with a comment naming the steps left. close, the Issue closed
#                                        workflow and the relay watch run it with --apply
#   hack/lib/github.sh show N            issue N, its newest handoff comment and the comments after it
#   hack/lib/github.sh take N            claim N for this checkout and swap it to status/in-progress;
#                                        prints NAME BRANCH, tab-separated. Exit 4: someone else holds it.
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
#   hack/lib/github.sh milestone N [TITLE]
#                                        set N's milestone to the open milestone titled TITLE; without TITLE,
#                                        clear it. Exit 1: no open milestone has that title (the error lists them)
#   hack/lib/github.sh milestones        the open milestones, by due date: TITLE OPEN CLOSED DUE, tab-separated,
#                                        OPEN and CLOSED being their issue counts, DUE the date or empty
#   hack/lib/github.sh cluster acquire [--wait DURATION] [--fresh] [--bare]
#                                        for a human only: refused (exit 5) unless C4_HUMAN_CLUSTER=1, which the
#                                        relay never sets. Lease one of this device's Kind clusters (C4_CLUSTERS, default 2) to this
#                                        checkout and print its name, creating it if it does not exist yet; wait up
#                                        to DURATION (default 30m) while other checkouts hold every one; exit 4 names them.
#                                        --fresh: prefer one not created yet, else delete and recreate the leased one
#                                        (only it); pass it when the issue asks for a fresh cluster or what is installed
#                                        isn't yours: a pool cluster belongs to nobody between leases.
#                                        --bare (implies --fresh): create it with make cluster only, no testenv Gitea
#                                        or seed; pass it to install a release (docs/getting-started.md) on it. The
#                                        cluster stays marked bare: a plain acquire takes it last and recreates it
#                                        with Gitea and the seed (one this checkout already holds is kept, with a warning)
#   hack/lib/github.sh cluster release   drop this checkout's cluster lease
#   hack/lib/github.sh cluster name      the cluster this checkout's make targets use: its lease's, else c4factory
#   hack/lib/github.sh cluster holder [NAME]
#                                        the live cluster leases (or NAME's); exit 1: none
#   hack/lib/github.sh cluster pool      each cluster in the pool: leased, free or not created yet, and bare
#   hack/lib/github.sh status N STATUS   swap N's status label: proposed, ready, in-progress, waiting,
#                                        needs-maintainer, needs-human. Agents escalate with needs-maintainer; only
#                                        the maintainer sets needs-human (GOVERNANCE.md). Those two and waiting
#                                        (a plan's parent, waiting on its steps) release the claim and lease. ready is refused
#                                        (exit 1) for an issue with no kind/* label: take names the branch by it
#   hack/lib/github.sh close N FILE      comment FILE (what landed: SHAs, branch), close N, drop its status labels,
#                                        release the claim and lease, then close the plan parents N finishes (parents).
#                                        Refused (exit 1) while a "Depends on #S" line of N's body names an open S:
#                                        a plan's parent closes itself when its last step closes
#   hack/lib/github.sh closes-check FILE whether a pull request body or commit message (FILE, - for stdin) may
#                                        close what it names: refused (exit 1, naming the line) when a closing
#                                        keyword (closes, fixed, resolves, ..., any case, prose too) names an issue
#                                        the text also says Refs, or one with a Depends on step still open that the
#                                        text doesn't close too. Exit 2: GitHub could not be asked. hack/close-guard.sh
#                                        runs it before a session's gh pr create, edit or merge and git commit
#   hack/lib/github.sh pr-check PR       closes-check over pull request PR's title, body and commit messages
#   hack/lib/github.sh merge PR          merge pull request PR with a merge commit. Refused (exit 1) when its body
#                                        says Refs #N and a closing word next to #N (closes #N, fixed #N, even in
#                                        prose or a commit message) or GitHub's closingIssuesReferences would
#                                        still close N: a plan's pull request must not close its parent. Refused too
#                                        when it would close an issue no line starts with Closes, Fixes or Resolves #N
#                                        for (prose such as "its PR closes #9"), naming the line to reword, and
#                                        when its head is behind its base (main): two pull requests green alone can
#                                        make main red together: land it instead. Refused as well while another
#                                        pull request holds the merge turn (land), naming it, and when it closes a
#                                        kind/bug issue but changes no test file (*_test.go, *_test.sh, *.test.ts)
#                                        and no line of its body says No test: <why>
#   hack/lib/github.sh land PR [--wait DURATION]
#                                        the way to merge PR: wait for the merge turn (one pull request at a time,
#                                        across every device, first come first served), then, in this checkout of
#                                        PR's branch (a bug fix without a test is refused before it queues, as merge
#                                        refuses it), git fetch (retried GH_LAND_FETCH_TRIES times) and merge
#                                        origin/main, make check, push and merge PR,
#                                        and give the turn up, merged or not (a conflict or a red check is said; make
#                                        check's output is kept in .relay/land-PR.log). The turn is a comment on the
#                                        Maintainer report issue, refreshed every GH_LAND_BEAT seconds while it
#                                        waits and runs; one older than C4_LAND_TTL (45m) was abandoned and is
#                                        dropped. Exit 4: not its turn within DURATION (default 2h); its place is
#                                        given up. Refused before it queues when PR changes more than
#                                        GH_REVIEW_LINES (400) lines in hand-written files and carries no review
#                                        pass (review-check)
#   hack/lib/github.sh review-check PR   whether PR may land as reviewed: the lines it changes in hand-written
#                                        files (zz_generated.*, config/crd/bases/, config/rbac/role.yaml,
#                                        lockfiles and docs/screenshots/ don't count); over GH_REVIEW_LINES it
#                                        needs a comment that starts with Review:, by the owner's account (an
#                                        owner, member or collaborator) with an Agent: NAME line, whose every
#                                        top-level list item (a finding) starts with Fixed or Dismissed. Exit 1: it
#                                        needs one and has none, saying why
#   hack/lib/github.sh duplicate N M     close N as a duplicate of M, saying so in a comment on N; drops N's status labels
#   hack/lib/github.sh closed N          drop closed issue N's status/* labels (open N is left alone); the
#                                        Issue closed workflow runs it, for issues a pull request's Closes #N closes
#   hack/lib/github.sh create TITLE FILE LABEL...
#                                        file an issue, FILE as its body; prints its number. An open
#                                        issue with the same title is left as it is.
#   hack/lib/github.sh red WORKFLOW      whether WORKFLOW (its file, test.yml) is red on main: its two newest
#                                        finished runs there failed (cancelled and skipped runs, and runs its
#                                        runner lost, don't count: see gh_lost)
#                                        and none runs there now (it may be the fix).
#                                        Prints an issue body naming the runs, their commits, the failing jobs
#                                        and the newest run's last 40 failed log lines. Exit 1: not red
#                                        (a run whose jobs never started doesn't count). Exit 3: GitHub
#                                        started none of the newest run's failed jobs (failed payments, a
#                                        spending limit); prints an issue body with their annotation
#   hack/lib/github.sh flakes [--days N] [--seen FILE] [--count]
#                                        the Ginkgo specs that failed on main in the last N days (default 7):
#                                        SPEC<tab>RUN<tab>TIME for each [FAIL] line of each failed run's log.
#                                        --seen skips the runs FILE lists and writes there the runs it read
#                                        (the relay watch's); --count prints COUNT<tab>SPEC instead, most first
#   hack/lib/github.sh queued [--minutes M]
#                                        whether runs on main wait for a runner: when the oldest run queued on
#                                        main was queued more than M minutes ago (default 30), prints
#                                        OLDEST<tab>COUNT, its createdAt and how many runs are queued there.
#                                        Exit 1: none waits that long. A queued run never fails, so red can't
#                                        see runners that are all offline; the relay watch asks this
#   hack/lib/github.sh activity          one line that changes whenever an issue or pull request changes (the
#                                        Maintainer report excluded) or main moves; the relay watch compares it
#   hack/lib/github.sh report FILE       set FILE as the body of the pinned Maintainer report issue (label
#                                        maintainer/report, no status), creating and pinning it the first time;
#                                        prints its URL
#   hack/lib/github.sh report-note KEY [HEALTH [NEEDS]]
#                                        put the line HEALTH under the Maintainer report's Health and NEEDS
#                                        (default HEALTH) as a task under its Needs you, each marked
#                                        <!-- watch:KEY --> and replacing the lines KEY marked before; with no
#                                        text, remove them. The relay watch's notes; the maintainer keeps them
#   hack/lib/github.sh intake N          label issue N by where it came from: a "### Reported by" name in its
#                                        body gives user/<name> + source/user, else source/dev; adds
#                                        status/proposed when it has no status. Only for issues from the
#                                        owner, members and collaborators; safe to repeat
#
# FILE may be - for stdin. Pickup order, oldest first: a priority/urgent issue
# (a status/in-progress one without a live claim, else a status/ready one),
# else a status/in-progress issue without a live claim, else a status/ready
# one; issues with a live claim, or whose body says "Depends on #M" while #M is
# open, are skipped. A handoff comment starts with the line
# <!-- relay-handoff -->.
#
# The Kind cluster: agents never create, lease or deploy to one; Go tests,
# validate-yaml, envtest and the local Gitea are the gate, and Kind is a tier
# only a human runs. So `cluster acquire` refuses unless the human sets
# C4_HUMAN_CLUSTER=1 (the relay never does), and pick and take hand out every
# issue whether or not the device has a cluster (C4_CLUSTER=none). The human's
# checkouts share a pool of Kind clusters through leases: acquire, deploy the
# branch (make deploy) to the cluster it got, verify, `cluster release`. A lease lives in the git common dir,
# beside every checkout, names the cluster, agent, issue and worktree, is
# refreshed by beat and is stale after C4_LEASE_TTL. release, handoff, close,
# needs-maintainer and needs-human drop it too.
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

# gh_now: the clock heartbeats are written with and their age and freshness
# read from, in epoch seconds. GH_NOW pins it (hack/lib/github_test.sh does, so
# an age it expects is the same however long the run takes; #1194). How long a
# wait has lasted is timed with SECONDS or date, never with this clock.
gh_now() { echo "${GH_NOW:-$(date +%s)}"; }
# GH_NOW_JQ: jq's now, read from the same clock.
GH_NOW_JQ='
  def _now: now;
  def now: $ENV.GH_NOW // "" | if . == "" then _now else tonumber end;
'

# jq definitions over comments ({id, updated_at, body}); need --arg cm and --argjson ttl.
GH_CLAIM_JQ=$GH_NOW_JQ'
  def claim: .body | sub("^\\s+"; "") | startswith($cm);
  def held: .body | test("\\*\\* works on this issue");
  def fresh: (.updated_at | fromdateiso8601) > (now - $ttl);
  def live: claim and held and fresh;
  def cname: [.body | capture("\\*\\*(?<n>[^*]+)\\*\\*") | .n] | first // "";
  def cfield($f): [.body | capture($f + " `(?<v>[^`]+)`") | .v] | first // "";
  def age: (now - (.updated_at | fromdateiso8601)) / 60 | floor | "\(.)m ago";
'

# name|color|description, the same words the factory uses in Gitea. GitHub
# refuses a description longer than 100 characters.
GH_LABELS=(
  'status/proposed|c5def5|Filed, not yet decided: triage, the maintainer or the owner readies it'
  'status/ready|0e8a16|Decided and unblocked: the relay picks it'
  'status/in-progress|fbca04|An agent holds a claim on it'
  'status/waiting|bfdadc|A plan'"'"'s parent, waiting on its steps, not on an agent'
  'status/needs-maintainer|e99695|An agent escalated it; the newest comment says what it needs'
  'status/needs-human|d93f0b|The maintainer sent it to the owner; only the maintainer sets it'
  'priority/urgent|b60205|Picked before every other issue, in progress or ready'
  'kind/bug|d73a4a|Something is broken'
  'kind/feature|a2eeef|New behaviour'
  'kind/chore|ededed|Upkeep: operations, releases, tooling'
  'kind/docs|0075ca|Documentation'
  'kind/improvement|84b6eb|Make something that works better'
  'source/dev|bfd4f2|Made while developing c4factory'
  'source/user|f9d0c4|Reported from a device that runs c4factory; user/<name> names it'
  'maintainer/report|5319e7|The pinned Maintainer report; the maintainer rewrites it each sweep'
)
GH_REPORT_LABEL=maintainer/report
GH_REPORT_TITLE='Maintainer report'

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
  since=$(date -u -d "@$(($(gh_now) - $1))" +%Y-%m-%dT%H:%M:%SZ)
  pages=$(gh_api --paginate --slurp "repos/$C4_GITHUB_REPO/issues/comments?since=$since&per_page=100") || return 1
  jq -c --arg cm "$GH_CLAIM_MARKER" --argjson ttl 0 "$GH_CLAIM_JQ"'
    [.[][] | select(claim) | {id, issue: (.issue_url | sub(".*/"; "") | tonumber), updated_at, html_url, body}]' <<<"$pages"
}

# gh_pick: the issue to work next, as NUMBER TITLE URL STATUS (tab-separated),
# STATUS being in-progress, ready-urgent or ready. Urgent comes first, so a
# "main is red" fix (the relay watch files it ready and urgent) is worked
# before an in-progress issue that keeps failing; within each, an in-progress
# issue without a live claim comes before a ready one, oldest first. Exit 1:
# nothing to pick; exit 2: GitHub could not be asked.
gh_pick() {
  local ttl issues claims row
  ttl=$(gh_ttl) || return 2
  issues=$(gh_api --paginate --slurp \
    "repos/$C4_GITHUB_REPO/issues?state=open&sort=created&direction=asc&per_page=100") || return 2
  claims=$(gh_recent_claims "$ttl") || return 2
  row=$(jq -r --arg cm "$GH_CLAIM_MARKER" --argjson ttl "$ttl" --argjson claims "$claims" "$GH_CLAIM_JQ"'
    def has($l): .labels | any(. == $l);
    [.[][]] as $all
    | [$all[].number] as $open
    | [$claims[] | select(live) | .issue] as $taken
    | [$all[] | select(.pull_request | not)
        | select(.number as $n | $taken | any(. == $n) | not)
        | select([(.body // "") | scan("[Dd]epends on #([0-9]+)") | .[0] | tonumber]
            | any(. as $d | $open | any(. == $d)) | not)
        | {number, title, html_url, labels: [.labels[].name]}] as $free
    | [($free[] | select(has("status/in-progress") and has("priority/urgent")) | . + {how: "in-progress"}),
       ($free[] | select(has("status/ready") and has("priority/urgent")) | . + {how: "ready-urgent"}),
       ($free[] | select(has("status/in-progress")) | . + {how: "in-progress"}),
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

GH_PARENT_MARKER='<!-- relay-parent -->'

# gh_plan_pr N: N's plan: the newest merged pull request whose body says
# Refs #N (or Closes, Fixes, Resolves #N), found in N's timeline, and that
# adds or changes a file in docs/plans/ or docs/design.md (a plan written as a
# change to the design, AGENTS.md "Designing is our job"); nothing if none.
# The plan file is what tells a plan from a step's partial work that says
# Refs #N.
gh_plan_pr() {
  local pages pr files
  pages=$(gh_api --paginate --slurp "repos/$C4_GITHUB_REPO/issues/$1/timeline?per_page=100") || return 1
  for pr in $(jq -r --argjson n "$1" '[.[][] | select(.event == "cross-referenced") | .source.issue // empty
    | select(.pull_request.merged_at != null)
    | select((.body // "") | test("(?i)\\b(refs|closes|fixes|resolves) #\($n)\\b"))
    | .number] | unique | reverse | .[]' <<<"$pages"); do
    files=$(gh_api --paginate "repos/$C4_GITHUB_REPO/pulls/$pr/files?per_page=100" --jq '.[].filename') || return 1
    grep -qE '^docs/(plans/|design\.md$)' <<<"$files" && { echo "$pr"; return 0; }
  done
  return 0
}

# gh_parents [--apply] [M]: the plan parents (AGENTS.md, "Small pull requests
# to main"): open status/in-progress or status/waiting issues whose body says
# Depends on #S, one per step, and that have a merged plan pull request
# (gh_plan_pr). One line each, oldest first: "#N closes: ..." when every step
# is closed, else "#N waits on #S, ... (of #S, ...; plan #P)". With M, only the
# parents that depend on M (close passes the issue it closed). --apply closes
# the first kind with a comment naming the steps (gh_close, which tends their
# own parents in turn), and moves an in-progress one that no live claim holds
# to status/waiting with a comment naming the steps left: pick takes neither
# status/waiting nor an issue with an open Depends on, so a waiting parent is
# never handed out. An issue with Depends on lines but no merged plan is a
# plain wait and is left alone. Exit 2: GitHub could not be asked.
gh_parents() {
  local apply=false only=null issues rows n deps left all pr d st claims= ttl steps open says
  while [ $# -gt 0 ]; do
    case $1 in
      --apply) apply=true ;;
      *[!0-9]* | '') echo "usage: hack/lib/github.sh parents [--apply] [M]" >&2; return 1 ;;
      *) only=$1 ;;
    esac
    shift
  done
  issues=$(gh_open_issues) || return 2
  # NUMBER, STATUS, its steps and its open steps; with --apply, a waiting
  # parent with steps left has nothing to do.
  rows=$(jq -r --argjson only "$only" --argjson apply "$apply" "$GH_DEPS_JQ"'
    def has($l): .labels | any(.name == $l);
    [.[].number] as $open
    | .[] | select(has("status/in-progress") or has("status/waiting"))
    | select(deps != [] and ($only == null or (deps | any(. == $only))))
    | [deps[] | select(. as $d | $open | any(. == $d))] as $left
    | (if has("status/waiting") then "waiting" else "in-progress" end) as $s
    | select(($apply and $s == "waiting" and $left != []) | not)
    | [.number, $s, (deps | map(tostring) | join(" ")), ($left | map(tostring) | join(" "))] | @tsv' <<<"$issues") || return 2
  while IFS=$'\t' read -r n st all left; do
    [ -n "$n" ] || continue
    pr=$(gh_plan_pr "$n") || return 2
    [ -n "$pr" ] || continue
    steps=$(sed 's/[0-9][0-9]*/#&/g; s/ /, /g' <<<"$all")
    if [ -z "$left" ]; then
      # Not among the open issues: closed, or a pull request; ask each. A step
      # closed however (completed, or not planned: a duplicate whose work lands
      # elsewhere) counts as closed; one GitHub can't answer for (no such
      # issue) keeps the parent open.
      for d in $all; do
        if ! says=$(gh_api "repos/$C4_GITHUB_REPO/issues/$d" --jq '"\(.state) \(.state_reason // "")"' 2>/dev/null); then
          echo "github: #$n depends on #$d, which GitHub could not show; #$n stays open" >&2
          left+=" $d"
          continue
        fi
        [[ $says == closed* ]] || left+=" $d"
      done
      left=${left# }
    fi
    # An agent that still holds it (the plan's own session, before its
    # handoff) keeps it as it is.
    if $apply && { [ -z "$left" ] || [ "$st" = in-progress ]; }; then
      if [ -z "$claims" ]; then
        ttl=$(gh_ttl) || return 2
        claims=$(gh_recent_claims "$ttl") || return 2
      fi
      if jq -e --arg cm "$GH_CLAIM_MARKER" --argjson ttl "$ttl" --argjson n "$n" "$GH_CLAIM_JQ"'
        any(.[]; live and .issue == $n)' <<<"$claims" >/dev/null; then
        continue
      fi
    fi
    if [ -z "$left" ]; then
      if $apply; then
        printf '%s\n%s\n' "$GH_PARENT_MARKER" \
          "Its steps $steps are all closed and its plan's pull request #$pr merged: nothing it waits on is left, so the relay closes it." |
          gh_close "$n" - >/dev/null || return 2
        echo "#$n closed: its steps $steps closed; plan #$pr"
      else
        echo "#$n closes: its steps $steps closed; plan #$pr"
      fi
      continue
    fi
    open=$(sed 's/[0-9][0-9]*/#&/g; s/ /, /g' <<<"$left")
    if ! $apply || [ "$st" = waiting ]; then
      echo "#$n waits on $open (of $steps; plan #$pr)"
      continue
    fi
    printf '%s\n%s\n' "$GH_PARENT_MARKER" \
      "Waiting on its steps, not on an agent: $open still open (of $steps). Its plan's pull request #$pr merged, so it is status/waiting: pick skips it, and it closes itself when its last step closes." |
      gh_comment "$n" - >/dev/null || return 2
    gh_set_status "$n" waiting >/dev/null || return 2
    echo "#$n to status/waiting: waits on $open (of $steps; plan #$pr)"
  done <<<"$rows"
}

GH_CYCLE_MARKER='<!-- relay-cycle -->'

# gh_cycles [--report]: the open issues that wait on each other in a cycle
# (Depends on #M in their bodies), one cycle per line as #A #B ..., oldest
# first. With --report, each issue in a cycle gets one comment naming it (an
# issue that has that comment already is left alone) and, when one was posted
# or no issue in the cycle waits on the maintainer or the owner
# (status/needs-maintainer or status/needs-human), the newest goes to
# status/needs-maintainer; prints what it did.
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
    body="$GH_CYCLE_MARKER"$'\n'"${cycle// /, } wait on each other in a cycle (\`Depends on #M\` in their bodies), so pick skips each of them for good. Drop one \`Depends on\` line to break it; #$newest, the newest, goes to status/needs-maintainer until then."
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
      | any(.[] | select(.number as $n | $m | any(. == $n));
          .labels | any(.name == "status/needs-maintainer" or .name == "status/needs-human"))' \
      <<<"$issues" >/dev/null || continue
    gh_set_status "$newest" needs-maintainer >/dev/null || return 2
    echo "cycle $cycle: ${posted:+commented on${posted}; }#$newest to status/needs-maintainer"
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

# gh_escalated: the open status/needs-maintainer issues, oldest first, one per
# line as N SINCE, SINCE being when N last got that label (epoch seconds; 0
# when its events don't say). hack/relay-watch.sh starts the maintainer for
# one escalated after the last sweep started; the maintainer's own comments on
# it don't count, so a sweep that leaves it there doesn't start another.
gh_escalated() {
  local pages n events
  pages=$(gh_api --paginate --slurp \
    "repos/$C4_GITHUB_REPO/issues?state=open&labels=status%2Fneeds-maintainer&sort=created&direction=asc&per_page=100") ||
    return 1
  for n in $(jq -r '.[][] | select(.pull_request | not) | select(.labels | any(.name == "status/needs-maintainer"))
    | .number' <<<"$pages"); do
    events=$(gh_api --paginate --slurp "repos/$C4_GITHUB_REPO/issues/$n/events?per_page=100") || return 1
    jq -r --argjson n "$n" '[.[][] | select(.event == "labeled" and .label.name == "status/needs-maintainer")
      | .created_at | fromdateiso8601] | "\($n) \(max // 0)"' <<<"$events" || return 1
  done
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
    proposed | ready | in-progress | waiting | needs-maintainer | needs-human) want=status/$2 ;;
    *) echo "github: status must be proposed, ready, in-progress, waiting, needs-maintainer or needs-human" >&2; return 1 ;;
  esac
  labels=$(gh_api "repos/$C4_GITHUB_REPO/issues/$n/labels?per_page=100" --jq '.[].name') || return 1
  # take names the branch from the kind/* label, and the release notes file
  # the work by it, so a ready issue needs one.
  if [ "$want" = status/ready ] && ! grep -q '^kind/' <<<"$labels"; then
    echo "github: #$n has no kind/* label; give it one (kind/bug, kind/feature, kind/improvement, kind/chore or kind/docs) before readying it" >&2
    return 1
  fi
  while IFS= read -r l; do
    [[ $l == status/* && $l != "$want" ]] || continue
    gh_api --method DELETE "repos/$C4_GITHUB_REPO/issues/$n/labels/${l//\//%2F}" >/dev/null || return 1
  done <<<"$labels"
  if ! grep -qxF -- "$want" <<<"$labels"; then
    jq -n --arg l "$want" '{labels: [$l]}' |
      gh_api --method POST "repos/$C4_GITHUB_REPO/issues/$n/labels" --input - >/dev/null || return 1
  fi
  case $want in
    status/waiting | status/needs-maintainer | status/needs-human) gh_lease_release_for "$n"; gh_release_for "$n" "${want#status/}" ;;
  esac
}

# gh_unstatus N: drop every status/* label from N.
gh_unstatus() {
  local n=${1:?usage: closed N} labels l
  labels=$(gh_api "repos/$C4_GITHUB_REPO/issues/$n/labels?per_page=100" --jq '.[].name') || return 1
  while IFS= read -r l; do
    [[ $l == status/* ]] || continue
    gh_api --method DELETE "repos/$C4_GITHUB_REPO/issues/$n/labels/${l//\//%2F}" >/dev/null || return 1
  done <<<"$labels"
}

# gh_closed N: drop closed issue N's status labels; an open N is left alone.
gh_closed() {
  local n=${1:?usage: closed N} state
  [[ $n =~ ^[0-9]+$ ]] || { echo "github: closed takes an issue number" >&2; return 1; }
  state=$(gh_api "repos/$C4_GITHUB_REPO/issues/$n" --jq .state) || return 1
  [ "$state" = closed ] || { echo "github: #$n is open; its status labels stay" >&2; return 0; }
  gh_unstatus "$n"
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
    --argjson labels "$(jq -c '[.labels[].name]' <<<"$issue")" --argjson beat "$(gh_now)" \
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

# gh_open_milestones: the open milestones as JSON objects, one per line, by due date.
gh_open_milestones() {
  local all
  all=$(gh_api --paginate "repos/$C4_GITHUB_REPO/milestones?state=open&sort=due_on&direction=asc&per_page=100") || return 1
  jq -c '.[]' <<<"$all"
}

# gh_milestone N [TITLE]: set N's milestone to the open one titled TITLE, or clear it.
gh_milestone() {
  local n=${1:?usage: milestone N [TITLE]} title=${2:-} open num=null
  if [ -n "$title" ]; then
    open=$(gh_open_milestones) || return 1
    num=$(jq -r --arg t "$title" 'select(.title == $t) | .number' <<<"$open" | head -1)
    if [ -z "$num" ]; then
      echo "github: no open milestone is titled \"$title\"; open: $(jq -rs 'map(.title) | join(", ")' <<<"$open")" >&2
      return 1
    fi
  fi
  jq -n --argjson m "$num" '{milestone: $m}' |
    gh_api --method PATCH "repos/$C4_GITHUB_REPO/issues/$n" --input - >/dev/null || return 1
  echo "milestone #$n: ${title:-cleared}"
}

# gh_milestones: the open milestones: TITLE OPEN CLOSED DUE, tab-separated.
gh_milestones() {
  local open
  open=$(gh_open_milestones) || return 1
  [ -z "$open" ] || jq -r '[.title, .open_issues, .closed_issues, ((.due_on // "")[:10])] | @tsv' <<<"$open"
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
# installed, for a release's chart, which brings its own Gitea. A cluster made
# so is marked bare beside its lease, a mark that outlives the lease: a plain
# acquire prefers other clusters, and recreates a bare one it gets with Gitea
# and the seed, so it never hands out a bare cluster as a testenv one.
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

# gh_bare_file NAME: the mark that cluster NAME was created bare (acquire
# --bare), beside its lease; it outlives the lease.
gh_bare_file() { local f; f=$(gh_lease_file "${1:?usage: gh_bare_file NAME}"); echo "${f%.lease}.bare"; }

# gh_bare NAME: exit 0 when cluster NAME is marked bare.
gh_bare() { [ -e "$(gh_bare_file "$1")" ]; }

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
  [ $(($(gh_now) - ${l_beat:-0})) -lt "$ttl" ]
}

# gh_lease_mine: exit 0 when the lease read names this checkout.
gh_lease_mine() { [ "$(realpath -m "$l_wt")" = "$(gh_checkout)" ]; }

# gh_lease_desc: the lease read, as "otter-23 (#23, PATH), heartbeat 5m ago".
gh_lease_desc() {
  echo "$l_name (${l_issue:+#$l_issue, }$l_wt), heartbeat $((($(gh_now) - ${l_beat:-0}) / 60))m ago"
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

# gh_kind_exists NAME: the Kind cluster NAME exists. It reads the whole list
# first: grep -q on kind's pipe can exit while kind still writes, and
# pipefail turns its SIGPIPE into a "no".
gh_kind_exists() {
  local list
  list=$(gh_kind_clusters)
  grep -qxF -- "$1" <<<"$list"
}

# gh_lease_take [FRESH]: lease a pool cluster to this checkout and print its
# name: the one it already holds, else a free one that exists and is not
# marked bare, else one that does not exist yet, else the first free one; with
# FRESH set, a free one that does not exist yet, else the first free one. With every cluster held by
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
      if grep -qxF -- "$c" <<<"$exist"; then [ -n "$fresh" ] || gh_bare "$c" || { pick=$c; break; }; else [ -z "$fresh" ] || { pick=$c; break; }; fi
    done
    if [ -z "$pick$fresh" ]; then
      for c in "${free[@]}"; do grep -qxF -- "$c" <<<"$exist" || { pick=$c; break; }; done
    fi
    pick=${pick:-${free[0]}}
  fi
  if gh_claim_read; then name=$c_name issue=$c_issue; fi
  f=$(gh_lease_file "$pick")
  jq -n --arg c "$pick" --arg name "${name:-${USER:-$(id -un)}}" --arg issue "$issue" --arg wt "$(gh_checkout)" \
    --argjson beat "$(gh_now)" '{cluster: $c, name: $name, issue: ($issue | tonumber? // null), worktree: $wt, beat: $beat}' \
    >"$f.$$" && mv "$f.$$" "$f" && echo "$pick"
}

# gh_lease_acquire [--wait DURATION] [--fresh] [--bare]: lease a pool cluster
# to this checkout, waiting up to DURATION (default 30m) while other checkouts
# hold every one; create it if it does not exist yet. --fresh prefers one that
# does not exist yet, and deletes and recreates the leased one if it does.
# --bare implies --fresh and creates it without Gitea and the seed, and marks
# it bare. Without either, a newly leased cluster marked bare is recreated with
# them; one this checkout already held is kept, with a warning. Prints
# the cluster's name on stdout, the rest on stderr. Exit 4 naming the holders
# when the wait runs out. Exit 5 without C4_HUMAN_CLUSTER=1: only a human
# runs Kind; agents never lease a cluster.
gh_lease_acquire() {
  local wait=30m secs t0 out said= fresh= bare= held= l_name l_issue l_wt l_beat
  if [ "${C4_HUMAN_CLUSTER:-}" != 1 ]; then
    echo "github: cluster acquire is for a human only: set C4_HUMAN_CLUSTER=1 to run Kind yourself. Agents never lease a cluster; make check (Go tests, validate-yaml, envtest) is the gate" >&2
    return 5
  fi
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
  held=$(gh_lease_name) || held=
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
  if [ -z "$fresh" ] && gh_bare "$out" && gh_kind_exists "$out"; then
    if [ "$out" = "$held" ]; then
      echo "github: Kind cluster $out, which this checkout already leased, was created bare (acquire --bare): no testenv Gitea or seed. Kept as it is; acquire --fresh makes it a testenv one" >&2
    else
      echo "github: Kind cluster $out was created bare (acquire --bare), with no testenv Gitea or seed, and is the only free one; recreating it as a testenv one" >&2
      fresh=1
    fi
  fi
  if [ -n "$fresh" ] && gh_kind_exists "$out"; then
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
    rm -f "$(gh_bare_file "$out")"
  fi
  if [ -n "$bare" ] && ! gh_kind_exists "$out"; then
    echo "github: --bare: creating Kind cluster $out with nothing installed (make cluster KIND_CLUSTER=$out)" >&2
    if ! "${C4_MAKE:-make}" -C "$GH_LIB_ROOT" cluster "KIND_CLUSTER=$out" >&2; then
      gh_lease_release >/dev/null
      echo "github: could not create Kind cluster $out; released its lease" >&2
      return 1
    fi
    : >"$(gh_bare_file "$out")"
  elif ! gh_kind_exists "$out"; then
    echo "github: Kind cluster $out does not exist yet; creating it with Gitea and the seeded templates (make cluster gitea seed KIND_CLUSTER=$out)" >&2
    rm -f "$(gh_bare_file "$out")"
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

# gh_lease_release_for N: drop this checkout's lease if it works on N: its
# claim is on N, or its lease was taken for N. Marking, handing off or closing
# another issue leaves the lease alone.
gh_lease_release_for() {
  local c c_issue c_id c_name c_branch l_name l_issue l_wt l_beat
  if ! { gh_claim_read && [ "$c_issue" = "$1" ]; }; then
    c=$(gh_lease_name) && gh_lease_read "$c" && [ "$l_issue" = "$1" ] || return 0
  fi
  gh_lease_release
}

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
  [ $(($(gh_now) - ${l_beat:-0})) -ge "$GH_BEAT_EVERY" ] || return 0
  gh_lease_locked gh_lease_beat_locked
}

gh_lease_beat_locked() {
  local c f
  c=$(gh_lease_name) || return 0
  f=$(gh_lease_file "$c")
  jq --argjson t "$(gh_now)" '.beat = $t' "$f" >"$f.$$" && mv "$f.$$" "$f"
}

# gh_lease_pool: each pool cluster and whether it is leased, free or not
# created yet, and whether it was created bare.
gh_lease_pool() {
  local c pool exist b l_name l_issue l_wt l_beat
  pool=$(gh_pool) || return 64
  exist=$(gh_kind_clusters)
  for c in $pool; do
    b=
    if grep -qxF -- "$c" <<<"$exist" && gh_bare "$c"; then b=", bare (acquire --bare: no testenv Gitea or seed)"; fi
    if gh_lease_live "$c"; then
      echo "$c: leased to $(gh_lease_desc)$(gh_lease_mine && echo ' (this checkout)')$b"
    elif [ -n "$b" ]; then
      echo "$c: free$b; a plain acquire takes it last and recreates it"
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
  [ $(($(gh_now) - last)) -ge "$GH_BEAT_EVERY" ] || return 0
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
  jq --argjson t "$(gh_now)" '.beat = $t' "$GH_CLAIM_FILE" >"$GH_CLAIM_FILE.tmp" &&
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
# and drop this checkout's cluster lease if it works on N. If GitHub can't be reached, keep the text in .relay/pending-handoff-N.md and
# exit 3; gh_flush posts it later. A newer handoff replaces an unposted older
# one: each handoff is a full snapshot.
gh_handoff() {
  local n=${1:?usage: handoff N FILE} body pending url
  body=$(gh_read "${2:--}") || return 1
  [ -n "$body" ] || { echo "github: empty handoff" >&2; return 1; }
  [ "$(head -1 <<<"$body")" = "$GH_HANDOFF_MARKER" ] || body="$GH_HANDOFF_MARKER"$'\n'"$body"
  pending="$GH_PENDING_DIR/pending-handoff-$n.md"
  gh_lease_release_for "$n" >&2
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

# gh_closing_refs [--leading]: the issues the text on stdin closes, one number
# per line: a GitHub closing keyword (close[sd], fix(e[sd]), resolve[sd], any
# case, an optional colon) before #N, OWNER/REPO#N or the issue's URL, of this
# repository. A pull request body, or a commit message that reaches main,
# saying one closes N at merge, wherever the words sit, prose included. With
# --leading, only the keywords that start a line (Closes #N on its own line).
# The one closing-keyword matcher: merge, closes-check and close-guard use it.
gh_closing_refs() {
  local m ref repo before='(^|[^[:alnum:]_])'
  [ "${1:-}" != --leading ] || before='^[[:space:]]*'
  grep -oiE "$before"'(close[sd]?|fix(e[sd])?|resolve[sd]?):?[[:space:]]+(https?://github\.com/[[:alnum:]_.-]+/[[:alnum:]_.-]+/issues/[0-9]+|([[:alnum:]_.-]+/[[:alnum:]_.-]+)?#[0-9]+)' |
    while IFS= read -r m; do
      ref=${m##*[[:space:]]}
      if [[ $ref =~ github\.com/([^/]+/[^/]+)/issues/([0-9]+)$ ]]; then
        repo=${BASH_REMATCH[1]} ref=${BASH_REMATCH[2]}
      else
        repo=${ref%#*} ref=${ref##*#}
      fi
      [ -z "$repo" ] || [ "${repo,,}" = "${C4_GITHUB_REPO,,}" ] || continue
      echo "$ref"
    done
}

# gh_closing_line N: the first line of the text on stdin that closes N
# (gh_closing_refs), trimmed; nothing when none does.
gh_closing_line() {
  local line
  while IFS= read -r line; do
    grep -qxF -- "$1" <<<"$(gh_closing_refs <<<"$line")" && { sed -E 's/^[[:space:]]+|[[:space:]]+$//g' <<<"$line"; return; }
  done
}

# gh_steps N [SKIP...]: N's steps, the "Depends on #S" lines of its body, one
# per line as S<TAB>STATE<TAB>LINE, STATE being S's (open or closed), or
# closing for a SKIP, which GitHub is not asked about.
gh_steps() {
  local n=$1 body line s state
  shift
  body=$(gh_api "repos/$C4_GITHUB_REPO/issues/$n" --jq '.body // ""') || return 1
  while IFS= read -r line; do
    line=$(sed -E 's/^[[:space:]]+|[[:space:]]+$//g' <<<"$line")
    for s in $(grep -oiE 'depends on #[0-9]+' <<<"$line" | grep -oE '[0-9]+'); do
      if [[ " $* " == *" $s "* ]]; then
        state=closing
      else
        state=$(gh_api "repos/$C4_GITHUB_REPO/issues/$s" --jq .state) || return 1
      fi
      printf '%s\t%s\t%s\n' "$s" "$state" "$line"
    done
  done < <(grep -iE 'depends on #[0-9]+' <<<"$body" | tr -d '\r')
  return 0
}

# gh_closes_check FILE: refuse a pull request body or commit message (FILE, or
# - for stdin) that would close the parent of a plan too early: a closing
# keyword for an issue with a "Depends on #S" step still open, or for one the
# same text names with Refs #N while it has no steps yet (a plan's pull
# request, whose steps are filed after it merges). Steps the same text closes
# count as closed, so a last step's Closes #S and Closes #N passes.
# Each refusal names the line, on stderr. Exit 1: refused; 2: GitHub could
# not be asked. Text without a closing keyword never asks GitHub.
gh_closes_check() {
  local text refs closes line n s state dline rc=0
  text=$(gh_read "${1:--}") || return 2
  closes=$(gh_closing_refs <<<"$text" | sort -nu)
  [ -n "$closes" ] || return 0
  refs=$(grep -oiE '(^|[^[:alnum:]_])refs:?[[:space:]]+#[0-9]+([[:space:]]*(,|and)[[:space:]]*#[0-9]+)*' <<<"$text" |
    grep -oE '#[0-9]+' | tr -d '#' | sort -nu)
  local -A steps=()
  for n in $closes; do
    # shellcheck disable=SC2086 # one SKIP per closed issue
    steps[$n]=$(gh_steps "$n" $closes) || return 2
  done
  while IFS= read -r line; do
    for n in $(gh_closing_refs <<<"$line" | sort -nu); do
      line=$(sed -E 's/^[[:space:]]+|[[:space:]]+$//g' <<<"$line")
      if [ -z "${steps[$n]}" ] && grep -qxF -- "$n" <<<"$refs"; then
        echo "github: refused: \"$line\" closes #$n at merge, and the same text says Refs #$n. A plan's pull request only refers to its parent: reword the line so no close, fix or resolve word sits before #$n. If this pull request finishes #$n, which has no plan, the Refs #$n is wrong: say Closes #$n, never Refs #$n; when a pushed commit says Refs #$n, reword the history onto a new branch <branch>-2, open a fresh pull request and close this one (the handoff skill, Finish)." >&2
        rc=1
        continue
      fi
      while IFS=$'\t' read -r s state dline; do
        [ "$state" = open ] || continue
        echo "github: refused: \"$line\" closes #$n at merge, but its step #$s is open (\"$dline\" in #$n's body). The parent of a plan closes itself when its last step closes." >&2
        rc=1
      done <<<"${steps[$n]}"
    done
  done <<<"$text"
  return $rc
}

# gh_pr_text PR: what merging pull request PR puts on main that can close an
# issue: its title (the merge commit's), body and commit messages.
gh_pr_text() {
  local pr=${1:?usage: pr-check PR}
  gh_api "repos/$C4_GITHUB_REPO/pulls/$pr" --jq '.title, (.body // "")' || return 1
  gh_api --paginate "repos/$C4_GITHUB_REPO/pulls/$pr/commits?per_page=100" --jq '.[].commit.message' || return 1
}

# gh_pr_check PR: gh_closes_check over pull request PR's title, body and commits.
gh_pr_check() {
  local text
  text=$(gh_pr_text "$@") || return 2
  gh_closes_check - <<<"$text"
}

# gh_close N FILE: comment what landed, close N as completed, drop its status
# labels, then release the claim and the cluster lease if they are on N. Closing comes first: if it fails, the claim stays live, so pick does
# not offer the open, in-progress issue again as abandoned work. A close that
# fails because a merged pull request's Closes #N already closed N (GitHub
# answers 422) still drops the labels and releases. Last, the plan parents
# whose last step N was close too (gh_parents); that failing is only said.
# An N whose body has a "Depends on #S" step still open is refused, naming
# it: the parent of a plan closes itself when its last step closes, never by hand.
gh_close() {
  local n=${1:?usage: close N FILE} steps s line
  steps=$(gh_steps "$n") || return 1
  steps=$(awk -F'\t' '$2 == "open" { print $1 "\t" $3 }' <<<"$steps")
  if [ -n "$steps" ]; then
    while IFS=$'\t' read -r s line; do
      echo "github: refused to close #$n: its step #$s is open (\"$line\" in #$n's body). The parent of a plan closes itself when its last step closes, never by hand." >&2
    done <<<"$steps"
    return 1
  fi
  gh_comment "$n" "${2:--}" || return 1
  if ! gh_api --method PATCH "repos/$C4_GITHUB_REPO/issues/$n" \
    -f state=closed -f state_reason=completed --jq '"closed #\(.number)"'; then
    [ "$(gh_api "repos/$C4_GITHUB_REPO/issues/$n" --jq .state 2>/dev/null)" = closed ] || return 1
    echo "#$n was already closed"
  fi
  gh_unstatus "$n" || echo "github: closed #$n but could not drop its status labels" >&2
  gh_lease_release_for "$n"
  gh_release_for "$n" finished
  gh_parents --apply "$n" || echo "github: closed #$n but could not close the plan parents waiting on it (hack/lib/github.sh parents --apply)" >&2
}

# gh_merge PR: merge pull request PR with a merge commit, unless it would close
# an issue its body says Refs #N to. GitHub reads a closing keyword next to #N
# anywhere in the body or a commit message, prose included ("its PR closes
# #N"), so a plan's pull request would close its parent with the steps
# unbuilt. Refused (exit 1) when such a phrase is there or GraphQL's
# closingIssuesReferences lists N; the message names the line to reword.
# Refused as well when it would close any issue that no line of the body or a
# commit message starts with Closes, Fixes or Resolves #N for: a step's or a
# fix's prose ("its PR closes #9") closes #9 too, and the message names the line.
# Refused too when it would close an issue with a "Depends on #S" step still
# open (gh_closes_check), or GitHub can't be asked whether it would.
# Refused when it closes a kind/bug issue and changes no test file, unless its
# body says why on a line No test: <why> (gh_bugfix_check).
# Refused while another pull request holds the merge turn (gh_land), naming it.
# Refused last when its head doesn't contain its base's current commit: two
# pull requests that each pass make check alone can still fail together, so
# only a branch that has main merged in and passed make check merges (land
# does both in its turn). The merge is pinned to the head checked, so a push
# after the check refuses too.
gh_merge() {
  local pr=${1:?usage: merge PR} view text refs closing meant line n bad= base head behind turn
  [[ $pr =~ ^[0-9]+$ ]] || { echo "github: merge takes a pull request number" >&2; return 1; }
  view=$(gh_api graphql -F owner="${C4_GITHUB_REPO%/*}" -F name="${C4_GITHUB_REPO#*/}" -F pr="$pr" -f query='
    query($owner: String!, $name: String!, $pr: Int!) { repository(owner: $owner, name: $name) { pullRequest(number: $pr) {
      body baseRefName headRefOid closingIssuesReferences(first: 100) { nodes { number labels(first: 30) { nodes { name } } } }
      files(first: 100) { totalCount nodes { path } } commits(last: 100) { nodes { commit { message } } } } } }' \
    --jq .data.repository.pullRequest) || return 1
  text=$(jq -r '.body // "", (.commits.nodes[]?.commit.message)' <<<"$view")
  closing=$(jq -r '.closingIssuesReferences.nodes[]?.number' <<<"$view")
  refs=$(jq -r '.body // ""' <<<"$view" | grep -oiE '\brefs:?[[:space:]]+#[0-9]+\b' | grep -oE '[0-9]+$' | sort -un)
  for n in $refs; do
    line=$(gh_closing_line "$n" <<<"$text")
    if [ -n "$line" ]; then
      echo "github: #$pr says Refs #$n, but \"$line\" in it closes #$n on merge; reword it (no close, fix or resolve word before #$n)" >&2
      bad=1
    elif grep -qx "$n" <<<"$closing"; then
      echo "github: #$pr says Refs #$n, but GitHub would close #$n on merge (closingIssuesReferences); drop the link or the closing word before it" >&2
      bad=1
    fi
  done
  [ -z "$bad" ] || return 1
  # It closes only what a line starts with Closes, Fixes or Resolves #N names.
  meant=$(gh_closing_refs --leading <<<"$text" | sort -un)
  for n in $( { gh_closing_refs <<<"$text"; echo "$closing"; } | sort -un); do
    grep -qxF -- "$n" <<<"$meant" && continue
    line=$(gh_closing_line "$n" <<<"$text")
    if [ -n "$line" ]; then
      echo "github: #$pr would close #$n on merge: \"$line\" names it with a closing word, and no line starts with Closes #$n; reword it (no close, fix or resolve word before #$n), or put Closes #$n on its own line" >&2
    else
      echo "github: #$pr would close #$n on merge (closingIssuesReferences), and no line starts with Closes #$n; unlink #$n, or put Closes #$n on its own line" >&2
    fi
    bad=1
  done
  [ -z "$bad" ] || return 1
  # Nor may it close an issue with a Depends on step still open (closes-check).
  gh_closes_check - <<<"$text" || return 1
  # Nor may it fix a bug without a test.
  gh_bugfix_check "$pr" "$view" || return 1
  # Nor may it jump the merge queue: while another pull request holds the turn, only that one merges.
  turn=$(gh_land_holder) || { echo "github: #$pr: could not ask GitHub who holds the merge turn; merge refused" >&2; return 1; }
  if [ -n "$turn" ] && [ "${turn%%$'\t'*}" != "$pr" ]; then
    echo "github: #$pr: it is #${turn%%$'\t'*}'s turn to merge (${turn#*$'\t'}); land with hack/lib/github.sh land $pr, which waits for its turn" >&2
    return 1
  fi
  # Nor may it be behind its base: the merged tree is one no make check ran on.
  base=$(jq -r '.baseRefName // "main"' <<<"$view")
  head=$(jq -r '.headRefOid // empty' <<<"$view")
  [ -n "$head" ] || { echo "github: #$pr: GitHub did not say its head commit; merge refused" >&2; return 1; }
  behind=$(gh_api "repos/$C4_GITHUB_REPO/compare/$base...$head" --jq .behind_by) || {
    echo "github: #$pr: could not ask GitHub whether it is behind $base; merge refused" >&2; return 1; }
  if [ "$behind" != 0 ]; then
    echo "github: #$pr is $behind commit(s) behind $base: land it (hack/lib/github.sh land $pr), which merges $base in, reruns make check and merges in its turn" >&2
    return 1
  fi
  gh_token || return 1
  gh pr merge "$pr" --repo "$C4_GITHUB_REPO" --merge --match-head-commit "$head"
}

# gh_bugfix_check PR VIEW: refuse (exit 1, naming the rule and the issue) a
# pull request that closes a kind/bug issue but changes no test file
# (*_test.go, *_test.sh, *.test.ts or .tsx), unless a line of its body is
# "No test: <why>" (a docs-only or chart-only fix says so). VIEW is the pull
# request as gh_merge's GraphQL query reads it: body, closingIssuesReferences
# with their labels, and files. Bugs came back after fixes with no test that
# would have caught them (#606 then #864, #835 then #1019). A pull request of
# more than 100 files is let through: the first 100 can't say it has none.
gh_bugfix_check() {
  local pr=$1 view=$2 bugs
  bugs=$(jq -r '.closingIssuesReferences.nodes[]? | select(any(.labels.nodes[]?; .name == "kind/bug")) | "#\(.number)"' <<<"$view" | paste -sd, - | sed 's/,/, /g')
  [ -n "$bugs" ] || return 0
  jq -e '((.files.totalCount // 0) > 100) or any(.files.nodes[]?.path; test("(_test\\.(go|sh)|\\.test\\.tsx?)$"))' <<<"$view" >/dev/null && return 0
  grep -qiE '^[[:space:]]*No test:[[:space:]]*[^[:space:]]' <<<"$(jq -r '.body // ""' <<<"$view")" && return 0
  echo "github: #$pr closes $bugs, a kind/bug issue, but changes no test file (*_test.go, *_test.sh, *.test.ts): a bug fix adds a test that failed before the fix and names its issue (AGENTS.md, How to Work); if no test can, say why on a line of the body: No test: <why>" >&2
  return 1
}

# gh_bugfix_pr PR: gh_bugfix_check for pull request PR, read from GitHub;
# land runs it before it queues for the merge turn.
gh_bugfix_pr() {
  local pr=$1 view
  view=$(gh_api graphql -F owner="${C4_GITHUB_REPO%/*}" -F name="${C4_GITHUB_REPO#*/}" -F pr="$pr" -f query='
    query($owner: String!, $name: String!, $pr: Int!) { repository(owner: $owner, name: $name) { pullRequest(number: $pr) {
      body closingIssuesReferences(first: 100) { nodes { number labels(first: 30) { nodes { name } } } }
      files(first: 100) { totalCount nodes { path } } } } }' \
    --jq .data.repository.pullRequest) || { echo "github: #$pr: could not ask GitHub what it closes and changes; land refused" >&2; return 1; }
  gh_bugfix_check "$pr" "$view"
}

# gh_duplicate N M: comment on N that it duplicates M, close N as not planned
# and drop its status labels.
gh_duplicate() {
  local n=${1:?usage: duplicate N M} m=${2:?usage: duplicate N M}
  [[ $n =~ ^[0-9]+$ && $m =~ ^[0-9]+$ && $n != "$m" ]] || { echo "github: duplicate takes two issue numbers" >&2; return 1; }
  echo "Duplicate of #$m." | gh_comment "$n" - >/dev/null || return 1
  gh_api --method PATCH "repos/$C4_GITHUB_REPO/issues/$n" \
    -f state=closed -f state_reason=not_planned --jq '"closed #\(.number) as a duplicate of #'"$m"'"' || return 1
  gh_unstatus "$n"
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

# gh_activity: one line, UPDATED SHA: the newest updated_at among the issues
# and pull requests (the report issue left out, so the maintainer's own report
# doesn't count) and main's commit. It changes whenever anything does.
gh_activity() {
  local updated sha
  updated=$(gh_api "repos/$C4_GITHUB_REPO/issues?state=all&sort=updated&direction=desc&per_page=20" \
    --jq "[.[] | select(.labels | any(.name == \"$GH_REPORT_LABEL\") | not) | .updated_at] | first // \"none\"") || return 1
  sha=$(gh_api "repos/$C4_GITHUB_REPO/commits/main" --jq .sha) || return 1
  [ -n "$updated" ] && [ -n "$sha" ] || { echo "github: activity: GitHub gave no answer" >&2; return 1; }
  echo "$updated $sha"
}

# gh_report FILE: make FILE the body of the open issue labeled
# maintainer/report, and print its URL. The first time there is none, create
# it (and the label) and pin it; a pin that fails is said, not fatal. The issue
# carries no status, so nothing picks or triages it.
gh_report() {
  local body n url id line color desc
  body=$(gh_read "${1:?usage: report FILE}") || return 1
  [ -n "$body" ] || { echo "github: empty report" >&2; return 1; }
  n=$(gh_api "repos/$C4_GITHUB_REPO/issues?state=open&labels=${GH_REPORT_LABEL//\//%2F}&per_page=10" \
    --jq '[.[] | select(.pull_request | not) | .number] | min // empty') || return 1
  if [ -n "$n" ]; then
    jq -n --arg b "$body" '{body: $b}' |
      gh_api --method PATCH "repos/$C4_GITHUB_REPO/issues/$n" --input - --jq .html_url
    return
  fi
  if ! gh_api "repos/$C4_GITHUB_REPO/labels/${GH_REPORT_LABEL//\//%2F}" >/dev/null 2>&1; then
    for line in "${GH_LABELS[@]}"; do
      [[ $line == "$GH_REPORT_LABEL|"* ]] || continue
      IFS='|' read -r _ color desc <<<"$line"
      gh_api --method POST "repos/$C4_GITHUB_REPO/labels" -f name="$GH_REPORT_LABEL" -f color="$color" \
        -f description="$desc" >/dev/null || return 1
    done
  fi
  url=$(jq -n --arg t "$GH_REPORT_TITLE" --arg b "$body" --arg l "$GH_REPORT_LABEL" '{title: $t, body: $b, labels: [$l]}' |
    gh_api --method POST "repos/$C4_GITHUB_REPO/issues" --input - --jq '"\(.node_id) \(.html_url)"') || return 1
  id=${url%% *} url=${url#* }
  gh_api graphql -f query='mutation($id: ID!) { pinIssue(input: {issueId: $id}) { issue { number } } }' \
    -f id="$id" >/dev/null || echo "github: created $url but could not pin it; pin it by hand" >&2
  echo "$url"
}

# gh_note_body KEY [HEALTH [NEEDS]]: the report body on stdin with the lines
# marked <!-- watch:KEY --> replaced by HEALTH, first under "## Health", and
# "- [ ] NEEDS", first under "## Needs you" (see the usage above). A heading
# the body lacks is added at its end. Needs you's "Nothing." line goes while
# a note is there, and comes back when removing the note empties it.
GH_NOTHING='Nothing. The project runs without you.'
gh_note_body() {
  local key="<!-- watch:${1:?usage: report-note KEY [HEALTH [NEEDS]]} -->" health=${2:-} needs=${3:-${2:-}}
  H=$health N=$needs awk -v key="$key" -v nothing="$GH_NOTHING" '
    { l[++n] = $0 }
    # empty(J): whether the section after line J has no line but blank ones.
    function empty(j) {
      for (j++; j <= m && o[j] !~ /^## /; j++) if (o[j] !~ /^[ \t]*$/) return 0
      return 1
    }
    # put(J, LINE): print heading J, its blank line, then LINE; return the last line printed.
    function put(j, line) {
      print o[j]
      if (j < m && o[j + 1] ~ /^[ \t]*$/) print o[++j]
      print line
      return j
    }
    END {
      h = ENVIRON["H"]; nd = ENVIRON["N"]
      for (i = 1; i <= n; i++) {
        if (index(l[i], key)) continue
        if (l[i] ~ /^## /) sec = l[i]
        if (h != "" && sec == "## Needs you" && index(l[i], nothing) == 1) continue
        o[++m] = l[i]
      }
      while (m > 0 && o[m] ~ /^[ \t]*$/) m--
      for (j = 1; j <= m; j++) {
        if (h != "" && o[j] == "## Needs you" && !needs_done) { j = put(j, "- [ ] " nd " " key); needs_done = 1; continue }
        if (h != "" && o[j] == "## Health" && !health_done) { j = put(j, h " " key); health_done = 1; continue }
        if (h == "" && o[j] == "## Needs you" && empty(j)) {
          print o[j]; print ""; print nothing
          for (j++; j <= m && o[j] !~ /^## /; j++) ;
          if (j <= m) { print ""; j-- }
          continue
        }
        print o[j]
      }
      if (h != "" && !needs_done) { if (m) print ""; print "## Needs you"; print ""; print "- [ ] " nd " " key; m++ }
      if (h != "" && !health_done) { if (m) print ""; print "## Health"; print ""; print h " " key }
    }'
}

# gh_report_note KEY [HEALTH [NEEDS]]: see the usage above. The report is
# edited only when the note changes it; exit 1 when there is no report.
gh_report_note() {
  local n body new
  n=$(gh_api "repos/$C4_GITHUB_REPO/issues?state=open&labels=${GH_REPORT_LABEL//\//%2F}&per_page=10" \
    --jq '[.[] | select(.pull_request | not) | .number] | min // empty') || return 1
  [ -n "$n" ] || { echo "github: no Maintainer report issue to note in" >&2; return 1; }
  body=$(gh_api "repos/$C4_GITHUB_REPO/issues/$n" --jq '.body // ""') || return 1
  new=$(gh_note_body "$@" <<<"$body") || return 1
  [ "$new" != "$body" ] || return 0
  jq -n --arg b "$new" '{body: $b}' | gh_api --method PATCH "repos/$C4_GITHUB_REPO/issues/$n" --input - >/dev/null
}

# The maintainer lease (#526): the maintainer runs once per project, not once
# per device. Before hack/relay.sh --maintain starts a session it takes the
# lease, a comment on the pinned Maintainer report issue that starts with
# <!-- c4-maintainer --> and names the host and pid. It is live while it was
# edited within C4_LEASE_TTL, as a claim is; the relay refreshes it every
# GH_BEAT_EVERY seconds while the session runs and deletes it when the session
# ends. Only the relay's maintainer, under its device lock
# (.relay/maintain.lock), takes it, so a live lease of this host is one a
# crashed maintainer left: it is taken over like a stale one.
GH_MAINT_MARKER='<!-- c4-maintainer -->'
GH_MAINT_JQ=$GH_NOW_JQ'
  def lease: .body | sub("^\\s+"; "") | startswith($mm);
  def lfield($f): [.body | capture($f + " `(?<v>[^`]+)`") | .v] | first // "";
  def fresh: (.updated_at | fromdateiso8601) > (now - $ttl);
  def mine: lfield("host") == $host;
  def age: (now - (.updated_at | fromdateiso8601)) / 60 | floor | "\(.)m ago";
'

# gh_report_issue: the Maintainer report issue's number, creating it (with a
# placeholder the first sweep replaces) when there is none.
gh_report_issue() {
  local n
  n=$(gh_api "repos/$C4_GITHUB_REPO/issues?state=open&labels=${GH_REPORT_LABEL//\//%2F}&per_page=10" \
    --jq '[.[] | select(.pull_request | not) | .number] | min // empty') || return 1
  if [ -z "$n" ]; then
    gh_report - <<<"No sweep has written this report yet." >/dev/null || return 1
    n=$(gh_api "repos/$C4_GITHUB_REPO/issues?state=open&labels=${GH_REPORT_LABEL//\//%2F}&per_page=10" \
      --jq '[.[] | select(.pull_request | not) | .number] | min // empty') || return 1
  fi
  [ -n "$n" ] || { echo "github: no Maintainer report issue to hold the lease" >&2; return 1; }
  echo "$n"
}

# gh_maint_body: the lease comment's body.
gh_maint_body() {
  printf '%s\n**maintainer** sweeps · host `%s` · pid `%s`\nHeartbeat: %s\n' \
    "$GH_MAINT_MARKER" "${C4_HOST:-$(hostname)}" "${GH_MAINT_PID:-$$}" "$(date -u +%Y-%m-%dT%H:%MZ)"
}

# gh_maint_take: take the maintainer lease and print its comment's ID. Exit 4:
# another host holds a live one, or won the race to it; prints who, as "host
# H (pid P), heartbeat 5m ago". Exit 1: GitHub could not be asked.
gh_maint_take() {
  local ttl n all id holder winner
  ttl=$(gh_ttl) || return 1
  n=$(gh_report_issue) || return 1
  all=$(gh_comments "$n") || return 1
  local q=(--arg mm "$GH_MAINT_MARKER" --argjson ttl "$ttl" --arg host "${C4_HOST:-$(hostname)}")
  holder=$(jq -r "${q[@]}" "$GH_MAINT_JQ"'
    [.[] | select(lease and fresh and (mine | not))] | min_by(.id) // empty
    | "host \(lfield("host")) (pid \(lfield("pid"))), heartbeat \(age)"' <<<"$all")
  if [ -n "$holder" ]; then
    echo "$holder"
    return 4
  fi
  # Stale leases, and this host's (its maintainer crashed), are deleted.
  while read -r id; do
    [ -n "$id" ] || continue
    gh_api --method DELETE "repos/$C4_GITHUB_REPO/issues/comments/$id" >/dev/null || return 1
  done < <(jq -r "${q[@]}" "$GH_MAINT_JQ"'.[] | select(lease and ((fresh | not) or mine)) | .id' <<<"$all")
  # Post, wait, and keep it only if it is the oldest live lease.
  id=$(gh_maint_body | jq -Rs '{body: .}' |
    gh_api --method POST "repos/$C4_GITHUB_REPO/issues/$n/comments" --input - --jq .id) || return 1
  sleep "$GH_CLAIM_SETTLE"
  if ! all=$(gh_comments "$n"); then
    gh_maint_release "$id"
    return 1
  fi
  winner=$(jq -r "${q[@]}" "$GH_MAINT_JQ"'
    [.[] | select(lease and fresh)] | min_by(.id) // empty
    | "\(.id)\thost \(lfield("host")) (pid \(lfield("pid"))), heartbeat \(age)"' <<<"$all")
  if [ "${winner%%$'\t'*}" != "$id" ]; then
    gh_maint_release "$id"
    echo "${winner#*$'\t'}"
    return 4
  fi
  echo "$id"
}

# gh_maint_beat ID: refresh lease ID's heartbeat. Exit 1: it is gone (another
# host took it over as stale) or GitHub could not be asked.
gh_maint_beat() { gh_edit_comment "${1:?usage: gh_maint_beat ID}" "$(gh_maint_body)"; }

# gh_maint_release ID: delete lease ID; one already gone is released too.
gh_maint_release() {
  local out
  out=$(gh_api --method DELETE "repos/$C4_GITHUB_REPO/issues/comments/${1:?usage: gh_maint_release ID}" 2>&1) && return 0
  grep -q 'HTTP 404' <<<"$out" && return 0
  echo "$out" >&2
  return 1
}

# The merge turn (#1037): merge refuses a pull request behind main, so with
# several agents landing at once a branch's make check is outrun by the next
# merge to main, over and over. So one pull request at a time, across every
# device, merges main in, runs make check and merges. land PR queues for that
# turn with a comment on the Maintainer report issue that starts with
# <!-- c4-land -->, names the agent, the pull request, the host and the pid,
# and is refreshed every GH_LAND_BEAT seconds. The oldest fresh one holds the
# turn (first come, first served); one not edited within C4_LAND_TTL was
# abandoned and is deleted by the next land that sees it. land deletes its own
# when it ends, merged or not.
GH_LAND_MARKER='<!-- c4-land -->'
C4_LAND_TTL=${C4_LAND_TTL:-45m}
GH_LAND_POLL=${GH_LAND_POLL:-60}    # seconds between looks at the queue
GH_LAND_BEAT=${GH_LAND_BEAT:-300}   # seconds between heartbeats of a place in it
GH_LAND_SETTLE=${GH_LAND_SETTLE:-3} # seconds between asks whether GitHub saw the push
# git fetch tries before land gives its turn up; worktrees share refs/remotes,
# so a fetch in another one at the same moment fails this one's ref update.
GH_LAND_FETCH_TRIES=${GH_LAND_FETCH_TRIES:-5}
GH_LAND_FETCH_PAUSE=${GH_LAND_FETCH_PAUSE:-5} # seconds between those tries
GH_LAND_JQ=$GH_NOW_JQ'
  def turn: .body | sub("^\\s+"; "") | startswith($lm);
  def fresh: (.updated_at | fromdateiso8601) > (now - $ttl);
  def lfield($f): [.body | capture($f + " `(?<v>[^`]+)`") | .v] | first // "";
  def lname: [.body | capture("\\*\\*(?<n>[^*]+)\\*\\*") | .n] | first // "someone";
  def who: "\(lname) lands #\(lfield("pull request")) (host \(lfield("host"))), heartbeat \((now - (.updated_at | fromdateiso8601)) / 60 | floor)m ago";
'

# gh_land_issue: the Maintainer report issue's number, or nothing when there is none.
gh_land_issue() {
  gh_api "repos/$C4_GITHUB_REPO/issues?state=open&labels=${GH_REPORT_LABEL//\//%2F}&per_page=10" \
    --jq '[.[] | select(.pull_request | not) | .number] | min // empty'
}

# gh_land_queue N: the merge-turn comments on issue N, as JSON.
gh_land_queue() {
  local all ttl
  ttl=$(gh_seconds "$C4_LAND_TTL" C4_LAND_TTL) || return 1
  all=$(gh_comments "$1") || return 1
  jq -c --arg lm "$GH_LAND_MARKER" --argjson ttl "$ttl" "$GH_LAND_JQ"'
    [.[] | select(turn) | . + {fresh: fresh, who: who, pr: lfield("pull request")}]' <<<"$all"
}

# gh_land_holder: who holds the merge turn, as PR<tab>WHO; nothing when nobody
# does. Exit 1: GitHub could not be asked.
gh_land_holder() {
  local n q
  n=$(gh_land_issue) || return 1
  [ -n "$n" ] || return 0
  q=$(gh_land_queue "$n") || return 1
  jq -r '[.[] | select(.fresh)] | min_by(.id) // empty | "\(.pr)\t\(.who)"' <<<"$q"
}

# gh_land_body PR: a merge-turn comment's body.
gh_land_body() {
  local name
  name=$(jq -r '.name // empty' "$GH_CLAIM_FILE" 2>/dev/null)
  printf '%s\n**%s** lands pull request `%s` · host `%s` · pid `%s`\nHeartbeat: %s\n' "$GH_LAND_MARKER" \
    "${name:-${USER:-someone}}" "$1" "${C4_HOST:-$(hostname)}" "${GH_LAND_PID:-$$}" "$(date -u +%Y-%m-%dT%H:%MZ)"
}

# The review pass (#1104): nothing read a change before it landed but its
# author, and bugs named earlier pull requests as their cause. So a pull
# request over GH_REVIEW_LINES changed lines in hand-written files lands only
# with a review: a comment starting with Review:, posted through the owner's
# account (every agent posts as the owner, so an outsider's comment never
# counts) and naming its agent on an Agent: line, each finding a top-level
# list item that starts with Fixed or Dismissed (the handoff skill has the format).
GH_REVIEW_LINES=${GH_REVIEW_LINES:-400}
GH_REVIEW_SKIP='(^|/)zz_generated\.[^/]*$|^config/crd/bases/|^config/rbac/role\.yaml$|^docs/screenshots/|(^|/)(go\.sum|go\.work\.sum|package-lock\.json|npm-shrinkwrap\.json|yarn\.lock|pnpm-lock\.yaml|bun\.lockb?|uv\.lock|poetry\.lock|Cargo\.lock|Gemfile\.lock|composer\.lock|Chart\.lock)$'

# gh_review_lines PR: the lines PR adds and deletes in hand-written files.
gh_review_lines() {
  local files
  files=$(gh_api --paginate "repos/$C4_GITHUB_REPO/pulls/$1/files?per_page=100" \
    --jq '.[] | "\(.additions + .deletions)\t\(.filename)"') || return 1
  # Through the environment: awk -v would read the regexp's backslashes as escapes.
  SKIP=$GH_REVIEW_SKIP awk -F'\t' '$2 !~ ENVIRON["SKIP"] { n += $1 } END { print n + 0 }' <<<"$files"
}

# gh_review_check PR: exit 0 when PR is small or carries a review pass that
# counts (see above); else exit 1, saying what is missing.
gh_review_check() {
  local pr=${1:?usage: review-check PR} lines pages verdict
  [[ $pr =~ ^[0-9]+$ ]] || { echo "github: review-check takes a pull request number" >&2; return 64; }
  lines=$(gh_review_lines "$pr") || { echo "github: #$pr: could not ask GitHub how many lines it changes" >&2; return 1; }
  if ((lines <= GH_REVIEW_LINES)); then
    echo "github: #$pr changes $lines lines in hand-written files: no review pass needed" >&2
    return 0
  fi
  pages=$(gh_api --paginate --slurp "repos/$C4_GITHUB_REPO/issues/$pr/comments?per_page=100") || {
    echo "github: #$pr: could not read its comments" >&2; return 1; }
  # The newest Review: comment that counts decides; an outsider's or one naming no agent never does.
  verdict=$(jq -r '
    def review: .body // "" | sub("^\\s+"; "") | startswith("Review:");
    def ours: .author_association | IN("OWNER", "MEMBER", "COLLABORATOR");
    def agent: [.body | capture("(?m)^[ \\t]*Agent:[ \\t]*(?<n>[^\\s]+)") | .n] | first;
    # A finding is a top-level list item outside a code fence, a rule (* * *) is none;
    # settled, it starts with Fixed or Dismissed (bold or not).
    def open: [foreach (.body | gsub("[\r\t]"; " ") | split("\n")[]) as $l ({f: false};
        .l = $l | if $l | test("^\\s*(```|~~~)") then .f = (.f | not) | .l = null else . end; .)
      | select(.f | not) | .l // empty
      | select(test("^([-*+]|[0-9]+[.)])\\s+\\S") and (test("^([-*_]\\s*){3,}$") | not))
      | select(test("(?i)^([-*+]|[0-9]+[.)])\\s+[*_[]*(fixed|dismissed)\\b") | not)];
    [.[][] | select(review)] as $all
    | ([$all[] | select(ours and agent != null)] | last) as $r
    | if $r == null then
        ($all | last) as $l
        | if $l == null then "none"
          elif ($l | ours | not) then "outsider\t\($l.user.login) (\($l.author_association))"
          else "noagent\t\($l.html_url)" end
      elif ($r | open | length) > 0 then "open\t\($r.html_url)\t\($r | open | first)\t\($r | open | length)"
      else "ok\t\($r | agent)\t\($r.html_url)" end' <<<"$pages") || return 1
  local how="run /code-review in a subagent, post its findings as a comment that starts with Review: and names the agent on an Agent: line, fix or dismiss each, then land (the handoff skill, \"Review before landing\")"
  case ${verdict%%$'\t'*} in
    ok)
      verdict=${verdict#*$'\t'}
      echo "github: #$pr changes $lines lines in hand-written files; reviewed by ${verdict%%$'\t'*} (${verdict#*$'\t'})" >&2
      return 0 ;;
    none) echo "github: #$pr changes $lines lines in hand-written files (over $GH_REVIEW_LINES) and has no review pass: $how" >&2 ;;
    outsider) echo "github: #$pr changes $lines lines in hand-written files (over $GH_REVIEW_LINES); its Review: comment by ${verdict#*$'\t'} doesn't count, only one posted through the owner's account does: $how" >&2 ;;
    noagent) echo "github: #$pr changes $lines lines in hand-written files (over $GH_REVIEW_LINES); its Review: comment (${verdict#*$'\t'}) names no agent: add an Agent: NAME line to it" >&2 ;;
    open)
      local rest=${verdict#*$'\t'} url item n
      IFS=$'\t' read -r url item n <<<"$rest"
      echo "github: #$pr changes $lines lines in hand-written files (over $GH_REVIEW_LINES); $n finding(s) of its review ($url) start with neither Fixed nor Dismissed, the first: \"$item\"; fix or dismiss each and say so in the comment" >&2 ;;
  esac
  return 1
}

# gh_land PR [--wait DURATION]: wait for the merge turn, take it, merge the
# base into this checkout's branch, run make check, push and merge PR (gh_merge),
# then give the turn up, also when a step fails. Exit 0: merged. Exit 1: refused
# or failed, the reason said. Exit 4: still not its turn after DURATION (default 2h).
gh_land() {
  local pr=${1:?usage: land PR [--wait DURATION]} wait=2h
  [[ $pr =~ ^[0-9]+$ ]] || { echo "github: land takes a pull request number" >&2; return 64; }
  shift
  case "${1:-}" in
    --wait) wait=${2:?usage: land PR [--wait DURATION]} ;;
    '') ;;
    *) echo "usage: land PR [--wait DURATION]" >&2; return 64 ;;
  esac
  ( gh_land_turn "$pr" "$wait" )
}

gh_land_turn() {
  local pr=$1 wait ttl info state base head branch n id me q first ahead said= t0 beat rc log local_head s who i try=1
  wait=$(gh_seconds "$2" --wait) || return 64
  ttl=$(gh_seconds "$C4_LAND_TTL" C4_LAND_TTL) || return 64
  info=$(gh_api "repos/$C4_GITHUB_REPO/pulls/$pr" --jq '"\(.state) \(.base.ref) \(.head.ref)"') || return 1
  read -r state base head <<<"$info"
  [ "$state" = open ] || { echo "github: #$pr is $state; nothing to land" >&2; return 1; }
  branch=$(git -C "$GH_LIB_ROOT" symbolic-ref --short -q HEAD)
  [ "$branch" = "$head" ] || {
    echo "github: #$pr's branch is $head, but this checkout is on ${branch:-a detached HEAD}: land it from its own checkout" >&2; return 1; }
  [ -z "$(git -C "$GH_LIB_ROOT" status --porcelain --untracked-files=no)" ] || {
    echo "github: this checkout has uncommitted changes: commit and push them, then land" >&2; return 1; }
  gh_bugfix_pr "$pr" || return 1
  gh_review_check "$pr" || return 1
  n=$(gh_report_issue) || return 1
  id=$(gh_land_body "$pr" | jq -Rs '{body: .}' |
    gh_api --method POST "repos/$C4_GITHUB_REPO/issues/$n/comments" --input - --jq .id) || return 1
  [ -n "$id" ] || { echo "github: could not queue #$pr for the merge turn" >&2; return 1; }
  # Give the place up however this ends; jobs it forks inherit the trap, so only this shell runs it.
  me=$BASHPID
  GH_LAND_BEATER=
  trap 'if [ "$BASHPID" = "'"$me"'" ]; then [ -z "$GH_LAND_BEATER" ] || kill "$GH_LAND_BEATER" 2>/dev/null; gh_maint_release "'"$id"'" >/dev/null 2>&1 ||
    echo "github: could not delete the merge turn (comment '"$id"' on #'"$n"'); it lapses after $C4_LAND_TTL" >&2; fi' EXIT
  trap 'exit 130' INT TERM
  t0=$SECONDS beat=$SECONDS
  while :; do
    q=$(gh_land_queue "$n") || { echo "github: could not read the merge queue on #$n" >&2; return 1; }
    while IFS=$'\t' read -r s who; do
      [ -n "$s" ] || continue
      gh_maint_release "$s" >/dev/null && echo "github: dropped an abandoned merge turn: $who" >&2
    done < <(jq -r '.[] | select(.fresh | not) | "\(.id)\t\(.who)"' <<<"$q")
    jq -e --argjson id "$id" 'any(.[]; .id == $id and .fresh)' <<<"$q" >/dev/null || {
      echo "github: #$pr lost its place in the merge queue (its comment on #$n is gone or lapsed); land again" >&2; return 1; }
    first=$(jq -r '[.[] | select(.fresh)] | min_by(.id) | "\(.id)\t\(.who)"' <<<"$q")
    [ "${first%%$'\t'*}" = "$id" ] && break
    ahead=$(jq --argjson id "$id" '[.[] | select(.fresh and .id < $id)] | length' <<<"$q")
    if [ "$said" != "$ahead ${first%%$'\t'*}" ] || ((SECONDS - beat >= GH_LAND_BEAT)); then
      echo "github: #$pr waits for its turn to land: $ahead ahead; ${first#*$'\t'}" >&2
      said="$ahead ${first%%$'\t'*}"
    fi
    if ((SECONDS - t0 >= wait)); then
      echo "github: #$pr gave up its place after $2: still $ahead ahead; ${first#*$'\t'}" >&2
      return 4
    fi
    if ((SECONDS - beat >= GH_LAND_BEAT)); then
      gh_edit_comment "$id" "$(gh_land_body "$pr")" || echo "github: could not refresh #$pr's place in the merge queue" >&2
      beat=$SECONDS
    fi
    sleep "$GH_LAND_POLL"
  done
  echo "github: #$pr's turn to land: merging origin/$base, then make check" >&2
  gh_edit_comment "$id" "$(gh_land_body "$pr")" || true
  until git -C "$GH_LIB_ROOT" fetch -q origin; do
    ((try >= GH_LAND_FETCH_TRIES)) && { echo "github: #$pr: git fetch origin failed $try times" >&2; return 1; }
    echo "github: #$pr: git fetch origin failed; trying again in ${GH_LAND_FETCH_PAUSE}s" >&2
    sleep "$GH_LAND_FETCH_PAUSE"
    ((try++))
  done
  if ! git -C "$GH_LIB_ROOT" merge -q --no-edit "origin/$base" >&2; then
    git -C "$GH_LIB_ROOT" merge --abort 2>/dev/null
    echo "github: #$pr: merging origin/$base into $branch conflicts; merge it by hand (git merge origin/$base), make check, push and land again" >&2
    return 1
  fi
  mkdir -p "$GH_PENDING_DIR"
  log=$GH_PENDING_DIR/land-$pr.log
  # The heartbeat; its sleep is killed with it, and holds no pipe of the caller's open.
  (
    trap 'kill "$s" 2>/dev/null; exit 0' TERM
    while :; do
      sleep "$GH_LAND_BEAT" &
      s=$!
      wait "$s"
      gh_edit_comment "$id" "$(gh_land_body "$pr")"
    done
  ) >/dev/null 2>&1 &
  GH_LAND_BEATER=$!
  "${C4_MAKE:-make}" -C "$GH_LIB_ROOT" check >"$log" 2>&1
  rc=$?
  kill "$GH_LAND_BEATER" 2>/dev/null
  wait "$GH_LAND_BEATER" 2>/dev/null
  GH_LAND_BEATER=
  if [ "$rc" != 0 ]; then
    tail -40 "$log" >&2
    echo "github: #$pr: make check failed (exit $rc) with origin/$base merged in (the merge is committed here, not pushed; the whole output is in $log); fix it, push and land again" >&2
    return 1
  fi
  git -C "$GH_LIB_ROOT" push -q origin HEAD || { echo "github: #$pr: git push failed" >&2; return 1; }
  # merge reads the head from GitHub: wait until it shows the push.
  local_head=$(git -C "$GH_LIB_ROOT" rev-parse HEAD)
  for i in 1 2 3 4 5 6 7 8 9 10; do
    [ "$(gh_api "repos/$C4_GITHUB_REPO/pulls/$pr" --jq .head.sha)" = "$local_head" ] && break
    sleep "$GH_LAND_SETTLE"
  done
  gh_merge "$pr"
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
  runs=$(jq -c '[.[] | select(.conclusion != "cancelled" and .conclusion != "skipped")] | sort_by(.createdAt) | reverse' <<<"$runs") ||
    return 2
  # A failed run its runner lost says nothing about the code either: drop it
  # like a cancelled one, and keep the two newest left.
  local kept='[]' run
  while IFS= read -r run; do
    [ "$(jq length <<<"$kept")" -lt 2 ] || break
    if jq -e '.conclusion == "failure"' <<<"$run" >/dev/null; then
      gh_lost "$(jq -r .databaseId <<<"$run")"
      case $? in
        0) continue ;;
        1) ;;
        *) return 2 ;;
      esac
    fi
    kept=$(jq -c --argjson r "$run" '. + [$r]' <<<"$kept")
  done < <(jq -c '.[]' <<<"$runs")
  runs=$kept
  n=$(jq '[.[] | select(.conclusion == "failure" or .conclusion == "timed_out" or .conclusion == "startup_failure")] | length' <<<"$runs")
  [ "$n" = 2 ] || return 1
  # A run whose jobs GitHub never started says nothing about the code: when
  # the newest is one, Actions is at fault (exit 3); when only the older is,
  # main hasn't failed twice.
  local why rc
  why=$(gh_unstarted "$(jq -r '.[0].databaseId' <<<"$runs")")
  rc=$?
  case $rc in
    0)
      echo "GitHub Actions started no job of \`$(jq -r '.[0].workflowName' <<<"$runs")\` (\`$wf\`) on main, so main's CI says nothing about the code. Its annotation:"
      echo
      sed 's/^/> /' <<<"$why"
      echo
      jq -r '.[0] | "Run: [\(.databaseId)](\(.url)) at \(.createdAt), commit \(.headSha[:7]): \(.displayTitle)"' <<<"$runs"
      echo
      echo "Only the owner can fix this (the account's billing or spending limit, or Actions settings); no code change will. Filed by hack/relay-watch.sh, which files no \"main is red\" issue while it lasts. Close this issue once Actions runs jobs again."
      return 3
      ;;
    1) ;;
    *) return 2 ;;
  esac
  why=$(gh_unstarted "$(jq -r '.[1].databaseId' <<<"$runs")")
  case $? in
    0) return 1 ;;
    1) ;;
    *) return 2 ;;
  esac
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

# gh_queued [--minutes M]: see the usage above; exit 2 when GitHub can't say.
gh_queued() {
  local minutes=30 runs
  while [ $# -gt 0 ]; do
    case $1 in
      --minutes) minutes=${2:?usage: queued [--minutes M]}; shift 2 ;;
      *) echo "usage: queued [--minutes M]" >&2; return 64 ;;
    esac
  done
  [[ $minutes =~ ^[0-9]+$ ]] || { echo "usage: queued [--minutes M]" >&2; return 64; }
  gh_token || return 2
  runs=$(gh run list --repo "$C4_GITHUB_REPO" --branch main --status queued --limit 100 \
    --json databaseId,status,createdAt) || return 2
  runs=$(jq -r --argjson m "$minutes" '[.[] | select(.status == "queued")] | sort_by(.createdAt) |
    select(length > 0 and (now - (.[0].createdAt | fromdateiso8601)) > $m * 60) | "\(.[0].createdAt)\t\(length)"' <<<"$runs") ||
    return 2
  [ -n "$runs" ] || return 1
  echo "$runs"
}

# gh_fail_specs: the Ginkgo specs a failed log on stdin names in its
# "[FAIL] <spec>" summary lines, once each, colour codes, carriage returns
# and "[It] " dropped and tabs made spaces (the spec is a column of a TSV).
# gh run view --log-failed writes a colour code's escape as the text "^[" when
# its output isn't a terminal, and as the byte when it is: both go.
gh_fail_specs() {
  tr -d '\r' | LC_ALL=C sed -E "s/("$'\e'"|\\^\\[)\\[[0-9;]*m//g" | sed -nE 's/^.*\[FAIL\] +(.*[^ ])[ ]*$/\1/p' |
    sed 's/\[It\] //g; s/\t/ /g' | awk '!seen[$0]++'
}

# gh_flakes [--days N] [--seen FILE] [--count]: see the usage above. A run
# whose log can't be read is left out of FILE, so the next call reads it again;
# FILE keeps only the runs still in the window.
gh_flakes() {
  local days=7 seen= count= since runs id at log out= read=
  while [ $# -gt 0 ]; do
    case $1 in
      --days) days=${2:?usage: flakes [--days N] [--seen FILE] [--count]}; shift 2 ;;
      --seen) seen=${2:?usage: flakes [--days N] [--seen FILE] [--count]}; shift 2 ;;
      --count) count=1; shift ;;
      *) echo "usage: flakes [--days N] [--seen FILE] [--count]" >&2; return 64 ;;
    esac
  done
  gh_token || return 1
  since=$(date -u -d "$days days ago" +%Y-%m-%dT%H:%M:%SZ) || return 1
  runs=$(gh run list --repo "$C4_GITHUB_REPO" --branch main --status failure --created ">=${since%%T*}" --limit 100 \
    --json databaseId,createdAt --jq ".[] | select(.createdAt >= \"$since\") | \"\(.databaseId)\t\(.createdAt)\"") || return 1
  while IFS=$'\t' read -r id at; do
    [ -n "$id" ] || continue
    if [ -n "$seen" ] && grep -qxF "$id" "$seen" 2>/dev/null; then
      read+="$id"$'\n'
      continue
    fi
    log=$(gh run view "$id" --repo "$C4_GITHUB_REPO" --log-failed 2>/dev/null) || continue
    read+="$id"$'\n'
    out+=$(gh_fail_specs <<<"$log" | awk -v r="$id" -v t="$at" '{ print $0 "\t" r "\t" t }')$'\n'
  done <<<"$runs"
  [ -z "$seen" ] || printf '%s' "$read" | grep -v '^$' >"$seen"
  out=$(grep -v '^$' <<<"$out")
  if [ -n "$count" ]; then
    [ -z "$out" ] || cut -f1 <<<"$out" | sort | uniq -c | sort -k1,1nr -k2 | sed -E 's/^ *([0-9]+) /\1\t/'
  else
    [ -z "$out" ] || printf '%s\n' "$out"
  fi
}

# gh_lost RUN: exit 0 when failed RUN failed for its runner, not its code: a
# self-hosted runner stopped mid-job (its step is cancelled, "The operation
# was canceled.") or never took the job (the job is cancelled, without steps,
# "not acquired by Runner"). No job timed out, none has a failed step, and none
# failed without steps (that is gh_unstarted's). Exit 1 otherwise, 2 when
# GitHub can't say.
gh_lost() {
  local jobs
  jobs=$(gh run view "${1:?usage: gh_lost RUN}" --repo "$C4_GITHUB_REPO" --json jobs --jq '.jobs') || return 2
  jq -e 'length > 0 and all(.[]; .conclusion != "timed_out"
    and (((.steps // []) | length > 0) or .conclusion != "failure")
    and all((.steps // [])[]; .conclusion != "failure"))' <<<"$jobs" >/dev/null || return 1
}

# gh_unstarted RUN: print the annotations of RUN's failed jobs and exit 0 when
# GitHub never started any of them: RUN has failed jobs, none has a step, and
# each has an annotation saying the job was not started (failed payments, a
# spending limit). Exit 1 when a failed job started, 2 when GitHub can't say.
gh_unstarted() {
  local run=${1:?usage: gh_unstarted RUN} jobs id msg out=
  jobs=$(gh run view "$run" --repo "$C4_GITHUB_REPO" --json jobs \
    --jq '[.jobs[] | select(.conclusion == "failure" or .conclusion == "timed_out" or .conclusion == "startup_failure")]') ||
    return 2
  [ "$(jq length <<<"$jobs")" -gt 0 ] || return 1
  [ "$(jq '[.[] | select((.steps // []) | length > 0)] | length' <<<"$jobs")" = 0 ] || return 1
  for id in $(jq -r '.[].databaseId' <<<"$jobs"); do
    msg=$(gh_api "repos/$C4_GITHUB_REPO/check-runs/$id/annotations" --jq '.[].message') || return 2
    grep -qi 'was not started' <<<"$msg" || return 1
    [[ $'\n'"$out"$'\n' == *$'\n'"$msg"$'\n'* ]] || out+=${out:+$'\n'}$msg
  done
  printf '%s\n' "$out"
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
  # The Maintainer report is no piece of work: no source, no status.
  grep -qxF -- "$GH_REPORT_LABEL" <<<"$labels" && return 0
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
    escalated) gh_escalated ;;
    parents) gh_parents "$@" ;;
    show) gh_show "$@" ;;
    take) gh_take "$@" ;;
    release) gh_release "$@" ;;
    claims) gh_claims ;;
    beat) gh_beat ;;
    handoff) gh_handoff "$@" ;;
    flush) gh_flush ;;
    comment) gh_comment "$@" ;;
    label) gh_label "$@" ;;
    milestone) gh_milestone "$@" ;;
    milestones) gh_milestones ;;
    cluster) gh_cluster "$@" ;;
    status) gh_set_status "$@" ;;
    close) gh_close "$@" ;;
    closes-check) gh_closes_check "$@" ;;
    pr-check) gh_pr_check "$@" ;;
    merge) gh_merge "$@" ;;
    land) gh_land "$@" ;;
    review-check) gh_review_check "$@" ;;
    duplicate) gh_duplicate "$@" ;;
    closed) gh_closed "$@" ;;
    create) gh_create "$@" ;;
    intake) gh_intake "$@" ;;
    red) gh_red "$@" ;;
    flakes) gh_flakes "$@" ;;
    queued) gh_queued "$@" ;;
    activity) gh_activity ;;
    report) gh_report "$@" ;;
    report-note) gh_report_note "$@" ;;
    *)
      sed -n '2,/^$/{s/^# \{0,1\}//;p}' "$0"
      [ "$cmd" = help ] || exit 64
      ;;
  esac
fi
