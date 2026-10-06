#!/usr/bin/env bash
# A Gitea of this checkout's own, for tests on any device: no Kubernetes, no
# Docker, no lease. `make gitea-local` downloads the binary and runs `start`.
#
#   hack/gitea-local.sh start      start Gitea from .gitea-local/ (a no-op while it runs)
#   hack/gitea-local.sh stop       stop it; its data stays for the next start
#   hack/gitea-local.sh fixtures   what the gitprovider integration tests read: the
#                                  templates organization with its pinned tags (the
#                                  seed, without Secrets) and the webshop organization
#   hack/gitea-local.sh seed       what `make seed` does in Kind, without kubectl: the
#                                  templates organization with its pinned tags, and
#                                  the webshop organization with its token in
#                                  .gitea-local/webshop.env (hack/seed/seed.sh, file mode)
#   hack/gitea-local.sh port       the port this checkout's Gitea listens on
#
# It runs the same Gitea version as config/gitea, with SQLite under .gitea-local/
# (gitignored), on 127.0.0.1 only, with Actions, mail, SSH and LFS off. The first
# start picks a free port near one derived from the checkout's path, so worktrees
# don't collide, and keeps it in app.ini.
#
# Credentials: the first start creates admin GITEA_ADMIN with a random password
# and an access token, and writes them to .gitea-local/credentials.env (mode
# 0600) as C4_GITEA_URL, C4_GITEA_USERNAME, C4_GITEA_PASSWORD and C4_GITEA_TOKEN.
# They are never printed; source the file (`set -a; . .gitea-local/credentials.env`).
# rm -rf .gitea-local (after stop) starts over with new ones.
set -euo pipefail

GITEA=${GITEA:-gitea}
TOP=$(git rev-parse --show-toplevel 2>/dev/null || pwd)
DIR=$TOP/.gitea-local
GITEA_ADMIN=${GITEA_ADMIN:-c4admin}
GITEA_ADMIN_EMAIL=${GITEA_ADMIN_EMAIL:-c4admin@c4.factory.io}
# The scopes the seed gives the operator's token (hack/seed/seed.sh).
TOKEN_SCOPES=${TOKEN_SCOPES:-write:admin,write:issue,write:organization,write:repository,read:user}
# Seconds to wait for Gitea to answer after starting it.
GITEA_WAIT=${GITEA_WAIT:-60}
CONFIG=$DIR/app.ini
CREDENTIALS=$DIR/credentials.env
PIDFILE=$DIR/gitea.pid

log() { printf 'gitea-local: %s\n' "$*"; }
die() { log "ERROR: $*" >&2; exit 1; }

# in_use PORT: whether something listens on 127.0.0.1:PORT.
in_use() { (exec 3<>"/dev/tcp/127.0.0.1/$1") 2>/dev/null; }

# pick_port: a free port in 13000-13999, starting from a hash of the checkout's
# path, so each checkout usually lands on its own and keeps it.
pick_port() {
  local start port i
  start=$((13000 + $(printf '%s' "$TOP" | cksum | cut -d' ' -f1) % 1000))
  for ((i = 0; i < 1000; i++)); do
    port=$((13000 + (start - 13000 + i) % 1000))
    in_use "$port" || { echo "$port"; return; }
  done
  die "no free port in 13000-13999"
}

port() {
  [[ -f $CONFIG ]] || die "$CONFIG does not exist; run make gitea-local"
  sed -n 's/^HTTP_PORT *= *//p' "$CONFIG"
}

url() { echo "http://127.0.0.1:$(port)"; }

# running: whether the pid in PIDFILE is this checkout's Gitea. A pid left over
# from a crash or a reboot may belong to another process by now, so the process
# must also name this checkout's app.ini.
running() {
  local pid
  [[ -f $PIDFILE ]] && pid=$(cat "$PIDFILE") && [[ -n $pid ]] &&
    tr '\0' '\n' <"/proc/$pid/cmdline" 2>/dev/null | grep -qxF -- "$CONFIG"
}

healthy() { curl -fs -o /dev/null "$(url)/api/healthz"; }

g() { "$GITEA" --config "$CONFIG" --work-path "$DIR" "$@"; }

