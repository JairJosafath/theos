/*
Copyright 2026.

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
	"errors"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/gitprovider"
	"github.com/JairJosafath/c4factory/internal/scaffold"
)

// GitClient reaches Git hosts. gitprovider.Client implements it; tests use a fake.
type GitClient interface {
	Provider(cfg gitprovider.Config) (gitprovider.Provider, error)
	FetchTemplate(ctx context.Context, url, ref string, creds *gitprovider.Credentials) (scaffold.Tree, error)
}

// scaffoldTimeout bounds one attempt at creating and filling a repository.
const scaffoldTimeout = 2 * time.Minute

// scaffold creates the Container's repository and pushes the single initial
// commit. It is safe to retry: an empty repository gets the commit, one holding
// only our own commit is recognised, and anything else fails the Container.
func (r *ContainerReconciler) scaffold(ctx context.Context, container *c4v1alpha1.Container,
	res resolved, status *c4v1alpha1.ContainerStatus) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// The cache can lag behind our own status writes. Check the live object, so a
	// reconcile queued during the last attempt doesn't scaffold a second time.
	var live c4v1alpha1.Container
	if err := r.APIReader.Get(ctx, client.ObjectKeyFromObject(container), &live); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if live.Status.Phase == c4v1alpha1.ContainerReady {
		return ctrl.Result{}, nil
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

	ctx, cancel := context.WithTimeout(ctx, scaffoldTimeout)
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

	repo, err := provider.EnsureRepository(ctx, container.Name, repoDescription(container))
	if err != nil {
		return r.retry(ctx, container, status, c4v1alpha1.ConditionRepositoryCreated, err)
	}
	status.RepositoryURL = repo.WebURL
	r.condition(container, status, c4v1alpha1.ConditionRepositoryCreated, metav1.ConditionTrue,
		c4v1alpha1.ReasonCreated, "Repository "+repo.WebURL)

	commits, err := provider.History(ctx, repo, 2)
	if err != nil {
		return r.retry(ctx, container, status, c4v1alpha1.ConditionScaffolded, err)
	}
	if len(commits) > 0 {
		info, parseErr := scaffold.ParseCommitMessage(commits[0].Message)
		if len(commits) == 1 && parseErr == nil && info.Container == container.Namespace+"/"+container.Name {
			// An earlier attempt pushed but didn't get to record it.
			log.Info("Found initial commit from an earlier attempt", "url", repo.WebURL)
			return r.ready(ctx, container, status, info.ScaffoldedFrom, info.Relationships,
				"Found the initial commit from an earlier attempt")
		}
		return r.fail(ctx, container, status, c4v1alpha1.ConditionScaffolded, c4v1alpha1.ReasonForeignCommits,
			fmt.Sprintf("Repository %s has commits C4 Factory didn't make; it only scaffolds into a new, empty repository",
				repo.WebURL))
	}

	layers, err := r.fetchLayers(ctx, res)
	if errors.Is(err, gitprovider.ErrUnpinnedRef) {
		return r.fail(ctx, container, status, c4v1alpha1.ConditionScaffolded, c4v1alpha1.ReasonUnpinnedRef, err.Error())
	}
	if err != nil {
		return r.retry(ctx, container, status, c4v1alpha1.ConditionScaffolded, err)
	}

	in := scaffold.Input{
		Project:        container.Namespace,
		SoftwareSystem: container.Spec.SoftwareSystemRef.Name,
		Container:      container.Name,
		DisplayName:    container.Spec.DisplayName,
		Type:           string(container.Spec.Type),
		Connectors:     res.use.Connectors,
	}
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
	if _, err := provider.PushInitialCommit(ctx, repo, result.Files, message, author); err != nil {
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
	status *c4v1alpha1.ContainerStatus, from, relationships []string, message string) (ctrl.Result, error) {
	status.Phase = c4v1alpha1.ContainerReady
	status.ScaffoldedFrom = from
	status.AppliedRelationships = relationships
	r.condition(container, status, c4v1alpha1.ConditionScaffolded, metav1.ConditionTrue,
		c4v1alpha1.ReasonScaffolded, message)
	return ctrl.Result{}, r.writeStatus(ctx, container, status)
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

func repoDescription(container *c4v1alpha1.Container) string {
	if container.Spec.Description != "" {
		return container.Spec.Description
	}
	if container.Spec.DisplayName != "" {
		return container.Spec.DisplayName
	}
	return fmt.Sprintf("The %s %s of %s", container.Name, container.Spec.Type, container.Spec.SoftwareSystemRef.Name)
}
