#!/usr/bin/env bash
# Tests for ./dev and hack/dev/env.sh in git worktrees (#1132): a worktree
# whose .dev/ make or a test made before ./dev setup could link it still runs
# with the main checkout's tools and caches, against a throwaway repository with
# a fake tool in the main checkout's .dev/bin and fakes for curl, kind and zsh;
# and ./dev setup in the main checkout, every tool already there, warning when
# git has no committer identity (#1362).
# Run: hack/dev/dev_test.sh
T=$(mktemp -d)
trap 'chmod -R u+w "$T" 2>/dev/null; rm -rf "$T"' EXIT
SRC="$(cd "$(dirname "$0")/../.." && pwd)"
fail=0
check() { if [ "$2" = "$3" ]; then echo "ok   $1"; else echo "FAIL $1: got [$2] want [$3]"; fail=1; fi; }
export GIT_AUTHOR_NAME=t GIT_AUTHOR_EMAIL=t@t GIT_COMMITTER_NAME=t GIT_COMMITTER_EMAIL=t@t
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1

# The main checkout, set up: a tool in .dev/bin and the project shell's .zshrc.
git init -q -b main "$T/main"
mkdir -p "$T/main/hack/dev"
cp "$SRC/dev" "$T/main/"
cp "$SRC/hack/dev/env.sh" "$T/main/hack/dev/"
printf '.dev\n' >"$T/main/.gitignore"
git -C "$T/main" add -A && git -C "$T/main" commit -qm one
mkdir -p "$T/main/.dev/bin" "$T/main/.dev/zsh"
printf '#!/bin/sh\necho main tools\n' >"$T/main/.dev/bin/c4-tool"
chmod +x "$T/main/.dev/bin/c4-tool"
touch "$T/main/.dev/zsh/.zshrc"

# Fakes that record their calls: nothing below may download or touch a cluster.
# zsh prints the project shell it was given.
mkdir -p "$T/fake" "$T/home"
for f in curl kind kubectl sudo; do
  printf '#!/bin/sh\necho "%s $*" >>"%s/called"\nexit 1\n' "$f" "$T" >"$T/fake/$f"
done
# The system packages ./dev setup would ask apt for.
for f in make gcc; do printf '#!/bin/sh\n' >"$T/fake/$f"; done
printf '#!/bin/sh\necho "ZDOTDIR=$ZDOTDIR"\n' >"$T/fake/zsh"
chmod +x "$T/fake/"*

# dev DIR ARGS...: DIR/dev ARGS from a plain shell, with no project tools on PATH.
dev() {
  local dir=$1
  shift
  (cd "$dir" && env -i HOME="$T/home" PATH="$T/fake:/usr/bin:/bin" \
    GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1 ./dev "$@" 2>&1)
}

# A worktree where make ran first: its .dev/ holds caches and no bin/ (#1132).
git -C "$T/main" worktree add -q "$T/wt" -b wt
mkdir -p "$T/wt/.dev/cache/go-build"
check "#1132 leftover .dev: ./dev run finds the main checkout's tools" "$(dev "$T/wt" run c4-tool)" "main tools"
check "leftover .dev: caches go to the main checkout's" \
  "$(dev "$T/wt" run sh -c 'echo "$C4_DEV $GOCACHE"')" "$T/main/.dev $T/main/.dev/cache/go-build"
check "leftover .dev: ./dev shell opens the main checkout's shell" "$(dev "$T/wt" shell)" "ZDOTDIR=$T/main/.dev/zsh"
out=$(dev "$T/wt" setup)
check "leftover .dev: ./dev setup succeeds" "$?" 0
check "leftover .dev: ./dev setup says it shares" "$(grep -c "Worktree: sharing $T/main/.dev" <<<"$out")" 1
check "leftover .dev: ./dev setup points at ./dev nuke" "$(grep -c 'nuke deletes it' <<<"$out")" 1
check "leftover .dev: ./dev setup keeps it" "$([ -d "$T/wt/.dev/cache/go-build" ] && [ ! -L "$T/wt/.dev" ] && echo kept)" kept

# ./dev nuke there deletes only the worktree's own .dev/, after asking.
out=$( (cd "$T/wt" && env -i HOME="$T/home" PATH="$T/fake:/usr/bin:/bin" ./dev nuke <<<n 2>&1))
check "leftover .dev: nuke answered no keeps it" "$([ -d "$T/wt/.dev" ] && echo kept)" kept
out=$( (cd "$T/wt" && env -i HOME="$T/home" PATH="$T/fake:/usr/bin:/bin" ./dev nuke <<<y 2>&1))
check "leftover .dev: nuke deletes it" "$([ -e "$T/wt/.dev" ] || echo gone)" gone
check "leftover .dev: nuke says the clusters stay" "$(grep -c 'Kind clusters stay' <<<"$out")" 1
check "leftover .dev: nuke leaves the main checkout's" "$([ -x "$T/main/.dev/bin/c4-tool" ] && echo kept)" kept

