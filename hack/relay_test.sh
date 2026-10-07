#!/usr/bin/env bash
# Tests for the relay's memory guard in hack/relay.sh: the worker cap on the
# device with the Kind cluster, and the wait for headroom before a pick, with a
# faked /proc/meminfo; the flush of a kept handoff, which releases the
# session's claim so the relay has none left to release; the cluster
# lease a session leaves behind, which the relay drops; the prompts, which
# never send a session to a cluster; the triage of the
# proposed issues and the maintainer's prompt, as dry runs; and, with claude faked, the pause a usage
# limit sets, the issue that goes to the user after failed sessions and the
# session the relay ends when its transcript stops growing; and idle workers,
# which stop without taking a session-start slot; the maintainer, which goes
# first in admit; and a maintainer sweep, which records the activity it saw.
# Run: hack/relay_test.sh
T=$(mktemp -d)
trap 'kill $(jobs -p) 2>/dev/null; rm -rf "$T"' EXIT
unset C4_CLUSTER RELAY_MAX_WORKERS RELAY_MIN_MEM_GIB RELAY_ADMIT_GAP RELAY_DRY_RUN
fail=0
check() { if [ "$2" = "$3" ]; then echo "ok   $1"; else echo "FAIL $1: got [$2] want [$3]"; fail=1; fi; }
# The defaults, from today's measurements (docs/dev/testenv.md): nine workers,
# 2 GiB free and 120s between session starts.
check "defaults" "$(. "$(dirname "$0")/relay.sh"; echo "$MAX_WORKERS $MIN_MEM_GIB $ADMIT_GAP")" "9 2 120"

export RELAY_MEMINFO=$T/meminfo RELAY_MIN_MEM_GIB=4 RELAY_ADMIT_GAP=300 RELAY_MEM_POLL=1 RELAY_MEM_WAIT=30
# shellcheck source=hack/relay.sh
. "$(dirname "$0")/relay.sh"
DIR=$T/relay STOP=$DIR/stop LOG=$T/log
mkdir -p "$DIR"
meminfo() { printf 'MemTotal:       15400000 kB\nMemAvailable:   %s kB\n' $(($1 * 1048576)) >"$RELAY_MEMINFO"; }

# The cap, on the device with the cluster.
cap 9 2>/dev/null; check "cap: nine workers by default" $? 0
MAX_WORKERS=1
cap 1 2>/dev/null; check "cap: one worker" $? 0
cap 2 2>"$T/err"; check "cap: two workers" $? 1
grep -q RELAY_MAX_WORKERS "$T/err"; check "cap: names RELAY_MAX_WORKERS" $? 0
MAX_WORKERS=2 cap 2 2>/dev/null; check "cap: raised on purpose" $? 0
C4_CLUSTER=none cap 9 2>/dev/null; check "cap: no cluster" $? 0
flock "$DIR/lock-3" sleep 30 &
until ! flock -n "$DIR/lock-3" true; do sleep 0.1; done
check "running workers" "$(running_workers)" 1
cap 1 2>/dev/null; check "cap: worker 3 already runs" $? 1
flock "$DIR/lock-12" sleep 30 &
until ! flock -n "$DIR/lock-12" true; do sleep 0.1; done
check "running workers: worker 12 counts" "$(running_workers)" 2
kill %1 %2; wait 2>/dev/null

# Arguments: one or two digits.
check "--workers 12" "$(. "$(dirname "$0")/relay.sh" --workers 12; echo "$WORKERS")" 12
(. "$(dirname "$0")/relay.sh" --workers 100) 2>/dev/null; check "--workers 100 refused" $? 64
(. "$(dirname "$0")/relay.sh" --workers 0) 2>/dev/null; check "--workers 0 refused" $? 64

# The wait for headroom.
meminfo 8
admit; check "admit: headroom" $? 0
check "admit: marks the start" "$(test -e "$DIR/admitted" && echo yes)" yes
rm -f "$DIR/admitted"
meminfo 2
(sleep 2; meminfo 6) &
t0=$(date +%s)
admit; check "admit: waits until memory frees" $? 0
check "admit: waited" "$(($(date +%s) - t0 >= 2))" 1
grep -q "waiting for headroom: 2 GiB available" "$LOG"; check "admit: logs the wait" $? 0
grep -q "headroom: 6 GiB available" "$LOG"; check "admit: logs the end" $? 0
MEM_WAIT=1 admit; check "admit: sessions start apart" $? 1
grep -q "sessions start at least 300s apart" "$LOG"; check "admit: logs the gap" $? 0
rm -f "$DIR/admitted"
meminfo 1
MEM_WAIT=1 admit; check "admit: gives up" $? 1
touch "$STOP"
admit; check "admit: stop file" $? 1

