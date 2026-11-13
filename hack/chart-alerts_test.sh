#!/usr/bin/env bash
# Tests of the chart's alert receiver values (dist/chart/values.yaml, alerts;
# docs/alerts.md, #64): the git-events NetworkPolicy admits alert senders only
# when alerts.from names them, never a rule without a from, which would admit
# everyone; the alert Ingress serves /alerts/ alone, with TLS, and is refused
# without its host, its peers or the listener; the manager takes
# --alert-drain-timeout from alerts.drainTimeout, and the chart refuses one
# that is no Go duration; the manager's --alerts-url is alerts.url, else the
# alert Ingress's https://<host> (#240).
# Run: hack/chart-alerts_test.sh (needs bin/helm: make install-helm)
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
ROOT=$(dirname "$(cd "$(dirname "$0")" && pwd)")
HELM=${HELM:-$ROOT/bin/helm}
fail=0
if ! [ -x "$HELM" ]; then
	echo "FAIL no helm at $HELM; run make install-helm"
	exit 1
fi
PEER='alerts.from[0].namespaceSelector.matchLabels.kubernetes\.io/metadata\.name=monitoring'
INGRESS='alerts.ingress.enabled=true,alerts.ingress.host=alerts.example.com'
# render SET: the chart with gitea.projects={apps} and SET; its output and
# errors in $T/out.
render() {
	"$HELM" template theos "$ROOT/dist/chart" --namespace theos-system \
		--set "gitea.projects={apps}${1:+,$1}" >"$T/out" 2>&1
}
# doc SOURCE: the rendered documents of template SOURCE.
doc() { awk -v s="# Source: theos/templates/$1" '/^---/ { p = 0 } $0 == s { p = 1 } p' "$T/out"; }
ok() { echo "ok   $1"; }
bad() {
	echo "FAIL $1"
	fail=1
}
# The rules of the git-events NetworkPolicy: one line per rule, its peers
# ("-" for none) and ports.
rules() {
	doc git-events/networkpolicy.yaml | awk '
		/^  ingress:$/ { in_rules = 1; next }
		!in_rules { next }
		/^    - / { if (r != "") print r; r = ""; peers = 0 }
		/^    - from:$/ || /^      from:$/ { peers = 1; next }
		/^      ports:$/ || /^    - ports:$/ { peers = 0; r = r (r ~ /from/ ? "" : "from - ") "ports"; next }
		peers && /kubernetes.io\/metadata.name: / { r = r "from " $2 " " }
		/- port: / { r = r " " $3 }
		END { if (r != "") print r }'
}
want_rules() { # want_rules WHAT WANT
	got=$(rules)
	if [ "$got" = "$2" ]; then ok "$1"; else bad "$1: got [$got] want [$2]"; fi
}

render ""
want_rules "by default only Gitea reaches the listener" "from gitea ports 8082
from - ports 8081 8443 9443"
[ -z "$(doc git-events/ingress.yaml)" ] && ok "by default no Ingress" || bad "an Ingress by default"
grep -q -- '^ *- --alert-drain-timeout=5s$' "$T/out" && ok "the drain timeout defaults to 5s" ||
	bad "no --alert-drain-timeout=5s by default"

render "$PEER"
want_rules "alerts.from admits its peers to the listener alone" "from gitea ports 8082
from monitoring ports 8082
from - ports 8081 8443 9443"

render "$PEER,alerts.drainTimeout=1m30s,manager.terminationGracePeriodSeconds=120"
grep -q -- '^ *- --alert-drain-timeout=1m30s$' "$T/out" && ok "alerts.drainTimeout reaches the flag" ||
	bad "alerts.drainTimeout=1m30s is not the flag"

render "$PEER,$INGRESS,alerts.ingress.className=nginx,alerts.ingress.tlsSecretName=alerts-tls"
ing=$(doc git-events/ingress.yaml)
if grep -q '^kind: Ingress$' <<<"$ing" &&
	grep -q '^  ingressClassName: nginx$' <<<"$ing" &&
	grep -A2 '^  tls:$' <<<"$ing" | grep -q '"alerts.example.com"' &&
	grep -q '^      secretName: alerts-tls$' <<<"$ing" &&
	[ "$(grep -c '^ *- path: ' <<<"$ing")" = 1 ] &&
	grep -q '^          - path: /alerts/$' <<<"$ing" &&
	grep -q '^                name: theos-git-events$' <<<"$ing"; then
	ok "the Ingress serves /alerts/ alone, with TLS for its host, to the git-events Service"
else
	bad "the Ingress is not /alerts/ alone with TLS to theos-git-events:"
	echo "$ing" | sed 's/^/     /'
fi
if ! render "$PEER,alerts.ingress.enabled=true,alerts.ingress.host=alerts.example.com"; then
	bad "the Ingress without tlsSecretName does not render:"
	head -3 "$T/out"
elif grep -q '^      secretName:' <<<"$(doc git-events/ingress.yaml)"; then
	bad "a TLS secretName without tlsSecretName"
else
	ok "without tlsSecretName the Ingress controller's default certificate"
fi

# refuses WHAT SET: the render fails, and its message holds WHAT.
refuses() {
	if render "$2"; then bad "renders $2"; elif grep -q -- "$1" "$T/out"; then ok "refuses $2"; else
		bad "refuses $2, but not for $1:"
		head -3 "$T/out"
	fi
}
refuses "needs alerts.ingress.host" "$PEER,alerts.ingress.enabled=true"
refuses "needs alerts.from" "$INGRESS"
refuses "needs gitEvents.enabled" "$PEER,$INGRESS,gitEvents.enabled=false"
refuses "is no Go duration" "alerts.drainTimeout=5"
refuses "is no Go duration" "alerts.drainTimeout=five"

