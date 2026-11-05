#!/usr/bin/env bash
# Tests for the chart's Matrix accounts Job (dist/chart/files/matrix-accounts.sh):
# the registration HMAC against Tuwunel's reference vector; the bot made once,
# its password kept and its token put in each namespace that lacks it; a
# person's account and Secret; nothing made twice; a homeserver that doesn't
# answer; and no password or token in kubectl's or curl's argv. kubectl and
# the homeserver (curl) are faked.
# Run: hack/matrix-accounts_test.sh
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SCRIPT=$ROOT/dist/chart/files/matrix-accounts.sh
fail=0
check() { if [ "$2" = "$3" ]; then echo "ok   $1"; else echo "FAIL $1: got [$2] want [$3]"; fail=1; fi; }

# Tuwunel's own test vector (src/api/client/admin/register.rs).
got=$(MATRIX_ACCOUNTS_SOURCED=1 SHARED_SECRET=shared-secret-12345 SCRIPT=$SCRIPT \
	bash -c '. "$SCRIPT"; mac thenonce0123456789abcdef alice "p4ssw0rd!"')
check "mac: Tuwunel's reference vector" "$got" 44e0ec50d52aaa4029731dfcfe7e22123fa4c53e

mkdir -p "$T/bin"
# The fake kubectl: Secret NS/NAME's keys are files in $FAKE/secrets/NS/NAME.
cat >"$T/bin/kubectl" <<'KUBECTL'
#!/usr/bin/env bash
echo "kubectl $*" >>"$FAKE/argv"
ns=theos-system
if [ "$1" = -n ]; then ns=$2; shift 2; fi
case "$1 $2" in
"get secret")
	dir="$FAKE/secrets/$ns/$3"
	[ -d "$dir" ] || exit 1
	if [ "${4:-}" = -o ]; then key=${5#jsonpath=\{.data.}; base64 -w0 <"$dir/${key%\}}"; fi ;;
"create secret")
	dir="$FAKE/secrets/$ns/$4"
	[ -d "$dir" ] && { echo "AlreadyExists" >&2; exit 1; }
	mkdir -p "$dir"
	for a in "${@:5}"; do
		case $a in
		--from-file=*=/dev/stdin) k=${a#--from-file=}; cat >"$dir/${k%%=*}" ;;
		--from-literal=*) kv=${a#--from-literal=}; printf '%s' "${kv#*=}" >"$dir/${kv%%=*}" ;;
		esac
	done
	echo "create $ns/$4" >>"$FAKE/creates" ;;
*) echo "unexpected kubectl $*" >&2; exit 2 ;;
esac
KUBECTL
# The fake homeserver: accounts are files in $FAKE/users, each holding its
# password; a registration's HMAC is checked under $SHARED.
cat >"$T/bin/curl" <<'CURL'
#!/usr/bin/env bash
echo "curl $*" >>"$FAKE/argv"
url=${*: -1}
case $url in
*/_matrix/client/versions) [ -e "$FAKE/up" ] || exit 7 ;;
*/_synapse/admin/v1/register)
	if [[ " $* " != *" POST "* ]]; then echo '{"nonce":"n1"}'; exit 0; fi
	body=$(cat)
	user=$(jq -r .username <<<"$body") pass=$(jq -r .password <<<"$body")
	want=$(printf '%s\0%s\0%s\0notadmin' n1 "$user" "$pass" | openssl dgst -sha1 -hmac "$SHARED" | awk '{print $NF}')
	if [ "$(jq -r .mac <<<"$body")" != "$want" ]; then echo '{"errcode":"M_FORBIDDEN"}'; exit 0; fi
	if [ -e "$FAKE/users/$user" ]; then echo '{"errcode":"M_USER_IN_USE"}'; exit 0; fi
	mkdir -p "$FAKE/users" && printf '%s' "$pass" >"$FAKE/users/$user"
	echo "{\"user_id\":\"@$user:theos.internal\"}" ;;
