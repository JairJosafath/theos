package controller

import (
	"context"
	"reflect"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/diff"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	c4v1beta1 "github.com/JairJosafath/c4factory/api/v1beta1"
	"github.com/JairJosafath/c4factory/internal/model"
)

// These tests check what only a real node used to: that an agent's pod can't
// reach the Kubernetes API, stops at its deadline, and is the pod the fence
// selects, and that the fence lets out only what design.md says.

func fenceScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{
		corev1.AddToScheme, networkingv1.AddToScheme, c4v1beta1.AddToScheme,
	} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	return scheme
}

func TestBuildSandboxPodHasNoClusterAccess(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		name         string
		minutes      int32
		wantDeadline int64
	}{
		{name: "the Agent's timeout", minutes: 10, wantDeadline: 600},
		{name: "45 minutes when the Agent sets none", wantDeadline: 2700},
	} {
		t.Run(tt.name, func(t *testing.T) {
			sandbox, err := buildSandbox(sandboxRun{
				task:      &c4v1beta1.Task{ObjectMeta: objectMeta("apps", "add-crop")},
				status:    &c4v1beta1.TaskStatus{Round: 1},
				container: &c4v1beta1.Container{ObjectMeta: objectMeta("apps", "image-shrink")},
				agent: &c4v1beta1.Agent{ObjectMeta: objectMeta("apps", "claude"),
					Spec: c4v1beta1.AgentSpec{TimeoutMinutes: tt.minutes}},
				name: "add-crop-r1-implement", role: model.RoleImplementer,
			}, fenceScheme(t), now)
			if err != nil {
				t.Fatal(err)
			}
			pod := podOf(t, sandbox)
			if pod.AutomountServiceAccountToken == nil || *pod.AutomountServiceAccountToken {
				t.Errorf("automountServiceAccountToken %v, want false", pod.AutomountServiceAccountToken)
			}
			if pod.EnableServiceLinks == nil || *pod.EnableServiceLinks {
				t.Errorf("enableServiceLinks %v, want false", pod.EnableServiceLinks)
			}
			if pod.ServiceAccountName != "" {
				t.Errorf("serviceAccountName %q, want the namespace's default", pod.ServiceAccountName)
			}
			for _, v := range pod.Volumes {
				if v.Projected != nil || v.Secret != nil {
					t.Errorf("volume %q mounts a token or Secret", v.Name)
				}
			}
			if pod.RestartPolicy != corev1.RestartPolicyNever {
				t.Errorf("restartPolicy %q, want Never", pod.RestartPolicy)
			}
			if pod.ActiveDeadlineSeconds == nil || *pod.ActiveDeadlineSeconds != tt.wantDeadline {
				t.Errorf("activeDeadlineSeconds %v, want %d", pod.ActiveDeadlineSeconds, tt.wantDeadline)
			}
			spec := sandbox.Object["spec"].(map[string]any)
			wantShutdown := now.Add(time.Duration(tt.wantDeadline)*time.Second + sandboxGrace).Format(time.RFC3339)
			if spec["shutdownTime"] != wantShutdown || spec["shutdownPolicy"] != "Retain" {
				t.Errorf("shutdown %v %v, want %s Retain", spec["shutdownTime"], spec["shutdownPolicy"], wantShutdown)
			}
		})
	}
}

