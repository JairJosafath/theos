package main

import (
	"slices"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// fenced is a runner as the chart renders it, with Gitea, which reads the
// registration token as GITEA_RUNNER_REGISTRATION_TOKEN, and a ConfigMap
// and a comment the check passes over.
const fenced = `
# Source: c4-factory/templates/gitea/deployment.yaml
apiVersion: apps/v1
kind: Deployment
metadata: {name: gitea, namespace: gitea}
spec:
  template:
    metadata:
      labels: {app.kubernetes.io/name: gitea, app.kubernetes.io/instance: c4-factory}
    spec:
      containers:
        - name: gitea
          env:
            - name: GITEA_RUNNER_REGISTRATION_TOKEN
              valueFrom: {secretKeyRef: {name: gitea-runner, key: token}}
---
# Source: c4-factory/templates/gitea/runners.yaml
apiVersion: v1
kind: ConfigMap
metadata: {name: gitea-runner-go-env, namespace: gitea}
---
apiVersion: apps/v1
kind: Deployment
metadata: {name: gitea-runner-go, namespace: gitea}
spec:
  template:
    metadata:
      labels: {app.kubernetes.io/name: gitea-runner-go, app.kubernetes.io/component: ci-runner}
    spec:
      automountServiceAccountToken: false
      initContainers:
        - name: register
          command: [c4-runner-register]
          env:
            - name: RUNNER_TOKEN
              valueFrom: {secretKeyRef: {name: gitea-runner, key: token}}
          volumeMounts: [{name: data, mountPath: /data}]
        - name: go-cache
      containers:
        - name: runner
          volumeMounts: [{name: data, mountPath: /data}]
      volumes:
        - {name: data, emptyDir: {}}
---
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata: {name: ci-runner, namespace: gitea}
spec:
  podSelector:
    matchLabels: {app.kubernetes.io/component: ci-runner}
  policyTypes: [Ingress, Egress]
  egress:
    - to:
        - namespaceSelector:
            matchLabels: {kubernetes.io/metadata.name: kube-system}
          podSelector:
            matchLabels: {k8s-app: kube-dns}
      ports:
        - {protocol: UDP, port: 53}
        - {protocol: TCP, port: 53}
    - to:
        - podSelector:
            matchLabels: {app.kubernetes.io/name: gitea, app.kubernetes.io/instance: c4-factory}
      ports:
        - {protocol: TCP, port: 3000}
    - to:
        - ipBlock:
            cidr: 0.0.0.0/0
            except: [10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 169.254.0.0/16]
      ports:
        - {protocol: TCP, port: 443}
`

func runner(o *objects) *appsv1.Deployment {
	i := slices.IndexFunc(o.deployments, func(d appsv1.Deployment) bool { return d.Name == "gitea-runner-go" })
	return &o.deployments[i]
}

func runnerContainer(o *objects) *corev1.Container {
	return &runner(o).Spec.Template.Spec.Containers[0]
}

func fence(o *objects) *networkingv1.NetworkPolicySpec { return &o.policies[0].Spec }

func tcp(port int32) networkingv1.NetworkPolicyPort {
	protocol, p := corev1.ProtocolTCP, intstr.FromInt32(port)
	return networkingv1.NetworkPolicyPort{Protocol: &protocol, Port: &p}
}

func TestCheck(t *testing.T) {
	tokenEnv := corev1.EnvVar{Name: "RUNNER_TOKEN", ValueFrom: &corev1.EnvVarSource{
		SecretKeyRef: &corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: "gitea-runner"}, Key: "token"}}}
	yes, always := true, corev1.ContainerRestartPolicyAlways
	tests := []struct {
		name   string
		change func(o *objects)
		want   string // a problem says it; empty: no problem
	}{
		{"the chart's runner is fenced", func(*objects) {}, ""},
		{"the token in the runner container (#1138)", func(o *objects) {
			runnerContainer(o).Env = append(runnerContainer(o).Env, tokenEnv)
		}, "container runner, where jobs run, can read the registration token"},
		{"the token under another name", func(o *objects) {
			e := tokenEnv
			e.Name = "TOKEN"
			runnerContainer(o).Env = append(runnerContainer(o).Env, e)
		}, "container runner, where jobs run, can read the registration token"},
		{"the token's Secret as the runner's environment", func(o *objects) {
			runnerContainer(o).EnvFrom = []corev1.EnvFromSource{{SecretRef: &corev1.SecretEnvSource{
				LocalObjectReference: corev1.LocalObjectReference{Name: "gitea-runner"}}}}
		}, "container runner, where jobs run, can read the registration token"},
		{"the token's Secret mounted in the runner", func(o *objects) {
			spec := &runner(o).Spec.Template.Spec
			spec.Volumes = append(spec.Volumes, corev1.Volume{Name: "token", VolumeSource: corev1.VolumeSource{
				Projected: &corev1.ProjectedVolumeSource{Sources: []corev1.VolumeProjection{{Secret: &corev1.SecretProjection{
					LocalObjectReference: corev1.LocalObjectReference{Name: "gitea-runner"}}}}}}})
			runnerContainer(o).VolumeMounts = append(runnerContainer(o).VolumeMounts,
				corev1.VolumeMount{Name: "token", MountPath: "/token"})
		}, "container runner, where jobs run, can read the registration token"},
		{"no init container registers", func(o *objects) {
			runner(o).Spec.Template.Spec.InitContainers = nil
		}, "no init container registers the runner with RUNNER_TOKEN"},
		{"the registering init container as a sidecar beside the jobs", func(o *objects) {
			runner(o).Spec.Template.Spec.InitContainers[0].RestartPolicy = &always
		}, "sidecar register, which runs beside the jobs, can read the registration token"},
		{"a shared process namespace", func(o *objects) {
			runner(o).Spec.Template.Spec.ShareProcessNamespace = &yes
		}, "its containers share a process namespace"},
		{"the node's network", func(o *objects) {
			runner(o).Spec.Template.Spec.HostNetwork = true
		}, "its pods use the node's network, which no NetworkPolicy fences"},
		{"a ServiceAccount token", func(o *objects) {
			runner(o).Spec.Template.Spec.AutomountServiceAccountToken = nil
		}, "its pods mount a ServiceAccount token"},
		{"a runner the fence doesn't select", func(o *objects) {
			delete(runner(o).Spec.Template.Labels, componentLabel)
		}, "Deployment gitea/gitea-runner-go registers a runner, but its pods lack label " +
			"app.kubernetes.io/component: ci-runner"},
		{"no runner at all", func(o *objects) {
			o.deployments = o.deployments[:1]
		}, "No runner Deployment"},
		{"no Gitea to check the rule against", func(o *objects) {
			o.deployments = o.deployments[1:]
		}, "no Deployment gitea/gitea to check the fence's Gitea rule against"},
		{"no NetworkPolicy", func(o *objects) {
			o.policies = nil
		}, "no NetworkPolicy in namespace gitea selects its pods"},
		{"the NetworkPolicy in another namespace", func(o *objects) {
			o.policies[0].Namespace = "default"
		}, "no NetworkPolicy in namespace gitea selects its pods"},
		{"the NetworkPolicy selects other pods", func(o *objects) {
			fence(o).PodSelector.MatchLabels = map[string]string{componentLabel: "other"}
		}, "no NetworkPolicy in namespace gitea selects its pods"},
		{"egress fenced, ingress open", func(o *objects) {
			fence(o).PolicyTypes = []networkingv1.PolicyType{networkingv1.PolicyTypeEgress}
		}, "no NetworkPolicy selects its pods for both Ingress and Egress"},
		{"policy types left to the default, with egress rules", func(o *objects) {
			fence(o).PolicyTypes = nil
		}, ""},
		{"an ingress rule", func(o *objects) {
			fence(o).Ingress = []networkingv1.NetworkPolicyIngressRule{{}}
		}, "NetworkPolicy gitea/ci-runner lets traffic into the runner pods"},
		{"egress anywhere", func(o *objects) {
			fence(o).Egress = append(fence(o).Egress, networkingv1.NetworkPolicyEgressRule{})
		}, "egress rule 4 reaches more than DNS, Gitea on port 3000 and HTTPS to public addresses"},
		{"another policy that opens egress", func(o *objects) {
			o.policies = append(o.policies, networkingv1.NetworkPolicy{
				ObjectMeta: metav1.ObjectMeta{Name: "allow-all", Namespace: "gitea"},
				Spec: networkingv1.NetworkPolicySpec{PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeEgress},
					Egress: []networkingv1.NetworkPolicyEgressRule{{}}},
			})
		}, "NetworkPolicy gitea/allow-all: egress rule 1 reaches more than"},
		{"DNS only in a policy that doesn't fence egress, which the API server ignores", func(o *objects) {
			o.policies = append(o.policies, networkingv1.NetworkPolicy{
				ObjectMeta: metav1.ObjectMeta{Name: "dns", Namespace: "gitea"},
				Spec: networkingv1.NetworkPolicySpec{PodSelector: fence(o).PodSelector,
					PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
					Egress:      fence(o).Egress[:1]},
			})
			fence(o).Egress = fence(o).Egress[1:]
		}, "no NetworkPolicy lets its pods reach DNS, which a job needs"},
		{"no HTTPS rule", func(o *objects) {
			fence(o).Egress = fence(o).Egress[:2]
		}, "no NetworkPolicy lets its pods reach HTTPS to public addresses, which a job needs"},
		{"HTTPS to the cluster network", func(o *objects) {
			fence(o).Egress[2].To[0].IPBlock.Except = []string{"172.16.0.0/12", "192.168.0.0/16", "169.254.0.0/16"}
		}, "egress rule 3 reaches the private range 10.0.0.0/8 over HTTPS"},
		{"plain HTTP to public addresses", func(o *objects) {
			fence(o).Egress[2].Ports = append(fence(o).Egress[2].Ports, tcp(80))
		}, "egress rule 3 reaches more than"},
		{"every port of public addresses", func(o *objects) {
			fence(o).Egress[2].Ports = nil
		}, "egress rule 3 reaches more than"},
		{"DNS in every namespace", func(o *objects) {
			fence(o).Egress[0].To[0].NamespaceSelector = &metav1.LabelSelector{}
		}, "egress rule 1 reaches more than"},
		{"Gitea's SSH port too", func(o *objects) {
			fence(o).Egress[1].Ports = append(fence(o).Egress[1].Ports, tcp(22))
		}, "egress rule 2 reaches more than"},
		{"every pod in Gitea's namespace", func(o *objects) {
			fence(o).Egress[1].To[0].PodSelector = &metav1.LabelSelector{}
		}, "egress rule 2 reaches more than"},
		{"Gitea's pods under labels they don't have", func(o *objects) {
			fence(o).Egress[1].To[0].PodSelector.MatchLabels["app.kubernetes.io/instance"] = "other"
		}, "egress rule 2 doesn't select the Gitea pods"},
		{"the port's protocol left to the default, TCP", func(o *objects) {
			fence(o).Egress[1].Ports[0].Protocol = nil
		}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var o objects
			if err := decode(strings.NewReader(fenced), &o); err != nil {
				t.Fatal(err)
			}
			tt.change(&o)
			problems := check(o)
			if tt.want == "" {
				if len(problems) > 0 {
					t.Fatalf("want no problem, got %q", problems)
				}
				return
			}
			if !slices.ContainsFunc(problems, func(p string) bool { return strings.Contains(p, tt.want) }) {
				t.Fatalf("want a problem saying %q, got %q", tt.want, problems)
			}
		})
	}
}

func TestDecode(t *testing.T) {
	var o objects
	if err := decode(strings.NewReader(fenced), &o); err != nil {
		t.Fatal(err)
	}
	if got := runnerNames(o); !slices.Equal(got, []string{"gitea/gitea-runner-go"}) {
		t.Errorf("runners %q", got)
	}
	if len(o.deployments) != 2 || len(o.policies) != 1 {
		t.Errorf("%d Deployments and %d NetworkPolicies, want 2 and 1", len(o.deployments), len(o.policies))
	}
}
