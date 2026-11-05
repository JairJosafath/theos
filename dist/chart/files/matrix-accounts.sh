#!/usr/bin/env bash
# The in-cluster Matrix homeserver's accounts (chat.homeserver), made by Job
# <release>-matrix-accounts after every install and upgrade
# (templates/chat/accounts.yaml), on the seed image. Registration is off on
# the homeserver, so each account is made through its shared-secret
# registration (POST /_synapse/admin/v1/register: an HMAC-SHA1 under the
# shared secret of the nonce, the name, the password and "notadmin"):
#
# - the bot, BOT, whose password it keeps in Secret BOT_SECRET in its own
#   namespace, and whose access token it puts, under accessToken, in Secret
#   TOKEN_SECRET in each of NAMESPACES that lacks one: what a Chat's
#   credentialsSecretRef names;
# - each of ACCOUNTS, a person's account, whose user ID and first password it
#   keeps in Secret <ACCOUNT_PREFIX><localpart>, for the admin to hand over.
#
# It never overwrites: a Secret that exists is left alone and an account that
# exists is kept. To rotate the bot's token, delete a namespace's
# TOKEN_SECRET and run it again (helm upgrade). Every password and token goes
# to kubectl and curl on stdin, never in argv; only openssl takes the shared
# secret as an argument, inside this Job's own container.
#
# Environment: HOMESERVER (the homeserver's URL), SERVER_NAME, SHARED_SECRET,
# WAIT (seconds to wait for the homeserver to answer), BOT, BOT_SECRET,
# TOKEN_SECRET, NAMESPACES and ACCOUNTS (space-separated), ACCOUNT_PREFIX.
# hack/matrix-accounts_test.sh runs it against fakes.
set -euo pipefail

# mac NONCE USER PASSWORD: the registration's HMAC-SHA1, in hex.
mac() {
	printf '%s\0%s\0%s\0notadmin' "$1" "$2" "$3" | openssl dgst -sha1 -hmac "$SHARED_SECRET" | awk '{ print $NF }'
}

# register USER PASSWORD: makes the account. Returns 0 when it made it, 2
# when the account exists, 1 on anything else.
register() {
	local nonce out
	nonce=$(curl -fsS -m 10 "$HOMESERVER/_synapse/admin/v1/register" | jq -er .nonce)
	out=$(NONCE=$nonce NAME=$1 PASSWORD=$2 MAC=$(mac "$nonce" "$1" "$2") jq -n \
		'{nonce: env.NONCE, username: env.NAME, password: env.PASSWORD, mac: env.MAC, admin: false, inhibit_login: true}' |
		curl -sS -m 10 -X POST -H 'Content-Type: application/json' --data-binary @- \
			"$HOMESERVER/_synapse/admin/v1/register")
	if jq -e '.user_id' >/dev/null 2>&1 <<<"$out"; then
		echo "Made @$1:$SERVER_NAME"
		return 0
	fi
	if [ "$(jq -r '.errcode // empty' 2>/dev/null <<<"$out")" = M_USER_IN_USE ]; then
		return 2
	fi
	echo "The homeserver refused to make @$1:$SERVER_NAME: $out" >&2
	return 1
}

# login USER PASSWORD: prints a new access token of the account.
login() {
	NAME=$1 PASSWORD=$2 jq -n '{type: "m.login.password", identifier: {type: "m.id.user", user: env.NAME},
		password: env.PASSWORD, initial_device_display_name: "theos"}' |
		curl -fsS -m 10 -X POST -H 'Content-Type: application/json' --data-binary @- \
			"$HOMESERVER/_matrix/client/v3/login" | jq -er .access_token
}

# has NAME [NAMESPACE]: whether the Secret exists.
has() {
	if [ $# -gt 1 ]; then kubectl -n "$2" get secret "$1" >/dev/null 2>&1; else kubectl get secret "$1" >/dev/null 2>&1; fi
}

# password: a new random password.
password() { openssl rand -hex 24; }

main() {
	local i pw token ns a s rc missing=()
	for ((i = 0; i < WAIT; i++)); do
		curl -fsS -m 5 -o /dev/null "$HOMESERVER/_matrix/client/versions" && break
		sleep 1
	done
	if ! curl -fsS -m 5 -o /dev/null "$HOMESERVER/_matrix/client/versions"; then
		echo "The homeserver $HOMESERVER did not answer within ${WAIT}s" >&2
		return 1
	fi

	if ! has "$BOT_SECRET"; then
		password | tr -d '\n' | kubectl create secret generic "$BOT_SECRET" --from-file=password=/dev/stdin
	fi
	pw=$(kubectl get secret "$BOT_SECRET" -o jsonpath='{.data.password}' | base64 -d)
	rc=0
	register "$BOT" "$pw" || rc=$?
	if [ "$rc" = 1 ]; then return 1; fi
	for ns in $NAMESPACES; do
		has "$TOKEN_SECRET" "$ns" || missing+=("$ns")
	done
	if [ ${#missing[@]} -gt 0 ]; then
		if ! token=$(login "$BOT" "$pw"); then
			echo "Could not log in as @$BOT:$SERVER_NAME with the password in Secret $BOT_SECRET." >&2
			echo "If that Secret was made again after the account, deactivate the account or restore the Secret." >&2
			return 1
		fi
		for ns in "${missing[@]}"; do
			printf '%s' "$token" | kubectl -n "$ns" create secret generic "$TOKEN_SECRET" --from-file=accessToken=/dev/stdin
		done
	fi

	for a in $ACCOUNTS; do
		s=$ACCOUNT_PREFIX$a
		if has "$s"; then
			continue
		fi
		pw=$(password)
		rc=0
		register "$a" "$pw" || rc=$?
		case $rc in
		0)
			printf '%s' "$pw" | kubectl create secret generic "$s" \
				--from-literal=userID="@$a:$SERVER_NAME" --from-file=password=/dev/stdin
			;;
		2) echo "@$a:$SERVER_NAME exists already; its password is its own, so Secret $s is not made" ;;
		*) return 1 ;;
		esac
	done
}

if [ -z "${MATRIX_ACCOUNTS_SOURCED:-}" ]; then
	main
fi
