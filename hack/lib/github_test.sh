#!/usr/bin/env bash
# Tests for take claiming an issue on a device without a cluster,
# acquire refusing without the human opt-in (C4_HUMAN_CLUSTER=1),
# and the cluster pool's leases (acquire, wait, exit 4, stale lease, two leases
# on two clusters, a third waiting, a released cluster reused, a missing one
# created, --fresh recreating only the cluster it leased, a bare cluster taken last and recreated; release on handoff, close and release, and kept when marking, handing off or closing another issue),
# for flush releasing the claim of the handoff it posts,
# for blocked naming what holds back the issues pick skips and cycles reporting the issues that wait on each other,
# for parents closing a plan's parent when its last step closes (close doing it too), moving a waiting one to status/waiting and leaving a plain Depends on wait alone,
# for status needs-maintainer releasing the claim and lease, for escalated listing the issues waiting on the maintainer,
# for milestone setting and clearing an issue's milestone and milestones listing the open ones,
# for proposed listing the issues the relay's triage reads, for report creating the pinned Maintainer report once and then editing it, for activity leaving the report out, for closes-check and pr-check refusing a closing keyword for a plan's parent too early, for close refusing a parent with an open step, for close keeping the claim when the close fails and releasing it when a pull request already closed the issue, for close, duplicate and closed dropping the status labels, for merge refusing a pull request that says Refs #N and would close N (by #N, OWNER/REPO#N or the issue URL) or is behind main or while another pull request holds the merge turn, for land waiting for the merge turn, taking over an abandoned one, heartbeating it, merging main in, retrying a failed fetch, checking and merging, and giving the turn up merged, red, conflicting, unable to fetch or out of time, for take releasing a claim on a closed issue, and for pick's order, against a stubbed GitHub and a throwaway repository with two worktrees, a fake kind and a fake make.
# Run: hack/lib/github_test.sh
. "$(dirname "$0")/github.sh"
set -o pipefail # as github.sh runs as a script
unset C4_CLUSTER C4_HUMAN_CLUSTER # ./dev setup --lite sets the one, a human the other; each test sets its own
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
    */issues/50/labels*) printf 'status/in-progress\n' ;;
    */issues/51/labels*) printf 'status/in-progress\n' ;;
    */issues/7[2-5]/labels*) printf 'kind/bug\nstatus/in-progress\nstatus/ready\n' ;;
    */issues/60) echo '{"number":60,"title":"Deploy","labels":[{"name":"kind/feature"}]}' ;;
    */issues/71) return 1 ;; # closing #71 fails
    */issues/73) echo closed ;; # --jq .state, as take asks
    */issues/74) echo open ;;
    */issues/75) [[ " $* " == *" PATCH "* ]] && return 1; echo closed ;; # a merged pull request closed #75 first
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
    */issues\?state=open*) [ -e "$T/no-open" ] && return 0; [ -n "${OPEN:-}" ] && { echo "$OPEN"; return; }; echo '[[{"number":60,"title":"Deploy","html_url":"u60","body":"","labels":[{"name":"status/ready"}]},{"number":61,"title":"Docs","html_url":"u61","body":"","labels":[{"name":"status/ready"}]}]]' ;;
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
# It goes on writing after the list, more than a pipe holds: under pipefail
# (as github.sh runs) a grep -q on its pipe would exit early and read a "no".
printf '#!/bin/sh\n[ "$1 $2" = "get clusters" ] && cat "%s/kind-clusters" && seq 100000\n' "$T" >"$T/bin/kind"
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
out=$(as "$T/wt2" gh_cluster acquire --wait 0 2>"$T/err"); check "acquire, no human opt-in: refused" "$?|$out|$([ -e "$LEASE" ] && echo leased)" "5||"
grep -q "C4_HUMAN_CLUSTER=1" "$T/err"; check "acquire, no human opt-in: names it" $? 0
C4_HUMAN_CLUSTER=0 as "$T/wt2" gh_cluster acquire --wait 0 >/dev/null 2>&1; check "acquire, opt-in not 1: refused" $? 5
export C4_HUMAN_CLUSTER=1 # the rest of the pool tests are a human's
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

# take claims an issue without a cluster, and while another checkout holds the lease.
clear; lease "$T/wt2" 50 "$(date +%s)"
rm -f "$T/posted"; out=$(C4_CLUSTER=none gh_take 60 2>/dev/null); check "take 60, no cluster" "$? $(cut -f1 <<<"$out" | grep -c .)" "0 1"
rm -f "$T/main/.relay/claim"
rm -f "$T/posted"; gh_take 60 >/dev/null 2>&1; check "take 60, lease held elsewhere" $? 0
rm -f "$T/main/.relay/claim"
gh_label 61 kind/docs >/dev/null 2>&1; check "label" $? 0

# release, handoff, close and needs-human drop this checkout's lease when it works on that issue.
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
# ready needs a kind/* label: take names the branch by it, the release notes file by it.
clear; : >"$T/calls"
out=$(real_set_status 51 ready 2>&1); rc=$?
check "ready without a kind label is refused" "$rc $(grep -c 'no kind/\* label' <<<"$out") $(grep -c . "$T/calls")" "1 1 0"
clear; : >"$T/calls"
real_set_status 72 ready >/dev/null 2>&1; rc=$?
check "ready with a kind label" "$rc $(cat "$T/calls")" "0 DELETE repos/JairJosafath/c4factory/issues/72/labels/status%2Fin-progress"
# Marking, handing off or closing another issue keeps this checkout's lease and claim.
clear; claim "$T/main" 52 "$(date +%s)"; lease "$T/main" 52 "$(date +%s)"
real_set_status 51 needs-human >/dev/null 2>&1
check "needs-human on another issue keeps the lease and the claim" \
  "$([ -e "$LEASE" ] && echo kept || echo gone) $([ -e "$T/main/.relay/claim" ] && echo kept || echo released)" "kept kept"
