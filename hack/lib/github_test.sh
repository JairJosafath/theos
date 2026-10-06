#!/usr/bin/env bash
# Tests for the needs/cluster rules in hack/lib/github.sh (pick, take, label)
# and the cluster pool's leases (acquire, wait, exit 4, stale lease, two leases
# on two clusters, a third waiting, a released cluster reused, a missing one
# created, --fresh recreating only the cluster it leased, a bare cluster taken last and recreated; release on handoff, close and release),
# for flush releasing the claim of the handoff it posts,
# for blocked naming what holds back the issues pick skips and cycles reporting the issues that wait on each other,
# for status needs-maintainer releasing the claim and lease, for escalated listing the issues waiting on the maintainer,
# for proposed listing the issues the relay's triage reads, for report creating the pinned Maintainer report once and then editing it, for activity leaving the report out, for close keeping the claim when the close fails, for take releasing a claim on a closed issue, and for pick's order, against a stubbed GitHub and a throwaway repository with two worktrees, a fake kind and a fake make.
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
    */issues/*/events*) local e=${url#*/issues/}; cat "$T/events-${e%%/*}.json" 2>/dev/null || echo '[[]]' ;;
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
    */issues/comments\?since*) echo "${CLAIMS:-[[]]}" ;;
    */issues/comments*) echo '[[]]' ;;
    */issues\?state=open*) [ -e "$T/no-open" ] && return 0; [ -n "${OPEN:-}" ] && { echo "$OPEN"; return; }; echo '[[{"number":60,"title":"Deploy","html_url":"u60","body":"","labels":[{"name":"needs/cluster"},{"name":"status/ready"}]},{"number":61,"title":"Docs","html_url":"u61","body":"","labels":[{"name":"status/ready"}]}]]' ;;
    *) return 0 ;;
  esac
}
eval "real_set_status() $(declare -f gh_set_status | tail -n +2)"
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
cat >"$T/bin/make" <<EOF
#!/bin/sh
echo "\$*" >>"$T/made"
[ ! -e "$T/make-fails" ] || exit 1
for a; do case \$a in KIND_CLUSTER=*) c=\${a#*=} ;; esac; done
if [ "\$3" = cluster-delete ]; then grep -vxF "\$c" "$T/kind-clusters" >"$T/kind-clusters.new"; mv "$T/kind-clusters.new" "$T/kind-clusters"
else echo "\$c" >>"$T/kind-clusters"; fi
EOF
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

# --fresh: a cluster not created yet goes first; else the leased one, and only
# it, is deleted and created again; with every cluster held it waits and exits 4.
rm -f "$T/made"; echo c4factory >"$T/kind-clusters"
out=$(gh_cluster acquire --wait 0 --fresh 2>"$T/err"); check "fresh: a not-yet-created cluster first" "$? $out" "0 c4factory-2"
check "fresh, not created: created, nothing deleted" "$(cat "$T/made")" "-C $T/main cluster gitea seed KIND_CLUSTER=c4factory-2"
gh_cluster release >/dev/null; rm -f "$T/made"
lease "$T/wt2" 50 "$(date +%s)"
out=$(gh_cluster acquire --fresh --wait 0 2>"$T/err"); check "fresh: an existing free cluster when all exist" "$? $out" "0 c4factory-2"
check "fresh: recreates only the cluster it leased" "$(cat "$T/made")" "-C $T/main cluster-delete KIND_CLUSTER=c4factory-2
-C $T/main cluster gitea seed KIND_CLUSTER=c4factory-2"
check "fresh: the held cluster is left alone" "$(sort "$T/kind-clusters" | tr '\n' ' ') $(jq -r .worktree "$LEASE")" "c4factory c4factory-2  $T/wt2"
grep -q "deleting Kind cluster c4factory-2, which this checkout now leases (no other checkout holds it)" "$T/err"; check "fresh: says what it deletes and why" $? 0
rm -f "$T/made"
check "fresh, own lease: recreated again" "$(gh_cluster acquire --wait 0 --fresh 2>/dev/null) $(head -1 "$T/made")" "c4factory-2 -C $T/main cluster-delete KIND_CLUSTER=c4factory-2"
rm -f "$T/made"
as "$T/wt3" gh_cluster acquire --wait 0 --fresh 2>"$T/err"; check "fresh, every cluster held: exit 4" $? 4
check "fresh, every cluster held: nothing deleted" "$(cat "$T/made" 2>/dev/null)" ""
touch "$T/make-fails"
gh_cluster acquire --wait 0 --fresh >/dev/null 2>"$T/err"; check "fresh, delete fails: exit 1" $? 1
check "fresh, delete fails: lease dropped" "$([ -e "$LEASE2" ] && echo kept || echo gone)" gone
rm -f "$T/make-fails" "$LEASE" "$T/made"; echo c4factory >"$T/kind-clusters"

# --bare, with or without --fresh: as --fresh, and created with make cluster
# only: no testenv Gitea, no seed, for a release install.
out=$(gh_cluster acquire --wait 0 --fresh --bare 2>"$T/err"); check "bare: a not-yet-created cluster first" "$? $out" "0 c4factory-2"
check "bare, not created: make cluster only" "$(cat "$T/made")" "-C $T/main cluster KIND_CLUSTER=c4factory-2"
grep -q "creating Kind cluster c4factory-2 with nothing installed" "$T/err"; check "bare: says it installs nothing" $? 0
rm -f "$T/made"
check "bare, own lease: recreated bare" "$(gh_cluster acquire --wait 0 --bare 2>/dev/null) $(cat "$T/made")" "c4factory-2 -C $T/main cluster-delete KIND_CLUSTER=c4factory-2
-C $T/main cluster KIND_CLUSTER=c4factory-2"
touch "$T/make-fails"; gh_cluster release >/dev/null; : >"$T/kind-clusters"
gh_cluster acquire --wait 0 --bare >/dev/null 2>"$T/err"; check "bare, create fails: exit 1" $? 1
check "bare, create fails: lease dropped" "$([ -e "$LEASE" ] && echo kept || echo gone)" gone
rm -f "$T/make-fails" "$LEASE" "$LEASE2" "$T/made"; echo c4factory >"$T/kind-clusters"

# A cluster made bare stays marked bare after its lease: a plain acquire
# prefers any other free cluster, and recreates a bare one it must take with
# Gitea and the seed; one this checkout already holds is kept, with a warning.
BARE=$T/main/.git/c4-cluster.bare
BARE2=$T/main/.git/c4-cluster-c4factory-2.bare
check "bare mark beside the lease" "$(gh_bare_file c4factory) $(gh_bare_file c4factory-2)" "$BARE $BARE2"
check "bare: the mark outlives the lease" "$([ -e "$BARE2" ] && echo kept || echo gone)" kept
rm -f "$BARE2"; printf 'c4factory\nc4factory-2\n' >"$T/kind-clusters"; touch "$BARE"
check "pool says free, bare" "$(gh_cluster pool | head -1)" "c4factory: free, bare (acquire --bare: no testenv Gitea or seed); a plain acquire takes it last and recreates it"
out=$(gh_cluster acquire --wait 0 2>/dev/null); check "plain: a free testenv cluster before a bare one" "$? $out $(cat "$T/made" 2>/dev/null)" "0 c4factory-2 "
gh_cluster release >/dev/null
echo c4factory >"$T/kind-clusters"
out=$(gh_cluster acquire --wait 0 2>/dev/null); check "plain: a cluster not created yet before a bare one" "$? $out $(cat "$T/made")" "0 c4factory-2 -C $T/main cluster gitea seed KIND_CLUSTER=c4factory-2"
gh_cluster release >/dev/null; rm -f "$T/made"
lease "$T/wt2" 50 "$(date +%s)" "$LEASE2"
out=$(gh_cluster acquire --wait 0 2>"$T/err"); check "plain, only a bare one free: it" "$? $out" "0 c4factory"
check "plain, only a bare one free: recreated as a testenv one" "$(cat "$T/made")" "-C $T/main cluster-delete KIND_CLUSTER=c4factory
-C $T/main cluster gitea seed KIND_CLUSTER=c4factory"
grep -q "Kind cluster c4factory was created bare (acquire --bare), with no testenv Gitea or seed, and is the only free one; recreating it" "$T/err"; check "plain, bare: says why it recreates" $? 0
check "plain, bare recreated: the mark is gone" "$([ -e "$BARE" ] && echo kept || echo gone) $(gh_cluster pool | head -1 | cut -d' ' -f1-3)" "gone c4factory: leased to"
gh_cluster release >/dev/null; rm -f "$T/made"
gh_cluster acquire --wait 0 --bare >/dev/null 2>&1; rm -f "$T/made"
case $(gh_cluster pool | head -1) in "c4factory: leased to "*"(this checkout), bare (acquire --bare: no testenv Gitea or seed)") r=yes ;; *) r=no ;; esac
check "pool says leased, bare" $r yes
out=$(gh_cluster acquire --wait 0 2>"$T/err"); check "plain, own bare lease: kept as it is" "$? $out $(cat "$T/made" 2>/dev/null)" "0 c4factory "
grep -q "which this checkout already leased, was created bare (acquire --bare): no testenv Gitea or seed. Kept as it is; acquire --fresh makes it a testenv one" "$T/err"; check "plain, own bare lease: warns" $? 0
gh_cluster acquire --wait 0 --fresh >/dev/null 2>&1; check "fresh: a bare cluster becomes a testenv one" "$([ -e "$BARE" ] && echo kept || echo gone) $(tail -1 "$T/made")" "gone -C $T/main cluster gitea seed KIND_CLUSTER=c4factory"
rm -f "$LEASE" "$LEASE2" "$BARE" "$BARE2" "$T/made"; echo c4factory >"$T/kind-clusters"

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
clear; claim "$T/main" 51 "$(date +%s)"; lease "$T/main" 51 "$(date +%s)"; : >"$T/calls"
real_set_status 51 needs-maintainer >/dev/null 2>&1
check "needs-maintainer drops the lease and the claim" \
  "$([ -e "$LEASE" ] && echo kept || echo gone) $([ -e "$T/main/.relay/claim" ] && echo kept || echo released)" "gone released"
