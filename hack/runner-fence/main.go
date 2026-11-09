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

// Command runner-fence checks the CI fence (docs/design.md, "The CI fence") on
// the runner Deployments in YAML files: make validate-yaml runs it on the
// rendered chart and on config/runner with config/gitea.
//
//	go run ./hack/runner-fence FILE...
//
// A runner's pods (label app.kubernetes.io/component: ci-runner) give the
// registration token (env RUNNER_TOKEN, Secret gitea-runner) to an init
// container only, never to a container where jobs run or one beside them;
// they mount no ServiceAccount token, share no process namespace and use no
// host network. No container runs privileged or adds a capability, and only
// the image runner's job container (the runner container of the Deployment
// registering label image) runs with seccomp or AppArmor Unconfined, as a
// user other than root, with /proc masked, in a pod with no hostPath volume
// and none of the node's process or IPC namespaces. Every NetworkPolicy that selects them lets no traffic in and
// lets out only DNS (port 53 to cluster DNS: pods under labels in one
// namespace, or an address block inside model.PrivateRanges, the chart's
// clusterDNS, #1257), Gitea's pods on port 3000 and HTTPS to public addresses
// (model.PrivateRanges kept out); together they let out all three, and one of
// them isolates the pods both ways.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/intstr"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"

	"github.com/JairJosafath/theos/internal/model"
)

const (
	componentLabel  = "app.kubernetes.io/component"
	runnerComponent = "ci-runner"
	nameLabel       = "app.kubernetes.io/name"
	// tokenSecret and tokenEnv hold Gitea's instance-wide registration token.
	tokenSecret   = "gitea-runner"
	tokenEnv      = "RUNNER_TOKEN"
	readsTheToken = "can read the registration token (" + tokenEnv + ", Secret " + tokenSecret +
		"); only the init container that registers may"
	// gitea is the Gitea Deployment, and the name label of its pods.
	gitea     = "gitea"
	giteaPort = 3000
	// imageLabel is the runner label of rootless BuildKit, whose user
	// namespaces the runtime's default seccomp and AppArmor profiles refuse:
	// its job container, jobContainer, alone may run Unconfined
	// (docs/plans/images.md, decision 2).
	imageLabel   = "image"
	jobContainer = "runner"
	labelsEnv    = "RUNNER_LABELS"
	// appArmorAnnotation is the AppArmor profile's annotation before the field.
	appArmorAnnotation = "container.apparmor.security.beta.kubernetes.io/"
)

// The egress a job needs, each one rule.
const (
	reachDNS   = "DNS"
	reachGitea = "Gitea on port 3000"
	reachHTTPS = "HTTPS to public addresses"
)

type objects struct {
	deployments []appsv1.Deployment
	policies    []networkingv1.NetworkPolicy
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: runner-fence FILE...")
		os.Exit(2)
	}
	objs, err := read(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if problems := check(objs); len(problems) > 0 {
		for _, p := range problems {
			fmt.Fprintln(os.Stderr, p)
		}
		os.Exit(1)
	}
	fmt.Printf("The CI runners in %s are fenced: %s\n",
		strings.Join(os.Args[1:], ", "), strings.Join(runnerNames(objs), ", "))
}

// read returns the Deployments and NetworkPolicies in the YAML files.
func read(paths []string) (objects, error) {
	var objs objects
	for _, path := range paths {
		f, err := os.Open(path) //nolint:gosec // a YAML file named on the command line
		if err != nil {
			return objs, err
		}
		err = decode(f, &objs)
		_ = f.Close()
		if err != nil {
			return objs, fmt.Errorf("%s: %w", path, err)
		}
	}
	return objs, nil
}

