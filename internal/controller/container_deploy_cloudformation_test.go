/*
Copyright 2026 The theos Authors.

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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
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
	mustCreate(&c4v1beta1.Deployment{ObjectMeta: objectMeta(ns, model.DeploymentGiteaRelease),
		Spec: newCatalogSpec(templatesURL+model.DeploymentGiteaRelease, "v1.0.0", c4v1beta1.ContainerTypeService)})
	Eventually(func(g Gomega) {
		framework := &c4v1beta1.Framework{}
		g.Expect(k8sClient.Get(ctx, key(ns, "python-fastapi"), framework)).To(Succeed())
		framework.Spec.DeploysItself = true
		g.Expect(k8sClient.Update(ctx, framework)).To(Succeed())
	}).Should(Succeed())
	Eventually(func(g Gomega) {
		container := &c4v1beta1.Container{}
		g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), container)).To(Succeed())
		container.Spec.ComputeRef = nil
		container.Spec.DeploymentRef = &c4v1beta1.LocalRef{Name: model.DeploymentGiteaRelease}
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
		setDeploy(ns, c4v1beta1.ContainerDeploy{Target: "prod"})
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
		setDeploy(ns, c4v1beta1.ContainerDeploy{Target: "prod"})
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
		setDeploy(ns, c4v1beta1.ContainerDeploy{Target: "prod"})
		d := deployIs(ns, "prod", model.DeployPhaseWaiting, model.DeployIncompatible, "", model.DeployIncompatible)
		Expect(d.Message).To(ContainSubstring("Container orders-api can't ship to DeployTarget prod: it lacks " +
			"a Framework that deploys itself"))
		Expect(d.Message).To(ContainSubstring("the gitea-release Deployment"))
		Expect(deployFake.Deploys(targetName(ns, "prod"), ns+"/orders-api")).To(BeZero())
	})
})
