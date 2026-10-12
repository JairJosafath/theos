/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"context"
	"errors"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/gitprovider"
	"github.com/JairJosafath/c4factory/internal/model"
	"github.com/JairJosafath/c4factory/internal/scaffold"
)

// GitClient reaches Git hosts. gitprovider.Client implements it; tests use a fake.
type GitClient interface {
	Provider(cfg gitprovider.Config) (gitprovider.Provider, error)
	FetchTemplate(ctx context.Context, url, ref string, creds *gitprovider.Credentials) (scaffold.Tree, error)
}

// defaultScaffoldTimeout bounds one attempt at creating and filling a repository.
const defaultScaffoldTimeout = 2 * time.Minute

func (r *ContainerReconciler) scaffoldTimeout() time.Duration {
	if r.ScaffoldTimeout > 0 {
		return r.ScaffoldTimeout
	}
	return defaultScaffoldTimeout
}

// scaffold creates the Container's repository and pushes the single initial
// commit. It is safe to retry: an empty repository gets the commit, one holding
// only our own commit is recognised, and anything else fails the Container.
func (r *ContainerReconciler) scaffold(ctx context.Context, container *c4v1alpha1.Container,
	res resolved, status *c4v1alpha1.ContainerStatus) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// The cache can lag behind our own status writes. Check the live object, so a
	// reconcile queued during the last attempt doesn't scaffold a second time.
	// That reconcile took the place of the requeue ready asked for, so ask
	// again: a status write alone doesn't bring the Container back.
	var live c4v1alpha1.Container
	if err := r.APIReader.Get(ctx, client.ObjectKeyFromObject(container), &live); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if live.Status.Phase == c4v1alpha1.ContainerReady {
		return ctrl.Result{RequeueAfter: r.checksRecheck()}, nil
	}

	// Show progress before the slow part.
	if status.Phase != c4v1alpha1.ContainerScaffolding {
		status.Phase = c4v1alpha1.ContainerScaffolding
		r.condition(container, status, c4v1alpha1.ConditionScaffolded, metav1.ConditionFalse,
			c4v1alpha1.ReasonScaffolding, "Creating the repository and rendering the templates")
		if err := r.writeStatus(ctx, container, status); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Git calls get a deadline of their own, so the status writes after them still land.
	gitCtx, cancel := context.WithTimeout(ctx, r.scaffoldTimeout())
	defer cancel()

	provider, err := r.Git.Provider(gitprovider.Config{
		Kind:        string(res.provider.Spec.Provider),
		URL:         res.provider.Spec.URL,
		Owner:       res.provider.Spec.Owner,
		Credentials: res.creds,
	})
	if err != nil {
		return r.fail(ctx, container, status, c4v1alpha1.ConditionRepositoryCreated, c4v1alpha1.ReasonFailed,
			fmt.Sprintf("GitProvider %s: %v", res.provider.Name, err))
	}

	repo, err := provider.EnsureRepository(gitCtx, container.Name, repoDescription(container))
	if err != nil {
		return r.retry(ctx, container, status, c4v1alpha1.ConditionRepositoryCreated, err)
	}
	status.RepositoryURL = repo.WebURL
	r.condition(container, status, c4v1alpha1.ConditionRepositoryCreated, metav1.ConditionTrue,
		c4v1alpha1.ReasonCreated, "Repository "+repo.WebURL)

	commits, err := provider.History(gitCtx, repo, 2)
	if err != nil {
		return r.retry(ctx, container, status, c4v1alpha1.ConditionScaffolded, err)
	}
	var info scaffold.CommitInfo
	if len(commits) > 0 {
		info, _ = scaffold.ParseCommitMessage(commits[0].Message) // not ours leaves Container empty
	}
	switch model.ScaffoldHistory(len(commits), info.Container, container.Namespace+"/"+container.Name) {
	case model.HistoryRecover:
		log.Info("Found initial commit from an earlier attempt", "url", repo.WebURL)
		if err := protectRepository(gitCtx, r.Client, provider, repo, container); err != nil {
			return r.retry(ctx, container, status, c4v1alpha1.ConditionScaffolded, err)
		}
		return r.ready(ctx, container, status, info.ScaffoldedFrom, info.Relationships,
			"Found the initial commit from an earlier attempt")
	case model.HistoryForeign:
		return r.fail(ctx, container, status, c4v1alpha1.ConditionScaffolded, c4v1alpha1.ReasonForeignCommits,
			fmt.Sprintf("Repository %s has commits C4 Factory didn't make; it only scaffolds into a new, empty repository",
				repo.WebURL))
	}

	layers, err := r.fetchLayers(gitCtx, res)
	if errors.Is(err, gitprovider.ErrUnpinnedRef) {
		return r.fail(ctx, container, status, c4v1alpha1.ConditionScaffolded, c4v1alpha1.ReasonUnpinnedRef, err.Error())
	}
	if err != nil {
		return r.retry(ctx, container, status, c4v1alpha1.ConditionScaffolded, err)
	}

	in := scaffoldInput(container, res.use.Connectors,
		model.EffectiveChecks(string(container.Spec.Checks), res.standard != nil && res.standard.ChecksBeforeRelease))
	result, err := scaffold.Build(in, layers, r.ScaffoldOptions)
	if err != nil {
		reason := c4v1alpha1.ReasonFailed
		if _, ok := errors.AsType[*scaffold.ConflictError](err); ok {
			reason = c4v1alpha1.ReasonLayerConflict
		}
		return r.fail(ctx, container, status, c4v1alpha1.ConditionScaffolded, reason, err.Error())
	}

	message := scaffold.CommitMessage(in, result.ScaffoldedFrom, res.use.Relationships)
	author := gitprovider.Signature{Name: scaffold.AuthorName, Email: scaffold.AuthorEmail, When: time.Now()}
	if _, err := provider.PushInitialCommit(gitCtx, repo, result.Files, message, author); err != nil {
		return r.retry(ctx, container, status, c4v1alpha1.ConditionScaffolded, err)
	}
	if err := protectRepository(gitCtx, r.Client, provider, repo, container); err != nil {
		return r.retry(ctx, container, status, c4v1alpha1.ConditionScaffolded, err)
	}
	log.Info("Scaffolded repository", "url", repo.WebURL, "files", len(result.Files),
		"scaffoldedFrom", result.ScaffoldedFrom)
	return r.ready(ctx, container, status, result.ScaffoldedFrom, res.use.Relationships,
		fmt.Sprintf("Pushed the initial commit with %d files", len(result.Files)))
}

