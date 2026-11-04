/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of theos, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
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

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/ports/fake"
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
	mergedIssueIn(ns, "v0.1.0", commit)
}

// mergedIssueIn is mergedIssueAt with the release the issue is in.
func mergedIssueIn(ns, release, commit string) {
	GinkgoHelper()
	n := gitFake.AddIssue(ns, "orders-api", "Validate orders", clearIssue, nil)
	gitFake.SetIssueRelease(ns, "orders-api", n, release, releaseDue)
	Expect(gitFake.CloseIssueByHand(ns, "orders-api", n)).To(Succeed())
	mergedTask(ns, "orders-api-merged", n, commit, time.Now())
}

// mergedTask records issue n as merged at commit, at time at, by Task name.
func mergedTask(ns, name string, n int, commit string, at time.Time) {
	GinkgoHelper()
	// The Task's Agent doesn't exist, so the Task does nothing before it is Merged.
	task := newTask(ns, name, "")
	task.Spec.AgentRef = c4v1beta1.LocalRef{Name: "nobody"}
	task.Spec.Issue = int32(n)
	mustCreate(task)
	// Once it waits for its Agent, the Task reconciler writes nothing more.
	Eventually(func(g Gomega) {
		cond := condition(getTask(g, ns, name).Status.Conditions, ConditionProgressing)
		g.Expect(cond).NotTo(BeNil())
		g.Expect(cond.Reason).To(Equal("AgentNotFound"))
	}).Should(Succeed())
	Eventually(func(g Gomega) {
		task := getTask(g, ns, name)
		completed := metav1.NewTime(at)
		task.Status.Phase, task.Status.MergeCommit, task.Status.CompletedAt = c4v1beta1.TaskMerged, commit, &completed
		g.Expect(k8sClient.Status().Update(ctx, task)).To(Succeed())
	}).Should(Succeed())
}

