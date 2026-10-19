#!/usr/bin/env bash
# Tests for the relay's memory guard in hack/relay.sh: the worker cap on the
# device with the Kind cluster, and the wait for headroom before a pick, with a
# faked /proc/meminfo; the disk guard, with a stub df, and its .vhdx lookup,
# with stub reg.exe and wslpath (#1344); the flush of a kept handoff, which releases the
# session's claim so the relay has none left to release; the prompts, which
# never send a session to a cluster; the triage of the
# proposed issues and the maintainer's prompt, as dry runs; the triage's own
# worktree and lock, which it starts in however many workers run; against a
# fake origin, switch_branch's three ways to the issue's branch (the local one
# fast-forwarded, origin's, a new one from origin/main), worktree()'s fetch,
# creation and stash of what a session left, and update() pulling nothing
# under RELAY_PULL=0 and stopping, not restarting, on the stop file; and, with claude faked, the pause a usage
# limit sets and the limit the claim's release names, the issue that goes to the user after failed sessions and the
# session the relay ends when its transcript stops growing, and each session's
# TMPDIR, outside any checkout, emptied before and removed after it; a
# worktree's own .dev/, which becomes the link to the main checkout's; the worktree
# with no git identity, which starts no session (#1289); and idle workers,
# which stop without taking a session-start slot; the maintainer, which goes
# first in admit; the maintainer lease, which keeps a second device's
# maintainer from starting while one runs; a maintainer sweep, which
# records the activity it saw; and the move to origin/main's code in
# relay-code from a checkout on another branch, against a scratch origin.
# Run: hack/relay_test.sh
T=$(mktemp -d)
# Only this shell cleans up (#544): a job killed just after it is forked, before
# it resets the traps it inherits, runs this trap too, and would remove $T.
trap 'if [ "$BASHPID" = $$ ]; then kill $(jobs -p) 2>/dev/null; rm -rf "$T"; fi' EXIT
unset C4_CLUSTER RELAY_MAX_WORKERS RELAY_MIN_MEM_GIB RELAY_ADMIT_GAP RELAY_DRY_RUN RELAY_MIN_FREE_GB RELAY_DISK_PATHS RELAY_OSRELEASE
fail=0
check() { if [ "$2" = "$3" ]; then echo "ok   $1"; else echo "FAIL $1: got [$2] want [$3]"; fail=1; fi; }
# Jobs killed as soon as they start, as maintain kills its heartbeat and
# session its idle watch when claude ends at once, leave $T.
{ for _ in $(seq 100); do (sleep 9) & kill $!; wait $!; done; } 2>/dev/null
check "trap: killed jobs leave the scratch dir" "$(test -d "$T" && echo kept)" kept
# The defaults, from today's measurements (docs/dev/testenv.md): nine workers,
# 2 GiB free and 120s between session starts.
check "defaults" "$(. "$(dirname "$0")/relay.sh"; echo "$MAX_WORKERS $MIN_MEM_GIB $ADMIT_GAP $MIN_FREE_GB")" "9 2 120 30"
check "defaults: RELAY_MIN_FREE_GB and RELAY_DISK_PATHS" "$(RELAY_MIN_FREE_GB=5 RELAY_DISK_PATHS='/a /b'; . "$(dirname "$0")/relay.sh"; echo "$MIN_FREE_GB" $(disk_paths))" "5 /a /b"
check "defaults: scratch in the user's cache, not ./dev's XDG_CACHE_HOME (#1307, #1326)" "$(unset RELAY_SCRATCH; HOME=/h XDG_CACHE_HOME=/repo/.dev/cache; . "$(dirname "$0")/relay.sh"; echo "$SCRATCH")" /h/.cache/c4factory/tmp
# With XDG_CACHE_HOME inside a real git checkout, as ./dev sets it, the
# scratch is in no git checkout (#1347).
git init -q "$T/main" && mkdir -p "$T/home"
scratch=$(unset RELAY_SCRATCH; HOME=$T/home XDG_CACHE_HOME=$T/main/.dev/cache; . "$(dirname "$0")/relay.sh"; echo "$SCRATCH")
check "defaults #1347: scratch in no git checkout" "$(mkdir -p "$scratch" && cd "$scratch" && GIT_CEILING_DIRECTORIES=$T git rev-parse --show-toplevel 2>/dev/null)" ""

# No disk is low unless a test says so; the disk guard's own tests stub df.
export RELAY_MIN_FREE_GB=0 RELAY_DISK_PATHS=/
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

# The disk guard (#1344): a stub df reports each path's mount and free GB from
# $T/df (PATH MOUNT GB lines); a stub trim records its runs.
rm -f "$STOP" "$DIR/admitted"
mkdir -p "$T/dfbin"
cat >"$T/dfbin/df" <<'STUB'
#!/usr/bin/env bash
echo "Filesystem 1024-blocks Used Available Capacity Mounted on"
awk -v p="${@: -1}" '$1 == p { printf "dev 1 1 %d 1%% %s\n", $3 * 1048576, $2; found = 1 } END { exit !found }' "$DF"
STUB
chmod +x "$T/dfbin/df"
export DF=$T/df
printf '/ / 100\n/repo / 100\n/mnt/c/wsl /mnt/c 12\n/mnt/d /mnt/d 50\n' >"$DF"
trim_caches() { echo trim >>"$T/trims"; }
disk() { PATH=$T/dfbin:$PATH "$@"; }
meminfo 8
OSRELEASE=$T/osrelease
echo 'Linux 6.8.0-generic' >"$OSRELEASE"
MIN_FREE_GB=30 DISK_PATHS='/ /repo /mnt/d'
disk admit; check "disk: above the minimum, a session starts" $? 0
check "disk: no trim above it" "$(cat "$T/trims" 2>/dev/null)" ""
rm -f "$DIR/admitted"
DISK_PATHS='/ /repo /mnt/c/wsl /mnt/d'
: >"$LOG"
MEM_WAIT=1 disk admit; check "disk: below on one path, no session" $? 1
check "disk: no start slot taken" "$(test -e "$DIR/admitted" && echo taken)" ""
check "disk: the log names the path, its free space and the minimum" \
  "$(grep -c 'waiting for disk: /mnt/c/wsl has 12 GB free, a session needs 30 (RELAY_MIN_FREE_GB)' "$LOG")" 1
check "disk: names no path above it" "$(grep -c 'waiting for disk: /mnt/c/wsl' "$LOG") $(grep -c 'waiting for disk' "$LOG")" "1 1"
check "disk: trims the caches at once" "$(wc -l <"$T/trims")" 1
MEM_WAIT=1 disk admit; check "disk: still below" $? 1
check "disk: the line repeats at most once an hour" "$(grep -c 'waiting for disk' "$LOG")" 1
check "disk: so does the trim" "$(wc -l <"$T/trims")" 1
touch -d '-61 minutes' "$DIR/disk-low--mnt-c-wsl"
MEM_WAIT=1 disk admit
check "disk: after an hour it says it again" "$(grep -c 'waiting for disk' "$LOG")" 2
check "disk: and trims again" "$(wc -l <"$T/trims")" 2
check "disk: outside WSL no word of the .vhdx" "$(grep -c vhdx "$LOG")" 0
echo 6.18.54.1-microsoft-standard-WSL2 >"$OSRELEASE"
rm -f "$DIR"/disk-low-*
: >"$LOG"
MEM_WAIT=1 disk admit
check "disk: under WSL it says only compacting the .vhdx helps" "$(grep -c 'only compacting the .vhdx.*docs/dev/testenv.md' "$LOG")" 1
MIN_FREE_GB=10 disk admit; check "disk: RELAY_MIN_FREE_GB lowers the minimum" $? 0
rm -f "$DIR/admitted" "$DIR"/disk-low-*
printf '/mnt/c/wsl /mnt/c 12\n/mnt/c/docker /mnt/c 12\n' >"$DF"
: >"$LOG"
MEM_WAIT=1 DISK_PATHS='/mnt/c/wsl /mnt/c/docker' disk admit
check "disk: one line per filesystem, for the first path on it" "$(grep 'waiting for disk' "$LOG" | sed 's/.*disk: \([^ ]*\).*/\1/')" /mnt/c/wsl
rm -f "$DIR/admitted" "$DIR"/disk-low-*
echo 'Linux 6.8.0-generic' >"$OSRELEASE"