func TestAgentNetworkFence(t *testing.T) {
	scheme := fenceScheme(t)
	project := &c4v1beta1.Project{ObjectMeta: metav1.ObjectMeta{Namespace: "apps", Name: "apps", UID: "project-1"}}
	provider := func(name, url string) *c4v1beta1.GitProvider {
		return &c4v1beta1.GitProvider{ObjectMeta: objectMeta("apps", name), Spec: c4v1beta1.GitProviderSpec{URL: url}}
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(project,
		provider("gitea", "http://gitea-http.gitea.svc.cluster.local:3000"),
		provider("gitea-again", "http://gitea.gitea.svc:3000"),
		provider("forgejo", "http://forgejo.forge.svc.cluster.local"),
		provider("github", "https://github.com"),
	).Build()
	fence := func(t *testing.T, r *ProjectReconciler) networkingv1.NetworkPolicy {
		t.Helper()
		r.Client, r.Scheme = c, scheme
		if err := r.ensureAgentNetwork(context.Background(), project); err != nil {
			t.Fatal(err)
		}
		var policy networkingv1.NetworkPolicy
		if err := c.Get(context.Background(), client.ObjectKey{Namespace: "apps", Name: AgentNetworkPolicyName}, &policy); err != nil {
			t.Fatal(err)
		}
		return policy
	}
	policy := fence(t, &ProjectReconciler{})

	tcp, udp := corev1.ProtocolTCP, corev1.ProtocolUDP
	dns, https := intstr.FromInt32(53), intstr.FromInt32(443)
	namespace := func(ns string) *metav1.LabelSelector {
		return &metav1.LabelSelector{MatchLabels: map[string]string{"kubernetes.io/metadata.name": ns}}
	}
	want := networkingv1.NetworkPolicySpec{
		PodSelector: metav1.LabelSelector{MatchLabels: map[string]string{"c4.factory.io/agent-sandbox": "true"}},
		// Both types, so the empty ingress list denies all traffic in.
		PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress, networkingv1.PolicyTypeEgress},
		Egress: []networkingv1.NetworkPolicyEgressRule{
			{
				To: []networkingv1.NetworkPolicyPeer{{
					NamespaceSelector: namespace("kube-system"),
					PodSelector:       &metav1.LabelSelector{MatchLabels: map[string]string{"k8s-app": "kube-dns"}},
				}},
				Ports: []networkingv1.NetworkPolicyPort{{Protocol: &udp, Port: &dns}, {Protocol: &tcp, Port: &dns}},
			},
			{
				// The Kubernetes API, pods, services and nodes all sit in these ranges.
				To: []networkingv1.NetworkPolicyPeer{{IPBlock: &networkingv1.IPBlock{CIDR: "0.0.0.0/0",
					Except: []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "169.254.0.0/16"}}}},
				Ports: []networkingv1.NetworkPolicyPort{{Protocol: &tcp, Port: &https}},
			},
			{To: []networkingv1.NetworkPolicyPeer{{NamespaceSelector: namespace("forge")}}},
			{To: []networkingv1.NetworkPolicyPeer{{NamespaceSelector: namespace("gitea")}}},
		},
	}
	if !reflect.DeepEqual(policy.Spec, want) {
		t.Errorf("fence spec differs (-got +want):\n%s", diff.Diff(policy.Spec, want))
	}
	if len(policy.OwnerReferences) != 1 || policy.OwnerReferences[0].UID != project.UID {
		t.Errorf("owners %v, want the Project", policy.OwnerReferences)
	}

	// On a cluster whose pods resolve through NodeLocal DNSCache, or through
	// DNS in another namespace, --cluster-dns names the peers; only the DNS
	// rule changes (#1257).
	peers, err := ClusterDNSPeers([]string{"169.254.20.10/32", "openshift-dns/dns.operator.openshift.io/daemonset-dns=default"})
	if err != nil {
		t.Fatal(err)
	}
	elsewhere := fence(t, &ProjectReconciler{ClusterDNS: peers})
	wantDNS := []networkingv1.NetworkPolicyPeer{
		{IPBlock: &networkingv1.IPBlock{CIDR: "169.254.20.10/32"}},
		{NamespaceSelector: namespace("openshift-dns"), PodSelector: &metav1.LabelSelector{
			MatchLabels: map[string]string{"dns.operator.openshift.io/daemonset-dns": "default"}}},
	}
	if !reflect.DeepEqual(elsewhere.Spec.Egress[0].To, wantDNS) {
		t.Errorf("DNS peers with --cluster-dns differ (-got +want):\n%s", diff.Diff(elsewhere.Spec.Egress[0].To, wantDNS))
	}
	if !reflect.DeepEqual(elsewhere.Spec.Egress[0].Ports, want.Egress[0].Ports) ||
		!reflect.DeepEqual(elsewhere.Spec.Egress[1:], want.Egress[1:]) {
		t.Errorf("rules beside DNS changed with --cluster-dns (-got +want):\n%s", diff.Diff(elsewhere.Spec.Egress, want.Egress))
	}

	// With a model proxy, one more rule: its pods in the release namespace, on
	// its port, and nothing else of that namespace (#1136).
	proxied := fence(t, &ProjectReconciler{SourceNamespace: "c4-factory-system",
		ModelProxyURL: "http://c4-model-proxy.c4-factory-system.svc:8080"})
	proxyPort := intstr.FromInt32(8080)
	want.Egress = append(want.Egress, networkingv1.NetworkPolicyEgressRule{
		To: []networkingv1.NetworkPolicyPeer{{
			NamespaceSelector: namespace("c4-factory-system"),
			PodSelector:       &metav1.LabelSelector{MatchLabels: map[string]string{"app.kubernetes.io/name": "c4-model-proxy"}},
		}},
		Ports: []networkingv1.NetworkPolicyPort{{Protocol: &tcp, Port: &proxyPort}},
	})
	if !reflect.DeepEqual(proxied.Spec, want) {
		t.Errorf("fence spec with a model proxy differs (-got +want):\n%s", diff.Diff(proxied.Spec, want))
	}

	// Every sandbox pod the Task controller starts, implement or plan, is one the
	// fence selects.
	selector, err := metav1.LabelSelectorAsSelector(&policy.Spec.PodSelector)
	if err != nil {
		t.Fatal(err)
	}
	for _, run := range []sandboxRun{
		{container: &c4v1beta1.Container{ObjectMeta: objectMeta("apps", "image-shrink")}, role: model.RoleImplementer},
		{brief: "cel-lens", role: model.RolePlanner},
	} {
		run.task = &c4v1beta1.Task{ObjectMeta: objectMeta("apps", "add-crop")}
		run.status = &c4v1beta1.TaskStatus{Round: 1}
		run.agent = &c4v1beta1.Agent{ObjectMeta: objectMeta("apps", "claude")}
		run.name = "add-crop-r1"
		sandbox, err := buildSandbox(run, scheme, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		meta := sandbox.Object["spec"].(map[string]any)["podTemplate"].(map[string]any)["metadata"].(map[string]any)
		podLabels := labels.Set{}
		for k, v := range meta["labels"].(map[string]any) {
			podLabels[k] = v.(string)
		}
		if !selector.Matches(podLabels) {
			t.Errorf("%s pod labels %v, not selected by the fence %v", run.role, podLabels, selector)
		}
	}
}