refuses "is not under manager.terminationGracePeriodSeconds" "alerts.drainTimeout=10s"
refuses "is not under manager.terminationGracePeriodSeconds" "alerts.drainTimeout=30s"
refuses "is not under manager.terminationGracePeriodSeconds" "alerts.drainTimeout=1m"
refuses "is not under manager.terminationGracePeriodSeconds" "alerts.drainTimeout=1m30s,manager.terminationGracePeriodSeconds=90"
refuses "is not under manager.terminationGracePeriodSeconds" "alerts.drainTimeout=15s,manager.terminationGracePeriodSeconds=15"
if render "alerts.drainTimeout=9s,manager.terminationGracePeriodSeconds=10" &&
	grep -q -- '^ *- --alert-drain-timeout=9s$' "$T/out"; then
	ok "a drain timeout under the grace period renders"
else
	bad "alerts.drainTimeout=9s under a 10s grace period does not render:"
	head -3 "$T/out"
fi
if render "alerts.drainTimeout=1m,manager.terminationGracePeriodSeconds=null" ||
	! grep -q "is not under" "$T/out"; then
	bad "a null grace period (30s) did not refuse alerts.drainTimeout=1m"
else
	ok "a null grace period counts as 30s"
fi
if render "alerts.drainTimeout=25s,manager.terminationGracePeriodSeconds=null" &&
	grep -q -- '^ *- --alert-drain-timeout=25s$' "$T/out"; then
	ok "25s fits a null grace period"
else
	bad "alerts.drainTimeout=25s with a null grace period does not render:"
	head -3 "$T/out"
fi
# #64: 0 is admitted and means the 5s default, as in the receiver; it must
# not crash the render, and it is compared with the grace period as 5s.
renders_drain() {
	if render "$2" && grep -q -- "^ *- --alert-drain-timeout=$1\$" "$T/out"; then
		ok "renders $2"
	else
		bad "does not render $2:"
		head -3 "$T/out"
	fi
}
renders_drain 0 "alerts.drainTimeout=0"
renders_drain 0 "alerts.drainTimeout=0,manager.terminationGracePeriodSeconds=6"
renders_drain 0s "alerts.drainTimeout=0s,manager.terminationGracePeriodSeconds=6"
refuses "is not under manager.terminationGracePeriodSeconds" "alerts.drainTimeout=0,manager.terminationGracePeriodSeconds=3"
refuses "is not under manager.terminationGracePeriodSeconds" "alerts.drainTimeout=0s,manager.terminationGracePeriodSeconds=3"
# The same through --set-string.
for v in 0 0s 00s 0ms; do
	if "$HELM" template theos "$ROOT/dist/chart" --namespace theos-system \
		--set "gitea.projects={apps}" --set-string "alerts.drainTimeout=$v" >"$T/out" 2>&1; then
		ok "--set-string alerts.drainTimeout=$v renders"
	else
		bad "--set-string alerts.drainTimeout=$v does not render:"
		head -3 "$T/out"
	fi
	if "$HELM" template theos "$ROOT/dist/chart" --namespace theos-system \
		--set "gitea.projects={apps}" --set-string "alerts.drainTimeout=$v" \
		--set manager.terminationGracePeriodSeconds=3 >"$T/out" 2>&1 ||
		! grep -q "is not under" "$T/out"; then
		bad "--set-string alerts.drainTimeout=$v under grace 3 is not refused"
	else
		ok "--set-string alerts.drainTimeout=$v under grace 3 is refused"
	fi
done
# #240: --alerts-url, which AlertSource status.url starts with: none by
# default, https://<host> with the alert Ingress, alerts.url when given (its
# trailing slash trimmed), and none without the listener.
alerts_url() { # alerts_url WHAT SET WANT: the manager's --alerts-url is WANT, or absent when WANT is empty.
	if ! render "$2"; then
		bad "$1: does not render $2:"
		head -3 "$T/out"
		return
	fi
	got=$(sed -n 's/^ *- --alerts-url=//p' "$T/out")
	if [ "$got" = "$3" ]; then ok "$1"; else bad "$1: --alerts-url is [$got], want [$3]"; fi
}
alerts_url "by default no --alerts-url" "" ""
alerts_url "the alert Ingress's host is the --alerts-url" "$PEER,$INGRESS" "https://alerts.example.com"
alerts_url "alerts.url is the --alerts-url" "alerts.url=https://hooks.example.org/" "https://hooks.example.org"
alerts_url "alerts.url wins over the Ingress's host" "$PEER,$INGRESS,alerts.url=https://hooks.example.org" \
	"https://hooks.example.org"
alerts_url "without the listener no --alerts-url" "gitEvents.enabled=false,alerts.url=https://hooks.example.org" ""
# #240: alerts.url without a scheme is refused, naming the value.
refuses "alerts.url" "alerts.url=hooks.example.org"

if ! render "gitEvents.enabled=false"; then
	bad "the chart without the listener does not render:"
	head -3 "$T/out"
elif grep -q -- '--alert-drain-timeout' "$T/out"; then
	bad "the drain flag without the listener"
else
	ok "without the listener no drain flag"
fi

exit $fail
