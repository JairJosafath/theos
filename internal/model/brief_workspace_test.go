package model

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func workspaceFiles(t *testing.T, ws BriefWorkspace) map[string]string {
	t.Helper()
	files, err := BriefWorkspaceFiles(ws)
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestBriefWorkspaceFilesPaths(t *testing.T) {
	files := workspaceFiles(t, BriefWorkspace{Brief: "cel-lens", Idea: "A linter.", GitProvider: "gitea"})
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	slices.Sort(paths)
	want := slices.Sorted(slices.Values(WorkspaceFiles))
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("paths = %v, want %v", paths, want)
	}
	if files[PlannerGuide] != plannerGuideText || plannerGuideText == "" {
		t.Fatalf("PLANNER.md is not the embedded guide")
	}
}

func TestBriefWorkspaceBrief(t *testing.T) {
	for _, tc := range []struct {
		name string
		ws   BriefWorkspace
		want []string
		not  []string
	}{
		{
			name: "the first round plans the idea",
			ws:   BriefWorkspace{Brief: "cel-lens", Idea: "  A linter for CEL rules.\n", GitProvider: "gitea"},
			want: []string{"# Brief cel-lens\n", "## Idea (**new**)\n\nA linter for CEL rules.\n",
				"`gitProviderRef: {name: gitea}`"},
			not: []string{"## Prompts"},
		},
		{
			name: "prompts before the first round are new with the idea",
			ws:   BriefWorkspace{Brief: "b", Idea: "Idea.", Prompts: []string{"one", "two"}},
			want: []string{"## Idea (**new**)", "1. (**new**) one\n2. (**new**) two\n"},
		},
		{
			name: "a follow-up round marks only the prompts after the planned ones",
			ws: BriefWorkspace{Brief: "b", Idea: "Idea.", IdeaPlanned: true, PromptsPlanned: 1,
				Prompts: []string{"one", "two", "three"}},
			want: []string{"## Idea\n", "1. (planned) one\n2. (**new**) two\n3. (**new**) three\n"},
			not:  []string{"## Idea (**new**)"},
		},
		{
			name: "a prompt's line breaks stay inside its list item",
			ws:   BriefWorkspace{Brief: "b", Idea: "Idea.", IdeaPlanned: true, Prompts: []string{"first line\n\n- not a list"}},
			want: []string{"1. (**new**) first line - not a list\n"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := workspaceFiles(t, tc.ws)[BriefFile]
			for _, s := range tc.want {
				if !strings.Contains(got, s) {
					t.Errorf("brief.md lacks %q:\n%s", s, got)
				}
			}
			for _, s := range tc.not {
				if strings.Contains(got, s) {
					t.Errorf("brief.md has %q:\n%s", s, got)
				}
			}
		})
	}
}

// The planner copies model/model.yaml to plan/model.yaml: what the operator
// writes must parse back to the same objects.
func TestBriefWorkspaceModelRoundTrip(t *testing.T) {
	objects, err := parsePlanModel(happyModel + `---
apiVersion: c4.factory.io/v1alpha1
kind: Container
metadata:
  name: worker
spec:
  description: |
    Runs jobs.
    Two lines: and a colon.
  softwareSystemRef: {name: shop}
  type: worker
  gitProviderRef: {name: gitea}
  frameworkRef: {name: go-service}
  standardRef: {name: go-standard}
  computeRef: {name: small}
  deploymentRef: {name: k8s}
---
apiVersion: c4.factory.io/v1alpha1
kind: Person
metadata:
  name: ann
spec:
  displayName: Ann
`)
	if err != nil {
		t.Fatal(err)
	}
	for i := range objects {
		objects[i].APIVersion = ""
	}
	// The namespace's objects come in any order; the file doesn't.
	reversed := slices.Clone(objects)
	slices.Reverse(reversed)
	got := workspaceFiles(t, BriefWorkspace{Objects: objects})[WorkspaceModelFile]
	if again := workspaceFiles(t, BriefWorkspace{Objects: reversed})[WorkspaceModelFile]; again != got {
		t.Fatalf("model.yaml depends on the objects' order:\n%s\n---- vs ----\n%s", got, again)
	}
	parsed, err := parsePlanModel(got)
	if err != nil {
		t.Fatalf("model.yaml doesn't parse as a plan: %v\n%s", err, got)
	}
	if len(parsed) != len(objects) {
		t.Fatalf("parsed %d objects, want %d", len(parsed), len(objects))
	}
	for _, o := range objects {
		p, ok := findObject(parsed, o.Ref())
		if !ok {
			t.Fatalf("%s is missing from model.yaml", o.Ref())
		}
		if p.APIVersion != PlanAPIVersion {
			t.Errorf("%s: apiVersion %q", o.Ref(), p.APIVersion)
		}
		p.APIVersion = ""
		if !reflect.DeepEqual(p, o) {
			t.Errorf("%s round-trips as\n%+v\nwant\n%+v", o.Ref(), p, o)
		}
	}
	if first := parsed[0]; first.Kind != KindProject {
		t.Errorf("model.yaml starts with %s, want the Project", first.Ref())
	}
	if last := parsed[len(parsed)-1]; last.Kind != KindRelationship {
		t.Errorf("model.yaml ends with %s, want a Relationship", last.Ref())
	}
}

