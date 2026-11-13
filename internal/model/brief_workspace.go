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

package model

import (
	"cmp"
	_ "embed"
	"fmt"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

// The files the operator keeps on the main branch of a Brief's repository for
// the planner to read (plans/done/brief.md, B4).
const (
	WorkspaceModelFile      = "model/model.yaml"
	WorkspaceCatalogFile    = "model/catalog.yaml"
	WorkspaceContainersFile = "model/containers.yaml"
)

// WorkspaceFiles are every file BriefWorkspaceFiles writes, so the operator can
// read them back to see whether main is already up to date.
var WorkspaceFiles = []string{BriefFile, PlannerGuide, WorkspaceModelFile, WorkspaceCatalogFile, WorkspaceContainersFile,
	WorkspaceSuggestionsFile, WorkspaceFailedFile, WorkspaceTendedFile, WorkspaceRequestsFile, WorkspaceEnvironmentsFile}

// MaxWorkspaceIssues is how many open issue titles model/containers.yaml lists
// per Container; the rest are counted, not listed.
const MaxWorkspaceIssues = 50

// plannerGuideText is PLANNER.md, the planner's guide, embedded in the operator.
//
//go:embed planner.md
var plannerGuideText string

// BriefRepository names the Gitea repository of Brief name.
func BriefRepository(brief string) string { return "brief-" + brief }

// BriefWorkspace is what the planner reads before a round: the Brief, the
// namespace's story, the installed catalog and the Containers' repositories.
type BriefWorkspace struct {
	Brief string
	Idea  string
	// Prompts are every prompt written, oldest first, the retired ones empty
	// (PlacedPrompts).
	Prompts []string
	// PromptsPlanned is how many of Prompts earlier rounds took; the rest are
	// the round's new ones.
	PromptsPlanned int
	// IdeaPlanned says an earlier round planned the idea; when it hasn't, the
	// round plans the idea too.
	IdeaPlanned bool
	// Namespace is the Brief's, which a new Project is named after.
	Namespace string
	// GitProvider is the Brief's, which every new Container names.
	GitProvider string
	// Objects are the namespace's story objects.
	Objects []PlanObject
	// Catalog is every installed catalog entry.
	Catalog []CatalogEntry
	// Bundles are the namespace's Bundles.
	Bundles []Bundle
	// Containers are the namespace's Containers that have a repository.
	Containers []WorkspaceContainer
	// Suggestions are the reviewers' suggestions waiting in the Brief's
	// Containers' repositories, for the person to answer in a prompt.
	Suggestions []Suggestion
	// Failed are the issues handed back to a person in the Brief's
	// Containers' repositories, for the person to answer in a prompt.
	Failed []FailedIssue
	// Tended are the failed issues rounds answered, as the Brief remembers
	// them; an ask's recommendation shows in model/failed.yaml too.
	Tended []TendedIssue
	// Tending says the round is a tending round: no new prompt, it decides
	// the failed issues (plans/done/brief-autopilot.md, decision 5).
	Tending bool
	// Releases are the Brief's spec.releases entries (plans/done/brief-releases.md, R5).
	Releases []WorkspaceRelease
	// Cadence is the Brief's spec.releaseCadence (plans/done/release-cadence.md).
	Cadence ReleaseCadence
	// Requests are the requests from GitHub issues the round plans like new
	// prompts (WorkspaceRequests; docs/plans/done/brief-requests.md, decision 7).
	Requests []Request
	// SpecParallel is the Brief's spec.parallel; PromptParallel the number a
	// prompt of round PromptParallelRound gave. 0 is unset
	// (plans/done/brief-parallel.md, decisions 4 and 5).
	SpecParallel        int32
	PromptParallel      int32
	PromptParallelRound int32
	// Environments are the namespace's TestEnvironments, which an issue asks
	// for with env/<name> (docs/plans/done/test-environments.md, T13).
	Environments []WorkspaceEnvironment
}

// WorkspaceRelease is a spec.releases entry: the person asks for the
// Container's releases, published to a GitHub repository.
type WorkspaceRelease struct {
	Container string
	// Repository is the GitHub repository, owner/name.
	Repository string
	TagPrefix  string
}

// WorkspaceContainer is a Container's repository and its open issues.
type WorkspaceContainer struct {
	Name       string
	Repository string
	// Issues are its open issues; IssuesUnread says they couldn't be read.
	Issues       []WorkspaceIssue
	IssuesUnread bool
	// Released says the Container ships releases already.
	Released bool
	// Checks is the Container's spec.checks: when its CI runs the full check.
	Checks string
}

// WorkspaceIssue is one open issue of a Container.
type WorkspaceIssue struct {
	Number int
	Title  string
}

// BriefWorkspaceFiles renders the files of a Brief's main branch, by path. The
// same workspace always renders the same bytes, so an unchanged one writes
// nothing.
func BriefWorkspaceFiles(ws BriefWorkspace) (map[string]string, error) {
	story, err := renderWorkspaceModel(ws.Objects)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", WorkspaceModelFile, err)
	}
	catalog, err := marshalDocument(workspaceCatalog(ws.Catalog, ws.Bundles))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", WorkspaceCatalogFile, err)
	}
	containers, err := marshalDocument(workspaceContainers(ws.Containers))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", WorkspaceContainersFile, err)
	}
	suggestions, err := marshalDocument(workspaceSuggestions(ws.Suggestions))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", WorkspaceSuggestionsFile, err)
	}
	failed, err := marshalDocument(workspaceFailed(ws.Failed, ws.Tended))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", WorkspaceFailedFile, err)
	}
	tended, err := marshalDocument(workspaceTended(ws.Tended))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", WorkspaceTendedFile, err)
	}
	requests, err := marshalDocument(workspaceRequests(ws.Requests))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", WorkspaceRequestsFile, err)
	}
	environments, err := marshalDocument(workspaceEnvironments(ws.Environments))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", WorkspaceEnvironmentsFile, err)
	}
	return map[string]string{
		BriefFile:                 renderBrief(ws),
		PlannerGuide:              plannerGuideText,
		WorkspaceModelFile:        story,
		WorkspaceCatalogFile:      catalog,
		WorkspaceContainersFile:   containers,
		WorkspaceSuggestionsFile:  suggestions,
		WorkspaceFailedFile:       failed,
		WorkspaceTendedFile:       tended,
		WorkspaceRequestsFile:     requests,
		WorkspaceEnvironmentsFile: environments,
	}, nil
}

