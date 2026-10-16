#!/usr/bin/env bash
# Tests for hack/gh-runner/gh-runner.sh: N runners up, fewer up, down, the
# single runner from before there were several, and status with Docker
# unreachable, a container down and a runner offline, with docker, gh and stat
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
      printf '%s\t%s\tc4factory/gh-runner:dev\n' "${c##*/}" "$st"; done | if [[ $* == *Status* ]]; then cat; else cut -f1; fi ;;
  inspect)
    c=${*: -1}; [ -e "$S/containers/$c" ] || exit 1
    case "$3" in *rev*) cat "$S/containers/$c" ;; *StartedAt*) echo 2026-10-07T00:00:00Z ;; esac ;;
  stop|start) [ -e "$S/containers/$2" ] ;;
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
        c4factory/gh-runner:dev) shift; break ;;
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

gh_runner() { PATH="$T/bin:$PATH" GH_RUNNER_NAME=c4factory-dev GH_RUNNER_WORK="$T/work" "$SCRIPT" "$@"; }
containers() { ls "$S/containers" | sort | paste -sd' '; }
configs() { ls "$S/volumes" | grep -- '-config$' | sort | paste -sd' '; }
regs() { cat "$S"/reg/* 2>/dev/null | sort | paste -sd';'; }

# Three runners: three containers, three registration volumes, three names,
# and the Kind label on runner 1 only.
RUNNERS=3 gh_runner up >"$T/out" 2>&1
check "up 3: exit" "$?" 0
check "up 3: containers" "$(containers)" "c4factory-gh-runner-1 c4factory-gh-runner-2 c4factory-gh-runner-3"
check "up 3: registration volumes" "$(configs)" "c4factory-gh-runner-1-config c4factory-gh-runner-2-config c4factory-gh-runner-3-config"
check "up 3: registrations" "$(regs)" "c4factory-dev-1 c4factory,c4factory-kind;c4factory-dev-2 c4factory;c4factory-dev-3 c4factory"
check "up 3: work directories" "$(ls "$T/work" | paste -sd' ')" "1 2 3"
check "up 3: one shared cache" "$(ls "$S/volumes" | grep -c '^c4factory-gh-runner-cache$')" 1
check "up 3: status says all online" "$(grep -c ' online ' "$T/out")" 3
check "up 3: no wait for a runner that listens" "$(cat "$S/slept" 2>/dev/null)" ""

# Again: nothing registers again or restarts.
RUNNERS=3 gh_runner up >"$T/out" 2>&1
check "up 3 again: no registration" "$(grep -c registering "$T/out")" 0
check "up 3 again: no new container" "$(grep -c 'starting container' "$T/out")" 0

# One: runners 2 and 3 go, from Docker and from GitHub.
RUNNERS=1 gh_runner up >"$T/out" 2>&1
check "up 1 after 3: exit" "$?" 0
check "up 1 after 3: containers" "$(containers)" "c4factory-gh-runner-1"
check "up 1 after 3: registration volumes" "$(configs)" "c4factory-gh-runner-1-config"
check "up 1 after 3: registrations" "$(regs)" "c4factory-dev-1 c4factory,c4factory-kind"
check "up 1 after 3: their tool caches go" "$(ls "$S/volumes" | grep -c toolcache)" 1

gh_runner status >"$T/out" 2>&1
check "status: online" "$? | $(cat "$T/out")" "0 | docker: reachable from this host
container: c4factory-gh-runner-1 up (Up 1 minute, c4factory/gh-runner:dev)
github: c4factory-dev-1 online busy=false labels=self-hosted,Linux,X64,c4factory,c4factory-kind"

# Docker not reachable (Docker Desktop stopped): said, with what GitHub sees
# and the fix, from the distro WSL names.
touch "$S/no-docker" "$S/offline-c4factory-dev-1"
WSL_DISTRO_NAME=Ubuntu gh_runner status >"$T/out" 2>&1
check "status: no Docker" "$? | $(cat "$T/out")" "1 | docker: not reachable from WSL distro Ubuntu; the runners stop with it
github: c4factory-dev-1 offline busy=false
fix: start Docker Desktop, with WSL integration on for Ubuntu (Settings > Resources > WSL integration); the runners restart with it. Then make gh-runner-status again (DEVELOPMENT.md)."
rm "$S/no-docker"

# A container stopped, GitHub seeing its runner offline: both said, and the fix.
touch "$S/exited-c4factory-gh-runner-1"
gh_runner status >"$T/out" 2>&1
check "status: a container down" "$? | $(sed -n '2p;4p' "$T/out")" "1 | container: c4factory-gh-runner-1 down (Exited (0) 2 hours ago, c4factory/gh-runner:dev)
fix: make gh-runner RUNNERS=1 starts the containers that are down and registers again the runners GitHub lost; a container that is up while GitHub sees it offline says why in docker logs --tail 50 NAME (DEVELOPMENT.md)."
check "status: GitHub sees it offline" "$(grep -c '^github: c4factory-dev-1 offline' "$T/out")" 1
rm "$S/exited-c4factory-gh-runner-1" "$S/offline-c4factory-dev-1"

# Down removes them all; the shared cache stays.
RUNNERS=3 gh_runner up >/dev/null 2>&1
gh_runner down >"$T/out" 2>&1
check "down: exit" "$?" 0
check "down: no containers" "$(containers)" ""
check "down: no registration volumes" "$(configs)" ""
check "down: no registrations" "$(regs)" ""
check "down: shared cache stays" "$(ls "$S/volumes" | grep -c '^c4factory-gh-runner-cache$')" 1
gh_runner status >/dev/null 2>&1
check "status: none is a failure" "$?" 1

# A runner 1 registered without the Kind label registers again.
RUNNERS=1 gh_runner up >/dev/null 2>&1
echo "c4factory-dev-1 c4factory" >"$S/reg/c4factory-gh-runner-1-config"
RUNNERS=1 gh_runner up >"$T/out" 2>&1
check "wrong labels: registers again" "$(regs)" "c4factory-dev-1 c4factory,c4factory-kind"
check "wrong labels: new container" "$(grep -c 'starting container' "$T/out")" 1
gh_runner down >/dev/null 2>&1

# The single runner from before: removed, and runner 1 takes its place.
cat "$S/image" >"$S/containers/c4factory-gh-runner"
touch "$S/volumes/c4factory-gh-runner-config" "$S/volumes/c4factory-gh-runner-toolcache"
echo "c4factory-dev c4factory" >"$S/reg/c4factory-gh-runner-config"
RUNNERS=1 gh_runner up >"$T/out" 2>&1
check "migrate: exit" "$?" 0
check "migrate: containers" "$(containers)" "c4factory-gh-runner-1"
check "migrate: registrations" "$(regs)" "c4factory-dev-1 c4factory,c4factory-kind"
check "migrate: old volumes go" "$(ls "$S/volumes" | grep -c '^c4factory-gh-runner-\(config\|toolcache\)$')" 0

RUNNERS=0 gh_runner up >/dev/null 2>&1
check "RUNNERS=0 refused" "$?" 64
RUNNERS=x gh_runner up >/dev/null 2>&1
check "RUNNERS=x refused" "$?" 64

exit $fail
