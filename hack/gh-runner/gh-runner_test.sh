#!/usr/bin/env bash
# Tests for hack/gh-runner/gh-runner.sh: N runners up, fewer up, down, the
# single runner from before there were several, status with Docker
# unreachable, a container down and a runner offline, and the keeper (keep,
# install, uninstall), with docker, gh, stat, systemctl, loginctl and crontab
# faked. The fake Docker keeps its containers, volumes and registrations as
# files; the fake GitHub lists the registrations, each online.
# Run: hack/gh-runner/gh-runner_test.sh
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
SCRIPT=$(cd "$(dirname "$0")" && pwd)/gh-runner.sh
fail=0
check() { if [ "$2" = "$3" ]; then echo "ok   $1"; else echo "FAIL $1: got [$2] want [$3]"; fail=1; fi; }

export S=$T/state
unset WSL_DISTRO_NAME # each test names its own distro
mkdir -p "$T/bin" "$S/containers" "$S/volumes" "$S/reg"

cat >"$T/bin/docker" <<'FAKE'
#!/usr/bin/env bash
# The fake Docker: what gh-runner.sh calls, on files under $S.
arg() { local want=$1; shift; while [ $# -gt 0 ]; do [ "$1" = "$want" ] && { echo "$2"; return; }; shift; done; }
case "$1" in
  info) [ ! -e "$S/no-docker" ] ;;
  build) rev=$(arg --label "$@"); echo "${rev#*=}" >"$S/image" ;;
  image) [ -s "$S/image" ] || exit 1; [ "$3" = -f ] && cat "$S/image"; exit 0 ;;
  volume)
    case "$2" in
      create) touch "$S/volumes/$3" ;;
      rm) [ -e "$S/volumes/$3" ] || exit 1; rm "$S/volumes/$3" ;;
      inspect) [ -e "$S/volumes/$3" ] ;;
      ls) ls "$S/volumes" ;;
    esac ;;
  ps) for c in "$S"/containers/*; do [ -e "$c" ] || continue; st='Up 1 minute'; [ -e "$S/exited-${c##*/}" ] && st='Exited (0) 2 hours ago'
      printf '%s\t%s\ttheos/gh-runner:dev\n' "${c##*/}" "$st"; done | if [[ $* == *Status* ]]; then cat; else cut -f1; fi ;;
  inspect)
    c=${*: -1}; [ -e "$S/containers/$c" ] || exit 1
    case "$3" in *rev*) cat "$S/containers/$c" ;; *StartedAt*) echo 2026-10-07T00:00:00Z ;; esac ;;
  stop) [ -e "$S/containers/$2" ] ;;
  start) [ -e "$S/containers/$2" ] && rm -f "$S/exited-$2" ;;
  rm) rm "$S/containers/$2" ;;
  # More than a pipe holds after the line: a grep -q on its pipe would exit
  # early and, under the script's pipefail, read a "no".
  logs) echo "Listening for Jobs"; seq 100000 ;;
  run)
    shift
    detach=false name= vol= rname= rlabels=
    while [ $# -gt 0 ]; do
      case "$1" in
        -d) detach=true ;;
        --name) name=$2; shift ;;
        -v) case "$2" in *:/runner-state) vol=${2%%:*} ;; esac; shift ;;
        -e) case "$2" in RUNNER_NAME=*) rname=${2#*=} ;; RUNNER_LABELS=*) rlabels=${2#*=} ;; esac; shift ;;
        --rm|--init|--restart|--stop-timeout|--network|--group-add) case "$1" in --rm|--init) ;; *) shift ;; esac ;;
        theos/gh-runner:dev) shift; break ;;
      esac
      shift
    done
    if $detach; then
      [ -e "$S/containers/$name" ] && { echo "name $name in use" >&2; exit 1; }
      cat "$S/image" >"$S/containers/$name"; exit 0
    fi
    [ -e "$S/volumes/$vol" ] || touch "$S/volumes/$vol"
    case "$1" in
      registered) [ -s "$S/reg/$vol" ] ;;
      configure) [ "$RUNNER_TOKEN" = reg-token ] || exit 1; echo "$rname $rlabels" >"$S/reg/$vol" ;;
      remove) [ "$RUNNER_TOKEN" = remove-token ] || exit 1; rm "$S/reg/$vol" ;;
    esac ;;
