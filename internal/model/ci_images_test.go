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
