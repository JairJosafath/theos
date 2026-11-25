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

package nocluster

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"slices"
	"strings"
	"time"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/modelfacts"
	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/scaffold"
)

// Hosts builds the GitHost a GitProvider's config reaches. cmd/theos builds
// it from the host adapters; tests use the ports' fake.
type Hosts interface {
	GitHost(cfg ports.Config) (ports.GitHost, error)
}

// Env is what the commands run on.
type Env struct {
	Hosts Hosts
	// Templates fetches the catalog entries' templates at their pinned refs.
	Templates modelfacts.TemplateFetcher
	// CredentialHost reads the one place the credential is for
	// (CredentialHost); the commands ask it before any request.
	CredentialHost func(ctx context.Context) (Bound, error)
	// Credential reads the credential for a GitProvider's url where b, the
	// credential's place, lets it go (Credential).
	Credential func(ctx context.Context, b Bound, hostURL string) (ports.Credentials, error)
	// Now is the clock a license's year and a commit's time are read from.
	Now func() time.Time
	// Out is where the commands report.
	Out io.Writer
	// Project is the project repository, where status looks for the lead's
	// files (agentDrift); nil looks for none.
	Project fs.FS
	// Runtime is the lead runtime of container on host, its markers counted
	// by rules: cmd/theos's hostrun.New. nil runs no lead command.
	Runtime func(host LeadHost, container string, rules model.ClaimRules) ports.Runtime
	// Clone clones repo's branch into dir, under the project repository, for
	// a claimed Task's work; nil clones nothing.
	Clone func(ctx context.Context, repo ports.Repository, branch, dir string) error
}

// container is one Container of the model, resolved and on its host.
type container struct {
	obj *c4v1beta1.Container
	// view is the Container as the scaffold reads its choices: its
	// status.bundle the resolved Bundle, as the reconciler's withBundle.
	view *c4v1beta1.Container
	res  modelfacts.Resolved
	host ports.GitHost
	// templates fetches its templates, with the credential on its host only.
	templates modelfacts.TemplateFetcher
}

// containers resolves each Container of m, sorted by name, and reaches its
// host with the credential env reads for it. Before any request, and
// before git credential fill is asked for any, a GitProvider the
// credential may not go to fails the command (CheckHosts), as does a
// Container that misses an object or whose choices the business rules
// refuse, naming what.
func containers(ctx context.Context, env Env, m *Model) ([]container, error) {
	bound, err := env.CredentialHost(ctx)
	if err != nil {
		return nil, err
	}
	objs := m.Objects
	if err := CheckHosts(bound, objs.GitProviders); err != nil {
		return nil, err
	}
	templates := TemplatesFor(bound, env.Templates)
	objs.Secrets = map[string]ports.Credentials{}
	for _, gp := range objs.GitProviders {
		creds, err := env.Credential(ctx, bound, gp.Spec.URL)
		if err != nil {
			return nil, fmt.Errorf("GitProvider %s: %w", gp.Name, err)
		}
		objs.Secrets[gp.Spec.CredentialsSecretRef.Name] = creds
	}
	sorted := slices.Clone(m.Containers)
	slices.SortFunc(sorted, func(a, b c4v1beta1.Container) int { return strings.Compare(a.Name, b.Name) })
	out := make([]container, 0, len(sorted))
	for i := range sorted {
		c := &sorted[i]
		system := modelfacts.Find(m.Systems, c.Spec.SoftwareSystemRef.Name)
		if system == nil {
			return nil, fmt.Errorf("container %s: SoftwareSystem %s is not in %s", c.Name,
				c.Spec.SoftwareSystemRef.Name, ModelFile)
		}
		res, missing := modelfacts.Resolve(c, system, objs)
		if len(missing) > 0 {
			return nil, fmt.Errorf("container %s: the model has no %s", c.Name, strings.Join(missing, ", "))
		}
		if err := modelfacts.Check(c, res); err != nil {
			return nil, fmt.Errorf("container %s: %w", c.Name, err)
		}
		host, err := env.Hosts.GitHost(modelfacts.Config(res.Provider, res.Credentials))
		if err != nil {
			return nil, fmt.Errorf("container %s: GitProvider %s: %w", c.Name, res.Provider.Name, err)
		}
		view := c.DeepCopy()
		view.Status.Bundle = res.Bundle.DeepCopy()
		out = append(out, container{obj: c, view: view, res: res, host: host, templates: templates})
	}
	return out, nil
}