check "needs-maintainer swaps the status" "$(cat "$T/calls")" \
  "DELETE repos/JairJosafath/c4factory/issues/51/labels/status%2Fin-progress
POST repos/JairJosafath/c4factory/issues/51/labels {\"labels\":[\"status/needs-maintainer\"]}"
clear; claim "$T/main" 51 "$(date +%s)"; lease "$T/main" 51 "$(date +%s)"
real_set_status 51 needs-human >/dev/null 2>&1
check "needs-human drops the lease and the claim" \
  "$([ -e "$LEASE" ] && echo kept || echo gone) $([ -e "$T/main/.relay/claim" ] && echo kept || echo released)" "gone released"
real_set_status 51 bogus 2>/dev/null; check "status, unknown" $? 1
rm -f "$LEASE" "$T/posted" "$T/edited" "$T/calls"

# escalated: the open issues waiting on the maintainer, with when each last got
# that label; one whose events don't say gets 0.
echo '[[{"event":"labeled","label":{"name":"status/needs-maintainer"},"created_at":"2026-10-06T08:00:00Z"},
  {"event":"commented","created_at":"2026-10-06T08:30:00Z"},
  {"event":"labeled","label":{"name":"status/ready"},"created_at":"2026-10-06T08:40:00Z"},
  {"event":"labeled","label":{"name":"status/needs-maintainer"},"created_at":"2026-10-06T09:00:00Z"}]]' >"$T/events-88.json"
