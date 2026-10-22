/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"context"

	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
)

// AgentNetworkPolicyName is the NetworkPolicy that fences agent sandboxes.
const AgentNetworkPolicyName = "c4-agent-sandbox"

// ensureAgentNetwork fences agent sandboxes in the project: no traffic in, and
// out only what fenceEgress allows, the in-cluster Git hosts being those of the
// project's GitProviders.
func (r *ProjectReconciler) ensureAgentNetwork(ctx context.Context, project *c4v1alpha1.Project) error {
	var providers c4v1alpha1.GitProviderList
	if err := r.List(ctx, &providers, client.InNamespace(project.Namespace)); err != nil {
		return err
	}
	gitNamespaces := gitNamespacesOf(providers.Items)

	policy := &networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{
		Name: AgentNetworkPolicyName, Namespace: project.Namespace}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, policy, func() error {
		policy.Labels = projectLabels(project.Namespace)
		policy.Spec = fencePolicySpec(map[string]string{LabelAgentSandbox: agentSandboxValue}, gitNamespaces)
		return controllerutil.SetControllerReference(project, policy, r.Scheme)
	})
	return err
}
