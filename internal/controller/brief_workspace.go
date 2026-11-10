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
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/scaffold"
)

// briefWorkspaceMessage is the message of every commit the operator makes on a
// Brief repository's main.
const briefWorkspaceMessage = "Update the brief, the model and the catalog for the planner\n"

// briefIssuesTimeout bounds reading one Container's open issues, so a few
// unreachable hosts don't stall the Brief.
const briefIssuesTimeout = 30 * time.Second

// briefGitSecret names the Secret holding the Git credentials of Brief brief's bot.
func briefGitSecret(brief string) string { return model.BriefRepository(brief) + "-git" }

// errNeedsInput says what the person has to make before the Brief can go on,
// in one sentence for the NeedsInput condition.
type errNeedsInput struct{ sentence string }

func (e errNeedsInput) Error() string { return e.sentence }

// briefGit reads the Brief's GitProvider and its credentials. A missing one is
// errNeedsInput, naming it.
func (r *BriefReconciler) briefGit(ctx context.Context, brief *c4v1beta1.Brief) (interface {
	ports.GitHost
	ports.AgentAccess
}, error) {
	name := brief.Spec.GitProviderRef.Name
	var provider c4v1beta1.GitProvider
	err := r.Get(ctx, client.ObjectKey{Namespace: brief.Namespace, Name: name}, &provider)
	if apierrors.IsNotFound(err) {
		return nil, errNeedsInput{fmt.Sprintf("GitProvider %s is missing: make it in namespace %s, "+
			"or name another in spec.gitProviderRef, so the Brief has a repository to plan in", name, brief.Namespace)}
	}
	if err != nil {
		return nil, err
	}
	creds, found, err := readCredentials(ctx, r.APIReader, &provider)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, errNeedsInput{fmt.Sprintf("Secret %s of GitProvider %s is missing: make it with the Git host's "+
			"username and token, so the Brief has a repository to plan in", provider.Spec.CredentialsSecretRef.Name, name)}
	}
	return r.Git.Provider(providerConfig(&provider, creds, packagesOf(&provider)))
}

// prepareWorkspace makes the Brief's repository and its bot the first time,
// and brings main's brief.md, model/ and PLANNER.md up to date. It writes
// nothing when main already holds them as they are. It returns the
// repository's web address.
func (r *BriefReconciler) prepareWorkspace(ctx context.Context, brief *c4v1beta1.Brief) (string, error) {
	provider, err := r.briefGit(ctx, brief)
	if err != nil {
		return "", err
	}
	ws, err := r.gatherWorkspace(ctx, brief)
	if err != nil {
		return "", err
	}
	recordSuggestions(ctx, brief, ws.Suggestions, slices.ContainsFunc(ws.Containers,
		func(c model.WorkspaceContainer) bool { return c.IssuesUnread }))
	r.recordFailed(ctx, brief, ws.Failed, slices.ContainsFunc(ws.Containers,
		func(c model.WorkspaceContainer) bool { return c.IssuesUnread }))
	rendered, err := model.BriefWorkspaceFiles(ws)
	if err != nil {
		return "", err
	}
	files := scaffold.Tree{}
	for p, data := range rendered {
		files[p] = scaffold.File{Data: []byte(data)}
	}

	repoName := model.BriefRepository(brief.Name)
	var clash c4v1beta1.Container
	if err := r.Get(ctx, client.ObjectKey{Namespace: brief.Namespace, Name: repoName}, &clash); err == nil {
		return "", errNeedsInput{fmt.Sprintf("Container %s has the repository the Brief needs: rename the Brief, "+
			"so its repository %s is its own", repoName, repoName)}
	} else if !apierrors.IsNotFound(err) {
		return "", err
	}

	gitCtx, cancel := context.WithTimeout(ctx, r.gitTimeout())
	defer cancel()
	repo, err := provider.EnsureRepository(gitCtx, model.BriefRepository(brief.Name),
		fmt.Sprintf("Brief %s/%s: the planner's workspace", brief.Namespace, brief.Name))
	if err != nil {
		return "", err
	}
	if err := writeWorkspace(gitCtx, provider, repo, files); err != nil {
		return "", err
	}
	if brief.Status.RepositoryURL != repo.WebURL {
		// Only the operator writes main; the bot pushes plan branches. Protected
		// before the bot exists, so it never could, and again when the
		// repository moved to another GitProvider.
		if err := provider.ProtectBranch(gitCtx, repo, ports.DefaultBranch,
			ports.BranchRule{OperatorPushes: true}); err != nil {
			return "", fmt.Errorf("protecting %s: %w", ports.DefaultBranch, err)
		}
	}
	// A host that gives agents no credential, as its provider is configured,
	// has none for the planner: the Brief asks for what the provider lacks.
	if ok, _, why := ports.AgentAccessState(provider); !ok {
		return "", errNeedsInput{fmt.Sprintf("GitProvider %s gives agents no credential, so the Brief's planner "+
			"has none: %s", brief.Spec.GitProviderRef.Name, why)}
	}
	if err := r.holdForBot(ctx, brief); err != nil {
		return "", err
	}
	// A plan run asks for a token that outlives it when it starts
	// (TaskReconciler.planSandbox); here the Secret only has to be there.
	if _, err := ensureAgentSecret(ctx, r.Client, r.APIReader, brief, briefGitSecret(brief.Name),
		briefBotLabels(brief), briefBotExtra(repo), r.now(), 0,
		func() (ports.Credentials, error) {
			return provider.EnsureAgentAccess(gitCtx, repo,
				model.AgentUsername(brief.Namespace, model.BriefRepository(brief.Name)))
		}); err != nil {
		return "", err
	}
	return repo.WebURL, nil
}

