#!/usr/bin/env bash
# The passing-tree cache of `make check`: a check that passed is recorded under
# the git tree hash of the working state (tracked and untracked non-ignored
# files alike), shared by every checkout on the device. `make check` on a tree
# that already passed says so and exits 0 without taking a heavy-job slot.
#
# The pass is recorded under the tree as it is after the check, so after
# manifests, generate and fmt rewrote what they rewrite. A tree whose generated
# or formatted files are stale never matches a recorded pass: running the check
# on it would change those files, so its hash differs from any tree that passed.
#
#   hack/check-cache.sh tree     print the tree hash of the working state
#   hack/check-cache.sh lookup   print the cached-pass line and exit 0 on a hit; exit 1 on a miss or FORCE=1
#   hack/check-cache.sh record   record a pass of the working state; warn and exit 0 when it can't
#   hack/check-cache.sh key TIER        print the hash of TIER's inputs in the working state
#   hack/check-cache.sh run TIER CMD... run CMD unless TIER's inputs passed on this device; record a pass
#
# A tier of make check (ui-test, test-hack, validate-yaml) is cached on its own,
# keyed on the git hashes of the files it reads and the versions of the tools
# it runs, so a change to hack/ doesn't rerun the UI's check, nor one to ui/ the
# shell tests (#1285). The whole-tree record stays for land and CHECK_CACHED.
#
#   CHECK_CACHE_DIR  where passes live (default $XDG_CACHE_HOME/c4factory/checks, which ./dev puts in .dev/cache)
#   FORCE            non-empty: lookup always misses
set -uo pipefail

DIR=${CHECK_CACHE_DIR:-${XDG_CACHE_HOME:-$HOME/.cache}/c4factory/checks}

# with_index CMD...: runs CMD with GIT_INDEX_FILE set to a copy of the index,
# for CMD to git add the working state to: git add only rehashes what changed.
# The copy keeps the index's mtime: git rehashes an entry whose file is as new
# as the index (racy git), and a fresh mtime would make it trust stale stat data.
with_index() {
  local index tmp
  index=$(git rev-parse --path-format=absolute --git-path index) || return 1
  tmp=$(mktemp) || return 1
  if [ -f "$index" ]; then cp -p "$index" "$tmp"; else rm -f "$tmp"; fi
  GIT_INDEX_FILE=$tmp "$@"
  local rc=$?
  rm -f "$tmp"
  return $rc
}

# tree: the hash git would give the working state if it were all added.
tree() { with_index add_all git write-tree; }

# add_all CMD...: git add -A, then CMD.
add_all() { git add -A >/dev/null 2>&1 && "$@"; }

# tier_paths TIER: the pathspecs of the files TIER reads, a "--" line between
# groups. Git's exclude pathspecs override every positive one, so each group
# is listed on its own. docs/dev/testenv.md says what each covers.
tier_paths() {
  case $1 in
    ui-test) printf '%s\n' ui Makefile 'hack/*.mk' hack/check-cache.sh ;;
    # The shell tests read the Makefile, the chart, config/, .claude/ and more,
    # so everything but the Go, UI and docs trees, and the files there they read.
    test-hack)
      printf '%s\n' . ':!api' ':!cmd' ':!internal' ':!templates' ':!test' ':!features' ':!docs' ':!ui' --
      printf '%s\n' docs/getting-started.md docs/dev/testenv.md \
        ui/Dockerfile ui/package.json ui/package-lock.json
      ;;
    # validate-yaml renders repositories with go run ./hack/render and fences
    # the runner with ./hack/runner-fence: the module's packages they build
    # count too, go.sum the rest.
    validate-yaml)
      printf '%s\n' config dist api templates test/e2e/testdata test/crds test/invalid \
        hack/validate-yaml.sh 'hack/chart-*.sh' hack/k8s-openapi.sh hack/seed/job.yaml hack/session/pod.yaml \
        Makefile 'hack/*.mk' hack/check-cache.sh go.mod go.sum
      local mod
      mod=$(go list -m -f '{{.Dir}}') || return 1
      go list -deps -f '{{if not .Standard}}{{.Dir}}{{end}}' ./hack/render ./hack/runner-fence |
        sed -n "s|^$mod/||p"
      [ "${PIPESTATUS[0]}" = 0 ]
      ;;
    *) echo "check-cache: unknown tier $1" >&2; return 64 ;;
  esac
}

# tier_tools TIER: the versions of the tools TIER runs that the Makefile
# doesn't pin. A missing tool is keyed as missing, never a failure of the key:
# CI's shell tests run without Node (#1285). Not as bash's error, which names
# the script as it was called, so make's hack/check-cache.sh and a full path
# keyed apart (#1399).
tier_tools() {
  case $1 in
    ui-test) version node --version; version npm --version ;;
    test-hack) bash --version | head -1; git --version ;;
    validate-yaml) version go version ;;
  esac 2>&1
  return 0
}

# version TOOL ARGS...: TOOL's version, or "TOOL: missing" when it isn't on PATH.
version() {
  if command -v "$1" >/dev/null; then "$@"; else echo "$1: missing"; fi
}

# tier_desc TIER: what TIER's key covers, in a word, for the cached line.
tier_desc() {
  case $1 in
    ui-test) echo "ui/" ;;
    test-hack) echo "hack/ and the rest outside the Go, UI and docs trees" ;;
    validate-yaml) echo "the YAML, api/, templates/ and hack/render" ;;
  esac
}