# write_config: app.ini for a fresh instance, with its own secrets.
write_config() {
  local port secret_key internal_token
  port=$(pick_port)
  secret_key=$("$GITEA" generate secret SECRET_KEY)
  internal_token=$("$GITEA" generate secret INTERNAL_TOKEN)
  (umask 077 && cat >"$CONFIG" <<EOF
APP_NAME = C4 Factory local Gitea
RUN_MODE = prod
WORK_PATH = $DIR

[server]
PROTOCOL = http
HTTP_ADDR = 127.0.0.1
HTTP_PORT = $port
DOMAIN = 127.0.0.1
ROOT_URL = http://127.0.0.1:$port/
DISABLE_SSH = true
START_SSH_SERVER = false
OFFLINE_MODE = true
LFS_START_SERVER = false
APP_DATA_PATH = $DIR/data

[database]
DB_TYPE = sqlite3
PATH = $DIR/data/gitea.db
SQLITE_JOURNAL_MODE = WAL

[repository]
ROOT = $DIR/data/repositories
DEFAULT_BRANCH = main

[security]
INSTALL_LOCK = true
SECRET_KEY = $secret_key
INTERNAL_TOKEN = $internal_token

[service]
DISABLE_REGISTRATION = true
REQUIRE_SIGNIN_VIEW = false

[oauth2]
ENABLED = false

[indexer]
ISSUE_INDEXER_TYPE = db

[actions]
ENABLED = false

[mailer]
ENABLED = false

[log]
MODE = file
LEVEL = Warn
ROOT_PATH = $DIR/log
EOF
  )
}

# create_admin: the admin user, a token and a new password, written to
# CREDENTIALS. Values go to files and stdin, never to argv or the terminal. It
# runs again after a failed start: an existing admin gets a new token (names
# are unique per user, hence the timestamp) and a new password.
create_admin() {
  local password token status
  if ! g admin user list --admin | awk 'NR > 1 {print $2}' | grep -qxF -- "$GITEA_ADMIN"; then
    # The CLI's random password stays unknown; the API sets the one we keep.
    g admin user create --admin --username "$GITEA_ADMIN" --email "$GITEA_ADMIN_EMAIL" \
      --random-password --must-change-password=false >/dev/null
  fi
  token=$(g admin user generate-access-token --username "$GITEA_ADMIN" \
    --token-name "c4factory-local-$(date +%s)" --scopes "$TOKEN_SCOPES" --raw)
  [[ -n $token ]] || die "Gitea returned no access token"
  password=$(head -c 24 /dev/urandom | base64 | tr -d '\n/+=')
  status=$({ printf 'Authorization: token %s\n' "$token"; } |
    curl -sS -o /dev/null -w '%{http_code}' -X PATCH -H @- -H 'Content-Type: application/json' \
      --data @<(jq -nc --arg u "$GITEA_ADMIN" --arg p "$password" \
        '{login_name: $u, source_id: 0, password: $p, must_change_password: false}') \
      "$(url)/api/v1/admin/users/$GITEA_ADMIN")
  [[ $status == 200 ]] || die "setting the password of $GITEA_ADMIN: HTTP $status"
  (umask 077 && cat >"$CREDENTIALS.tmp" <<EOF
C4_GITEA_URL=$(url)
C4_GITEA_USERNAME=$GITEA_ADMIN
C4_GITEA_PASSWORD=$password
C4_GITEA_TOKEN=$token
EOF
  )
  mv "$CREDENTIALS.tmp" "$CREDENTIALS"
  log "Created admin $GITEA_ADMIN and a token; credentials are in ${CREDENTIALS#"$TOP"/}"
}

