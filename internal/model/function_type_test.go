package model

import (
	"slices"
	"strings"
	"testing"
)

// functionPlan is a planner's plan for a serverless API on AWS: a function on
// aws-lambda-ts with aws-standard, and no Compute or Deployment.
const functionPlan = `apiVersion: c4.factory.io/v1beta1
kind: SoftwareSystem
metadata:
  name: notes
spec:
  description: Keeps notes
---
apiVersion: c4.factory.io/v1beta1
kind: Container
metadata:
  name: notes-api
spec:
  softwareSystemRef: {name: notes}
  description: Stores and returns notes over HTTP, on AWS Lambda
  type: function
  gitProviderRef: {name: gitea}
  frameworkRef: {name: aws-lambda-ts}
  standardRef: {name: aws-standard}
`

// The function type's first templates (plans/done/aws.md, step 2): only the AWS
// layers support it in the chart, the dynamodb Connector (#1117) among them,
// and gitea-release, which ships its assembly (#1607).
func TestFunctionTypeIsSupportedByTheAWSLayersOnly(t *testing.T) {
	if !slices.Contains(containerTypes, TypeFunction) {
		t.Errorf("containerTypes = %v, want it to hold %q", containerTypes, TypeFunction)
	}
	var got []string
	for _, e := range chartCatalog(t) {
		if slices.Contains(e.SupportedTypes, TypeFunction) {
			got = append(got, e.Side+" "+e.Name)
		}
	}
	want := []string{"Framework aws-lambda-ts", "Framework aws-lambda-go", "Framework aws-lambda-py", "Standard aws-standard", "Deployment gitea-release", "Connector dynamodb"}
	if !slices.Equal(got, want) {
		t.Errorf("chart entries supporting %q = %v, want %v", TypeFunction, got, want)
	}
}

func TestPlannerGuideStatesTheFunctionCue(t *testing.T) {
	guide := strings.Join(strings.Fields(plannerGuideText), " ")
	for _, rule := range []string{
		"An idea that asks for AWS Lambda, or serverless on AWS, is a `function`",
		"and the plan check refuses either ref)",
		"such as `aws-lambda-ts` (TypeScript), `aws-lambda-go` (Go: pick it when the idea asks for Go)",
		"or `aws-lambda-py` (Python: pick it when the idea asks for Python)",
		"it takes no `computeRef` and no `deploymentRef`",
	} {
		if !strings.Contains(guide, rule) {
			t.Errorf("PLANNER.md doesn't say %q", rule)
		}
	}
}

func TestFunctionPlansOnTheAWSLambdas(t *testing.T) {
	current := CurrentModel{Namespace: "notes", Catalog: chartCatalog(t), GitProvider: "gitea"}
	files := map[string]string{
		PlanModelFile:   functionPlan,
		PlanChoicesFile: "- notes-api runs on AWS Lambda, so it is a function on aws-lambda-ts\n",
		PlanSummaryFile: "Planned a notes API on AWS Lambda.\n",
	}
	check := func(model string) []Refusal {
		files[PlanModelFile] = model
		plan, err := ParsePlan(files)
		if err != nil {
			t.Fatal(err)
		}
		return CheckPlan(plan, current)
	}
	if got := check(functionPlan); got != nil {
		t.Fatalf("the function plan is refused: %v", got)
	}
	for _, framework := range []string{"aws-lambda-go", "aws-lambda-py"} {
		if got := check(strings.Replace(functionPlan, "aws-lambda-ts", framework, 1)); got != nil {
			t.Errorf("the function plan on %s is refused: %v", framework, got)
		}
	}
	// A Deployment or Compute for the cluster doesn't fit a function, and
	// the AWS Frameworks build functions only.
	for name, model := range map[string]string{
		"k8s-manifests": functionPlan + "  deploymentRef: {name: k8s-manifests}\n",
		"small":         functionPlan + "  computeRef: {name: small}\n",
		"a service":     strings.Replace(functionPlan, "type: function", "type: service", 1),
	} {
		if check(model) == nil {
			t.Errorf("the function plan with %s passes the plan check", name)
		}
	}
}
