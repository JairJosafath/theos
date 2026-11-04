#!/usr/bin/env bash
# Tests for hack/seed/seed.sh's file mode (CREDENTIALS_DIR, what make seed-local
# uses): credentials go to a file, not a Secret, with no kubectl. A stand-in curl
# plays the Gitea API from a state directory; versions.txt lists no templates,
# so no git push happens. Run: hack/seed_test.sh
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
HACK="$(cd "$(dirname "$0")" && pwd)"
SEED=$HACK/seed/seed.sh
fail=0
check() { if [ "$2" = "$3" ]; then echo "ok   $1"; else echo "FAIL $1: got [$2] want [$3]"; fail=1; fi; }

# The stand-in Gitea: $T/gitea/orgs/NAME, $T/gitea/tokens/NAME holding the token,
# $T/gitea/calls one line per API call.
mkdir -p "$T/bin" "$T/gitea/orgs" "$T/gitea/tokens" "$T/templates"
: >"$T/templates/versions.txt"
cat >"$T/bin/curl" <<'CURL'
#!/usr/bin/env bash
G=$GITEA_STATE out=/dev/stdout fmt="" method=GET data="" auth="" fail=false url=""
while (($#)); do
  case $1 in
  -o) out=$2; shift ;;
  -w) fmt=$2; shift ;;
  -X) method=$2; shift ;;
  --data) data=$2; shift ;;
  -H)
    case $2 in
    @-) auth=$(cat) ;;
    @*) auth=$(cat "${2#@}") ;;
    Authorization:*) auth=$2 ;;
    esac
    shift ;;
  -*f*) [[ $1 != --* ]] && fail=true ;;
  http*) url=$1 ;;
  esac
  shift
done
path=${url#*/api}
code=200 body='{}'
authed=false
case $auth in
"Authorization: Basic $(printf 'admin:%s' "$GITEA_PASSWORD" | base64 | tr -d '\n')") authed=true ;;
"Authorization: token "*) grep -qxF -- "${auth#Authorization: token }" "$G"/tokens/* 2>/dev/null && authed=true ;;
esac
[[ $path == /healthz ]] || echo "$method $path" >>"$G/calls"
case "$method $path" in
"GET /healthz") ;;
"GET /v1/version") body='{"version":"test"}' ;;
"GET /v1/user") $authed || code=401 ;;
"GET /v1/orgs/"*) [[ -e $G/orgs/${path#/v1/orgs/} ]] || code=404 ;;
"POST /v1/orgs") jq -r .visibility <<<"$data" >"$G/orgs/$(jq -r .username <<<"$data")"; code=201 ;;
"DELETE /v1/users/admin/tokens/"*)
  f=$G/tokens/${path##*/}
  if [[ -e $f ]]; then rm "$f"; code=204; else code=404; fi ;;
"POST /v1/users/admin/tokens")
  tok=tok-$RANDOM$RANDOM
  echo "$tok" >"$G/tokens/$(jq -r .name <<<"$data")"
  body=$(jq -nc --arg t "$tok" '{sha1: $t}') code=201 ;;
# Gitea hands out the organization's token it has until it is reset.
"POST /v1/orgs/templates/actions/runners/registration-token")
  [[ -e $G/runner-token ]] || head -c 18 /dev/urandom | base64 >"$G/runner-token"
  body=$(jq -nc --arg t "$(cat "$G/runner-token")" '{token: $t}') ;;
*) code=500 ;;
esac
if [[ $path != /healthz && $path != /v1/version && $authed != true ]]; then code=401; fi
[[ $path == /healthz ]] && code=200
printf '%s' "$body" >"$out"
[[ -n $fmt ]] && printf '%s' "$code"
$fail && ((code >= 400)) && exit 22
exit 0
CURL
# kubectl logs its calls; get finds no Secret, label reads nothing, the rest
# pass their input on.
cat >"$T/bin/kubectl" <<'KUBECTL'
#!/usr/bin/env bash
echo "kubectl $*" >>"$GITEA_STATE/kubectl"
[[ " $* " == *" get "* ]] && exit 1
[[ " $* " == *" create "* ]] && { echo "kind: Secret"; exit 0; }
[[ " $* " == *" label "* ]] && exit 0
cat
KUBECTL
chmod +x "$T/bin/curl" "$T/bin/kubectl"

export PATH="$T/bin:$PATH" GITEA_STATE=$T/gitea
export GITEA_URL=http://gitea.test GITEA_USERNAME=admin GITEA_WAIT=5
GITEA_PASSWORD=$(head -c 18 /dev/urandom | base64 | tr -d '\n/+=')
export GITEA_PASSWORD
export TEMPLATES_DIR=$T/templates PROJECTS=webshop
CREDS=$T/creds
SCOPES=write:admin,write:issue,write:organization,write:package,write:repository,read:user
seed() { CREDENTIALS_DIR=$CREDS "$SEED" >"$T/out" 2>&1; }
token() { sed -n 's/^THEOS_GITEA_TOKEN=//p' "$CREDS/webshop.env"; }

