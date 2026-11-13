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
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestStandInPlan(t *testing.T) {
	withStandard := []CatalogEntry{
		{Side: SideFramework, Name: "zeta-service", SupportedTypes: []string{TypeService}},
		{Side: SideFramework, Name: "hello", SupportedTypes: []string{TypeWorker, TypeCLI}},
		{Side: SideStandard, Name: "a-standard", SupportedTypes: []string{TypeService},
			SupportedFrameworks: []string{"zeta-service"}},
		{Side: SideStandard, Name: "hello-standard", SupportedTypes: []string{TypeCLI},
			SupportedFrameworks: []string{"hello"}},
		{Side: SideCompute, Name: "small", SupportedTypes: []string{TypeCLI}},
	}
	noStandard := []CatalogEntry{{Side: SideFramework, Name: "go-cli", SupportedTypes: []string{TypeCLI}}}
	// #1760: two Domains are installed; an idea's words name one of them.
	withDomains := append(slices.Clone(noStandard), CatalogEntry{Side: SideDomain, Name: "webshop"},
		CatalogEntry{Side: SideDomain, Name: "payments"})
	// A Deployment ships worker and service, but a-cloud deploys itself, no
	// Standard fits b-bare, and c-tool's first type is cli: the stand-in takes
	// c-tool as a worker.
	withDeployment := []CatalogEntry{
		{Side: SideFramework, Name: "a-cloud", SupportedTypes: []string{TypeService}, DeploysItself: true},
		{Side: SideFramework, Name: "b-bare", SupportedTypes: []string{TypeWorker}},
		{Side: SideFramework, Name: "b-web", SupportedTypes: []string{TypeStaticSite}},
		{Side: SideFramework, Name: "c-tool", SupportedTypes: []string{TypeWorker, TypeCLI}},
		{Side: SideStandard, Name: "c-standard", SupportedTypes: []string{TypeWorker}, SupportedFrameworks: []string{"c-tool"}},
		{Side: SideDeployment, Name: "z-ship", SupportedTypes: []string{TypeService, TypeWorker}},
	}
	// #1622: a-web supports static-site only; c-ship supports worker, the type
	// the stand-in picks from withDeployment, so it takes c-ship over the sides.
	bundles := []Bundle{{Name: "c-ship", Framework: "c-tool", Standard: "c-standard", Deployment: "z-ship"},
		{Name: "a-web", Framework: "b-web"}}
	system := PlanObject{Kind: KindSoftwareSystem, Name: "tools"}
	lend := PlanObject{Kind: KindContainer, Name: "lend", SoftwareSystem: "tools", Type: TypeCLI,
		GitProvider: "gitea", Framework: "go-cli"}
	other := PlanObject{Kind: KindContainer, Name: "api", SoftwareSystem: "tools", Type: TypeCLI,
		GitProvider: "gitea", Framework: "go-cli"}

	cases := []struct {
		name string
		ws   BriefWorkspace
		// created are the objects the plan adds, as Kind/name; container is the
		// one it adds, with its refs, when it adds one.
		created   []string
		container PlanObject
		// domains are the Domains the created SoftwareSystem names, when it
		// creates one.
		domains []string
		// issues are Container: title.
		issues []string
		// releases are the Containers releases.yaml names.
		releases   []string
		needsInput bool
	}{
		{
			name:    "idea, on the first Framework and type a Deployment ships, released (#1083)",
			ws:      BriefWorkspace{Idea: "Neighbours lend tools.", Catalog: withDeployment, Objects: []PlanObject{system}},
			created: []string{"Container/lend"},
			container: PlanObject{Kind: KindContainer, Name: "lend", SoftwareSystem: "tools", Type: TypeWorker,
				GitProvider: "gitea", Framework: "c-tool", Standard: "c-standard", Deployment: "z-ship"},
			issues:   []string{"lend: Stand-in: start lend"},
			releases: []string{"lend"},
		},
		{
			name: "idea, on the first Bundle that supports the type, released (#1622)",
			ws: BriefWorkspace{Idea: "Neighbours lend tools.", Catalog: withDeployment, Bundles: bundles,
				Objects: []PlanObject{system}},
			created: []string{"Container/lend"},
			container: PlanObject{Kind: KindContainer, Name: "lend", SoftwareSystem: "tools", Type: TypeWorker,
				GitProvider: "gitea", Bundle: "c-ship"},
			issues:   []string{"lend: Stand-in: start lend"},
			releases: []string{"lend"},
		},
		{
			name: "idea, on the sides when no Bundle supports the type (#1622)",
			ws: BriefWorkspace{Idea: "Neighbours lend tools.", Catalog: withDeployment, Bundles: bundles[1:],
				Objects: []PlanObject{system}},
			created: []string{"Container/lend"},
			container: PlanObject{Kind: KindContainer, Name: "lend", SoftwareSystem: "tools", Type: TypeWorker,
				GitProvider: "gitea", Framework: "c-tool", Standard: "c-standard", Deployment: "z-ship"},
			issues:   []string{"lend: Stand-in: start lend"},
			releases: []string{"lend"},
		},
		{
			name: "prompts, the Container released already: nothing released again",
			ws: BriefWorkspace{Idea: "Neighbours lend tools.", IdeaPlanned: true, Catalog: withDeployment,
				Objects: []PlanObject{system, lend}, Prompts: []string{"Add a calendar."}},
			issues: []string{"lend: Stand-in: Add a calendar."},
		},
		{
			name:    "idea, in the first SoftwareSystem, with the first Standard that fits",
			ws:      BriefWorkspace{Idea: "Neighbours lend tools.", Catalog: withStandard, Objects: []PlanObject{system}},
			created: []string{"Container/lend"},
			container: PlanObject{Kind: KindContainer, Name: "lend", SoftwareSystem: "tools", Type: TypeCLI,
				GitProvider: "gitea", Framework: "hello", Standard: "hello-standard"},
			issues: []string{"lend: Stand-in: start lend"},
		},
		{
			name: "idea, in the first Internal SoftwareSystem, not an External one before it (#1428)",
			ws: BriefWorkspace{Idea: "Neighbours lend tools.", Catalog: noStandard, Objects: []PlanObject{
				{Kind: KindSoftwareSystem, Name: "payments", Location: LocationExternal}, system}},
			created: []string{"Container/lend"},
			container: PlanObject{Kind: KindContainer, Name: "lend", SoftwareSystem: "tools", Type: TypeCLI,
				GitProvider: "gitea", Framework: "go-cli"},
			issues: []string{"lend: Stand-in: start lend"},
		},
		{
			name:    "idea, in a new SoftwareSystem, only an External one there",
			ws:      BriefWorkspace{Idea: "Neighbours lend tools.", Catalog: noStandard, Objects: []PlanObject{{Kind: KindSoftwareSystem, Name: "payments", Location: LocationExternal}}},
			created: []string{"SoftwareSystem/lend", "Container/lend"},
			container: PlanObject{Kind: KindContainer, Name: "lend", SoftwareSystem: "lend", Type: TypeCLI,
				GitProvider: "gitea", Framework: "go-cli"},
			issues: []string{"lend: Stand-in: start lend"},
		},
		{
			name:    "idea, in a new SoftwareSystem, without a Standard",
			ws:      BriefWorkspace{Idea: "Neighbours lend tools.", Catalog: noStandard},
			created: []string{"SoftwareSystem/lend", "Container/lend"},
			container: PlanObject{Kind: KindContainer, Name: "lend", SoftwareSystem: "lend", Type: TypeCLI,
				GitProvider: "gitea", Framework: "go-cli"},
			issues: []string{"lend: Stand-in: start lend"},
		},
		{
			name:    "idea, in a new SoftwareSystem about the installed Domains the idea names (#1760)",
			ws:      BriefWorkspace{Idea: "A Webshop for plants, no payments-provider yet.", Catalog: withDomains},
			created: []string{"SoftwareSystem/lend", "Container/lend"},
			container: PlanObject{Kind: KindContainer, Name: "lend", SoftwareSystem: "lend", Type: TypeCLI,
				GitProvider: "gitea", Framework: "go-cli"},
			domains: []string{"payments", "webshop"},
			issues:  []string{"lend: Stand-in: start lend"},
		},
		{
			name:    "idea, in a new SoftwareSystem, no installed Domain a word of the idea (#1760)",
			ws:      BriefWorkspace{Idea: "A webshopping site.", Catalog: withDomains},
			created: []string{"SoftwareSystem/lend", "Container/lend"},
			container: PlanObject{Kind: KindContainer, Name: "lend", SoftwareSystem: "lend", Type: TypeCLI,
				GitProvider: "gitea", Framework: "go-cli"},
			issues: []string{"lend: Stand-in: start lend"},
		},
		{
			// The Namespace section names the namespace; it is not the idea.
			name:    "idea with no prompts, the namespace named like a Domain: no Domain (#1760)",
			ws:      BriefWorkspace{Idea: "Neighbours lend tools.", Namespace: "webshop", Catalog: withDomains},
			created: []string{"SoftwareSystem/lend", "Container/lend"},
			container: PlanObject{Kind: KindContainer, Name: "lend", SoftwareSystem: "lend", Type: TypeCLI,
				GitProvider: "gitea", Framework: "go-cli"},
			issues: []string{"lend: Stand-in: start lend"},
		},
		{
			name:    "idea naming a Domain, in an existing SoftwareSystem: its Domains stay (#1760)",
			ws:      BriefWorkspace{Idea: "A webshop for plants.", Catalog: withDomains, Objects: []PlanObject{system}},
			created: []string{"Container/lend"},
			container: PlanObject{Kind: KindContainer, Name: "lend", SoftwareSystem: "tools", Type: TypeCLI,
				GitProvider: "gitea", Framework: "go-cli"},
			issues: []string{"lend: Stand-in: start lend"},
		},
		{
			name:    "idea, the Brief's name taken by a Container",
			ws:      BriefWorkspace{Idea: "Again.", Catalog: noStandard, Objects: []PlanObject{system, lend}},
			created: []string{"Container/lend-2"},
			container: PlanObject{Kind: KindContainer, Name: "lend-2", SoftwareSystem: "tools", Type: TypeCLI,
				GitProvider: "gitea", Framework: "go-cli"},
			issues: []string{"lend-2: Stand-in: start lend"},
		},
		{
			name: "idea and prompts in one round",
			ws: BriefWorkspace{Idea: "Neighbours lend tools.", Catalog: noStandard, Objects: []PlanObject{system},
				Prompts: []string{"Add a calendar."}},
			created: []string{"Container/lend"},
			container: PlanObject{Kind: KindContainer, Name: "lend", SoftwareSystem: "tools", Type: TypeCLI,
				GitProvider: "gitea", Framework: "go-cli"},
			issues: []string{"lend: Stand-in: start lend", "lend: Stand-in: Add a calendar."},
		},
		{
			name: "a prompt naming AGENTS.md, titled without it",
			ws: BriefWorkspace{Idea: "Neighbours lend tools.", IdeaPlanned: true, Catalog: noStandard,
				Objects: []PlanObject{system, lend}, Prompts: []string{
					"Add a note to `AGENTS.md` and docs/agents/reviewer.md: run make check as CI does.",
				}},
			issues: []string{"lend: Stand-in: Add a note to a protected file and a protected file: run make check as CI does."},
		},
		{
			name: "a prompt with criteria of its own naming AGENTS.md",
			ws: BriefWorkspace{Idea: "Neighbours lend tools.", IdeaPlanned: true, Catalog: noStandard,
				Objects: []PlanObject{system, lend}, Prompts: []string{
					"Tell the agents about CI.\n\n## Acceptance criteria\n\n- [ ] AGENTS.md says to run make check",
				}},
			issues: []string{"lend: Stand-in: Tell the agents about CI. ## Acceptance criteria - [ ] a protected file says to…"},
		},
		{
			name: "prompts, for the Brief's Container",
			ws: BriefWorkspace{Idea: "Neighbours lend tools.", IdeaPlanned: true, Catalog: noStandard,
				Objects: []PlanObject{system, other, lend}, PromptsPlanned: 1, Prompts: []string{
					"Add a calendar.", "Support ValidatingAdmissionPolicy files first.",
					"A long prompt that goes on and on about what the people who lend their tools want next, " +
						"so its title is cut short",
				}},
			issues: []string{
				"lend: Stand-in: Support ValidatingAdmissionPolicy files first.",
				"lend: Stand-in: A long prompt that goes on and on about what the people who lend their tools…",
			},
		},
		{
			name: "prompts, for the first Container when none is the Brief's",
			ws: BriefWorkspace{Idea: "Neighbours lend tools.", IdeaPlanned: true, Catalog: noStandard,
				Objects: []PlanObject{system, other}, Prompts: []string{"Add a calendar."}},
			issues: []string{"api: Stand-in: Add a calendar."},
		},
		{
			name: "prompts, and no Container to file them for",
			ws: BriefWorkspace{Idea: "Neighbours lend tools.", IdeaPlanned: true, Catalog: noStandard,
				Objects: []PlanObject{system}, Prompts: []string{"Add a calendar."}},
		},
		{
			name:       "no Framework in the catalog",
			ws:         BriefWorkspace{Idea: "Neighbours lend tools.", Catalog: []CatalogEntry{withStandard[2]}},
			needsInput: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.ws.Brief, tc.ws.GitProvider = "lend", "gitea"
			workspace, err := BriefWorkspaceFiles(tc.ws)
			if err != nil {
				t.Fatal(err)
			}
			files, err := StandInPlan(workspace)
			if err != nil {
				t.Fatal(err)
			}
			plan, err := ParsePlan(files)
			if err != nil {
				t.Fatalf("ParsePlan: %v\n%v", err, files)
			}
			if tc.needsInput {
				if plan.NeedsInput == "" {
					t.Fatalf("want needs-input.md, got %v", files)
				}
				return
			}
			current := CurrentModel{Objects: tc.ws.Objects, Catalog: tc.ws.Catalog, Bundles: tc.ws.Bundles,
				GitProvider: tc.ws.GitProvider}
			if refusals := CheckPlan(plan, current); len(refusals) > 0 {
				t.Fatalf("CheckPlan refuses the stand-in's plan: %v\n%s", refusals, files[PlanModelFile])
			}
			created, changed := PlanDiff(plan, current)
			if len(changed) > 0 {
				t.Errorf("changed %v, want nothing changed", changed)
			}
			var gotCreated []string
			for _, o := range created {
				gotCreated = append(gotCreated, o.Ref().String())
				if o.Kind == KindContainer {
					o.APIVersion = ""
					if !reflect.DeepEqual(o, tc.container) {
						t.Errorf("container\n got %+v\nwant %+v", o, tc.container)
					}
				}
				if o.Kind == KindSoftwareSystem {
					if !slices.Equal(o.Domains, tc.domains) {
						t.Errorf("system %s domains %q, want %q", o.Name, o.Domains, tc.domains)
					}
					for _, d := range tc.domains {
						if !strings.Contains(plan.Summary, d) {
							t.Errorf("summary %q doesn't name the Domain %s", plan.Summary, d)
						}
					}
				}
			}
			if !slices.Equal(gotCreated, tc.created) {
				t.Errorf("created %v, want %v", gotCreated, tc.created)
			}
			var gotIssues []string
			for _, i := range plan.Issues {
				gotIssues = append(gotIssues, i.Container+": "+i.Title)
			}
			if !slices.Equal(gotIssues, tc.issues) {
				t.Errorf("issues\n got %q\nwant %q", gotIssues, tc.issues)
			}
			if !slices.Equal(plan.Releases, tc.releases) {
				t.Errorf("releases %q, want %q", plan.Releases, tc.releases)
			}
			if len(plan.Choices) == 0 || !strings.Contains(plan.Choices[0], "stand-in") {
				t.Errorf("choices %q, want the stand-in to say what it chose", plan.Choices)
			}
		})
	}
}

