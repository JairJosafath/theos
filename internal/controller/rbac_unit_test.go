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

	rbacv1 "k8s.io/api/rbac/v1"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
)

// controller-gen writes any resource name an RBAC marker gives, and the
// envtest suite only catches a missing grant for a call some spec makes. So a
// marker naming a resource that doesn't exist (persons for people, #371)
// passes both. This test holds the generated role to the CRDs: every resource
// of our group is a CRD's plural, and every subresource is one that CRD has.

// crdNames is the part of a CRD this test reads.
type crdNames struct {
	Spec struct {
		Group string `json:"group"`
		Names struct {
			Plural string `json:"plural"`
		} `json:"names"`
		Versions []struct {
			Subresources struct {
				Status *struct{} `json:"status"`
			} `json:"subresources"`
		} `json:"versions"`
	} `json:"spec"`
}

func TestRoleNamesOnlyRealCRDResources(t *testing.T) {
	root := filepath.Join("..", "..", "config")

	// The CRD plurals of our group, and whether each has a status subresource.
	hasStatus := map[string]bool{}
	files, err := filepath.Glob(filepath.Join(root, "crd", "bases", "*.yaml"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no CRDs in config/crd/bases: %v", err)
	}
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var crd crdNames
		if err := utilyaml.Unmarshal(data, &crd); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		if crd.Spec.Group != c4v1alpha1.GroupVersion.Group || crd.Spec.Names.Plural == "" {
			continue
		}
		status := false
		for _, v := range crd.Spec.Versions {
			status = status || v.Subresources.Status != nil
		}
		hasStatus[crd.Spec.Names.Plural] = status
	}

	data, err := os.ReadFile(filepath.Join(root, "rbac", "role.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var role rbacv1.ClusterRole
	if err := utilyaml.Unmarshal(data, &role); err != nil {
		t.Fatalf("role.yaml: %v", err)
	}

	checked := 0
	for _, rule := range role.Rules {
		ours := false
		for _, g := range rule.APIGroups {
			ours = ours || g == c4v1alpha1.GroupVersion.Group
		}
		if !ours {
			continue
		}
		for _, resource := range rule.Resources {
			checked++
			plural, sub, _ := strings.Cut(resource, "/")
			status, ok := hasStatus[plural]
			switch {
			case !ok:
				t.Errorf("role.yaml grants %q, but no %s CRD has the plural %q; fix the +kubebuilder:rbac marker",
					resource, c4v1alpha1.GroupVersion.Group, plural)
			case sub == "status" && !status:
				t.Errorf("role.yaml grants %q, but the %s CRD has no status subresource", resource, plural)
			// finalizers is not a CRD subresource: the API server checks it
			// only to let an ownerReference set blockOwnerDeletion, for any
			// resource, so the CRD existing is enough.
			case sub != "" && sub != "status" && sub != "finalizers":
				t.Errorf("role.yaml grants %q, but %s CRDs have no %q subresource", resource, plural, sub)
			}
		}
	}
	if checked == 0 {
		t.Fatalf("role.yaml grants nothing in %s; is it the generated role?", c4v1alpha1.GroupVersion.Group)
	}
}