start() {
  command -v "$GITEA" >/dev/null || die "no Gitea binary at $GITEA; run make gitea-local"
  mkdir -p "$DIR/log" "$DIR/data"
  if [[ -f $CONFIG ]] && running && healthy; then
    log "Gitea already runs at $(url) (pid $(cat "$PIDFILE"))"
    return
  fi
  if [[ ! -f $CONFIG ]]; then
    write_config
    log "Wrote ${CONFIG#"$TOP"/} (port $(port))"
  fi
  if in_use "$(port)" && ! running; then
    die "port $(port) is taken by another process; stop it, or rm -rf ${DIR#"$TOP"/} to pick a new port"
  fi
  local pid
  if running; then
    pid=$(cat "$PIDFILE")
  else
    g migrate >"$DIR/log/migrate.log" 2>&1 || { tail -n 20 "$DIR/log/migrate.log" >&2; die "gitea migrate failed"; }
    # setsid: Gitea outlives the make that started it, and Ctrl-C in that
    # terminal doesn't reach it.
    setsid "$GITEA" web --config "$CONFIG" --work-path "$DIR" \
      >>"$DIR/log/gitea.out" 2>&1 </dev/null &
    pid=$!
    echo "$pid" >"$PIDFILE"
  fi
  # kill -0, not running: until the forked shell execs setsid, the new pid's
  # cmdline is still this script's, and running would call a booting Gitea
  # exited (#311).
  local deadline=$((SECONDS + GITEA_WAIT))
  until healthy; do
    kill -0 "$pid" 2>/dev/null || {
      tail -n 20 "$DIR/log/gitea.out" "$DIR/log/gitea.log" >&2 2>/dev/null || true
      die "Gitea exited; its log is ${DIR#"$TOP"/}/log/"
    }
    ((SECONDS < deadline)) || die "Gitea did not answer at $(url) within ${GITEA_WAIT}s"
    sleep 1
  done
  [[ -f $CREDENTIALS ]] || create_admin
  log "Gitea $(curl -fsS "$(url)/api/v1/version" | jq -r .version) runs at $(url) (pid $(cat "$PIDFILE"))"
}

stop() {
  if ! running; then
    rm -f "$PIDFILE"
    log "Gitea is not running"
    return
  fi
  local pid
  pid=$(cat "$PIDFILE")
  kill "$pid"
  for _ in $(seq 1 20); do
    kill -0 "$pid" 2>/dev/null || { rm -f "$PIDFILE"; log "Stopped Gitea (pid $pid)"; return; }
    sleep 0.5
  done
  kill -9 "$pid" 2>/dev/null || true
  rm -f "$PIDFILE"
  log "Killed Gitea (pid $pid)"
}

# fixtures: the seed's organizations and template tags (hack/seed/seed.sh,
# without its Secrets), and the webshop organization the tests write to.
fixtures() {
  running && healthy || die "Gitea is not running; run make gitea-local"
  # shellcheck disable=SC1090
  set -a && . "$CREDENTIALS" && set +a
  GITEA_URL=$C4_GITEA_URL GITEA_USERNAME=$C4_GITEA_USERNAME GITEA_PASSWORD=$C4_GITEA_PASSWORD \
    TEMPLATES_DIR=$TOP/templates PROJECTS="" RUNNER_SECRET="" "$TOP/hack/seed/seed.sh"
  local status
  status=$(printf 'Authorization: token %s\n' "$C4_GITEA_TOKEN" |
    curl -sS -o /dev/null -w '%{http_code}' -H @- "$C4_GITEA_URL/api/v1/orgs/webshop")
  if [[ $status == 404 ]]; then
    status=$(printf 'Authorization: token %s\n' "$C4_GITEA_TOKEN" |
      curl -sS -o /dev/null -w '%{http_code}' -H @- -H 'Content-Type: application/json' \
        --data '{"username":"webshop","visibility":"public"}' "$C4_GITEA_URL/api/v1/orgs")
    [[ $status == 201 ]] || die "creating organization webshop: HTTP $status"
    log "Created organization webshop"
  else
    [[ $status == 200 ]] || die "looking up organization webshop: HTTP $status"
  fi
}

# seed: hack/seed/seed.sh against this Gitea, its credentials files in DIR.
seed() {
  running && healthy || die "Gitea is not running; run make gitea-local"
  # shellcheck disable=SC1090
  set -a && . "$CREDENTIALS" && set +a
  GITEA_URL=$C4_GITEA_URL GITEA_USERNAME=$C4_GITEA_USERNAME GITEA_PASSWORD=$C4_GITEA_PASSWORD \
    TEMPLATES_DIR=$TOP/templates PROJECTS=${PROJECTS:-webshop} CREDENTIALS_DIR=$DIR \
    "$TOP/hack/seed/seed.sh"
}

case ${1:-} in
start | stop | fixtures | seed | port) "$1" ;;
*) die "usage: $0 start|stop|fixtures|seed|port" ;;
esac
