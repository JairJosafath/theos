#!/usr/bin/env bash
# Tests for hack/relay-watch.sh: when a round starts the relay, and when it
# starts a triage of the proposed issues, and what an idle round logs of the
# issues pick skips, with pick, blocked, cycles, the proposed list and
# hack/relay.sh faked; that it tends the plans' parents (parents faked); when a triage reads a proposed issue again, with
# hack/lib/github.sh's proposed against a stubbed GitHub; and when a CI round
# files a "main is red" issue, and when a flake round records a failed spec
# and files a "Flaky:" bug, and when a runner round notes runs queued too long
# in the Maintainer report (report_note faked), with gh faked under the real hack/lib/github.sh;
# when it starts the maintainer, on a schedule or for an issue escalated to it,
# with the activity, the escalated issues and hack/relay.sh --maintain faked;
# --install and --uninstall with systemctl, loginctl and crontab faked; and the
# stop file and the lock in a scratch copy of the watch, never this checkout's
# .relay/.
# Run: hack/relay-watch_test.sh
T=$(mktemp -d)
# Only this shell cleans up (#544): a job killed just after it is forked, before
# it resets the traps it inherits, runs this trap too, and would remove $T.
trap 'if [ "$BASHPID" = $$ ]; then kill $(jobs -p) 2>/dev/null; rm -rf "$T"; fi' EXIT
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
# parents --apply: what it did, and the parents still waiting.
PARENTS= PARENTS_RC=0
parents() { echo x >>"$T/tends"; [ -n "$PARENTS" ] && printf '%s\n' "$PARENTS"; return "$PARENTS_RC"; }
tends() { cat "$T/tends" 2>/dev/null | wc -l | tr -d ' '; }
run_relay() { echo started >>"$T/starts"; echo "worker 1 started"; return "$RELAY_RC"; }
# run_triage records the triage in .relay/triaged as hack/relay.sh --triage
# does, the only writer: the issues (the watch's list) as its session starts,
# only the time when it fails to start one.
run_triage() {
  echo triage >>"$T/triages"
  if [ "$TRIAGE_RC" = 0 ]; then echo "$(date +%s) $list" >"$DIR/triaged"; else date +%s >"$DIR/triaged"; fi
  echo "triage started (pid 1)"
  return "$TRIAGE_RC"
}
# The watch asks for the proposed issues every round here; its WATCH_RETRY
# between asks has its own tests below.
TRIAGE_ASK=0
# The maintainer is off (WATCH_MAINTAIN=0) until its own tests below.
MAINTAIN_EVERY=0 ACT='t1 sha1' ACT_RC=0 MAINT_RC=0
activity() { echo "$ACT"; return "$ACT_RC"; }
ESC= ESC_RC=0
escalated() { [ -n "$ESC" ] && printf '%s\n' "$ESC"; return "$ESC_RC"; }
run_maintain() { echo maintain >>"$T/maintains"; echo "maintainer started (pid 1)"; return "$MAINT_RC"; }
maintains() { cat "$T/maintains" 2>/dev/null | wc -l | tr -d ' '; }
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
check "proposed: one line" "$(logged 'triage: proposed #40 #41 (new: #40 #41); hack/relay.sh --triage: triage started (pid 1)$')" 1
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

# A triage started by hand (hack/relay.sh --triage from a terminal) records
# its issues as the watch's does (#688): the watch starts none for them, even
# after WATCH_TRIAGE, and still starts one for an issue filed after it.
echo "$(($(date +%s) - 2 * TRIAGE_EVERY)) 41" >"$DIR/triaged"
PROPOSED='41 44 45'
(. "$(dirname "$0")/relay.sh"; DIR=$T/relay DRY_RUN=; triaged "#41 #44 #45")
before=$(triages)
aged
round; round
check "triaged by hand: no triage for its issues" "$(triages)" "$before"
PROPOSED='41 44 45 46'
round
check "triaged by hand: one for an issue filed after it" "$(triages)" $((before + 1))
check "triaged by hand: names the new one" "$(logged 'triage: proposed #41 #44 #45 #46 (new: #46)')" 1

# The proposed issues can't be listed: no triage, logged once.
PROPOSED='github: no token' PROPOSED_RC=2
echo 0 >"$DIR/triaged"
round; round
check "proposed fails: no triage" "$(triages)" 6
check "proposed fails: logged once" "$(logged 'could not list the proposed issues (exit 2)')" 1
PROPOSED= PROPOSED_RC=0

# Issues ready (#1191): a new proposed issue gets its triage all the same,
# with a worker free or every worker busy, as the triage runs in its own
# worktree; none within WATCH_TRIAGE of the last one, and none while one runs.
PICKED=$'60\tReady\thttps://github.com/x/y/issues/60\tready' PICK_RC=0
echo "$(($(date +%s) - TRIAGE_EVERY)) 41" >"$DIR/triaged"
PROPOSED='41 61'
before=$(triages) relays=$(starts)
round
check "ready: starts a triage" "$(triages)" $((before + 1))
check "ready: names the new one" "$(logged 'triage: proposed #41 #61 (new: #61)')" 1
check "ready: starts the relay too" "$(starts)" $((relays + 1))
PROPOSED='41 61 62'
round
check "ready: none within WATCH_TRIAGE" "$(triages)" $((before + 1))
flock -o "$DIR/lock-1" sleep 30 &
l1=$!
flock -o "$DIR/lock-2" sleep 30 &
l2=$!
until ! flock -n "$DIR/lock-1" true && ! flock -n "$DIR/lock-2" true; do sleep 0.1; done
aged
round
check "every worker busy: starts a triage" "$(triages)" $((before + 2))
check "every worker busy: names the new one" "$(logged 'triage: proposed #41 #61 #62 (new: #62)')" 1
# A triage that runs holds .relay/triage.lock: no second one beside it.
PROPOSED='41 61 62 63'
flock -o "$DIR/triage.lock" sleep 30 &
l3=$!
until ! flock -n "$DIR/triage.lock" true; do sleep 0.1; done
aged
round
check "a triage runs: no second one" "$(triages)" $((before + 2))
kill "$l3"
wait "$l3" 2>/dev/null
round
check "the triage ended: the next one" "$(triages)" $((before + 3))
check "every worker busy: no relay" "$(starts)" $((relays + 1))
kill "$l1" "$l2"
wait "$l1" "$l2" 2>/dev/null

