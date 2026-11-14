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

// Package modelfacts turns a project's API objects into the plain values the
// business rules take: what a Container's scaffold resolves to, its layers,
// what its templates render with, and the rule its main is protected by. The
// Container reconciler and the theos binary call the same functions on the
// objects each read, from the cluster or from the model files, so a scaffold
// without a cluster is the operator's (docs/plans/no-cluster.md, decision 4).
// It reads nothing itself, and imports no controller-runtime, no client-go
// and no adapter.
package modelfacts

import (
	"context"
	"fmt"
	"slices"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/scaffold"
)

// TemplateFetcher reads a template at a pinned ref; a non-empty dir reads
// only that directory of the repository (template.path, #35). The
// controller's GitClient is one.
type TemplateFetcher interface {
	FetchTemplate(ctx context.Context, url, ref, dir string, creds *ports.Credentials) (scaffold.Tree, error)
}

// Objects are one project's objects a Container's scaffold reads, as the
// caller read them: each kind as a List gives it, or as the model files
// hold it.
type Objects struct {
	// Project is the project's Project, nil when it has none.
	Project       *c4v1beta1.Project
	GitProviders  []c4v1beta1.GitProvider
	Bundles       []c4v1beta1.Bundle
	Frameworks    []c4v1beta1.Framework
	Standards     []c4v1beta1.Standard
	Computes      []c4v1beta1.Compute
	Deployments   []c4v1beta1.Deployment
	Connectors    []c4v1beta1.Connector
	Domains       []c4v1beta1.Domain
	DataStores    []c4v1beta1.DataStore
	Relationships []c4v1beta1.Relationship
	// Secrets are the credentials in each Secret the caller read, by the
	// Secret's name. A GitProvider whose Secret is not here waits for it.
	Secrets map[string]ports.Credentials
}

// Resolved is everything a Container's scaffold depends on, as the plain
// values the business rules take.
type Resolved struct {
	Provider *c4v1beta1.GitProvider
	// Credentials come from the GitProvider's credentials Secret.
	Credentials ports.Credentials
	// Entries are the catalog entries in layer order: Framework, Standard,
	// Compute, Deployment, each Connector, then each Domain.
	Entries []model.CatalogEntry
	// Domains are the names of the system's Domains, sorted and unique: the
	// Domain layers at the end of Entries.
	Domains []string
	// Standard is the Standard's entry, or nil when the Container picks none.
	Standard *model.CatalogEntry
	// Use is what the Container picks up through its Relationships to DataStores.
	Use model.DataStoreUse
	// Choices are the entries the Container picked, by name: its spec's refs,
	// or its Bundle's.
	Choices c4v1beta1.ContainerChoices
	// Bundle is what the Container's Bundle expanded to, or nil when it names
	// none or the Bundle is missing.
	Bundle *c4v1beta1.ContainerBundleStatus
}

// Find is the item of items called name, or nil.
func Find[T any, PT interface {
	*T
	GetName() string
}](items []T, name string) PT {
	for i := range items {
		if p := PT(&items[i]); p.GetName() == name {
			return p
		}
	}
	return nil
}

