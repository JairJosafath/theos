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

package controller

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	utilyaml "k8s.io/apimachinery/pkg/util/yaml"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
)

// crdStorage is the part of a CRD this test reads.
type crdStorage struct {
	Spec struct {
		Group string `json:"group"`
		Names struct {
			Kind string `json:"kind"`
		} `json:"names"`
		Versions []struct {
			Name               string `json:"name"`
			Served             bool   `json:"served"`
			Storage            bool   `json:"storage"`
			Deprecated         bool   `json:"deprecated"`
			DeprecationWarning string `json:"deprecationWarning"`
		} `json:"versions"`
	} `json:"spec"`
}

// groupCRDs reads every generated CRD of our group in config/crd/bases, by
// file name; groupCRDFiles names them in order.
func groupCRDs(t *testing.T) map[string]crdStorage {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("..", "..", "config", "crd", "bases", "*.yaml"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no CRDs in config/crd/bases: %v", err)
	}
	crds := map[string]crdStorage{}
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var crd crdStorage
		if err := utilyaml.Unmarshal(data, &crd); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		if crd.Spec.Group == c4v1beta1.GroupVersion.Group {
			crds[filepath.Base(file)] = crd
		}
	}
	if len(crds) == 0 {
		t.Fatalf("no %s CRDs in config/crd/bases", c4v1beta1.GroupVersion.Group)
	}
	return crds
}

func groupCRDFiles(crds map[string]crdStorage) []string {
	return slices.Sorted(maps.Keys(crds))
}

// TestEveryCRDStoresTheHub holds every generated CRD of our group to
// docs/plans/done/api-versions.md: the version with storage: true is v1beta1,
// so an object written today is at the version that stays when the next one
// is added beside it. The +kubebuilder:storageversion marker belongs in
// api/v1beta1; Domain once carried it in the older version's package and
// nothing noticed (#1914).
func TestEveryCRDStoresTheHub(t *testing.T) {
	hub := c4v1beta1.GroupVersion.Version
	crds := groupCRDs(t)
	for _, file := range groupCRDFiles(crds) {
		crd := crds[file]
		stored := ""
		for _, v := range crd.Spec.Versions {
			if v.Storage {
				stored = v.Name
			}
		}
		if stored != hub {
			t.Errorf("%s: the %s CRD stores %q, not the hub %q; move +kubebuilder:storageversion to api/%s/%s_types.go and run make manifests",
				file, crd.Spec.Names.Kind, stored, hub, hub, strings.ToLower(crd.Spec.Names.Kind))
		}
	}
}

// TestEveryCRDServesV1beta1Alone holds every generated CRD of our group to
// docs/design.md, "Compatibility" (#1963): v1alpha1 is dropped, and every kind
// is served at v1beta1 alone, deprecated in nothing. A kind scaffolded at
// another version (kubebuilder's default is v1alpha1) fails here. The change
// that adds the next version beside v1beta1, and deprecates v1beta1, rewrites
// this test.
func TestEveryCRDServesV1beta1Alone(t *testing.T) {
	hub := c4v1beta1.GroupVersion.Version
	crds := groupCRDs(t)
	for _, file := range groupCRDFiles(crds) {
		crd := crds[file]
		var served []string
		for _, v := range crd.Spec.Versions {
			if v.Served {
				served = append(served, v.Name)
			}
			if v.Deprecated || v.DeprecationWarning != "" {
				t.Errorf("%s: the %s CRD marks %s deprecated; none is while every kind serves %s alone (#1963)",
					file, crd.Spec.Names.Kind, v.Name, hub)
			}
		}
		if len(crd.Spec.Versions) != 1 || len(served) != 1 || served[0] != hub {
			t.Errorf("%s: the %s CRD serves %v of %d version(s), want %s alone (#1963); a kind is scaffolded with "+
				"kubebuilder create api --group theos --version %s",
				file, crd.Spec.Names.Kind, served, len(crd.Spec.Versions), hub, hub)
		}
	}
}
