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

package chart

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/kubernetes/scheme"
)

// minProbeTimeout is the least a probe of the operator or the UI waits for an
// answer: on a busy single-node host a healthy process can take longer than
// Kubernetes' 1 s default, and a liveness failure restarts it (#957).
const minProbeTimeout = 5

// TestProbesWaitForABusyHost fails when a probe of the controller-manager or
// the UI, in the chart or in config/, waits less than minProbeTimeout.
func TestProbesWaitForABusyHost(t *testing.T) {
	helm := helmBinary(t)
	out, err := exec.Command(helm, "template", release, chartDir, "--namespace", releaseNS).Output()
	if err != nil {
		t.Fatalf("helm template: %v", err)
	}
	checkProbes(t, "the chart", out, release+"-controller-manager", release+"-ui")

	for file, name := range map[string]string{
		"config/manager/manager.yaml": "controller-manager",
		"config/ui/deployment.yaml":   "theos-ui",
	} {
		b, err := os.ReadFile(filepath.Join("..", "..", file))
		if err != nil {
			t.Fatal(err)
		}
		checkProbes(t, file, b, name)
	}
}

// checkProbes decodes manifests, requires the Deployments names in them, and
// checks every probe of their containers.
func checkProbes(t *testing.T, where string, manifests []byte, names ...string) {
	t.Helper()
	found := map[string]bool{}
	reader := utilyaml.NewYAMLReader(bufio.NewReader(bytes.NewReader(manifests)))
	for {
		doc, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(doc), "kind: Deployment") {
			continue
		}
		obj, _, err := scheme.Codecs.UniversalDeserializer().Decode(doc, nil, nil)
		if err != nil {
			t.Fatalf("%s: decode: %v", where, err)
		}
		d, ok := obj.(*appsv1.Deployment)
		if !ok || !slices.Contains(names, d.Name) {
			continue
		}
		found[d.Name] = true
		for _, c := range d.Spec.Template.Spec.Containers {
			for kind, p := range map[string]*corev1.Probe{
				"liveness": c.LivenessProbe, "readiness": c.ReadinessProbe, "startup": c.StartupProbe,
			} {
				if p != nil && p.TimeoutSeconds < minProbeTimeout {
					t.Errorf("%s: Deployment %s, container %s: %s probe timeoutSeconds is %d, want at least %d",
						where, d.Name, c.Name, kind, p.TimeoutSeconds, minProbeTimeout)
				}
			}
			if c.LivenessProbe == nil || c.ReadinessProbe == nil {
				t.Errorf("%s: Deployment %s, container %s lacks a liveness or readiness probe", where, d.Name, c.Name)
			}
		}
	}
	for _, n := range names {
		if !found[n] {
			t.Errorf("%s renders no Deployment %s", where, n)
		}
	}
}