// renderBrief writes the idea and every prompt, marking what the round plans.
func renderBrief(ws BriefWorkspace) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Brief %s\n\n", ws.Brief)
	b.WriteString("Written by the operator before each round; don't edit it. Plan what is marked **new**.\n\n")
	b.WriteString("## Idea")
	if !ws.IdeaPlanned {
		b.WriteString(" (**new**)")
	}
	b.WriteString("\n\n" + strings.TrimSpace(ws.Idea) + "\n")
	if len(ws.Prompts) > 0 {
		b.WriteString("\n## Prompts\n\nOldest first. The planned ones are done; plan the new ones on top of them.\n\n")
		retired := 0
		for _, p := range ws.Prompts {
			if p == "" {
				retired++
			}
		}
		switch {
		case retired == 1:
			b.WriteString("The oldest prompt was planned and retired; the model holds what it asked for.\n\n")
		case retired > 1:
			fmt.Fprintf(&b, "The oldest %d prompts were planned and retired; the model holds what they asked for.\n\n",
				retired)
		}
		for i, p := range ws.Prompts {
			if p == "" {
				continue
			}
			mark := "planned"
			if i >= ws.PromptsPlanned {
				mark = "**new**"
			}
			fmt.Fprintf(&b, "%d. (%s) %s\n", i+1, mark, oneParagraph(p))
		}
	}
	if len(ws.Requests) > 0 {
		writeRequests(&b, ws.Requests)
	}
	fmt.Fprintf(&b, "\n## Namespace\n\nThe Brief's namespace is `%s`; a Project the plan makes is named `%s`.\n",
		ws.Namespace, ws.Namespace)
	if len(ws.Suggestions) > 0 {
		fmt.Fprintf(&b, "\n## Reviewer suggestions\n\n%d wait for the person's answer, listed in `%s`. "+
			"Answer one only when a **new** prompt speaks to it.\n", len(ws.Suggestions), WorkspaceSuggestionsFile)
		if more := len(ws.Suggestions) - MaxWorkspaceSuggestions; more > 0 {
			fmt.Fprintf(&b, "\n%d more wait unlisted; a plan may still answer one a prompt names, by its "+
				"Container and number.\n", more)
		}
		b.WriteString("\nA suggestion is its Container and number together: the same number in two Containers " +
			"is two suggestions. Match the repository name or address a prompt writes to an entry's " +
			"`repository` or `url`, and answer that entry's `container` and `issue`. A suggestion a new prompt " +
			"names that no entry lists, answer `missing`, with why: the operator says on the round what it " +
			"found. Never write in `choices.md` that a named suggestion is handled.\n")
	}
	switch {
	case ws.Tending:
		fmt.Fprintf(&b, "\n%s\n\nNo new prompt: decide the failed issues in `%s`.\n", TendingHeading,
			WorkspaceFailedFile)
	case len(ws.Failed) > 0:
		fmt.Fprintf(&b, "\n## Failed issues\n\n%d were handed back to the person, listed with why in `%s`. "+
			"Answer one only when a **new** prompt speaks to it.\n", len(ws.Failed), WorkspaceFailedFile)
	}
	fmt.Fprintf(&b, "\n## Git provider\n\nEvery new Container names `gitProviderRef: {name: %s}`.\n", ws.GitProvider)
	b.WriteString(renderParallel(ws))
	b.WriteString(renderCadence(ws))
	b.WriteString(renderNeverPaths())
	if len(ws.Releases) > 0 {
		b.WriteString("\n## Releases\n\nThe person asked for these Containers' releases. Name each in " +
			"`plan/releases.yaml`; plan the Container under that name if it isn't in the model yet.\n\n" +
			"The factory makes these releases: the Crew tags `vX.Y.Z` in Gitea, the Container's release " +
			"workflow builds the release's files on `" + ReleaseTags + "` tags, and the Crew publishes the release " +
			"to GitHub under the tag below with the operator's token. Issues may ask what a release contains, " +
			"never to tag or publish a release, to change which tags start the release workflow, or to put a " +
			"GitHub token in CI.\n\n")
		releases := slices.Clone(ws.Releases)
		slices.SortFunc(releases, func(a, b WorkspaceRelease) int { return strings.Compare(a.Container, b.Container) })
		for _, r := range releases {
			fmt.Fprintf(&b, "- `%s`, published by the factory to github.com/%s as `%s`\n",
				r.Container, r.Repository, ReleaseTag(r.TagPrefix, "vX.Y.Z"))
		}
	}
	return b.String()
}

