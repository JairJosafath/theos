/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"crypto/rand"
	"slices"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	eventsv1 "k8s.io/api/events/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	c4v1beta1 "github.com/JairJosafath/c4factory/api/v1beta1"
	"github.com/JairJosafath/c4factory/internal/gitprovider"
)

// appSource is the files of orders-api at v0.1.0 for module path, the
// factory's own among them, and the app's alone as they are published.
func appSource(module string) (all []fakeSourceFile, app map[string]string) {
	all = []fakeSourceFile{
		{"go.mod", gitprovider.SourcePlain, "module " + module + "\n\ngo 1.25\n"},
		{"main.go", gitprovider.SourcePlain, "package main\n\nfunc main() {}\n"},
		{"scripts/run.sh", gitprovider.SourceExecutable, "#!/bin/sh\ngo run .\n"},
		{"run", gitprovider.SourceSymlink, "scripts/run.sh"},
		{"LICENSE", gitprovider.SourcePlain, "MIT License\n"},
		{"README.md", gitprovider.SourcePlain, "# orders-api\n"},
		{"AGENTS.md", gitprovider.SourcePlain, "# Agents\n"},
		{".c4factory.yaml", gitprovider.SourcePlain, "container: orders-api\n"},
		{".gitea/workflows/ci.yaml", gitprovider.SourcePlain, "on: push\n"},
		{"docs/agents/review.md", gitprovider.SourcePlain, "Review well.\n"},
	}
	app = map[string]string{}
	for _, f := range all[:6] {
		app[f.path] = f.data
	}
	return all, app
}

// leakedSource is the files of appSource with config/app.env, whose line 2
// holds a made-up GitHub token, random each run (the secrets skill), and the
// token.
func leakedSource(module string) ([]fakeSourceFile, string) {
	all, _ := appSource(module)
	token := "gh" + "p_" + rand.Text() + rand.Text()[:10]
	return append(all, fakeSourceFile{"config/app.env", gitprovider.SourcePlain, "PORT=8080\nTOKEN=" + token + "\n"}),
		token
}

// sourceCrew makes publishingCrew's release, with module's source in Gitea,
// and a Crew that publishes it to repository with its source in folder path,
// the release tagged path-v0.1.0 there. With no Secret, it waits for one.
func sourceCrew(ns, repository, path, module string) map[string]string {
	GinkgoHelper()
	all, app := appSource(module)
	gitFake.setSource(ns, "orders-api", "v0.1.0", all...)
	publishingCrew(ns, repository, func(crew *c4v1beta1.Crew) {
		crew.Spec.PublishReleases.TagPrefix = path + "-"
		crew.Spec.PublishReleases.Latest = new(false)
		crew.Spec.PublishReleases.Source = &c4v1beta1.CrewPublishSource{Path: path}
	})
	return app
}

// sourcePublished waits for the Crew's release and its source to be published
// to repository, its source in folder path, and checks the source tag names a
// commit whose folder is app, made by the Crew's commit, and the release's tag
// that same commit.
func sourcePublished(ns, repository, path string, app map[string]string) {
	GinkgoHelper()
	sourceTag := path + "/v0.1.0"
	url := "https://github.com/" + repository + "/tree/" + sourceTag + "/" + path
	var source *c4v1beta1.CrewReleaseSource
	Eventually(func(g Gomega) {
		crew := getCrew(g, ns)
		g.Expect(crew.Status.LastRelease).NotTo(BeNil())
		g.Expect(crew.Status.LastRelease.Publish).NotTo(BeNil())
		g.Expect(crew.Status.LastRelease.Publish.State).To(Equal("Published"))
		g.Expect(crew.Status.LastRelease.Publish.Message).To(HaveSuffix(", its source at " + url))
		cond := condition(crew.Status.Conditions, ConditionPublished)
		g.Expect(cond).NotTo(BeNil())
		g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		source = crew.Status.LastRelease.Publish.Source
		g.Expect(source).NotTo(BeNil())
		g.Expect(crewEvents(g, ns)).To(ContainElement(And(HaveField("Type", "Normal"),
			HaveField("Reason", "SourcePublished"),
			HaveField("Note", "The source of v0.1.0 is published at "+url))))
	}, publishWait).Should(Succeed())

	Expect(source.Tag).To(Equal(sourceTag))
	Expect(source.URL).To(Equal(url))
	Expect(githubFake.ref(repository, "tags/"+sourceTag)).To(Equal(source.Commit))
	Expect(githubFake.ref(repository, "tags/"+path+"-v0.1.0")).To(Equal(source.Commit),
		"the release's tag names the source commit")
	Expect(githubFake.folder(repository, "tags/"+sourceTag, path)).To(Equal(app),
		"the app's files, the factory's own left out")
	var made fakeGitCommit
	for c := source.Commit; c != ""; {
		commit := githubFake.commitOf(repository, c)
		if commit.message == "orders-api v0.1.0: source\n\nC4factory-Source: "+ns+"/orders-api "+path+"-v0.1.0\n" {
			made = commit
			break
		}
		c = ""
		if len(commit.parents) == 1 {
			c = commit.parents[0]
		}
	}
	Expect(made.tree).NotTo(BeEmpty(), "the Crew's commit is on the branch")
	var rel *fakeGitHubRelease
	for _, r := range githubFake.releases(repository) {
		if r.TagName == path+"-v0.1.0" {
			rel = &r
		}
	}
	Expect(rel).NotTo(BeNil())
	Expect(rel.Draft).To(BeFalse())
	Expect(rel.Target).To(Equal(source.Commit))
}

