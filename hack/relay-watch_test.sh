#!/usr/bin/env bash
# Tests for hack/relay-watch.sh: when a round starts the relay, and when it
# starts a triage of the proposed issues, and what an idle round logs of the
# issues pick skips, with pick, blocked, cycles, the proposed list and
# hack/relay.sh faked; and when a triage reads a proposed issue again, with
# hack/lib/github.sh's proposed against a stubbed GitHub.
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

PICKED= PICK_RC=1 RELAY_RC=0 PROPOSED= PROPOSED_RC=0 TRIAGE_RC=0 BLOCKED= BLOCKED_RC=0 CYCLES=
pick_next() { [ -n "$PICKED" ] && printf '%s\n' "$PICKED"; return "$PICK_RC"; }
proposed() { echo "$*" >"$T/proposed-args"; [ -n "$PROPOSED" ] && printf '%s\n' $PROPOSED; return "$PROPOSED_RC"; }
blocked() { [ -n "$BLOCKED" ] && printf '%s\n' "$BLOCKED"; return "$BLOCKED_RC"; }
# cycles reports a cycle once, as hack/lib/github.sh cycles --report does.
cycles() { [ -n "$CYCLES" ] && ! grep -qxF "$CYCLES" "$T/cycles" 2>/dev/null && echo "$CYCLES" | tee -a "$T/cycles"; return 0; }
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

