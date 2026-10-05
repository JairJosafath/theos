#!/usr/bin/env bash
# Tests for the needs/cluster rules in hack/lib/github.sh (pick, take, label)
# for flush releasing the claim of the handoff it posts,
# for close keeping the claim when the close fails, for take releasing a claim on a closed issue, and for pick's order, against a stubbed GitHub and a throwaway repository with two worktrees.
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
