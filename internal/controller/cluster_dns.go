/*
Copyright 2026 The theos Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"fmt"
	"regexp"
	"strings"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/JairJosafath/theos/internal/model"
)

// DefaultClusterDNS is where cluster DNS runs unless --cluster-dns says
// otherwise: kube-dns (CoreDNS) in kube-system.
const DefaultClusterDNS = "kube-system/k8s-app=kube-dns"

// addressBlock is the address form of a --cluster-dns entry: an IPv4 address
// with or without a prefix length.
var addressBlock = regexp.MustCompile(`^[0-9.]+(/[0-9]+)?$`)

// ParseClusterDNS turns one --cluster-dns entry into the peer the fences let
// DNS out to (#1257). An entry is either namespace/key=value[,key=value], the
// namespace and pod labels DNS runs under (kube-system/k8s-app=kube-dns,
// openshift-dns/dns.operator.openshift.io/daemonset-dns=default), or an
// address block inside model.PrivateRanges (169.254.20.10/32 for NodeLocal
// DNSCache; a bare address means /32). An address outside the private ranges
// is refused: cluster DNS answers on a cluster address, and a public one would
// open the fence to the world on port 53.
func ParseClusterDNS(entry string) (networkingv1.NetworkPolicyPeer, error) {
	if addressBlock.MatchString(entry) {
		cidr := entry
		if !strings.Contains(cidr, "/") {
			cidr += "/32"
		}
		if !model.PrivateBlock(cidr) {
			return networkingv1.NetworkPolicyPeer{}, fmt.Errorf(
				"cluster DNS %q: not an address block inside the private ranges %s", entry,
				strings.Join(model.PrivateRanges(), ", "))
		}
		return networkingv1.NetworkPolicyPeer{IPBlock: &networkingv1.IPBlock{CIDR: cidr}}, nil
	}
	namespace, selector, ok := strings.Cut(entry, "/")
	if !ok || selector == "" || strings.Contains(entry, ":") { // the fence is IPv4: no IPv6 block
		return networkingv1.NetworkPolicyPeer{}, fmt.Errorf(
			"cluster DNS %q: want namespace/key=value[,key=value] or an IPv4 address block", entry)
	}
	if errs := validation.IsDNS1123Label(namespace); len(errs) > 0 {
		return networkingv1.NetworkPolicyPeer{}, fmt.Errorf("cluster DNS %q: namespace %q: %s", entry, namespace, errs[0])
	}
	// The pod labels are exactly key=value pairs, the grammar the chart's
	// theos.clusterDNSPeers helper accepts, so one value renders the same
	// peer for both fences: no ==, no !=, no set expression, no spaces.
	pods := map[string]string{}
	for pair := range strings.SplitSeq(selector, ",") {
		key, value, ok := strings.Cut(pair, "=")
		if !ok || key == "" || strings.ContainsAny(pair, " \t") || strings.Contains(value, "=") {
			return networkingv1.NetworkPolicyPeer{}, fmt.Errorf(
				"cluster DNS %q: pod labels must be key=value pairs, at least one", entry)
		}
		if errs := validation.IsQualifiedName(key); len(errs) > 0 {
			return networkingv1.NetworkPolicyPeer{}, fmt.Errorf("cluster DNS %q: pod labels: key %q: %s", entry, key, errs[0])
		}
		if errs := validation.IsValidLabelValue(value); len(errs) > 0 {
			return networkingv1.NetworkPolicyPeer{}, fmt.Errorf("cluster DNS %q: pod labels: value %q: %s", entry, value, errs[0])
		}
		pods[key] = value
	}
	return networkingv1.NetworkPolicyPeer{
		NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{corev1.LabelMetadataName: namespace}},
		PodSelector:       &metav1.LabelSelector{MatchLabels: pods},
	}, nil
}

// ClusterDNSPeers parses every --cluster-dns entry, in order; none means
// DefaultClusterDNS.
func ClusterDNSPeers(entries []string) ([]networkingv1.NetworkPolicyPeer, error) {
	if len(entries) == 0 {
		entries = []string{DefaultClusterDNS}
	}
	peers := make([]networkingv1.NetworkPolicyPeer, 0, len(entries))
	for _, entry := range entries {
		peer, err := ParseClusterDNS(entry)
		if err != nil {
			return nil, err
		}
		peers = append(peers, peer)
	}
	return peers, nil
}