# A triage reads a seen proposed issue again: hack/lib/github.sh proposed
# --since, against a stubbed GitHub ($T/gh/*.json), says what changed since it.
mkdir -p "$T/gh"
iso() { date -u -d "@$1" +%Y-%m-%dT%H:%M:%SZ; }
gh_proposed_stubbed() {
  (
    # shellcheck source=hack/lib/github.sh
    . "$(dirname "${BASH_SOURCE[0]}")/lib/github.sh"
    gh_token() { :; }
    gh_api() {
      local a url
      for a in "$@"; do case $a in repos/*) url=$a ;; esac; done
      case $url in
        *issues\?state=open*) cat "$T/gh/open.json" ;;
        *issues\?state=closed*) cat "$T/gh/closed.json" ;;
        */issues/comments\?since=*) cat "$T/gh/comments.json" ;;
        */issues/*/comments*) n=${url#*/issues/}; cat "$T/gh/comments-${n%%/*}.json" 2>/dev/null || echo '[[]]' ;;
      esac
    }
    gh_proposed "$@"
  )
}
proposed() { echo "$*" >"$T/proposed-args"; gh_proposed_stubbed "$@"; }
# open N UPDATED BODY...: the open proposed issues, each updated at epoch UPDATED.
open_issues() {
  local rows=() n u b
  while [ $# -gt 0 ]; do
    n=$1 u=$2 b=$3; shift 3
    rows+=("$(jq -n --argjson n "$n" --arg u "$(iso "$u")" --arg b "$b" \
      '{number: $n, title: "t", body: $b, updated_at: $u, labels: [{name: "status/proposed"}]}')")
  done
  printf '%s\n' "${rows[@]}" | jq -s '[.]' >"$T/gh/open.json"
}
# comment N AT BODY: a comment on N, updated at epoch AT, among those since the last triage.
comment() {
  jq --argjson n "$1" --arg at "$(iso "$2")" --arg b "$3" \
    '.[0] += [{issue_url: "https://api.github.com/repos/x/y/issues/\($n)", updated_at: $at, body: $b}]' \
    "$T/gh/comments.json" >"$T/gh/c" && mv "$T/gh/c" "$T/gh/comments.json"
}
# closed M AT: issue M closed at epoch AT.
closed() {
  jq --argjson m "$1" --arg at "$(iso "$2")" '.[0] += [{number: $m, closed_at: $at}]' \
    "$T/gh/closed.json" >"$T/gh/c" && mv "$T/gh/c" "$T/gh/closed.json"
}
# retriaged: a last triage of 50-53 at WATCH_TRIAGE ago, nothing since it.
retriaged() {
  t0=$(($(date +%s) - TRIAGE_EVERY))
  echo "$t0 50 51 52 53" >"$DIR/triaged"
  echo '[[]]' >"$T/gh/comments.json"
  echo '[[]]' >"$T/gh/closed.json"
  open_issues 50 $((t0 - 100)) "" 51 $((t0 - 100)) "" 52 $((t0 - 100)) "Depends on #127" 53 $((t0 - 100)) ""
}
TRIAGE_RC=0
before=$(triages)

retriaged
round
check "unchanged: asks what changed since the last triage" "$(cat "$T/proposed-args")" "--since $t0"
check "unchanged: no triage" "$(triages)" "$before"

# The triage's own comment bumps the issue's updated_at: no triage.
retriaged
comment 50 $((t0 + 60)) $'<!-- relay-triage -->\nLacks a Done when; waits on #130.'
open_issues 50 $((t0 + 61)) "" 51 $((t0 - 100)) "" 52 $((t0 - 100)) "Depends on #127" 53 $((t0 - 100)) ""
round
check "triage's own comment: no triage" "$(triages)" "$before"

# A comment that isn't the triage's: triage again, once.
comment 51 $((t0 + 120)) "Added the Done when."
open_issues 50 $((t0 + 61)) "" 51 $((t0 + 120)) "" 52 $((t0 - 100)) "Depends on #127" 53 $((t0 - 100)) ""
round
check "changed by a comment: triage" "$(triages)" $((before + 1))
check "changed by a comment: logged" "$(logged 'triage: proposed #50 #51 #52 #53 (changed: #51); ')" 1
round
check "changed by a comment: not twice" "$(triages)" $((before + 1))

# A body edit or a label (only the issue's updated_at moves): triage again.
retriaged
comment 50 $((t0 + 60)) $'<!-- relay-triage -->\nLacks a Done when.'
open_issues 50 $((t0 + 600)) "" 51 $((t0 - 100)) "" 52 $((t0 - 100)) "Depends on #127" 53 $((t0 - 100)) ""
round
check "body edited after the triage's comment: triage" "$(triages)" $((before + 2))
check "body edited: logged" "$(logged '(changed: #50)')" 1

# An issue it depends on closed after the last triage: triage again.
retriaged
closed 127 $((t0 + 30))
round
check "dependency closed: triage" "$(triages)" $((before + 3))
check "dependency closed: logged" "$(logged '(waited on a closed issue: #52)')" 1

# One closed before the last triage: no triage.
retriaged
closed 127 $((t0 - 30))
round
check "dependency closed before: no triage" "$(triages)" $((before + 3))

# An #M in the issue's newest triage comment closed after the last triage: triage again.
retriaged
echo '[[{"id":1,"created_at":"x","updated_at":"x","html_url":"u","user":{"login":"x"},"body":"<!-- relay-triage -->\nWaits for #130 to land."}]]' >"$T/gh/comments-53.json"
closed 130 $((t0 + 30))
round
check "named in the triage comment, closed: triage" "$(triages)" $((before + 4))
check "named in the triage comment: logged" "$(logged '(waited on a closed issue: #53)')" 1

# Nothing changed, but the last triage is WATCH_RETRIAGE old: read them all again.
retriaged
sed -i "1s/^[0-9]*/$(($(date +%s) - RETRIAGE))/" "$DIR/triaged"
round
check "WATCH_RETRIAGE: asks for the list only" "$(cat "$T/proposed-args")" ""
check "WATCH_RETRIAGE: triage" "$(triages)" $((before + 5))
check "WATCH_RETRIAGE: logged" "$(logged '(read again after 3h)')" 1
round
check "WATCH_RETRIAGE: not twice" "$(triages)" $((before + 5))
proposed() { echo "$*" >"$T/proposed-args"; [ -n "$PROPOSED" ] && printf '%s\n' $PROPOSED; return "$PROPOSED_RC"; }

# Nothing ready, and skipped issues wait on issues nobody works: logged once
# per change, on one line.
PROPOSED= SAID=
BLOCKED=$'161 waits on 156 (status/proposed, no claim)\n151 waits on 149 (status/ready, no claim)'
round; round
check "held: logged once" "$(logged 'held back by issues nobody works: 161 waits on 156 (status/proposed, no claim); 151 waits on 149 (status/ready, no claim)$')" 1
BLOCKED='161 waits on 156 (status/proposed, no claim)'
round; round
check "held: a change logged once" "$(logged 'held back by issues nobody works: 161 waits on 156 (status/proposed, no claim)$')" 1
BLOCKED=
round; round
check "held: none left, logged once" "$(logged 'no skipped issue waits on an issue nobody works')" 1
BLOCKED='github: no token' BLOCKED_RC=2
round; round
check "held: GitHub away, logged once" "$(logged 'could not list what holds back the skipped issues (exit 2): github: no token')" 1
BLOCKED='161 waits on 156 (status/proposed, no claim)' BLOCKED_RC=0
round
check "held: back again, logged again" "$(logged 'held back by issues nobody works: 161 waits on 156 (status/proposed, no claim)$')" 2
BLOCKED=
round
# A cycle: cycles --report's account is logged.
CYCLES='cycle #105 #106: commented on #105 #106; #106 to status/needs-human'
round; round
check "cycle: logged once" "$(logged 'cycle #105 #106: commented on #105 #106; #106 to status/needs-human$')" 1
cycles() { echo 'github: no token'; return 2; }
round; round
check "cycle: a failure logged once" "$(logged 'github: no token (cycles --report failed, exit 2)$')" 1
cycles() { [ -n "$CYCLES" ] && ! grep -qxF "$CYCLES" "$T/cycles" 2>/dev/null && echo "$CYCLES" | tee -a "$T/cycles"; return 0; }
CYCLES=

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

# Arguments.
bash "$(dirname "$0")/relay-watch.sh" --workers 0 2>/dev/null
check "--workers 0 refused" $? 64
check "--workers 12" "$(. "$(dirname "$0")/relay-watch.sh" --workers 12; echo "$WORKERS")" 12
bash "$(dirname "$0")/relay-watch.sh" --workers 100 2>/dev/null
check "--workers 100 refused" $? 64
bash "$(dirname "$0")/relay-watch.sh" --bogus 2>/dev/null
check "unknown flag refused" $? 64

exit $fail
