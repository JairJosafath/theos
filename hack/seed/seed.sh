#!/usr/bin/env bash
# Seeds the test Gitea. `make seed` runs it as Job gitea/seed (see job.yaml):
#   1. waits for the Gitea API and checks the admin credentials;
#   2. creates the organizations for the templates and for each project;
#   3. pushes and tags each template in versions.txt that is not tagged yet;
#   4. stores an admin access token as Secret gitea-credentials in each project's
#      namespace;
#   5. stores a runner registration token as Secret gitea/gitea-runner, unless
#      RUNNER_SECRET is empty (the Helm chart makes that token itself).
# With CREDENTIALS_DIR set (`make seed-local`), step 4 writes each project's
# token to CREDENTIALS_DIR/PROJECT.env instead, step 5 is skipped, and the
# script needs no kubectl.
# Every step looks before it acts, so a second run changes nothing.
set -euo pipefail

GITEA_URL=${GITEA_URL:-http://gitea.gitea.svc.cluster.local:3000}
# Seconds to wait for Gitea to answer: on a fresh Helm install its image may
# still be pulling when the seed starts.
GITEA_WAIT=${GITEA_WAIT:-120}
# The admin credentials come from Secret gitea-admin (see job.yaml).
for var in GITEA_USERNAME GITEA_PASSWORD; do
  [ -n "${!var:-}" ] || { echo "set $var (from Secret gitea-admin)" >&2; exit 1; }
done
TEMPLATES_DIR=${TEMPLATES_DIR:-/templates}
TEMPLATE_ORG=${TEMPLATE_ORG:-templates}
# The projects, space-separated: each one's organization holds its Container
# repositories, and its namespace (same name) gets the credentials Secret.
# PROJECTS="" seeds only the templates, with no kubectl (hack/gitea-local.sh).
PROJECTS=${PROJECTS-${PROJECT:-webshop}}
CREDENTIALS_SECRET=${CREDENTIALS_SECRET:-gitea-credentials}
# Enough to create repositories in an organization and push to them over HTTP;
# read:user lets the operator (and this script) check the token with GET /user;
# write:admin lets the operator make each Container's agent bot user; write:issue
# lets it label, comment on and close issues, and comment on pull requests.
TOKEN_SCOPES=${TOKEN_SCOPES:-write:admin,write:issue,write:organization,write:repository,read:user}
# File mode: each project's credentials go to CREDENTIALS_DIR/PROJECT.env (mode
# 0600) as C4_GITEA_URL, C4_GITEA_USERNAME, C4_GITEA_TOKEN and
# C4_GITEA_TOKEN_SCOPES, not to a Secret; there is no runner to register.
CREDENTIALS_DIR=${CREDENTIALS_DIR:-}
RUNNER_SECRET=${RUNNER_SECRET-gitea-runner}
if [[ -n $CREDENTIALS_DIR ]]; then RUNNER_SECRET=""; fi
RUNNER_NAMESPACE=${RUNNER_NAMESPACE:-gitea}

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
  local deadline=$((SECONDS + GITEA_WAIT))
  while ((SECONDS < deadline)); do
    if curl -fs -o /dev/null "$GITEA_URL/api/healthz"; then
      api GET /user
      expect "Signing in as $GITEA_USERNAME" 200
      log "Gitea $(curl -fsS "$GITEA_URL/api/v1/version" | jq -r .version) is up; signed in as $GITEA_USERNAME"
      return
    fi
    sleep 2
  done
  die "Gitea did not become healthy within ${GITEA_WAIT}s"
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
  # A template's workflows are for the Containers scaffolded from it, not for the
  # template repository itself.
  if [[ $(jq -r .has_actions <<<"$BODY") != false ]]; then
    api PATCH "/repos/$repo" '{"has_actions": false}'
    expect "Turning off Actions in $repo" 200
    log "Turned off Actions in $repo"
  fi

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

# token_works TOKEN: whether Gitea accepts TOKEN.
token_works() {
  [[ $(printf 'Authorization: token %s\n' "$1" |
    curl -s -o /dev/null -w '%{http_code}' -H @- "$GITEA_URL/api/v1/user") == 200 ]]
}

# new_token PROJECT FILE: a new admin token c4factory-PROJECT with TOKEN_SCOPES,
# written to FILE (mode 0600), replacing the previous one.
new_token() {
  local token_name="c4factory-$1" file=$2
  # Token names are unique per user, so drop our previous token first.
  api DELETE "/users/$GITEA_USERNAME/tokens/$token_name"
  expect "Deleting old token $token_name" 204 404
  if [[ $STATUS == 204 ]]; then log "Deleted old token $token_name"; fi
  api POST "/users/$GITEA_USERNAME/tokens" "$(jq -nc --arg n "$token_name" --arg s "$TOKEN_SCOPES" \
    '{name: $n, scopes: ($s | split(","))}')"
  expect "Creating token $token_name" 201
  log "Created token $token_name with scopes $TOKEN_SCOPES"
  (umask 077 && jq -j .sha1 <<<"$BODY" > "$file")
}

# ensure_credentials_file PROJECT: CREDENTIALS_DIR/PROJECT.env with the URL,
# username and token, for scripts and tests to source. A token that still works
# and has the scopes asked for (recorded in the file) is kept.
ensure_credentials_file() {
  local project=$1 file="$CREDENTIALS_DIR/$1.env" token="" scopes=""
  if [[ -f $file ]]; then
    token=$(sed -n 's/^C4_GITEA_TOKEN=//p' "$file")
    scopes=$(sed -n 's/^C4_GITEA_TOKEN_SCOPES=//p' "$file")
    if [[ -z $token ]]; then
      log "$file holds no token; replacing it"
    elif [[ $scopes != "$TOKEN_SCOPES" ]]; then
      log "$file holds a token with scopes '${scopes:-unknown}'; replacing it"
    elif token_works "$token"; then
      log "$file holds a working token; keeping it"
      return
    else
      log "$file holds a token Gitea rejects; replacing it"
    fi
  fi
  new_token "$project" "$WORK/token-$project"
  mkdir -p "$CREDENTIALS_DIR"
  # Written next to the file and moved, so a reader never sees half of it.
  (umask 077 && {
    printf 'C4_GITEA_URL=%s\n' "$GITEA_URL"
    printf 'C4_GITEA_USERNAME=%s\n' "$GITEA_USERNAME"
    printf 'C4_GITEA_TOKEN=%s\n' "$(cat "$WORK/token-$project")"
    printf 'C4_GITEA_TOKEN_SCOPES=%s\n' "$TOKEN_SCOPES"
  } > "$file.tmp")
  mv "$file.tmp" "$file"
  log "Stored the token in $file"
}

# ensure_credentials PROJECT: Secret CREDENTIALS_SECRET in namespace PROJECT with
# keys username and token. A token that still works and has the scopes asked for
# (recorded on the Secret) is kept.
ensure_credentials() {
  local ns=$1 secret=$CREDENTIALS_SECRET token="" scopes=""
  if token=$(kubectl -n "$ns" get secret "$secret" -o jsonpath='{.data.token}' 2>/dev/null | base64 -d) &&
    [[ -n $token ]]; then
    scopes=$(kubectl -n "$ns" get secret "$secret" -o jsonpath='{.metadata.annotations.c4\.factory\.io/token-scopes}')
    if [[ $scopes != "$TOKEN_SCOPES" ]]; then
      log "Secret $ns/$secret holds a token with scopes '${scopes:-unknown}'; replacing it"
    elif token_works "$token"; then
      log "Secret $ns/$secret holds a working token; keeping it"
      return
    else
      log "Secret $ns/$secret holds a token Gitea rejects; replacing it"
    fi
  fi

  # Secret values come from files, so the token never shows up in argv.
  local creds="$WORK/credentials-$ns"
  (umask 077 && mkdir "$creds" && printf '%s' "$GITEA_USERNAME" > "$creds/username")
  new_token "$ns" "$creds/token"
  # Server-side apply: no last-applied annotation carrying the token around.
  kubectl -n "$ns" create secret generic "$secret" --from-file="$creds" \
    --dry-run=client -o yaml |
    kubectl annotate --local -f - "c4.factory.io/token-scopes=$TOKEN_SCOPES" -o yaml |
    kubectl apply --server-side --field-manager=c4factory-seed --force-conflicts -f - >/dev/null
  log "Stored the token as Secret $ns/$secret (keys username, token)"
}

# ensure_runner_token: Secret RUNNER_SECRET in RUNNER_NAMESPACE with a registration
# token for the CI runner (config/runner).
ensure_runner_token() {
  api POST /admin/actions/runners/registration-token
  expect "Getting a runner registration token" 200 201
  local file="$WORK/runner-token"
  (umask 077 && jq -j .token <<<"$BODY" > "$file")
  kubectl -n "$RUNNER_NAMESPACE" create secret generic "$RUNNER_SECRET" --from-file=token="$file" \
    --dry-run=client -o yaml |
    kubectl apply --server-side --field-manager=c4factory-seed --force-conflicts -f - >/dev/null
  log "Stored a runner registration token as Secret $RUNNER_NAMESPACE/$RUNNER_SECRET"
}

main() {
  wait_for_gitea
  ensure_org "$TEMPLATE_ORG" "Template repositories for the C4 Factory catalog"
  local project
  for project in $PROJECTS; do
    ensure_org "$project" "Container repositories of project $project"
  done

  local name version _
  while read -r name version _ <&3; do
    [[ -z $name || $name == \#* ]] && continue
    [[ -n $version ]] || die "versions.txt: no version for $name"
    seed_template "$name" "$version"
  done 3<"$TEMPLATES_DIR/versions.txt"

  for project in $PROJECTS; do
    if [[ -n $CREDENTIALS_DIR ]]; then
      ensure_credentials_file "$project"
    else
      ensure_credentials "$project"
    fi
  done
  if [[ -n $RUNNER_SECRET ]]; then ensure_runner_token; fi
  if [[ -n $PROJECTS ]]; then
    log "Done: organizations $TEMPLATE_ORG and $PROJECTS, templates tagged, credentials${RUNNER_SECRET:+ and runner token} stored"
  else
    log "Done: organization $TEMPLATE_ORG, templates tagged${RUNNER_SECRET:+, runner token stored}"
  fi
}

main "$@"
