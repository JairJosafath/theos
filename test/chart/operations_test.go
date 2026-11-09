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

package chart

import (
	"os"
	"path/filepath"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// The catalog the seed applies says readsOperations: true on every Framework
// whose template renders infra/operations.ts, and on no other: without it
// the operator reports OperationsApplied=False, NotReadByFramework, on every
// Container on it with the block, and the planner refuses the block (#1703).
func TestSeedCatalogSaysWhichFrameworksReadOperations(t *testing.T) {
	helm := helmBinary(t)
	var frameworks, reading int
	for _, o := range decodeObjects(t, renderCatalog(t.Context(), t, helm, "test").objects) {
		if o.GetKind() != "Framework" {
			continue
		}
		frameworks++
		tmpl := filepath.Join("..", "..", "templates", o.GetName(), "infra", "operations.ts.tmpl")
		_, err := os.Stat(tmpl)
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		want := err == nil
		if want {
			reading++
		}
		got, _, _ := unstructured.NestedBool(o.Object, "spec", "readsOperations")
		if got != want {
			t.Errorf("%s: spec.readsOperations %v in ConfigMap %s, want %v (%s exists: %v)",
				key(o), got, catalogConfigMap, want, tmpl, want)
		}
	}
	if frameworks == 0 || reading == 0 {
		t.Fatalf("ConfigMap %s holds %d Frameworks, %d with an operations template; want some of each",
			catalogConfigMap, frameworks, reading)
	}
}
