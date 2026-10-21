/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"context"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/gitprovider"
	"github.com/JairJosafath/c4factory/internal/model"
)

// imageTimeout bounds one image push: it streams the release's whole archive
// to the registry.
const imageTimeout = 30 * time.Minute

// buildsImage reports whether the Container's catalog choice builds an image
// on release.
func buildsImage(container *c4v1alpha1.Container) bool {
	return model.BuildsImage(refName(container.Spec.DeploymentRef), string(container.Spec.Type))
}

// publishImage pushes the image of the Container's newest release to the
// owner's container registry, once, when its Deployment builds one, and
// reports it in status.image and the condition ImagePublished. A push the
// Git host failed is tried again at each poll; it returns how soon, zero
// when nothing waits.
func (r *ContainerReconciler) publishImage(ctx context.Context, container *c4v1alpha1.Container,
	status *c4v1alpha1.ContainerStatus) time.Duration {
	tag := status.NewestRelease
	if !buildsImage(container) || tag == "" {
		status.Image = nil
		apimeta.RemoveStatusCondition(&status.Conditions, c4v1alpha1.ConditionImagePublished)
		return 0
	}
	prev := status.Image
	if prev == nil || prev.Tag != tag || !model.ImageSettled(prev.State) {
		status.Image = r.pushedImage(ctx, container, prev, tag)
	}
	img := status.Image
	value := metav1.ConditionFalse
	if img.State == model.ImageStatePublished {
		value = metav1.ConditionTrue
	}
	setCondition(&status.Conditions, container.Generation, c4v1alpha1.ConditionImagePublished, value, img.State,
		img.Message)
	if !model.ImageSettled(img.State) {
		return r.deployPoll()
	}
	return 0
}

// pushedImage is release tag's image for a Container whose status.image,
// prev, does not report it: the push this Reconciler last made when status
// has not caught up with it (the cache can lag a status write by a
// reconcile), else a new push, reported.
func (r *ContainerReconciler) pushedImage(ctx context.Context, container *c4v1alpha1.Container,
	prev *c4v1alpha1.ContainerImageStatus, tag string) *c4v1alpha1.ContainerImageStatus {
	if last, ok := r.imagePushed.Load(container.UID); ok {
		img := last.(*c4v1alpha1.ContainerImageStatus)
		behind := prev == nil || prev.Tag != tag || prev.State != img.State || prev.Message != img.Message
		if img.Tag == tag && behind {
			return img.DeepCopy()
		}
	}
	img := r.pushImage(ctx, container, tag)
	r.imagePushed.Store(container.UID, img.DeepCopy())
	r.reportImage(ctx, container, prev, img)
	return img
}

// pushImage pushes release tag's image archive and says how it went.
func (r *ContainerReconciler) pushImage(ctx context.Context, container *c4v1alpha1.Container,
	tag string) *c4v1alpha1.ContainerImageStatus {
	img := &c4v1alpha1.ContainerImageStatus{Tag: tag, State: model.ImageStateFailed}
	provider, err := providerFor(ctx, r.Client, r.APIReader, r.Git, container.Namespace,
		container.Spec.GitProviderRef.Name)
	if err != nil {
		img.Message = err.Error()
		return img
	}
	ctx, cancel := context.WithTimeout(ctx, imageTimeout)
	defer cancel()
	out, err := provider.PublishImage(ctx, provider.Repository(container.Name), tag)
	img.Repository, img.Digest, img.Platforms = out.Repository, out.Digest, out.Platforms
	switch {
	case err != nil:
		img.Message = err.Error()
	case out.Outcome == gitprovider.ImagePublished:
		now := metav1.Now()
		img.State, img.PublishedTime = model.ImageStatePublished, &now
		img.Message = fmt.Sprintf("%s:%s is %s, for %s.", out.Repository, tag, out.Digest,
			strings.Join(out.Platforms, ", "))
	case out.Outcome == gitprovider.ImageInvalid:
		img.State, img.Message = model.ImageStateInvalid, out.Reason
	case out.Outcome == gitprovider.ImageTagTaken:
		img.State, img.Message = model.ImageStateTagTaken, out.Reason
	default:
		img.State = model.ImageStateNoImage
		img.Message = fmt.Sprintf("Release %s has no image archive (*%s) to push.", tag, model.ImageArchiveSuffix)
	}
	return img
}

// reportImage logs a release's new image state, with its Event, once per
// release: the Reconciler remembers what it said too, as the last poll's
// status write can fail after its Event.
func (r *ContainerReconciler) reportImage(ctx context.Context, container *c4v1alpha1.Container,
	prev, img *c4v1alpha1.ContainerImageStatus) {
	log := logf.FromContext(ctx).WithValues("tag", img.Tag)
	switch img.State {
	case model.ImageStatePublished:
		log.Info("Published image", "repository", img.Repository, "digest", img.Digest)
	case model.ImageStateFailed:
		log.Error(fmt.Errorf("%s", img.Message), "Failed to publish the image")
	default:
		log.Info("Left an image unpublished", "state", img.State, "message", img.Message)
	}
	var prevTag, prevState string
	if prev != nil {
		prevTag, prevState = prev.Tag, prev.State
	}
	reason, warning := model.ImageEvent(prevTag, prevState, img.Tag, img.State)
	said := img.Tag + "\n" + img.State
	if last, ok := r.imageSaid.Load(container.UID); reason == "" || ok && last == said {
		return
	}
	r.imageSaid.Store(container.UID, said)
	eventType := corev1.EventTypeNormal
	if warning {
		eventType = corev1.EventTypeWarning
	}
	if r.Recorder != nil {
		r.Recorder.Eventf(container, nil, eventType, reason, "PublishImage", "%s", img.Message)
	}
}

// shippedImage is the image of version, <repository>@<digest>: status.image's
// when it is version's, else the tag resolved in the owner's registry, ""
// when there is none.
func (p *deployPass) shippedImage(ctx context.Context, version string) (string, error) {
	if img := p.image; img != nil && img.Tag == version && img.State == model.ImageStatePublished &&
		img.Digest != "" {
		return img.Repository + "@" + img.Digest, nil
	}
	if p.repo.ImageRegistry == "" {
		return "", nil
	}
	digest, err := p.provider.ImageDigest(ctx, p.repo, version)
	if err != nil || digest == "" {
		return "", err
	}
	return p.repo.ImageRegistry + "/" + strings.ToLower(p.repo.Name) + "@" + digest, nil
}