// Scaffold renders each Container not yet scaffolded as the operator does,
// from the same functions on the same objects, and pushes its first commit
// with the operator's trailers into a new repository; then it protects main
// as the operator does. A Container already scaffolded gets nothing pushed,
// only its rule put again; one whose repository holds commits theos did not
// make, or that adopts its repository, is the cluster's to take over and
// fails the command.
func Scaffold(ctx context.Context, env Env, m *Model) error {
	settings, err := m.Settings.Scaffold()
	if err != nil {
		return err
	}
	cs, err := containers(ctx, env, m)
	if err != nil {
		return err
	}
	for _, c := range cs {
		if err := scaffoldOne(ctx, env, m, c, settings); err != nil {
			return fmt.Errorf("container %s: %w", c.obj.Name, err)
		}
	}
	return nil
}

func scaffoldOne(ctx context.Context, env Env, m *Model, c container, settings modelfacts.Settings) error {
	repo, err := c.host.EnsureRepository(ctx, c.obj.Name, modelfacts.RepoDescription(c.obj))
	if err != nil {
		return err
	}
	commits, err := c.host.History(ctx, repo, 2)
	if err != nil {
		return err
	}
	var info scaffold.CommitInfo
	if len(commits) > 0 {
		info, _ = scaffold.ParseCommitMessage(commits[0].Message) // not ours leaves Container empty
	}
	switch model.ScaffoldHistory(len(commits), info.Container, c.obj.Namespace+"/"+c.obj.Name, c.obj.Spec.Adopt) {
	case model.HistoryRecover:
		if err := protect(ctx, env, m, c, repo); err != nil {
			return err
		}
		_, err := fmt.Fprintf(env.Out, "%s: scaffolded already at %s; pushed nothing\n", c.obj.Name, repo.WebURL)
		return err
	case model.HistoryForeign:
		return fmt.Errorf("repository %s has commits theos didn't make; it only scaffolds into a new, empty repository",
			repo.WebURL)
	case model.HistoryAdopt, model.HistoryNothingToAdopt:
		return errors.New("spec.adopt takes over an existing repository, which an operator does; theos scaffold " +
			"makes new ones only")
	}

	layers, err := modelfacts.FetchLayers(ctx, c.templates, c.res)
	if err != nil {
		return err
	}
	ci, err := modelfacts.CI(c.res.Provider)
	if err != nil {
		return err
	}
	license := modelfacts.License(c.obj, m.Objects.Project, env.Now())
	render, err := modelfacts.Build(c.view, c.res, layers, ci, repo.NpmScope, license, settings)
	if err != nil {
		return err
	}
	author := ports.Signature{Name: scaffold.AuthorName, Email: scaffold.AuthorEmail, When: env.Now()}
	if _, err := c.host.PushInitialCommit(ctx, repo, render.Result.Files, render.Message, author); err != nil {
		return err
	}
	if err := protect(ctx, env, m, c, repo); err != nil {
		return err
	}
	_, err = fmt.Fprintf(env.Out, "%s: pushed the initial commit with %d files to %s, from %s\n", c.obj.Name,
		len(render.Result.Files), repo.WebURL, strings.Join(render.Result.ScaffoldedFrom, ", "))
	return err
}

// Protect puts on each Container's main the rule the operator puts
// (modelfacts.Protection) and keeps the release tags to the protecting
// account. A Container without a Standard has no checks to require, so its
// main is left as it is.
func Protect(ctx context.Context, env Env, m *Model) error {
	cs, err := containers(ctx, env, m)
	if err != nil {
		return err
	}
	for _, c := range cs {
		if err := protect(ctx, env, m, c, c.host.Repository(c.obj.Name)); err != nil {
			return fmt.Errorf("container %s: %w", c.obj.Name, err)
		}
	}
	return nil
}

