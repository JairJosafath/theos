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
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/gitprovider"
)

// ConditionModulePublished says whether a Crew's last release's Go module is in
// the Git host's package registry, and why not.
const ConditionModulePublished = "ModulePublished"

// Reasons and Events of publishing a Go module.
const (
	reasonModulePublished  = "ModulePublished"
	reasonModuleNotGo      = "NotGo"
	reasonModuleInvalid    = "InvalidModule"
	reasonModuleGitFailure = "GitHostUnavailable"
)

// States of status.lastRelease.goModule.
const (
	moduleStatePublished = "Published"
	moduleStateNotGo     = "NotGo"
	moduleStateInvalid   = "Invalid"
	moduleStateFailed    = "Failed"
)

// goModuleTimeout bounds one publish: it downloads the tag's archive and
// uploads the module zip.
const goModuleTimeout = 5 * time.Minute

// publishGoModule publishes the Go module of the Crew's last release to the
// owner's Go package registry, once: a repository with no go.mod and a module Go
// can't take at the tag are recorded, a failure is tried again on the next poll.
// An upload is a Normal Event, a module Go can't take a Warning Event, once per
// release.
func (r *CrewReconciler) publishGoModule(ctx context.Context, crew *c4v1alpha1.Crew, status *c4v1alpha1.CrewStatus,
	provider gitprovider.Provider, repo gitprovider.Repository) {
	release := status.LastRelease
	if release == nil {
		return
	}
	if tag, ok := r.tagged.Load(crew.UID); ok && tag != release.Tag {
		// The cached Crew predates the newer release.
		return
	}
	if m := release.GoModule; m != nil && m.State != moduleStateFailed {
		r.setModuleCondition(crew, status, m)
		return
	}
	log := logf.FromContext(ctx)
	gitCtx, cancel := context.WithTimeout(ctx, goModuleTimeout)
	defer cancel()
	out, err := provider.PublishGoModule(gitCtx, repo, release.Tag)
	m := &c4v1alpha1.CrewReleaseGoModule{Path: out.Path, Version: release.Tag}
	// The last poll's status write can fail after its Event, so the Reconciler
	// remembers what it said too.
	said := release.Tag + "\n" + string(out.Outcome)
	last, _ := r.moduleSaid.Load(crew.UID)
	switch {
	case err != nil:
		m.State, m.Message = moduleStateFailed, fmt.Sprintf("Could not publish the Go module of %s: %v", release.Tag, err)
		said += "\n" + err.Error()
		if last != said {
			log.Error(err, "Failed to publish the Go module", "tag", release.Tag)
		}
	case out.Outcome == gitprovider.GoModulePublished || out.Outcome == gitprovider.GoModuleExists:
		m.State, m.Message = moduleStatePublished, fmt.Sprintf("Published %s %s", out.Path, release.Tag)
		if out.Outcome == gitprovider.GoModulePublished && last != said {
			log.Info("Published Go module", "module", out.Path, "version", release.Tag)
			r.event(crew, corev1.EventTypeNormal, reasonModulePublished, "PublishGoModule", m.Message)
		}
	case out.Outcome == gitprovider.GoModuleInvalid:
		m.State = moduleStateInvalid
		m.Message = fmt.Sprintf("Did not publish %s %s: %s", out.Path, release.Tag, out.Reason)
		if last != said {
			log.Info("Left a Go module unpublished", "module", out.Path, "version", release.Tag, "reason", out.Reason)
			r.event(crew, corev1.EventTypeWarning, reasonModuleInvalid, "PublishGoModule", m.Message)
		}
	default:
		m.State = moduleStateNotGo
		m.Message = fmt.Sprintf("%s has no go.mod at the repository's root; no Go module to publish", release.Tag)
	}
	r.moduleSaid.Store(crew.UID, said)
	release.GoModule = m
	r.setModuleCondition(crew, status, m)
}

// setModuleCondition says in the ModulePublished condition how publishing the
// last release's Go module went.
func (r *CrewReconciler) setModuleCondition(crew *c4v1alpha1.Crew, status *c4v1alpha1.CrewStatus,
	m *c4v1alpha1.CrewReleaseGoModule) {
	value, reason := metav1.ConditionFalse, reasonModuleGitFailure
	switch m.State {
	case moduleStatePublished:
		value, reason = metav1.ConditionTrue, reasonModulePublished
	case moduleStateNotGo:
		value, reason = metav1.ConditionTrue, reasonModuleNotGo
	case moduleStateInvalid:
		reason = reasonModuleInvalid
	}
	setCondition(&status.Conditions, crew.Generation, ConditionModulePublished, value, reason, m.Message)
}
