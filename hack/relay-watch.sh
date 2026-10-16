#!/usr/bin/env bash
# Relay watch: keeps the relay ready for new work. hack/relay.sh works the
# issues that are ready and stops when there are none, so an issue marked ready
# or handed back later waits until someone starts the relay again. The watch
# stays up instead: every WATCH_EVERY seconds (default 60) it asks GitHub for
# the next issue (hack/lib/github.sh pick, which changes nothing) and, when
# there is one and a worker is free, runs hack/relay.sh --workers N, which
# starts the workers that aren't running and leaves the others alone. Asking
# pick first keeps the relay's logs free of a worker that starts, finds nothing
# and stops every minute.
#
# When pick names the same issue again within
# WATCH_RETRY seconds (default 600), nobody claimed it, so the relay stopped
# without it or hack/relay.sh failed: the watch starts it again for that issue
# once the time has passed, not every minute.
#
# Each round, busy or idle, ready issues or not (#1191), when open issues wait
# in status/proposed, the watch may start one triage session (hack/relay.sh
# --triage, in its own worktree, .claude/worktrees/relay-triage, so it needs no
# free worker): it reads each proposed issue against docs/design.md and the
# open issues, readies the sound ones, comments on the others what they lack
# and closes duplicates. It takes no issue and changes no code. Triage runs
# again, at most every WATCH_TRIAGE seconds (default 600) and never while one
# runs (.relay/triage.lock), when a proposed issue it hasn't seen appears, when
# one changed after it (its body, title, labels or a comment that isn't the
# triage's own), when one waits on an issue that closed after it (Depends on
# #M, or an #M in the triage's comment), and in any case after WATCH_RETRIAGE
# seconds (default 10800, 3h); .relay/triaged keeps its time and the issues it
# saw across restarts. Once WATCH_TRIAGE has passed, it asks GitHub for the
# proposed issues at most every WATCH_RETRY, not every round: the ask reads
# every open issue and the comments since the last triage.
# status/needs-maintainer is the maintainer's and status/needs-human stays the
# owner's.
#
# On an idle round it also logs, once per change, what holds back the issues
# pick skips for an open "Depends on #M" when nobody works #M (hack/lib/github.sh
# blocked --unworked), and reports issues that wait on each other in a cycle:
# a comment on each, the newest to status/needs-maintainer (cycles --report).
#
# Every WATCH_PARENTS seconds (default 600; 0 turns it off), busy or idle, it
# tends the plans' parents (hack/lib/github.sh parents --apply): one whose
# steps all closed is closed, one in progress that waits on its steps goes to
# status/waiting. close and the Issue closed workflow do it as each step
# closes; the watch catches the ones they missed, while the relay was down.
#
# Every WATCH_CI_EVERY seconds (default 900) it checks main's CI: each
# workflow in .github/workflows that runs on a push to main and whose two
# newest finished runs on main failed (hack/lib/github.sh red) gets an issue
# titled "main is red: <workflow>", status/ready and priority/1, so pick
# takes it first. hack/lib/github.sh create skips a title that is open, so a
# red main that lasts, or both devices' watches, file one issue; a single
# failure files nothing. The session that fixes main closes it. When GitHub
# started none of the newest failed run's jobs (no steps, an annotation saying
# the job was not started: failed payments, a spending limit), no code is at
# fault: the watch files one "GitHub Actions starts no jobs" issue in
# status/needs-maintainer, with the annotation, and no "main is red", once per
# outage (.relay/ci-stopped), even when it is closed as a duplicate.
#
# In the same round it records each Ginkgo spec that failed on main
# (hack/lib/github.sh flakes) in .relay/flakes.tsv, and files a "Flaky: <spec>"
# bug, status/ready and priority/3, not 1, when one failed in two
# runs within WATCH_FLAKE_DAYS days (default 7): a flaky spec rarely fails
# twice in a row, so it never turns main red. An open one is not filed again.
#
# In the same round it notices runs on main that wait for a runner: a queued
# run never fails, so red sees nothing while the device's self-hosted runners
# are down (Docker Desktop stopped). When a run on main has had a job queued
# for more than WATCH_QUEUED minutes (default 30; hack/lib/github.sh queued),
# the watch puts a note under the Maintainer report's Health and Needs you
# (hack/lib/github.sh report-note runners), one line each, kept current while
# it lasts (.relay/runners-offline), and removes them in the first round in
# which no run waits that long. With no runner of the repository online the
# note says "Runners offline since T: N runs queued" and asks for Docker
# Desktop; with some online, "N runs queued since T with M runners online":
# they are busy, or none has the labels the jobs ask for. A run GitHub made
# no jobs for waits for good, as no runner can take it: the watch logs it once
# and never notes it (#1236).
#
# In the same round it notes when /tmp on this device is at least
# WATCH_TMP_FULL percent full (default 90), naming its three largest entries,
# under the Maintainer report's Health only (hack/lib/github.sh report-note
# tmp-<host>, with no Needs you line), kept current while it lasts
# (.relay/tmp-full), and removes it when /tmp drops back. Relay sessions keep
# their scratch in ~/.cache/c4factory/tmp (#1269, #1307), so what fills /tmp is
# someone else's.
#
# Each pass, busy or idle, it may start the maintainer (hack/relay.sh
# --maintain), which acts as the project's maintainer on the owner's behalf and
# keeps the pinned Maintainer report: when none runs (.relay/maintain.lock),
# the last sweep (.relay/maintained) is WATCH_MAINTAIN seconds old (default
# 7200, 2h; 0 turns it off), and hack/lib/github.sh activity differs from the
# one that sweep recorded, so quiet nights cost nothing. Agents escalate to the
# maintainer with status/needs-maintainer: when an issue got that label after
# the last sweep started (hack/lib/github.sh escalated), the watch starts it at
# once, however recent that sweep. One the sweep looked at and left there
# waits for the next regular sweep, or for an agent to escalate it again. It asks at most every
# WATCH_RETRY, and a sweep that doesn't run to its end is tried again then.
#
# While the relay is paused for a usage limit or a failed login
# (.relay/paused-until, which hack/relay.sh writes), the watch starts nothing,
# not even a triage or the maintainer, and logs the pause once.
#
# Every 5 minutes it runs hack/pull.sh, so pick never reads stale rules, moves
# the worktree .claude/worktrees/relay-code to origin/main, and restarts itself
# on relay-code's copy when it runs from elsewhere or when that changed this
# script: the watch, and the relay it starts, run origin/main's code whatever
# branch this checkout has, and keep their state (.relay/) here (#1126,
# hack/lib/relay-code.sh). RELAY_PULL=0 keeps it on the code where it was
# started, to try relay changes from a branch. Every WATCH_TRIM_EVERY seconds
# (default 1800) it runs hack/trim-go-cache.sh: nine workers' builds grow Go's
# build cache by gigabytes an hour, faster than opening VS Code trims it.
#
#   hack/relay-watch.sh                start the watch, detached, for one worker
#   hack/relay-watch.sh --workers N    for N workers (1-99); on the cluster device RELAY_MAX_WORKERS (default 9)
#                                      caps them, and hack/relay.sh admits each session by memory
#   hack/relay-watch.sh --install [--workers N]
#                                      keep the watch up: a systemd user unit (hack/relay-watch.service)
#                                      restarts it after a crash, a kill or a reboot; without systemd, a
#                                      crontab line starts it at boot and every 5 minutes
#   hack/relay-watch.sh --uninstall    remove the unit or the crontab lines
#   touch .relay/stop                  stop the watch, and every worker after its current session; an
#                                      installed watch stays down until hack/relay-watch.sh starts the unit
#
# The unit runs the watch with --foreground: in its own process, waiting for the
# lock while a watch started by hand holds it. The crontab line runs it with
# --cron: started detached unless a watch runs or .relay/stop is there.
#
# At start, the watch logs a warning when it lacks one of the user's groups in
# /etc/group, and --install prints it when the systemd user manager does: a
# manager that started before the user joined docker passes its groups to the
# watch, the workers and their sessions, which then can't reach Docker.
#
# Log: .relay/watch.log, one line per change; the workers log as usual, to
# .relay/relay-<k>.log.
set -uo pipefail