# Past WATCH_TRIAGE, the watch asks for the proposed issues at most every
# WATCH_RETRY, not every round: each ask reads every open issue and the
# comments since the last triage.
TRIAGE_ASK=600 TRIAGE_T=0
asked() { [ -e "$T/proposed-args" ] && echo asked; rm -f "$T/proposed-args"; }
rm -f "$T/proposed-args"
aged
round
check "asks: once past WATCH_TRIAGE" "$(asked)" asked
check "asks: nothing new, no triage" "$(triages)" $((before + 3))
round
check "asks: not again within WATCH_RETRY" "$(asked)" ""
TRIAGE_T=$(($(date +%s) - 600))
round
check "asks: again after WATCH_RETRY" "$(asked)" asked
TRIAGE_ASK=0
# The ready tests below count their own starts and log lines.
PICKED= PICK_RC=1 LAST_N= LAST_T=0 SAID=
rm -f "$T/starts"
: >"$LOG"

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

# The plans' parents: tended every WATCH_PARENTS seconds, busy or idle; what
# it closed or marked is logged, the parents still waiting are not, and a
# failure is logged once.
TEND_T=0 PARENTS_EVERY=600
PARENTS=$'#120 closed: its steps #121, #122 closed; plan #130\n#123 to status/waiting: waits on #124 (of #124, #125; plan #131)\n#128 waits on #124 (of #124; plan #131)'
before=$(tends)
round; round
check "parents: tended once per WATCH_PARENTS" "$(($(tends) - before))" 1
check "parents: what it did is logged" "$(logged "plans' parents: #120 closed: its steps #121, #122 closed; plan #130; #123 to status/waiting: waits on #124 (of #124, #125; plan #131)$")" 1
TEND_T=0 PARENTS='github: no token' PARENTS_RC=2
round; TEND_T=0; round
check "parents: a failure logged once" "$(logged "could not tend the plans' parents (parents --apply, exit 2): github: no token$")" 1
TEND_T=0 PARENTS='#128 waits on #124 (of #124; plan #131)' PARENTS_RC=0
before=$(grep -c "plans' parents" "$LOG")
round
check "parents: only waiting ones, nothing logged" "$(grep -c "plans' parents" "$LOG")" "$before"
PARENTS_EVERY=0 TEND_T=0 PARENTS=
before=$(tends)
round
check "parents: WATCH_PARENTS=0 turns it off" "$(tends)" "$before"
PARENTS_EVERY=600

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

# Trimming the build caches: at once, then again once TRIM_EVERY has passed;
# only what the trim says is logged.
TRIM_EVERY=1800 TRIM_OUT=
trim_caches() { echo trim >>"$T/trims"; [ -z "$TRIM_OUT" ] || echo "$TRIM_OUT"; }
trims() { cat "$T/trims" 2>/dev/null | wc -l | tr -d ' '; }
now=$(date +%s)
trim "$now"
check "trim: on the first loop" "$(trims)" 1
check "trim: under the limit, nothing logged" "$(logged 'trim-go-cache')" 0
TRIM_OUT='trim-go-cache: /c was 9 MB, limit 4 MB; dropped 6 MB of entries unused for 2+ hours, now 3 MB'
trim $((now + 60))
check "trim: not again before TRIM_EVERY" "$(trims)" 1
trim $((now + 1800))
check "trim: again after TRIM_EVERY" "$(trims)" 2
check "trim: logs what it dropped" "$(logged 'dropped 6 MB of entries unused for 2+ hours, now 3 MB')" 1

