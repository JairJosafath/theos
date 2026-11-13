#!/usr/bin/env bash
# Tests of the chart's alert receiver values (dist/chart/values.yaml, alerts;
# docs/alerts.md, #64): the git-events NetworkPolicy admits alert senders only
# when alerts.from names them, never a rule without a from, which would admit
# everyone; the alert Ingress serves /alerts/ alone, with TLS, and is refused
# without its host, its peers or the listener; the manager takes
# --alert-drain-timeout from alerts.drainTimeout, and the chart refuses one
# that is no Go duration.
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

render "$PEER,alerts.drainTimeout=1m30s"
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
render "$PEER,alerts.ingress.enabled=true,alerts.ingress.host=alerts.example.com"
grep -q '^      secretName:' <<<"$(doc git-events/ingress.yaml)" && bad "a TLS secretName without tlsSecretName" ||
	ok "without tlsSecretName the Ingress controller's default certificate"

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

render "gitEvents.enabled=false"
grep -q -- '--alert-drain-timeout' "$T/out" && bad "the drain flag without the listener" ||
	ok "without the listener no drain flag"

exit $fail
