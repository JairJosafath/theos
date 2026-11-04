/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of theos, which is proprietary. No license is granted
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
	"regexp"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/kubernetes/scheme"
)

const runnersSource = "templates/gitea/runners.yaml"

// TestGitProvidersCarryRunnerPool (#1698): since the chart's shared runners
// register for the templates organization only, a project's checks run on
// the per-project pool its GitProvider's spec.ci.giteaActions.runners names.
// Every GitProvider the chart renders, the Gitea projects' in ConfigMap
// theos-catalog and the user's in theos-gitproviders, carries a pool
// with a runner for each runs-on label of the templates' check workflows, on
// the image the shared runner of that label runs, and the go one with the
// Go cache.
func TestGitProvidersCarryRunnerPool(t *testing.T) {
	helm := helmBinary(t)
	labels := checkLabels(t)
	if len(labels) == 0 {
		t.Fatal("no check workflow under templates/ names a runs-on label")
	}

	args := []string{"template", release, chartDir, "--namespace", releaseNS,
		"--show-only", catalogSource, "--show-only", gitProvidersSource, "--show-only", runnersSource,
		"--set", "gitea.projects={" + project + "}",
		"--set", "gitProviders[0].namespace=" + project, "--set", "gitProviders[0].url=http://git.example:3000",
		"--set", "gitProviders[0].owner=" + project, "--set", "gitProviders[0].existingSecret=git-credentials"}
	var stdout, stderr bytes.Buffer
	cmd := exec.Command(helm, args...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("helm %s: %v\n%s", strings.Join(args, " "), err, stderr.String())
	}

	// The shared runner Deployments' images by label, and both ConfigMaps' objects.
	images := map[string]string{}
	objects := map[string]string{}
	reader := utilyaml.NewYAMLReader(bufio.NewReader(&stdout))
	for {
		doc, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if len(bytes.TrimSpace(doc)) == 0 {
			continue
		}
		obj, _, err := scheme.Codecs.UniversalDeserializer().Decode(doc, nil, nil)
		if err != nil {
			t.Fatalf("decode: %v\n%s", err, doc)
		}
		switch o := obj.(type) {
		case *corev1.ConfigMap:
			objects[o.Name] = o.Data["objects.yaml"]
		case *appsv1.Deployment:
			if label, ok := strings.CutPrefix(o.Name, "gitea-runner-"); ok {
				for _, c := range o.Spec.Template.Spec.Containers {
					if c.Name == "runner" {
						images[label] = c.Image
					}
				}
			}
		}
	}
	for _, label := range labels {
		if images[label] == "" {
			t.Errorf("the chart renders no shared runner Deployment gitea-runner-%s with a runner container", label)
		}
	}

	var gitProviders []*unstructured.Unstructured
	for _, cm := range []string{catalogConfigMap, gitProvidersConfigMap} {
		n := 0
		for _, o := range decodeObjects(t, objects[cm]) {
			if o.GetKind() == "GitProvider" {
				gitProviders = append(gitProviders, o)
				n++
			}
		}
		if n == 0 {
			t.Errorf("ConfigMap %s holds no GitProvider", cm)
		}
	}
	for _, gp := range gitProviders {
		runners, _, _ := unstructured.NestedSlice(gp.Object, "spec", "ci", "giteaActions", "runners")
		pool := map[string]map[string]any{}
		for _, r := range runners {
			if m, ok := r.(map[string]any); ok {
				label, _ := m["label"].(string)
				pool[label] = m
			}
		}
		for _, label := range labels {
			r, ok := pool[label]
			if !ok {
				t.Errorf("GitProvider %s/%s has no %s runner in spec.ci.giteaActions.runners; the templates' checks run on it",
					gp.GetNamespace(), gp.GetName(), label)
				continue
			}
			if size, _ := r["size"].(int64); size < 1 {
				t.Errorf("GitProvider %s/%s's %s runner has size %d, want at least 1", gp.GetNamespace(), gp.GetName(), label, size)
			}
			if image, _ := r["image"].(string); image != images[label] {
				t.Errorf("GitProvider %s/%s's %s runner has image %q, want the shared runner's %q",
					gp.GetNamespace(), gp.GetName(), label, image, images[label])
			}
			if goCache, _ := r["goCache"].(bool); label == "go" && !goCache {
				t.Errorf("GitProvider %s/%s's go runner has no goCache", gp.GetNamespace(), gp.GetName())
			}
		}
	}
}

// checkLabels returns the runs-on labels of the templates' check workflows
// (templates/*/.theos/ci/giteaActions/.gitea/workflows/check.yaml*), sorted
// and unique.
func checkLabels(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("..", "..", "templates", "*", ".theos", "ci", "giteaActions",
		".gitea", "workflows", "check.yaml*"))
	if err != nil {
		t.Fatal(err)
	}
	runsOn := regexp.MustCompile(`(?m)^\s*runs-on:\s*(\S+)`)
	seen := map[string]bool{}
	var labels []string
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range runsOn.FindAllStringSubmatch(string(b), -1) {
			if !seen[m[1]] {
				seen[m[1]] = true
				labels = append(labels, m[1])
			}
		}
	}
	return labels
}
