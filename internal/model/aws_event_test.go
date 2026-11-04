package model

import (
	"slices"
	"strings"
	"testing"
)

// eventPlan is a planner's plan for an event consumer on AWS: a worker on
// aws-event-ts with aws-standard, and no Compute or Deployment.
const eventPlan = `apiVersion: theos.factory.io/v1beta1
kind: SoftwareSystem
metadata:
  name: shop
spec:
  description: Sells lamps
---
apiVersion: theos.factory.io/v1beta1
kind: Container
metadata:
  name: order-events
spec:
  softwareSystemRef: {name: shop}
  description: Keeps the orders other services announce as EventBridge events
  type: worker
  gitProviderRef: {name: gitea}
  frameworkRef: {name: aws-event-ts}
  standardRef: {name: aws-standard}
`

func TestAWSEventTSIsAWorkerFrameworkOfAWSStandard(t *testing.T) {
	var event, standard *CatalogEntry
	entries := chartCatalog(t)
	for i := range entries {
		switch entries[i].Side + " " + entries[i].Name {
		case "Framework aws-event-ts":
			event = &entries[i]
		case "Standard aws-standard":
			standard = &entries[i]
		}
	}
	if event == nil || standard == nil {
		t.Fatalf("the chart lacks aws-event-ts (%v) or aws-standard (%v)", event != nil, standard != nil)
	}
	if !slices.Equal(event.SupportedTypes, []string{TypeWorker}) {
		t.Errorf("aws-event-ts supports %v, want [%s]", event.SupportedTypes, TypeWorker)
	}
	if !event.DeploysItself {
		t.Error("aws-event-ts doesn't deploy itself; its CDK app is its deployment")
	}
	if !slices.Contains(standard.SupportedTypes, TypeWorker) {
		t.Errorf("aws-standard supports %v, want worker among them", standard.SupportedTypes)
	}
	if !slices.Contains(standard.SupportedFrameworks, "aws-event-ts") {
		t.Errorf("aws-standard supports Frameworks %v, want aws-event-ts among them", standard.SupportedFrameworks)
	}
}

func TestPlannerGuideStatesTheAWSEventCue(t *testing.T) {
	guide := strings.Join(strings.Fields(plannerGuideText), " ")
	for _, rule := range []string{
		"An idea that reacts to events on AWS, names an EventBridge rule or event bus (not a rule on a schedule or EventBridge Scheduler, which is the `job` above), or consumes events another service puts on an event bus is a `worker`",
		"such as `aws-event-ts`",
		"since the rule is in its CDK app",
		"A `service`, `static-site`, `worker` or `job` that names no AWS runs in the cluster",
	} {
		if !strings.Contains(guide, rule) {
			t.Errorf("PLANNER.md doesn't say %q", rule)
		}
	}
}

func TestEventConsumerPlansOnAWSEventTS(t *testing.T) {
	current := CurrentModel{Namespace: "shop", Catalog: chartCatalog(t), GitProvider: "gitea"}
	files := map[string]string{
		PlanChoicesFile: "- order-events consumes EventBridge events on AWS, so it is a worker on aws-event-ts\n",
		PlanSummaryFile: "Planned an order consumer on EventBridge.\n",
	}
	check := func(model string) []Refusal {
		files[PlanModelFile] = model
		plan, err := ParsePlan(files)
		if err != nil {
			t.Fatal(err)
		}
		return CheckPlan(plan, current)
	}
	if got := check(eventPlan); got != nil {
		t.Fatalf("the event plan is refused: %v", got)
	}
	for name, c := range map[string]struct{ model, names string }{
		"a function": {strings.Replace(eventPlan, "type: worker", "type: function", 1), "aws-event-ts"},
		"a Compute":  {eventPlan + "  computeRef: {name: small}\n", "aws-event-ts deploys itself"},
		"a Deployment": {eventPlan + "  deploymentRef: {name: k8s-manifests}\n",
			"aws-event-ts deploys itself"},
		"ts-standard": {strings.Replace(eventPlan, "standardRef: {name: aws-standard}",
			"standardRef: {name: ts-standard}", 1), "ts-standard"},
	} {
		refusals := check(c.model)
		if !slices.ContainsFunc(refusals, func(r Refusal) bool { return strings.Contains(r.Reason, c.names) }) {
			t.Errorf("the event plan with %s: refusals %v, want one naming %s", name, refusals, c.names)
		}
	}
}