// briefBotLabels and briefBotExtra are the labels of a Brief bot's Secret
// and what it keeps beside the credential: the repository it is for, so
// another repository's is minted again.
func briefBotLabels(brief *c4v1beta1.Brief) map[string]string {
	return map[string]string{c4v1beta1.LabelProject: brief.Namespace, c4v1beta1.LabelBrief: brief.Name}
}

func briefBotExtra(repo ports.Repository) map[string]string {
	return map[string]string{"repository": repo.CloneURL}
}

// briefBotFinalizer keeps a Brief until the operator deleted its bot on the
// Git host.
const briefBotFinalizer = "theos.factory.io/brief-bot"

// holdForBot puts briefBotFinalizer on the Brief before it has a bot, so its
// deletion waits for deleteBot.
func (r *BriefReconciler) holdForBot(ctx context.Context, brief *c4v1beta1.Brief) error {
	if controllerutil.ContainsFinalizer(brief, briefBotFinalizer) {
		return nil
	}
	// Patching only the metadata of a copy keeps brief's status for its own patch.
	held := brief.DeepCopy()
	controllerutil.AddFinalizer(held, briefBotFinalizer)
	if err := r.Patch(ctx, held, client.MergeFrom(brief)); err != nil {
		return err
	}
	brief.Finalizers, brief.ResourceVersion = held.Finalizers, held.ResourceVersion
	return nil
}

// botDeleteRetry is how often a deleted Brief tries again to delete its bot
// on a Git host that refused or didn't answer.
const botDeleteRetry = 30 * time.Second

// Reasons of the Events about a Brief's bot.
const (
	eventBotNotDeleted = "BotNotDeleted"
	eventBotLeft       = "BotLeft"
)

