/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"maps"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"
	eventsv1 "k8s.io/api/events/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/model"
	"github.com/JairJosafath/c4factory/internal/scaffold"
)

// updatedStandard is the Standard the Container in these tests uses.
const updatedStandard = "python-strict"

// standardSteps is how long a spec waits for the Container to get somewhere:
// scaffolded and Ready, or a Standard update planned, pushed and opened, or
// merged. Each takes a chain of reconciles, under a second on an idle device;
// with three make checks at once the scaffold alone was seen past 10s (#597),
// and an API call alone at 8s (#501).
const standardSteps = 30 * time.Second

// Files of the Standard's template.
const (
	agentsTemplate = "AGENTS.md.tmpl"
	implementer    = "docs/agents/implementer.md"
	reviewer       = "docs/agents/reviewer.md"
	makefile       = "Makefile"
)

// standardRefs are the Standard's template at each ref. v1.0.1 renders as
// v1.0.0 does; v1.1.0 changes AGENTS.md and the implementer's guide; v1.2.0
// changes them again and the Makefile, and drops the reviewer's guide; v1.3.0
// changes the implementer's guide only; v1.4.0 puts back v1.0.0's check target
// below a new lint target.
func standardRefs() map[string]scaffold.Tree {
	file := func(s string) scaffold.File { return scaffold.File{Data: []byte(s)} }
	v100 := scaffold.Tree{
		// Rendered with the scaffold's Connectors: an update that rendered with
		// others would find AGENTS.md changed since and leave it out.
		agentsTemplate: file("Rules for {{ .Container }}, which uses{{ range .Connectors }} {{ . }}{{ end }}\n"),
		implementer:    file("Implement the issue.\n"),
		reviewer:       file("Review the change.\n"),
		makefile:       file("check:\n\ttrue\n"),
	}
	v101 := maps.Clone(v100)
	v101[".c4factory/README.md"] = file("Notes, never copied\n")
	v110 := maps.Clone(v100)
	v110[agentsTemplate] = file("Rules for {{ .Container }}, which uses{{ range .Connectors }} {{ . }}{{ end }}.\n" +
		"Run make check.\n")
	v110[implementer] = file("Implement the issue.\nRun make check before you finish.\n")
	v120 := maps.Clone(v110)
	v120[implementer] = file("Implement the issue.\nRun make check before you finish.\nAsk when unsure.\n")
	v120[makefile] = file("check:\n\tmake lint test\n")
	delete(v120, reviewer)
	v130 := maps.Clone(v120)
	v130[implementer] = file("Implement the issue.\nRun make check before you finish.\nAsk when unsure.\nKeep it small.\n")
	v140 := maps.Clone(v130)
	v140[makefile] = file("lint:\n\truff check\n\ncheck:\n\ttrue\n")
	return map[string]scaffold.Tree{"v1.0.0": v100, "v1.0.1": v101, "v1.1.0": v110, "v1.2.0": v120,
		"v1.3.0": v130, "v1.4.0": v140}
}

// updatableContainer creates orders-api, which uses orders-db, on the Standard
// python-strict at v1.0.0, and waits until it is Ready with its Standard files
// up to date.
func updatableContainer(ns string) {
	GinkgoHelper()
	createUpdatableContainer(ns, standardRefs(), false, "")
	eventuallyAt(ns, "v1.0.0")
	Expect(gitFake.mainFiles(ns, "orders-api")).To(HaveKeyWithValue("AGENTS.md",
		HaveField("Data", BeEquivalentTo("Rules for orders-api, which uses postgres\n"))))
}

// createUpdatableContainer creates orders-api, which uses orders-db and asks
// for checks, on the Standard python-strict at v1.0.0 with the template refs,
// which can run the full check only before a release when checksBeforeRelease.
func createUpdatableContainer(ns string, refs map[string]scaffold.Tree, checksBeforeRelease bool,
	checks c4v1alpha1.ChecksMode) {
	GinkgoHelper()
	createFactory(ns)
	standardContainer(ns, nil, refs, checksBeforeRelease, checks)
}