esac
FAKE

cat >"$T/bin/gh" <<'FAKE'
#!/usr/bin/env bash
# The fake GitHub: tokens, and the registrations as its list of runners.
case "$*" in
  *registration-token*) echo reg-token ;;
  *remove-token*) echo remove-token ;;
  *"-X DELETE"*) echo "gh $*" >>"$S/deleted" ;;
  *actions/runners\?per_page*)
    jq_expr=${*: -1}
    for r in "$S"/reg/*; do
      [ -e "$r" ] || continue
      read -r n l <"$r"
      st=online; [ -e "$S/offline-$n" ] && st=offline
      jq -n --arg n "$n" --arg l "$l" --arg st "$st" '{id: 7, name: $n, status: $st, busy: false,
        labels: ((["self-hosted", "Linux", "X64"] | map({id: 0, name: ., type: "read-only"}))
          + ($l | split(",") | map({id: 0, name: ., type: "custom"})))}'
    done | jq -s '{runners: .}' | jq -r "$jq_expr" ;;
esac
FAKE

printf '#!/bin/sh\necho 999\n' >"$T/bin/stat"
# sleep only records: up waits on a runner that does not show as online.
printf '#!/bin/sh\necho "$*" >>"$S/slept"\n' >"$T/bin/sleep"
chmod +x "$T/bin/"*

# HOME too: up records in it, and down removes the record.
gh_runner() { PATH="$T/bin:$PATH" HOME="$T/home" GH_RUNNER_NAME=theos-dev GH_RUNNER_WORK="$T/work" "$SCRIPT" "$@"; }
containers() { ls "$S/containers" | sort | paste -sd' '; }
configs() { ls "$S/volumes" | grep -- '-config$' | sort | paste -sd' '; }
regs() { cat "$S"/reg/* 2>/dev/null | sort | paste -sd';'; }

# Three runners: three containers, three registration volumes, three names,
# and the Kind label on runner 1 only.
RUNNERS=3 gh_runner up >"$T/out" 2>&1
check "up 3: exit" "$?" 0
check "up 3: containers" "$(containers)" "theos-gh-runner-1 theos-gh-runner-2 theos-gh-runner-3"
check "up 3: registration volumes" "$(configs)" "theos-gh-runner-1-config theos-gh-runner-2-config theos-gh-runner-3-config"
check "up 3: registrations" "$(regs)" "theos-dev-1 theos,theos-kind;theos-dev-2 theos;theos-dev-3 theos"
check "up 3: work directories" "$(ls "$T/work" | paste -sd' ')" "1 2 3"
check "up 3: one shared cache" "$(ls "$S/volumes" | grep -c '^theos-gh-runner-cache$')" 1
check "up 3: status says all online" "$(grep -c ' online ' "$T/out")" 3
check "up 3: no wait for a runner that listens" "$(cat "$S/slept" 2>/dev/null)" ""

# Again: nothing registers again or restarts.
RUNNERS=3 gh_runner up >"$T/out" 2>&1
check "up 3 again: no registration" "$(grep -c registering "$T/out")" 0
check "up 3 again: no new container" "$(grep -c 'starting container' "$T/out")" 0

# One: runners 2 and 3 go, from Docker and from GitHub.
RUNNERS=1 gh_runner up >"$T/out" 2>&1
check "up 1 after 3: exit" "$?" 0
check "up 1 after 3: containers" "$(containers)" "theos-gh-runner-1"
check "up 1 after 3: registration volumes" "$(configs)" "theos-gh-runner-1-config"
check "up 1 after 3: registrations" "$(regs)" "theos-dev-1 theos,theos-kind"
check "up 1 after 3: their tool caches go" "$(ls "$S/volumes" | grep -c toolcache)" 1

gh_runner status >"$T/out" 2>&1
check "status: online" "$? | $(cat "$T/out")" "0 | docker: reachable from this host
container: theos-gh-runner-1 up (Up 1 minute, theos/gh-runner:dev)
github: theos-dev-1 online busy=false labels=self-hosted,Linux,X64,theos,theos-kind"

# Docker not reachable (Docker Desktop stopped): said, with what GitHub sees
# and the fix, from the distro WSL names.
touch "$S/no-docker" "$S/offline-theos-dev-1"
WSL_DISTRO_NAME=Ubuntu gh_runner status >"$T/out" 2>&1
check "status: no Docker" "$? | $(cat "$T/out")" "1 | docker: not reachable from WSL distro Ubuntu; the runners stop with it
github: theos-dev-1 offline busy=false
fix: start Docker Desktop, with WSL integration on for Ubuntu (Settings > Resources > WSL integration); the runners restart with it. Then make gh-runner-status again (DEVELOPMENT.md)."
rm "$S/no-docker"

# A container stopped, GitHub seeing its runner offline: both said, and the fix.
touch "$S/exited-theos-gh-runner-1"
gh_runner status >"$T/out" 2>&1
check "status: a container down" "$? | $(sed -n '2p;4p' "$T/out")" "1 | container: theos-gh-runner-1 down (Exited (0) 2 hours ago, theos/gh-runner:dev)
fix: make gh-runner RUNNERS=1 starts the containers that are down and registers again the runners GitHub lost; a container that is up while GitHub sees it offline says why in docker logs --tail 50 NAME (DEVELOPMENT.md)."
check "status: GitHub sees it offline" "$(grep -c '^github: theos-dev-1 offline' "$T/out")" 1
rm "$S/exited-theos-gh-runner-1" "$S/offline-theos-dev-1"

# Down removes them all; the shared cache stays.
RUNNERS=3 gh_runner up >/dev/null 2>&1
gh_runner down >"$T/out" 2>&1
check "down: exit" "$?" 0
check "down: no containers" "$(containers)" ""
check "down: no registration volumes" "$(configs)" ""
check "down: no registrations" "$(regs)" ""
check "down: shared cache stays" "$(ls "$S/volumes" | grep -c '^theos-gh-runner-cache$')" 1
gh_runner status >/dev/null 2>&1
check "status: none is a failure" "$?" 1

# A runner 1 registered without the Kind label registers again.
RUNNERS=1 gh_runner up >/dev/null 2>&1
echo "theos-dev-1 theos" >"$S/reg/theos-gh-runner-1-config"
RUNNERS=1 gh_runner up >"$T/out" 2>&1
check "wrong labels: registers again" "$(regs)" "theos-dev-1 theos,theos-kind"
check "wrong labels: new container" "$(grep -c 'starting container' "$T/out")" 1
gh_runner down >/dev/null 2>&1

# The single runner from before: removed, and runner 1 takes its place.
cat "$S/image" >"$S/containers/theos-gh-runner"
touch "$S/volumes/theos-gh-runner-config" "$S/volumes/theos-gh-runner-toolcache"
echo "theos-dev theos" >"$S/reg/theos-gh-runner-config"
RUNNERS=1 gh_runner up >"$T/out" 2>&1
check "migrate: exit" "$?" 0
check "migrate: containers" "$(containers)" "theos-gh-runner-1"
check "migrate: registrations" "$(regs)" "theos-dev-1 theos,theos-kind"
check "migrate: old volumes go" "$(ls "$S/volumes" | grep -c '^theos-gh-runner-\(config\|toolcache\)$')" 0

RUNNERS=0 gh_runner up >/dev/null 2>&1
check "RUNNERS=0 refused" "$?" 64
RUNNERS=x gh_runner up >/dev/null 2>&1
check "RUNNERS=x refused" "$?" 64
check "a refused up records nothing" "$(cat "$T/home/.local/state/theos/gh-runner/runners")" "RUNNERS=1
GH_RUNNER_NAME=theos-dev
GH_RUNNER_WORK=$T/work
THEOS_GITHUB_REPO=JairJosafath/theos"

# The keeper (#1248). up records the count, with the names, work directories
# and repository it used; keep starts the runners the record names whose
# container is gone or not up, and never changes the record.
rec=$T/home/.local/state/theos/gh-runner/runners
RUNNERS=3 gh_runner up >/dev/null 2>&1
check "up records the count" "$(sed -n 1p "$rec")" "RUNNERS=3"
cp "$rec" "$T/recorded"
recorded() { cmp -s "$rec" "$T/recorded" && echo same; }

# All up: nothing to do, nothing said.
gh_runner keep >"$T/out" 2>&1
check "keep, all up: exit" "$?" 0
check "keep, all up: says nothing" "$(cat "$T/out")" ""

# Docker Desktop recreated its data disk: the containers, volumes,
# registrations and image are gone. keep makes the recorded three again, in
# the recorded work directories, whatever RUNNERS and GH_RUNNER_WORK say now.
rm -f "$S"/containers/* "$S"/volumes/* "$S"/reg/* "$S/image"
RUNNERS=1 GH_RUNNER_WORK=$T/elsewhere gh_runner keep >"$T/out" 2>&1
check "keep, all gone: exit" "$?" 0
check "keep, all gone: the recorded count" "$(containers)" "theos-gh-runner-1 theos-gh-runner-2 theos-gh-runner-3"
check "keep, all gone: registered again" "$(regs)" "theos-dev-1 theos,theos-kind;theos-dev-2 theos;theos-dev-3 theos"
check "keep, all gone: says which, with the time" \
  "$(grep -c '^[0-9-]* [0-9:]* gh-runner: runner(s) 1 2 3 of 3 not up; starting them$' "$T/out")" 1
check "keep, all gone: builds the image" "$(grep -c 'building' "$T/out")" 1
check "keep, all gone: the recorded work directories" "$([ -e "$T/elsewhere" ] && echo moved)" ""
check "keep, all gone: the record stays" "$(recorded)" same

# Runner 1 is up from an older image, 2 is gone, 3 stopped: 1 stays as it is,
# since making it again would kill its job; 2 is made, 3 started.
echo older >"$S/containers/theos-gh-runner-1"
rm "$S/containers/theos-gh-runner-2"
touch "$S/exited-theos-gh-runner-3"
gh_runner keep >"$T/out" 2>&1
check "keep, one up: exit" "$?" 0
check "keep, one up: says which" "$(grep -c 'runner(s) 2 3 of 3 not up' "$T/out")" 1
check "keep, one up: leaves it alone" "$(cat "$S/containers/theos-gh-runner-1")" older
check "keep, one up: makes the gone one" "$(containers)" "theos-gh-runner-1 theos-gh-runner-2 theos-gh-runner-3"
check "keep, one up: starts the stopped one" "$([ -e "$S/exited-theos-gh-runner-3" ] && echo stopped)" ""
check "keep, one up: the record stays" "$(recorded)" same

# Docker not reachable: nothing starts, and it is said once, until Docker
# answers again.
rm "$S/containers/theos-gh-runner-2"
touch "$S/no-docker"
gh_runner keep >"$T/out" 2>&1
check "keep, no Docker: exit" "$?" 0
check "keep, no Docker: says so" "$(grep -c 'Docker is not reachable; starting nothing' "$T/out")" 1
check "keep, no Docker: starts nothing" "$(containers)" "theos-gh-runner-1 theos-gh-runner-3"
gh_runner keep >"$T/out" 2>&1
check "keep, no Docker again: says it once" "$(cat "$T/out")" ""
rm "$S/no-docker"
gh_runner keep >"$T/out" 2>&1
check "keep, Docker back: says so" "$(grep -c 'Docker answers again' "$T/out")" 1
check "keep, Docker back: starts the gone one" "$(containers)" "theos-gh-runner-1 theos-gh-runner-2 theos-gh-runner-3"
check "keep, Docker back: the record stays" "$(recorded)" same

# While up or down runs, keep skips its turn.
rm "$S/containers/theos-gh-runner-2"
exec 9>"$T/home/.local/state/theos/gh-runner/lock"
flock 9
gh_runner keep >"$T/out" 2>&1
check "keep, while up runs: exit" "$?" 0
check "keep, while up runs: starts nothing" "$(containers)" "theos-gh-runner-1 theos-gh-runner-3"
exec 9>&-

# down removes the record: the keeper starts nothing until the next up.
gh_runner down >/dev/null 2>&1
check "down: removes the record" "$([ -e "$rec" ] && echo there)" ""
gh_runner keep >"$T/out" 2>&1
check "keep after down: exit" "$?" 0
check "keep after down: starts nothing" "$(containers)" ""
check "keep after down: says nothing" "$(cat "$T/out")" ""

# install with systemd: a timer and a oneshot service that run this checkout's
# keep every 2 minutes, without ./dev run, under the installer's PATH, logging
# next to the record; enabled, started, with linger. From a copy of the script
# outside any git checkout.
F=$T/fake
mkdir -p "$F" "$T/repo/hack/gh-runner"
cp "$SCRIPT" "$T/repo/hack/gh-runner/"
cat >"$T/bin/systemctl" <<'SH'
#!/bin/sh
echo "systemctl $*" >>"$F/calls"
case "$*" in *show-environment*) [ ! -e "$F/no-systemd" ] ;; esac
SH
printf '#!/bin/sh\necho "loginctl $*" >>"$F/calls"\n' >"$T/bin/loginctl"
printf '#!/bin/sh\nexit 1\n' >"$T/bin/pgrep"
cat >"$T/bin/crontab" <<'SH'
#!/bin/sh
case "$1" in
  -l) [ -e "$F/crontab" ] || exit 1; cat "$F/crontab" ;;
  -) t=$(cat); printf "%s\n" "$t" >"$F/crontab" ;;
esac
SH
chmod +x "$T/bin/"*
export F
keeper() { PATH="$T/bin:$PATH" HOME="$T/home" GIT_CEILING_DIRECTORIES=$T "$T/repo/hack/gh-runner/gh-runner.sh" "$@"; }
units=$T/home/.config/systemd/user
log=$T/home/.local/state/theos/gh-runner/keep.log
keeper install >"$T/out" 2>&1
check "install: exit" "$?" 0
check "install: runs this checkout's keep, without ./dev" \
  "$(grep -c "^ExecStart=\"$T/repo/hack/gh-runner/gh-runner.sh\" keep$" "$units/theos-gh-runner-keep.service")" 1
check "install: a oneshot" "$(grep -c '^Type=oneshot$' "$units/theos-gh-runner-keep.service")" 1
check "install: keeps the installer's PATH" \
  "$(grep -cF "Environment=\"PATH=$T/bin:$PATH\"" "$units/theos-gh-runner-keep.service")" 1
check "install: logs next to the record" "$(grep -c "^Standard\(Output\|Error\)=append:$log$" "$units/theos-gh-runner-keep.service")" 2
check "install: every 2 minutes" "$(grep -c '^OnCalendar=\*:0/2$' "$units/theos-gh-runner-keep.timer")" 1
check "install: enables and starts the timer" "$(paste -sd';' "$F/calls")" \
  "systemctl --user show-environment;systemctl --user daemon-reload;systemctl --user enable --now theos-gh-runner-keep.timer;loginctl enable-linger $(id -un)"
check "install: says where it logs" "$(grep -c "log: $log" "$T/out")" 1

# uninstall: disabled, stopped and gone.
: >"$F/calls"
keeper uninstall >/dev/null 2>&1
check "uninstall: exit" "$?" 0
check "uninstall: disables and stops the timer" "$(grep -c 'disable --now theos-gh-runner-keep.timer' "$F/calls")" 1
check "uninstall: removes the units" "$(ls "$units")" ""

# install without systemd: a crontab line, the user's other lines kept;
# installing again doesn't add another; uninstall removes it.
touch "$F/no-systemd"
echo '0 3 * * * backup' >"$F/crontab"
keeper install >/dev/null 2>&1
keeper install >/dev/null 2>&1
check "install, cron: keeps the other lines" "$(grep -c backup "$F/crontab")" 1
check "install, cron: keep every 2 minutes, without ./dev" \
  "$(grep -c "^\*/2 \* \* \* \* PATH=.* $T/repo/hack/gh-runner/gh-runner.sh keep >>$log 2>&1 # theos gh-runner keeper$" "$F/crontab")" 1
keeper uninstall >/dev/null 2>&1
check "uninstall, cron: removes the line" "$(paste -sd';' "$F/crontab")" "0 3 * * * backup"

# Not from a worktree: it changes branches, and goes.
git init -q "$T/main"
git -C "$T/main" -c user.name=t -c user.email=t@t commit -q --allow-empty -m init
git -C "$T/main" worktree add -q "$T/main-wt"
mkdir -p "$T/main-wt/hack/gh-runner"
cp "$SCRIPT" "$T/main-wt/hack/gh-runner/"
PATH="$T/bin:$PATH" HOME="$T/home" "$T/main-wt/hack/gh-runner/gh-runner.sh" install >"$T/out" 2>&1
check "install from a worktree: refused" "$? | $(cat "$T/out")" "1 | gh-runner: install the keeper from the main checkout, not a worktree"

exit $fail
