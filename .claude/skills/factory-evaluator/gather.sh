#!/usr/bin/env bash
# Gathers read-only facts about the running factory for the factory-evaluator
# skill: Tasks and their rounds, Crews, operator log errors, resource use,
# Gitea Actions runs and issues, and the relay's session accounts. It writes
# small files to OUT, computes OUT/metrics.json, and prints OUT/summary.md.
# It never changes the factory and never prints a secret.
#
#   gather.sh OUT SINCE        SINCE: UTC, e.g. 2026-10-03T00:00:00Z
#
# Env: EVAL_PROJECT (default cel-lens), RELAY_DIR (default: the main checkout's
# .relay), GITEA_PORT (default 3927, apart from factory-ops' 3917).
#
# No `set -e`: one source failing must not hide the others. Each failure is
# written to OUT/failures.txt and shown at the top of summary.md.
set -uo pipefail

OUT=${1:?usage: gather.sh OUT SINCE}
SINCE=${2:?usage: gather.sh OUT SINCE}
SINCE=$(date -u -d "$SINCE" +%Y-%m-%dT%H:%M:%SZ) || { echo "bad SINCE" >&2; exit 2; }
NOW=$(date -u +%Y-%m-%dT%H:%M:%SZ)
P=${EVAL_PROJECT:-cel-lens}
PORT=${GITEA_PORT:-3927}
if [ -z "${RELAY_DIR:-}" ]; then
  common=$(git rev-parse --path-format=absolute --git-common-dir 2>/dev/null) || common=$PWD/.git
  RELAY_DIR=$(dirname "$common")/.relay
fi
RAW=$OUT/raw
mkdir -p "$RAW/pages"
: >"$OUT/failures.txt"
fail() { echo "- $*" >>"$OUT/failures.txt"; }

# jq helpers shared by the Task queries.
JQ_DEFS='
def ts: sub("\\.[0-9]+"; "") | sub("\\+00:00$"; "Z") | fromdateiso8601;
def r1: . * 10 | round / 10;
def r2: . * 100 | round / 100;
def median: sort | if length == 0 then null
  elif length % 2 == 1 then .[length / 2 | floor]
  else (.[length / 2 - 1] + .[length / 2]) / 2 end;
def bucket: if .reason == "AgentUnavailable" then "unavailable"
  elif .reason == "WaitingForSlot" then "slot"
  elif .reason == "ResolvingConflicts" then "conflicts"
  else .phase | ascii_downcase end;
'

