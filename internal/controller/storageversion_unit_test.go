/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	utilyaml "k8s.io/apimachinery/pkg/util/yaml"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	c4v1beta1 "github.com/JairJosafath/c4factory/api/v1beta1"
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
			Storage            bool   `json:"storage"`
			Deprecated         bool   `json:"deprecated"`
			DeprecationWarning string `json:"deprecationWarning"`
		} `json:"versions"`
	} `json:"spec"`
}

// TestEveryCRDStoresTheHub holds every generated CRD of our group to
// docs/plans/api-versions.md: the version with storage: true is the v1beta1
// hub, so an object written today is already at the version that stays when
// v1alpha1 is dropped. The +kubebuilder:storageversion marker belongs in
// api/v1beta1; Domain carried it in api/v1alpha1 and nothing noticed (#1914).
func TestEveryCRDStoresTheHub(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", "config", "crd", "bases", "*.yaml"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no CRDs in config/crd/bases: %v", err)
	}
	hub := c4v1beta1.GroupVersion.Version
	checked := 0
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var crd crdStorage
		if err := utilyaml.Unmarshal(data, &crd); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		if crd.Spec.Group != c4v1beta1.GroupVersion.Group {
			continue
		}
		checked++
		stored := ""
		for _, v := range crd.Spec.Versions {
			if v.Storage {
				stored = v.Name
			}
		}
		if stored != hub {
			t.Errorf("%s: the %s CRD stores %q, not the hub %q; move +kubebuilder:storageversion to api/%s/%s_types.go and run make manifests",
				filepath.Base(file), crd.Spec.Names.Kind, stored, hub, hub, strings.ToLower(crd.Spec.Names.Kind))
		}
	}
	if checked == 0 {
		t.Fatalf("no %s CRDs in config/crd/bases", c4v1beta1.GroupVersion.Group)
	}
}

// TestEveryCRDDeprecatesV1alpha1 holds every generated CRD of our group to
// docs/plans/api-versions.md decision 6 (#1618): the v1alpha1 version is
// marked deprecated, and its warning (what the API server sends back in a
// Warning header, which the UI shows) names v1beta1 and the drop issue. The
// +kubebuilder:deprecatedversion marker sits above each kind in api/v1alpha1.
func TestEveryCRDDeprecatesV1alpha1(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", "config", "crd", "bases", "*.yaml"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no CRDs in config/crd/bases: %v", err)
	}
	old := c4v1alpha1.GroupVersion.Version
	hub := c4v1beta1.GroupVersion.String()
	checked := 0
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var crd crdStorage
		if err := utilyaml.Unmarshal(data, &crd); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		if crd.Spec.Group != c4v1beta1.GroupVersion.Group {
			continue
		}
		checked++
		found := false
		for _, v := range crd.Spec.Versions {
			if v.Name != old {
				continue
			}
			found = true
			if !v.Deprecated {
				t.Errorf("%s: the %s CRD does not mark %s deprecated; add +kubebuilder:deprecatedversion above the kind in api/%s/%s_types.go and run make manifests",
					filepath.Base(file), crd.Spec.Names.Kind, old, old, strings.ToLower(crd.Spec.Names.Kind))
			}
			for _, want := range []string{hub, "#1963"} {
				if !strings.Contains(v.DeprecationWarning, want) {
					t.Errorf("%s: the %s CRD's %s deprecation warning %q does not name %q", filepath.Base(file), crd.Spec.Names.Kind, old, v.DeprecationWarning, want)
				}
			}
		}
		if !found {
			t.Errorf("%s: the %s CRD no longer serves %s; dropping it is #1963", filepath.Base(file), crd.Spec.Names.Kind, old)
		}
	}
	if checked == 0 {
		t.Fatalf("no %s CRDs in config/crd/bases", c4v1beta1.GroupVersion.Group)
	}
}