// setRerunRelease sets the rerun-release annotation of the Crew of orders-api
// to value.
func setRerunRelease(ns, value string) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		crew := getCrew(g, ns)
		if crew.Annotations == nil {
			crew.Annotations = map[string]string{}
		}
		crew.Annotations[c4v1beta1.AnnotationRerunRelease] = value
		g.Expect(k8sClient.Update(ctx, crew)).To(Succeed())
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
			g.Expect(gitFake.Tag(ns, "orders-api", "v0.1.0")).To(Equal("5a-merge"))
			g.Expect(gitFake.MilestoneClosed(ns, "orders-api", "v0.1.0")).To(BeTrue())
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
		gitFake.SetTag(ns, "orders-api", "v0.1.0", "5a-other")
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
		Expect(gitFake.Tag(ns, "orders-api", "v0.1.0")).To(Equal("5a-other"), "a tag is never moved")
		Expect(gitFake.MilestoneClosed(ns, "orders-api", "v0.1.0")).To(BeFalse())
	})

	It("leaves a release whose tag would not come after the previous release's to a person", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		crewAgents(ns, 1)
		// v0.1.0 went out at the very merge v0.2.0 would be tagged at.
		shipped := gitFake.AddIssue(ns, "orders-api", "Ship it", clearIssue, nil)
		gitFake.SetIssueRelease(ns, "orders-api", shipped, "v0.1.0", releaseDue.AddDate(0, 0, -7))
		Expect(gitFake.CloseIssueByHand(ns, "orders-api", shipped)).To(Succeed())
		gitFake.MarkMilestoneClosed(ns, "orders-api", "v0.1.0")
		gitFake.SetTag(ns, "orders-api", "v0.1.0", "5a-merge")
		// v0.1.5 was dropped without a tag, and v0.4.0 closed ahead of v0.2.0
		// by hand: neither is the previous release.
		for title, due := range map[string]time.Time{"v0.1.5": releaseDue.AddDate(0, 0, -3),
			"v0.4.0": releaseDue.AddDate(0, 0, 14)} {
			n := gitFake.AddIssue(ns, "orders-api", "Dropped "+title, clearIssue, nil)
			gitFake.SetIssueRelease(ns, "orders-api", n, title, due)
			Expect(gitFake.CloseIssueByHand(ns, "orders-api", n)).To(Succeed())
			gitFake.MarkMilestoneClosed(ns, "orders-api", title)
		}
		gitFake.SetTag(ns, "orders-api", "v0.4.0", "5a-later")
		mergedIssueIn(ns, "v0.2.0", "5a-merge")
		crew := newCrew(ns)
		crew.Spec.TagReleases = true
		mustCreate(crew)

		Eventually(func(g Gomega) {
			cond := condition(getCrew(g, ns).Status.Conditions, ConditionReleased)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(cond.Reason).To(Equal("BehindPreviousRelease"))
			g.Expect(cond.Message).To(ContainSubstring("v0.1.0's tag names that commit already"))
		}).Should(Succeed())
		Expect(gitFake.Tag(ns, "orders-api", "v0.2.0")).To(BeEmpty())
		Expect(gitFake.MilestoneClosed(ns, "orders-api", "v0.2.0")).To(BeFalse())
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
			g.Expect(gitFake.Tag(ns, "orders-api", "v0.1.0")).To(BeEmpty())
			g.Expect(gitFake.MilestoneClosed(ns, "orders-api", "v0.1.0")).To(BeFalse())
		}, 3*time.Second).Should(Succeed())
	})

	It("closes the milestone of a release it tagged once closing works again", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		crewAgents(ns, 1)
		mergedIssue(ns)
		gitFake.FailCloses(1)
		crew := newCrew(ns)
		crew.Spec.TagReleases = true
		mustCreate(crew)

		// The second poll finds the tag at its commit and only closes; creating
		// the tag again would fail, so a Released condition shows it didn't.
		Eventually(func(g Gomega) {
			g.Expect(gitFake.MilestoneClosed(ns, "orders-api", "v0.1.0")).To(BeTrue())
			crew := getCrew(g, ns)
			g.Expect(crew.Status.LastRelease).NotTo(BeNil())
			g.Expect(crew.Status.LastRelease.Commit).To(Equal("5a-merge"))
			cond := condition(crew.Status.Conditions, ConditionReleased)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		}).Should(Succeed())
		Expect(gitFake.Tag(ns, "orders-api", "v0.1.0")).To(Equal("5a-merge"))
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
		Expect(gitFake.Tag(ns, "orders-api", "v0.1.0")).To(BeEmpty())

		Eventually(func(g Gomega) {
			task := getTask(g, ns, "orders-api-merged")
			task.Status.MergeCommit = "5a-merge"
			g.Expect(k8sClient.Status().Update(ctx, task)).To(Succeed())
		}).Should(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(gitFake.Tag(ns, "orders-api", "v0.1.0")).To(Equal("5a-merge"))
			cond := condition(getCrew(g, ns).Status.Conditions, ConditionReleased)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		}).Should(Succeed())
	})

	It("releases an issue its Standard change did at the change's merge", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		crewAgents(ns, 1)
		mergedIssueAt(ns, "5a-merge")
		change := getTask(Default, ns, "orders-api-merged").Spec.Issue
		n := gitFake.AddIssue(ns, "orders-api", "Tidy the Makefile", clearIssue, nil)
		gitFake.SetIssueRelease(ns, "orders-api", n, "v0.1.0", releaseDue)
		Expect(gitFake.CloseIssueByHand(ns, "orders-api", n)).To(Succeed())
		mergedTask(ns, "orders-api-done", n, "", time.Now())
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "orders-api-done")
			task.Status.StandardChange = &c4v1beta1.TaskStandardChange{Issue: change, Paths: []string{"Makefile"},
				Count: 1}
			task.Status.Conditions = append(task.Status.Conditions, metav1.Condition{Type: ConditionSucceeded,
				Status: metav1.ConditionTrue, Reason: reasonDoneByStandardChange, Message: "done",
				LastTransitionTime: metav1.Now()})
			g.Expect(k8sClient.Status().Update(ctx, task)).To(Succeed())
		}).Should(Succeed())
		crew := newCrew(ns)
		crew.Spec.TagReleases = true
		mustCreate(crew)

		Eventually(func(g Gomega) {
			g.Expect(gitFake.Tag(ns, "orders-api", "v0.1.0")).To(Equal("5a-merge"))
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
		gitFake.SetTag(ns, "orders-api", "v0.1.0", "5a-other")
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
		run := ports.WorkflowRun{ID: 5, Workflow: "release.yaml", Status: "in_progress", URL: runURL}
		gitFake.SetRuns(ns, "orders-api", "v0.1.0", "5a-merge", run)
		Eventually(func(g Gomega) {
			g.Expect(getCrew(g, ns).Status.LastRelease.Run).To(Equal("Running"))
		}).Should(Succeed())

		run.Status, run.Conclusion = "completed", "failure"
		gitFake.SetRuns(ns, "orders-api", "v0.1.0", "5a-merge", run)
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
		gitFake.SetRuns(ns, "orders-api", "v0.1.0", "5a-merge", run)
		Eventually(func(g Gomega) {
			g.Expect(getCrew(g, ns).Status.LastRelease.Run).To(Equal("Succeeded"))
			g.Expect(crewEvents(g, ns)).To(ContainElement(And(HaveField("Type", "Normal"),
				HaveField("Reason", "ReleaseRunSucceeded"))))
		}).Should(Succeed())
	})

	It("re-runs a failed release run once per rerun-release value", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		crewAgents(ns, 1)
		mergedIssue(ns)
		crew := newCrew(ns)
		crew.Spec.TagReleases = true
		mustCreate(crew)
		Eventually(func(g Gomega) {
			g.Expect(getCrew(g, ns).Status.LastRelease).NotTo(BeNil())
		}).Should(Succeed())

		runURL := "http://gitea/" + ns + "/orders-api/actions/runs/5"
		passed := ports.WorkflowRun{ID: 4, Workflow: "notes.yaml", Status: "completed", Conclusion: "success"}
		run := ports.WorkflowRun{ID: 5, Workflow: "release.yaml", Status: "completed", Conclusion: "failure",
			URL: runURL}
		gitFake.SetRuns(ns, "orders-api", "v0.1.0", "5a-merge", run, passed)
		Eventually(func(g Gomega) {
			g.Expect(getCrew(g, ns).Status.LastRelease.Run).To(Equal("Failed"))
		}).Should(Succeed())

		setRerunRelease(ns, "1")
		Eventually(func(g Gomega) {
			status := getCrew(g, ns).Status
			g.Expect(status.RerunRelease).To(Equal("1"))
			g.Expect(status.LastRelease.Run).To(Equal("Running"))
			g.Expect(status.LastRelease.RerunTime).NotTo(BeNil())
			g.Expect(crewEvents(g, ns)).To(ContainElement(And(HaveField("Type", "Normal"),
				HaveField("Reason", "ReleaseRunRerun"), HaveField("Note", "Re-ran the failed runs of v0.1.0: release.yaml"))))
		}).Should(Succeed())
		Expect(gitFake.Reruns(ns, "orders-api", 5)).To(Equal(1))
		Expect(gitFake.Reruns(ns, "orders-api", 4)).To(Equal(0))

		// The new attempt fails too: the same value asks for nothing more.
		gitFake.SetRuns(ns, "orders-api", "v0.1.0", "5a-merge", run, passed)
		Eventually(func(g Gomega) {
			g.Expect(getCrew(g, ns).Status.LastRelease.Run).To(Equal("Failed"))
		}).Should(Succeed())
		Consistently(func() int {
			return gitFake.Reruns(ns, "orders-api", 5)
		}, 3*time.Second).Should(Equal(1))

		setRerunRelease(ns, "2")
		Eventually(func(g Gomega) {
			g.Expect(getCrew(g, ns).Status.RerunRelease).To(Equal("2"))
			g.Expect(gitFake.Reruns(ns, "orders-api", 5)).To(Equal(2))
		}).Should(Succeed())

		run.Conclusion = "success"
		gitFake.SetRuns(ns, "orders-api", "v0.1.0", "5a-merge", run, passed)
		Eventually(func(g Gomega) {
			g.Expect(getCrew(g, ns).Status.LastRelease.Run).To(Equal("Succeeded"))
		}).Should(Succeed())
	})

	It("warns when the host refuses a re-run, and tries again for a new rerun-release value", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		crewAgents(ns, 1)
		mergedIssue(ns)
		crew := newCrew(ns)
		crew.Spec.TagReleases = true
		mustCreate(crew)
		Eventually(func(g Gomega) {
			g.Expect(getCrew(g, ns).Status.LastRelease).NotTo(BeNil())
		}).Should(Succeed())
		run := ports.WorkflowRun{ID: 5, Workflow: "release.yaml", Status: "completed", Conclusion: "failure"}
		gitFake.SetRuns(ns, "orders-api", "v0.1.0", "5a-merge", run)
		Eventually(func(g Gomega) {
			g.Expect(getCrew(g, ns).Status.LastRelease.Run).To(Equal("Failed"))
		}).Should(Succeed())

		gitFake.RefuseRerun(ns, "orders-api", 5, true)
		setRerunRelease(ns, "1")
		warned := And(HaveField("Type", "Warning"), HaveField("Reason", "ReleaseRunRerunFailed"),
			HaveField("Note", "Could not re-run the failed runs of v0.1.0: re-run of run 5 refused; "+
				"set a new theos.factory.io/rerun-release value to try again"))
		Eventually(func(g Gomega) {
			status := getCrew(g, ns).Status
			g.Expect(status.RerunRelease).To(Equal("1"))
			g.Expect(crewEvents(g, ns)).To(ContainElement(warned))
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			status := getCrew(g, ns).Status
			g.Expect(status.LastRelease.Run).To(Equal("Failed"))
			g.Expect(status.LastRelease.RerunTime).To(BeNil())
			g.Expect(gitFake.Reruns(ns, "orders-api", 5)).To(Equal(1))
		}, 3*time.Second).Should(Succeed())

		gitFake.RefuseRerun(ns, "orders-api", 5, false)
		setRerunRelease(ns, "2")
		Eventually(func(g Gomega) {
			status := getCrew(g, ns).Status
			g.Expect(status.RerunRelease).To(Equal("2"))
			g.Expect(status.LastRelease.Run).To(Equal("Running"))
			g.Expect(crewEvents(g, ns)).To(ContainElement(And(HaveField("Type", "Normal"),
				HaveField("Reason", "ReleaseRunRerun"), HaveField("Note", "Re-ran the failed runs of v0.1.0: release.yaml"))))
		}).Should(Succeed())
		Expect(gitFake.Reruns(ns, "orders-api", 5)).To(Equal(2))
	})

	It("keeps a release Failed when the host refuses only some re-runs, and a new value re-runs those", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		crewAgents(ns, 1)
		mergedIssue(ns)
		crew := newCrew(ns)
		crew.Spec.TagReleases = true
		mustCreate(crew)
		Eventually(func(g Gomega) {
			g.Expect(getCrew(g, ns).Status.LastRelease).NotTo(BeNil())
		}).Should(Succeed())
		lint := ports.WorkflowRun{ID: 6, Workflow: "lint.yaml", Status: "completed", Conclusion: "failure"}
		release := ports.WorkflowRun{ID: 5, Workflow: "release.yaml", Status: "completed", Conclusion: "failure"}
		gitFake.SetRuns(ns, "orders-api", "v0.1.0", "5a-merge", lint, release)
		Eventually(func(g Gomega) {
			g.Expect(getCrew(g, ns).Status.LastRelease.Run).To(Equal("Failed"))
		}).Should(Succeed())

		// The host refuses the newer run; the older one re-runs all the same.
		gitFake.RefuseRerun(ns, "orders-api", 6, true)
		setRerunRelease(ns, "1")
		warned := And(HaveField("Type", "Warning"), HaveField("Reason", "ReleaseRunRerunFailed"),
			HaveField("Note", "Re-ran release.yaml of v0.1.0, but could not re-run lint.yaml: re-run of run 6 refused; "+
				"set a new theos.factory.io/rerun-release value to try those again"))
		Eventually(func(g Gomega) {
			status := getCrew(g, ns).Status
			g.Expect(status.RerunRelease).To(Equal("1"))
			g.Expect(status.LastRelease.RerunTime).NotTo(BeNil())
			g.Expect(crewEvents(g, ns)).To(ContainElement(warned))
		}).Should(Succeed())
		Expect(gitFake.Reruns(ns, "orders-api", 5)).To(Equal(1))
		Expect(gitFake.Reruns(ns, "orders-api", 6)).To(Equal(1))
		// lint.yaml still failed, so the release is Failed, and the watch says
		// nothing new about it.
		Consistently(func(g Gomega) {
			g.Expect(getCrew(g, ns).Status.LastRelease.Run).To(Equal("Failed"))
			var warnings []eventsv1.Event
			for _, e := range crewEvents(g, ns) {
				if e.Reason == "ReleaseRunFailed" {
					warnings = append(warnings, e)
				}
			}
			g.Expect(warnings).To(HaveLen(1))
		}, 3*time.Second).Should(Succeed())

		// A new value re-runs only the refused run: release.yaml is queued.
		gitFake.RefuseRerun(ns, "orders-api", 6, false)
		setRerunRelease(ns, "2")
		Eventually(func(g Gomega) {
			status := getCrew(g, ns).Status
			g.Expect(status.RerunRelease).To(Equal("2"))
			g.Expect(status.LastRelease.Run).To(Equal("Running"))
			g.Expect(crewEvents(g, ns)).To(ContainElement(And(HaveField("Type", "Normal"),
				HaveField("Reason", "ReleaseRunRerun"), HaveField("Note", "Re-ran the failed runs of v0.1.0: lint.yaml"))))
		}).Should(Succeed())
		Expect(gitFake.Reruns(ns, "orders-api", 5)).To(Equal(1))
		Expect(gitFake.Reruns(ns, "orders-api", 6)).To(Equal(2))
	})

	It("re-runs nothing for a rerun-release value set on a run that succeeded, and says so", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		crewAgents(ns, 1)
		mergedIssue(ns)
		crew := newCrew(ns)
		crew.Spec.TagReleases = true
		mustCreate(crew)
		Eventually(func(g Gomega) {
			g.Expect(getCrew(g, ns).Status.LastRelease).NotTo(BeNil())
		}).Should(Succeed())
		run := ports.WorkflowRun{ID: 5, Workflow: "release.yaml", Status: "completed", Conclusion: "success"}
		gitFake.SetRuns(ns, "orders-api", "v0.1.0", "5a-merge", run)
		Eventually(func(g Gomega) {
			g.Expect(getCrew(g, ns).Status.LastRelease.Run).To(Equal("Succeeded"))
		}).Should(Succeed())

		setRerunRelease(ns, "1")
		said := And(HaveField("Type", "Normal"), HaveField("Reason", "NoFailedReleaseRun"),
			HaveField("Note", "Release runs of v0.1.0 are Succeeded, not Failed, so there is nothing to re-run"))
		Eventually(func(g Gomega) {
			g.Expect(getCrew(g, ns).Status.RerunRelease).To(Equal("1"))
			g.Expect(crewEvents(g, ns)).To(ContainElement(said))
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			var notes []eventsv1.Event
			for _, e := range crewEvents(g, ns) {
				if e.Reason == "NoFailedReleaseRun" {
					notes = append(notes, e)
				}
			}
			g.Expect(notes).To(HaveLen(1))
			g.Expect(notes[0].Series).To(BeNil())
		}, 3*time.Second).Should(Succeed())
		Expect(gitFake.Reruns(ns, "orders-api", 5)).To(Equal(0))
	})

	It("reports release runs that haven't ended an hour after tagging, once, and their later end", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		crewAgents(ns, 1)
		mergedIssue(ns)
		crew := newCrew(ns)
		crew.Spec.TagReleases = true
		mustCreate(crew)

		runURL := "http://gitea/" + ns + "/orders-api/actions/runs/5"
		run := ports.WorkflowRun{ID: 5, Workflow: "release.yaml", Status: "queued", URL: runURL}
		gitFake.SetRuns(ns, "orders-api", "v0.1.0", "5a-merge", run)
		Eventually(func(g Gomega) {
			release := getCrew(g, ns).Status.LastRelease
			g.Expect(release).NotTo(BeNil())
			g.Expect(release.Run).To(Equal("Running"))
		}).Should(Succeed())
		Expect(crewEvents(Default, ns)).NotTo(ContainElement(HaveField("Reason", "ReleaseRunOverdue")))

		// No runner picks the run up; the release was tagged two hours ago.
		Eventually(func(g Gomega) {
			crew := getCrew(g, ns)
			crew.Status.LastRelease.Time = metav1.NewTime(time.Now().Add(-2 * time.Hour))
			g.Expect(k8sClient.Status().Update(ctx, crew)).To(Succeed())
		}).Should(Succeed())
		overdue := And(HaveField("Type", "Warning"), HaveField("Reason", "ReleaseRunOverdue"),
			HaveField("Note", "Release runs of v0.1.0 have not ended an hour after tagging, Running: "+runURL))
		Eventually(func(g Gomega) {
			crew := getCrew(g, ns)
			g.Expect(crew.Status.LastRelease.Run).To(Equal("Running"))
			cond := condition(crew.Status.Conditions, ConditionReleased)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Message).To(Equal("Released v0.1.0 at 5a-merge; its runs have not ended an hour after tagging"))
			g.Expect(crewEvents(g, ns)).To(ContainElement(overdue))
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			var warnings []eventsv1.Event
			for _, e := range crewEvents(g, ns) {
				if e.Reason == "ReleaseRunOverdue" {
					warnings = append(warnings, e)
				}
			}
			g.Expect(warnings).To(HaveLen(1))
			g.Expect(warnings[0].Series).To(BeNil())
		}, 3*time.Second).Should(Succeed())

		run.Status, run.Conclusion = "completed", "success"
		gitFake.SetRuns(ns, "orders-api", "v0.1.0", "5a-merge", run)
		Eventually(func(g Gomega) {
			crew := getCrew(g, ns)
			g.Expect(crew.Status.LastRelease.Run).To(Equal("Succeeded"))
			g.Expect(condition(crew.Status.Conditions, ConditionReleased).Message).
				To(Equal("Released v0.1.0 at 5a-merge"))
			g.Expect(crewEvents(g, ns)).To(ContainElement(And(HaveField("Type", "Normal"),
				HaveField("Reason", "ReleaseRunSucceeded"))))
		}).Should(Succeed())
	})
})