// deleteBot deletes the bot of a Brief being deleted from its Git host, then
// lets the Brief go. Its repository and the model it made stay (B16). With
// its GitProvider or credentials gone there is no host to reach, and the bot
// is left. A host that fails the delete is tried again until the namespace is
// terminating or BotDeleteTimeout has passed since the deletion; then the bot
// is left too.
func (r *BriefReconciler) deleteBot(ctx context.Context, brief *c4v1beta1.Brief) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(brief, briefBotFinalizer) {
		return ctrl.Result{}, nil
	}
	log := logf.FromContext(ctx)
	user := model.AgentUsername(brief.Namespace, model.BriefRepository(brief.Name))
	provider, err := r.briefGit(ctx, brief)
	if sentence, unreachable := needsInputSentence(err); unreachable {
		r.leaveBot(ctx, brief, user, sentence)
	} else if err != nil {
		return ctrl.Result{}, err
	} else {
		gitCtx, cancel := context.WithTimeout(ctx, r.gitTimeout())
		err := provider.RemoveAgentAccess(gitCtx, provider.Repository(model.BriefRepository(brief.Name)), user)
		cancel()
		if err == nil {
			log.Info("Deleted the Brief's bot", "user", user)
		} else if why := r.giveUpOnBot(ctx, brief); why != "" {
			r.leaveBot(ctx, brief, user, fmt.Sprintf("deleting it failed (%v), and %s", err, why))
		} else {
			deadline := brief.DeletionTimestamp.Add(r.botDeleteTimeout())
			log.Info("Could not delete the Brief's bot; trying again", "user", user, "error", err.Error(),
				"until", deadline.UTC().Format(time.RFC3339))
			r.event(brief, corev1.EventTypeWarning, eventBotNotDeleted, "DeleteBot", fmt.Sprintf(
				"Could not delete bot %s on the Git host: %v. Trying again until %s, then the bot is left there.",
				user, err, deadline.UTC().Format(time.RFC3339)))
			return ctrl.Result{RequeueAfter: min(botDeleteRetry, max(time.Until(deadline), time.Second))}, nil
		}
	}
	orig := brief.DeepCopy()
	controllerutil.RemoveFinalizer(brief, briefBotFinalizer)
	return ctrl.Result{}, client.IgnoreNotFound(r.Patch(ctx, brief, client.MergeFrom(orig)))
}

// giveUpOnBot says why a deleted Brief stops trying to delete its bot: its
// namespace is terminating, or BotDeleteTimeout has passed. It is empty while
// the Brief should try again.
func (r *BriefReconciler) giveUpOnBot(ctx context.Context, brief *c4v1beta1.Brief) string {
	// An operator with only namespaced RBAC can't read the Namespace; the
	// timeout still bounds its wait.
	var ns corev1.Namespace
	if err := r.APIReader.Get(ctx, client.ObjectKey{Name: brief.Namespace}, &ns); err == nil &&
		(!ns.DeletionTimestamp.IsZero() || ns.Status.Phase == corev1.NamespaceTerminating) {
		return fmt.Sprintf("namespace %s is terminating", brief.Namespace)
	}
	if time.Since(brief.DeletionTimestamp.Time) >= r.botDeleteTimeout() {
		return fmt.Sprintf("the Brief has waited %s for it", r.botDeleteTimeout())
	}
	return ""
}

// leaveBot logs and records that the deleted Brief goes without deleting its
// bot on the Git host, and why.
func (r *BriefReconciler) leaveBot(ctx context.Context, brief *c4v1beta1.Brief, user, reason string) {
	logf.FromContext(ctx).Info("Left the Brief's bot on the Git host", "user", user, "reason", reason)
	r.event(brief, corev1.EventTypeWarning, eventBotLeft, "LeaveBot", fmt.Sprintf(
		"Left bot %s on the Git host: %s. Delete the user there by hand.", user, reason))
}

// writeWorkspace puts files on main: as its first commit in an empty
// repository, else as one more commit when any of them differs.
func writeWorkspace(ctx context.Context, provider ports.GitHost, repo ports.Repository,
	files scaffold.Tree) error {
	author := ports.Signature{Name: scaffold.AuthorName, Email: scaffold.AuthorEmail, When: time.Now()}
	commits, err := provider.History(ctx, repo, 1)
	if err != nil {
		return err
	}
	if len(commits) == 0 {
		_, err := provider.PushInitialCommit(ctx, repo, files, briefWorkspaceMessage, author)
		return err
	}
	current, head, err := provider.ReadFiles(ctx, repo, ports.DefaultBranch, model.WorkspaceFiles)
	if err != nil {
		return err
	}
	if maps.EqualFunc(current, files, func(a, b scaffold.File) bool { return string(a.Data) == string(b.Data) }) {
		return nil
	}
	_, err = provider.PushDefaultBranch(ctx, repo, head, files, nil, briefWorkspaceMessage, author)
	if errors.Is(err, ports.ErrEmptyChange) {
		return nil
	}
	return err
}

