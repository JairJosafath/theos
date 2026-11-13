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
	"context"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
)

// labelComponent is app.kubernetes.io/component, model.TestEnvComponent on
// a local test environment's lease pods.
const labelComponent = "app.kubernetes.io/component"

// ensureTestEnvironmentNetwork fences the project's local test environment
// pods, which run the agents' code (docs/plans/done/test-environments.md, T5).
func (r *ProjectReconciler) ensureTestEnvironmentNetwork(ctx context.Context, project *c4v1beta1.Project) error {
	policy := &networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{
		Name: model.TestEnvNetworkPolicy, Namespace: project.Namespace}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, policy, func() error {
		policy.Labels = projectLabels(project.Namespace)
		policy.Spec = testEnvPolicySpec(r.ClusterDNS)
		return controllerutil.SetControllerReference(project, policy, r.Scheme)
	})
	return err
}

// testEnvPolicySpec lets in only the project's own agent sandboxes, on
// theos-env's port, and lets out only cluster DNS and public HTTPS: an
// environment gets its tree from the sandbox, so it needs neither the Git
// host nor the model. CI runs on an environment's image itself (T8), so no
// CI pod calls one. Each lease's own token keeps one Task's run from
// another's lease, so the policy stays one per project.
func testEnvPolicySpec(dns []networkingv1.NetworkPolicyPeer) networkingv1.NetworkPolicySpec {
	spec := fencePolicySpec(map[string]string{labelComponent: model.TestEnvComponent}, fenceEgress(dns, nil))
	tcp := corev1.ProtocolTCP
	port := intstr.FromInt32(model.TestEnvPort)
	spec.Ingress = []networkingv1.NetworkPolicyIngressRule{{
		From: []networkingv1.NetworkPolicyPeer{{PodSelector: &metav1.LabelSelector{
			MatchLabels: map[string]string{LabelAgentSandbox: agentSandboxValue}}}},
		Ports: []networkingv1.NetworkPolicyPort{{Protocol: &tcp, Port: &port}},
	}}
	return spec
}
