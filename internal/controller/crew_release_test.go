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
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	eventsv1 "k8s.io/api/events/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/gitprovider"
)

var releaseDue = time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)

// mergedIssue files an issue in release v0.1.0, closes it, and records it as
// merged at commit 5a-merge by a Task.
func mergedIssue(ns string) {
	GinkgoHelper()
	mergedIssueAt(ns, "5a-merge")
}

// mergedIssueAt is mergedIssue with the Task's merge commit, "" for none read yet.
func mergedIssueAt(ns, commit string) {
	GinkgoHelper()
	n := gitFake.addIssue(ns, "orders-api", "Validate orders", clearIssue, nil)
	gitFake.setIssueRelease(ns, "orders-api", n, "v0.1.0", releaseDue)
	Expect(gitFake.closeIssue(ns, "orders-api", n)).To(Succeed())
	// The Task's Agent doesn't exist, so the Task does nothing before it is Merged.
	task := newTask(ns, "orders-api-merged", "")
	task.Spec.AgentRef = c4v1alpha1.LocalRef{Name: "nobody"}
	task.Spec.Issue = int32(n)
	mustCreate(task)
	// Once it waits for its Agent, the Task reconciler writes nothing more.
	Eventually(func(g Gomega) {
		cond := condition(getTask(g, ns, "orders-api-merged").Status.Conditions, ConditionProgressing)
		g.Expect(cond).NotTo(BeNil())
		g.Expect(cond.Reason).To(Equal("AgentNotFound"))
	}).Should(Succeed())
	Eventually(func(g Gomega) {
		task := getTask(g, ns, "orders-api-merged")
		completed := metav1.Now()
		task.Status.Phase, task.Status.MergeCommit, task.Status.CompletedAt = c4v1alpha1.TaskMerged, commit, &completed
		g.Expect(k8sClient.Status().Update(ctx, task)).To(Succeed())
	}).Should(Succeed())
}

// crewEvents lists the Events about the Crew of orders-api.
func crewEvents(g Gomega, ns string) []eventsv1.Event {
	var events eventsv1.EventList
	g.Expect(k8sClient.List(ctx, &events, client.InNamespace(ns))).To(Succeed())
	var out []eventsv1.Event
	for _, e := range events.Items {
		if e.Regarding.Kind == "Crew" && e.Regarding.Name == "orders-api" {
			out = append(out, e)
		}
	}
	return out
}

