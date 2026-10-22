/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

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

// readyArgoTarget makes argoCD DeployTarget name in ns, its Argo CD in a
// fresh namespace holding AppProject default, waits for it to be Ready, and
// returns the Argo CD namespace.
func readyArgoTarget(ns, name string) string {
	GinkgoHelper()
	argoNS := newArgoNamespace()
	argoProject(argoNS)
	dt := newArgoCDTarget(ns, name, argoNS, "apps")
	mustCreate(dt)
	deployReady(dt, metav1.ConditionTrue, model.DeployTargetReady)
	return argoNS
}

// argoApplication waits for orders-api's Application in argoNS and returns it.
func argoApplication(ns, argoNS string) *unstructured.Unstructured {
	GinkgoHelper()
	app := &unstructured.Unstructured{}
	app.SetGroupVersionKind(argocd.ApplicationGVK)
	Eventually(func() error {
		return k8sClient.Get(ctx, key(argoNS, model.ArgoCDApplicationName(ns+"/orders-api")), app)
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

var _ = Describe("Container deploys to Argo CD", func() {
	It("writes the Application with the image digest and shows what Argo CD reports (#1588)", func() {
		ns := newProjectNamespace()
		deployingContainer(ns)
		gitFake.setImage(ns, "orders-api", "v0.1.0", gitprovider.Image{Outcome: gitprovider.ImagePublished,
			Digest: testDigest, Platforms: []string{"linux/amd64"}})
		shipRelease(ns, "v0.1.0", true)
		argoNS := readyArgoTarget(ns, "dev")
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
		readyArgoTarget(ns, "dev")
		setDeploy(ns, c4v1alpha1.ContainerDeploy{Target: "dev"})
		d := deployIs(ns, "dev", model.DeployPhaseFailed, model.DeployArtifactMissing, "", model.DeployFailed)
		Expect(d.Message).To(ContainSubstring("the release has no image"))
	})

	It("can't take a Container without a Deployment that writes deploy/k8s/ (#1588)", func() {
		ns := newProjectNamespace()
		createFactory(ns)
		container := newContainer(ns, "orders-api", "webshop-core", c4v1alpha1.ContainerTypeService)
		container.Spec.DeploymentRef = nil
		mustCreate(newSystem(ns, "webshop-core"), container, newCrew(ns))
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), container)).To(Succeed())
			g.Expect(container.Status.Phase).To(Equal(c4v1alpha1.ContainerReady))
		}).Should(Succeed())
		shipRelease(ns, "v0.1.0", true)
		argoNS := readyArgoTarget(ns, "dev")
		setDeploy(ns, c4v1alpha1.ContainerDeploy{Target: "dev"})
		d := deployIs(ns, "dev", model.DeployPhaseWaiting, model.DeployIncompatible, "", model.DeployIncompatible)
		Expect(d.Message).To(Equal("Container orders-api can't ship to DeployTarget dev: it lacks a Deployment " +
			"that writes deploy/k8s/"))
		app := &unstructured.Unstructured{}
		app.SetGroupVersionKind(argocd.ApplicationGVK)
		Consistently(func() error {
			return k8sClient.Get(ctx, key(argoNS, model.ArgoCDApplicationName(ns+"/orders-api")), app)
		}, 2*time.Second).ShouldNot(Succeed())
	})
})