# The maintainer goes first (#262): while it waits in admit, no worker takes
# the start slot; a crashed maintainer's file holds no one.
rm -f "$STOP" "$DIR/admitted"
meminfo 8
: >"$LOG"
sleep 30 &
echo $! >"$DIR/admit-first"
MEM_WAIT=2 admit; check "admit first: a worker waits for the maintainer" $? 1
check "admit first: no slot taken" "$(test -e "$DIR/admitted" && echo taken)" ""
grep -q "the maintainer waits for a start, and it goes first" "$LOG"; check "admit first: logs the wait" $? 0
kill $!; wait $! 2>/dev/null
admit; check "admit first: a dead maintainer holds no one" $? 0
# A session just started; the maintainer waits, then a worker: the maintainer
# takes the next slot, the worker the one after, and the file goes.
: >"$T/order"
(ADMIT_GAP=2 admit first; echo "maintainer $?" >>"$T/order") &
until [ -s "$DIR/admit-first" ]; do sleep 0.1; done
(ADMIT_GAP=2 admit; echo "worker $?" >>"$T/order") &
wait
check "admit first: the maintainer starts first" "$(paste -sd, "$T/order")" "maintainer 0,worker 0"
check "admit first: its file goes" "$(test -e "$DIR/admit-first" && echo left)" ""

# The flush: a session's kept handoff is posted and its claim released as
# handed off, so the relay finds no claim left and logs no failed release.
GH_PENDING_DIR=$T/wt GH_CLAIM_FILE=$T/wt/claim
mkdir -p "$GH_PENDING_DIR"
echo '{"issue":76,"id":1,"name":"otter-76","branch":"b"}' >"$GH_CLAIM_FILE"
echo handoff >"$GH_PENDING_DIR/pending-handoff-76.md"
gh_comment() { echo "https://github.com/x/issues/$1#issuecomment-5"; }
gh_release() { rm -f "$GH_CLAIM_FILE"; echo "released #76 (otter-76): $1"; }
flush; check "flush: posts" $? 0
grep -q "posted the pending handoff on #76: https://github.com/x/issues/76#issuecomment-5; released #76: handed off$" "$LOG"
check "flush: logs the URL and the release" $? 0
gh_claim_read; check "flush: no claim left to release" $? 1
grep -q "could not release" "$LOG"; check "flush: no failed release logged" $? 1

# The cluster lease a session left behind is dropped and logged; another
# checkout's is left alone.
export C4_CLUSTER_LEASE=$T/lease
mkdir -p "$T/other"
lease() { jq -n --arg wt "$1" --argjson b "$(date +%s)" '{name:"otter-76",issue:76,worktree:$wt,beat:$b}' >"$C4_CLUSTER_LEASE"; }
lease "$T/wt/.."
drop_lease "that session 1 left behind"
check "lease: dropped" "$([ -e "$C4_CLUSTER_LEASE" ] && echo kept || echo gone)" gone
grep -q "released Kind cluster c4factory (otter-76, #76) that session 1 left behind$" "$LOG"; check "lease: logged" $? 0
lease "$T/other"
: >"$LOG"
drop_lease "that session 1 left behind"
check "lease: another checkout's kept" "$(jq -r .worktree "$C4_CLUSTER_LEASE")" "$T/other"
check "lease: nothing logged" "$(cat "$LOG")" ""

# No prompt tells a session to lease, create or deploy to a cluster (#353):
# the worker's, the triage's and the maintainer's, on a device with or without one.
for c in unset none; do
  out=$(if [ "$c" = unset ]; then unset C4_CLUSTER; else C4_CLUSTER=$c; fi
    prompt 1 t u ready n b; prompt 1 t u in-progress n b)
  check "prompt, C4_CLUSTER $c: no cluster steps" "$(grep -cE 'cluster acquire|cluster release|make deploy|--fresh|needs/cluster' <<<"$out")" 0
  check "prompt, C4_CLUSTER $c: never a cluster" "$(grep -c 'Never lease, create or deploy to a Kind cluster' <<<"$out")" 2
done

