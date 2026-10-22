package model

import (
	"slices"
	"strings"
	"testing"
)

// fargatePlan is a planner's plan for a container service on AWS: a service
// on aws-fargate-ts with aws-standard, and no Compute or Deployment.
const fargatePlan = `apiVersion: c4.factory.io/v1alpha1
kind: SoftwareSystem
metadata:
  name: shop
spec:
  description: Sells lamps
---
apiVersion: c4.factory.io/v1alpha1
kind: Container
metadata:
  name: orders-api
spec:
  softwareSystemRef: {name: shop}
  description: Stores and returns orders over HTTPS, on ECS on Fargate
  type: service
  gitProviderRef: {name: gitea}
  frameworkRef: {name: aws-fargate-ts}
  standardRef: {name: aws-standard}
`

// The PaaS Framework (plans/done/aws.md, step 3): aws-fargate-ts builds services,
// and aws-standard fits it and aws-lambda-ts alike.
func TestAWSFargateTSIsAServiceFrameworkOfAWSStandard(t *testing.T) {
	var fargate, standard *CatalogEntry
	entries := chartCatalog(t)
	for i := range entries {
		switch entries[i].Side + " " + entries[i].Name {
		case "Framework aws-fargate-ts":
			fargate = &entries[i]
		case "Standard aws-standard":
			standard = &entries[i]
		}
	}
	if fargate == nil || standard == nil {
		t.Fatalf("the chart lacks aws-fargate-ts (%v) or aws-standard (%v)", fargate != nil, standard != nil)
	}
	if !slices.Equal(fargate.SupportedTypes, []string{TypeService}) {
		t.Errorf("aws-fargate-ts supports %v, want [%s]", fargate.SupportedTypes, TypeService)
	}
	if want := []string{TypeFunction, TypeService, TypeStaticSite, TypeWorker, TypeJob}; !slices.Equal(standard.SupportedTypes, want) {
		t.Errorf("aws-standard supports %v, want %v", standard.SupportedTypes, want)
	}
	if !slices.Contains(standard.SupportedFrameworks, "aws-fargate-ts") {
		t.Errorf("aws-standard supports Frameworks %v, want aws-fargate-ts among them", standard.SupportedFrameworks)
	}
}

func TestPlannerGuideStatesTheAWSServiceCue(t *testing.T) {
	guide := strings.Join(strings.Fields(plannerGuideText), " ")
	for _, rule := range []string{
		"An idea that asks for a container service on AWS, ECS or Fargate is a `service`",
		"such as `aws-fargate-ts`",
		"no `computeRef`, no `deploymentRef` and no DataStore",
	} {
		if !strings.Contains(guide, rule) {
			t.Errorf("PLANNER.md doesn't say %q", rule)
		}
	}
}

func TestServicePlansOnAWSFargateTS(t *testing.T) {
	current := CurrentModel{Namespace: "shop", Catalog: chartCatalog(t), GitProvider: "gitea"}
	files := map[string]string{
		PlanChoicesFile: "- orders-api runs on ECS on Fargate, so it is a service on aws-fargate-ts\n",
		PlanSummaryFile: "Planned an orders API on Fargate.\n",
	}
	check := func(model string) []Refusal {
		files[PlanModelFile] = model
		plan, err := ParsePlan(files)
		if err != nil {
			t.Fatal(err)
		}
		return CheckPlan(plan, current)
	}
	if got := check(fargatePlan); got != nil {
		t.Fatalf("the Fargate plan is refused: %v", got)
	}
	// aws-fargate-ts builds services only, and aws-standard fits AWS
	// Frameworks only: each refusal names the entry that doesn't fit.
	for name, c := range map[string]struct{ model, names string }{
		"a function": {strings.Replace(fargatePlan, "type: service", "type: function", 1), "aws-fargate-ts"},
		"ts-standard": {strings.Replace(fargatePlan, "standardRef: {name: aws-standard}",
			"standardRef: {name: ts-standard}", 1), "ts-standard"},
		"aws-standard on python-fastapi": {strings.Replace(fargatePlan, "frameworkRef: {name: aws-fargate-ts}",
			"frameworkRef: {name: python-fastapi}", 1), "aws-standard"},
	} {
		refusals := check(c.model)
		if !slices.ContainsFunc(refusals, func(r Refusal) bool { return strings.Contains(r.Reason, c.names) }) {
			t.Errorf("the Fargate plan with %s: refusals %v, want one naming %s", name, refusals, c.names)
		}
	}
}