// standardContainer creates orders-api as createUpdatableContainer does, in a
// namespace with a factory, with labels.
func standardContainer(ns string, labels map[string]string, refs map[string]scaffold.Tree,
	checksBeforeRelease bool, checks c4v1alpha1.ChecksMode) {
	GinkgoHelper()
	url := templatesURL + ns + "/" + updatedStandard
	for ref, tree := range refs {
		gitFake.addTemplate(url, ref, tree)
	}
	container := newContainer(ns, "orders-api", "webshop-core", c4v1alpha1.ContainerTypeService)
	container.Labels = labels
	container.Spec.StandardRef = &c4v1alpha1.LocalRef{Name: updatedStandard}
	container.Spec.Checks = checks
	mustCreate(
		newSystem(ns, "webshop-core"),
		newOrdersDB(ns),
		newRelationship(ns, "orders-api-to-orders-db",
			element(c4v1alpha1.ElementContainer, "orders-api"),
			element(c4v1alpha1.ElementDataStore, "orders-db")),
		&c4v1alpha1.Standard{
			ObjectMeta: objectMeta(ns, updatedStandard),
			Spec: c4v1alpha1.StandardSpec{
				CatalogEntrySpec:    newCatalogSpec(url, "v1.0.0", c4v1alpha1.ContainerTypeService),
				SupportedFrameworks: []string{"python-fastapi"},
				ChecksBeforeRelease: checksBeforeRelease,
			},
		},
		container,
	)
}

func getContainer(g Gomega, ns string) *c4v1alpha1.Container {
	container := &c4v1alpha1.Container{}
	g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), container)).To(Succeed())
	return container
}

// bumpStandard moves the Standard's template to ref.
func bumpStandard(ns, ref string) {
	GinkgoHelper()
	var standard c4v1alpha1.Standard
	Expect(k8sClient.Get(ctx, key(ns, updatedStandard), &standard)).To(Succeed())
	Expect(patch(&standard, func() { standard.Spec.Template.Ref = ref })).To(Succeed())
}

// standardCondition is orders-api's StandardUpdated condition.
func standardCondition(g Gomega, container *c4v1alpha1.Container) metav1.Condition {
	cond := condition(container.Status.Conditions, c4v1alpha1.ConditionStandardUpdated)
	g.Expect(cond).NotTo(BeNil())
	return *cond
}

// eventuallyAt waits until orders-api's Standard files are at ref, with checks
// EveryChange, leftOut left out (behind) and no update in flight.
func eventuallyAt(ns, ref string, leftOut ...string) {
	GinkgoHelper()
	eventuallyAtChecks(ns, model.StandardVersion{Ref: ref, Checks: model.ChecksEveryChange}, leftOut...)
}

// eventuallyAtChecks waits until orders-api's Standard files are at the version
// at, with leftOut left out (behind) and no update in flight.
func eventuallyAtChecks(ns string, at model.StandardVersion, leftOut ...string) {
	GinkgoHelper()
	ref := at.Ref
	leftOutMatcher, behindMatcher := BeEmpty(), BeEmpty()
	if len(leftOut) > 0 {
		leftOutMatcher, behindMatcher = Equal(leftOut), HaveExactElements(func() []any {
			paths := make([]any, 0, len(leftOut))
			for _, p := range leftOut {
				paths = append(paths, HaveField("Path", p))
			}
			return paths
		}()...)
	}
	Eventually(func(g Gomega) {
		container := getContainer(g, ns)
		g.Expect(container.Status.Phase).To(Equal(c4v1alpha1.ContainerReady))
		g.Expect(container.Status.Standard).To(PointTo(MatchAllFields(Fields{
			"Name": Equal(updatedStandard), "Ref": Equal(ref), "Update": BeNil(), "LeftOut": leftOutMatcher,
			"Behind": behindMatcher, "Kept": BeEmpty(),
			"Checks": BeEquivalentTo(at.Checks)})))
		cond := standardCondition(g, container)
		if len(leftOut) > 0 {
			g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(cond.Reason).To(Equal(c4v1alpha1.ReasonFilesBehind))
		} else {
			g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			g.Expect(cond.Reason).To(Equal(c4v1alpha1.ReasonUpToDate))
		}
		g.Expect(cond.Message).To(HavePrefix(updatedStandard + " is at " + at.String()))
	}).WithTimeout(standardSteps).Should(Succeed())
}

