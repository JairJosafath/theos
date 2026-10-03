#!/usr/bin/env bash
# Tests for the needs/cluster rules in hack/lib/github.sh (pick, take, label)
# for close keeping the claim when the close fails, for take releasing a claim on a closed issue, and for pick's order, against a stubbed GitHub and a throwaway repository with two worktrees.
# Run: hack/lib/github_test.sh
. "$(dirname "$0")/github.sh"
unset C4_CLUSTER # the light devcontainer sets it; each test sets its own
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
  case $url in
    */issues/50/labels*) printf 'needs/cluster\nstatus/in-progress\n' ;;
    */issues/51/labels*) printf 'status/in-progress\n' ;;
    */issues/60) echo '{"number":60,"title":"Deploy","labels":[{"name":"needs/cluster"},{"name":"kind/feature"}]}' ;;
    */issues/71) return 1 ;; # closing #71 fails
    */issues/73) echo closed ;; # --jq .state, as take asks
    */issues/74) echo open ;;
    */issues/61) echo '{"number":61,"title":"Docs","labels":[{"name":"kind/docs"}]}' ;;
    */issues/*/comments*)
      if [[ " $* " == *" POST "* ]]; then touch "$T/posted"; echo 999; return; fi
      [ -e "$T/posted" ] || { echo '[[]]'; return; }
      echo "[[{\"id\":999,\"created_at\":\"$now\",\"updated_at\":\"$now\",\"html_url\":\"u\",\"user\":{\"login\":\"x\"},\"body\":\"<!-- c4-claim -->\\n**otter-6x** works on this issue\"}]]" ;;
    */issues/comments/1) # the claim() comment: heron-73 and heron-74 still hold theirs
      if [[ " $* " == *" PATCH "* ]]; then cat >"$T/edited"; return; fi
      echo '{"id":1,"updated_at":"'"$now"'","body":"<!-- c4-claim -->\n**heron-73** works on this issue"}' |
        { [ -e "$T/hold74" ] && sed 's/heron-73/heron-74/' || cat; } ;;
    */issues/comments*) echo '[[]]' ;;
    */issues\?state=open*) [ -n "${OPEN:-}" ] && { echo "$OPEN"; return; }; echo '[[{"number":60,"title":"Deploy","html_url":"u60","body":"","labels":[{"name":"needs/cluster"},{"name":"status/ready"}]},{"number":61,"title":"Docs","html_url":"u61","body":"","labels":[{"name":"status/ready"}]}]]' ;;
    *) return 0 ;;
  esac
}
gh_set_status() { :; }
fail=0
check() { if [ "$2" = "$3" ]; then echo "ok   $1"; else echo "FAIL $1: got [$2] want [$3]"; fail=1; fi; }
claim() { mkdir -p "$1/.relay"; jq -n --argjson i "$2" --argjson b "$3" '{issue:$i,id:1,name:"heron-\($i)",branch:"b",labels:[],beat:$b}' >"$1/.relay/claim"; }
clear() { rm -rf "$T"/{main,wt2,wt3}/.relay; }

clear
check "pick, cluster free" "$(gh_pick | cut -f1)" 60
check "pick, no cluster" "$(C4_CLUSTER=none gh_pick | cut -f1)" 61
claim "$T/wt2" 50 "$(date +%s)"
check "holder" "$(gh_cluster_holder)" "heron-50 (#50, $T/wt2)"
check "pick, cluster busy" "$(gh_pick | cut -f1)" 61
rm -f "$T/posted"; C4_CLUSTER= gh_take 60 >/dev/null 2>&1; check "take 60, cluster busy" $? 4
rm -f "$T/posted"; gh_take 61 >/dev/null 2>&1; check "take 61, cluster busy" $? 0
rm -f "$T/main/.relay/claim"
claim "$T/wt2" 50 $(($(date +%s) - 3 * 3600))
check "stale holder ignored" "$(gh_cluster_holder)" ""
claim "$T/wt2" 51 "$(date +%s)"
check "holder on unlabelled issue ignored" "$(gh_cluster_holder)" ""
clear; claim "$T/main" 50 "$(date +%s)"
check "own claim ignored" "$(gh_cluster_holder)" ""
clear
rm -f "$T/posted"; C4_CLUSTER=none gh_take 60 >/dev/null 2>&1; check "take 60, no cluster" $? 4
rm -f "$T/posted"; gh_take 60 >/dev/null 2>&1; check "take 60, cluster free" $? 0
rm -f "$T/main/.relay/claim"
claim "$T/wt3" 50 "$(date +%s)"
gh_label 61 needs/cluster >/dev/null 2>&1; check "label needs/cluster, cluster busy" $? 4
C4_CLUSTER=none gh_label 61 needs/cluster >/dev/null 2>&1; check "label needs/cluster, no cluster" $? 0
gh_label 61 kind/docs >/dev/null 2>&1; check "label other" $? 0
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
exit $fail