// renderCadence says how often the released apps release, for the planner
// (plans/done/release-cadence.md, C5, C9); nothing with every: Release, where a
// round's fixes and features go to releases of their own.
func renderCadence(ws BriefWorkspace) string {
	c := ws.Cadence
	if !c.Cuts() {
		return ""
	}
	at := cmp.Or(c.At, DefaultCutAt) + " " + c.Zone()
	when := "every day at " + at
	if c.Every == CadenceWeek {
		when = "every " + cmp.Or(c.Weekday, DefaultCutWeekday) + " at " + at
	}
	return fmt.Sprintf("\n## Release cadence\n\nReleased apps release %s: each release holds the work merged by "+
		"then, fixes and features alike, and the factory picks its version. Mark an issue `release: now` only "+
		"when a prompt asks to ship it at once, or it fixes what users hit in the latest release.\n", when)
}

// renderParallel says how many issues the Brief's Crews build at once now,
// where that comes from, and whether a plan may change it.
func renderParallel(ws BriefWorkspace) string {
	n := BriefParallel(ws.SpecParallel, ws.PromptParallel)
	head := fmt.Sprintf("\n## Building at once\n\nEach Crew of the Brief builds up to %d issues at once, ", n)
	switch {
	case ws.SpecParallel > 0:
		return head + fmt.Sprintf("set on the Brief (`spec.parallel`), which wins over a prompt. Don't write `%s`; "+
			"if a new prompt asks for another number, say in `%s` that it is set on the Brief.\n",
			PlanParallelFile, PlanChoicesFile)
	case ws.PromptParallel > 0:
		return head + fmt.Sprintf("as a prompt of round %d asked. Write `%s` only when a **new** prompt asks "+
			"for another number.\n", ws.PromptParallelRound, PlanParallelFile)
	}
	return head + fmt.Sprintf("the operator's default. Write `%s` only when a **new** prompt asks how many to "+
		"build at once.\n", PlanParallelFile)
}

