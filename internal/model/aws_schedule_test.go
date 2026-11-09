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
	"slices"
	"strings"
	"testing"
)

// schedulePlan is a planner's plan for a scheduled job on AWS: a job on
// aws-schedule-ts with aws-standard, and no Compute or Deployment.
const schedulePlan = `apiVersion: theos.factory.io/v1beta1
kind: SoftwareSystem
metadata:
  name: shop
spec:
  description: Sells lamps
---
apiVersion: theos.factory.io/v1beta1
kind: Container
metadata:
  name: nightly-report
spec:
  softwareSystemRef: {name: shop}
  description: Every night at three, sums the day's sales on an EventBridge Scheduler schedule
  type: job
  gitProviderRef: {name: gitea}
  frameworkRef: {name: aws-schedule-ts}
  standardRef: {name: aws-standard}
`

func TestAWSScheduleTSIsAJobFrameworkOfAWSStandard(t *testing.T) {
	var schedule, standard *CatalogEntry
	entries := chartCatalog(t)
	for i := range entries {
		switch entries[i].Side + " " + entries[i].Name {
		case "Framework aws-schedule-ts":
			schedule = &entries[i]
		case "Standard aws-standard":
			standard = &entries[i]
		}
	}
	if schedule == nil || standard == nil {
		t.Fatalf("the chart lacks aws-schedule-ts (%v) or aws-standard (%v)", schedule != nil, standard != nil)
	}
	if !slices.Equal(schedule.SupportedTypes, []string{TypeJob}) {
		t.Errorf("aws-schedule-ts supports %v, want [%s]", schedule.SupportedTypes, TypeJob)
	}
	if !schedule.DeploysItself {
		t.Error("aws-schedule-ts doesn't deploy itself; its CDK app is its deployment")
	}
	if !slices.Contains(standard.SupportedTypes, TypeJob) {
		t.Errorf("aws-standard supports %v, want job among them", standard.SupportedTypes)
	}
	if !slices.Contains(standard.SupportedFrameworks, "aws-schedule-ts") {
		t.Errorf("aws-standard supports Frameworks %v, want aws-schedule-ts among them", standard.SupportedFrameworks)
	}
}

func TestPlannerGuideStatesTheAWSScheduleCue(t *testing.T) {
	guide := strings.Join(strings.Fields(plannerGuideText), " ")
	for _, rule := range []string{
		"An idea that runs on a schedule on AWS (every night, every hour, a cron, EventBridge Scheduler) is a `job`",
		"such as `aws-schedule-ts`",
		"since the schedule is in its CDK app",
		"A `service`, `static-site`, `worker` or `job` that names no AWS runs in the cluster",
	} {
		if !strings.Contains(guide, rule) {
			t.Errorf("PLANNER.md doesn't say %q", rule)
		}
	}
}

func TestJobPlansOnAWSScheduleTS(t *testing.T) {
	current := CurrentModel{Namespace: "shop", Catalog: chartCatalog(t), GitProvider: "gitea"}
	files := map[string]string{
		PlanChoicesFile: "- nightly-report runs every night on AWS, so it is a job on aws-schedule-ts\n",
		PlanSummaryFile: "Planned a nightly sales report on EventBridge Scheduler.\n",
	}
	check := func(model string) []Refusal {
		files[PlanModelFile] = model
		plan, err := ParsePlan(files)
		if err != nil {
			t.Fatal(err)
		}
		return CheckPlan(plan, current)
	}
	if got := check(schedulePlan); got != nil {
		t.Fatalf("the schedule plan is refused: %v", got)
	}
	for name, c := range map[string]struct{ model, names string }{
		"a function": {strings.Replace(schedulePlan, "type: job", "type: function", 1), "aws-schedule-ts"},
		"a Compute":  {schedulePlan + "  computeRef: {name: small}\n", "aws-schedule-ts deploys itself"},
		"a Deployment": {schedulePlan + "  deploymentRef: {name: k8s-manifests}\n",
			"aws-schedule-ts deploys itself"},
		"ts-standard": {strings.Replace(schedulePlan, "standardRef: {name: aws-standard}",
			"standardRef: {name: ts-standard}", 1), "ts-standard"},
	} {
		refusals := check(c.model)
		if !slices.ContainsFunc(refusals, func(r Refusal) bool { return strings.Contains(r.Reason, c.names) }) {
			t.Errorf("the schedule plan with %s: refusals %v, want one naming %s", name, refusals, c.names)
		}
	}
}
