#!/bin/bash
# claude-git.sh ARGS...: git as Gitea user claude; the token stays in its 0600 file.
W=${THEOS_SESSION_DIR:?set THEOS_SESSION_DIR to the folder this session keeps its factory in}
GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=http.extraHeader GIT_CONFIG_VALUE_0="$(cat $W/secrets/claude-token.header)" GIT_TERMINAL_PROMPT=0 exec git "$@"
