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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/scaffold"
)

// adoptableFiles is the tree an orders-api repository holds when the factory
// scaffolded it for project ns: the webshop is scaffolded in a fresh namespace
// and its .c4factory.yaml rewritten to name ns, with nothing scaffolded.
func adoptableFiles(ns string) scaffold.Tree {
	GinkgoHelper()
	source := newProjectNamespace()
	createAdoptingWebshop(source, false)
	eventuallyOrdersAPI(source, c4v1alpha1.ContainerReady)
	files := maps.Clone(gitFake.repo(source, "orders-api").files)
	id, err := scaffold.ParseIdentity(files[scaffold.IdentityPath].Data)
	Expect(err).NotTo(HaveOccurred())
	id.Project, id.ScaffoldedFrom = ns, nil
	data, err := id.Marshal()
	Expect(err).NotTo(HaveOccurred())
	files[scaffold.IdentityPath] = scaffold.File{Data: data}
	return files
}

// createAdoptingWebshop creates the webshop with orders-api under the Standard
// python-strict, adopting its repository when adopt is set.
func createAdoptingWebshop(ns string, adopt bool) {
	GinkgoHelper()
	mustCreate(newStandard(ns, "python-strict", "python-fastapi"))
	createWebshopWith(ns, func(c *c4v1alpha1.Container) {
		c.Spec.StandardRef = &c4v1alpha1.LocalRef{Name: "python-strict"}
		c.Spec.Adopt = adopt
	})
}

// failedAdoption waits for orders-api to fail and returns its Scaffolded condition.
func failedAdoption(ns, reason string) *metav1.Condition {
	GinkgoHelper()
	container := eventuallyOrdersAPI(ns, c4v1alpha1.ContainerFailed)
	cond := condition(container.Status.Conditions, c4v1alpha1.ConditionScaffolded)
	Expect(cond).NotTo(BeNil())
	Expect(cond.Reason).To(Equal(reason))
	Expect(gitFake.repo(ns, "orders-api").commits).To(HaveLen(1), "an adopted repository is never written")
	return cond
}

// #1888: a Container with spec.adopt takes over a repository that already has
// commits, writing nothing.
var _ = Describe("Container adoption", func() {
	It("adopts a repository carrying its identity and its Standard's files, and protects main", func() {
		ns := newProjectNamespace()
		gitFake.seedRepoFiles(ns, "orders-api", "Import orders-api\n", adoptableFiles(ns))
		createAdoptingWebshop(ns, true)

		container := eventuallyOrdersAPI(ns, c4v1alpha1.ContainerReady)
		cond := condition(container.Status.Conditions, c4v1alpha1.ConditionScaffolded)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Message).To(ContainSubstring("Adopted repository"))
		Expect(gitFake.repo(ns, "orders-api").commits).To(HaveLen(1), "nothing pushed")
		Expect(gitFake.protectedBranches(ns, "orders-api")).To(Equal([]string{"main"}))
		Expect(container.Status.ScaffoldedFrom).To(BeEmpty(), "nothing was scaffolded")
		Expect(container.Status.Standard).To(Equal(&c4v1alpha1.ContainerStandardStatus{
			Name: "python-strict", Ref: "v1.0.0", Checks: c4v1alpha1.ChecksEveryChange}))
		Expect(container.Status.RepositoryURL).NotTo(BeEmpty())
	})

	It("fails IdentityMismatch when main has no .c4factory.yaml", func() {
		ns := newProjectNamespace()
		files := adoptableFiles(ns)
		delete(files, scaffold.IdentityPath)
		gitFake.seedRepoFiles(ns, "orders-api", "Import orders-api\n", files)
		createAdoptingWebshop(ns, true)

		cond := failedAdoption(ns, c4v1alpha1.ReasonIdentityMismatch)
		Expect(cond.Message).To(ContainSubstring("has no .c4factory.yaml on main"))
		Expect(gitFake.protectedBranches(ns, "orders-api")).NotTo(ContainElement("main"))
	})

	It("fails IdentityMismatch when .c4factory.yaml names another project, saying what it holds", func() {
		ns := newProjectNamespace()
		files := adoptableFiles(ns)
		id, err := scaffold.ParseIdentity(files[scaffold.IdentityPath].Data)
		Expect(err).NotTo(HaveOccurred())
		id.Project, id.Container = "other-shop", "billing-api"
		data, err := id.Marshal()
		Expect(err).NotTo(HaveOccurred())
		files[scaffold.IdentityPath] = scaffold.File{Data: data}
		gitFake.seedRepoFiles(ns, "orders-api", "Import orders-api\n", files)
		createAdoptingWebshop(ns, true)

		cond := failedAdoption(ns, c4v1alpha1.ReasonIdentityMismatch)
		Expect(cond.Message).To(ContainSubstring(`names project "other-shop" and container "billing-api", not ` +
			ns + "/orders-api"))
	})

	It("fails StandardFilesDiffer, naming the paths, when a Standard file is missing or changed", func() {
		ns := newProjectNamespace()
		files := adoptableFiles(ns)
		Expect(files).To(HaveKey("AGENTS.md"), "the Standard's file")
		files["AGENTS.md"] = scaffold.File{Data: []byte("edited by hand\n")}
		gitFake.seedRepoFiles(ns, "orders-api", "Import orders-api\n", files)
		createAdoptingWebshop(ns, true)

		cond := failedAdoption(ns, c4v1alpha1.ReasonStandardFilesDiffer)
		Expect(cond.Message).To(ContainSubstring("differs from Standard python-strict at v1.0.0 in AGENTS.md"))
	})

	It("fails NothingToAdopt on an empty repository", func() {
		ns := newProjectNamespace()
		createAdoptingWebshop(ns, true)

		container := eventuallyOrdersAPI(ns, c4v1alpha1.ContainerFailed)
		cond := condition(container.Status.Conditions, c4v1alpha1.ConditionScaffolded)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Reason).To(Equal(c4v1alpha1.ReasonNothingToAdopt))
		Expect(cond.Message).To(ContainSubstring("has no commits to adopt"))
		Expect(gitFake.repo(ns, "orders-api").commits).To(BeEmpty(), "never scaffolded")
	})

	It("refuses a change of spec.adopt either way", func() {
		ns := newProjectNamespace()
		plain := newContainer(ns, "orders-api", "webshop-core", c4v1alpha1.ContainerTypeService)
		adopted := newContainer(ns, "billing-api", "webshop-core", c4v1alpha1.ContainerTypeService)
		adopted.Spec.Adopt = true
		mustCreate(plain, adopted)

		Expect(patch(plain, func() { plain.Spec.Adopt = true })).
			To(MatchError(ContainSubstring("adopt is immutable")))
		Expect(patch(adopted, func() { adopted.Spec.Adopt = false })).
			To(MatchError(ContainSubstring("adopt is immutable")))
	})
})
