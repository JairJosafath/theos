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
	"crypto/rand"
	"encoding/json"
	"path"
	"slices"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.yaml.in/yaml/v3"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/modelfacts"
	"github.com/JairJosafath/theos/internal/scaffold"
)

var ordersAPIScaffoldedFrom = []string{
	"python-fastapi@v1.6.0", "small@v1.1.0", "k8s-manifests@v1.9.1", "postgres@v1.3.0",
}

// createWebshop creates the sample story in ns: webshop-core with orders-api
// reading and writing orders-db, and the factory it scaffolds from.
func createWebshop(ns string) {
	GinkgoHelper()
	createWebshopWith(ns, nil)
}

// createWebshopWith is createWebshop with orders-api changed by mutate first.
func createWebshopWith(ns string, mutate func(*c4v1beta1.Container)) {
	GinkgoHelper()
	createWebshopOn(ns, newSystem(ns, "webshop-core"), mutate)
}

// createWebshopOn is createWebshopWith on the given webshop-core system, for a
// story whose system names Domains.
func createWebshopOn(ns string, system *c4v1beta1.SoftwareSystem, mutate func(*c4v1beta1.Container)) {
	GinkgoHelper()
	createFactory(ns)
	container := newContainer(ns, "orders-api", "webshop-core", c4v1beta1.ContainerTypeService)
	container.Spec.DisplayName = "Orders API"
	if mutate != nil {
		mutate(container)
	}
	mustCreate(
		system,
		newOrdersDB(ns),
		newRelationship(ns, "orders-api-to-orders-db",
			element(c4v1beta1.ElementContainer, "orders-api"),
			element(c4v1beta1.ElementDataStore, "orders-db")),
		container,
	)
}

// ordersDBApplied and ordersDBConnector are what the sample's orders-db link adds.
var (
	ordersDBApplied = model.AppliedRelationship{
		Name: "orders-api-to-orders-db", DataStore: "orders-db", Connector: "postgres",
	}
	ordersDBConnector = c4v1beta1.AppliedConnector{
		Relationship: "orders-api-to-orders-db", DataStore: "orders-db", Connector: "postgres",
	}
)

// eventuallyApplied waits for a Relationship's Applied condition.
func eventuallyApplied(ns, name string, status metav1.ConditionStatus, reason string) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		var rel c4v1beta1.Relationship
		g.Expect(k8sClient.Get(ctx, key(ns, name), &rel)).To(Succeed())
		cond := condition(rel.Status.Conditions, c4v1beta1.ConditionApplied)
		g.Expect(cond).NotTo(BeNil())
		g.Expect(cond.Status).To(Equal(status))
		g.Expect(cond.Reason).To(Equal(reason))
	}).Should(Succeed())
}

// eventuallyOrdersAPI waits for orders-api to reach a phase and returns it.
func eventuallyOrdersAPI(ns string, phase c4v1beta1.ContainerPhase) *c4v1beta1.Container {
	GinkgoHelper()
	var container c4v1beta1.Container
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), &container)).To(Succeed())
		g.Expect(container.Status.Phase).To(Equal(phase))
	}).Should(Succeed())
	return &container
}

// historySettle is how long settledHistoryCount waits without a read. It
// is far longer than the reconciles a story's own creation queues take
// (milliseconds, a second read following another reconcile's status write
// and live read, which a loaded API server can stretch), and short enough
// that a reconciler retrying a Failed Container cannot slip past both it and
// the 2s the specs then watch the count for, from historySettle to 3s after
// the last read: a retry every historySettle or less never settles; a fixed
// period under 3s reads inside the watch; a doubling backoff settles on its
// first pause longer than historySettle, which is at most twice it, so its
// next read comes at most 2s after the last read, at most historySettle (1s)
// into the watch (#238).
const historySettle = 1 * time.Second

// settledHistoryCount waits until ns/orders-api's history has not been read
// for historySettle and returns how often it was read. A Failed Container's
// creation queues more than one reconcile (the Relationship created with it
// maps to it, then its own Create), and each reads the history once, so the
// count taken the moment it turns Failed can still move (#64).
func settledHistoryCount(ns string) int {
	GinkgoHelper()
	const repo = "orders-api"
	reads, since := gitFake.HistoryCount(ns, repo), time.Now()
	Eventually(func() time.Duration {
		if n := gitFake.HistoryCount(ns, repo); n != reads {
			reads, since = n, time.Now()
		}
		return time.Since(since)
	}).WithPolling(10*time.Millisecond).Should(BeNumerically(">=", historySettle),
		"the history is still read again and again")
	return reads
}

