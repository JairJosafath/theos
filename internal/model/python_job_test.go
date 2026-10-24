package model

import "testing"

// pythonJobPlan is a planner's plan for a job on Kubernetes: python-cli with
// python-standard, run by k8s-manifests' Job on small.
const pythonJobPlan = `apiVersion: c4.factory.io/v1beta1
kind: SoftwareSystem
metadata:
  name: shop
spec:
  description: Sells lamps
---
apiVersion: c4.factory.io/v1beta1
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
