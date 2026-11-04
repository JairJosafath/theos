/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of theos, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
)

var _ = Describe("Bundle", func() {
	It("takes a frameworkRef alone or with the three optional sides", func() {
		ns := newProjectNamespace()
		mustCreate(
			&c4v1beta1.Bundle{ObjectMeta: objectMeta(ns, "bare"),
				Spec: c4v1beta1.BundleSpec{FrameworkRef: c4v1beta1.LocalRef{Name: "python-fastapi"}}},
			&c4v1beta1.Bundle{ObjectMeta: objectMeta(ns, "python-service"),
				Spec: c4v1beta1.BundleSpec{
					DisplayName:   "Python service",
					Description:   "A FastAPI service on the cluster",
					FrameworkRef:  c4v1beta1.LocalRef{Name: "python-fastapi"},
					StandardRef:   &c4v1beta1.LocalRef{Name: "python-standard"},
					ComputeRef:    &c4v1beta1.LocalRef{Name: "small"},
					DeploymentRef: &c4v1beta1.LocalRef{Name: "k8s-manifests"},
				}},
		)
	})

	It("is refused without a frameworkRef", func() {
		ns := newProjectNamespace()
		bundle := &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": c4v1beta1.GroupVersion.String(),
			"kind":       "Bundle",
			"metadata":   map[string]any{"name": "no-framework", "namespace": ns},
			"spec": map[string]any{
				"standardRef": map[string]any{"name": "python-standard"},
				"computeRef":  map[string]any{"name": "small"},
			},
		}}
		Expect(k8sClient.Create(ctx, bundle)).To(MatchError(And(
			ContainSubstring("spec.frameworkRef"), ContainSubstring("Required value"))))
	})
})
