package localenv

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/ports/contract"
)

const toolsImage = "ghcr.io/jairjosafath/theos/agent-base:0.31.0"

func browser(namespace string) ports.Environment {
	return ports.Environment{Namespace: namespace, Name: "browser", UID: "env-uid", Member: model.TestEnvMemberLocal,
		Local: &ports.LocalEnvironment{Image: "ghcr.io/jairjosafath/theos/env-browser:0.31.0",
			Requests: map[string]string{"cpu": "1", "memory": "2Gi"}, Limits: map[string]string{"memory": "4Gi"},
			PullSecrets: []string{"ghcr"}}}
}

// leasePod is the pod of a lease of a browser that asks for KVM on nodes
// tainted for it.
func leasePod(t *testing.T) (ports.LeaseRequest, *corev1.Pod) {
	t.Helper()
	env := browser("apps")
	env.Local.Devices = map[string]string{"devices.kubevirt.io/kvm": "1"}
	env.Local.RuntimeClassName = "kvm"
	env.Local.NodeSelector = map[string]string{"theos.factory.io/kvm": "true"}
	seconds := int64(60)
	env.Local.Tolerations = []ports.Toleration{{Key: "kvm", Operator: "Exists", Effect: "NoExecute", Seconds: &seconds}}
	req := ports.LeaseRequest{ID: model.LeaseID("browser", "add-crop-r1-implement"), Env: env, Holder: "add-crop",
		Token: rand.Text(), Deadline: 45 * time.Minute}
	pod, err := BuildPod(req, toolsImage)
	if err != nil {
		t.Fatal(err)
	}
	return req, pod
}

// The lease pod is fenced like an agent sandbox (T5): no ServiceAccount
// token, no service links, no user Secret or node directory; it stops at
// the lease's deadline, carries the label the project's policy selects, and
// is owned by its TestEnvironment.
func TestBuildPodIsFenced(t *testing.T) {
	_, pod := leasePod(t)
	spec := pod.Spec
	if spec.AutomountServiceAccountToken == nil || *spec.AutomountServiceAccountToken {
		t.Errorf("automountServiceAccountToken %v, want false", spec.AutomountServiceAccountToken)
	}
	if spec.EnableServiceLinks == nil || *spec.EnableServiceLinks {
		t.Errorf("enableServiceLinks %v, want false", spec.EnableServiceLinks)
	}
	if spec.ServiceAccountName != "" || spec.HostNetwork || spec.HostPID || spec.HostIPC {
		t.Errorf("pod has a ServiceAccount %q or the host's namespaces", spec.ServiceAccountName)
	}
	for _, v := range spec.Volumes {
		if v.Projected != nil || v.Secret != nil || v.HostPath != nil {
			t.Errorf("volume %q mounts a token, a Secret or a node directory", v.Name)
		}
	}
	if spec.RestartPolicy != corev1.RestartPolicyNever {
		t.Errorf("restartPolicy %q, want Never", spec.RestartPolicy)
	}
	if spec.ActiveDeadlineSeconds == nil || *spec.ActiveDeadlineSeconds != 2700 {
		t.Errorf("activeDeadlineSeconds %v, want the lease's 2700", spec.ActiveDeadlineSeconds)
	}
	if pod.Labels[LabelComponent] != model.TestEnvComponent || pod.Labels[LabelEnvironment] != "browser" {
		t.Errorf("labels %v, want the component the policy selects and the environment", pod.Labels)
	}
	if len(pod.OwnerReferences) != 1 || pod.OwnerReferences[0].Kind != "TestEnvironment" ||
		pod.OwnerReferences[0].UID != "env-uid" {
		t.Errorf("owners %v, want the TestEnvironment", pod.OwnerReferences)
	}
}

// theos-env comes from the tools image, read-only to the environment, and
// serves on the environment's image, not privileged, with the lease's token
// from its own Secret alone (T4, T5).
func TestBuildPodRunsTheosEnv(t *testing.T) {
	req, pod := leasePod(t)
	spec := pod.Spec
	if len(spec.InitContainers) != 1 || spec.InitContainers[0].Image != toolsImage ||
		!reflect.DeepEqual(spec.InitContainers[0].Command, []string{"/usr/local/bin/theos-env", "install", Binary}) {
		t.Fatalf("init containers %+v, want theos-env copied in from the tools image", spec.InitContainers)
	}
	if len(spec.Containers) != 1 {
		t.Fatalf("containers %d, want the env container", len(spec.Containers))
	}
	c := spec.Containers[0]
	if c.Image != req.Env.Local.Image || c.Command[0] != Binary || c.Command[1] != "serve" {
		t.Errorf("env container %s %v, want theos-env serve on the environment's image", c.Image, c.Command)
	}
	if sc := c.SecurityContext; sc == nil || sc.Privileged == nil || *sc.Privileged || sc.Capabilities != nil {
		t.Errorf("securityContext %+v, want not privileged and no capability added", sc)
	}
	if len(c.Env) != 1 || c.Env[0].Name != EnvToken || c.Env[0].Value != "" || c.Env[0].ValueFrom == nil ||
		c.Env[0].ValueFrom.SecretKeyRef.Name != req.ID || len(c.EnvFrom) != 0 {
		t.Errorf("env %+v from %+v, want only the token, from the lease's own Secret", c.Env, c.EnvFrom)
	}
	if len(c.Ports) != 1 || c.Ports[0].ContainerPort != model.TestEnvPort {
		t.Errorf("ports %v, want theos-env's %d", c.Ports, model.TestEnvPort)
	}
	for _, m := range c.VolumeMounts {
		if m.Name == toolsVolume && !m.ReadOnly {
			t.Error("the env container may write theos-env")
		}
	}
}

