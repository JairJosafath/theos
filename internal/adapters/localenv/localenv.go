// Package localenv is the local member of a TestEnvironment: one pod per
// lease in the environment's namespace, built from the environment's image
// and fenced like an agent sandbox, with theos-env copied in from the tools
// image by an init container and serving on model.TestEnvPort
// (docs/plans/test-environments.md, T4, T5 and T7). A pod, not a
// Deployment: each lease is used once and thrown away, as a CI job's pod is,
// so no lease sees another's leftovers. A local lease costs nothing.
package localenv

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

// Labels and annotations on a lease's pod and Secret.
const (
	// LabelComponent is app.kubernetes.io/component, model.TestEnvComponent
	// on every lease pod: the project's theos-test-environment policy
	// selects it.
	LabelComponent = "app.kubernetes.io/component"
	// LabelEnvironment names the TestEnvironment a lease is of; Leases lists
	// by it.
	LabelEnvironment = "theos.factory.io/test-environment"
	// LabelProject is the project's label, as on every object the factory
	// makes in a project's namespace.
	LabelProject = "theos.factory.io/project"
	// AnnotationHolder names the Task whose run holds the lease.
	AnnotationHolder = "theos.factory.io/lease-holder"
)

// The pod's parts.
const (
	// EnvContainer runs theos-env serve on the environment's image.
	EnvContainer = "env"
	// installContainer copies theos-env from the tools image.
	installContainer = "theos-env"
	// toolsVolume holds the copy, at toolsMount.
	toolsVolume = "theos-env"
	toolsMount  = "/theos-env"
	// Binary is where theos-env is in the env container, and toolsBinary
	// where the tools image has it.
	Binary      = toolsMount + "/bin/theos-env"
	toolsBinary = "/usr/local/bin/theos-env"
	// WorkDir is where each run's tree goes, replaced on each run.
	WorkDir = "/work"
	// TokenKey is the key of the lease's token in its Secret, which the env
	// container reads as EnvToken.
	TokenKey = "token"
	EnvToken = "THEOS_ENV_TOKEN" //nolint:gosec // an environment variable's name
)

// ownerAPIVersion and ownerKind are the TestEnvironment's, the owner of
// every lease pod and Secret, so deleting the environment deletes its leases.
const (
	ownerAPIVersion = "theos.factory.io/v1beta1"
	ownerKind       = "TestEnvironment"
)

// Provider leases local environments with a Kubernetes client: the
// operator's.
type Provider struct {
	c client.Client
	// toolsImage is the image that holds theos-env at toolsBinary: the
	// release's theos-agent image.
	toolsImage string
}

var _ ports.TestEnvironments = (*Provider)(nil)

// New returns the local provider on c, copying theos-env from toolsImage.
func New(c client.Client, toolsImage string) *Provider {
	return &Provider{c: c, toolsImage: toolsImage}
}

// Lease makes the lease's Secret and pod, or returns the pod made for the
// request's ID before.
func (p *Provider) Lease(ctx context.Context, req ports.LeaseRequest) (ports.Lease, error) {
	env := req.Env
	if env.Local == nil {
		return ports.Lease{}, fmt.Errorf("test environment %s/%s has no local member", env.Namespace, env.Name)
	}
	var pod corev1.Pod
	err := p.c.Get(ctx, client.ObjectKey{Namespace: env.Namespace, Name: req.ID}, &pod)
	if err == nil {
		return leaseOf(&pod), nil
	}
	if !apierrors.IsNotFound(err) {
		return ports.Lease{}, err
	}
	if p.toolsImage == "" {
		return ports.Lease{}, errors.New("no image holds theos-env: set the operator's --test-environment-tools-image")
	}
	built, err := BuildPod(req, p.toolsImage)
	if err != nil {
		return ports.Lease{}, err
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: req.ID, Namespace: env.Namespace, Labels: built.Labels,
			OwnerReferences: built.OwnerReferences},
		Data: map[string][]byte{TokenKey: []byte(req.Token)},
	}
	if err := p.c.Create(ctx, secret); apierrors.IsAlreadyExists(err) {
		// Left by a lease whose Return stopped half way: this lease's token
		// replaces its. The operator caches no Secrets, so it patches what
		// it doesn't read.
		if err := p.c.Patch(ctx, secret, client.Merge); err != nil {
			return ports.Lease{}, err
		}
	} else if err != nil {
		return ports.Lease{}, err
	}
	if err := p.c.Create(ctx, built); err != nil && !apierrors.IsAlreadyExists(err) {
		return ports.Lease{}, err
	}
	return leaseOf(built), nil
}

// Get reads the lease's pod.
func (p *Provider) Get(ctx context.Context, env ports.Environment, id string) (ports.Lease, error) {
	var pod corev1.Pod
	err := p.c.Get(ctx, client.ObjectKey{Namespace: env.Namespace, Name: id}, &pod)
	if apierrors.IsNotFound(err) || err == nil && pod.Labels[LabelEnvironment] != env.Name {
		return ports.Lease{}, ports.ErrLeaseNotFound
	}
	if err != nil {
		return ports.Lease{}, err
	}
	return leaseOf(&pod), nil
}