# A session escalates to the maintainer, never to the user.
out=$(prompt 1 t u ready n b)
check "prompt: escalates with needs-maintainer" "$(grep -c 'hack/lib/github.sh status 1 needs-maintainer' <<<"$out")" 1
check "prompt: never sets needs-human" "$(grep -c 'status 1 needs-human' <<<"$out") $(grep -c 'Never set status/needs-human' <<<"$out")" "0 1"

# outcome: needs-maintainer and needs-human end a session, as closed does.
(
  handoff_id() { echo 7; }
  gh_api() { echo "$STATE"; }
  STATE='open status/needs-maintainer,kind/bug'; check "outcome: needs-maintainer" "$(outcome 1 7)" needs-maintainer
  STATE='open status/needs-human'; check "outcome: needs-human" "$(outcome 1 7)" needs-human
  STATE='closed status/in-progress'; check "outcome: closed" "$(outcome 1 7)" closed
  STATE='open status/in-progress'; check "outcome: nothing" "$(outcome 1 7)" nothing
  STATE='open status/in-progress'; check "outcome: handed off" "$(outcome 1 6)" handed-off
  exit $fail
) || fail=1

# Triage, as a dry run: the proposed issues go into its prompt; none, no session.
rm -f "$STOP" "$DIR/admitted"
meminfo 8
gh_proposed() { printf '40\n41\n'; }
out=$(DRY_RUN=1 triage)
check "triage: names the issues" "$(grep -c 'status/proposed: #40 #41\.' <<<"$out")" 1
check "triage: readies by status" "$(grep -c 'hack/lib/github.sh status N ready' <<<"$out")" 1
check "triage: needs-human stays the user's" "$(grep -c "needs-human stays the user's" <<<"$out")" 1
check "triage: writes a missing Depends on itself" "$(grep -c 'a "Depends on #M" line it lacks' <<<"$out")" 1
check "triage: escalates a user's decision to the maintainer" "$(grep -c 'hack/lib/github.sh status N needs-maintainer' <<<"$out")" 1
check "triage: never sets needs-human" "$(grep -c 'status N needs-human' <<<"$out") $(grep -c 'never set status/needs-human' <<<"$out")" "0 1"
check "triage: needs-maintainer is the maintainer's" "$(grep -c "needs-maintainer is the maintainer's" <<<"$out")" 1
check "triage: readies an unsettled design as design work" "$(grep -c '^- Ready it as design work' <<<"$out")" 1
check "triage: never asks how to build it" "$(grep -c 'Never ask how to' <<<"$out")" 1
check "triage: a direction comes with the choice it would make" "$(grep -c 'says the choice you would make and why' <<<"$out")" 1
check "triage: no cluster steps" "$(grep -cE 'cluster acquire|make deploy|needs/cluster' <<<"$out")" 0
gh_proposed() { :; }
rm -f "$DIR/admitted"
check "triage: none proposed" "$(DRY_RUN=1 triage)" "dry run: no proposed issues; no triage"
# A triage whose session never ran keeps only the time, for the watch to try again.
echo "1 40 41" >"$DIR/triaged"
DRY_RUN= untriaged
check "triage: never ran" "$(awk '{print NF}' "$DIR/triaged")" 1

# The maintainer, as a dry run: its prompt names the charter, the skill and the
# report, says it commits nothing, and gives the last sweep's time.
rm -f "$DIR/admitted" "$DIR/maintained"
out=$(DRY_RUN=1 maintain)
check "maintain: no cluster steps" "$(grep -cE 'cluster acquire|make deploy|needs/cluster' <<<"$out")" 0
check "maintain: follows GOVERNANCE.md" "$(grep -c 'follow GOVERNANCE.md and the maintainer skill' <<<"$out")" 1
check "maintain: sets the report" "$(grep -c 'hack/lib/github.sh report FILE' <<<"$out")" 1
check "maintain: commits nothing" "$(grep -c 'commit nothing' <<<"$out")" 1
check "maintain: the owner's word wins" "$(grep -c "The owner's word wins" <<<"$out")" 1
check "maintain: first sweep" "$(grep -c 'No sweep ran before this one' <<<"$out")" 1
check "maintain: decides the escalations first" "$(grep -c 'hack/lib/github.sh escalated' <<<"$out")" 1
check "maintain: only it sets needs-human" "$(grep -c 'Only you set status/needs-human' <<<"$out")" 1
echo "86400 x y" >"$DIR/maintained"
rm -f "$DIR/admitted"
check "maintain: the last sweep's time" "$(DRY_RUN=1 maintain | grep -c "The last sweep was at $(date -d @86400 -Is);")" 1
check "maintain: a dry run records nothing" "$(cat "$DIR/maintained")" "86400 x y"

