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
	"os/exec"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/kubernetes/scheme"
)

// TestInstallWaitEndsForTheRunners fails when `helm install --wait` cannot
// end (#2030): a Deployment that reads a Secret only a Job makes would wait
// for a Job that is a Helm hook, which --wait runs only after the release's
// resources are ready, or would pass its progress deadline before the Job
// may end. The seed's RUNNER_SECRET is the Secret the shared runners read.
func TestInstallWaitEndsForTheRunners(t *testing.T) {
	helm := helmBinary(t)
	out, err := exec.Command(helm, "template", release, chartDir, "--namespace", releaseNS).Output()
	if err != nil {
		t.Fatalf("helm template: %v", err)
	}

	var jobs []*batchv1.Job
	var deployments []*appsv1.Deployment
	reader := utilyaml.NewYAMLReader(bufio.NewReader(bytes.NewReader(out)))
	for {
		doc, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(doc, []byte("kind: Job")) && !bytes.Contains(doc, []byte("kind: Deployment")) {
			continue
		}
		obj, _, err := scheme.Codecs.UniversalDeserializer().Decode(doc, nil, nil)
		if err != nil {
			continue // a CRD or a ConfigMap that only mentions the kinds
		}
		switch o := obj.(type) {
		case *batchv1.Job:
			jobs = append(jobs, o)
		case *appsv1.Deployment:
			deployments = append(deployments, o)
		}
	}

	checked := 0
	for _, job := range jobs {
		secret := envValue(job.Spec.Template.Spec, "RUNNER_SECRET")
		if secret == "" {
			continue
		}
		for _, d := range deployments {
			if !readsSecret(d.Spec.Template.Spec, secret) {
				continue
			}
			checked++
			if hook := job.Annotations["helm.sh/hook"]; hook != "" {
				t.Errorf("Deployment %s reads Secret %s, which Job %s makes as a %s hook: "+
					"helm install --wait waits for %s before it runs the hook (#2030)",
					d.Name, secret, job.Name, hook, d.Name)
			}
			if job.Spec.ActiveDeadlineSeconds == nil {
				t.Errorf("Job %s has no activeDeadlineSeconds, so Deployment %s's progress deadline may pass first (#2030)",
					job.Name, d.Name)
				continue
			}
			progress := int64(600) // Kubernetes' default
			if d.Spec.ProgressDeadlineSeconds != nil {
				progress = int64(*d.Spec.ProgressDeadlineSeconds)
			}
			if progress < *job.Spec.ActiveDeadlineSeconds {
				t.Errorf("Deployment %s's progressDeadlineSeconds is %d, want at least Job %s's activeDeadlineSeconds %d (#2030)",
					d.Name, progress, job.Name, *job.Spec.ActiveDeadlineSeconds)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no Deployment reads the Secret a Job's RUNNER_SECRET names: the test checks nothing")
	}
}

// envValue returns the value of env var name in spec's containers.
func envValue(spec corev1.PodSpec, name string) string {
	for _, c := range append(spec.InitContainers, spec.Containers...) {
		for _, e := range c.Env {
			if e.Name == name {
				return e.Value
			}
		}
	}
	return ""
}

// readsSecret reports whether a container of spec reads Secret secret
// through an env var or envFrom.
func readsSecret(spec corev1.PodSpec, secret string) bool {
	for _, c := range append(spec.InitContainers, spec.Containers...) {
		for _, e := range c.Env {
			if e.ValueFrom != nil && e.ValueFrom.SecretKeyRef != nil && e.ValueFrom.SecretKeyRef.Name == secret {
				return true
			}
		}
		for _, e := range c.EnvFrom {
			if e.SecretRef != nil && e.SecretRef.Name == secret {
				return true
			}
		}
	}
	return false
}