// Resolve works out what the Container's scaffold needs from objs. missing
// names the objects that don't exist yet, such as "Framework python-fastapi",
// in the order the scaffold reads them.
func Resolve(container *c4v1beta1.Container, system *c4v1beta1.SoftwareSystem, objs Objects) (Resolved, []string) {
	var res Resolved
	var missing []string
	spec := container.Spec

	if provider := Find(objs.GitProviders, spec.GitProviderRef.Name); provider != nil {
		res.Provider = provider
		if creds, ok := objs.Secrets[provider.Spec.CredentialsSecretRef.Name]; ok {
			res.Credentials = creds
		} else {
			missing = append(missing, "Secret "+provider.Spec.CredentialsSecretRef.Name)
		}
	} else {
		missing = append(missing, "GitProvider "+spec.GitProviderRef.Name)
	}

	// A Bundle is expanded afresh at each resolve, as if the Container had
	// named its entries; only a Container not yet scaffolded resolves.
	res.Choices = container.Choices()
	if spec.BundleRef != nil {
		res.Choices = c4v1beta1.ContainerChoices{Bundle: spec.BundleRef.Name}
		if bundle := Find(objs.Bundles, spec.BundleRef.Name); bundle != nil {
			b := bundle.Spec
			res.Bundle = &c4v1beta1.ContainerBundleStatus{Name: bundle.Name, Framework: b.FrameworkRef.Name,
				Standard: refName(b.StandardRef), Compute: refName(b.ComputeRef), Deployment: refName(b.DeploymentRef)}
			res.Choices = c4v1beta1.ContainerChoices{Bundle: bundle.Name, Framework: res.Bundle.Framework,
				Standard: res.Bundle.Standard, Compute: res.Bundle.Compute, Deployment: res.Bundle.Deployment}
		} else {
			missing = append(missing, "Bundle "+spec.BundleRef.Name)
		}
	}

	for _, c := range model.BundleChoices(model.Bundle{Framework: res.Choices.Framework,
		Standard: res.Choices.Standard, Compute: res.Choices.Compute, Deployment: res.Choices.Deployment}) {
		obj := sideObject(objs, c.Side, c.Name)
		if obj == nil {
			missing = append(missing, c.Side+" "+c.Name)
			continue
		}
		entry := CatalogEntry(c.Side, obj)
		res.Entries = append(res.Entries, entry)
		if c.Side == model.SideStandard {
			res.Standard = &entry
		}
	}

	// Connectors come through Relationships from this Container to DataStores.
	connectorOf := make(map[string]string, len(objs.DataStores))
	for _, s := range objs.DataStores {
		connectorOf[s.Name] = s.Spec.ConnectorRef.Name
	}
	rels := make([]model.Relationship, 0, len(objs.Relationships))
	for _, rel := range objs.Relationships {
		source := rel.Spec.Source
		if rel.DeletionTimestamp.IsZero() && source.Kind == c4v1beta1.ElementContainer && source.Name == container.Name {
			rels = append(rels, Relationship(rel))
		}
	}
	res.Use = model.DataStoresUsedBy(container.Name, rels, connectorOf)
	for _, name := range res.Use.MissingDataStores {
		missing = append(missing, "DataStore "+name)
	}
	for _, name := range res.Use.Connectors {
		connector := Find(objs.Connectors, name)
		if connector == nil {
			missing = append(missing, "Connector "+name)
			continue
		}
		res.Entries = append(res.Entries, CatalogEntry(model.SideConnector, connector))
	}
	// The system's Domains render last, sorted by name (docs/plans/domains.md).
	for _, name := range DomainNames(system) {
		domain := Find(objs.Domains, name)
		if domain == nil {
			missing = append(missing, "Domain "+name)
			continue
		}
		res.Entries = append(res.Entries, CatalogEntry(model.SideDomain, domain))
		res.Domains = append(res.Domains, name)
	}
	return res, missing
}

// sideObject is the catalog entry of side called name, or nil.
func sideObject(objs Objects, side, name string) metav1.Object {
	var obj metav1.Object
	switch side {
	case model.SideFramework:
		if p := Find(objs.Frameworks, name); p != nil {
			obj = p
		}
	case model.SideStandard:
		if p := Find(objs.Standards, name); p != nil {
			obj = p
		}
	case model.SideCompute:
		if p := Find(objs.Computes, name); p != nil {
			obj = p
		}
	case model.SideDeployment:
		if p := Find(objs.Deployments, name); p != nil {
			obj = p
		}
	}
	return obj
}

// Check is the first of the business rules the Container's resolved choices
// break, in the order the reconciler reports them, or nil.
func Check(container *c4v1beta1.Container, res Resolved) error {
	choices := res.Choices
	err := model.CheckChoices(string(container.Spec.Type), res.Entries)
	if err == nil {
		err = model.CheckStandard(choices.Framework, res.Standard)
	}
	if err == nil {
		err = model.CheckWiring(choices.Framework, res.Entries)
	}
	if err == nil {
		err = model.CheckDeploysItself(FrameworkEntry(res.Entries), string(container.Spec.Type), choices.Compute,
			choices.Deployment)
	}
	return model.InBundle(choices.Bundle, err)
}