func decode(r io.Reader, objs *objects) error {
	d := utilyaml.NewYAMLOrJSONDecoder(r, 4096)
	for {
		var raw json.RawMessage
		if err := d.Decode(&raw); errors.Is(err, io.EOF) {
			return nil
		} else if err != nil {
			return err
		}
		var tm metav1.TypeMeta
		if err := json.Unmarshal(raw, &tm); err != nil {
			continue // a document of comments only, or not an object
		}
		var err error
		switch {
		case tm.APIVersion == "apps/v1" && tm.Kind == "Deployment":
			var o appsv1.Deployment
			err = json.Unmarshal(raw, &o)
			objs.deployments = append(objs.deployments, o)
		case tm.APIVersion == "networking.k8s.io/v1" && tm.Kind == "NetworkPolicy":
			var o networkingv1.NetworkPolicy
			err = json.Unmarshal(raw, &o)
			objs.policies = append(objs.policies, o)
		}
		if err != nil {
			return err
		}
	}
}

func isRunner(d appsv1.Deployment) bool {
	return d.Spec.Template.Labels[componentLabel] == runnerComponent
}

func runnerNames(objs objects) []string {
	var names []string
	for _, d := range objs.deployments {
		if isRunner(d) {
			names = append(names, d.Namespace+"/"+d.Name)
		}
	}
	return names
}

// check returns what breaks the fence, or nothing.
func check(objs objects) []string {
	var problems []string
	runners := 0
	for _, d := range objs.deployments {
		name := fmt.Sprintf("Deployment %s/%s", d.Namespace, d.Name)
		if !isRunner(d) {
			if registers(d.Spec.Template.Spec) {
				problems = append(problems, fmt.Sprintf(
					"%s registers a runner, but its pods lack label %s: %s, which the fence selects",
					name, componentLabel, runnerComponent))
			}
			continue
		}
		runners++
		problems = append(problems, checkPod(name, d.Spec.Template.Spec)...)
		problems = append(problems, checkConfinement(name, d.Spec.Template)...)
		problems = append(problems, checkFence(name, d, objs)...)
	}
	if runners == 0 {
		problems = append(problems, fmt.Sprintf("No runner Deployment (pod label %s: %s) to check",
			componentLabel, runnerComponent))
	}
	return problems
}

// registers says whether a pod registers a runner. Gitea reads the token
// too, as GITEA_RUNNER_REGISTRATION_TOKEN; a runner registers with it as
// RUNNER_TOKEN (hack/runner/register.sh).
func registers(spec corev1.PodSpec) bool {
	return slices.ContainsFunc(slices.Concat(spec.InitContainers, spec.Containers), func(c corev1.Container) bool {
		return slices.ContainsFunc(c.Env, func(e corev1.EnvVar) bool { return e.Name == tokenEnv })
	})
}

// checkPod checks that only an init container, which ends before the jobs
// start, gets the registration token, and that the pod gives the jobs no
// way around the fence.
func checkPod(name string, spec corev1.PodSpec) []string {
	var problems []string
	if spec.HostNetwork {
		problems = append(problems, name+": its pods use the node's network, which no NetworkPolicy fences")
	}
	if spec.AutomountServiceAccountToken == nil || *spec.AutomountServiceAccountToken {
		problems = append(problems, name+
			": its pods mount a ServiceAccount token, with which a job reaches the API; set automountServiceAccountToken: false")
	}
	if spec.ShareProcessNamespace != nil && *spec.ShareProcessNamespace {
		problems = append(problems, name+
			": its containers share a process namespace, so a job could read the others' environment")
	}
	registered := false
	for _, c := range spec.InitContainers {
		switch {
		case !readsToken(spec, c):
		case c.RestartPolicy != nil && *c.RestartPolicy == corev1.ContainerRestartPolicyAlways:
			problems = append(problems, fmt.Sprintf("%s: sidecar %s, which runs beside the jobs, %s",
				name, c.Name, readsTheToken))
		default:
			registered = true
		}
	}
	for _, c := range spec.Containers {
		if readsToken(spec, c) {
			problems = append(problems, fmt.Sprintf("%s: container %s, where jobs run, %s", name, c.Name, readsTheToken))
		}
	}
	if !registered {
		problems = append(problems, fmt.Sprintf("%s: no init container registers the runner with %s", name, tokenEnv))
	}
	return problems
}