// gatherWorkspace reads what the planner needs: the Brief, the namespace's
// story, its catalog and its Containers' open issues.
func (r *BriefReconciler) gatherWorkspace(ctx context.Context, brief *c4v1beta1.Brief) (model.BriefWorkspace, error) {
	status := &brief.Status
	outcomes := make([]string, 0, len(status.History))
	for _, h := range status.History {
		outcomes = append(outcomes, h.Outcome)
	}
	roundRuns := status.Round > 0 && !roundOver(status)
	ws := model.BriefWorkspace{
		Brief:               brief.Name,
		Idea:                brief.Spec.Idea,
		Prompts:             model.WorkspacePrompts(briefPrompts(brief), int(status.RoundPrompts), roundRuns),
		PromptsPlanned:      int(status.PromptsPlanned),
		IdeaPlanned:         model.IdeaPlanned(outcomes, int(status.PromptsPlanned)),
		Namespace:           brief.Namespace,
		GitProvider:         brief.Spec.GitProviderRef.Name,
		Tended:              modelTended(status.Tended),
		Tending:             roundRuns && tending(status),
		SpecParallel:        brief.Spec.Parallel,
		Cadence:             briefModelCadence(brief),
		PromptParallel:      status.Parallel,
		PromptParallelRound: status.ParallelRound,
	}
	if !ws.Tending {
		ws.Requests = model.WorkspaceRequests(modelRequests(status.Requests), status.RoundRequests, roundRuns)
	}
	for _, entry := range brief.Spec.Releases {
		ws.Releases = append(ws.Releases, model.WorkspaceRelease{Container: entry.Container,
			Repository: entry.GitHub.Repository, TagPrefix: entry.TagPrefix})
	}
	var err error
	if ws.Objects, err = storyObjects(ctx, r.Client, brief.Namespace); err != nil {
		return ws, err
	}
	if ws.Catalog, err = installedCatalog(ctx, r.Client, brief.Namespace); err != nil {
		return ws, err
	}
	if ws.Bundles, err = installedBundles(ctx, r.Client, brief.Namespace); err != nil {
		return ws, err
	}
	if ws.Environments, err = projectEnvironments(ctx, r.Client, brief.Namespace); err != nil {
		return ws, err
	}
	ws.Containers, ws.Suggestions, ws.Failed, err = r.containerIssues(ctx, brief)
	return ws, err
}