check "escalated" "$(OPEN='[[{"number":88,"labels":[{"name":"status/needs-maintainer"}]},
  {"number":89,"labels":[{"name":"status/needs-maintainer"}]},
  {"number":91,"labels":[{"name":"status/needs-human"}]},
  {"number":92,"labels":[{"name":"status/needs-maintainer"}],"pull_request":{}}]]' gh_escalated)" \
  "88 $(date -d 2026-10-06T09:00:00Z +%s)
89 0"
check "escalated, none" "$(OPEN='[[]]' gh_escalated)" ""

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
gh_proposed --since soon 2>/dev/null
check "proposed --since takes epoch seconds" $? 1

# Blocked: what holds back each ready or in-progress issue that pick skips for
# an open dependency, with the blocker's status, claim and own dependencies;
# --unworked leaves out the blockers in progress under a live claim. Cycles:
# the issues that wait on each other, and --report comments each once.
dep='[[100, ["status/ready"], "Depends on #101"], [101, ["status/proposed"]],
  [102, ["status/ready"], "Depends on #103\nDepends on #104"], [104, ["status/in-progress"]],
  [105, ["status/in-progress"], "Depends on #106"], [106, ["status/ready"], "depends on #105"],
  [107, ["status/ready"], "Depends on #101"], [108, ["status/ready"], "Depends on #109"], [109, []],
  [110, ["status/proposed"], "Depends on #111"], [111, ["status/proposed"], "Depends on #112"],
  [112, ["status/needs-human"], "Depends on #110"]]'
