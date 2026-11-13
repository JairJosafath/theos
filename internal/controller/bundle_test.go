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