// Checks is when CI runs the full check in the Container's repository
// (model.EffectiveChecks).
func (r Resolved) Checks(container *c4v1beta1.Container) string {
	return model.EffectiveChecks(string(container.Spec.Checks), r.Standard != nil && r.Standard.ChecksBeforeRelease)
}

// DomainNames are the Domains a system names, sorted and unique.
func DomainNames(system *c4v1beta1.SoftwareSystem) []string {
	names := slices.Clone(system.Spec.DomainRefs)
	slices.Sort(names)
	return slices.Compact(names)
}

// Relationship is a Relationship as the business rules read it.
func Relationship(rel c4v1beta1.Relationship) model.Relationship {
	return model.Relationship{
		Name:   rel.Name,
		Source: model.Ref{Kind: string(rel.Spec.Source.Kind), Name: rel.Spec.Source.Name},
		Target: model.Ref{Kind: string(rel.Spec.Target.Kind), Name: rel.Spec.Target.Name},
	}
}

// CatalogEntry converts any of the six catalog kinds into a model.CatalogEntry.
func CatalogEntry(side string, obj metav1.Object) model.CatalogEntry {
	var spec c4v1beta1.CatalogEntrySpec
	var frameworks, wiredBy []string
	var checksBeforeRelease, deploysItself, readsOperations bool
	switch o := obj.(type) {
	case *c4v1beta1.Framework:
		spec = o.Spec.CatalogEntrySpec
		deploysItself = o.Spec.DeploysItself
		readsOperations = o.Spec.ReadsOperations
	case *c4v1beta1.Standard:
		spec = o.Spec.CatalogEntrySpec
		frameworks = o.Spec.SupportedFrameworks
		checksBeforeRelease = o.Spec.ChecksBeforeRelease
	case *c4v1beta1.Compute:
		spec = o.Spec
	case *c4v1beta1.Deployment:
		spec = o.Spec
	case *c4v1beta1.Connector:
		spec = o.Spec.CatalogEntrySpec
		wiredBy = o.Spec.WiredBy
	case *c4v1beta1.Domain:
		// No supportedTypes: what a system is about doesn't depend on a
		// Container's type.
		spec = c4v1beta1.CatalogEntrySpec{DisplayName: o.Spec.DisplayName, Description: o.Spec.Description,
			Template: o.Spec.Template}
	}
	supported := make([]string, 0, len(spec.SupportedTypes))
	for _, t := range spec.SupportedTypes {
		supported = append(supported, string(t))
	}
	return model.CatalogEntry{
		Side:                side,
		Name:                obj.GetName(),
		Description:         spec.Description,
		SupportedTypes:      supported,
		TemplateURL:         spec.Template.URL,
		TemplateRef:         spec.Template.Ref,
		TemplatePath:        spec.Template.Path,
		SupportedFrameworks: frameworks,
		WiredBy:             wiredBy,
		ChecksBeforeRelease: checksBeforeRelease,
		DeploysItself:       deploysItself,
		ReadsOperations:     readsOperations,
	}
}

// FrameworkEntry is the Framework's entry among a Container's resolved
// entries, which start with it. A Container that adopts its repository may
// name no Framework (#60): its entries then start with another side, or are
// none, and the entry is the zero one, a Framework that deploys nothing
// itself and reads no operations.
func FrameworkEntry(entries []model.CatalogEntry) model.CatalogEntry {
	if len(entries) == 0 || entries[0].Side != model.SideFramework {
		return model.CatalogEntry{}
	}
	return entries[0]
}

