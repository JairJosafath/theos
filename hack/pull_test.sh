#!/usr/bin/env bash
# Tests for hack/pull.sh: main is fast-forwarded where it is checked out
# nowhere and left alone where another worktree has it, also when git worktree
# list writes more than a pipe holds, against a throwaway origin and clone.
# Run: hack/pull_test.sh
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
fail=0
check() { if [ "$2" = "$3" ]; then echo "ok   $1"; else echo "FAIL $1: got [$2] want [$3]"; fail=1; fi; }
export GIT_AUTHOR_NAME=t GIT_AUTHOR_EMAIL=t@t GIT_COMMITTER_NAME=t GIT_COMMITTER_EMAIL=t@t
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1

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

exit $fail