# list_groups: git ls-files -s of each group of pathspecs on stdin, each
# group git added first.
list_groups() {
  local group=() p
  while IFS= read -r p; do
    if [ "$p" = -- ]; then
      [ ${#group[@]} -gt 0 ] && { list_group "${group[@]}" || return 1; }
      echo --
      group=()
    else
      group+=("$p")
    fi
  done
  [ ${#group[@]} -eq 0 ] || list_group "${group[@]}"
}

# list_group PATHSPEC...: git add -A of the working state under PATHSPEC only,
# then git ls-files -s of it. Only the tier's files are added, so a file
# elsewhere that vanishes or can't be read while git adds (a parallel test's
# scratch in the checkout) leaves the key alone (#1814). git add fails on a
# pathspec that matches nothing, so those are left out of it.
list_group() {
  local add=() matched= p
  for p in "$@"; do
    case $p in
      :!* | ':(exclude)'*) add+=("$p") ;;
      *) [ -n "$(git ls-files -c -o --exclude-standard -- "$p" 2>/dev/null | head -1)" ] && add+=("$p") && matched=1 ;;
    esac
  done
  if [ -n "$matched" ]; then git add -A -- "${add[@]}" >/dev/null 2>&1 || return 1; fi
  git ls-files -s -- "$@"
}

# key TIER: the hash of TIER's name, its inputs as they are in the working
# state and its tools' versions.
key() {
  local paths
  cd "$(git rev-parse --show-toplevel)" || return 1
  paths=$(tier_paths "$1") || return
  {
    echo "tier $1"
    with_index list_groups <<<"$paths" || return 1
    tier_tools "$1"
  } | git hash-object --stdin
  [ "${PIPESTATUS[0]}" = 0 ]
}

# run TIER CMD...: CMD's exit code, or 0 without running it when TIER's key
# passed on this device. A key that can't be taken runs CMD and records nothing.
run() {
  local tier=$1 k f when= checkout= name val
  shift
  if ! k=$(key "$tier"); then
    echo "$tier: warning: could not hash its inputs; running it uncached" >&2
    "$@"
    return
  fi
  f=$DIR/tiers/$tier-$k
  if [ -z "${FORCE:-}" ] && [ -f "$f" ]; then
    while IFS='=' read -r name val; do
      case $name in
        when) when=$val ;;
        checkout) checkout=$val ;;
      esac
    done <"$f"
    echo "$tier: cached ($(tier_desc "$tier") unchanged since $when, passed in $checkout); FORCE=1 to rerun"
    return 0
  fi
  "$@" || return
  if ! { mkdir -p "$DIR/tiers" &&
    printf 'when=%s\ncheckout=%s\n' "$(date '+%Y-%m-%d %H:%M')" "$(git rev-parse --show-toplevel)" >"$f.$$" &&
    mv "$f.$$" "$f"; } 2>/dev/null; then
    rm -f "$f.$$" 2>/dev/null
    echo "$tier: passed; warning: $DIR is not writable, pass not recorded" >&2
  fi
  return 0
}

lookup() {
  local t when= checkout= agent= key val
  [ -n "${FORCE:-}" ] && return 1
  t=$(tree) || return 1
  [ -f "$DIR/$t" ] || return 1
  while IFS='=' read -r key val; do
    case $key in
      when) when=$val ;;
      checkout) checkout=$val ;;
      agent) agent=$val ;;
    esac
  done <"$DIR/$t"
  echo "make check: passed on tree $t at $when in $checkout${agent:+ by $agent} (cached); FORCE=1 to rerun"
}

# record is best effort: the check passed, so a cache it can't write (a
# sandboxed session may only write inside its own worktree) warns and exits 0.
record() {
  local t agent= claim
  if ! t=$(tree); then
    echo "make check: passed; warning: could not hash the tree, pass not recorded" >&2
    return 0
  fi
  claim=$(git rev-parse --show-toplevel)/.relay/claim
  [ -s "$claim" ] && agent=$(jq -r '.name // empty' "$claim" 2>/dev/null)
  if ! { mkdir -p "$DIR" &&
    printf 'when=%s\ncheckout=%s\nagent=%s\n' \
      "$(date '+%Y-%m-%d %H:%M')" "$(git rev-parse --show-toplevel)" "${agent:-${C4_AGENT:-}}" >"$DIR/$t.$$" &&
    mv "$DIR/$t.$$" "$DIR/$t"; } 2>/dev/null; then
    rm -f "$DIR/$t.$$" 2>/dev/null
    echo "make check: passed on tree $t; warning: $DIR is not writable, pass not recorded" >&2
    return 0
  fi
  echo "make check: passed on tree $t; recorded"
}

case ${1:-} in
  tree) tree ;;
  lookup) lookup ;;
  record) record ;;
  key) [ $# -eq 2 ] || { echo "usage: hack/check-cache.sh key TIER" >&2; exit 64; }; key "$2" ;;
  run) [ $# -ge 3 ] || { echo "usage: hack/check-cache.sh run TIER CMD..." >&2; exit 64; }; shift; run "$@" ;;
  *) echo "usage: hack/check-cache.sh tree | lookup | record | key TIER | run TIER CMD..." >&2; exit 64 ;;
esac