# The .vhdx lookup: stub reg.exe and wslpath; /mnt/c when they give nothing.
mkdir -p "$T/regbin"
cat >"$T/regbin/reg.exe" <<'STUB'
#!/usr/bin/env bash
[ -n "$REG_FAIL" ] && exit 1
printf '\r\nHKEY_CURRENT_USER\\Software\\Microsoft\\Windows\\CurrentVersion\\Lxss\\{0283}\r\n'
printf '    BasePath    REG_SZ    C:\\Users\\j\\AppData\\Local\\wsl\\{0283}\r\n\r\n'
printf 'HKEY_CURRENT_USER\\Software\\Microsoft\\Windows\\CurrentVersion\\Lxss\\{6923}\r\n'
printf '    BasePath    REG_SZ    \\\\?\\D:\\Docker\\wsl\\main\r\n\r\nEnd of search: 2 match(es) found.\r\n'
STUB
cat >"$T/regbin/wslpath" <<'STUB'
#!/usr/bin/env bash
[ -n "$WSLPATH_FAIL" ] && exit 1
[ "$1" = -u ] && shift
case $1 in [A-Z]:\\*) d=${1%%:*}; r=${1#?:}; echo "/mnt/${d,,}${r//\\//}" ;; *) exit 1 ;; esac
STUB
chmod +x "$T/regbin/reg.exe" "$T/regbin/wslpath"
vhdx() { PATH=$T/regbin:$PATH vhdx_dirs | paste -sd' '; }
check "vhdx: nothing outside WSL" "$(vhdx)" ""
echo 6.18.54.1-microsoft-standard-WSL2 >"$OSRELEASE"
check "vhdx: each distro's BasePath, mapped" "$(vhdx)" "/mnt/c/Users/j/AppData/Local/wsl/{0283} /mnt/d/Docker/wsl/main"
check "vhdx: /mnt/c when reg.exe fails" "$(REG_FAIL=1 vhdx)" /mnt/c
check "vhdx: /mnt/c when wslpath fails" "$(WSLPATH_FAIL=1 vhdx)" /mnt/c
check "vhdx: watched with / and the checkouts" "$(DISK_PATHS= REPO=/repo PATH=$T/regbin:$PATH disk_paths | paste -sd' ')" "/ /repo /mnt/c/Users/j/AppData/Local/wsl/{0283} /mnt/d/Docker/wsl/main"
OSRELEASE=/proc/sys/kernel/osrelease
MIN_FREE_GB=0 DISK_PATHS=/

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

# No prompt tells a session to lease, create or deploy to a cluster (#353):
# the worker's, the triage's and the maintainer's, on a device with or without one.
for c in unset none; do
  out=$(if [ "$c" = unset ]; then unset C4_CLUSTER; else C4_CLUSTER=$c; fi
    prompt 1 t u ready n b; prompt 1 t u in-progress n b)
  check "prompt, C4_CLUSTER $c: no cluster steps" "$(grep -cE 'cluster acquire|cluster release|make deploy|--fresh|needs/cluster' <<<"$out")" 0
  check "prompt, C4_CLUSTER $c: never a cluster" "$(grep -c 'Never lease, create or deploy to a Kind cluster' <<<"$out")" 2
done

# A follow-up (#1284): fixed in the pull request when it is already there,
# filed --next when decided, proposed when it needs a decision; a session on
# a --next issue is told where it came from.
out=$(prompt 1 t u ready n b)
check "prompt: a small follow-up goes in the pull request" "$(grep -c 'Also fixes:' <<<"$out")" 1
check "prompt: a decided follow-up is filed --next" "$(grep -c 'hack/lib/github.sh create --next TITLE FILE' <<<"$out")" 1
check "prompt: the rest is filed proposed" "$(grep -c 'FILE status/proposed kind/<kind>' <<<"$out")" 1
check "prompt, next: the last session filed it" "$(prompt 1 t u next n b | grep -c 'filed it with create --next: the issue body is the goal')" 1

# A resumed issue continues from its newest handoff at every priority (#1309):
# pick says in-progress for a priority/3 one (#1329), and its prompt says so.
(
  mkdir -p "$DIR"
  gh_api() {
    case $* in
      *issues/comments*) echo '[[]]' ;;
      *issues\?state=open*) echo '[[{"number":7,"title":"t","html_url":"u","body":"","labels":[{"name":"status/in-progress"},{"name":"priority/3"}]}]]' ;;
    esac
  }
  pick; check "pick, in-progress + priority/3: in-progress (#1309)" "$? $ISSUE $HOW" "0 7 in-progress"
  check "prompt, in-progress + priority/3: continues from the handoff (#1309)" \
    "$(prompt "$ISSUE" "$TITLE" "$URL" "$HOW" n b | grep -c 'continue from its newest handoff comment')" 1
  exit $fail
) || fail=1

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
  STATE='open status/waiting,kind/feature'; check "outcome: waiting" "$(outcome 1 7)" waiting
  STATE='open status/waiting'; check "outcome: waiting after a handoff" "$(outcome 1 6)" waiting
  STATE='open status/ready'; check "outcome: set back to ready" "$(outcome 1 7)" released
  STATE='open status/proposed'; check "outcome: set back to proposed" "$(outcome 1 7)" released
  STATE='open status/ready'; check "outcome: ready after a handoff" "$(outcome 1 6)" handed-off
  exit $fail
) || fail=1