CODE=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
REPO=${C4_RELAY_REPO:-$CODE}
unset C4_RELAY_REPO
# shellcheck source=hack/lib/session-env.sh
. "$CODE/hack/lib/session-env.sh"
# shellcheck source=hack/lib/relay-code.sh
. "$CODE/hack/lib/relay-code.sh"
DIR="$REPO/.relay"
STOP="$DIR/stop"
LOG="$DIR/watch.log"
EVERY=${WATCH_EVERY:-60}
RETRY=${WATCH_RETRY:-600}
TRIAGE_EVERY=${WATCH_TRIAGE:-600}
RETRIAGE=${WATCH_RETRIAGE:-10800}
PULL_EVERY=${WATCH_PULL_EVERY:-300}
CI_EVERY=${WATCH_CI_EVERY:-900}
TRIM_EVERY=${WATCH_TRIM_EVERY:-1800}
MAINTAIN_EVERY=${WATCH_MAINTAIN:-7200}
PARENTS_EVERY=${WATCH_PARENTS:-600}
FLAKE_DAYS=${WATCH_FLAKE_DAYS:-7}
QUEUED_MINUTES=${WATCH_QUEUED:-30}
TMP_FULL=${WATCH_TMP_FULL:-90}
# The report is every device's: each device's /tmp has its own line.
TMP_HOST=$(hostname -s)
TMP_KEY=tmp-$TMP_HOST
PULL=${RELAY_PULL:-1}

WORKERS=1 MODE=start
while [ $# -gt 0 ]; do
  case $1 in
    --workers) WORKERS=${2:-}; shift 2 || shift ;;
    --workers=*) WORKERS=${1#*=}; shift ;;
    --install | --uninstall | --foreground | --cron) MODE=${1#--}; shift ;;
    *) echo "usage: hack/relay-watch.sh [--install | --uninstall] [--workers N]" >&2; exit 64 ;;
  esac
done
[[ $WORKERS =~ ^[1-9][0-9]?$ ]] || { echo "relay-watch: --workers takes 1 to 99" >&2; exit 64; }

log() { echo "$(date -Is) $*" >>"$LOG"; }

# running K: whether relay worker K runs (it holds its lock).
running() { [ -e "$DIR/lock-$1" ] && ! flock -n "$DIR/lock-$1" true; }

# worker_free: whether one of the watch's workers doesn't run.
worker_free() {
  local k
  for k in $(seq 1 "$WORKERS"); do running "$k" || return 0; done
  return 1
}

# maintaining: whether the maintainer runs (it holds its lock).
maintaining() { [ -e "$DIR/maintain.lock" ] && ! flock -n "$DIR/maintain.lock" true; }

# triaging: whether a triage session runs (it holds its lock).
triaging() { [ -e "$DIR/triage.lock" ] && ! flock -n "$DIR/triage.lock" true; }

