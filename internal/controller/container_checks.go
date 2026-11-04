/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of theos, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

// defaultContainerChecksRecheck is how often a Ready Container reads its checks
// again. The Git host doesn't call back, so this is polled.
const defaultContainerChecksRecheck = time.Minute

func (r *ContainerReconciler) checksRecheck() time.Duration {
	if r.ChecksRecheck > 0 {
		return r.ChecksRecheck
	}
	return defaultContainerChecksRecheck
}

// refreshChecks reads the latest CI result on the default branch into status.
// A Git error keeps the last known result: checks are a report, not a step.
func (r *ContainerReconciler) refreshChecks(ctx context.Context, container *c4v1beta1.Container,
	status *c4v1beta1.ContainerStatus) ctrl.Result {
	if !model.Protected(container.Choices().Standard != "") {
		return ctrl.Result{}
	}
	provider, err := providerFor(ctx, r.Client, r.APIReader, r.Git, container.Namespace, container.Spec.GitProviderRef.Name)
	if err == nil {
		var checks ports.CheckStatus
		gitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		checks, err = provider.CombinedStatus(gitCtx, provider.Repository(container.Name), ports.DefaultBranch)
		cancel()
		if err == nil {
			status.Checks = &c4v1beta1.CheckStatus{Commit: checks.Commit, State: checks.State, URL: checks.URL}
		}
	}
	if err != nil {
		logf.FromContext(ctx).Info("Could not read checks", "error", err.Error())
	}
	return ctrl.Result{RequeueAfter: r.checksRecheck()}
}

// providerFor returns the Provider for a GitProvider in namespace ns, with its
// credentials read straight from the API server.
func providerFor(ctx context.Context, c client.Client, apiReader client.Reader, git GitClient,
	ns, name string) (interface {
	ports.GitHost
	ports.Issues
	ports.CI
	ports.AgentAccess
	ports.Registry
}, error) {
	access, err := gitAccessFor(ctx, c, apiReader, git, ns, name)
	return access.provider, err
}

// gitAccess is a GitProvider, its credentials, the Provider built from them
// and its CI system.
type gitAccess struct {
	gitProvider *c4v1beta1.GitProvider
	creds       ports.Credentials
	provider    interface {
		ports.GitHost
		ports.Issues
		ports.CI
		ports.AgentAccess
		ports.Registry
	}
	ci model.CI
}

// gitAccessFor reads a GitProvider in namespace ns and its credentials, straight
// from the API server, and builds its Provider.
func gitAccessFor(ctx context.Context, c client.Client, apiReader client.Reader, git GitClient,
	ns, name string) (gitAccess, error) {
	var provider c4v1beta1.GitProvider
	if err := c.Get(ctx, client.ObjectKey{Namespace: ns, Name: name}, &provider); err != nil {
		return gitAccess{}, fmt.Errorf("GitProvider %s: %w", name, err)
	}
	creds, found, err := readCredentials(ctx, apiReader, &provider)
	if err != nil {
		return gitAccess{}, err
	}
	if !found {
		return gitAccess{}, fmt.Errorf("secret %s of GitProvider %s not found",
			provider.Spec.CredentialsSecretRef.Name, name)
	}
	packages, err := packagesWithTokens(ctx, apiReader, &provider)
	if err != nil {
		return gitAccess{}, fmt.Errorf("GitProvider %s: %w", name, err)
	}
	p, err := git.Provider(ports.Config{
		Kind:        string(provider.Spec.Provider),
		URL:         provider.Spec.URL,
		Owner:       provider.Spec.Owner,
		Credentials: creds,
		Packages:    packages,
	})
	if err != nil {
		return gitAccess{}, err
	}
	ci, err := gitProviderCI(&provider)
	if err != nil {
		return gitAccess{}, err
	}
	return gitAccess{gitProvider: &provider, creds: creds, provider: p, ci: ci}, nil
}