// writeRequests is brief.md's section on the round's requests. A GitHub
// request is from the GitHub user who opened it; a source's request is named
// by its source and key alone, since whoever made a tracker's item chose the
// name it shows, which is a field of model/requests.yaml and nothing more
// (decision 15 of docs/plans/requests-and-chat.md).
func writeRequests(b *strings.Builder, requests []Request) {
	var issues int
	var items []string
	for _, r := range requests {
		if r.Source == "" {
			issues++
		} else {
			items = append(items, "`"+r.Name()+"`")
		}
	}
	fmt.Fprintf(b, "\n## Requests (**new**)\n\nThe round takes %s, listed in `%s`.", count(len(requests), "request"),
		WorkspaceRequestsFile)
	if issues > 0 {
		fmt.Fprintf(b, " Those from GitHub issues (%d) are each a request from the GitHub user who opened it, "+
			"whom the person lets ask.", issues)
	}
	if len(items) > 0 {
		fmt.Fprintf(b, " Those from the items of a request source (%d), a tracker the person reads requests "+
			"from, are named by their source and key: %s.", len(items), strings.Join(items, ", "))
	}
	fmt.Fprintf(b, " Plan each like a **new** prompt, within the idea. Answer each in `%s`: take it with the "+
		"issues it became, or skip it saying why; the answer is posted on the issue or item.\n", PlanRequestsFile)
}

// oneParagraph keeps a prompt inside its list item: its line breaks become spaces.
func oneParagraph(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// kindOrder is the order model.yaml lists the story kinds in.
var kindOrder = []string{KindProject, KindPerson, KindSoftwareSystem, KindDataStore, KindContainer, KindRelationship}

type manifestOut struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Name string `yaml:"name"`
	} `yaml:"metadata"`
	Spec any `yaml:"spec"`
}

type textOut struct {
	DisplayName string `yaml:"displayName,omitempty"`
	Description string `yaml:"description,omitempty"`
}

type refOut struct {
	Name string `yaml:"name"`
}

type elementRefOut struct {
	Kind string `yaml:"kind"`
	Name string `yaml:"name"`
}

type systemOut struct {
	textOut    `yaml:",inline"`
	Needs      []string `yaml:"needs,omitempty"`
	KPIs       []string `yaml:"kpis,omitempty"`
	Location   string   `yaml:"location"`
	DomainRefs []string `yaml:"domainRefs,omitempty"`
}

type containerOut struct {
	textOut           `yaml:",inline"`
	SoftwareSystemRef refOut  `yaml:"softwareSystemRef"`
	Type              string  `yaml:"type"`
	GitProviderRef    refOut  `yaml:"gitProviderRef"`
	BundleRef         *refOut `yaml:"bundleRef,omitempty"`
	FrameworkRef      *refOut `yaml:"frameworkRef,omitempty"`
	StandardRef       *refOut `yaml:"standardRef,omitempty"`
	ComputeRef        *refOut `yaml:"computeRef,omitempty"`
	DeploymentRef     *refOut `yaml:"deploymentRef,omitempty"`
}

type dataStoreOut struct {
	textOut           `yaml:",inline"`
	SoftwareSystemRef refOut `yaml:"softwareSystemRef"`
	ConnectorRef      refOut `yaml:"connectorRef"`
}

type relationshipOut struct {
	Source      elementRefOut `yaml:"source"`
	Target      elementRefOut `yaml:"target"`
	Description string        `yaml:"description,omitempty"`
	Bundles     bool          `yaml:"bundles,omitempty"`
}

func optionalRef(name string) *refOut {
	if name == "" {
		return nil
	}
	return &refOut{name}
}