# pick_next, proposed, blocked, cycles, parents, activity, escalated, run_relay,
# run_triage, run_maintain, trim_caches, report_note, tmp_use and tmp_largest:
# what the watch asks and starts (faked in the tests).
# pick_next counts every worker's reservations (create --next, .relay/next in
# its worktree) as its own, so an issue a worker reserved starts the relay
# after that worker stopped; only that worker's pick takes it first.
pick_next() { GH_NEXT_FILES=$(printf '%s\n' "$REPO"/.claude/worktrees/relay-[0-9]*/.relay/next) github pick; }
blocked() { github blocked --unworked; }
cycles() { github cycles --report; }
parents() { github parents --apply; }
proposed() { github proposed; }
run_relay() { C4_RELAY_REPO=$REPO "$CODE/hack/relay.sh" --workers "$WORKERS"; }
run_triage() { C4_RELAY_REPO=$REPO "$CODE/hack/relay.sh" --triage; }
activity() { github activity; }
escalated() { github escalated; }
run_maintain() { C4_RELAY_REPO=$REPO "$CODE/hack/relay.sh" --maintain; }
trim_caches() { "$CODE/hack/trim-go-cache.sh"; }
report_note() { github report-note "$@"; }
# tmp_use: the percent of /tmp's filesystem in use, a bare number.
tmp_use() { df -P /tmp | awk 'NR == 2 { sub(/%/, "", $5); print $5 }'; }
# tmp_largest: /tmp's three largest entries, SIZE<tab>PATH, largest first.
tmp_largest() { du -sxh /tmp/* /tmp/.[!.]* 2>/dev/null | sort -rh | head -n 3; }

# trim NOW: trim the build caches when the last trim is TRIM_EVERY seconds old,
# and log what it dropped (it says nothing when a cache is under its limit).
TRIM_T=0
trim() {
  local out
  [ $(($1 - TRIM_T)) -ge "$TRIM_EVERY" ] || return 0
  TRIM_T=$1
  out=$(trim_caches 2>&1 8>&-)
  [ -n "$out" ] && log "${out//$'\n'/; }"
  return 0
}

# tend NOW: when the last look is PARENTS_EVERY seconds old, close the plans'
# parents whose steps all closed and mark the waiting ones (parents --apply),
# logging what it did; a failure is logged once until it works again.
TEND_T=0 TEND_ERR=
tend() {
  local out rc
  [ "$PARENTS_EVERY" -gt 0 ] && [ $(($1 - TEND_T)) -ge "$PARENTS_EVERY" ] || return 0
  TEND_T=$1
  out=$(parents 2>&1 8>&-)
  rc=$?
  if [ "$rc" != 0 ]; then
    [ -n "$TEND_ERR" ] || log "could not tend the plans' parents (parents --apply, exit $rc): ${out//$'\n'/; }"
    TEND_ERR=1
    return 0
  fi
  TEND_ERR=
  out=$(grep -Ev '^#[0-9]+ waits on ' <<<"$out")
  [ -z "$out" ] || log "plans' parents: ${out//$'\n'/; }"
}

# github: hack/lib/github.sh, keeping its local state in this checkout's .relay/.
github() { GH_PENDING_DIR=$DIR "$CODE/hack/lib/github.sh" "$@"; }

# ci_workflows: FILE<tab>NAME of each workflow that runs on a push to main.
ci_workflows() {
  local f
  for f in "$CODE"/.github/workflows/*.yml "$CODE"/.github/workflows/*.yaml; do
    [ -f "$f" ] || continue
    awk -v file="${f##*/}" '
      /^name:/ { n = $0; sub(/^name:[ \t]*/, "", n); gsub(/["\047]/, "", n) }
      /^on:/ { on = 1; next }
      on && /^[^ \t#]/ { on = 0 }
      on && /^  [a-z_]+:/ { push = ($0 ~ /^  push:/); br = 0; next }
      on && push && /^    [a-z_-]+:/ { br = ($0 ~ /^    branches:/) }
      on && push && br && (/[[ ,]main[],]/ || /^ +- *[\047"]?main[\047"]? *$/) { found = 1 }
      END { if (found) print file "\t" (n == "" ? file : n) }
    ' "$f"
  done
}

# ci_round: file a priority/1 issue for each workflow that is red on main. When
# GitHub started none of a workflow's jobs (red exits 3), the code isn't at
# fault: it files one "GitHub Actions starts no jobs" issue for the maintainer
# instead, which every such workflow shares. CI_SAID holds the workflows whose
# state was logged, so a state that lasts is logged once.
#
# The shared issue's number is kept in $DIR/ci-stopped from the round that
# files it until a round in which every workflow's state was read and none
# had unstarted jobs (the outage is over); while it is there, no round files
# the issue again. The file, not GitHub, is asked: GitHub's list of open
# issues lags a create by seconds (two workflows in one round filed two), and
# the maintainer may close the issue as a duplicate of one that already
# tracks the outage, after which a create would find no open issue and file
# it again every round. Asking GitHub for issues closed since the outage
# began would need the outage's start and the close reason; the file needs
# neither, and outlives a restart of the watch. An open issue with this title
# that the watch didn't file (one in status/needs-human, say) is reused by
# the create, which leaves its labels alone.
CI_SAID=
CI_STOPPED_TITLE='GitHub Actions starts no jobs'
ci_round() {
  local file name body rc out said= stopped= unstarted= unread=
  [ -r "$DIR/ci-stopped" ] && read -r stopped <"$DIR/ci-stopped"
  while IFS=$'\t' read -r file name; do
    [ -n "$file" ] || continue
    body=$(github red "$file" 2>&1)
    rc=$?
    case $rc in
      0)
        out=$(github create "main is red: $name" - status/ready priority/1 kind/bug source/dev <<<"$body" 2>&1)
        rc=$?
        if [ "$rc" != 0 ]; then
          said+=" fail:$file"
          [[ " $CI_SAID " == *" fail:$file "* ]] ||
            log "main is red: $name; could not file its issue (exit $rc): ${out//$'\n'/; }"
        else
          said+=" red:$file"
          [[ " $CI_SAID " == *" red:$file "* ]] ||
            log "main is red: $name; issue #${out##*$'\n'}"
        fi
        ;;
      1) ;;
      3)
        unstarted=1
        if [ -n "$stopped" ]; then
          out=$stopped rc=0
        else
          out=$(github create "$CI_STOPPED_TITLE" - status/needs-maintainer kind/bug source/dev <<<"$body" 2>&1)
          rc=$?
          if [ "$rc" = 0 ]; then
            stopped=${out##*$'\n'}
            echo "$stopped" >"$DIR/ci-stopped"
          fi
        fi
        if [ "$rc" != 0 ]; then
          said+=" stopfail:$file"
          [[ " $CI_SAID " == *" stopfail:$file "* ]] ||
            log "GitHub Actions started no job of $name; could not file its issue (exit $rc): ${out//$'\n'/; }"
        else
          said+=" stopped:$file"
          [[ " $CI_SAID " == *" stopped:$file "* ]] ||
            log "GitHub Actions started no job of $name; issue #${out##*$'\n'}"
        fi
        ;;
      *)
        unread=1
        said+=" err:$file"
        [[ " $CI_SAID " == *" err:$file "* ]] ||
          log "could not read main's CI for $name (exit $rc): ${body//$'\n'/; }"
        ;;
    esac
  done < <(ci_workflows)
  [ -n "$unstarted$unread" ] || rm -f "$DIR/ci-stopped"
  CI_SAID=$said
}

