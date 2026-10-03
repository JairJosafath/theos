#!/usr/bin/env bash
# Tests for the needs/cluster rules in hack/lib/github.sh (pick, take, label),
# against a stubbed GitHub and a throwaway repository with two worktrees.
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
    */issues/61) echo '{"number":61,"title":"Docs","labels":[{"name":"kind/docs"}]}' ;;
    */issues/*/comments*)
      if [[ " $* " == *" POST "* ]]; then touch "$T/posted"; echo 999; return; fi
      [ -e "$T/posted" ] || { echo '[[]]'; return; }
      echo "[[{\"id\":999,\"created_at\":\"$now\",\"updated_at\":\"$now\",\"html_url\":\"u\",\"user\":{\"login\":\"x\"},\"body\":\"<!-- c4-claim -->\\n**otter-6x** works on this issue\"}]]" ;;
    */issues/comments*) echo '[[]]' ;;
    */issues\?state=open*) echo '[[{"number":60,"title":"Deploy","html_url":"u60","body":"","labels":[{"name":"needs/cluster"},{"name":"status/ready"}]},{"number":61,"title":"Docs","html_url":"u61","body":"","labels":[{"name":"status/ready"}]}]]' ;;
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
exit $fail
