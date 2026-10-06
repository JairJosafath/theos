/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
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
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
)

const apps = "apps"

// The Standard of the hello Container; its files are hello-html's role guides.
const standard = "hello-html-standard"

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
	kubectl("-n", apps, "patch", "standard", standard, "--type=merge",
		"-p", fmt.Sprintf(`{"spec":{"template":{"ref":%q}}}`, ref))
}

// Three of the four specs, on the fixture's hello Container: scaffolded from
// hello-html and its Standard with main protected, a Task that goes two rounds
// to a merge with the stand-in agent, and a Standard bump that reaches main.
// The fourth, a Brief, is in brief_test.go. The e2e target applies
// test/e2e/testdata/apps first.
var _ = Describe("Hello page", Ordered, ContinueOnFailure, func() {
	It("scaffolds a Container from hello-html and protects main", func() {
		Eventually(func(g Gomega) {
			var list c4v1alpha1.ContainerList
			g.Expect(k8s.List(ctx, &list, client.InNamespace(apps))).To(Succeed())
			g.Expect(list.Items).To(HaveLen(1))
			for _, c := range list.Items {
				g.Expect(c.Status.Phase).To(Equal(c4v1alpha1.ContainerReady), c.Name)
				// The Standard's ref moves (templates/versions.txt), and a cluster
				// kept from an earlier run holds the hello Container from then.
				g.Expect(c.Status.ScaffoldedFrom).To(HaveExactElements(
					"hello-html@v1.0.0", HavePrefix(standard+"@")), c.Name)
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

	It("takes a Task two rounds to a merge with the stand-in agent", func() {
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

	It("carries a Standard bump to main through a pull request it merges", func() {
		By("waiting until the Standard's files are at the ref the fixture pins")
		var std c4v1alpha1.Standard
		Expect(k8s.Get(ctx, key(apps, standard), &std)).To(Succeed())
		pinned := std.Spec.Template.Ref
		waitStandardAt(pinned)

		By("committing a change to the reviewer's guide of templates/" + standard)
		const guide = "docs/agents/reviewer.md"
		var file struct {
			Content string `json:"content"`
			SHA     string `json:"sha"`
		}
		giteaGet("/api/v1/repos/templates/"+standard+"/contents/"+guide+"?ref=main", &file)
		old, err := base64.StdEncoding.DecodeString(file.Content)
		Expect(err).NotTo(HaveOccurred())
		// Unique per run, so a kept cluster gets a new commit every time.
		line := fmt.Sprintf("<!-- e2e standard update %d -->\n", time.Now().UnixNano())
		var written struct {
			Commit struct {
				SHA string `json:"sha"`
			} `json:"commit"`
		}
		giteaSend(http.MethodPut, "/api/v1/repos/templates/"+standard+"/contents/"+guide, map[string]string{
			"content":    base64.StdEncoding.EncodeToString(append([]byte(line), old...)),
			"sha":        file.SHA,
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
		giteaGet("/api/v1/repos/apps/hello/contents/"+guide+"?ref=main", &onMain)
		content, err := base64.StdEncoding.DecodeString(onMain.Content)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(content)).To(HavePrefix(line))

		// Merged, its branch is gone and Gitea reports the head as refs/pull/N/head.
		var pulls []struct {
			Title  string `json:"title"`
			Merged bool   `json:"merged"`
		}
		giteaGet("/api/v1/repos/apps/hello/pulls?state=closed&limit=50", &pulls)
		Expect(pulls).To(ContainElement(And(
			HaveField("Merged", true), HaveField("Title", "Update the Standard "+standard+" to "+commit))))
	})
})
