package model

import (
	"slices"
	"strings"
	"testing"
)

// queuePlan is a planner's plan for a queue consumer on AWS: a worker on
// aws-queue-ts with aws-standard, and no Compute or Deployment
// (plans/aws-production.md, step 8).
const queuePlan = `apiVersion: theos.factory.io/v1beta1
kind: SoftwareSystem
metadata:
  name: shop
spec:
  description: Sells lamps
---
apiVersion: theos.factory.io/v1beta1
kind: Container
metadata:
  name: stock-updates
spec:
  softwareSystemRef: {name: shop}
  description: Consumes stock changes from an SQS queue and keeps the stock levels
  type: worker
  gitProviderRef: {name: gitea}
  frameworkRef: {name: aws-queue-ts}
  standardRef: {name: aws-standard}
`

func TestAWSQueueTSIsAWorkerFrameworkOfAWSStandard(t *testing.T) {
	var queue, standard *CatalogEntry
	entries := chartCatalog(t)
	for i := range entries {
		switch entries[i].Side + " " + entries[i].Name {
		case "Framework aws-queue-ts":
			queue = &entries[i]
		case "Standard aws-standard":
			standard = &entries[i]
		}
	}
	if queue == nil || standard == nil {
		t.Fatalf("the chart lacks aws-queue-ts (%v) or aws-standard (%v)", queue != nil, standard != nil)
	}
	if !slices.Equal(queue.SupportedTypes, []string{TypeWorker}) {
		t.Errorf("aws-queue-ts supports %v, want [%s]", queue.SupportedTypes, TypeWorker)
	}
	if !queue.DeploysItself {
		t.Error("aws-queue-ts doesn't deploy itself; its CDK app is its deployment")
	}
	if !slices.Contains(standard.SupportedTypes, TypeWorker) {
		t.Errorf("aws-standard supports %v, want worker among them", standard.SupportedTypes)
	}
	if !slices.Contains(standard.SupportedFrameworks, "aws-queue-ts") {
		t.Errorf("aws-standard supports Frameworks %v, want aws-queue-ts among them", standard.SupportedFrameworks)
	}
}

func TestPlannerGuideStatesTheAWSQueueCue(t *testing.T) {
	guide := strings.Join(strings.Fields(plannerGuideText), " ")
	for _, rule := range []string{
		"An idea that consumes a queue on AWS, names SQS, or works through messages in the background on AWS is a `worker`",
		"such as `aws-queue-ts`",
		"A `service`, `static-site`, `worker` or `job` that names no AWS runs in the cluster",
	} {
		if !strings.Contains(guide, rule) {
			t.Errorf("PLANNER.md doesn't say %q", rule)
		}
	}
}

func TestWorkerPlansOnAWSQueueTS(t *testing.T) {
	current := CurrentModel{Namespace: "shop", Catalog: chartCatalog(t), GitProvider: "gitea"}
	files := map[string]string{
		PlanChoicesFile: "- stock-updates consumes an SQS queue, so it is a worker on aws-queue-ts\n",
		PlanSummaryFile: "Planned a stock consumer on SQS.\n",
	}
	check := func(model string) []Refusal {
		files[PlanModelFile] = model
		plan, err := ParsePlan(files)
		if err != nil {
			t.Fatal(err)
		}
		return CheckPlan(plan, current)
	}
	if got := check(queuePlan); got != nil {
		t.Fatalf("the queue plan is refused: %v", got)
	}
	for name, c := range map[string]struct{ model, names string }{
		"a function": {strings.Replace(queuePlan, "type: worker", "type: function", 1), "aws-queue-ts"},
		"a Compute":  {queuePlan + "  computeRef: {name: small}\n", "aws-queue-ts deploys itself"},
		"a Deployment": {queuePlan + "  deploymentRef: {name: k8s-manifests}\n",
			"aws-queue-ts deploys itself"},
		"ts-standard": {strings.Replace(queuePlan, "standardRef: {name: aws-standard}",
			"standardRef: {name: ts-standard}", 1), "ts-standard"},
	} {
		refusals := check(c.model)
		if !slices.ContainsFunc(refusals, func(r Refusal) bool { return strings.Contains(r.Reason, c.names) }) {
			t.Errorf("the queue plan with %s: refusals %v, want one naming %s", name, refusals, c.names)
		}
	}
}