// fetchLayers fetches every catalog entry's template at its pinned ref, in layer
// order. The GitProvider's credentials are used for templates on the same host.
func (r *ContainerReconciler) fetchLayers(ctx context.Context, res resolved) ([]scaffold.Layer, error) {
	layers := make([]scaffold.Layer, 0, len(res.entries))
	for _, entry := range res.entries {
		var creds *gitprovider.Credentials
		if gitprovider.SameHost(res.provider.Spec.URL, entry.TemplateURL) {
			creds = &res.creds
		}
		files, err := r.Git.FetchTemplate(ctx, entry.TemplateURL, entry.TemplateRef, creds)
		if err != nil {
			return nil, fmt.Errorf("%s %s: %w", entry.Side, entry.Name, err)
		}
		layers = append(layers, scaffold.Layer{Side: entry.Side, Name: entry.Name, Ref: entry.TemplateRef, Files: files})
	}
	return layers, nil
}

// ready records a finished scaffold. From here on the factory only reports.
func (r *ContainerReconciler) ready(ctx context.Context, container *c4v1alpha1.Container,
	status *c4v1alpha1.ContainerStatus, from []string, relationships []model.AppliedRelationship, message string) (ctrl.Result, error) {
	status.Phase = c4v1alpha1.ContainerReady
	status.ScaffoldedFrom = from
	status.AppliedRelationships, status.AppliedConnectors = appliedStatus(relationships)
	r.condition(container, status, c4v1alpha1.ConditionScaffolded, metav1.ConditionTrue,
		c4v1alpha1.ReasonScaffolded, message)
	// The scaffold's push starts CI; read its result like every later reconcile
	// does. A status write alone doesn't reconcile the Container again.
	return ctrl.Result{RequeueAfter: r.checksRecheck()}, r.writeStatus(ctx, container, status)
}

