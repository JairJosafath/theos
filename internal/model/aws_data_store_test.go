package model

import (
	"fmt"
	"strings"
	"testing"
)

// dynamodbDataStore is the DataStore on the dynamodb Connector and the
// Relationship to it that the planner adds to an AWS Container that keeps
// data (plans/aws-data-stores.md, step 5, #1743).
func dynamodbDataStore(system, container string) string {
	return fmt.Sprintf(`---
apiVersion: c4.factory.io/v1alpha1
kind: DataStore
metadata:
  name: %[2]s-table
spec:
  softwareSystemRef: {name: %[1]s}
  description: What %[2]s keeps
  connectorRef: {name: dynamodb}
---
apiVersion: c4.factory.io/v1alpha1
kind: Relationship
metadata:
  name: %[2]s-keeps-data
spec:
  source: {kind: Container, name: %[2]s}
  target: {kind: DataStore, name: %[2]s-table}
  description: Keeps its items
`, system, container)
}

// Step 5 of plans/aws-data-stores.md (#1743): once every AWS Framework with a
// Lambda or a task wires the dynamodb Connector, the planner plans a DataStore
// on it for an AWS Container that keeps data, and still none for a static site.
func TestPlannerGuidePlansADynamoDBDataStoreForAnAWSContainer(t *testing.T) {
	guide := strings.Join(strings.Fields(plannerGuideText), " ")
	for _, rule := range []string{
		"An AWS Container that keeps data (orders, notes, anything it reads back later) gets one DataStore on the `dynamodb` Connector and a Relationship from the Container to it",
		"Plan one per Container: two DataStores on `dynamodb` for one Container are one table",
		"The `dynamodb` Connector goes only on the `aws-*` Frameworks (`aws-lambda-ts`, `aws-lambda-py`, `aws-lambda-go`, `aws-fargate-ts`, `aws-ec2-ts`, `aws-queue-ts`, `aws-event-ts` and `aws-schedule-ts`)",
		"a Framework that doesn't wire it fails the Container",
		"plan no DataStore for a `static-site` on `aws-static-site-ts`",
	} {
		if !strings.Contains(guide, rule) {
			t.Errorf("PLANNER.md doesn't say %q", rule)
		}
	}
	for _, stale := range []string{"plan no DataStore for it", "and no DataStore", "the table are in its CDK app"} {
		if strings.Contains(guide, stale) {
			t.Errorf("PLANNER.md still says %q, which told the planner to plan no DataStore for an AWS Container", stale)
		}
	}
}

// A Brief plan with an AWS Container and a dynamodb DataStore validates on
// every AWS Framework the Connector supports, and not on aws-static-site-ts,
// whose type the Connector doesn't support (#1743).
func TestAWSPlansWithADynamoDBDataStoreValidate(t *testing.T) {
	check := func(t *testing.T, namespace, model string) []Refusal {
		t.Helper()
		current := CurrentModel{Namespace: namespace, Catalog: chartCatalog(t), GitProvider: "gitea"}
		plan, err := ParsePlan(map[string]string{
			PlanModelFile:   model,
			PlanChoicesFile: "- the Container keeps its data in a DataStore on dynamodb\n",
			PlanSummaryFile: "Planned an AWS Container with a table.\n",
		})
		if err != nil {
			t.Fatal(err)
		}
		return CheckPlan(plan, current)
	}
	for name, tc := range map[string]struct {
		plan, namespace, container string
	}{
		"aws-lambda-ts":   {functionPlan, "notes", "notes-api"},
		"aws-lambda-go":   {strings.Replace(functionPlan, "aws-lambda-ts", "aws-lambda-go", 1), "notes", "notes-api"},
		"aws-lambda-py":   {strings.Replace(functionPlan, "aws-lambda-ts", "aws-lambda-py", 1), "notes", "notes-api"},
		"aws-ec2-ts":      {ec2Plan, "notes", "notes-api"},
		"aws-fargate-ts":  {fargatePlan, "shop", "orders-api"},
		"aws-queue-ts":    {queuePlan, "shop", "stock-updates"},
		"aws-event-ts":    {eventPlan, "shop", "order-events"},
		"aws-schedule-ts": {schedulePlan, "shop", "nightly-report"},
	} {
		t.Run(name, func(t *testing.T) {
			model := tc.plan + dynamodbDataStore(tc.namespace, tc.container)
			if got := check(t, tc.namespace, model); got != nil {
				t.Errorf("the %s plan with a dynamodb DataStore is refused: %v", name, got)
			}
		})
	}
	t.Run("aws-static-site-ts", func(t *testing.T) {
		got := check(t, "bakery", staticSitePlan+dynamodbDataStore("bakery", "bakery-site"))
		found := false
		for _, r := range got {
			found = found || strings.Contains(r.Reason, "Connector dynamodb does not support static-site")
		}
		if !found {
			t.Errorf("the static site plan with a dynamodb DataStore is not refused for the type: %v", got)
		}
	})
}
