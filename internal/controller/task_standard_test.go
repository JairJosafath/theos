/*
Copyright 2026.

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

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/model"
)

var _ = Describe("Task allowedProtectedPaths", func() {
	It("takes eligible paths on an issue Task only, and never changes", func() {
		ns := newProjectNamespace()
		gated := func(name string, paths ...string) *c4v1alpha1.Task {
			t := newTask(ns, name, "")
			t.Spec.Issue = 2
			t.Spec.AllowedProtectedPaths = paths
			return t
		}
		Expect(k8sClient.Create(ctx, gated("ok", "Makefile", ".gitea/workflows/release.yaml"))).To(Succeed())
		for _, never := range model.AgentNeverPaths {
			err := k8sClient.Create(ctx, gated("never", never))
			Expect(err).To(MatchError(ContainSubstring("may not include AGENTS.md")), never)
		}
		goal := newTask(ns, "goal", "Do it")
		goal.Spec.AllowedProtectedPaths = []string{"Makefile"}
		Expect(k8sClient.Create(ctx, goal)).To(MatchError(ContainSubstring("needs an issue")))
		task := getTask(Default, ns, "ok")
		Expect(patch(task, func() { task.Spec.AllowedProtectedPaths = []string{"Makefile", ".golangci.yml"} })).
			To(MatchError(ContainSubstring("immutable")))
	})
})
