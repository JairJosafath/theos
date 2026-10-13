/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"context"
	"crypto/rand"
	"errors"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	eventsv1 "k8s.io/api/events/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/gitprovider"
)

// unreadableSecrets holds the Secrets, namespace/name, the Crew reconciler is
// refused, as if its role lacked them.
var unreadableSecrets sync.Map

// secretReader is the Crew reconciler's API reader, refused the Secrets of
// unreadableSecrets.
type secretReader struct{ client.Reader }

func (r secretReader) Get(ctx context.Context, key client.ObjectKey, obj client.Object,
	opts ...client.GetOption) error {
	if _, refused := unreadableSecrets.Load(key.String()); refused {
		if _, ok := obj.(*corev1.Secret); ok {
			return apierrors.NewForbidden(corev1.Resource("secrets"), key.Name, errors.New("the role lacks it"))
		}
	}
	return r.Reader.Get(ctx, key, obj, opts...)
}

// publishingCrew makes a release v0.1.0 of orders-api whose runs succeeded,
// with files in Gitea, and a Crew that tags it and publishes it to repository
// with the token in Secret orders-api-github, changed by change when given. It
// returns the files.
func publishingCrew(ns, repository string, change ...func(*c4v1alpha1.Crew)) []fakeFile {
	GinkgoHelper()
	readyContainer(ns)
	crewAgents(ns, 1)
	mergedIssue(ns)
	files := []fakeFile{{"orders-api.vsix", []byte("a vsix")}, {"SHA256SUMS", []byte("abc  orders-api.vsix\n")}}
	gitFake.setGiteaRelease(ns, "orders-api", "v0.1.0", "", files...)
	gitFake.setRuns(ns, "orders-api", "v0.1.0", "5a-merge", gitprovider.WorkflowRun{ID: 5,
		Workflow: "release.yaml", Status: "completed", Conclusion: "success"})
	crew := newCrew(ns)
	crew.Spec.TagReleases = true
	crew.Spec.PublishReleases = &c4v1alpha1.CrewPublishReleases{GitHub: c4v1alpha1.CrewPublishGitHub{
		Repository: repository, TokenSecretRef: c4v1alpha1.LocalRef{Name: "orders-api-github"}}}
	for _, c := range change {
		c(crew)
	}
	mustCreate(crew)
	return files
}

func publishSecret(ns, token string) *corev1.Secret {
	return &corev1.Secret{ObjectMeta: objectMeta(ns, "orders-api-github"),
		StringData: map[string]string{"token": token}}
}

// publishFailedWith waits for the Crew's publish to fail with reason, with
// one Warning Event saying message.
func publishFailedWith(ns, reason, message string) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		crew := getCrew(g, ns)
		g.Expect(crew.Status.LastRelease).NotTo(BeNil())
		g.Expect(crew.Status.LastRelease.Publish).NotTo(BeNil())
		g.Expect(crew.Status.LastRelease.Publish.State).To(Equal("Failed"))
		g.Expect(crew.Status.LastRelease.Publish.Message).To(ContainSubstring(message))
		cond := condition(crew.Status.Conditions, ConditionPublished)
		g.Expect(cond).NotTo(BeNil())
		g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		g.Expect(cond.Reason).To(Equal(reason))
		g.Expect(crewEvents(g, ns)).To(ContainElement(And(HaveField("Type", "Warning"),
			HaveField("Reason", "ReleasePublishFailed"), HaveField("Note", ContainSubstring(message)))))
	}).Should(Succeed())
	// The Crew polls each second; a repeated Warning would be a second Event
	// or a raised series count.
	Consistently(func(g Gomega) {
		var warnings []eventsv1.Event
		for _, e := range crewEvents(g, ns) {
			if e.Reason == "ReleasePublishFailed" {
				warnings = append(warnings, e)
			}
		}
		g.Expect(warnings).To(HaveLen(1))
		g.Expect(warnings[0].Series).To(BeNil())
	}, 3*time.Second).Should(Succeed())
}

// publishedTo waits for the Crew's release to be published to repository with
// the notes of its merged issue and files, checks GitHub holds it so, and
// returns it.
func publishedTo(ns, repository string, files []fakeFile) fakeGitHubRelease {
	const notes = "- Validate orders (#1)\n"
	GinkgoHelper()
	url := "https://github.com/" + repository + "/releases/tag/v0.1.0"
	Eventually(func(g Gomega) {
		crew := getCrew(g, ns)
		g.Expect(crew.Status.LastRelease).NotTo(BeNil())
		g.Expect(crew.Status.LastRelease.Publish).NotTo(BeNil())
		g.Expect(crew.Status.LastRelease.Publish.State).To(Equal("Published"))
		g.Expect(crew.Status.LastRelease.Publish.URL).To(Equal(url))
		cond := condition(crew.Status.Conditions, ConditionPublished)
		g.Expect(cond).NotTo(BeNil())
		g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		g.Expect(cond.Reason).To(Equal("Published"))
		g.Expect(crewEvents(g, ns)).To(ContainElement(And(HaveField("Type", "Normal"),
			HaveField("Reason", "ReleasePublished"), HaveField("Note", "v0.1.0 is published at "+url))))
	}, 20*time.Second).Should(Succeed())

	releases := githubFake.releases(repository)
	Expect(releases).To(HaveLen(1))
	rel := releases[0]
	Expect(rel.TagName).To(Equal("v0.1.0"))
	Expect(rel.Draft).To(BeFalse())
	Expect(rel.Body).To(Equal(notes + "\n<!-- c4factory: " + ns + "/orders-api v0.1.0 -->\n"))
	Expect(rel.assets).To(HaveLen(len(files)))
	for i, f := range files {
		Expect(rel.assets[i].Name).To(Equal(f.name))
		Expect(rel.assets[i].data).To(Equal(f.data))
	}
	return rel
}

