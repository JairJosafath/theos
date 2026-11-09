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

package controller

import (
	"context"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

// imageTimeout bounds one image push: it streams the release's whole archive
// to the registry, beside the Container's reconciles.
const imageTimeout = 30 * time.Minute

// buildsImage reports whether the Container's catalog choice builds an image
// on release, at the refs it was scaffolded from (#1797).
func buildsImage(container *c4v1beta1.Container) bool {
	return model.BuildsImage(container.Choices().Deployment, container.Choices().Framework,
		string(container.Spec.Type), container.Status.ScaffoldedFrom)
}

// publishImage pushes the image of the Container's newest release to the
// owner's container registry, once, when its Deployment builds one, and
// reports it in status.image and the condition ImagePublished. A push the
// Git host failed is tried again at each poll; it returns how soon, zero
// when nothing waits.
func (r *ContainerReconciler) publishImage(ctx context.Context, container *c4v1beta1.Container,
	status *c4v1beta1.ContainerStatus) time.Duration {
	tag := status.NewestRelease
	if !buildsImage(container) || tag == "" {
		status.Image = nil
		apimeta.RemoveStatusCondition(&status.Conditions, c4v1beta1.ConditionImagePublished)
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
	setCondition(&status.Conditions, container.Generation, c4v1beta1.ConditionImagePublished, value, img.State,
		img.Message)
	if !model.ImageSettled(img.State) {
		return r.deployPoll()
	}
	return 0
}

// imagePush is a Container's image push in flight: the release it pushes
// and how to stop it.
type imagePush struct {
	tag    string
	cancel context.CancelFunc
}

// pushedImage is release tag's image for a Container whose status.image,
// prev, does not report it: Pushing while this Reconciler pushes one, the
// push it last made when status has not caught up with it (the cache can lag
// a status write by a reconcile), else Pushing as it starts a new push. The
// push runs beside the reconcile, so a slow registry holds no Container's
// reconcile (#1572), and its end wakes the Container.
func (r *ContainerReconciler) pushedImage(ctx context.Context, container *c4v1beta1.Container,
	prev *c4v1beta1.ContainerImageStatus, tag string) *c4v1beta1.ContainerImageStatus {
	if push, ok := r.imagePushing.Load(container.UID); ok {
		return pushingImage(tag, push.(*imagePush).tag)
	}
	if last, ok := r.imagePushed.Load(container.UID); ok {
		img := last.(*c4v1beta1.ContainerImageStatus)
		behind := prev == nil || prev.Tag != tag || prev.State != img.State || prev.Message != img.Message
		if img.Tag == tag && behind {
			return img.DeepCopy()
		}
	}
	r.startPush(ctx, container.DeepCopy(), prev.DeepCopy(), tag)
	return pushingImage(tag, tag)
}

// pushingImage is release tag's image while the push of release pushing
// runs.
func pushingImage(tag, pushing string) *c4v1beta1.ContainerImageStatus {
	img := &c4v1beta1.ContainerImageStatus{Tag: tag, State: model.ImageStatePushing,
		Message: fmt.Sprintf("Pushing the image of release %s.", tag)}
	if pushing != tag {
		img.Message = fmt.Sprintf("Waiting for the push of release %s's image to end.", pushing)
	}
	return img
}

// startPush pushes release tag's image in a goroutine of its own, bounded by
// imageTimeout and stopped when the Container is forgotten. Its end remembers
// the image, reports it and wakes the Container, whose reconcile writes it.
func (r *ContainerReconciler) startPush(ctx context.Context, container *c4v1beta1.Container,
	prev *c4v1beta1.ContainerImageStatus, tag string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), imageTimeout)
	push := &imagePush{tag: tag, cancel: cancel}
	r.imagePushing.Store(container.UID, push)
	go func() {
		defer cancel()
		img := r.pushImage(ctx, container, tag)
		if cur, ok := r.imagePushing.Load(container.UID); !ok || cur != push {
			return
		}
		r.imagePushed.Store(container.UID, img.DeepCopy())
		r.imagePushing.CompareAndDelete(container.UID, push)
		r.reportImage(ctx, container, prev, img)
		if r.imagePushEnded != nil {
			select {
			case r.imagePushEnded <- event.GenericEvent{Object: container}:
			default:
			}
		}
	}()
}

// pushImage pushes release tag's image archive and says how it went.
func (r *ContainerReconciler) pushImage(ctx context.Context, container *c4v1beta1.Container,
	tag string) *c4v1beta1.ContainerImageStatus {
	img := &c4v1beta1.ContainerImageStatus{Tag: tag, State: model.ImageStateFailed}
	provider, err := providerFor(ctx, r.Client, r.APIReader, r.Git, container.Namespace,
		container.Spec.GitProviderRef.Name)
	if err != nil {
		img.Message = err.Error()
		return img
	}
	out, err := provider.PublishImage(ctx, provider.Repository(container.Name), tag)
	img.Repository, img.Digest, img.Platforms = out.Repository, out.Digest, out.Platforms
	switch {
	case err != nil:
		img.Message = err.Error()
	case out.Outcome == ports.ImagePublished:
		now := metav1.Now()
		img.State, img.PublishedTime = model.ImageStatePublished, &now
		img.Message = fmt.Sprintf("%s:%s is %s, for %s.", out.Repository, tag, out.Digest,
			strings.Join(out.Platforms, ", "))
	case out.Outcome == ports.ImageInvalid:
		img.State, img.Message = model.ImageStateInvalid, out.Reason
	case out.Outcome == ports.ImageTagTaken:
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
func (r *ContainerReconciler) reportImage(ctx context.Context, container *c4v1beta1.Container,
	prev, img *c4v1beta1.ContainerImageStatus) {
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
// when version is its tag, else (a pin) the tag resolved in the owner's
// registry, "" when there is none. The newest release's image in a settled
// state other than Published ships "": its ImagePublished condition says why,
// and the registry is not asked, as the tag may name another image
// (TagTaken) or an older release's. A Failed push fails the ship, retried at
// the next poll like the push (#1570), and so does one still Pushing.
func (p *deployPass) shippedImage(ctx context.Context, version string) (string, error) {
	if img := p.image; img != nil && img.Tag == version {
		switch img.State {
		case model.ImageStatePublished:
			return img.Repository + "@" + img.Digest, nil
		case model.ImageStatePushing, model.ImageStateFailed:
			return "", fmt.Errorf("the image of %s is not published yet: %s", version, img.Message)
		}
		return "", nil
	}
	repository := p.repo.ImageRepository()
	if repository == "" {
		return "", nil
	}
	digest, err := p.provider.ImageDigest(ctx, p.repo, version)
	if err != nil || digest == "" {
		return "", err
	}
	return repository + "@" + digest, nil
}
