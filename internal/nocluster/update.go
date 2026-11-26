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
	"slices"
	"strings"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/modelfacts"
	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/scaffold"
)

// UpdateOptions are theos update's flags.
type UpdateOptions struct {
	// Container is the one Container to update; "" updates each.
	Container string
	// From is the ref the Standard's files are at, in place of the one the
	// history names; it needs Container.
	From string
}

// historyLimit is how many of main's newest commits update reads for the
// ref the Standard's files are at. A repository whose last update and
// scaffold are both older takes -from.
const historyLimit = 1000

// Update brings each Container's Standard files to its Standard's ref in
// model/setup.yaml, as the operator's Standard update does
// (docs/plans/no-cluster.md, step 7; #295): the same renders and plan
// (scaffold.RenderStandardUpdate), the same branch, commit and title, and a
// pull request a person merges on the host's pages. The ref and checks the
// files are at, which a cluster keeps in status, are read from main's
// history (scaffold.StandardAt). Files a past update left out are not
// tracked, as a cluster's status tracks them: each update plans them from
// the ref the history names.
func Update(ctx context.Context, env Env, m *Model, opts UpdateOptions) error {
	if opts.From != "" && opts.Container == "" {
		return errors.New("-from names the ref of one Container's Standard files: pass -container too")
	}
	if opts.Container != "" && modelfacts.Find(m.Containers, opts.Container) == nil {
		return fmt.Errorf("%s has no Container %s", ModelFile, opts.Container)
	}
	settings, err := m.Settings.Scaffold()
	if err != nil {
		return err
	}
	cs, err := containers(ctx, env, m)
	if err != nil {
		return err
	}
	for _, c := range cs {
		if opts.Container != "" && c.obj.Name != opts.Container {
			continue
		}
		if err := updateOne(ctx, env, m, c, settings, opts.From); err != nil {
			return fmt.Errorf("container %s: %w", c.obj.Name, err)
		}
	}
	return nil
}

// standardUpdate is one Container's Standard update as theos update works it.
type standardUpdate struct {
	env      Env
	m        *Model
	c        container
	settings modelfacts.Settings
	repo     ports.Repository
	standard *c4v1beta1.Standard
	// info is the scaffold's commit as it reads, zero when the history
	// holds none.
	info scaffold.CommitInfo
}

func updateOne(ctx context.Context, env Env, m *Model, c container, settings modelfacts.Settings, from string) error {
	name := c.view.Choices().Standard
	say := func(format string, args ...any) error {
		_, err := fmt.Fprintf(env.Out, "%s: "+format+"\n", append([]any{c.obj.Name}, args...)...)
		return err
	}
	if name == "" {
		return say("no Standard, so nothing to update")
	}
	standard := modelfacts.Find(m.Objects.Standards, name)
	if standard == nil {
		return fmt.Errorf("%s has no Standard %s", SetupFile, name)
	}
	u := &standardUpdate{env: env, m: m, c: c, settings: settings, repo: c.host.Repository(c.obj.Name),
		standard: standard}
	h, err := readMain(ctx, c, u.repo)
	if err != nil {
		return err
	}
	if len(h.messages) == 0 {
		return say("not scaffolded; theos scaffold makes %s", u.repo.WebURL)
	}
	u.info = h.info
	at, found := scaffold.StandardAt(h.messages, name)
	if from != "" {
		at.Ref, found = from, true
	}
	if !found {
		return say("%s: the Standard %s was not part of the scaffold, and no commit of the newest %d names an "+
			"update of it; pass -from with the ref its files are at", c4v1beta1.ReasonAddedAfterScaffold, name,
			historyLimit)
	}
	to := model.StandardVersion{Ref: standard.Spec.Template.Ref,
		Checks: model.EffectiveChecks(string(c.obj.Spec.Checks), standard.Spec.ChecksBeforeRelease)}
	if at.Normal() == to.Normal() {
		return say("the files of the Standard %s are at %s, its ref; nothing to update", name, to)
	}
	return u.open(ctx, at.Normal(), to, say)
}

