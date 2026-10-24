#!/usr/bin/env bash
# Tests for hack/pull.sh: main is fast-forwarded where it is checked out
# nowhere and left alone where another worktree has it, also when git worktree
# list writes more than a pipe holds, and the checkout C4_PULL_REPO names is
# pulled, against a throwaway origin and clone.
# Run: hack/pull_test.sh
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
fail=0
check() { if [ "$2" = "$3" ]; then echo "ok   $1"; else echo "FAIL $1: got [$2] want [$3]"; fail=1; fi; }
export GIT_AUTHOR_NAME=t GIT_AUTHOR_EMAIL=t@t GIT_COMMITTER_NAME=t GIT_COMMITTER_EMAIL=t@t
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1
unset IS_SANDBOX # an agent sandbox sets it; pull.sh does nothing there (#1894)

# origin, with pull.sh in it, and a clone on a feature branch.
git init -q -b main "$T/src"
mkdir "$T/src/hack"
cp "$(dirname "$0")/pull.sh" "$T/src/hack/"
git -C "$T/src" add -A && git -C "$T/src" commit -qm one
git clone -q --bare "$T/src" "$T/origin.git"
git clone -q "$T/origin.git" "$T/clone"
git -C "$T/clone" switch -qc feature

# advance: a new commit on origin/main; prints it.
advance() {
  git -C "$T/src" commit -q --allow-empty -m "$1"
  git -C "$T/src" push -q "$T/origin.git" main
  git -C "$T/src" rev-parse HEAD
}

# A git whose worktree list goes on writing after the worktrees, more than a
# pipe holds: under pipefail a grep -q on its pipe would exit early and read a
# "no".
REAL_GIT=$(command -v git)
mkdir "$T/bin"
cat >"$T/bin/git" <<EOF
#!/usr/bin/env bash
"$REAL_GIT" "\$@" || exit
[ "\$1 \$2" = "worktree list" ] && exec seq 100000
exit 0
EOF
chmod +x "$T/bin/git"
pull() { (cd "$T/clone" && PATH="$T/bin:$PATH" hack/pull.sh); }

# main checked out nowhere: the ref moves.
want=$(advance two)
# In an agent sandbox (IS_SANDBOX=1) pull.sh exits 0 at once and main stays
# where it is (#1894); the same pull outside one moves it, next.
before=$(git -C "$T/clone" rev-parse main)
out=$(IS_SANDBOX=1 pull 2>&1); rc=$?
check "sandbox: exit 0" "$rc" 0
check "sandbox: silent" "$out" ""
check "sandbox: main stays" "$(git -C "$T/clone" rev-parse main)" "$before"
out=$(pull)
check "nowhere: main moves" "$(git -C "$T/clone" rev-parse main)" "$want"
check "nowhere: says so" "$(grep -c 'pull: main .* on feature' <<<"$out")" 1

# main checked out in another worktree: left alone.
git -C "$T/clone" worktree add -q "$T/wt-main" main
before=$(git -C "$T/clone" rev-parse main)
advance three >/dev/null
out=$(pull)
check "other worktree: main left alone" "$(git -C "$T/clone" rev-parse main)" "$before"
check "other worktree: says why" "$(grep -c 'checked out in another worktree' <<<"$out")" 1
check "other worktree: its checkout untouched" "$(git -C "$T/wt-main" status --porcelain)" ""

# C4_PULL_REPO: another checkout's copy pulls the checkout it names, as the
# relay runs origin/main's copy on the checkout it was started from (#1126).
want=$(git -C "$T/src" rev-parse main)
out=$(C4_PULL_REPO=$T/wt-main "$T/src/hack/pull.sh")
check "C4_PULL_REPO: that checkout's main moves" "$(git -C "$T/wt-main" rev-parse HEAD)" "$want"
check "C4_PULL_REPO: says so" "$(grep -c 'pull: main .* on main' <<<"$out")" 1

exit $fail
