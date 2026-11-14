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
	"testing"

	"github.com/JairJosafath/theos/internal/model"
)

// TestToolchainReleaseIsPublished263 checks that the release whose toolchain
// images the Standards' GitHub checks fall back to is a release (#263), the
// chart's own (#284): hack/release.sh bumps both at once, so one that lags
// was moved by hand, and one ahead names images not published yet.
func TestToolchainReleaseIsPublished263(t *testing.T) {
	chart, err := os.ReadFile(filepath.Join("..", "..", "dist", "chart", "Chart.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^version: *(\S+)$`).FindSubmatch(chart)
	if m == nil {
		t.Fatal("dist/chart/Chart.yaml has no version")
	}
	if !releaseVersion.Match(m[1]) {
		t.Fatalf("dist/chart/Chart.yaml's version %q is not X.Y.Z", m[1])
	}
	if !releaseVersion.MatchString(model.ToolchainRelease) {
		t.Fatalf("model.ToolchainRelease %q is not X.Y.Z: name a release, never latest or a branch",
			model.ToolchainRelease)
	}
	if model.ToolchainRelease != string(m[1]) {
		t.Errorf("model.ToolchainRelease is %s, not the chart's version %s: hack/release.sh's bump "+
			"moves both, and its check holds them equal (#284)", model.ToolchainRelease, m[1])
	}
	for _, image := range []string{model.DefaultCIImages().Go, model.DefaultCIImages().Base} {
		if problem := model.PinnedImageProblem(image); problem != "" {
			t.Errorf("model.DefaultCIImages: %s", problem)
		}
	}
}

// releaseVersion is a release's version, X.Y.Z.
var releaseVersion = regexp.MustCompile(`^\d+\.\d+\.\d+$`)
