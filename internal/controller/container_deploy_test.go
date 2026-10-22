/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	eventsv1 "k8s.io/api/events/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/model"
	"github.com/JairJosafath/c4factory/internal/ports"
	"github.com/JairJosafath/c4factory/internal/ports/fake"
)

// testDeployMember is the model's member table with one need no real member
// has yet, so a spec can see Incompatible: the github member wants a
// Framework that leaves deploying to the target.
func testDeployMember(name string) (model.DeployMember, error) {
	m, err := model.DeployMemberByName(name)
	if err == nil && name == model.DeployMemberGitHub {
		m.Needs = []model.DeployNeed{{What: "a Framework that leaves deploying to the target",
			Met: func(c model.DeployContainer) bool { return !c.DeploysItself }}}
	}
	return m, err
}

// deployingContainer makes Ready Container orders-api in ns and its Crew,
// which has no Agents and so only waits: a spec sets its last release.
func deployingContainer(ns string) {
	GinkgoHelper()
	readyContainer(ns)
	mustCreate(newCrew(ns))
}

// shipRelease puts release tag of orders-api in Gitea, with files when withFiles,
// and makes it the Crew's last release, its runs succeeded.
func shipRelease(ns, tag string, withFiles bool) {
	GinkgoHelper()
	var files []fakeFile
	if withFiles {
		files = []fakeFile{{"orders-api-" + tag + ".tar.gz", []byte("the build of " + tag)}}
	}
	gitFake.setGiteaRelease(ns, "orders-api", tag, "Notes of "+tag, files...)
	crewRelease(ns, tag, model.RunSucceeded)
}

// crewRelease makes tag orders-api's Crew's last release, its runs run.
func crewRelease(ns, tag, run string) {
	GinkgoHelper()
	crewReleaseOf(ns, "orders-api", tag, run)
}

// crewReleaseOf makes tag the last release of Crew name, its runs run.
func crewReleaseOf(ns, name, tag, run string) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		crew := &c4v1alpha1.Crew{}
		g.Expect(k8sClient.Get(ctx, key(ns, name), crew)).To(Succeed())
		crew.Status.LastRelease = &c4v1alpha1.CrewRelease{Tag: tag, Commit: "c-" + tag, Time: metav1.Now(),
			Run: run}
		g.Expect(k8sClient.Status().Update(ctx, crew)).To(Succeed())
	}).Should(Succeed())
}

// readyTarget makes DeployTarget name in ns at tier, and waits for it to be
// Ready.
func readyTarget(ns, name string, tier c4v1alpha1.DeployTier) {
	GinkgoHelper()
	dt := newDeployTarget(ns, name, name+"-github")
	dt.Spec.Tier = tier
	mustCreate(newDeploySecret(ns, name+"-github", map[string]string{"token": "t0ken"}), dt)
	deployReady(dt, metav1.ConditionTrue, model.DeployTargetReady)
}

// setDeploy sets orders-api's spec.deploy.
func setDeploy(ns string, deploy ...c4v1alpha1.ContainerDeploy) {
	GinkgoHelper()
	setDeployOf(ns, "orders-api", deploy...)
}

// setDeployOf sets Container name's spec.deploy.
func setDeployOf(ns, name string, deploy ...c4v1alpha1.ContainerDeploy) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		container := &c4v1alpha1.Container{}
		g.Expect(k8sClient.Get(ctx, key(ns, name), container)).To(Succeed())
		container.Spec.Deploy = deploy
		g.Expect(k8sClient.Update(ctx, container)).To(Succeed())
	}).Should(Succeed())
}

// deployOf returns orders-api's status of target, and its Deployed condition.
func deployOf(g Gomega, ns, target string) (*c4v1alpha1.ContainerDeployStatus, *metav1.Condition) {
	return deployOfContainer(g, ns, "orders-api", target)
}

