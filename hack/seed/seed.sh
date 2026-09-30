#!/usr/bin/env bash
# Seeds the test Gitea. `make seed` runs it as Job gitea/seed (see job.yaml):
#   1. waits for the Gitea API and checks the admin credentials;
#   2. creates the organizations for the templates and for the project;
#   3. pushes and tags each template in versions.txt that is not tagged yet;
#   4. stores an admin access token as Secret gitea-credentials in the project namespace.
# Every step looks before it acts, so a second run changes nothing.
set -euo pipefail

GITEA_URL=${GITEA_URL:-http://gitea.gitea.svc.cluster.local:3000}
# The admin credentials come from Secret gitea-admin (see job.yaml).
for var in GITEA_USERNAME GITEA_PASSWORD; do
  [ -n "${!var:-}" ] || { echo "set $var (from Secret gitea-admin)" >&2; exit 1; }
done
TEMPLATES_DIR=${TEMPLATES_DIR:-/templates}
TEMPLATE_ORG=${TEMPLATE_ORG:-templates}
# The project: its organization holds the Container repositories, and its
# namespace (same name) gets the credentials Secret.
PROJECT=${PROJECT:-webshop}
CREDENTIALS_SECRET=${CREDENTIALS_SECRET:-gitea-credentials}
TOKEN_NAME=${TOKEN_NAME:-c4factory-$PROJECT}
# Enough to create repositories in an organization and push to them over HTTP;
# read:user lets the operator (and this script) check the token with GET /user.
TOKEN_SCOPES=${TOKEN_SCOPES:-write:organization,write:repository,read:user}

export GIT_AUTHOR_NAME="C4 Factory seed" GIT_AUTHOR_EMAIL="seed@c4.factory.io"
export GIT_COMMITTER_NAME="$GIT_AUTHOR_NAME" GIT_COMMITTER_EMAIL="$GIT_AUTHOR_EMAIL"
export GIT_TERMINAL_PROMPT=0

log() { printf '%s seed: %s\n' "$(date -u +%H:%M:%S)" "$*"; }
die() { log "ERROR: $*" >&2; exit 1; }

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

# Admin basic auth, kept off the command line: curl reads the header from a
# file and git from environment config.
basic=$(printf '%s:%s' "$GITEA_USERNAME" "$GITEA_PASSWORD" | base64 | tr -d '\n')
(umask 077 && printf 'Authorization: Basic %s\n' "$basic" > "$WORK/auth")
export GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=http.extraHeader
export GIT_CONFIG_VALUE_0="Authorization: Basic $basic"

# api METHOD PATH [JSON]: calls the Gitea API as the admin; sets STATUS and BODY.
api() {
  local method=$1 path=$2 data=${3:-}
  local args=(-sS -o "$WORK/body" -w '%{http_code}' -X "$method"
    -H @"$WORK/auth" -H 'Accept: application/json')
  if [[ -n $data ]]; then
    args+=(-H 'Content-Type: application/json' --data "$data")
  fi
  STATUS=$(curl "${args[@]}" "$GITEA_URL/api/v1$path")
  BODY=$(cat "$WORK/body")
}

# expect WHAT CODE...: stops unless the last call returned one of the codes.
expect() {
  local what=$1 code
  shift
  for code; do [[ $STATUS == "$code" ]] && return 0; done
  die "$what: HTTP $STATUS: $BODY"
}

wait_for_gitea() {
  log "Waiting for Gitea at $GITEA_URL"
  local _
  for _ in $(seq 1 60); do
    if curl -fs -o /dev/null "$GITEA_URL/api/healthz"; then
      api GET /user
      expect "Signing in as $GITEA_USERNAME" 200
      log "Gitea $(curl -fsS "$GITEA_URL/api/v1/version" | jq -r .version) is up; signed in as $GITEA_USERNAME"
      return
    fi
    sleep 2
  done
  die "Gitea did not become healthy within 120s"
}

# ensure_org NAME DESCRIPTION
ensure_org() {
  local org=$1 description=$2
  api GET "/orgs/$org"
  if [[ $STATUS == 200 ]]; then
    log "Organization $org exists"
    return
  fi
  expect "Looking up organization $org" 404
  api POST /orgs "$(jq -nc --arg u "$org" --arg d "$description" \
    '{username: $u, description: $d, visibility: "public"}')"
  expect "Creating organization $org" 201
  log "Created organization $org"
}

# check_tag NAME VERSION: warns when templates/NAME no longer matches its
# published tag, since the seed never moves a tag.
check_tag() {
  local name=$1 version=$2 repo="$TEMPLATE_ORG/$1" tmp
  tmp=$(mktemp -d -p "$WORK")
  git init -q "$tmp"
  cp -R "$TEMPLATES_DIR/$name/." "$tmp/"
  git -C "$tmp" add --all --force
  git -C "$tmp" fetch -q --depth 1 "$GITEA_URL/$repo.git" "refs/tags/$version"
  if git -C "$tmp" diff --cached --quiet 'FETCH_HEAD^{commit}'; then
    log "Template $repo $version is already tagged and matches templates/$name"
  else
    log "WARNING: templates/$name differs from tag $version of $repo; bump its version in templates/versions.txt to publish the change:"
    git -C "$tmp" diff --cached --stat 'FETCH_HEAD^{commit}' | sed 's/^/    /'
  fi
}

# seed_template NAME VERSION: a public repo templates/NAME whose main holds the
# folder's files, tagged VERSION. Tags never move: a changed template needs a
# new version in versions.txt.
seed_template() {
  local name=$1 version=$2
  local dir="$TEMPLATES_DIR/$name" repo="$TEMPLATE_ORG/$name"
  [[ -d $dir ]] || die "versions.txt lists $name, but $dir does not exist"

  api GET "/repos/$repo"
  if [[ $STATUS == 404 ]]; then
    api POST "/orgs/$TEMPLATE_ORG/repos" "$(jq -nc --arg n "$name" \
      '{name: $n, description: "C4 Factory template \($n)", private: false, default_branch: "main"}')"
    expect "Creating repository $repo" 201
    log "Created repository $repo"
  else
    expect "Looking up repository $repo" 200
  fi
  local empty
  empty=$(jq -r .empty <<<"$BODY")

  api GET "/repos/$repo/tags/$version"
  if [[ $STATUS == 200 ]]; then
    check_tag "$name" "$version"
    return
  fi
  expect "Looking up tag $version of $repo" 404

  local src="$WORK/$name"
  if [[ $empty == true ]]; then
    git init -q -b main "$src"
    git -C "$src" remote add origin "$GITEA_URL/$repo.git"
  else
    # A new version on top of the previous one: replace all files, keep history.
    git clone -q --branch main "$GITEA_URL/$repo.git" "$src"
    find "$src" -mindepth 1 -maxdepth 1 ! -name .git -exec rm -rf {} +
  fi
  cp -R "$dir/." "$src/"
  # --force: a template's own .gitignore must not drop any of its files.
  git -C "$src" add --all --force
  if [[ $empty != true ]] && git -C "$src" diff --cached --quiet; then
    log "Template $repo has no file changes for $version; tagging the current main"
  else
    git -C "$src" commit -q -m "$name $version"
  fi
  git -C "$src" tag -a "$version" -m "$name $version"
  git -C "$src" push -q origin main "refs/tags/$version"
  log "Pushed template $repo $version (commit $(git -C "$src" rev-parse --short HEAD))"
}

# ensure_credentials: Secret CREDENTIALS_SECRET in namespace PROJECT with keys
# username and token. A token that still works is kept.
ensure_credentials() {
  local ns=$PROJECT secret=$CREDENTIALS_SECRET token=""
  if token=$(kubectl -n "$ns" get secret "$secret" -o jsonpath='{.data.token}' 2>/dev/null | base64 -d) &&
    [[ -n $token ]]; then
    if [[ $(printf 'Authorization: token %s\n' "$token" |
      curl -s -o /dev/null -w '%{http_code}' -H @- "$GITEA_URL/api/v1/user") == 200 ]]; then
      log "Secret $ns/$secret holds a working token; keeping it"
      return
    fi
    log "Secret $ns/$secret holds a token Gitea rejects; replacing it"
  fi

  # Token names are unique per user, so drop our previous token first.
  api DELETE "/users/$GITEA_USERNAME/tokens/$TOKEN_NAME"
  expect "Deleting old token $TOKEN_NAME" 204 404
  if [[ $STATUS == 204 ]]; then log "Deleted old token $TOKEN_NAME"; fi
  api POST "/users/$GITEA_USERNAME/tokens" "$(jq -nc --arg n "$TOKEN_NAME" --arg s "$TOKEN_SCOPES" \
    '{name: $n, scopes: ($s | split(","))}')"
  expect "Creating token $TOKEN_NAME" 201
  log "Created token $TOKEN_NAME with scopes $TOKEN_SCOPES"

  # Secret values come from files, so the token never shows up in argv.
  local creds="$WORK/credentials"
  (umask 077 && mkdir "$creds" &&
    printf '%s' "$GITEA_USERNAME" > "$creds/username" &&
    jq -j .sha1 <<<"$BODY" > "$creds/token")
  # Server-side apply: no last-applied annotation carrying the token around.
  kubectl -n "$ns" create secret generic "$secret" --from-file="$creds" \
    --dry-run=client -o yaml |
    kubectl apply --server-side --field-manager=c4factory-seed --force-conflicts -f - >/dev/null
  log "Stored the token as Secret $ns/$secret (keys username, token)"
}

main() {
  wait_for_gitea
  ensure_org "$TEMPLATE_ORG" "Template repositories for the C4 Factory catalog"
  ensure_org "$PROJECT" "Container repositories of project $PROJECT"

  local name version _
  while read -r name version _ <&3; do
    [[ -z $name || $name == \#* ]] && continue
    [[ -n $version ]] || die "versions.txt: no version for $name"
    seed_template "$name" "$version"
  done 3<"$TEMPLATES_DIR/versions.txt"

  ensure_credentials
  log "Done: organizations $TEMPLATE_ORG and $PROJECT, templates tagged, Secret $PROJECT/$CREDENTIALS_SECRET"
}

main "$@"
