/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package chart

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/kubernetes/scheme"
)

// The model proxy (docs/plans/model-proxy.md, decisions 2 and 7, #1135) is the
// one place the Claude token is used. The token enters its pod as a Secret
// volume, never as env or through a subPath, so the kubelet refreshes it, and
// only agent sandboxes in the namespaces labelled for the shared token reach
// it.
const (
	modelProxyName        = "c4-model-proxy"
	modelProxyCredentials = "claude-credentials"
	sandboxPodLabel       = "c4.factory.io/agent-sandbox"
	sharedSecretsNSLabel  = "c4.factory.io/shared-secrets"
)

// modelProxyObjects is what a render holds of the model proxy.
type modelProxyObjects struct {
	deployment *appsv1.Deployment
	policy     *networkingv1.NetworkPolicy
	service    bool
}

// TestModelProxyChart checks the chart's model proxy: the Deployment, Service
// and NetworkPolicy render with agents.enabled, and nothing of it without.
func TestModelProxyChart(t *testing.T) {
	helm := helmBinary(t)
	out, err := exec.Command(helm, "template", release, chartDir, "--namespace", releaseNS).Output()
	if err != nil {
		t.Fatalf("helm template: %v", err)
	}
	checkModelProxy(t, "the chart", out, modelProxyName, modelProxyCredentials)

	out, err = exec.Command(helm, "template", release, chartDir, "--namespace", releaseNS,
		"--set", "agents.enabled=false").Output()
	if err != nil {
		t.Fatalf("helm template agents.enabled=false: %v", err)
	}
	got := decodeModelProxy(t, "the chart without agents", out, modelProxyName)
	if got.deployment != nil || got.policy != nil || got.service {
		t.Errorf("the chart renders the model proxy with agents.enabled=false")
	}
}

// TestModelProxyKustomize checks config/model-proxy's files the same way.
func TestModelProxyKustomize(t *testing.T) {
	var manifests []byte
	for _, f := range []string{"deployment.yaml", "service.yaml", "networkpolicy.yaml"} {
		b, err := os.ReadFile(filepath.Join("..", "..", "config", "model-proxy", f))
		if err != nil {
			t.Fatal(err)
		}
		manifests = append(manifests, "\n---\n"...)
		manifests = append(manifests, b...)
	}
	checkModelProxy(t, "config/model-proxy", manifests, "model-proxy", modelProxyCredentials)
}

// checkModelProxy requires the proxy's three objects in manifests, the Secret
// mounted as a whole volume and in no env, and an ingress only from sandboxes.
func checkModelProxy(t *testing.T, where string, manifests []byte, name, secret string) {
	t.Helper()
	got := decodeModelProxy(t, where, manifests, name)
	if !got.service {
		t.Errorf("%s renders no Service %s", where, name)
	}

	if got.deployment == nil {
		t.Errorf("%s renders no Deployment %s", where, name)
	} else {
		checkModelProxyPod(t, where, got.deployment.Spec.Template.Spec, secret)
	}

	if got.policy == nil {
		t.Errorf("%s renders no NetworkPolicy %s", where, name)
	} else {
		checkModelProxyIngress(t, where, got.policy)
	}
}

