#!/usr/bin/env bash
# Tests for hack/relay-watch.sh: when a round starts the relay, and when it
# starts a triage of the proposed issues, with pick, the proposed list and
# hack/relay.sh faked; and when a CI round files a "main is red" issue, with
# gh faked under the real hack/lib/github.sh.
# Run: hack/relay-watch_test.sh
T=$(mktemp -d)
trap 'kill $(jobs -p) 2>/dev/null; rm -rf "$T"' EXIT
unset C4_CLUSTER
# shellcheck source=hack/relay-watch.sh
. "$(dirname "$0")/relay-watch.sh"
DIR=$T/relay LOG=$T/log WORKERS=2 RETRY=600 TRIAGE_EVERY=1800
mkdir -p "$DIR"
fail=0
check() { if [ "$2" = "$3" ]; then echo "ok   $1"; else echo "FAIL $1: got [$2] want [$3]"; fail=1; fi; }

PICKED= PICK_RC=1 RELAY_RC=0 PROPOSED= PROPOSED_RC=0 TRIAGE_RC=0
pick_next() { [ -n "$PICKED" ] && printf '%s\n' "$PICKED"; return "$PICK_RC"; }
proposed() { [ -n "$PROPOSED" ] && printf '%s\n' $PROPOSED; return "$PROPOSED_RC"; }
run_relay() { echo started >>"$T/starts"; echo "worker 1 started"; return "$RELAY_RC"; }
run_triage() { echo triage >>"$T/triages"; echo "triage started in worker 2"; return "$TRIAGE_RC"; }
starts() { cat "$T/starts" 2>/dev/null | wc -l | tr -d ' '; }
triages() { cat "$T/triages" 2>/dev/null | wc -l | tr -d ' '; }
# aged: make the last triage WATCH_TRIAGE old.
aged() { sed -i "1s/^[0-9]*/$(($(date +%s) - TRIAGE_EVERY))/" "$DIR/triaged"; }
logged() { grep -c "$1" "$LOG" 2>/dev/null || true; }

# Nothing ready, on a device without the cluster: wait, and say so once.
C4_CLUSTER=none
round; round
check "idle: no relay" "$(starts)" 0
check "idle: logged once" "$(logged 'nothing ready; waiting')" 1
check "idle, nothing proposed: no triage" "$(triages)" 0

# Nothing ready, two issues proposed: one triage, logged on one line.
PROPOSED='40 41'
round
check "proposed: starts a triage" "$(triages)" 1
check "proposed: one line" "$(logged 'triage: proposed #40 #41 (new: #40 #41); hack/relay.sh --triage: triage started in worker 2$')" 1
check "proposed: kept" "$(cut -d' ' -f2- "$DIR/triaged")" "40 41"
check "proposed: no relay" "$(starts)" 0

# The same set again, and a smaller one (the triage readied #40): no triage.
round
PROPOSED='41'
round
check "same set: no second triage" "$(triages)" 1
check "same set: one line in all" "$(logged 'triage:')" 1

# A new proposed issue: not before WATCH_TRIAGE, then once.
PROPOSED='41 42'
round
check "new issue: waits for WATCH_TRIAGE" "$(triages)" 1
aged
round; round
check "new issue: triage after WATCH_TRIAGE" "$(triages)" 2
check "new issue: names the new one" "$(logged 'triage: proposed #41 #42 (new: #42)')" 1

# A triage that fails to start: logged, and the same set tries again after WATCH_TRIAGE.
PROPOSED='43' TRIAGE_RC=1
echo 0 >"$DIR/triaged"
round
check "triage fails: logged" "$(logged 'triage: proposed #43 (new: #43).*(failed, exit 1; again after 30m)')" 1
TRIAGE_RC=0
round
check "triage fails: not straight away" "$(triages)" 3
aged
round
check "triage fails: again after WATCH_TRIAGE" "$(triages)" 4

# The triage started but its session never ran: hack/relay.sh --triage kept
# only the time, so the same issues start it again after WATCH_TRIAGE.
round
check "session never ran: not straight away" "$(triages)" 4
echo "$(($(date +%s) - TRIAGE_EVERY))" >"$DIR/triaged"
round
check "session never ran: again after WATCH_TRIAGE" "$(triages)" 5