// checkConfinement checks that no container of a runner pod is privileged or
// adds a capability, and that only the image runner's job container runs with
// seccomp or AppArmor Unconfined, not as root, with /proc masked, in a pod
// sharing nothing of the node's.
func checkConfinement(name string, pod corev1.PodTemplateSpec) []string {
	var problems []string
	spec := pod.Spec
	image := registersLabel(spec, imageLabel)
	if p := spec.SecurityContext; p != nil && (unconfined(p.SeccompProfile) || unconfinedAppArmor(p.AppArmorProfile)) {
		problems = append(problems, name+
			": its pods run every container with seccomp or AppArmor Unconfined; only the image runner's job container may")
	}
	loosened := false
	for i, c := range slices.Concat(spec.InitContainers, spec.Containers) {
		sc := c.SecurityContext
		if sc == nil {
			sc = &corev1.SecurityContext{}
		}
		if sc.Privileged != nil && *sc.Privileged {
			problems = append(problems, fmt.Sprintf("%s: container %s runs privileged", name, c.Name))
		}
		if sc.Capabilities != nil && len(sc.Capabilities.Add) > 0 {
			problems = append(problems, fmt.Sprintf("%s: container %s adds capabilities %v", name, c.Name, sc.Capabilities.Add))
		}
		if !unconfined(sc.SeccompProfile) && !unconfinedAppArmor(sc.AppArmorProfile) &&
			pod.Annotations[appArmorAnnotation+c.Name] != "unconfined" {
			continue
		}
		if job := i >= len(spec.InitContainers) && c.Name == jobContainer; !image || !job {
			problems = append(problems, fmt.Sprintf(
				"%s: container %s runs with seccomp or AppArmor Unconfined; only the job container of the runner labelled %s may",
				name, c.Name, imageLabel))
			continue
		}
		loosened = true
		if sc.ProcMount != nil && *sc.ProcMount == corev1.UnmaskedProcMount {
			problems = append(problems, fmt.Sprintf("%s: container %s runs Unconfined with an unmasked /proc", name, c.Name))
		}
		if !nonRoot(spec.SecurityContext, sc) {
			problems = append(problems, fmt.Sprintf(
				"%s: container %s runs Unconfined but may run as root; set runAsNonRoot: true and runAsUser: 1000", name, c.Name))
		}
	}
	if loosened {
		if spec.HostPID || spec.HostIPC {
			problems = append(problems, name+
				": its pods share the node's process or IPC namespace beside an Unconfined container")
		}
		for _, v := range spec.Volumes {
			if v.HostPath != nil {
				problems = append(problems, fmt.Sprintf(
					"%s: its pods mount node directory %s (volume %s) beside an Unconfined container", name, v.HostPath.Path, v.Name))
			}
		}
	}
	return problems
}

// registersLabel says whether the init container that registers, the one
// given the registration token, names the runner label (RUNNER_LABELS,
// label:schema,...): the label Gitea gives the runner.
func registersLabel(spec corev1.PodSpec, label string) bool {
	for _, c := range spec.InitContainers {
		if !readsToken(spec, c) {
			continue
		}
		for _, e := range c.Env {
			if e.Name != labelsEnv {
				continue
			}
			for l := range strings.SplitSeq(e.Value, ",") {
				if name, _, _ := strings.Cut(strings.TrimSpace(l), ":"); name == label {
					return true
				}
			}
		}
	}
	return false
}

func unconfined(p *corev1.SeccompProfile) bool {
	return p != nil && p.Type == corev1.SeccompProfileTypeUnconfined
}

func unconfinedAppArmor(p *corev1.AppArmorProfile) bool {
	return p != nil && p.Type == corev1.AppArmorProfileTypeUnconfined
}

// nonRoot says whether the container must run as a user other than root: the
// container's settings, else the pod's.
func nonRoot(pod *corev1.PodSecurityContext, c *corev1.SecurityContext) bool {
	user, must := c.RunAsUser, c.RunAsNonRoot
	if pod != nil {
		if user == nil {
			user = pod.RunAsUser
		}
		if must == nil {
			must = pod.RunAsNonRoot
		}
	}
	return must != nil && *must && user != nil && *user != 0
}