// sourceWarnings is the Crew's SourceNotGoInstallable Events.
func sourceWarnings(g Gomega, ns string) []eventsv1.Event {
	var out []eventsv1.Event
	for _, e := range crewEvents(g, ns) {
		if e.Reason == "SourceNotGoInstallable" {
			out = append(out, e)
		}
	}
	return out
}

// overlapping waits for the Crew's publish to fail with FolderOverlaps, with
// one Warning Event saying message. The Crew waited for its Secret before.
func overlapping(ns, message string) {
	GinkgoHelper()
	warnings := func(g Gomega) []eventsv1.Event {
		var out []eventsv1.Event
		for _, e := range crewEvents(g, ns) {
			if e.Reason == "ReleasePublishFailed" && strings.Contains(e.Note, message) {
				out = append(out, e)
			}
		}
		return out
	}
	Eventually(func(g Gomega) {
		crew := getCrew(g, ns)
		g.Expect(crew.Status.LastRelease).NotTo(BeNil())
		g.Expect(crew.Status.LastRelease.Publish).NotTo(BeNil())
		g.Expect(crew.Status.LastRelease.Publish.State).To(Equal("Failed"))
		g.Expect(crew.Status.LastRelease.Publish.Message).To(ContainSubstring(message))
		cond := condition(crew.Status.Conditions, ConditionPublished)
		g.Expect(cond).NotTo(BeNil())
		g.Expect(cond.Reason).To(Equal("FolderOverlaps"))
		g.Expect(warnings(g)).NotTo(BeEmpty())
	}).Should(Succeed())
	Consistently(func(g Gomega) {
		g.Expect(warnings(g)).To(ConsistOf(HaveField("Series", BeNil())))
	}, 3*time.Second).Should(Succeed())
}