# The proposed issues can't be listed: no triage, logged once.
PROPOSED='github: no token' PROPOSED_RC=2
echo 0 >"$DIR/triaged"
round; round
check "proposed fails: no triage" "$(triages)" 5
check "proposed fails: logged once" "$(logged 'could not list the proposed issues (exit 2)')" 1
PROPOSED= PROPOSED_RC=0

# An issue is ready: start the relay, and log the issue and its output.
PICKED=$'12\tFix the guides\thttps://github.com/x/y/issues/12\tready' PICK_RC=0
round
check "ready: starts the relay" "$(starts)" 1
check "ready: logs the issue" "$(logged 'ready: 12 | Fix the guides')" 1
check "ready: logs the relay's output" "$(logged 'relay: worker 1 started')" "$(starts)"

# Still unclaimed right after: wait for RETRY, saying so once.
round; round
check "unclaimed: no second start" "$(starts)" 1
check "unclaimed: logged once" "$(logged '#12 is still unclaimed')" 1
LAST_T=$(($(date +%s) - RETRY))
round
check "unclaimed: starts again after RETRY" "$(starts)" 2

# Another issue: start straight away.
PICKED=$'13\tAnother\thttps://github.com/x/y/issues/13\tready'
round
check "another issue: starts the relay" "$(starts)" 3

# Every worker runs: don't even ask GitHub.
PICKED=$'14\tA third\thttps://github.com/x/y/issues/14\tready'
flock -o "$DIR/lock-1" sleep 30 &
flock -o "$DIR/lock-2" sleep 30 &
until ! flock -n "$DIR/lock-1" true && ! flock -n "$DIR/lock-2" true; do sleep 0.1; done
round
check "busy: no relay" "$(starts)" 3
kill %1 %2; wait 2>/dev/null

# A free worker again: issue 14 starts it.
round
check "free again: starts the relay" "$(starts)" 4

# hack/relay.sh fails: log it.
PICKED=$'15\tFourth\thttps://github.com/x/y/issues/15\tready' RELAY_RC=1
round
check "relay fails: logged" "$(logged 'hack/relay.sh failed (exit 1)')" 1
RELAY_RC=0

# GitHub can't be reached: no relay, logged once.
PICKED='github: no token' PICK_RC=2
round; round
check "pick fails: no relay" "$(starts)" 5
check "pick fails: logged once" "$(logged 'could not ask GitHub for the next issue (exit 2): github: no token')" 1

# Main's CI, with gh faked: runs from $T/runs-<file>.json, open issues in
# $T/issues.json, and every issue filed appended there.
HERE=$(cd "$(dirname "$0")" && pwd)
REPO=$T/repo
mkdir -p "$REPO/.github/workflows" "$T/bin"
printf 'name: Tests\non:\n  push:\n    branches: [main]\n  pull_request:\n' >"$REPO/.github/workflows/test.yml"
printf 'name: Lint\non:\n  push:\n    branches:\n      - main\n' >"$REPO/.github/workflows/lint.yml"
printf 'name: Release\non:\n  push:\n    tags: [main]\n' >"$REPO/.github/workflows/release.yml"
printf 'name: E2E\non:\n  workflow_dispatch:\n  pull_request:\n    branches: [main]\n' >"$REPO/.github/workflows/e2e.yml"
github() { "$HERE/lib/github.sh" "$@"; }
echo '[]' >"$T/issues.json"
cat >"$T/bin/gh" <<'GH'
#!/usr/bin/env bash
q= wf= id=
args=("$@")
for ((i = 0; i < $#; i++)); do
  case ${args[i]} in
    --jq) q=${args[i + 1]} ;;
    --workflow) wf=${args[i + 1]} ;;
  esac