# Then ./dev setup links the main checkout's .dev/, as in a fresh worktree.
dev "$T/wt" setup >/dev/null
check "no .dev: ./dev setup links the main checkout's" "$(readlink "$T/wt/.dev")" "$T/main/.dev"
check "linked: ./dev run finds the tools" "$(dev "$T/wt" run c4-tool)" "main tools"
out=$(dev "$T/wt" nuke)
check "linked: nuke removes only the link" "$([ -e "$T/wt/.dev" ] || [ ! -x "$T/main/.dev/bin/c4-tool" ] || echo ok)" ok

# A worktree before the main checkout's setup is told to run it there.
git -C "$T/main" worktree add -q "$T/wt2" -b wt2
mv "$T/main/.dev/bin" "$T/main/.dev/bin.off"
out=$(dev "$T/wt2" setup)
check "main not set up: setup fails" "$?" 1
check "main not set up: says where to run it" "$(grep -c "worktree of $T/main; run ./dev setup there first" <<<"$out")" 1
check "main not set up: no link" "$([ -e "$T/wt2/.dev" ] || echo none)" none
mv "$T/main/.dev/bin.off" "$T/main/.dev/bin"

# The main checkout keeps its own .dev/.
check "main checkout: its own .dev" "$(dev "$T/main" run sh -c 'echo "$C4_DEV"')" "$T/main/.dev"

# The project shell sources env.sh in zsh.
if [ -x /usr/bin/zsh ] || [ -x /bin/zsh ]; then
  mkdir -p "$T/wt2/.dev/cache"
  check "zsh: a worktree's leftover .dev resolves to the main checkout's" \
    "$(cd "$T/wt2" && env -i PATH=/usr/bin:/bin C4_ROOT="$T/wt2" zsh -fc 'source hack/dev/env.sh && print -r -- $C4_DEV')" "$T/main/.dev"
else
  echo "skip zsh: no zsh on this device"
fi

# ./dev setup in the main checkout, every tool already there (a lite device:
# no Docker, Kind or Helm), warns when git has no committer identity, in the
# project environment (XDG_CONFIG_HOME in .dev/, empty), with the commands
# that set one, and still finishes (#1362). The global config says
# useConfigOnly, as hack/relay_test.sh's #1289 case does: git would guess an
# identity from the account and the host otherwise, on some devices.
touch "$T/main/.dev/lite"
mkdir -p "$T/main/.dev/tools/go/bin" "$T/main/.dev/tools/node/bin" "$T/main/.dev/npm/bin" \
  "$T/main/.dev/zsh/oh-my-zsh" "$T/main/.dev/zsh/custom/plugins/zsh-autosuggestions" \
  "$T/main/.dev/zsh/custom/plugins/zsh-syntax-highlighting"
for f in tools/go/bin/go tools/node/bin/node npm/bin/claude bin/uv bin/jq bin/fzf bin/kubebuilder; do
  printf '#!/bin/sh\necho "%s"\n' "${f##*/} fake" >"$T/main/.dev/$f"
  chmod +x "$T/main/.dev/$f"
done
printf '[user]\n\tuseConfigOnly = true\n' >"$T/noident"
printf '[user]\n\tname = t\n\temail = t@t\n' >"$T/ident"
setup_main() { # GIT_CONFIG_GLOBAL
  (cd "$T/main" && env -i HOME="$T/home" PATH="$T/fake:/usr/bin:/bin" \
    GIT_CONFIG_GLOBAL="$1" GIT_CONFIG_NOSYSTEM=1 ./dev setup 2>&1)
}
out=$(setup_main "$T/noident")
check "no identity (#1362): ./dev setup still finishes" "$?" 0
check "no identity (#1362): says git can't commit, with git's reason" \
  "$(grep -c "WARNING: git can't commit here: fatal: no email was given and auto-detection is disabled" <<<"$out")" 1
check "no identity (#1362): names the two commands" "$(grep -c "git config --global user\.\(name\|email\) " <<<"$out")" 2
check "no identity (#1362): the warning comes after the tools" \
  "$(grep -o "==> Versions\|==> Git identity\|^Ready" <<<"$out" | tr '\n' ' ')" "==> Versions ==> Git identity Ready "
out=$(setup_main "$T/ident")
check "identity (#1362): ./dev setup finishes" "$?" 0
check "identity (#1362): says nothing of it" "$(grep -c "Git identity\|can't commit" <<<"$out")" 0

check "nothing downloaded or touched a cluster" "$(cat "$T/called" 2>/dev/null)" ""
exit $fail
