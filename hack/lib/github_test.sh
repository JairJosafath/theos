#!/usr/bin/env bash
# Tests for the needs/cluster rules in hack/lib/github.sh (pick, take, label)
# and the cluster pool's leases (acquire, wait, exit 4, stale lease, two leases
# on two clusters, a third waiting, a released cluster reused, a missing one
# created; release on handoff, close and release),
# for flush releasing the claim of the handoff it posts,
# for proposed listing the issues the relay's triage reads, for close keeping the claim when the close fails, for take releasing a claim on a closed issue, and for pick's order, against a stubbed GitHub and a throwaway repository with two worktrees, a fake kind and a fake make.
# Run: hack/lib/github_test.sh
. "$(dirname "$0")/github.sh"
unset C4_CLUSTER # ./dev setup --lite sets it; each test sets its own
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
git init -q "$T/main"
git -C "$T/main" -c user.name=t -c user.email=t@t commit -q --allow-empty -m init
git -C "$T/main" worktree add -q --detach "$T/wt2"
git -C "$T/main" worktree add -q --detach "$T/wt3"
GH_LIB_ROOT=$T/main
GH_CLAIM_FILE=$T/main/.relay/claim
GH_PENDING_DIR=$T/main/.relay
GH_CLAIM_SETTLE=0
now=$(date -u +%Y-%m-%dT%H:%M:%SZ)
gh_token() { :; }
gh_api() {
  local url
  for a in "$@"; do case $a in repos/*) url=$a ;; esac; done
  if [[ " $* " == *" --method POST "* || " $* " == *" --method DELETE "* ]]; then
    local m=POST in=
    [[ " $* " == *" --method DELETE "* ]] && m=DELETE
    [[ " $* " == *" --input - "* ]] && in=" $(jq -c . </dev/stdin)"
    echo "$m $url$in" >>"$T/calls"
    [ "$m" = DELETE ] && return 0
    [[ $url == */issues ]] && { echo 99; return 0; }
  fi
  case $url in
    */issues/9[0-9]) cat "$T/issue-${url##*/}.json" ;;
    */labels/user%2Flaptop-2) echo '{}' ;;
    */labels/user%2F*) return 1 ;;
    */issues/50/labels*) printf 'needs/cluster\nstatus/in-progress\n' ;;
    */issues/51/labels*) printf 'status/in-progress\n' ;;
    */issues/60) echo '{"number":60,"title":"Deploy","labels":[{"name":"needs/cluster"},{"name":"kind/feature"}]}' ;;
    */issues/71) return 1 ;; # closing #71 fails
    */issues/73) echo closed ;; # --jq .state, as take asks
    */issues/74) echo open ;;
    */issues/61) echo '{"number":61,"title":"Docs","labels":[{"name":"kind/docs"}]}' ;;
    */issues/77/comments*) return 1 ;; # posting on #77 fails
    */issues/*/comments*)
      if [[ " $* " == *" POST "* ]]; then touch "$T/posted"; echo 999; return; fi
      [ -e "$T/posted" ] || { echo '[[]]'; return; }
      echo "[[{\"id\":999,\"created_at\":\"$now\",\"updated_at\":\"$now\",\"html_url\":\"u\",\"user\":{\"login\":\"x\"},\"body\":\"<!-- c4-claim -->\\n**otter-6x** works on this issue\"}]]" ;;
    */issues/comments/1) # the claim() comment: heron-73 and heron-74 still hold theirs
      if [[ " $* " == *" PATCH "* ]]; then cat >"$T/edited"; return; fi
      echo '{"id":1,"updated_at":"'"$now"'","body":"<!-- c4-claim -->\n**heron-73** works on this issue"}' |
        { [ -e "$T/hold74" ] && sed 's/heron-73/heron-74/' || cat; } ;;
    */issues/comments*) echo '[[]]' ;;
    */issues\?state=open*) [ -e "$T/no-open" ] && return 0; [ -n "${OPEN:-}" ] && { echo "$OPEN"; return; }; echo '[[{"number":60,"title":"Deploy","html_url":"u60","body":"","labels":[{"name":"needs/cluster"},{"name":"status/ready"}]},{"number":61,"title":"Docs","html_url":"u61","body":"","labels":[{"name":"status/ready"}]}]]' ;;
    *) return 0 ;;
  esac
}
gh_set_status() { :; }
fail=0
check() { if [ "$2" = "$3" ]; then echo "ok   $1"; else echo "FAIL $1: got [$2] want [$3]"; fail=1; fi; }
claim() { mkdir -p "$1/.relay"; jq -n --argjson i "$2" --argjson b "$3" '{issue:$i,id:1,name:"heron-\($i)",branch:"b",labels:[],beat:$b}' >"$1/.relay/claim"; }
clear() { rm -rf "$T"/{main,wt2,wt3}/.relay; }

