/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package e2e

import (
	"bytes"
	crand "crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/rand"
	"sigs.k8s.io/controller-runtime/pkg/client"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/test/utils"
)

const webshop = "webshop"

// Ordered keeps the milestones in sequence; ContinueOnFailure still runs the rest
// when one fails, so a run shows every milestone's state.
var _ = Describe("C4 Factory", Ordered, ContinueOnFailure, func() {
	Context("milestone 1: test environment", func() {
		It("has the four template repositories, tagged", func() {
			for name, tag := range map[string]string{
				"python-fastapi": "v1.2.1", "small": "v1.0.0", "k8s-manifests": "v1.1.0", "postgres": "v1.1.0",
			} {
				var tags []struct {
					Name string `json:"name"`
				}
				giteaGet("/api/v1/repos/templates/"+name+"/tags", &tags)
				Expect(tags).To(ContainElement(HaveField("Name", tag)), name)
			}
		})

		It("has the gitea-credentials Secret in webshop", func() {
			Expect(giteaToken).NotTo(BeEmpty())
		})
	})

	Context("milestone 2: story model", func() {
		It("builds the webshop tree from parent refs", func() {
			var project c4v1alpha1.Project
			var system c4v1alpha1.SoftwareSystem
			var container c4v1alpha1.Container
			var store c4v1alpha1.DataStore
			var person c4v1alpha1.Person
			var rel c4v1alpha1.Relationship
			Eventually(func(g Gomega) {
				g.Expect(k8s.Get(ctx, key(webshop, webshop), &project)).To(Succeed())
				g.Expect(k8s.Get(ctx, key(webshop, "webshop-core"), &system)).To(Succeed())
				g.Expect(k8s.Get(ctx, key(webshop, "orders-api"), &container)).To(Succeed())
				g.Expect(k8s.Get(ctx, key(webshop, "orders-db"), &store)).To(Succeed())
				g.Expect(k8s.Get(ctx, key(webshop, "customer"), &person)).To(Succeed())
				g.Expect(k8s.Get(ctx, key(webshop, "orders-api-to-orders-db"), &rel)).To(Succeed())
				g.Expect(metav1.IsControlledBy(&system, &project)).To(BeTrue())
				g.Expect(metav1.IsControlledBy(&person, &project)).To(BeTrue())
				g.Expect(metav1.IsControlledBy(&container, &system)).To(BeTrue())
				g.Expect(metav1.IsControlledBy(&store, &system)).To(BeTrue())
				g.Expect(metav1.IsControlledBy(&rel, &container)).To(BeTrue())
				g.Expect(container.Labels).To(HaveKeyWithValue(c4v1alpha1.LabelSoftwareSystem, "webshop-core"))
			}).WithTimeout(time.Minute).Should(Succeed())
		})

		It("rejects a Project not named after its namespace", func() {
			ns := newNamespace("e2e-policy")
			err := k8s.Create(ctx, &c4v1alpha1.Project{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "webshop"}})
			Expect(err).To(MatchError(ContainSubstring("a Project must be named after its namespace")))
		})

		It("cascades when a SoftwareSystem is deleted", func() {
			ns := newNamespace("e2e-cascade")
			system := &c4v1alpha1.SoftwareSystem{ObjectMeta: objMeta(ns, "doomed")}
			container := &c4v1alpha1.Container{ObjectMeta: objMeta(ns, "doomed-api"), Spec: c4v1alpha1.ContainerSpec{
				SoftwareSystemRef: ref("doomed"), Type: c4v1alpha1.ContainerTypeService,
				GitProviderRef: ref("none"), FrameworkRef: ref("none"),
			}}
			store := &c4v1alpha1.DataStore{ObjectMeta: objMeta(ns, "doomed-db"), Spec: c4v1alpha1.DataStoreSpec{
				SoftwareSystemRef: ref("doomed"), ConnectorRef: ref("none"),
			}}
			rel := &c4v1alpha1.Relationship{ObjectMeta: objMeta(ns, "doomed-link"), Spec: c4v1alpha1.RelationshipSpec{
				Source: c4v1alpha1.ElementRef{Kind: c4v1alpha1.ElementContainer, Name: "doomed-api"},
				Target: c4v1alpha1.ElementRef{Kind: c4v1alpha1.ElementDataStore, Name: "doomed-db"},
			}}
			session := &c4v1alpha1.Session{ObjectMeta: objMeta(ns, "doomed-session"), Spec: c4v1alpha1.SessionSpec{
				ContainerRef: ref("doomed-api"), Actor: c4v1alpha1.ActorHuman,
			}}
			for _, obj := range []client.Object{
				&c4v1alpha1.Project{ObjectMeta: objMeta(ns, ns)}, system, container, store, rel, session,
			} {
				Expect(k8s.Create(ctx, obj)).To(Succeed())
			}
			children := []client.Object{container, store, rel, session}
			Eventually(func(g Gomega) {
				for _, obj := range children {
					g.Expect(k8s.Get(ctx, client.ObjectKeyFromObject(obj), obj)).To(Succeed())
					g.Expect(metav1.GetControllerOf(obj)).NotTo(BeNil(), "%T %s not adopted yet", obj, obj.GetName())
				}
			}).WithTimeout(time.Minute).Should(Succeed())

			Expect(k8s.Delete(ctx, system)).To(Succeed())
			Eventually(func(g Gomega) {
				for _, obj := range children {
					err := k8s.Get(ctx, client.ObjectKeyFromObject(obj), obj)
					g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "%T %s still exists", obj, obj.GetName())
				}
			}).WithTimeout(time.Minute).Should(Succeed())
		})
	})

	Context("milestone 3: catalog and GitProvider", func() {
		It("fails a static-site Container that uses python-fastapi, with a clear message", func() {
			ns := newNamespace("e2e-catalog")
			kubectl("apply", "-n", ns, "-f", "config/samples/webshop/factory.yaml")
			Expect(k8s.Create(ctx, &corev1.Secret{ObjectMeta: objMeta(ns, "gitea-credentials"),
				StringData: map[string]string{"username": "nobody", "token": crand.Text()}})).To(Succeed())
			for _, obj := range []client.Object{
				&c4v1alpha1.Project{ObjectMeta: objMeta(ns, ns)},
				&c4v1alpha1.SoftwareSystem{ObjectMeta: objMeta(ns, "webshop-core")},
				&c4v1alpha1.Container{ObjectMeta: objMeta(ns, "storefront"), Spec: c4v1alpha1.ContainerSpec{
					SoftwareSystemRef: ref("webshop-core"), Type: c4v1alpha1.ContainerTypeStaticSite,
					GitProviderRef: ref("gitea"), FrameworkRef: ref("python-fastapi"),
				}},
			} {
				Expect(k8s.Create(ctx, obj)).To(Succeed())
			}
			Eventually(func(g Gomega) {
				var c c4v1alpha1.Container
				g.Expect(k8s.Get(ctx, key(ns, "storefront"), &c)).To(Succeed())
				g.Expect(c.Status.Phase).To(Equal(c4v1alpha1.ContainerFailed))
				cond := meta.FindStatusCondition(c.Status.Conditions, c4v1alpha1.ConditionChoicesValid)
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(cond.Message).To(Equal("python-fastapi does not support static-site"))
			}).WithTimeout(time.Minute).Should(Succeed())
		})
	})

	Context("milestone 4: scaffolding", func() {
		It("has scaffolded orders-api into Gitea, once", func() {
			var container c4v1alpha1.Container
			Eventually(func(g Gomega) {
				g.Expect(k8s.Get(ctx, key(webshop, "orders-api"), &container)).To(Succeed())
				g.Expect(container.Status.Phase).To(Equal(c4v1alpha1.ContainerReady))
			}).WithTimeout(3 * time.Minute).Should(Succeed())
			Expect(container.Status.RepositoryURL).To(Equal("http://gitea.gitea.svc.cluster.local:3000/webshop/orders-api"))
			Expect(container.Status.ScaffoldedFrom).To(Equal([]string{
				"python-fastapi@v1.2.1", "small@v1.0.0", "k8s-manifests@v1.1.0", "postgres@v1.1.0",
			}))
			Expect(commitsOf("orders-api")).To(HaveLen(1))

			By("a repeat reconcile changing nothing")
			before := container.Status.DeepCopy()
			poke(&container)
			Consistently(func(g Gomega) {
				g.Expect(k8s.Get(ctx, key(webshop, "orders-api"), &container)).To(Succeed())
				g.Expect(container.Status.Phase).To(Equal(before.Phase))
				g.Expect(container.Status.ScaffoldedFrom).To(Equal(before.ScaffoldedFrom))
				g.Expect(container.Status.Conditions).To(Equal(before.Conditions))
			}).WithTimeout(10 * time.Second).WithPolling(time.Second).Should(Succeed())
			Expect(commitsOf("orders-api")).To(HaveLen(1))
		})

		It("scaffolds a fresh Container with every layer and the Session feature", func() {
			ns := newNamespace("e2e-scaffold")
			name := "e2e-api-" + rand.String(5)
			DeferCleanup(func() { giteaDelete("/api/v1/repos/webshop/" + name) })
			kubectl("apply", "-n", ns, "-f", "config/samples/webshop/factory.yaml")
			copySecret(webshop, ns, "gitea-credentials")
			for _, obj := range []client.Object{
				&c4v1alpha1.Project{ObjectMeta: objMeta(ns, ns)},
				&c4v1alpha1.SoftwareSystem{ObjectMeta: objMeta(ns, "webshop-core")},
				&c4v1alpha1.DataStore{ObjectMeta: objMeta(ns, "orders-db"), Spec: c4v1alpha1.DataStoreSpec{
					SoftwareSystemRef: ref("webshop-core"), ConnectorRef: ref("postgres"),
				}},
				&c4v1alpha1.DataStore{ObjectMeta: objMeta(ns, "audit-db"), Spec: c4v1alpha1.DataStoreSpec{
					SoftwareSystemRef: ref("webshop-core"), ConnectorRef: ref("postgres"),
				}},
				&c4v1alpha1.Relationship{ObjectMeta: objMeta(ns, name+"-to-orders-db"), Spec: c4v1alpha1.RelationshipSpec{
					Source: c4v1alpha1.ElementRef{Kind: c4v1alpha1.ElementContainer, Name: name},
					Target: c4v1alpha1.ElementRef{Kind: c4v1alpha1.ElementDataStore, Name: "orders-db"},
				}},
				&c4v1alpha1.Container{ObjectMeta: objMeta(ns, name), Spec: c4v1alpha1.ContainerSpec{
					SoftwareSystemRef: ref("webshop-core"), DisplayName: "E2E API", Type: c4v1alpha1.ContainerTypeService,
					GitProviderRef: ref("gitea"), FrameworkRef: ref("python-fastapi"),
					ComputeRef: refPtr("small"), DeploymentRef: refPtr("k8s-manifests"),
				}},
			} {
				Expect(k8s.Create(ctx, obj)).To(Succeed())
			}
			Eventually(func(g Gomega) {
				var c c4v1alpha1.Container
				g.Expect(k8s.Get(ctx, key(ns, name), &c)).To(Succeed())
				g.Expect(c.Status.Phase).To(Equal(c4v1alpha1.ContainerReady))
			}).WithTimeout(3 * time.Minute).Should(Succeed())

			By("pushing the uv project, the manifests and a devcontainer with Postgres and the Session client")
			files := filesOf(name)
			Expect(files).To(ContainElements(
				".c4factory.yaml", "pyproject.toml", "app/main.py", "tests/test_main.py", "Dockerfile",
				"deploy/k8s/kustomization.yaml", "deploy/k8s/compute.yaml", "deploy/k8s/connector-postgres.yaml",
				".devcontainer/devcontainer.json", ".devcontainer/compose.yaml", ".devcontainer/compose.postgres.yaml",
				".devcontainer/c4session/install.sh", ".devcontainer/c4session/devcontainer-feature.json",
			))
			Expect(files).NotTo(ContainElement(HavePrefix(".c4factory/")))
			commits := commitsOf(name)
			Expect(commits).To(HaveLen(1))
			Expect(commits[0]).To(ContainSubstring("C4-Factory-Container: " + ns + "/" + name))

			By("marking a Relationship added after scaffolding as not applied")
			late := &c4v1alpha1.Relationship{ObjectMeta: objMeta(ns, name+"-to-audit-db"), Spec: c4v1alpha1.RelationshipSpec{
				Source: c4v1alpha1.ElementRef{Kind: c4v1alpha1.ElementContainer, Name: name},
				Target: c4v1alpha1.ElementRef{Kind: c4v1alpha1.ElementDataStore, Name: "audit-db"},
			}}
			Expect(k8s.Create(ctx, late)).To(Succeed())
			Eventually(func(g Gomega) {
				g.Expect(k8s.Get(ctx, client.ObjectKeyFromObject(late), late)).To(Succeed())
				cond := meta.FindStatusCondition(late.Status.Conditions, c4v1alpha1.ConditionApplied)
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Reason).To(Equal(c4v1alpha1.ReasonAddedAfterScaffold))
			}).WithTimeout(time.Minute).Should(Succeed())
			Expect(commitsOf(name)).To(HaveLen(1))
		})
	})

	Context("milestone 5: sessions", func() {
		BeforeEach(func() {
			kubectl("-n", webshop, "delete", "pod", "session-orders-api", "--ignore-not-found", "--wait=true")
			kubectl("-n", webshop, "delete", "sessions", "--all", "--wait=true")
		})

		It("registers the test pod's Session and deletes it when the pod stops", func() {
			startSessionPod()
			name := activeSession()
			kubectl("-n", webshop, "delete", "pod", "session-orders-api", "--wait=true")
			Eventually(func() bool {
				return apierrors.IsNotFound(k8s.Get(ctx, key(webshop, name), &c4v1alpha1.Session{}))
			}).WithTimeout(30 * time.Second).Should(BeTrue())
		})

		It("marks the Session Stale after the client is killed", func() {
			startSessionPod()
			name := activeSession()
			kubectl("-n", webshop, "exec", "session-orders-api", "-c", "client", "--", "pkill", "-KILL", "c4session")
			Eventually(func(g Gomega) {
				var s c4v1alpha1.Session
				g.Expect(k8s.Get(ctx, key(webshop, name), &s)).To(Succeed())
				g.Expect(s.Status.Phase).To(Equal(c4v1alpha1.SessionStale))
				var c c4v1alpha1.Container
				g.Expect(k8s.Get(ctx, key(webshop, "orders-api"), &c)).To(Succeed())
				g.Expect(c.Status.ActiveSessions).To(BeZero())
			}).WithTimeout(3 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())
		})
	})
})

