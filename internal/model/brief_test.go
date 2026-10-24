package model

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

const happyModel = `apiVersion: c4.factory.io/v1beta1
kind: Project
metadata:
  name: shop
spec:
  displayName: Shop
---
apiVersion: c4.factory.io/v1beta1
kind: SoftwareSystem
metadata:
  name: shop
spec:
  description: Sells things
  needs: [orders]
  kpis: [orders per day]
---
apiVersion: c4.factory.io/v1beta1
kind: Container
metadata:
  name: api
spec:
  softwareSystemRef: {name: shop}
  type: service
  gitProviderRef: {name: gitea}
  frameworkRef: {name: go-service}
---
apiVersion: c4.factory.io/v1beta1
kind: Container
metadata:
  name: web
spec:
  softwareSystemRef: {name: shop}
  displayName: Web
  type: static-site
  gitProviderRef: {name: gitea}
  frameworkRef: {name: react-site}
  standardRef: {name: web-standard}
---
apiVersion: c4.factory.io/v1beta1
kind: DataStore
metadata:
  name: db
spec:
  softwareSystemRef: {name: shop}
  connectorRef: {name: postgres}
---
apiVersion: c4.factory.io/v1beta1
kind: Relationship
metadata:
  name: api-to-db
spec:
  source: {kind: Container, name: api}
  target: {kind: DataStore, name: db}
  description: Keeps orders
`

const readyIssue = `---
title: Show the orders
labels: [kind/feature]
---
## Goal

A page lists the orders.

## Acceptance criteria

- [ ] The page lists every order
`

func happyFiles() map[string]string {
	return map[string]string{
		PlanModelFile:             happyModel,
		"plan/issues/01-web.md":   readyIssue,
		"plan/issues/02-api.md":   strings.Replace(readyIssue, "Show the orders", "Serve the orders", 1),
		PlanChoicesFile:           "- A static site for the shop\n- Postgres keeps orders\n",
		PlanSummaryFile:           "\nPlanned the shop: a web site and an API.\n",
		"brief.md":                "not part of the plan",
		"plan/issues/.gitkeep":    "",
		"model/containers/api.md": "not part of the plan",
	}
}

func happyCurrent() CurrentModel {
	return CurrentModel{
		Namespace: "shop",
		Objects: []PlanObject{
			{Kind: KindProject, Name: "shop", DisplayName: "Shop"},
			{Kind: KindSoftwareSystem, Name: "shop", Description: "Sells things", Needs: []string{"orders"},
				KPIs: []string{"orders per day"}},
			{Kind: KindContainer, Name: "api", SoftwareSystem: "shop", Type: TypeService, GitProvider: "gitea",
				Framework: "go-service"},
		},
		Catalog: []CatalogEntry{
			{Side: SideFramework, Name: "go-service", SupportedTypes: []string{TypeService}, ReadsOperations: true},
			{Side: SideFramework, Name: "react-site", SupportedTypes: []string{TypeStaticSite}, ReadsOperations: true},
			{Side: SideStandard, Name: "web-standard", SupportedTypes: []string{TypeStaticSite},
				SupportedFrameworks: []string{"react-site"}},
			{Side: SideStandard, Name: "go-standard", SupportedTypes: []string{TypeService},
				SupportedFrameworks: []string{"go-service"}},
			{Side: SideCompute, Name: "k8s", SupportedTypes: []string{TypeService}},
			{Side: SideConnector, Name: "postgres", SupportedTypes: []string{TypeService}},
		},
		// #1622: a plan's Container may name a Bundle instead of its sides.
		Bundles: []Bundle{
			{Name: "react-web", Framework: "react-site", Standard: "web-standard"},
			{Name: "go-k8s", Framework: "go-service", Standard: "go-standard", Compute: "k8s"},
			{Name: "vue-web", Framework: "vue-site"},
		},
		GitProvider: "gitea",
	}
}

func TestParsePlan(t *testing.T) {
	plan, err := ParsePlan(happyFiles())
	if err != nil {
		t.Fatal(err)
	}
	refs := make([]string, 0, len(plan.Objects))
	for _, o := range plan.Objects {
		refs = append(refs, o.Ref().String())
	}
	wantRefs := []string{"Project/shop", "SoftwareSystem/shop", "Container/api", "Container/web", "DataStore/db",
		"Relationship/api-to-db"}
	if !reflect.DeepEqual(refs, wantRefs) {
		t.Errorf("objects %v, want %v", refs, wantRefs)
	}
	web := plan.Objects[3]
	if web.Type != TypeStaticSite || web.Framework != "react-site" || web.Standard != "web-standard" ||
		web.SoftwareSystem != "shop" || web.GitProvider != "gitea" || web.DisplayName != "Web" {
		t.Errorf("web = %+v", web)
	}
	if s := plan.Objects[1]; !reflect.DeepEqual(s.KPIs, []string{"orders per day"}) || s.Description != "Sells things" ||
		s.Location != LocationInternal {
		t.Errorf("system = %+v", s)
	}
	if r := plan.Objects[5]; r.Source != (Ref{KindContainer, "api"}) || r.Target != (Ref{KindDataStore, "db"}) {
		t.Errorf("relationship = %+v", r)
	}
	if len(plan.Issues) != 2 || plan.Issues[0].Container != "web" || plan.Issues[0].N != 1 ||
		plan.Issues[0].Title != "Show the orders" || !reflect.DeepEqual(plan.Issues[0].Labels, []string{"kind/feature"}) ||
		!strings.HasPrefix(plan.Issues[0].Body, "## Goal") || plan.Issues[1].Container != "api" {
		t.Errorf("issues = %+v", plan.Issues)
	}
	if want := []string{"A static site for the shop", "Postgres keeps orders"}; !reflect.DeepEqual(plan.Choices, want) {
		t.Errorf("choices %v, want %v", plan.Choices, want)
	}
	if plan.NeedsInput != "" {
		t.Errorf("needs input %q", plan.NeedsInput)
	}
	if plan.Summary != "Planned the shop: a web site and an API." {
		t.Errorf("summary %q", plan.Summary)
	}
}

