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

// ec2Plan is a planner's plan for a service on EC2 instances: aws-ec2-ts with
// aws-standard, and no Compute or Deployment (plans/done/aws.md, step 4).
const ec2Plan = `apiVersion: theos.factory.io/v1beta1
kind: SoftwareSystem
metadata:
  name: notes
spec:
  description: Keeps notes
---
apiVersion: theos.factory.io/v1beta1
kind: Container
metadata:
  name: notes-api
spec:
  softwareSystemRef: {name: notes}
  description: Stores and returns notes over HTTP, on EC2 instances
  type: service
  gitProviderRef: {name: gitea}
  frameworkRef: {name: aws-ec2-ts}
  standardRef: {name: aws-standard}
`

func TestAWSStandardSupportsTheEC2Framework(t *testing.T) {
	for _, e := range chartCatalog(t) {
		if e.Name == "aws-ec2-ts" && !slices.Equal(e.SupportedTypes, []string{TypeService}) {
			t.Errorf("aws-ec2-ts supports %v, want only %q", e.SupportedTypes, TypeService)
		}
		if e.Name == "aws-standard" && !slices.Contains(e.SupportedFrameworks, "aws-ec2-ts") {
			t.Errorf("aws-standard supports Frameworks %v, want aws-ec2-ts among them", e.SupportedFrameworks)
		}
	}
}

func TestPlannerGuideStatesTheEC2Cue(t *testing.T) {
	guide := strings.Join(strings.Fields(plannerGuideText), " ")
	for _, rule := range []string{
		"An idea that asks for EC2, virtual machines or servers you run on AWS is a `service`",
		"such as `aws-ec2-ts`",
	} {
		if !strings.Contains(guide, rule) {
			t.Errorf("PLANNER.md doesn't say %q", rule)
		}
	}
}

func TestServicePlansOnAWSEC2TS(t *testing.T) {
	current := CurrentModel{Namespace: "notes", Catalog: chartCatalog(t), GitProvider: "gitea"}
	files := map[string]string{
		PlanChoicesFile: "- notes-api runs on EC2, so it is a service on aws-ec2-ts\n",
		PlanSummaryFile: "Planned a notes API on EC2.\n",
	}
	check := func(model string) []Refusal {
		files[PlanModelFile] = model
		plan, err := ParsePlan(files)
		if err != nil {
			t.Fatal(err)
		}
		return CheckPlan(plan, current)
	}
	if got := check(ec2Plan); got != nil {
		t.Fatalf("the EC2 plan is refused: %v", got)
	}
	// aws-ec2-ts builds services only, and aws-standard fits no other
	// service Framework.
	for name, model := range map[string]string{
		"a function": strings.Replace(ec2Plan, "type: service", "type: function", 1),
		"python-fastapi": strings.Replace(ec2Plan, "frameworkRef: {name: aws-ec2-ts}",
			"frameworkRef: {name: python-fastapi}", 1),
	} {
		if check(model) == nil {
			t.Errorf("the EC2 plan with %s passes the plan check", name)
		}
	}
}