// A Brief named brief-x plans its idea as Container x; a later prompt round
// files the prompt for x, not for the model's first Container.
func TestStandInPlanBriefNamedBrief(t *testing.T) {
	catalog := []CatalogEntry{{Side: SideFramework, Name: "go-cli", SupportedTypes: []string{TypeCLI}}}
	objects := []PlanObject{
		{Kind: KindSoftwareSystem, Name: "tools"},
		{Kind: KindContainer, Name: "api", SoftwareSystem: "tools", Type: TypeCLI, GitProvider: "gitea", Framework: "go-cli"},
	}
	round := func(ws BriefWorkspace) Plan {
		t.Helper()
		ws.Brief, ws.GitProvider, ws.Catalog = BriefRepository("x"), "gitea", catalog
		workspace, err := BriefWorkspaceFiles(ws)
		if err != nil {
			t.Fatal(err)
		}
		files, err := StandInPlan(workspace)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := ParsePlan(files)
		if err != nil {
			t.Fatalf("ParsePlan: %v\n%v", err, files)
		}
		return plan
	}

	idea := round(BriefWorkspace{Idea: "Neighbours lend tools.", Objects: objects})
	if len(idea.Issues) != 1 || idea.Issues[0].Container != "x" {
		t.Fatalf("idea round issues %+v, want one for Container x", idea.Issues)
	}
	prompt := round(BriefWorkspace{Idea: "Neighbours lend tools.", IdeaPlanned: true, Objects: idea.Objects,
		Prompts: []string{"Add a calendar."}})
	got := make([]string, 0, len(prompt.Issues))
	for _, i := range prompt.Issues {
		got = append(got, i.Container+": "+i.Title)
	}
	if want := []string{"x: Stand-in: Add a calendar."}; !slices.Equal(got, want) {
		t.Errorf("prompt round issues %q, want %q", got, want)
	}
}

// In a tending round the stand-in decides nothing, and the plan is a tending
// round's.
func TestStandInPlanTending(t *testing.T) {
	workspace, err := BriefWorkspaceFiles(BriefWorkspace{Brief: "lens", GitProvider: "gitea", Idea: "A linter.",
		IdeaPlanned: true, Tending: true,
		Failed: []FailedIssue{{Container: "cli", Number: 1, Title: "Flags", Kind: FailedTask, Tries: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	files, err := StandInPlan(workspace)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := ParseTendPlan(files)
	if err != nil {
		t.Fatalf("ParseTendPlan: %v\n%v", err, files)
	}
	if len(plan.FailedAnswers) != 0 || plan.Summary != StandInTendSummary {
		t.Errorf("plan = %+v, want no answers and the stand-in's summary", plan)
	}
}