// checkModelProxyPod requires the Secret as an optional volume with no
// subPath and no Secret in any container's env or envFrom.
func checkModelProxyPod(t *testing.T, where string, pod corev1.PodSpec, secret string) {
	t.Helper()
	var volume string
	for _, v := range pod.Volumes {
		if v.Secret != nil && v.Secret.SecretName == secret {
			volume = v.Name
			// The guide makes the Secret after `helm install --wait`; a required
			// volume keeps the pod ContainerCreating and the install times out (#1627).
			if v.Secret.Optional == nil || !*v.Secret.Optional {
				t.Errorf("%s: volume %s of Secret %s is not optional, so the pod waits for the Secret (#1627)",
					where, v.Name, secret)
			}
		}
	}
	if volume == "" {
		t.Errorf("%s: the Deployment has no volume of Secret %s", where, secret)
		return
	}
	if pod.AutomountServiceAccountToken == nil || *pod.AutomountServiceAccountToken {
		t.Errorf("%s: the Deployment mounts a ServiceAccount token", where)
	}
	mounted := false
	for _, c := range append(pod.Containers, pod.InitContainers...) {
		for _, m := range c.VolumeMounts {
			if m.Name != volume {
				continue
			}
			mounted = true
			if m.SubPath != "" || m.SubPathExpr != "" {
				t.Errorf("%s: container %s mounts the Secret with a subPath, which the kubelet never refreshes", where, c.Name)
			}
		}
		for _, e := range c.Env {
			if e.ValueFrom != nil && e.ValueFrom.SecretKeyRef != nil {
				t.Errorf("%s: container %s puts Secret %s in env %s", where, c.Name, e.ValueFrom.SecretKeyRef.Name, e.Name)
			}
		}
		for _, e := range c.EnvFrom {
			if e.SecretRef != nil {
				t.Errorf("%s: container %s puts Secret %s in envFrom", where, c.Name, e.SecretRef.Name)
			}
		}
	}
	if !mounted {
		t.Errorf("%s: no container mounts volume %s", where, volume)
	}
}

// checkModelProxyIngress requires one ingress rule with one peer, from pods
// labelled as sandboxes in namespaces labelled for the shared token.
func checkModelProxyIngress(t *testing.T, where string, p *networkingv1.NetworkPolicy) {
	t.Helper()
	if len(p.Spec.PolicyTypes) != 1 || p.Spec.PolicyTypes[0] != networkingv1.PolicyTypeIngress {
		t.Errorf("%s: NetworkPolicy policyTypes are %v, want [Ingress]", where, p.Spec.PolicyTypes)
	}
	if len(p.Spec.Ingress) != 1 || len(p.Spec.Ingress[0].From) != 1 {
		t.Errorf("%s: NetworkPolicy has %d ingress rules, want one with one from", where, len(p.Spec.Ingress))
		return
	}
	peer := p.Spec.Ingress[0].From[0]
	if peer.NamespaceSelector == nil || peer.NamespaceSelector.MatchLabels[sharedSecretsNSLabel] != "true" ||
		len(peer.NamespaceSelector.MatchExpressions) != 0 {
		t.Errorf("%s: NetworkPolicy ingress namespaceSelector is %v, want only %s=true",
			where, peer.NamespaceSelector, sharedSecretsNSLabel)
	}
	if peer.PodSelector == nil || peer.PodSelector.MatchLabels[sandboxPodLabel] != "true" ||
		len(peer.PodSelector.MatchExpressions) != 0 {
		t.Errorf("%s: NetworkPolicy ingress podSelector is %v, want only %s=true", where, peer.PodSelector, sandboxPodLabel)
	}
	if peer.IPBlock != nil {
		t.Errorf("%s: NetworkPolicy ingress admits an ipBlock", where)
	}
}

// decodeModelProxy picks the proxy's objects named name out of manifests.
func decodeModelProxy(t *testing.T, where string, manifests []byte, name string) modelProxyObjects {
	t.Helper()
	var got modelProxyObjects
	reader := utilyaml.NewYAMLReader(bufio.NewReader(bytes.NewReader(manifests)))
	for {
		doc, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if len(bytes.TrimSpace(doc)) == 0 || !strings.Contains(string(doc), "name: "+name+"\n") {
			continue
		}
		obj, _, err := scheme.Codecs.UniversalDeserializer().Decode(doc, nil, nil)
		if err != nil {
			t.Fatalf("%s: decode: %v\n%s", where, err, doc)
		}
		switch o := obj.(type) {
		case *appsv1.Deployment:
			if o.Name == name {
				got.deployment = o
			}
		case *networkingv1.NetworkPolicy:
			if o.Name == name {
				got.policy = o
			}
		case *corev1.Service:
			if o.Name == name {
				got.service = true
			}
		}
	}
	return got
}