# --- Tasks -------------------------------------------------------------------
tasks() {
  kubectl -n "$P" get tasks.c4.factory.io -o json >"$RAW/tasks.json" || { fail "kubectl get tasks"; return; }
  # One object per Task that finished in the window or is still active, with
  # the minutes it spent in each phase (and in each kind of waiting).
  jq --arg since "$SINCE" --arg now "$NOW" "$JQ_DEFS"'
    [ .items[]
      | select(.status.completedAt == null or (.status.completedAt | ts) >= ($since | ts))
      | . as $t
      | ($t.status.history // [] | sort_by(.at)) as $h
      | ($t.status.completedAt // $now) as $stop
      | { name: $t.metadata.name,
          crew: $t.spec.containerRef.name,
          issue: $t.spec.issue,
          phase: $t.status.phase,
          round: ($t.status.round // 0),
          conflicts: ([$h[] | select(.reason == "ResolvingConflicts")] | length),
          created: $t.metadata.creationTimestamp,
          completed: $t.status.completedAt,
          minutes: ((($stop | ts) - ($t.metadata.creationTimestamp | ts)) / 60 | floor),
          phaseMinutes: ([ range(0; $h | length) as $i
              | { k: ($h[$i] | bucket),
                  v: ((if $i + 1 < ($h | length) then $h[$i + 1].at else $stop end | ts) - ($h[$i].at | ts)) } ]
            | group_by(.k) | map({ (.[0].k): ((map(.v) | add) / 60 | floor) }) | add // {}),
          followUps: ($t.status.followUps // [] | length),
          standardChange: ($t.status.standardChange // null),
          verdict: ($t.status.review.verdict // null),
          failure: (if $t.status.phase == "Failed"
            then (($t.status.conditions // [] | map(select(.type == "Succeeded"))[0].message) // "" | .[0:400])
            else null end) } ]' "$RAW/tasks.json" >"$OUT/tasks.json" || { fail "jq tasks"; return; }

  jq -r '["task","issue","phase","round","conflicts","minutes","implementing","checking","reviewing","blocked","unavailable","slot","followUps"],
    (.[] | [.name, .issue, .phase, .round, .conflicts, .minutes,
      (.phaseMinutes.implementing // 0), (.phaseMinutes.checking // 0), (.phaseMinutes.reviewing // 0),
      (.phaseMinutes.blocked // 0), (.phaseMinutes.unavailable // 0), (.phaseMinutes.slot // 0), .followUps])
    | @tsv' "$OUT/tasks.json" | column -t >"$OUT/tasks.tsv"

  # Why rounds were not accepted, and how failed Tasks ended.
  jq -r --arg since "$SINCE" "$JQ_DEFS"'
    .items[]
    | select(.status.completedAt == null or (.status.completedAt | ts) >= ($since | ts))
    | .metadata.name as $n
    | ((.status.history // [])[]
        | select(.reason | test("NextRound|ResolvingConflicts|AgentUnavailable|WaitingForStandardChange|ReviewerFailed|Failed"))
        | "\($n) r\(.round) \(.at) \(.reason): \(.message | .[0:300])"),
      (select(.status.phase == "Failed") | "\($n) FAILED review: \(.status.review.summary // "" | .[0:600])")
  ' "$RAW/tasks.json" >"$OUT/rounds.txt" || fail "jq rounds"
}

# --- Crews -------------------------------------------------------------------
crews() {
  kubectl -n "$P" get crews.c4.factory.io -o json >"$RAW/crews.json" || { fail "kubectl get crews"; return; }
  jq -r '.items[] | "\(.metadata.name): phase \(.status.phase), paused \(.spec.paused // false), parallel \(.spec.parallel // 1), active \(.status.activeTasks // [] | join(" ")), ready \(.status.ready // [] | map(tostring) | join(" ")), waiting \(.status.waiting // [] | map(tostring) | join(" ")), polled \(.status.lastPolled)\n  " + (.status.conditions // [] | map("\(.type)=\(.status) \(.reason) since \(.lastTransitionTime): \(.message | .[0:300])") | join("\n  "))' \
    "$RAW/crews.json" >"$OUT/crews.txt" || fail "jq crews"
}

# --- Operator ----------------------------------------------------------------
operator() {
  kubectl -n c4-factory-system get pods -o json |
    jq -r '.items[] | select(.metadata.name | startswith("c4-factory-controller-manager"))
      | "\(.metadata.name) started \(.status.startTime), restarts \([.status.containerStatuses[]?.restartCount] | add)"' \
      >"$OUT/operator.txt" || fail "operator pod"
  kubectl -n c4-factory-system logs deploy/c4-factory-controller-manager --since-time="$SINCE" >"$RAW/operator.log" 2>/dev/null ||
    { fail "operator logs"; return; }
  echo "log lines in window: $(wc -l <"$RAW/operator.log") (the log starts when the pod started; a deploy loses the earlier ones)" >>"$OUT/operator.txt"
  # Errors and warnings grouped by message and error text, numbers folded.
  grep -aE $'^[0-9-]+T[^\t]*\t(ERROR|WARN)\t' "$RAW/operator.log" |
    awk -F'\t' '{
      msg = $3; if (NF >= 5) msg = $3 ": " $4
      err = ""; if (match($0, /"error": "([^"\\]|\\.)*"/)) { err = substr($0, RSTART + 10, RLENGTH - 11); gsub(/\\"/, "\"", err) }
      obj = ""; if (match($0, /"(Task|Crew|Container)": \{"name":"[^"]*"/)) obj = substr($0, RSTART, RLENGTH)
      if (length(err) > 200) err = substr(err, 1, 90) " ... " substr(err, length(err) - 90)
      key = $2 " " msg " | " err; gsub(/[0-9]+/, "N", key)
      c[key]++; if (!(key in f)) { f[key] = $1; s[key] = obj }; l[key] = $1
    } END { for (k in c) printf "%d x  %s .. %s  %s  %s\n", c[k], f[k], l[k], k, s[k] }' |
    sort -rn | head -25 >>"$OUT/operator.txt"
}

# --- Resources ---------------------------------------------------------------
resources() {
  {
    echo "## node"; kubectl top node 2>&1 | head -3
    kubectl get nodes -o json | jq -r '.items[] | "allocatable cpu \(.status.allocatable.cpu), memory \(.status.allocatable.memory); pressure: \([.status.conditions[] | select(.type != "Ready" and .status == "True") | .type] | join(",") | if . == "" then "none" else . end)"'
    echo "## host disk"; df -h / | tail -1
    echo "## top pods by cpu"; kubectl top pods -A --sort-by=cpu 2>&1 | head -10
    echo "## top pods by memory"; kubectl top pods -A --sort-by=memory 2>&1 | head -8
    echo "## restarted containers (last termination; IN WINDOW marks one since SINCE)"
    kubectl get pods -A -o json | jq -r --arg since "$SINCE" '.items[] | .metadata as $m | .status.containerStatuses[]?
      | select(.restartCount > 0)
      | "\(if (.lastState.terminated.finishedAt // "") >= $since then "IN WINDOW " else "" end)\($m.namespace)/\($m.name) \(.name): \(.restartCount) restarts, last \(.lastState.terminated.reason // "?") at \(.lastState.terminated.finishedAt // "?")"'
    echo "## etcd warnings in the window (slow disk or overload)"
    kubectl -n kube-system logs -l component=etcd --tail=-1 --since-time="$SINCE" 2>/dev/null |
      grep -oE 'slow fdatasync|apply request took too long|ReadIndex response took too long|failed to send out heartbeat' | sort | uniq -c
    echo "## warning events (the API keeps about an hour)"
    kubectl get events -A --field-selector type=Warning -o json | jq -r '
      .items | group_by(.involvedObject.namespace + " " + .involvedObject.kind + " " + .reason)
      | map("\(map(.count // 1) | add) x \(.[0].involvedObject.namespace) \(.[0].involvedObject.kind) \(.[0].reason): \(.[-1].message | .[0:160])")
      | .[]' | head -15
  } >"$OUT/resources.txt" 2>&1 || fail "resources"
}

# --- Gitea -------------------------------------------------------------------
gitea() {
  local pw pf ok=""
  pw=$(kubectl -n gitea get secret gitea-admin -o jsonpath='{.data.password}' | base64 -d) || { fail "gitea admin secret"; return; }
  kubectl -n gitea port-forward svc/gitea "$PORT:3000" >/dev/null 2>&1 &
  pf=$!
  for _ in $(seq 1 30); do
    curl -s -o /dev/null "http://localhost:$PORT/api/v1/version" && { ok=1; break; }
    sleep 1
  done
  if [ -z "$ok" ]; then fail "gitea port-forward"; kill "$pf" 2>/dev/null; return; fi
  # The password goes to curl on stdin, so it is in no argument list.
  # Gitea under load answers late; try each read three times.
  get() {
    local try
    for try in 1 2 3; do
      curl -sf -m 30 -K- "http://localhost:$PORT/api/v1$1" <<<"user = \"c4admin:$pw\"" && return 0
      [ "$try" -lt 3 ] && sleep 3
    done
    return 1
  }

  local repos r page n oldest
  repos=$(jq -r '.items[].spec.containerRef.name' "$RAW/crews.json" 2>/dev/null | sort -u)
  [ -n "$repos" ] || fail "no Crews, so no repositories to read"
  for r in $repos; do
    # Actions runs (newest first), until older than the window.
    for page in 1 2 3 4 5 6 7 8; do
      get "/repos/$P/$r/actions/tasks?limit=50&page=$page" >"$RAW/pages/actions-$r-$page.json" || { fail "actions $r page $page"; break; }
      n=$(jq '.workflow_runs | length' "$RAW/pages/actions-$r-$page.json")
      [ "$n" -gt 0 ] || break
      oldest=$(jq -r '[.workflow_runs[].created_at] | min' "$RAW/pages/actions-$r-$page.json")
      [[ "$oldest" < "$SINCE" ]] && break
    done
    jq -s --arg r "$r" '[.[].workflow_runs[]?] | map(. + {repo: $r})' "$RAW"/pages/actions-"$r"-*.json >"$RAW/actions-$r.json"
    # Issues: every open one, and those closed in the window.
    for page in 1 2 3 4; do
      get "/repos/$P/$r/issues?state=open&type=issues&limit=50&page=$page" >"$RAW/pages/open-$r-$page.json" || { fail "open issues $r"; break; }
      [ "$(jq length "$RAW/pages/open-$r-$page.json")" -eq 50 ] || break
    done
    jq -s --arg r "$r" '[.[][]] | map(. + {repo: $r})' "$RAW"/pages/open-"$r"-*.json >"$RAW/open-$r.json"
    get "/repos/$P/$r/issues?state=closed&type=issues&since=$SINCE&limit=50" |
      jq --arg r "$r" 'map(. + {repo: $r})' >"$RAW/closed-$r.json" || fail "closed issues $r"
    get "/repos/$P/$r/milestones?state=all&limit=50" |
      jq -r --arg r "$r" '.[] | "\($r) \(.title): due \(.due_on // "none"), \(.open_issues) open, \(.closed_issues) closed, \(.state)"' \
        >>"$OUT/milestones.txt" || fail "milestones $r"
  done
  kill "$pf" 2>/dev/null

  jq -s 'add // []' "$RAW"/actions-*.json >"$OUT/actions.json" || fail "jq actions"
  # Per repository, workflow and result: runs in the window and their minutes.
  # Release runs are listed one by one, with those of the oldest page read.
  jq -r --arg since "$SINCE" "$JQ_DEFS"'
    def mins: ((.updated_at | ts) - (.run_started_at | ts)) / 60;
    (map(select((.created_at | ts) >= ($since | ts)))
     | group_by(.repo + " " + .workflow_id + " " + .status)
     | map("\(.[0].repo) \(.[0].workflow_id) \(.[0].status): \(length) runs, median \(map(mins) | median | r1) min, max \(map(mins) | max | r1) min")[]),
    "release runs:",
    (map(select(.workflow_id | test("release")))[]
     | "  \(.repo) \(.head_branch) \(.status) \(.run_started_at): \(mins | r1) min")
  ' "$OUT/actions.json" >"$OUT/actions.txt" || fail "jq actions summary"

  jq -s 'add // []' "$RAW"/open-*.json >"$OUT/issues-open.json" 2>/dev/null
  jq -s 'add // []' "$RAW"/closed-*.json >"$OUT/issues-closed.json" 2>/dev/null
  jq -r --arg since "$SINCE" '
    def labels: [.labels[].name] | join(",");
    "open issues by milestone and status:",
    (group_by(.repo + " " + (.milestone.title // "none") + " " + ([.labels[].name | select(startswith("status/"))] | join(",")))
     | map("  \(.[0].repo) \(.[0].milestone.title // "no milestone") [\([.[0].labels[].name | select(startswith("status/"))] | join(","))]: \(length) (\(map(select(.created_at >= $since)) | length) new)")[]),
    "needs-human (open):",
    (map(select(any(.labels[]; .name == "status/needs-human")))[] | "  \(.repo)#\(.number) \(.title | .[0:90]) (updated \(.updated_at))"),
    "standard changes (open):",
    (map(select(any(.labels[]; .name == "kind/standard-change")))[] | "  \(.repo)#\(.number) \(.title | .[0:90])")
  ' "$OUT/issues-open.json" >"$OUT/issues.txt" || fail "jq issues"
}

# --- Relay -------------------------------------------------------------------
relay() {
  [ -d "$RELAY_DIR" ] || { fail "no relay directory at $RELAY_DIR"; return; }
  {
    echo "## relay.log in window"
    awk -v s="${SINCE:0:19}" 'substr($1, 1, 19) >= s' "$RELAY_DIR/relay.log" 2>/dev/null | cut -c1-220 | tail -40
    echo "## evaluate.log (last 5)"
    tail -5 "$RELAY_DIR/evaluate.log" 2>/dev/null
    echo "## session accounts in window (file, turns, minutes, USD, error)"
  } >"$OUT/relay.txt"
  find "$RELAY_DIR" -maxdepth 2 -name '*.json' -newermt "$SINCE" -size +0 2>/dev/null | sort >"$RAW/accounts.lst"
  : >"$RAW/accounts.jsonl"
  while read -r f; do
    jq -c --arg f "$(basename "$f")" 'select(.type == "result")
      | {file: $f, turns: .num_turns, minutes: ((.duration_ms // 0) / 60000 | floor),
         usd: ((.total_cost_usd // 0) * 100 | round / 100), error: .is_error}' "$f" 2>/dev/null >>"$RAW/accounts.jsonl"
  done <"$RAW/accounts.lst"
  jq -r '"  \(.file) \(.turns) turns, \(.minutes) min, $\(.usd), error \(.error)"' "$RAW/accounts.jsonl" >>"$OUT/relay.txt"
}

# --- Metrics -----------------------------------------------------------------
metrics() {
  local relayj gitj
  relayj=$(jq -s "$JQ_DEFS"'{
      sessions: map(select(.file | startswith("evaluate") | not)) | {count: length, usd: ((map(.usd) | add // 0) | r2), minutes: (map(.minutes) | add // 0)},
      evaluations: map(select(.file | startswith("evaluate"))) | {count: length, usd: ((map(.usd) | add // 0) | r2)} }' \
    "$RAW/accounts.jsonl" 2>/dev/null) || relayj=null
  gitj=$(jq -n --arg since "$SINCE" --slurpfile o "$OUT/issues-open.json" --slurpfile c "$OUT/issues-closed.json" '{
      laterOpen: ($o[0] | map(select(.milestone.title == "Later")) | length),
      laterNew: ($o[0] | map(select(.milestone.title == "Later" and .created_at >= $since)) | length),
      needsHumanOpen: ($o[0] | map(select(any(.labels[]; .name == "status/needs-human"))) | length),
      readyOpen: ($o[0] | map(select(any(.labels[]; .name == "status/ready"))) | length),
      closedInWindow: ($c[0] | length) }' 2>/dev/null) || gitj=null
  jq --arg since "$SINCE" --arg now "$NOW" --argjson relay "$relayj" --argjson gitea "$gitj" \
     --arg opErrors "$(grep -caE $'\tERROR\t' "$RAW/operator.log" 2>/dev/null)" \
     --arg crewErrors "$(grep -caE $'\tERROR\t.*"Crew": ' "$RAW/operator.log" 2>/dev/null)" "$JQ_DEFS"'
    def stats: (map(select(.phase == "Merged"))) as $m | {
      merged: ($m | length),
      failed: (map(select(.phase == "Failed")) | length),
      active: (map(select(.completed == null)) | length),
      firstRoundApprovalPct: (if ($m | length) > 0 then (($m | map(select(.round == 1)) | length) * 100 / ($m | length) | round) else null end),
      roundsPerMerged: (if ($m | length) > 0 then ($m | map(.round) | add / length | r1) else null end),
      hoursPerMergedMedian: ($m | map(.minutes / 60) | median | if . == null then null else r2 end),
      waitMinutes: { unavailable: (map(.phaseMinutes.unavailable // 0) | add // 0),
                     slot: (map(.phaseMinutes.slot // 0) | add // 0),
                     blocked: (map(.phaseMinutes.blocked // 0) | add // 0),
                     conflicts: (map(.phaseMinutes.conflicts // 0) | add // 0) },
      checkingMinutesMedian: ($m | map(.phaseMinutes.checking // 0) | median),
      followUpsFiled: (map(.followUps) | add // 0) };
    { window: {since: $since, until: $now},
      tasks: stats,
      byCrew: (group_by(.crew) | map({ (.[0].crew): stats }) | add // {}),
      relay: $relay, gitea: $gitea,
      operatorErrorLines: ($opErrors | tonumber? // null),
      crewErrorLines: ($crewErrors | tonumber? // null),
      agentCost: null }
    | .relay.maintenanceUsdPerMerged = (if (.tasks.merged > 0 and $relay != null) then ($relay.sessions.usd / .tasks.merged * 100 | round / 100) else null end)
  ' "$OUT/tasks.json" >"$OUT/metrics.json" || fail "jq metrics"
}

crews
tasks
operator
resources
gitea
relay
metrics

{
  echo "# Factory facts for $P, $SINCE .. $NOW"
  echo
  if [ -s "$OUT/failures.txt" ]; then echo "## Gathering failures"; cat "$OUT/failures.txt"; echo; fi
  echo "## Metrics (metrics.json)"; jq -c . "$OUT/metrics.json" 2>/dev/null; echo
  echo "## Crews"; cat "$OUT/crews.txt" 2>/dev/null; echo
  echo "## Tasks in the window (minutes per phase)"; cat "$OUT/tasks.tsv" 2>/dev/null; echo
  echo "## Rounds not accepted, waits and failures"; cut -c1-400 "$OUT/rounds.txt" 2>/dev/null; echo
  echo "## Operator"; cat "$OUT/operator.txt" 2>/dev/null; echo
  echo "## Resources"; cat "$OUT/resources.txt" 2>/dev/null; echo
  echo "## Gitea Actions in the window"; cat "$OUT/actions.txt" 2>/dev/null
  echo
  echo "## Gitea issues"; cat "$OUT/issues.txt" "$OUT/milestones.txt" 2>/dev/null; echo
  echo "## Relay"; cat "$OUT/relay.txt" 2>/dev/null
} >"$OUT/summary.md"
echo "$OUT/summary.md"
