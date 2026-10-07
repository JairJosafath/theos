#!/usr/bin/env bash
# Tests for hack/gh-runner/gh-runner.sh: N runners up, fewer up, down, and the
# single runner from before there were several, with docker, gh and stat
# faked. The fake Docker keeps its containers, volumes and registrations as
# files; the fake GitHub lists the registrations, each online.
# Run: hack/gh-runner/gh-runner_test.sh
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
SCRIPT=$(cd "$(dirname "$0")" && pwd)/gh-runner.sh
fail=0
check() { if [ "$2" = "$3" ]; then echo "ok   $1"; else echo "FAIL $1: got [$2] want [$3]"; fail=1; fi; }

export S=$T/state
mkdir -p "$T/bin" "$S/containers" "$S/volumes" "$S/reg"

cat >"$T/bin/docker" <<'FAKE'
#!/usr/bin/env bash
# The fake Docker: what gh-runner.sh calls, on files under $S.
arg() { local want=$1; shift; while [ $# -gt 0 ]; do [ "$1" = "$want" ] && { echo "$2"; return; }; shift; done; }
case "$1" in
  build) rev=$(arg --label "$@"); echo "${rev#*=}" >"$S/image" ;;
  image) [ -s "$S/image" ] || exit 1; [ "$3" = -f ] && cat "$S/image"; exit 0 ;;
  volume)
    case "$2" in
      create) touch "$S/volumes/$3" ;;
      rm) [ -e "$S/volumes/$3" ] || exit 1; rm "$S/volumes/$3" ;;
      inspect) [ -e "$S/volumes/$3" ] ;;
      ls) ls "$S/volumes" ;;
    esac ;;
  ps) for c in "$S"/containers/*; do [ -e "$c" ] && printf '%s\tUp 1 minute\tc4factory/gh-runner:dev\n' "${c##*/}"; done | if [[ $* == *Status* ]]; then cat; else cut -f1; fi ;;
  inspect)
    c=${*: -1}; [ -e "$S/containers/$c" ] || exit 1
    case "$3" in *rev*) cat "$S/containers/$c" ;; *StartedAt*) echo 2026-10-07T00:00:00Z ;; esac ;;
  stop|start) [ -e "$S/containers/$2" ] ;;
  rm) rm "$S/containers/$2" ;;
  logs) echo "Listening for Jobs" ;;
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
      jq -n --arg n "$n" --arg l "$l" '{id: 7, name: $n, status: "online", busy: false,
        labels: (["self-hosted", "Linux", "X64"] | map({id: 0, name: ., type: "read-only"}))
          + ($l | split(",") | map({id: 0, name: ., type: "custom"}))}'
    done | jq -s '{runners: .}' | jq -r "$jq_expr" ;;
esac
FAKE

printf '#!/bin/sh\necho 999\n' >"$T/bin/stat"
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

gh_runner status >/dev/null 2>&1
check "status: online" "$?" 0

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