// behindEvents are the StandardFilesBehind Events about orders-api, each sent
// once.
func behindEvents(g Gomega, ns string) []string {
	var events eventsv1.EventList
	g.Expect(k8sClient.List(ctx, &events, client.InNamespace(ns))).To(Succeed())
	var out []string
	for _, e := range events.Items {
		if e.Regarding.Kind == "Container" && e.Regarding.Name == "orders-api" && e.Reason == "StandardFilesBehind" {
			g.Expect(e.Type).To(Equal("Warning"))
			g.Expect(e.Series).To(BeNil(), "the recorder folds a repeat into a series")
			out = append(out, e.Note)
		}
	}
	return out
}

// openedUpdate waits for an update to ref in flight, and returns it.
func openedUpdate(ns, ref string) c4v1alpha1.StandardUpdateStatus {
	GinkgoHelper()
	var update c4v1alpha1.StandardUpdateStatus
	Eventually(func(g Gomega) {
		container := getContainer(g, ns)
		g.Expect(container.Status.Standard).NotTo(BeNil())
		g.Expect(container.Status.Standard.Update).NotTo(BeNil())
		update = *container.Status.Standard.Update
		g.Expect(update.Ref).To(Equal(ref))
		g.Expect(update.Stopped).To(BeEmpty())
		g.Expect(update.PullRequest).NotTo(BeZero())
		cond := standardCondition(g, container)
		g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		g.Expect(cond.Reason).To(Equal(c4v1alpha1.ReasonUpdating))
		g.Expect(cond.Message).To(Equal("Pull request " + update.PullRequestURL + " brings python-strict from " +
			container.Status.Standard.Ref + " to " + ref + "; it merges once CI passes"))
	}).WithTimeout(standardSteps).Should(Succeed())
	return update
}

// stoppedUpdate waits for the update in flight to stop with reason, and returns
// the condition's message.
func stoppedUpdate(ns, reason string) string {
	GinkgoHelper()
	var message string
	Eventually(func(g Gomega) {
		container := getContainer(g, ns)
		g.Expect(container.Status.Standard).NotTo(BeNil())
		g.Expect(container.Status.Standard.Update).To(PointTo(HaveField("Stopped", reason)))
		cond := standardCondition(g, container)
		g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		g.Expect(cond.Reason).To(Equal(reason))
		message = cond.Message
	}).WithTimeout(standardSteps).Should(Succeed())
	return message
}

func mainFile(ns, path string) string {
	GinkgoHelper()
	files := gitFake.mainFiles(ns, "orders-api")
	Expect(files).To(HaveKey(path))
	return string(files[path].Data)
}