clear; lease "$T/main" 52 "$(date +%s)"
real_set_status 51 needs-maintainer >/dev/null 2>&1
check "needs-maintainer on another issue, no claim, keeps the lease" "$([ -e "$LEASE" ] && echo kept || echo gone)" kept
clear; claim "$T/main" 51 "$(date +%s)"; lease "$T/main" null "$(date +%s)"
real_set_status 51 needs-human >/dev/null 2>&1
check "needs-human, claim on it, lease taken before the claim: dropped" "$([ -e "$LEASE" ] && echo kept || echo gone)" gone
clear; claim "$T/main" 52 "$(date +%s)"; lease "$T/main" 52 "$(date +%s)"
echo h | gh_handoff 73 - >/dev/null 2>&1; check "handoff of another issue keeps the lease" "$([ -e "$LEASE" ] && echo kept || echo gone)" kept
clear; claim "$T/main" 52 "$(date +%s)"; lease "$T/main" 52 "$(date +%s)"
echo landed | gh_close 72 - >/dev/null 2>&1; check "close of another issue keeps the lease" "$([ -e "$LEASE" ] && echo kept || echo gone)" kept
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
claim "$T/main" 72 "$(date +%s)"; : >"$T/calls"
echo landed | gh_close 72 - >/dev/null 2>&1; check "close" $? 0
check "close, claim released" "$([ -e "$T/main/.relay/claim" ] && echo kept || echo released)" released
R=repos/$C4_GITHUB_REPO
check "close drops every status label" "$(grep DELETE "$T/calls")" "DELETE $R/issues/72/labels/status%2Fin-progress
DELETE $R/issues/72/labels/status%2Fready"
claim "$T/main" 75 "$(date +%s)"; lease "$T/main" 75 "$(date +%s)"; : >"$T/calls"
echo landed | gh_close 75 - >/dev/null 2>&1; check "close, already closed" $? 0
check "close, already closed, claim and lease released" \
  "$([ -e "$T/main/.relay/claim" ] && echo kept || echo released) $([ -e "$LEASE" ] && echo kept || echo gone)" "released gone"
check "close, already closed, drops every status label" "$(grep DELETE "$T/calls")" "DELETE $R/issues/75/labels/status%2Fin-progress
DELETE $R/issues/75/labels/status%2Fready"
: >"$T/calls"
gh_duplicate 72 61 >/dev/null 2>&1; check "duplicate" $? 0
check "duplicate drops every status label" "$(grep DELETE "$T/calls")" "DELETE $R/issues/72/labels/status%2Fin-progress
DELETE $R/issues/72/labels/status%2Fready"
# closed: what the Issue closed workflow runs after a pull request's Closes #N.
: >"$T/calls"
gh_closed 73 >/dev/null 2>&1; check "closed" $? 0
check "closed drops every status label" "$(grep DELETE "$T/calls")" "DELETE $R/issues/73/labels/status%2Fin-progress
DELETE $R/issues/73/labels/status%2Fready"
: >"$T/calls"
gh_closed 74 >/dev/null 2>&1; check "closed, open issue" "$? $(cat "$T/calls")" "0 "
gh_closed x 2>/dev/null; check "closed, not a number" $? 1
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

# A parent issue split into a plan and steps stays open and in progress with
# no claim, its body a "Depends on #S" line per step: pick skips it while a
# step is open and offers it again once every step closed.
parent='[[90, ["status/in-progress"], "Plan: docs/plans/p.md\nDepends on #91\nDepends on #92"],
  [91, ["status/ready"]], [92, ["status/proposed"]]]'
check "pick, parent waits on its steps" "$(OPEN=$(issues <<<"$parent") gh_pick | cut -f1,4)" "91	ready"
OPEN=$(jq 'map(select(.[0] != 91))' <<<"$parent" | issues) gh_pick >/dev/null
check "pick, parent waits on its last step" $? 1
check "pick, parent once its steps closed" "$(OPEN=$(jq 'map(select(.[0] == 90))' <<<"$parent" | issues) gh_pick | cut -f1,4)" "90	in-progress"

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