# The sessions, with claude faked (FAKE: limit, fail, handoff or commit) and
# GitHub faked: one ready issue, #175 on branch b.
WT=$T/wt
git init -q -b b "$WT"
git -C "$WT" -c user.name=t -c user.email=t@t commit -q --allow-empty -m first
mkdir -p "$T/bin"
cat >"$T/bin/claude" <<'FAKE'
#!/usr/bin/env bash
while [ $# -gt 0 ]; do [ "$1" = --session-id ] && id=$2; shift; done
t=$CLAUDE_CONFIG_DIR/projects/${PWD//[^a-zA-Z0-9]/-}/$id.jsonl
case $(cat "$FAKE") in
  leftover) sleep 600 >/dev/null 2>&1 & echo $! >"$FAKE.child"; echo '{"is_error":false,"result":"left","num_turns":1}' ;;
  hang) mkdir -p "${t%/*}"; echo '{}' >"$t"; sleep 60 & echo $! >"$FAKE.child"; wait ;;
  busy) mkdir -p "${t%/*}"; for _ in 1 2 3 4 5 6; do echo '{}' >>"$t"; sleep 1; done
    echo '{"is_error":false,"result":"busy","num_turns":9}' ;;
  agent) mkdir -p "${t%.jsonl}/subagents"; echo '{}' >"$t"
    for _ in 1 2 3 4 5 6; do echo '{}' >>"${t%.jsonl}/subagents/agent-1.jsonl"; sleep 1; done
    echo '{"is_error":false,"result":"agent","num_turns":9}' ;;
  quiet) sleep 4; echo '{"is_error":false,"result":"quiet","num_turns":1}' ;;
  limit) echo '{"is_error":true,"result":"You'"'"'ve hit your session limit · resets 3am (UTC)","num_turns":1}'; exit 1 ;;
  login) echo 'Not logged in · Please run /login' >&2; exit 1 ;;
  crash) echo "crashed" >&2; exit 1 ;;
  fail) echo "boom $RANDOM" >&2; echo '{"is_error":false,"result":"gave up","num_turns":3}' ;;
  handoff) echo $((RANDOM + 100)) >"$HANDOFF"; echo '{"is_error":false,"result":"handed off","num_turns":9}' ;;
  commit) git -c user.name=t -c user.email=t@t commit -q --allow-empty -m more; echo '{"is_error":false,"result":"committed","num_turns":9}' ;;
esac
FAKE
chmod +x "$T/bin/claude"
export PATH=$T/bin:$PATH FAKE=$T/fake HANDOFF=$T/handoff CLAUDE_CONFIG_DIR=$T/claude
echo 1 >"$HANDOFF"
update() { :; }
worktree() { :; }
drop_lease() { :; }
gh_claim_read() { return 1; }
pick() { ISSUE=175 TITLE=t URL=u HOW=ready; }
gh_take() { echo take >>"$T/takes"; printf 'otter-175\tb\n'; }
handoff_id() { cat "$HANDOFF"; }
outcome() { if [ "$(handoff_id)" != "$2" ]; then echo handed-off; else echo nothing; fi; }
gh_comment() { cat >"$T/comment"; echo "https://github.com/x/issues/$1#issuecomment-9"; }
gh_set_status() { echo "$1 $2" >"$T/status"; }
takes() { cat "$T/takes" 2>/dev/null | wc -l | tr -d ' '; }
rm -f "$STOP" "$DIR/admitted"
: >"$LOG"
ADMIT_GAP=0 MAX_SESSIONS=1 MEM_POLL=1

# A usage limit pauses the device until the time it names, and isn't counted.
echo limit >"$FAKE"
work
want=$(TZ=UTC date -d 3am +%s)
[ "$want" -le "$(date +%s)" ] && want=$((want + 86400))
check "limit: paused until the reset" "$(cut -d' ' -f1 "$DIR/paused-until")" "$want"
grep -q "paused the relay until .*: You've hit your session limit" "$LOG"; check "limit: logged" $? 0
check "limit: not counted" "$(test -e "$DIR/fails-175" && echo counted)" ""
check "limit: one claim" "$(takes)" 1
# While paused, every worker waits in admit and claims nothing; then goes on.
echo "$(($(date +%s) + 3)) limit" >"$DIR/paused-until"
echo fail >"$FAKE"
work &
sleep 2
check "paused: nothing claimed" "$(takes)" 1
check "paused: logged once" "$(grep -c 'paused until .*(limit); picking nothing' "$LOG")" 1
wait
check "pause over: claims" "$(takes)" 2
grep -q "the pause is over" "$LOG"; check "pause over: logged" $? 0
rm -f "$DIR/fails-175"
# A failed login, with no result, pauses for RELAY_LIMIT_WAIT.
echo login >"$FAKE"
LIMIT_WAIT=10m work
check "login: paused for RELAY_LIMIT_WAIT" "$(($(cut -d' ' -f1 "$DIR/paused-until") - $(date +%s) > 590))" 1
check "login: not counted" "$(test -e "$DIR/fails-175" && echo counted)" ""
rm -f "$DIR/paused-until"

