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
	"fmt"
	"regexp"
	"strings"
)

// ToolchainRegistry is where a release of the factory publishes its images,
// the toolchain images base and go among them (.github/workflows/release.yml).
const ToolchainRegistry = "ghcr.io/jairjosafath/theos"

// ToolchainRelease is the factory release whose toolchain images a
// Standard's GitHub check falls back to when the repository's Actions
// variable names none (#263): a release that is published, never latest or
// a branch. Moving it changes what new scaffolds and Standard updates render;
// a repository scaffolded before keeps its image until its Standard's ref is
// bumped.
const ToolchainRelease = "0.32.0"

// CIImages are the toolchain images a CI job runs in when the CI system
// itself names none, one per toolchain: Go for the Standards whose own
// runners carry the go label, Base for those on base. A Standard's GitHub
// check runs in the image its Actions variable (THEOS_CI_IMAGE_GO or THEOS_CI_IMAGE_BASE)
// names, else in this one, so a repository without the operator, in a
// personal account, rendered by hack/render or adopted by hand, still runs
// its check.
type CIImages struct {
	Go   string
	Base string
}

// DefaultCIImages are ToolchainRelease's toolchain images on ToolchainRegistry.
func DefaultCIImages() CIImages {
	return CIImages{
		Go:   ToolchainRegistry + "/go:" + ToolchainRelease,
		Base: ToolchainRegistry + "/base:" + ToolchainRelease,
	}
}

// OrDefault fills each image left empty from DefaultCIImages.
func (c CIImages) OrDefault() CIImages {
	d := DefaultCIImages()
	if c.Go == "" {
		c.Go = d.Go
	}
	if c.Base == "" {
		c.Base = d.Base
	}
	return c
}

// pinnedImage is name:tag, name@sha256:digest or name:tag@sha256:digest, the
// name with no scheme and the reference with no quote or space, so it can
// sit in a workflow's quoted expression.
var pinnedImage = regexp.MustCompile(`^[a-z0-9][A-Za-z0-9._/:-]*[A-Za-z0-9_](@sha256:[0-9a-f]{64})?$`)

// PinnedImageProblem says why image isn't pinned, or "" when it is: it must
// name a tag or a digest, and a tag other than latest, dev or main, which
// move.
func PinnedImageProblem(image string) string {
	if !pinnedImage.MatchString(image) || strings.Contains(image, "//") {
		return fmt.Sprintf("%q is not an image reference such as registry/name:1.2.3", image)
	}
	name, digest, hasDigest := strings.Cut(image, "@")
	if hasDigest && digest != "" {
		return ""
	}
	slash := strings.LastIndex(name, "/")
	colon := strings.LastIndex(name, ":")
	if colon <= slash {
		return fmt.Sprintf("%q names no tag or digest: name a release tag or a digest", image)
	}
	switch tag := name[colon+1:]; tag {
	case "latest", "dev", "main", "master":
		return fmt.Sprintf("%q names the tag %s, which moves: name a release tag or a digest", image, tag)
	}
	return ""
}