// FetchLayers fetches every resolved entry's template at its pinned ref, in
// layer order. The GitProvider's credentials are used for templates on the
// same host.
func FetchLayers(ctx context.Context, fetcher TemplateFetcher, res Resolved) ([]scaffold.Layer, error) {
	layers := make([]scaffold.Layer, 0, len(res.Entries))
	for _, entry := range res.Entries {
		var creds *ports.Credentials
		if ports.SameHost(res.Provider.Spec.URL, entry.TemplateURL) {
			creds = &res.Credentials
		}
		files, err := fetcher.FetchTemplate(ctx, entry.TemplateURL, entry.TemplateRef, entry.TemplatePath, creds)
		if err != nil {
			return nil, fmt.Errorf("%s %s: %w", entry.Side, entry.Name, err)
		}
		layers = append(layers, scaffold.Layer{Side: entry.Side, Name: entry.Name, Ref: entry.TemplateRef, Files: files})
	}
	return layers, nil
}

// Render is a Container's first commit: what its templates render with, what
// they render, and the commit's message.
type Render struct {
	Input   scaffold.Input
	Result  scaffold.Result
	Message string
}

// Settings are what an operator renders every scaffold with: its flags.
type Settings struct {
	// CIImages are the toolchain images a GitHub check falls back to (#284).
	CIImages model.CIImages
	// Options are the parts of a scaffold the operator owns.
	Options scaffold.Options
}

// Build renders the Container's first commit from its layers: container is
// the Container as the scaffold reads its choices (its status.bundle the
// resolved one), ci its repository's CI system, npmScope the scope its npm
// package is named under (ports.Repository.NpmScope) and license the license
// it carries (License).
func Build(container *c4v1beta1.Container, res Resolved, layers []scaffold.Layer, ci model.CI, npmScope string,
	license scaffold.License, settings Settings) (Render, error) {
	in := ScaffoldInput(container, ci, res.Use.Connectors, res.Domains, res.Checks(container),
		settings.CIImages.OrDefault(), Operations(container), npmScope, license)
	result, err := scaffold.Build(in, layers, settings.Options)
	if err != nil {
		return Render{}, err
	}
	return Render{Input: in, Result: result,
		Message: scaffold.CommitMessage(in, result.ScaffoldedFrom, res.Use.Relationships)}, nil
}