# Failed sessions in a row: a handoff or a commit resets the count; the third
# escalates the issue to the maintainer, with a comment naming each session.
echo fail >"$FAKE"
work; work
check "fails: two counted" "$(wc -l <"$DIR/fails-175")" 2
check "fails: not yet the maintainer's" "$(cat "$T/status" 2>/dev/null)" ""
echo handoff >"$FAKE"
work
check "fails: a handoff resets" "$(test -e "$DIR/fails-175" && echo kept)" ""
echo fail >"$FAKE"
work
echo commit >"$FAKE"
work
check "fails: a commit resets" "$(test -e "$DIR/fails-175" && echo kept)" ""
: >"$LOG"
echo fail >"$FAKE"
work; work
check "fails: two, still not the maintainer's" "$(cat "$T/status" 2>/dev/null)" ""
work
check "fails: the third sets needs-maintainer" "$(cat "$T/status")" "175 needs-maintainer"
check "fails: count cleared" "$(test -e "$DIR/fails-175" && echo kept)" ""
for id in $(sed -n 's/.*session 1 start: id \([^,]*\),.*/\1/p' "$LOG"); do
  grep -q "^### Session \`$id\`: exit 0 after 0m$" "$T/comment"; check "fails: comment names $id" $? 0
done
check "fails: comment has each stderr" "$(grep -c '^boom ' "$T/comment")" 3
check "fails: comment has each account" "$(grep -c '^gave up$' "$T/comment")" 3
grep -q "#175 failed 3 sessions in a row; set it to status/needs-maintainer: https://github.com/x/issues/175#issuecomment-9" "$LOG"
check "fails: logged" $? 0

# A session whose transcript stops growing is ended and counted as failed; one
# that keeps writing, or writes none, runs on.
IDLE=2s IDLE_KILL=2s IDLE_POLL=1
: >"$LOG"
rm -f "$DIR/fails-175" "$T/status"
echo hang >"$FAKE"
t0=$(date +%s)
work
check "idle: ended early" "$(($(date +%s) - t0 < 20))" 1
grep -q "session 1 stopped: its transcript was idle for 2s$" "$LOG"; check "idle: logged" $? 0
check "idle: counted" "$(wc -l <"$DIR/fails-175")" 1
check "idle: what claude started ended too" "$(kill -0 "$(cat "$FAKE.child")" 2>/dev/null && echo running)" ""
: >"$LOG"
echo busy >"$FAKE"
work
grep -q "stopped: its transcript was idle" "$LOG"; check "busy: not ended" $? 1
grep -q "session 1 end: exit 0 after 0m, 9 turns" "$LOG"; check "busy: ran to its end" $? 0
: >"$LOG"
echo agent >"$FAKE"
work
grep -q "stopped: its transcript was idle" "$LOG"; check "subagent writing: not ended" $? 1
: >"$LOG"
echo quiet >"$FAKE"
work
grep -q "stopped: its transcript was idle" "$LOG"; check "no transcript: not ended" $? 1
check "no transcript: logged once" "$(grep -c "session 1 wrote no transcript in 2s (.*); it runs until the 6h timeout" "$LOG")" 1
check "seconds: 45m" "$(seconds 45m)" 2700
check "seconds: 1d" "$(seconds 1d)" 86400
seconds 1.5h >/dev/null; check "seconds: 1.5h refused" $? 1
: >"$LOG"
# A session that ends at once still logs it (#479): the relay checks RELAY_IDLE
# before the session starts, not in the watch the session's end cuts short.
IDLE=1.5h
echo fail >"$FAKE"
SESSION_TIMEOUT=4s work
grep -q "RELAY_IDLE (1.5h) or RELAY_IDLE_KILL (2s) isn't like 45m; session 1 runs until the 4s timeout" "$LOG"; check "idle: a bad RELAY_IDLE logged" $? 0