var _ = Describe("Crew publishing releases", func() {
	It("needs tagReleases and an owner/name repository", func() {
		ns := newProjectNamespace()
		crew := newCrew(ns)
		crew.Spec.PublishReleases = &c4v1alpha1.CrewPublishReleases{GitHub: c4v1alpha1.CrewPublishGitHub{
			Repository: "acme/orders", TokenSecretRef: c4v1alpha1.LocalRef{Name: "orders-api-github"}}}
		Expect(k8sClient.Create(ctx, crew)).To(MatchError(ContainSubstring("publishReleases needs tagReleases")))

		crew.Spec.TagReleases = true
		crew.Spec.PublishReleases.GitHub.Repository = "https://github.com/acme/orders"
		Expect(k8sClient.Create(ctx, crew)).To(MatchError(ContainSubstring("repository must be owner/name")))
	})

	It("publishes a release whose runs succeeded to GitHub, with its notes and every file", func() {
		ns := newProjectNamespace()
		repository := "acme/" + ns
		token := rand.Text()
		githubFake.addRepository(repository, token)
		mustCreate(publishSecret(ns, token))
		files := publishingCrew(ns, repository)

		rel := publishedTo(ns, repository, files)
		Expect(gitFake.giteaReleaseBody(ns, "orders-api", "v0.1.0")).To(Equal("- Validate orders (#1)\n"),
			"the notes are written on Gitea too")
		Expect(rel.Name).To(Equal("orders-api v0.1.0"), "named after the Container, with no display name")
		Expect(rel.makeLatest).To(Equal("true"))
	})

	It("leaves an issue closed without a merge out of the notes", func() {
		ns := newProjectNamespace()
		repository := "acme/" + ns
		token := rand.Text()
		githubFake.addRepository(repository, token)
		mustCreate(publishSecret(ns, token))
		files := publishingCrew(ns, repository, func(*c4v1alpha1.Crew) {
			// Closed by prompt, as not doable: no Task merged it.
			n := gitFake.addIssue(ns, "orders-api", "Check the cost of every expression", clearIssue, nil)
			gitFake.setIssueRelease(ns, "orders-api", n, "v0.1.0", releaseDue)
			Expect(gitFake.closeIssue(ns, "orders-api", n)).To(Succeed())
		})

		publishedTo(ns, repository, files)
		Expect(gitFake.giteaReleaseBody(ns, "orders-api", "v0.1.0")).To(Equal("- Validate orders (#1)\n"))
	})

	It("names a release after the app's display name, and leaves Latest alone when latest is false", func() {
		ns := newProjectNamespace()
		repository := "acme/" + ns
		token := rand.Text()
		githubFake.addRepository(repository, token)
		// No Secret yet, so nothing is drafted before the display name is set.
		files := publishingCrew(ns, repository, func(crew *c4v1alpha1.Crew) {
			crew.Spec.PublishReleases.Latest = new(false)
		})
		var container c4v1alpha1.Container
		Expect(k8sClient.Get(ctx, key(ns, "orders-api"), &container)).To(Succeed())
		container.Spec.DisplayName = "Orders API"
		Expect(k8sClient.Update(ctx, &container)).To(Succeed())
		publishFailedWith(ns, "SecretMissing", "Secret orders-api-github with the key token is missing")

		mustCreate(publishSecret(ns, token))
		rel := publishedTo(ns, repository, files)
		Expect(rel.TagName).To(Equal("v0.1.0"))
		Expect(rel.Name).To(Equal("Orders API v0.1.0"))
		Expect(rel.makeLatest).To(Equal("false"))
	})

	It("publishes nothing while its target differs from its Brief's, and publishes once they agree (#991)", func() {
		ns := newProjectNamespace()
		repository := "acme/" + ns
		token := rand.Text()
		githubFake.addRepository(repository, token)
		mustCreate(publishSecret(ns, token))
		files := publishingCrew(ns, repository, func(crew *c4v1alpha1.Crew) {
			crew.Labels = map[string]string{c4v1alpha1.LabelBrief: "orders"}
			crew.Annotations = map[string]string{c4v1alpha1.AnnotationBriefPublishReleases: `{"github":` +
				`{"repository":"acme/old-orders","tokenSecretRef":{"name":"orders-api-github"}}}`}
		})

		Eventually(func(g Gomega) {
			crew := getCrew(g, ns)
			target := condition(crew.Status.Conditions, c4v1alpha1.ConditionReleaseTarget)
			g.Expect(target).NotTo(BeNil())
			g.Expect(target.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(target.Reason).To(Equal("DiffersFromBrief"))
			g.Expect(target.Message).To(ContainSubstring("points to " + repository + ", but Brief orders's " +
				"spec.releases entry for orders-api says acme/old-orders"))
			cond := condition(crew.Status.Conditions, ConditionPublished)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(cond.Reason).To(Equal("TargetDiffersFromBrief"))
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			var warnings []eventsv1.Event
			for _, e := range crewEvents(g, ns) {
				if e.Reason == "ReleaseTargetDiffers" {
					warnings = append(warnings, e)
				}
			}
			g.Expect(warnings).To(HaveLen(1))
			g.Expect(warnings[0].Series).To(BeNil())
			g.Expect(githubFake.releases(repository)).To(BeEmpty())
		}, 3*time.Second).Should(Succeed())

		Eventually(func(g Gomega) {
			crew := getCrew(g, ns)
			crew.Annotations[c4v1alpha1.AnnotationBriefPublishReleases] = publishJSON(crew.Spec.PublishReleases)
			g.Expect(k8sClient.Update(ctx, crew)).To(Succeed())
		}).Should(Succeed())
		publishedTo(ns, repository, files)
		target := condition(getCrew(Default, ns).Status.Conditions, c4v1alpha1.ConditionReleaseTarget)
		Expect(target.Reason).To(Equal("SetByBrief"))
	})

	It("drops a held release's Pending publish once the hold is settled to publish nowhere (#1021)", func() {
		ns := newProjectNamespace()
		publishingCrew(ns, "acme/"+ns, func(crew *c4v1alpha1.Crew) {
			crew.Labels = map[string]string{c4v1alpha1.LabelBrief: "orders"}
			crew.Annotations = map[string]string{c4v1alpha1.AnnotationBriefPublishReleases: publishJSON(
				crew.Spec.PublishReleases)}
			crew.Spec.PublishReleases = nil
		})

		Eventually(func(g Gomega) {
			crew := getCrew(g, ns)
			release := crew.Status.LastRelease
			g.Expect(release).NotTo(BeNil())
			g.Expect(release.Publish).NotTo(BeNil())
			g.Expect(release.Publish.State).To(Equal("Pending"))
			cond := condition(crew.Status.Conditions, ConditionPublished)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Reason).To(Equal("TargetDiffersFromBrief"))
		}).Should(Succeed())

		By("removing the Brief's entry")
		Eventually(func(g Gomega) {
			crew := getCrew(g, ns)
			delete(crew.Annotations, c4v1alpha1.AnnotationBriefPublishReleases)
			g.Expect(k8sClient.Update(ctx, crew)).To(Succeed())
		}).Should(Succeed())
		Eventually(func(g Gomega) {
			crew := getCrew(g, ns)
			release := crew.Status.LastRelease
			g.Expect(release).NotTo(BeNil())
			g.Expect(release.Publish).To(BeNil())
			g.Expect(condition(crew.Status.Conditions, ConditionPublished)).To(BeNil())
			app := c4v1alpha1.BriefApp{Releases: releasesOn,
				LastRelease: &c4v1alpha1.BriefAppRelease{Tag: release.Tag, Run: release.Run}}
			g.Expect(appMessage(&app, true)).To(Equal("Released v0.1.0."))
		}).Should(Succeed())
	})

	It("fails without its Secret, and publishes once the Secret is there", func() {
		ns := newProjectNamespace()
		repository := "acme/" + ns
		token := rand.Text()
		githubFake.addRepository(repository, token)
		files := publishingCrew(ns, repository)

		publishFailedWith(ns, "SecretMissing", "Secret orders-api-github with the key token is missing")
		Expect(githubFake.releases(repository)).To(BeEmpty())

		mustCreate(publishSecret(ns, token))
		publishedTo(ns, repository, files)
	})

	It("fails while its Secret can't be read, and publishes once it can", func() {
		ns := newProjectNamespace()
		repository := "acme/" + ns
		token := rand.Text()
		githubFake.addRepository(repository, token)
		mustCreate(publishSecret(ns, token))
		secret := ns + "/orders-api-github"
		unreadableSecrets.Store(secret, true)
		DeferCleanup(func() { unreadableSecrets.Delete(secret) })
		files := publishingCrew(ns, repository)

		publishFailedWith(ns, "SecretUnreadable", `Secret orders-api-github can't be read: `+
			`secrets "orders-api-github" is forbidden: the role lacks it`)
		Expect(githubFake.releases(repository)).To(BeEmpty())

		unreadableSecrets.Delete(secret)
		publishedTo(ns, repository, files)
	})

	It("fails on a repository GitHub doesn't show the token", func() {
		ns := newProjectNamespace()
		mustCreate(publishSecret(ns, rand.Text()))
		publishingCrew(ns, "acme/"+ns)

		publishFailedWith(ns, "RepositoryNotFound", "GitHub has no repository acme/"+ns+" the token in Secret "+
			"orders-api-github can see")
	})
})
