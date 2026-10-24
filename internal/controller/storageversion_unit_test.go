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
			Name    string `json:"name"`
			Storage bool   `json:"storage"`
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
		if crd.Spec.Group != c4v1alpha1.GroupVersion.Group {
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
		t.Fatalf("no %s CRDs in config/crd/bases", c4v1alpha1.GroupVersion.Group)
	}
}