// containerIssues lists each Container that has a repository, with its open
// issues, whether a Crew of it tags releases, and the reviewers' suggestions
// and failed issues waiting in the ones the Brief made. Issues that can't be
// read are marked so, not an error: one broken repository doesn't stop the
// Brief, and its suggestions and failed issues are the ones the Brief last saw.
func (r *BriefReconciler) containerIssues(ctx context.Context, brief *c4v1beta1.Brief) (
	[]model.WorkspaceContainer, []model.Suggestion, []model.FailedIssue, error) {
	var containers c4v1beta1.ContainerList
	if err := r.List(ctx, &containers, client.InNamespace(brief.Namespace)); err != nil {
		return nil, nil, nil, err
	}
	var crews c4v1beta1.CrewList
	if err := r.List(ctx, &crews, client.InNamespace(brief.Namespace)); err != nil {
		return nil, nil, nil, err
	}
	var taskList c4v1beta1.TaskList
	if err := r.List(ctx, &taskList, client.InNamespace(brief.Namespace)); err != nil {
		return nil, nil, nil, err
	}
	tasks := issueTasks(taskList.Items)
	released := map[string]bool{}
	for _, crew := range crews.Items {
		if crew.Spec.TagReleases {
			released[crew.Spec.ContainerRef.Name] = true
		}
	}
	providers := map[string]interface {
		ports.GitHost
		ports.Issues
	}{}
	var out []model.WorkspaceContainer
	var suggestions []model.Suggestion
	var failed []model.FailedIssue
	for _, c := range containers.Items {
		if c.Status.RepositoryURL == "" {
			continue
		}
		mine := c.Labels[c4v1beta1.LabelBrief] == brief.Name
		entry := model.WorkspaceContainer{Name: c.Name, Repository: c.Status.RepositoryURL,
			Released: released[c.Name], Checks: string(c.Spec.Checks)}
		issues, err := r.openIssues(ctx, providers, &c)
		if err != nil {
			logf.FromContext(ctx).Info("Could not read a Container's issues", "container", c.Name, "error", err.Error())
			entry.IssuesUnread = true
			if mine {
				for _, s := range lastSuggestions(brief, c.Name) {
					s.Repository = repositoryName(&c)
					suggestions = append(suggestions, s)
				}
				failed = append(failed, lastFailed(brief, c.Name)...)
			}
		}
		for _, i := range issues {
			entry.Issues = append(entry.Issues, model.WorkspaceIssue{Number: i.Number, Title: i.Title})
			if mine && model.IsSuggestion(i.Labels, i.Body) {
				suggestions = append(suggestions, waitingSuggestion(&c, i))
			}
			if mine && model.IsFailedIssue(i.Labels) {
				f := failedIssue(c.Name, i.Number, i.Title, i.URL, i.Labels, i.Body, tasks[c.Name])
				if f.Release, err = r.readFailedRelease(ctx, providers[c.Spec.GitProviderRef.Name], &c, i,
					tasks[c.Name]); err != nil {
					// The plan's check reads it again before it closes the step.
					logf.FromContext(ctx).Info("Could not read a release step's milestone", "container", c.Name,
						"number", i.Number, "error", err.Error())
				}
				if f.ProposalGone, err = r.readGoneProposal(ctx, providers[c.Spec.GitProviderRef.Name], &c,
					i); err != nil {
					// The plan's check reads it again before it retries the change.
					logf.FromContext(ctx).Info("Could not read a Standard change's proposal branch",
						"container", c.Name, "number", i.Number, "error", err.Error())
				}
				if f.ProposalGone != "" {
					f.Reason = model.ProposalGoneNote(f.ProposalGone) + " " + f.Reason
				}
				failed = append(failed, f)
			}
		}
		out = append(out, entry)
	}
	return out, suggestions, failed, nil
}

// readFailedRelease is failedRelease with the workspace's Git timeout.
func (r *BriefReconciler) readFailedRelease(ctx context.Context, provider interface {
	ports.GitHost
	ports.Issues
},
	c *c4v1beta1.Container, issue ports.Issue, tasks []model.IssueTask) (*model.FailedRelease, error) {
	gitCtx, cancel := context.WithTimeout(ctx, briefIssuesTimeout)
	defer cancel()
	return failedRelease(gitCtx, provider, provider.Repository(c.Name), issue, tasks)
}

// readGoneProposal is goneProposal with the workspace's Git timeout.
func (r *BriefReconciler) readGoneProposal(ctx context.Context, provider ports.GitHost,
	c *c4v1beta1.Container, issue ports.Issue) (string, error) {
	gitCtx, cancel := context.WithTimeout(ctx, briefIssuesTimeout)
	defer cancel()
	return goneProposal(gitCtx, provider, provider.Repository(c.Name), issue)
}

func (r *BriefReconciler) openIssues(ctx context.Context, providers map[string]interface {
	ports.GitHost
	ports.Issues
},
	c *c4v1beta1.Container) ([]ports.Issue, error) {
	name := c.Spec.GitProviderRef.Name
	provider, ok := providers[name]
	if !ok {
		var err error
		if provider, err = providerFor(ctx, r.Client, r.APIReader, r.Git, c.Namespace, name); err != nil {
			return nil, err
		}
		providers[name] = provider
	}
	gitCtx, cancel := context.WithTimeout(ctx, briefIssuesTimeout)
	defer cancel()
	return provider.ListIssues(gitCtx, provider.Repository(c.Name), "")
}