func TestCheckDeploysItself(t *testing.T) {
	fargate := CatalogEntry{Side: SideFramework, Name: "aws-fargate-ts", DeploysItself: true}
	fastapi := CatalogEntry{Side: SideFramework, Name: "python-fastapi"}
	reactVite := CatalogEntry{Side: SideFramework, Name: "react-vite"}
	pythonCLI := CatalogEntry{Side: SideFramework, Name: "python-cli"}
	queue := CatalogEntry{Side: SideFramework, Name: "aws-queue-ts", DeploysItself: true}
	schedule := CatalogEntry{Side: SideFramework, Name: "aws-schedule-ts", DeploysItself: true}
	for name, c := range map[string]struct {
		framework                CatalogEntry
		typ, compute, deployment string
		want                     string
	}{
		"its own deployment":          {fargate, "service", "", "", ""},
		"a Framework that needs":      {fastapi, "service", "small", "k8s-manifests", ""},
		"a Compute":                   {fargate, "service", "small", "", "remove computeRef small"},
		"a Deployment":                {fargate, "service", "", "k8s-manifests", "remove deploymentRef k8s-manifests"},
		"gitea-release (#1607)":       {fargate, "service", "", "gitea-release", ""},
		"a Compute and gitea-release": {fargate, "service", "small", "gitea-release", "remove computeRef small"},
		"gitea-release for a service that doesn't deploy itself (#1607)": {fastapi, "service", "small", "gitea-release",
			"gitea-release ships a service only for a Framework that deploys itself, its cloud assembly; python-fastapi doesn't"},
		"gitea-release for a static site that doesn't deploy itself (#1607)": {reactVite, "static-site", "", "gitea-release",
			"gitea-release ships a static-site only for a Framework that deploys itself"},
		"gitea-release for a CLI":                 {pythonCLI, "cli", "", "gitea-release", ""},
		"gitea-release for an AWS worker (#1773)": {queue, "worker", "", "gitea-release", ""},
		"gitea-release for an AWS job (#1773)":    {schedule, "job", "", "gitea-release", ""},
		"gitea-release for a worker that doesn't deploy itself (#1773)": {pythonCLI, "worker", "small", "gitea-release",
			"gitea-release ships a worker only for a Framework that deploys itself, its cloud assembly; python-cli doesn't"},
		"gitea-release for a job that doesn't deploy itself (#1773)": {pythonCLI, "job", "small", "gitea-release",
			"gitea-release ships a job only for a Framework that deploys itself, its cloud assembly; python-cli doesn't"},
		"both": {fargate, "service", "small", "k8s-manifests",
			"aws-fargate-ts deploys itself: its template's app (on AWS, its CDK app) is the Container's deployment, " +
				"so it takes no Compute and no Deployment but gitea-release, which ships its cloud assembly; remove computeRef small and deploymentRef k8s-manifests"},
	} {
		err := CheckDeploysItself(c.framework, c.typ, c.compute, c.deployment)
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s: %v, want none", name, err)
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
			t.Errorf("%s: %v, want one saying %q", name, err, c.want)
		}
	}
}

// Every AWS Framework is its own deployment (plans/done/aws.md, decision 4).
func TestAWSFrameworksDeployThemselves(t *testing.T) {
	for _, entry := range chartCatalog(t) {
		if entry.Side != SideFramework {
			continue
		}
		if want := strings.HasPrefix(entry.Name, "aws-"); entry.DeploysItself != want {
			t.Errorf("Framework %s: deploysItself %v, want %v", entry.Name, entry.DeploysItself, want)
		}
	}
}

func TestPlanRefusesADeploymentForAWSFargateTS(t *testing.T) {
	current := CurrentModel{Namespace: "shop", Catalog: chartCatalog(t), GitProvider: "gitea"}
	for name, refs := range map[string]string{
		"a Compute":    "  computeRef: {name: small}\n",
		"a Deployment": "  deploymentRef: {name: k8s-manifests}\n",
	} {
		plan, err := ParsePlan(map[string]string{
			PlanModelFile:   fargatePlan + refs,
			PlanChoicesFile: "- orders-api runs on ECS on Fargate, so it is a service on aws-fargate-ts\n",
			PlanSummaryFile: "Planned an orders API on Fargate.\n",
		})
		if err != nil {
			t.Fatal(err)
		}
		refusals := CheckPlan(plan, current)
		if !slices.ContainsFunc(refusals, func(r Refusal) bool {
			return r.Subject == "Container/orders-api" && strings.Contains(r.Reason, "frameworkRef: aws-fargate-ts deploys itself") &&
				strings.Contains(r.Reason, "its CDK app) is the Container's deployment")
		}) {
			t.Errorf("the Fargate plan with %s: refusals %v, want one saying aws-fargate-ts deploys itself", name, refusals)
		}
	}
}

func TestWorkspaceCatalogSaysWhichFrameworksDeployThemselves(t *testing.T) {
	out := workspaceCatalog(chartCatalog(t), nil)
	for _, e := range out.Frameworks {
		if want := strings.HasPrefix(e.Name, "aws-"); e.DeploysItself != want {
			t.Errorf("the workspace catalog's %s: deploysItself %v, want %v", e.Name, e.DeploysItself, want)
		}
	}
}
