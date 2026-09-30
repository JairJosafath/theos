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

package e2e

import (
	"encoding/base64"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	networkingv1 "k8s.io/api/networking/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
)

const toolbox = "toolbox"

// The agent factory milestone: Flutter Containers scaffolded with a Standard, CI
// on the protected main, and a Task that goes through two rounds to a merge with
// the stand-in agent. `make e2e` applies config/samples/toolbox first.
var _ = Describe("Agent factory", Ordered, ContinueOnFailure, func() {
	It("scaffolds the toolbox apps from the Flutter templates and protects main", func() {
		Eventually(func(g Gomega) {
			var list c4v1alpha1.ContainerList
			g.Expect(k8s.List(ctx, &list, client.InNamespace(toolbox))).To(Succeed())
			g.Expect(list.Items).To(HaveLen(6))
			for _, c := range list.Items {
				g.Expect(c.Status.Phase).To(Equal(c4v1alpha1.ContainerReady), c.Name)
				g.Expect(c.Status.ScaffoldedFrom).To(Equal(
					[]string{"flutter-app@v1.0.0", "flutter-standard@v1.0.0", "android-release@v1.0.0"}), c.Name)
			}
		}).WithTimeout(3 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())

		var rules []struct {
			RuleName          string   `json:"rule_name"`
			EnablePush        bool     `json:"enable_push"`
			EnableStatusCheck bool     `json:"enable_status_check"`
			Contexts          []string `json:"status_check_contexts"`
		}
		giteaGet("/api/v1/repos/toolbox/image-shrink/branch_protections", &rules)
		Expect(rules).To(ContainElement(And(
			HaveField("RuleName", "main"), HaveField("EnablePush", false),
			HaveField("EnableStatusCheck", true), HaveField("Contexts", ContainElement("check*")))))
	})

	It("fences agent sandboxes in the project", func() {
		var policy networkingv1.NetworkPolicy
		Expect(k8s.Get(ctx, key(toolbox, "c4-agent-sandbox"), &policy)).To(Succeed())
		Expect(policy.Spec.PodSelector.MatchLabels).To(HaveKeyWithValue("c4.factory.io/agent-sandbox", "true"))
	})

	It("runs CI on main and reports it on the Container", func() {
		Eventually(func(g Gomega) {
			var c c4v1alpha1.Container
			g.Expect(k8s.Get(ctx, key(toolbox, "image-shrink"), &c)).To(Succeed())
			g.Expect(c.Status.Checks).NotTo(BeNil())
			g.Expect(c.Status.Checks.State).To(Equal("success"), "checks: %+v", c.Status.Checks)
		}).WithTimeout(15 * time.Minute).WithPolling(15 * time.Second).Should(Succeed())
	})

	It("takes a Task through two rounds to a merge with the stand-in agent", func() {
		kubectl("apply", "-f", "config/samples/toolbox/tasks/stand-in.yaml")
		DeferCleanup(func() { kubectl("-n", toolbox, "delete", "task", "stand-in-loop", "--ignore-not-found") })

		var task c4v1alpha1.Task
		Eventually(func(g Gomega) {
			g.Expect(k8s.Get(ctx, key(toolbox, "stand-in-loop"), &task)).To(Succeed())
			g.Expect(task.Status.Phase).NotTo(Equal(c4v1alpha1.TaskFailed), "task: %+v", task.Status)
			g.Expect(task.Status.Phase).To(Equal(c4v1alpha1.TaskMerged))
		}).WithTimeout(40 * time.Minute).WithPolling(15 * time.Second).Should(Succeed())
		Expect(task.Status.Round).To(Equal(int32(2)), "the stand-in reviewer asks for changes once")
		Expect(task.Status.Review).To(HaveField("Verdict", "approve"))

		var file struct {
			Content string `json:"content"`
		}
		giteaGet("/api/v1/repos/toolbox/image-shrink/contents/STAND-IN.md?ref=main", &file)
		content, err := base64.StdEncoding.DecodeString(file.Content)
		Expect(err).NotTo(HaveOccurred())
		Expect(strings.Count(string(content), "stand-in agent")).To(Equal(2), string(content))

		var pulls []struct {
			Merged bool `json:"merged"`
		}
		giteaGet("/api/v1/repos/toolbox/image-shrink/pulls?state=closed", &pulls)
		Expect(pulls).To(ContainElement(HaveField("Merged", true)))
	})
})