# Paused for a limit: nothing starts, not even a triage, and the pause is logged
# once; once it passes, the relay starts again.
echo "$(($(date +%s) + 3600)) You've hit your session limit" >"$DIR/paused-until"
n=$(triages)
PICKED= PICK_RC=1 PROPOSED='70 71 72'
aged
round
check "paused: no triage" "$(triages)" "$n"
PICKED=$'60\tTitle\turl\tready' PICK_RC=0 LAST_N=
round
check "paused: no relay" "$(starts)" 5
check "paused: logged once" "$(logged "the relay is paused until .* (You've hit your session limit); starting nothing")" 1
echo "$(($(date +%s) - 1)) You've hit your session limit" >"$DIR/paused-until"
round
check "pause over: starts the relay" "$(starts)" 6

# The maintainer: each pass, busy or idle, when on, not running, the last sweep
# WATCH_MAINTAIN old and the activity changed since; never while paused.
MAINTAIN_EVERY=7200 PICKED= PICK_RC=1 PROPOSED=
now=$(date +%s)
rm -f "$DIR/maintained"
round
check "maintain: no sweep yet: starts" "$(maintains)" 1
check "maintain: one line" "$(logged 'maintainer: no sweep recorded (t1 sha1); hack/relay.sh --maintain: maintainer started (pid 1)$')" 1
round
check "maintain: not twice" "$(maintains)" 1
# The sweep ends and records the activity it saw.
MAINT_T=0
echo "$((now - 7200)) t1 sha1" >"$DIR/maintained"
round
check "maintain: due, unchanged: no sweep" "$(maintains)" 1
# Quiet: the activity is asked at most every WATCH_RETRY.
ACT='t2 sha1'
round
check "maintain: quiet: not asked again before WATCH_RETRY" "$(maintains)" 1
MAINT_T=0
echo "$((now - 3600)) t1 sha1" >"$DIR/maintained"
round
check "maintain: changed, not due: no sweep" "$(maintains)" 1
echo "$((now - 7200)) t1 sha1" >"$DIR/maintained"
exec 5>"$DIR/maintain.lock"; flock 5
MAINT_T=0
round
check "maintain: already running: no sweep" "$(maintains)" 1
exec 5>&-
echo "$(($(date +%s) + 3600)) You've hit your session limit" >"$DIR/paused-until"
round
check "maintain: paused: no sweep" "$(maintains)" 1
rm -f "$DIR/paused-until"
MAINTAIN_EVERY=0 round
check "maintain: off: no sweep" "$(maintains)" 1
# Every worker busy: the maintainer starts all the same.
exec 5>"$DIR/lock-1" 6>"$DIR/lock-2"; flock 5; flock 6
MAINT_T=0
round
check "maintain: due and changed, workers busy: starts" "$(maintains)" 2
check "maintain: names the last sweep" "$(logged "maintainer: activity since the sweep of $(date -d "@$((now - 7200))" -Is) (t2 sha1)")" 1
check "maintain: workers busy: no relay" "$(starts)" 6
exec 5>&- 6>&-
# A start that fails: logged, and tried again only after WATCH_RETRY.
MAINT_T=0 MAINT_RC=1 ACT='t3 sha1'
round; round
check "maintain: failed start: once" "$(maintains)" 3
check "maintain: failed start: logged" "$(logged 'hack/relay.sh --maintain: maintainer started (pid 1) (failed, exit 1; again after 10m)')" 1
MAINT_T=$((MAINT_T - RETRY)) MAINT_RC=0
round
check "maintain: retried after WATCH_RETRY" "$(maintains)" 4
# GitHub can't say: logged once, no sweep.
MAINT_T=0 ACT_RC=1 ACT='boom'
round; round
check "maintain: no activity: no sweep" "$(maintains)" 4
check "maintain: no activity: logged once" "$(logged 'could not read the activity for the maintainer: boom')" 1
ACT_RC=0 ACT='t3 sha1'

# An issue escalated to the maintainer (status/needs-maintainer) after the
# last sweep started starts it within a pass, however recent that sweep; at most once
# per WATCH_RETRY, and not again for an issue that sweep already saw.
now=$(date +%s)
echo "$((now - 600)) t3 sha1" >"$DIR/maintained"
MAINT_T=0 ESC="247 $((now - 900))"
round
check "escalated before the last sweep started: no sweep" "$(maintains)" 4
ESC="246 $((now - 900))
247 $((now - 60))"
round
check "escalated after the sweep started: starts" "$(maintains)" 5
check "escalated: one line" "$(logged 'maintainer: escalated #247 (status/needs-maintainer); hack/relay.sh --maintain: maintainer started (pid 1)$')" 1
round
check "escalated: not again before WATCH_RETRY" "$(maintains)" 5
MAINT_T=$((MAINT_T - RETRY))
exec 5>"$DIR/maintain.lock"; flock 5
round
check "escalated: already running: no sweep" "$(maintains)" 5
exec 5>&-
# A sweep that started after the escalation ran to its end: no sweep.
echo "$now t3 sha1" >"$DIR/maintained"
round
check "escalated: the sweep saw it: no sweep" "$(maintains)" 5
# A sweep that didn't end left the time as it was: again after WATCH_RETRY.
echo "$((now - 600)) t3 sha1" >"$DIR/maintained"
MAINT_T=0
round
check "escalated: sweep didn't end: again" "$(maintains)" 6
echo "$(($(date +%s) + 3600)) You've hit your session limit" >"$DIR/paused-until"
MAINT_T=0
round
check "escalated: paused: no sweep" "$(maintains)" 6
rm -f "$DIR/paused-until"
MAINTAIN_EVERY=0 MAINT_T=0 round
check "escalated: off: no sweep" "$(maintains)" 6
MAINTAIN_EVERY=7200
# GitHub can't say: logged once, and the regular sweep still runs.
MAINT_T=0 ESC=boom ESC_RC=1 ACT='t4 sha1'
echo "$((now - 7200)) t3 sha1" >"$DIR/maintained"
round
echo "$((now - 7200)) t4 sha1" >"$DIR/maintained"
MAINT_T=0; round
check "escalated: can't list: logged once" "$(logged 'could not list the issues escalated to the maintainer: boom')" 1
check "escalated: can't list: the regular sweep runs" "$(maintains)" 7
ESC= ESC_RC=0
MAINTAIN_EVERY=0 ACT_RC=0

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
  "run list"*)
    if [ -n "$q" ]; then jq -r "$q" "$FAKE/runs-$wf.json" 2>/dev/null || echo '[]' | jq -r "$q"
    else cat "$FAKE/runs-$wf.json" 2>/dev/null || echo '[]'; fi ;;
  "run view"*)
    if [[ " $* " == *" --log-failed "* ]] && [ -f "$FAKE/log-$3.fail" ]; then exit 1
    elif [[ " $* " == *" --log-failed "* ]] && [ -f "$FAKE/log-$3.txt" ]; then echo "$3" >>"$FAKE/log-reads"; cat "$FAKE/log-$3.txt"
    elif [[ " $* " == *" --log-failed "* ]]; then seq -f 'log line %g' 1 50
    elif [ -f "$FAKE/jobs-$3.json" ]; then jq -r "$q" "$FAKE/jobs-$3.json"
    else echo '{"jobs":[{"databaseId":1,"name":"Run on Ubuntu","conclusion":"failure","steps":[{"name":"Test","conclusion":"failure"}]},
      {"databaseId":2,"name":"Setup","conclusion":"success","steps":[{"name":"Set up","conclusion":"success"}]}]}' | jq -r "$q"; fi ;;
  *"/check-runs/"*)
    for a in "$@"; do [[ $a == */check-runs/* ]] && id=${a##*/check-runs/} id=${id%%/*}; done
    jq -r "$q" "$FAKE/annotations-$id.json" 2>/dev/null || echo '[]' | jq -r "$q" ;;
  *" --method POST "*)
    [ -f "$FAKE/post.fail" ] && { echo "HTTP 502" >&2; exit 1; }
    n=$(($(jq length "$FAKE/issues.json") + 100))
    jq --argjson n "$n" '. + [input + {number: $n}]' "$FAKE/issues.json" - >"$FAKE/issues.new" &&
      mv "$FAKE/issues.new" "$FAKE/issues.json" && echo "$n" ;;
  *) # $FAKE/listed.json, when there, stands for a list of open issues that
    # lags behind the creates, or that a close took an issue out of.
    if [ -f "$FAKE/listed.json" ]; then jq -r "$q" "$FAKE/listed.json"; else jq -r "$q" "$FAKE/issues.json"; fi ;;
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

# A run whose jobs GitHub never started (failed payments): its failed jobs
# have no steps and an annotation saying so. The watch files one issue for the
# maintainer, shared by every such workflow, and no "main is red".
NOTSTARTED='The job was not started because recent account payments have failed or your spending limit needs to be increased. Please check the '"'"'Billing & plans'"'"' section of your settings'
unstarted() {
  echo '{"jobs":[{"databaseId":'"$2"',"name":"Run on Ubuntu","conclusion":"failure","steps":[]}]}' >"$T/jobs-$1.json"
  jq -n --arg m "$NOTSTARTED" '[{message: $m, annotation_level: "failure"}]' >"$T/annotations-$2.json"
}
echo '[]' >"$T/issues.json"
: >"$LOG"
CI_SAID=
runs test.yml failure failure
runs lint.yml failure failure
runs_test=$(jq -r '.[].databaseId' "$T/runs-test.yml.json")
for id in $runs_test; do unstarted "$id" "5$id"; done
ci_round
check "ci: unstarted jobs file one issue for both workflows" "$(filed)" 1
check "ci: its title" "$(jq -r '.[0].title' "$T/issues.json")" "GitHub Actions starts no jobs"
check "ci: its labels" "$(jq -r '.[0].labels | join(" ")' "$T/issues.json")" "status/needs-maintainer kind/bug source/dev"
body=$(jq -r '.[0].body' "$T/issues.json")
check "ci: the body carries the annotation" "$(grep -cxF "> $NOTSTARTED" <<<"$body")" 1
check "ci: the body names the run" "$(grep -c '^Run: \[899\](https://x/runs/899) ' <<<"$body")" 1
check "ci: no main is red" "$(jq '[.[] | select(.title | startswith("main is red"))] | length' "$T/issues.json")" 0
check "ci: logged once per workflow" "$(logged 'GitHub Actions started no job of Tests; issue #100$'):$(logged 'GitHub Actions started no job of Lint; issue #100$')" 1:1
ci_round
check "ci: a later round files no other issue" "$(filed)" 1
check "ci: unstarted jobs that last are logged once" "$(logged 'GitHub Actions started no job')" 2

# GitHub's open-issue list lags the create: the round still files one issue.
echo '[]' >"$T/issues.json"
echo '[]' >"$T/listed.json"
rm -f "$DIR/ci-stopped"
: >"$LOG"
CI_SAID=
ci_round
check "ci: a lagging open-issue list still files one issue" "$(filed)" 1
check "ci: both workflows name it" "$(logged 'GitHub Actions started no job of Tests; issue #100$'):$(logged 'GitHub Actions started no job of Lint; issue #100$')" 1:1
check "ci: its number is kept" "$(cat "$DIR/ci-stopped")" 100
# The maintainer closed it as a duplicate (not_planned): no open issue has
# its title, and while the outage lasts, nothing is filed again, even after
# the watch restarts (CI_SAID empty).
CI_SAID=
ci_round
check "ci: after a close as duplicate, the outage files nothing" "$(filed)" 1
# Every workflow ran its jobs again: the outage is over, and the next one
# files anew.
rm -f "$T"/jobs-*.json "$T"/annotations-*.json
runs test.yml failure success
runs lint.yml success
ci_round
check "ci: the end of the outage forgets the issue" "$([ -e "$DIR/ci-stopped" ] && echo kept || echo gone)" gone
for id in $runs_test; do unstarted "$id" "5$id"; done
runs test.yml failure failure
runs lint.yml failure failure
ci_round
check "ci: the next outage files anew" "$(filed)" 2
# An unreadable workflow doesn't end the outage.
mv "$T/bin/gh" "$T/bin/gh.ok"
printf '#!/usr/bin/env bash\nexit 1\n' >"$T/bin/gh"
chmod +x "$T/bin/gh"
ci_round
mv "$T/bin/gh.ok" "$T/bin/gh"
check "ci: an unreadable CI keeps the issue" "$(cat "$DIR/ci-stopped" 2>/dev/null)" 101

# The outage already has an open issue in status/needs-human: it is reused
# and its labels left alone.
rm -f "$DIR/ci-stopped" "$T/listed.json"
jq -n '[{number: 379, title: "GitHub Actions starts no jobs", labels: ["status/needs-human", "kind/bug", "source/dev"]}]' >"$T/issues.json"
ci_round
check "ci: a needs-human issue: nothing filed" "$(filed)" 1
check "ci: a needs-human issue keeps its labels" "$(jq -r '.[0].labels | join(" ")' "$T/issues.json")" "status/needs-human kind/bug source/dev"
check "ci: a needs-human issue is the one kept" "$(cat "$DIR/ci-stopped")" 379
rm -f "$DIR/ci-stopped"

# A failed job with no steps but no such annotation is code work, and so is a
# never-started run before the newest; the newest decides.
echo '[]' >"$T/issues.json"
rm -f "$T"/jobs-*.json "$T"/annotations-*.json
runs lint.yml failure failure
echo '{"jobs":[{"databaseId":7,"name":"Run on Ubuntu","conclusion":"failure","steps":[]}]}' >"$T/jobs-899.json"
echo '[{"message":"Process completed with exit code 1."}]' >"$T/annotations-7.json"
runs test.yml failure failure
ci_round
check "ci: failed jobs that started stay main is red" "$(jq -r '[.[].title] | sort | join(",")' "$T/issues.json")" "main is red: Lint,main is red: Tests"
echo '[]' >"$T/issues.json"
rm -f "$T"/jobs-*.json "$T"/annotations-*.json
unstarted 898 5898
ci_round
check "ci: an older never-started run doesn't count as a failure" "$(filed)" 0

# A run its self-hosted runner lost says nothing about the code: one stopped
# mid-step (its step cancelled), or one no runner took (its job cancelled,
# without steps), counts no more than a cancelled run.
rm -f "$T"/jobs-*.json "$T"/annotations-*.json
lost() {
  echo '{"jobs":[{"databaseId":1,"name":"Run on Ubuntu","conclusion":"failure","steps":[{"name":"Set up job","conclusion":"success"},
    {"name":"Running Tests","conclusion":"cancelled"},{"name":"Complete job","conclusion":"success"}]}]}' >"$T/jobs-$1.json"
}
unacquired() { echo '{"jobs":[{"databaseId":2,"name":"Run on Ubuntu","conclusion":"cancelled","steps":[]}]}' >"$T/jobs-$1.json"; }
runs lint.yml success
runs test.yml failure failure success
unacquired 899
lost 898
ci_round
check "ci: runs the runner lost don't count" "$(filed)" 0
runs test.yml failure failure failure failure
unacquired 899
lost 898
ci_round
check "ci: a red main skips the runs its runner lost" "$(filed)" 1
check "ci: it names the two runs that failed for the code" \
  "$(jq -r '.[0].body' "$T/issues.json" | grep -o '^- \[[0-9]*\]' | tr '\n' ' ')" "- [897] - [896] "
echo '{"jobs":[{"databaseId":3,"name":"Run on Ubuntu","conclusion":"timed_out","steps":[{"name":"Running Tests","conclusion":"cancelled"}]}]}' >"$T/jobs-899.json"
echo '[]' >"$T/issues.json"
rm -f "$T/jobs-898.json"
runs test.yml failure failure
ci_round
check "ci: a job that timed out counts" "$(filed)" 1
echo '[]' >"$T/issues.json"
rm -f "$T"/jobs-*.json

# Flaky specs, with the same fake gh: the failed runs on main in
# $T/runs-.json, each one's failed log in $T/log-RUN.txt, from a real Tests
# run's log (gh writes its colour codes as the text "^[").
SPEC="Brief apps copies a Crew's latest release to the Brief, and says an entry's app isn't made"
ago() { date -u -d "$1 ago" +%Y-%m-%dT%H:%M:%SZ; }
failed() { # failed RUN AGO: RUN failed on main AGO ago
  jq --argjson id "$1" --arg at "$(ago "$2")" '. + [{databaseId: $id, createdAt: $at}]' "$T/runs-.json" >"$T/runs.new" &&
    mv "$T/runs.new" "$T/runs-.json"
}
flaky_issues() { jq '[.[] | select(.title | startswith("Flaky: "))] | length' "$T/issues.json"; }
echo '[]' >"$T/issues.json"
echo '[]' >"$T/runs-.json"
: >"$LOG"
cp "$HERE/testdata/ginkgo-fail.log" "$T/log-801.txt"
failed 801 '3 days'
flake_round
check "flake: one failure is recorded" "$(cat "$DIR/flakes.tsv")" "$SPEC	801	$(jq -r '.[0].createdAt' "$T/runs-.json")"
check "flake: one failure files nothing" "$(filed)" 0
check "flake: logged" "$(logged "a spec failed on main: $SPEC$")" 1
flake_round
check "flake: a run's log is read once" "$(wc -l <"$T/log-reads")" 1
check "flake: a run is recorded once" "$(wc -l <"$DIR/flakes.tsv")" 1

# The same spec in a second run within seven days: one bug, not urgent.
sed 's/\^\[/\x1b/g' "$HERE/testdata/ginkgo-fail.log" >"$T/log-802.txt"
failed 802 '1 hour'
flake_round
check "flake: the escape byte is read like its text" "$(cut -f1-2 "$DIR/flakes.tsv" | tail -1)" "$SPEC	802"
check "flake: a second failure files one bug" "$(filed)" 1
check "flake: its title" "$(jq -r '.[0].title' "$T/issues.json")" "Flaky: $SPEC"
check "flake: its labels" "$(jq -r '.[0].labels | join(" ")' "$T/issues.json")" "status/ready kind/bug source/dev"
check "flake: the body names both runs" "$(jq -r '.[0].body' "$T/issues.json" | grep -c '^- https://github.com/.*/actions/runs/80[12] at ')" 2
check "flake: logged with the issue" "$(logged "a spec failed on main 2 times in 7 days: $SPEC; issue #100$")" 1

# A third failure while the bug is open files no other.
cp "$T/log-801.txt" "$T/log-803.txt"
failed 803 '10 minutes'
flake_round
check "flake: a third failure doesn't duplicate the open bug" "$(filed)" 1
check "flake: the third is recorded" "$(wc -l <"$DIR/flakes.tsv")" 3

# A failure older than the window doesn't count, and a log with no [FAIL]
# line, or one that can't be read yet, records nothing.
echo '[]' >"$T/issues.json"
printf 'Old spec\t700\t%s\n' "$(ago '9 days')" >"$DIR/flakes.tsv"
rm -f "$DIR/flakes-runs" "$T/log-reads"
echo '[]' >"$T/runs-.json"
printf '  ^[[38;5;9m[FAIL]^[[0m ^[[0mOld ^[[38;5;9m^[[1m[It] spec^[[0m\n' >"$T/log-804.txt"
failed 804 '1 hour'
seq -f 'log line %g' 1 5 >"$T/log-805.txt"
failed 805 '1 hour'
failed 806 '1 hour'
touch "$T/log-806.fail"
flake_round
check "flake: a failure past the window doesn't count" "$(filed)" 0
check "flake: no [FAIL] line, no record" "$(grep -c '	805	' "$DIR/flakes.tsv")" 0
check "flake: an unreadable log is read again next round" "$(grep -cx 806 "$DIR/flakes-runs")" 0
check "flake: the runs read are kept" "$(sort "$DIR/flakes-runs" | tr '\n' ' ')" "804 805 "
check "flake: --count lists this week's specs" "$("$HERE/lib/github.sh" flakes --count)" "1	Old spec"

# A spec with backslashes, in a log with CRLF line ends: one name in every
# run. Its bug can't be filed at first: the next round files it, though no
# run is new.
rm -f "$DIR/flakes.tsv" "$DIR/flakes-runs"
echo '[]' >"$T/runs-.json"
printf '  ^[[38;5;9m[FAIL]^[[0m ^[[0mParse ^[[38;5;9m^[[1m[It] matches \\d+ and a\\nb^[[0m\r\n' >"$T/log-811.txt"
tr -d '\r' <"$T/log-811.txt" >"$T/log-812.txt"
failed 811 '2 hours'
failed 812 '1 hour'
touch "$T/post.fail"
: >"$LOG"
flake_round
check "flake: a failed filing is logged" "$(logged 'Parse matches .*could not file its issue')" 1
rm "$T/post.fail"
flake_round
check "flake: CR and backslashes: one name" "$(cut -f1 "$DIR/flakes.tsv" | sort -u)" 'Parse matches \d+ and a\nb'
check "flake: the next round files it" "$(jq -r '.[].title' "$T/issues.json")" 'Flaky: Parse matches \d+ and a\nb'
flake_round
check "flake: filed once" "$(filed)" 1
rm -f "$T/runs-.json" "$T"/log-*.txt "$T"/log-*.fail "$DIR/flakes.tsv" "$DIR/flakes-runs" "$T/log-reads"
echo '[]' >"$T/issues.json"

# Runners offline: a run queued on main for 40 minutes puts the note in the
# Maintainer report (report_note faked; the runs are gh run list's output,
# read by the real hack/lib/github.sh queued), 10 minutes says nothing, and
# runs that start again take it out. A failure is logged once.
NOTE_RC=0
report_note() { printf '%s\n' "$*" >>"$T/notes"; return "$NOTE_RC"; }
notes() { cat "$T/notes" 2>/dev/null; }
# queued AGE...: the runs queued on main, created AGE ago each, beside one in progress.
queued() {
  local a i=0
  for a in "$@"; do
    i=$((i + 1))
    jq -n --argjson id "$((700 + i))" --arg at "$(date -u -d "$a ago" +%Y-%m-%dT%H:%M:%SZ)" \
      '{databaseId: $id, status: "queued", createdAt: $at}'
  done | jq -s '. + [{databaseId: 1, status: "in_progress", createdAt: "2026-10-01T00:00:00Z"}]' >"$T/runs-.json"
}
: >"$LOG"
rm -f "$T/notes"
queued '10 minutes'
runner_round
check "runners: queued 10 minutes says nothing" "$(notes)|$(cat "$LOG")|$(ls "$DIR/runners-offline" 2>/dev/null)" "||"
queued '40 minutes' '5 minutes'
since=$(TZ=UTC date -d "40 minutes ago" '+%Y-%m-%d %H:%M UTC')
TZ=UTC runner_round
check "runners: queued 40 minutes notes it in Health and Needs you" "$(notes)" \
  "runners Runners offline since $since: 2 runs queued Runners offline since $since: 2 runs queued. Start Docker Desktop (WSL integration on for this distro) on the runners' device, then check \`make gh-runner-status\` (DEVELOPMENT.md)."
check "runners: logged" "$(logged "Runners offline since $since: 2 runs queued; said in the Maintainer report$")" 1
queued '41 minutes' '6 minutes' '1 minute'
TZ=UTC runner_round
check "runners: the note follows the count, logged once" "$(notes | tail -1 | sed "s/ queued .*/ queued/")|$(logged 'said in the Maintainer report')" \
  "runners Runners offline since $(TZ=UTC date -d "41 minutes ago" '+%Y-%m-%d %H:%M UTC'): 3 runs queued|1"
rm -f "$T/notes"
queued
runner_round
check "runners: running again clears the note" "$(notes)|$(ls "$DIR/runners-offline" 2>/dev/null)|$(logged 'runners back')" "runners||1"
runner_round
check "runners: online, the report is left alone" "$(notes)" "runners"
queued '1 hour'
NOTE_RC=1
runner_round
runner_round
check "runners: a report that can't be edited is logged once" "$(logged 'could not say it in the Maintainer report')|$(ls "$DIR/runners-offline" 2>/dev/null)" "1|"
NOTE_RC=0
runner_round
check "runners: and noted once it can" "$(logged 'said in the Maintainer report')|$(cat "$DIR/runners-offline")" \
  "2|Runners offline since $(date -d "1 hour ago" '+%Y-%m-%d %H:%M %Z'): 1 run queued"
rm -f "$T/runs-.json" "$T/notes" "$DIR/runners-offline"

# The groups: a watch, or a user manager, without a group the user has in
# /etc/group says so, with the fix; one with all of them says nothing.
OWN="1000 4 27"
id() {
  case "$*" in
    -un) echo math ;;
    -u) echo 1000 ;;
    "-G math") echo "1000 4 27 1001" ;;
    "-Gn math") echo "math adm sudo docker" ;;
    -G) echo "$OWN" ;;
    *) command id "$@" ;;
  esac
}
check "groups: missing" "$(missing_groups "$OWN")" docker
check "groups: none missing" "$(missing_groups "4 1000 1001 27")" ""
check "groups: two missing" "$(missing_groups "1000")" "adm sudo docker"
: >"$LOG"
own_groups
check "groups: the watch logs the missing group" \
  "$(logged 'this watch lacks the group(s) docker that math has in /etc/group: .*Restart the user manager: log out and in again, or on WSL run wsl --shutdown from Windows. Check: grep Groups /proc/\$(pgrep -u "\$USER" -x systemd)/status$')" 1
