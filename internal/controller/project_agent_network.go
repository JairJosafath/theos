/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
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
// out only what fenceEgress allows, the in-cluster Git hosts being those of the
// project's GitProviders, and, when the operator has one, the model proxy.
func (r *ProjectReconciler) ensureAgentNetwork(ctx context.Context, project *c4v1alpha1.Project) error {
	var providers c4v1alpha1.GitProviderList
	if err := r.List(ctx, &providers, client.InNamespace(project.Namespace)); err != nil {
		return err
	}
	gitNamespaces := gitNamespacesOf(providers.Items)
	egress := fenceEgress(r.ClusterDNS, gitNamespaces)
	if r.ModelProxyURL != "" {
		// A sandbox on the shared token holds no token: the proxy's pods in the
		// release namespace are its only way to Claude with one (decision 6 of
		// docs/plans/done/model-proxy.md). The proxy's own policy admits only
		// sandboxes of namespaces opted in to the shared Secrets. The CI runners'
		// policy has no such rule: a job never talks to Claude.
		tcp := corev1.ProtocolTCP
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
		policy.Spec = fencePolicySpec(map[string]string{LabelAgentSandbox: agentSandboxValue}, egress)
		return controllerutil.SetControllerReference(project, policy, r.Scheme)
	})
	return err
}
