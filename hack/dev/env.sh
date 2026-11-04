# shellcheck shell=bash
# The project environment: every tool comes from, and every cache goes to, .dev/.
# Sourced (not run) by ./dev and by the project shell (hack/dev/zshrc). Works in
# bash and zsh. Expects THEOS_ROOT to point at the checkout.
: "${THEOS_ROOT:?set THEOS_ROOT to the repository root before sourcing hack/dev/env.sh}"
# A git worktree uses the main checkout's .dev/, so tools and caches exist once
# per device: also when make, a test or VS Code made a .dev/ of its own here
# before ./dev setup could link it, which holds no tools (#1132).
THEOS_DEV="$THEOS_ROOT/.dev"
if [ -f "$THEOS_ROOT/.git" ]; then
  _c4_common=$(git -C "$THEOS_ROOT" rev-parse --path-format=absolute --git-common-dir 2>/dev/null) || _c4_common=
  _c4_common=${_c4_common%/}
  case $_c4_common in */.git) THEOS_DEV="${_c4_common%/.git}/.dev" ;; esac
  unset _c4_common
fi
export THEOS_DEV

# Tools downloaded by ./dev setup, then the Makefile's (versioned names), first
# on PATH.
for _c4_dir in "$THEOS_DEV/tools/node/bin" "$THEOS_DEV/tools/go/bin" "$THEOS_DEV/go/bin" \
  "$THEOS_DEV/npm/bin" "$THEOS_DEV/bin" "$THEOS_DEV/make/bin"; do
  case ":$PATH:" in
    *":$_c4_dir:"*) ;;
    *) PATH="$_c4_dir:$PATH" ;;
  esac
done
unset _c4_dir
export PATH

# Go: workspace, module cache, build cache and `go env -w` all inside .dev/.
export GOPATH="$THEOS_DEV/go"
export GOCACHE="$THEOS_DEV/cache/go-build"
export GOENV="$THEOS_DEV/go/env"
# Go makes its module cache read-only by default, which makes a plain `rm -rf`
# of the folder fail. Keep it writable so deleting the folder just works.
case " ${GOFLAGS:-} " in
  *" -modcacherw "*) ;;
  *) export GOFLAGS="${GOFLAGS:+$GOFLAGS }-modcacherw" ;;
esac

# Anything that follows the XDG convention (Helm, uv and the Pythons it installs,
# gopls, Go telemetry, oh-my-zsh, ...) writes into .dev/ too.
export XDG_CONFIG_HOME="$THEOS_DEV/config"
export XDG_CACHE_HOME="$THEOS_DEV/cache"
export XDG_DATA_HOME="$THEOS_DEV/share"
export XDG_STATE_HOME="$THEOS_DEV/state"
# Except the GitHub CLI: its login stays machine-wide, as it was before.
export GH_CONFIG_DIR="${GH_CONFIG_DIR:-$HOME/.config/gh}"

# npm -g installs into .dev/npm; its cache sits next to the others.
export NPM_CONFIG_PREFIX="$THEOS_DEV/npm"
export NPM_CONFIG_CACHE="$THEOS_DEV/cache/npm"

# Kubernetes: Kind writes cluster contexts here, never into ~/.kube.
export KUBECONFIG="$THEOS_DEV/kube/config"
export KUBECACHEDIR="$THEOS_DEV/kube/cache"

# Claude Code: login, settings and session history belong to this project.
export CLAUDE_CONFIG_DIR="$THEOS_DEV/claude"

# A lite device has no cluster; hack/lib/github.sh and hack/relay.sh read this.
if [ -f "$THEOS_DEV/lite" ]; then
  export THEOS_CLUSTER=none
fi