: >"$LOG"
OWN="1000 4 27 1001" own_groups
check "groups: all there, nothing logged" "$(cat "$LOG")" ""
MANAGER="1000 4 27"
manager_groups() { echo "$MANAGER"; }

# --install with systemd: the unit for this checkout and WORKERS, enabled and
# (re)started, linger on, and the stop file gone.
mkdir -p "$T/bin" "$T/home"
cat >"$T/bin/systemctl" <<'SH'
#!/usr/bin/env bash
echo "systemctl $*" >>"$FAKE/calls"
[ -e "$FAKE/no-systemd" ] && exit 1
exit 0
SH
cat >"$T/bin/loginctl" <<'SH'
#!/usr/bin/env bash
echo "loginctl $*" >>"$FAKE/calls"
SH
cat >"$T/bin/crontab" <<'SH'
#!/usr/bin/env bash
echo "crontab $*" >>"$FAKE/calls"
case $1 in
  -l) [ -e "$FAKE/crontab" ] || { echo "no crontab for you" >&2; exit 1; }; cat "$FAKE/crontab"
      # A slow producer: still writing after the reader has what it needs.
      [ -e "$FAKE/slow-crontab" ] && { sleep 0.5; echo; } ;;
  -) t=$(cat); printf "%s\n" "$t" >"$FAKE/crontab" ;;
