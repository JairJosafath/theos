/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/adapters/argocd"
	"github.com/JairJosafath/c4factory/internal/gitprovider"
	"github.com/JairJosafath/c4factory/internal/model"
)

// newArgoNamespace makes a fresh namespace standing for one where Argo CD
// runs, with no AppProject yet, and returns its name. Each spec gets its own,
// so none sees another's Applications.
func newArgoNamespace() string {
	GinkgoHelper()
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "argocd-"}}
	Expect(k8sClient.Create(ctx, ns)).To(Succeed())
	return ns.Name
}

// argoProject puts AppProject default in argoNS.
func argoProject(argoNS string) {
	GinkgoHelper()
	project := &unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{"name": model.ArgoCDDefaultProject, "namespace": argoNS},
		"spec":     map[string]any{},
	}}
	project.SetGroupVersionKind(argocd.AppProjectGVK)
	Expect(k8sClient.Create(ctx, project)).To(Succeed())
}

// readyArgoTarget makes argoCD DeployTarget dev in ns, its Argo CD in a
// fresh namespace holding AppProject default, waits for it to be Ready, and
// returns the Argo CD namespace.
func readyArgoTarget(ns string) string {
	GinkgoHelper()
	argoNS := newArgoNamespace()
	argoProject(argoNS)
	dt := newArgoCDTarget(ns, "dev", argoNS, "apps")
	mustCreate(dt)
	deployReady(dt, metav1.ConditionTrue, model.DeployTargetReady)
	return argoNS
}

// argoKey is orders-api's Application in argoNS, as the adapter names it.
func argoKey(ns, argoNS string) types.NamespacedName {
	GinkgoHelper()
	name, err := model.ArgoCDApplicationName(ns + "/orders-api")
	Expect(err).NotTo(HaveOccurred())
	return key(argoNS, name)
}

// argoApplication waits for orders-api's Application in argoNS and returns it.
func argoApplication(ns, argoNS string) *unstructured.Unstructured {
	GinkgoHelper()
	app := &unstructured.Unstructured{}
	app.SetGroupVersionKind(argocd.ApplicationGVK)
	Eventually(func() error {
		return k8sClient.Get(ctx, argoKey(ns, argoNS), app)
	}, deployWait).Should(Succeed())
	return app
}

// argoStatus is what a fake Argo CD writes to an Application's status.
type argoStatus struct {
	sync, operation, operationMessage, health string
}

// setArgoStatus writes s to orders-api's Application in argoNS as Argo CD
// would: the Application CRD has no status subresource, so a plain Update
// writes it, and comparedTo and operationState carry what the CRD requires.
func setArgoStatus(ns, argoNS string, s argoStatus) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		app := argoApplication(ns, argoNS)
		source, _, _ := unstructured.NestedMap(app.Object, "spec", "source")
		destination, _, _ := unstructured.NestedMap(app.Object, "spec", "destination")
		revision, _, _ := unstructured.NestedString(app.Object, "spec", "source", "targetRevision")
		app.Object["status"] = map[string]any{
			"sync": map[string]any{"status": s.sync,
				"comparedTo": map[string]any{"source": source, "destination": destination}},
			"health": map[string]any{"status": s.health},
			"operationState": map[string]any{"phase": s.operation, "message": s.operationMessage,
				"operation": map[string]any{"sync": map[string]any{"revision": revision}},
				"startedAt": time.Now().UTC().Format(time.RFC3339)},
		}
		g.Expect(k8sClient.Update(ctx, app)).To(Succeed())
	}, deployWait).Should(Succeed())
}

// readyCrewContainer makes Container name of a fresh factory in ns, changed
// by change, with its Crew, and waits for it to be Ready. It has no Standard,
// so its name is free: nothing else is named after it.
func readyCrewContainer(ns, name string, change func(*c4v1alpha1.Container)) {
	GinkgoHelper()
	createFactory(ns)
	container := newContainer(ns, name, "webshop-core", c4v1alpha1.ContainerTypeService)
	if change != nil {
		change(container)
	}
	mustCreate(newSystem(ns, "webshop-core"), container, newCrewOf(ns, name))
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, key(ns, name), container)).To(Succeed())
		g.Expect(container.Status.Phase).To(Equal(c4v1alpha1.ContainerReady))
	}).Should(Succeed())
}

