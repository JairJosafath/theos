/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"crypto/rand"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	eventsv1 "k8s.io/api/events/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/gitprovider"
)

// publishingCrew makes a release v0.1.0 of orders-api whose runs succeeded,
// with files in Gitea, and a Crew that tags it and publishes it to repository
// with the token in Secret orders-api-github. It returns the files.
func publishingCrew(ns, repository string) []fakeFile {
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
// notes and files, and checks GitHub holds it so.
func publishedTo(ns, repository, notes string, files []fakeFile) {
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
	Expect(rel.Name).To(Equal("v0.1.0"))
	Expect(rel.Draft).To(BeFalse())
	Expect(rel.Body).To(Equal(notes + "\n<!-- c4factory: " + ns + "/orders-api v0.1.0 -->\n"))
	Expect(rel.assets).To(HaveLen(len(files)))
	for i, f := range files {
		Expect(rel.assets[i].Name).To(Equal(f.name))
		Expect(rel.assets[i].data).To(Equal(f.data))
	}
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

		notes := "- Validate orders (#1)\n"
		publishedTo(ns, repository, notes, files)
		Expect(gitFake.giteaReleaseBody(ns, "orders-api", "v0.1.0")).To(Equal(notes),
			"the notes are written on Gitea too")
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
		publishedTo(ns, repository, "- Validate orders (#1)\n", files)
	})

	It("fails on a repository GitHub doesn't show the token", func() {
		ns := newProjectNamespace()
		mustCreate(publishSecret(ns, rand.Text()))
		publishingCrew(ns, "acme/"+ns)

		publishFailedWith(ns, "RepositoryNotFound", "GitHub has no repository acme/"+ns+" the token in Secret "+
			"orders-api-github can see")
	})
})