// open plans the update from from to to and opens its pull request.
func (u *standardUpdate) open(ctx context.Context, from, to model.StandardVersion,
	say func(string, ...any) error) error {
	name := u.standard.Name
	in, err := u.input(ctx, from, to)
	if err != nil {
		return err
	}
	tmpl := u.standard.Spec.Template
	oldFiles, err := u.fetch(ctx, tmpl, from.Ref)
	if err != nil {
		return fmt.Errorf("the Standard %s at %s: %w", name, from.Ref, err)
	}
	nextFiles, err := u.fetch(ctx, tmpl, to.Ref)
	if err != nil {
		return fmt.Errorf("the Standard %s at %s: %w", name, to.Ref, err)
	}
	// The files main holds render with the CI images they were rendered with,
	// which with no cluster are the settings' (#284).
	renders, err := scaffold.RenderStandardUpdate(in, name, from, to, in.CIImages, oldFiles, nextFiles)
	if err != nil {
		return fmt.Errorf("could not render the Standard %s %w", name, err)
	}
	current, base, err := u.c.host.ReadFiles(ctx, u.repo, ports.DefaultBranch, renders.Paths(nil))
	if err != nil {
		return err
	}
	plan := renders.Plan(current, nil)
	if len(plan.ChecksLeftOut) > 0 {
		return say("%s: CI can't change to run the full check %s: %s changed in the repository since %s and "+
			"doesn't merge with the change; nothing opened", c4v1beta1.ReasonChecksLeftOut, checksWhen(to.Checks),
			strings.Join(plan.ChecksLeftOut, ", "), from)
	}
	if plan.NoLightCheck {
		return say("%s: CI can't change to run the full check only before a release: the %s the update leaves "+
			"has no check-light target; nothing opened", c4v1beta1.ReasonNoLightCheck, model.LightCheckFile)
	}
	if plan.Empty() {
		return say("main already holds the Standard %s at %s; nothing to open", name, to)
	}
	leftOut := model.LeftOut{Files: renders.LeftOut(nil, nil, current, plan.LeftOut)}
	branch := model.StandardUpdateBranch(name, to)
	author := ports.Signature{Name: scaffold.AuthorName, Email: scaffold.AuthorEmail, When: u.env.Now()}
	if _, err := u.c.host.PushBranch(ctx, u.repo, base, branch, plan.Files, plan.Removes,
		scaffold.UpdateMessage(in, name, from, to, plan.Update), author); errors.Is(err, ports.ErrEmptyChange) {
		return say("main already holds the Standard %s at %s; nothing to open", name, to)
	} else if err != nil {
		return err
	}
	// The description ends with the commit's trailers, so a squash merge on
	// the host's pages that keeps the description reads back (#295).
	body := model.PersonStandardUpdateBody(name, from, to, plan.Paths(), plan.Merged, leftOut) + "\n" +
		scaffold.UpdateTrailers(name, from, to)
	pr, err := u.c.host.OpenPullRequest(ctx, u.repo, branch, ports.DefaultBranch,
		model.StandardUpdateTitle(name, from, to), body)
	if err != nil {
		return err
	}
	if err := say("opened %s, the Standard %s from %s to %s: %d files, %d merged with the repository's changes, "+
		"%d left out", pr.URL, name, from, to, len(plan.Paths()), len(plan.Merged), len(plan.LeftOut)); err != nil {
		return err
	}
	_, err = fmt.Fprintf(u.env.Out, "  %s: merge it on the host's pages once CI passes; theos merge merges Tasks "+
		"only\n", model.LossPersonMerges)
	return err
}

