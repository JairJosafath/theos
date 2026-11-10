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
	"os"
	"path/filepath"
	"testing"
)

// templateRendersOperations says the Framework's template directory has
// infra/operations.ts.tmpl, the file a Container's operations block renders to.
func templateRendersOperations(t *testing.T, framework string) bool {
	t.Helper()
	_, err := os.Stat(filepath.Join("..", "..", "templates", framework, "infra", "operations.ts.tmpl"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return err == nil
}

// Every Framework whose template renders infra/operations.ts says so in the
// chart's values, readsOperations: true, and no other does: without it the
// operator reports OperationsApplied=False, NotReadByFramework, on every
// Container with the block, and the planner refuses the block (#1703).
func TestChartFrameworksWithAnOperationsTemplateReadOperations(t *testing.T) {
	var frameworks, reading int
	for _, entry := range chartCatalog(t) {
		if entry.Side != SideFramework {
			continue
		}
		frameworks++
		want := templateRendersOperations(t, entry.Name)
		if want {
			reading++
		}
		if entry.ReadsOperations != want {
			t.Errorf("Framework %s: readsOperations %v in %s, want %v (templates/%s/infra/operations.ts.tmpl exists: %v)",
				entry.Name, entry.ReadsOperations, chartValues, want, entry.Name, want)
		}
	}
	if frameworks == 0 || reading == 0 {
		t.Fatalf("%s lists %d Frameworks, %d with an operations template; want some of each", chartValues, frameworks, reading)
	}
}

// The planner's workspace catalog carries readsOperations through, so a plan
// sets operations only on a Container whose Framework renders it (#1703).
func TestWorkspaceCatalogSaysWhichFrameworksReadOperations(t *testing.T) {
	out := workspaceCatalog(chartCatalog(t), nil)
	for _, e := range out.Frameworks {
		if want := templateRendersOperations(t, e.Name); e.ReadsOperations != want {
			t.Errorf("the workspace catalog's %s: readsOperations %v, want %v", e.Name, e.ReadsOperations, want)
		}
	}
}
