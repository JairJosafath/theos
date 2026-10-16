// Command runner-fence checks the CI fence (docs/design.md, "The CI fence") on
// the runner Deployments in YAML files: make validate-yaml runs it on the
// rendered chart and on config/runner with config/gitea.
//
//	go run ./hack/runner-fence FILE...
//
// A runner's pods (label app.kubernetes.io/component: ci-runner) give the
// registration token (env RUNNER_TOKEN, Secret gitea-runner) to an init
// container only, never to a container where jobs run. Every NetworkPolicy
// that selects them lets no traffic in and lets out only DNS, Gitea's pods on
// port 3000 and HTTPS to public addresses; together they let out all three,
// and one of them isolates the pods both ways.
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
)

const (
	componentLabel  = "app.kubernetes.io/component"
	runnerComponent = "ci-runner"
	nameLabel       = "app.kubernetes.io/name"
	// tokenSecret and tokenEnv hold Gitea's instance-wide registration token.
	tokenSecret = "gitea-runner"
	tokenEnv    = "RUNNER_TOKEN"
	// gitea is the Gitea Deployment, and the name label of its pods.
	gitea     = "gitea"
	giteaPort = 3000
)

// privateRanges are the addresses a job can't reach over HTTPS, as for the
// agent sandboxes (internal/controller/project_agent_network.go).
var privateRanges = []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "169.254.0.0/16"}

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
	fmt.Printf("The CI runners in %s are fenced: %s\n", strings.Join(os.Args[1:], ", "), strings.Join(runnerNames(objs), ", "))
}

// read returns the Deployments and NetworkPolicies in the YAML files.
func read(paths []string) (objects, error) {
	var objs objects
	for _, path := range paths {
		f, err := os.Open(path)
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
		spec := d.Spec.Template.Spec
		name := fmt.Sprintf("Deployment %s/%s", d.Namespace, d.Name)
		if !isRunner(d) {
			// Gitea reads the token too, as GITEA_RUNNER_REGISTRATION_TOKEN;
			// a runner registers with it as RUNNER_TOKEN (hack/runner/register.sh).
			if slices.ContainsFunc(slices.Concat(spec.InitContainers, spec.Containers), func(c corev1.Container) bool {
				return slices.ContainsFunc(c.Env, func(e corev1.EnvVar) bool { return e.Name == tokenEnv })
			}) {
				problems = append(problems, fmt.Sprintf("%s registers a runner, but its pods lack label %s: %s, which the fence selects",
					name, componentLabel, runnerComponent))
			}
			continue
		}
		runners++
		for _, c := range spec.Containers {
			if readsToken(spec, c) {
				problems = append(problems, fmt.Sprintf("%s: container %s, where jobs run, can read the registration token (%s, Secret %s); only the init container that registers may",
					name, c.Name, tokenEnv, tokenSecret))
			}
		}
		if !slices.ContainsFunc(spec.InitContainers, func(c corev1.Container) bool { return readsToken(spec, c) }) {
			problems = append(problems, fmt.Sprintf("%s: no init container registers the runner with %s", name, tokenEnv))
		}
		problems = append(problems, checkFence(name, d, objs)...)
	}
	if runners == 0 {
		problems = append(problems, fmt.Sprintf("No runner Deployment (pod label %s: %s) to check", componentLabel, runnerComponent))
	}
	return problems
}

// readsToken says whether container c gets the registration token, through
// its environment or a mounted Secret.
func readsToken(spec corev1.PodSpec, c corev1.Container) bool {
	for _, e := range c.Env {
		if e.Name == tokenEnv || e.ValueFrom != nil && e.ValueFrom.SecretKeyRef != nil && e.ValueFrom.SecretKeyRef.Name == tokenSecret {
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
		problems = append(problems, fmt.Sprintf("%s: no Deployment %s/%s to check the fence's Gitea rule against", name, d.Namespace, gitea))
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
		if slices.Contains(types, networkingv1.PolicyTypeIngress) && slices.Contains(types, networkingv1.PolicyTypeEgress) {
			isolated = true
		}
		if len(p.Spec.Ingress) > 0 {
			problems = append(problems, fmt.Sprintf("%s lets traffic into the runner pods of %s", policy, name))
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
			problems = append(problems, fmt.Sprintf("%s: no NetworkPolicy lets its pods reach %s, which a job needs", name, reach))
		}
	}
	return problems
}

// egress says which of the three a rule allows, or what else it allows.
func egress(rule networkingv1.NetworkPolicyEgressRule, giteaPods labels.Set) (reach, problem string) {
	const more = "reaches more than DNS, Gitea on port 3000 and HTTPS to public addresses"
	if len(rule.To) != 1 {
		return "", more
	}
	to := rule.To[0]
	switch {
	case to.IPBlock != nil && to.PodSelector == nil && to.NamespaceSelector == nil:
		if to.IPBlock.CIDR != "0.0.0.0/0" || !ports(rule.Ports, port{corev1.ProtocolTCP, 443}) {
			return "", more
		}
		for _, r := range privateRanges {
			if !slices.Contains(to.IPBlock.Except, r) {
				return "", fmt.Sprintf("reaches the private range %s over HTTPS", r)
			}
		}
		return reachHTTPS, ""
	case to.IPBlock == nil && to.NamespaceSelector != nil && to.PodSelector != nil:
		if !only(to.NamespaceSelector, corev1.LabelMetadataName, metav1.NamespaceSystem) ||
			!only(to.PodSelector, "k8s-app", "kube-dns") ||
			!ports(rule.Ports, port{corev1.ProtocolUDP, 53}, port{corev1.ProtocolTCP, 53}) {
			return "", more
		}
		return reachDNS, ""
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

// only says whether the selector matches exactly the one label.
func only(s *metav1.LabelSelector, key, value string) bool {
	return len(s.MatchExpressions) == 0 && len(s.MatchLabels) == 1 && s.MatchLabels[key] == value
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
