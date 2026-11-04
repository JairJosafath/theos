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

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

var _ = Describe("The deploy monitor", func() {
	It("files one bug for a degraded deploy, and none again for the same state of the same version", func() {
		ns := newProjectNamespace()
		deployingContainer(ns)
		readyTarget(ns, "staging", c4v1beta1.DeployTierDevelopment)
		shipRelease(ns, "v0.1.0", true)
		setDeploy(ns, c4v1beta1.ContainerDeploy{Target: "staging"})
		state := func(health, message string) {
			deployFake.SetState(targetName(ns, "staging"), ns+"/orders-api", ports.DeployState{Version: "v0.1.0",
				Phase: ports.PhaseDeployed, Health: health, Message: message})
		}

		By("filing nothing while it is healthy")
		state(ports.HealthHealthy, "")
		deployIs(ns, "staging", model.DeployPhaseDeployed, model.DeployDeployed, "v0.1.0", model.DeployDeployed)
		Expect(gitFake.IssueCount(ns, "orders-api")).To(BeZero())

		By("filing the bug once it turns Degraded")
		state(ports.HealthDegraded, "2 of 2 pods crash-loop")
		var number int32
		Eventually(func(g Gomega) {
			d, _ := deployOf(g, ns, "staging")
			g.Expect(d.Issue).NotTo(BeNil())
			number = d.Issue.Number
			g.Expect(d.Issue.Version).To(Equal("v0.1.0"))
			g.Expect(d.Issue.State).To(Equal(model.DeployMonitorDegraded))
		}).Should(Succeed())
		Expect(gitFake.IssueCount(ns, "orders-api")).To(Equal(1))
		issue := gitFake.Issue(ns, "orders-api", int(number))
		Expect(issue.Title).To(Equal("Deploy v0.1.0 to staging is Degraded"))
		Expect(issue.Labels).To(ConsistOf(model.LabelBug, model.LabelProposed))
		Expect(issue.Body).To(ContainSubstring("staging runs v0.1.0"))
		Expect(model.HasDeployMonitorMarker(issue.Body, "staging", "v0.1.0", model.DeployMonitorDegraded)).To(BeTrue())

		By("filing nothing on later passes, nor when it recovers and degrades again")
		state(ports.HealthHealthy, "")
		Eventually(func(g Gomega) {
			d, _ := deployOf(g, ns, "staging")
			g.Expect(d.Health).To(Equal(model.DeployHealthHealthy))
			g.Expect(d.Issue).NotTo(BeNil())
			g.Expect(d.Issue.Number).To(Equal(number))
		}).Should(Succeed())
		state(ports.HealthDegraded, "2 of 2 pods crash-loop")
		Eventually(func(g Gomega) {
			d, _ := deployOf(g, ns, "staging")
			g.Expect(d.Health).To(Equal(model.DeployHealthDegraded))
		}).Should(Succeed())
		Consistently(func() int { return gitFake.IssueCount(ns, "orders-api") }).Should(Equal(1))
	})

	It("files a bug for a failed deploy, and not twice across a lost status", func() {
		ns := newProjectNamespace()
		deployingContainer(ns)
		readyTarget(ns, "staging", c4v1beta1.DeployTierDevelopment)
		deployFake.HoldAt(targetName(ns, "staging"))
		shipRelease(ns, "v0.1.0", true)
		setDeploy(ns, c4v1beta1.ContainerDeploy{Target: "staging"})
		deployIs(ns, "staging", model.DeployPhaseDeploying, model.DeployDeploying, "", model.DeployDeploying)
		deployFake.SetState(targetName(ns, "staging"), ns+"/orders-api", ports.DeployState{Version: "v0.1.0",
			Phase: ports.PhaseFailed, Health: ports.HealthDegraded, Reason: "RolledBack",
			Message: "the new version failed its health check"})
		deployIs(ns, "staging", model.DeployPhaseFailed, "RolledBack", "", model.DeployFailed)
		Eventually(func() int { return gitFake.IssueCount(ns, "orders-api") }).Should(Equal(1))
		open := gitFake.OpenIssues(ns, "orders-api")
		Expect(open).To(HaveLen(1))
		Expect(open[0].Title).To(Equal("Deploy v0.1.0 to staging is Failed"))
		Expect(open[0].Body).To(ContainSubstring("<!-- theos-monitor: staging v0.1.0 Failed -->"))
		Consistently(func() int { return gitFake.IssueCount(ns, "orders-api") }).Should(Equal(1))
	})

	It("looks the marker up on the Git host before it files", func() {
		ns := newProjectNamespace()
		deployingContainer(ns)
		readyTarget(ns, "staging", c4v1beta1.DeployTierDevelopment)
		shipRelease(ns, "v0.1.0", true)
		n := gitFake.AddIssue(ns, "orders-api", "Deploy v0.1.0 to staging is Degraded",
			"Seen before.\n\n"+model.DeployMonitorMarker("staging", "v0.1.0", model.DeployMonitorDegraded)+"\n",
			[]string{model.LabelBug, model.LabelProposed})
		deployFake.SetState(targetName(ns, "staging"), ns+"/orders-api", ports.DeployState{Version: "v0.1.0",
			Phase: ports.PhaseDeployed, Health: ports.HealthDegraded})
		setDeploy(ns, c4v1beta1.ContainerDeploy{Target: "staging"})
		Eventually(func(g Gomega) {
			d, _ := deployOf(g, ns, "staging")
			g.Expect(d.Issue).NotTo(BeNil())
			g.Expect(int(d.Issue.Number)).To(Equal(n))
		}).Should(Succeed())
		Expect(gitFake.IssueCount(ns, "orders-api")).To(Equal(1))
	})
})
