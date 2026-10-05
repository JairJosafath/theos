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
	"encoding/json"
	"path"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.yaml.in/yaml/v3"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/model"
	"github.com/JairJosafath/c4factory/internal/scaffold"
)

var ordersAPIScaffoldedFrom = []string{
	"python-fastapi@v1.2.1", "small@v1.0.0", "k8s-manifests@v1.1.0", "postgres@v1.1.0",
}

// createWebshop creates the sample story in ns: webshop-core with orders-api
// reading and writing orders-db, and the factory it scaffolds from.
func createWebshop(ns string) {
	GinkgoHelper()
	createFactory(ns)
	container := newContainer(ns, "orders-api", "webshop-core", c4v1alpha1.ContainerTypeService)
	container.Spec.DisplayName = "Orders API"
	mustCreate(
		newSystem(ns, "webshop-core"),
		newOrdersDB(ns),
		newRelationship(ns, "orders-api-to-orders-db",
			element(c4v1alpha1.ElementContainer, "orders-api"),
			element(c4v1alpha1.ElementDataStore, "orders-db")),
		container,
	)
}

// ordersDBApplied and ordersDBConnector are what the sample's orders-db link adds.
var (
	ordersDBApplied = model.AppliedRelationship{
		Name: "orders-api-to-orders-db", DataStore: "orders-db", Connector: "postgres",
	}
	ordersDBConnector = c4v1alpha1.AppliedConnector{
		Relationship: "orders-api-to-orders-db", DataStore: "orders-db", Connector: "postgres",
	}
)

// eventuallyApplied waits for a Relationship's Applied condition.
func eventuallyApplied(ns, name string, status metav1.ConditionStatus, reason string) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		var rel c4v1alpha1.Relationship
		g.Expect(k8sClient.Get(ctx, key(ns, name), &rel)).To(Succeed())
		cond := condition(rel.Status.Conditions, c4v1alpha1.ConditionApplied)
		g.Expect(cond).NotTo(BeNil())
		g.Expect(cond.Status).To(Equal(status))
		g.Expect(cond.Reason).To(Equal(reason))
	}).Should(Succeed())
}

// eventuallyOrdersAPI waits for orders-api to reach a phase and returns it.
func eventuallyOrdersAPI(ns string, phase c4v1alpha1.ContainerPhase) *c4v1alpha1.Container {
	GinkgoHelper()
	var container c4v1alpha1.Container
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), &container)).To(Succeed())
		g.Expect(container.Status.Phase).To(Equal(phase))
	}).Should(Succeed())
	return &container
}

// kustomizationFiles returns the repository paths that deploy/k8s/kustomization.yaml
// names as resources and patches.
func kustomizationFiles(files scaffold.Tree) []string {
	GinkgoHelper()
	var k struct {
		Resources []string `yaml:"resources"`
		Patches   []struct {
			Path string `yaml:"path"`
		} `yaml:"patches"`
	}
	Expect(yaml.Unmarshal(files["deploy/k8s/kustomization.yaml"].Data, &k)).To(Succeed())
	out := make([]string, 0, len(k.Resources)+len(k.Patches))
	for _, r := range k.Resources {
		out = append(out, path.Join("deploy/k8s", r))
	}
	for _, p := range k.Patches {
		out = append(out, path.Join("deploy/k8s", p.Path))
	}
	return out
}