# runner_round: say in the Maintainer report when runs on main have waited
# QUEUED_MINUTES for a runner (see the top), and take it out again when they
# start. The report is asked each round while they wait, so a sweep that
# rewrote it without the note gets it back; report-note edits only when the
# note changes it. $DIR/runners-offline holds the note the report carries, so
# a round with nothing waiting asks GitHub nothing more. A run GitHub made no
# jobs for waits for good and no runner can take it (#1236): it is logged once,
# by id ($DIR/runs-without-jobs), and never noted. A failure is logged once
# until it works again.
RUNNERS_SAID=
runner_round() {
  local out rc tag since n online id at s note needs was= said= ghosts=
  [ -r "$DIR/runners-offline" ] && was=$(cat "$DIR/runners-offline")
  out=$(github queued --minutes "$QUEUED_MINUTES" 2>&1)
  rc=$?
  if [ "$rc" -le 1 ]; then
    while IFS=$'\t' read -r tag id at; do
      [ "$tag" = no-jobs ] || continue
      ghosts+=$id$'\n'
      grep -qxF "$id" "$DIR/runs-without-jobs" 2>/dev/null ||
        log "run $id on main, queued since $(date -d "$at" '+%Y-%m-%d %H:%M %Z' 2>/dev/null || echo "$at"), has no jobs: no runner can take it, so the runner note doesn't count it (GitHub's, not the runners')"
    done <<<"$out"
    if [ -n "$ghosts" ]; then printf '%s' "$ghosts" >"$DIR/runs-without-jobs"; else rm -f "$DIR/runs-without-jobs"; fi
  fi
  case $rc in
    0)
      IFS=$'\t' read -r tag since n online < <(grep $'^waiting\t' <<<"$out")
      since=$(date -d "$since" '+%Y-%m-%d %H:%M %Z' 2>/dev/null || echo "$since")
      s=$([ "$n" = 1 ] || echo s)
      if [ "$online" = 0 ]; then
        note="Runners offline since $since: $n run$s queued"
        needs="$note. Start Docker Desktop (WSL integration on for this distro) on the runners' device, then check \`make gh-runner-status\` (DEVELOPMENT.md)."
      else
        note="$n run$s queued since $since with $online runner$([ "$online" = 1 ] || echo s) online"
        needs="$note: they are busy, or none has the labels the jobs ask for. Look at the oldest run's jobs, and \`make gh-runner-status\` on the runners' devices (DEVELOPMENT.md)."
      fi
      if out=$(report_note runners "$note" "$needs" 2>&1); then
        [ -n "$was" ] || log "$note; said in the Maintainer report"
        echo "$note" >"$DIR/runners-offline"
      else
        said=note
        [ "$RUNNERS_SAID" = note ] || log "$note; could not say it in the Maintainer report: ${out//$'\n'/; }"
      fi
      ;;
    1)
      if [ -n "$was" ]; then
        if out=$(report_note runners 2>&1); then
          log "runners back: no run on main waits for one; the note left the Maintainer report"
          rm -f "$DIR/runners-offline"
        else
          said=clear
          [ "$RUNNERS_SAID" = clear ] || log "runners back; could not take the note out of the Maintainer report: ${out//$'\n'/; }"
        fi
      fi
      ;;
    *)
      said=err
      [ "$RUNNERS_SAID" = err ] || log "could not read the runs queued on main (queued, exit $rc): ${out//$'\n'/; }"
      ;;
  esac
  RUNNERS_SAID=$said
}

# tmp_round: say under the Maintainer report's Health, and not under Needs
# you, when /tmp on this device is TMP_FULL percent full or more, with its
# three largest entries (see the top), and take it out again when it drops
# back. The report is asked each round while /tmp is full, as runner_round
# does. $DIR/tmp-full holds the note the report carries. A failure is logged
# once until it works again.
TMP_SAID=
tmp_round() {
  local use out size path largest= note was= said=
  [ -r "$DIR/tmp-full" ] && was=$(cat "$DIR/tmp-full")
  if ! use=$(tmp_use 2>&1) || ! [[ $use =~ ^[0-9]+$ ]]; then
    [ "$TMP_SAID" = err ] || log "could not read how full /tmp is: ${use//$'\n'/; }"
    TMP_SAID=err
    return 0
  fi
  if [ "$use" -ge "$TMP_FULL" ]; then
    while IFS=$'\t' read -r size path; do
      [ -n "$path" ] && largest+="${largest:+, }$path ($size)"
    done < <(tmp_largest)
    note="/tmp on $TMP_HOST is $use% full${largest:+; largest: $largest}"
    if out=$(report_note "$TMP_KEY" "$note" "" 2>&1); then
      [ -n "$was" ] || log "$note; said under the Maintainer report's Health"
      echo "$note" >"$DIR/tmp-full"
    else
      said=note
      [ "$TMP_SAID" = note ] || log "$note; could not say it in the Maintainer report: ${out//$'\n'/; }"
    fi
  elif [ -n "$was" ]; then
    if out=$(report_note "$TMP_KEY" 2>&1); then
      log "/tmp on $TMP_HOST is back under $TMP_FULL%; the note left the Maintainer report"
      rm -f "$DIR/tmp-full"
    else
      said=clear
      [ "$TMP_SAID" = clear ] || log "/tmp on $TMP_HOST is back under $TMP_FULL%; could not take the note out of the Maintainer report: ${out//$'\n'/; }"
    fi
  fi
  TMP_SAID=$said
}