// Return deletes the lease's pod and Secret.
func (p *Provider) Return(ctx context.Context, env ports.Environment, id string) error {
	meta := metav1.ObjectMeta{Namespace: env.Namespace, Name: id}
	pod := &corev1.Pod{ObjectMeta: meta}
	if err := p.c.Delete(ctx, pod, client.PropagationPolicy(metav1.DeletePropagationBackground)); err != nil &&
		!apierrors.IsNotFound(err) {
		return err
	}
	if err := p.c.Delete(ctx, &corev1.Secret{ObjectMeta: meta}); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}

// Leases lists the environment's lease pods.
func (p *Provider) Leases(ctx context.Context, env ports.Environment) ([]ports.Lease, error) {
	var pods corev1.PodList
	if err := p.c.List(ctx, &pods, client.InNamespace(env.Namespace),
		client.MatchingLabels{LabelEnvironment: env.Name, LabelComponent: model.TestEnvComponent}); err != nil {
		return nil, err
	}
	out := make([]ports.Lease, 0, len(pods.Items))
	for i := range pods.Items {
		out = append(out, leaseOf(&pods.Items[i]))
	}
	slices.SortFunc(out, func(a, b ports.Lease) int { return strings.Compare(a.ID, b.ID) })
	return out, nil
}

// leaseOf is what a lease pod says of its lease: Ready once theos-env
// answers its readiness probe, at the pod's address; Failed once the pod
// failed; Ended once it ended or is being deleted.
func leaseOf(pod *corev1.Pod) ports.Lease {
	l := ports.Lease{ID: pod.Name, Holder: pod.Annotations[AnnotationHolder], Started: pod.CreationTimestamp.Time,
		State: model.LeasePending}
	switch {
	case pod.DeletionTimestamp != nil:
		l.State, l.Message = model.LeaseEnded, "the pod is being deleted"
	case pod.Status.Phase == corev1.PodSucceeded:
		l.State, l.Message = model.LeaseEnded, "the pod ended"
	case pod.Status.Phase == corev1.PodFailed:
		l.State, l.Message = model.LeaseFailed, failure(pod)
	case pod.Status.Phase == corev1.PodRunning && podReady(pod) && pod.Status.PodIP != "":
		l.State = model.LeaseReady
		l.Endpoint = "http://" + endpointHost(pod.Status.PodIP) + ":" + strconv.Itoa(model.TestEnvPort)
	default:
		l.Message = pending(pod)
	}
	return l
}

// endpointHost is ip as a URL's host: an IPv6 address in brackets.
func endpointHost(ip string) string {
	if strings.Contains(ip, ":") {
		return "[" + ip + "]"
	}
	return ip
}