esac
SH
chmod +x "$T/bin/"*
SAVED_PATH=$PATH SAVED_HOME=$HOME
export FAKE=$T PATH="$T/bin:$PATH" HOME=$T/home
STOP=$DIR/stop REPO=$T/repo WORKERS=3
mkdir -p "$REPO/.relay"
touch "$STOP"
RELAY_MAX_WORKERS=5 install_watch >/dev/null 2>"$T/install.err"
unit=$T/home/.config/systemd/user/c4factory-relay-watch.service
check "install: warns of the manager's missing group" \
  "$(grep -c '^relay-watch: the systemd user manager lacks the group(s) docker that math has in /etc/group: .*wsl --shutdown' "$T/install.err")" 1
check "install: writes the unit" "$([ -s "$unit" ] && echo yes)" yes
check "install: runs this checkout's watch in the foreground" \
  "$(grep -c "^ExecStart=\"$REPO/dev\" run \"$REPO/hack/relay-watch.sh\" --foreground --workers 3$" "$unit")" 1
check "install: keeps the installer's PATH" "$(grep -cF "Environment=\"PATH=$PATH\"" "$unit")" 1
check "install: restarts, but not after the stop file" \
  "$(grep -E '^(Restart|RestartPreventExitStatus|KillMode)=' "$unit" | tr '\n' ' ')" \
  "Restart=always RestartPreventExitStatus=0 KillMode=process "
