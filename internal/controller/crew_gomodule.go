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
	"io"
	"net/http"
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

// ConditionModulePublished says whether go get finds a Crew's last release's
// Go module where its path is read, and why not.
const ConditionModulePublished = "ModulePublished"

// Events of publishing a Go module.
const (
	reasonModulePublished    = "ModulePublished"
	reasonModuleInvalid      = "InvalidModule"
	reasonModuleNotFetchable = "ModuleNotFetchable"
)

// DefaultGoProxy is the Go proxy the operator asks unless told otherwise.
const DefaultGoProxy = "https://proxy.golang.org"

// goModuleTimeout bounds one publish: it downloads the tag's archive and
// uploads the module zip.
const goModuleTimeout = 5 * time.Minute

// goProxyTimeout bounds one question to the Go proxy, which may fetch the
// version from its origin before it answers.
const goProxyTimeout = time.Minute

// moduleAnswer is the Go proxy's last answer about a release's module.
type moduleAnswer struct {
	tag     string
	answer  model.ProxyAnswer
	detail  string
	checked time.Time
}

// publishGoModule publishes the Go module of the Crew's last release to the
// owner's Go package registry, once, and says whether go get finds it where
// its path is read, as model.DecideModule rules, asking the Go proxy when the
// rule says to. A repository with no go.mod and a module Go can't take at the
// tag are recorded, a failed upload is tried again on the next poll. An upload
// of a c4factory.local/ path, or the first time any other path turns
// fetchable, is a Normal Event; a module Go can't take, or can't fetch, a
// Warning Event, once per release.
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
	prev := release.GoModule
	if prev != nil && (prev.State == model.ModuleStateNotGo || prev.State == model.ModuleStateInvalid) {
		// Settled at the upload: the tag's go.mod doesn't change.
		reason := model.ModuleReasonNotGo
		value := metav1.ConditionTrue
		if prev.State == model.ModuleStateInvalid {
			reason, value = model.ModuleReasonInvalid, metav1.ConditionFalse
		}
		setCondition(&status.Conditions, crew.Generation, ConditionModulePublished, value, reason, prev.Message)
		return
	}
	m := prev
	registry, detail, uploadedNow := model.RegistryUploaded, "", false
	if m == nil || m.State == model.ModuleStateFailed {
		gitCtx, cancel := context.WithTimeout(ctx, goModuleTimeout)
		out, err := provider.PublishGoModule(gitCtx, repo, release.Tag)
		cancel()
		m = &c4v1alpha1.CrewReleaseGoModule{Path: out.Path, Version: release.Tag}
		switch {
		case err != nil:
			registry, detail = model.RegistryFailed, err.Error()
		case out.Outcome == gitprovider.GoModulePublished:
			uploadedNow = true
		case out.Outcome == gitprovider.GoModuleExists:
		case out.Outcome == gitprovider.GoModuleInvalid:
			registry, detail = model.RegistryInvalid, out.Reason
		default:
			registry = model.RegistryNoGoMod
		}
	}
	if registry == model.RegistryUploaded {
		r.readPrivate(ctx, crew, release)
	}
	facts := r.moduleFacts(crew, status, m, registry, detail)
	d := model.DecideModule(facts)
	asked := d.Ask
	if asked {
		facts.Answer, facts.AnswerDetail = r.askGoProxy(ctx, m.Path, m.Version)
		now := metav1.NewTime(r.now())
		facts.Checked = now.Time
		m.CheckedTime = &now
		r.moduleAnswers.Store(crew.UID, moduleAnswer{tag: release.Tag, answer: facts.Answer,
			detail: facts.AnswerDetail, checked: now.Time})
		d = model.DecideModule(facts)
	}
	m.State, m.Message = d.State, d.Message
	release.GoModule = m

	// The last poll's status write can fail after its Event, so the Reconciler
	// remembers what it said too.
	said := release.Tag + "\n" + d.State + "\n" + d.Reason
	if d.State == model.ModuleStateFailed {
		said += "\n" + d.Message
	}
	last, _ := r.moduleSaid.Load(crew.UID)
	if last != said {
		r.reportModule(ctx, crew, prev, m, d, uploadedNow || asked)
	}
	r.moduleSaid.Store(crew.UID, said)
	value := metav1.ConditionFalse
	if d.Fetchable {
		value = metav1.ConditionTrue
	}
	setCondition(&status.Conditions, crew.Generation, ConditionModulePublished, value, d.Reason, d.Message)
}