func podReady(pod *corev1.Pod) bool {
	for _, c := range pod.Status.Conditions {
		if c.Type == corev1.PodReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

// failure says why a pod failed: its deadline, a container's end, or its
// status.
func failure(pod *corev1.Pod) string {
	if pod.Status.Reason == "DeadlineExceeded" {
		return "the lease passed its deadline"
	}
	for _, cs := range slices.Concat(pod.Status.InitContainerStatuses, pod.Status.ContainerStatuses) {
		if term := cs.State.Terminated; term != nil && term.ExitCode != 0 {
			msg := fmt.Sprintf("container %s ended with exit code %d", cs.Name, term.ExitCode)
			if term.Reason != "" {
				msg += " (" + term.Reason + ")"
			}
			return msg
		}
	}
	return strings.TrimSpace("the pod failed: " + pod.Status.Reason + " " + pod.Status.Message)
}

// pending says what a pod that isn't ready yet waits for.
func pending(pod *corev1.Pod) string {
	for _, cs := range slices.Concat(pod.Status.InitContainerStatuses, pod.Status.ContainerStatuses) {
		if w := cs.State.Waiting; w != nil && w.Reason != "" && w.Reason != "PodInitializing" &&
			w.Reason != "ContainerCreating" {
			return strings.TrimSpace(fmt.Sprintf("container %s waits: %s %s", cs.Name, w.Reason, w.Message))
		}
	}
	for _, c := range pod.Status.Conditions {
		if c.Type == corev1.PodScheduled && c.Status == corev1.ConditionFalse {
			return strings.TrimSpace("the pod is not scheduled: " + c.Message)
		}
	}
	return "the pod is starting"
}

// BuildPod is the pod of one lease, a pure function of the request and the
// tools image: no ServiceAccount token, no service links, not privileged,
// the devices requested and limited, the lease's deadline as the pod's, and
// the token only through the lease's own Secret (T5, T7).
func BuildPod(req ports.LeaseRequest, toolsImage string) (*corev1.Pod, error) {
	env, local := req.Env, req.Env.Local
	resources, err := resourcesOf(local)
	if err != nil {
		return nil, fmt.Errorf("test environment %s/%s: %w", env.Namespace, env.Name, err)
	}
	deadline := int64(req.Deadline.Seconds())
	if deadline <= 0 {
		deadline = int64(model.LeaseDeadline(0).Seconds())
	}
	pullSecrets := make([]corev1.LocalObjectReference, 0, len(local.PullSecrets))
	for _, name := range local.PullSecrets {
		pullSecrets = append(pullSecrets, corev1.LocalObjectReference{Name: name})
	}
	tolerations := make([]corev1.Toleration, 0, len(local.Tolerations))
	for _, t := range local.Tolerations {
		tolerations = append(tolerations, corev1.Toleration{Key: t.Key, Operator: corev1.TolerationOperator(t.Operator),
			Value: t.Value, Effect: corev1.TaintEffect(t.Effect), TolerationSeconds: t.Seconds})
	}
	var runtimeClass *string
	if local.RuntimeClassName != "" {
		runtimeClass = new(local.RuntimeClassName)
	}
	port := intstr.FromInt32(model.TestEnvPort)
	small := corev1.ResourceRequirements{
		Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("10m"), corev1.ResourceMemory: resource.MustParse("16Mi")},
		Limits:   corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("200m"), corev1.ResourceMemory: resource.MustParse("64Mi")},
	}
	tools := corev1.VolumeMount{Name: toolsVolume, MountPath: toolsMount}
	toolsRO := tools
	toolsRO.ReadOnly = true
	labels := map[string]string{LabelComponent: model.TestEnvComponent, LabelEnvironment: env.Name,
		LabelProject: env.Namespace}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: req.ID, Namespace: env.Namespace, Labels: labels,
			Annotations: map[string]string{AnnotationHolder: req.Holder},
			OwnerReferences: []metav1.OwnerReference{{APIVersion: ownerAPIVersion, Kind: ownerKind, Name: env.Name,
				UID: types.UID(env.UID), Controller: new(true), BlockOwnerDeletion: new(true)}},
		},
		Spec: corev1.PodSpec{
			RestartPolicy:                corev1.RestartPolicyNever,
			AutomountServiceAccountToken: new(false),
			EnableServiceLinks:           new(false),
			ActiveDeadlineSeconds:        &deadline,
			ImagePullSecrets:             pullSecrets,
			RuntimeClassName:             runtimeClass,
			NodeSelector:                 maps.Clone(local.NodeSelector),
			Tolerations:                  tolerations,
			InitContainers: []corev1.Container{{
				Name: installContainer, Image: toolsImage, ImagePullPolicy: corev1.PullIfNotPresent,
				Command: []string{toolsBinary, "install", Binary}, Resources: small,
				VolumeMounts: []corev1.VolumeMount{tools},
			}},
			Containers: []corev1.Container{{
				Name: EnvContainer, Image: local.Image, ImagePullPolicy: corev1.PullIfNotPresent,
				// theos-env is the container's process, whatever the image's
				// entrypoint, so any image with the tools works.
				Command: []string{Binary, "serve", "--listen", ":" + strconv.Itoa(model.TestEnvPort), "--work", WorkDir},
				Env: []corev1.EnvVar{{Name: EnvToken, ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: req.ID}, Key: TokenKey}}}},
				Ports: []corev1.ContainerPort{{Name: "theos-env", ContainerPort: model.TestEnvPort, Protocol: corev1.ProtocolTCP}},
				ReadinessProbe: &corev1.Probe{ProbeHandler: corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{
					Path: "/healthz", Port: port}}, PeriodSeconds: 2},
				Resources:       resources,
				SecurityContext: &corev1.SecurityContext{Privileged: new(false)},
				VolumeMounts: []corev1.VolumeMount{toolsRO,
					{Name: "work", MountPath: WorkDir},
					{Name: "shm", MountPath: "/dev/shm"}},
			}},
			Volumes: []corev1.Volume{
				{Name: toolsVolume, VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
				{Name: "work", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
				// A browser shares memory through /dev/shm, which is 64Mi in
				// a pod unless it is given more.
				{Name: "shm", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{
					Medium: corev1.StorageMediumMemory, SizeLimit: new(resource.MustParse("1Gi"))}}},
			},
		},
	}, nil
}

// resourcesOf is the env container's resources: the member's requests and
// limits, with each device both requested and limited, as an extended
// resource must be.
func resourcesOf(local *ports.LocalEnvironment) (corev1.ResourceRequirements, error) {
	var out corev1.ResourceRequirements
	add := func(list *corev1.ResourceList, from map[string]string) error {
		for name, value := range from {
			q, err := resource.ParseQuantity(value)
			if err != nil {
				return fmt.Errorf("resource %s: %w", name, err)
			}
			if *list == nil {
				*list = corev1.ResourceList{}
			}
			(*list)[corev1.ResourceName(name)] = q
		}
		return nil
	}
	for _, step := range []struct {
		list *corev1.ResourceList
		from map[string]string
	}{
		{&out.Requests, local.Requests}, {&out.Limits, local.Limits},
		{&out.Requests, local.Devices}, {&out.Limits, local.Devices},
	} {
		if err := add(step.list, step.from); err != nil {
			return out, err
		}
	}
	return out, nil
}