// readsToken says whether container c gets the registration token, through
// its environment or a mounted Secret.
func readsToken(spec corev1.PodSpec, c corev1.Container) bool {
	for _, e := range c.Env {
		if e.Name == tokenEnv ||
			e.ValueFrom != nil && e.ValueFrom.SecretKeyRef != nil && e.ValueFrom.SecretKeyRef.Name == tokenSecret {
			return true
		}
	}
	for _, e := range c.EnvFrom {
		if e.SecretRef != nil && e.SecretRef.Name == tokenSecret {
			return true
		}
	}
	for _, m := range c.VolumeMounts {
		for _, v := range spec.Volumes {
			if v.Name == m.Name && mountsToken(v) {
				return true
			}
		}
	}
	return false
}

func mountsToken(v corev1.Volume) bool {
	if v.Secret != nil && v.Secret.SecretName == tokenSecret {
		return true
	}
	return v.Projected != nil && slices.ContainsFunc(v.Projected.Sources, func(s corev1.VolumeProjection) bool {
		return s.Secret != nil && s.Secret.Name == tokenSecret
	})
}

// checkFence checks the NetworkPolicies that select the runner's pods.
func checkFence(name string, d appsv1.Deployment, objs objects) []string {
	var problems []string
	var giteaPods labels.Set
	for _, g := range objs.deployments {
		if g.Namespace == d.Namespace && g.Name == gitea {
			giteaPods = g.Spec.Template.Labels
		}
	}
	if giteaPods == nil {
		problems = append(problems, fmt.Sprintf("%s: no Deployment %s/%s to check the fence's Gitea rule against",
			name, d.Namespace, gitea))
	}
	selected, isolated, reaches := false, false, map[string]bool{}
	for _, p := range objs.policies {
		if p.Namespace != d.Namespace {
			continue
		}
		selector, err := metav1.LabelSelectorAsSelector(&p.Spec.PodSelector)
		if err != nil {
			problems = append(problems, fmt.Sprintf("NetworkPolicy %s/%s: %v", p.Namespace, p.Name, err))
			continue
		}
		if !selector.Matches(labels.Set(d.Spec.Template.Labels)) {
			continue
		}
		selected = true
		policy := fmt.Sprintf("NetworkPolicy %s/%s", p.Namespace, p.Name)
		types := p.Spec.PolicyTypes
		if len(types) == 0 { // the API server's default
			types = []networkingv1.PolicyType{networkingv1.PolicyTypeIngress}
			if len(p.Spec.Egress) > 0 {
				types = append(types, networkingv1.PolicyTypeEgress)
			}
		}
		// The API server ignores the rules of a type the policy doesn't name.
		ingress := slices.Contains(types, networkingv1.PolicyTypeIngress)
		egressed := slices.Contains(types, networkingv1.PolicyTypeEgress)
		isolated = isolated || ingress && egressed
		if ingress && len(p.Spec.Ingress) > 0 {
			problems = append(problems, fmt.Sprintf("%s lets traffic into the runner pods of %s", policy, name))
		}
		if !egressed {
			continue
		}
		for i, rule := range p.Spec.Egress {
			reach, problem := egress(rule, giteaPods)
			if problem != "" {
				problems = append(problems, fmt.Sprintf("%s: egress rule %d %s", policy, i+1, problem))
				continue
			}
			reaches[reach] = true
		}
	}
	if !selected {
		return append(problems, fmt.Sprintf("%s: no NetworkPolicy in namespace %s selects its pods", name, d.Namespace))
	}
	if !isolated {
		problems = append(problems, fmt.Sprintf("%s: no NetworkPolicy selects its pods for both Ingress and Egress", name))
	}
	for _, reach := range []string{reachDNS, reachGitea, reachHTTPS} {
		if !reaches[reach] {
			problems = append(problems, fmt.Sprintf("%s: no NetworkPolicy lets its pods reach %s, which a job needs",
				name, reach))
		}
	}
	return problems
}

