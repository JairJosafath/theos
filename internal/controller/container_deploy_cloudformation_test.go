/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/model"
)

// readyCloudFormationTarget makes cloudFormation DeployTarget name in ns,
// and waits for it to be Ready.
func readyCloudFormationTarget(ns, name string) {
	GinkgoHelper()
	dt := newCloudFormationTarget(ns, name, name+"-aws", "123456789012")
	mustCreate(newDeploySecret(ns, name+"-aws", awsKeys()), dt)
	deployReady(dt, metav1.ConditionTrue, model.DeployTargetReady)
}

// selfDeploying makes orders-api's Framework one that deploys itself and its
// Deployment gitea-release, what a cloudFormation target needs.
func selfDeploying(ns string) {
	GinkgoHelper()
	mustCreate(&c4v1alpha1.Deployment{ObjectMeta: objectMeta(ns, model.DeploymentGiteaRelease),
		Spec: newCatalogSpec(templatesURL+model.DeploymentGiteaRelease, "v1.0.0", c4v1alpha1.ContainerTypeService)})
	Eventually(func(g Gomega) {
		framework := &c4v1alpha1.Framework{}
		g.Expect(k8sClient.Get(ctx, key(ns, "python-fastapi"), framework)).To(Succeed())
		framework.Spec.DeploysItself = true
		g.Expect(k8sClient.Update(ctx, framework)).To(Succeed())
	}).Should(Succeed())
	Eventually(func(g Gomega) {
		container := &c4v1alpha1.Container{}
		g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), container)).To(Succeed())
		container.Spec.ComputeRef = nil
		container.Spec.DeploymentRef = &c4v1alpha1.LocalRef{Name: model.DeploymentGiteaRelease}
		g.Expect(k8sClient.Update(ctx, container)).To(Succeed())
	}).Should(Succeed())
}

var _ = Describe("Container deploys to a cloudFormation target (#1609)", func() {
	It("ships a release of a Container that deploys itself and fills status.deploys[]", func() {
		ns := newProjectNamespace()
		deployingContainer(ns)
		selfDeploying(ns)
		readyCloudFormationTarget(ns, "prod")
		shipRelease(ns, "v0.1.0", true)
		setDeploy(ns, c4v1alpha1.ContainerDeploy{Target: "prod"})
		d := deployIs(ns, "prod", model.DeployPhaseDeployed, model.DeployDeployed, "v0.1.0", model.DeployDeployed)
		Expect(d.WantedVersion).To(Equal("v0.1.0"))
		Expect(d.Since).NotTo(BeNil())
		Expect(deployFake.Deploys(targetName(ns, "prod"), ns+"/orders-api")).To(Equal(1))
	})

	It("says when the account lacks the CDK bootstrap, and ships once a person bootstraps it", func() {
		ns := newProjectNamespace()
		deployingContainer(ns)
		selfDeploying(ns)
		readyCloudFormationTarget(ns, "prod")
		shipRelease(ns, "v0.1.0", true)
		deployFake.Unprepare(targetName(ns, "prod"))
		setDeploy(ns, c4v1alpha1.ContainerDeploy{Target: "prod"})
		// DeployFailed on Deployed is what the UI lists under Needs you.
		d := deployIs(ns, "prod", model.DeployPhaseFailed, model.DeployPrerequisiteMissing, "", model.DeployFailed)
		Expect(d.Message).To(ContainSubstring("DeployTarget prod lacks what shipping v0.1.0 needs"))
		Expect(deployFake.Deploys(targetName(ns, "prod"), ns+"/orders-api")).To(BeZero())

		deployFake.Fail(targetName(ns, "prod"), nil)
		deployIs(ns, "prod", model.DeployPhaseDeployed, model.DeployDeployed, "v0.1.0", model.DeployDeployed)
	})

	It("is Incompatible for a Container whose Framework doesn't deploy itself", func() {
		ns := newProjectNamespace()
		deployingContainer(ns)
		readyCloudFormationTarget(ns, "prod")
		shipRelease(ns, "v0.1.0", true)
		setDeploy(ns, c4v1alpha1.ContainerDeploy{Target: "prod"})
		d := deployIs(ns, "prod", model.DeployPhaseWaiting, model.DeployIncompatible, "", model.DeployIncompatible)
		Expect(d.Message).To(ContainSubstring("Container orders-api can't ship to DeployTarget prod: it lacks " +
			"a Framework that deploys itself"))
		Expect(d.Message).To(ContainSubstring("the gitea-release Deployment"))
		Expect(deployFake.Deploys(targetName(ns, "prod"), ns+"/orders-api")).To(BeZero())
	})
})
