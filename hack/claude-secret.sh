#!/usr/bin/env bash
# Stores the agents' Claude token as Secret claude-credentials in a namespace:
# the operator's, which shares it with every labeled project namespace, or a
# project's, for a token of its own. make claude-secret runs it.
#
#   KUBECTL="kubectl --context kind-c4factory" hack/claude-secret.sh NAMESPACE
#
# The token comes from CLAUDE_TOKEN in the environment when it is set, else from
# the file CLAUDE_TOKEN_FILE (make sets the device's .dev/secrets/claude-token),
# which must have mode 0600 or tighter. The token goes to kubectl on stdin, never
# in argv, and is never printed.
set -euo pipefail

ns=${1:?usage: hack/claude-secret.sh NAMESPACE}
read -ra kubectl <<<"${KUBECTL:-kubectl}"

if [ -n "${CLAUDE_TOKEN:-}" ]; then
  token=$CLAUDE_TOKEN
else
  file=${CLAUDE_TOKEN_FILE:?set CLAUDE_TOKEN or CLAUDE_TOKEN_FILE}
  if [ ! -f "$file" ]; then
    echo "No Claude token: $file is missing. Make it with: claude setup-token, then install -m 600 /dev/stdin $file (paste the token, then Ctrl-D)" >&2
    exit 1
  fi
  mode=$(stat -c %a "$file" 2>/dev/null || stat -f %Lp "$file")
  if (((8#$mode & 8#077) != 0)); then
    echo "Claude token file $file has mode $mode, looser than 0600; run: chmod 600 $file" >&2
    exit 1
  fi
  token=$(tr -d ' \t\r\n' <"$file")
  if [ -z "$token" ]; then
    echo "Claude token file $file is empty" >&2
    exit 1
  fi
fi

printf '%s' "$token" | "${kubectl[@]}" -n "$ns" create secret generic claude-credentials \
  --from-file=CLAUDE_CODE_OAUTH_TOKEN=/dev/stdin --dry-run=client -o yaml |
  "${kubectl[@]}" apply --server-side --field-manager=c4factory-testenv -f - >/dev/null
echo "Stored Secret $ns/claude-credentials"