check "install: passes the relay settings" "$(grep -c '^Environment="RELAY_MAX_WORKERS=5"$' "$unit")" 1
check "install: no placeholder left" "$(grep -c @ "$unit")" 0
check "install: enables and starts it" \
  "$(grep -E 'enable|restart|daemon-reload' "$T/calls" | tr '\n' ';')" \
  "systemctl --user daemon-reload;systemctl --user enable c4factory-relay-watch.service;systemctl --user restart c4factory-relay-watch.service;loginctl enable-linger $(id -un);"
check "install: removes the stop file" "$([ -e "$STOP" ] && echo there)" ""
check "install: no crontab" "$([ -e "$T/crontab" ] && echo there)" ""

# --uninstall without the user bus: the unit stays, and it says so.
touch "$T/no-systemd"
uninstall_watch 2>/dev/null
check "uninstall without systemd: fails" $? 1
check "uninstall without systemd: unit stays" "$([ -e "$unit" ] && echo there)" there
rm "$T/no-systemd"

# --uninstall: disabled, stopped and gone.
: >"$T/calls"
uninstall_watch >/dev/null
check "uninstall: removes the unit" "$([ -e "$unit" ] && echo there)" ""
check "uninstall: disables and stops it" "$(grep -c 'disable --now c4factory-relay-watch.service' "$T/calls")" 1

