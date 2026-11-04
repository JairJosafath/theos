#!/usr/bin/env bash
# Tests for make claude-secret (hack/claude-secret.sh): the token from
# CLAUDE_TOKEN or the 0600 file, a missing or loose file failing, and the token
# never in kubectl's argv or the output. kubectl is faked.
# Run: hack/claude-secret_test.sh
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
fail=0
check() { if [ "$2" = "$3" ]; then echo "ok   $1"; else echo "FAIL $1: got [$2] want [$3]"; fail=1; fi; }

# The fake kubectl: each call's argv a line in $T/calls; create prints its stdin
# as the manifest, apply keeps it in $T/applied.
cat >"$T/kubectl" <<'KUBECTL'
#!/usr/bin/env bash
echo "$*" >>"$FAKE/calls"
case " $* " in
*" create "*) echo "token: $(cat)" ;;
*" apply "*) cat >"$FAKE/applied" ;;
esac
KUBECTL
chmod +x "$T/kubectl"
export FAKE=$T

file=$T/dev/secrets/claude-token
fake_token=$(head -c 18 /dev/urandom | base64 | tr -d '/+=')
cli_token=$(head -c 18 /dev/urandom | base64 | tr -d '/+=')

# run [VAR=VALUE...]: make claude-secret against the fake; its output in $T/out.
run() {
  rm -f "$T/calls" "$T/applied"
  env -u CLAUDE_TOKEN "$@" make -s -C "$ROOT" claude-secret KUBECTL="$T/kubectl" KIND_CLUSTER=test \
    CLAUDE_TOKEN_FILE="$file" CLAUDE_NAMESPACE=apps >"$T/out" 2>&1
}
leaked() { cat "$T/calls" "$T/out" 2>/dev/null | grep -c -e "$fake_token" -e "$cli_token"; }

# Missing file: fails and names the path.
run
check "missing: fails" "$?" 2
check "missing: names the path" "$(grep -c "$file is missing" "$T/out")" 1
check "missing: no kubectl" "$(cat "$T/calls" 2>/dev/null)" ""

# A loose mode fails and names the path.
mkdir -p "$(dirname "$file")"
printf '%s\n' "$fake_token" >"$file"
chmod 644 "$file"
run
check "loose: fails" "$?" 2
check "loose: names the path" "$(grep -c "$file has mode 644" "$T/out")" 1
check "loose: no kubectl" "$(cat "$T/calls" 2>/dev/null)" ""
check "loose: token not printed" "$(leaked)" 0
chmod 640 "$file"
run
check "group-readable: fails" "$?" 2

# 0600: the file's token, without its newline, goes to the Secret.
chmod 600 "$file"
run
check "file: succeeds" "$?" 0
check "file: says so" "$(cat "$T/out")" "Stored Secret apps/claude-credentials"
check "file: the token, on stdin" "$(cat "$T/applied")" "token: $fake_token"
# The two ends of the pipe start in either order.
check "file: kubectl calls" "$(LC_ALL=C sort "$T/calls")" "--context kind-test -n apps create secret generic claude-credentials --from-file=CLAUDE_CODE_OAUTH_TOKEN=/dev/stdin --dry-run=client -o yaml
--context kind-test apply --server-side --field-manager=theos-testenv -f -"
check "file: token not in argv or output" "$(leaked)" 0
chmod 400 "$file"
run
check "0400: succeeds" "$?" 0

# CLAUDE_TOKEN wins over the file, from the environment or the command line.
run CLAUDE_TOKEN="$cli_token"
check "env: succeeds" "$?" 0
check "env: its token" "$(cat "$T/applied")" "token: $cli_token"
check "env: token not in argv or output" "$(leaked)" 0
rm -f "$T/calls" "$T/applied"
make -s -C "$ROOT" claude-secret KUBECTL="$T/kubectl" KIND_CLUSTER=test CLAUDE_TOKEN_FILE="$T/none" \
  CLAUDE_NAMESPACE=apps CLAUDE_TOKEN="$cli_token" >"$T/out" 2>&1
check "command line: succeeds without the file" "$?" 0
check "command line: its token" "$(cat "$T/applied")" "token: $cli_token"

# By default the token goes to the operator's namespace, which shares it.
rm -f "$T/calls" "$T/applied"
make -s -C "$ROOT" claude-secret KUBECTL="$T/kubectl" KIND_CLUSTER=test CLAUDE_TOKEN_FILE="$file" >"$T/out" 2>&1
check "default: the operator's namespace" "$(cat "$T/out")" "Stored Secret theos-system/claude-credentials"

# An empty file fails.
chmod 600 "$file"; : >"$file"
run
check "empty: fails" "$?" 2
check "empty: says so" "$(grep -c "$file is empty" "$T/out")" 1

exit $fail
