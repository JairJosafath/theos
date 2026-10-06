#!/usr/bin/env bash
# Tests for hack/relay-watch.sh: when a round starts the relay, and when it
# starts a triage of the proposed issues, with pick, the proposed list and
# hack/relay.sh faked; --install and --uninstall with systemctl, loginctl and
# crontab faked; and the stop file and the lock in a scratch copy of the watch,
# never this checkout's .relay/.
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
  -l) [ -e "$FAKE/crontab" ] || { echo "no crontab for you" >&2; exit 1; }; cat "$FAKE/crontab" ;;
  -) t=$(cat); printf "%s\n" "$t" >"$FAKE/crontab" ;;
esac
SH
chmod +x "$T/bin/"*
SAVED_PATH=$PATH SAVED_HOME=$HOME
export FAKE=$T PATH="$T/bin:$PATH" HOME=$T/home
STOP=$DIR/stop REPO=$T/repo WORKERS=3
mkdir -p "$REPO/.relay"
touch "$STOP"
RELAY_MAX_WORKERS=5 install_watch >/dev/null
unit=$T/home/.config/systemd/user/c4factory-relay-watch.service
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
uninstall_watch >/dev/null
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
S=$T/scratch
mkdir -p "$S/hack" "$S/.relay"
cp "$(dirname "$0")/relay-watch.sh" "$S/hack/"
touch "$S/.relay/stop"
bash "$S/hack/relay-watch.sh" --cron
check "cron start with the stop file: nothing" "$([ -e "$S/.relay/watch.log" ] && echo started)" ""
rm "$S/.relay/stop"
flock "$S/.relay/watch.lock" sleep 2 &
until ! flock -n "$S/.relay/watch.lock" true; do sleep 0.1; done
WATCH_EVERY=1 WATCH_PULL_EVERY=999 bash "$S/hack/relay-watch.sh" --foreground 2>/dev/null &
fg=$!
sleep 1
check "foreground: waits for the lock" "$(grep -c 'another watch holds the lock; pid .* waits' "$S/.relay/watch.log")" 1
check "foreground: hasn't started" "$(grep -c 'watch started' "$S/.relay/watch.log")" 0
wait %flock 2>/dev/null
for _ in $(seq 30); do grep -q 'watch started' "$S/.relay/watch.log" && break; sleep 0.1; done
check "foreground: takes over" "$(grep -c 'watch started: pid .*, under systemd' "$S/.relay/watch.log")" 1
touch "$S/.relay/stop"
wait "$fg"
check "foreground: exits 0 for the stop file" $? 0
check "foreground: says why" "$(grep -c 'stop file found; stopping' "$S/.relay/watch.log")" 1

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