# --install without systemd: two crontab lines that start the watch with --cron,
# the user's other lines kept; installing again doesn't add more.
touch "$T/no-systemd"
echo '0 3 * * * backup' >"$T/crontab"
mkdir -p "$REPO/hack"
printf '#!/usr/bin/env bash\nshift; echo "$@" >>"%s/started"\n' "$T" >"$REPO/dev"
chmod +x "$REPO/dev"
RELAY_MAX_WORKERS=5 install_watch >/dev/null
RELAY_MAX_WORKERS=5 install_watch >/dev/null
check "cron: no unit" "$([ -e "$unit" ] && echo there)" ""
check "cron: keeps the other lines" "$(grep -c backup "$T/crontab")" 1
check "cron: at boot" "$(grep -c "^@reboot cd $REPO && PATH=.* ./dev run hack/relay-watch.sh --cron --workers 3 .*# c4factory relay watch: $REPO$" "$T/crontab")" 1
check "cron: passes the relay settings" "$(grep -c ' RELAY_MAX_WORKERS=5 .*./dev run' "$T/crontab")" 2
check "cron: every 5 minutes" "$(grep -c "^\*/5 \* \* \* \* cd $REPO && .* --cron --workers 3 " "$T/crontab")" 1
check "cron: starts the watch now" "$(cat "$T/started")" $'hack/relay-watch.sh --cron --workers 3\nhack/relay-watch.sh --cron --workers 3'
# A crontab -l still writing when the tag is found doesn't fail the uninstall.
touch "$T/slow-crontab"
uninstall_watch >/dev/null
rm "$T/slow-crontab"
check "cron uninstall: only the other lines left" "$(cat "$T/crontab")" "0 3 * * * backup"