var _ = Describe("Crew publishing a release's source", func() {
	It("puts each of two apps' source in its folder of one repository, the other's left alone", func() {
		engine, vscode := newProjectNamespace(), newProjectNamespace()
		repository := "acme/" + engine
		token := rand.Text()
		githubFake.addRepository(repository, token)
		// A copy made by hand is replaced; the rest of the repository stays.
		githubFake.putFile(repository, "engine/old.go", "package old\n")
		githubFake.putFile(repository, "docs/index.md", "# Docs\n")
		engineApp := sourceCrew(engine, repository, "engine", "github.com/"+repository+"/engine")
		vscodeApp := sourceCrew(vscode, repository, "vscode", "example.com/vscode")
		mustCreate(publishSecret(engine, token))
		mustCreate(publishSecret(vscode, token))

		sourcePublished(engine, repository, "engine", engineApp)
		sourcePublished(vscode, repository, "vscode", vscodeApp)
		Expect(githubFake.folder(repository, "heads/main", "engine")).To(Equal(engineApp))
		Expect(githubFake.folder(repository, "heads/main", "vscode")).To(Equal(vscodeApp))
		Expect(githubFake.folder(repository, "heads/main", "docs")).To(Equal(map[string]string{
			"index.md": "# Docs\n"}))
		Expect(githubFake.folder(repository, "heads/main", ".")).To(HaveKey("README.md"))

		By("warning once that go install can't fetch the vscode module, which declares another path")
		Eventually(func(g Gomega) {
			g.Expect(sourceWarnings(g, vscode)).To(ConsistOf(And(HaveField("Type", "Warning"),
				HaveField("Note", ContainSubstring("go.mod declares module example.com/vscode, but go install "+
					"from GitHub finds it only as github.com/"+repository+"/vscode")))))
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			g.Expect(sourceWarnings(g, vscode)).To(HaveLen(1))
			g.Expect(sourceWarnings(g, engine)).To(BeEmpty())
		}, 2*time.Second).Should(Succeed())
	})

	It("builds on the new head when another app's commit lands first", func() {
		ns := newProjectNamespace()
		repository := "acme/" + ns
		token := rand.Text()
		githubFake.addRepository(repository, token)
		githubFake.landFirst(repository, "cli/main.go", "package main\n")
		app := sourceCrew(ns, repository, "engine", "github.com/"+repository+"/engine")
		mustCreate(publishSecret(ns, token))

		sourcePublished(ns, repository, "engine", app)
		Expect(githubFake.folder(repository, "heads/main", "cli")).To(Equal(map[string]string{
			"main.go": "package main\n"}), "the other app's commit stays")
	})

	It("publishes nothing while two Crews' folders of one repository overlap", func() {
		engine, nested := newProjectNamespace(), newProjectNamespace()
		repository := "acme/" + engine
		token := rand.Text()
		githubFake.addRepository(repository, token)
		sourceCrew(engine, repository, "engine", "github.com/"+repository+"/engine")
		sourceCrew(nested, "ACME/"+engine, "engine/lsp", "github.com/"+repository+"/engine/lsp")
		mustCreate(publishSecret(engine, token))
		mustCreate(publishSecret(nested, token))

		overlapping(engine, "source folder engine of "+repository+" overlaps that of Crew "+nested+"/orders-api")
		overlapping(nested, "source folder engine/lsp of ACME/"+engine+" overlaps that of Crew "+engine+"/orders-api")
		Expect(githubFake.releases(repository)).To(BeEmpty())
		Expect(githubFake.ref(repository, "tags/engine/v0.1.0")).To(BeEmpty())
	})

	It("leaves a source tag someone else made alone", func() {
		ns := newProjectNamespace()
		repository := "acme/" + ns
		token := rand.Text()
		githubFake.addRepository(repository, token)
		githubFake.tagHead(repository, "engine/v0.1.0")
		head := githubFake.ref(repository, "heads/main")
		sourceCrew(ns, repository, "engine", "github.com/"+repository+"/engine")
		mustCreate(publishSecret(ns, token))

		publishFailedWith(ns, "SourceTagTaken", repository+" already has a tag engine/v0.1.0 whose folder engine "+
			"isn't the source of v0.1.0; it leaves it alone")
		Expect(githubFake.ref(repository, "tags/engine/v0.1.0")).To(Equal(head))
		Expect(githubFake.releases(repository)).To(BeEmpty())
	})

	It("publishes nothing of a release whose source holds a secret, naming the file and line (#1602)", func() {
		ns := newProjectNamespace()
		repository := "acme/" + ns
		token := rand.Text()
		githubFake.addRepository(repository, token)
		head := githubFake.ref(repository, "heads/main")
		sourceCrew(ns, repository, "engine", "github.com/"+repository+"/engine")
		leaky, leaked := leakedSource("github.com/" + repository + "/engine")
		gitFake.setSource(ns, "orders-api", "v0.1.0", leaky...)
		mustCreate(publishSecret(ns, token))

		publishFailedWith(ns, "SecretFound", "the source of v0.1.0 isn't published: config/app.env line 2: "+
			"a GitHub token (ghp_); a newer release publishes instead")
		Expect(getCrew(Default, ns).Status.LastRelease.Publish.Message).NotTo(ContainSubstring(leaked))
		Expect(githubFake.releases(repository)).To(BeEmpty())
		Expect(githubFake.ref(repository, "heads/main")).To(Equal(head))
		Expect(githubFake.ref(repository, "tags/engine/v0.1.0")).To(BeEmpty())
	})

	It("publishes nothing of a release whose source has no license (#1602)", func() {
		ns := newProjectNamespace()
		repository := "acme/" + ns
		token := rand.Text()
		githubFake.addRepository(repository, token)
		sourceCrew(ns, repository, "engine", "github.com/"+repository+"/engine")
		all, _ := appSource("github.com/" + repository + "/engine")
		gitFake.setSource(ns, "orders-api", "v0.1.0", slices.DeleteFunc(all, func(f fakeSourceFile) bool {
			return f.path == "LICENSE"
		})...)
		mustCreate(publishSecret(ns, token))

		publishFailedWith(ns, "LicenseMissing", "no license: set spec.license on the Project or the Container "+
			"before it is scaffolded, or commit a non-empty LICENSE, LICENSE.md, LICENSE.txt or COPYING at the root, "+
			"the only way once scaffolded")
		Expect(githubFake.releases(repository)).To(BeEmpty())
	})
})