# A process the session leaves behind doesn't get the worker's lock (fd 9), so
# the lock is free once the worker exits.
IDLE=2s
echo leftover >"$FAKE"
(exec 9>"$DIR/lock-7" && flock -n 9 && work)
check "leftover: still running" "$(kill -0 "$(cat "$FAKE.child")" 2>/dev/null && echo running)" running
flock -n "$DIR/lock-7" true; check "leftover: the lock is free" $? 0
kill "$(cat "$FAKE.child")" 2>/dev/null

# Idle workers take no start slot (#242): pick asks before admit, so with one
# ready issue N workers start one session, and the other N-1 stop without
# touching .relay/admitted; a triage started next isn't held up by them.
IDLE=45m
echo commit >"$FAKE"
pick() { [ -s "$T/ready" ] || return 1; ISSUE=$(cat "$T/ready") TITLE=t URL=u HOW=ready; }
gh_take() { [ -s "$T/ready" ] || return 4; : >"$T/ready"; echo take >>"$T/takes"; printf 'otter-175\tb\n'; }
: >"$LOG"
rm -f "$T/takes" "$DIR/admitted" "$DIR/fails-175"
echo 175 >"$T/ready"
ADMIT_GAP=300
work
check "idle workers: one session" "$(takes)" 1
started=$(stat -c %Y "$DIR/admitted")
touch -d @$((started - 60)) "$DIR/admitted"
: >"$LOG"
for k in 2 3 4 5; do (K=$k work) & done
t0=$(date +%s)
wait
check "idle workers: stop at once" "$(($(date +%s) - t0 < 5))" 1
check "idle workers: no more sessions" "$(takes)" 1
check "idle workers: all stopped" "$(grep -c "no issue ready; stopping" "$LOG")" 4
check "idle workers: admitted untouched" "$(stat -c %Y "$DIR/admitted")" $((started - 60))
grep -q "waiting: a session started" "$LOG"; check "idle workers: none waited" $? 1
rm -f "$DIR/admitted"
for k in 2 3 4; do (K=$k work) & done
wait
check "idle workers: no slot taken" "$(test -e "$DIR/admitted" && echo taken)" ""
gh_proposed() { echo 40; }
DRY_RUN=1 triage >/dev/null; check "idle workers: triage not held up" $? 0
gh_proposed() { :; }
DRY_RUN= triage; check "triage: none proposed, no slot" "$(test -e "$DIR/admitted" && echo taken)" ""
# An issue taken while the worker waits in admit: it asks again and stops.
: >"$LOG"
echo 175 >"$T/ready"
touch "$DIR/admitted"
(sleep 2; : >"$T/ready") &
ADMIT_GAP=4 work
wait
check "taken while waiting: no session" "$(takes)" 1
grep -q "waiting: a session started" "$LOG"; check "taken while waiting: waited" $? 0
check "taken while waiting: stopped" "$(grep -c "no issue ready; stopping" "$LOG")" 1

# A maintainer sweep, with claude faked, on origin/main: it records the
# activity after it; one cut short by a limit or a crash records nothing, so
# the next one still looks back to the last full sweep.
git -C "$WT" update-ref refs/remotes/origin/main HEAD
gh_activity() { echo "2026-10-06T08:00:00Z abc123"; }
rm -f "$DIR/paused-until" "$DIR/maintained"
: >"$LOG"
echo fail >"$FAKE"
ADMIT_GAP=0 maintain
check "maintain: records the activity" "$(cut -d' ' -f2- "$DIR/maintained")" "2026-10-06T08:00:00Z abc123"
check "maintain: start logged" "$(grep -c 'maintainer start: id .*, last sweep never' "$LOG")" 1
check "maintain: on origin/main" "$(git -C "$WT" rev-parse --abbrev-ref HEAD)" HEAD
echo "86400 t0 sha0" >"$DIR/maintained"
echo limit >"$FAKE"
ADMIT_GAP=0 maintain
check "maintain: a limit records nothing" "$(cat "$DIR/maintained")" "86400 t0 sha0"
rm -f "$DIR/paused-until"
echo crash >"$FAKE"
ADMIT_GAP=0 maintain
check "maintain: a crash records nothing" "$(cat "$DIR/maintained")" "86400 t0 sha0"
check "maintain: says so" "$(grep -c "the sweep didn't run to its end" "$LOG")" 2

exit $fail