// deployOfContainer returns Container name's status of target, and its
// Deployed condition.
func deployOfContainer(g Gomega, ns, name, target string) (*c4v1alpha1.ContainerDeployStatus, *metav1.Condition) {
	container := &c4v1alpha1.Container{}
	g.Expect(k8sClient.Get(ctx, key(ns, name), container)).To(Succeed())
	cond := condition(container.Status.Conditions, c4v1alpha1.ConditionDeployed)
	g.Expect(cond).NotTo(BeNil())
	for i := range container.Status.Deploys {
		if d := &container.Status.Deploys[i]; d.Target == target {
			return d, cond
		}
	}
	g.Expect(container.Status.Deploys).To(ContainElement(HaveField("Target", target)))
	return nil, cond
}

// deployIs waits for target's deploy to have phase, reason and version
// running, and the Deployed condition reason deployed.
func deployIs(ns, target, phase, reason, version, deployed string) *c4v1alpha1.ContainerDeployStatus {
	GinkgoHelper()
	return deployOfIs(ns, "orders-api", target, phase, reason, version, deployed)
}

// deployOfIs is deployIs for Container name.
func deployOfIs(ns, name, target, phase, reason, version, deployed string) *c4v1alpha1.ContainerDeployStatus {
	GinkgoHelper()
	var d *c4v1alpha1.ContainerDeployStatus
	Eventually(func(g Gomega) {
		var cond *metav1.Condition
		d, cond = deployOfContainer(g, ns, name, target)
		g.Expect(d.Phase).To(Equal(phase), d.Message)
		g.Expect(d.Reason).To(Equal(reason), d.Message)
		g.Expect(d.Version).To(Equal(version))
		g.Expect(cond.Reason).To(Equal(deployed), cond.Message)
		want := metav1.ConditionFalse
		if deployed == model.DeployDeployed {
			want = metav1.ConditionTrue
		}
		g.Expect(cond.Status).To(Equal(want))
	}, deployWait).Should(Succeed())
	return d
}

// containerEvents are the Events about orders-api with reason.
func containerEvents(g Gomega, ns, reason string) []eventsv1.Event {
	var events eventsv1.EventList
	g.Expect(k8sClient.List(ctx, &events, client.InNamespace(ns))).To(Succeed())
	var out []eventsv1.Event
	for _, e := range events.Items {
		if e.Regarding.Kind == "Container" && e.Regarding.Name == "orders-api" && e.Reason == reason {
			out = append(out, e)
		}
	}
	return out
}

// saidOnce waits for one Event about orders-api with reason whose note has
// note, and checks it stays one: a repeat would be a second Event or a
// series.
func saidOnce(ns, reason, note string) {
	GinkgoHelper()
	said := func(g Gomega) {
		var matching []eventsv1.Event
		for _, e := range containerEvents(g, ns, reason) {
			if strings.Contains(e.Note, note) {
				matching = append(matching, e)
			}
		}
		g.Expect(matching).To(HaveLen(1))
		g.Expect(matching[0].Series).To(BeNil())
	}
	Eventually(said, deployWait).Should(Succeed())
	Consistently(said, 3*time.Second).Should(Succeed())
}

func targetName(ns, name string) string { return ns + "/" + name }