# Labels: GitHub refuses a description longer than 100 characters.
long=$(for l in "${GH_LABELS[@]}"; do d=${l#*|}; d=${d#*|}; [ ${#d} -le 100 ] || echo "${l%%|*}"; done)
check "labels: descriptions fit GitHub's 100 characters" "$long" ""

# parents: a plan's parent (in progress or waiting, Depends on #S per step, a
# merged pull request that says Refs #N and adds its plan in docs/plans/ or
# changes docs/design.md)
# closes with a comment once every step closed; one with a step open stays
# open and, unclaimed, goes to status/waiting; Depends on lines without a
# merged plan close nothing. pick skips a waiting parent, and blocked tells it
# apart from an issue an agent works.
(
  : >"$T/calls"
  gh_set_status() { real_set_status "$@"; }
  # timeline N: merged (M) or open (O) pull requests P that say Refs #N.
  xref() { local n=$1 out= x; shift; for x; do out+="${out:+,}{\"event\":\"cross-referenced\",\"source\":{\"issue\":{\"number\":${x#?},\"body\":\"Plan.\\nRefs #$n\",\"pull_request\":{\"merged_at\":$([ "${x:0:1}" = M ] && echo '"t"' || echo null)}}}}"; done; echo "[[{\"event\":\"labeled\"},$out]]"; }
  gh_api() {
    local url= a m=GET in=
    for a in "$@"; do case $a in repos/*) url=$a ;; PATCH | POST | DELETE) m=$a ;; esac; done
    [ "$m" = GET ] || echo "$m $url" >>"$T/calls"
    [[ " $* " != *" --input - "* ]] || in=$(jq -r '.body // empty')
    case "$m $url" in
      "GET $R/issues?state=open"*) echo "$OPEN" ;;
      "GET $R/issues/comments?since"*) echo "${CLAIMS:-[[]]}" ;;
      "GET $R/issues/120/timeline"*) xref 120 M130 ;;
      "GET $R/issues/"12[389]/timeline* | "GET $R/issues/"14[024]/timeline*) a=${url#*issues/}; xref "${a%%/*}" M131 ;;
      "GET $R/issues/126/timeline"*) xref 126 O132 M133 ;;
      "GET $R/issues/146/timeline"*) xref 146 M134 ;;
      "GET $R/issues/"*/timeline*) echo '[[]]' ;;
      "GET $R/pulls/"13[01]/files*) printf 'docs/plans/p.md\nhack/x.sh\n' ;;
      "GET $R/pulls/134/files"*) printf 'docs/design.md\n' ;;
      "GET $R/pulls/"*/files*) echo hack/x.sh ;;
      "GET $R/issues/"12[1257]) echo "closed completed" ;;
      "GET $R/issues/141") echo "closed not_planned" ;;
      "GET $R/issues/143") return 1 ;;
      "GET $R/issues/"*/labels*) echo status/in-progress ;;
      "GET $R/issues/"[0-9]*) echo open ;;
      "POST $R/issues/"*/comments) a=${url#*issues/}; echo "$in" >"$T/comment-${a%%/*}"; echo "https://x/c" ;;
      "PATCH $R/issues/"*) echo "closed #${url##*/}" ;;
    esac
  }
  R=repos/$C4_GITHUB_REPO
  plans='[[120, ["status/in-progress"], "Plan: docs/plans/p.md\nDepends on #121\nDepends on #122"],
    [123, ["status/in-progress"], "Depends on #124\nDepends on #125"], [124, ["status/ready"]],
    [126, ["status/in-progress"], "Depends on #127"],
    [128, ["status/waiting"], "Depends on #124"],
    [129, ["status/in-progress"], "Depends on #124"]]'
  export OPEN; OPEN=$(issues <<<"$plans")
  check "parents: what each does" "$(gh_parents)" "#120 closes: its steps #121, #122 closed; plan #130
#123 waits on #124 (of #124, #125; plan #131)
#128 waits on #124 (of #124; plan #131)
#129 waits on #124 (of #124; plan #131)"
  check "parents: asking changes nothing" "$(cat "$T/calls")" ""
  out=$(CLAIMS=$(claimed 129) gh_parents --apply 2>&1)
  check "parents --apply: closes the finished one, marks the waiting one" "$out" "#120 closed: its steps #121, #122 closed; plan #130
#123 to status/waiting: waits on #124 (of #124, #125; plan #131)"
  check "parents --apply: the close comment names the steps" "$(cat "$T/comment-120")" "<!-- relay-parent -->
Its steps #121, #122 are all closed and its plan's pull request #130 merged: nothing it waits on is left, so the relay closes it."
  check "parents --apply: the waiting comment names the steps left" "$(sed -n 2p "$T/comment-123")" "Waiting on its steps, not on an agent: #124 still open (of #124, #125). Its plan's pull request #131 merged, so it is status/waiting: pick skips it, and it closes itself when its last step closes."
  check "parents --apply: what it changed" "$(cat "$T/calls")" "POST $R/issues/120/comments
PATCH $R/issues/120
DELETE $R/issues/120/labels/status%2Fin-progress
POST $R/issues/123/comments
DELETE $R/issues/123/labels/status%2Fin-progress
POST $R/issues/123/labels"
  : >"$T/calls"
  check "parents --apply M: only M's parents" "$(gh_parents --apply 125 2>&1)" "#123 to status/waiting: waits on #124 (of #124, #125; plan #131)"
  check "parents --apply M: a step without parents" "$(gh_parents --apply 999 2>&1)" ""
  check "parents --apply M: a plain wait closes nothing" "$(gh_parents --apply 127 2>&1)$(grep -c 126 "$T/calls")" "0"
  gh_parents --bogus 2>/dev/null; check "parents, bad argument" $? 1
  # Not closed: a step GitHub can't show, a parent its plan's session still
  # holds. Closed: a parent whose step closed as not planned (a duplicate),
  # and one whose plan only changed docs/design.md.
  : >"$T/calls"
  odd=$(issues <<<'[[140, ["status/in-progress"], "Depends on #141"], [142, ["status/in-progress"], "Depends on #143"],
    [144, ["status/in-progress"], "Depends on #121"], [146, ["status/in-progress"], "Depends on #121"]]')
  check "parents: lists what each does" "$(OPEN=$odd gh_parents 2>/dev/null)" "#140 closes: its steps #141 closed; plan #131
#142 waits on #143 (of #143; plan #131)
#144 closes: its steps #121 closed; plan #131
#146 closes: its steps #121 closed; plan #134"
  out=$(OPEN=$odd CLAIMS=$(claimed 142 144) gh_parents --apply 2>"$T/err")
  check "parents --apply: not planned or design-only closes, missing or held stays open" "$? $out | $(grep -E 'PATCH|POST' "$T/calls" | tr '\n' ' ')" "0 #140 closed: its steps #141 closed; plan #131
#146 closed: its steps #121 closed; plan #134 | POST $R/issues/140/comments PATCH $R/issues/140 POST $R/issues/146/comments PATCH $R/issues/146 "
  check "parents --apply: says which step it could not see" "$(cat "$T/err")" "github: #142 depends on #143, which GitHub could not show; #142 stays open"
  # close tends the parents of the issue it closed.
  : >"$T/calls"
  echo landed | gh_close 122 - >/dev/null 2>&1
  check "close closes the parent its last step finishes" "$(grep -c "PATCH $R/issues/120" "$T/calls")" 1
  # A waiting parent: pick skips it, even with its steps closed; blocked lists
  # the in-progress one and not the waiting one.
  check "pick skips a waiting parent" "$(OPEN=$(jq -c '[[.[][] | select(.number == 128) | .body = ""]]' <<<"$OPEN") gh_pick; echo $?)" 1
  waiting=$(issues <<<'[[123, ["status/waiting"], "Depends on #124"], [124, ["status/ready"]], [129, ["status/in-progress"], "Depends on #124"]]')
  check "blocked tells a waiting parent from a worked one" "$(OPEN=$waiting gh_blocked | cut -d' ' -f1-4)" "129 waits on 124"
  real_set_status 123 bogus 2>/dev/null; check "status waiting is known, bogus not" "$?$(real_set_status 123 waiting >/dev/null 2>&1; echo $?)" 10
  exit $fail
) || fail=1

