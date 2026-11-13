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

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"sigs.k8s.io/controller-runtime/pkg/client"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

// gitProviderCI returns the CI system of a GitProvider's Git host: the host's
// own, since no GitProvider names another yet.
func gitProviderCI(gp *c4v1beta1.GitProvider) (model.CI, error) {
	ci, err := model.HostCI(string(gp.Spec.Provider))
	if err != nil {
		return model.CI{}, fmt.Errorf("GitProvider %s: %w", gp.Name, err)
	}
	return ci, nil
}

// adoptsBare reports whether the Container adopts its repository with no
// Standard: its checks are the repository's own, which the Container names
// (docs/plans/done/adopt-existing.md, step 2; #95).
func adoptsBare(container *c4v1beta1.Container) bool {
	return container.Spec.Adopt && container.Choices().Standard == ""
}

// adoptedChecks reports whether the default branch of a Container that
// adopts its repository with no Standard is protected by the repository's
// own checks: its Protected condition, which protectAdopted keeps, is True.
func adoptedChecks(container *c4v1beta1.Container) bool {
	return adoptsBare(container) && apimeta.IsStatusConditionTrue(container.Status.Conditions,
		c4v1beta1.ConditionProtected)
}

// containerCIOf is ci, a Git host's CI system, as the Container uses it: with
// the Container's own check contexts and the system's fallback workflow
// paths when it adopts its repository with no Standard (model.CI.ForAdopted).
func containerCIOf(ci model.CI, container *c4v1beta1.Container) model.CI {
	if adoptsBare(container) {
		return ci.ForAdopted(container.Spec.CheckContexts)
	}
	return ci
}

// containerCI returns the CI system the Container's repository runs.
func containerCI(ctx context.Context, c client.Reader, container *c4v1beta1.Container) (model.CI, error) {
	var gp c4v1beta1.GitProvider
	key := client.ObjectKey{Namespace: container.Namespace, Name: container.Spec.GitProviderRef.Name}
	if err := c.Get(ctx, key, &gp); err != nil {
		return model.CI{}, fmt.Errorf("GitProvider %s: %w", key.Name, err)
	}
	ci, err := gitProviderCI(&gp)
	return containerCIOf(ci, container), err
}

// protectedPaths returns the paths agents may not change in the Container: the
// platform's for its CI system and its Standard's.
func protectedPaths(ctx context.Context, c client.Reader, container *c4v1beta1.Container) ([]string, error) {
	_, paths, err := protection(ctx, c, container)
	return paths, err
}

// protection returns the Container's CI system and the paths agents may not
// change in it.
func protection(ctx context.Context, c client.Reader, container *c4v1beta1.Container) (model.CI, []string, error) {
	ci, err := containerCI(ctx, c, container)
	if err != nil {
		return model.CI{}, nil, err
	}
	var standard []string
	if name := container.Choices().Standard; name != "" {
		var s c4v1beta1.Standard
		if err := c.Get(ctx, client.ObjectKey{Namespace: container.Namespace, Name: name}, &s); err != nil {
			return model.CI{}, nil, fmt.Errorf("reading Standard %s: %w", name, err)
		}
		standard = s.Spec.ProtectedPaths
	}
	return ci, model.AgentProtectedPaths(ci, standard), nil
}

// protectRepository makes the Container's default branch take changes only
// through pull requests that pass its CI system's checks, leave the protected paths alone and
// are merged by the operator, and keeps release tags to the operator. It does
// nothing for a Container without a Standard, which has no checks to require,
// unless it adopts its repository and main reports the checks it names
// (adoptedChecks). It is safe to repeat, so it also brings older
// repositories up to date.
func protectRepository(ctx context.Context, c client.Reader, provider ports.GitHost,
	repo ports.Repository, container *c4v1beta1.Container) error {
	if !model.Protected(container.Choices().Standard != "", adoptedChecks(container)) {
		return nil
	}
	ci, paths, err := protection(ctx, c, container)
	if err != nil {
		return err
	}
	return protectWith(ctx, provider, repo, ci, paths)
}

// protectWith puts the rule on the default branch: ci's check contexts and
// paths, the paths no agent may change. Release tags stay the operator's.
func protectWith(ctx context.Context, provider ports.GitHost, repo ports.Repository, ci model.CI,
	paths []string) error {
	rule := ports.BranchRule{StatusContexts: ci.CheckContexts, ProtectedPaths: paths}
	if err := provider.ProtectBranch(ctx, repo, ports.DefaultBranch, rule); err != nil {
		return fmt.Errorf("protecting %s: %w", ports.DefaultBranch, err)
	}
	if err := provider.ProtectTags(ctx, repo, model.ReleaseTags); err != nil {
		return fmt.Errorf("protecting tags %s: %w", model.ReleaseTags, err)
	}
	return nil
}

// protectAdopted keeps the Protected condition of a Container that adopts
// its repository with no Standard, from contexts, the checks main's head
// reports by context. Once the head reports a check for each of the
// Container's contexts, it protects main with them and says so; while it
// doesn't, it protects nothing and says what the head reports instead
// (model.AdoptedChecks): a pattern nothing reports would block every merge,
// the people's too. Once main is protected it stays so, and the condition
// with it: the operator never takes a rule away, and a head that has no
// result yet, as after every merge until its run starts, says nothing
// against one. The checks are read again when the Container's spec changed,
// and the rule put again with what it now names.
func protectAdopted(ctx context.Context, c client.Reader, provider ports.GitHost, repo ports.Repository,
	container *c4v1beta1.Container, status *c4v1beta1.ContainerStatus, contexts map[string]string) error {
	was := apimeta.FindStatusCondition(status.Conditions, c4v1beta1.ConditionProtected)
	if was != nil && was.Status == metav1.ConditionTrue &&
		(was.ObservedGeneration == container.Generation || len(contexts) == 0) {
		return nil
	}
	ci, paths, err := protection(ctx, c, container)
	if err != nil {
		return err
	}
	ok, message := model.AdoptedChecks(ci.CheckContexts, contexts)
	if !ok {
		if was == nil || was.Status != metav1.ConditionFalse {
			logf.FromContext(ctx).Info("Left an adopted repository's default branch unprotected", "message", message)
		}
		setCondition(&status.Conditions, container.Generation, c4v1beta1.ConditionProtected, metav1.ConditionFalse,
			model.ReasonNoMatchingCheck, message)
		return nil
	}
	if err := protectWith(ctx, provider, repo, ci, paths); err != nil {
		return err
	}
	logf.FromContext(ctx).Info("Protected an adopted repository's default branch", "contexts", ci.CheckContexts,
		"paths", paths)
	setCondition(&status.Conditions, container.Generation, c4v1beta1.ConditionProtected, metav1.ConditionTrue,
		model.ReasonProtected, message)
	return nil
}

// unprotected says why the default branch of a Container that adopts its
// repository with no Standard isn't protected yet, for the Task and the Crew
// that wait for it: its Protected condition's message. Empty for a protected
// one and for any other Container.
func unprotected(container *c4v1beta1.Container) string {
	if !adoptsBare(container) || adoptedChecks(container) {
		return ""
	}
	if cond := apimeta.FindStatusCondition(container.Status.Conditions, c4v1beta1.ConditionProtected); cond != nil {
		return cond.Message
	}
	return "the operator hasn't read the checks of its main yet"
}