func TestBriefWorkspaceCatalog(t *testing.T) {
	catalog := []CatalogEntry{
		{Side: SideFramework, Name: "python-fastapi", SupportedTypes: []string{"worker", "service"}},
		{Side: SideFramework, Name: "go-service", Description: " A Go service. ", SupportedTypes: []string{"service"},
			TemplateURL: "http://gitea/templates/go-service", TemplateRef: "v1.0.0"},
		{Side: SideStandard, Name: "go-standard", SupportedTypes: []string{"service"},
			SupportedFrameworks: []string{"go-service"}},
		{Side: SideConnector, Name: "postgres", SupportedTypes: []string{"service"}},
	}
	got := workspaceFiles(t, BriefWorkspace{Catalog: catalog})[WorkspaceCatalogFile]
	want := `frameworks:
    - name: go-service
      description: A Go service.
      supportedTypes:
        - service
    - name: python-fastapi
      supportedTypes:
        - service
        - worker
standards:
    - name: go-standard
      supportedTypes:
        - service
      supportedFrameworks:
        - go-service
computes: []
deployments: []
connectors:
    - name: postgres
      supportedTypes:
        - service
`
	if got != want {
		t.Fatalf("catalog.yaml =\n%s\nwant\n%s", got, want)
	}
}

func TestBriefWorkspaceContainers(t *testing.T) {
	many := make([]WorkspaceIssue, MaxWorkspaceIssues+2)
	for i := range many {
		many[i] = WorkspaceIssue{Number: len(many) - i, Title: fmt.Sprintf("issue %d", len(many)-i)}
	}
	for _, tc := range []struct {
		name       string
		containers []WorkspaceContainer
		want       []string
		not        []string
	}{
		{
			name:       "none",
			containers: nil,
			want:       []string{"[]\n"},
		},
		{
			name: "sorted by name, issues by number",
			containers: []WorkspaceContainer{
				{Name: "web", Repository: "http://gitea/o/web", Issues: []WorkspaceIssue{{7, "Style"}, {2, "Login\npage"}}},
				{Name: "api", Repository: "http://gitea/o/api"},
			},
			want: []string{"- name: api\n  repository: http://gitea/o/api\n  openIssues: []\n" +
				"- name: web\n  repository: http://gitea/o/web\n  openIssues:\n    - '#2 Login page'\n    - '#7 Style'\n"},
		},
		{
			name:       "unread issues say so",
			containers: []WorkspaceContainer{{Name: "api", Repository: "r", IssuesUnread: true}},
			want:       []string{"issuesUnread: true"},
		},
		{
			name:       "past the cap, issues are counted",
			containers: []WorkspaceContainer{{Name: "api", Repository: "r", Issues: many}},
			want:       []string{"'#1 issue 1'", fmt.Sprintf("'#%d issue %d'", MaxWorkspaceIssues, MaxWorkspaceIssues), "moreIssues: 2"},
			not:        []string{fmt.Sprintf("'#%d issue", MaxWorkspaceIssues+1)},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := workspaceFiles(t, BriefWorkspace{Containers: tc.containers})[WorkspaceContainersFile]
			for _, s := range tc.want {
				if !strings.Contains(got, s) {
					t.Errorf("containers.yaml lacks %q:\n%s", s, got)
				}
			}
			for _, s := range tc.not {
				if strings.Contains(got, s) {
					t.Errorf("containers.yaml has %q:\n%s", s, got)
				}
			}
		})
	}
}

// PLANNER.md says what #278 asks of the planner.
func TestPlannerGuide(t *testing.T) {
	for _, s := range []string{
		"Plan the story kinds only",
		"Use only the listed catalog",
		"Write ready issues",
		"## Acceptance criteria",
		"Explain each choice in `choices.md`, in plain words",
		"Write `needs-input.md` only for what costs money or touches access",
	} {
		if !strings.Contains(plannerGuideText, s) {
			t.Errorf("PLANNER.md lacks %q", s)
		}
	}
}
