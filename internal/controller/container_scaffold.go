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
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	c4v1beta1 "github.com/JairJosafath/c4factory/api/v1beta1"
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
func (r *ContainerReconciler) scaffold(ctx context.Context, container *c4v1beta1.Container,
	res resolved, status *c4v1beta1.ContainerStatus) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	// What the scaffold reads of the Container's choices: status.bundle is
	// written only with the rest of the status, so a copy carries it.
	view := withBundle(container, res.bundle)

	// The cache can lag behind our own status writes. Check the live object, so a
	// reconcile queued during the last attempt doesn't scaffold a second time.
	// That reconcile took the place of the requeue ready asked for, so ask
	// again: a status write alone doesn't bring the Container back.
	var live c4v1beta1.Container
	if err := r.APIReader.Get(ctx, client.ObjectKeyFromObject(container), &live); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if live.Status.Phase == c4v1beta1.ContainerReady {
		return ctrl.Result{RequeueAfter: r.checksRecheck()}, nil
	}

	// Show progress before the slow part.
	if status.Phase != c4v1beta1.ContainerScaffolding {
		status.Phase = c4v1beta1.ContainerScaffolding
		r.condition(container, status, c4v1beta1.ConditionScaffolded, metav1.ConditionFalse,
			c4v1beta1.ReasonScaffolding, "Creating the repository and rendering the templates")
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
		Packages:    packagesOf(res.provider),
	})
	if err != nil {
		return r.fail(ctx, container, status, c4v1beta1.ConditionRepositoryCreated, c4v1beta1.ReasonFailed,
			fmt.Sprintf("GitProvider %s: %v", res.provider.Name, err))
	}

	repo, err := provider.EnsureRepository(gitCtx, container.Name, repoDescription(container))
	if err != nil {
		return r.retry(ctx, container, status, c4v1beta1.ConditionRepositoryCreated, err)
	}
	status.RepositoryURL = repo.WebURL
	r.condition(container, status, c4v1beta1.ConditionRepositoryCreated, metav1.ConditionTrue,
		c4v1beta1.ReasonCreated, "Repository "+repo.WebURL)

	commits, err := provider.History(gitCtx, repo, 2)
	if err != nil {
		return r.retry(ctx, container, status, c4v1beta1.ConditionScaffolded, err)
	}
	var info scaffold.CommitInfo
	if len(commits) > 0 {
		info, _ = scaffold.ParseCommitMessage(commits[0].Message) // not ours leaves Container empty
	}
	switch model.ScaffoldHistory(len(commits), info.Container, container.Namespace+"/"+container.Name,
		container.Spec.Adopt) {
	case model.HistoryAdopt:
		return r.adopt(ctx, gitCtx, container, status, res, view, provider, repo)
	case model.HistoryNothingToAdopt:
		return r.fail(ctx, container, status, c4v1beta1.ConditionScaffolded, c4v1beta1.ReasonNothingToAdopt,
			fmt.Sprintf("Repository %s has no commits to adopt; spec.adopt takes over an existing repository, "+
				"it never scaffolds one", repo.WebURL))
	case model.HistoryRecover:
		log.Info("Found initial commit from an earlier attempt", "url", repo.WebURL)
		if err := protectRepository(gitCtx, r.Client, provider, repo, view); err != nil {
			return r.retry(ctx, container, status, c4v1beta1.ConditionScaffolded, err)
		}
		// The commit records the expansion the push used; the Bundle may
		// have changed since, which is reported, never applied (#1621). An
		// older commit without it keeps the expansion resolved now.
		standard := res.choices.Standard
		if b := info.Bundle; b != nil {
			status.Bundle = &c4v1beta1.ContainerBundleStatus{Name: b.Name, Framework: b.Framework,
				Standard: b.Standard, Compute: b.Compute, Deployment: b.Deployment}
			standard = b.Standard
		}
		return r.ready(ctx, container, status, standard, model.ScaffoldedRef(info.ScaffoldedFrom, standard),
			info.ScaffoldedFrom, info.Relationships, info.Checks, res.entries[0].ReadsOperations,
			"Found the initial commit from an earlier attempt")
	case model.HistoryForeign:
		return r.fail(ctx, container, status, c4v1beta1.ConditionScaffolded, c4v1beta1.ReasonForeignCommits,
			fmt.Sprintf("Repository %s has commits C4 Factory didn't make; it only scaffolds into a new, empty repository",
				repo.WebURL))
	}

	layers, err := r.fetchLayers(gitCtx, res)
	if errors.Is(err, gitprovider.ErrUnpinnedRef) {
		return r.fail(ctx, container, status, c4v1beta1.ConditionScaffolded, c4v1beta1.ReasonUnpinnedRef, err.Error())
	}
	if err != nil {
		return r.retry(ctx, container, status, c4v1beta1.ConditionScaffolded, err)
	}

	ci, err := gitProviderCI(res.provider)
	if err != nil {
		return r.fail(ctx, container, status, c4v1beta1.ConditionScaffolded, c4v1beta1.ReasonFailed, err.Error())
	}
	in := scaffoldInput(view, ci, res.use.Connectors,
		model.EffectiveChecks(string(container.Spec.Checks), res.standard != nil && res.standard.ChecksBeforeRelease),
		specOperations(container), repo.NpmScope)
	result, err := scaffold.Build(in, layers, r.ScaffoldOptions)
	if err != nil {
		reason := c4v1beta1.ReasonFailed
		if _, ok := errors.AsType[*scaffold.ConflictError](err); ok {
			reason = c4v1beta1.ReasonLayerConflict
		}
		if _, ok := errors.AsType[*scaffold.UnsupportedCIError](err); ok {
			reason = c4v1beta1.ReasonUnsupportedCI
		}
		return r.fail(ctx, container, status, c4v1beta1.ConditionScaffolded, reason, err.Error())
	}

	message := scaffold.CommitMessage(in, result.ScaffoldedFrom, res.use.Relationships)
	author := gitprovider.Signature{Name: scaffold.AuthorName, Email: scaffold.AuthorEmail, When: time.Now()}
	if _, err := provider.PushInitialCommit(gitCtx, repo, result.Files, message, author); err != nil {
		return r.retry(ctx, container, status, c4v1beta1.ConditionScaffolded, err)
	}
	if err := protectRepository(gitCtx, r.Client, provider, repo, view); err != nil {
		return r.retry(ctx, container, status, c4v1beta1.ConditionScaffolded, err)
	}
	log.Info("Scaffolded repository", "url", repo.WebURL, "files", len(result.Files),
		"scaffoldedFrom", result.ScaffoldedFrom)
	return r.ready(ctx, container, status, res.choices.Standard, model.ScaffoldedRef(result.ScaffoldedFrom, res.choices.Standard),
		result.ScaffoldedFrom, res.use.Relationships, in.Checks, res.entries[0].ReadsOperations,
		fmt.Sprintf("Pushed the initial commit with %d files", len(result.Files)))
}

