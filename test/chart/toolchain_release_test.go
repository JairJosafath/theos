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
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"

	"github.com/JairJosafath/theos/internal/model"
)

// TestToolchainReleaseIsPublished263 checks that the release whose toolchain
// images the Standards' GitHub checks fall back to is a release, no later
// than the chart's newest (#263): a version the chart has not reached has
// published no images yet.
func TestToolchainReleaseIsPublished263(t *testing.T) {
	chart, err := os.ReadFile(filepath.Join("..", "..", "dist", "chart", "Chart.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^version: *(\S+)$`).FindSubmatch(chart)
	if m == nil {
		t.Fatal("dist/chart/Chart.yaml has no version")
	}
	newest, ok := versionParts(string(m[1]))
	if !ok {
		t.Fatalf("dist/chart/Chart.yaml's version %q is not X.Y.Z", m[1])
	}
	release, ok := versionParts(model.ToolchainRelease)
	if !ok {
		t.Fatalf("model.ToolchainRelease %q is not X.Y.Z: name a release, never latest or a branch",
			model.ToolchainRelease)
	}
	for i := range release {
		if release[i] != newest[i] {
			if release[i] > newest[i] {
				t.Errorf("model.ToolchainRelease is %s, after the chart's newest release %s",
					model.ToolchainRelease, m[1])
			}
			break
		}
	}
	for _, image := range []string{model.DefaultCIImages().Go, model.DefaultCIImages().Base} {
		if problem := model.PinnedImageProblem(image); problem != "" {
			t.Errorf("model.DefaultCIImages: %s", problem)
		}
	}
}

// versionParts returns X.Y.Z's three numbers.
func versionParts(v string) ([3]int, bool) {
	var out [3]int
	m := regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)$`).FindStringSubmatch(v)
	if m == nil {
		return out, false
	}
	for i := range out {
		out[i], _ = strconv.Atoi(m[i+1])
	}
	return out, true
}
