#!/usr/bin/env bash
# gh with a GitHub token: GH_TOKEN from the environment (hack/evaluate.sh
# exports it), else from the git credential helper. Never prints the token.
set -euo pipefail
if [ -z "${GH_TOKEN:-}" ]; then
  GH_TOKEN=$(printf 'protocol=https\nhost=github.com\n\n' | git credential fill | sed -n 's/^password=//p')
  export GH_TOKEN
fi
exec gh "$@"