# flake_round: record each spec that failed on main (hack/lib/github.sh
# flakes, which reads each failed run's log once: $DIR/flakes-runs) in
# $DIR/flakes.tsv, SPEC<tab>RUN<tab>TIME, and file a "Flaky: <spec>" bug,
# status/ready and priority/3, for a spec just recorded that failed twice in
# FLAKE_DAYS days. A flake rarely fails two runs in a row, so ci_round's "main
# is red" misses it. hack/lib/github.sh create leaves an open issue with the
# title alone, so a spec that fails again files nothing new while its bug is
# open. GitHub caps a title at 256 characters: a longer spec is cut, at 240
# characters whatever the locale, the same way each time. A failure to read
# or file is logged once until it works; a spec whose bug could not be filed
# is tried again next round (FLAKE_RETRY), though no run of it is new.
FLAKE_SAID= FLAKE_RETRY=
flake_round() {
  local out rc spec n since title body said= retry=
  out=$(github flakes --days "$FLAKE_DAYS" --seen "$DIR/flakes-runs" 2>"$DIR/flakes.err")
  rc=$?
  if [ "$rc" != 0 ]; then
    out=$(cat "$DIR/flakes.err")
    [ "$FLAKE_SAID" = err ] || log "could not read the specs that failed on main (flakes, exit $rc): ${out//$'\n'/; }"
    FLAKE_SAID=err
    return 0
  fi
  [ "$FLAKE_SAID" != err ] || FLAKE_SAID=
  [ -n "$out$FLAKE_RETRY" ] || return 0
  [ -z "$out" ] || printf '%s\n' "$out" >>"$DIR/flakes.tsv"
  since=$(date -u -d "$FLAKE_DAYS days ago" +%Y-%m-%dT%H:%M:%SZ)
  while IFS= read -r spec; do
    [ -n "$spec" ] || continue
    # The spec goes in through the environment: awk -v would read its backslashes.
    n=$(S=$spec awk -F'\t' -v t="$since" '$1 == ENVIRON["S"] && $3 >= t { r[$2] = 1 } END { print length(r) }' "$DIR/flakes.tsv")
    if [ "$n" -lt 2 ]; then
      log "a spec failed on main: $spec"
      continue
    fi
    title="Flaky: $(LC_ALL=C.UTF-8 bash -c 'printf %s "${1:0:240}"' _ "$spec")"
    body="The spec \`$spec\` failed on main in $n runs in the last $FLAKE_DAYS days, yet main isn't red twice in a row: it is flaky.

## Runs

$(S=$spec awk -F'\t' -v t="$since" -v repo="${C4_GITHUB_REPO:-JairJosafath/c4factory}" \
  '$1 == ENVIRON["S"] && $3 >= t && !r[$2]++ { print "- https://github.com/" repo "/actions/runs/" $2 " at " $3 }' "$DIR/flakes.tsv")

Find why it fails now and then (a race, a timeout too short for a busy runner, an order it depends on) and fix the spec or the code; retrying until it passes is no fix. \`hack/lib/github.sh flakes --count\` lists the specs that failed on main this week. Filed by hack/relay-watch.sh, which files no other issue for this spec while this one is open."
    out=$(github create "$title" - status/ready priority/3 kind/bug source/dev <<<"$body" 2>&1)
    rc=$?
    if [ "$rc" = 0 ]; then
      log "a spec failed on main $n times in $FLAKE_DAYS days: $spec; issue #${out##*$'\n'}"
    else
      said=fail retry+=$spec$'\n'
      [ "$FLAKE_SAID" = fail ] || log "a spec failed on main $n times in $FLAKE_DAYS days: $spec; could not file its issue (exit $rc): ${out//$'\n'/; }"
    fi
  done < <({ [ -z "$out" ] || cut -f1 <<<"$out"; printf '%s' "$FLAKE_RETRY"; } | awk '!seen[$0]++')
  FLAKE_SAID=$said FLAKE_RETRY=$retry
}

# start WHY: log WHY and run hack/relay.sh for the workers that aren't running.
start() {
  local out rc
  log "$1; starting the relay"
  out=$(run_relay 2>&1 8>&-)
  rc=$?
  [ -n "$out" ] && log "relay: ${out//$'\n'/; }"
  [ "$rc" = 0 ] || log "hack/relay.sh failed (exit $rc)"
}

# triage NOW: ready issues or not, start a triage session, at most every
# TRIAGE_EVERY seconds and not while one runs, when a proposed issue waits that
# the last triage didn't see, one changed after it (not by the triage's own
# comment), one waits on an issue that closed after it, or the last triage is
# RETRIAGE seconds old. $DIR/triaged holds the last triage's time and issues,
# this watch's or one started by hand; only hack/relay.sh --triage writes it,
# as its session starts, and when the session never ran it keeps only the
# time there, so the same issues start it again. TRIAGE_T keeps the
# watch from asking for the proposed issues more often than every TRIAGE_ASK
# (WATCH_RETRY): every round, busy ones too, would cost GitHub calls by the
# dozen a minute.
TRIAGE_ERR= TRIAGE_T=0 TRIAGE_ASK=$RETRY
triage() {
  local now=$1 list= rc n changed closed why=() new= moved= freed= out t=0 seen= since=()
  [ -r "$DIR/triaged" ] && read -r t seen <"$DIR/triaged"
  t=${t:-0}
  [ $((now - t)) -ge "$TRIAGE_EVERY" ] || return 0
  [ $((now - TRIAGE_T)) -ge "$TRIAGE_ASK" ] || return 0
  triaging && return 0
  TRIAGE_T=$now
  # Within RETRIAGE, ask what changed since the last triage too.
  [ "$t" -gt 0 ] && [ $((now - t)) -lt "$RETRIAGE" ] && since=(--since "$t")
  out=$(proposed "${since[@]}" 2>"$DIR/proposed.err")
  rc=$?
  if [ "$rc" != 0 ]; then
    out="$out $(cat "$DIR/proposed.err" 2>/dev/null)"
    [ -n "$TRIAGE_ERR" ] || log "could not list the proposed issues (exit $rc): ${out//$'\n'/; }"
    TRIAGE_ERR=1
    return 0
  fi
  TRIAGE_ERR=
  while read -r n changed closed; do
    [[ $n =~ ^[0-9]+$ ]] || continue
    list+=" $n"
    if [[ " $seen " != *" $n "* ]]; then
      new+=" #$n"
    elif [ "${changed:-0}" -gt "$t" ] 2>/dev/null; then
      moved+=" #$n"
    elif [ "${closed:-0}" -gt "$t" ] 2>/dev/null; then
      freed+=" #$n"
    fi
  done <<<"$out"
  list=${list# }
  [ -n "$list" ] || return 0
  [ -n "$new" ] && why+=("new:$new")
  [ -n "$moved" ] && why+=("changed:$moved")
  [ -n "$freed" ] && why+=("waited on a closed issue:$freed")
  [ ${#why[@]} -gt 0 ] || [ ${#since[@]} = 0 ] || return 0
  [ ${#why[@]} -gt 0 ] || why=("read again after $((RETRIAGE / 3600))h")
  out=$(run_triage 2>&1 8>&-)
  rc=$?
  why=$(printf '%s; ' "${why[@]}")
  log "triage: proposed #${list// / #} (${why%; }); hack/relay.sh --triage${out:+: ${out//$'\n'/; }}$([ "$rc" = 0 ] || echo " (failed, exit $rc; again after $((TRIAGE_EVERY / 60))m)")"
}

# maintain NOW: start the maintainer when it is on, none runs, and an issue
# got status/needs-maintainer after the last sweep started, or the last sweep
# is MAINTAIN_EVERY seconds old and the activity changed since (see the
# header). .relay/maintained holds the last full sweep's start and the
# activity it saw; hack/relay.sh --maintain writes it, so the sweep's prompt reads when the
# last one ran. MAINT_T keeps the watch from asking for the activity, or
# repeating a start that didn't take, more often than every RETRY.
MAINT_T=0 MAINT_ERR= ESC_ERR=
maintain() {
  local now=$1 t= seen= act out rc esc n u new=
  [ "$MAINTAIN_EVERY" -gt 0 ] 2>/dev/null || return 0
  [ $((now - MAINT_T)) -ge "$RETRY" ] || return 0
  maintaining && return 0
  [ -r "$DIR/maintained" ] && read -r t seen <"$DIR/maintained"
  [[ $t =~ ^[0-9]+$ ]] || t=0
  if esc=$(escalated 2>&1); then
    ESC_ERR=
    while read -r n u; do
      [[ $u =~ ^[0-9]+$ ]] && [ "$u" -gt "$t" ] && new+=" #$n"
    done <<<"$esc"
  else
    [ -n "$ESC_ERR" ] || log "could not list the issues escalated to the maintainer: ${esc//$'\n'/; }"
    ESC_ERR=1
  fi
  if [ -n "$new" ]; then
    MAINT_T=$now
    out=$(run_maintain 2>&1 8>&-)
    rc=$?
    log "maintainer: escalated${new} (status/needs-maintainer); hack/relay.sh --maintain${out:+: ${out//$'\n'/; }}$([ "$rc" = 0 ] || echo " (failed, exit $rc; again after $((RETRY / 60))m)")"
    return 0
  fi
  [ $((now - t)) -ge "$MAINTAIN_EVERY" ] || return 0
  if ! act=$(activity 2>&1); then
    [ -n "$MAINT_ERR" ] || log "could not read the activity for the maintainer: ${act//$'\n'/; }"
    MAINT_ERR=1
    return 0
  fi
  MAINT_ERR=
  # Asked at most every RETRY, on a quiet night too.
  MAINT_T=$now
  [ "$act" != "$seen" ] || return 0
  out=$(run_maintain 2>&1 8>&-)
  rc=$?
  log "maintainer: $([ "$t" = 0 ] && echo "no sweep recorded" || echo "activity since the sweep of $(date -d "@$t" -Is)") ($act); hack/relay.sh --maintain${out:+: ${out//$'\n'/; }}$([ "$rc" = 0 ] || echo " (failed, exit $rc; again after $((RETRY / 60))m)")"
}

# held: on an idle round, log what holds back the issues pick skips whose
# blocker nobody works (hack/lib/github.sh blocked --unworked), once per
# change, and report the issues that wait on each other in a cycle (cycles
# --report: a comment on each, the newest to status/needs-maintainer).
HELD= HELD_ERR= CYCLES_ERR=
held() {
  local out rc
  out=$(blocked 2>"$DIR/blocked.err")
  rc=$?
  if [ "$rc" != 0 ]; then
    out="$out $(cat "$DIR/blocked.err" 2>/dev/null)"
    [ -n "$HELD_ERR" ] || log "could not list what holds back the skipped issues (exit $rc): ${out//$'\n'/; }"
    HELD_ERR=1
    return 0
  fi
  HELD_ERR=
  if [ "$out" != "$HELD" ]; then
    HELD=$out
    if [ -n "$out" ]; then
      log "held back by issues nobody works: ${out//$'\n'/; }"
    else
      log "no skipped issue waits on an issue nobody works"
    fi
  fi
  out=$(cycles 2>&1)
  rc=$?
  [ "$rc" = 0 ] && [ -z "$out" ] && { CYCLES_ERR=; return 0; }
  [ "$rc" = 0 ] || { [ -z "$CYCLES_ERR" ] || return 0; CYCLES_ERR=1; }
  log "${out//$'\n'/; }$([ "$rc" = 0 ] || echo " (cycles --report failed, exit $rc)")"
}

# round: one look at GitHub, and the relay started when there is work for it.
# SAID is the last state logged, so a state that lasts is logged once.
SAID= LAST_N= LAST_T=0
round() {
  local row rc n now until why
  now=$(date +%s)
  if [ -r "$DIR/paused-until" ] && read -r until why <"$DIR/paused-until" &&
    [[ $until =~ ^[0-9]+$ ]] && [ "$until" -gt "$now" ]; then
    [ "$SAID" = "paused$until" ] ||
      log "the relay is paused until $(date -d "@$until" -Is) ($why); starting nothing"
    SAID=paused$until
    return 0
  fi
  maintain "$now"
  tend "$now"
  # The triage needs no free worker (#1191).
  triage "$now"
  worker_free || { SAID=busy; return 0; }
  row=$(pick_next 2>&1)
  rc=$?
  now=$(date +%s)
  case $rc in
    0)
      n=${row%%$'\t'*}
      if [ "$n" = "$LAST_N" ] && [ $((now - LAST_T)) -lt "$RETRY" ]; then
        [ "$SAID" = "retry$n" ] ||
          log "#$n is still unclaimed; starting the relay for it again after $((RETRY / 60))m (see .relay/relay-*.log)"
        SAID=retry$n
        return 0
      fi
      LAST_N=$n LAST_T=$now SAID=
      start "ready: ${row//$'\t'/ | }"
      ;;
    1)
      [ "$SAID" = idle ] || log "nothing ready; waiting"
      SAID=idle
      held
      ;;
    *)
      [ "$SAID" = err ] || log "could not ask GitHub for the next issue (exit $rc): ${row//$'\n'/; }"
      SAID=err
      ;;
  esac
}

code_sum() { cat "$CODE/hack/relay-watch.sh" "$CODE/hack/lib/session-env.sh" "$CODE/hack/lib/relay-code.sh" 2>/dev/null | cksum; }

# update: run origin/main's hack/pull.sh on this checkout, move relay-code to
# origin/main, and restart the watch on relay-code's copy when it runs from
# elsewhere or that changed the watch's code (see the header).
update() {
  local out key
  out=$(C4_PULL_REPO=$REPO "$CODE/hack/pull.sh" 2>&1)
  # A reason pull.sh repeats (its lines start with the time) is logged once.
  key=$(sed 's/^[0-9][0-9]:[0-9][0-9] //' <<<"$out")
  [ -n "$out" ] && [ "$key" != "$PULLED" ] && log "${out//$'\n'/; }"
  PULLED=$key
  [ "$PULL" = 0 ] && return 0
  relay_code_stale || return 0
  if [ "$CODE" = "$(relay_code)" ]; then
    log "the watch's code changed ($(relay_code) at $(git -C "$(relay_code)" rev-parse --short HEAD)); restarting"
  else
    log "moving to origin/main's code ($(relay_code) at $(git -C "$(relay_code)" rev-parse --short HEAD)) from $CODE; restarting"
  fi
  # The new watch keeps this one's lock on fd 8.
  exec env C4_RELAY_WATCH_CHILD=1 C4_RELAY_WATCH_RESTART=1 C4_RELAY_REPO="$REPO" bash "$(relay_code)/hack/relay-watch.sh" \
    --workers "$WORKERS" $([ "$MODE" = foreground ] && echo --foreground)
}

# --install and --uninstall. The unit goes to the user's own systemd folder,
# not $XDG_CONFIG_HOME, which the project shell points into .dev/.
UNIT=c4factory-relay-watch.service
TEMPLATE="$CODE/hack/relay-watch.service"
unit_file() { echo "$HOME/.config/systemd/user/$UNIT"; }
cron_tag() { echo "# c4factory relay watch: $REPO"; }

# has_systemd: whether this user has a systemd user manager to run the unit.
has_systemd() { command -v systemctl >/dev/null && systemctl --user show-environment >/dev/null 2>&1; }

# missing_groups GIDS: the groups the user has in /etc/group (id -G USER) that
# GIDS, a process's groups, lack, by name, space-separated.
missing_groups() {
  local have=" $1 " user i gids names out=
  user=$(id -un)
  read -ra gids <<<"$(id -G "$user")"
  read -ra names <<<"$(id -Gn "$user")"
  for i in "${!gids[@]}"; do
    [[ $have == *" ${gids[$i]} "* ]] || out+=" ${names[$i]:-${gids[$i]}}"
  done
  echo "${out# }"
}

# group_warning WHO GIDS: the warning when WHO, with groups GIDS, lacks a group
# the user has. A process keeps the groups it started with, so a systemd user
# manager that started before the user joined docker, and every watch, worker
# and session it starts, can't reach Docker.
group_warning() {
  local missing
  missing=$(missing_groups "$2")
  [ -n "$missing" ] || return 0
  echo "$1 lacks the group(s) $missing that $(id -un) has in /etc/group: it inherits the groups of the systemd user manager, which started before $(id -un) joined them, so its sessions can't reach Docker (make test-gitea runs Gitea in it). Restart the user manager: log out and in again, or on WSL run wsl --shutdown from Windows. Check: grep Groups /proc/\$(pgrep -u \"\$USER\" -x systemd)/status"
}

# own_groups: log the warning when this watch lacks one of the user's groups.
own_groups() {
  local warning
  warning=$(group_warning "this watch" "$(id -G)")
  [ -z "$warning" ] || log "$warning"
}

# manager_groups: the groups of this user's systemd user manager.
manager_groups() {
  local pid
  pid=$(pgrep -u "$(id -u)" -x systemd | head -n 1) && [ -n "$pid" ] || return 1
  awk '/^Groups:/ { $1 = ""; print }' "/proc/$pid/status"
}

# settings: the installer's relay settings, as NAME=VALUE lines, for the unit
# and cron, whose environments have none. Credentials stay out: the watch gets
# its GitHub token from the git credential helper, as hack/lib/github.sh does.
settings() {
  local v
  for v in $(compgen -v | grep -E '^(RELAY_|WATCH_|HEAVY_JOBS$|C4_CLUSTER)'); do
    printf '%s=%s\n' "$v" "${!v}"
  done
}

# render_unit: the unit for this checkout and WORKERS, with the installer's PATH
# (the user manager's own PATH lacks gh and claude) and relay settings.
render_unit() {
  local t env= line
  t=$(<"$TEMPLATE")
  t=${t//@REPO@/"$REPO"}
  t=${t//@WORKERS@/"$WORKERS"}
  t=${t//@PATH@/"$PATH"}
  while IFS= read -r line; do env+=$'\n'"Environment=\"$line\""; done < <(settings)
  t=${t//$'\n'@ENV@/"$env"}
  printf '%s\n' "$t"
}

# cron_lines: the crontab without this checkout's lines (stdin), then them.
cron_lines() {
  local cmd
  local line env=
  while IFS= read -r line; do env+="${line%%=*}=$(printf %q "${line#*=}") "; done < <(settings)
  cmd="cd $(printf %q "$REPO") && PATH=$(printf %q "$PATH") $env./dev run hack/relay-watch.sh --cron --workers $WORKERS >>.relay/watch.out 2>&1"
  grep -vF "$(cron_tag)"
  echo "@reboot $cmd $(cron_tag)"
  echo "*/5 * * * * $cmd $(cron_tag)"
}

# without_cron: remove this checkout's crontab lines; fails when there are
# none. It reads the whole table first: a grep -q on crontab -l's pipe can
# exit before crontab -l is done, and pipefail then fails the pipeline.
without_cron() {
  local tab
  command -v crontab >/dev/null || return 1
  tab=$(crontab -l 2>/dev/null) || return 1
  grep -qF "$(cron_tag)" <<<"$tab" || return 1
  grep -vF "$(cron_tag)" <<<"$tab" | crontab -
}

# in_worktree: whether REPO is a linked git worktree, not the main checkout.
in_worktree() {
  local git common
  git=$(git -C "$REPO" rev-parse --absolute-git-dir 2>/dev/null) || return 1
  common=$(cd "$REPO" && cd "$(git rev-parse --git-common-dir)" && pwd)
  [ "$git" != "$common" ]
}

install_watch() {
  local groups
  # One unit per device: from a worktree it would run that worktree's watch.
  in_worktree && { echo "relay-watch: install from the main checkout, not a worktree" >&2; return 1; }
  mkdir -p "$DIR"
  rm -f "$STOP"
  if has_systemd; then
    mkdir -p "$(dirname "$(unit_file)")"
    render_unit >"$(unit_file)"
    without_cron || true
    systemctl --user daemon-reload &&
      systemctl --user enable "$UNIT" &&
      systemctl --user restart "$UNIT" || { echo "relay-watch: systemctl failed" >&2; return 1; }
    # Linger starts the user's manager, and so the watch, at boot without a login.
    loginctl enable-linger "$(id -un)" 2>/dev/null ||
      echo "relay-watch: loginctl enable-linger failed; the watch starts at the first login, not at boot" >&2
    echo "relay watch installed as $(unit_file) for $WORKERS worker(s): tail -f $LOG"
    groups=$(manager_groups) && group_warning "relay-watch: the systemd user manager" "$groups" >&2
  else
    command -v crontab >/dev/null || { echo "relay-watch: neither systemd nor crontab here" >&2; return 1; }
    { crontab -l 2>/dev/null || true; } | cron_lines | crontab - || { echo "relay-watch: crontab failed" >&2; return 1; }
    echo "relay watch installed in the crontab (at boot and every 5 minutes) for $WORKERS worker(s): tail -f $LOG"
    (cd "$REPO" && ./dev run hack/relay-watch.sh --cron --workers "$WORKERS" >>"$DIR/watch.out" 2>&1)
  fi
  echo "stop: touch $STOP; start again: hack/relay-watch.sh"
}

uninstall_watch() {
  if [ -e "$(unit_file)" ]; then
    has_systemd && systemctl --user disable --now "$UNIT" ||
      { echo "relay-watch: systemctl --user failed; $(unit_file) stays" >&2; return 1; }
    rm -f "$(unit_file)"
    systemctl --user daemon-reload
    echo "removed $(unit_file); its watch is stopped"
  fi
  if without_cron; then
    echo "removed the crontab lines; a watch running now keeps running (touch $STOP)"
  fi
  return 0
}

# Sourced (hack/relay-watch_test.sh): the functions only.
[ "${BASH_SOURCE[0]}" = "$0" ] || return 0
# Whoever starts the watch, a Claude Code session among them, the watch and the
# relay it starts run as from a plain project shell (#645), headless: no
# cluster lease code (gh_headless in hack/lib/github.sh).
drop_session_env
export C4_HEADLESS=1

mkdir -p "$DIR"
[ -e "$DIR/.gitignore" ] || echo '*' >"$DIR/.gitignore"

case $MODE in
  install) install_watch; exit ;;
  uninstall) uninstall_watch; exit ;;
esac

# Started from a terminal with the unit installed: start the unit (after a
# stop file), so the watch stays supervised.
if [ "$MODE" = start ] && [ "${C4_RELAY_WATCH_CHILD:-}" != 1 ] && [ -e "$(unit_file)" ] && has_systemd; then
  rm -f "$STOP"
  systemctl --user start "$UNIT" || exit 1
  echo "relay watch started under systemd ($UNIT, its own worker count; --install --workers N changes it): tail -f $LOG"
  exit 0
fi

# Started from a terminal or cron: start the watch, detached, and return.
# From cron, a stop file stays and keeps the watch down.
if [ "${C4_RELAY_WATCH_CHILD:-}" != 1 ] && [ "$MODE" != foreground ]; then
  if ! flock -n "$DIR/watch.lock" true; then
    [ "$MODE" = cron ] || echo "the relay watch is already running; see $LOG"
    exit 0
  fi
  if [ "$MODE" = cron ]; then
    [ -e "$STOP" ] && exit 0
  else
    rm -f "$STOP"
  fi
  C4_RELAY_WATCH_CHILD=1 setsid nohup bash "$0" --workers "$WORKERS" >>"$DIR/watch.out" 2>&1 </dev/null &
  echo "relay watch started (pid $!) for $WORKERS worker(s): tail -f $LOG"
  echo "stop: touch $STOP"
  exit 0
fi

cd "$REPO" || exit 1
if [ "${C4_RELAY_WATCH_RESTART:-}" = 1 ]; then
  flock -n 8 || { log "restarted without the watch's lock; exiting"; exit 1; }
else
  exec 8>"$DIR/watch.lock"
  if ! flock -n 8; then
    [ "$MODE" = foreground ] || { log "another watch holds the lock; exiting"; exit 1; }
    # Under the unit: wait for the watch started by hand, then take over.
    log "another watch holds the lock; pid $$ waits for it to end"
    flock 8
  fi
fi
# The workers and their sessions don't need to know they were started by a watch.
unset C4_RELAY_WATCH_CHILD C4_RELAY_WATCH_RESTART
CODE_SUM=$(code_sum)
log "watch started: pid $$${C4_CLUSTER:+, C4_CLUSTER=$C4_CLUSTER}$([ "$MODE" = foreground ] && echo ", under systemd"), code $CODE at $(git -C "$CODE" rev-parse --short HEAD 2>/dev/null), $WORKERS worker(s), asks GitHub every ${EVERY}s"
own_groups
PULL_T=0 PULLED= CI_T=0
while :; do
  [ -e "$STOP" ] && { log "stop file found; stopping"; exit 0; }
  now=$(date +%s)
  if [ $((now - CI_T)) -ge "$CI_EVERY" ]; then
    CI_T=$now
    ci_round
    flake_round
    runner_round
    tmp_round
  fi
  if [ $((now - PULL_T)) -ge "$PULL_EVERY" ]; then
    PULL_T=$now
    update
  fi
  trim "$now"
  round
  sleep "$EVERY"
done
