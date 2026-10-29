package model

import (
	"os"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// chartValues is the Helm chart's values, whose catalog is what a user's
// planner reads.
const chartValues = "../../dist/chart/values.yaml"

// operatorIdea is the idea plans/done/kubebuilder-operators.md, step 5, plans.
const operatorIdea = "an operator that backs up every PVC labelled `backup: nightly`"

// operatorPlan is a planner's plan for operatorIdea: the cue in planner.md
// makes it an operator on go-kubebuilder.
const operatorPlan = `apiVersion: c4.factory.io/v1beta1
kind: SoftwareSystem
metadata:
  name: pvc-backup
spec:
  description: Backs up the cluster's labelled volumes every night
---
apiVersion: c4.factory.io/v1beta1
kind: Container
metadata:
  name: backup-operator
spec:
  softwareSystemRef: {name: pvc-backup}
  description: Watches PVCs labelled backup nightly and backs each one up
  type: operator
  gitProviderRef: {name: gitea}
  frameworkRef: {name: go-kubebuilder}
  standardRef: {name: go-operator-standard}
`

// chartCatalog returns the chart's catalog entries.
func chartCatalog(t *testing.T) []CatalogEntry {
	t.Helper()
	data, err := os.ReadFile(chartValues)
	if err != nil {
		t.Fatal(err)
	}
	var values struct {
		Catalog struct {
			Entries []struct {
				Kind string `yaml:"kind"`
				Name string `yaml:"name"`
				Spec struct {
					Description         string   `yaml:"description"`
					SupportedTypes      []string `yaml:"supportedTypes"`
					SupportedFrameworks []string `yaml:"supportedFrameworks"`
					DeploysItself       bool     `yaml:"deploysItself"`
					ReadsOperations     bool     `yaml:"readsOperations"`
					WiredBy             []string `yaml:"wiredBy"`
				} `yaml:"spec"`
			} `yaml:"entries"`
		} `yaml:"catalog"`
	}
	if err := yaml.Unmarshal(data, &values); err != nil {
		t.Fatalf("%s: %v", chartValues, err)
	}
	entries := make([]CatalogEntry, 0, len(values.Catalog.Entries))
	for _, e := range values.Catalog.Entries {
		entries = append(entries, CatalogEntry{Side: e.Kind, Name: e.Name, Description: e.Spec.Description,
			SupportedTypes: e.Spec.SupportedTypes, SupportedFrameworks: e.Spec.SupportedFrameworks,
			DeploysItself: e.Spec.DeploysItself, ReadsOperations: e.Spec.ReadsOperations, WiredBy: e.Spec.WiredBy})
	}
	return entries
}

func TestPlannerGuideStatesTheOperatorCue(t *testing.T) {
	guide := strings.Join(strings.Fields(plannerGuideText), " ")
	for _, rule := range []string{
		"An idea that extends Kubernetes, or watches and reconciles cluster objects, is an `operator`",
		`"` + operatorIdea + `" is an ` + "`operator`",
		"such as `go-kubebuilder`",
	} {
		if !strings.Contains(guide, rule) {
			t.Errorf("PLANNER.md doesn't say %q", rule)
		}
	}
}

func TestGoKubebuilderDescriptionCarriesTheOperatorCue(t *testing.T) {
	for _, e := range chartCatalog(t) {
		if e.Side != SideFramework || e.Name != "go-kubebuilder" {
			continue
		}
		for _, cue := range []string{"extends Kubernetes", "watches and reconciles cluster objects"} {
			if !strings.Contains(e.Description, cue) {
				t.Errorf("go-kubebuilder's description %q doesn't say %q", e.Description, cue)
			}
		}
		return
	}
	t.Fatalf("%s has no Framework go-kubebuilder", chartValues)
}

func TestOperatorIdeaPlansAnOperatorOnGoKubebuilder(t *testing.T) {
	current := CurrentModel{Namespace: "pvc-backup", Catalog: chartCatalog(t), GitProvider: "gitea"}
	files := map[string]string{
		PlanModelFile:   operatorPlan,
		PlanChoicesFile: "- Backing up PVCs watches and reconciles cluster objects, so backup-operator is an operator on go-kubebuilder\n",
		PlanSummaryFile: "Planned " + operatorIdea + ".\n",
	}
	plan, err := ParsePlan(files)
	if err != nil {
		t.Fatal(err)
	}
	if got := CheckPlan(plan, current); got != nil {
		t.Fatalf("the operator plan is refused: %v", got)
	}

	// The same Container as a job or a service is refused: go-kubebuilder
	// builds operators only.
	for _, kind := range []string{TypeJob, TypeService} {
		files[PlanModelFile] = strings.Replace(operatorPlan, "type: operator", "type: "+kind, 1)
		plan, err := ParsePlan(files)
		if err != nil {
			t.Fatal(err)
		}
		if CheckPlan(plan, current) == nil {
			t.Errorf("a %s on go-kubebuilder passes the plan check", kind)
		}
	}
}

// TestProjectNotesUnprotected pins that any issue may change a project's notes:
// neither the platform, nor the never-paths, nor a Standard of the chart's
// catalog protects them.
func TestProjectNotesUnprotected(t *testing.T) {
	data, err := os.ReadFile(chartValues)
	if err != nil {
		t.Fatal(err)
	}
	var values struct {
		Catalog struct {
			Entries []struct {
				Kind string `yaml:"kind"`
				Name string `yaml:"name"`
				Spec struct {
					ProtectedPaths []string `yaml:"protectedPaths"`
				} `yaml:"spec"`
			} `yaml:"entries"`
		} `yaml:"catalog"`
	}
	if err := yaml.Unmarshal(data, &values); err != nil {
		t.Fatal(err)
	}
	lists := map[string][]string{"AgentNeverPaths": AgentNeverPaths, "PlatformProtectedPaths": PlatformProtectedPaths(giteaActions)}
	standards := 0
	for _, e := range values.Catalog.Entries {
		if e.Kind == "Standard" {
			standards++
			lists["Standard "+e.Name] = e.Spec.ProtectedPaths
		}
	}
	if standards == 0 {
		t.Fatal("the chart's catalog has no Standard")
	}
	for name, protected := range lists {
		if got := TouchedPaths([]string{ProjectNotesPath}, protected); len(got) > 0 {
			t.Errorf("%s protects %s: %v", name, ProjectNotesPath, protected)
		}
	}
}