func key(ns, name string) types.NamespacedName {
	return types.NamespacedName{Namespace: ns, Name: name}
}

func objMeta(ns, name string) metav1.ObjectMeta { return metav1.ObjectMeta{Namespace: ns, Name: name} }

func ref(name string) c4v1alpha1.LocalRef { return c4v1alpha1.LocalRef{Name: name} }

func refPtr(name string) *c4v1alpha1.LocalRef { return &c4v1alpha1.LocalRef{Name: name} }

// newNamespace creates a namespace for one spec and deletes it afterwards.
func newNamespace(prefix string) string {
	GinkgoHelper()
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: prefix + "-"}}
	Expect(k8s.Create(ctx, ns)).To(Succeed())
	DeferCleanup(func() { _ = k8s.Delete(ctx, ns) })
	return ns.Name
}

func kubectl(args ...string) string {
	GinkgoHelper()
	out, err := utils.Run(exec.Command("kubectl", append([]string{"--context", kubeCtx}, args...)...))
	Expect(err).NotTo(HaveOccurred())
	return out
}

func copySecret(from, to, name string) {
	GinkgoHelper()
	var secret corev1.Secret
	Expect(k8s.Get(ctx, key(from, name), &secret)).To(Succeed())
	Expect(k8s.Create(ctx, &corev1.Secret{ObjectMeta: objMeta(to, name), Data: secret.Data})).To(Succeed())
}

