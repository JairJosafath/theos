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

package model

import (
	"strings"
	"testing"
)

// TestPinnedImageProblem263 checks which images a GitHub check may fall back
// to (#263): a tag that doesn't move or a digest, nothing a workflow's quoted
// expression can't hold.
func TestPinnedImageProblem263(t *testing.T) {
	digest := "@sha256:" + strings.Repeat("0f", 32)
	cases := []struct {
		image string
		ok    bool
	}{
		{"ghcr.io/jairjosafath/theos/go:0.32.0", true},
		{"registry.example.com:5000/tools/go:1.2.3", true},
		{"ghcr.io/jairjosafath/theos/go" + digest, true},
		{"ghcr.io/jairjosafath/theos/go:0.32.0" + digest, true},
		{"ghcr.io/jairjosafath/theos/go:latest" + digest, true},
		{"ghcr.io/jairjosafath/theos/go", false},
		{"registry.example.com:5000/tools/go", false},
		{"ghcr.io/jairjosafath/theos/go:latest", false},
		{"theos/go:dev", false},
		{"ghcr.io/jairjosafath/theos/go:main", false},
		{"", false},
		{"ghcr.io/x/go:1' }}", false},
		{"https://ghcr.io/x/go:1", false},
		{"ghcr.io/x/go:1@sha256:short", false},
	}
	for _, c := range cases {
		if problem := PinnedImageProblem(c.image); (problem == "") != c.ok {
			t.Errorf("PinnedImageProblem(%q) = %q, want pinned %v", c.image, problem, c.ok)
		}
	}
}

// TestPinnedImageProblemGrammar284 checks the image-reference grammar
// (#284): a lowercase repository after an optional host with a port, one tag
// of [A-Za-z0-9_][A-Za-z0-9_.-]{0,127} or a sha256 digest, and no tag that
// moves.
func TestPinnedImageProblemGrammar284(t *testing.T) {
	digest := "@sha256:" + strings.Repeat("ab", 32)
	cases := []struct {
		name, image string
		want        string // a part of the problem; "" when pinned
	}{
		{"a host with a port, a path and a tag", "registry.example.com:5000/team/tools/go:1.2.3", ""},
		{"localhost with a port", "localhost:5000/go:1.2.3", ""},
		{"a path with no host", "theos/go:0.32.0", ""},
		{"a single name", "go:1", ""},
		{"a digest", "registry.example.com/tools/go" + digest, ""},
		{"a tag and a digest", "registry.example.com/tools/go:1.2.3" + digest, ""},
		{"a moving tag pinned by a digest", "ghcr.io/x/go:stable" + digest, ""},
		{"separators in the path", "ghcr.io/a.b/c__d/e-f--g/h_i:v1_2.3-rc.1", ""},
		{"a tag starting with an underscore", "go:_1", ""},
		{"a 128-character tag", "go:" + strings.Repeat("a", 128), ""},
		{"an uppercase host", "Registry.Example.com/go:1", ""},
		{"an empty tag: go::1", "go::1", "not an image reference"},
		{"two tags: go:a:b", "go:a:b", "not an image reference"},
		{"a tag starting with a dash: go:-1", "go:-1", "not an image reference"},
		{"a tag starting with a dot", "go:.1", "not an image reference"},
		{"a 129-character tag", "go:" + strings.Repeat("a", 129), "not an image reference"},
		{"an uppercase repository", "ghcr.io/JairJosafath/theos/go:1.2.3", "not lowercase"},
		{"an uppercase repository with no host", "Theos/go:1.2.3", "not lowercase"},
		{"the moving tag latest", "ghcr.io/x/go:latest", "which moves"},
		{"the moving tag stable", "ghcr.io/x/go:stable", "which moves"},
		{"the moving tag nightly", "ghcr.io/x/go:nightly", "which moves"},
		{"the moving tag edge", "ghcr.io/x/go:edge", "which moves"},
		{"no tag", "registry.example.com:5000/go", "names no tag"},
		{"an empty path component", "ghcr.io/x//go:1", "not an image reference"},
		{"a path ending in a separator", "ghcr.io/x-/go:1", "not an image reference"},
		{"an empty name", ":1", "not an image reference"},
		{"a host with an empty port", "ghcr.io:/go:1", "not an image reference"},
		{"a digest that isn't sha256", "go@md5:" + strings.Repeat("ab", 16), "not an image reference"},
		{"an uppercase digest", "go@sha256:" + strings.Repeat("AB", 32), "not an image reference"},
		{"two digests", "go" + digest + digest, "not an image reference"},
		{"a quote that would end the workflow's string", "ghcr.io/x/go:1'", "not an image reference"},
		{"an expression", "ghcr.io/x/go:${{1}}", "not an image reference"},
		{"a space", "ghcr.io/x/go :1", "not an image reference"},
		{"a name over 255 characters", strings.Repeat("a", 256) + ":1", "not an image reference"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := PinnedImageProblem(c.image)
			if c.want == "" && got != "" || c.want != "" && !strings.Contains(got, c.want) {
				t.Errorf("PinnedImageProblem(%q) = %q, want %q", c.image, got, c.want)
			}
		})
	}
}

// TestDefaultCIImages263 checks that the fallback images are the release's
// own toolchain images, pinned, and that OrDefault fills only what is empty.
func TestDefaultCIImages263(t *testing.T) {
	d := DefaultCIImages()
	for name, image := range map[string]string{"go": d.Go, "base": d.Base} {
		if want := ToolchainRegistry + "/" + name + ":" + ToolchainRelease; image != want {
			t.Errorf("the %s image is %q, want %q", name, image, want)
		}
		if problem := PinnedImageProblem(image); problem != "" {
			t.Error(problem)
		}
	}
	if got := (CIImages{}).OrDefault(); got != d {
		t.Errorf("CIImages{}.OrDefault() = %+v, want %+v", got, d)
	}
	own := CIImages{Go: "registry.example.com/go:1.0.0"}
	if got, want := own.OrDefault(), (CIImages{Go: own.Go, Base: d.Base}); got != want {
		t.Errorf("%+v.OrDefault() = %+v, want %+v", own, got, want)
	}
}