func protect(ctx context.Context, env Env, m *Model, c container, repo ports.Repository) error {
	var standard *c4v1beta1.Standard
	if name := c.view.Choices().Standard; name != "" {
		standard = modelfacts.Find(m.Objects.Standards, name)
	}
	rule, ok, err := modelfacts.Protection(c.view, c.res.Provider, standard)
	if err != nil {
		return err
	}
	if !ok {
		_, err := fmt.Fprintf(env.Out, "%s: no Standard, so no checks to require; main left unprotected\n", c.obj.Name)
		return err
	}
	if err := c.host.ProtectBranch(ctx, repo, ports.DefaultBranch, rule); err != nil {
		return fmt.Errorf("protecting %s: %w", ports.DefaultBranch, err)
	}
	if err := c.host.ProtectTags(ctx, repo, model.ReleaseTags); err != nil {
		return fmt.Errorf("protecting tags %s: %w", model.ReleaseTags, err)
	}
	_, err = fmt.Fprintf(env.Out, "%s: %s takes changes through pull requests that pass %s and leave %d paths "+
		"alone; tags %s are the protecting account's\n", c.obj.Name, ports.DefaultBranch,
		strings.Join(rule.StatusContexts, ", "), len(rule.ProtectedPaths), model.ReleaseTags)
	return err
}

// Status prints, for each Container, whether it is scaffolded and each of
// its Relationships to a DataStore as the operator's Applied condition
// reads it (AppliedAtScaffold, or AddedAfterScaffold for one added after
// the scaffold); then each Crew's fields the lead runtime does not honour,
// with their reason words (crewNotes); then each of the lead's files that
// drifted from what theos agents writes (agentDrift); then the losses of
// the loss table that apply.
func Status(ctx context.Context, env Env, m *Model) error {
	cs, err := containers(ctx, env, m)
	if err != nil {
		return err
	}
	connectorOf := map[string]string{}
	for _, s := range m.Objects.DataStores {
		connectorOf[s.Name] = s.Spec.ConnectorRef.Name
	}
	for _, c := range cs {
		if err := status(ctx, env, m, c, connectorOf); err != nil {
			return fmt.Errorf("container %s: %w", c.obj.Name, err)
		}
	}
	if err := crewNotes(env.Out, m, model.LeadCapabilities()); err != nil {
		return err
	}
	if err := agentDrift(env.Out, m, env.Project); err != nil {
		return err
	}
	return losses(ctx, env, cs)
}

// crewNotes prints, for each Crew, every field it or its Agents set that a
// runtime with caps does not honour, with its reason word, and each
// Container no Crew works. Nothing is dropped silently
// (docs/plans/lead-runtime.md, decision 6).
func crewNotes(out io.Writer, m *Model, caps model.Capabilities) error {
	crews := slices.Clone(m.Crews)
	slices.SortFunc(crews, func(a, b c4v1beta1.Crew) int { return strings.Compare(a.Name, b.Name) })
	worked := map[string]bool{}
	for _, c := range crews {
		worked[c.Spec.ContainerRef.Name] = true
		notes := model.NotHonoured(CrewSetup(m, &c), caps)
		says := "what this runtime does not honour of it and its Agents:"
		if len(notes) == 0 {
			says = "this runtime honours all of it and its Agents"
		}
		if _, err := fmt.Fprintf(out, "Crew %s works %s; %s\n", c.Name, c.Spec.ContainerRef.Name,
			says); err != nil {
			return err
		}
		for _, n := range notes {
			if _, err := fmt.Fprintf(out, "  %s\n", model.FieldNoteLine(n)); err != nil {
				return err
			}
		}
	}
	names := make([]string, 0, len(m.Containers))
	for _, c := range m.Containers {
		if !worked[c.Name] {
			names = append(names, c.Name)
		}
	}
	slices.Sort(names)
	for _, name := range names {
		if _, err := fmt.Fprintf(out, "%s: no Crew works its issues; add one to %s\n", name, SetupFile); err != nil {
			return err
		}
	}
	return nil
}