*/_matrix/client/v3/login)
	body=$(cat)
	user=$(jq -r .identifier.user <<<"$body")
	[ "$(cat "$FAKE/users/$user" 2>/dev/null)" = "$(jq -r .password <<<"$body")" ] || exit 22
	n=$(($(cat "$FAKE/logins" 2>/dev/null || echo 0) + 1)); echo "$n" >"$FAKE/logins"
	echo "{\"access_token\":\"$TOKEN_PREFIX$n\"}" ;;
*) echo "unexpected curl $*" >&2; exit 2 ;;
esac
CURL
chmod +x "$T/bin/kubectl" "$T/bin/curl"

export FAKE=$T PATH="$T/bin:$PATH"
SHARED=$(head -c 18 /dev/urandom | base64 | tr -d '/+=')
TOKEN_PREFIX=tok$(head -c 9 /dev/urandom | base64 | tr -d '/+=')
export SHARED TOKEN_PREFIX
touch "$T/up"

# run: the script against the fakes; its output in $T/out, its exit code printed.
run() {
	rm -f "$T/creates"
	HOMESERVER=http://theos-matrix.theos-system.svc:8008 SERVER_NAME=theos.internal SHARED_SECRET=$SHARED \
		WAIT=${WAIT:-3} BOT=theos BOT_SECRET=theos-matrix-bot TOKEN_SECRET=matrix-bot NAMESPACES="apps shop" \
		ACCOUNTS="ada" ACCOUNT_PREFIX=theos-matrix-account- bash "$SCRIPT" >"$T/out" 2>&1
	echo $?
}
secret() { cat "$T/secrets/$1/$2/$3" 2>/dev/null; }
leaked() {
	local s n=0
	for s in "$@"; do [ -n "$s" ] && grep -qF -- "$s" "$T/argv" && n=$((n + 1)); done
	echo "$n"
}

check "first run: succeeds" "$(run)" 0
check "first run: makes the bot's password, the tokens and ada's Secret" "$(sort "$T/creates" | tr '\n' ' ')" \
	"create apps/matrix-bot create shop/matrix-bot create theos-system/theos-matrix-account-ada create theos-system/theos-matrix-bot "
check "first run: the bot's account has the kept password" "$(cat "$T/users/theos")" \
	"$(secret theos-system theos-matrix-bot password)"
check "first run: one login, its token in both namespaces" \
	"$(secret apps matrix-bot accessToken) $(secret shop matrix-bot accessToken)" "${TOKEN_PREFIX}1 ${TOKEN_PREFIX}1"
check "first run: ada's Secret names her" "$(secret theos-system theos-matrix-account-ada userID)" "@ada:theos.internal"
check "first run: ada's account has her Secret's password" "$(cat "$T/users/ada")" \
	"$(secret theos-system theos-matrix-account-ada password)"
check "first run: no password or token in argv" \
	"$(leaked "$(cat "$T/users/theos")" "$(cat "$T/users/ada")" "${TOKEN_PREFIX}1")" 0
check "first run: the shared secret in no kubectl or curl argv" "$(grep -cF -- "$SHARED" "$T/argv")" 0

check "second run: succeeds" "$(run)" 0
check "second run: makes nothing" "$(cat "$T/creates" 2>/dev/null)" ""
check "second run: logs in no more" "$(cat "$T/logins")" 1

rm -rf "$T/secrets/apps/matrix-bot"
check "a deleted token: succeeds" "$(run)" 0
check "a deleted token: made again, from a new login" "$(cat "$T/creates") $(secret apps matrix-bot accessToken)" \
	"create apps/matrix-bot ${TOKEN_PREFIX}2"
check "a deleted token: the other namespace's kept" "$(secret shop matrix-bot accessToken)" "${TOKEN_PREFIX}1"

rm -rf "$T/secrets/theos-system/theos-matrix-account-ada"
check "an account that exists: succeeds" "$(run)" 0
check "an account that exists: no Secret with a password it doesn't have" "$(cat "$T/creates" 2>/dev/null)" ""
check "an account that exists: says so" "$(grep -c 'exists already' "$T/out")" 1

rm -f "$T/up"
check "no homeserver: fails" "$(WAIT=1 run)" 1
check "no homeserver: says so" "$(grep -c 'did not answer within 1s' "$T/out")" 1

exit "$fail"
