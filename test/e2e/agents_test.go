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
	"fmt"
	"net/http"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
)

const apps = "apps"

// waitStandardAt waits until the hello Container's Standard files are at ref.
func waitStandardAt(ref string) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		var c c4v1alpha1.Container
		g.Expect(k8s.Get(ctx, key(apps, "hello"), &c)).To(Succeed())
		g.Expect(c.Status.Standard).NotTo(BeNil())
		g.Expect(c.Status.Standard.Ref).To(Equal(ref), "status: %+v", c.Status.Standard)
		cond := meta.FindStatusCondition(c.Status.Conditions, c4v1alpha1.ConditionStandardUpdated)
		g.Expect(cond).NotTo(BeNil())
		g.Expect(cond.Status).To(Equal(metav1.ConditionTrue), "%s: %s", cond.Reason, cond.Message)
	}).WithTimeout(15 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
}

func patchStandardRef(ref string) {
	GinkgoHelper()
	kubectl("-n", apps, "patch", "standard", "hello-standard", "--type=merge",
		"-p", fmt.Sprintf(`{"spec":{"template":{"ref":%q}}}`, ref))
}

// The agent factory milestone: a hello-world Container scaffolded with a Standard, CI
// on the protected main, and a Task that goes through two rounds to a merge with
// the stand-in agent. `make e2e` applies test/e2e/testdata/apps first.
var _ = Describe("Agent factory", Ordered, ContinueOnFailure, func() {
	It("scaffolds the hello Container from the hello-world templates and protects main", func() {
		Eventually(func(g Gomega) {
			var list c4v1alpha1.ContainerList
			g.Expect(k8s.List(ctx, &list, client.InNamespace(apps))).To(Succeed())
			g.Expect(list.Items).To(HaveLen(1))
			for _, c := range list.Items {
				g.Expect(c.Status.Phase).To(Equal(c4v1alpha1.ContainerReady), c.Name)
				// The Standard's ref moves (templates/versions.txt), and a cluster
				// kept from an earlier run holds the hello Container from then.
				g.Expect(c.Status.ScaffoldedFrom).To(HaveExactElements(
					"hello@v1.0.0", HavePrefix("hello-standard@")), c.Name)
			}
		}).WithTimeout(3 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())

		var rules []struct {
			RuleName          string   `json:"rule_name"`
			EnablePush        bool     `json:"enable_push"`
			EnableStatusCheck bool     `json:"enable_status_check"`
			Contexts          []string `json:"status_check_contexts"`
		}
		giteaGet("/api/v1/repos/apps/hello/branch_protections", &rules)
		Expect(rules).To(ContainElement(And(
			HaveField("RuleName", "main"), HaveField("EnablePush", false),
			HaveField("EnableStatusCheck", true), HaveField("Contexts", ContainElement("check*")))))
	})

	It("fences agent sandboxes in the project", func() {
		var policy networkingv1.NetworkPolicy
		Expect(k8s.Get(ctx, key(apps, "c4-agent-sandbox"), &policy)).To(Succeed())
		Expect(policy.Spec.PodSelector.MatchLabels).To(HaveKeyWithValue("c4.factory.io/agent-sandbox", "true"))
	})

	It("runs CI on main and reports it on the Container", func() {
		Eventually(func(g Gomega) {
			var c c4v1alpha1.Container
			g.Expect(k8s.Get(ctx, key(apps, "hello"), &c)).To(Succeed())
			g.Expect(c.Status.Checks).NotTo(BeNil())
			g.Expect(c.Status.Checks.State).To(Equal("success"), "checks: %+v", c.Status.Checks)
		}).WithTimeout(15 * time.Minute).WithPolling(15 * time.Second).Should(Succeed())
	})

	It("takes a Task through two rounds to a merge with the stand-in agent", func() {
		kubectl("apply", "-f", "test/e2e/testdata/apps/tasks/stand-in.yaml")
		DeferCleanup(func() { kubectl("-n", apps, "delete", "task", "stand-in-loop", "--ignore-not-found") })

		var task c4v1alpha1.Task
		Eventually(func(g Gomega) {
			g.Expect(k8s.Get(ctx, key(apps, "stand-in-loop"), &task)).To(Succeed())
			g.Expect(task.Status.Phase).NotTo(Equal(c4v1alpha1.TaskFailed), "task: %+v", task.Status)
			g.Expect(task.Status.Phase).To(Equal(c4v1alpha1.TaskMerged))
		}).WithTimeout(40 * time.Minute).WithPolling(15 * time.Second).Should(Succeed())
		Expect(task.Status.Round).To(Equal(int32(2)), "the stand-in reviewer asks for changes once")
		Expect(task.Status.Review).To(HaveField("Verdict", "approve"))

		var file struct {
			Content string `json:"content"`
		}
		giteaGet("/api/v1/repos/apps/hello/contents/STAND-IN.md?ref=main", &file)
		content, err := base64.StdEncoding.DecodeString(file.Content)
		Expect(err).NotTo(HaveOccurred())
		Expect(strings.Count(string(content), "stand-in agent")).To(Equal(2), string(content))

		var pulls []struct {
			Merged bool `json:"merged"`
		}
		giteaGet("/api/v1/repos/apps/hello/pulls?state=closed", &pulls)
		Expect(pulls).To(ContainElement(HaveField("Merged", true)))
	})

	It("carries a new commit of the Standard to main through a pull request it merges", func() {
		By("waiting until the Standard's files are at the ref the fixture pins")
		var standard c4v1alpha1.Standard
		Expect(k8s.Get(ctx, key(apps, "hello-standard"), &standard)).To(Succeed())
		pinned := standard.Spec.Template.Ref
		waitStandardAt(pinned)

		By("committing a change to the Makefile of templates/hello-standard")
		var makefile struct {
			Content string `json:"content"`
			SHA     string `json:"sha"`
		}
		giteaGet("/api/v1/repos/templates/hello-standard/contents/Makefile?ref=main", &makefile)
		old, err := base64.StdEncoding.DecodeString(makefile.Content)
		Expect(err).NotTo(HaveOccurred())
		// Unique per run, so a kept cluster gets a new commit every time.
		line := fmt.Sprintf("# e2e standard update %d\n", time.Now().UnixNano())
		var written struct {
			Commit struct {
				SHA string `json:"sha"`
			} `json:"commit"`
		}
		giteaSend(http.MethodPut, "/api/v1/repos/templates/hello-standard/contents/Makefile", map[string]string{
			"content":    base64.StdEncoding.EncodeToString(append([]byte(line), old...)),
			"sha":        makefile.SHA,
			"branch":     "main",
			"new_branch": fmt.Sprintf("e2e-standard-update-%d", time.Now().Unix()),
			"message":    "e2e: a Standard update",
		}, &written)
		commit := written.Commit.SHA
		Expect(commit).To(HaveLen(40))

		By("pinning the Standard to that commit")
		DeferCleanup(func() { patchStandardRef(pinned) })
		patchStandardRef(commit)

		waitStandardAt(commit)
		var onMain struct {
			Content string `json:"content"`
		}
		giteaGet("/api/v1/repos/apps/hello/contents/Makefile?ref=main", &onMain)
		content, err := base64.StdEncoding.DecodeString(onMain.Content)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(content)).To(HavePrefix(line))

		var pulls []struct {
			Merged bool `json:"merged"`
			Head   struct {
				Ref string `json:"ref"`
			} `json:"head"`
		}
		giteaGet("/api/v1/repos/apps/hello/pulls?state=closed&limit=50", &pulls)
		Expect(pulls).To(ContainElement(And(
			HaveField("Merged", true), HaveField("Head.Ref", "standard-update/hello-standard-"+commit))))
	})
})