// CrewSetup is c as a runtime reads it: the spec fields its document wrote,
// and each Agent it names, once, with the fields its document wrote and its
// harness.
func CrewSetup(m *Model, c *c4v1beta1.Crew) model.CrewSetup {
	setup := model.CrewSetup{Name: c.Name, Fields: m.Written["Crew "+c.Name]}
	seen := map[string]bool{}
	for _, ref := range CrewAgentRefs(&c.Spec) {
		agent := modelfacts.Find(m.Agents, ref.Name)
		if agent == nil || seen[ref.Name] {
			continue
		}
		seen[ref.Name] = true
		setup.Agents = append(setup.Agents, model.AgentSetup{Name: agent.Name, Fields: m.Written["Agent "+agent.Name],
			Harness: string(agent.Spec.Harness)})
	}
	return setup
}

func status(ctx context.Context, env Env, m *Model, c container, connectorOf map[string]string) error {
	repo := c.host.Repository(c.obj.Name)
	commits, err := c.host.History(ctx, repo, 2)
	if errors.Is(err, ports.ErrRepositoryNotFound) { // not made yet: as an empty one
		commits, err = nil, nil
	}
	if err != nil {
		return err
	}
	var info scaffold.CommitInfo
	if len(commits) > 0 {
		info, _ = scaffold.ParseCommitMessage(commits[0].Message)
	}
	history := model.ScaffoldHistory(len(commits), info.Container, c.obj.Namespace+"/"+c.obj.Name, c.obj.Spec.Adopt)
	scaffolded := history == model.HistoryRecover
	switch {
	case scaffolded:
		_, err = fmt.Fprintf(env.Out, "%s: scaffolded at %s from %s\n", c.obj.Name, repo.WebURL,
			strings.Join(info.ScaffoldedFrom, ", "))
	case len(commits) == 0:
		_, err = fmt.Fprintf(env.Out, "%s: not scaffolded; theos scaffold makes %s\n", c.obj.Name, repo.WebURL)
	default:
		_, err = fmt.Fprintf(env.Out, "%s: %s has commits theos didn't make\n", c.obj.Name, repo.WebURL)
	}
	if err != nil {
		return err
	}
	names := make([]string, 0, len(info.Relationships))
	for _, a := range info.Relationships {
		names = append(names, a.Name)
	}
	for _, rel := range m.Objects.Relationships {
		r := modelfacts.Relationship(rel)
		if !r.ChangesScaffold() || r.Source.Name != c.obj.Name {
			continue
		}
		facts := model.ScaffoldedRelationship{Name: rel.Name, ContainerReady: scaffolded, AppliedNames: names,
			AppliedRecords: info.Relationships, DataStore: r.Target.Name, Connector: connectorOf[r.Target.Name]}
		reason, says := c4v1beta1.ReasonWaiting, "the Connector is applied when the Container is scaffolded"
		switch model.RelationshipApplied(facts) {
		case model.AppliedAtScaffold:
			reason, says = c4v1beta1.ReasonAppliedAtScaffold, "the Connector was part of the scaffold"
		case model.AddedAfterScaffold:
			reason, says = c4v1beta1.ReasonAddedAfterScaffold, fmt.Sprintf(
				"%s was scaffolded before this Relationship existed; scaffolding happens once", c.obj.Name)
		}
		if _, err := fmt.Fprintf(env.Out, "  Relationship %s: %s: %s\n", rel.Name, reason, says); err != nil {
			return err
		}
	}
	return nil
}

// losses prints the losses that apply to each host the Containers are on.
// The sessions hold the credential theos reads, the one that protects main:
// no setting names another account's yet.
func losses(ctx context.Context, env Env, cs []container) error {
	var hosts []string
	for _, c := range cs {
		provider := string(c.res.Provider.Spec.Provider)
		if slices.Contains(hosts, provider) {
			continue
		}
		hosts = append(hosts, provider)
		login, err := c.host.Login(ctx)
		if err != nil {
			login = fmt.Sprintf("an account the host did not name (%v)", err)
		}
		if _, err := fmt.Fprintf(env.Out, "Without a cluster, on %s as %s:\n", c.res.Provider.Spec.URL,
			login); err != nil {
			return err
		}
		for _, l := range model.NoClusterLosses(provider, true) {
			if _, err := fmt.Fprintf(env.Out, "  %s: %s\n", l.Reason, l.Says); err != nil {
				return err
			}
		}
	}
	return nil
}
