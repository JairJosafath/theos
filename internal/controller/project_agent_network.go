/*
Copyright 2026.

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

// privateRanges are the addresses agents can't reach over HTTPS: the cluster's
// pods, services and nodes, and anything else on a private network.
var privateRanges = []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "169.254.0.0/16"}

// ensureAgentNetwork fences agent sandboxes in the project: no traffic in, and
// out only DNS, the in-cluster Git hosts of the project's GitProviders, and HTTPS
// to public addresses (the model API, package registries). The Kubernetes API and
// every other in-cluster service stay out of reach.
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
			To:    []networkingv1.NetworkPolicyPeer{{IPBlock: &networkingv1.IPBlock{CIDR: "0.0.0.0/0", Except: privateRanges}}},
			Ports: []networkingv1.NetworkPolicyPort{{Protocol: &tcp, Port: &https}},
		},
	}
	for _, ns := range gitNamespaces {
		egress = append(egress, networkingv1.NetworkPolicyEgressRule{To: []networkingv1.NetworkPolicyPeer{{
			NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{corev1.LabelMetadataName: ns}},
		}}})
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