// The pod requests and is limited to its devices, beside the member's
// resources, and is placed as the member says (T7).
func TestBuildPodPlacesAndRequestsDevices(t *testing.T) {
	req, pod := leasePod(t)
	spec, local := pod.Spec, req.Env.Local
	if spec.RuntimeClassName == nil || *spec.RuntimeClassName != "kvm" ||
		!reflect.DeepEqual(spec.NodeSelector, local.NodeSelector) ||
		len(spec.Tolerations) != 1 || spec.Tolerations[0].Key != "kvm" || *spec.Tolerations[0].TolerationSeconds != 60 {
		t.Errorf("placement %v %v %v, want the member's", spec.RuntimeClassName, spec.NodeSelector, spec.Tolerations)
	}
	if len(spec.ImagePullSecrets) != 1 || spec.ImagePullSecrets[0].Name != "ghcr" {
		t.Errorf("imagePullSecrets %v, want the shared ghcr", spec.ImagePullSecrets)
	}
	resources := spec.Containers[0].Resources
	kvm := corev1.ResourceName("devices.kubevirt.io/kvm")
	one := resource.MustParse("1")
	if q := resources.Requests[kvm]; q.Cmp(one) != 0 {
		t.Errorf("requests %v, want the device", resources.Requests)
	}
	if q := resources.Limits[kvm]; q.Cmp(one) != 0 {
		t.Errorf("limits %v, want the device", resources.Limits)
	}
	if q := resources.Requests[corev1.ResourceMemory]; q.Cmp(resource.MustParse("2Gi")) != 0 {
		t.Errorf("memory request %v, want the member's 2Gi", q)
	}
	local.Devices = map[string]string{"nvidia.com/gpu": "one"}
	if _, err := BuildPod(req, toolsImage); err == nil {
		t.Error("BuildPod with a quantity that doesn't parse succeeded")
	}
}

// What a lease pod says of its lease.
func TestLeaseOf(t *testing.T) {
	ready := []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}
	now := metav1.Now()
	for _, tt := range []struct {
		name      string
		pod       corev1.Pod
		state     string
		endpoint  string
		inMessage string
	}{
		{name: "just made", state: model.LeasePending, inMessage: "starting"},
		{name: "unschedulable", state: model.LeasePending, inMessage: "not scheduled: 0/1 nodes have kvm",
			pod: corev1.Pod{Status: corev1.PodStatus{Conditions: []corev1.PodCondition{{Type: corev1.PodScheduled,
				Status: corev1.ConditionFalse, Message: "0/1 nodes have kvm"}}}}},
		{name: "pulling in vain", state: model.LeasePending, inMessage: "ImagePullBackOff",
			pod: corev1.Pod{Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{Name: EnvContainer,
				State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ImagePullBackOff"}}}}}}},
		{name: "running, not ready", state: model.LeasePending,
			pod: corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodRunning, PodIP: "10.244.0.7"}}},
		{name: "ready", state: model.LeaseReady, endpoint: "http://10.244.0.7:7070",
			pod: corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodRunning, PodIP: "10.244.0.7", Conditions: ready}}},
		{name: "ready on IPv6", state: model.LeaseReady, endpoint: "http://[fd00::7]:7070",
			pod: corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodRunning, PodIP: "fd00::7", Conditions: ready}}},
		{name: "past its deadline", state: model.LeaseFailed, inMessage: "deadline",
			pod: corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodFailed, Reason: "DeadlineExceeded"}}},
		{name: "theos-env not copied", state: model.LeaseFailed, inMessage: "container theos-env ended with exit code 127",
			pod: corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodFailed, InitContainerStatuses: []corev1.ContainerStatus{{
				Name: installContainer, State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 127}}}}}}},
		{name: "ended", state: model.LeaseEnded, pod: corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodSucceeded}}},
		{name: "being deleted", state: model.LeaseEnded, pod: corev1.Pod{ObjectMeta: metav1.ObjectMeta{DeletionTimestamp: &now},
			Status: corev1.PodStatus{Phase: corev1.PodRunning, PodIP: "10.244.0.7", Conditions: ready}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tt.pod.Name = "browser-0123456789"
			l := leaseOf(&tt.pod)
			if l.State != tt.state || l.Endpoint != tt.endpoint || l.ID != tt.pod.Name {
				t.Errorf("leaseOf = %+v, want %s at %q", l, tt.state, tt.endpoint)
			}
			if tt.inMessage != "" && !strings.Contains(l.Message, tt.inMessage) {
				t.Errorf("message %q, want it to say %q", l.Message, tt.inMessage)
			}
		})
	}
}