// renderWorkspaceModel writes the namespace's story objects as manifests, in
// plan/model.yaml's format, so the planner starts its plan from a copy. A
// Container's checks is left out, so a copy leaves it alone; containers.yaml
// says it. A Container on a Bundle shows the bundle and none of the sides it
// expanded to: the bundle stays what the team wrote.
func renderWorkspaceModel(objects []PlanObject) (string, error) {
	sorted := slices.Clone(objects)
	slices.SortStableFunc(sorted, func(a, b PlanObject) int {
		if d := slices.Index(kindOrder, a.Kind) - slices.Index(kindOrder, b.Kind); d != 0 {
			return d
		}
		return strings.Compare(a.Name, b.Name)
	})
	var b strings.Builder
	b.WriteString("# The namespace's story now. Copy it to plan/model.yaml and change it there.\n")
	for _, o := range sorted {
		m := manifestOut{APIVersion: PlanAPIVersion, Kind: o.Kind}
		m.Metadata.Name = o.Name
		text := textOut{o.DisplayName, o.Description}
		switch o.Kind {
		case KindSoftwareSystem:
			m.Spec = systemOut{text, o.Needs, o.KPIs, SystemLocation(o), o.Domains}
		case KindContainer:
			spec := containerOut{textOut: text, SoftwareSystemRef: refOut{o.SoftwareSystem}, Type: o.Type,
				GitProviderRef: refOut{o.GitProvider}, BundleRef: optionalRef(o.Bundle)}
			if o.Bundle == "" {
				spec.FrameworkRef, spec.StandardRef = &refOut{o.Framework}, optionalRef(o.Standard)
				spec.ComputeRef, spec.DeploymentRef = optionalRef(o.Compute), optionalRef(o.Deployment)
			}
			m.Spec = spec
		case KindDataStore:
			m.Spec = dataStoreOut{text, refOut{o.SoftwareSystem}, refOut{o.Connector}}
		case KindRelationship:
			m.Spec = relationshipOut{elementRefOut{o.Source.Kind, o.Source.Name},
				elementRefOut{o.Target.Kind, o.Target.Name}, o.Description, o.Bundles}
		default:
			m.Spec = text
		}
		doc, err := yaml.Marshal(m)
		if err != nil {
			return "", fmt.Errorf("%s: %w", o.Ref(), err)
		}
		b.WriteString("---\n")
		b.Write(doc)
	}
	return b.String(), nil
}

type catalogOut struct {
	Frameworks  []catalogEntryOut `yaml:"frameworks"`
	Standards   []catalogEntryOut `yaml:"standards"`
	Computes    []catalogEntryOut `yaml:"computes"`
	Deployments []catalogEntryOut `yaml:"deployments"`
	Connectors  []catalogEntryOut `yaml:"connectors"`
	// Domains are the installed Domains, what a system may be about; a new
	// Internal system names the ones the idea is about (#1760).
	Domains []domainOut `yaml:"domains"`
	// Bundles are the namespace's Bundles, each with the entries it names and
	// the types they all support.
	Bundles []bundleOut `yaml:"bundles"`
}

type domainOut struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description,omitempty"`
}

type bundleOut struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description,omitempty"`
	Framework   string `yaml:"framework"`
	Standard    string `yaml:"standard,omitempty"`
	Compute     string `yaml:"compute,omitempty"`
	Deployment  string `yaml:"deployment,omitempty"`
	// SupportedTypes are the Container types every entry of the bundle
	// supports; none when an entry it names isn't installed.
	SupportedTypes []string `yaml:"supportedTypes"`
}

type catalogEntryOut struct {
	Name                string   `yaml:"name"`
	Description         string   `yaml:"description,omitempty"`
	SupportedTypes      []string `yaml:"supportedTypes"`
	SupportedFrameworks []string `yaml:"supportedFrameworks,omitempty"`
	// ChecksBeforeRelease says a Standard can run the full check only before
	// a release, so a Container's checks: BeforeRelease takes effect.
	ChecksBeforeRelease bool `yaml:"checksBeforeRelease,omitempty"`
	// DeploysItself says a Framework is its Container's deployment, so the
	// Container names no Compute and no Deployment but gitea-release.
	DeploysItself bool `yaml:"deploysItself,omitempty"`
	// WiredBy lists the Frameworks whose templates call a Connector's files;
	// a Container on another Framework can't link to its DataStore. Empty
	// when the Connector takes any Framework.
	WiredBy []string `yaml:"wiredBy,omitempty"`
	// ReadsOperations says a Framework renders infra/operations.ts from a
	// Container's operations; a plan sets operations only on such a Container.
	ReadsOperations bool `yaml:"readsOperations,omitempty"`
}

