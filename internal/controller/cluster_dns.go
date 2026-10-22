/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
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

	"github.com/JairJosafath/c4factory/internal/model"
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
	if !ok || selector == "" {
		return networkingv1.NetworkPolicyPeer{}, fmt.Errorf(
			"cluster DNS %q: want namespace/key=value[,key=value] or an address block", entry)
	}
	if errs := validation.IsDNS1123Label(namespace); len(errs) > 0 {
		return networkingv1.NetworkPolicyPeer{}, fmt.Errorf("cluster DNS %q: namespace %q: %s", entry, namespace, errs[0])
	}
	pods, err := metav1.ParseToLabelSelector(selector)
	if err != nil {
		return networkingv1.NetworkPolicyPeer{}, fmt.Errorf("cluster DNS %q: pod labels: %w", entry, err)
	}
	if len(pods.MatchLabels) == 0 || len(pods.MatchExpressions) > 0 {
		return networkingv1.NetworkPolicyPeer{}, fmt.Errorf(
			"cluster DNS %q: pod labels must be key=value pairs, at least one", entry)
	}
	return networkingv1.NetworkPolicyPeer{
		NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{corev1.LabelMetadataName: namespace}},
		PodSelector:       &metav1.LabelSelector{MatchLabels: pods.MatchLabels},
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