// reportChecksApplied sets ChecksApplied: whether the Container's Standard runs
// the full check when spec.checks asks. Once scaffolded, that is what the
// repository's CI runs (status.standard.checks): while it differs, the condition
// is False and says why, the update in flight or the stop. A Container without
// a Standard, or whose Standard isn't there (ReferencesResolved says so), has no
// such condition; a read error keeps the last one.
func (r *ContainerReconciler) reportChecksApplied(ctx context.Context, container *c4v1beta1.Container,
	status *c4v1beta1.ContainerStatus) {
	if container.Choices().Standard == "" {
		meta.RemoveStatusCondition(&status.Conditions, c4v1beta1.ConditionChecksApplied)
		return
	}
	name := container.Choices().Standard
	var standard c4v1beta1.Standard
	found, err := r.lookup(ctx, container.Namespace, name, &standard)
	if err != nil {
		logf.FromContext(ctx).Info("Could not read Standard", "standard", name, "error", err.Error())
		return
	}
	if !found {
		meta.RemoveStatusCondition(&status.Conditions, c4v1beta1.ConditionChecksApplied)
		return
	}
	asked := string(container.Spec.Checks)
	effective := model.EffectiveChecks(asked, standard.Spec.ChecksBeforeRelease)
	if status.Phase == c4v1beta1.ContainerReady {
		if runs := recordedChecks(status); runs != effective {
			reason, message := checksNotRunYet(name, status, runs, effective)
			r.condition(container, status, c4v1beta1.ConditionChecksApplied, metav1.ConditionFalse, reason, message)
			return
		}
	}
	if asked == model.ChecksBeforeRelease && effective != asked {
		r.condition(container, status, c4v1beta1.ConditionChecksApplied, metav1.ConditionFalse,
			c4v1beta1.ReasonStandardRunsFullChecks, fmt.Sprintf(
				"The Standard %s can't run the full check only before a release; CI runs it on every change", name))
		return
	}
	reason := c4v1beta1.ReasonEveryChange
	if effective == model.ChecksBeforeRelease {
		reason = c4v1beta1.ReasonBeforeRelease
	}
	r.condition(container, status, c4v1beta1.ConditionChecksApplied, metav1.ConditionTrue, reason, "")
}

// recordedChecks is when the repository's CI runs the full check, as its
// Standard files were last rendered; empty reads as EveryChange.
func recordedChecks(status *c4v1beta1.ContainerStatus) string {
	if status.Standard == nil || status.Standard.Checks == "" {
		return model.ChecksEveryChange
	}
	return string(status.Standard.Checks)
}

// checksNotRunYet says why the repository's CI still runs the full check as
// runs, not as effective, the Container's checks: an update in flight
// (Pending), its stop, or a Standard the scaffold didn't have. A switch held
// for a missing light check says so in StandardUpdated's words.
func checksNotRunYet(name string, status *c4v1beta1.ContainerStatus, runs, effective string) (string, string) {
	now := "CI runs the full check " + checksWhen(runs)
	if status.Standard == nil || status.Standard.Name != name {
		return c4v1beta1.ReasonAddedAfterScaffold, fmt.Sprintf(
			"The Standard %s was not part of the scaffold, so its updates don't reach this Container; %s", name, now)
	}
	update := status.Standard.Update
	if update == nil || updateChecks(update) != effective {
		return c4v1beta1.ReasonPending, fmt.Sprintf("%s until a Standard update brings it %s", now, checksWhen(effective))
	}
	if update.Stopped == c4v1beta1.ReasonNoLightCheck {
		if c := meta.FindStatusCondition(status.Conditions, c4v1beta1.ConditionStandardUpdated); c != nil &&
			c.Reason == c4v1beta1.ReasonNoLightCheck {
			return update.Stopped, c.Message
		}
	}
	if update.Stopped != "" {
		return update.Stopped, fmt.Sprintf("%s: the Standard update that brings it %s stopped (%s); StandardUpdated says why",
			now, checksWhen(effective), update.Stopped)
	}
	if update.PullRequestURL == "" {
		return c4v1beta1.ReasonPending, fmt.Sprintf("%s until the Standard update that brings it %s merges",
			now, checksWhen(effective))
	}
	return c4v1beta1.ReasonPending, fmt.Sprintf("%s until pull request %s merges", now, update.PullRequestURL)
}

// updateChecks is when CI runs the full check once update merges; empty reads
// as EveryChange.
func updateChecks(update *c4v1beta1.StandardUpdateStatus) string {
	if update.Checks == "" {
		return model.ChecksEveryChange
	}
	return string(update.Checks)
}