// adopt takes over a repository that already has commits (#1888, spec.adopt).
// Nothing is written: main must exist and carry a .c4factory.yaml naming this project
// and Container, and every file of the Container's Standard with the content
// the Standard renders for it at its ref. Then main is protected and the
// Container is Ready, its Standard recorded at that ref and nothing recorded
// as scaffolded. Its spec.operations is recorded as it is now, as a recovered
// scaffold's is.
func (r *ContainerReconciler) adopt(ctx, gitCtx context.Context, container *c4v1beta1.Container,
	status *c4v1beta1.ContainerStatus, res resolved, view *c4v1beta1.Container, provider gitprovider.Provider,
	repo gitprovider.Repository) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	// Only the Standard is checked, rendered from what a later Standard update
	// renders an adopted Container with: no Framework ref and no Connectors
	// recorded, so no Framework files and no Connectors.
	standardOnly := res
	standardOnly.entries = nil
	for _, entry := range res.entries {
		if entry.Side == model.SideStandard {
			standardOnly.entries = append(standardOnly.entries, entry)
		}
	}
	layers, err := r.fetchLayers(gitCtx, standardOnly)
	if errors.Is(err, gitprovider.ErrUnpinnedRef) {
		return r.fail(ctx, container, status, c4v1beta1.ConditionScaffolded, c4v1beta1.ReasonUnpinnedRef, err.Error())
	}
	if err != nil {
		return r.retry(ctx, container, status, c4v1beta1.ConditionScaffolded, err)
	}
	ci, err := gitProviderCI(res.provider)
	if err != nil {
		return r.fail(ctx, container, status, c4v1beta1.ConditionScaffolded, c4v1beta1.ReasonFailed, err.Error())
	}
	in := scaffoldInput(view, ci, nil,
		model.EffectiveChecks(string(container.Spec.Checks), res.standard != nil && res.standard.ChecksBeforeRelease),
		specOperations(container), repo.NpmScope)
	rendered := scaffold.Tree{}
	var standardRef string
	for _, layer := range layers {
		tree, err := scaffold.RenderLayer(in, layer)
		if err != nil {
			return r.fail(ctx, container, status, c4v1beta1.ConditionScaffolded, c4v1beta1.ReasonFailed, err.Error())
		}
		rendered, standardRef = tree, layer.Ref
	}

	onMain, _, err := provider.ReadFiles(gitCtx, repo, gitprovider.DefaultBranch,
		append(rendered.Paths(), scaffold.IdentityPath))
	if errors.Is(err, gitprovider.ErrBranchNotFound) {
		// #1908: the commits History found are on another branch; no retry
		// makes main appear.
		return r.fail(ctx, container, status, c4v1beta1.ConditionScaffolded, c4v1beta1.ReasonNoDefaultBranch,
			fmt.Sprintf("Repository %s has commits but no %s branch; an adopted repository names its project and "+
				"Container and carries its Standard's files on %s, so rename its default branch to %s first",
				repo.WebURL, gitprovider.DefaultBranch, gitprovider.DefaultBranch, gitprovider.DefaultBranch))
	}
	if err != nil {
		return r.retry(ctx, container, status, c4v1beta1.ConditionScaffolded, err)
	}
	identity, found := onMain[scaffold.IdentityPath]
	delete(onMain, scaffold.IdentityPath)
	if !found {
		return r.fail(ctx, container, status, c4v1beta1.ConditionScaffolded, c4v1beta1.ReasonIdentityMismatch,
			fmt.Sprintf("Repository %s has no %s on %s; an adopted repository names its project and Container there",
				repo.WebURL, scaffold.IdentityPath, gitprovider.DefaultBranch))
	}
	id, err := scaffold.ParseIdentity(identity.Data)
	if err != nil {
		return r.fail(ctx, container, status, c4v1beta1.ConditionScaffolded, c4v1beta1.ReasonIdentityMismatch,
			fmt.Sprintf("Repository %s: %v", repo.WebURL, err))
	}
	if id.Project != container.Namespace || id.Container != container.Name {
		return r.fail(ctx, container, status, c4v1beta1.ConditionScaffolded, c4v1beta1.ReasonIdentityMismatch,
			fmt.Sprintf("Repository %s: %s names project %q and container %q, not %s/%s",
				repo.WebURL, scaffold.IdentityPath, id.Project, id.Container, container.Namespace, container.Name))
	}
	if differ := scaffold.ChangedPaths(rendered, onMain); len(differ) > 0 {
		return r.fail(ctx, container, status, c4v1beta1.ConditionScaffolded, c4v1beta1.ReasonStandardFilesDiffer,
			fmt.Sprintf("Repository %s: %s differs from Standard %s at %s in %s; an adopted repository carries the "+
				"Standard's files as it renders them", repo.WebURL, gitprovider.DefaultBranch, res.choices.Standard,
				standardRef, strings.Join(differ, ", ")))
	}

	if err := protectRepository(gitCtx, r.Client, provider, repo, view); err != nil {
		return r.retry(ctx, container, status, c4v1beta1.ConditionScaffolded, err)
	}
	log.Info("Adopted repository", "url", repo.WebURL, "standard", res.choices.Standard, "ref", standardRef)
	return r.ready(ctx, container, status, res.choices.Standard, standardRef, nil, nil, in.Checks,
		res.entries[0].ReadsOperations, fmt.Sprintf("Adopted repository %s", repo.WebURL))
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
// standardRef is the ref the Standard's files on main are at: the one the
// scaffold pinned, or the one an adopted repository was checked against; empty
// records no Standard. checks is when CI runs the full check as the scaffold
// rendered the Standard's files; empty reads as EveryChange. readsOperations says the Framework
// rendered spec.operations into the repository (#1209): only then is the
// block recorded, so a Framework that doesn't read it never reports it
// applied.
func (r *ContainerReconciler) ready(ctx context.Context, container *c4v1beta1.Container,
	status *c4v1beta1.ContainerStatus, standard, standardRef string, from []string,
	relationships []model.AppliedRelationship, checks string, readsOperations bool, message string) (ctrl.Result, error) {
	status.Phase = c4v1beta1.ContainerReady
	status.ScaffoldedFrom = from
	if standard != "" && standardRef != "" {
		status.Standard = &c4v1beta1.ContainerStandardStatus{Name: standard, Ref: standardRef,
			Checks: c4v1beta1.ChecksMode(checks)}
	}
	status.AppliedRelationships, status.AppliedConnectors = appliedStatus(relationships)
	// The commit an earlier attempt pushed doesn't say its operations, so a
	// recovered scaffold records the block as it is now.
	status.Operations = nil
	if readsOperations {
		status.Operations = operationsStatus(specOperations(container))
	}
	r.reportOperationsApplied(container, status)
	r.condition(container, status, c4v1beta1.ConditionScaffolded, metav1.ConditionTrue,
		c4v1beta1.ReasonScaffolded, message)
	// The scaffold's push starts CI; read its result like every later reconcile
	// does. A status write alone doesn't reconcile the Container again.
	return ctrl.Result{RequeueAfter: r.checksRecheck()}, r.writeStatus(ctx, container, status)
}