# The cluster pool. as WT CMD...: run CMD as the checkout WT would. A fake kind
# lists $T/kind-clusters; a fake make records what it was asked and creates the cluster.
as() { local wt=$1; shift; GH_PENDING_DIR=$wt/.relay GH_CLAIM_FILE=$wt/.relay/claim "$@"; }
LEASE=$T/main/.git/c4-cluster.lease
LEASE2=$T/main/.git/c4-cluster-c4factory-2.lease
lease() { jq -n --arg wt "$1" --argjson i "$2" --argjson b "$3" '{name:"heron-\($i)",issue:$i,worktree:$wt,beat:$b}' >"${4:-$LEASE}"; }
mkdir -p "$T/bin"
printf '#!/bin/sh\n[ "$1 $2" = "get clusters" ] && cat "%s/kind-clusters"\n' "$T" >"$T/bin/kind"
printf '#!/bin/sh\necho "$*" >>"%s/made"\nfor a; do case $a in KIND_CLUSTER=*) echo "${a#*=}" >>"%s/kind-clusters" ;; esac; done\n[ ! -e "%s/make-fails" ]\n' "$T" "$T" "$T" >"$T/bin/make"
chmod +x "$T"/bin/*
export KIND=$T/bin/kind C4_MAKE=$T/bin/make
echo c4factory >"$T/kind-clusters"
clear

# One cluster (C4_CLUSTERS=1): acquire, wait, exit 4, stale lease, release, beat.
export C4_CLUSTERS=1
check "lease file in the git common dir" "$(as "$T/wt2" gh_lease_file c4factory)" "$LEASE"
check "second cluster's lease beside it" "$(as "$T/wt2" gh_lease_file c4factory-2)" "$LEASE2"
as "$T/wt2" gh_cluster holder >/dev/null; check "holder, none" $? 1
check "name, no lease: the default" "$(as "$T/wt2" gh_cluster name)" c4factory
claim "$T/wt2" 50 "$(date +%s)"
out=$(as "$T/wt2" gh_cluster acquire --wait 0 2>"$T/err"); check "acquire, free" "$? $out" "0 c4factory"
case $(cat "$T/err") in "github: Kind cluster c4factory is leased to heron-50 (#50, $T/wt2), heartbeat 0m ago."*) r=yes ;; *) r=no ;; esac
check "acquire names the lease" $r yes
check "lease names cluster, agent, issue, worktree" "$(jq -c '[.cluster, .name, .issue, .worktree]' "$LEASE")" "[\"c4factory\",\"heron-50\",50,\"$T/wt2\"]"
check "holder, other checkout" "$(gh_cluster holder)" "c4factory: heron-50 (#50, $T/wt2), heartbeat 0m ago"
check "holder, this checkout" "$(as "$T/wt2" gh_cluster holder)" "c4factory: heron-50 (#50, $T/wt2), heartbeat 0m ago (this checkout)"
check "acquire again, own lease" "$(as "$T/wt2" gh_cluster acquire --wait 0 2>/dev/null)" c4factory
gh_cluster acquire --wait 0 2>"$T/err"; check "acquire, held: exit 4" $? 4
grep -q "c4factory: heron-50 (#50, $T/wt2)" "$T/err"; check "acquire, held: names the holder" $? 0
gh_cluster acquire --wait x 2>/dev/null; check "acquire, bad wait" $? 64
C4_LEASE_TTL=bogus gh_cluster acquire --wait 0 2>/dev/null; check "acquire, bad TTL" $? 64
C4_CLUSTERS=0 gh_cluster acquire --wait 0 2>/dev/null; check "acquire, bad C4_CLUSTERS" $? 64
check "bad TTL: the lease counts as live" "$(C4_LEASE_TTL=bogus gh_lease_holder c4factory | cut -d' ' -f1)" heron-50
(sleep 1; as "$T/wt2" gh_cluster release >/dev/null) &
t0=$(date +%s)
GH_LEASE_POLL=0.2 gh_cluster acquire --wait 10s >/dev/null 2>"$T/err"; check "acquire waits for a release" $? 0
wait
check "acquire waited" "$(($(date +%s) - t0 >= 1))" 1
grep -q "every Kind cluster in the pool is leased (c4factory: heron-50.*); waiting up to 10s" "$T/err"; check "acquire says it waits" $? 0
check "acquire after the wait: lease is main's" "$(jq -r .worktree "$LEASE")" "$T/main"
check "name, own lease" "$(gh_cluster name)" c4factory
gh_cluster release >/dev/null; check "release" "$([ -e "$LEASE" ] && echo kept || echo gone)" gone
lease "$T/wt2" 50 $(($(date +%s) - 3 * 3600))
gh_cluster holder >/dev/null; check "stale lease: no holder" $? 1
gh_cluster acquire --wait 0 >/dev/null 2>&1; check "stale lease taken over" "$? $(jq -r .worktree "$LEASE")" "0 $T/main"
lease "$T/gone" 50 "$(date +%s)"
gh_cluster acquire --wait 0 >/dev/null 2>&1; check "lease of a removed worktree taken over" "$? $(jq -r .worktree "$LEASE")" "0 $T/main"
lease "$T/wt2" 50 "$(date +%s)"
gh_cluster release >/dev/null; check "release leaves another checkout's lease" "$(jq -r .worktree "$LEASE")" "$T/wt2"
lease "$T/main" 50 $(($(date +%s) - 3600))
gh_beat; check "beat refreshes the lease" "$(($(date +%s) - $(jq .beat "$LEASE") < 5))" 1
lease "$T/wt2" 50 $(($(date +%s) - 3600)); b=$(jq .beat "$LEASE")
gh_beat; check "beat leaves another checkout's lease" "$(jq .beat "$LEASE")" "$b"
rm -f "$LEASE"

# A pool of two: two leases at once get different clusters, a third waits, a
# released cluster is reused, and a missing one is created on its first lease.
export C4_CLUSTERS=2
clear; rm -f "$T/made"; claim "$T/wt2" 50 "$(date +%s)"; claim "$T/wt3" 51 "$(date +%s)"
check "pool, c4factory-2 not created" "$(gh_cluster pool | tail -1)" "c4factory-2: free, not created yet (its first lease creates it)"
check "two at once: the first gets c4factory" "$(as "$T/wt2" gh_cluster acquire --wait 0 2>/dev/null)" c4factory
check "two at once: no cluster made for an existing one" "$(cat "$T/made" 2>/dev/null)" ""
out=$(as "$T/wt3" gh_cluster acquire --wait 0 2>"$T/err"); check "two at once: the second gets c4factory-2" "$? $out" "0 c4factory-2"
check "a missing cluster is created on its first lease" "$(cat "$T/made")" "-C $T/main cluster gitea seed KIND_CLUSTER=c4factory-2"
grep -q "Kind cluster c4factory-2 does not exist yet; creating it" "$T/err"; check "acquire says it creates" $? 0
check "second lease names its cluster" "$(jq -r '"\(.cluster) \(.worktree)"' "$LEASE2")" "c4factory-2 $T/wt3"
check "name, each checkout its own" "$(as "$T/wt2" gh_cluster name) $(as "$T/wt3" gh_cluster name)" "c4factory c4factory-2"
check "holder lists both" "$(gh_cluster holder | cut -d' ' -f1-2 | tr '\n' ' ')" "c4factory: heron-50 c4factory-2: heron-51 "
check "holder NAME" "$(gh_cluster holder c4factory-2 | cut -d: -f1)" c4factory-2
check "pool, both leased" "$(gh_cluster pool | grep -c ': leased to ')" 2
gh_cluster acquire --wait 0 2>"$T/err"; check "a third: exit 4" $? 4
grep -q "c4factory: heron-50.*; c4factory-2: heron-51" "$T/err"; check "a third: names both holders" $? 0
(sleep 1; as "$T/wt3" gh_cluster release >/dev/null) &
out=$(GH_LEASE_POLL=0.2 gh_cluster acquire --wait 10s 2>/dev/null); check "a third waits, then reuses the released cluster" "$? $out" "0 c4factory-2"
wait
check "reused cluster not created again" "$(wc -l <"$T/made")" 1
as "$T/wt2" gh_cluster release >/dev/null
check "release leaves the other checkout's cluster" "$([ -e "$LEASE" ] && echo kept || echo gone) $(jq -r .worktree "$LEASE2")" "gone $T/main"
gh_cluster release >/dev/null
echo c4factory-2 >"$T/kind-clusters"
check "a free cluster that exists goes first" "$(gh_cluster acquire --wait 0 2>/dev/null)" c4factory-2
gh_cluster release >/dev/null
: >"$T/kind-clusters"; touch "$T/make-fails"
gh_cluster acquire --wait 0 >/dev/null 2>"$T/err"; check "create fails: exit 1" $? 1
check "create fails: lease dropped" "$([ -e "$LEASE" ] && echo kept || echo gone)" gone
rm -f "$T/make-fails"; echo c4factory >"$T/kind-clusters"
lease "$T/main" 50 "$(date +%s)" "$LEASE2"
check "pool shrunk: a lease outside it is dropped for one inside" "$(C4_CLUSTERS=1 gh_cluster acquire --wait 0 2>/dev/null) $([ -e "$LEASE2" ] && echo kept || echo gone)" "c4factory gone"
gh_cluster release >/dev/null

# needs/cluster issues are picked and taken while another checkout holds the lease.
clear; lease "$T/wt2" 50 "$(date +%s)"
check "pick, lease held elsewhere" "$(gh_pick | cut -f1)" 60
check "pick, no cluster" "$(C4_CLUSTER=none gh_pick | cut -f1)" 61
rm -f "$T/posted"; C4_CLUSTER=none gh_take 60 >/dev/null 2>&1; check "take 60, no cluster" $? 4
rm -f "$T/posted"; gh_take 60 >/dev/null 2>&1; check "take 60, lease held elsewhere" $? 0
rm -f "$T/main/.relay/claim"
gh_label 61 needs/cluster >/dev/null 2>&1; check "label needs/cluster, lease held elsewhere" $? 0
C4_CLUSTER=none gh_label 61 needs/cluster >/dev/null 2>&1; check "label needs/cluster, no cluster" $? 0
gh_label 61 kind/docs >/dev/null 2>&1; check "label other" $? 0

# release, handoff, close and needs-human drop this checkout's lease.
clear; claim "$T/main" 72 "$(date +%s)"; lease "$T/main" 72 "$(date +%s)"
echo landed | gh_close 72 - >/dev/null 2>&1; check "close drops the lease" "$([ -e "$LEASE" ] && echo kept || echo gone)" gone
clear; claim "$T/main" 71 "$(date +%s)"; lease "$T/main" 71 "$(date +%s)"
echo landed | gh_close 71 - >/dev/null 2>&1; check "close fails, lease kept" "$([ -e "$LEASE" ] && echo kept || echo gone)" kept
clear; lease "$T/main" 73 "$(date +%s)"
echo h | gh_handoff 73 - >/dev/null 2>&1; check "handoff drops the lease" "$([ -e "$LEASE" ] && echo kept || echo gone)" gone
clear; lease "$T/main" 77 "$(date +%s)"
echo h | gh_handoff 77 - >/dev/null 2>&1; check "handoff kept for later drops the lease" "$? $([ -e "$LEASE" ] && echo kept || echo gone)" "3 gone"
clear; lease "$T/main" 73 "$(date +%s)"
gh_release >/dev/null 2>&1; check "release without a claim drops the lease" "$([ -e "$LEASE" ] && echo kept || echo gone)" gone
rm -f "$LEASE" "$T/posted" "$T/edited"

rm -rf "$T"/{main,wt2,wt3}/.relay "$T/posted"
flock "$T/main/.git/c4-take.lock" sleep 2 &
sleep 0.2
t0=$(date +%s)
gh_take 61 >/dev/null 2>&1
check "take waits for another checkout's take" "$(( $(date +%s) - t0 >= 1 ))" 1
wait
rm -rf "$T"/{main,wt2,wt3}/.relay
claim "$T/main" 71 "$(date +%s)"
echo landed | gh_close 71 - >/dev/null 2>&1; check "close fails" $? 1
check "close fails, claim kept" "$(jq .issue "$T/main/.relay/claim")" 71
claim "$T/main" 72 "$(date +%s)"
echo landed | gh_close 72 - >/dev/null 2>&1; check "close" $? 0
check "close, claim released" "$([ -e "$T/main/.relay/claim" ] && echo kept || echo released)" released
rm -rf "$T"/{main,wt2,wt3}/.relay "$T/posted" "$T/edited"
claim "$T/main" 73 "$(date +%s)"
gh_take 61 >/dev/null 2>&1; check "take, own claim on a closed issue" $? 0
check "take, closed issue's claim released" "$(jq -r .body "$T/edited" | grep -c 'heron-73\*\* released this issue: finished')" 1
check "take, new claim" "$(jq .issue "$T/main/.relay/claim")" 61
rm -rf "$T"/{main,wt2,wt3}/.relay "$T/posted" "$T/edited"
touch "$T/hold74"; claim "$T/main" 74 "$(date +%s)"
gh_take 61 >/dev/null 2>&1; check "take, own claim on an open issue" $? 1
check "take, open issue's claim kept" "$(jq .issue "$T/main/.relay/claim")" 74
rm -f "$T/hold74"

# Flush: a kept handoff, once posted, releases this checkout's claim on its
# issue as handed off, and leaves a claim on another issue alone.
pending() { mkdir -p "$T/main/.relay"; echo handoff >"$T/main/.relay/pending-handoff-$1.md"; }
rm -rf "$T"/{main,wt2,wt3}/.relay "$T/posted" "$T/edited"
claim "$T/main" 73 "$(date +%s)"; pending 73
out=$(gh_flush 2>&1); check "flush" $? 0
check "flush prints the URL" "$(head -1 <<<"$out")" "posted the pending handoff on #73: 999"
check "flush says it released" "$(sed -n 2p <<<"$out")" "released #73: handed off"
check "flush, claim released as handed off" "$(jq -r .body "$T/edited" | grep -c 'heron-73\*\* released this issue: handed off')" 1
check "flush, claim file gone" "$([ -e "$T/main/.relay/claim" ] && echo kept || echo released)" released
check "flush, pending file gone" "$([ -e "$T/main/.relay/pending-handoff-73.md" ] && echo kept || echo gone)" gone
rm -rf "$T"/{main,wt2,wt3}/.relay "$T/posted" "$T/edited"
touch "$T/hold74"; claim "$T/main" 74 "$(date +%s)"; pending 73
out=$(gh_flush 2>&1); check "flush, claim on another issue" "$? $out" "0 posted the pending handoff on #73: 999"
check "flush, other claim kept" "$(jq .issue "$T/main/.relay/claim")" 74
check "flush, other claim not edited" "$([ -e "$T/edited" ] && echo edited || echo untouched)" untouched
rm -f "$T/hold74"
rm -rf "$T"/{main,wt2,wt3}/.relay
claim "$T/main" 77 "$(date +%s)"; pending 77
gh_flush >/dev/null 2>&1; check "flush fails" $? 1
check "flush fails, claim kept" "$(jq .issue "$T/main/.relay/claim")" 77
check "flush fails, pending kept" "$([ -e "$T/main/.relay/pending-handoff-77.md" ] && echo kept || echo gone)" kept
rm -rf "$T"/{main,wt2,wt3}/.relay

# Pick order: an in-progress issue nobody holds, else ready + urgent, else
# ready, oldest first; proposed and needs-human issues are never picked, nor
# ones that depend on an open issue.
rm -rf "$T"/{main,wt2,wt3}/.relay "$T/posted" "$T/edited"
issues() { jq -c '[[.[] | {number: .[0], title: "t", html_url: "u", body: (.[2] // ""), labels: (.[1] | map({name: .}))}]]'; }
all='[[80, ["status/needs-human"]], [81, ["status/proposed"]], [82, ["status/ready"]],
  [83, ["status/ready", "priority/urgent"], "Depends on #81"], [84, ["status/ready", "priority/urgent"]],
  [85, ["status/in-progress"]]]'
check "pick, in-progress first" "$(OPEN=$(issues <<<"$all") gh_pick | cut -f1,4)" "85	in-progress"
check "pick, then ready + urgent" "$(OPEN=$(jq 'map(select(.[0] != 85))' <<<"$all" | issues) gh_pick | cut -f1,4)" "84	ready-urgent"
check "pick, then ready" "$(OPEN=$(jq 'map(select(.[0] < 83))' <<<"$all" | issues) gh_pick | cut -f1,4)" "82	ready"
OPEN=$(jq 'map(select(.[0] < 82))' <<<"$all" | issues) gh_pick >/dev/null
check "pick, only proposed and needs-human" $? 1

# Proposed: the open status/proposed issues, oldest first, for the relay's triage.
check "proposed" "$(OPEN=$(jq '. + [[86, ["status/proposed", "kind/bug"]]]' <<<"$all" | issues) gh_proposed | tr '\n' ' ')" "81 86 "
check "proposed, none" "$(OPEN=$(jq 'map(select(.[0] != 81))' <<<"$all" | issues) gh_proposed)" ""

# Intake: labels an issue by where it came from.
R=repos/JairJosafath/c4factory
issue() { # N ASSOC LABELS BODY
  jq -n --argjson n "$1" --arg a "$2" --arg b "$4" --arg l "$3" \
    '{number: $n, author_association: $a, body: $b, labels: ($l | split(" ") | map(select(. != "") | {name: .}))}' >"$T/issue-$1.json"
  : >"$T/calls"
}
calls() { cat "$T/calls"; }
form=$'### Reported by\n\nlaptop-2\n\n### What happened\n\nCrew stalls'
check "reporter form" "$(gh_reporter <<<"$form")" laptop-2
check "reporter crlf" "$(gh_reporter <<<$'### Reported by  \r\n\r\nlaptop-2\r\n')" laptop-2
check "reporter none" "$(gh_reporter <<<$'### Reported by\n\n_No response_\n')" ""
check "reporter next heading" "$(gh_reporter <<<$'### Reported by\n\n### What happened\nx')" ""
check "fenced heading" "$(gh_reporter <<<$'```\n### Reported by\n\nlaptop-2\n```')" ""
check "slug" "$(gh_user_slug '  WSL_box.2 ')" wsl-box-2
check "slug empty" "$(gh_user_slug '!!!')" ""
check "slug long" "$(gh_user_slug "$(printf 'a%.0s' {1..39})-bbbb")" "$(printf 'a%.0s' {1..39})"

issue 90 OWNER '' "$form"; gh_intake 90 >/dev/null
check "new report" "$(calls)" "POST $R/issues/90/labels {\"labels\":[\"user/laptop-2\",\"source/user\",\"status/proposed\"]}"
issue 91 OWNER '' "${form/laptop-2/laptop-3}"; gh_intake 91 >/dev/null
check "new device" "$(calls | sed 's|^\(POST [^ ]*\) .*|\1|')" "POST $R/labels
POST $R/issues/91/labels"
check "new device labels" "$(calls | tail -1)" "POST $R/issues/91/labels {\"labels\":[\"user/laptop-3\",\"source/user\",\"status/proposed\"]}"
issue 92 OWNER 'user/laptop-3 source/dev status/ready' "$form"; gh_intake 92 >/dev/null
check "reporter changed" "$(calls)" "DELETE $R/issues/92/labels/user%2Flaptop-3
DELETE $R/issues/92/labels/source%2Fdev
POST $R/issues/92/labels {\"labels\":[\"user/laptop-2\",\"source/user\"]}"
issue 93 OWNER '' 'Plain text'; gh_intake 93 >/dev/null
check "dev issue" "$(calls)" "POST $R/issues/93/labels {\"labels\":[\"source/dev\",\"status/proposed\"]}"
issue 94 OWNER 'user/laptop-2 source/user status/proposed' "$form"; gh_intake 94 >/dev/null
check "idempotent" "$(calls)" ""
issue 95 NONE '' "$form"; gh_intake 95 >/dev/null
check "stranger" "$? $(calls)" "0 "

: >"$T/calls"; touch "$T/no-open"
echo x >"$T/body"
gh_create T "$T/body" status/proposed kind/bug >/dev/null
check "create adds source/dev" "$(calls | jq -c '.labels' <<<"$(sed 's/^POST [^ ]* //' "$T/calls")")" '["status/proposed","kind/bug","source/dev"]'
: >"$T/calls"
gh_create T "$T/body" source/user >/dev/null
check "create keeps source/user" "$(sed 's/^POST [^ ]* //' "$T/calls" | jq -c .labels)" '["source/user"]'
rm -f "$T/no-open"
exit $fail