# milestone: sets an issue's milestone by the open milestone's title, clears it
# without one, and refuses a title no open milestone has; milestones lists them.
(
  : >"$T/calls"
  gh_api() {
    local url= a m=GET
    for a in "$@"; do case $a in repos/*) url=$a ;; PATCH) m=$a ;; esac; done
    if [[ " $* " == *" --input - "* ]]; then echo "$m $url $(jq -c . </dev/stdin)" >>"$T/calls"; else echo "$m $url" >>"$T/calls"; fi
    case "$m $url" in
      "GET $R/milestones?state=open"*) echo '[{"number":1,"title":"Briefs","open_issues":3,"closed_issues":2,"due_on":"2026-10-09T07:00:00Z"},{"number":3,"title":"1.0: a stable API","open_issues":0,"closed_issues":0,"due_on":null}]' ;;
    esac
  }
  out=$(gh_milestone 42 '1.0: a stable API' 2>&1)
  check "milestone: sets it by title" "$out | $(calls | tail -1)" "milestone #42: 1.0: a stable API | PATCH $R/issues/42 {\"milestone\":3}"
  : >"$T/calls"
  out=$(gh_milestone 42 2>&1)
  check "milestone: no title clears it" "$out | $(calls)" "milestone #42: cleared | PATCH $R/issues/42 {\"milestone\":null}"
  : >"$T/calls"
  out=$(gh_milestone 42 Upgrades 2>&1)
  check "milestone: an unknown title fails and sets nothing" "$? $out | $(calls | grep -c PATCH)" '1 github: no open milestone is titled "Upgrades"; open: Briefs, 1.0: a stable API | 0'
  check "milestones: title, open, closed, due" "$(gh_milestones)" "Briefs	3	2	2026-10-09
1.0: a stable API	0	0	"
  exit $fail
) || fail=1
# closes-check and close: a plan's parent closes with its last step, never
# before. #467 waits on its steps #472 to #475 (#475 open, #474 open while
# $T/474-open exists); #468's steps are all closed. A fake GitHub notes each
# call in $T/calls.
(
  : >"$T/calls"
  rm -f "$T/474-open"
  gh_api() {
    local url= a m=GET
    for a in "$@"; do case $a in repos/*) url=$a ;; POST | PATCH) m=$a ;; esac; done
    [[ " $* " == *" --input - "* ]] && cat >/dev/null
    echo "$m $url" >>"$T/calls"
    case "$m $url" in
      "GET $R/issues/467") printf 'Plan: docs/plans/x.md\nDepends on #472\nDepends on #473\r\n- depends on #474, the docs\nDepends on #475\n' ;;
      "GET $R/issues/468") printf 'Depends on #472\nDepends on #473\n' ;;
      "GET $R/issues/486") echo 'The plan is #510.' ;;
      "GET $R/issues/474") [ -e "$T/474-open" ] && echo open || echo closed ;;
      "GET $R/issues/475") echo open ;;
      "GET $R/issues/47"[23]) echo closed ;;
      "GET $R/pulls/510") printf 'docs(486): plan\nRefs #486\n' ;;
      "GET $R/pulls/510/commits"*) printf 'docs(486): plan\n\nSteps: #506 (turn on Ask by default, closes #486)\n' ;;
      "POST $R/issues/"*/comments) echo u ;;
      "PATCH $R/issues/"*) echo "closed #${url##*/}" ;;
    esac
  }
  out=$(printf 'docs(486): plan\n\nRefs #486\n\n- #506 (turn on `Ask` by default, closes #486)\n' | gh_closes_check - 2>&1)
  check "closes-check: closes #N in prose beside Refs #N is refused, naming the line" "$? $out" \
    '1 github: refused: "- #506 (turn on `Ask` by default, closes #486)" closes #486 at merge, and the same text says Refs #486. A plan'"'"'s pull request only refers to its parent: reword the line so no close, fix or resolve word sits before #486. If this pull request finishes #486, which has no plan, the Refs #486 is wrong: say Closes #486, never Refs #486; when a pushed commit says Refs #486, reword the history onto a new branch <branch>-2, open a fresh pull request and close this one (the handoff skill, Finish).'
  check "closes-check: asks GitHub only for the body of the issue it closes" "$(cat "$T/calls")" "GET $R/issues/486"
  : >"$T/calls"
  out=$(printf 'feat(486): ask\n\nRefs #486\n' | gh_closes_check - 2>&1)
  check "closes-check: no closing keyword asks GitHub nothing" "$? $out $(grep -c . "$T/calls")" "0  0"
  out=$(echo 'Resolves: https://github.com/JairJosafath/c4factory/issues/486 (Refs #486)' | gh_closes_check - 2>&1)
  check "closes-check: an issue URL after a keyword closes it too" "$? $(grep -c 'closes #486 at merge' <<<"$out")" "1 1"
  out=$(echo 'Fixes other/repo#486, see https://github.com/other/repo/issues/467' | gh_closes_check - 2>&1)
  check "closes-check: another repository's issue is not ours" "$? $out" "0 "
  out=$(gh_pr_check 510 2>&1)
  check "pr-check: a commit message's closes #N beside the body's Refs #N is refused" "$? $(grep -c 'refused: "Steps: #506' <<<"$out")" "1 1"
  : >"$T/calls"
  printf 'feat(475): the last step\n\nCloses #475\ncloses #467\n' >"$T/last"
  out=$(gh_closes_check "$T/last" 2>&1)
  check "closes-check: the last step's Closes #N, its other steps closed, passes" "$? $out" "0 "
  out=$(printf 'feat(475): the last step\n\nRefs #467\n\nCloses #475\nCloses #467\n' | gh_closes_check - 2>&1)
  check "closes-check: the last step may say Refs #N in a commit too" "$? $out" "0 "
  touch "$T/474-open"
  out=$(gh_closes_check "$T/last" 2>&1)
  check "closes-check: Closes #N while another step is open is refused, naming the step" "$? $out" \
    '1 github: refused: "closes #467" closes #467 at merge, but its step #474 is open ("- depends on #474, the docs" in #467'"'"'s body). The parent of a plan closes itself when its last step closes.'
  : >"$T/calls"
  out=$(echo landed | gh_close 467 - 2>&1)
  check "close: a parent with an open step is refused, naming each" "$? $out" \
    '1 github: refused to close #467: its step #474 is open ("- depends on #474, the docs" in #467'"'"'s body). The parent of a plan closes itself when its last step closes, never by hand.
