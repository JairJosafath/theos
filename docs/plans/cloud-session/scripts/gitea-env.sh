# Sourced: the Gitea URL and the admin's basic auth, read from Secret
# gitea/gitea-admin into a 0600 header file; nothing is printed.
W=${THEOS_SESSION_DIR:?set THEOS_SESSION_DIR to the folder this session keeps its factory in}
GITEA_URL=http://gitea.gitea.svc.cluster.local:3000
ADMIN_HEADER=$W/secrets/admin-auth.header
if [ ! -s "$ADMIN_HEADER" ]; then
  ( umask 077
    u=$(kubectl --context kind-theos -n gitea get secret gitea-admin -o jsonpath='{.data.username}' | base64 -d)
    p=$(kubectl --context kind-theos -n gitea get secret gitea-admin -o jsonpath='{.data.password}' | base64 -d)
    printf 'Authorization: Basic %s\n' "$(printf '%s:%s' "$u" "$p" | base64 | tr -d '\n')" > "$ADMIN_HEADER" )
fi
( umask 077; : > "$W/secrets/.body" )
# api METHOD PATH [JSON]: as the admin; prints the status, body in $W/secrets/.body
api() {
  local method=$1 path=$2 data=${3:-}
  local args=(-sS --noproxy '*' -o "$W/secrets/.body" -w '%{http_code}' -X "$method" -H @"$ADMIN_HEADER" -H 'Accept: application/json')
  [ -n "$data" ] && args+=(-H 'Content-Type: application/json' --data "$data")
  STATUS=$(curl "${args[@]}" "$GITEA_URL/api/v1$path")
  BODY=$(cat "$W/secrets/.body"); : > "$W/secrets/.body"
}
# admin_git ARGS...: git with the admin's header, kept off the command line.
admin_git() {
  GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=http.extraHeader GIT_CONFIG_VALUE_0="$(cat "$ADMIN_HEADER")" GIT_TERMINAL_PROMPT=0 git "$@"
}