// watchContainer records every version of ns/orders-api the API server sends a
// watch from now on, so a status that lasts a single write is seen, not only
// what a poll happens to catch (#237). The returned func waits until the
// watch has seen the live object's version, or a later one, and returns the
// versions so far, which can include writes after that.
func watchContainer(ns string) func() []c4v1beta1.Container {
	const name = "orders-api"
	GinkgoHelper()
	wc, err := client.NewWithWatch(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	w, err := wc.Watch(ctx, &c4v1beta1.ContainerList{}, client.InNamespace(ns),
		client.MatchingFields{"metadata.name": name})
	Expect(err).NotTo(HaveOccurred())
	var mu sync.Mutex
	var versions []c4v1beta1.Container
	done := make(chan struct{})
	go func() {
		defer close(done)
		for event := range w.ResultChan() {
			if c, ok := event.Object.(*c4v1beta1.Container); ok {
				mu.Lock()
				versions = append(versions, *c.DeepCopy())
				mu.Unlock()
			}
		}
	}()
	DeferCleanup(func() {
		w.Stop()
		<-done
	})
	return func() []c4v1beta1.Container {
		GinkgoHelper()
		var live c4v1beta1.Container
		Expect(k8sClient.Get(ctx, key(ns, name), &live)).To(Succeed())
		// Contain, not end with: a status write landing after the Get above
		// makes a later version the last, and the live one stays in the list.
		Eventually(func() []string {
			mu.Lock()
			defer mu.Unlock()
			out := make([]string, 0, len(versions))
			for _, v := range versions {
				out = append(out, v.ResourceVersion)
			}
			return out
		}).Should(ContainElement(live.ResourceVersion), "the watch catching up with the live Container")
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(versions)
	}
}

// phases are the phases the versions pass through, a repeat counted once.
func phases(versions []c4v1beta1.Container) []c4v1beta1.ContainerPhase {
	var out []c4v1beta1.ContainerPhase
	for _, v := range versions {
		if len(out) == 0 || out[len(out)-1] != v.Status.Phase {
			out = append(out, v.Status.Phase)
		}
	}
	return out
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

		container := eventuallyOrdersAPI(ns, c4v1beta1.ContainerReady)
		Expect(container.Status.RepositoryURL).To(Equal("http://gitea.gitea.svc.cluster.local:3000/" + ns + "/orders-api"))
		Expect(container.Status.ScaffoldedFrom).To(Equal(ordersAPIScaffoldedFrom))
		Expect(container.Status.AppliedRelationships).To(ConsistOf("orders-api-to-orders-db"))
		for _, t := range []string{c4v1beta1.ConditionReferencesResolved, c4v1beta1.ConditionChoicesValid,
			c4v1beta1.ConditionRepositoryCreated, c4v1beta1.ConditionScaffolded} {
			Expect(condition(container.Status.Conditions, t)).To(HaveField("Status", metav1.ConditionTrue), t)
		}

		By("pushing the uv project, the manifests and a devcontainer with Postgres")
		repo := gitFake.Repo(ns, "orders-api")
		Expect(repo.Commits).To(HaveLen(1))
		Expect(repo.Commits[0].Message).To(ContainSubstring("theos-Container: " + ns + "/orders-api"))
		Expect(repo.Files).To(HaveKey("pyproject.toml"))
		Expect(string(repo.Files["pyproject.toml"].Data)).To(ContainSubstring(`name = "orders-api"`))
		Expect(string(repo.Files["pyproject.toml"].Data)).To(ContainSubstring("psycopg"))
		Expect(repo.Files).To(HaveKey("app/main.py"))
		Expect(repo.Files).To(HaveKey("Dockerfile"))
		Expect(repo.Files).To(HaveKey("deploy/k8s/compute.yaml"))
		Expect(repo.Files).To(HaveKey("deploy/k8s/service.yaml"))
		Expect(repo.Files).To(HaveKey("deploy/k8s/connector-postgres.yaml"))
		Expect(repo.Files).To(HaveKey(".devcontainer/compose.yaml"))
		Expect(repo.Files).To(HaveKey(".devcontainer/compose.postgres.yaml"))
		for p := range repo.Files {
			Expect(p).NotTo(HavePrefix(".theos/"), "a template's inputs are never pushed")
		}
		Expect(repo.Files).NotTo(HaveKey(scaffold.LicensePath), "no license set writes no LICENSE (#1788)")

		var devcontainer map[string]any
		Expect(json.Unmarshal(repo.Files[".devcontainer/devcontainer.json"].Data, &devcontainer)).To(Succeed())
		Expect(devcontainer).To(HaveKeyWithValue("name", "Orders API"))
		Expect(devcontainer).To(HaveKeyWithValue("service", "app"))
		Expect(devcontainer).To(HaveKeyWithValue("features", HaveKey("./theos-session")),
			"the Session client is part of every devcontainer")
		Expect(repo.Files[".devcontainer/theos-session/install.sh"].Executable).To(BeTrue())

		identity, err := scaffold.ParseIdentity(repo.Files[scaffold.IdentityPath].Data)
		Expect(err).NotTo(HaveOccurred())
		Expect(identity.Project).To(Equal(ns))
		Expect(identity.Container).To(Equal("orders-api"))

		By("the Relationship reporting it was applied")
		Eventually(func(g Gomega) {
			var rel c4v1beta1.Relationship
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-api-to-orders-db"), &rel)).To(Succeed())
			g.Expect(condition(rel.Status.Conditions, c4v1beta1.ConditionApplied)).
				To(HaveField("Status", metav1.ConditionTrue))
		}).Should(Succeed())

		By("a repeat reconcile changing nothing")
		pushes := gitFake.PushCount()
		before := container.Status.DeepCopy()
		orig := container.DeepCopy()
		container.Annotations = map[string]string{c4v1beta1.AnnotationRetry: time.Now().String()}
		Expect(k8sClient.Patch(ctx, container, client.MergeFrom(orig))).To(Succeed())
		Consistently(func(g Gomega) {
			g.Expect(gitFake.PushCount()).To(Equal(pushes))
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), container)).To(Succeed())
			g.Expect(container.Status.ScaffoldedFrom).To(Equal(before.ScaffoldedFrom))
			g.Expect(container.Status.Conditions).To(Equal(before.Conditions))
		}).WithTimeout(2 * time.Second).Should(Succeed())
		Expect(gitFake.Repo(ns, "orders-api").Commits).To(HaveLen(1))
	})

	It("comes back to a Container its cache has not seen Ready yet", func() {
		ns := newProjectNamespace()
		createWebshop(ns)
		container := eventuallyOrdersAPI(ns, c4v1beta1.ContainerReady)
		pushes := gitFake.PushCount()

		// An event queued during the scaffold absorbs the requeue ready asked
		// for, and its reconcile still reads the Container as Scaffolding.
		stale := container.DeepCopy()
		stale.Status.Phase = c4v1beta1.ContainerScaffolding
		r := &ContainerReconciler{Client: k8sClient, Scheme: scheme.Scheme, APIReader: k8sClient, Git: fakeGitClient{gitFake},
			ScaffoldTimeout: testScaffoldTimeout}
		result, err := r.scaffold(ctx, stale, modelfacts.Resolved{}, stale.Status.DeepCopy())
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(BeNumerically(">", 0), "nothing else reconciles a Ready Container")
		Expect(gitFake.PushCount()).To(Equal(pushes))
	})

	It("writes manifests that name only files it wrote, with or without a Compute", func() {
		ns := newProjectNamespace()
		createWebshop(ns)
		catalogAPI := newContainer(ns, "catalog-api", "webshop-core", c4v1beta1.ContainerTypeService)
		catalogAPI.Spec.ComputeRef = nil
		mustCreate(catalogAPI)
		eventuallyOrdersAPI(ns, c4v1beta1.ContainerReady)
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, "catalog-api"), catalogAPI)).To(Succeed())
			g.Expect(catalogAPI.Status.Phase).To(Equal(c4v1beta1.ContainerReady))
		}).Should(Succeed())

		ordersFiles := gitFake.Repo(ns, "orders-api").Files
		// auth.yaml: python-fastapi's OIDC issuer and audience (#1540).
		Expect(kustomizationFiles(ordersFiles)).To(ConsistOf("deploy/k8s/deployment.yaml",
			"deploy/k8s/service.yaml", "deploy/k8s/auth.yaml", "deploy/k8s/connector-postgres.yaml",
			"deploy/k8s/compute.yaml"))
		catalogFiles := gitFake.Repo(ns, "catalog-api").Files
		Expect(kustomizationFiles(catalogFiles)).To(ConsistOf("deploy/k8s/deployment.yaml",
			"deploy/k8s/service.yaml", "deploy/k8s/auth.yaml"))
		for _, files := range []scaffold.Tree{ordersFiles, catalogFiles} {
			for _, p := range kustomizationFiles(files) {
				Expect(files).To(HaveKey(p))
			}
		}
	})

	It("rolls the Ready Container up into its SoftwareSystem and Project", func() {
		ns := newProjectNamespace()
		createWebshop(ns)
		eventuallyOrdersAPI(ns, c4v1beta1.ContainerReady)
		Eventually(func(g Gomega) {
			var system c4v1beta1.SoftwareSystem
			g.Expect(k8sClient.Get(ctx, key(ns, "webshop-core"), &system)).To(Succeed())
			g.Expect(system.Status.Summary).To(Equal("1 of 1 container Ready, 0 active sessions"))
			var project c4v1beta1.Project
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
			newContainer(ns, "orders-api", "webshop-core", c4v1beta1.ContainerTypeService),
		)
		container := eventuallyOrdersAPI(ns, c4v1beta1.ContainerReady)
		Expect(container.Status.ScaffoldedFrom).NotTo(ContainElement("postgres@v1.3.0"))

		mustCreate(newRelationship(ns, "orders-api-to-orders-db",
			element(c4v1beta1.ElementContainer, "orders-api"),
			element(c4v1beta1.ElementDataStore, "orders-db")))
		Eventually(func(g Gomega) {
			var rel c4v1beta1.Relationship
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-api-to-orders-db"), &rel)).To(Succeed())
			cond := condition(rel.Status.Conditions, c4v1beta1.ConditionApplied)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(cond.Reason).To(Equal(c4v1beta1.ReasonAddedAfterScaffold))
		}).Should(Succeed())
		Expect(gitFake.Repo(ns, "orders-api").Commits).To(HaveLen(1), "no second scaffold")
	})

	It("recognises its own commit from an earlier attempt", func() {
		ns := newProjectNamespace()
		in := scaffold.Input{Project: ns, SoftwareSystem: "webshop-core", Container: "orders-api"}
		gitFake.SeedRepo(ns, "orders-api",
			scaffold.CommitMessage(in, ordersAPIScaffoldedFrom, []model.AppliedRelationship{ordersDBApplied}))
		pushes := gitFake.PushCount()
		createWebshop(ns)

		container := eventuallyOrdersAPI(ns, c4v1beta1.ContainerReady)
		Expect(container.Status.ScaffoldedFrom).To(Equal(ordersAPIScaffoldedFrom))
		Expect(container.Status.AppliedRelationships).To(ConsistOf("orders-api-to-orders-db"))
		Expect(container.Status.AppliedConnectors).To(ConsistOf(ordersDBConnector))
		Expect(condition(container.Status.Conditions, c4v1beta1.ConditionScaffolded).Message).
			To(ContainSubstring("earlier attempt"))
		Expect(gitFake.PushCount()).To(Equal(pushes))
	})

	It("reads a commit from before targets were recorded as applied by name", func() {
		ns := newProjectNamespace()
		in := scaffold.Input{Project: ns, SoftwareSystem: "webshop-core", Container: "orders-api"}
		gitFake.SeedRepo(ns, "orders-api", scaffold.CommitMessage(in, ordersAPIScaffoldedFrom,
			[]model.AppliedRelationship{{Name: "orders-api-to-orders-db"}}))
		createWebshop(ns)

		container := eventuallyOrdersAPI(ns, c4v1beta1.ContainerReady)
		Expect(container.Status.AppliedRelationships).To(ConsistOf("orders-api-to-orders-db"))
		Expect(container.Status.AppliedConnectors).To(BeEmpty())
		eventuallyApplied(ns, "orders-api-to-orders-db", metav1.ConditionTrue, c4v1beta1.ReasonAppliedAtScaffold)
	})

	It("doesn't count a Relationship recreated under the same name with a new target as applied", func() {
		ns := newProjectNamespace()
		createWebshop(ns)
		container := eventuallyOrdersAPI(ns, c4v1beta1.ContainerReady)
		Expect(container.Status.AppliedConnectors).To(ConsistOf(ordersDBConnector))
		eventuallyApplied(ns, "orders-api-to-orders-db", metav1.ConditionTrue, c4v1beta1.ReasonAppliedAtScaffold)

		rel := &c4v1beta1.Relationship{}
		Expect(k8sClient.Get(ctx, key(ns, "orders-api-to-orders-db"), rel)).To(Succeed())
		Expect(k8sClient.Delete(ctx, rel)).To(Succeed())
		Eventually(func() bool {
			return apierrors.IsNotFound(k8sClient.Get(ctx, key(ns, "orders-api-to-orders-db"), rel))
		}).Should(BeTrue())
		cache := newOrdersDB(ns)
		cache.Name = "cache-db"
		cache.Spec.ConnectorRef.Name = "redis"
		mustCreate(cache, newRelationship(ns, "orders-api-to-orders-db",
			element(c4v1beta1.ElementContainer, "orders-api"),
			element(c4v1beta1.ElementDataStore, "cache-db")))

		eventuallyApplied(ns, "orders-api-to-orders-db", metav1.ConditionFalse, c4v1beta1.ReasonAddedAfterScaffold)
		Expect(gitFake.Repo(ns, "orders-api").Commits).To(HaveLen(1), "no second scaffold")
	})

	It("stops when the repository has commits it didn't make", func() {
		ns := newProjectNamespace()
		gitFake.SeedRepo(ns, "orders-api", "Initial commit\n")
		createWebshop(ns)

		container := eventuallyOrdersAPI(ns, c4v1beta1.ContainerFailed)
		cond := condition(container.Status.Conditions, c4v1beta1.ConditionScaffolded)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Reason).To(Equal(c4v1beta1.ReasonForeignCommits))
		Expect(cond.Message).To(ContainSubstring("has commits theos didn't make"))

		By("not trying again on its own")
		reads := settledHistoryCount(ns)
		Consistently(func() int { return gitFake.HistoryCount(ns, "orders-api") }, "2s").Should(Equal(reads))
		Expect(k8sClient.Get(ctx, key(ns, "orders-api"), container)).To(Succeed())
		Expect(container.Status.Phase).To(Equal(c4v1beta1.ContainerFailed))

		By("not trying again when a DataStore, Relationship or catalog entry changes only status or metadata")
		var db c4v1beta1.DataStore
		Expect(k8sClient.Get(ctx, key(ns, "orders-db"), &db)).To(Succeed())
		Expect(patch(&db, func() { metav1.SetMetaDataLabel(&db.ObjectMeta, "example.com/touched", "yes") })).To(Succeed())
		orig := db.DeepCopy()
		db.Status.Conditions = append(db.Status.Conditions, metav1.Condition{
			Type: "Touched", Status: metav1.ConditionTrue, Reason: "Test", LastTransitionTime: metav1.Now(),
		})
		Expect(k8sClient.Status().Patch(ctx, &db, client.MergeFrom(orig))).To(Succeed())
		var framework c4v1beta1.Framework
		Expect(k8sClient.Get(ctx, key(ns, "python-fastapi"), &framework)).To(Succeed())
		Expect(patch(&framework, func() { metav1.SetMetaDataLabel(&framework.ObjectMeta, "example.com/touched", "yes") })).To(Succeed())
		var rel c4v1beta1.Relationship
		Expect(k8sClient.Get(ctx, key(ns, "orders-api-to-orders-db"), &rel)).To(Succeed())
		Expect(patch(&rel, func() { metav1.SetMetaDataLabel(&rel.ObjectMeta, "example.com/touched", "yes") })).To(Succeed())
		Consistently(func() int { return gitFake.HistoryCount(ns, "orders-api") }, "2s").Should(Equal(reads))
	})

	It("keeps a Failed Container Failed while events read it again (#237)", func() {
		ns := newProjectNamespace()
		gitFake.SeedRepo(ns, "orders-api", "Initial commit\n")
		createWebshop(ns)
		container := eventuallyOrdersAPI(ns, c4v1beta1.ContainerFailed)
		reads := settledHistoryCount(ns)
		Expect(k8sClient.Get(ctx, key(ns, "orders-api"), container)).To(Succeed())
		failedWith := condition(container.Status.Conditions, c4v1beta1.ConditionScaffolded).DeepCopy()
		Expect(failedWith).To(HaveField("Reason", c4v1beta1.ReasonForeignCommits))
		versions := watchContainer(ns)

		By("events that reconcile it: its retry annotation, set and changed, and a Compute created")
		readAgain := func() {
			GinkgoHelper()
			Eventually(func() int { return gitFake.HistoryCount(ns, "orders-api") }).Should(BeNumerically(">", reads),
				"the event reconciling the Container, which reads its history again")
			reads = settledHistoryCount(ns)
		}
		for _, retry := range []string{"1", "2"} {
			Expect(patch(container, func() {
				container.Annotations = map[string]string{c4v1beta1.AnnotationRetry: retry}
			})).To(Succeed())
			readAgain()
		}
		mustCreate(&c4v1beta1.Compute{ObjectMeta: objectMeta(ns, "spare"),
			Spec: newCatalogSpec(templatesURL+"spare-compute", "v1.0.0", c4v1beta1.ContainerTypeService)})
		readAgain()

		By("every version it passed through being Failed, its condition unchanged")
		seen := versions()
		Expect(phases(seen)).To(Equal([]c4v1beta1.ContainerPhase{c4v1beta1.ContainerFailed}),
			"no status write in between shows Scaffolding")
		for _, v := range seen {
			cond := condition(v.Status.Conditions, c4v1beta1.ConditionScaffolded)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(failedWith.Reason))
			Expect(cond.Message).To(Equal(failedWith.Message))
			Expect(cond.LastTransitionTime.Equal(&failedWith.LastTransitionTime)).To(BeTrue(),
				"lastTransitionTime %s moved from %s", cond.LastTransitionTime, failedWith.LastTransitionTime)
		}
	})

	It("takes a Failed Container straight to its new reason when the cause changes (#237)", func() {
		ns := newProjectNamespace()
		gitFake.SeedRepo(ns, "orders-api", "Initial commit\n")
		createWebshop(ns)
		container := eventuallyOrdersAPI(ns, c4v1beta1.ContainerFailed)
		Expect(condition(container.Status.Conditions, c4v1beta1.ConditionScaffolded)).
			To(HaveField("Reason", c4v1beta1.ReasonForeignCommits))
		settledHistoryCount(ns)
		versions := watchContainer(ns)

		By("the Framework's template moved to a branch and the repository emptied")
		var framework c4v1beta1.Framework
		Expect(k8sClient.Get(ctx, key(ns, "python-fastapi"), &framework)).To(Succeed())
		Expect(patch(&framework, func() { framework.Spec.Template.Ref = "main" })).To(Succeed())
		gitFake.ClearRepo(ns, "orders-api")
		Expect(patch(container, func() {
			container.Annotations = map[string]string{c4v1beta1.AnnotationRetry: "1"}
		})).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), container)).To(Succeed())
			g.Expect(condition(container.Status.Conditions, c4v1beta1.ConditionScaffolded)).
				To(HaveField("Reason", c4v1beta1.ReasonUnpinnedRef))
		}).Should(Succeed())
		Expect(container.Status.Phase).To(Equal(c4v1beta1.ContainerFailed))

		seen := versions()
		Expect(phases(seen)).To(Equal([]c4v1beta1.ContainerPhase{c4v1beta1.ContainerFailed}),
			"the new reason replaces the old one without Scaffolding in between")
		Expect(condition(seen[len(seen)-1].Status.Conditions, c4v1beta1.ConditionScaffolded)).
			To(HaveField("Reason", c4v1beta1.ReasonUnpinnedRef))
	})

	It("keeps a Failed Container Failed, with a GitError condition, while the Git host is down (#237)", func() {
		ns := newProjectNamespace()
		gitFake.SeedRepo(ns, "orders-api", "Initial commit\n")
		createWebshop(ns)
		container := eventuallyOrdersAPI(ns, c4v1beta1.ContainerFailed)
		settledHistoryCount(ns)
		versions := watchContainer(ns)

		By("a retry while the host doesn't answer")
		release := gitFake.Hang(ns, "orders-api")
		DeferCleanup(release)
		Expect(patch(container, func() {
			container.Annotations = map[string]string{c4v1beta1.AnnotationRetry: "1"}
		})).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), container)).To(Succeed())
			cond := condition(container.Status.Conditions, c4v1beta1.ConditionScaffolded)
			g.Expect(cond).To(HaveField("Reason", c4v1beta1.ReasonGitError))
			g.Expect(cond.Message).To(ContainSubstring("deadline exceeded"))
		}).Should(Succeed())
		Expect(container.Status.Phase).To(Equal(c4v1beta1.ContainerFailed))
		Expect(phases(versions())).To(Equal([]c4v1beta1.ContainerPhase{c4v1beta1.ContainerFailed}),
			"a Git error that may pass doesn't show Scaffolding either")
	})

	It("shows Scaffolding the first time, and again once a Failed Container's cause is gone (#237)", func() {
		ns := newProjectNamespace()
		versions := watchContainer(ns)
		gitFake.SeedRepo(ns, "orders-api", "Initial commit\n")
		createWebshop(ns)
		container := eventuallyOrdersAPI(ns, c4v1beta1.ContainerFailed)
		first := phases(versions())
		Expect(first).To(ContainElement(c4v1beta1.ContainerScaffolding))
		Expect(slices.Index(first, c4v1beta1.ContainerScaffolding)).
			To(BeNumerically("<", slices.Index(first, c4v1beta1.ContainerFailed)), "Scaffolding before it failed")
		settledHistoryCount(ns)
		before := len(versions())

		gitFake.ClearRepo(ns, "orders-api")
		Expect(patch(container, func() {
			container.Annotations = map[string]string{c4v1beta1.AnnotationRetry: "1"}
		})).To(Succeed())
		eventuallyOrdersAPI(ns, c4v1beta1.ContainerReady)
		Expect(phases(versions()[before-1:])).To(Equal([]c4v1beta1.ContainerPhase{
			c4v1beta1.ContainerFailed, c4v1beta1.ContainerScaffolding, c4v1beta1.ContainerReady,
		}), "a Failed Container that can be scaffolded now shows Scaffolding, then Ready")
	})

	It("stays Ready when its SoftwareSystem goes missing", func() {
		ns := newProjectNamespace()
		createWebshop(ns)
		eventuallyOrdersAPI(ns, c4v1beta1.ContainerReady)
		reads := gitFake.HistoryCount(ns, "orders-api")

		// Envtest has no garbage collector, so the Container outlives its system,
		// as it would after `kubectl delete --cascade=orphan`.
		Expect(k8sClient.Delete(ctx, newSystem(ns, "webshop-core"))).To(Succeed())
		var container c4v1beta1.Container
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), &container)).To(Succeed())
			cond := condition(container.Status.Conditions, c4v1beta1.ConditionReferencesResolved)
			g.Expect(cond).To(HaveField("Reason", c4v1beta1.ReasonNotFound))
		}).Should(Succeed())
		Expect(container.Status.Phase).To(Equal(c4v1beta1.ContainerReady))

		By("not scaffolding again when it comes back")
		mustCreate(newSystem(ns, "webshop-core"))
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), &container)).To(Succeed())
			cond := condition(container.Status.Conditions, c4v1beta1.ConditionReferencesResolved)
			g.Expect(cond).To(HaveField("Status", metav1.ConditionTrue))
		}).Should(Succeed())
		Expect(container.Status.Phase).To(Equal(c4v1beta1.ContainerReady))
		Expect(gitFake.HistoryCount(ns, "orders-api")).To(Equal(reads))
	})

	It("builds nothing for an External system, and scaffolds once it turns Internal", func() {
		ns := newProjectNamespace()
		system := newSystem(ns, "webshop-core")
		system.Spec.Location = c4v1beta1.SystemExternal
		createFactory(ns)
		mustCreate(system, newOrdersDB(ns),
			newContainer(ns, "orders-api", "webshop-core", c4v1beta1.ContainerTypeService))

		var container c4v1beta1.Container
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), &container)).To(Succeed())
			cond := condition(container.Status.Conditions, c4v1beta1.ConditionReferencesResolved)
			g.Expect(cond).To(HaveField("Reason", c4v1beta1.ReasonExternalSystem))
			g.Expect(cond.Message).To(ContainSubstring("SoftwareSystem webshop-core is External"))
		}).Should(Succeed())
		Expect(container.Status.Phase).To(Equal(c4v1beta1.ContainerPending))
		var db c4v1beta1.DataStore
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-db"), &db)).To(Succeed())
			cond := condition(db.Status.Conditions, c4v1beta1.ConditionReferencesResolved)
			g.Expect(cond).To(HaveField("Reason", c4v1beta1.ReasonExternalSystem))
		}).Should(Succeed())
		Consistently(func() int { return gitFake.HistoryCount(ns, "orders-api") }, "1s").Should(BeZero())

		By("scaffolding once the system turns Internal")
		Expect(k8sClient.Get(ctx, key(ns, "webshop-core"), system)).To(Succeed())
		Expect(patch(system, func() { system.Spec.Location = c4v1beta1.SystemInternal })).To(Succeed())
		eventuallyOrdersAPI(ns, c4v1beta1.ContainerReady)
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-db"), &db)).To(Succeed())
			cond := condition(db.Status.Conditions, c4v1beta1.ConditionReferencesResolved)
			g.Expect(cond).To(HaveField("Status", metav1.ConditionTrue))
		}).Should(Succeed())

		By("staying Ready when it turns External again")
		reads := gitFake.HistoryCount(ns, "orders-api")
		Expect(k8sClient.Get(ctx, key(ns, "webshop-core"), system)).To(Succeed())
		Expect(patch(system, func() { system.Spec.Location = c4v1beta1.SystemExternal })).To(Succeed())
		Consistently(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), &container)).To(Succeed())
			g.Expect(container.Status.Phase).To(Equal(c4v1beta1.ContainerReady))
		}, "1s").Should(Succeed())
		Expect(gitFake.HistoryCount(ns, "orders-api")).To(Equal(reads))
	})

	It("defaults a SoftwareSystem's location to Internal", func() {
		ns := newProjectNamespace()
		system := newSystem(ns, "webshop-core")
		mustCreate(system)
		Expect(k8sClient.Get(ctx, key(ns, "webshop-core"), system)).To(Succeed())
		Expect(system.Spec.Location).To(Equal(c4v1beta1.SystemInternal))

		bad := newSystem(ns, "stripe")
		bad.Spec.Location = "Elsewhere"
		Expect(k8sClient.Create(ctx, bad)).To(MatchError(ContainSubstring("spec.location")))
	})

	It("records a Git host that runs out of time, and tries again", func() {
		ns := newProjectNamespace()
		release := gitFake.Hang(ns, "orders-api")
		DeferCleanup(release)
		createWebshop(ns)

		Eventually(func(g Gomega) {
			var container c4v1beta1.Container
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), &container)).To(Succeed())
			cond := condition(container.Status.Conditions, c4v1beta1.ConditionScaffolded)
			g.Expect(cond).To(HaveField("Reason", c4v1beta1.ReasonGitError))
			g.Expect(cond.Message).To(ContainSubstring("deadline exceeded"))
		}).Should(Succeed())

		release()
		eventuallyOrdersAPI(ns, c4v1beta1.ContainerReady)
	})

	It("tries a Failed Container again when the retry annotation changes", func() {
		ns := newProjectNamespace()
		gitFake.SeedRepo(ns, "orders-api", "Initial commit\n")
		createWebshop(ns)
		container := eventuallyOrdersAPI(ns, c4v1beta1.ContainerFailed)

		gitFake.ClearRepo(ns, "orders-api")
		Expect(patch(container, func() {
			container.Annotations = map[string]string{c4v1beta1.AnnotationRetry: "1"}
		})).To(Succeed())

		container = eventuallyOrdersAPI(ns, c4v1beta1.ContainerReady)
		Expect(container.Status.ScaffoldedFrom).To(Equal(ordersAPIScaffoldedFrom))
	})

	It("scaffolds npm packages under the GitProvider's spec.packages.npm.scope (#1728)", func() {
		ns := newProjectNamespace()
		gitFake.AddTemplate(templatesURL+"scoped-compute", "v1.0.0", scaffold.Tree{
			"package.json.tmpl": {Data: []byte("{\n  \"name\": \"{{ .NpmScope }}/{{ .Container }}\"\n}\n")},
		})
		createFactory(ns)
		setNpmScope(ns, "@acme")
		mustCreate(&c4v1beta1.Compute{ObjectMeta: objectMeta(ns, "scoped"),
			Spec: newCatalogSpec(templatesURL+"scoped-compute", "v1.0.0", c4v1beta1.ContainerTypeService)})
		container := newContainer(ns, "orders-api", "webshop-core", c4v1beta1.ContainerTypeService)
		container.Spec.ComputeRef = &c4v1beta1.LocalRef{Name: "scoped"}
		mustCreate(newSystem(ns, "webshop-core"), container)

		eventuallyOrdersAPI(ns, c4v1beta1.ContainerReady)
		Expect(gitFake.MainFiles(ns, "orders-api")).To(HaveKeyWithValue("package.json",
			HaveField("Data", BeEquivalentTo("{\n  \"name\": \"@acme/orders-api\"\n}\n"))),
			"the scaffold's provider Config carries the GitProvider's packages")
	})

	It("writes LICENSE from the Project's spec.license (#1788)", func() {
		ns := newProjectNamespace()
		var project c4v1beta1.Project
		Expect(k8sClient.Get(ctx, key(ns, ns), &project)).To(Succeed())
		orig := project.DeepCopy()
		project.Spec.License = "MIT"
		Expect(k8sClient.Patch(ctx, &project, client.MergeFrom(orig))).To(Succeed())
		createWebshop(ns)

		eventuallyOrdersAPI(ns, c4v1beta1.ContainerReady)
		repo := gitFake.Repo(ns, "orders-api")
		Expect(repo.Files).To(HaveKey(scaffold.LicensePath))
		Expect(string(repo.Files[scaffold.LicensePath].Data)).To(ContainSubstring("Copyright (c) %d Test story",
			time.Now().Year()), "the copyright line names the Project's displayName and the year")
		identity, err := scaffold.ParseIdentity(repo.Files[scaffold.IdentityPath].Data)
		Expect(err).NotTo(HaveOccurred())
		Expect(identity.License).To(Equal("MIT"))
	})

	It("fails when two layers write the same path, naming it", func() {
		ns := newProjectNamespace()
		gitFake.AddTemplate(templatesURL+"clashing-compute", "v1.0.0", scaffold.Tree{
			"pyproject.toml": {Data: []byte("[project]\n")},
		})
		createFactory(ns)
		mustCreate(&c4v1beta1.Compute{ObjectMeta: objectMeta(ns, "clashing"),
			Spec: newCatalogSpec(templatesURL+"clashing-compute", "v1.0.0", c4v1beta1.ContainerTypeService)})
		container := newContainer(ns, "orders-api", "webshop-core", c4v1beta1.ContainerTypeService)
		container.Spec.ComputeRef = &c4v1beta1.LocalRef{Name: "clashing"}
		mustCreate(newSystem(ns, "webshop-core"), container)

		container = eventuallyOrdersAPI(ns, c4v1beta1.ContainerFailed)
		cond := condition(container.Status.Conditions, c4v1beta1.ConditionScaffolded)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Reason).To(Equal(c4v1beta1.ReasonLayerConflict))
		Expect(cond.Message).To(Equal("python-fastapi and clashing both write pyproject.toml"))
	})

	It("fails a template that has no files for the Git host's CI system, naming the ones it has", func() {
		ns := newProjectNamespace()
		gitFake.AddTemplate(templatesURL+"gitlab-compute", "v1.0.0", scaffold.Tree{
			".theos/ci/gitlabCI/.gitlab-ci.yml": {Data: []byte("check:\n")},
		})
		createFactory(ns)
		mustCreate(&c4v1beta1.Compute{ObjectMeta: objectMeta(ns, "gitlab-only"),
			Spec: newCatalogSpec(templatesURL+"gitlab-compute", "v1.0.0", c4v1beta1.ContainerTypeService)})
		container := newContainer(ns, "orders-api", "webshop-core", c4v1beta1.ContainerTypeService)
		container.Spec.ComputeRef = &c4v1beta1.LocalRef{Name: "gitlab-only"}
		mustCreate(newSystem(ns, "webshop-core"), container)

		container = eventuallyOrdersAPI(ns, c4v1beta1.ContainerFailed)
		cond := condition(container.Status.Conditions, c4v1beta1.ConditionScaffolded)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Reason).To(Equal(c4v1beta1.ReasonUnsupportedCI))
		Expect(cond.Message).To(Equal("gitlab-only doesn't support giteaActions; it supports gitlabCI"))
	})

	It("fails a template pinned to a branch", func() {
		ns := newProjectNamespace()
		createFactory(ns)
		var framework c4v1beta1.Framework
		Expect(k8sClient.Get(ctx, key(ns, "python-fastapi"), &framework)).To(Succeed())
		Expect(patch(&framework, func() { framework.Spec.Template.Ref = "main" })).To(Succeed())
		mustCreate(newSystem(ns, "webshop-core"),
			newContainer(ns, "orders-api", "webshop-core", c4v1beta1.ContainerTypeService))

		container := eventuallyOrdersAPI(ns, c4v1beta1.ContainerFailed)
		cond := condition(container.Status.Conditions, c4v1beta1.ConditionScaffolded)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Reason).To(Equal(c4v1beta1.ReasonUnpinnedRef))
		Expect(cond.Message).To(ContainSubstring("Framework python-fastapi"))
	})

	It("waits for the GitProvider's credentials", func() {
		ns := newProjectNamespace()
		createFactory(ns)
		Expect(k8sClient.Delete(ctx, &corev1.Secret{ObjectMeta: objectMeta(ns, "gitea-credentials")})).To(Succeed())
		mustCreate(newSystem(ns, "webshop-core"),
			newContainer(ns, "orders-api", "webshop-core", c4v1beta1.ContainerTypeService))
		Eventually(func(g Gomega) {
			var container c4v1beta1.Container
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), &container)).To(Succeed())
			g.Expect(container.Status.Phase).To(Equal(c4v1beta1.ContainerPending))
			g.Expect(condition(container.Status.Conditions, c4v1beta1.ConditionReferencesResolved)).
				To(HaveField("Message", "Secret gitea-credentials not found"))
		}).Should(Succeed())

		By("scaffolding once the Secret appears, though Secrets aren't watched")
		mustCreate(&corev1.Secret{
			ObjectMeta: objectMeta(ns, "gitea-credentials"),
			StringData: map[string]string{"username": "c4admin", "token": rand.Text()},
		})
		Eventually(func(g Gomega) {
			var container c4v1beta1.Container
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), &container)).To(Succeed())
			g.Expect(container.Status.Phase).To(Equal(c4v1beta1.ContainerReady))
		}).Should(Succeed())
	})
})
