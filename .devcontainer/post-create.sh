#!/usr/bin/env bash
# Runs once after the dev container is created. Safe to run again.
set -euo pipefail

step() { printf '\n==> %s\n' "$1"; }
export PATH="$HOME/.local/bin:$PATH"

step "kubebuilder"
if ! command -v kubebuilder >/dev/null 2>&1; then
  curl -fsSL -o /tmp/kubebuilder "https://go.kubebuilder.io/dl/latest/$(go env GOOS)/$(go env GOARCH)"
  sudo install -m 0755 /tmp/kubebuilder /usr/local/bin/kubebuilder
  rm -f /tmp/kubebuilder
fi

step "uv, for the Python templates"
if ! command -v uv >/dev/null 2>&1 && [ ! -x "$HOME/.local/bin/uv" ]; then
  curl -LsSf https://astral.sh/uv/install.sh | env UV_NO_MODIFY_PATH=1 sh
fi

step "Dev Container CLI, for testing scaffolded devcontainers and the Session client"
if ! command -v devcontainer >/dev/null 2>&1; then
  npm install -g @devcontainers/cli
fi

step "fzf, for fuzzy history search"
if ! command -v fzf >/dev/null 2>&1; then
  sudo apt-get update -qq
  sudo apt-get install -y -qq fzf
fi

step "Volumes"
sudo mkdir -p /commandhistory
sudo chown -R "$(id -u):$(id -g)" /commandhistory
if [ -d "$HOME/.claude" ]; then
  sudo chown -R "$(id -u):$(id -g)" "$HOME/.claude"
fi

step "zsh plugins"
ZSH_CUSTOM="${ZSH_CUSTOM:-$HOME/.oh-my-zsh/custom}"
for plugin in zsh-autosuggestions zsh-syntax-highlighting; do
  if [ ! -d "$ZSH_CUSTOM/plugins/$plugin" ]; then
    git clone --depth 1 "https://github.com/zsh-users/$plugin" "$ZSH_CUSTOM/plugins/$plugin"
  fi
done

# zsh-syntax-highlighting must stay last in this list.
sed -i 's/^plugins=(.*)$/plugins=(git golang docker kubectl helm gh fzf zsh-autosuggestions zsh-syntax-highlighting)/' "$HOME/.zshrc"

if ! grep -q "c4factory shell" "$HOME/.zshrc"; then
  cat >> "$HOME/.zshrc" <<'EOF'

# --- c4factory shell ------------------------------------------------------
export PATH="$HOME/.local/bin:$PATH"

# History lives in a volume, so it survives rebuilds and keeps feeding suggestions.
export HISTFILE=/commandhistory/.zsh_history
HISTSIZE=100000
SAVEHIST=100000
setopt INC_APPEND_HISTORY SHARE_HISTORY HIST_IGNORE_ALL_DUPS HIST_REDUCE_BLANKS

# Grey suggestions while you type: from history first, then from completions.
# Accept all with Right arrow or End, one word with Ctrl+Right.
ZSH_AUTOSUGGEST_STRATEGY=(history completion)
ZSH_AUTOSUGGEST_HIGHLIGHT_STYLE='fg=#6b7280'
bindkey '^[[1;5C' forward-word

# Completions oh-my-zsh doesn't ship.
command -v kind >/dev/null && source <(kind completion zsh)
command -v kubebuilder >/dev/null && source <(kubebuilder completion zsh)
command -v uv >/dev/null && eval "$(uv generate-shell-completion zsh)"
# ---------------------------------------------------------------------------
EOF
fi

step "Versions"
set +e
go version
kubebuilder version
kind version
kubectl version --client
helm version --short
uv --version
node --version
devcontainer --version
echo
echo "Ready. Open a new terminal to get the zsh setup."