// poke changes an annotation, which makes the operator reconcile the object.
func poke(obj client.Object) {
	GinkgoHelper()
	patch := fmt.Sprintf(`{"metadata":{"annotations":{"e2e.c4.factory.io/poke":%q}}}`, time.Now().Format(time.RFC3339Nano))
	Expect(k8s.Patch(ctx, obj, client.RawPatch(types.MergePatchType, []byte(patch)))).To(Succeed())
}

func startSessionPod() {
	GinkgoHelper()
	kubectl("apply", "-f", "hack/session/pod.yaml")
	kubectl("-n", webshop, "wait", "pod/session-orders-api", "--for=condition=Ready", "--timeout=120s")
}

// activeSession waits for the test pod's Session to be Active on orders-api.
func activeSession() string {
	GinkgoHelper()
	var name string
	Eventually(func(g Gomega) {
		var list c4v1alpha1.SessionList
		g.Expect(k8s.List(ctx, &list, client.InNamespace(webshop))).To(Succeed())
		g.Expect(list.Items).To(HaveLen(1))
		s := list.Items[0]
		g.Expect(s.Spec.ContainerRef.Name).To(Equal("orders-api"))
		g.Expect(s.Spec.Actor).To(Equal(c4v1alpha1.ActorAgent))
		g.Expect(s.Status.Phase).To(Equal(c4v1alpha1.SessionActive))
		var c c4v1alpha1.Container
		g.Expect(k8s.Get(ctx, key(webshop, "orders-api"), &c)).To(Succeed())
		g.Expect(c.Status.ActiveSessions).To(BeEquivalentTo(1))
		name = s.Name
	}).WithTimeout(time.Minute).Should(Succeed())
	return name
}

