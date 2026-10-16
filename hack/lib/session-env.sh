# Sourced by hack/relay.sh and hack/relay-watch.sh (#645).
#
# drop_session_env: unset the variables a Claude Code or VS Code session gives
# the processes it starts, so a relay started from one runs as one started from
# a plain project shell or the systemd unit: its headless sessions are not
# children of that session, don't take its effort level, messaging socket or
# session ID (hack/lib/github.sh names claims by CLAUDE_CODE_SESSION_ID), and
# git doesn't ask VS Code's askpass, which stops working with the window.
# A human's cluster opt-in goes too (C4_HUMAN_CLUSTER, #1128): a relay started
# from a shell that exported it would let its agents lease a Kind cluster.
# Configuration a shell sets on purpose stays: CLAUDE_CONFIG_DIR,
# CLAUDE_CODE_OAUTH_TOKEN and the other CLAUDE_CODE_* settings.
drop_session_env() {
  local v
  unset CLAUDECODE CLAUDE_PID CLAUDE_EFFORT AI_AGENT ELECTRON_RUN_AS_NODE \
    CLAUDE_CODE_SESSION_ID CLAUDE_CODE_CHILD_SESSION CLAUDE_CODE_SESSION_ATTENDED \
    CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_EXECPATH CLAUDE_CODE_SSE_PORT \
    CLAUDE_CODE_MESSAGING_SOCKET CLAUDE_CODE_MESSAGING_TOKEN C4_HUMAN_CLUSTER
  case ${GIT_ASKPASS:-} in *vscode* | *code-server*) unset GIT_ASKPASS ;; esac
  [ "${TERM_PROGRAM:-}" != vscode ] || unset TERM_PROGRAM TERM_PROGRAM_VERSION
  for v in $(compgen -e); do
    case $v in VSCODE_*) unset "$v" ;; esac
  done
}