// storyObjects reads the namespace's story kinds, the ones a plan may write.
func storyObjects(ctx context.Context, c client.Reader, ns string) ([]model.PlanObject, error) {
	var out []model.PlanObject
	in := client.InNamespace(ns)
	var projects c4v1beta1.ProjectList
	if err := c.List(ctx, &projects, in); err != nil {
		return nil, err
	}
	for _, o := range projects.Items {
		out = append(out, model.PlanObject{Kind: model.KindProject, Name: o.Name,
			DisplayName: o.Spec.DisplayName, Description: o.Spec.Description})
	}
	var persons c4v1beta1.PersonList
	if err := c.List(ctx, &persons, in); err != nil {
		return nil, err
	}
	for _, o := range persons.Items {
		out = append(out, model.PlanObject{Kind: model.KindPerson, Name: o.Name,
			DisplayName: o.Spec.DisplayName, Description: o.Spec.Description})
	}
	var systems c4v1beta1.SoftwareSystemList
	if err := c.List(ctx, &systems, in); err != nil {
		return nil, err
	}
	for _, o := range systems.Items {
		out = append(out, model.PlanObject{Kind: model.KindSoftwareSystem, Name: o.Name,
			DisplayName: o.Spec.DisplayName, Description: o.Spec.Description, Needs: o.Spec.Needs, KPIs: o.Spec.KPIs,
			Location: string(o.Spec.Location), Domains: o.Spec.DomainRefs})
	}
	var containers c4v1beta1.ContainerList
	if err := c.List(ctx, &containers, in); err != nil {
		return nil, err
	}
	for _, o := range containers.Items {
		s, picked := o.Spec, o.Choices()
		out = append(out, model.PlanObject{Kind: model.KindContainer, Name: o.Name,
			DisplayName: s.DisplayName, Description: s.Description, SoftwareSystem: s.SoftwareSystemRef.Name,
			Type: string(s.Type), GitProvider: s.GitProviderRef.Name, Bundle: picked.Bundle,
			Framework: picked.Framework, Standard: picked.Standard, Compute: picked.Compute, Deployment: picked.Deployment,
			Checks: string(s.Checks), Operations: plannedOperations(s.Operations)})
	}
	var stores c4v1beta1.DataStoreList
	if err := c.List(ctx, &stores, in); err != nil {
		return nil, err
	}
	for _, o := range stores.Items {
		out = append(out, model.PlanObject{Kind: model.KindDataStore, Name: o.Name,
			DisplayName: o.Spec.DisplayName, Description: o.Spec.Description,
			SoftwareSystem: o.Spec.SoftwareSystemRef.Name, Connector: o.Spec.ConnectorRef.Name})
	}
	var relationships c4v1beta1.RelationshipList
	if err := c.List(ctx, &relationships, in); err != nil {
		return nil, err
	}
	for _, o := range relationships.Items {
		s := o.Spec
		out = append(out, model.PlanObject{Kind: model.KindRelationship, Name: o.Name, Description: s.Description,
			Source:  model.Ref{Kind: string(s.Source.Kind), Name: s.Source.Name},
			Target:  model.Ref{Kind: string(s.Target.Kind), Name: s.Target.Name},
			Bundles: s.Bundles})
	}
	return out, nil
}

func refName(ref *c4v1beta1.LocalRef) string {
	if ref == nil {
		return ""
	}
	return ref.Name
}

