/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"context"
	"net/url"
	"slices"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/model"
)

// AgentNetworkPolicyName is the NetworkPolicy that fences agent sandboxes.
const AgentNetworkPolicyName = "c4-agent-sandbox"

// The model proxy's pods, as the chart and config/model-proxy label them, and
// the port they listen on.
const (
	ModelProxyAppName = "c4-model-proxy"
	ModelProxyPort    = 8080
)

// ensureAgentNetwork fences agent sandboxes in the project: no traffic in, and
// out only DNS, the in-cluster Git hosts of the project's GitProviders, HTTPS
// to public addresses (the model API, package registries) and, when the
// operator has one, the model proxy. The Kubernetes API and every other
// in-cluster service stay out of reach.
func (r *ProjectReconciler) ensureAgentNetwork(ctx context.Context, project *c4v1alpha1.Project) error {
	var providers c4v1alpha1.GitProviderList
	if err := r.List(ctx, &providers, client.InNamespace(project.Namespace)); err != nil {
		return err
	}
	var gitNamespaces []string
	for _, p := range providers.Items {
		u, err := url.Parse(p.Spec.URL)
		if err != nil {
			continue
		}
		if ns, ok := model.ClusterNamespace(u.Hostname()); ok && !slices.Contains(gitNamespaces, ns) {
			gitNamespaces = append(gitNamespaces, ns)
		}
	}
	slices.Sort(gitNamespaces)

	tcp, udp := corev1.ProtocolTCP, corev1.ProtocolUDP
	dns, https := intstr.FromInt32(53), intstr.FromInt32(443)
	egress := []networkingv1.NetworkPolicyEgressRule{
		{
			To: []networkingv1.NetworkPolicyPeer{{
				NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{
					corev1.LabelMetadataName: metav1.NamespaceSystem}},
				PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"k8s-app": "kube-dns"}},
			}},
			Ports: []networkingv1.NetworkPolicyPort{{Protocol: &udp, Port: &dns}, {Protocol: &tcp, Port: &dns}},
		},
		{
			To: []networkingv1.NetworkPolicyPeer{{IPBlock: &networkingv1.IPBlock{
				CIDR: "0.0.0.0/0", Except: model.PrivateRanges()}}},
			Ports: []networkingv1.NetworkPolicyPort{{Protocol: &tcp, Port: &https}},
		},
	}
	for _, ns := range gitNamespaces {
		egress = append(egress, networkingv1.NetworkPolicyEgressRule{To: []networkingv1.NetworkPolicyPeer{{
			NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{corev1.LabelMetadataName: ns}},
		}}})
	}
	if r.ModelProxyURL != "" {
		// A sandbox on the shared token holds no token: the proxy's pods in the
		// release namespace are its only way to Claude with one (decision 6 of
		// docs/plans/model-proxy.md). The proxy's own policy admits only
		// sandboxes of namespaces opted in to the shared Secrets.
		proxy := intstr.FromInt32(ModelProxyPort)
		egress = append(egress, networkingv1.NetworkPolicyEgressRule{
			To: []networkingv1.NetworkPolicyPeer{{
				NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{
					corev1.LabelMetadataName: r.SourceNamespace}},
				PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{
					"app.kubernetes.io/name": ModelProxyAppName}},
			}},
			Ports: []networkingv1.NetworkPolicyPort{{Protocol: &tcp, Port: &proxy}},
		})
	}

	policy := &networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{
		Name: AgentNetworkPolicyName, Namespace: project.Namespace}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, policy, func() error {
		policy.Labels = projectLabels(project.Namespace)
		policy.Spec = networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{MatchLabels: map[string]string{LabelAgentSandbox: agentSandboxValue}},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress, networkingv1.PolicyTypeEgress},
			Egress:      egress,
		}
		return controllerutil.SetControllerReference(project, policy, r.Scheme)
	})
	return err
}
