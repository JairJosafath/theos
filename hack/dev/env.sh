# shellcheck shell=bash
# The project environment: every tool comes from, and every cache goes to, .dev/.
# Sourced (not run) by ./dev and by the project shell (hack/dev/zshrc). Works in
# bash and zsh. Expects C4_ROOT to point at the checkout.
: "${C4_ROOT:?set C4_ROOT to the repository root before sourcing hack/dev/env.sh}"
export C4_DEV="$C4_ROOT/.dev"

# Tools downloaded by ./dev setup, then the Makefile's bin/, first on PATH.
for _c4_dir in "$C4_DEV/tools/node/bin" "$C4_DEV/tools/go/bin" "$C4_DEV/go/bin" \
  "$C4_DEV/npm/bin" "$C4_DEV/bin" "$C4_ROOT/bin"; do
  case ":$PATH:" in
    *":$_c4_dir:"*) ;;
    *) PATH="$_c4_dir:$PATH" ;;
  esac
done
unset _c4_dir
export PATH

# Go: workspace, module cache, build cache and `go env -w` all inside .dev/.
export GOPATH="$C4_DEV/go"
export GOCACHE="$C4_DEV/cache/go-build"
export GOENV="$C4_DEV/go/env"
# Go makes its module cache read-only by default, which makes a plain `rm -rf`
# of the folder fail. Keep it writable so deleting the folder just works.
case " ${GOFLAGS:-} " in
  *" -modcacherw "*) ;;
  *) export GOFLAGS="${GOFLAGS:+$GOFLAGS }-modcacherw" ;;
esac

# Anything that follows the XDG convention (Helm, uv and the Pythons it installs,
# gopls, Go telemetry, oh-my-zsh, ...) writes into .dev/ too.
export XDG_CONFIG_HOME="$C4_DEV/config"
export XDG_CACHE_HOME="$C4_DEV/cache"
export XDG_DATA_HOME="$C4_DEV/share"
export XDG_STATE_HOME="$C4_DEV/state"
# Except the GitHub CLI: its login stays machine-wide, as it was before.
export GH_CONFIG_DIR="${GH_CONFIG_DIR:-$HOME/.config/gh}"

# npm -g installs into .dev/npm; its cache sits next to the others.
export NPM_CONFIG_PREFIX="$C4_DEV/npm"
export NPM_CONFIG_CACHE="$C4_DEV/cache/npm"

# Kubernetes: Kind writes cluster contexts here, never into ~/.kube.
export KUBECONFIG="$C4_DEV/kube/config"
export KUBECACHEDIR="$C4_DEV/kube/cache"

# Claude Code: login, settings and session history belong to this project.
export CLAUDE_CONFIG_DIR="$C4_DEV/claude"

# A lite device has no cluster; hack/lib/github.sh and hack/relay.sh read this.
if [ -f "$C4_DEV/lite" ]; then
  export C4_CLUSTER=none
fi