// fail stops the Container with a reason a person has to act on.
func (r *ContainerReconciler) fail(ctx context.Context, container *c4v1alpha1.Container,
	status *c4v1alpha1.ContainerStatus, condType, reason, message string) (ctrl.Result, error) {
	logf.FromContext(ctx).Info("Could not scaffold Container", "reason", reason, "message", message)
	status.Phase = c4v1alpha1.ContainerFailed
	r.condition(container, status, condType, metav1.ConditionFalse, reason, message)
	return ctrl.Result{}, r.writeStatus(ctx, container, status)
}

// retry records a Git error that may pass, and returns it so the attempt is
// repeated with backoff.
func (r *ContainerReconciler) retry(ctx context.Context, container *c4v1alpha1.Container,
	status *c4v1alpha1.ContainerStatus, condType string, cause error) (ctrl.Result, error) {
	r.condition(container, status, condType, metav1.ConditionFalse, c4v1alpha1.ReasonGitError,
		cause.Error()+"; retrying")
	if err := r.writeStatus(ctx, container, status); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, cause
}

func (r *ContainerReconciler) condition(container *c4v1alpha1.Container, status *c4v1alpha1.ContainerStatus,
	condType string, s metav1.ConditionStatus, reason, message string) {
	setCondition(&status.Conditions, container.Generation, condType, s, reason, message)
}

// scaffoldInput is what every template of the Container renders with.
// connectors are the unique Connector names, sorted, and checks is when CI runs
// the full check (model.EffectiveChecks).
func scaffoldInput(container *c4v1alpha1.Container, connectors []string, checks string) scaffold.Input {
	in := scaffold.Input{
		Project:        container.Namespace,
		SoftwareSystem: container.Spec.SoftwareSystemRef.Name,
		Container:      container.Name,
		DisplayName:    container.Spec.DisplayName,
		Type:           string(container.Spec.Type),
		Framework:      container.Spec.FrameworkRef.Name,
		Connectors:     connectors,
		Checks:         checks,
	}
	if container.Spec.StandardRef != nil {
		in.Standard = container.Spec.StandardRef.Name
	}
	if container.Spec.ComputeRef != nil {
		in.Compute = container.Spec.ComputeRef.Name
	}
	if container.Spec.DeploymentRef != nil {
		in.Deployment = container.Spec.DeploymentRef.Name
	}
	return in
}

func repoDescription(container *c4v1alpha1.Container) string {
	if container.Spec.Description != "" {
		return container.Spec.Description
	}
	if container.Spec.DisplayName != "" {
		return container.Spec.DisplayName
	}
	return fmt.Sprintf("The %s %s of %s", container.Name, container.Spec.Type, container.Spec.SoftwareSystemRef.Name)
}

// appliedStatus splits what was applied into the Relationship names and, for
// the ones whose targets are known, what each added.
func appliedStatus(relationships []model.AppliedRelationship) ([]string, []c4v1alpha1.AppliedConnector) {
	names := make([]string, 0, len(relationships))
	var connectors []c4v1alpha1.AppliedConnector
	for _, a := range relationships {
		names = append(names, a.Name)
		if a.DataStore != "" {
			connectors = append(connectors, c4v1alpha1.AppliedConnector{
				Relationship: a.Name, DataStore: a.DataStore, Connector: a.Connector,
			})
		}
	}
	return names, connectors
}