// input is what the Standard renders with, as the operator's update renders
// it from the Container's status: the Connectors, Domains and license the
// scaffold recorded, to's checks, the settings' CI images, the operations
// block as the model holds it, and the paths the Framework renders at the
// ref the scaffold pinned.
func (u *standardUpdate) input(ctx context.Context, from, to model.StandardVersion) (scaffold.Input, error) {
	ci, err := modelfacts.CI(u.c.res.Provider)
	if err != nil {
		return scaffold.Input{}, err
	}
	license, err := u.license(ctx)
	if err != nil {
		return scaffold.Input{}, err
	}
	in := modelfacts.ScaffoldInput(u.c.view, ci, connectorsOf(u.info.Relationships), sortedUnique(u.info.Domains),
		to.Checks, u.settings.CIImages.OrDefault(), modelfacts.Operations(u.c.obj), u.repo.NpmScope, license)
	framework := u.c.view.Choices().Framework
	ref := model.ScaffoldedRef(u.info.ScaffoldedFrom, framework)
	if ref == "" {
		return in, nil
	}
	f := modelfacts.Find(u.m.Objects.Frameworks, framework)
	if f == nil {
		return scaffold.Input{}, fmt.Errorf("%s has no Framework %s", SetupFile, framework)
	}
	files, err := u.fetch(ctx, f.Spec.Template, ref)
	if err != nil {
		return scaffold.Input{}, fmt.Errorf("the Framework %s at %s: %w", framework, ref, err)
	}
	at := in
	at.Checks = from.Checks
	tree, err := scaffold.RenderLayer(at, scaffold.Layer{Side: model.SideFramework, Name: framework, Ref: ref,
		Files: files})
	if err != nil {
		return scaffold.Input{}, fmt.Errorf("could not render the Framework %s at %s: %w", framework, ref, err)
	}
	in.FrameworkFiles = tree.Paths()
	return in, nil
}

// fetch fetches a template at ref, with the credential on its host only.
func (u *standardUpdate) fetch(ctx context.Context, tmpl c4v1beta1.TemplateSource, ref string) (scaffold.Tree,
	error) {
	var creds *ports.Credentials
	if ports.SameHost(u.c.res.Provider.Spec.URL, tmpl.URL) {
		creds = &u.c.res.Credentials
	}
	return u.c.templates.FetchTemplate(ctx, tmpl.URL, ref, tmpl.Path, creds)
}

// license is the license the repository carries, as the operator reads it:
// the id its .theos.yaml on main records, with the holder and the year as a
// scaffold resolves them; none when it records no id.
func (u *standardUpdate) license(ctx context.Context) (scaffold.License, error) {
	files, _, err := u.c.host.ReadFiles(ctx, u.repo, ports.DefaultBranch, []string{scaffold.IdentityPath})
	if err != nil {
		return scaffold.License{}, err
	}
	f, ok := files[scaffold.IdentityPath]
	if !ok {
		return scaffold.License{}, nil
	}
	id, err := scaffold.ParseIdentity(f.Data)
	if err != nil {
		return scaffold.License{}, fmt.Errorf("reading %s: %w", scaffold.IdentityPath, err)
	}
	if id.License == "" {
		return scaffold.License{}, nil
	}
	license := modelfacts.License(u.c.obj, u.m.Objects.Project, u.env.Now())
	license.ID = id.License
	return license, nil
}

// connectorsOf are the unique Connectors of the Relationships a scaffold
// applied, sorted, as the operator's status records them.
func connectorsOf(applied []model.AppliedRelationship) []string {
	names := make([]string, 0, len(applied))
	for _, a := range applied {
		if a.DataStore != "" {
			names = append(names, a.Connector)
		}
	}
	return sortedUnique(names)
}

// sortedUnique is names sorted, each once.
func sortedUnique(names []string) []string {
	names = slices.Clone(names)
	slices.Sort(names)
	return slices.Compact(names)
}

// checksWhen says when CI runs the full check with checks.
func checksWhen(checks string) string {
	if checks == model.ChecksBeforeRelease {
		return "only before a release"
	}
	return "on every change"
}