var _ = Describe("Crew releases", func() {
	It("tags a done release at its newest merge and closes its milestone", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		crewAgents(ns, 1)
		mergedIssue(ns)
		crew := newCrew(ns)
		crew.Spec.TagReleases = true
		mustCreate(crew)

		Eventually(func(g Gomega) {
			g.Expect(gitFake.tag(ns, "orders-api", "v0.1.0")).To(Equal("5a-merge"))
			g.Expect(gitFake.milestoneClosed(ns, "orders-api", "v0.1.0")).To(BeTrue())
			crew := getCrew(g, ns)
			g.Expect(crew.Status.LastRelease).NotTo(BeNil())
			g.Expect(crew.Status.LastRelease.Tag).To(Equal("v0.1.0"))
			g.Expect(crew.Status.LastRelease.Commit).To(Equal("5a-merge"))
			cond := condition(crew.Status.Conditions, ConditionReleased)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			g.Expect(cond.Message).To(Equal("Released v0.1.0 at 5a-merge"))
			g.Expect(crewEvents(g, ns)).To(ContainElement(And(HaveField("Type", "Normal"),
				HaveField("Reason", "Released"), HaveField("Note", "Released v0.1.0 at 5a-merge"))))
		}).Should(Succeed())
	})

	It("leaves a release whose tag names another commit to a person, and says so once", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		crewAgents(ns, 1)
		mergedIssue(ns)
		gitFake.setTag(ns, "orders-api", "v0.1.0", "5a-other")
		crew := newCrew(ns)
		crew.Spec.TagReleases = true
		mustCreate(crew)

		Eventually(func(g Gomega) {
			cond := condition(getCrew(g, ns).Status.Conditions, ConditionReleased)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(cond.Reason).To(Equal("TagClash"))
			g.Expect(crewEvents(g, ns)).To(ContainElement(And(HaveField("Type", "Warning"),
				HaveField("Reason", "TagClash"))))
		}).Should(Succeed())
		// The Crew polls each second; a repeated Warning would be a second Event
		// or a raised series count.
		Consistently(func(g Gomega) {
			events := crewEvents(g, ns)
			g.Expect(events).To(HaveLen(1))
			g.Expect(events[0].Series).To(BeNil())
		}, 3*time.Second).Should(Succeed())
		Expect(gitFake.tag(ns, "orders-api", "v0.1.0")).To(Equal("5a-other"), "a tag is never moved")
		Expect(gitFake.milestoneClosed(ns, "orders-api", "v0.1.0")).To(BeFalse())
	})

	It("tags nothing without spec.tagReleases", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		crewAgents(ns, 1)
		mergedIssue(ns)
		mustCreate(newCrew(ns))

		Eventually(func(g Gomega) {
			g.Expect(getCrew(g, ns).Status.LastPolled).NotTo(BeNil())
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			crew := getCrew(g, ns)
			g.Expect(crew.Status.LastRelease).To(BeNil())
			g.Expect(condition(crew.Status.Conditions, ConditionReleased)).To(BeNil())
			g.Expect(gitFake.tag(ns, "orders-api", "v0.1.0")).To(BeEmpty())
			g.Expect(gitFake.milestoneClosed(ns, "orders-api", "v0.1.0")).To(BeFalse())
		}, 3*time.Second).Should(Succeed())
	})

	It("closes the milestone of a release it tagged once closing works again", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		crewAgents(ns, 1)
		mergedIssue(ns)
		gitFake.failCloses(1)
		crew := newCrew(ns)
		crew.Spec.TagReleases = true
		mustCreate(crew)

		// The second poll finds the tag at its commit and only closes; creating
		// the tag again would fail, so a Released condition shows it didn't.
		Eventually(func(g Gomega) {
			g.Expect(gitFake.milestoneClosed(ns, "orders-api", "v0.1.0")).To(BeTrue())
			crew := getCrew(g, ns)
			g.Expect(crew.Status.LastRelease).NotTo(BeNil())
			g.Expect(crew.Status.LastRelease.Commit).To(Equal("5a-merge"))
			cond := condition(crew.Status.Conditions, ConditionReleased)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		}).Should(Succeed())
		Expect(gitFake.tag(ns, "orders-api", "v0.1.0")).To(Equal("5a-merge"))
	})

	It("holds a release until its Task has read its merge commit", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		crewAgents(ns, 1)
		mergedIssueAt(ns, "")
		crew := newCrew(ns)
		crew.Spec.TagReleases = true
		mustCreate(crew)

		Eventually(func(g Gomega) {
			cond := condition(getCrew(g, ns).Status.Conditions, ConditionReleased)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Reason).To(Equal("NoMergeCommit"))
		}).Should(Succeed())
		Expect(gitFake.tag(ns, "orders-api", "v0.1.0")).To(BeEmpty())

		Eventually(func(g Gomega) {
			task := getTask(g, ns, "orders-api-merged")
			task.Status.MergeCommit = "5a-merge"
			g.Expect(k8sClient.Status().Update(ctx, task)).To(Succeed())
		}).Should(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(gitFake.tag(ns, "orders-api", "v0.1.0")).To(Equal("5a-merge"))
			cond := condition(getCrew(g, ns).Status.Conditions, ConditionReleased)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		}).Should(Succeed())
	})

	It("drops the Released condition when spec.tagReleases is switched off", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		crewAgents(ns, 1)
		mergedIssue(ns)
		gitFake.setTag(ns, "orders-api", "v0.1.0", "5a-other")
		crew := newCrew(ns)
		crew.Spec.TagReleases = true
		mustCreate(crew)

		Eventually(func(g Gomega) {
			g.Expect(condition(getCrew(g, ns).Status.Conditions, ConditionReleased)).NotTo(BeNil())
		}).Should(Succeed())
		Eventually(func(g Gomega) {
			crew := getCrew(g, ns)
			crew.Spec.TagReleases = false
			g.Expect(k8sClient.Update(ctx, crew)).To(Succeed())
		}).Should(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(condition(getCrew(g, ns).Status.Conditions, ConditionReleased)).To(BeNil())
		}).Should(Succeed())
	})

	It("reports a release run that fails, once, and one that a person fixes", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		crewAgents(ns, 1)
		mergedIssue(ns)
		crew := newCrew(ns)
		crew.Spec.TagReleases = true
		mustCreate(crew)

		Eventually(func(g Gomega) {
			release := getCrew(g, ns).Status.LastRelease
			g.Expect(release).NotTo(BeNil())
			g.Expect(release.Run).To(Equal("Waiting"))
		}).Should(Succeed())

		runURL := "http://gitea/" + ns + "/orders-api/actions/runs/5"
		run := gitprovider.WorkflowRun{ID: 5, Workflow: "release.yaml", Status: "in_progress", URL: runURL}
		gitFake.setRuns(ns, "orders-api", "v0.1.0", "5a-merge", run)
		Eventually(func(g Gomega) {
			g.Expect(getCrew(g, ns).Status.LastRelease.Run).To(Equal("Running"))
		}).Should(Succeed())

		run.Status, run.Conclusion = "completed", "failure"
		gitFake.setRuns(ns, "orders-api", "v0.1.0", "5a-merge", run)
		failed := And(HaveField("Type", "Warning"), HaveField("Reason", "ReleaseRunFailed"),
			HaveField("Note", "Release run release.yaml of v0.1.0 ended failure: "+runURL))
		Eventually(func(g Gomega) {
			release := getCrew(g, ns).Status.LastRelease
			g.Expect(release.Run).To(Equal("Failed"))
			g.Expect(release.RunURL).To(Equal(runURL))
			g.Expect(crewEvents(g, ns)).To(ContainElement(failed))
		}).Should(Succeed())
		// The Crew polls each second; a repeated Warning would be a second Event
		// or a raised series count.
		Consistently(func(g Gomega) {
			var warnings []eventsv1.Event
			for _, e := range crewEvents(g, ns) {
				if e.Reason == "ReleaseRunFailed" {
					warnings = append(warnings, e)
				}
			}
			g.Expect(warnings).To(HaveLen(1))
			g.Expect(warnings[0].Series).To(BeNil())
		}, 3*time.Second).Should(Succeed())

		run.Conclusion = "success"
		gitFake.setRuns(ns, "orders-api", "v0.1.0", "5a-merge", run)
		Eventually(func(g Gomega) {
			g.Expect(getCrew(g, ns).Status.LastRelease.Run).To(Equal("Succeeded"))
			g.Expect(crewEvents(g, ns)).To(ContainElement(And(HaveField("Type", "Normal"),
				HaveField("Reason", "ReleaseRunSucceeded"))))
		}).Should(Succeed())
	})
})