var _ = Describe("Container Standard update", func() {
	It("opens a pull request for a new ref of the Standard and merges it once CI passes", func() {
		ns := newProjectNamespace()
		updatableContainer(ns)

		bumpStandard(ns, "v1.1.0")
		update := openedUpdate(ns, "v1.1.0")
		Expect(update.Branch).To(Equal("standard-update/python-strict-v1.1.0"))
		Expect(update.Paths).To(Equal([]string{"AGENTS.md", implementer}))
		Expect(update.LeftOut).To(BeEmpty())
		Expect(gitFake.pullRequests(ns, "orders-api")).To(ConsistOf(MatchFields(IgnoreExtras, Fields{
			"Number": BeEquivalentTo(update.PullRequest), "Head": Equal(update.Branch),
			"HeadSHA": Equal(update.Commit), "State": Equal("open")})))

		By("waiting while CI runs")
		Consistently(func(g Gomega) {
			g.Expect(gitFake.forcedMerges(ns, "orders-api")).To(BeEmpty())
			g.Expect(standardCondition(g, getContainer(g, ns)).Reason).To(Equal(c4v1alpha1.ReasonUpdating))
		}).WithTimeout(2 * time.Second).Should(Succeed())

		By("merging it as admin once CI passes on its commit")
		gitFake.setChecks(ns, "orders-api", update.Commit, "success")
		eventuallyAt(ns, "v1.1.0")
		Expect(gitFake.forcedMerges(ns, "orders-api")).To(Equal([]int{int(update.PullRequest)}))
		Expect(mainFile(ns, implementer)).To(Equal("Implement the issue.\nRun make check before you finish.\n"))
		Expect(mainFile(ns, "AGENTS.md")).To(Equal("Rules for orders-api, which uses postgres.\nRun make check.\n"))
		Expect(mainFile(ns, reviewer)).To(Equal("Review the change.\n"))
		head := gitFake.mainHead(ns, "orders-api")
		Expect(head.Message).To(HavePrefix("Update the Standard python-strict to v1.1.0\n\n"))
		Expect(head.Message).To(ContainSubstring("Standard-Update: python-strict@v1.0.0..v1.1.0"))
		Expect(gitFake.hasBranch(ns, "orders-api", update.Branch)).To(BeFalse())

		container := getContainer(Default, ns)
		Expect(container.Status.ScaffoldedFrom).To(ContainElement("python-strict@v1.0.0"),
			"scaffoldedFrom keeps recording the scaffold")
	})

	It("leaves out a file the repository changed in the same lines, and flags it until it merges", func() {
		ns := newProjectNamespace()
		updatableContainer(ns)
		gitFake.commitToMain(ns, "orders-api", scaffold.Tree{makefile: {Data: []byte("check:\n\tpytest\n")}})

		bumpStandard(ns, "v1.2.0")
		update := openedUpdate(ns, "v1.2.0")
		Expect(update.Paths).To(Equal([]string{"AGENTS.md", implementer, reviewer}))
		Expect(update.LeftOut).To(Equal([]string{makefile}))

		gitFake.setChecks(ns, "orders-api", update.Commit, "success")
		eventuallyAt(ns, "v1.2.0", makefile)
		Expect(mainFile(ns, makefile)).To(Equal("check:\n\tpytest\n"))
		Expect(mainFile(ns, implementer)).To(HaveSuffix("Ask when unsure.\n"))
		Expect(gitFake.mainFiles(ns, "orders-api")).NotTo(HaveKey(reviewer), "v1.2.0 drops the reviewer's guide")
		container := getContainer(Default, ns)
		Expect(container.Status.Standard.Behind).To(Equal([]c4v1alpha1.StandardFile{
			{Path: makefile, Ref: "v1.0.0", PullRequestURL: update.PullRequestURL}}))
		annotate := "kubectl -n " + ns + " annotate container orders-api c4.factory.io/standard-"
		flagged := "python-strict is at v1.2.0, but its updates left out Makefile (at v1.0.0): the repository " +
			"changed them in lines the Standard changed too. Take the Standard's version: `" + annotate +
			"take=v1.2.0`; or keep the repository's: `" + annotate + "keep=v1.2.0`"
		Expect(standardCondition(Default, container).Message).To(Equal(flagged))
		Eventually(behindEvents).WithArguments(ns).WithTimeout(standardSteps).Should(Equal([]string{flagged}))

		By("keeping it flagged through a ref that doesn't change it")
		bumpStandard(ns, "v1.3.0")
		next := openedUpdate(ns, "v1.3.0")
		Expect(next.Paths).To(Equal([]string{implementer}))
		Expect(next.LeftOut).To(Equal([]string{makefile}))
		gitFake.setChecks(ns, "orders-api", next.Commit, "success")
		eventuallyAt(ns, "v1.3.0", makefile)
		container = getContainer(Default, ns)
		Expect(container.Status.Standard.Behind).To(Equal([]c4v1alpha1.StandardFile{
			{Path: makefile, Ref: "v1.0.0", PullRequestURL: next.PullRequestURL}}),
			"the file stays at the ref its copy was last brought to")
		Eventually(behindEvents).WithArguments(ns).WithTimeout(standardSteps).Should(HaveLen(2), "once per ref")
		Consistently(behindEvents).WithArguments(ns).WithTimeout(2 * time.Second).Should(HaveLen(2))

		By("merging it once a ref's change no longer overlaps the repository's")
		bumpStandard(ns, "v1.4.0")
		last := openedUpdate(ns, "v1.4.0")
		Expect(last.Paths).To(Equal([]string{makefile}))
		Expect(last.Merged).To(Equal([]string{makefile}))
		Expect(last.LeftOut).To(BeEmpty())
		gitFake.setChecks(ns, "orders-api", last.Commit, "success")
		eventuallyAt(ns, "v1.4.0")
		Expect(mainFile(ns, makefile)).To(Equal("lint:\n\truff check\n\ncheck:\n\tpytest\n"))
	})

	It("merges a file the repository changed in other lines, and names it", func() {
		ns := newProjectNamespace()
		updatableContainer(ns)
		gitFake.commitToMain(ns, "orders-api", scaffold.Tree{makefile: {Data: []byte("# Our own targets.\ncheck:\n\ttrue\n")}})

		bumpStandard(ns, "v1.2.0")
		update := openedUpdate(ns, "v1.2.0")
		Expect(update.Paths).To(Equal([]string{"AGENTS.md", makefile, implementer, reviewer}))
		Expect(update.Merged).To(Equal([]string{makefile}))
		Expect(update.LeftOut).To(BeEmpty())

		gitFake.setChecks(ns, "orders-api", update.Commit, "success")
		eventuallyAt(ns, "v1.2.0")
		Expect(mainFile(ns, makefile)).To(Equal("# Our own targets.\ncheck:\n\tmake lint test\n"))
		Expect(gitFake.mainHead(ns, "orders-api").Message).
			To(ContainSubstring("Merged with the repository's changes since v1.0.0: Makefile.\n"))
	})

	It("drops an update whose pull request a person closed until the ref changes again", func() {
		ns := newProjectNamespace()
		updatableContainer(ns)
		bumpStandard(ns, "v1.1.0")
		first := openedUpdate(ns, "v1.1.0")

		gitFake.closePull(ns, "orders-api", int(first.PullRequest))
		message := stoppedUpdate(ns, c4v1alpha1.ReasonUpdateClosed)
		Expect(message).To(Equal("Pull request " + first.PullRequestURL +
			" was closed without merging; python-strict stays at v1.0.0 until its ref or the checks change again"))
		Expect(gitFake.hasBranch(ns, "orders-api", first.Branch)).To(BeFalse())
		Consistently(func(g Gomega) {
			g.Expect(gitFake.pullRequests(ns, "orders-api")).To(HaveLen(1))
			g.Expect(standardCondition(g, getContainer(g, ns)).Reason).To(Equal(c4v1alpha1.ReasonUpdateClosed))
		}).WithTimeout(2 * time.Second).Should(Succeed())

		bumpStandard(ns, "v1.2.0")
		second := openedUpdate(ns, "v1.2.0")
		Expect(second.PullRequest).NotTo(Equal(first.PullRequest))
		Expect(gitFake.pullRequests(ns, "orders-api")).To(HaveLen(2))
	})

	It("closes an update the Standard moved past and opens one for its new ref", func() {
		ns := newProjectNamespace()
		updatableContainer(ns)
		bumpStandard(ns, "v1.1.0")
		first := openedUpdate(ns, "v1.1.0")

		bumpStandard(ns, "v1.2.0")
		second := openedUpdate(ns, "v1.2.0")
		Expect(second.Branch).To(Equal("standard-update/python-strict-v1.2.0"))
		Expect(second.Paths).To(Equal([]string{"AGENTS.md", makefile, implementer, reviewer}))
		Expect(gitFake.pullRequests(ns, "orders-api")).To(ConsistOf(
			MatchFields(IgnoreExtras, Fields{"Number": BeEquivalentTo(first.PullRequest), "State": Equal("closed"),
				"Merged": BeFalse()}),
			MatchFields(IgnoreExtras, Fields{"Number": BeEquivalentTo(second.PullRequest), "State": Equal("open")})))
		Expect(gitFake.hasBranch(ns, "orders-api", first.Branch)).To(BeFalse())

		gitFake.setChecks(ns, "orders-api", second.Commit, "success")
		eventuallyAt(ns, "v1.2.0")
		Expect(mainFile(ns, makefile)).To(Equal("check:\n\tmake lint test\n"))
	})

	It("takes no step from a Container its cache has not caught up with", func() {
		ns := newProjectNamespace()
		updatableContainer(ns)
		stale := getContainer(Default, ns)
		bumpStandard(ns, "v1.1.0")
		first := openedUpdate(ns, "v1.1.0")

		// A reconcile queued before the update opened still reads no update in flight.
		r := &ContainerReconciler{Client: k8sClient, Scheme: scheme.Scheme, APIReader: k8sClient, Git: gitFake,
			ScaffoldTimeout: testScaffoldTimeout}
		status := stale.Status.DeepCopy()
		r.updateStandard(ctx, stale, status)
		Expect(status.Standard).To(Equal(stale.Status.Standard))
		Expect(gitFake.pullRequests(ns, "orders-api")).To(ConsistOf(MatchFields(IgnoreExtras, Fields{
			"Number": BeEquivalentTo(first.PullRequest), "HeadSHA": Equal(first.Commit), "State": Equal("open")})))
	})

	It("waits for a re-run when CI fails, and merges once it passes", func() {
		ns := newProjectNamespace()
		updatableContainer(ns)
		bumpStandard(ns, "v1.1.0")
		update := openedUpdate(ns, "v1.1.0")

		gitFake.setChecks(ns, "orders-api", update.Commit, "failure")
		Eventually(func(g Gomega) {
			container := getContainer(g, ns)
			cond := standardCondition(g, container)
			g.Expect(cond.Reason).To(Equal(c4v1alpha1.ReasonChecksFailed))
			g.Expect(cond.Message).To(Equal("CI is failure on http://gitea/" + ns + "/orders-api/actions/runs/1; " +
				"the update merges once a re-run passes, or close " + update.PullRequestURL + " to drop it"))
			g.Expect(container.Status.Standard.Update).To(PointTo(HaveField("Stopped", BeEmpty())))
		}).WithTimeout(standardSteps).Should(Succeed())
		Consistently(func() []int { return gitFake.forcedMerges(ns, "orders-api") }).
			WithTimeout(2 * time.Second).Should(BeEmpty())

		gitFake.setChecks(ns, "orders-api", update.Commit, "success")
		eventuallyAt(ns, "v1.1.0")
		Expect(gitFake.forcedMerges(ns, "orders-api")).To(Equal([]int{int(update.PullRequest)}))
	})

	It("refuses an update someone else pushed to", func() {
		ns := newProjectNamespace()
		updatableContainer(ns)
		bumpStandard(ns, "v1.1.0")
		update := openedUpdate(ns, "v1.1.0")

		gitFake.pushBranch(ns, "orders-api", update.Branch, "sha-person")
		gitFake.setChecks(ns, "orders-api", "sha-person", "success")
		message := stoppedUpdate(ns, c4v1alpha1.ReasonUpdateRefused)
		Expect(message).To(Equal("Someone else pushed to standard-update/python-strict-v1.1.0; a person looks at " +
			update.PullRequestURL))
		Consistently(func(g Gomega) {
			g.Expect(gitFake.forcedMerges(ns, "orders-api")).To(BeEmpty())
			g.Expect(gitFake.pullRequests(ns, "orders-api")).To(ConsistOf(HaveField("State", "open")))
		}).WithTimeout(2 * time.Second).Should(Succeed())
	})

	It("refuses an update whose pull request changes files it did not plan", func() {
		ns := newProjectNamespace()
		updatableContainer(ns)
		bumpStandard(ns, "v1.1.0")
		update := openedUpdate(ns, "v1.1.0")

		gitFake.setPullFiles(ns, "orders-api", int(update.PullRequest), "AGENTS.md", implementer, makefile)
		gitFake.setChecks(ns, "orders-api", update.Commit, "success")
		message := stoppedUpdate(ns, c4v1alpha1.ReasonUpdateRefused)
		Expect(message).To(Equal("The update may not merge: the pull request changes Makefile, which the update " +
			"did not plan; a person looks at " + update.PullRequestURL))
		Expect(gitFake.forcedMerges(ns, "orders-api")).To(BeEmpty())
	})

	It("leaves a Standard added after the scaffold alone", func() {
		ns := newProjectNamespace()
		createFactory(ns)
		mustCreate(newSystem(ns, "webshop-core"),
			newContainer(ns, "orders-api", "webshop-core", c4v1alpha1.ContainerTypeService),
			&c4v1alpha1.Standard{
				ObjectMeta: objectMeta(ns, updatedStandard),
				Spec: c4v1alpha1.StandardSpec{
					CatalogEntrySpec:    newCatalogSpec(templatesURL+ns+"/"+updatedStandard, "v1.0.0", c4v1alpha1.ContainerTypeService),
					SupportedFrameworks: []string{"python-fastapi"},
				},
			})
		container := eventuallyOrdersAPI(ns, c4v1alpha1.ContainerReady)
		Expect(condition(container.Status.Conditions, c4v1alpha1.ConditionStandardUpdated)).To(BeNil())

		Expect(patch(container, func() {
			container.Spec.StandardRef = &c4v1alpha1.LocalRef{Name: updatedStandard}
		})).To(Succeed())
		Eventually(func(g Gomega) {
			c := getContainer(g, ns)
			cond := standardCondition(g, c)
			g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(cond.Reason).To(Equal(c4v1alpha1.ReasonAddedAfterScaffold))
			g.Expect(cond.Message).To(Equal("The Standard python-strict was not part of the scaffold; " +
				"its updates don't reach this Container"))
			g.Expect(c.Status.Standard).To(BeNil())
		}).WithTimeout(standardSteps).Should(Succeed())
		Expect(gitFake.pullRequests(ns, "orders-api")).To(BeEmpty())
	})

	It("records a ref that renders the files main already holds, without a pull request", func() {
		ns := newProjectNamespace()
		updatableContainer(ns)
		head := gitFake.mainHead(ns, "orders-api")

		bumpStandard(ns, "v1.0.1")
		eventuallyAt(ns, "v1.0.1")
		Expect(gitFake.pullRequests(ns, "orders-api")).To(BeEmpty())
		Expect(gitFake.mainHead(ns, "orders-api")).To(Equal(head))
	})

	It("plans an update again when main moved with conflicting changes", func() {
		ns := newProjectNamespace()
		updatableContainer(ns)
		bumpStandard(ns, "v1.1.0")
		first := openedUpdate(ns, "v1.1.0")

		// A person changed the implementer's guide on main meanwhile.
		gitFake.commitToMain(ns, "orders-api", scaffold.Tree{implementer: {Data: []byte("Our own rules.\n")}})
		gitFake.conflict(ns, "orders-api", int(first.PullRequest), true)
		Eventually(func(g Gomega) {
			g.Expect(getContainer(g, ns).Status.Standard.Update).
				To(PointTo(HaveField("PullRequest", Not(Equal(first.PullRequest)))))
		}).WithTimeout(standardSteps).Should(Succeed())
		second := openedUpdate(ns, "v1.1.0")
		Expect(second.Branch).To(Equal(first.Branch))
		Expect(second.Paths).To(Equal([]string{"AGENTS.md"}))
		Expect(second.LeftOut).To(Equal([]string{implementer}))
		Expect(gitFake.pullRequests(ns, "orders-api")).To(ContainElement(MatchFields(IgnoreExtras, Fields{
			"Number": BeEquivalentTo(first.PullRequest), "State": Equal("closed"), "Merged": BeFalse()})))

		gitFake.setChecks(ns, "orders-api", second.Commit, "success")
		eventuallyAt(ns, "v1.1.0", implementer)
		Expect(mainFile(ns, implementer)).To(Equal("Our own rules.\n"))
	})
})