func giteaRequest(method, path string) *http.Response {
	GinkgoHelper()
	req, err := http.NewRequestWithContext(ctx, method, giteaURL+path, nil)
	Expect(err).NotTo(HaveOccurred())
	req.Header.Set("Authorization", "token "+giteaToken)
	resp, err := http.DefaultClient.Do(req)
	Expect(err).NotTo(HaveOccurred())
	return resp
}

// giteaGet reads a Gitea API path into into. Right after a push the API can still
// report a repository as empty (409) for a moment, so it retries briefly.
func giteaGet(path string, into any) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		resp := giteaRequest(http.MethodGet, path)
		defer func() { _ = resp.Body.Close() }()
		g.Expect(resp.StatusCode).To(Equal(http.StatusOK), path)
		g.Expect(json.NewDecoder(resp.Body).Decode(into)).To(Succeed())
	}).WithTimeout(15 * time.Second).WithPolling(500 * time.Millisecond).Should(Succeed())
}

// giteaSend sends body as JSON to a Gitea API path and reads the reply into into.
func giteaSend(method, path string, body, into any) {
	GinkgoHelper()
	data, err := json.Marshal(body)
	Expect(err).NotTo(HaveOccurred())
	req, err := http.NewRequestWithContext(ctx, method, giteaURL+path, bytes.NewReader(data))
	Expect(err).NotTo(HaveOccurred())
	req.Header.Set("Authorization", "token "+giteaToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	Expect(err).NotTo(HaveOccurred())
	defer func() { _ = resp.Body.Close() }()
	Expect(resp.StatusCode).To(BeNumerically("<", 300), "%s %s", method, path)
	Expect(json.NewDecoder(resp.Body).Decode(into)).To(Succeed())
}

func giteaDelete(path string) {
	resp := giteaRequest(http.MethodDelete, path)
	_ = resp.Body.Close()
}

// commitsOf returns the commit messages on the default branch of webshop/<repo>.
func commitsOf(repo string) []string {
	GinkgoHelper()
	var commits []struct {
		Commit struct {
			Message string `json:"message"`
		} `json:"commit"`
	}
	giteaGet(fmt.Sprintf("/api/v1/repos/webshop/%s/commits?limit=10", repo), &commits)
	out := make([]string, 0, len(commits))
	for _, c := range commits {
		out = append(out, c.Commit.Message)
	}
	return out
}

// filesOf lists every file on the default branch of webshop/<repo>.
func filesOf(repo string) []string {
	GinkgoHelper()
	var tree struct {
		Tree []struct {
			Path string `json:"path"`
			Type string `json:"type"`
		} `json:"tree"`
	}
	giteaGet(fmt.Sprintf("/api/v1/repos/webshop/%s/git/trees/main?recursive=1&per_page=1000", repo), &tree)
	var files []string
	for _, e := range tree.Tree {
		if e.Type == "blob" {
			files = append(files, e.Path)
		}
	}
	return files
}