var _ = Describe("Crew release preparation", func() {
	// prepareIssues lists the open issues of orders-api titled "Prepare v0.1.0".
	prepareIssues := func(ns string) []fakeIssue {
		var out []fakeIssue
		for _, i := range gitFake.openIssues(ns, "orders-api") {
			if i.Title == "Prepare v0.1.0" {
				out = append(out, i)
			}
		}
		return out
	}

	It("files the prepare issue of a release whose work merged, once", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		crewAgents(ns, 1)
		mergedIssue(ns)
		crew := newCrew(ns)
		// Paused, it starts no Task on the prepare issue, but still files it.
		crew.Spec.Paused = true
		crew.Spec.PrepareReleases = true
		mustCreate(crew)

		Eventually(func(g Gomega) {
			issues := prepareIssues(ns)
			g.Expect(issues).To(HaveLen(1))
			g.Expect(issues[0].Labels).To(ConsistOf("kind/release", "status/ready"))
			g.Expect(issues[0].milestone).To(Equal("v0.1.0"))
			g.Expect(issues[0].Body).To(ContainSubstring("Validate orders"))
			g.Expect(crewEvents(g, ns)).To(ContainElement(And(HaveField("Type", "Normal"),
				HaveField("Reason", "PreparingRelease"), HaveField("Action", "PrepareRelease"))))
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			g.Expect(prepareIssues(ns)).To(HaveLen(1))
			crew := getCrew(g, ns)
			g.Expect(condition(crew.Status.Conditions, ConditionReleased)).To(BeNil(), "tagging is off")
			g.Expect(gitFake.tag(ns, "orders-api", "v0.1.0")).To(BeEmpty())
		}, 3*time.Second).Should(Succeed())
	})

	It("files the prepare issue before it tags, with both switches", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		crewAgents(ns, 1)
		mergedIssue(ns)
		crew := newCrew(ns)
		crew.Spec.Paused = true
		crew.Spec.PrepareReleases = true
		crew.Spec.TagReleases = true
		mustCreate(crew)

		Eventually(func(g Gomega) {
			g.Expect(prepareIssues(ns)).To(HaveLen(1))
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			g.Expect(gitFake.tag(ns, "orders-api", "v0.1.0")).To(BeEmpty(), "the release waits for its prepare issue")
			g.Expect(gitFake.milestoneClosed(ns, "orders-api", "v0.1.0")).To(BeFalse())
			g.Expect(prepareIssues(ns)).To(HaveLen(1))
		}, 3*time.Second).Should(Succeed())
	})

	It("starts the prepare issue before the next release's work", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		crewAgents(ns, 1)
		mergedIssue(ns)
		next := gitFake.addIssue(ns, "orders-api", "Ship orders", clearIssue, []string{"status/ready"})
		gitFake.setIssueRelease(ns, "orders-api", next, "v0.2.0", releaseDue.AddDate(0, 0, 7))
		crew := newCrew(ns)
		crew.Spec.PrepareReleases = true
		mustCreate(crew)

		var prepare int
		Eventually(func(g Gomega) {
			issues := prepareIssues(ns)
			g.Expect(issues).To(HaveLen(1))
			prepare = issues[0].Number
			g.Expect(getTask(g, ns, fmt.Sprintf("orders-api-%d", prepare)).Spec.Issue).To(BeEquivalentTo(prepare))
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			var task c4v1alpha1.Task
			err := k8sClient.Get(ctx, client.ObjectKey{Namespace: ns, Name: fmt.Sprintf("orders-api-%d", next)}, &task)
			g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "v0.2.0 waits for v0.1.0's prepare issue")
		}, 3*time.Second).Should(Succeed())
	})

	It("files nothing without spec.prepareReleases", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		crewAgents(ns, 1)
		mergedIssue(ns)
		crew := newCrew(ns)
		crew.Spec.Paused = true
		mustCreate(crew)

		Eventually(func(g Gomega) {
			g.Expect(getCrew(g, ns).Status.LastPolled).NotTo(BeNil())
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			g.Expect(prepareIssues(ns)).To(BeEmpty())
		}, 3*time.Second).Should(Succeed())
	})
})