claimed() { jq -nc --arg now "$now" '[[$ARGS.positional[] | tonumber | {id: ., issue_url: "x/issues/\(.)", updated_at: $now,
  body: "<!-- c4-claim -->\n**otter-\(.)** works on this issue"}]]' --args "$@"; }
check "blocked" "$(OPEN=$(issues <<<"$dep") CLAIMS=$(claimed 104 107) gh_blocked)" "100 waits on 101 (status/proposed, no claim)
102 waits on 104 (status/in-progress, claimed by otter-104)
105 waits on 106 (status/ready, no claim, waits on #105)
106 waits on 105 (status/in-progress, no claim, waits on #106)
108 waits on 109 (no status, no claim)"
check "blocked --unworked" "$(OPEN=$(issues <<<"$dep") CLAIMS=$(claimed 104 107) gh_blocked --unworked | cut -d' ' -f1-4)" "100 waits on 101
105 waits on 106
106 waits on 105
108 waits on 109"
check "blocked, a claimed blocker not in progress is unworked" \
  "$(OPEN=$(issues <<<"$dep") CLAIMS=$(claimed 101) gh_blocked --unworked | head -1)" "100 waits on 101 (status/proposed, claimed by otter-101)"
check "blocked, none" "$(OPEN=$(issues <<<"$all") gh_blocked)" "83 waits on 81 (status/proposed, no claim)"
check "blocked, the blocker closed" "$(OPEN=$(jq 'map(select(.[0] != 81))' <<<"$all" | issues) gh_blocked)" ""
gh_blocked --bogus 2>/dev/null
check "blocked, unknown flag" $? 1
check "cycles" "$(OPEN=$(issues <<<"$dep") gh_cycles)" "#105 #106
#110 #111 #112"
check "cycles, none" "$(OPEN=$(issues <<<"$all") gh_cycles)" ""
cyc=$T/cycle
mkdir -p "$cyc"
report() {
  (
    gh_comments() { if [ -e "$cyc/$1" ]; then jq -sc . "$cyc/$1"; else echo '[]'; fi; }
    gh_comment() { jq -Rsc '{body: rtrimstr("\n")}' >>"$cyc/$1"; echo "comment #$1" >>"$cyc/log"; }
    gh_set_status() { echo "status #$1 $2" >>"$cyc/log"; }
    OPEN=$(issues <<<"$dep") gh_cycles --report
  )
}
body="$GH_CYCLE_MARKER"$'\n#105, #106 wait on each other in a cycle (`Depends on #M` in their bodies), so pick skips each of them for good. Drop one `Depends on` line to break it; #106, the newest, goes to status/needs-maintainer until then.'
jq -nc --arg b "$body" '{body: $b}' >"$cyc/105"
check "cycles --report" "$(report)" "cycle #105 #106: commented on #106; #106 to status/needs-maintainer
cycle #110 #111 #112: commented on #110 #111 #112; #112 to status/needs-maintainer"
check "cycles --report, what it did" "$(cat "$cyc/log")" "comment #106
status #106 needs-maintainer
comment #110
comment #111
comment #112
status #112 needs-maintainer"
check "cycles --report, the comment names the cycle" "$(jq -r .body "$cyc/106")" "$body"
: >"$cyc/log"
# The maintainer may have sent one on to the owner: either status counts.
dep=$(jq '(.[] | select(.[0] == 106)) |= (.[1] = ["status/needs-maintainer"])
  | (.[] | select(.[0] == 112)) |= (.[1] = ["status/needs-human"])' <<<"$dep")
check "cycles --report, once" "$(report)$(cat "$cyc/log")" ""
dep=$(jq '(.[] | select(.[0] == 106)) |= (.[1] = ["status/ready"])
  | (.[] | select(.[0] == 112)) |= (.[1] = ["status/proposed"])' <<<"$dep")
check "cycles --report, commented but not moved: moves the newest" "$(report)" "cycle #105 #106: #106 to status/needs-maintainer
cycle #110 #111 #112: #112 to status/needs-maintainer"

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
issue 96 OWNER 'maintainer/report' 'The report'; gh_intake 96 >/dev/null
check "intake leaves the report issue alone" "$(calls)" ""

# report: the first call creates the label, the issue and pins it; later calls
# edit that issue's body. activity: the newest updated_at, the report left out,
# and main's SHA. A fake GitHub keeps the report issue in $T/report.
(
  rm -f "$T/report" "$T/report-label"
  : >"$T/calls"
  gh_api() {
    local url= a m=GET
    for a in "$@"; do case $a in repos/*) url=$a ;; graphql) url=$a m=POST ;; POST | PATCH) m=$a ;; esac; done
    [[ " $* " == *" --input - "* ]] && cat >"$T/in"
    echo "$m $url" >>"$T/calls"
    case "$m $url" in
      "GET $R/issues?state=open&labels=maintainer%2Freport"*) [ ! -e "$T/report" ] || echo 7 ;;
      "GET $R/labels/maintainer%2Freport") [ -e "$T/report-label" ] ;;
      "POST $R/labels") touch "$T/report-label" ;;
      "POST $R/issues") jq -c '{title, labels}' "$T/in" >"$T/report"; echo "N7 https://x/issues/7" ;;
      "PATCH $R/issues/7") jq -r .body "$T/in" >"$T/body7"; echo https://x/issues/7 ;;
      "POST graphql") ;;
      "GET $R/issues?state=all"*) jq -r '[.[] | select(.labels | any(.name == "maintainer/report") | not) | .updated_at] | first // "none"' <<<'[{"updated_at":"2026-10-06T09:00:00Z","labels":[{"name":"maintainer/report"}]},{"updated_at":"2026-10-06T08:00:00Z","labels":[]}]' ;;
      "GET $R/commits/main") echo abc123 ;;
    esac
  }
  echo "first" >"$T/r1"
  out=$(gh_report "$T/r1" 2>&1)
  check "report: first call creates and pins" "$out | $(calls | tr '\n' ',')" "https://x/issues/7 | GET $R/issues?state=open&labels=maintainer%2Freport&per_page=10,GET $R/labels/maintainer%2Freport,POST $R/labels,POST $R/issues,POST graphql,"
  check "report: the issue's title and label" "$(cat "$T/report")" '{"title":"Maintainer report","labels":["maintainer/report"]}'
  : >"$T/calls"
  echo "second" | gh_report - >"$T/out" 2>&1
  check "report: then edits it" "$(cat "$T/out") | $(calls | tr '\n' ',') | $(cat "$T/body7")" "https://x/issues/7 | GET $R/issues?state=open&labels=maintainer%2Freport&per_page=10,PATCH $R/issues/7, | second"
  check "activity: newest change, the report left out, and main" "$(gh_activity)" "2026-10-06T08:00:00Z abc123"
  exit $fail
) || fail=1
exit $fail