// TestParsePlanLocation: a plan's SoftwareSystem carries its location, and
// one the planner leaves out is Internal (#1150).
func TestParsePlanLocation(t *testing.T) {
	system := func(location string) string {
		return "apiVersion: c4.factory.io/v1beta1\nkind: SoftwareSystem\nmetadata:\n  name: payments\nspec:\n" +
			"  description: Takes the money\n" + location
	}
	tests := []struct {
		name, spec, want string
	}{
		{"left out", "", LocationInternal},
		{"Internal", "  location: Internal\n", LocationInternal},
		{"External", "  location: External\n", LocationExternal},
		{"another word, for the check to refuse", "  location: Outside\n", "Outside"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objects, err := parsePlanModel(system(tt.spec))
			if err != nil {
				t.Fatal(err)
			}
			if got := objects[0].Location; got != tt.want {
				t.Errorf("location %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRoundSummary(t *testing.T) {
	plan, err := ParsePlan(happyFiles())
	if err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("Planned the shop. ", 60) + "It sells thi"
	for _, tc := range []struct {
		name string
		plan Plan
		want string
	}{
		{"the planner's summary, on one line", Plan{Summary: "Planned the shop:\n  a web site."},
			"Planned the shop: a web site."},
		{"a long one ends on a whole sentence", Plan{Summary: long},
			strings.TrimSpace(strings.Repeat("Planned the shop. ", 55))},
		{"without summary.md, what the plan holds", Plan{Objects: plan.Objects, Issues: plan.Issues},
			"The model has 2 Containers; the round files 2 issues."},
		{"one of each", Plan{Objects: plan.Objects[2:3], Issues: plan.Issues[:1]},
			"The model has one Container; the round files one issue."},
		{"a question is not a plan", Plan{Summary: "Planned the shop.", NeedsInput: "Which payment provider?"},
			"Asked for input before planning."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := RoundSummary(tc.plan); got != tc.want {
				t.Errorf("RoundSummary = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParsePlanNeedsInput(t *testing.T) {
	plan, err := ParsePlan(map[string]string{PlanNeedsInputFile: "  Which payment provider?\n"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.NeedsInput != "Which payment provider?" || plan.Objects != nil {
		t.Errorf("plan = %+v", plan)
	}
}

func TestParsePlanErrors(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		want  string // the error starts with the file it names
	}{
		{"no model", map[string]string{PlanChoicesFile: "x"}, PlanModelFile},
		{"model isn't YAML", map[string]string{PlanModelFile: "kind: [Container"}, PlanModelFile},
		{"a document without a name", map[string]string{PlanModelFile: "kind: Person\n"}, PlanModelFile},
		{"a field the kind doesn't have", map[string]string{
			PlanModelFile: "apiVersion: c4.factory.io/v1beta1\nkind: Person\nmetadata: {name: ann}\nspec: {type: service}\n"},
			PlanModelFile},
		{"an empty needs-input", map[string]string{PlanNeedsInputFile: " \n", PlanModelFile: ""}, PlanNeedsInputFile},
		{"an issue file badly named", map[string]string{PlanModelFile: "", "plan/issues/web.md": readyIssue},
			"plan/issues/web.md"},
		{"an issue for a name that isn't a DNS label", map[string]string{PlanModelFile: "", "plan/issues/01-Web.md": readyIssue},
			"plan/issues/01-Web.md"},
		{"an issue without front matter", map[string]string{PlanModelFile: "", "plan/issues/01-web.md": "## Goal\n"},
			"plan/issues/01-web.md"},
		{"front matter never closed", map[string]string{PlanModelFile: "", "plan/issues/01-web.md": "---\ntitle: x\n"},
			"plan/issues/01-web.md"},
		{"an issue without a title", map[string]string{PlanModelFile: "", "plan/issues/01-web.md": "---\nlabels: [a]\n---\nbody"},
			"plan/issues/01-web.md"},
		{"an unknown front matter field", map[string]string{PlanModelFile: "",
			"plan/issues/01-web.md": "---\ntitle: x\nmilestone: v1\n---\nbody"}, "plan/issues/01-web.md"},
		{"two issues with one number", map[string]string{PlanModelFile: "",
			"plan/issues/01-web.md": readyIssue, "plan/issues/1-api.md": readyIssue}, "plan/issues/1-api.md"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParsePlan(tt.files)
			if err == nil || !strings.HasPrefix(err.Error(), tt.want) {
				t.Errorf("err = %v, want one naming %s", err, tt.want)
			}
		})
	}
}

func TestCheckPlan(t *testing.T) {
	object := func(p *Plan, ref string) *PlanObject {
		for i, o := range p.Objects {
			if o.Ref().String() == ref {
				return &p.Objects[i]
			}
		}
		t.Fatalf("no %s in the plan", ref)
		return nil
	}
	// webUsesAPI makes web's issue use api's module, with the Relationship it
	// needs and api released this round.
	webUsesAPI := func(p *Plan) {
		p.Objects = append(p.Objects, PlanObject{APIVersion: PlanAPIVersion, Kind: KindRelationship, Name: "web-to-api",
			Source: Ref{KindContainer, "web"}, Target: Ref{KindContainer, "api"}, Description: "Reads orders"})
		p.Releases = []string{"api"}
		p.Issues[0].Uses = []string{"api"}
	}
	payments := func(location string) PlanObject {
		return PlanObject{APIVersion: PlanAPIVersion, Kind: KindSoftwareSystem, Name: "payments",
			Description: "Takes the money", Location: location}
	}
	tests := []struct {
		name    string
		change  func(p *Plan, c *CurrentModel)
		subject string // "" means the plan passes
		reason  string
	}{
		{"the happy path", func(*Plan, *CurrentModel) {}, "", ""},
		{"uses: an app the round releases", func(p *Plan, _ *CurrentModel) { webUsesAPI(p) }, "", ""},
		{"uses: an app that released before", func(p *Plan, c *CurrentModel) {
			webUsesAPI(p)
			p.Releases, p.Issues = nil, p.Issues[:1]
			c.Released = []string{"api"}
		}, "", ""},
		{"uses: the issue's own Container", func(p *Plan, _ *CurrentModel) {
			webUsesAPI(p)
			p.Issues[0].Uses = []string{"web"}
		}, "plan/issues/01-web.md", `uses: names "web", the issue's own Container`},
		{"uses: a Container not in the model", func(p *Plan, _ *CurrentModel) {
			webUsesAPI(p)
			p.Issues[0].Uses = []string{"core"}
		}, "plan/issues/01-web.md", `uses: names "core", which is not a Container of plan/model.yaml`},
		{"uses: a DataStore", func(p *Plan, _ *CurrentModel) {
			webUsesAPI(p)
			p.Issues[0].Uses = []string{"db"}
		}, "plan/issues/01-web.md", `uses: names "db", which is not a Container`},
		{"uses: a Container twice", func(p *Plan, _ *CurrentModel) {
			webUsesAPI(p)
			p.Issues[0].Uses = []string{"api", "api"}
		}, "plan/issues/01-web.md", `uses: names "api" twice`},
		{"uses: with no Relationship to it", func(p *Plan, _ *CurrentModel) {
			webUsesAPI(p)
			p.Objects = p.Objects[:len(p.Objects)-1]
		}, "plan/issues/01-web.md", `no Relationship goes from Container web to it`},
		{"uses: with the Relationship the other way", func(p *Plan, _ *CurrentModel) {
			webUsesAPI(p)
			rel := &p.Objects[len(p.Objects)-1]
			rel.Source, rel.Target = rel.Target, rel.Source
		}, "plan/issues/01-web.md", `no Relationship goes from Container web to it`},
		{"uses: an app with no release made or planned", func(p *Plan, _ *CurrentModel) {
			webUsesAPI(p)
			p.Releases = nil
		}, "plan/issues/01-web.md", `"api", which has made no release and gets none this round; name it in plan/releases.yaml`},
		{"uses: an app released this round with no work", func(p *Plan, _ *CurrentModel) {
			webUsesAPI(p)
			p.Issues = p.Issues[:1]
		}, "plan/issues/01-web.md", `the round files no issue for it`},
		{"a design idea with uses:", func(p *Plan, c *CurrentModel) {
			webUsesAPI(p)
			c.Designer = true
			p.Issues[0].Body, p.Issues[0].Labels = "A calm page for orders", []string{LabelDesign}
		}, "plan/issues/01-web.md", "is a design idea (status/design) and has uses:"},
		{"story text of what exists may change", func(p *Plan, _ *CurrentModel) {
			api := object(p, "Container/api")
			api.DisplayName, api.Description = "API", "Serves orders"
			object(p, "SoftwareSystem/shop").Needs = []string{"orders", "refunds"}
		}, "", ""},
		{"a Secret", func(p *Plan, _ *CurrentModel) {
			p.Objects = append(p.Objects, PlanObject{APIVersion: "v1", Kind: "Secret", Name: "token"})
		}, "Secret/token", "is the person's to make"},
		{"a GitProvider", func(p *Plan, _ *CurrentModel) {
			p.Objects = append(p.Objects, PlanObject{APIVersion: PlanAPIVersion, Kind: "GitProvider", Name: "github"})
		}, "GitProvider/github", "a plan writes only Project, Person"},
		{"an Agent", func(p *Plan, _ *CurrentModel) {
			p.Objects = append(p.Objects, PlanObject{APIVersion: PlanAPIVersion, Kind: "Agent", Name: "cheap"})
		}, "Agent/cheap", "is the person's to make"},
		{"a story kind of another group", func(p *Plan, _ *CurrentModel) {
			object(p, "Container/web").APIVersion = "example.com/v1"
		}, "Container/web", PlanAPIVersion},
		{"a name that isn't a DNS label", func(p *Plan, _ *CurrentModel) {
			p.Objects = append(p.Objects, PlanObject{APIVersion: PlanAPIVersion, Kind: KindPerson, Name: "Ann_B"})
		}, "Person/Ann_B", "not a DNS label"},
		{"an object twice", func(p *Plan, _ *CurrentModel) {
			p.Objects = append(p.Objects, *object(p, "DataStore/db"))
		}, "DataStore/db", "twice"},
		{"more than 20 objects", func(p *Plan, _ *CurrentModel) {
			for i := range 18 {
				p.Objects = append(p.Objects, PlanObject{APIVersion: PlanAPIVersion, Kind: KindPerson, Name: fmt.Sprintf("p%d", i)})
			}
		}, PlanModelFile, "creates or changes 20+ objects"},
		{"more than 10 issues", func(p *Plan, _ *CurrentModel) {
			for i := range 9 {
				p.Issues = append(p.Issues, PlanIssue{File: "x", N: 10 + i, Container: "web", Body: p.Issues[0].Body})
			}
		}, planIssuesDir, "has 11 issues"},
		// #1622: a Container on a Bundle is checked as the operator checks it.
		{"a Bundle that supports the type", func(p *Plan, _ *CurrentModel) {
			web := object(p, "Container/web")
			web.Bundle, web.Framework, web.Standard = "react-web", "", ""
		}, "", ""},
		{"a Bundle that doesn't support the type", func(p *Plan, _ *CurrentModel) {
			web := object(p, "Container/web")
			web.Bundle, web.Framework, web.Standard = "go-k8s", "", ""
		}, "Container/web", "bundleRef: bundle go-k8s: go-service, go-standard and k8s do not support static-site"},
		{"a Bundle whose Framework deploys itself with a Compute", func(p *Plan, c *CurrentModel) {
			c.Catalog = append(c.Catalog,
				CatalogEntry{Side: SideFramework, Name: "aws-lambda", SupportedTypes: []string{TypeFunction}, DeploysItself: true},
				CatalogEntry{Side: SideCompute, Name: "lambda", SupportedTypes: []string{TypeFunction}})
			c.Bundles = append(c.Bundles, Bundle{Name: "lambda-on-k8s", Framework: "aws-lambda", Compute: "lambda"})
			web := object(p, "Container/web")
			web.Type, web.Bundle, web.Framework, web.Standard = TypeFunction, "lambda-on-k8s", "", ""
		}, "Container/web", "bundleRef: bundle lambda-on-k8s: aws-lambda deploys itself"},
		{"an existing Container on a Bundle, the plan naming only the Bundle", func(p *Plan, c *CurrentModel) {
			c.Objects = append(c.Objects, PlanObject{Kind: KindContainer, Name: "mailer", SoftwareSystem: "shop",
				Type: TypeService, GitProvider: "gitea", Bundle: "go-k8s", Framework: "go-service",
				Standard: "go-standard", Deployment: "k8s"})
			p.Objects = append(p.Objects, PlanObject{APIVersion: PlanAPIVersion, Kind: KindContainer, Name: "mailer", SoftwareSystem: "shop",
				Type: TypeService, GitProvider: "gitea", Bundle: "go-k8s"})
		}, "", ""},
		{"an existing Container on a Bundle, the plan naming its sides instead", func(p *Plan, c *CurrentModel) {
			c.Objects = append(c.Objects, PlanObject{Kind: KindContainer, Name: "mailer", SoftwareSystem: "shop",
				Type: TypeService, GitProvider: "gitea", Bundle: "go-k8s", Framework: "go-service",
				Standard: "go-standard", Deployment: "k8s"})
			p.Objects = append(p.Objects, PlanObject{APIVersion: PlanAPIVersion, Kind: KindContainer, Name: "mailer", SoftwareSystem: "shop",
				Type: TypeService, GitProvider: "gitea", Framework: "go-service", Standard: "go-standard", Deployment: "k8s"})
		}, "Container/mailer", `bundleRef can't change (from "go-k8s" to "")`},
		{"a Bundle beside a side", func(p *Plan, _ *CurrentModel) {
			object(p, "Container/web").Bundle = "react-web"
		}, "Container/web", "bundleRef replaces frameworkRef, standardRef, computeRef and deploymentRef: name the Bundle or the sides, never both"},
		{"a Bundle not of the namespace", func(p *Plan, _ *CurrentModel) {
			web := object(p, "Container/web")
			web.Bundle, web.Framework, web.Standard = "svelte-web", "", ""
		}, "Container/web", `bundleRef names "svelte-web", which is not a Bundle of the namespace; installed: go-k8s, react-web and vue-web`},
		{"a Bundle on an entry that isn't installed", func(p *Plan, _ *CurrentModel) {
			web := object(p, "Container/web")
			web.Bundle, web.Framework, web.Standard = "vue-web", "", ""
		}, "Container/web", "bundleRef: bundle vue-web names Framework vue-site, which is not installed; fix the Bundle or name another"},
		{"neither a Bundle nor a Framework", func(p *Plan, _ *CurrentModel) {
			web := object(p, "Container/web")
			web.Framework, web.Standard = "", ""
		}, "Container/web", "names no bundleRef and no frameworkRef"},
		{"a Framework not installed", func(p *Plan, _ *CurrentModel) {
			object(p, "Container/web").Framework = "vue-site"
		}, "Container/web", `frameworkRef names "vue-site", which is not an installed Framework; installed: go-service and react-site`},
		{"a Framework that doesn't support the type", func(p *Plan, _ *CurrentModel) {
			object(p, "Container/web").Framework = "go-service"
		}, "Container/web", "frameworkRef names go-service, which does not support static-site; Frameworks that do: react-site"},
		{"a Standard that doesn't support the type", func(p *Plan, _ *CurrentModel) {
			object(p, "Container/web").Standard = "go-standard"
		}, "Container/web", "standardRef names go-standard, which does not support static-site"},
		{"a Standard that doesn't fit the Framework", func(p *Plan, c *CurrentModel) {
			c.Catalog = append(c.Catalog, CatalogEntry{Side: SideFramework, Name: "vue-site", SupportedTypes: []string{TypeStaticSite}})
			object(p, "Container/web").Framework = "vue-site"
		}, "Container/web", "standardRef: web-standard does not support vue-site"},
		{"a Compute not installed", func(p *Plan, _ *CurrentModel) {
			object(p, "Container/web").Compute = "lambda"
		}, "Container/web", `computeRef names "lambda"`},
		{"a Deployment when none is installed", func(p *Plan, _ *CurrentModel) {
			object(p, "Container/web").Deployment = "helm"
		}, "Container/web", "installed: none"},
		{"a Connector not installed", func(p *Plan, _ *CurrentModel) {
			object(p, "DataStore/db").Connector = "mysql"
		}, "DataStore/db", `connectorRef names "mysql"`},
		{"a Connector that doesn't support the Container", func(p *Plan, _ *CurrentModel) {
			rel := object(p, "Relationship/api-to-db")
			rel.Name, rel.Source = "web-to-db", Ref{KindContainer, "web"}
		}, "Relationship/web-to-db", "Connector postgres does not support static-site, the type of Container web"},
		{"a Container without a type", func(p *Plan, _ *CurrentModel) {
			object(p, "Container/web").Type = ""
		}, "Container/web", `type "" is not a Container type; one of static-site, service`},
		{"a Container of an unknown type", func(p *Plan, _ *CurrentModel) {
			object(p, "Container/web").Type = "website"
		}, "Container/web", `type "website" is not a Container type`},
		{"a Container named as a Brief's repository", func(p *Plan, _ *CurrentModel) {
			c := *object(p, "Container/web")
			c.Name = "brief-notes"
			p.Objects = append(p.Objects, c)
		}, "Container/brief-notes", "a Container may not be named brief-*: that name is a Brief's repository"},
		{"a Relationship to itself", func(p *Plan, _ *CurrentModel) {
			object(p, "Relationship/api-to-db").Target = Ref{KindContainer, "api"}
		}, "Relationship/api-to-db", "points to itself"},
		{"a Relationship that bundles a DataStore", func(p *Plan, _ *CurrentModel) {
			object(p, "Relationship/api-to-db").Bundles = true
		}, "Relationship/api-to-db", "only a Container to Container link can bundle"},
		{"another GitProvider than the Brief's", func(p *Plan, _ *CurrentModel) {
			object(p, "Container/web").GitProvider = "github"
		}, "Container/web", `gitProviderRef names "github"; a Brief's Containers use its GitProvider, "gitea"`},
		{"a Container of a system not in the plan", func(p *Plan, _ *CurrentModel) {
			object(p, "Container/web").SoftwareSystem = "blog"
		}, "Container/web", `softwareSystemRef names "blog"`},
		{"a Relationship to nothing", func(p *Plan, _ *CurrentModel) {
			object(p, "Relationship/api-to-db").Target = Ref{KindDataStore, "cache"}
		}, "Relationship/api-to-db", "target names DataStore/cache"},
		{"a second Project", func(p *Plan, _ *CurrentModel) {
			p.Objects = append(p.Objects, PlanObject{APIVersion: PlanAPIVersion, Kind: KindProject, Name: "blog"})
		}, "Project/blog", "has Project shop already"},
		{"a Project not named after its namespace", func(p *Plan, c *CurrentModel) {
			c.Objects = nil
			c.Namespace = "orders"
		}, "Project/shop", "a Project is named after its namespace; name it orders"},
		{"two Projects in an empty namespace", func(p *Plan, c *CurrentModel) {
			c.Objects = nil
			p.Objects = append(p.Objects, PlanObject{APIVersion: PlanAPIVersion, Kind: KindProject, Name: "blog"})
		}, "Project/blog", "more than one Project"},
		{"a Container's type changes", func(p *Plan, c *CurrentModel) {
			c.Catalog[0].SupportedTypes = append(c.Catalog[0].SupportedTypes, TypeJob)
			object(p, "Container/api").Type = TypeJob
		}, "Container/api", `type can't change (from "service" to "job")`},
		{"a Container's Framework changes", func(p *Plan, _ *CurrentModel) {
			object(p, "Container/api").Framework = "react-site"
		}, "Container/api", `frameworkRef can't change (from "go-service" to "react-site")`},
		{"a Standard is added to a Container", func(p *Plan, _ *CurrentModel) {
			object(p, "Container/api").Standard = "go-standard"
		}, "Container/api", `standardRef can't change (from "" to "go-standard")`},
		{"an existing DataStore's Connector changes", func(p *Plan, c *CurrentModel) {
			c.Objects = append(c.Objects, PlanObject{Kind: KindDataStore, Name: "db", SoftwareSystem: "shop", Connector: "redis"})
		}, "DataStore/db", `connectorRef can't change (from "redis" to "postgres")`},
		{"an existing Relationship's target changes", func(p *Plan, c *CurrentModel) {
			c.Objects = append(c.Objects, PlanObject{Kind: KindRelationship, Name: "api-to-db",
				Source: Ref{KindContainer, "api"}, Target: Ref{KindSoftwareSystem, "shop"}})
		}, "Relationship/api-to-db", `target can't change (from "SoftwareSystem/shop" to "DataStore/db")`},
		{"a Project's displayName over 100 characters", func(p *Plan, _ *CurrentModel) {
			object(p, "Project/shop").DisplayName = strings.Repeat("x", 101)
		}, "Project/shop", "spec.displayName is 101 characters; it may be at most 100"},
		{"a Person's description over 1000 characters", func(p *Plan, _ *CurrentModel) {
			p.Objects = append(p.Objects, PlanObject{APIVersion: PlanAPIVersion, Kind: KindPerson, Name: "ann",
				Description: strings.Repeat("x", 1001)})
		}, "Person/ann", "spec.description is 1001 characters; it may be at most 1000"},
		{"a description of 1000 characters, some of them two bytes", func(p *Plan, _ *CurrentModel) {
			object(p, "SoftwareSystem/shop").Description = strings.Repeat("é", 1000)
		}, "", ""},
		{"a SoftwareSystem's displayName over 100 characters", func(p *Plan, _ *CurrentModel) {
			object(p, "SoftwareSystem/shop").DisplayName = strings.Repeat("x", 101)
		}, "SoftwareSystem/shop", "spec.displayName is 101 characters"},
		{"a SoftwareSystem's description over 1000 characters", func(p *Plan, _ *CurrentModel) {
			object(p, "SoftwareSystem/shop").Description = strings.Repeat("x", 1001)
		}, "SoftwareSystem/shop", "spec.description is 1001 characters"},
		{"a SoftwareSystem with more than 20 needs", func(p *Plan, _ *CurrentModel) {
			object(p, "SoftwareSystem/shop").Needs = make([]string, 21)
		}, "SoftwareSystem/shop", "spec.needs has 21 items; it may have at most 20"},
		{"a SoftwareSystem with more than 20 kpis", func(p *Plan, _ *CurrentModel) {
			object(p, "SoftwareSystem/shop").KPIs = make([]string, 21)
		}, "SoftwareSystem/shop", "spec.kpis has 21 items; it may have at most 20"},
		{"#1150: a new External SoftwareSystem", func(p *Plan, _ *CurrentModel) {
			p.Objects = append(p.Objects, payments(LocationExternal))
		}, "", ""},
		{"#1150: a SoftwareSystem location that is neither", func(p *Plan, _ *CurrentModel) {
			object(p, "SoftwareSystem/shop").Location = "Outside"
		}, "SoftwareSystem/shop", `location "Outside" is not a SoftwareSystem location: write Internal or External`},
		{"#1150: a new Container in a new External system", func(p *Plan, _ *CurrentModel) {
			p.Objects = append(p.Objects, payments(LocationExternal))
			object(p, "Container/web").SoftwareSystem = "payments"
		}, "Container/web", `softwareSystemRef names "payments", which is External`},
		{"#1150: a new DataStore in a new External system", func(p *Plan, _ *CurrentModel) {
			p.Objects = append(p.Objects, payments(LocationExternal))
			object(p, "DataStore/db").SoftwareSystem = "payments"
		}, "DataStore/db", `softwareSystemRef names "payments", which is External: the factory builds nothing`},
		{"#1150: a new Container in an External system the plan calls Internal", func(p *Plan, c *CurrentModel) {
			p.Objects = append(p.Objects, payments(LocationInternal))
			c.Objects = append(c.Objects, payments(LocationExternal))
			c.Objects[len(c.Objects)-1].APIVersion = ""
			object(p, "Container/web").SoftwareSystem = "payments"
		}, "Container/web", `softwareSystemRef names "payments", which is External`},
		{"#1150: a later round calling an External system Internal", func(p *Plan, c *CurrentModel) {
			p.Objects = append(p.Objects, payments(LocationInternal))
			c.Objects = append(c.Objects, payments(LocationExternal))
			c.Objects[len(c.Objects)-1].APIVersion = ""
		}, "", ""},
		{"a new Container's displayName over 100 characters", func(p *Plan, _ *CurrentModel) {
			object(p, "Container/web").DisplayName = strings.Repeat("x", 101)
		}, "Container/web", "spec.displayName is 101 characters"},
		{"an existing Container's description over 1000 characters", func(p *Plan, _ *CurrentModel) {
			object(p, "Container/api").Description = strings.Repeat("x", 1001)
		}, "Container/api", "spec.description is 1001 characters"},
		{"a DataStore's displayName over 100 characters", func(p *Plan, _ *CurrentModel) {
			object(p, "DataStore/db").DisplayName = strings.Repeat("x", 101)
		}, "DataStore/db", "spec.displayName is 101 characters"},
		{"a DataStore's description over 1000 characters", func(p *Plan, _ *CurrentModel) {
			object(p, "DataStore/db").Description = strings.Repeat("x", 1001)
		}, "DataStore/db", "spec.description is 1001 characters"},
		{"a Relationship's description over 200 characters", func(p *Plan, _ *CurrentModel) {
			object(p, "Relationship/api-to-db").Description = strings.Repeat("x", 201)
		}, "Relationship/api-to-db", "spec.description is 201 characters; it may be at most 200"},
		{"a deletion", func(p *Plan, _ *CurrentModel) {
			p.Objects = slicesDelete(p.Objects, "Container/api")
			p.Objects = slicesDelete(p.Objects, "Relationship/api-to-db")
			p.Issues = p.Issues[:1]
		}, "Container/api", "a plan deletes nothing"},
		{"an issue for an unknown Container", func(p *Plan, _ *CurrentModel) {
			p.Issues[1].Container = "mobile"
		}, "plan/issues/02-api.md", `Container "mobile", which is not in the namespace or the plan`},
		{"an issue titled to change AGENTS.md", func(p *Plan, _ *CurrentModel) {
			p.Issues[0].Title = "Add a note to AGENTS.md"
		}, "plan/issues/01-web.md", "names AGENTS.md in its title, a path no change may make"},
		{"an issue whose criteria name a role guide", func(p *Plan, _ *CurrentModel) {
			p.Issues[0].Body = "## Goal\n\nThe reviewer checks orders.\n\n## Acceptance criteria\n\n" +
				"- [ ] `docs/agents/reviewer.md` says to check orders\n"
		}, "plan/issues/01-web.md", "names docs/agents/reviewer.md in its acceptance criteria, a path no change " +
			"may make: `AGENTS.md`, `docs/agents/` and `.c4factory.yaml` are the agents' rules and the platform's " +
			"settings. File no issue for it; say what wasn't planned in plan/summary.md and why in plan/choices.md. " +
			"Notes for the agents go in docs/project-notes.md"},
		{"an issue whose criteria name .c4factory.yaml", func(p *Plan, _ *CurrentModel) {
			p.Issues[1].Body += "- [ ] .c4factory.yaml names the new check.\n"
		}, "plan/issues/02-api.md", "names .c4factory.yaml in its acceptance criteria"},
		{"an issue whose body names AGENTS.md outside its criteria", func(p *Plan, _ *CurrentModel) {
			p.Issues[0].Body = "## Goal\n\nList the orders, as AGENTS.md says.\n\n## Acceptance criteria\n\n" +
				"- [ ] A page lists the orders\n\n## Notes\n\nSee `docs/agents/implementer.md`.\n"
		}, "", ""},
		{"an issue writing the project's notes", func(p *Plan, _ *CurrentModel) {
			p.Issues[0].Title = "Tell the agents how CI runs make check"
			p.Issues[0].Body = "## Goal\n\nA note for the agents.\n\n## Acceptance criteria\n\n" +
				"- [ ] `docs/project-notes.md` says CI runs `make check` with Go 1.24\n"
		}, "", ""},
		{"an issue without acceptance criteria", func(p *Plan, _ *CurrentModel) {
			p.Issues[0].Body = "## Goal\n\nA page lists the orders."
		}, "plan/issues/01-web.md", "MissingAcceptanceCriteria"},
		{"a design idea for a new Container when the Brief has a designer", func(p *Plan, c *CurrentModel) {
			c.Designer = true
			p.Issues[0].Body, p.Issues[0].Labels = "A calm page for orders", []string{LabelDesign}
		}, "", ""},
		{"a design idea for an existing Container whose Crew has a designer", func(p *Plan, c *CurrentModel) {
			c.CrewDesigners = []string{"api"}
			p.Issues[1].Body, p.Issues[1].Labels = "An API people like", []string{LabelDesign}
		}, "", ""},
		{"a design idea for a new Container when the Brief has no designer", func(p *Plan, _ *CurrentModel) {
			p.Issues[0].Body, p.Issues[0].Labels = "A calm page for orders", []string{LabelDesign}
		}, "plan/issues/01-web.md", "web's Crew has no designer"},
		{"a design idea for an existing Container whose Crew has no designer", func(p *Plan, c *CurrentModel) {
			c.Designer = true
			p.Issues[1].Body, p.Issues[1].Labels = "An API people like", []string{LabelDesign}
		}, "plan/issues/02-api.md", "api's Crew has no designer"},
		{"an ordered plan across Containers", func(p *Plan, _ *CurrentModel) {
			p.Issues[0].After = []int{2}
			p.Issues = append(p.Issues, PlanIssue{File: "plan/issues/03-api.md", N: 3, Container: "api",
				Body: p.Issues[0].Body, After: []int{1, 2}})
		}, "", ""},
		{"after: naming no issue of the plan", func(p *Plan, _ *CurrentModel) {
			p.Issues[0].After = []int{7}
		}, "plan/issues/01-web.md", "after: names 7, which is no issue of the plan"},
		{"after: naming the issue itself", func(p *Plan, _ *CurrentModel) {
			p.Issues[0].After = []int{1}
		}, "plan/issues/01-web.md", "after: names 1, the issue itself"},
		{"after: naming a number twice", func(p *Plan, _ *CurrentModel) {
			p.Issues[0].After = []int{2, 2}
		}, "plan/issues/01-web.md", "after: names 2 twice"},
		{"after: naming a number two issues share", func(p *Plan, _ *CurrentModel) {
			p.Issues = append(p.Issues, PlanIssue{File: "plan/issues/02-web.md", N: 2, Container: "web",
				Body: p.Issues[0].Body})
			p.Issues[0].After = []int{2}
		}, "plan/issues/01-web.md", "after: names 2, which 2 issues share"},
		{"after: naming a design idea", func(p *Plan, c *CurrentModel) {
			c.CrewDesigners = []string{"api"}
			p.Issues[1].Body, p.Issues[1].Labels = "An API people like", []string{LabelDesign}
			p.Issues[0].After = []int{2}
		}, "plan/issues/01-web.md", "after: names 2, a design idea"},
		{"a design idea with after:", func(p *Plan, c *CurrentModel) {
			c.Designer = true
			p.Issues[0].Body, p.Issues[0].Labels = "A calm page for orders", []string{LabelDesign}
			p.Issues[0].After = []int{2}
		}, "plan/issues/01-web.md", "is a design idea (status/design) and has after:"},
		{"a person's work, after the agents' work it shows", func(p *Plan, _ *CurrentModel) {
			p.Issues[0].Labels = []string{LabelByHand}
			p.Issues[0].After = []int{2}
		}, "", ""},
		{"a person's work without acceptance criteria", func(p *Plan, _ *CurrentModel) {
			p.Issues[0].Body, p.Issues[0].Labels = "A screenshot of the shop", []string{LabelByHand}
		}, "plan/issues/01-web.md", "MissingAcceptanceCriteria"},
		{"after: naming a person's work", func(p *Plan, _ *CurrentModel) {
			p.Issues[1].Labels = []string{"kind/docs", LabelByHand}
			p.Issues[0].After = []int{2}
		}, "plan/issues/01-web.md", "after: names 2, a person's work (kind/by-hand)"},
		{"after: naming more than 10 issues", func(p *Plan, _ *CurrentModel) {
			p.Issues[0].After = []int{2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}
		}, "plan/issues/01-web.md", "after: names 11 issues; it may name at most 10"},
		{"after: making a cycle", func(p *Plan, _ *CurrentModel) {
			p.Issues = append(p.Issues, PlanIssue{File: "plan/issues/03-api.md", N: 3, Container: "api",
				Body: p.Issues[0].Body, After: []int{1}})
			p.Issues[0].After = []int{2}
			p.Issues[1].After = []int{3}
		}, "plan/issues/01-web.md", "after: makes a cycle (1 after 2 after 3 after 1)"},
		{"empty choices", func(p *Plan, _ *CurrentModel) {
			p.Choices = nil
		}, PlanChoicesFile, "is empty"},
		{"choices with only a heading and empty items", func(p *Plan, _ *CurrentModel) {
			parsed, err := ParsePlan(map[string]string{PlanModelFile: "", PlanChoicesFile: "# Choices\n-\n- [ ]\n*\n"})
			if err != nil {
				t.Fatal(err)
			}
			p.Choices = parsed.Choices
		}, PlanChoicesFile, "is empty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan, err := ParsePlan(happyFiles())
			if err != nil {
				t.Fatal(err)
			}
			current := happyCurrent()
			tt.change(&plan, &current)
			got := CheckPlan(plan, current)
			if tt.subject == "" {
				if got != nil {
					t.Errorf("refused: %v", got)
				}
				return
			}
			reason := strings.Replace(tt.reason, "20+", "21", 1)
			for _, r := range got {
				if r.Subject == tt.subject && strings.Contains(r.Reason, reason) {
					return
				}
			}
			t.Errorf("got %v, want %s: ...%s...", got, tt.subject, reason)
		})
	}
}

func TestParsePlanAfter(t *testing.T) {
	files := happyFiles()
	files["plan/issues/01-web.md"] = strings.Replace(readyIssue, "labels: [kind/feature]\n",
		"labels: [kind/feature]\nafter: [2]\n", 1)
	plan, err := ParsePlan(files)
	if err != nil {
		t.Fatal(err)
	}
	if got := plan.Issues[0].After; !reflect.DeepEqual(got, []int{2}) {
		t.Errorf("after = %v, want [2]", got)
	}
	if got := plan.Issues[1].After; got != nil {
		t.Errorf("an issue without after: has %v", got)
	}
	files["plan/issues/01-web.md"] = strings.Replace(readyIssue, "labels: [kind/feature]\n",
		"labels: [kind/feature]\nafter: [core]\n", 1)
	if _, err := ParsePlan(files); err == nil {
		t.Error("after: [core] parsed; want an error")
	}
	// Numbers are read base 10 like the file names, not as YAML octal.
	files["plan/issues/01-web.md"] = strings.Replace(readyIssue, "labels: [kind/feature]\n",
		"labels: [kind/feature]\nafter: [02, 08, 010]\n", 1)
	plan, err = ParsePlan(files)
	if err != nil {
		t.Fatal(err)
	}
	if got := plan.Issues[0].After; !reflect.DeepEqual(got, []int{2, 8, 10}) {
		t.Errorf("after = %v, want [2 8 10]", got)
	}
}

func TestFilingOrder(t *testing.T) {
	issue := func(n int, container string, after ...int) PlanIssue {
		return PlanIssue{N: n, Container: container, After: after}
	}
	tests := []struct {
		name   string
		issues []PlanIssue
		want   []int
	}{
		{"no order keeps the numbers", []PlanIssue{issue(1, "web"), issue(2, "api"), issue(3, "web")}, []int{1, 2, 3}},
		{"a blocker in another Container first", []PlanIssue{issue(1, "core"), issue(2, "cli", 3), issue(3, "core")},
			[]int{1, 3, 2}},
		{"a chain across Containers", []PlanIssue{issue(1, "cli", 2), issue(2, "core", 4), issue(3, "web"),
			issue(4, "core")}, []int{3, 4, 2, 1}},
		{"several blockers", []PlanIssue{issue(1, "web", 2, 3), issue(2, "api"), issue(3, "core", 2)},
			[]int{2, 3, 1}},
		{"unsorted input", []PlanIssue{issue(3, "cli", 1), issue(2, "core"), issue(1, "core")}, []int{1, 2, 3}},
		{"a cycle comes last", []PlanIssue{issue(1, "web", 2), issue(2, "api", 1), issue(3, "api")}, []int{3, 1, 2}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FilingOrder(tt.issues)
			ns := make([]int, len(got))
			for i, issue := range got {
				ns[i] = issue.N
			}
			if !reflect.DeepEqual(ns, tt.want) {
				t.Fatalf("order %v, want %v", ns, tt.want)
			}
			if tt.name == "a cycle comes last" {
				return
			}
			pos := map[int]int{}
			for i, n := range ns {
				pos[n] = i
			}
			for _, issue := range tt.issues {
				for _, m := range issue.After {
					if pos[m] > pos[issue.N] {
						t.Errorf("%d (%s) filed before its blocker %d", issue.N, issue.Container, m)
					}
				}
			}
		})
	}
}

func slicesDelete(objects []PlanObject, ref string) []PlanObject {
	var out []PlanObject
	for _, o := range objects {
		if o.Ref().String() != ref {
			out = append(out, o)
		}
	}
	return out
}

func TestPlanDiff(t *testing.T) {
	plan, err := ParsePlan(happyFiles())
	if err != nil {
		t.Fatal(err)
	}
	plan.Objects[2].Description = "Serves orders" // Container/api
	created, changed := PlanDiff(plan, happyCurrent())
	refs := func(objects []PlanObject) (out []string) {
		for _, o := range objects {
			out = append(out, o.Ref().String())
		}
		return out
	}
	if want := []string{"Container/web", "DataStore/db", "Relationship/api-to-db"}; !reflect.DeepEqual(refs(created), want) {
		t.Errorf("created %v, want %v", refs(created), want)
	}
	if want := []string{"Container/api"}; !reflect.DeepEqual(refs(changed), want) {
		t.Errorf("changed %v, want %v", refs(changed), want)
	}
}

func TestRoundPrompts(t *testing.T) {
	tests := []struct {
		name    string
		prompts []string
		planned int
		want    []string
	}{
		{"the idea's round takes no prompt", nil, 0, nil},
		{"every prompt when none was planned", []string{"a", "b"}, 0, []string{"a", "b"}},
		{"the new ones after the planned", []string{"a", "b", "c"}, 1, []string{"b", "c"}},
		{"none when every prompt was planned", []string{"a", "b"}, 2, nil},
		{"none when planned is past the end", []string{"a"}, 3, nil},
		{"none for a negative count", []string{"a"}, -1, nil},
		{"none of the retired ones", []string{"", "", "c"}, 1, []string{"c"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RoundPrompts(tt.prompts, tt.planned); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}

	// A prompt appended during a round is taken by the next one, never twice:
	// a round records how many prompts it took when it started, and the Brief's
	// promptsPlanned becomes that once the round applies.
	prompts := make([]string, 0, 2)
	prompts = append(prompts, "support VAP files first")
	planned := 0
	round1 := RoundPrompts(prompts, planned)
	prompts = append(prompts, "and CRDs") // appended while round 1 runs
	planned += len(round1)                // round 1 applies
	round2 := RoundPrompts(prompts, planned)
	planned += len(round2)
	if !reflect.DeepEqual(round1, []string{"support VAP files first"}) || !reflect.DeepEqual(round2, []string{"and CRDs"}) ||
		RoundPrompts(prompts, planned) != nil {
		t.Errorf("round 1 %v, round 2 %v, then %v", round1, round2, RoundPrompts(prompts, planned))
	}
}

func TestParsePlanUses(t *testing.T) {
	files := happyFiles()
	files["plan/issues/01-web.md"] = strings.Replace(readyIssue, "labels: [kind/feature]\n",
		"labels: [kind/feature]\nuses: [api, \" core \"]\n", 1)
	plan, err := ParsePlan(files)
	if err != nil {
		t.Fatal(err)
	}
	if got := plan.Issues[0].Uses; !reflect.DeepEqual(got, []string{"api", "core"}) {
		t.Errorf("uses = %q, want [api core]", got)
	}
	if got := plan.Issues[1].Uses; got != nil {
		t.Errorf("an issue without uses: has %v", got)
	}
	files["plan/issues/01-web.md"] = strings.Replace(readyIssue, "labels: [kind/feature]\n",
		"labels: [kind/feature]\nuses: api\n", 1)
	if _, err := ParsePlan(files); err == nil {
		t.Error("uses: api parsed; want an error asking for a list")
	}
}

func TestBriefIssueBodyUsing(t *testing.T) {
	issue := PlanIssue{N: 3, Body: "## Goal\n\nCheck a file."}
	got := BriefIssueBodyUsing(issue, "cel-lens", 1,
		[]BuildsOn{{Repository: "shop/core", Number: 2, Title: "The checker"}},
		[]UsedPackage{{Ecosystem: EcosystemGo, Name: "c4factory.local/cel-lens/cel-lens-core", Version: "v0.1.0"},
			{Ecosystem: EcosystemNpm, Name: "@c4factory/cel-lens-core", Version: "0.1.0"}})
	want := "## Goal\n\nCheck a file.\n\n" +
		"Builds on shop/core#2 (The checker).\n\n" +
		"Uses c4factory.local/cel-lens/cel-lens-core v0.1.0: go get it at that version.\n" +
		"Uses npm package @c4factory/cel-lens-core 0.1.0: npm install --save-exact @c4factory/cel-lens-core@0.1.0.\n\n" +
		"<!-- c4-brief: cel-lens/1/3 -->\n"
	if got != want {
		t.Errorf("body\n%q\nwant\n%q", got, want)
	}
}

// #1233: one line per thing a used release published, with the command the
// app's AGENTS.md has an agent run; the npm scope and the PyPI index are the
// GitProvider's settings, so the line takes them as published.
func TestUsesLine(t *testing.T) {
	index := "https://gitea.example/api/packages/shop/pypi/simple"
	tests := []struct {
		name string
		pkg  UsedPackage
		want string
	}{
		{"Go module", UsedPackage{Ecosystem: EcosystemGo, Name: "c4factory.local/cel-lens/cel-lens-core", Version: "v0.1.0"},
			"Uses c4factory.local/cel-lens/cel-lens-core v0.1.0: go get it at that version."},
		{"Go module, ecosystem unset", UsedPackage{Name: "c4factory.local/cel-lens/cel-lens-core", Version: "v0.1.0"},
			"Uses c4factory.local/cel-lens/cel-lens-core v0.1.0: go get it at that version."},
		{"npm, the default scope", UsedPackage{Ecosystem: EcosystemNpm, Name: "@c4factory/cel-lens-core", Version: "0.1.0"},
			"Uses npm package @c4factory/cel-lens-core 0.1.0: npm install --save-exact @c4factory/cel-lens-core@0.1.0."},
		{"npm, a custom scope", UsedPackage{Ecosystem: EcosystemNpm, Name: "@acme/cel-lens-core", Version: "0.1.0"},
			"Uses npm package @acme/cel-lens-core 0.1.0: npm install --save-exact @acme/cel-lens-core@0.1.0."},
		{"PyPI, with its index", UsedPackage{Ecosystem: EcosystemPyPI, Name: "cel-lens-core", Version: "0.1.0", Index: index},
			"Uses PyPI package cel-lens-core 0.1.0 from the c4factory index " + index +
				": uv add cel-lens-core==0.1.0, pinned to that index as AGENTS.md says."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := UsesLine(tt.pkg); got != tt.want {
				t.Errorf("UsesLine\n%q\nwant\n%q", got, tt.want)
			}
		})
	}
}

// #1233: a settled release that published nothing keeps the issue waiting,
// and the message says why on both sides.
func TestUsesNothingPublished(t *testing.T) {
	got := UsesNothingPublished("core", "v0.1.0", "v0.1.0 has no go.mod at the repository's root",
		"v0.1.0 attached no npm tarball, wheel or sdist; no package to publish")
	want := "core, whose release v0.1.0 published nothing other apps install: v0.1.0 has no go.mod at the " +
		"repository's root; v0.1.0 attached no npm tarball, wheel or sdist; no package to publish"
	if got != want {
		t.Errorf("message\n%q\nwant\n%q", got, want)
	}
}

func TestBriefIssueBodyBuildsOn(t *testing.T) {
	issue := PlanIssue{N: 3, Body: "## Goal\n\nCheck a file."}
	got := BriefIssueBody(issue, "cel-lens", 1,
		BuildsOn{Repository: "shop/core", Number: 2, Title: "The checker"},
		BuildsOn{Repository: "shop/cli", Number: 1, Title: "A <!-- odd\ntitle"})
	want := "## Goal\n\nCheck a file.\n\n" +
		"Builds on shop/core#2 (The checker).\n" +
		"Builds on shop/cli#1 (A &lt;!-- odd title).\n\n" +
		"<!-- c4-brief: cel-lens/1/3 -->\n"
	if got != want {
		t.Errorf("body\n%q\nwant\n%q", got, want)
	}
	if !HasIssueMarker(got, "cel-lens", 1, 3) {
		t.Error("no marker")
	}
	if got := BriefIssueBody(issue, "cel-lens", 1); got != "## Goal\n\nCheck a file.\n\n<!-- c4-brief: cel-lens/1/3 -->\n" {
		t.Errorf("without blockers %q", got)
	}
}

func TestIssueMarker(t *testing.T) {
	if got := IssueMarker("cel-lens", 2, 3); got != "<!-- c4-brief: cel-lens/2/3 -->" {
		t.Errorf("marker %q", got)
	}
	issue := PlanIssue{N: 3, Body: "## Goal\n\n<!-- c4-brief: cel-lens/2/4 -->\n\nx"}
	body := BriefIssueBody(issue, "cel-lens", 2)
	tests := []struct {
		name   string
		body   string
		brief  string
		round  int
		n      int
		wanted bool
	}{
		{"the body it wrote", body, "cel-lens", 2, 3, true},
		{"another number", body, "cel-lens", 2, 4, false},
		{"another round", body, "cel-lens", 1, 3, false},
		{"another Brief", body, "cel", 2, 3, false},
		{"inside a line", "see <!-- c4-brief: cel-lens/2/3 --> here", "cel-lens", 2, 3, false},
		{"with spaces around", "x\n  <!-- c4-brief: cel-lens/2/3 -->  \n", "cel-lens", 2, 3, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HasIssueMarker(tt.body, tt.brief, tt.round, tt.n); got != tt.wanted {
				t.Errorf("got %v, want %v in %q", got, tt.wanted, tt.body)
			}
		})
	}
}

// TestSpecLimitsMatchTheCRDs holds the plan check's limits to the CRDs' own,
// both ways, so a marker changed in api/v1beta1 can't drift from them (#382).
func TestSpecLimitsMatchTheCRDs(t *testing.T) {
	plural := map[string]string{KindProject: "projects", KindPerson: "people", KindSoftwareSystem: "softwaresystems",
		KindContainer: "containers", KindDataStore: "datastores", KindRelationship: "relationships"}
	for _, kind := range planKinds {
		t.Run(kind, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("..", "..", "config", "crd", "bases", "c4.factory.io_"+plural[kind]+".yaml"))
			if err != nil {
				t.Fatal(err)
			}
			var crd struct {
				Spec struct {
					Versions []struct {
						Schema struct {
							OpenAPIV3Schema struct {
								Properties struct {
									Spec struct {
										Properties map[string]struct {
											MaxLength *int `yaml:"maxLength"`
											MaxItems  *int `yaml:"maxItems"`
										} `yaml:"properties"`
									} `yaml:"spec"`
								} `yaml:"properties"`
							} `yaml:"openAPIV3Schema"`
						} `yaml:"schema"`
					} `yaml:"versions"`
				} `yaml:"spec"`
			}
			if err := yaml.Unmarshal(data, &crd); err != nil {
				t.Fatal(err)
			}
			// Fields a plan never writes: a Brief names no deploy target
			// (docs/plans/done/deploy.md, decision 2).
			unplanned := map[string]bool{"deploy": kind == KindContainer}
			want := map[string]string{}
			for field, schema := range crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties.Spec.Properties {
				switch {
				case unplanned[field]:
				case schema.MaxLength != nil:
					want[field] = fmt.Sprintf("%d characters", *schema.MaxLength)
				case schema.MaxItems != nil:
					want[field] = fmt.Sprintf("%d items", *schema.MaxItems)
				}
			}
			got := map[string]string{}
			for _, limit := range specLimits(kind) {
				got[limit.field] = fmt.Sprintf("%d characters", limit.max)
				if limit.items {
					got[limit.field] = fmt.Sprintf("%d items", limit.max)
				}
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("the plan check's limits are %v, the CRD's %v", got, want)
			}
		})
	}
}

func TestPromptsToRetire(t *testing.T) {
	tests := []struct {
		name                     string
		retired, listed, planned int
		want                     int
	}{
		{"none while ten or fewer are planned", 0, 12, KeptPlannedPrompts, 0},
		{"the planned past the newest ten", 0, 50, 49, 39},
		{"none retired twice", 39, 11, 49, 0},
		{"the planned past the newest ten after an earlier retirement", 39, 15, 54, 5},
		{"never more than are listed", 0, 3, 20, 3},
		{"none for a planned count behind the retired", 30, 5, 10, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PromptsToRetire(tt.retired, tt.listed, tt.planned); got != tt.want {
				t.Errorf("got %d, want %d", got, tt.want)
			}
		})
	}
}

func TestPlacedPrompts(t *testing.T) {
	if got, want := PlacedPrompts(2, []string{"c", "d"}), []string{"", "", "c", "d"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
	if got := PlacedPrompts(0, nil); len(got) != 0 {
		t.Errorf("got %q, want none", got)
	}
}

// webOnBundle rewrites happyFiles' Container web onto the Bundle react-web
// (react-site and web-standard) in place of its sides (#1622).
func webOnBundle(modelFile string) string {
	return strings.Replace(modelFile, "  frameworkRef: {name: react-site}\n  standardRef: {name: web-standard}\n",
		"  bundleRef: {name: react-web}\n", 1)
}
