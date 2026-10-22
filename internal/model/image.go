/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package model

// The states of a release's image, Container.status.image.state
// (docs/plans/images.md, decision 8).
const (
	// ImageStatePublished means the owner's registry has the image under the
	// release's tag.
	ImageStatePublished = "Published"
	// ImageStateNoImage means the release has no image archive.
	ImageStateNoImage = "NoImage"
	// ImageStateInvalid means the release's archive can't be pushed.
	ImageStateInvalid = "Invalid"
	// ImageStateTagTaken means the tag names another digest in the registry.
	ImageStateTagTaken = "TagTaken"
	// ImageStateFailed means the Git host couldn't be reached; it is tried
	// again each poll.
	ImageStateFailed = "Failed"
)

// The Events a release's image says, once per release.
const (
	ImageEventPublished = "ImagePublished"
	ImageEventInvalid   = "InvalidImage"
	ImageEventTagTaken  = "ImageTagTaken"
)

// DeploymentK8sManifests is the catalog Deployment whose release builds an
// image and writes deploy/k8s/, what an argoCD target ships.
const DeploymentK8sManifests = "k8s-manifests"

// imageBuilders are the catalog choices whose release builds an image: a
// Deployment, and the Container type it does so for, "" for any.
var imageBuilders = []struct{ deployment, typ string }{
	{DeploymentK8sManifests, ""},
	{"gitea-release", TypeOperator},
}

// BuildsImage reports whether a release of a Container with Deployment
// deployment and type typ builds an image the operator pushes.
func BuildsImage(deployment, typ string) bool {
	for _, b := range imageBuilders {
		if b.deployment == deployment && (b.typ == "" || b.typ == typ) {
			return true
		}
	}
	return false
}

// ImageSettled reports whether a release's image state stays as it is: every
// state but Failed, which is tried again.
func ImageSettled(state string) bool { return state != ImageStateFailed }

// ImageEvent is the Event a release's image says when its state turns state,
// from prevState of the same release, or of none when prevTag is another
// release: "" for none, and whether it is a Warning.
func ImageEvent(prevTag, prevState, tag, state string) (reason string, warning bool) {
	if prevTag == tag && prevState == state {
		return "", false
	}
	switch state {
	case ImageStatePublished:
		return ImageEventPublished, false
	case ImageStateInvalid:
		return ImageEventInvalid, true
	case ImageStateTagTaken:
		return ImageEventTagTaken, true
	}
	return "", false
}