// workspaceCatalog lists the installed entries by side and name, with what
// each supports, and the Bundles by name, with the types their entries all
// support: a Container may only name these.
func workspaceCatalog(catalog []CatalogEntry, bundles []Bundle) catalogOut {
	sorted := slices.Clone(catalog)
	slices.SortFunc(sorted, func(a, b CatalogEntry) int { return strings.Compare(a.Name, b.Name) })
	out := catalogOut{}
	sides := map[string]*[]catalogEntryOut{
		SideFramework: &out.Frameworks, SideStandard: &out.Standards, SideCompute: &out.Computes,
		SideDeployment: &out.Deployments, SideConnector: &out.Connectors,
	}
	out.Domains = []domainOut{}
	for _, e := range sorted {
		if e.Side == SideDomain {
			out.Domains = append(out.Domains, domainOut{e.Name, strings.TrimSpace(e.Description)})
			continue
		}
		list, ok := sides[e.Side]
		if !ok {
			continue
		}
		types := slices.Clone(e.SupportedTypes)
		slices.Sort(types)
		frameworks := slices.Clone(e.SupportedFrameworks)
		slices.Sort(frameworks)
		wiredBy := slices.Clone(e.WiredBy)
		slices.Sort(wiredBy)
		*list = append(*list, catalogEntryOut{e.Name, strings.TrimSpace(e.Description), types, frameworks,
			e.ChecksBeforeRelease, e.DeploysItself, wiredBy, e.ReadsOperations})
	}
	for _, list := range sides {
		if *list == nil {
			*list = []catalogEntryOut{}
		}
	}
	out.Bundles = []bundleOut{}
	sortedBundles := slices.Clone(bundles)
	slices.SortFunc(sortedBundles, func(a, b Bundle) int { return strings.Compare(a.Name, b.Name) })
	for _, b := range sortedBundles {
		// The types the operator would accept the Bundle for: every entry
		// supports it, and CheckBundle passes (the Standard fits the Framework,
		// a self-deploying Framework has no Compute), so the planner is never
		// led to a Bundle the check refuses.
		types := []string{}
		if entries, missing := BundleEntries(b, catalog); missing == "" {
			for _, t := range BundleSupportedTypes(entries) {
				if CheckBundle(b, t, entries) == nil {
					types = append(types, t)
				}
			}
			slices.Sort(types)
		}
		out.Bundles = append(out.Bundles, bundleOut{b.Name, strings.TrimSpace(b.Description), b.Framework, b.Standard,
			b.Compute, b.Deployment, types})
	}
	return out
}

type containerFileOut struct {
	Name       string `yaml:"name"`
	Repository string `yaml:"repository"`
	// Released says the Container ships releases already.
	Released bool `yaml:"released"`
	// Checks is when the Container's CI runs the full check now; a plan
	// changes it by writing checks on the Container in plan/model.yaml.
	Checks     string   `yaml:"checks"`
	OpenIssues []string `yaml:"openIssues"`
	// MoreIssues counts the open issues past MaxWorkspaceIssues.
	MoreIssues int `yaml:"moreIssues,omitempty"`
	// Unread says the issues couldn't be read this time.
	Unread bool `yaml:"issuesUnread,omitempty"`
}

// workspaceContainers lists each Container's repository and its open issues,
// lowest number first, so the planner doesn't file one twice.
func workspaceContainers(containers []WorkspaceContainer) []containerFileOut {
	sorted := slices.Clone(containers)
	slices.SortFunc(sorted, func(a, b WorkspaceContainer) int { return strings.Compare(a.Name, b.Name) })
	out := make([]containerFileOut, 0, len(sorted))
	for _, c := range sorted {
		issues := slices.Clone(c.Issues)
		slices.SortFunc(issues, func(a, b WorkspaceIssue) int { return a.Number - b.Number })
		entry := containerFileOut{Name: c.Name, Repository: c.Repository, OpenIssues: []string{}, Unread: c.IssuesUnread,
			Released: c.Released, Checks: checksOrDefault(c.Checks)}
		if len(issues) > MaxWorkspaceIssues {
			entry.MoreIssues = len(issues) - MaxWorkspaceIssues
			issues = issues[:MaxWorkspaceIssues]
		}
		for _, i := range issues {
			entry.OpenIssues = append(entry.OpenIssues, fmt.Sprintf("#%d %s", i.Number, oneParagraph(i.Title)))
		}
		out = append(out, entry)
	}
	return out
}

func marshalDocument(v any) (string, error) {
	data, err := yaml.Marshal(v)
	return string(data), err
}
