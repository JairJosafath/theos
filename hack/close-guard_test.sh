#!/usr/bin/env bash
# Tests for hack/close-guard.sh, the PreToolUse hook that denies a gh pr
# create, edit or merge or a git commit whose text would close the parent of a
# plan too early, against a fake gh in a throwaway repository whose origin is
# this one.
# Run: hack/close-guard_test.sh
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
HOOK="$(cd "$(dirname "$0")" && pwd)/close-guard.sh"
export GH_TOKEN=x FAKE=$T/github CALLED=$T/called C4_GITHUB_REPO=JairJosafath/c4factory
fail=0
check() { if [ "$2" = "$3" ]; then echo "ok   $1"; else echo "FAIL $1: got [$2] want [$3]"; fail=1; fi; }

# A fake gh: gh api URL [--jq EXPR] answers from $FAKE/URL with / as _, through
# jq -r EXPR when the file is JSON; gh pr view prints $FAKE/current.
mkdir -p "$T/bin" "$FAKE"
cat >"$T/bin/gh" <<'EOF'
#!/usr/bin/env bash
echo "gh $*" >>"$CALLED"
if [ "$1 $2" = "pr view" ]; then cat "$FAKE/current" 2>/dev/null; exit; fi
url= jqx=
while [ $# -gt 0 ]; do
  case $1 in repos/*) url=$1 ;; --jq) jqx=$2; shift ;; esac
  shift
done
f=$FAKE/${url//\//_}
[ -e "$f" ] || exit 1
if [ -n "$jqx" ]; then jq -r "$jqx" "$f"; else cat "$f"; fi
EOF
chmod +x "$T/bin/gh"
PATH=$T/bin:$PATH
R=repos_JairJosafath_c4factory
issue() { jq -n --arg s "$2" --arg b "${3:-}" '{state: $s, body: $b}' >"$FAKE/${R}_issues_$1"; }
issue 467 open $'Depends on #472\nDepends on #475'
issue 472 closed
issue 475 open
issue 486 open 'Plan only.'
issue 300 open
jq -n '{title: "docs(486): plan", body: "Refs #486"}' >"$FAKE/${R}_pulls_510"
jq -n '[{commit: {message: "docs(486): plan\n\nSteps: #506 (turn on Ask by default, closes #486)"}}]' >"$FAKE/${R}_pulls_510_commits?per_page=100"
jq -n '{title: "fix(300): it works", body: "Closes #300"}' >"$FAKE/${R}_pulls_511"
jq -n '[{commit: {message: "fix(300): it works"}}]' >"$FAKE/${R}_pulls_511_commits?per_page=100"

git init -q "$T/repo"
git -C "$T/repo" remote add origin https://github.com/JairJosafath/c4factory.git
git init -q "$T/other"
git -C "$T/other" remote add origin https://example.com/someone/else.git

# hook CMD [DIR]: run the hook on a Bash tool call in DIR (default the repo).
hook() {
  : >"$CALLED"
  jq -n --arg c "$1" --arg d "${2:-$T/repo}" '{hook_event_name: "PreToolUse", tool_name: "Bash", cwd: $d, tool_input: {command: $c}}' |
    "$BASH" "$HOOK" >"$T/out" 2>"$T/err"
}
decision() { [ -s "$T/out" ] || { echo none; return; }; jq -r '.hookSpecificOutput.permissionDecision // "none"' "$T/out" 2>/dev/null || echo none; }
reason() { jq -r '.hookSpecificOutput.permissionDecisionReason // .hookSpecificOutput.additionalContext // ""' "$T/out" 2>/dev/null; }
calls() { grep -c . "$CALLED"; }

hook 'ls -la && make test'
check "other commands: allowed, no gh call" "$(decision) $(calls)" "none 0"
hook 'git commit -m "feat(300): it works"'
check "commit without a closing keyword: allowed, no gh call" "$(decision) $(calls)" "none 0"

hook "gh pr create --title t --body \"\$(cat <<'EOF'
Refs #486

Steps: #503, #506 (turn on Ask by default, closes #486)
EOF
)\""
check "pr create: closes #N in prose beside Refs #N is denied" "$(decision)" deny
check "pr create: the denial names the line" "$(reason | grep -c '"Steps: #503, #506 (turn on Ask by default, closes #486)" closes #486')" 1

printf 'Refs #486\n\nfixes #486\n' >"$T/repo/body.md"
hook 'gh pr edit 510 --body-file body.md'
check "pr edit: a --body-file is read" "$(decision)" deny

hook 'git -C . commit -q -m "feat(475): last step" -m "Closes #467"'
check "commit: closes a parent with an open step, denied" "$(decision) $(reason | grep -c 'step #475 is open')" "deny 1"
hook 'git commit -m "feat(475): last step" -m "Closes #475" -m "Closes #467"'
check "commit: the last step closes it and its parent, allowed" "$(decision)" none

hook 'gh pr merge 510 --merge'
check "pr merge: the pull request's commits are read, denied" "$(decision) $(reason | grep -c 'Steps: #506')" "deny 1"
hook 'gh pr merge https://github.com/JairJosafath/c4factory/pull/511 --merge --delete-branch'
check "pr merge: a clean pull request by URL, allowed" "$(decision)" none
hook 'gh pr merge --merge --delete-branch 510'
check "pr merge: the number after its flags" "$(decision)" deny
echo 511 >"$FAKE/current"
hook 'gh pr merge feat/439-shared-credentials-step-5 --merge'
check "pr merge: a branch is asked of gh pr view, not read as a number" "$(decision) $(grep -c 'pr view feat/439-shared-credentials-step-5' "$CALLED")" "none 1"
hook 'gh pr merge 510 -R someone/else --merge'
check "pr merge: another repository's pull request, not guarded" "$(decision)" none
hook 'gh pr merge 510; echo done'
check "pr merge: a number before a separator" "$(decision)" deny
echo 510 >"$FAKE/current"
hook 'gh pr merge --merge'
check "pr merge: without a number, the current branch's pull request" "$(decision)" deny
hook 'gh pr merge 999 --merge'
check "pr merge: GitHub can't be asked, denied" "$(decision) $(reason | grep -c 'could not read pull request #999')" "deny 1"

hook "echo \"it's\" && git commit -m \"Closes #467, that's it\""
check "commit: apostrophes around it don't hide it" "$(decision)" deny
hook 'echo "git commit -m \"closes #467\"" >notes.txt'
check "a command that only mentions git commit: allowed, no gh call" "$(decision) $(calls)" "none 0"

hook 'git commit -m "x" -m "closes #467"' "$T/other"
check "another repository: not guarded" "$(decision) $(calls)" "none 0"

rm "$FAKE/${R}_issues_467"
hook 'git commit -m "closes #467"'
check "commit: GitHub can't be asked, allowed with a warning" "$(decision) $(reason | grep -c 'could not be asked')" "none 1"

exit $fail
