package model

import (
	_ "embed"
	"fmt"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

// The files the operator keeps on the main branch of a Brief's repository for
// the planner to read (plans/brief.md, B4).
const (
	WorkspaceModelFile      = "model/model.yaml"
	WorkspaceCatalogFile    = "model/catalog.yaml"
	WorkspaceContainersFile = "model/containers.yaml"
)

// WorkspaceFiles are every file BriefWorkspaceFiles writes, so the operator can
// read them back to see whether main is already up to date.
var WorkspaceFiles = []string{BriefFile, PlannerGuide, WorkspaceModelFile, WorkspaceCatalogFile, WorkspaceContainersFile}

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
	Brief   string
	Idea    string
	Prompts []string
	// PromptsPlanned is how many of Prompts earlier rounds took; the rest are
	// the round's new ones.
	PromptsPlanned int
	// IdeaPlanned says an earlier round planned the idea; when it hasn't, the
	// round plans the idea too.
	IdeaPlanned bool
	// GitProvider is the Brief's, which every new Container names.
	GitProvider string
	// Objects are the namespace's story objects.
	Objects []PlanObject
	// Catalog is every installed catalog entry.
	Catalog []CatalogEntry
	// Containers are the namespace's Containers that have a repository.
	Containers []WorkspaceContainer
}

// WorkspaceContainer is a Container's repository and its open issues.
type WorkspaceContainer struct {
	Name       string
	Repository string
	// Issues are its open issues; IssuesUnread says they couldn't be read.
	Issues       []WorkspaceIssue
	IssuesUnread bool
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
	catalog, err := marshalDocument(workspaceCatalog(ws.Catalog))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", WorkspaceCatalogFile, err)
	}
	containers, err := marshalDocument(workspaceContainers(ws.Containers))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", WorkspaceContainersFile, err)
	}
	return map[string]string{
		BriefFile:               renderBrief(ws),
		PlannerGuide:            plannerGuideText,
		WorkspaceModelFile:      story,
		WorkspaceCatalogFile:    catalog,
		WorkspaceContainersFile: containers,
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
		for i, p := range ws.Prompts {
			mark := "planned"
			if i >= ws.PromptsPlanned {
				mark = "**new**"
			}
			fmt.Fprintf(&b, "%d. (%s) %s\n", i+1, mark, oneParagraph(p))
		}
	}
	fmt.Fprintf(&b, "\n## Git provider\n\nEvery new Container names `gitProviderRef: {name: %s}`.\n", ws.GitProvider)
	return b.String()
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
	textOut `yaml:",inline"`
	Needs   []string `yaml:"needs,omitempty"`
	KPIs    []string `yaml:"kpis,omitempty"`
}

type containerOut struct {
	textOut           `yaml:",inline"`
	SoftwareSystemRef refOut  `yaml:"softwareSystemRef"`
	Type              string  `yaml:"type"`
	GitProviderRef    refOut  `yaml:"gitProviderRef"`
	FrameworkRef      refOut  `yaml:"frameworkRef"`
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
// plan/model.yaml's format, so the planner starts its plan from a copy.
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
			m.Spec = systemOut{text, o.Needs, o.KPIs}
		case KindContainer:
			m.Spec = containerOut{textOut: text, SoftwareSystemRef: refOut{o.SoftwareSystem}, Type: o.Type,
				GitProviderRef: refOut{o.GitProvider}, FrameworkRef: refOut{o.Framework},
				StandardRef: optionalRef(o.Standard), ComputeRef: optionalRef(o.Compute),
				DeploymentRef: optionalRef(o.Deployment)}
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
}

type catalogEntryOut struct {
	Name                string   `yaml:"name"`
	Description         string   `yaml:"description,omitempty"`
	SupportedTypes      []string `yaml:"supportedTypes"`
	SupportedFrameworks []string `yaml:"supportedFrameworks,omitempty"`
}

// workspaceCatalog lists the installed entries by side and name, with what
// each supports: a Container may only name these.
func workspaceCatalog(catalog []CatalogEntry) catalogOut {
	sorted := slices.Clone(catalog)
	slices.SortFunc(sorted, func(a, b CatalogEntry) int { return strings.Compare(a.Name, b.Name) })
	out := catalogOut{}
	sides := map[string]*[]catalogEntryOut{
		SideFramework: &out.Frameworks, SideStandard: &out.Standards, SideCompute: &out.Computes,
		SideDeployment: &out.Deployments, SideConnector: &out.Connectors,
	}
	for _, e := range sorted {
		list, ok := sides[e.Side]
		if !ok {
			continue
		}
		types := slices.Clone(e.SupportedTypes)
		slices.Sort(types)
		frameworks := slices.Clone(e.SupportedFrameworks)
		slices.Sort(frameworks)
		*list = append(*list, catalogEntryOut{e.Name, strings.TrimSpace(e.Description), types, frameworks})
	}
	for _, list := range sides {
		if *list == nil {
			*list = []catalogEntryOut{}
		}
	}
	return out
}

type containerFileOut struct {
	Name       string   `yaml:"name"`
	Repository string   `yaml:"repository"`
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
		entry := containerFileOut{Name: c.Name, Repository: c.Repository, OpenIssues: []string{}, Unread: c.IssuesUnread}
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