// fail stops the Container with a reason a person has to act on.
func (r *ContainerReconciler) fail(ctx context.Context, container *c4v1beta1.Container,
	status *c4v1beta1.ContainerStatus, condType, reason, message string) (ctrl.Result, error) {
	logf.FromContext(ctx).Info("Could not scaffold Container", "reason", reason, "message", message)
	status.Phase = c4v1beta1.ContainerFailed
	r.condition(container, status, condType, metav1.ConditionFalse, reason, message)
	return ctrl.Result{}, r.writeStatus(ctx, container, status)
}

// retry records a Git error that may pass, and returns it so the attempt is
// repeated with backoff.
func (r *ContainerReconciler) retry(ctx context.Context, container *c4v1beta1.Container,
	status *c4v1beta1.ContainerStatus, condType string, cause error) (ctrl.Result, error) {
	r.condition(container, status, condType, metav1.ConditionFalse, c4v1beta1.ReasonGitError,
		cause.Error()+"; retrying")
	if err := r.writeStatus(ctx, container, status); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, cause
}

func (r *ContainerReconciler) condition(container *c4v1beta1.Container, status *c4v1beta1.ContainerStatus,
	condType string, s metav1.ConditionStatus, reason, message string) {
	setCondition(&status.Conditions, container.Generation, condType, s, reason, message)
}