# First run: the file holds a working token; no kubectl, no runner token.
seed; check "file mode: seed succeeds" $? 0
check "file mode: credentials file is mode 0600" "$(stat -c %a "$CREDS/webshop.env" 2>&1)" 600
check "file mode: file names the URL" "$(sed -n 's/^THEOS_GITEA_URL=//p' "$CREDS/webshop.env")" "$GITEA_URL"
check "file mode: file names the username" "$(sed -n 's/^THEOS_GITEA_USERNAME=//p' "$CREDS/webshop.env")" admin
check "file mode: file holds the token Gitea made" "$(token)" "$(cat "$T/gitea/tokens/theos-webshop")"
check "file mode: file records the scopes" "$(sed -n 's/^THEOS_GITEA_TOKEN_SCOPES=//p' "$CREDS/webshop.env")" "$SCOPES"
check "file mode: no kubectl" "$(test -e "$T/gitea/kubectl" && echo called)" ""
check "file mode: no runner token" "$(grep -c runners "$T/gitea/calls")" 0
check "file mode: templates and webshop organizations" "$(ls "$T/gitea/orgs" | tr '\n' ' ')" "templates webshop "
# Public, so a node's image pull and Argo CD's clone need no Secret (#1589).
check "file mode: both organizations are public" "$(cat "$T/gitea/orgs/templates" "$T/gitea/orgs/webshop" | tr '\n' ' ')" "public public "
grep -qF -- "$(token)" "$T/out"; check "file mode: the token is never printed" $? 1
grep -q "Stored the token in $CREDS/webshop.env" "$T/out"; check "file mode: says where it stored the token" $? 0
first=$(token)

# Second run: the working token stays.
seed; check "second run: succeeds" $? 0
check "second run: keeps the token" "$(token)" "$first"
grep -q "holds a working token; keeping it" "$T/out"; check "second run: says it keeps it" $? 0

# A token Gitea no longer accepts is replaced.
rm "$T/gitea/tokens/theos-webshop"
seed; check "revoked token: succeeds" $? 0
grep -q "holds a token Gitea rejects; replacing it" "$T/out"; check "revoked token: says it replaces it" $? 0
check "revoked token: file holds the new token" "$(token)" "$(cat "$T/gitea/tokens/theos-webshop")"
[ "$(token)" != "$first" ]; check "revoked token: the token changed" $? 0

# A token with other scopes is replaced, and the old one deleted.
second=$(token)
TOKEN_SCOPES=read:user CREDENTIALS_DIR=$CREDS "$SEED" >"$T/out" 2>&1; check "new scopes: succeeds" $? 0
seed; check "scopes back: succeeds" $? 0
grep -q "holds a token with scopes 'read:user'; replacing it" "$T/out"; check "other scopes: says it replaces it" $? 0
[ "$(token)" != "$second" ]; check "other scopes: the token changed" $? 0
check "other scopes: one token in Gitea" "$(ls "$T/gitea/tokens" | wc -l)" 1

# A file without a token is replaced.
printf 'THEOS_GITEA_URL=x\n' >"$CREDS/webshop.env"
seed; check "file without a token: succeeds" $? 0
grep -q "holds no token; replacing it" "$T/out"; check "file without a token: says it replaces it" $? 0
check "file without a token: file holds the token" "$(token)" "$(cat "$T/gitea/tokens/theos-webshop")"

# Without CREDENTIALS_DIR the seed stores Secrets as before, and the runner token.
"$SEED" >"$T/out" 2>&1; check "secret mode: succeeds" $? 0
grep -q "create secret generic gitea-credentials" "$T/gitea/kubectl"; check "secret mode: stores Secret gitea-credentials" $? 0
grep -q "create secret generic gitea-runner" "$T/gitea/kubectl"; check "secret mode: stores the runner token" $? 0
# The shared runners register to the templates organization, never to the
# instance (#1140): its token, annotated with its owner.
grep -q "orgs/templates/actions/runners/registration-token" "$T/gitea/calls"; check "secret mode: the templates organization's runner token" $? 0
grep -q "admin/actions/runners" "$T/gitea/calls"; check "secret mode: no instance runner token" $? 1
grep -q "theos.factory.io/token-owner=org/templates" "$T/gitea/kubectl"; check "secret mode: the runner token names its owner" $? 0
grep -q "projects" "$T/gitea/kubectl"; check "secret mode: no Project without PROJECT_OBJECTS" $? 1

# The chart's seed (PROJECT_OBJECTS=true) creates the project's Project when
# the namespace has none; get finds none here.
: >"$T/gitea/kubectl"
PROJECT_OBJECTS=true "$SEED" >"$T/out" 2>&1; check "project objects: succeeds" $? 0
grep -q "kubectl -n webshop get projects.theos.factory.io webshop" "$T/gitea/kubectl"; check "project objects: looks for the Project first" $? 0
grep -q "Created Project webshop/webshop" "$T/out"; check "project objects: creates Project webshop" $? 0
PROJECT_OBJECTS=true CREDENTIALS_DIR=$CREDS "$SEED" >"$T/out" 2>&1; check "project objects, file mode: succeeds" $? 0
grep -q "Project" "$T/out"; check "project objects, file mode: no Project" $? 1

# The chart's seed (SHARE_SECRETS=true) labels each project namespace to
# receive the shared Secrets; get finds no label here. Off, it never touches one.
grep -q "namespace" "$T/gitea/kubectl"; check "share secrets off: no namespace label" $? 1
: >"$T/gitea/kubectl"
SHARE_SECRETS=true "$SEED" >"$T/out" 2>&1; check "share secrets: succeeds" $? 0
grep -q "kubectl label namespace webshop theos.factory.io/shared-secrets=true --overwrite" "$T/gitea/kubectl"
check "share secrets: labels namespace webshop" $? 0
: >"$T/gitea/kubectl"
SHARE_SECRETS=true CREDENTIALS_DIR=$CREDS "$SEED" >"$T/out" 2>&1; check "share secrets, file mode: succeeds" $? 0
grep -q "namespace" "$T/gitea/kubectl"; check "share secrets, file mode: no kubectl" $? 1

exit $fail