var _ = Describe("Crew release preparation", func() {
	// prepareIssues lists the open issues of orders-api titled "Prepare v0.1.0".
	prepareIssues := func(ns string) []fake.Issue {
		var out []fake.Issue
		for _, i := range gitFake.OpenIssues(ns, "orders-api") {
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
			g.Expect(issues[0].InMilestone).To(Equal("v0.1.0"))
			g.Expect(issues[0].Body).To(ContainSubstring("Validate orders"))
			g.Expect(crewEvents(g, ns)).To(ContainElement(And(HaveField("Type", "Normal"),
				HaveField("Reason", "PreparingRelease"), HaveField("Action", "PrepareRelease"))))
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			g.Expect(prepareIssues(ns)).To(HaveLen(1))
			crew := getCrew(g, ns)
			g.Expect(condition(crew.Status.Conditions, ConditionReleased)).To(BeNil(), "tagging is off")
			g.Expect(gitFake.Tag(ns, "orders-api", "v0.1.0")).To(BeEmpty())
		}, 3*time.Second).Should(Succeed())
	})

	It("files no prepare issue while a Task is at work, then one listing its work (#956)", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		crewAgents(ns, 1)
		mergedIssue(ns)
		// The next release's fix is in review when v0.1.0 is done; its merge
		// moves main under a prepare step started now.
		fix := gitFake.AddIssue(ns, "orders-api", "Track orders", clearIssue, nil)
		gitFake.SetIssueRelease(ns, "orders-api", fix, "v0.2.0", releaseDue.AddDate(0, 0, 7))
		task := newTask(ns, "orders-api-fix-2", "")
		task.Spec.AgentRef = c4v1beta1.LocalRef{Name: "nobody"}
		task.Spec.Issue = int32(fix)
		mustCreate(task)
		Eventually(func(g Gomega) {
			cond := condition(getTask(g, ns, task.Name).Status.Conditions, ConditionProgressing)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Reason).To(Equal("AgentNotFound"))
		}).Should(Succeed())
		Eventually(func(g Gomega) {
			task := getTask(g, ns, task.Name)
			task.Status.Phase = c4v1beta1.TaskReviewing
			g.Expect(k8sClient.Status().Update(ctx, task)).To(Succeed())
		}).Should(Succeed())
		crew := newCrew(ns)
		crew.Spec.Paused = true
		crew.Spec.PrepareReleases = true
		crew.Spec.TagReleases = true
		mustCreate(crew)

		Eventually(func(g Gomega) {
			cond := condition(getCrew(g, ns).Status.Conditions, ConditionReleased)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Reason).To(Equal(model.ReleasePrepareWaits))
			g.Expect(cond.Message).To(ContainSubstring("Task orders-api-fix-2 to end"))
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			g.Expect(prepareIssues(ns)).To(BeEmpty())
			g.Expect(getTask(g, ns, task.Name).Status.Phase).To(Equal(c4v1beta1.TaskReviewing))
			for _, e := range crewEvents(g, ns) {
				g.Expect(e.Type).NotTo(Equal("Warning"), "waiting for a Task is no warning")
			}
		}, 3*time.Second).Should(Succeed())

		// The Task closes its issue before it records the merge, as the Task
		// reconciler does: the other order let a reconcile between the two
		// file the prepare issue without the fix under load (#956, #1363).
		Expect(gitFake.CloseIssueByHand(ns, "orders-api", fix)).To(Succeed())
		Eventually(func(g Gomega) {
			task := getTask(g, ns, task.Name)
			completed := metav1.Now()
			task.Status.Phase, task.Status.MergeCommit, task.Status.CompletedAt = c4v1beta1.TaskMerged, "fix-merge", &completed
			g.Expect(k8sClient.Status().Update(ctx, task)).To(Succeed())
		}).Should(Succeed())
		Eventually(func(g Gomega) {
			issues := prepareIssues(ns)
			g.Expect(issues).To(HaveLen(1))
			g.Expect(issues[0].Body).To(ContainSubstring("Validate orders"))
			g.Expect(issues[0].Body).To(ContainSubstring("Track orders"))
		}).Should(Succeed())
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
			g.Expect(gitFake.Tag(ns, "orders-api", "v0.1.0")).To(BeEmpty(), "the release waits for its prepare issue")
			g.Expect(gitFake.MilestoneClosed(ns, "orders-api", "v0.1.0")).To(BeFalse())
			g.Expect(prepareIssues(ns)).To(HaveLen(1))
		}, 3*time.Second).Should(Succeed())
	})

	It("starts the prepare issue before the next release's work", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		crewAgents(ns, 1)
		mergedIssue(ns)
		next := gitFake.AddIssue(ns, "orders-api", "Ship orders", clearIssue, []string{"status/ready"})
		gitFake.SetIssueRelease(ns, "orders-api", next, "v0.2.0", releaseDue.AddDate(0, 0, 7))
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
			var task c4v1beta1.Task
			err := k8sClient.Get(ctx, client.ObjectKey{Namespace: ns, Name: fmt.Sprintf("orders-api-%d", next)}, &task)
			g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "v0.2.0 waits for v0.1.0's prepare issue")
		}, 3*time.Second).Should(Succeed())
	})

	It("tags a release whose prepare issue merged after later work, moves that work in, and goes on", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		crewAgents(ns, 1)
		start := time.Now().Add(-time.Hour)
		// issue files a closed issue in release, merged at commit minutes after start.
		issue := func(title, release string, due time.Time, labels []string, commit string, minutes int) int {
			n := gitFake.AddIssue(ns, "orders-api", title, clearIssue, labels)
			gitFake.SetIssueRelease(ns, "orders-api", n, release, due)
			Expect(gitFake.CloseIssueByHand(ns, "orders-api", n)).To(Succeed())
			mergedTask(ns, fmt.Sprintf("orders-api-merged-%d", n), n, commit, start.Add(time.Duration(minutes)*time.Minute))
			return n
		}
		next := releaseDue.AddDate(0, 0, 7)
		// v0.1.3's last fix was handed back; the Crew went on with v0.2.0, whose
		// first fix merged before v0.1.3's prepare issue.
		issue("Validate orders", "v0.1.3", releaseDue, nil, "c-fix", 1)
		early := issue("Ship orders", "v0.2.0", next, nil, "c-early", 2)
		issue("Prepare v0.1.3", "v0.1.3", releaseDue, []string{"kind/release"}, "c-prepare-013", 3)
		late := issue("Track orders", "v0.2.0", next, nil, "c-late", 4)
		crew := newCrew(ns)
		crew.Spec.Paused = true
		crew.Spec.PrepareReleases = true
		crew.Spec.TagReleases = true
		mustCreate(crew)

		var prepare fake.Issue
		Eventually(func(g Gomega) {
			g.Expect(gitFake.Tag(ns, "orders-api", "v0.1.3")).To(Equal("c-prepare-013"))
			g.Expect(gitFake.MilestoneClosed(ns, "orders-api", "v0.1.3")).To(BeTrue())
			moved := gitFake.Issue(ns, "orders-api", early)
			g.Expect(moved.InMilestone).To(Equal("v0.1.3"))
			g.Expect(moved.Comments).To(ConsistOf(ContainSubstring("Moved from v0.2.0 to v0.1.3")))
			g.Expect(gitFake.Issue(ns, "orders-api", late).InMilestone).To(Equal("v0.2.0"))
			g.Expect(gitFake.MilestoneClosed(ns, "orders-api", "v0.2.0")).To(BeFalse())
			g.Expect(crewEvents(g, ns)).To(ContainElement(And(HaveField("Type", "Normal"),
				HaveField("Reason", "Released"),
				HaveField("Note", fmt.Sprintf("Released v0.1.3 at c-prepare-013, carrying #%d of v0.2.0", early)))))
			// v0.2.0 is next, with only its own work.
			for _, i := range gitFake.OpenIssues(ns, "orders-api") {
				if i.Title == "Prepare v0.2.0" {
					prepare = i
				}
			}
			g.Expect(prepare.Number).NotTo(BeZero())
			g.Expect(prepare.InMilestone).To(Equal("v0.2.0"))
			g.Expect(prepare.Body).To(ContainSubstring("Track orders"))
			g.Expect(prepare.Body).NotTo(ContainSubstring("Ship orders"))
		}).Should(Succeed())

		// The merge is recorded before the issue closes, as when its Task merges.
		mergedTask(ns, "orders-api-merged-prepare-020", prepare.Number, "c-prepare-020", time.Now())
		Expect(gitFake.CloseIssueByHand(ns, "orders-api", prepare.Number)).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(gitFake.Tag(ns, "orders-api", "v0.2.0")).To(Equal("c-prepare-020"))
			g.Expect(gitFake.MilestoneClosed(ns, "orders-api", "v0.2.0")).To(BeTrue())
			cond := condition(getCrew(g, ns).Status.Conditions, ConditionReleased)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Message).To(Equal("Released v0.2.0 at c-prepare-020"))
		}).Should(Succeed())
		Expect(gitFake.Tag(ns, "orders-api", "v0.1.3")).To(Equal("c-prepare-013"), "a tag is never moved")
	})

	It("closes a later release left with no issue, untagged", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		crewAgents(ns, 1)
		start := time.Now().Add(-time.Hour)
		next := releaseDue.AddDate(0, 0, 7)
		fix := gitFake.AddIssue(ns, "orders-api", "Ship orders", clearIssue, nil)
		gitFake.SetIssueRelease(ns, "orders-api", fix, "v0.2.0", next)
		Expect(gitFake.CloseIssueByHand(ns, "orders-api", fix)).To(Succeed())
		mergedTask(ns, "orders-api-merged-early", fix, "c-early", start)
		own := gitFake.AddIssue(ns, "orders-api", "Validate orders", clearIssue, nil)
		gitFake.SetIssueRelease(ns, "orders-api", own, "v0.1.3", releaseDue)
		Expect(gitFake.CloseIssueByHand(ns, "orders-api", own)).To(Succeed())
		mergedTask(ns, "orders-api-merged-own", own, "c-own", start.Add(time.Minute))
		crew := newCrew(ns)
		crew.Spec.Paused = true
		crew.Spec.TagReleases = true
		mustCreate(crew)

		Eventually(func(g Gomega) {
			g.Expect(gitFake.Tag(ns, "orders-api", "v0.1.3")).To(Equal("c-own"))
			g.Expect(gitFake.Issue(ns, "orders-api", fix).InMilestone).To(Equal("v0.1.3"))
			g.Expect(gitFake.MilestoneClosed(ns, "orders-api", "v0.1.3")).To(BeTrue())
			g.Expect(gitFake.MilestoneClosed(ns, "orders-api", "v0.2.0")).To(BeTrue())
			g.Expect(gitFake.Tag(ns, "orders-api", "v0.2.0")).To(BeEmpty())
			cond := condition(getCrew(g, ns).Status.Conditions, ConditionReleased)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Message).To(Equal(fmt.Sprintf("Released v0.1.3 at c-own, carrying #%d of v0.2.0; "+
				"v0.2.0 had nothing left and closed untagged", fix)))
		}).Should(Succeed())
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
