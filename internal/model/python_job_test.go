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

import "testing"

// pythonJobPlan is a planner's plan for a job on Kubernetes: python-cli with
// python-standard, run by k8s-manifests' Job on small.
const pythonJobPlan = `apiVersion: theos.factory.io/v1beta1
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
  description: Sums the day's sales once and exits
  type: job
  gitProviderRef: {name: gitea}
  frameworkRef: {name: python-cli}
  standardRef: {name: python-standard}
  computeRef: {name: small}
  deploymentRef: {name: k8s-manifests}
`

// #1595: k8s-manifests renders a Job, and python-cli is the Framework that
// runs one; the chart's catalog takes a python-cli job with python-standard.
func TestJobPlansOnPythonCLIOnKubernetes(t *testing.T) {
	current := CurrentModel{Namespace: "shop", Catalog: chartCatalog(t), GitProvider: "gitea"}
	plan, err := ParsePlan(map[string]string{
		PlanModelFile:   pythonJobPlan,
		PlanChoicesFile: "- nightly-report runs once a night on Kubernetes, so it is a job on python-cli\n",
		PlanSummaryFile: "Planned a nightly sales report as a Kubernetes Job.\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := CheckPlan(plan, current); got != nil {
		t.Errorf("the python-cli job plan is refused: %v", got)
	}
}
