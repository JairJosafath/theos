#!/usr/bin/env bash
# Tests for the relay's memory guard in hack/relay.sh: the worker cap on the
# device with the Kind cluster, and the wait for headroom before a pick, with a
# faked /proc/meminfo; the flush of a kept handoff, which releases the
# session's claim so the relay has none left to release; the cluster
# lease a session leaves behind, which the relay drops; the Docker gate, with
# docker faked; the triage of the
# proposed issues, as a dry run; and, with claude faked, the pause a usage
# limit sets, the issue that goes to the user after failed sessions and the
# session the relay ends when its transcript stops growing.
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

# The Docker gate, with docker faked (DOCKER: ok, denied, down, slow): a worker
# that can't reach Docker runs its sessions as C4_CLUSTER=none and logs why
# once per change; one that can keeps the device's C4_CLUSTER.
mkdir -p "$T/bin"
cat >"$T/bin/docker" <<'FAKE'
#!/usr/bin/env bash
case $(cat "$DOCKER" 2>/dev/null) in
  denied) echo 'permission denied while trying to connect to the Docker daemon socket at unix:///var/run/docker.sock: Get "http://%2Fvar%2Frun%2Fdocker.sock/v1.47/info": dial unix /var/run/docker.sock: connect: permission denied' >&2; exit 1 ;;
  down) echo 'Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?' >&2; exit 1 ;;
  slow) sleep 5 ;;
  *) echo 27.5.1 ;;
esac
FAKE
chmod +x "$T/bin/docker"
export DOCKER=$T/docker
gate() { # gate CLUSTER0 STATE: run the gate on a worker started with C4_CLUSTER=CLUSTER0; print C4_CLUSTER after it
  echo "$2" >"$DOCKER"
  if [ "$1" = unset ]; then unset C4_CLUSTER; else export C4_CLUSTER=$1; fi
  CLUSTER0=$1 PATH=$T/bin:$PATH DOCKER_WAIT=1 docker_gate
  echo "${C4_CLUSTER-unset}"
}
: >"$LOG"
while IFS='|' read -r what cluster0 state want reason; do
  check "docker gate: $what" "$(gate "$cluster0" "$state")" "$want"
  if [ -n "$reason" ]; then
    grep -q "docker: $reason.*; sessions run as C4_CLUSTER=none and skip needs/cluster issues$" "$LOG"
    check "docker gate: $what: logs why" $? 0
  else
    check "docker gate: $what: logs nothing" "$(cat "$LOG")" ""
  fi
  : >"$LOG"
done <<'TABLE'
reachable|unset|ok|unset|
reachable, cluster named|c4factory|ok|c4factory|
permission denied|unset|denied|none|permission denied on the Docker socket: the relay runs without the docker group
daemon down|unset|down|none|the Docker daemon is down: Cannot connect
no answer|unset|slow|none|the Docker daemon didn't answer in 1s
no cluster on the device|none|denied|none|
TABLE
# Once per change, and back when Docker answers again, in one worker.
(
  gate unset denied >/dev/null; gate unset denied >/dev/null; gate unset denied >/dev/null
  check "docker gate: logged once" "$(grep -c "docker: permission denied" "$LOG")" 1
  gate unset down >/dev/null
  check "docker gate: a new reason logged" "$(grep -c "docker: " "$LOG")" 2
  check "docker gate: the prompt says why" "$(prompt 1 t u ready n b | grep -c "can't reach Docker (the Docker daemon is down")" 1
  gate unset ok >/dev/null
  check "docker gate: back" "${C4_CLUSTER-unset}" unset
  check "docker gate: back logged" "$(grep -c "docker: reachable again" "$LOG")" 1
  check "docker gate: the prompt is the cluster's again" "$(prompt 1 t u ready n b | grep -c "can't reach Docker")" 0
)
: >"$LOG"
rm -f "$DOCKER"

# Triage, as a dry run: the proposed issues go into its prompt; none, no session.
rm -f "$STOP" "$DIR/admitted"
meminfo 8
gh_proposed() { printf '40\n41\n'; }
out=$(DRY_RUN=1 triage)
check "triage: names the issues" "$(grep -c 'status/proposed: #40 #41\.' <<<"$out")" 1
check "triage: readies by status" "$(grep -c 'hack/lib/github.sh status N ready' <<<"$out")" 1
check "triage: needs-human stays the user's" "$(grep -c "needs-human stays the user's" <<<"$out")" 1
check "triage: writes a missing Depends on itself" "$(grep -c 'a "Depends on #M" line it lacks' <<<"$out")" 1
check "triage: sends a user's decision to needs-human" "$(grep -c 'hack/lib/github.sh status N needs-human' <<<"$out")" 1
gh_proposed() { :; }
rm -f "$DIR/admitted"
check "triage: none proposed" "$(DRY_RUN=1 triage)" "dry run: no proposed issues; no triage"
# A triage whose session never ran keeps only the time, for the watch to try again.
echo "1 40 41" >"$DIR/triaged"
DRY_RUN= untriaged
check "triage: never ran" "$(awk '{print NF}' "$DIR/triaged")" 1

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
  hang) mkdir -p "${t%/*}"; echo '{}' >"$t"; sleep 60 & echo $! >"$FAKE.child"; wait ;;
  busy) mkdir -p "${t%/*}"; for _ in 1 2 3 4 5 6; do echo '{}' >>"$t"; sleep 1; done
    echo '{"is_error":false,"result":"busy","num_turns":9}' ;;
  agent) mkdir -p "${t%.jsonl}/subagents"; echo '{}' >"$t"
    for _ in 1 2 3 4 5 6; do echo '{}' >>"${t%.jsonl}/subagents/agent-1.jsonl"; sleep 1; done
    echo '{"is_error":false,"result":"agent","num_turns":9}' ;;
  quiet) sleep 4; echo '{"is_error":false,"result":"quiet","num_turns":1}' ;;
  limit) echo '{"is_error":true,"result":"You'"'"'ve hit your session limit · resets 3am (UTC)","num_turns":1}'; exit 1 ;;
  login) echo 'Not logged in · Please run /login' >&2; exit 1 ;;
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
# sets the issue to needs-human, with a comment naming each session.
echo fail >"$FAKE"
work; work
check "fails: two counted" "$(wc -l <"$DIR/fails-175")" 2
check "fails: not yet the user's" "$(cat "$T/status" 2>/dev/null)" ""
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
check "fails: two, still not the user's" "$(cat "$T/status" 2>/dev/null)" ""
work
check "fails: the third sets needs-human" "$(cat "$T/status")" "175 needs-human"
check "fails: count cleared" "$(test -e "$DIR/fails-175" && echo kept)" ""
for id in $(sed -n 's/.*session 1 start: id \([^,]*\),.*/\1/p' "$LOG"); do
  grep -q "^### Session \`$id\`: exit 0 after 0m$" "$T/comment"; check "fails: comment names $id" $? 0
done
check "fails: comment has each stderr" "$(grep -c '^boom ' "$T/comment")" 3
check "fails: comment has each account" "$(grep -c '^gave up$' "$T/comment")" 3
grep -q "#175 failed 3 sessions in a row; set it to status/needs-human: https://github.com/x/issues/175#issuecomment-9" "$LOG"
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
IDLE=1.5h
echo hang >"$FAKE"
SESSION_TIMEOUT=4s work
grep -q "RELAY_IDLE (1.5h) or RELAY_IDLE_KILL (2s) isn't like 45m; session 1 runs until the 4s timeout" "$LOG"; check "idle: a bad RELAY_IDLE logged" $? 0

exit $fail
