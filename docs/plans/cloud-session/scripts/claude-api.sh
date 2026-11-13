#!/bin/bash
# claude-api.sh METHOD PATH [JSON]: the Gitea API as user claude; prints the body.
W=${THEOS_SESSION_DIR:?set THEOS_SESSION_DIR to the folder this session keeps its factory in}
args=(-sS --noproxy '*' -X "$1" -H @"$W/secrets/claude-token.header" -H 'Accept: application/json')
[ -n "${3:-}" ] && args+=(-H 'Content-Type: application/json' --data "$3")
exec curl "${args[@]}" "http://gitea.gitea.svc.cluster.local:3000/api/v1$2"