// The contract on envtest (T6): a lease's pod is made, a second Lease with
// the same ID makes no second pod, Return deletes it, and Leases finds
// every pod of the environment, one the operator lost track of included.
// envtest runs no kubelet, so the suite's Ready and Fail write the pod's
// status as one would.
func TestTestEnvironmentsContractOnEnvtest(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS not set; run with make test")
	}
	testEnv := &envtest.Environment{}
	cfg, err := testEnv.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = testEnv.Stop() })
	c, err := client.New(cfg, client.Options{Scheme: clientgoscheme.Scheme})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	namespaces := 0
	pods := func(t *testing.T, env ports.Environment) []corev1.Pod {
		t.Helper()
		var list corev1.PodList
		if err := c.List(ctx, &list, client.InNamespace(env.Namespace)); err != nil {
			t.Fatal(err)
		}
		// Return deletes in the background; envtest's pods have no kubelet
		// to wait for and go at once.
		var live []corev1.Pod
		for _, p := range list.Items {
			if p.DeletionTimestamp == nil {
				live = append(live, p)
			}
		}
		return live
	}
	setStatus := func(t *testing.T, env ports.Environment, id string, status corev1.PodStatus) {
		t.Helper()
		var pod corev1.Pod
		if err := c.Get(ctx, client.ObjectKey{Namespace: env.Namespace, Name: id}, &pod); err != nil {
			t.Fatal(err)
		}
		pod.Status = status
		if err := c.Status().Update(ctx, &pod); err != nil {
			t.Fatal(err)
		}
	}
	provider := New(c, toolsImage)
	contract.TestEnvironments(t, contract.TestEnvironmentsEnv{
		Provider: provider,
		Environment: func(t *testing.T) ports.Environment {
			namespaces++
			ns := fmt.Sprintf("apps-%d", namespaces)
			if err := c.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}); err != nil {
				t.Fatal(err)
			}
			return browser(ns)
		},
		Places: func(t *testing.T, env ports.Environment) int { return len(pods(t, env)) },
		Ready: func(t *testing.T, env ports.Environment, id string) {
			setStatus(t, env, id, corev1.PodStatus{Phase: corev1.PodRunning, PodIP: "10.244.0.7",
				Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}})
		},
		Fail: func(t *testing.T, env ports.Environment, id string) {
			setStatus(t, env, id, corev1.PodStatus{Phase: corev1.PodFailed, Reason: "DeadlineExceeded"})
		},
	})

	t.Run("a lease's Secret holds its token, and goes with it", func(t *testing.T) {
		namespaces++
		ns := fmt.Sprintf("apps-%d", namespaces)
		if err := c.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}); err != nil {
			t.Fatal(err)
		}
		token := rand.Text()
		req := ports.LeaseRequest{ID: model.LeaseID("browser", "add-crop-r1-implement"), Env: browser(ns),
			Holder: "add-crop", Token: token, Deadline: time.Hour}
		if _, err := provider.Lease(ctx, req); err != nil {
			t.Fatal(err)
		}
		var secret corev1.Secret
		if err := c.Get(ctx, client.ObjectKey{Namespace: ns, Name: req.ID}, &secret); err != nil {
			t.Fatal(err)
		}
		if string(secret.Data[TokenKey]) != token {
			t.Error("the lease's Secret doesn't hold its token")
		}
		// An operator that restarted lost its memory: a new provider finds
		// the lease by its environment.
		found, err := New(c, toolsImage).Leases(ctx, req.Env)
		if err != nil || len(found) != 1 || found[0].ID != req.ID {
			t.Errorf("Leases after a restart = %v, %v; want the lease", found, err)
		}
		if err := provider.Return(ctx, req.Env, req.ID); err != nil {
			t.Fatal(err)
		}
		if err := c.Get(ctx, client.ObjectKey{Namespace: ns, Name: req.ID}, &secret); err == nil {
			t.Error("the lease's Secret outlived its Return")
		}
	})
}
