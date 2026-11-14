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
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/JairJosafath/theos/internal/model"
)

// ciImageFlag finds the operator's toolchain images for a scaffolded GitHub
// check in rendered manifests.
var ciImageFlag = regexp.MustCompile(`(?m)^\s*- --ci-image-(go|base)=(\S+)$`)

// TestChartNamesTheCIImages284 covers #284: the chart's ciImages reach the
// operator as --ci-image-go and --ci-image-base, so a scaffolded GitHub check
// falls back to the registry the chart was given. A released chart names its
// own release's images; the committed chart (appVersion dev) names the
// release it follows, model.DefaultCIImages; a repository and a tag of the
// user's replace them, and an empty repository leaves the flag out. Every
// image it renders is pinned, as the operator requires at startup.
func TestChartNamesTheCIImages284(t *testing.T) {
	helm := helmBinary(t)
	const version = "1.2.3"
	dir := t.TempDir()
	if out, err := exec.Command(helm, "package", chartDir, "--version", version, "--app-version", version,
		"--destination", dir).CombinedOutput(); err != nil {
		t.Fatalf("helm package: %v\n%s", err, out)
	}
	defaults := model.DefaultCIImages()
	for _, c := range []struct {
		name  string
		chart string
		sets  []string
		want  map[string]string
	}{
		{"a released chart names its release's images", filepath.Join(dir, "theos-"+version+".tgz"), nil,
			map[string]string{"go": model.ToolchainRegistry + "/go:" + version,
				"base": model.ToolchainRegistry + "/base:" + version}},
		{"the committed chart names the release it follows", chartDir, nil,
			map[string]string{"go": defaults.Go, "base": defaults.Base}},
		{"a registry and a tag of the user's", chartDir,
			[]string{"ciImages.go.repository=registry.example.com:5000/tools/go", "ciImages.go.tag=9.9.9",
				"ciImages.base.repository=registry.example.com:5000/tools/base"},
			map[string]string{"go": "registry.example.com:5000/tools/go:9.9.9",
				"base": "registry.example.com:5000/tools/base:" + model.ToolchainRelease}},
		{"an empty repository leaves its flag out", chartDir, []string{"ciImages.base.repository="},
			map[string]string{"go": defaults.Go}},
	} {
		t.Run(c.name, func(t *testing.T) {
			args := make([]string, 0, 7+2*len(c.sets))
			args = append(args, "template", release, c.chart, "--namespace", releaseNS,
				"--show-only", "templates/manager/manager.yaml")
			for _, s := range c.sets {
				args = append(args, "--set", s)
			}
			out, err := exec.Command(helm, args...).Output()
			if err != nil {
				t.Fatalf("helm template: %v", err)
			}
			got := map[string]string{}
			for _, m := range ciImageFlag.FindAllSubmatch(out, -1) {
				got[string(m[1])] = string(m[2])
				if problem := model.PinnedImageProblem(string(m[2])); problem != "" {
					t.Errorf("--ci-image-%s: %s", m[1], problem)
				}
			}
			for _, toolchain := range []string{"go", "base"} {
				if got[toolchain] != c.want[toolchain] {
					t.Errorf("--ci-image-%s is %q, want %q", toolchain, got[toolchain], c.want[toolchain])
				}
			}
		})
	}
}
