/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package model

import "golang.org/x/mod/semver"

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
	// ImageStatePushing means the operator is pushing the archive; the push
	// runs beside the Container's reconciles, which it never holds (#1572).
	ImageStatePushing = "Pushing"
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

// DeploymentGiteaRelease is the catalog Deployment that attaches make dist's
// files to a Gitea release; the one Deployment a Framework that deploys
// itself takes, its release carrying the cloud assembly.
const DeploymentGiteaRelease = "gitea-release"

// FrameworkAWSFargate is the catalog Framework that deploys a service to
// ECS on Fargate; its release builds the service's image, which its stack
// takes by digest (docs/plans/aws-deploy.md, decision 8).
const FrameworkAWSFargate = "aws-fargate-ts"

// FargateImageMinRef is the first aws-fargate-ts tag whose release workflow
// builds the image (#1610, with gitea-release v1.7.0). A Container
// scaffolded from an earlier tag never builds one: scaffold once means its
// workflow stays as it was rendered (#1797).
const FargateImageMinRef = "v2.0.0"

// imageBuilders are the catalog choices whose release builds an image: a
// Deployment, and the Framework and Container type it does so for, "" for
// any; and, for a Framework that only came to build one at a tag, that
// tag, which the Container's scaffolded ref must reach.
var imageBuilders = []struct{ deployment, framework, typ, minRef string }{
	{DeploymentK8sManifests, "", "", ""},
	{DeploymentGiteaRelease, "", TypeOperator, ""},
	{DeploymentGiteaRelease, FrameworkAWSFargate, "", FargateImageMinRef},
}

// BuildsImage reports whether a release of a Container with Deployment
// deployment, Framework framework and type typ builds an image the operator
// pushes. scaffoldedFrom is the Container's status.scaffoldedFrom, name@ref
// per template: the scaffolded ref decides, not the catalog's current pin,
// as scaffold once leaves the repository's release workflow as it was
// rendered (#1797). A Framework ref that is not a semver tag (a commit), or
// none, counts as current.
func BuildsImage(deployment, framework, typ string, scaffoldedFrom []string) bool {
	for _, b := range imageBuilders {
		if b.deployment != deployment || (b.framework != "" && b.framework != framework) ||
			(b.typ != "" && b.typ != typ) {
			continue
		}
		if b.minRef != "" {
			if ref := ScaffoldedRef(scaffoldedFrom, b.framework); semver.IsValid(ref) && semver.Compare(ref, b.minRef) < 0 {
				continue
			}
		}
		return true
	}
	return false
}

// ImageSettled reports whether a release's image state stays as it is: every
// state but Pushing, which ends, and Failed, which is tried again.
func ImageSettled(state string) bool { return state != ImageStatePushing && state != ImageStateFailed }

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