# --install from a worktree: refused, nothing written.
git init -q "$T/main" && git -C "$T/main" -c user.name=t -c user.email=t@t commit -q --allow-empty -m init &&
  git -C "$T/main" worktree add -q "$T/wt" 2>/dev/null
rm -f "$T/no-systemd"
REPO=$T/wt install_watch 2>/dev/null
check "worktree: refused" $? 1
check "worktree: no unit" "$([ -e "$unit" ] && echo there)" ""
export PATH=$SAVED_PATH HOME=$SAVED_HOME

# The watch itself, in a scratch copy: a --cron start with the stop file starts
# nothing; a --foreground watch waits while another holds the lock, takes over
# when it ends, and exits 0 for the stop file, which the unit doesn't restart.
# Started from a Claude Code session in VS Code, what it runs (here a faked
# hack/pull.sh) doesn't see that session's variables (#645).
S=$T/scratch
mkdir -p "$S/hack/lib" "$S/.relay"
cp "$(dirname "$0")/relay-watch.sh" "$S/hack/"
cp "$(dirname "$0")/lib/session-env.sh" "$S/hack/lib/"
printf '#!/usr/bin/env bash\nenv >"%s.tmp" && mv "%s.tmp" "%s"\n' "$T/pull-env" "$T/pull-env" "$T/pull-env" >"$S/hack/pull.sh"
chmod +x "$S/hack/pull.sh"
touch "$S/.relay/stop"
bash "$S/hack/relay-watch.sh" --cron
check "cron start with the stop file: nothing" "$([ -e "$S/.relay/watch.log" ] && echo started)" ""
rm "$S/.relay/stop"
# The other watch holds the lock until the test has seen this one wait (at
# most 30 seconds), so a loaded device can't start this one after it ended.
flock "$S/.relay/watch.lock" bash -c "for _ in \$(seq 300); do [ -e '$S/release' ] && break; sleep 0.1; done" &
holder=$!
until ! flock -n "$S/.relay/watch.lock" true; do sleep 0.1; done
CLAUDECODE=1 CLAUDE_CODE_SESSION_ID=parent CLAUDE_CODE_ENTRYPOINT=claude-vscode CLAUDE_EFFORT=xhigh \
  VSCODE_PID=1 GIT_ASKPASS=/vscode/askpass.sh CLAUDE_CODE_OAUTH_TOKEN=kept \
  WATCH_EVERY=1 WATCH_PULL_EVERY=999 bash "$S/hack/relay-watch.sh" --foreground 2>/dev/null &
fg=$!
for _ in $(seq 100); do grep -qs 'another watch holds the lock' "$S/.relay/watch.log" && break; sleep 0.1; done
check "foreground: waits for the lock" "$(grep -cs 'another watch holds the lock; pid .* waits' "$S/.relay/watch.log")" 1
check "foreground: hasn't started" "$(grep -cs 'watch started' "$S/.relay/watch.log")" 0
touch "$S/release"
wait "$holder"
for _ in $(seq 100); do grep -qs 'watch started' "$S/.relay/watch.log" && break; sleep 0.1; done
check "foreground: takes over" "$(grep -c 'watch started: pid .*, under systemd' "$S/.relay/watch.log")" 1
# The stop file waits for the faked pull: the watch checks it before its first
# pull, so a loaded device could otherwise stop the watch before it pulls (#821).
# The fake renames its output into place, so pull-env is whole once it exists.
for _ in $(seq 300); do [ -e "$T/pull-env" ] && break; sleep 0.1; done
touch "$S/.relay/stop"
wait "$fg"
check "foreground: exits 0 for the stop file" $? 0
check "foreground: says why" "$(grep -c 'stop file found; stopping' "$S/.relay/watch.log")" 1
check "foreground: no Claude Code or VS Code session variables" \
  "$(grep -E '^(CLAUDECODE|CLAUDE_CODE_SESSION_ID|CLAUDE_CODE_ENTRYPOINT|CLAUDE_EFFORT|VSCODE_PID|GIT_ASKPASS)=' "$T/pull-env")" ""
check "foreground: keeps the Claude settings" "$(grep -c '^CLAUDE_CODE_OAUTH_TOKEN=kept$' "$T/pull-env")" 1

# Arguments.
bash "$(dirname "$0")/relay-watch.sh" --workers 0 2>/dev/null
check "--workers 0 refused" $? 64
check "--workers 12" "$(. "$(dirname "$0")/relay-watch.sh" --workers 12; echo "$WORKERS")" 12
bash "$(dirname "$0")/relay-watch.sh" --workers 100 2>/dev/null
check "--workers 100 refused" $? 64
bash "$(dirname "$0")/relay-watch.sh" --bogus 2>/dev/null
check "unknown flag refused" $? 64
check "--install --workers 4" "$(. "$(dirname "$0")/relay-watch.sh" --install --workers 4; echo "$MODE $WORKERS")" "install 4"

exit $fail