// nameRejected waits for Container name's deploy to target to be Failed,
// reason NameRejected, condition DeployFailed, and returns it (#1677).
func nameRejected(ns, name, target string) *c4v1alpha1.ContainerDeployStatus {
	GinkgoHelper()
	return deployOfIs(ns, name, target, model.DeployPhaseFailed, model.DeployNameRejected, "", model.DeployFailed)
}

var _ = Describe("Container deploys to Argo CD", func() {
	It("writes the Application with the image digest and shows what Argo CD reports (#1588)", func() {
		ns := newProjectNamespace()
		deployingContainer(ns)
		gitFake.setImage(ns, "orders-api", "v0.1.0", gitprovider.Image{Outcome: gitprovider.ImagePublished,
			Digest: testDigest, Platforms: []string{"linux/amd64"}})
		shipRelease(ns, "v0.1.0", true)
		argoNS := readyArgoTarget(ns)
		setDeploy(ns, c4v1alpha1.ContainerDeploy{Target: "dev"})

		By("writing the Application from the release")
		app := argoApplication(ns, argoNS)
		Expect(app.Object).To(HaveKeyWithValue("spec", SatisfyAll(
			HaveKeyWithValue("project", model.ArgoCDDefaultProject),
			HaveKeyWithValue("source", SatisfyAll(
				HaveKeyWithValue("repoURL", "http://gitea.gitea.svc.cluster.local:3000/"+ns+"/orders-api.git"),
				HaveKeyWithValue("targetRevision", "v0.1.0"))),
			HaveKeyWithValue("destination", SatisfyAll(
				HaveKeyWithValue("server", model.ArgoCDDefaultServer),
				HaveKeyWithValue("namespace", "apps"))))))
		images, _, _ := unstructured.NestedStringSlice(app.Object, "spec", "source", "kustomize", "images")
		Expect(images).To(ConsistOf(ContainSubstring("@" + testDigest)))
		Expect(images[0]).To(HavePrefix("orders-api=" + imageRegistry + ns + "/orders-api@"))

		By("Deployed and Healthy once Argo CD synced it")
		setArgoStatus(ns, argoNS, argoStatus{sync: model.ArgoCDSyncSynced, operation: model.ArgoCDOperationSucceeded,
			health: model.ArgoCDHealthHealthy})
		d := deployIs(ns, "dev", model.DeployPhaseDeployed, model.DeployDeployed, "v0.1.0", model.DeployDeployed)
		Expect(d.Health).To(Equal(model.DeployHealthHealthy))
		Expect(d.Message).To(Equal("dev runs v0.1.0"))

		By("Degraded when Argo CD says so")
		setArgoStatus(ns, argoNS, argoStatus{sync: model.ArgoCDSyncSynced, operation: model.ArgoCDOperationSucceeded,
			health: model.ArgoCDHealthDegraded})
		Eventually(func(g Gomega) {
			d, _ := deployOf(g, ns, "dev")
			g.Expect(d.Health).To(Equal(model.DeployHealthDegraded))
			g.Expect(d.Phase).To(Equal(model.DeployPhaseDeployed))
		}, deployWait).Should(Succeed())

		By("Failed, reason SyncFailed, with Argo CD's message when the sync failed")
		setArgoStatus(ns, argoNS, argoStatus{sync: model.ArgoCDSyncOutOfSync, operation: model.ArgoCDOperationFailed,
			operationMessage: "one or more objects failed to apply", health: model.ArgoCDHealthDegraded})
		d = deployIs(ns, "dev", model.DeployPhaseFailed, model.DeployReasonSyncFailed, "v0.1.0", model.DeployFailed)
		Expect(d.Message).To(ContainSubstring("Argo CD reports deploying v0.1.0 to dev failed: " +
			"one or more objects failed to apply"))
	})

	It("is ArtifactMissing when the release has no image (#1588)", func() {
		ns := newProjectNamespace()
		deployingContainer(ns)
		shipRelease(ns, "v0.1.0", true)
		readyArgoTarget(ns)
		setDeploy(ns, c4v1alpha1.ContainerDeploy{Target: "dev"})
		d := deployIs(ns, "dev", model.DeployPhaseFailed, model.DeployArtifactMissing, "", model.DeployFailed)
		Expect(d.Message).To(ContainSubstring("the release has no image"))
	})

	It("can't take a Container without a Deployment that writes deploy/k8s/ (#1588)", func() {
		ns := newProjectNamespace()
		readyCrewContainer(ns, "orders-api", func(c *c4v1alpha1.Container) { c.Spec.DeploymentRef = nil })
		shipRelease(ns, "v0.1.0", true)
		argoNS := readyArgoTarget(ns)
		setDeploy(ns, c4v1alpha1.ContainerDeploy{Target: "dev"})
		d := deployIs(ns, "dev", model.DeployPhaseWaiting, model.DeployIncompatible, "", model.DeployIncompatible)
		Expect(d.Message).To(Equal("Container orders-api can't ship to DeployTarget dev: it lacks a Deployment " +
			"that writes deploy/k8s/"))
		app := &unstructured.Unstructured{}
		app.SetGroupVersionKind(argocd.ApplicationGVK)
		Consistently(func() error {
			return k8sClient.Get(ctx, argoKey(ns, argoNS), app)
		}, 2*time.Second).ShouldNot(Succeed())
	})

	It("fails a Container whose name joins to another's Application name, naming it (#1677, #1669)", func() {
		ns := newProjectNamespace()
		deployingContainer(ns)
		gitFake.setImage(ns, "orders-api", "v0.1.0", gitprovider.Image{Outcome: gitprovider.ImagePublished,
			Digest: testDigest, Platforms: []string{"linux/amd64"}})
		shipRelease(ns, "v0.1.0", true)
		argoNS := readyArgoTarget(ns)
		setDeploy(ns, c4v1alpha1.ContainerDeploy{Target: "dev"})
		first := argoApplication(ns, argoNS)

		By("<ns>-orders/api joins to the same Application name as <ns>/orders-api")
		other := newProjectNamespaceNamed(ns + "-orders")
		readyCrewContainer(other, "api", nil)
		crewReleaseOf(other, "api", "v0.1.0", model.RunSucceeded)
		dt := newArgoCDTarget(other, "dev", argoNS, "apps")
		mustCreate(dt)
		deployReady(dt, metav1.ConditionTrue, model.DeployTargetReady)
		setDeployOf(other, "api", c4v1alpha1.ContainerDeploy{Target: "dev"})

		By("Failed, reason NameRejected, naming the other Container: a terminal reason, not a wait")
		d := nameRejected(other, "api", "dev")
		Expect(d.Message).To(ContainSubstring("belongs to Container " + ns + "/orders-api, whose name joins to " +
			"the same: rename one of them"))
		Consistently(func(g Gomega) {
			d, cond := deployOfContainer(g, other, "api", "dev")
			g.Expect(d.Reason).To(Equal(model.DeployNameRejected), d.Message)
			g.Expect(cond.Reason).To(Equal(model.DeployFailed))
		}, 2*time.Second).Should(Succeed())

		By("the first Container keeps its Application")
		app := argoApplication(ns, argoNS)
		Expect(app.GetLabels()).To(Equal(first.GetLabels()))
		Expect(app.Object["spec"]).To(Equal(first.Object["spec"]))
	})

	It("fails a Container whose Application name is over 63 characters, saying so (#1677, #1669)", func() {
		ns := newProjectNamespace()
		name := "orders-api-" + strings.Repeat("x", 45)
		joined := len(ns) + 1 + len(name)
		Expect(joined).To(BeNumerically(">", model.ArgoCDApplicationNameMax))
		readyCrewContainer(ns, name, nil)
		crewReleaseOf(ns, name, "v0.1.0", model.RunSucceeded)
		argoNS := readyArgoTarget(ns)
		setDeployOf(ns, name, c4v1alpha1.ContainerDeploy{Target: "dev"})

		d := nameRejected(ns, name, "dev")
		Expect(d.Message).To(ContainSubstring(fmt.Sprintf("the Application name %s-%s is %d characters, over the %d "+
			"a label value holds: shorten the Container's namespace or name", ns, name, joined,
			model.ArgoCDApplicationNameMax)))
		var apps unstructured.UnstructuredList
		apps.SetGroupVersionKind(argocd.ApplicationGVK.GroupVersion().WithKind("ApplicationList"))
		Consistently(func(g Gomega) {
			g.Expect(k8sClient.List(ctx, &apps, client.InNamespace(argoNS))).To(Succeed())
			g.Expect(apps.Items).To(BeEmpty(), "nothing is written")
		}, 2*time.Second).Should(Succeed())
	})
})
