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
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/scaffold"
)

// GitClient reaches Git hosts and fetches templates. cmd/main.go builds it
// from the Gitea and Git adapters; tests use a fake.
type GitClient interface {
	Provider(cfg ports.Config) (ports.Provider, error)
	// FetchTemplate reads the template at url and ref; a non-empty dir reads
	// only that directory of the repository (template.path, #35).
	FetchTemplate(ctx context.Context, url, ref, dir string, creds *ports.Credentials) (scaffold.Tree, error)
}

// templateFailure is the permanent reason a template fetch failed with, an
// unpinned ref or a path the ref doesn't have; false for an error a retry
// may clear.
func templateFailure(err error) (string, bool) {
	switch {
	case errors.Is(err, ports.ErrUnpinnedRef):
		return c4v1beta1.ReasonUnpinnedRef, true
	case errors.Is(err, ports.ErrTemplatePathNotFound):
		return c4v1beta1.ReasonTemplatePathNotFound, true
	}
	return "", false
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
		return ctrl.Result{RequeueAfter: hostRecheck(ctx, r.checksRecheck())}, nil
	}

	// Show progress before the slow part. A Failed Container is read again on
	// every event that reconciles it (#237): it keeps its phase and condition
	// until the attempt knows it will push, so one that fails the same way
	// again never shows Scaffolding in between.
	failed := live.Status.Phase == c4v1beta1.ContainerFailed
	if !failed {
		if err := r.showScaffolding(ctx, container, status); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Git calls get a deadline of their own, so the status writes after them still land.
	gitCtx, cancel := context.WithTimeout(ctx, r.scaffoldTimeout())
	defer cancel()

	provider, err := r.Git.Provider(ports.Config{
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
			info.ScaffoldedFrom, info.Domains, info.Relationships, info.Checks, frameworkEntry(res.entries).ReadsOperations,
			"Found the initial commit from an earlier attempt")
	case model.HistoryForeign:
		return r.fail(ctx, container, status, c4v1beta1.ConditionScaffolded, c4v1beta1.ReasonForeignCommits,
			fmt.Sprintf("Repository %s has commits theos didn't make; it only scaffolds into a new, empty repository",
				repo.WebURL))
	}

	layers, err := r.fetchLayers(gitCtx, res)
	if reason, ok := templateFailure(err); ok {
		return r.fail(ctx, container, status, c4v1beta1.ConditionScaffolded, reason, err.Error())
	}
	if err != nil {
		return r.retry(ctx, container, status, c4v1beta1.ConditionScaffolded, err)
	}

	ci, err := gitProviderCI(res.provider)
	if err != nil {
		return r.fail(ctx, container, status, c4v1beta1.ConditionScaffolded, c4v1beta1.ReasonFailed, err.Error())
	}
	license, err := r.resolveLicense(ctx, container)
	if err != nil {
		return r.retry(ctx, container, status, c4v1beta1.ConditionScaffolded, err)
	}
	in := scaffoldInput(view, ci, res.use.Connectors, res.domains,
		model.EffectiveChecks(string(container.Spec.Checks), res.standard != nil && res.standard.ChecksBeforeRelease),
		r.ciImages(), specOperations(container), repo.NpmScope, license)
	result, err := scaffold.Build(in, layers, r.ScaffoldOptions)
	if err != nil {
		reason := c4v1beta1.ReasonFailed
		if _, ok := errors.AsType[*scaffold.ConflictError](err); ok {
			reason = c4v1beta1.ReasonLayerConflict
		}
		if _, ok := errors.AsType[*scaffold.UnsupportedCIError](err); ok {
			reason = c4v1beta1.ReasonUnsupportedCI
		}
		if _, ok := errors.AsType[*scaffold.DomainNotMarkdownError](err); ok {
			reason = c4v1beta1.ReasonDomainNotMarkdown
		}
		if _, ok := errors.AsType[*scaffold.DomainNoReadmeError](err); ok {
			reason = c4v1beta1.ReasonDomainNoReadme
		}
		return r.fail(ctx, container, status, c4v1beta1.ConditionScaffolded, reason, err.Error())
	}

	if failed {
		if err := r.showScaffolding(ctx, container, status); err != nil {
			return ctrl.Result{}, err
		}
	}
	message := scaffold.CommitMessage(in, result.ScaffoldedFrom, res.use.Relationships)
	author := ports.Signature{Name: scaffold.AuthorName, Email: scaffold.AuthorEmail, When: time.Now()}
	if _, err := provider.PushInitialCommit(gitCtx, repo, result.Files, message, author); err != nil {
		return r.retry(ctx, container, status, c4v1beta1.ConditionScaffolded, err)
	}
	if err := protectRepository(gitCtx, r.Client, provider, repo, view); err != nil {
		return r.retry(ctx, container, status, c4v1beta1.ConditionScaffolded, err)
	}
	log.Info("Scaffolded repository", "url", repo.WebURL, "files", len(result.Files),
		"scaffoldedFrom", result.ScaffoldedFrom)
	return r.ready(ctx, container, status, res.choices.Standard, model.ScaffoldedRef(result.ScaffoldedFrom, res.choices.Standard),
		result.ScaffoldedFrom, in.Domains, res.use.Relationships, in.Checks, frameworkEntry(res.entries).ReadsOperations,
		fmt.Sprintf("Pushed the initial commit with %d files", len(result.Files)))
}