var _ = Describe("Container scaffolding", func() {
	It("scaffolds orders-api from every layer, once", func() {
		ns := newProjectNamespace()
		createWebshop(ns)

		container := eventuallyOrdersAPI(ns, c4v1alpha1.ContainerReady)
		Expect(container.Status.RepositoryURL).To(Equal("http://gitea.gitea.svc.cluster.local:3000/" + ns + "/orders-api"))
		Expect(container.Status.ScaffoldedFrom).To(Equal(ordersAPIScaffoldedFrom))
		Expect(container.Status.AppliedRelationships).To(ConsistOf("orders-api-to-orders-db"))
		for _, t := range []string{c4v1alpha1.ConditionReferencesResolved, c4v1alpha1.ConditionChoicesValid,
			c4v1alpha1.ConditionRepositoryCreated, c4v1alpha1.ConditionScaffolded} {
			Expect(condition(container.Status.Conditions, t)).To(HaveField("Status", metav1.ConditionTrue), t)
		}

		By("pushing the uv project, the manifests and a devcontainer with Postgres")
		repo := gitFake.repo(ns, "orders-api")
		Expect(repo.commits).To(HaveLen(1))
		Expect(repo.files).To(HaveKey("pyproject.toml"))
		Expect(string(repo.files["pyproject.toml"].Data)).To(ContainSubstring(`name = "orders-api"`))
		Expect(string(repo.files["pyproject.toml"].Data)).To(ContainSubstring("psycopg"))
		Expect(repo.files).To(HaveKey("app/main.py"))
		Expect(repo.files).To(HaveKey("Dockerfile"))
		Expect(repo.files).To(HaveKey("deploy/k8s/compute.yaml"))
		Expect(repo.files).To(HaveKey("deploy/k8s/service.yaml"))
		Expect(repo.files).To(HaveKey("deploy/k8s/connector-postgres.yaml"))
		Expect(repo.files).To(HaveKey(".devcontainer/compose.yaml"))
		Expect(repo.files).To(HaveKey(".devcontainer/compose.postgres.yaml"))
		Expect(repo.files).NotTo(HaveKey(".c4factory/devcontainer.json"))

		var devcontainer map[string]any
		Expect(json.Unmarshal(repo.files[".devcontainer/devcontainer.json"].Data, &devcontainer)).To(Succeed())
		Expect(devcontainer).To(HaveKeyWithValue("name", "Orders API"))
		Expect(devcontainer).To(HaveKeyWithValue("service", "app"))
		Expect(devcontainer).To(HaveKeyWithValue("features", HaveKey("./c4session")),
			"the Session client is part of every devcontainer")
		Expect(repo.files[".devcontainer/c4session/install.sh"].Executable).To(BeTrue())

		identity, err := scaffold.ParseIdentity(repo.files[scaffold.IdentityPath].Data)
		Expect(err).NotTo(HaveOccurred())
		Expect(identity.Project).To(Equal(ns))
		Expect(identity.Container).To(Equal("orders-api"))

		By("the Relationship reporting it was applied")
		Eventually(func(g Gomega) {
			var rel c4v1alpha1.Relationship
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-api-to-orders-db"), &rel)).To(Succeed())
			g.Expect(condition(rel.Status.Conditions, c4v1alpha1.ConditionApplied)).
				To(HaveField("Status", metav1.ConditionTrue))
		}).Should(Succeed())

		By("a repeat reconcile changing nothing")
		pushes := gitFake.pushCount()
		before := container.Status.DeepCopy()
		orig := container.DeepCopy()
		container.Annotations = map[string]string{c4v1alpha1.AnnotationRetry: time.Now().String()}
		Expect(k8sClient.Patch(ctx, container, client.MergeFrom(orig))).To(Succeed())
		Consistently(func(g Gomega) {
			g.Expect(gitFake.pushCount()).To(Equal(pushes))
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), container)).To(Succeed())
			g.Expect(container.Status.ScaffoldedFrom).To(Equal(before.ScaffoldedFrom))
			g.Expect(container.Status.Conditions).To(Equal(before.Conditions))
		}).WithTimeout(2 * time.Second).Should(Succeed())
		Expect(gitFake.repo(ns, "orders-api").commits).To(HaveLen(1))
	})

	It("writes manifests that name only files it wrote, with or without a Compute", func() {
		ns := newProjectNamespace()
		createWebshop(ns)
		catalogAPI := newContainer(ns, "catalog-api", "webshop-core", c4v1alpha1.ContainerTypeService)
		catalogAPI.Spec.ComputeRef = nil
		mustCreate(catalogAPI)
		eventuallyOrdersAPI(ns, c4v1alpha1.ContainerReady)
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, "catalog-api"), catalogAPI)).To(Succeed())
			g.Expect(catalogAPI.Status.Phase).To(Equal(c4v1alpha1.ContainerReady))
		}).Should(Succeed())

		ordersFiles := gitFake.repo(ns, "orders-api").files
		Expect(kustomizationFiles(ordersFiles)).To(ConsistOf("deploy/k8s/deployment.yaml",
			"deploy/k8s/service.yaml", "deploy/k8s/connector-postgres.yaml", "deploy/k8s/compute.yaml"))
		catalogFiles := gitFake.repo(ns, "catalog-api").files
		Expect(kustomizationFiles(catalogFiles)).To(ConsistOf("deploy/k8s/deployment.yaml",
			"deploy/k8s/service.yaml"))
		for _, files := range []scaffold.Tree{ordersFiles, catalogFiles} {
			for _, p := range kustomizationFiles(files) {
				Expect(files).To(HaveKey(p))
			}
		}
	})

	It("rolls the Ready Container up into its SoftwareSystem and Project", func() {
		ns := newProjectNamespace()
		createWebshop(ns)
		eventuallyOrdersAPI(ns, c4v1alpha1.ContainerReady)
		Eventually(func(g Gomega) {
			var system c4v1alpha1.SoftwareSystem
			g.Expect(k8sClient.Get(ctx, key(ns, "webshop-core"), &system)).To(Succeed())
			g.Expect(system.Status.Summary).To(Equal("1 of 1 container Ready, 0 active sessions"))
			var project c4v1alpha1.Project
			g.Expect(k8sClient.Get(ctx, key(ns, ns), &project)).To(Succeed())
			g.Expect(project.Status.ReadyContainers).To(BeEquivalentTo(1))
		}).Should(Succeed())
	})

	It("marks a Relationship added after scaffolding as not applied", func() {
		ns := newProjectNamespace()
		createFactory(ns)
		mustCreate(
			newSystem(ns, "webshop-core"),
			newOrdersDB(ns),
			newContainer(ns, "orders-api", "webshop-core", c4v1alpha1.ContainerTypeService),
		)
		container := eventuallyOrdersAPI(ns, c4v1alpha1.ContainerReady)
		Expect(container.Status.ScaffoldedFrom).NotTo(ContainElement("postgres@v1.1.0"))

		mustCreate(newRelationship(ns, "orders-api-to-orders-db",
			element(c4v1alpha1.ElementContainer, "orders-api"),
			element(c4v1alpha1.ElementDataStore, "orders-db")))
		Eventually(func(g Gomega) {
			var rel c4v1alpha1.Relationship
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-api-to-orders-db"), &rel)).To(Succeed())
			cond := condition(rel.Status.Conditions, c4v1alpha1.ConditionApplied)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(cond.Reason).To(Equal(c4v1alpha1.ReasonAddedAfterScaffold))
		}).Should(Succeed())
		Expect(gitFake.repo(ns, "orders-api").commits).To(HaveLen(1), "no second scaffold")
	})

	It("recognises its own commit from an earlier attempt", func() {
		ns := newProjectNamespace()
		in := scaffold.Input{Project: ns, SoftwareSystem: "webshop-core", Container: "orders-api"}
		gitFake.seedRepo(ns, "orders-api",
			scaffold.CommitMessage(in, ordersAPIScaffoldedFrom, []model.AppliedRelationship{ordersDBApplied}))
		pushes := gitFake.pushCount()
		createWebshop(ns)

		container := eventuallyOrdersAPI(ns, c4v1alpha1.ContainerReady)
		Expect(container.Status.ScaffoldedFrom).To(Equal(ordersAPIScaffoldedFrom))
		Expect(container.Status.AppliedRelationships).To(ConsistOf("orders-api-to-orders-db"))
		Expect(container.Status.AppliedConnectors).To(ConsistOf(ordersDBConnector))
		Expect(condition(container.Status.Conditions, c4v1alpha1.ConditionScaffolded).Message).
			To(ContainSubstring("earlier attempt"))
		Expect(gitFake.pushCount()).To(Equal(pushes))
	})

	It("reads a commit from before targets were recorded as applied by name", func() {
		ns := newProjectNamespace()
		in := scaffold.Input{Project: ns, SoftwareSystem: "webshop-core", Container: "orders-api"}
		gitFake.seedRepo(ns, "orders-api", scaffold.CommitMessage(in, ordersAPIScaffoldedFrom,
			[]model.AppliedRelationship{{Name: "orders-api-to-orders-db"}}))
		createWebshop(ns)

		container := eventuallyOrdersAPI(ns, c4v1alpha1.ContainerReady)
		Expect(container.Status.AppliedRelationships).To(ConsistOf("orders-api-to-orders-db"))
		Expect(container.Status.AppliedConnectors).To(BeEmpty())
		eventuallyApplied(ns, "orders-api-to-orders-db", metav1.ConditionTrue, c4v1alpha1.ReasonAppliedAtScaffold)
	})

	It("doesn't count a Relationship recreated under the same name with a new target as applied", func() {
		ns := newProjectNamespace()
		createWebshop(ns)
		container := eventuallyOrdersAPI(ns, c4v1alpha1.ContainerReady)
		Expect(container.Status.AppliedConnectors).To(ConsistOf(ordersDBConnector))
		eventuallyApplied(ns, "orders-api-to-orders-db", metav1.ConditionTrue, c4v1alpha1.ReasonAppliedAtScaffold)

		rel := &c4v1alpha1.Relationship{}
		Expect(k8sClient.Get(ctx, key(ns, "orders-api-to-orders-db"), rel)).To(Succeed())
		Expect(k8sClient.Delete(ctx, rel)).To(Succeed())
		Eventually(func() bool {
			return apierrors.IsNotFound(k8sClient.Get(ctx, key(ns, "orders-api-to-orders-db"), rel))
		}).Should(BeTrue())
		cache := newOrdersDB(ns)
		cache.Name = "cache-db"
		cache.Spec.ConnectorRef.Name = "redis"
		mustCreate(cache, newRelationship(ns, "orders-api-to-orders-db",
			element(c4v1alpha1.ElementContainer, "orders-api"),
			element(c4v1alpha1.ElementDataStore, "cache-db")))

		eventuallyApplied(ns, "orders-api-to-orders-db", metav1.ConditionFalse, c4v1alpha1.ReasonAddedAfterScaffold)
		Expect(gitFake.repo(ns, "orders-api").commits).To(HaveLen(1), "no second scaffold")
	})

	It("stops when the repository has commits it didn't make", func() {
		ns := newProjectNamespace()
		gitFake.seedRepo(ns, "orders-api", "Initial commit\n")
		createWebshop(ns)

		container := eventuallyOrdersAPI(ns, c4v1alpha1.ContainerFailed)
		cond := condition(container.Status.Conditions, c4v1alpha1.ConditionScaffolded)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Reason).To(Equal(c4v1alpha1.ReasonForeignCommits))
		Expect(cond.Message).To(ContainSubstring("has commits C4 Factory didn't make"))

		By("not trying again on its own")
		reads := gitFake.historyCount(ns, "orders-api")
		Consistently(func() int { return gitFake.historyCount(ns, "orders-api") }, "2s").Should(Equal(reads))
		Expect(k8sClient.Get(ctx, key(ns, "orders-api"), container)).To(Succeed())
		Expect(container.Status.Phase).To(Equal(c4v1alpha1.ContainerFailed))

		By("not trying again when a DataStore, Relationship or catalog entry changes only status or metadata")
		var db c4v1alpha1.DataStore
		Expect(k8sClient.Get(ctx, key(ns, "orders-db"), &db)).To(Succeed())
		Expect(patch(&db, func() { metav1.SetMetaDataLabel(&db.ObjectMeta, "example.com/touched", "yes") })).To(Succeed())
		orig := db.DeepCopy()
		db.Status.Conditions = append(db.Status.Conditions, metav1.Condition{
			Type: "Touched", Status: metav1.ConditionTrue, Reason: "Test", LastTransitionTime: metav1.Now(),
		})
		Expect(k8sClient.Status().Patch(ctx, &db, client.MergeFrom(orig))).To(Succeed())
		var framework c4v1alpha1.Framework
		Expect(k8sClient.Get(ctx, key(ns, "python-fastapi"), &framework)).To(Succeed())
		Expect(patch(&framework, func() { metav1.SetMetaDataLabel(&framework.ObjectMeta, "example.com/touched", "yes") })).To(Succeed())
		var rel c4v1alpha1.Relationship
		Expect(k8sClient.Get(ctx, key(ns, "orders-api-to-orders-db"), &rel)).To(Succeed())
		Expect(patch(&rel, func() { metav1.SetMetaDataLabel(&rel.ObjectMeta, "example.com/touched", "yes") })).To(Succeed())
		Consistently(func() int { return gitFake.historyCount(ns, "orders-api") }, "2s").Should(Equal(reads))
	})

	It("stays Ready when its SoftwareSystem goes missing", func() {
		ns := newProjectNamespace()
		createWebshop(ns)
		eventuallyOrdersAPI(ns, c4v1alpha1.ContainerReady)
		reads := gitFake.historyCount(ns, "orders-api")

		// Envtest has no garbage collector, so the Container outlives its system,
		// as it would after `kubectl delete --cascade=orphan`.
		Expect(k8sClient.Delete(ctx, newSystem(ns, "webshop-core"))).To(Succeed())
		var container c4v1alpha1.Container
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), &container)).To(Succeed())
			cond := condition(container.Status.Conditions, c4v1alpha1.ConditionReferencesResolved)
			g.Expect(cond).To(HaveField("Reason", c4v1alpha1.ReasonNotFound))
		}).Should(Succeed())
		Expect(container.Status.Phase).To(Equal(c4v1alpha1.ContainerReady))

		By("not scaffolding again when it comes back")
		mustCreate(newSystem(ns, "webshop-core"))
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), &container)).To(Succeed())
			cond := condition(container.Status.Conditions, c4v1alpha1.ConditionReferencesResolved)
			g.Expect(cond).To(HaveField("Status", metav1.ConditionTrue))
		}).Should(Succeed())
		Expect(container.Status.Phase).To(Equal(c4v1alpha1.ContainerReady))
		Expect(gitFake.historyCount(ns, "orders-api")).To(Equal(reads))
	})

	It("records a Git host that runs out of time, and tries again", func() {
		ns := newProjectNamespace()
		release := gitFake.hang(ns, "orders-api")
		DeferCleanup(release)
		createWebshop(ns)

		Eventually(func(g Gomega) {
			var container c4v1alpha1.Container
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), &container)).To(Succeed())
			cond := condition(container.Status.Conditions, c4v1alpha1.ConditionScaffolded)
			g.Expect(cond).To(HaveField("Reason", c4v1alpha1.ReasonGitError))
			g.Expect(cond.Message).To(ContainSubstring("deadline exceeded"))
		}).Should(Succeed())

		release()
		eventuallyOrdersAPI(ns, c4v1alpha1.ContainerReady)
	})

	It("tries a Failed Container again when the retry annotation changes", func() {
		ns := newProjectNamespace()
		gitFake.seedRepo(ns, "orders-api", "Initial commit\n")
		createWebshop(ns)
		container := eventuallyOrdersAPI(ns, c4v1alpha1.ContainerFailed)

		gitFake.clearRepo(ns, "orders-api")
		Expect(patch(container, func() {
			container.Annotations = map[string]string{c4v1alpha1.AnnotationRetry: "1"}
		})).To(Succeed())

		container = eventuallyOrdersAPI(ns, c4v1alpha1.ContainerReady)
		Expect(container.Status.ScaffoldedFrom).To(Equal(ordersAPIScaffoldedFrom))
	})

	It("fails when two layers write the same path, naming it", func() {
		ns := newProjectNamespace()
		gitFake.addTemplate(templatesURL+"clashing-compute", "v1.0.0", scaffold.Tree{
			"pyproject.toml": {Data: []byte("[project]\n")},
		})
		createFactory(ns)
		mustCreate(&c4v1alpha1.Compute{ObjectMeta: objectMeta(ns, "clashing"),
			Spec: newCatalogSpec(templatesURL+"clashing-compute", "v1.0.0", c4v1alpha1.ContainerTypeService)})
		container := newContainer(ns, "orders-api", "webshop-core", c4v1alpha1.ContainerTypeService)
		container.Spec.ComputeRef = &c4v1alpha1.LocalRef{Name: "clashing"}
		mustCreate(newSystem(ns, "webshop-core"), container)

		container = eventuallyOrdersAPI(ns, c4v1alpha1.ContainerFailed)
		cond := condition(container.Status.Conditions, c4v1alpha1.ConditionScaffolded)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Reason).To(Equal(c4v1alpha1.ReasonLayerConflict))
		Expect(cond.Message).To(Equal("python-fastapi and clashing both write pyproject.toml"))
	})

	It("fails a template pinned to a branch", func() {
		ns := newProjectNamespace()
		createFactory(ns)
		var framework c4v1alpha1.Framework
		Expect(k8sClient.Get(ctx, key(ns, "python-fastapi"), &framework)).To(Succeed())
		Expect(patch(&framework, func() { framework.Spec.Template.Ref = "main" })).To(Succeed())
		mustCreate(newSystem(ns, "webshop-core"),
			newContainer(ns, "orders-api", "webshop-core", c4v1alpha1.ContainerTypeService))

		container := eventuallyOrdersAPI(ns, c4v1alpha1.ContainerFailed)
		cond := condition(container.Status.Conditions, c4v1alpha1.ConditionScaffolded)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Reason).To(Equal(c4v1alpha1.ReasonUnpinnedRef))
		Expect(cond.Message).To(ContainSubstring("Framework python-fastapi"))
	})

	It("waits for the GitProvider's credentials", func() {
		ns := newProjectNamespace()
		createFactory(ns)
		Expect(k8sClient.Delete(ctx, &corev1.Secret{ObjectMeta: objectMeta(ns, "gitea-credentials")})).To(Succeed())
		mustCreate(newSystem(ns, "webshop-core"),
			newContainer(ns, "orders-api", "webshop-core", c4v1alpha1.ContainerTypeService))
		Eventually(func(g Gomega) {
			var container c4v1alpha1.Container
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), &container)).To(Succeed())
			g.Expect(container.Status.Phase).To(Equal(c4v1alpha1.ContainerPending))
			g.Expect(condition(container.Status.Conditions, c4v1alpha1.ConditionReferencesResolved)).
				To(HaveField("Message", "Secret gitea-credentials not found"))
		}).Should(Succeed())

		By("scaffolding once the Secret appears, though Secrets aren't watched")
		mustCreate(&corev1.Secret{
			ObjectMeta: objectMeta(ns, "gitea-credentials"),
			StringData: map[string]string{"username": "c4admin", "token": "t0ken"},
		})
		Eventually(func(g Gomega) {
			var container c4v1alpha1.Container
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), &container)).To(Succeed())
			g.Expect(container.Status.Phase).To(Equal(c4v1alpha1.ContainerReady))
		}).WithTimeout(secretRecheck + 10*time.Second).Should(Succeed())
	})
})
