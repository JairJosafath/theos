#!/usr/bin/env bash
# Tests for the relay's memory guard in hack/relay.sh: the worker cap on the
# device with the Kind cluster, and the wait for headroom before a pick, with a
# faked /proc/meminfo; the flush of a kept handoff, which releases the
# session's claim so the relay has none left to release; the cluster
# lease a session leaves behind, which the relay drops; and the triage of the
# proposed issues, as a dry run.
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
kill %1; wait 2>/dev/null

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

# Triage, as a dry run: the proposed issues go into its prompt; none, no session.
rm -f "$STOP" "$DIR/admitted"
meminfo 8
gh_proposed() { printf '40\n41\n'; }
out=$(DRY_RUN=1 triage)
check "triage: names the issues" "$(grep -c 'status/proposed: #40 #41\.' <<<"$out")" 1
check "triage: readies by status" "$(grep -c 'hack/lib/github.sh status N ready' <<<"$out")" 1
check "triage: needs-human stays the user's" "$(grep -c "needs-human stays the user's" <<<"$out")" 1
gh_proposed() { :; }
rm -f "$DIR/admitted"
check "triage: none proposed" "$(DRY_RUN=1 triage)" "dry run: no proposed issues; no triage"
# A triage whose session never ran keeps only the time, for the watch to try again.
echo "1 40 41" >"$DIR/triaged"
DRY_RUN= untriaged
check "triage: never ran" "$(awk '{print NF}' "$DIR/triaged")" 1

exit $fail