var _ = Describe("Container deploys", func() {
	DescribeTable("refuses a spec.deploy its CEL rules or schema reject",
		func(deploy []c4v1alpha1.ContainerDeploy, message string) {
			ns := newProjectNamespace()
			container := newContainer(ns, "orders-api", "webshop-core", c4v1alpha1.ContainerTypeService)
			container.Spec.Deploy = deploy
			Expect(k8sClient.Create(ctx, container)).To(MatchError(ContainSubstring(message)))
		},
		Entry("a target twice", []c4v1alpha1.ContainerDeploy{{Target: "staging"}, {Target: "staging"}},
			"Duplicate value"),
		Entry("after a later entry", []c4v1alpha1.ContainerDeploy{{Target: "prod", After: "staging"},
			{Target: "staging"}}, "after must name an entry earlier in deploy"),
		Entry("after itself", []c4v1alpha1.ContainerDeploy{{Target: "prod", After: "prod"}},
			"after must name an entry earlier in deploy"),
		Entry("after no entry", []c4v1alpha1.ContainerDeploy{{Target: "prod", After: "staging"}},
			"after must name an entry earlier in deploy"),
		Entry("more than 5 entries", []c4v1alpha1.ContainerDeploy{{Target: "a"}, {Target: "b"}, {Target: "c"},
			{Target: "d"}, {Target: "e"}, {Target: "f"}}, "must have at most 5 items"),
		Entry("a version that isn't a tag", []c4v1alpha1.ContainerDeploy{{Target: "prod", Version: "v1 .0"}},
			"spec.deploy[0].version"),
		Entry("an approval that isn't a tag", []c4v1alpha1.ContainerDeploy{{Target: "prod", Approved: "-v1"}},
			"spec.deploy[0].approved"),
	)

	It("takes a chain of five entries", func() {
		ns := newProjectNamespace()
		container := newContainer(ns, "orders-api", "webshop-core", c4v1alpha1.ContainerTypeService)
		container.Spec.Deploy = []c4v1alpha1.ContainerDeploy{{Target: "a"}, {Target: "b", After: "a"},
			{Target: "c", After: "a", Version: "v1.2.0"}, {Target: "d", After: "c", Approved: "v1.2.0+build.1"},
			{Target: "e"}}
		Expect(k8sClient.Create(ctx, container)).To(Succeed())
	})

	It("ships each release whose runs succeeded, and a target added after the scaffold", func() {
		ns := newProjectNamespace()
		deployingContainer(ns)
		readyTarget(ns, "staging", c4v1alpha1.DeployTierDevelopment)
		setDeploy(ns, c4v1alpha1.ContainerDeploy{Target: "staging"})
		d := deployIs(ns, "staging", model.DeployPhaseWaiting, model.DeployNoRelease, "", model.DeployNoRelease)
		Expect(d.Message).To(ContainSubstring("No release of orders-api has succeeded its runs yet"))

		shipRelease(ns, "v0.1.0", true)
		d = deployIs(ns, "staging", model.DeployPhaseDeployed, model.DeployDeployed, "v0.1.0",
			model.DeployDeployed)
		Expect(d.WantedVersion).To(Equal("v0.1.0"))
		Expect(d.Health).To(Equal(model.DeployHealthHealthy))
		Expect(d.Since).NotTo(BeNil())
		saidOnce(ns, model.DeployEventDeployed, "staging runs v0.1.0")

		By("keeping the release that succeeded while a newer one's runs fail")
		crewRelease(ns, "v0.1.1", model.RunFailed)
		Consistently(func(g Gomega) {
			d, cond := deployOf(g, ns, "staging")
			g.Expect(d.WantedVersion).To(Equal("v0.1.0"))
			g.Expect(d.Phase).To(Equal(model.DeployPhaseDeployed))
			g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		}, 3*time.Second).Should(Succeed())

		By("streaming the files from the Git host's release")
		shipped := deployFake.Shipments()
		var ours []fake.Shipped
		for _, s := range shipped {
			if s.Target == targetName(ns, "staging") {
				ours = append(ours, s)
			}
		}
		Expect(ours).To(HaveLen(1))
		s := ours[0].Shipment
		Expect(s.App).To(Equal(ns + "/orders-api"))
		Expect(s.Version).To(Equal("v0.1.0"))
		Expect(s.Notes).To(Equal("Notes of v0.1.0"))
		Expect(s.Files).To(HaveLen(1))
		body, _, err := s.Files[0].Open(context.Background())
		Expect(err).NotTo(HaveOccurred())
		content, err := io.ReadAll(body)
		Expect(err).NotTo(HaveOccurred())
		Expect(body.Close()).To(Succeed())
		Expect(string(content)).To(Equal("the build of v0.1.0"))

		By("adding a target, which ships the newest release with no new scaffold")
		var before c4v1alpha1.Container
		Expect(k8sClient.Get(ctx, key(ns, "orders-api"), &before)).To(Succeed())
		readyTarget(ns, "oss", c4v1alpha1.DeployTierDevelopment)
		setDeploy(ns, c4v1alpha1.ContainerDeploy{Target: "staging"}, c4v1alpha1.ContainerDeploy{Target: "oss"})
		deployIs(ns, "oss", model.DeployPhaseDeployed, model.DeployDeployed, "v0.1.0", model.DeployDeployed)
		var after c4v1alpha1.Container
		Expect(k8sClient.Get(ctx, key(ns, "orders-api"), &after)).To(Succeed())
		Expect(after.Status.Phase).To(Equal(c4v1alpha1.ContainerReady))
		Expect(after.Status.ScaffoldedFrom).To(Equal(before.Status.ScaffoldedFrom))
		Expect(deployFake.Deploys(targetName(ns, "staging"), ns+"/orders-api")).To(Equal(1))

		By("shipping the next release to both")
		shipRelease(ns, "v0.2.0", true)
		deployIs(ns, "staging", model.DeployPhaseDeployed, model.DeployDeployed, "v0.2.0", model.DeployDeployed)
		deployIs(ns, "oss", model.DeployPhaseDeployed, model.DeployDeployed, "v0.2.0", model.DeployDeployed)

		By("rolling back by a pin")
		setDeploy(ns, c4v1alpha1.ContainerDeploy{Target: "staging", Version: "v0.1.0"},
			c4v1alpha1.ContainerDeploy{Target: "oss"})
		deployIs(ns, "staging", model.DeployPhaseDeployed, model.DeployDeployed, "v0.1.0", model.DeployDeployed)
		Expect(deployFake.Deploys(targetName(ns, "oss"), ns+"/orders-api")).To(Equal(2))

		By("dropping the status and the condition once spec.deploy is empty")
		setDeploy(ns)
		Eventually(func(g Gomega) {
			var c c4v1alpha1.Container
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), &c)).To(Succeed())
			g.Expect(c.Status.Deploys).To(BeEmpty())
			g.Expect(condition(c.Status.Conditions, c4v1alpha1.ConditionDeployed)).To(BeNil())
		}, deployWait).Should(Succeed())
	})

	It("ships two Containers to one github target under their names, and bare tags when tagPrefix is empty (#1486)", func() {
		ns := newProjectNamespace()
		deployingContainer(ns)
		cli := newContainer(ns, "orders-cli", "webshop-core", c4v1alpha1.ContainerTypeService)
		cli.Spec.StandardRef = &c4v1alpha1.LocalRef{Name: "python-strict"}
		crew := newCrew(ns)
		crew.Name, crew.Spec.ContainerRef.Name = "orders-cli", "orders-cli"
		mustCreate(cli, crew)
		shared, bare := "acme/"+ns+"-shared", "acme/"+ns+"-bare"
		githubFake.addRepository(shared, "t0ken")
		githubFake.addRepository(bare, "t0ken")
		for name, repository := range map[string]string{"shared": shared, "bare": bare} {
			dt := newDeployTarget(ns, name, name+"-github")
			dt.Spec.GitHub.Repository = repository
			if name == "bare" {
				dt.Spec.GitHub.TagPrefix = new("")
			}
			mustCreate(newDeploySecret(ns, name+"-github", map[string]string{"token": "t0ken"}), dt)
			deployReady(dt, metav1.ConditionTrue, model.DeployTargetReady)
		}
		shipRelease(ns, "v0.1.0", true)
		gitFake.setGiteaRelease(ns, "orders-cli", "v0.1.0", "Notes of v0.1.0",
			fakeFile{"orders-cli-v0.1.0.tar.gz", []byte("the build of v0.1.0")})
		crewReleaseOf(ns, "orders-cli", "v0.1.0", model.RunSucceeded)
		setDeploy(ns, c4v1alpha1.ContainerDeploy{Target: "shared"}, c4v1alpha1.ContainerDeploy{Target: "bare"})
		setDeployOf(ns, "orders-cli", c4v1alpha1.ContainerDeploy{Target: "shared"})

		published := func(repository string) []string {
			var tags []string
			for _, r := range githubFake.releases(repository) {
				if !r.Draft {
					tags = append(tags, r.TagName)
				}
			}
			return tags
		}
		Eventually(func(g Gomega) {
			for _, name := range []string{"orders-api", "orders-cli"} {
				d, cond := deployOfContainer(g, ns, name, "shared")
				g.Expect(d.Phase).To(Equal(model.DeployPhaseDeployed), d.Message)
				g.Expect(d.Version).To(Equal("v0.1.0"))
				g.Expect(cond.Reason).To(Equal(model.DeployDeployed), cond.Message)
			}
			g.Expect(published(shared)).To(ConsistOf("orders-api-v0.1.0", "orders-cli-v0.1.0"))
			g.Expect(published(bare)).To(ConsistOf("v0.1.0"))
		}, deployWait).Should(Succeed())
	})

	It("scaffolds another Container while one Container's Ship is still uploading (#1446)", func() {
		ns := newProjectNamespace()
		deployingContainer(ns)
		readyTarget(ns, "oss", c4v1alpha1.DeployTierDevelopment)
		release := deployFake.Block(targetName(ns, "oss"))
		DeferCleanup(release)
		shipRelease(ns, "v0.1.0", true)
		setDeploy(ns, c4v1alpha1.ContainerDeploy{Target: "oss"})
		Eventually(func() int { return deployFake.Blocked(targetName(ns, "oss")) }, deployWait).Should(Equal(1))

		other := newContainer(ns, "orders-cli", "webshop-core", c4v1alpha1.ContainerTypeService)
		other.Spec.StandardRef = &c4v1alpha1.LocalRef{Name: "python-strict"}
		mustCreate(other)
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-cli"), other)).To(Succeed())
			g.Expect(other.Status.Phase).To(Equal(c4v1alpha1.ContainerReady))
		}).Should(Succeed())
		Expect(deployFake.Blocked(targetName(ns, "oss"))).To(Equal(1))

		By("finishing the upload, which deploys the first Container")
		release()
		deployIs(ns, "oss", model.DeployPhaseDeployed, model.DeployDeployed, "v0.1.0", model.DeployDeployed)
	})

	It("ships to staging, then to prod once staging runs it and a person approves", func() {
		ns := newProjectNamespace()
		deployingContainer(ns)
		readyTarget(ns, "staging", c4v1alpha1.DeployTierDevelopment)
		readyTarget(ns, "prod", c4v1alpha1.DeployTierProduction)
		deployFake.HoldAt(targetName(ns, "staging"))
		shipRelease(ns, "v0.1.0", true)
		setDeploy(ns, c4v1alpha1.ContainerDeploy{Target: "staging"},
			c4v1alpha1.ContainerDeploy{Target: "prod", After: "staging"})

		By("deploying to staging while prod waits for it")
		deployIs(ns, "staging", model.DeployPhaseDeploying, model.DeployDeploying, "", model.DeployWaitingForPrevious)
		d := deployIs(ns, "prod", model.DeployPhaseWaiting, model.DeployWaitingForPrevious, "",
			model.DeployWaitingForPrevious)
		Expect(d.Message).To(Equal("v0.1.0 waits for staging to run it before it ships to prod"))
		Expect(deployFake.Deploys(targetName(ns, "prod"), ns+"/orders-api")).To(BeZero())

		By("asking for approval once staging runs it")
		deployFake.SetState(targetName(ns, "staging"), ns+"/orders-api", ports.DeployState{Version: "v0.1.0",
			Phase: ports.PhaseDeployed, Health: ports.HealthHealthy, URL: "https://staging.example.com"})
		d = deployIs(ns, "staging", model.DeployPhaseDeployed, model.DeployDeployed, "v0.1.0",
			model.DeployWaitingForApproval)
		Expect(d.URL).To(Equal("https://staging.example.com"))
		deployIs(ns, "prod", model.DeployPhaseWaiting, model.DeployWaitingForApproval, "",
			model.DeployWaitingForApproval)
		saidOnce(ns, model.DeployEventApprovalNeeded, "v0.1.0 waits for a person's approval to ship to prod")
		Expect(deployFake.Deploys(targetName(ns, "prod"), ns+"/orders-api")).To(BeZero())

		By("shipping once approved")
		setDeploy(ns, c4v1alpha1.ContainerDeploy{Target: "staging"},
			c4v1alpha1.ContainerDeploy{Target: "prod", After: "staging", Approved: "v0.1.0"})
		deployIs(ns, "prod", model.DeployPhaseDeployed, model.DeployDeployed, "v0.1.0", model.DeployDeployed)
	})

	It("doesn't ship again a rollout the tool failed, and ships the next release", func() {
		ns := newProjectNamespace()
		deployingContainer(ns)
		readyTarget(ns, "staging", c4v1alpha1.DeployTierDevelopment)
		deployFake.HoldAt(targetName(ns, "staging"))
		shipRelease(ns, "v0.1.0", true)
		setDeploy(ns, c4v1alpha1.ContainerDeploy{Target: "staging"})
		deployIs(ns, "staging", model.DeployPhaseDeploying, model.DeployDeploying, "", model.DeployDeploying)

		deployFake.SetState(targetName(ns, "staging"), ns+"/orders-api", ports.DeployState{Version: "v0.1.0",
			Phase: ports.PhaseFailed, Health: ports.HealthDegraded, Reason: "RolledBack",
			Message: "the new version failed its health check"})
		d := deployIs(ns, "staging", model.DeployPhaseFailed, "RolledBack", "", model.DeployFailed)
		Expect(d.Health).To(Equal(model.DeployHealthDegraded))
		saidOnce(ns, model.DeployEventFailed, "the new version failed its health check")
		Expect(deployFake.Deploys(targetName(ns, "staging"), ns+"/orders-api")).To(Equal(1))

		shipRelease(ns, "v0.2.0", true)
		deployIs(ns, "staging", model.DeployPhaseDeploying, model.DeployDeploying, "", model.DeployDeploying)
		Expect(deployFake.Deploys(targetName(ns, "staging"), ns+"/orders-api")).To(Equal(2))
	})

	It("waits for a deploy in flight before it ships a newer release", func() {
		ns := newProjectNamespace()
		deployingContainer(ns)
		readyTarget(ns, "staging", c4v1alpha1.DeployTierDevelopment)
		deployFake.HoldAt(targetName(ns, "staging"))
		shipRelease(ns, "v0.1.0", true)
		setDeploy(ns, c4v1alpha1.ContainerDeploy{Target: "staging"})
		deployIs(ns, "staging", model.DeployPhaseDeploying, model.DeployDeploying, "", model.DeployDeploying)

		shipRelease(ns, "v0.2.0", true)
		Eventually(func(g Gomega) {
			d, _ := deployOf(g, ns, "staging")
			g.Expect(d.WantedVersion).To(Equal("v0.2.0"))
			g.Expect(d.Message).To(Equal("Deploying v0.1.0 to staging; v0.2.0 ships once it ends"))
		}, deployWait).Should(Succeed())
		Consistently(func() int {
			return deployFake.Deploys(targetName(ns, "staging"), ns+"/orders-api")
		}, 2*time.Second).Should(Equal(1))

		deployFake.SetState(targetName(ns, "staging"), ns+"/orders-api", ports.DeployState{Version: "v0.1.0",
			Phase: ports.PhaseDeployed, Health: ports.HealthHealthy})
		Eventually(func() int {
			return deployFake.Deploys(targetName(ns, "staging"), ns+"/orders-api")
		}, deployWait).Should(Equal(2))
	})

	It("says when a release lacks what the target needs, and ships it again only for another version", func() {
		ns := newProjectNamespace()
		deployingContainer(ns)
		readyTarget(ns, "staging", c4v1alpha1.DeployTierDevelopment)
		shipRelease(ns, "v0.1.0", false)
		setDeploy(ns, c4v1alpha1.ContainerDeploy{Target: "staging"})
		d := deployIs(ns, "staging", model.DeployPhaseFailed, model.DeployArtifactMissing, "", model.DeployFailed)
		Expect(d.Message).To(ContainSubstring("Release v0.1.0 lacks what DeployTarget staging needs"))
		saidOnce(ns, model.DeployEventFailed, "Release v0.1.0 lacks")

		shipRelease(ns, "v0.2.0", true)
		deployIs(ns, "staging", model.DeployPhaseDeployed, model.DeployDeployed, "v0.2.0", model.DeployDeployed)
	})

	It("tries again a call the target never had", func() {
		ns := newProjectNamespace()
		deployingContainer(ns)
		readyTarget(ns, "staging", c4v1alpha1.DeployTierDevelopment)
		shipRelease(ns, "v0.1.0", true)
		unreachable := &ports.Error{Kind: ports.ErrUnreachable, Cause: fmt.Errorf("fake: 502 from GitHub")}
		deployFake.Fail(targetName(ns, "staging"), unreachable)
		setDeploy(ns, c4v1alpha1.ContainerDeploy{Target: "staging"})
		d := deployIs(ns, "staging", model.DeployPhaseWaiting, model.DeployTargetNotReady, "",
			model.DeployTargetNotReady)
		Expect(d.Message).To(ContainSubstring("502 from GitHub; asked again on the next poll"))

		deployFake.Fail(targetName(ns, "staging"), nil)
		deployIs(ns, "staging", model.DeployPhaseDeployed, model.DeployDeployed, "v0.1.0", model.DeployDeployed)
	})

	It("says when a target is missing, not ready, or can't take the Container", func() {
		ns := newProjectNamespace()
		deployingContainer(ns)
		shipRelease(ns, "v0.1.0", true)
		setDeploy(ns, c4v1alpha1.ContainerDeploy{Target: "staging"})
		d := deployIs(ns, "staging", model.DeployPhaseWaiting, model.DeployTargetNotReady, "",
			model.DeployTargetNotReady)
		Expect(d.Message).To(Equal("DeployTarget staging not found"))

		By("a target whose credential is refused")
		deployFake.Refuse(targetName(ns, "staging"))
		dt := newDeployTarget(ns, "staging", "staging-github")
		mustCreate(newDeploySecret(ns, "staging-github", map[string]string{"token": "t0ken"}), dt)
		Eventually(func(g Gomega) {
			d, _ := deployOf(g, ns, "staging")
			g.Expect(d.Message).To(ContainSubstring("DeployTarget staging is not ready: GitHub refused the credential"))
		}, deployWait).Should(Succeed())
		deployIs(ns, "staging", model.DeployPhaseWaiting, model.DeployTargetNotReady, "", model.DeployTargetNotReady)

		By("a Framework the target can't take, first in the condition")
		Eventually(func(g Gomega) {
			framework := &c4v1alpha1.Framework{}
			g.Expect(k8sClient.Get(ctx, key(ns, "python-fastapi"), framework)).To(Succeed())
			framework.Spec.DeploysItself = true
			g.Expect(k8sClient.Update(ctx, framework)).To(Succeed())
		}).Should(Succeed())
		readyTarget(ns, "oss", c4v1alpha1.DeployTierDevelopment)
		setDeploy(ns, c4v1alpha1.ContainerDeploy{Target: "staging"}, c4v1alpha1.ContainerDeploy{Target: "oss"})
		d = deployIs(ns, "oss", model.DeployPhaseWaiting, model.DeployIncompatible, "", model.DeployIncompatible)
		Expect(d.Message).To(Equal("Container orders-api can't ship to DeployTarget oss: it lacks a Framework " +
			"that leaves deploying to the target"))
		Expect(deployFake.Deploys(targetName(ns, "oss"), ns+"/orders-api")).To(BeZero())
	})
})