# Triage, as a dry run: the proposed issues go into its prompt, which loads the
# triage skill; the rules are the skill's. None proposed, no session.
rm -f "$STOP" "$DIR/admitted"
meminfo 8
gh_proposed() { printf '40\n41\n'; }
out=$(DRY_RUN=1 triage)
skill=$(cat "$(dirname "$0")/../.claude/skills/triage/SKILL.md")
check "triage: names the issues" "$(grep -c 'status/proposed: #40 #41\.' <<<"$out")" 1
check "triage: loads the skill" "$(grep -c 'Load the `triage` skill and follow it' <<<"$out")" 1
check "triage: escalates to the maintainer, never the owner" "$(grep -c 'send what needs a decision to the maintainer' <<<"$out") $(grep -c 'sent to the user' <<<"$out")" "1 0"
check "triage: no cluster steps" "$(grep -cE 'cluster acquire|make deploy|needs/cluster' <<<"$out$skill")" 0
check "triage skill: readies by status" "$(grep -c 'hack/lib/github.sh status N ready' <<<"$skill")" 1
check "triage skill: needs-human is the owner's" "$(grep -c "needs-human is the owner's" <<<"$skill")" 1
check "triage skill: writes a missing Depends on itself" "$(grep -c 'a "Depends on #M" line it lacks' <<<"$skill")" 1
check "triage skill: gives a kind label before readying" "$(grep -c 'exactly one `kind/\*` label' <<<"$skill") $(grep -c 'refuses an issue with no kind' <<<"$skill")" "1 1"
check "triage skill: escalates a decision to the maintainer" "$(grep -c 'hack/lib/github.sh status N needs-maintainer' <<<"$skill")" 1
check "triage skill: never sets needs-human" "$(grep -c 'status N needs-human' <<<"$out$skill") $(grep -c 'never set status/needs-human' <<<"$skill")" "0 1"
check "triage skill: needs-maintainer is the maintainer's" "$(grep -c "needs-maintainer is the maintainer's" <<<"$skill")" 1
check "triage skill: readies an unsettled design as design work" "$(grep -c '^- Ready it as design work' <<<"$skill")" 1
check "triage skill: a design issue's plan says Refs, not Closes (#491)" "$(grep -c 'request says `Refs #N`, never `Closes #N`' <<<"$skill")" 1
check "triage skill: a design issue waits on its steps (#491)" "$(grep -c 'a "Depends on #S" line per step' <<<"$skill")" 1
check "triage skill: the plan is not the issue's Done when (#491)" "$(grep -c 'that is its "Done when"' <<<"$skill")" 0
check "triage skill: never asks how to build it" "$(grep -c 'Never ask how to' <<<"$skill")" 1
check "triage skill: a direction comes with the choice it would make" "$(grep -c 'says the choice you would make and why' <<<"$skill")" 1
check "triage skill: never to the owner" "$(grep -c 'never to the owner' <<<"$skill")" 1
gh_proposed() { :; }
rm -f "$DIR/admitted"
check "triage: none proposed" "$(DRY_RUN=1 triage)" "dry run: no proposed issues; no triage"
# The triage runs in its own worktree, relay-triage, under its own lock (#1191),
# and its prompt says so: it starts while every worker runs, past
# RELAY_MAX_WORKERS on the device with the cluster, but not beside a triage
# that runs.
(
  . "$(dirname "$0")/relay.sh" --triage
  check "triage: its own worktree, log and lock" "${WT#"$REPO"/} ${LOG##*/} ${LOCK##*/}" \
    ".claude/worktrees/relay-triage relay-triage.log triage.lock"
  DIR=$T/triage STOP=$T/triage/stop LOCK=$T/triage/triage.lock LOG=$T/triage/log MAX_WORKERS=1
  mkdir -p "$DIR"
  gh_proposed() { printf '40\n41\n'; }
  check "triage: its prompt names its worktree" "$(DRY_RUN=1 triage | grep -c 'this worktree, relay-triage, is on origin/main')" 1
  gh_token() { :; }
  setsid() { echo "${C4_RELAY_CHILD:-} ${C4_RELAY_TRIAGE:-} $*" >"$T/setsid"; }
  flock -o "$DIR/lock-1" sleep 30 &
  l1=$!
  flock -o "$DIR/lock-2" sleep 30 &
  l2=$!
  until ! flock -n "$DIR/lock-1" true && ! flock -n "$DIR/lock-2" true; do sleep 0.1; done
  start_triage hack/relay.sh >"$T/out" 2>&1
  rc=$?
  wait $!
  check "triage: starts while every worker runs" "$rc $(grep -c "^triage started (pid [0-9]*): tail -f $LOG$" "$T/out")" "0 1"
  check "triage: started as the triage's child" "$(cat "$T/setsid")" "1 1 nohup bash hack/relay.sh"
  rm -f "$T/setsid"
  flock -o "$LOCK" sleep 30 &
  l3=$!
  until ! flock -n "$LOCK" true; do sleep 0.1; done
  start_triage hack/relay.sh >"$T/out" 2>&1
  check "triage: none beside one that runs" "$? $(grep -c 'a triage runs already' "$T/out") $(test -e "$T/setsid" && echo started)" "1 1 "
  # relay.sh alone writes .relay/triaged (#1128): the triage that runs keeps
  # its record, and a start with no token keeps only the time.
  echo "1 40 41" >"$DIR/triaged"
  DRY_RUN= start_triage hack/relay.sh >/dev/null 2>&1
  check "triage: the one that runs keeps its record" "$(cat "$DIR/triaged")" "1 40 41"
  kill "$l1" "$l2" "$l3"
  wait 2>/dev/null
  gh_token() { return 1; }
  DRY_RUN= start_triage hack/relay.sh >/dev/null 2>&1
  check "triage: no token, only the time recorded" "$? $(awk '{print NF}' "$DIR/triaged")" "1 1"
  exit $fail
) || fail=1
# A triage whose session never ran keeps only the time, for the watch to try again.
echo "1 40 41" >"$DIR/triaged"
DRY_RUN= untriaged
check "triage: never ran" "$(awk '{print NF}' "$DIR/triaged")" 1
# A triage that starts, from a terminal or the watch, records its time and issues (#688).
DRY_RUN= triaged "#40 #41"
check "triage: records its issues" "$(cut -d' ' -f2- "$DIR/triaged")" "40 41"
check "triage: records its time" "$(($(date +%s) - $(cut -d' ' -f1 "$DIR/triaged") < 5))" 1

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

# limit_name: the few words for each limit a session can end on (#1199), the
# most specific first: an expired OAuth session also failed to authenticate.
while IFS='|' read -r msg want; do
  check "limit_name: $msg" "$(limit_name "$msg")" "$want"
