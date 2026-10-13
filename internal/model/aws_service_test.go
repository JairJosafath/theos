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

// The PaaS Framework (plans/aws.md, step 3): aws-fargate-ts builds services,
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
	if want := []string{TypeFunction, TypeService}; !slices.Equal(standard.SupportedTypes, want) {
		t.Errorf("aws-standard supports %v, want %v", standard.SupportedTypes, want)
	}
	if want := []string{"aws-lambda-ts", "aws-fargate-ts"}; !slices.Equal(standard.SupportedFrameworks, want) {
		t.Errorf("aws-standard supports Frameworks %v, want %v", standard.SupportedFrameworks, want)
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
