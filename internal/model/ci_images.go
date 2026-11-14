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
	"slices"
	"strings"
)

// ToolchainRegistry is where a release of the factory publishes its images,
// the toolchain images base and go among them (.github/workflows/release.yml).
const ToolchainRegistry = "ghcr.io/jairjosafath/theos"

// ToolchainRelease is the factory release whose toolchain images a
// Standard's GitHub check falls back to when the repository's Actions
// variable names none (#263) and the operator was given no image
// (--ci-image-go, --ci-image-base: the chart's ciImages), as in a dev build
// or hack/render: a release that is published, never latest or a branch.
// hack/release.sh's bump sets it to the release it cuts and its check holds
// it there, and test/chart holds it equal to dist/chart/Chart.yaml's version
// (#284). Moving it changes what new scaffolds and Standard updates render;
// a repository scaffolded before keeps its image until its Standard's ref is
// bumped.
const ToolchainRelease = "0.32.0"

// CIImages are the toolchain images a CI job runs in when the CI system
// itself names none, one per toolchain: Go for the Standards whose own
// runners carry the go label, Base for those on base. A Standard's GitHub
// check runs in the image its Actions variable (THEOS_CI_IMAGE_GO or THEOS_CI_IMAGE_BASE)
// names, else in this one, so a repository without the operator, in a
// personal account, rendered by hack/render or adopted by hand, still runs
// its check. The operator takes them from --ci-image-go and --ci-image-base,
// the chart's ciImages, so they come from the registry the chart pulls
// from (#284); an image left empty is DefaultCIImages'.
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

// The image-reference grammar of the OCI distribution spec, which docker and
// the registries parse (#284): a name of lowercase path components, after an
// optional registry host with a port, then a tag, a digest or both. Nothing
// outside it, a quote, a space or a brace, can reach a workflow's quoted
// expression.
var (
	imageHost = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?` +
		`(?:\.[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?)*(?::[0-9]+)?$`)
	imagePathComponent = regexp.MustCompile(`^[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*$`)
	imageTag           = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)
	imageDigest        = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

// imageNameMax is the longest name, host included, a registry takes.
const imageNameMax = 255

// movingTags are tags a registry moves on to each new build or release, so
// an image named by one is not the same image twice.
var movingTags = []string{"latest", "stable", "nightly", "edge", "dev", "main", "master"}

// PinnedImageProblem says why image isn't pinned, or "" when it is: it must be
// an image reference (registry/name:tag, name@sha256:digest or
// name:tag@sha256:digest, the name in lowercase) that names a digest, or a
// tag other than one of movingTags.
func PinnedImageProblem(image string) string {
	notReference := fmt.Sprintf("%q is not an image reference such as registry/name:1.2.3", image)
	ref, digest, hasDigest := strings.Cut(image, "@")
	if hasDigest && !imageDigest.MatchString(digest) {
		return notReference
	}
	name, tag, hasTag := ref, "", false
	if colon := strings.LastIndex(ref, ":"); colon > strings.LastIndex(ref, "/") {
		name, tag, hasTag = ref[:colon], ref[colon+1:], true
		if !imageTag.MatchString(tag) {
			return notReference
		}
	}
	if name == "" || len(name) > imageNameMax {
		return notReference
	}
	path := name
	// The first component is a registry host when it has a dot or a port, or
	// is localhost, as docker reads it; else it is part of the path.
	if host, rest, ok := strings.Cut(name, "/"); ok && (strings.ContainsAny(host, ".:") || host == "localhost") {
		if !imageHost.MatchString(host) {
			return notReference
		}
		path = rest
	}
	for component := range strings.SplitSeq(path, "/") {
		if imagePathComponent.MatchString(component) {
			continue
		}
		if imagePathComponent.MatchString(strings.ToLower(component)) {
			return fmt.Sprintf("%q names the repository %s, which is not lowercase", image, path)
		}
		return notReference
	}
	if hasDigest {
		return ""
	}
	if !hasTag {
		return fmt.Sprintf("%q names no tag or digest: name a release tag or a digest", image)
	}
	if slices.Contains(movingTags, tag) {
		return fmt.Sprintf("%q names the tag %s, which moves: name a release tag or a digest", image, tag)
	}
	return ""
}
