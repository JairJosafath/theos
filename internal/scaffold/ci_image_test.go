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

package scaffold

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/JairJosafath/theos/internal/model"
)

// containerFallback is a GitHub job's container: the toolchain image an
// Actions variable names, else the pinned one after ||.
var containerFallback = regexp.MustCompile(`^\$\{\{ vars\.(THEOS_CI_IMAGE_[A-Z]+) \|\| '([^']*)' \}\}$`)

// imageJobs is what a GitHub workflow says of each job's image.
type imageJobs struct {
	Jobs map[string]struct {
		Container string `yaml:"container"`
		Steps     []struct {
			Name string `yaml:"name"`
			If   string `yaml:"if"`
		} `yaml:"steps"`
	} `yaml:"jobs"`
}

// TestGitHubCheckFallsBackToAPinnedImage263 covers #263: the GitHub check of
// every Standard that has one runs in the toolchain image its Actions
// variable names, else in a pinned image of the factory's release, so a
// repository the operator sets no variable for (a personal account's,
// hack/render's, one adopted by hand) still runs its check. It renders with
// no image in the input, as hack/render and the operator do, and with images
// named, which the render takes instead.
func TestGitHubCheckFallsBackToAPinnedImage263(t *testing.T) {
	github, err := model.CIByName(model.CIGitHubActions)
	if err != nil {
		t.Fatal(err)
	}
	defaults := model.DefaultCIImages()
	named := model.CIImages{Go: "registry.example.com/tools/go:2.0.0", Base: "registry.example.com/tools/base@sha256:" +
		strings.Repeat("ab", 32)}
	for _, standard := range standardsWithCI {
		typ, sides := standardCombo(t, standard)
		for _, checks := range []string{model.ChecksEveryChange, model.ChecksBeforeRelease} {
			for _, c := range []struct {
				name   string
				images model.CIImages
				want   model.CIImages
			}{
				{"no image named (#263)", model.CIImages{}, defaults},
				{"images named", named, named},
			} {
				t.Run(standard+" with "+checks+", "+c.name, func(t *testing.T) {
					in, layers := loadCombo(t, typ, sides, checks)
					in.CI, in.CIImages = github, c.images
					layer := layers[slices.IndexFunc(layers, func(l Layer) bool { return l.Name == standard })]
					files, err := RenderLayer(in, layer)
					if err != nil {
						t.Fatal(err)
					}
					f, ok := files[github.Dir()+"workflows/check.yaml"]
					if !ok {
						t.Fatalf("%s writes no %sworkflows/check.yaml", standard, github.Dir())
					}
					var w imageJobs
					if err := yaml.Unmarshal(f.Data, &w); err != nil {
						t.Fatal(err)
					}
					if len(w.Jobs) == 0 {
						t.Fatal("the check has no job")
					}
					for id, job := range w.Jobs {
						m := containerFallback.FindStringSubmatch(job.Container)
						if m == nil {
							t.Errorf("job %s runs in %q, want ${{ vars.THEOS_CI_IMAGE_<TOOLCHAIN> || '<pinned image>' }}",
								id, job.Container)
							continue
						}
						variable, fallback := m[1], m[2]
						want := map[string]string{"THEOS_CI_IMAGE_GO": c.want.Go, "THEOS_CI_IMAGE_BASE": c.want.Base}[variable]
						if want == "" {
							t.Errorf("job %s reads %s, which names no toolchain the factory has", id, variable)
						}
						if fallback != want {
							t.Errorf("job %s falls back to %q, want %q", id, fallback, want)
						}
						if problem := model.PinnedImageProblem(fallback); problem != "" {
							t.Errorf("job %s falls back to an image that isn't pinned: %s", id, problem)
						}
						for _, step := range job.Steps {
							if strings.Contains(step.If, variable) {
								t.Errorf("job %s's step %q still depends on %s: %s", id, step.Name, variable, step.If)
							}
						}
					}
				})
			}
		}
	}
}