// adopt takes over a repository that already has commits (#1888, spec.adopt).
// Nothing is written: main must exist and carry a .theos.yaml naming this project
// and Container. When it also carries every file of the Container's Standard
// with the content the Standard renders for it at its ref, main is protected
// and the Container is Ready, its Standard recorded at that ref. When it
// doesn't, the Container is Ready on its identity, its Standard unrecorded
// and main unprotected, and the Standard's files arrive as a pull request
// (#97, adoptStandard). Nothing is recorded as scaffolded. Its spec.operations
// is recorded as it is now, as a recovered scaffold's is.
func (r *ContainerReconciler) adopt(ctx, gitCtx context.Context, container *c4v1beta1.Container,
	status *c4v1beta1.ContainerStatus, res resolved, view *c4v1beta1.Container, provider ports.GitHost,
	repo ports.Repository) (ctrl.Result, error) {
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
	if reason, ok := templateFailure(err); ok {
		return r.fail(ctx, container, status, c4v1beta1.ConditionScaffolded, reason, err.Error())
	}
	if err != nil {
		return r.retry(ctx, container, status, c4v1beta1.ConditionScaffolded, err)
	}
	ci, err := gitProviderCI(res.provider)
	if err != nil {
		return r.fail(ctx, container, status, c4v1beta1.ConditionScaffolded, c4v1beta1.ReasonFailed, err.Error())
	}
	onMain, _, err := provider.ReadFiles(gitCtx, repo, ports.DefaultBranch, []string{scaffold.IdentityPath})
	if errors.Is(err, ports.ErrBranchNotFound) {
		// #1908: the commits History found are on another branch; no retry
		// makes main appear.
		return r.fail(ctx, container, status, c4v1beta1.ConditionScaffolded, c4v1beta1.ReasonNoDefaultBranch,
			fmt.Sprintf("Repository %s has commits but no %s branch; an adopted repository names its project and "+
				"Container and carries its Standard's files on %s, so rename its default branch to %s first",
				repo.WebURL, ports.DefaultBranch, ports.DefaultBranch, ports.DefaultBranch))
	}
	if err != nil {
		return r.retry(ctx, container, status, c4v1beta1.ConditionScaffolded, err)
	}
	identity, found := onMain[scaffold.IdentityPath]
	if !found {
		return r.fail(ctx, container, status, c4v1beta1.ConditionScaffolded, c4v1beta1.ReasonIdentityMismatch,
			fmt.Sprintf("Repository %s has no %s on %s; an adopted repository names its project and Container there",
				repo.WebURL, scaffold.IdentityPath, ports.DefaultBranch))
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
	// The Standard renders with the license the identity records, as a later
	// Standard update renders it (docs/plans/license.md, decision 5).
	var license scaffold.License
	if id.License != "" {
		if license, err = r.resolveLicense(ctx, container); err != nil {
			return r.retry(ctx, container, status, c4v1beta1.ConditionScaffolded, err)
		}
		license.ID = id.License
	}
	// #42: the Standard renders its agent guides with the system's Domains,
	// so the check tries the Domains the repository was scaffolded with,
	// then the system's, then none, and the first that renders main's files
	// is recorded as the ones it holds.
	domainSets, err := r.adoptionDomains(ctx, container.Namespace, id.ScaffoldedFrom, res.domains)
	if err != nil {
		return r.retry(ctx, container, status, c4v1beta1.ConditionScaffolded, err)
	}
	checks := model.EffectiveChecks(string(container.Spec.Checks), res.standard != nil && res.standard.ChecksBeforeRelease)
	renders := make([]scaffold.Tree, len(domainSets))
	paths := map[string]bool{}
	var standardRef string
	for i, domains := range domainSets {
		in := scaffoldInput(view, ci, nil, domains, checks, r.ciImages(), specOperations(container), repo.NpmScope, license)
		renders[i] = scaffold.Tree{}
		for _, layer := range layers {
			tree, err := scaffold.RenderLayer(in, layer)
			if err != nil {
				return r.fail(ctx, container, status, c4v1beta1.ConditionScaffolded, c4v1beta1.ReasonFailed, err.Error())
			}
			renders[i], standardRef = tree, layer.Ref
		}
		for _, p := range renders[i].Paths() {
			paths[p] = true
		}
	}
	onMain, _, err = provider.ReadFiles(gitCtx, repo, ports.DefaultBranch, slices.Sorted(maps.Keys(paths)))
	if err != nil {
		return r.retry(ctx, container, status, c4v1beta1.ConditionScaffolded, err)
	}
	differ := make([]int, len(renders))
	adopted := -1
	for i, rendered := range renders {
		differ[i] = len(scaffold.ChangedPaths(rendered, onMain))
		if differ[i] == 0 {
			adopted = i
			break
		}
	}
	if adopted < 0 {
		// #97: a Standard is something an adopted repository grows into. The
		// Container is Ready on its identity, with the Domains of the render
		// main is closest to; nothing is written or protected here, and the
		// Standard isn't recorded. The Ready Container's next pass opens the
		// pull request with the files that differ (adoptStandard).
		domains := domainSets[model.ClosestRender(differ)]
		log.Info("Adopted repository without its Standard's files", "url", repo.WebURL,
			"standard", res.choices.Standard, "ref", standardRef, "domains", domains)
		return r.ready(ctx, container, status, "", "", nil, domains, nil, checks,
			frameworkEntry(res.entries).ReadsOperations, fmt.Sprintf(
				"Adopted repository %s; the files of its Standard %s arrive as a pull request", repo.WebURL,
				res.choices.Standard))
	}
	domains := domainSets[adopted]

	if err := protectRepository(gitCtx, r.Client, provider, repo, view); err != nil {
		return r.retry(ctx, container, status, c4v1beta1.ConditionScaffolded, err)
	}
	log.Info("Adopted repository", "url", repo.WebURL, "standard", res.choices.Standard, "ref", standardRef,
		"domains", domains)
	return r.ready(ctx, container, status, res.choices.Standard, standardRef, nil, domains, nil, checks,
		frameworkEntry(res.entries).ReadsOperations, fmt.Sprintf("Adopted repository %s", repo.WebURL))
}

// adoptionDomains are the sets of Domains an adoption tries the Standard
// with (model.AdoptionDomains): a name in the identity's scaffoldedFrom is a
// Domain when the project's catalog has a Domain by that name.
func (r *ContainerReconciler) adoptionDomains(ctx context.Context, ns string, scaffoldedFrom,
	systemDomains []string) ([][]string, error) {
	domains := map[string]bool{}
	for _, pinned := range scaffoldedFrom {
		name, _, _ := strings.Cut(pinned, "@")
		if _, seen := domains[name]; seen {
			continue
		}
		found, err := r.lookup(ctx, ns, name, &c4v1beta1.Domain{})
		if err != nil {
			return nil, err
		}
		domains[name] = found
	}
	return model.AdoptionDomains(scaffoldedFrom, func(name string) bool { return domains[name] }, systemDomains), nil
}

// fetchLayers fetches every catalog entry's template at its pinned ref, in layer
// order. The GitProvider's credentials are used for templates on the same host.
func (r *ContainerReconciler) fetchLayers(ctx context.Context, res resolved) ([]scaffold.Layer, error) {
	layers := make([]scaffold.Layer, 0, len(res.entries))
	for _, entry := range res.entries {
		var creds *ports.Credentials
		if ports.SameHost(res.provider.Spec.URL, entry.TemplateURL) {
			creds = &res.creds
		}
		files, err := r.Git.FetchTemplate(ctx, entry.TemplateURL, entry.TemplateRef, entry.TemplatePath, creds)
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
// rendered the Standard's files; empty reads as EveryChange. The Standard is
// recorded as rendered with the operator's CI images (#284): the scaffold's
// own, the ones an adopted repository was checked against, and, for a
// recovered scaffold, the ones the earlier attempt most likely pushed with,
// as its commit doesn't say. readsOperations says the Framework
// rendered spec.operations into the repository (#1209): only then is the
// block recorded, so a Framework that doesn't read it never reports it
// applied.
func (r *ContainerReconciler) ready(ctx context.Context, container *c4v1beta1.Container,
	status *c4v1beta1.ContainerStatus, standard, standardRef string, from, domains []string,
	relationships []model.AppliedRelationship, checks string, readsOperations bool, message string) (ctrl.Result, error) {
	status.Phase = c4v1beta1.ContainerReady
	status.ScaffoldedFrom = from
	status.ScaffoldedDomains = domains
	if standard != "" && standardRef != "" {
		status.Standard = &c4v1beta1.ContainerStandardStatus{Name: standard, Ref: standardRef,
			Checks: c4v1beta1.ChecksMode(checks), CIImages: ciImagesRecord(r.ciImages())}
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
	return ctrl.Result{RequeueAfter: hostRecheck(ctx, r.checksRecheck())}, r.writeStatus(ctx, container, status)
}

// showScaffolding writes the Scaffolding phase, unless the status already
// shows it.
func (r *ContainerReconciler) showScaffolding(ctx context.Context, container *c4v1beta1.Container,
	status *c4v1beta1.ContainerStatus) error {
	if status.Phase == c4v1beta1.ContainerScaffolding {
		return nil
	}
	status.Phase = c4v1beta1.ContainerScaffolding
	r.condition(container, status, c4v1beta1.ConditionScaffolded, metav1.ConditionFalse,
		c4v1beta1.ReasonScaffolding, "Creating the repository and rendering the templates")
	return r.writeStatus(ctx, container, status)
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
// (ports.Repository.NpmScope; empty means ports.DefaultNpmScope).
// license is the license the repository carries (resolveLicense); a caller
// that only writes a message passes none. images are the toolchain images a
// GitHub check falls back to (#284): the operator's (ContainerReconciler.ciImages)
// for what a scaffold, an adoption or an update brings, the recorded ones
// (scaffoldedCIImages) for what the repository's files are at.
func scaffoldInput(container *c4v1beta1.Container, ci model.CI, connectors, domains []string,
	checks string, images model.CIImages, ops model.Operations, npmScope string, license scaffold.License) scaffold.Input {
	if npmScope == "" {
		npmScope = ports.DefaultNpmScope
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
		Domains:        domains,
		Checks:         checks,
		Operations:     ops,
		NpmScope:       npmScope,
		License:        license,
		CIImages:       images,
	}
	return in
}

// ciImages are the toolchain images the operator renders a scaffold, an
// adoption and the files a Standard update brings with: its own, each one it
// was given none of the release's (#284).
func (r *ContainerReconciler) ciImages() model.CIImages {
	return r.CIImages.OrDefault()
}

// scaffoldedCIImages are the toolchain images the repository's Standard files
// were last rendered with, as status.standard.ciImages records them; none
// recorded, or no Standard, reads as model.LegacyCIImages (#284).
func scaffoldedCIImages(status *c4v1beta1.ContainerStatus) model.CIImages {
	if status.Standard == nil {
		return model.LegacyCIImages()
	}
	return recordedCIImages(status.Standard.CIImages)
}

// recordedCIImages are the images a status record names, each it leaves
// empty model.LegacyCIImages' (#284).
func recordedCIImages(images *c4v1beta1.CIImages) model.CIImages {
	var c model.CIImages
	if images != nil {
		c = model.CIImages{Go: images.Go, Base: images.Base}
	}
	return c.OrLegacy()
}

// ciImagesRecord is images as a status records them.
func ciImagesRecord(images model.CIImages) *c4v1beta1.CIImages {
	return &c4v1beta1.CIImages{Go: images.Go, Base: images.Base}
}

// resolveLicense is the license the Container's repository carries: its own
// spec.license, else its Project's, the Project read by its namespace's name
// (#1788). A Project that is missing leaves only the Container's, with the
// namespace as the holder; the namespace is the Project's name.
func (r *ContainerReconciler) resolveLicense(ctx context.Context,
	container *c4v1beta1.Container) (scaffold.License, error) {
	var project c4v1beta1.Project
	if _, err := r.lookup(ctx, container.Namespace, container.Namespace, &project); err != nil {
		return scaffold.License{}, fmt.Errorf("reading Project %s: %w", container.Namespace, err)
	}
	return scaffold.ResolveLicense(string(container.Spec.License), string(project.Spec.License),
		project.Spec.DisplayName, container.Namespace, time.Now()), nil
}

// scaffoldedLicense is the license the Container's repository carries: the
// id its .theos.yaml on main records, with the holder and the year as a
// scaffold resolves them. An update renders with it, never with spec.license
// as it stands: changing the setting after the scaffold rewrites nothing
// (docs/plans/license.md, decision 5), so a layer writing LICENSE collides
// only while the repository holds the operator's. A repository recording no
// id, or none from before the identity file, carries none.
func (r *ContainerReconciler) scaffoldedLicense(ctx, gitCtx context.Context, git *gitAccess,
	container *c4v1beta1.Container) (scaffold.License, error) {
	files, _, err := git.provider.ReadFiles(gitCtx, git.provider.Repository(container.Name), ports.DefaultBranch,
		[]string{scaffold.IdentityPath})
	if err != nil {
		return scaffold.License{}, err
	}
	f, ok := files[scaffold.IdentityPath]
	if !ok {
		return scaffold.License{}, nil
	}
	identity, err := scaffold.ParseIdentity(f.Data)
	if err != nil {
		return scaffold.License{}, fmt.Errorf("reading %s: %w", scaffold.IdentityPath, err)
	}
	if identity.License == "" {
		return scaffold.License{}, nil
	}
	license, err := r.resolveLicense(ctx, container)
	if err != nil {
		return scaffold.License{}, err
	}
	license.ID = identity.License
	return license, nil
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
