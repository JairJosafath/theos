/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of theos, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
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
