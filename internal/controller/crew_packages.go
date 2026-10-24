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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	c4v1beta1 "github.com/JairJosafath/c4factory/api/v1beta1"
	"github.com/JairJosafath/c4factory/internal/gitprovider"
	"github.com/JairJosafath/c4factory/internal/model"
)

// ConditionPackagesPublished says whether every npm and PyPI package file of a
// Crew's last release is in its registry, and why not.
const ConditionPackagesPublished = "PackagesPublished"

// Events of publishing a release's packages, and the action of the Warning.
const (
	reasonPackagePublished = "PackagePublished"
	reasonPackageInvalid   = "InvalidPackage"
	actionPublishPackages  = "PublishReleasePackages"
)

// packageAction is the action of an upload's Event. The recorder folds Events
// of one reason and action into a series, keeping the first note, so each
// ecosystem's upload is its own action and its Event names its package.
func packageAction(ecosystem string) string {
	return "Publish" + strings.ToUpper(ecosystem[:1]) + ecosystem[1:] + "Package"
}

// packagesTimeout bounds one publish: it downloads each package file of the
// release and uploads it to its registry.
const packagesTimeout = 5 * time.Minute

// publishPackages publishes the npm tarballs, wheels and sdists the Crew's
// last release attached to the registries the GitProvider names, once the
// release runs that built them succeeded, and says how it went, as
// model.DecidePackages rules. A release with no package file and a file that
// can't be published at the tag's version are settled; a host that can't be
// reached is tried again on the next poll. Each upload is a Normal Event and
// a log line, with the ecosystem as its action (PublishNpmPackage,
// PublishPyPIPackage); a file left unpublished is a Warning Event, once per
// release.
func (r *CrewReconciler) publishPackages(ctx context.Context, crew *c4v1beta1.Crew, status *c4v1beta1.CrewStatus,
	provider gitprovider.Provider, repo gitprovider.Repository) {
	release := status.LastRelease
	if release == nil {
		return
	}
	if tag, ok := r.tagged.Load(crew.UID); ok && tag != release.Tag {
		// The cached Crew predates the newer release.
		return
	}
	prev := release.Packages
	if prev != nil && prev.State != model.PackagesStateWaiting && prev.State != model.PackagesStateFailed {
		// Settled at the publish: the release's files don't change.
		reason, value := model.PackagesReasonPublished, metav1.ConditionTrue
		switch prev.State {
		case model.PackagesStateNoPackage:
			reason = model.PackagesReasonNoPackage
		case model.PackagesStateInvalid:
			reason, value = model.PackagesReasonInvalid, metav1.ConditionFalse
		}
		setCondition(&status.Conditions, crew.Generation, ConditionPackagesPublished, value, reason, prev.Message)
		return
	}
	facts := model.PackagesFacts{Tag: release.Tag, Run: release.Run}
	var uploaded []model.ReleasePackage
	if release.Run == model.RunSucceeded {
		gitCtx, cancel := context.WithTimeout(ctx, packagesTimeout)
		out, err := provider.PublishReleasePackages(gitCtx, repo, release.Tag)
		cancel()
		if err != nil {
			facts.Failure = err.Error()
		}
		for _, p := range out.Packages {
			pkg := model.ReleasePackage{Outcome: string(p.Outcome), Ecosystem: p.Ecosystem, Name: p.Name,
				Version: p.Version, File: p.File, Reason: p.Reason}
			facts.Packages = append(facts.Packages, pkg)
			if p.Outcome == gitprovider.PackagePublished {
				uploaded = append(uploaded, pkg)
			}
		}
	}
	d := model.DecidePackages(facts)
	packages := &c4v1beta1.CrewReleasePackages{State: d.State, Message: d.Message}
	for _, p := range d.Published {
		packages.Published = append(packages.Published, c4v1beta1.CrewReleasePackage{Ecosystem: p.Ecosystem,
			Name: p.Name, Version: p.Version})
	}
	release.Packages = packages

	// The last poll's status write can fail after its Event, so the Reconciler
	// remembers what it said too.
	said := release.Tag + "\n" + d.State + "\n" + d.Message
	last, _ := r.packagesSaid.Load(crew.UID)
	if last != said || len(uploaded) > 0 {
		r.reportPackages(ctx, crew, release.Tag, d, uploaded)
	}
	r.packagesSaid.Store(crew.UID, said)
	value := metav1.ConditionFalse
	if d.Done {
		value = metav1.ConditionTrue
	}
	setCondition(&status.Conditions, crew.Generation, ConditionPackagesPublished, value, d.Reason, d.Message)
}

// reportPackages logs a release's packages' new state, with its Events: a
// Normal one per package uploaded now, and a Warning one when a file can't be
// published at the tag's version, once per release.
func (r *CrewReconciler) reportPackages(ctx context.Context, crew *c4v1beta1.Crew, tag string,
	d model.PackagesDecision, uploaded []model.ReleasePackage) {
	log := logf.FromContext(ctx).WithValues("tag", tag)
	for _, p := range uploaded {
		log.Info("Published package", "ecosystem", p.Ecosystem, "name", p.Name, "version", p.Version, "file", p.File)
		r.event(crew, corev1.EventTypeNormal, reasonPackagePublished, packageAction(p.Ecosystem),
			fmt.Sprintf("Published %s %s %s", p.Ecosystem, p.Name, p.Version))
	}
	switch d.State {
	case model.PackagesStateFailed:
		log.Error(fmt.Errorf("%s", d.Message), "Failed to publish the release's packages")
	case model.PackagesStateInvalid:
		log.Info("Left a package unpublished", "message", d.Message)
		if warned, _ := r.packagesWarned.Load(crew.UID); warned != tag {
			r.event(crew, corev1.EventTypeWarning, reasonPackageInvalid, actionPublishPackages, d.Message)
		}
		r.packagesWarned.Store(crew.UID, tag)
	case model.PackagesStateWaiting:
		log.Info("Release packages wait", "reason", d.Reason, "message", d.Message)
	case model.PackagesStateNoPackage:
		log.Info("Release has no package", "message", d.Message)
	}
}
