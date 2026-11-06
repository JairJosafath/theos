/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of theos, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package chart

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// The receiver of the Git host's deliveries (docs/plans/done/git-events.md,
// decision 10, #1223): with gitEvents.enabled the chart renders its Service,
// the manager's port and flags, a NetworkPolicy letting only Gitea's
// namespace reach the port, and Gitea's webhook allow-list set to the
// Service's host alone; without it, none of them.
const (
	gitEventsName    = "theos-git-events"
	gitEventsPort    = 8082
	gitEventsHost    = gitEventsName + "." + releaseNS + ".svc"
	gitEventsURL     = "http://" + gitEventsHost + ":8082"
	gitEventsBindArg = "--git-events-bind-address="
	gitEventsURLArg  = "--git-events-url="
	allowedHostKey   = "GITEA__webhook__ALLOWED_HOST_LIST"
	managerName      = "theos-controller-manager"
)

func TestGitEventsChart(t *testing.T) {
	helm := helmBinary(t)
	out := mustRender(t, helm, "gitea.projects={apps}")
	objs := objects(t, out)

	svc, ok := objs["Service/"+releaseNS+"/"+gitEventsName].(*corev1.Service)
	if !ok {
		t.Fatalf("the chart renders no Service %s", gitEventsName)
	}
	if len(svc.Spec.Ports) != 1 || svc.Spec.Ports[0].Port != gitEventsPort ||
		svc.Spec.Ports[0].TargetPort.String() != "git-events" {
		t.Errorf("Service %s ports are %v, want %d to git-events", gitEventsName, svc.Spec.Ports, gitEventsPort)
	}
	if svc.Spec.Selector["control-plane"] != "controller-manager" {
		t.Errorf("Service %s selects %v, not the manager", gitEventsName, svc.Spec.Selector)
	}

	if got := managerArg(t, "the chart", out, gitEventsBindArg); got != gitEventsBindArg+":8082" {
		t.Errorf("the chart's manager has %q, want %s:8082", got, gitEventsBindArg)
	}
	if got := managerArg(t, "the chart", out, gitEventsURLArg); got != gitEventsURLArg+gitEventsURL {
		t.Errorf("the chart's manager has %q, want %s%s", got, gitEventsURLArg, gitEventsURL)
	}
	if !hasContainerPort(managerOf(t, objs), "git-events", gitEventsPort) {
		t.Errorf("the chart's manager has no container port git-events %d", gitEventsPort)
	}

	policy, ok := objs["NetworkPolicy/"+releaseNS+"/"+gitEventsName].(*networkingv1.NetworkPolicy)
	if !ok {
		t.Fatalf("the chart renders no NetworkPolicy %s", gitEventsName)
	}
	checkGitEventsIngress(t, "the chart", policy, giteaNS)

	if got := giteaEnv(t, objs)[allowedHostKey]; got != gitEventsHost {
		t.Errorf("the chart's Gitea has %s=%q, want %q", allowedHostKey, got, gitEventsHost)
	}

	// A Gitea of its own: the URL given, and that host the one allowed.
	out = mustRender(t, helm, "gitea.projects={apps}", "gitEvents.url=http://10.1.2.3:9000/")
	got := managerArg(t, "the chart with gitEvents.url", out, gitEventsURLArg)
	if got != gitEventsURLArg+"http://10.1.2.3:9000" {
		t.Errorf("the chart with gitEvents.url has %q", got)
	}
	if got := giteaEnv(t, objects(t, out))[allowedHostKey]; got != "10.1.2.3" {
		t.Errorf("the chart with gitEvents.url has %s=%q, want 10.1.2.3", allowedHostKey, got)
	}

	out = mustRender(t, helm, "gitea.projects={apps}", "gitEvents.enabled=false")
	objs = objects(t, out)
	for _, k := range []string{"Service/", "NetworkPolicy/"} {
		k += releaseNS + "/" + gitEventsName
		if objs[k] != nil {
			t.Errorf("the chart renders %s with gitEvents.enabled=false", k)
		}
	}
	for _, prefix := range []string{gitEventsBindArg, gitEventsURLArg} {
		if got := managerArg(t, "the chart without Git events", out, prefix); got != "" {
			t.Errorf("the chart's manager has %q with gitEvents.enabled=false", got)
		}
	}
	if hasContainerPort(managerOf(t, objs), "git-events", gitEventsPort) {
		t.Errorf("the chart's manager has port git-events with gitEvents.enabled=false")
	}
	if got, ok := giteaEnv(t, objs)[allowedHostKey]; ok {
		t.Errorf("the chart's Gitea has %s=%q with gitEvents.enabled=false", allowedHostKey, got)
	}
}

