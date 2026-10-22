package controller

import (
	"reflect"
	"strings"
	"testing"

	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Each --cluster-dns entry is a namespace and pod labels or a private address
// block (#1257).
func TestParseClusterDNS(t *testing.T) {
	labels := func(ns string, pods map[string]string) networkingv1.NetworkPolicyPeer {
		return networkingv1.NetworkPolicyPeer{
			NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"kubernetes.io/metadata.name": ns}},
			PodSelector:       &metav1.LabelSelector{MatchLabels: pods},
		}
	}
	block := func(cidr string) networkingv1.NetworkPolicyPeer {
		return networkingv1.NetworkPolicyPeer{IPBlock: &networkingv1.IPBlock{CIDR: cidr}}
	}
	tests := []struct {
		entry string
		want  networkingv1.NetworkPolicyPeer
		err   string
	}{
		{entry: DefaultClusterDNS, want: labels("kube-system", map[string]string{"k8s-app": "kube-dns"})},
		// A label key may hold a slash: the entry splits at the first one.
		{entry: "openshift-dns/dns.operator.openshift.io/daemonset-dns=default",
			want: labels("openshift-dns", map[string]string{"dns.operator.openshift.io/daemonset-dns": "default"})},
		{entry: "dns/app=coredns,tier=system", want: labels("dns", map[string]string{"app": "coredns", "tier": "system"})},
		{entry: "169.254.20.10/32", want: block("169.254.20.10/32")},
		{entry: "169.254.20.10", want: block("169.254.20.10/32")},
		{entry: "10.96.0.0/24", want: block("10.96.0.0/24")},
		{entry: "8.8.8.8", err: "not an address block inside the private ranges"},
		{entry: "0.0.0.0/0", err: "not an address block inside the private ranges"},
		{entry: "10.0.0.0/7", err: "not an address block inside the private ranges"},
		{entry: "169.254.20", err: "not an address block inside the private ranges"},
		{entry: "kube-dns", err: "want namespace/key=value"},
		{entry: "kube-system/", err: "want namespace/key=value"},
		{entry: "/k8s-app=kube-dns", err: "namespace \"\""},
		{entry: "Kube_System/k8s-app=kube-dns", err: "namespace \"Kube_System\""},
		{entry: "kube-system/k8s-app", err: "key=value pairs, at least one"},
		{entry: "kube-system/k8s-app!=kube-dns", err: "pod labels"},
		{entry: "kube-system/k8s-app in (a)", err: "key=value pairs, at least one"},
		{entry: "kube-system/=kube-dns", err: "pod labels"},
	}
	for _, tc := range tests {
		got, err := ParseClusterDNS(tc.entry)
		if tc.err != "" {
			if err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Errorf("ParseClusterDNS(%q) error %v, want one saying %q", tc.entry, err, tc.err)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseClusterDNS(%q): %v", tc.entry, err)
		} else if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("ParseClusterDNS(%q) = %+v, want %+v", tc.entry, got, tc.want)
		}
	}

	peers, err := ClusterDNSPeers(nil)
	if err != nil || len(peers) != 1 || !reflect.DeepEqual(peers[0], tests[0].want) {
		t.Errorf("ClusterDNSPeers(nil) = %+v, %v; want the default alone", peers, err)
	}
	peers, err = ClusterDNSPeers([]string{"169.254.20.10", DefaultClusterDNS})
	if err != nil || len(peers) != 2 || peers[0].IPBlock == nil || peers[1].PodSelector == nil {
		t.Errorf("ClusterDNSPeers(two) = %+v, %v; want both, in order", peers, err)
	}
	if _, err := ClusterDNSPeers([]string{DefaultClusterDNS, "1.1.1.1"}); err == nil {
		t.Error("ClusterDNSPeers with a public address: no error")
	}
}
