/*
Copyright 2025.

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
	"maps"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/scaffold"
)

// adoptableFiles is the tree an orders-api repository holds when the factory
// scaffolded it for project ns: the webshop is scaffolded in a fresh namespace
// and its .theos.yaml rewritten to name ns, with nothing scaffolded.
func adoptableFiles(ns string) scaffold.Tree {
	GinkgoHelper()
	return adoptableFilesWith(ns, nil)
}

// adoptableFilesWith is adoptableFiles with the Container changed by mutate
// before it is created, in the source namespace too.
func adoptableFilesWith(ns string, mutate func(string, *c4v1beta1.Container)) scaffold.Tree {
	GinkgoHelper()
	source := newProjectNamespace()
	createAdoptingWebshopWith(source, false, mutate)
	eventuallyOrdersAPI(source, c4v1beta1.ContainerReady)
	files := maps.Clone(gitFake.Repo(source, "orders-api").Files)
	id, err := scaffold.ParseIdentity(files[scaffold.IdentityPath].Data)
	Expect(err).NotTo(HaveOccurred())
	id.Project, id.ScaffoldedFrom = ns, nil
	data, err := id.Marshal()
	Expect(err).NotTo(HaveOccurred())
	files[scaffold.IdentityPath] = scaffold.File{Data: data}
	return files
}

// createAdoptingWebshop creates the webshop with orders-api under the Standard
// python-strict, adopting its repository.
func createAdoptingWebshop(ns string) {
	GinkgoHelper()
	createAdoptingWebshopWith(ns, true, nil)
}

// createAdoptingWebshopWith is createAdoptingWebshop with the Container
// changed by mutate, given its namespace, before it is created; the Standard
// supports opsFramework too, for a Container mutate puts there.
func createAdoptingWebshopWith(ns string, adopt bool, mutate func(string, *c4v1beta1.Container)) {
	GinkgoHelper()
	mustCreate(newStandard(ns, "python-strict", "python-fastapi", opsFramework))
	createWebshopWith(ns, func(c *c4v1beta1.Container) {
		c.Spec.StandardRef = &c4v1beta1.LocalRef{Name: "python-strict"}
		c.Spec.Adopt = adopt
		if mutate != nil {
			mutate(ns, c)
		}
	})
}

// onOperationsFramework puts the Container on opsFramework, which reads and
// renders the block, registering the Framework in its namespace, with the
// block ops.
func onOperationsFramework(ops *c4v1beta1.OperationsSpec) func(string, *c4v1beta1.Container) {
	return func(ns string, c *c4v1beta1.Container) {
		mustCreate(newOperationsFramework(ns, true))
		c.Spec.FrameworkRef = c4v1beta1.LocalRef{Name: opsFramework}
		c.Spec.Operations = ops
	}
}

// failedAdoption waits for orders-api to fail and returns its Scaffolded condition.
func failedAdoption(ns, reason string) *metav1.Condition {
	GinkgoHelper()
	container := eventuallyOrdersAPI(ns, c4v1beta1.ContainerFailed)
	cond := condition(container.Status.Conditions, c4v1beta1.ConditionScaffolded)
	Expect(cond).NotTo(BeNil())
	Expect(cond.Reason).To(Equal(reason))
	Expect(gitFake.Repo(ns, "orders-api").Commits).To(HaveLen(1), "an adopted repository is never written")
	return cond
}

// #1888: a Container with spec.adopt takes over a repository that already has
// commits, writing nothing.
var _ = Describe("Container adoption", func() {
	It("adopts a repository carrying its identity and its Standard's files, and protects main", func() {
		ns := newProjectNamespace()
		gitFake.SeedRepoFiles(ns, "orders-api", "Import orders-api\n", adoptableFiles(ns))
		createAdoptingWebshop(ns)

		container := eventuallyOrdersAPI(ns, c4v1beta1.ContainerReady)
		cond := condition(container.Status.Conditions, c4v1beta1.ConditionScaffolded)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Message).To(ContainSubstring("Adopted repository"))
		Expect(gitFake.Repo(ns, "orders-api").Commits).To(HaveLen(1), "nothing pushed")
		Expect(gitFake.ProtectedBranches(ns, "orders-api")).To(Equal([]string{"main"}))
		Expect(container.Status.ScaffoldedFrom).To(BeEmpty(), "nothing was scaffolded")
		Expect(container.Status.Standard).To(Equal(&c4v1beta1.ContainerStandardStatus{
			Name: "python-strict", Ref: "v1.0.0", Checks: c4v1beta1.ChecksEveryChange}))
		Expect(container.Status.RepositoryURL).NotTo(BeEmpty())
	})

	// #1909: the factory never scaffolded an adopted repository, so it can't
	// know whether the operations file there is one it can render again.
	It("refuses a change to spec.operations after the adoption, AdoptedRepository, until it is undone", func() {
		ns := newProjectNamespace()
		ops := &c4v1beta1.OperationsSpec{Alerts: &c4v1beta1.OperationsAlerts{Email: "ops@example.com"}}
		gitFake.SeedRepoFiles(ns, "orders-api", "Import orders-api\n", adoptableFilesWith(ns, onOperationsFramework(ops)))
		createAdoptingWebshopWith(ns, true, onOperationsFramework(ops))

		container := eventuallyOperationsApplied(ns, metav1.ConditionTrue, c4v1beta1.ReasonAppliedAtScaffold)
		Expect(container.Status.Phase).To(Equal(c4v1beta1.ContainerReady))
		Expect(container.Status.ScaffoldedFrom).To(BeEmpty(), "nothing was scaffolded")
		Expect(container.Status.Operations.Alerts.Email).To(Equal("ops@example.com"),
			"the block named at adoption is recorded as the one the repository holds")
		Expect(container.Status.Operations.Recovery.RPO).To(Equal("1h"))

		By("changing the block after the adoption")
		pushes := gitFake.PushCount()
		Expect(patch(container, func() {
			container.Spec.Operations.Recovery = &c4v1beta1.OperationsRecovery{RPO: "5m"}
		})).To(Succeed())
		container = eventuallyOperationsApplied(ns, metav1.ConditionFalse, c4v1beta1.ReasonAdoptedRepository)
		Expect(condition(container.Status.Conditions, c4v1beta1.ConditionOperationsApplied).Message).
			To(ContainSubstring("the factory never scaffolded, so it can't know whether the Framework " + opsFramework))
		Expect(container.Status.Operations.Recovery.RPO).To(Equal("1h"), "status keeps the block recorded at adoption")
		Expect(container.Status.OperationsUpdate).To(PointTo(HaveField("Stopped", c4v1beta1.ReasonAdoptedRepository)))
		Consistently(gitFake.PushCount).WithTimeout(time.Second).Should(Equal(pushes), "nothing reaches the repository")
		Expect(gitFake.PullRequests(ns, "orders-api")).To(BeEmpty())
		Expect(gitFake.Repo(ns, "orders-api").Commits).To(HaveLen(1), "an adopted repository is never written")

		By("writing the recorded block back")
		Expect(patch(container, func() {
			container.Spec.Operations.Recovery = &c4v1beta1.OperationsRecovery{RPO: "1h"}
		})).To(Succeed())
		container = eventuallyOperationsApplied(ns, metav1.ConditionTrue, c4v1beta1.ReasonAppliedAtScaffold)
		Expect(container.Status.OperationsUpdate).To(BeNil())
	})

	It("fails IdentityMismatch when main has no .theos.yaml", func() {
		ns := newProjectNamespace()
		files := adoptableFiles(ns)
		delete(files, scaffold.IdentityPath)
		gitFake.SeedRepoFiles(ns, "orders-api", "Import orders-api\n", files)
		createAdoptingWebshop(ns)

		cond := failedAdoption(ns, c4v1beta1.ReasonIdentityMismatch)
		Expect(cond.Message).To(ContainSubstring("has no .theos.yaml on main"))
		Expect(gitFake.ProtectedBranches(ns, "orders-api")).NotTo(ContainElement("main"))
	})

	It("fails IdentityMismatch when .theos.yaml names another project, saying what it holds", func() {
		ns := newProjectNamespace()
		files := adoptableFiles(ns)
		id, err := scaffold.ParseIdentity(files[scaffold.IdentityPath].Data)
		Expect(err).NotTo(HaveOccurred())
		id.Project, id.Container = "other-shop", "billing-api"
		data, err := id.Marshal()
		Expect(err).NotTo(HaveOccurred())
		files[scaffold.IdentityPath] = scaffold.File{Data: data}
		gitFake.SeedRepoFiles(ns, "orders-api", "Import orders-api\n", files)
		createAdoptingWebshop(ns)

		cond := failedAdoption(ns, c4v1beta1.ReasonIdentityMismatch)
		Expect(cond.Message).To(ContainSubstring(`names project "other-shop" and container "billing-api", not ` +
			ns + "/orders-api"))
	})

	It("fails StandardFilesDiffer, naming the paths, when a Standard file is missing or changed", func() {
		ns := newProjectNamespace()
		files := adoptableFiles(ns)
		Expect(files).To(HaveKey("AGENTS.md"), "the Standard's file")
		files["AGENTS.md"] = scaffold.File{Data: []byte("edited by hand\n")}
		gitFake.SeedRepoFiles(ns, "orders-api", "Import orders-api\n", files)
		createAdoptingWebshop(ns)

		cond := failedAdoption(ns, c4v1beta1.ReasonStandardFilesDiffer)
		Expect(cond.Message).To(ContainSubstring("differs from Standard python-strict at v1.0.0 in AGENTS.md"))
	})

	It("fails NoDefaultBranch when the repository's only branch is not main (#1908)", func() {
		ns := newProjectNamespace()
		gitFake.SeedRepoBranch(ns, "orders-api", "master", "Import orders-api\n", adoptableFiles(ns))
		createAdoptingWebshop(ns)

		cond := failedAdoption(ns, c4v1beta1.ReasonNoDefaultBranch)
		Expect(cond.Message).To(ContainSubstring("has commits but no main branch"))
		Expect(cond.Message).To(ContainSubstring("rename its default branch to main"))
		Expect(gitFake.ProtectedBranches(ns, "orders-api")).NotTo(ContainElement("main"))
	})

	It("fails NothingToAdopt on an empty repository", func() {
		ns := newProjectNamespace()
		createAdoptingWebshop(ns)

		container := eventuallyOrdersAPI(ns, c4v1beta1.ContainerFailed)
		cond := condition(container.Status.Conditions, c4v1beta1.ConditionScaffolded)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Reason).To(Equal(c4v1beta1.ReasonNothingToAdopt))
		Expect(cond.Message).To(ContainSubstring("has no commits to adopt"))
		Expect(gitFake.Repo(ns, "orders-api").Commits).To(BeEmpty(), "never scaffolded")
	})

	It("refuses a change of spec.adopt either way", func() {
		ns := newProjectNamespace()
		plain := newContainer(ns, "orders-api", "webshop-core", c4v1beta1.ContainerTypeService)
		adopted := newContainer(ns, "billing-api", "webshop-core", c4v1beta1.ContainerTypeService)
		adopted.Spec.Adopt = true
		mustCreate(plain, adopted)

		Expect(patch(plain, func() { plain.Spec.Adopt = true })).
			To(MatchError(ContainSubstring("adopt is immutable")))
		Expect(patch(adopted, func() { adopted.Spec.Adopt = false })).
			To(MatchError(ContainSubstring("adopt is immutable")))
	})
})