// scaffoldInput is what every template of the Container renders with. ci is
// the CI system its repository runs, connectors are the unique Connector
// names, sorted, checks is when CI runs the full check
// (model.EffectiveChecks), ops its operations block, the defaults filled in,
// and npmScope the scope the repository's npm package is named under
// (gitprovider.Repository.NpmScope; empty means gitprovider.DefaultNpmScope).
func scaffoldInput(container *c4v1beta1.Container, ci model.CI, connectors []string,
	checks string, ops model.Operations, npmScope string) scaffold.Input {
	if npmScope == "" {
		npmScope = gitprovider.DefaultNpmScope
	}
	choices := container.Choices()
	in := scaffold.Input{
		CI:             ci,
		Project:        container.Namespace,
		SoftwareSystem: container.Spec.SoftwareSystemRef.Name,
		Container:      container.Name,
		DisplayName:    container.Spec.DisplayName,
		Description:    container.Spec.Description,
		Type:           string(container.Spec.Type),
		Framework:      choices.Framework,
		Standard:       choices.Standard,
		Compute:        choices.Compute,
		Deployment:     choices.Deployment,
		Bundle:         choices.Bundle,
		Connectors:     connectors,
		Checks:         checks,
		Operations:     ops,
		NpmScope:       npmScope,
	}
	return in
}

// withBundle is a copy of the Container carrying bundle as its status.bundle,
// for reading its choices before the status that records them is written.
func withBundle(container *c4v1beta1.Container, bundle *c4v1beta1.ContainerBundleStatus) *c4v1beta1.Container {
	view := container.DeepCopy()
	view.Status.Bundle = bundle.DeepCopy()
	return view
}

func repoDescription(container *c4v1beta1.Container) string {
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
func appliedStatus(relationships []model.AppliedRelationship) ([]string, []c4v1beta1.AppliedConnector) {
	names := make([]string, 0, len(relationships))
	var connectors []c4v1beta1.AppliedConnector
	for _, a := range relationships {
		names = append(names, a.Name)
		if a.DataStore != "" {
			connectors = append(connectors, c4v1beta1.AppliedConnector{
				Relationship: a.Name, DataStore: a.DataStore, Connector: a.Connector,
			})
		}
	}
	return names, connectors
}