// ScaffoldInput is what every template of the Container renders with. ci is
// the CI system its repository runs, connectors are the unique Connector
// names, sorted, checks is when CI runs the full check
// (model.EffectiveChecks), ops its operations block, the defaults filled in,
// and npmScope the scope the repository's npm package is named under
// (ports.Repository.NpmScope; empty means ports.DefaultNpmScope).
// license is the license the repository carries (License); a caller
// that only writes a message passes none. images are the toolchain images a
// GitHub check falls back to (#284): the operator's for what a scaffold, an
// adoption or an update brings, the recorded ones for what the repository's
// files are at.
func ScaffoldInput(container *c4v1beta1.Container, ci model.CI, connectors, domains []string,
	checks string, images model.CIImages, ops model.Operations, npmScope string, license scaffold.License) scaffold.Input {
	if npmScope == "" {
		npmScope = ports.DefaultNpmScope
	}
	choices := container.Choices()
	return scaffold.Input{
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
}

// License is the license the Container's repository carries: its own
// spec.license, else its Project's (#1788). With no Project only the
// Container's counts, with the namespace, the Project's name, as the holder.
func License(container *c4v1beta1.Container, project *c4v1beta1.Project, now time.Time) scaffold.License {
	var spec c4v1beta1.ProjectSpec
	if project != nil {
		spec = project.Spec
	}
	return scaffold.ResolveLicense(string(container.Spec.License), string(spec.License), spec.DisplayName,
		container.Namespace, now)
}

// OperationsChoices is spec.operations as model reads it; nil leaves every
// field out.
func OperationsChoices(spec *c4v1beta1.OperationsSpec) model.OperationsChoices {
	var c model.OperationsChoices
	if spec == nil {
		return c
	}
	if b := spec.Backups; b != nil {
		c.RetentionDays, c.CopyToRegion = b.RetentionDays, b.CopyToRegion
	}
	if r := spec.Recovery; r != nil {
		c.RPO, c.RTO = r.RPO, r.RTO
	}
	if a := spec.Alerts; a != nil {
		c.Email = a.Email
	}
	return c
}

// Operations is the Container's operations block, the defaults filled in.
func Operations(container *c4v1beta1.Container) model.Operations {
	return model.ResolveOperations(OperationsChoices(container.Spec.Operations))
}

// CI is the CI system of a GitProvider's Git host: the host's own, since no
// GitProvider names another yet.
func CI(gp *c4v1beta1.GitProvider) (model.CI, error) {
	ci, err := model.HostCI(string(gp.Spec.Provider))
	if err != nil {
		return model.CI{}, fmt.Errorf("GitProvider %s: %w", gp.Name, err)
	}
	return ci, nil
}

// Packages is a GitProvider's spec.packages as the Provider's settings:
// the registries it names, with "" for each it leaves to the host's own.
// The tokens are not here.
func Packages(gp *c4v1beta1.GitProvider) ports.Packages {
	var p ports.Packages
	if gp.Spec.Packages == nil {
		return p
	}
	if npm := gp.Spec.Packages.NPM; npm != nil {
		p.NpmScope, p.NpmRegistry = npm.Scope, npm.Registry
	}
	if pypi := gp.Spec.Packages.PyPI; pypi != nil {
		p.PyPIIndex, p.PyPIUpload = pypi.Index, pypi.Upload
	}
	return p
}

// Config is how to reach the GitProvider's host with creds.
func Config(gp *c4v1beta1.GitProvider, creds ports.Credentials) ports.Config {
	return ports.Config{
		Kind:        string(gp.Spec.Provider),
		URL:         gp.Spec.URL,
		Owner:       gp.Spec.Owner,
		Credentials: creds,
		Packages:    Packages(gp),
	}
}

// RepoDescription is the description a Container's repository is made with.
func RepoDescription(container *c4v1beta1.Container) string {
	if container.Spec.Description != "" {
		return container.Spec.Description
	}
	if container.Spec.DisplayName != "" {
		return container.Spec.DisplayName
	}
	return fmt.Sprintf("The %s %s of %s", container.Name, container.Spec.Type, container.Spec.SoftwareSystemRef.Name)
}

// Rule is the rule a Container's main is protected with: ci's check contexts
// and the paths no agent may change, the platform's for ci and
// standardPaths, its Standard's (model.AgentProtectedPaths). It is the one
// place the rule is built: the operator's protectRepository and
// protectAdopted put what it returns, and Protection returns it (#268).
func Rule(ci model.CI, standardPaths []string) ports.BranchRule {
	return ports.BranchRule{StatusContexts: ci.CheckContexts,
		ProtectedPaths: model.AgentProtectedPaths(ci, standardPaths)}
}

// Protection is the rule a scaffolded Container's main is protected with, as
// the operator's protectRepository puts it: Rule for its GitProvider's CI
// system and its Standard's protected paths; the release tags
// (model.ReleaseTags) stay the protecting account's. protect is false for a
// Container without a Standard, which has no checks to require
// (model.Protected): one that adopts its repository with no Standard is
// protected by the operator once main reports its own checks, which only it
// reads.
func Protection(container *c4v1beta1.Container, gp *c4v1beta1.GitProvider,
	standard *c4v1beta1.Standard) (rule ports.BranchRule, protect bool, err error) {
	if !model.Protected(container.Choices().Standard != "", false) {
		return ports.BranchRule{}, false, nil
	}
	ci, err := CI(gp)
	if err != nil {
		return ports.BranchRule{}, false, err
	}
	var paths []string
	if standard != nil {
		paths = standard.Spec.ProtectedPaths
	}
	return Rule(ci, paths), true, nil
}

func refName(ref *c4v1beta1.LocalRef) string {
	if ref == nil {
		return ""
	}
	return ref.Name
}