// installedCatalog reads every catalog entry of the namespace.
func installedCatalog(ctx context.Context, c client.Reader, ns string) ([]model.CatalogEntry, error) {
	in := client.InNamespace(ns)
	var out []model.CatalogEntry
	var frameworks c4v1beta1.FrameworkList
	if err := c.List(ctx, &frameworks, in); err != nil {
		return nil, err
	}
	for i := range frameworks.Items {
		out = append(out, catalogEntry(model.SideFramework, &frameworks.Items[i]))
	}
	var standards c4v1beta1.StandardList
	if err := c.List(ctx, &standards, in); err != nil {
		return nil, err
	}
	for i := range standards.Items {
		out = append(out, catalogEntry(model.SideStandard, &standards.Items[i]))
	}
	var computes c4v1beta1.ComputeList
	if err := c.List(ctx, &computes, in); err != nil {
		return nil, err
	}
	for i := range computes.Items {
		out = append(out, catalogEntry(model.SideCompute, &computes.Items[i]))
	}
	var deployments c4v1beta1.DeploymentList
	if err := c.List(ctx, &deployments, in); err != nil {
		return nil, err
	}
	for i := range deployments.Items {
		out = append(out, catalogEntry(model.SideDeployment, &deployments.Items[i]))
	}
	var connectors c4v1beta1.ConnectorList
	if err := c.List(ctx, &connectors, in); err != nil {
		return nil, err
	}
	for i := range connectors.Items {
		out = append(out, catalogEntry(model.SideConnector, &connectors.Items[i]))
	}
	var domains c4v1beta1.DomainList
	if err := c.List(ctx, &domains, in); err != nil {
		return nil, err
	}
	for i := range domains.Items {
		out = append(out, catalogEntry(model.SideDomain, &domains.Items[i]))
	}
	return out, nil
}

// installedBundles reads the namespace's Bundles, each as the entry names it
// picks, the way the operator expands a Container's bundleRef.
func installedBundles(ctx context.Context, c client.Reader, ns string) ([]model.Bundle, error) {
	var bundles c4v1beta1.BundleList
	if err := c.List(ctx, &bundles, client.InNamespace(ns)); err != nil {
		return nil, err
	}
	var out []model.Bundle
	for _, b := range bundles.Items {
		out = append(out, model.Bundle{Name: b.Name, Description: b.Spec.Description, Framework: b.Spec.FrameworkRef.Name,
			Standard: refName(b.Spec.StandardRef), Compute: refName(b.Spec.ComputeRef), Deployment: refName(b.Spec.DeploymentRef)})
	}
	return out, nil
}

// ensureAgentSecret returns Secret name holding a bot's Git credentials, owned
// by owner, making the bot with access and the Secret the first time, and
// again when the Secret's data lacks any of extra, such as the repository the
// bot was given. Made again, the Secret's annotationOldTokensBefore says the
// bot's older tokens may still be live, in sandboxes started before now. The
// Secret is read and written without the cache: the operator doesn't watch
// Secrets.
//
// On a host whose credentials end (ports.Credentials.Expires), the Secret
// keeps when its token ends and how long the host let it live. outlive is
// how long the token must still live, the time limit of the run it is for;
// 0 asks nothing. A token that would end sooner is minted again, and a host
// whose credentials live shorter than outlive gives a *credentialTooShort,
// after one minting and none again: no new token would be longer.
func ensureAgentSecret(ctx context.Context, c client.Client, apiReader client.Reader, owner client.Object,
	name string, labels, extra map[string]string, now time.Time, outlive time.Duration,
	access func() (ports.Credentials, error)) (string, error) {
	var secret corev1.Secret
	err := apiReader.Get(ctx, client.ObjectKey{Namespace: owner.GetNamespace(), Name: name}, &secret)
	current := err == nil && len(secret.Data[secretKeyToken]) > 0
	for k, v := range extra {
		current = current && string(secret.Data[k]) == v
	}
	if ends, lifetime, ending := agentTokenEnds(secret.Data); current && ending && outlive > 0 {
		if lifetime < outlive {
			return "", &credentialTooShort{lives: lifetime, needs: outlive}
		}
		current = !ends.Before(now.Add(outlive))
	}
	if current {
		return name, nil
	}
	if err != nil && !apierrors.IsNotFound(err) {
		return "", err
	}
	found := err == nil
	creds, err := access()
	if err != nil {
		return "", err
	}
	desired := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: owner.GetNamespace(), Name: name, Labels: labels},
		Data:       map[string][]byte{secretKeyUsername: []byte(creds.Username), secretKeyToken: []byte(creds.Token)},
	}
	for k, v := range extra {
		desired.Data[k] = []byte(v)
	}
	lifetime := creds.Expires.Sub(now)
	if !creds.Expires.IsZero() {
		desired.Data[agentSecretExpires] = []byte(creds.Expires.UTC().Format(time.RFC3339))
		desired.Data[agentSecretLifetime] = []byte(strconv.Itoa(int(lifetime.Seconds())))
	}
	if err := controllerutil.SetControllerReference(owner, &desired, c.Scheme()); err != nil {
		return "", err
	}
	if found {
		orig := secret.DeepCopy()
		secret.Labels, secret.Data, secret.OwnerReferences = desired.Labels, desired.Data, desired.OwnerReferences
		if len(orig.Data["token"]) > 0 {
			metav1.SetMetaDataAnnotation(&secret.ObjectMeta, annotationOldTokensBefore, now.UTC().Format(time.RFC3339))
		}
		err = c.Patch(ctx, &secret, client.MergeFrom(orig))
	} else {
		err = c.Create(ctx, &desired)
	}
	if err != nil {
		return "", err
	}
	logf.FromContext(ctx).Info("Created agent Git access", "user", creds.Username, "secret", name)
	if !creds.Expires.IsZero() && lifetime < outlive {
		return "", &credentialTooShort{lives: lifetime, needs: outlive}
	}
	return name, nil
}