done
case "$1 $2 $* " in
  "run list"*) cat "$FAKE/runs-$wf.json" 2>/dev/null || echo '[]' ;;
  "run view"*)
    if [[ " $* " == *" --log-failed "* ]]; then seq -f 'log line %g' 1 50
    else echo '{"jobs":[{"name":"Run on Ubuntu","conclusion":"failure"},{"name":"Setup","conclusion":"success"}]}' | jq -r "$q"; fi ;;
  *" --method POST "*)
    n=$(($(jq length "$FAKE/issues.json") + 100))
    jq --argjson n "$n" '. + [input + {number: $n}]' "$FAKE/issues.json" - >"$FAKE/issues.new" &&
      mv "$FAKE/issues.new" "$FAKE/issues.json" && echo "$n" ;;
  *) jq -r "$q" "$FAKE/issues.json" ;;
esac
GH
chmod +x "$T/bin/gh"
export FAKE=$T PATH=$T/bin:$PATH GH_TOKEN=fake
# runs FILE CONCLUSION...: FILE's runs on main, newest first; "" runs still.
runs() {
  local f=$1 i=0 c
  shift
  for c in "$@"; do
    i=$((i + 1))
    jq -n --arg c "$c" --argjson i "$i" '{databaseId: (900 - $i), status: (if $c == "" then "in_progress" else "completed" end), conclusion: $c, headSha: "abcdef\($i)123456",
      displayTitle: "Merge pull request #\($i)", url: "https://x/runs/\(900 - $i)",
      createdAt: "2026-10-05T23:\(50 - $i):00Z", workflowName: "Tests"}'
  done | jq -s . >"$T/runs-$f.json"
}
filed() { jq length "$T/issues.json"; }

check "ci: the workflows that run on a push to main" "$(ci_workflows | tr '\t\n' ':,')" "lint.yml:Lint,test.yml:Tests,"

runs test.yml success failure failure
runs lint.yml failure success
ci_round
check "ci: a pass, or a single failure, files nothing" "$(filed)" 0

runs test.yml failure success failure
runs lint.yml failure cancelled success
ci_round
check "ci: one failure after a pass files nothing" "$(filed)" 0

runs test.yml "" failure failure
ci_round
check "ci: a run still going on main files nothing" "$(filed)" 0

runs test.yml failure failure success
ci_round
check "ci: two failures file one issue" "$(filed)" 1
check "ci: its title" "$(jq -r '.[0].title' "$T/issues.json")" "main is red: Tests"
check "ci: its labels" "$(jq -r '.[0].labels | join(" ")' "$T/issues.json")" "status/ready priority/urgent kind/bug source/dev"
body=$(jq -r '.[0].body' "$T/issues.json")
check "ci: the body names both runs and commits" "$(grep -c '^- \[89[89]\](https://x/runs/89[89]) .*, failure, commit abcdef[12]: Merge pull request #[12]$' <<<"$body")" 2
check "ci: the body names the failing jobs" "$(grep -cx '  - failed job: Run on Ubuntu' <<<"$body")" 2
check "ci: the body has the last 40 log lines" "$(grep -c '^log line' <<<"$body"):$(grep -m1 '^log line' <<<"$body")" "40:log line 11"
check "ci: logged" "$(logged 'main is red: Tests; issue #100$')" 1

ci_round
check "ci: a second round files no other issue" "$(filed)" 1
check "ci: a red main that lasts is logged once" "$(logged 'main is red: Tests')" 1

# Cancelled runs don't count: the two newest that ran both failed.
runs lint.yml failure cancelled timed_out
ci_round
check "ci: cancelled runs are skipped" "$(jq -r '.[1].title' "$T/issues.json")" "main is red: Lint"

# Arguments.
bash "$(dirname "$0")/relay-watch.sh" --workers 0 2>/dev/null
check "--workers 0 refused" $? 64
check "--workers 12" "$(. "$(dirname "$0")/relay-watch.sh" --workers 12; echo "$WORKERS")" 12
bash "$(dirname "$0")/relay-watch.sh" --workers 100 2>/dev/null
check "--workers 100 refused" $? 64
bash "$(dirname "$0")/relay-watch.sh" --bogus 2>/dev/null
check "unknown flag refused" $? 64

exit $fail
