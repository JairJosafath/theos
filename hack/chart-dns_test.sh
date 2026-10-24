#!/usr/bin/env bash
# Tests of the chart's c4-factory.clusterDNSPeers helper
# (dist/chart/templates/_helpers.tpl): it accepts exactly the clusterDNS
# entries the operator's --cluster-dns accepts (internal/controller/cluster_dns.go,
# ParseClusterDNS; its TestParseClusterDNS table is mirrored here), so a value
# the chart renders is one the manager starts with (#1257, #1763). Each case
# renders the chart with the entries and expects a render or a refusal naming
# the fault.
# Run: hack/chart-dns_test.sh (needs bin/helm: make install-helm)
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
ROOT=$(dirname "$(cd "$(dirname "$0")" && pwd)")
HELM=${HELM:-$ROOT/bin/helm}
fail=0
if ! [ -x "$HELM" ]; then
	echo "FAIL no helm at $HELM; run make install-helm"
	exit 1
fi
# render ENTRY...: the chart with clusterDNS set to the entries (none: an
# empty list); its output and errors in $T/out.
render() {
	{
		echo "clusterDNS:"
		[ $# -gt 0 ] || echo "  []"
		for e; do printf '  - "%s"\n' "$e"; done
	} >"$T/values.yaml"
	"$HELM" template c4-factory "$ROOT/dist/chart" --namespace c4-factory-system \
		--set 'gitea.projects={apps}' -f "$T/values.yaml" >"$T/out" 2>&1
}
# accepts ENTRY...: the render passes.
accepts() {
	if render "$@"; then echo "ok   accepts $*"; else
		echo "FAIL accepts $*: refused:"
		grep -o 'clusterDNS.*' "$T/out" | head -1
		fail=1
	fi
}
# refuses WHAT ENTRY...: the render fails, and its message holds WHAT.
refuses() {
	local what=$1
	shift
	if render "$@"; then
		echo "FAIL refuses $*: rendered"
		fail=1
	elif ! grep -q -- "$what" "$T/out"; then
		echo "FAIL refuses $*: not for $what:"
		grep -o 'clusterDNS.*' "$T/out" | head -1
		fail=1
	else echo "ok   refuses $*"; fi
}

accepts kube-system/k8s-app=kube-dns
accepts openshift-dns/dns.operator.openshift.io/daemonset-dns=default
accepts dns/app=coredns,tier=system
accepts 169.254.20.10/32 kube-system/k8s-app=kube-dns
accepts 169.254.20.10
accepts 10.96.0.0/24
accepts kube-system/a=
# The operator's limits: 63 characters for a namespace, a key's name and a
# value, 253 for a key's prefix.
ns63=$(printf 'a%.0s' {1..63})
accepts "$ns63/a=b"
accepts "kube-system/$ns63=$ns63"
accepts "kube-system/$(printf 'aa.%.0s' {1..84})a/x=y"
# The CI fence's DNS rule carries the namespace, key and value, and the
# manager's --cluster-dns the entry.
render "$ns63/$ns63=$ns63"
if grep -q "metadata.name: \"$ns63\"" "$T/out" && grep -q "^ *\"$ns63\": \"$ns63\"$" "$T/out" &&
	grep -q -- "--cluster-dns=$ns63/$ns63=$ns63$" "$T/out"; then
	echo "ok   renders the 63-character namespace, key and value"
else
	echo "FAIL the render lacks the 63-character namespace, key or value"
	fail=1
fi

refuses "want at least one entry"
refuses "private ranges" 8.8.8.8
refuses "private ranges" 0.0.0.0/0
refuses "private ranges" 10.0.0.0/7
refuses "not an IPv4 address block" 169.254.20
refuses "want namespace/key=value" kube-dns
refuses "want key=value" kube-system/
refuses 'namespace ""' /k8s-app=kube-dns
refuses 'namespace "Kube_System"' Kube_System/k8s-app=kube-dns
refuses "want key=value" kube-system/k8s-app
refuses 'key "k8s-app!"' 'kube-system/k8s-app!=kube-dns'
refuses "want key=value" 'kube-system/k8s-app in (a)'
refuses "want key=value" kube-system/=kube-dns
refuses "want key=value" kube-system/k8s-app==kube-dns
refuses "want key=value" kube-system/a=b=c
refuses "want key=value" kube-system/a=b,
refuses 'key " a"' 'kube-system/ a=b'
refuses 'value "b c"' 'kube-system/a=b c'
refuses 'value "-b"' kube-system/a=-b
refuses 'value "b:c"' kube-system/a=b:c
refuses 'namespace "fd00:10:96::a"' fd00:10:96::a/128
# #1763: the key must start and end with an alphanumeric, as must the value.
refuses 'key "-x"' kube-system/-x=y.
refuses 'value "y."' kube-system/x=y.
refuses 'key "Foo.io/a"' kube-system/Foo.io/a=b
refuses 'key "a/b/c"' kube-system/a/b/c=d
refuses 'key "/a"' kube-system//a=b
# #1763: the 63- and 253-character limits.
refuses "at most 63 characters" "${ns63}a/a=b"
refuses "at most 63 characters" "kube-system/${ns63}a=b"
refuses "at most 63 characters" "kube-system/a=${ns63}a"
refuses "at most 253 characters" "kube-system/$(printf 'aa.%.0s' {1..84})aa/x=y"

exit $fail