// The keys of an agent Git Secret on a host whose credentials end: when the
// token ends (RFC 3339), and how long the host let it live when it was
// minted, in seconds.
const (
	agentSecretExpires  = "expires"
	agentSecretLifetime = "lifetimeSeconds"
)

// credentialFollowUp is how long an agent's credential must live past its
// run's time limit: the operator opens the run's pull request with it.
const credentialFollowUp = 5 * time.Minute

// agentTokenEnds reads when an agent Git Secret's token ends and how long
// the host let it live; ending is false for a token that never ends, as on
// a host whose credentials live until revoked.
func agentTokenEnds(data map[string][]byte) (ends time.Time, lifetime time.Duration, ending bool) {
	ends, err := time.Parse(time.RFC3339, string(data[agentSecretExpires]))
	if err != nil {
		return time.Time{}, 0, false
	}
	seconds, err := strconv.Atoi(string(data[agentSecretLifetime]))
	if err != nil {
		return time.Time{}, 0, false
	}
	return ends, time.Duration(seconds) * time.Second, true
}

// credentialTooShort is the error of an agent credential that the host lets
// live shorter than the run it is for may take: minting again changes
// nothing, a shorter run does.
type credentialTooShort struct {
	lives, needs time.Duration
}

func (e *credentialTooShort) Error() string {
	return fmt.Sprintf("a credential of the Git host lives %s, and the run needs one for %s",
		e.lives.Round(time.Minute), e.needs.Round(time.Minute))
}

// reasonCredentialTooShort is the Progressing reason of a Task whose Agent's
// runs may take longer than a credential of its Git host lives.
const reasonCredentialTooShort = "CredentialTooShort"

// tooShortForAgent is what such a Task says: the two times, and the
// timeoutMinutes that would fit.
func tooShortForAgent(agent *c4v1beta1.Agent, e *credentialTooShort) string {
	fits := int((e.lives - credentialFollowUp) / time.Minute)
	return fmt.Sprintf("A run of Agent %s may take %d minutes, and a credential of its Git host lives %d: the "+
		"run could end without one. Set the Agent's spec.timeoutMinutes to %d or less.", agent.Name,
		int(runTimeout(agent)/time.Minute), int(e.lives/time.Minute), fits)
}

// needsInputSentence returns the sentence of an errNeedsInput in err.
func needsInputSentence(err error) (string, bool) {
	if e, ok := errors.AsType[errNeedsInput](err); ok {
		return strings.TrimSpace(e.sentence), true
	}
	return "", false
}