// reportModule logs a Go module's new state, with its Events: a Normal one
// when a factory path is uploaded or another path turns fetchable now, a
// Warning one when Go can't take it, and when it first turns not fetchable,
// once per release.
func (r *CrewReconciler) reportModule(ctx context.Context, crew *c4v1alpha1.Crew, prev,
	m *c4v1alpha1.CrewReleaseGoModule, d model.ModuleDecision, now bool) {
	log := logf.FromContext(ctx).WithValues("module", m.Path, "version", m.Version)
	switch d.State {
	case model.ModuleStateFailed:
		log.Error(fmt.Errorf("%s", d.Message), "Failed to publish the Go module")
	case model.ModuleStateInvalid:
		log.Info("Left a Go module unpublished", "message", d.Message)
		r.event(crew, corev1.EventTypeWarning, reasonModuleInvalid, "PublishGoModule", d.Message)
	case model.ModuleStatePublished:
		factory := strings.HasPrefix(m.Path, model.FactoryModulePrefix)
		if (factory && now) || (!factory && (now || prev == nil || prev.State != model.ModuleStatePublished)) {
			log.Info("Published Go module")
			r.event(crew, corev1.EventTypeNormal, reasonModulePublished, "PublishGoModule", d.Message)
		}
	case model.ModuleStateNotFetchable:
		log.Info("Go module not fetchable", "message", d.Message)
		if warned, _ := r.moduleWarned.Load(crew.UID); warned != m.Version &&
			(prev == nil || prev.State != model.ModuleStateNotFetchable) {
			r.event(crew, corev1.EventTypeWarning, reasonModuleNotFetchable, "PublishGoModule", d.Message)
		}
		r.moduleWarned.Store(crew.UID, m.Version)
	case model.ModuleStateWaiting:
		log.Info("Go module waits", "reason", d.Reason, "message", d.Message)
	}
}

// moduleFacts is what the Crew knows of its last release's module m: the
// registry's outcome, its GitHub target and source, the Published condition
// and the Go proxy's last answer. A checked version whose answer the Reconciler
// doesn't remember, since it restarted, is asked again, unless it was found.
func (r *CrewReconciler) moduleFacts(crew *c4v1alpha1.Crew, status *c4v1alpha1.CrewStatus,
	m *c4v1alpha1.CrewReleaseGoModule, registry model.ModuleRegistry, detail string) model.ModuleFacts {
	release := status.LastRelease
	f := model.ModuleFacts{Path: m.Path, Version: m.Version, Registry: registry, RegistryDetail: detail,
		Source: publishSource(crew), Proxy: r.GoProxy, Now: r.now()}
	if spec := crew.Spec.PublishReleases; spec != nil {
		f.Repository = spec.GitHub.Repository
	}
	if p := release.Publish; p != nil && p.Source != nil {
		f.SourceTag, f.Private = p.Source.Tag, p.Source.Private != nil && *p.Source.Private
	}
	if c := apimeta.FindStatusCondition(status.Conditions, ConditionPublished); c != nil &&
		c.Status == metav1.ConditionFalse {
		f.PublishedReason, f.PublishedMessage = c.Reason, c.Message
	}
	if a, ok := r.moduleAnswers.Load(crew.UID); ok && a.(moduleAnswer).tag == release.Tag &&
		(m.CheckedTime != nil || m.State != model.ModuleStatePublished) {
		// Remembered even when the status write that records it failed; a
		// Published never checked is an older factory's, checked again.
		a := a.(moduleAnswer)
		f.Answer, f.AnswerDetail, f.Checked = a.answer, a.detail, a.checked
	} else if m.CheckedTime != nil && m.State == model.ModuleStatePublished {
		f.Answer, f.Checked = model.ProxyFound, m.CheckedTime.Time
	}
	return f
}

// readPrivate reads whether the Crew's GitHub target is private for a release
// whose source went out before the operator recorded it, once: the module
// rule needs it, and the publish, done, reads GitHub no more. A failed read
// leaves it unknown, read as public, and is tried again on the next poll.
func (r *CrewReconciler) readPrivate(ctx context.Context, crew *c4v1alpha1.Crew, release *c4v1alpha1.CrewRelease) {
	spec := crew.Spec.PublishReleases
	if spec == nil || release.Publish == nil || release.Publish.Source == nil ||
		release.Publish.Source.Private != nil {
		return
	}
	log := logf.FromContext(ctx)
	token, err := r.publishToken(ctx, crew.Namespace, spec.GitHub.TokenSecretRef.Name, spec.GitHub.Repository)
	if err != nil || token == "" {
		return
	}
	target, err := r.releaseTargets().GitHubReleases(gitprovider.GitHubConfig{Repository: spec.GitHub.Repository,
		Token: token})
	if err != nil {
		return
	}
	read, err := target.Repository(ctx)
	if err != nil {
		log.Info("Could not read whether the release target is private", "repository", spec.GitHub.Repository,
			"error", err.Error())
		return
	}
	release.Publish.Source.Private = new(read.Private)
}

// askGoProxy asks the Go proxy for a module version's .info, as go get does,
// which also makes the proxy fetch and keep it.
func (r *CrewReconciler) askGoProxy(ctx context.Context, path, version string) (model.ProxyAnswer, string) {
	url, err := model.ModuleProxyURL(r.GoProxy, path, version)
	if err != nil {
		return model.ProxyUnreachable, err.Error()
	}
	ctx, cancel := context.WithTimeout(ctx, goProxyTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return model.ProxyUnreachable, err.Error()
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return model.ProxyUnreachable, err.Error()
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	return model.ProxyAnswerOf(resp.StatusCode, string(body))
}