done <<'EOF2'
Failed to authenticate: OAuth session expired and could not be refreshed|OAuth session expired
API Error: 401 {"type":"error","error":{"type":"authentication_error","message":"OAuth token has expired."}}|OAuth token expired
OAuth token revoked · Please run /login|OAuth token revoked
Invalid API key · Please run /login|invalid API key
Not logged in · Please run /login|not logged in
API Error: 401 {"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}|authentication failed
Credit balance is too low|credit balance too low
You've hit your session limit · resets 3am (UTC)|usage limit
Usage limit reached · resets 5pm|usage limit
EOF2
limit_name "the build failed" >/dev/null; check "limit_name: no limit" $? 1

# switch_branch, worktree and update (#1129), in a scratch clone of a fake
# origin, so no stash lands in the real checkout; another device, up, pushes to it.
(
  S=$T/sw
  REPO=$S/repo WT=$S/wt LOG=$S/log GH_PENDING_DIR=$S/wt/.relay K=7 PULL=1 STOP=$S/stop CODE_SUM= DRY_RUN=
  mkdir -p "$S"
  # tip REPO REV MSG: a new commit on top of REV, with its tree.
  tip() { git -C "$1" -c user.name=t -c user.email=t@t commit-tree -p "$2" -m "$3" "$2^{tree}"; }
  at() { echo "$(git -C "$WT" rev-parse --abbrev-ref HEAD) $(git -C "$WT" rev-parse HEAD)"; }
  sha() { git -C "$REPO" rev-parse "$1"; }
  upstream() { git -C "$REPO" for-each-ref --format='%(upstream:short)' "refs/heads/$1"; }
  git init -q --bare -b main "$S/origin.git"
  git init -q -b main "$S/up"
  echo 1 >"$S/up/f"
  git -C "$S/up" add f
  git -C "$S/up" -c user.name=t -c user.email=t@t commit -q -m first
  git -C "$S/up" remote add origin "$S/origin.git"
  git -C "$S/up" push -q origin main
  git clone -q "$S/origin.git" "$REPO"
  git -C "$S/up" push -q origin "$(tip "$S/up" main second):refs/heads/main"
  # A missing worktree is created after the fetch, detached at origin/main, with .relay ignored.
  worktree
  check "worktree: created at the fetched origin/main" "$? $(at)" "0 HEAD $(git -C "$S/origin.git" rev-parse main)"
  check "worktree: creation logged, .relay ignored" "$(grep -c "created the worktree $WT$" "$LOG") $(cat "$GH_PENDING_DIR/.gitignore")" "1 *"
  # switch_branch, each way: up pushes a1 ahead of the local one, a2 apart
  # from it and b, which only origin has; a3 is local only and checked out in
  # the main checkout, so the switch ignores other worktrees; c is nowhere.
  m=$(sha origin/main)
  git -C "$REPO" branch -q a1 "$m"
  git -C "$REPO" branch -q a2 "$(tip "$REPO" "$m" "local a2")"
  git -C "$REPO" switch -q -c a3
  git -C "$S/up" push -q origin "$(tip "$S/up" "$m" "origin a1"):refs/heads/a1" \
    "$(tip "$S/up" "$m" "origin a2"):refs/heads/a2" "$(tip "$S/up" "$m" "origin b"):refs/heads/b"
  git -C "$REPO" fetch -q origin
  switch_branch a1; check "switch: local behind origin, fast-forwarded" "$? $(at)" "0 a1 $(sha origin/a1)"
  l=$(sha a2)
  switch_branch a2; check "switch: diverged, the local commit kept" "$? $(at)" "0 a2 $l"
  check "switch: diverged, logged" "$(grep -c 'local a2 and origin/a2 have diverged; the session sorts it out$' "$LOG")" 1
  l=$(sha a3)
  switch_branch a3; check "switch: local only, checked out elsewhere, unchanged" "$? $(at)" "0 a3 $l"
  switch_branch b; check "switch: origin only, tracking origin/b" "$? $(at) $(upstream b)" "0 b $(sha origin/b) origin/b"
  switch_branch c; check "switch: neither, from origin/main, no upstream" "$? $(at) $(upstream c)" "0 c $m "
  # A switch git refuses fails: a name it rejects, or d, whose f a local change would overwrite.
  switch_branch 'c..d'; check "switch: a name git rejects" "$(($? != 0)) $(at)" "1 c $m"
  git -C "$REPO" switch -q -c d
  echo 2 >"$REPO/f"
  git -C "$REPO" -c user.name=t -c user.email=t@t commit -q -am d
  echo 3 >"$WT/f"
  switch_branch d; check "switch: refused over a local change" "$? $(at)" "1 c $m"
  # What a dead session left uncommitted, a changed file and a new one, is
  # stashed in the scratch clone, and the tree is clean.
  echo new >"$WT/n"
  worktree
  check "worktree: stashed" "$? $(git -C "$WT" stash list | grep -c ': relay-7: left uncommitted on c, ') $(git -C "$WT" stash show --include-untracked --name-only | paste -sd,)" "0 1 f,n"
  check "worktree: clean after, logged" "$(git -C "$WT" status --porcelain) $(grep -c "stashed uncommitted changes left in $WT " "$LOG")" " 1"
  # A fetch that fails (origin gone) prepares nothing.
  mv "$S/origin.git" "$S/gone.git"
  worktree; check "worktree: the fetch fails" "$? $(grep -c 'git fetch failed$' "$LOG")" "1 1"
  mv "$S/gone.git" "$S/origin.git"
  # update: RELAY_PULL=0 pulls nothing; a worker due to restart on
  # relay-code's copy (it runs from the clone, not from relay-code) stops
  # instead when the stop file is there. Each runs in a subshell, for its
  # exit; the restart itself is "relay code" below.
  CODE=$REPO
  mkdir -p "$REPO/hack"
  printf '#!/usr/bin/env bash\necho pulled\n' >"$REPO/hack/pull.sh"
  chmod +x "$REPO/hack/pull.sh"
  : >"$LOG"
  (PULL=0 update 5); check "update: RELAY_PULL=0 pulls nothing" "$? $(grep -c pulled "$LOG")" "0 0"
  touch "$STOP"
  (update 4); check "update: the stop file stops it, no restart" \
    "$? $(grep -c pulled "$LOG") $(grep -c 'stop file found; stopping$' "$LOG") $(grep -c restarting "$LOG")" "0 1 1 0"
  exit $fail
) || fail=1

# The sessions, with claude faked (FAKE: limit, fail, handoff or commit) and
# GitHub faked: one ready issue, #175 on branch b.
WT=$T/wt
git init -q -b b "$WT"
git -C "$WT" config user.name t && git -C "$WT" config user.email t@t
git -C "$WT" commit -q --allow-empty -m first
git init -q --bare "$T/origin.git"
git -C "$WT" remote add origin "$T/origin.git"
mkdir -p "$T/bin"
cat >"$T/bin/claude" <<'FAKE'
#!/usr/bin/env bash
while [ $# -gt 0 ]; do [ "$1" = --session-id ] && id=$2; shift; done
t=$CLAUDE_CONFIG_DIR/projects/${PWD//[^a-zA-Z0-9]/-}/$id.jsonl
case $(cat "$FAKE") in
  leftover) sleep 600 >/dev/null 2>&1 & echo $! >"$FAKE.child"; echo '{"is_error":false,"result":"left","num_turns":1}' ;;
  hang) mkdir -p "${t%/*}"; echo '{}' >"$t"; sleep 60 & echo $! >"$FAKE.child"; wait ;;
  busy) mkdir -p "${t%/*}"; for _ in $(seq 30); do echo '{}' >>"$t"; sleep 0.2; done
    echo '{"is_error":false,"result":"busy","num_turns":9}' ;;
  agent) mkdir -p "${t%.jsonl}/subagents"; echo '{}' >"$t"
    for _ in $(seq 30); do echo '{}' >>"${t%.jsonl}/subagents/agent-1.jsonl"; sleep 0.2; done
    echo '{"is_error":false,"result":"agent","num_turns":9}' ;;
  quiet) sleep 4; echo '{"is_error":false,"result":"quiet","num_turns":1}' ;;
  limit) echo '{"is_error":true,"result":"You'"'"'ve hit your session limit · resets 3am (UTC)","num_turns":1}'; exit 1 ;;
  login) echo 'Not logged in · Please run /login' >&2; exit 1 ;;
  oauth) echo '{"is_error":true,"result":"Failed to authenticate: OAuth session expired and could not be refreshed","num_turns":0}'; exit 1 ;;
  oauth-ok) echo '{"is_error":false,"result":"fixed the failed to authenticate matcher","num_turns":9}' ;;
  oauth-quoted) echo '{"is_error":false,"result":"stopped: the Gitea call said failed to authenticate","num_turns":9}'; exit 1 ;;
  oauth-noturn) echo '{"is_error":false,"result":"Failed to authenticate: OAuth session expired and could not be refreshed","num_turns":0}'; exit 1 ;;
  login-result) echo '{"type":"result","subtype":"success","is_error":false,"num_turns":1,"duration_api_ms":0,"total_cost_usd":0,"result":"Not logged in · Please run /login"}'; exit 1 ;;
  login-quoted) echo '{"type":"result","subtype":"success","is_error":false,"num_turns":9,"duration_api_ms":81234,"result":"stopped: the CLI said Not logged in · Please run /login"}'; exit 1 ;;
  login-err) echo 'Not logged in · Please run /login' >&2; echo '{"is_error":false,"result":"stopped","num_turns":2}'; exit 1 ;;
  crash) echo "crashed" >&2; exit 1 ;;
  stray) echo x >"$MAIN/stray"; echo $((RANDOM + 100)) >"$HANDOFF"; echo '{"is_error":false,"result":"strayed","num_turns":1}' ;;
  env) env >"$FAKE.env"; (cd - >/dev/null && pwd) >"$FAKE.cd"; echo $((RANDOM + 100)) >"$HANDOFF"; echo '{"is_error":false,"result":"env","num_turns":1}' ;;
  tmp) echo "$TMPDIR" >"$FAKE.tmpdir"; (cd "$TMPDIR" && GIT_CEILING_DIRECTORIES=${FAKE%/*} git rev-parse --show-toplevel) >"$FAKE.repo" 2>/dev/null
     if [ -e "$TMPDIR/left" ]; then echo kept; else echo gone; fi >"$FAKE.left"
    d=$(mktemp -d); echo "$d" >"$FAKE.scratch"; mkdir "$d/ro" && touch "$d/ro/f" && chmod a-w "$d/ro"
    echo $((RANDOM + 100)) >"$HANDOFF"; echo '{"is_error":false,"result":"tmp","num_turns":1}' ;;
  fail) echo "boom $RANDOM" >&2; echo '{"is_error":false,"result":"gave up","num_turns":3}' ;;
  handoff) echo $((RANDOM + 100)) >"$HANDOFF"; echo '{"is_error":false,"result":"handed off","num_turns":9}' ;;
  lease) sleep 3; cp "$LEASES" "$LEASES.during"; echo '{"is_error":false,"result":"swept","num_turns":9}' ;;
  commit) git -c user.name=t -c user.email=t@t commit -q --allow-empty -m more; echo '{"is_error":false,"result":"committed","num_turns":9}' ;;
  commit-2) git switch -q -C b-2; git -c user.name=t -c user.email=t@t commit -q --allow-empty -m more; echo '{"is_error":false,"result":"committed on b-2","num_turns":9}' ;;
  push-name) c=$(git -c user.name=t -c user.email=t@t commit-tree -p HEAD -m more 'HEAD^{tree}')
    git push -q origin "$c:refs/heads/b-otter-175"; echo '{"is_error":false,"result":"pushed b-otter-175","num_turns":9}' ;;
  waiting) echo 'open status/waiting' >"$STATE"; echo '{"is_error":false,"result":"waiting","num_turns":9}' ;;
  ready) echo 'open status/ready' >"$STATE"; echo '{"is_error":false,"result":"gave it back","num_turns":9}' ;;
esac
FAKE
chmod +x "$T/bin/claude"
export PATH=$T/bin:$PATH FAKE=$T/fake HANDOFF=$T/handoff STATE=$T/state CLAUDE_CONFIG_DIR=$T/claude
# The main checkout the relay runs from, apart from the worktree (#1371).
REPO=$T/repo-main MAIN=$T/repo-main
export MAIN
git init -q "$REPO"
echo 1 >"$HANDOFF"
echo 'open status/in-progress' >"$STATE"
update() { :; }
worktree() { :; }
gh_claim_read() { return 1; }
pick() { ISSUE=175 TITLE=t URL=u HOW=ready; }
gh_take() { echo take >>"$T/takes"; printf 'otter-175\tb\n'; }
handoff_id() { cat "$HANDOFF"; }
gh_api() { cat "$STATE"; }
# #175's comments, in $COMMENTS; add_comment BODY [AGE] adds one AGE seconds old.
COMMENTS=$T/comments.json
echo '[]' >"$COMMENTS"
add_comment() {
  jq --arg b "$1" --arg t "$(date -u -d "@$(($(date +%s) - ${2:-0}))" +%FT%TZ)" \
    '. + [{id: (length + 1), created_at: $t, html_url: "https://github.com/x/issues/175#issuecomment-\(length + 1)", user: "relay", body: $b}]' \
    "$COMMENTS" >"$COMMENTS.new" && mv "$COMMENTS.new" "$COMMENTS"
}
eval "$(declare -f gh_comments | sed '1s/^gh_comments/gh_comments_api/')"
gh_comments() { if [ "$1" = 175 ]; then cat "$COMMENTS"; else gh_comments_api "$1"; fi; }
gh_comment() { cat >"$T/comment"; add_comment "$(cat "$T/comment")"; jq -r 'last.html_url' "$COMMENTS"; }
counted() { strikes 175 | grep -c .; }
# #175's label events, in $EVENTS; a status swap adds one.
EVENTS=$T/events.json
echo '[]' >"$EVENTS"
label_events() { cat "$EVENTS"; }
no_strikes() { echo '[]' >"$COMMENTS"; echo '[]' >"$EVENTS"; }
set_status() {
  echo "$1 $2" >"$T/status"
  jq --arg l "status/$2" --arg t "$(date -u +%FT%TZ)" '. + [{event: "labeled", created_at: $t, label: {name: $l}}]' \
    "$EVENTS" >"$EVENTS.new" && mv "$EVENTS.new" "$EVENTS"
}
gh_set_status() { set_status "$@"; }
takes() { cat "$T/takes" 2>/dev/null | wc -l | tr -d ' '; }
rm -f "$STOP" "$DIR/admitted"
: >"$LOG"
ADMIT_GAP=0 MAX_SESSIONS=1 MEM_POLL=1

# A worktree where git has no committer identity starts no session (#1289):
# the worker logs why, puts one line under Needs you naming the host and the
# worktree, once, and takes it away when the identity is back.
gh_report_note() { echo "$*" >>"$T/notes"; }
printf '[user]\n\tuseConfigOnly = true\n' >"$T/noident"
git -C "$WT" config --unset user.name && git -C "$WT" config --unset user.email
echo handoff >"$FAKE"
(export GIT_CONFIG_GLOBAL=$T/noident GIT_CONFIG_NOSYSTEM=1 XDG_CONFIG_HOME=$T/none; work; work)
check "no identity (#1289): no claim, no session" "$(takes) $(grep -c 'session 1 start' "$LOG")" "0 0"
grep -q "git has no committer identity in $WT, so no session could commit: fatal: no email was given" "$LOG"; check "no identity (#1289): logs git's reason" $? 0
check "no identity (#1289): one report note, naming host and worktree" \
  "$(grep -c . "$T/notes") $(grep -c "^ident-$(hostname)-wt .*$(hostname).*$WT" "$T/notes")" "1 1"
git -C "$WT" config user.name t && git -C "$WT" config user.email t@t
(export GIT_CONFIG_GLOBAL=$T/noident GIT_CONFIG_NOSYSTEM=1 XDG_CONFIG_HOME=$T/none; work)
check "identity back (#1289): the note goes, a session starts" \
  "$(tail -n1 "$T/notes") $(takes) $(grep -c 'session 1 start' "$LOG")" "ident-$(hostname)-wt 1 1"
(export GIT_CONFIG_GLOBAL=$T/noident GIT_CONFIG_NOSYSTEM=1 XDG_CONFIG_HOME=$T/none; work)
check "identity (#1289): asks GitHub nothing once the note is gone" "$(grep -c . "$T/notes") $(takes)" "2 2"
unset -f gh_report_note
rm -f "$T/takes"
: >"$LOG"

# A usage limit pauses the device until the time it names, and isn't counted.
# The claim's release names the limit (#1199).
echo limit >"$FAKE"
gh_claim_read() { [ -e "$T/claimed" ]; }
gh_take() { echo take >>"$T/takes"; : >"$T/claimed"; printf 'otter-175\tb\n'; }
gh_release() { rm -f "$T/claimed"; echo "$1" >"$T/released"; echo "released #175 (otter-175): $1"; }
work
check "limit: the release names it" "$(cat "$T/released")" "session ended without a handoff (a limit: usage limit)"
want=$(TZ=UTC date -d 3am +%s)
[ "$want" -le "$(date +%s)" ] && want=$((want + 86400))
check "limit: paused until the reset" "$(cut -d' ' -f1 "$DIR/paused-until")" "$want"
grep -q "paused the relay until .*: You've hit your session limit" "$LOG"; check "limit: logged" $? 0
check "limit: not counted" "$(counted)" 0
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
no_strikes
# A failed login, with no result, pauses for RELAY_LIMIT_WAIT.
echo login >"$FAKE"
LIMIT_WAIT=10m work
check "login: paused for RELAY_LIMIT_WAIT" "$(($(cut -d' ' -f1 "$DIR/paused-until") - $(date +%s) > 590))" 1
check "login: not counted" "$(counted)" 0
check "login: the release names it" "$(cat "$T/released")" "session ended without a handoff (a limit: not logged in)"
rm -f "$DIR/paused-until"
# An expired OAuth session, in an error result, pauses too and isn't counted.
echo oauth >"$FAKE"
LIMIT_WAIT=10m work
check "oauth: paused for RELAY_LIMIT_WAIT" "$(($(cut -d' ' -f1 "$DIR/paused-until") - $(date +%s) > 590))" 1
grep -q "paused the relay until .*: Failed to authenticate: OAuth session expired" "$LOG"; check "oauth: logged" $? 0
check "oauth: not counted" "$(counted)" 0
check "oauth: the release names it" "$(cat "$T/released")" "session ended without a handoff (a limit: OAuth session expired)"
rm -f "$DIR/paused-until"
# A session that ends well and only mentions a login failure pauses nothing.
echo oauth-ok >"$FAKE"
work
check "oauth-ok: not paused" "$(test -e "$DIR/paused-until" && echo paused)" ""
no_strikes
# A session that exits non-zero and whose final message only quotes a login
# failure pauses nothing either (#655): the agent's words aren't the CLI's.
echo oauth-quoted >"$FAKE"
work
check "oauth-quoted: not paused" "$(test -e "$DIR/paused-until" && echo paused)" ""
check "oauth-quoted: counted" "$(counted)" 1
no_strikes
# The CLI's login error as a non-error result before any turn still pauses.
echo oauth-noturn >"$FAKE"
LIMIT_WAIT=10m work
check "oauth-noturn: paused" "$(test -e "$DIR/paused-until" && echo paused)" paused
check "oauth-noturn: not counted" "$(counted)" 0
rm -f "$DIR/paused-until"
# #1396: the CLI's login error as the result of a session that exited 1 after
# a turn, is_error false, pauses too: no API call ran (duration_api_ms 0), so
# the words are the CLI's.
echo login-result >"$FAKE"
LIMIT_WAIT=10m work
check "login-result #1396: paused" "$(test -e "$DIR/paused-until" && echo paused)" paused
check "login-result #1396: not counted" "$(counted)" 0
check "login-result #1396: the release names it" "$(cat "$T/released")" "session ended without a handoff (a limit: not logged in)"
rm -f "$DIR/paused-until"
# One whose API calls ran and whose final message quotes it pauses nothing (#655).
echo login-quoted >"$FAKE"
work
check "login-quoted #1396: not paused" "$(test -e "$DIR/paused-until" && echo paused)" ""
check "login-quoted #1396: counted" "$(counted)" 1
no_strikes
# So does the CLI's login error on stderr of a session that exited non-zero.
echo login-err >"$FAKE"
LIMIT_WAIT=10m work
check "login-err: paused" "$(test -e "$DIR/paused-until" && echo paused)" paused
check "login-err: not counted" "$(counted)" 0
rm -f "$DIR/paused-until"

# A relay started from a Claude Code session in VS Code gives its sessions none
# of that session's variables, and keeps the settings a shell sets (#645).
echo env >"$FAKE"
(export CLAUDECODE=1 CLAUDE_CODE_SESSION_ID=parent CLAUDE_CODE_CHILD_SESSION=1 \
  CLAUDE_CODE_SESSION_ATTENDED=1 CLAUDE_CODE_ENTRYPOINT=claude-vscode \
  CLAUDE_CODE_MESSAGING_SOCKET=/tmp/s CLAUDE_CODE_MESSAGING_TOKEN=x CLAUDE_EFFORT=xhigh \
  CLAUDE_PID=1 AI_AGENT=claude ELECTRON_RUN_AS_NODE=1 VSCODE_PID=1 VSCODE_IPC_HOOK_CLI=/tmp/v \
  GIT_ASKPASS=/home/u/.vscode-server/askpass.sh TERM_PROGRAM=vscode CLAUDE_CODE_OAUTH_TOKEN=kept \
  C4_HUMAN_CLUSTER=1
  work)
check "env: no Claude Code or VS Code session variables" \
  "$(grep -E '^(CLAUDECODE|CLAUDE_CODE_(SESSION_ID|CHILD_SESSION|SESSION_ATTENDED|ENTRYPOINT|MESSAGING_SOCKET|MESSAGING_TOKEN)|CLAUDE_EFFORT|CLAUDE_PID|AI_AGENT|ELECTRON_RUN_AS_NODE|VSCODE_[A-Z_]*|GIT_ASKPASS|TERM_PROGRAM)=' "$FAKE.env")" ""
check "env: no human cluster opt-in (#1128)" "$(grep -c '^C4_HUMAN_CLUSTER=' "$FAKE.env")" 0
check "env: keeps the Claude settings" "$(grep -cE "^(CLAUDE_CODE_OAUTH_TOKEN=kept|CLAUDE_CONFIG_DIR=$T/claude|C4_HEADLESS=1)$" "$FAKE.env")" 3
check "env: names the worker, for create --next (#1284)" "$(grep -c "^C4_RELAY_WORKER=$K$" "$FAKE.env")" 1
check "env #1371: OLDPWD is the worktree, not the main checkout" "$(grep '^OLDPWD=' "$FAKE.env")" "OLDPWD=$WT"
check "env #1371: cd - stays in the worktree" "$(cat "$FAKE.cd")" "$WT"
no_strikes

# A session that leaves the main checkout with uncommitted changes, which stop
# hack/pull.sh for every session on the device, is named in the log and its
# account; the files stay (#1371). Changes that were there before it started
# aren't its own.
echo stray >"$FAKE"
: >"$LOG"
work
check "stray #1371: logged with the path" "$(grep -c "session 1 left the main checkout $REPO with uncommitted changes, which stop hack/pull.sh: stray$" "$LOG")" 1
check "stray #1371: in the session's account" "$(cat "$DIR"/session-*.md | grep -c "this session left the main checkout $REPO with uncommitted changes: stray$")" 1
check "stray #1371: the file stays" "$(cat "$REPO/stray")" x
: >"$LOG"
work
check "stray #1371: what was there before isn't the session's" "$(grep -c 'left the main checkout' "$LOG")" 0
rm "$REPO/stray"
echo handoff >"$FAKE"
work
check "stray #1371: a clean main logs nothing" "$(grep -c 'left the main checkout' "$LOG")" 0
no_strikes

# Each session's TMPDIR is SCRATCH/<its worktree>, not the device's shared
# /tmp tmpfs (#1269) nor inside a git checkout, where git in a test's scratch
# would find the worktree (#1307): emptied before it starts and removed after
# it ends.
echo tmp >"$FAKE"
SCRATCH=$T/cache/c4factory/tmp
mkdir -p "$SCRATCH/wt" && echo old >"$SCRATCH/wt/left"
work
check "tmp #1269: TMPDIR is SCRATCH/<worktree>" "$(cat "$FAKE.tmpdir")" "$SCRATCH/wt"
check "tmp #1307: TMPDIR is in no git checkout" "$(cat "$FAKE.repo")" ""
check "tmp #1269: a dead session's scratch is gone before the next starts" "$(cat "$FAKE.left")" gone
check "tmp #1269: mktemp -d lands in it" "$(case $(cat "$FAKE.scratch") in "$SCRATCH/wt/"?*) echo in ;; esac)" in
check "tmp #1269: removed after the session, read-only dirs too" "$(test -e "$SCRATCH/wt" && echo left)" ""
check "tmp #1269: nothing logged" "$(grep -c 'could not remove' "$LOG")" 0
no_strikes

# Before a session, a worktree's .dev/ of its own, the caches make check left
# there before #1132, becomes the link to the main checkout's (#1246).
(
  REPO=$T/main WT=$T/wt-dev
  mkdir -p "$REPO/.dev/cache" "$WT/.dev/cache/go-build" && head -c 300000 /dev/zero >"$WT/.dev/cache/go-build/x"
  mkdir "$WT/.dev/cache/go-build/ro" && chmod a-w "$WT/.dev/cache/go-build/ro"
  share_dev
  check "share_dev #1246: a .dev/ of its own becomes the link" "$(readlink "$WT/.dev")" "$REPO/.dev"
  check "share_dev #1246: the main checkout's .dev/ stays" "$(test -d "$REPO/.dev/cache" && echo kept)" kept
  check "share_dev #1246: logs what it freed" "$(grep -c "freed [0-9.]*[KM] in $WT/.dev, a .dev/ of its own; linked it to $REPO/.dev" "$LOG")" 1
  : >"$LOG"
  share_dev
  check "share_dev #1246: an already linked one stays" "$(readlink "$WT/.dev")" "$REPO/.dev"
  check "share_dev #1246: an already linked one logs nothing" "$(cat "$LOG")" ""
  rm "$WT/.dev"
  share_dev
  check "share_dev #1246: a worktree without .dev/ gets the link" "$(readlink "$WT/.dev")" "$REPO/.dev"
  rm "$WT/.dev" && mkdir "$WT/.dev" && rm -r "$REPO/.dev"
  share_dev
  check "share_dev #1246: no main .dev/, nothing changes" "$(test -d "$WT/.dev" && ! test -L "$WT/.dev" && echo kept)" kept
)

# Failed sessions are strikes, comments on the issue: a handoff or a commit
# resets the count; the third escalates the issue to the maintainer, with one
# comment linking each strike.
echo fail >"$FAKE"
work; work
check "fails: two counted" "$(counted)" 2
check "fails: not yet the maintainer's" "$(cat "$T/status" 2>/dev/null)" ""
grep -q "^\*\*Strike:\*\* session .* exit 0 after 0m" "$T/comment"; check "fails: a strike names its session" $? 0
check "fails: a strike has its stderr and account" "$(grep -c '^boom \|^gave up$' "$T/comment")" 2
echo handoff >"$FAKE"
pick() { ISSUE=175 TITLE=t URL=u HOW=next; }
work
pick() { ISSUE=175 TITLE=t URL=u HOW=ready; }
check "next: the log names the issue a --next reserved (#1284)" "$(grep -c 'session 1 start: id .*, otter-175 on #175 (next) t' "$LOG")" 1
check "fails: a handoff resets" "$(counted)" 0
check "fails: the reset is a comment" "$(head -1 "$T/comment")" "$STRIKE_RESET_MARKER"
echo fail >"$FAKE"
work
echo commit >"$FAKE"
work
check "fails: a commit resets" "$(counted)" 0
n=$(jq length "$COMMENTS")
echo commit >"$FAKE"
work
check "fails: nothing to reset, no comment" "$(jq length "$COMMENTS")" "$n"
# A commit on <branch>-2, or a push to <branch>-<name> after a takeover, is a
# commit too (#1122).
echo fail >"$FAKE"
work
echo commit-2 >"$FAKE"
work
check "fails: a commit on b-2 resets" "$(counted)" 0
grep -q "committed on b or a copy of it" "$LOG"; check "fails: b-2 logged as a commit" $? 0
echo fail >"$FAKE"
work
echo push-name >"$FAKE"
work
check "fails: a push to b-otter-175 resets" "$(counted)" 0
# A plan's parent left status/waiting is a success, like a handoff; one set
# back to ready isn't a strike, and leaves the count as it was (#1122).
echo fail >"$FAKE"
work
echo waiting >"$FAKE"
work
check "fails: waiting resets" "$(counted)" 0
grep -q "session 1 left #175 waiting on its steps" "$LOG"; check "fails: waiting logged" $? 0
echo 'open status/in-progress' >"$STATE"
echo fail >"$FAKE"
work
echo ready >"$FAKE"
work
check "fails: ready is no strike" "$(counted)" 1
grep -q "session 1 set #175 back to ready or proposed without a handoff; not counted" "$LOG"; check "fails: ready logged" $? 0
echo 'open status/in-progress' >"$STATE"
no_strikes
: >"$LOG"
echo fail >"$FAKE"
work; work
check "fails: two, still not the maintainer's" "$(cat "$T/status" 2>/dev/null)" ""
work
check "fails: the third sets needs-maintainer" "$(cat "$T/status")" "175 needs-maintainer"
check "fails: count cleared" "$(counted)" 0
check "fails: the comment links each strike" "$(grep -c '^- https://github.com/x/issues/175#issuecomment-' "$T/comment")" 3
url=$(jq -r 'last.html_url' "$COMMENTS")
grep -q "#175 reached 3 strikes; set it to status/needs-maintainer: $url" "$LOG"
check "fails: logged" $? 0

# Strikes add up across devices, read from the issue; one older than
# RELAY_STRIKE_TTL doesn't count; a handoff posted anywhere resets them.
no_strikes; rm -f "$T/status"
add_comment "$STRIKE_MARKER
**Strike:** session \`s1\` on host \`other\`" $((25 * 3600))
add_comment "$STRIKE_MARKER
**Strike:** session \`s2\` on host \`other\`" 600
add_comment "$STRIKE_MARKER
**Strike:** session \`s3\` on host \`other\`" 300
check "strikes: one older than 24h doesn't count" "$(counted)" 2
add_comment "$GH_HANDOFF_MARKER
handed off elsewhere" 200
check "strikes: a handoff elsewhere resets" "$(counted)" 0
add_comment "$STRIKE_MARKER
**Strike:** session \`s4\` on host \`other\`" 100
add_comment "$STRIKE_MARKER
**Strike:** session \`s5\` on host \`other\`" 50
echo fail >"$FAKE"
work
check "strikes: another device's add up" "$(cat "$T/status")" "175 needs-maintainer"
check "strikes: the comment links theirs" "$(grep -c '^- ' "$T/comment")" 3
check "strikes: an old one out" "$(grep -c 'issuecomment-1$' "$T/comment")" 0

# A failed status swap keeps the posted comment and retries the swap alone
# after the next failed session, with no new comment.
no_strikes; rm -f "$T/status"
gh_set_status() { return 1; }
: >"$LOG"
work; work; work
check "swap fails: not the maintainer's" "$(cat "$T/status" 2>/dev/null)" ""
check "swap fails: one escalation comment" "$(jq --arg e "$STRIKE_ESCALATED_MARKER" '[.[] | select(.body | startswith($e))] | length' "$COMMENTS")" 1
grep -q "setting it to status/needs-maintainer failed; trying again after the next failed session" "$LOG"
check "swap fails: logged" $? 0
n=$(jq length "$COMMENTS")
gh_set_status() { set_status "$@"; }
work
check "swap retried: the maintainer's" "$(cat "$T/status")" "175 needs-maintainer"
check "swap retried: no new comment" "$(jq length "$COMMENTS")" "$n"
# Swapped, then set back to ready by the maintainer: a failed session is a
# strike again, not another swap.
rm -f "$T/status"
work
check "swap done: a strike, not a swap" "$(cat "$T/status" 2>/dev/null) $(counted)" " 1"

# #1213: device A posts the escalation and its swap fails; device B, with
# nothing of A's in .relay, fails a session on #175 and retries the swap
# alone: it sets needs-maintainer and posts nothing.
no_strikes; rm -f "$T/status"
for i in 1 2 3; do add_comment "$STRIKE_MARKER
**Strike:** session \`a$i\` on host \`a\`" $((400 - i))
done
add_comment "$STRIKE_ESCALATED_MARKER
The relay stopped working #175" 300
n=$(jq length "$COMMENTS")
: >"$LOG"
work
check "swap elsewhere #1213: B sets needs-maintainer" "$(cat "$T/status" 2>/dev/null)" "175 needs-maintainer"
check "swap elsewhere #1213: B posts nothing" "$(jq length "$COMMENTS")" "$n"
grep -q "#175 reached 3 strikes; set it to status/needs-maintainer: https://github.com/x/issues/175#issuecomment-4" "$LOG"
check "swap elsewhere #1213: logged" $? 0

# A session whose transcript stops growing is ended and counted as failed; one
# that keeps writing, or writes none, runs on.
IDLE=2s IDLE_KILL=2s IDLE_POLL=1
: >"$LOG"
no_strikes; rm -f "$T/status"
echo hang >"$FAKE"
t0=$(date +%s)
work
check "idle: ended early" "$(($(date +%s) - t0 < 20))" 1
grep -q "session 1 stopped: its transcript was idle for 2s$" "$LOG"; check "idle: logged" $? 0
check "idle: counted" "$(counted)" 1
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
check "seconds: 45m" "$(gh_seconds 45m RELAY_IDLE)" 2700
check "seconds: 1d" "$(gh_seconds 1d RELAY_IDLE)" 86400
gh_seconds 1.5h RELAY_IDLE >/dev/null 2>&1; check "seconds: 1.5h refused" $? 1
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
rm -f "$T/takes" "$DIR/admitted"; no_strikes
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

# The maintainer lease (#526), on a fake GitHub that keeps the report issue's
# (#7) comments in $LEASES. lease HOST ID AGE: a lease comment of HOST, edited
# AGE seconds ago.
export LEASES=$T/leases.json
echo '[]' >"$LEASES"
gh_token() { :; }
gh_api() {
  local url= a m=GET prev= id
  for a in "$@"; do
    case $a in repos/*) url=${a#repos/x/} ;; esac
    [ "$prev" = --method ] && m=$a
    prev=$a
  done
  case "$m $url" in
    "GET issues?state=open&labels=maintainer%2Freport"*) echo 7 ;;
    "GET issues/7/comments"*) jq -c '[.]' "$LEASES" ;;
    "POST issues/7/comments")
      id=$(($(cat "$T/next-id" 2>/dev/null || echo 10) + 1))
      echo "$id" >"$T/next-id"
      jq --argjson id "$id" --arg b "$(jq -r .body)" --arg t "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
        '. + [{id: $id, created_at: $t, updated_at: $t, html_url: "u", user: {login: "x"}, body: $b}]' "$LEASES" >"$LEASES.new" &&
        mv "$LEASES.new" "$LEASES"
      echo "$id"
      # A second host posted at the same moment, a little earlier.
      if [ -e "$T/race" ]; then rm "$T/race"; lease raced 5 0; fi ;;
    "PATCH issues/comments/"*)
      jq -e --argjson id "${url##*/}" 'any(.id == $id)' "$LEASES" >/dev/null || { echo "HTTP 404" >&2; return 1; }
      echo PATCH >>"$T/beats"
      jq --argjson id "${url##*/}" --arg b "$(jq -r .body)" --arg t "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
        'map(if .id == $id then .body = $b | .updated_at = $t else . end)' "$LEASES" >"$LEASES.new" && mv "$LEASES.new" "$LEASES" ;;
    "DELETE issues/comments/"*)
      jq -e --argjson id "${url##*/}" 'any(.id == $id)' "$LEASES" >/dev/null || { echo "HTTP 404" >&2; return 1; }
      jq --argjson id "${url##*/}" 'map(select(.id != $id))' "$LEASES" >"$LEASES.new" && mv "$LEASES.new" "$LEASES" ;;
    *) echo "unfaked: $m $url" >&2; return 1 ;;
  esac
}
lease() {
  jq --arg h "$1" --argjson id "$2" --arg t "$(date -u -d "@$(($(date +%s) - $3))" +%Y-%m-%dT%H:%M:%SZ)" \
    '. + [{id: $id, created_at: $t, updated_at: $t, html_url: "u", user: {login: "x"},
      body: "<!-- c4-maintainer -->\n**maintainer** sweeps · host `\($h)` · pid `42`\nHeartbeat: x"}]' \
    "$LEASES" >"$LEASES.new" && mv "$LEASES.new" "$LEASES"
}
C4_GITHUB_REPO=x C4_HOST=here C4_LEASE_TTL=1h GH_CLAIM_SETTLE=0
git -C "$WT" update-ref refs/remotes/origin/main HEAD
gh_activity() { echo "2026-10-06T08:00:00Z abc123"; }
rm -f "$DIR/paused-until" "$DIR/maintained"
# Another device holds a live lease: nothing starts, and the log names it.
lease there 10 300 # the fake numbers the comments it is posted from 11
: >"$LOG"
echo fail >"$FAKE"
ADMIT_GAP=0 maintain
check "lease: held elsewhere, no sweep" "$(grep -c 'maintainer start' "$LOG")" 0
grep -q "another device's maintainer holds the lease: host there (pid 42), heartbeat 5m ago; no sweep" "$LOG"
check "lease: held elsewhere, the holder logged" $? 0
check "lease: held elsewhere, theirs kept and nothing recorded" "$(jq -c 'map(.id)' "$LEASES") $(test -e "$DIR/maintained" && echo recorded)" "[10] "
# A stale lease is taken over, the lease is beaten while the session runs and
# released when it ends.
echo '[]' >"$LEASES"
lease there 10 7200
rm -f "$T/beats"
echo lease >"$FAKE"
GH_BEAT_EVERY=1 ADMIT_GAP=0 maintain
check "lease: a stale one taken over" "$(grep -c 'maintainer start: .*, lease 11$' "$LOG")" 1
check "lease: held during the session, the stale one gone" "$(jq -c 'map({id, host: (.body | capture("host `(?<h>[^`]+)`").h)})' "$LEASES.during")" '[{"id":11,"host":"here"}]'
check "lease: beaten while the session runs" "$(test -s "$T/beats" && echo yes)" yes
check "lease: released when the session ends" "$(jq -c . "$LEASES") $(grep -c 'released the maintainer lease 11' "$LOG")" "[] 1"
# A live lease of this host is a crashed maintainer's: taken over.
lease here 20 60
echo 20 >"$T/next-id"
: >"$LOG"
echo fail >"$FAKE"
ADMIT_GAP=0 maintain
check "lease: this host's crashed one taken over" "$(grep -c 'maintainer start: .*, lease 21$' "$LOG") $(jq -c . "$LEASES")" "1 []"
# Two hosts post at once: the older lease wins and the other starts nothing.
: >"$LOG"
touch "$T/race"
ADMIT_GAP=0 maintain
check "lease: a lost race, no sweep" "$(grep -c 'maintainer start' "$LOG")" 0
grep -q "another device's maintainer holds the lease: host raced (pid 42), heartbeat 0m ago; no sweep" "$LOG"
check "lease: a lost race, the winner logged and ours deleted" "$? $(jq -c 'map(.id)' "$LEASES")" "0 [5]"
echo '[]' >"$LEASES"

# A maintainer sweep, with claude faked, on origin/main: it records the
# activity after it; one cut short by a limit or a crash records nothing, so
# the next one still looks back to the last full sweep.
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

# The relay runs origin/main's code whatever branch the checkout it was started
# from has (#1126). A scratch origin whose main has the relay's scripts, and a
# clone on a feature branch that edits them: the worker moves to relay-code at
# its first pull, a worktree at origin/main, and the restarted worker runs
# main's scripts there, with its state still in the clone; from relay-code it
# restarts only when origin/main's code changes, and an edit made there is
# undone. update 0 restarts it with no sessions left, so the real worker
# starts, logs and finishes without asking GitHub.
H=$(cd "$(dirname "$0")" && pwd)
G=(env GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1 GIT_AUTHOR_NAME=t GIT_AUTHOR_EMAIL=t@t
  GIT_COMMITTER_NAME=t GIT_COMMITTER_EMAIL=t@t git)
"${G[@]}" init -q -b main "$T/code-src"
mkdir -p "$T/code-src/hack/lib"
cp "$H/relay.sh" "$H/pull.sh" "$T/code-src/hack/"
cp "$H/lib/github.sh" "$H/lib/session-env.sh" "$H/lib/relay-code.sh" "$T/code-src/hack/lib/"
"${G[@]}" -C "$T/code-src" add -A && "${G[@]}" -C "$T/code-src" commit -qm main
"${G[@]}" clone -q --bare "$T/code-src" "$T/code-origin.git"
"${G[@]}" -C "$T/code-src" remote add origin "$T/code-origin.git"
C=$T/checkout
"${G[@]}" clone -q "$T/code-origin.git" "$C"
"${G[@]}" -C "$C" switch -qc feature
echo '# the feature branch' >>"$C/hack/relay.sh"
echo '# the feature branch' >>"$C/hack/lib/github.sh"
"${G[@]}" -C "$C" commit -qam feature
RC=$C/.claude/worktrees/relay-code
# move SUM: run the real update 0 (the sessions' tests above fake it) as
# worker 1 started from CODE_AT (default the clone) with the code checksum SUM
# (default CODE_AT's), holding its lock as the real worker does; prints
# "stayed" when it didn't restart.
move() {
  (
    sum=${1:-}
    set --
    export C4_RELAY_CHILD=1 C4_RELAY_WORKER=1 GH_TOKEN=x
    # shellcheck source=hack/relay.sh
    . "$H/relay.sh"
    CODE=${CODE_AT:-$C} REPO=$C DIR=$C/.relay STOP=$C/.relay/stop LOG=$C/.relay/relay-1.log K=1 PULL=1
    CODE_SUM=${sum:-$(code_sum)}
    mkdir -p "$DIR"
    exec 9>"$DIR/lock-1"
    flock -n 9 || exit 1
    update 0
    echo stayed
  ) 2>>"$T/move.err"
}
main=$(git -C "$T/code-src" rev-parse --short HEAD)
check "relay code: from a branch, restarts" "$(move)" ""
check "relay code: relay-code at origin/main" "$(git -C "$RC" rev-parse --short HEAD)" "$main"
check "relay code: relay-code has main's scripts, not the branch's" \
  "$(cmp -s "$RC/hack/relay.sh" "$H/relay.sh" && cmp -s "$RC/hack/lib/github.sh" "$H/lib/github.sh" && echo main)" main
check "relay code: the clone stays on its branch, edits and all" \
  "$(git -C "$C" rev-parse --abbrev-ref HEAD) $(grep -c 'the feature branch' "$C/hack/relay.sh")" "feature 1"
check "relay code: says it moves" \
  "$(grep -c "moving to origin/main's code ($RC at $main) from $C; restarting with 0 sessions left" "$C/.relay/relay-1.log")" 1
check "relay code: the restarted worker runs relay-code's code, its state in the clone" \
  "$(grep -c "worker 1 started: .*, worktree $C/.claude/worktrees/relay-1, code $RC at $main, at most 0 sessions" "$C/.relay/relay-1.log")" 1
check "relay code: the restarted worker finishes" "$(grep -c 'worker 1 finished' "$C/.relay/relay-1.log")" 1
: >"$C/.relay/relay-1.log"
CODE_AT=$RC
check "relay code: in relay-code, nothing new on main: stays" "$(move)" stayed
sum=$(CODE=$RC code_sum)
echo '# an edit by hand' >>"$RC/hack/relay.sh"
check "relay code: an edit in relay-code is undone, no restart" \
  "$(move "$sum") $(grep -c 'an edit by hand' "$RC/hack/relay.sh")" "stayed 0"
echo '# a fix on main' >>"$T/code-src/hack/relay.sh"
"${G[@]}" -C "$T/code-src" commit -qam fix
"${G[@]}" -C "$T/code-src" push -q origin main
main=$(git -C "$T/code-src" rev-parse --short HEAD)
check "relay code: main's code changed: restarts" "$(move "$sum")" ""
check "relay code: relay-code moved to the new main" "$(git -C "$RC" rev-parse --short HEAD)" "$main"
check "relay code: says why" \
  "$(grep -c "the relay's code changed ($RC at $main); restarting with 0 sessions left" "$C/.relay/relay-1.log")" 1
check "relay code: the restarted worker runs the new main" \
  "$(grep -c "worker 1 started: .*, code $RC at $main," "$C/.relay/relay-1.log")" 1
check "relay code: nothing on stderr" "$(cat "$T/move.err")" ""
unset CODE_AT

exit $fail