github: refused to close #467: its step #475 is open ("Depends on #475" in #467'"'"'s body). The parent of a plan closes itself when its last step closes, never by hand.'
  check "close: refused, nothing posted or closed" "$(grep -cE '^(POST|PATCH)' "$T/calls")" 0
  : >"$T/calls"
  out=$(echo landed | gh_close 468 - 2>&1)
  check "close: a parent whose steps are all closed closes" "$? $(grep -E '^(POST|PATCH)' "$T/calls" | tr '\n' ,)" \
    "0 POST $R/issues/468/comments,PATCH $R/issues/468,"
  exit $fail
) || fail=1

# merge refuses a plan's pull request that would close the parent it refers to.
(
  # PR is the pull request GraphQL shows: body, closing references, commit messages.
  # Issue N's body is $T/body-N, its state open when $T/open-N exists.
  gh_api() {
    local a
    [ "$1" = graphql ] && { echo "$PR"; return; }
    # compare BASE...HEAD: how far the head is behind its base, BEHIND (0 by default).
    # The merge queue: TURNS, the comments on the report issue #7 (none by default).
    [[ $1 == repos/*/issues\?state=open* ]] && { [ -z "${TURNS:-}" ] || echo 7; return; }
    [[ " $* " == *" repos/$C4_GITHUB_REPO/issues/7/comments"* ]] && { echo "$TURNS"; return; }
    [[ $1 == repos/*/compare/* ]] && { echo "$1" >>"$T/compared"; [ "${BEHIND:-0}" != fail ] || return 1; echo "${BEHIND:-0}"; return; }
    for a in "$@"; do case $a in repos/*/issues/*) a=${a##*/}; [[ " $* " == *" .state "* ]] && { [ -e "$T/open-$a" ] && echo open || echo closed; } || cat "$T/body-$a" 2>/dev/null; return 0 ;; esac; done
    return 1
  }
  gh_token() { :; }
  gh() { echo "gh $*" >>"$T/merged"; }
  pr() { jq -n --arg b "$1" --argjson c "${2:-[]}" --arg m "${3:-feat: plan}" \
    '{body: $b, baseRefName: "main", headRefOid: "h1", closingIssuesReferences: {nodes: [$c[] | {number: .}]}, commits: {nodes: [{commit: {message: $m}}]}}'; }
  rm -f "$T/merged"
  out=$(PR=$(pr $'Refs #7\n\n#9 (the prompt path; its PR closes #7)' '[7]') gh_merge 12 2>&1)
  check "merge: refuses Refs #7 plus prose that closes #7" "$? $(cat "$T/merged" 2>/dev/null)" "1 "
  check "merge: names the line to reword" "$out" 'github: #12 says Refs #7, but "#9 (the prompt path; its PR closes #7)" in it closes #7 on merge; reword it (no close, fix or resolve word before #7)'
  out=$(PR=$(pr $'Refs #7\n\nFixed: '"$C4_GITHUB_REPO"'#7 in step two' '[]') gh_merge 12 2>&1)
  check "merge: any closing word, colon and OWNER/REPO#N too" "$? $out" "1 github: #12 says Refs #7, but \"Fixed: $C4_GITHUB_REPO#7 in step two\" in it closes #7 on merge; reword it (no close, fix or resolve word before #7)"
  out=$(PR=$(pr $'Refs #7\n\nSteps: #8, #9; the last one closes https://github.com/'"$C4_GITHUB_REPO"'/issues/7' '[]') gh_merge 12 2>&1)
  check "merge: refuses Refs #7 plus the issue URL after a closing word" "$? $(grep -c 'says Refs #7, but "Steps: #8, #9; the last one closes https://github.com/.*/issues/7" in it closes #7 on merge' <<<"$out")" "1 1"
  out=$(PR=$(pr 'Refs #7' '[]' $'docs: plan\n\nresolves #7') gh_merge 12 2>&1)
  check "merge: a commit message counts" "$? $out" '1 github: #12 says Refs #7, but "resolves #7" in it closes #7 on merge; reword it (no close, fix or resolve word before #7)'
  out=$(PR=$(pr 'Refs #7, see https://github.com/o/r/issues/7' '[7]') gh_merge 12 2>&1)
  check "merge: closingIssuesReferences alone refuses" "$? $out" '1 github: #12 says Refs #7, but GitHub would close #7 on merge (closingIssuesReferences); drop the link or the closing word before it'
  check "merge: nothing merged while refused" "$(cat "$T/merged" 2>/dev/null)" ""
  out=$(PR=$(pr $'Refs #7\n\nSteps: #8 (finishes #7), #9\nCloses #70' '[70]') gh_merge 12 2>&1)
  check "merge: only Refs #7 merges" "$? $out | $(cat "$T/merged")" "0  | gh pr merge 12 --repo $C4_GITHUB_REPO --merge --match-head-commit h1"
  rm -f "$T/merged"
  out=$(PR=$(pr 'Closes #565' '[565]') gh_merge 13 2>&1)
  check "merge: a step's Closes #N merges" "$? $(cat "$T/merged")" "0 gh pr merge 13 --repo $C4_GITHUB_REPO --merge --match-head-commit h1"
  gh_merge abc 2>/dev/null; check "merge: bad argument" $? 1
  rm -f "$T/merged"
  out=$(PR=$(pr $'Closes #5\n\nFollows #8; its PR closes #9.' '[5, 9]') gh_merge 15 2>&1)
  check "merge: refuses prose that closes an issue no Closes line names" "$? $out $(cat "$T/merged" 2>/dev/null)" \
    '1 github: #15 would close #9 on merge: "Follows #8; its PR closes #9." names it with a closing word, and no line starts with Closes #9; reword it (no close, fix or resolve word before #9), or put Closes #9 on its own line '
  out=$(PR=$(pr 'Closes #5' '[5]' $'fix: thing\n\nThis fixes #9 too.') gh_merge 15 2>&1)
  check "merge: a commit message's prose counts" "$? $(grep -c '"This fixes #9 too." names it' <<<"$out")" "1 1"
  out=$(PR=$(pr 'Closes #5' '[5, 9]') gh_merge 15 2>&1)
  check "merge: a linked issue with no Closes line refuses" "$? $out" '1 github: #15 would close #9 on merge (closingIssuesReferences), and no line starts with Closes #9; unlink #9, or put Closes #9 on its own line'
  check "merge: nothing merged while prose closes" "$(cat "$T/merged" 2>/dev/null)" ""
  out=$(PR=$(pr $'Closes #5\n  fixes: #6\nResolves '"$C4_GITHUB_REPO#7" '[5, 6, 7]') gh_merge 15 2>&1)
  check "merge: only Closes, Fixes, Resolves lines merge" "$? $out | $(cat "$T/merged")" "0  | gh pr merge 15 --repo $C4_GITHUB_REPO --merge --match-head-commit h1"
  rm -f "$T/merged"
  out=$(PR=$(pr "Closes https://github.com/$C4_GITHUB_REPO/issues/5" '[5]') gh_merge 15 2>&1)
  check "merge: a Closes line with the issue URL merges" "$? $out | $(cat "$T/merged")" "0  | gh pr merge 15 --repo $C4_GITHUB_REPO --merge --match-head-commit h1"
  rm -f "$T/merged"
  printf 'Depends on #571\nDepends on #572\n' >"$T/body-570"
  touch "$T/open-571" "$T/open-572"
  out=$(PR=$(pr $'Closes #572\nCloses #570' '[572, 570]') gh_merge 14 2>&1)
  check "merge: refuses closing a parent while another step is open" "$? $(grep -c 'its step #571 is open' <<<"$out") $(cat "$T/merged" 2>/dev/null)" "1 1 "
  rm "$T/open-571"
  out=$(PR=$(pr $'Closes #572\nCloses #570' '[572, 570]') gh_merge 14 2>&1)
  check "merge: the last step closes its parent" "$? $(cat "$T/merged")" "0 gh pr merge 14 --repo $C4_GITHUB_REPO --merge --match-head-commit h1"
  # A pull request behind main is refused: green alone, it may be red merged.
  rm -f "$T/merged" "$T/compared"
  out=$(BEHIND=3 PR=$(pr 'Closes #5' '[5]') gh_merge 16 2>&1)
  check "merge: refuses a pull request behind main" "$? $out $(cat "$T/merged" 2>/dev/null)" \
    '1 github: #16 is 3 commit(s) behind main: land it (hack/lib/github.sh land 16), which merges main in, reruns make check and merges in its turn '
  check "merge: compares main with the head it merges" "$(cat "$T/compared")" "repos/$C4_GITHUB_REPO/compare/main...h1"
  out=$(BEHIND=fail PR=$(pr 'Closes #5' '[5]') gh_merge 16 2>&1)
  check "merge: refuses when GitHub can't say whether it is behind" "$? $out $(cat "$T/merged" 2>/dev/null)" \
    '1 github: #16: could not ask GitHub whether it is behind main; merge refused '
  out=$(BEHIND=0 PR=$(pr 'Closes #5' '[5]') gh_merge 16 2>&1)
  check "merge: merges a pull request that contains main" "$? $out | $(cat "$T/merged")" "0  | gh pr merge 16 --repo $C4_GITHUB_REPO --merge --match-head-commit h1"
  # While another pull request holds the merge turn, only that one merges.
  turn() { echo "{\"id\":$1,\"updated_at\":\"$3\",\"body\":\"<!-- c4-land -->\\n**heron-$2** lands pull request \`$2\` · host \`h\` · pid \`1\`\"}"; }
  old=$(date -u -d '-1 hour' +%Y-%m-%dT%H:%M:%SZ)
  rm -f "$T/merged"
  out=$(TURNS="[[$(turn 1 20 "$now"),$(turn 2 16 "$now")]]" PR=$(pr 'Closes #5' '[5]') gh_merge 16 2>&1)
  check "merge: refuses while another pull request holds the merge turn" "$? $out $(cat "$T/merged" 2>/dev/null)" \
    '1 github: #16: it is #20'"'"'s turn to merge (heron-20 lands #20 (host h), heartbeat 0m ago); land with hack/lib/github.sh land 16, which waits for its turn '
  out=$(TURNS="[[$(turn 1 20 "$old"),$(turn 2 16 "$now")]]" PR=$(pr 'Closes #5' '[5]') gh_merge 16 2>&1)
  check "merge: its own turn merges, an abandoned one ahead is no turn" "$? $out | $(cat "$T/merged")" "0  | gh pr merge 16 --repo $C4_GITHUB_REPO --merge --match-head-commit h1"
  rm -f "$T/merged"
  out=$(TURNS='[[]]' PR=$(pr 'Closes #5' '[5]') gh_merge 16 2>&1)
  check "merge: nobody's turn merges" "$? | $(cat "$T/merged")" "0 | gh pr merge 16 --repo $C4_GITHUB_REPO --merge --match-head-commit h1"
  exit $fail
) || fail=1

# land: the merge turn. A fake GitHub keeps the report issue #7's comments in
# $T/q/ID.json; pull request #40's branch feat is in a clone of a bare origin.
(
  Q=$T/q
  rm -rf "$Q" "$T/origin.git" "$T/land"
  mkdir -p "$Q"
  stamp() { date -u -d "${1:-now}" +%Y-%m-%dT%H:%M:%SZ; }
  # put ID PR [WHEN]: a merge-turn comment of heron-PR, edited WHEN (default now).
  put() { jq -n --argjson id "$1" --arg u "$(stamp "${3:-now}")" --arg b "$GH_LAND_MARKER"$'\n'"**heron-$2** lands pull request \`$2\` · host \`h\` · pid \`1\`" \
    '{id: $id, updated_at: $u, body: $b}' >"$Q/$1.json"; }
  gh_api() {
    local url= a m=GET q= data
    for a in "$@"; do case $a in repos/*) url=$a ;; POST | PATCH | DELETE) m=$a ;; esac; done
    while [ $# -gt 0 ]; do [ "$1" = --jq ] && q=$2; shift; done
    echo "$m $url" >>"$T/calls"
    case "$m $url" in
      "GET $R/issues?state=open&labels=maintainer%2Freport"*) data='[{"number":7}]' ;;
      "GET $R/issues/7/comments"*) data=$(cat "$Q"/*.json 2>/dev/null | jq -sc '[sort_by(.id)]') ;;
      "POST $R/issues/7/comments")
        a=$(($(ls "$Q" | sed 's/\.json//' | sort -n | tail -1) + 1))
        jq -c --argjson id "$a" --arg u "$(stamp)" '{id: $id, updated_at: $u, body}' >"$Q/$a.json"
        data="{\"id\":$a}" ;;
      "PATCH $R/issues/comments/"*)
        a=${url##*/}; [ -e "$Q/$a.json" ] || { echo "HTTP 404" >&2; return 1; }
        jq -c --argjson id "$a" --arg u "$(stamp)" '{id: $id, updated_at: $u, body}' >"$Q/$a.json" ;;
      "DELETE $R/issues/comments/"*)
        a=${url##*/}; [ -e "$Q/$a.json" ] || { echo "HTTP 404" >&2; return 1; }
        rm "$Q/$a.json" ;;
      "GET $R/pulls/40") data=$(jq -nc --arg s "$(git -C "$T/origin.git" rev-parse feat)" '{state: "open", base: {ref: "main"}, head: {ref: "feat", sha: $s}}') ;;
      *) return 1 ;;
    esac
    [ -n "${data:-}" ] || return 0
    if [ -n "$q" ]; then jq -r "$q" <<<"$data"; else echo "$data"; fi
  }
  gh_merge() { echo "merge $1 at $(git -C "$GH_LIB_ROOT" rev-parse HEAD)" >>"$T/landed"; }
  g() { git -C "$T/land" "$@"; }
  git init -q --bare -b main "$T/origin.git"
  git clone -q "$T/origin.git" "$T/land" 2>/dev/null
  # gh_land's merge commits here too: the clone needs an identity of its own,
  # as a device's git may have no global one.
  g config user.name t && g config user.email t@t
  g commit -q --allow-empty -m init && g push -q origin HEAD:main
  g checkout -q -b feat && echo feat >"$T/land/feat" && g add feat && g commit -q -m feat && g push -q -u origin feat
  # main moves on: another pull request merged.
  git clone -q "$T/origin.git" "$T/other" 2>/dev/null
  echo other >"$T/other/other" && git -C "$T/other" add other && git -C "$T/other" -c user.name=t -c user.email=t@t commit -q -m other && git -C "$T/other" push -q origin main
  # The fake make: records it ran, sleeps CHECK_SLEEP, fails when $T/red exists.
  printf '#!/bin/sh\necho "$*" >>"%s/checked"\nsleep "${CHECK_SLEEP:-0}"\n[ ! -e "%s/red" ]\n' "$T" "$T" >"$T/bin/check-make"
  chmod +x "$T/bin/check-make"
  # land merges main in itself: give it an identity a device without a global one lacks.
  export GIT_AUTHOR_NAME=t GIT_AUTHOR_EMAIL=t@t GIT_COMMITTER_NAME=t GIT_COMMITTER_EMAIL=t@t
  export C4_MAKE=$T/bin/check-make GH_LIB_ROOT=$T/land GH_PENDING_DIR=$T/land/.relay GH_LAND_POLL=0.2 GH_LAND_SETTLE=0
  queue() { ls "$Q" | sed 's/\.json//' | sort -n | tr '\n' ' '; }

  # Nobody holds it: take, merge main in, check, push, merge, give it up.
  : >"$T/calls"
  out=$(gh_land 40 2>&1)
  check "land: merges main in, checks, pushes and merges" "$? | $(cat "$T/landed") | $(cat "$T/checked")" \
    "0 | merge 40 at $(git -C "$T/origin.git" rev-parse feat) | -C $T/land check"
  check "land: the pushed head has main merged in" "$(git -C "$T/origin.git" merge-base --is-ancestor main feat && echo yes)" yes
  check "land: gives the turn up" "$(queue)" ""
  check "land: its turn named the agent, pull request and host" "$(grep -c '^POST .*/issues/7/comments' "$T/calls") $(grep -c '^DELETE ' "$T/calls")" "1 1"

  # Another holds it: wait, first come first served, until it is given up.
  rm -f "$T/landed" "$T/checked"
  put 1 41; put 2 42
  (sleep 1; rm "$Q/1.json"; sleep 1; rm "$Q/2.json") &
  t0=$(date +%s)
  out=$(gh_land 40 2>&1); rc=$?
  wait
  check "land: waits for those ahead" "$rc $(($(date +%s) - t0 >= 2)) $(cat "$T/landed" | cut -d' ' -f1-2)" "0 1 merge 40"
  grep -q '#40 waits for its turn to land: 2 ahead; heron-41 lands #41 (host h), heartbeat 0m ago' <<<"$out"; check "land: says who is ahead" $? 0
  grep -q '#40 waits for its turn to land: 1 ahead; heron-42 lands #42' <<<"$out"; check "land: says when the queue moves" $? 0
  check "land: gives the turn up after waiting" "$(queue)" ""

  # An abandoned turn (no heartbeat for C4_LAND_TTL) is dropped, not waited on.
  rm -f "$T/landed"
  put 5 41 '-1 hour'
  out=$(gh_land 40 2>&1)
  check "land: takes over an abandoned turn" "$? $(cut -d' ' -f1-2 "$T/landed") | $(queue)" "0 merge 40 | "
  grep -q 'dropped an abandoned merge turn: heron-41 lands #41 (host h), heartbeat 60m ago' <<<"$out"; check "land: says it dropped it" $? 0

  # Heartbeats: while it waits (whole seconds) and while make check runs.
  put 6 41
  : >"$T/calls"
  GH_LAND_BEAT=1 gh_land 40 --wait 3s >/dev/null 2>&1
  check "land: beats while it waits" "$? $(($(grep -c '^PATCH ' "$T/calls") >= 1))" "4 1"
  rm "$Q/6.json" "$T/landed"
  : >"$T/calls"
  out=$(GH_LAND_BEAT=0.2 CHECK_SLEEP=1.5 gh_land 40 2>&1)
  check "land: beats while make check runs" "$? $(($(grep -c '^PATCH ' "$T/calls") >= 3))" "0 1"

  # A fetch that races another worktree's fails once, then lands; one that
  # keeps failing gives the turn up after GH_LAND_FETCH_TRIES.
  git() {
    if [ "${3:-}" = fetch ] && [ "$(cat "$T/fetches" 2>/dev/null || echo 0)" -gt 0 ]; then
      echo $(($(cat "$T/fetches") - 1)) >"$T/fetches"
      echo "error: fetching ref refs/remotes/origin/main failed: incorrect old value provided" >&2
      return 1
    fi
    command git "$@"
  }
  rm -f "$T/landed"
  echo 1 >"$T/fetches"
  out=$(GH_LAND_FETCH_PAUSE=0 gh_land 40 2>&1)
  check "land: retries a failed fetch and lands" "$? $(cut -d' ' -f1-2 "$T/landed") | $(queue)" "0 merge 40 | "
  grep -q '#40: git fetch origin failed; trying again in 0s' <<<"$out"; check "land: says it retries the fetch" $? 0
  rm -f "$T/landed"
  echo 9 >"$T/fetches"
  out=$(GH_LAND_FETCH_PAUSE=0 GH_LAND_FETCH_TRIES=3 gh_land 40 2>&1)
  check "land: a fetch failing every try gives the turn up" "$? $(cat "$T/landed" 2>/dev/null) | $(queue) | $(cat "$T/fetches")" "1  |  | 6"
  grep -q '#40: git fetch origin failed 3 times' <<<"$out"; check "land: says the fetch failed" $? 0
  unset -f git

  # A red check gives the turn up, says so, and merges nothing.
  rm -f "$T/landed"
  git -C "$T/other" -c user.name=t -c user.email=t@t commit -q --allow-empty -m again && git -C "$T/other" push -q origin main
  touch "$T/red"
  out=$(gh_land 40 2>&1)
  check "land: a red check fails, merges nothing, gives the turn up" "$? $(cat "$T/landed" 2>/dev/null) | $(queue)" "1  | "
  grep -q '#40: make check failed (exit 1) with origin/main merged in' <<<"$out"; check "land: says the check failed" $? 0
  rm "$T/red"

  # A conflict with main is aborted, gives the turn up and merges nothing.
  echo main >"$T/other/feat" && git -C "$T/other" add feat && git -C "$T/other" -c user.name=t -c user.email=t@t commit -q -m clash && git -C "$T/other" push -q origin main
  out=$(gh_land 40 2>&1)
  check "land: a conflict fails, merges nothing, gives the turn up" "$? $(cat "$T/landed" 2>/dev/null) | $(queue) | $(g status --porcelain --untracked-files=no)" "1  |  | "
  grep -q '#40: merging origin/main into feat conflicts' <<<"$out"; check "land: says it conflicts" $? 0
  g reset -q --hard origin/feat

  # Not its turn within --wait: gives its place up, exit 4.
  put 8 41
  out=$(gh_land 40 --wait 1s 2>&1)
  check "land: gives up after --wait" "$? $(queue)" "4 8 "
  grep -q '#40 gave up its place after 1s: still 1 ahead; heron-41 lands #41' <<<"$out"; check "land: says it gave up" $? 0
  rm "$Q/8.json"

  # Refusals before it queues.
  g checkout -q -b elsewhere
  out=$(gh_land 40 2>&1)
  check "land: refuses from another branch" "$? $out | $(queue)" "1 github: #40's branch is feat, but this checkout is on elsewhere: land it from its own checkout | "
  g checkout -q feat
  echo dirty >"$T/land/feat"
  out=$(gh_land 40 2>&1)
  check "land: refuses uncommitted changes" "$? $(queue)" "1 "
  g checkout -q -- feat
  gh_land x 2>/dev/null; check "land: bad argument" $? 64
  exit $fail
) || fail=1
exit $fail
