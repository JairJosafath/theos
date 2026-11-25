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

package nocluster_test

import (
	"context"
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/nocluster"
)

// project is a project repository in memory, as theos plan reads and writes it.
type project struct{ fs fstest.MapFS }

func newProject(story, setup string) *project {
	return &project{fs: fstest.MapFS{
		nocluster.ModelFile: {Data: []byte(story)},
		nocluster.SetupFile: {Data: []byte(setup)},
	}}
}

func (p *project) files() nocluster.ProjectFiles {
	return nocluster.ProjectFiles{FS: p.fs,
		Write:  func(path string, data []byte) error { p.fs[path] = &fstest.MapFile{Data: data}; return nil },
		Remove: func(path string) error { delete(p.fs, path); return nil }}
}

func (p *project) read(t *testing.T, path string) string {
	t.Helper()
	data, err := fs.ReadFile(p.fs, path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func (p *project) write(path, data string) { p.fs[path] = &fstest.MapFile{Data: []byte(data)} }

// plan runs theos plan on the world's host.
func (p *project) plan(w *world, opts nocluster.PlanOptions) error {
	w.out.Reset()
	return nocluster.Plan(context.Background(), w.env, p.files(), opts)
}

// startRound runs theos plan with no round in plan/, which writes
// plan/model.yaml, and adds the choices every round writes.
func (p *project) startRound(t *testing.T, w *world) {
	t.Helper()
	if err := p.plan(w, nocluster.PlanOptions{}); err != nil {
		t.Fatal(err)
	}
	p.write(model.PlanChoicesFile, "- Kept the shop as it is.\n")
}

// addObjects appends manifests to the round's plan/model.yaml.
func (p *project) addObjects(t *testing.T, docs ...string) {
	t.Helper()
	story := p.read(t, model.PlanModelFile)
	for _, d := range docs {
		story += "---\n" + d
	}
	p.write(model.PlanModelFile, story)
}

// issue writes plan/issues/<nn>-<container>.md, after the issues named.
func (p *project) issue(n int, container, title string, after ...int) {
	front := fmt.Sprintf("---\ntitle: %s\nlabels: [kind/feature]\n", title)
	if len(after) > 0 {
		nums := make([]string, 0, len(after))
		for _, a := range after {
			nums = append(nums, fmt.Sprint(a))
		}
		front += "after: [" + strings.Join(nums, ", ") + "]\n"
	}
	p.write(fmt.Sprintf("plan/issues/%02d-%s.md", n, container), front+"---\n"+title+
		".\n\n## Acceptance criteria\n\n- [ ] "+title+" works.\n")
}

// adminContainer is a new Container a plan makes, on the storefront's
// choices; PROVIDER is the webshop's GitProvider (adminOn).
const adminContainer = `apiVersion: theos.factory.io/v1beta1
kind: Container
metadata:
  name: admin
spec:
  displayName: Admin
  description: The shop's back office.
  softwareSystemRef:
    name: webshop-core
  type: static-site
  gitProviderRef:
    name: PROVIDER
  frameworkRef:
    name: react-vite
  standardRef:
    name: ts-standard
  computeRef:
    name: small
  deploymentRef:
    name: k8s-manifests
  checks: BeforeRelease
`

// adminOn is adminContainer on the world's GitProvider.
func adminOn(w *world) string {
	return strings.ReplaceAll(adminContainer, "PROVIDER", w.provider().Name)
}

// withPlanlessFields is the webshop story with the storefront carrying
// fields a plan can't write, beside the deploy it has.
func withPlanlessFields(t *testing.T) string {
	t.Helper()
	story := readWebshop(t).story
	// checkCommand is for a Container that adopts its repository and has no
	// Standard, so the storefront drops its Standard.
	for old, is := range map[string]string{
		"  type: static-site\n":                   "  type: static-site\n  adopt: true\n  license: MIT\n  checkCommand: make verify\n",
		"  standardRef:\n    name: ts-standard\n": "",
	} {
		if strings.Count(story, old) != 1 {
			t.Fatalf("the webshop story has %q %d times", old, strings.Count(story, old))
		}
		story = strings.Replace(story, old, is, 1)
	}
	return story
}

// #293: with no Brief and no Crew, CurrentModel has the Project's name as
// its namespace, the model files' story, catalog, Bundles and GitProvider,
// a new prompt, a designer for every Container, no Container the Brief made
// and every Container released.
func TestCurrentModel293(t *testing.T) {
	m := decode(t, readWebshop(t), "")
	current, err := nocluster.CurrentModel(m)
	if err != nil {
		t.Fatal(err)
	}
	all := []string{"catalog-api", "orders-api", "payments-worker", "storefront"}
	if current.Namespace != "webshop" || current.GitProvider != m.Objects.GitProviders[0].Name || !current.NewPrompt ||
		!current.Designer || current.SpecParallel != 0 || current.BriefContainers != nil {
		t.Errorf("current model is %+v", current)
	}
	if !slices.Equal(current.CrewDesigners, all) || !slices.Equal(current.Released, all) {
		t.Errorf("designers %v and released %v, want every Container", current.CrewDesigners, current.Released)
	}
	kinds := map[string]int{}
	for _, o := range current.Objects {
		kinds[o.Kind]++
	}
	want := map[string]int{model.KindProject: 1, model.KindPerson: 1, model.KindSoftwareSystem: 2,
		model.KindContainer: 4, model.KindDataStore: 1, model.KindRelationship: 10}
	if !maps.Equal(kinds, want) {
		t.Errorf("objects by kind %v, want %v", kinds, want)
	}
	if len(current.Catalog) == 0 || len(current.Bundles) != len(m.Objects.Bundles) {
		t.Errorf("%d catalog entries and %d Bundles", len(current.Catalog), len(current.Bundles))
	}
}

// #293: a model whose Container carries adopt, license, checkCommand and
// deploy still plans: theos plan starts a round from it, and the copy it
// writes, applied as it is, is no change and no refusal.
func TestModelWithPlanlessFieldsStillPlans293(t *testing.T) {
	w := newWorld(t)
	story := withPlanlessFields(t)
	p := newProject(story, readWebshop(t).setup)
	p.startRound(t, w)
	if out := w.out.String(); !strings.Contains(out, "round 1: wrote plan/model.yaml") {
		t.Errorf("the start said:\n%s", out)
	}
	if got := p.read(t, model.PlanModelFile); !strings.Contains(got, "name: storefront") ||
		strings.Contains(got, "adopt") || strings.Contains(got, "checkCommand") {
		t.Errorf("plan/model.yaml is\n%s", got)
	}
	if err := p.plan(w, nocluster.PlanOptions{}); err != nil {
		t.Fatalf("%v:\n%s", err, w.out)
	}
	if strings.Contains(w.out.String(), "refused") || p.read(t, nocluster.ModelFile) != story {
		t.Errorf("the copy did not apply as no change:\n%s", w.out)
	}
}

// #293: a refused plan files nothing, writes nothing, and prints each of
// CheckPlan's refusals.
func TestRefusedPlanFilesNothing293(t *testing.T) {
	w := newWorld(t)
	if err := nocluster.Scaffold(context.Background(), w.env, w.m); err != nil {
		t.Fatal(err)
	}
	story := readWebshop(t).story
	p := newProject(story, readWebshop(t).setup)
	p.startRound(t, w)
	p.addObjects(t, strings.Replace(adminOn(w), "react-vite", "no-such-framework", 1))
	p.issue(1, "storefront", "Show the reorder button")
	p.issue(2, "ghost", "Haunt the shop")
	record := p.read(t, nocluster.PlanFiledFile)

	err := p.plan(w, nocluster.PlanOptions{})
	if err == nil || !strings.Contains(err.Error(), "nothing written or filed") {
		t.Fatalf("theos plan returned %v", err)
	}
	round := map[string]string{}
	for path, f := range p.fs {
		if strings.HasPrefix(path, "plan/") && path != nocluster.PlanFiledFile {
			round[path] = string(f.Data)
		}
	}
	plan, perr := model.ParsePlan(round)
	if perr != nil {
		t.Fatal(perr)
	}
	current, cerr := nocluster.CurrentModel(w.m)
	if cerr != nil {
		t.Fatal(cerr)
	}
	refusals := model.CheckPlan(plan, current)
	if len(refusals) < 2 {
		t.Fatalf("CheckPlan refuses %v", refusals)
	}
	for _, r := range refusals {
		if !strings.Contains(w.out.String(), "refused: "+r.String()+"\n") {
			t.Errorf("theos plan does not print %q:\n%s", r, w.out)
		}
	}
	owner := w.provider().Spec.Owner
	for _, c := range []string{"storefront", "catalog-api", "orders-api", "payments-worker"} {
		if n := w.git.IssueCount(owner, c); n != 0 {
			t.Errorf("%s has %d issues", c, n)
		}
	}
	if p.read(t, nocluster.ModelFile) != story || p.read(t, nocluster.PlanFiledFile) != record {
		t.Error("a refused plan wrote a file")
	}

	// A round that asks the person for input files nothing either.
	p.write(model.PlanNeedsInputFile, "Which payment provider does the shop use?\n")
	if err := p.plan(w, nocluster.PlanOptions{}); err == nil ||
		!strings.Contains(err.Error(), "Which payment provider") {
		t.Errorf("theos plan with a question returned %v", err)
	}
}

// #293: an applied plan writes model/model.yaml object by object: what it
// leaves alone stays as written, a changed object gets its new story text
// and keeps adopt, license, checkCommand, deploy and its comments, and a
// created one is a new document the model files read.
func TestAppliedPlanKeepsWhatAPlanCantWrite293(t *testing.T) {
	w := newWorld(t)
	story := withPlanlessFields(t)
	p := newProject(story, readWebshop(t).setup)
	p.startRound(t, w)
	plan := p.read(t, model.PlanModelFile)
	old := "  description: The shop in the browser, to browse, order, pay and reorder.\n"
	if !strings.Contains(plan, old) {
		t.Fatalf("plan/model.yaml has no storefront description:\n%s", plan)
	}
	p.write(model.PlanModelFile, strings.Replace(plan, old, "  description: The shop in the browser.\n", 1))
	p.addObjects(t, adminOn(w))
	p.write(model.PlanReleasesFile, "containers: [admin]\n")

	if err := p.plan(w, nocluster.PlanOptions{}); err != nil {
		t.Fatalf("%v:\n%s", err, w.out)
	}
	out := w.out.String()
	for _, want := range []string{"model/model.yaml: written; created Container/admin; changed the story text " +
		"of Container/storefront", "plan/releases.yaml: admin ship releases. Nothing acts on it",
		"the round files no issues"} {
		if !strings.Contains(out, want) {
			t.Errorf("theos plan does not say %q:\n%s", want, out)
		}
	}
	written := p.read(t, nocluster.ModelFile)
	for _, kept := range []string{"  adopt: true\n", "  license: MIT\n", "  checkCommand: make verify\n",
		"  deploy:\n    - target: kind-cluster\n", "  # Each release ships to the Argo CD in the cluster",
		"  description: The shop in the browser.\n"} {
		if !strings.Contains(written, kept) {
			t.Errorf("model/model.yaml lost %q:\n%s", kept, written)
		}
	}
	// A created object is the fields it sets, in the API type's order.
	if want := `---
apiVersion: theos.factory.io/v1beta1
kind: Container
metadata:
  name: admin
spec:
  softwareSystemRef:
    name: webshop-core
  displayName: Admin
  description: The shop's back office.
  type: static-site
  gitProviderRef:
    name: PROVIDER
  frameworkRef:
    name: react-vite
  standardRef:
    name: ts-standard
  computeRef:
    name: small
  deploymentRef:
    name: k8s-manifests
  checks: BeforeRelease
`; !strings.HasSuffix(written, strings.ReplaceAll(want, "PROVIDER", w.provider().Name)) {
		t.Errorf("the new Container's document is not\n%s\nmodel/model.yaml:\n%s", want, written)
	}
	// Every document the plan left alone is as it was written.
	start := strings.Index(story, "---\napiVersion: theos.factory.io/v1beta1\nkind: Container\nmetadata:\n  name: catalog-api")
	if start < 0 || !strings.Contains(written, story[start:]) {
		t.Error("the documents after the storefront's changed")
	}
	m, err := nocluster.Read(p.fs)
	if err != nil {
		t.Fatal(err)
	}
	admin := m.Containers[len(m.Containers)-1]
	if admin.Name != "admin" || string(admin.Spec.Checks) != model.ChecksBeforeRelease ||
		admin.Spec.FrameworkRef.Name != "react-vite" || admin.Spec.Adopt {
		t.Errorf("the new Container reads back as %+v", admin.Spec)
	}
	front := m.Containers[0]
	if front.Name != "storefront" || !front.Spec.Adopt || front.Spec.License != "MIT" ||
		front.Spec.CheckCommand != "make verify" || len(front.Spec.Deploy) != 1 {
		t.Errorf("the storefront reads back as %+v", front.Spec)
	}

	// Applied again, the plan is no change.
	before := p.read(t, nocluster.ModelFile)
	if err := p.plan(w, nocluster.PlanOptions{}); err != nil {
		t.Fatal(err)
	}
	if p.read(t, nocluster.ModelFile) != before || !strings.Contains(w.out.String(), "unchanged") {
		t.Errorf("a second run changed model/model.yaml:\n%s", w.out)
	}
}

// #293: an applied plan files its issues in FilingOrder, blockers first and
// each one after what it builds on, into the repositories that have their
// first commit; an issue of a Container not yet scaffolded waits. Filing
// twice files once, even when the record lost an issue filed before.
func TestPlanFilesInFilingOrder293(t *testing.T) {
	w := newWorld(t)
	if err := nocluster.Scaffold(context.Background(), w.env, w.m); err != nil {
		t.Fatal(err)
	}
	p := newProject(readWebshop(t).story, readWebshop(t).setup)
	p.startRound(t, w)
	p.addObjects(t, adminOn(w))
	p.issue(1, "storefront", "Show the reorder button", 2)
	p.issue(2, "catalog-api", "List what a customer bought")
	p.issue(3, "storefront", "Show past orders")
	p.issue(4, "admin", "Show the orders of the day", 3)

	if err := p.plan(w, nocluster.PlanOptions{}); err != nil {
		t.Fatalf("%v:\n%s", err, w.out)
	}
	out := w.out.String()
	at := make([]int, 0, 4)
	for _, line := range []string{"issue 2: filed as ", "issue 1: filed as ", "issue 3: filed as ",
		"issue 4 (admin): waits: admin has no repository"} {
		i := strings.Index(out, line)
		if i < 0 {
			t.Fatalf("theos plan does not say %q:\n%s", line, out)
		}
		at = append(at, i)
	}
	if !slices.IsSorted(at) {
		t.Errorf("the issues are not filed in FilingOrder:\n%s", out)
	}
	owner := w.provider().Spec.Owner
	catalog, front := w.git.AllIssues(owner, "catalog-api"), w.git.AllIssues(owner, "storefront")
	if len(catalog) != 1 || len(front) != 2 {
		t.Fatalf("catalog-api has %d issues and storefront %d", len(catalog), len(front))
	}
	reorder := w.git.Issue(owner, "storefront", 1)
	if reorder.Title != "Show the reorder button" ||
		!strings.Contains(reorder.Body, fmt.Sprintf("Builds on %s/catalog-api#1 (List what a customer bought).",
			owner)) || !slices.Equal(reorder.Blockers, []string{owner + "/catalog-api#1"}) ||
		!slices.Contains(reorder.Labels, model.LabelReady) || slices.Contains(reorder.FiledLabels, model.LabelReady) {
		t.Errorf("issue 1 is filed as %+v", reorder)
	}
	if !model.HasIssueMarker(reorder.Body, "webshop", 1, 1) ||
		!model.HasIssueMarker(w.git.Issue(owner, "storefront", 2).Body, "webshop", 1, 3) {
		t.Error("the issues carry no round marker")
	}
	if got := w.git.Issue(owner, "catalog-api", 1); !slices.Equal(got.FiledLabels,
		[]string{model.LabelReady, "kind/feature"}) {
		t.Errorf("issue 2 is filed with labels %v", got.FiledLabels)
	}

	// Filing twice files once.
	if err := p.plan(w, nocluster.PlanOptions{}); err != nil {
		t.Fatal(err)
	}
	if n := len(w.git.AllIssues(owner, "storefront")) + len(w.git.AllIssues(owner, "catalog-api")); n != 3 {
		t.Errorf("a second run filed %d issues more", n-3)
	}
	// A run that filed issue 3 and stopped before it recorded it finds it by
	// its marker.
	record := p.read(t, nocluster.PlanFiledFile)
	kept, _, ok := strings.Cut(record, "- nn: 3\n")
	if !ok {
		t.Fatalf("plan/filed.yaml is\n%s", record)
	}
	p.write(nocluster.PlanFiledFile, kept)
	if err := p.plan(w, nocluster.PlanOptions{}); err != nil {
		t.Fatal(err)
	}
	if n := len(w.git.AllIssues(owner, "storefront")); n != 2 || !strings.Contains(w.out.String(),
		fmt.Sprintf("issue 3: filed as %s/storefront#2", owner)) {
		t.Errorf("storefront has %d issues:\n%s", n, w.out)
	}

	// Once admin is scaffolded, the next run files its issue, after issue 3.
	m, err := nocluster.Read(p.fs)
	if err != nil {
		t.Fatal(err)
	}
	if err := nocluster.Scaffold(context.Background(), w.env, m); err != nil {
		t.Fatal(err)
	}
	if err := p.plan(w, nocluster.PlanOptions{}); err != nil {
		t.Fatal(err)
	}
	admin := w.git.Issue(owner, "admin", 1)
	if admin.Title != "Show the orders of the day" || !slices.Equal(admin.Blockers, []string{owner + "/storefront#2"}) {
		t.Errorf("admin's issue is %+v:\n%s", admin, w.out)
	}
	if err := p.plan(w, nocluster.PlanOptions{}); err != nil || !strings.Contains(w.out.String(),
		"round 1 is done") {
		t.Errorf("the run after the last issue said %v:\n%s", err, w.out)
	}
}

// #293: theos plan -new leaves a round only once every issue is filed, then
// starts the next, whose issues carry its number.
func TestPlanNew293(t *testing.T) {
	w := newWorld(t)
	if err := nocluster.Scaffold(context.Background(), w.env, w.m); err != nil {
		t.Fatal(err)
	}
	p := newProject(readWebshop(t).story, readWebshop(t).setup)
	if err := p.plan(w, nocluster.PlanOptions{New: true}); err == nil {
		t.Error("-new with no round in plan/ went ahead")
	}
	// A round's files left without their model start no round.
	p.issue(1, "storefront", "Left from an earlier round")
	if err := p.plan(w, nocluster.PlanOptions{}); err == nil ||
		!strings.Contains(err.Error(), "plan/issues/01-storefront.md but no plan/model.yaml") {
		t.Errorf("a start beside an old issue returned %v", err)
	}
	delete(p.fs, "plan/issues/01-storefront.md")
	p.startRound(t, w)
	p.issue(1, "storefront", "Show the reorder button")
	if err := p.plan(w, nocluster.PlanOptions{New: true}); err == nil ||
		!strings.Contains(err.Error(), "issue 1 (plan/issues/01-storefront.md) is not filed") {
		t.Errorf("-new on an unfiled round returned %v", err)
	}
	if err := p.plan(w, nocluster.PlanOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := p.plan(w, nocluster.PlanOptions{New: true}); err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{"plan/issues/01-storefront.md", model.PlanChoicesFile} {
		if _, ok := p.fs[gone]; ok {
			t.Errorf("-new left %s", gone)
		}
	}
	if record := p.read(t, nocluster.PlanFiledFile); !strings.Contains(record, "round: 2\nissues: []\n") {
		t.Errorf("plan/filed.yaml is\n%s", record)
	}
	p.write(model.PlanChoicesFile, "- Reorder from the order page too.\n")
	p.issue(1, "storefront", "Reorder from the order page")
	if err := p.plan(w, nocluster.PlanOptions{}); err != nil {
		t.Fatal(err)
	}
	owner := w.provider().Spec.Owner
	if got := w.git.Issue(owner, "storefront", 2); got.Title != "Reorder from the order page" ||
		!model.HasIssueMarker(got.Body, "webshop", 2, 1) {
		t.Errorf("round 2's issue is %+v", got)
	}
}

// #293: a plan that makes an object of every kind it writes is merged into
// model/model.yaml as documents the model files read back as the plan.
func TestPlanCreatesEveryKind293(t *testing.T) {
	w := newWorld(t)
	p := newProject(readWebshop(t).story, readWebshop(t).setup)
	p.startRound(t, w)
	p.addObjects(t, `apiVersion: theos.factory.io/v1beta1
kind: Person
metadata:
  name: clerk
spec:
  displayName: Clerk
`, `apiVersion: theos.factory.io/v1beta1
kind: SoftwareSystem
metadata:
  name: back-office
spec:
  displayName: Back office
  description: What the shop's staff use.
  needs: [See the day's orders]
`, `apiVersion: theos.factory.io/v1beta1
kind: SoftwareSystem
metadata:
  name: mail
spec:
  description: Sends the shop's mail.
  location: External
`, strings.Replace(adminOn(w), "webshop-core", "back-office", 1), `apiVersion: theos.factory.io/v1beta1
kind: DataStore
metadata:
  name: office-db
spec:
  softwareSystemRef:
    name: back-office
  connectorRef:
    name: postgres
`, `apiVersion: theos.factory.io/v1beta1
kind: Relationship
metadata:
  name: clerk-uses-admin
spec:
  source:
    kind: Person
    name: clerk
  target:
    kind: Container
    name: admin
  description: Looks up orders
`)
	if err := p.plan(w, nocluster.PlanOptions{}); err != nil {
		t.Fatalf("%v:\n%s", err, w.out)
	}
	m, err := nocluster.Read(p.fs)
	if err != nil {
		t.Fatal(err)
	}
	current, err := nocluster.CurrentModel(m)
	if err != nil {
		t.Fatal(err)
	}
	round := map[string]string{}
	for path, f := range p.fs {
		round[path] = string(f.Data)
	}
	plan, err := model.ParsePlan(round)
	if err != nil {
		t.Fatal(err)
	}
	if created, changed := model.PlanDiff(plan, current); len(created)+len(changed) > 0 {
		t.Errorf("model/model.yaml lacks %v and %v", created, changed)
	}
	if mail := m.Systems[len(m.Systems)-1]; mail.Name != "mail" || mail.Spec.Location != "External" {
		t.Errorf("the External system reads back as %+v", mail)
	}
}

// #293: a plan's new Containers go on the setup file's one GitProvider, or
// on the one every Container names; several the Containers split between
// are refused.
func TestPlanGitProvider293(t *testing.T) {
	m := decode(t, readWebshop(t), "")
	first := m.Objects.GitProviders[0].Name
	second := m.Objects.GitProviders[0].DeepCopy()
	second.Name = "forge"
	m.Objects.GitProviders = append(m.Objects.GitProviders, *second)
	if current, err := nocluster.CurrentModel(m); err != nil || current.GitProvider != first {
		t.Errorf("with every Container on %s, the GitProvider is %q, %v", first, current.GitProvider, err)
	}
	m.Containers[0].Spec.GitProviderRef.Name = "forge"
	if _, err := nocluster.CurrentModel(m); err == nil ||
		!strings.Contains(err.Error(), "GitProviders "+first+", forge") {
		t.Errorf("with the Containers split, CurrentModel returned %v", err)
	}
}