// TestGitEventsKustomize checks config/'s copies: the manager's flags and
// port, config/default's Service and NetworkPolicy, and config/gitea's
// allow-list.
func TestGitEventsKustomize(t *testing.T) {
	read := func(path ...string) []byte {
		b, err := os.ReadFile(filepath.Join(append([]string{"..", ".."}, path...)...))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	manager := read("config", "manager", "manager.yaml")
	if got := managerArg(t, "config/manager", manager, gitEventsBindArg); got != gitEventsBindArg+":8082" {
		t.Errorf("config/manager has %q", got)
	}
	if got := managerArg(t, "config/manager", manager, gitEventsURLArg); got != gitEventsURLArg+gitEventsURL {
		t.Errorf("config/manager has %q, want %s%s", got, gitEventsURLArg, gitEventsURL)
	}
	if !hasContainerPort(managerOf(t, objects(t, manager)), "git-events", gitEventsPort) {
		t.Errorf("config/manager has no container port git-events %d", gitEventsPort)
	}

	objs := objects(t, read("config", "default", "git_events_service.yaml"))
	svc, ok := objs["Service/system/git-events"].(*corev1.Service)
	if !ok || len(svc.Spec.Ports) != 1 || svc.Spec.Ports[0].Port != gitEventsPort {
		t.Errorf("config/default renders no Service git-events on %d: %v", gitEventsPort, objs)
	}
	objs = objects(t, read("config", "default", "git_events_network_policy.yaml"))
	policy, ok := objs["NetworkPolicy/system/git-events"].(*networkingv1.NetworkPolicy)
	if !ok {
		t.Fatalf("config/default has no NetworkPolicy git-events")
	}
	checkGitEventsIngress(t, "config/default", policy, "gitea")

	kustomization := string(read("config", "default", "kustomization.yaml"))
	for _, f := range []string{"git_events_service.yaml", "git_events_network_policy.yaml"} {
		if !strings.Contains(kustomization, "\n- "+f+"\n") {
			t.Errorf("config/default/kustomization.yaml does not include %s", f)
		}
	}

	if !slices.Contains(strings.Split(string(read("config", "gitea", "gitea.env")), "\n"),
		allowedHostKey+"="+gitEventsHost) {
		t.Errorf("config/gitea/gitea.env lacks %s=%s", allowedHostKey, gitEventsHost)
	}
}

// checkGitEventsIngress requires the rule for the receiver's port to admit
// only namespace ns, and no other rule to open that port.
func checkGitEventsIngress(t *testing.T, where string, p *networkingv1.NetworkPolicy, ns string) {
	t.Helper()
	if p.Spec.PodSelector.MatchLabels["control-plane"] != "controller-manager" {
		t.Errorf("%s: NetworkPolicy selects %v, not the manager", where, p.Spec.PodSelector.MatchLabels)
	}
	found := false
	for _, rule := range p.Spec.Ingress {
		opens := slices.ContainsFunc(rule.Ports, func(port networkingv1.NetworkPolicyPort) bool {
			return port.Port != nil && port.Port.IntValue() == gitEventsPort
		})
		if !opens {
			continue
		}
		if len(rule.From) != 1 || rule.From[0].NamespaceSelector == nil || rule.From[0].PodSelector != nil ||
			rule.From[0].IPBlock != nil ||
			rule.From[0].NamespaceSelector.MatchLabels["kubernetes.io/metadata.name"] != ns {
			t.Errorf("%s: the rule opening %d admits %v, want only namespace %s", where, gitEventsPort, rule.From, ns)
		}
		found = true
	}
	if !found {
		t.Errorf("%s: NetworkPolicy opens no port %d", where, gitEventsPort)
	}
}

// managerOf returns the manager Deployment's manager container.
func managerOf(t *testing.T, objs map[string]runtime.Object) corev1.Container {
	t.Helper()
	for _, o := range objs {
		d, ok := o.(*appsv1.Deployment)
		if !ok || !strings.HasSuffix(d.Name, "controller-manager") {
			continue
		}
		for _, c := range d.Spec.Template.Spec.Containers {
			if c.Name == "manager" {
				return c
			}
		}
	}
	t.Fatalf("no %s Deployment with a manager container", managerName)
	return corev1.Container{}
}

func hasContainerPort(c corev1.Container, name string, port int32) bool {
	return slices.ContainsFunc(c.Ports, func(p corev1.ContainerPort) bool {
		return p.Name == name && p.ContainerPort == port
	})
}

// giteaEnv returns the chart's Gitea settings, ConfigMap gitea-env.
func giteaEnv(t *testing.T, objs map[string]runtime.Object) map[string]string {
	t.Helper()
	cm, ok := objs["ConfigMap/"+giteaNS+"/gitea-env"].(*corev1.ConfigMap)
	if !ok {
		t.Fatalf("the chart renders no ConfigMap %s/gitea-env", giteaNS)
	}
	return cm.Data
}