// more is the problem with a rule that allows anything but the three.
const more = "reaches more than DNS, Gitea on port 3000 and HTTPS to public addresses"

// egress says which of the three a rule allows, or what else it allows. The
// DNS rule, the one on port 53 alone, may name several peers (NodeLocal
// DNSCache's address beside kube-dns); the others name one.
func egress(rule networkingv1.NetworkPolicyEgressRule, giteaPods labels.Set) (reach, problem string) {
	if ports(rule.Ports, port{corev1.ProtocolUDP, 53}, port{corev1.ProtocolTCP, 53}) {
		if len(rule.To) == 0 {
			return "", more // no peer means every address
		}
		for _, to := range rule.To {
			if problem := dnsPeer(to); problem != "" {
				return "", problem
			}
		}
		return reachDNS, ""
	}
	if len(rule.To) != 1 {
		return "", more
	}
	to := rule.To[0]
	switch {
	case to.IPBlock != nil && to.PodSelector == nil && to.NamespaceSelector == nil:
		if to.IPBlock.CIDR != "0.0.0.0/0" || !ports(rule.Ports, port{corev1.ProtocolTCP, 443}) {
			return "", more
		}
		for _, r := range model.PrivateRanges() {
			if !slices.Contains(to.IPBlock.Except, r) {
				return "", fmt.Sprintf("reaches the private range %s over HTTPS", r)
			}
		}
		return reachHTTPS, ""
	case to.IPBlock == nil && to.NamespaceSelector == nil && to.PodSelector != nil:
		if to.PodSelector.MatchLabels[nameLabel] != gitea || len(to.PodSelector.MatchExpressions) > 0 ||
			!ports(rule.Ports, port{corev1.ProtocolTCP, giteaPort}) {
			return "", more
		}
		if giteaPods != nil && !labels.SelectorFromSet(to.PodSelector.MatchLabels).Matches(giteaPods) {
			return "", "doesn't select the Gitea pods"
		}
		return reachGitea, ""
	}
	return "", more
}

// dnsPeer says what is wrong with a peer of the DNS rule, "" when nothing:
// cluster DNS is pods under labels in one namespace (kube-dns in
// kube-system, OpenShift's openshift-dns) or an address block inside the
// private ranges (NodeLocal DNSCache's 169.254.20.10/32), the forms of
// --cluster-dns (#1257); every namespace, every pod of one, or a public
// address on port 53 is more than DNS.
func dnsPeer(to networkingv1.NetworkPolicyPeer) string {
	switch {
	case to.IPBlock != nil && to.PodSelector == nil && to.NamespaceSelector == nil:
		if len(to.IPBlock.Except) > 0 || !model.PrivateBlock(to.IPBlock.CIDR) {
			return fmt.Sprintf("reaches %s on port 53, outside the private ranges", to.IPBlock.CIDR)
		}
		return ""
	case to.IPBlock == nil && to.NamespaceSelector != nil && to.PodSelector != nil:
		ns, pods := to.NamespaceSelector, to.PodSelector
		if len(ns.MatchExpressions) > 0 || len(ns.MatchLabels) != 1 || ns.MatchLabels[corev1.LabelMetadataName] == "" ||
			len(pods.MatchExpressions) > 0 || len(pods.MatchLabels) == 0 {
			return more
		}
		return ""
	}
	return more
}

type port struct {
	protocol corev1.Protocol
	number   int32
}

// ports says whether the rule's ports are exactly the ones given, in any order.
func ports(got []networkingv1.NetworkPolicyPort, want ...port) bool {
	if len(got) != len(want) {
		return false
	}
	for _, w := range want {
		if !slices.ContainsFunc(got, func(p networkingv1.NetworkPolicyPort) bool {
			protocol := corev1.ProtocolTCP // the API server's default
			if p.Protocol != nil {
				protocol = *p.Protocol
			}
			return protocol == w.protocol && p.Port != nil && *p.Port == intstr.FromInt32(w.number) && p.EndPort == nil
		}) {
			return false
		}
	}
	return true
}
