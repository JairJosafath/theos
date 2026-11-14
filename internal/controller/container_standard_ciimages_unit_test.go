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
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/api/meta"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/modelfacts"
	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/scaffold"
)

// The CI images a repository's Standard files were rendered with, and the
// operator's after two releases (#284).
var (
	imagesA = model.CIImages{Go: "registry.example.com/go:0.32.0", Base: "registry.example.com/base:0.32.0"}
	imagesB = model.CIImages{Go: "registry.example.com/go:0.33.0", Base: "registry.example.com/base:0.33.0"}
	imagesC = model.CIImages{Go: "registry.example.com/go:0.34.0", Base: "registry.example.com/base:0.34.0"}
)

const ciCheckFile = ".github/workflows/check.yaml"

// releaseCIImages are the CI images the envtest suite's operator, given none,
// renders the Standard's files with and records: the release's (#284).
func releaseCIImages() *c4v1beta1.CIImages {
	return ciImagesRecord(model.DefaultCIImages())
}

// ciTemplates are a Standard's template by ref.
type ciTemplates map[string]scaffold.Tree

func (t ciTemplates) Provider(ports.Config) (ports.Provider, error) {
	return nil, errors.New("no provider in this test")
}

func (t ciTemplates) FetchTemplate(_ context.Context, _, ref, _ string, _ *ports.Credentials) (scaffold.Tree, error) {
	files, ok := t[ref]
	if !ok {
		return nil, ports.ErrUnpinnedRef
	}
	return files, nil
}

// imagesHost is a Git host with one repository's main in memory, and the branch
// a Standard update pushes.
type imagesHost struct {
	ports.Provider
	main    scaffold.Tree
	pushed  scaffold.Tree
	removes []string
}

func (h *imagesHost) Repository(name string) ports.Repository {
	return ports.Repository{Owner: "apps", Name: name}
}

func (h *imagesHost) ReadFiles(_ context.Context, _ ports.Repository, _ string, paths []string) (scaffold.Tree, string,
	error) {
	files := scaffold.Tree{}
	for _, p := range paths {
		if f, ok := h.main[p]; ok {
			files[p] = f
		}
	}
	return files, "main-head", nil
}

func (h *imagesHost) PushBranch(_ context.Context, _ ports.Repository, _, _ string, files scaffold.Tree, removes []string,
	_ string, _ ports.Signature) (string, error) {
	h.pushed, h.removes = files, removes
	return "update-commit", nil
}

func (h *imagesHost) OpenPullRequest(context.Context, ports.Repository, string, string, string, string) (ports.PullRequest,
	error) {
	return ports.PullRequest{Number: 7, URL: "http://git/apps/orders-api/pulls/7"}, nil
}

// ciStandardTemplates is templates/go-standard at R1, and at the refs after
// it, each with one edit of R1's check workflow (old, new).
func ciStandardTemplates(t *testing.T, edits map[string][2]string) ciTemplates {
	t.Helper()
	r1, err := scaffold.ReadTree(os.DirFS(filepath.Join("..", "..", "templates", "go-standard")))
	if err != nil {
		t.Fatal(err)
	}
	const check = ".theos/ci/githubActions/.github/workflows/check.yaml.tmpl"
	templates := ciTemplates{"R1": r1}
	for ref, edit := range edits {
		data := string(r1[check].Data)
		if strings.Count(data, edit[0]) != 1 {
			t.Fatalf("%s's check template doesn't hold %q once", ref, edit[0])
		}
		files := maps.Clone(r1)
		files[check] = scaffold.File{Data: []byte(strings.Replace(data, edit[0], edit[1], 1))}
		templates[ref] = files
	}
	return templates
}

// ciUpdate is a Standard update of a go-standard Container on GitHub, its
// repository's main on host, run by an operator given images.
type ciUpdate struct {
	t         *testing.T
	container *c4v1beta1.Container
	status    *c4v1beta1.ContainerStatus
	standard  *c4v1beta1.Standard
	templates ciTemplates
	host      *imagesHost
	github    model.CI
}

func newCIUpdate(t *testing.T, templates ciTemplates, recorded *c4v1beta1.CIImages) *ciUpdate {
	t.Helper()
	github, err := model.CIByName(model.CIGitHubActions)
	if err != nil {
		t.Fatal(err)
	}
	return &ciUpdate{
		t: t,
		container: &c4v1beta1.Container{
			ObjectMeta: objectMeta("webshop", "orders-api"),
			Spec: c4v1beta1.ContainerSpec{
				SoftwareSystemRef: c4v1beta1.LocalRef{Name: "webshop-core"},
				Type:              c4v1beta1.ContainerTypeService,
				FrameworkRef:      c4v1beta1.LocalRef{Name: "go-cli"},
				StandardRef:       &c4v1beta1.LocalRef{Name: "go-standard"},
				GitProviderRef:    c4v1beta1.LocalRef{Name: "github"},
			},
		},
		status: &c4v1beta1.ContainerStatus{Standard: &c4v1beta1.ContainerStandardStatus{Name: "go-standard", Ref: "R1",
			Checks: c4v1beta1.ChecksEveryChange, CIImages: recorded}},
		standard: &c4v1beta1.Standard{ObjectMeta: objectMeta("webshop", "go-standard"),
			Spec: c4v1beta1.StandardSpec{CatalogEntrySpec: c4v1beta1.CatalogEntrySpec{
				Template: c4v1beta1.TemplateSource{URL: "http://templates/go-standard", Ref: "R1"}}}},
		templates: templates,
		host:      &imagesHost{},
		github:    github,
	}
}

// scaffolded is main as the scaffold left it: the Standard at R1, its checks
// EveryChange, with images, as an update renders it for this Container.
func (c *ciUpdate) scaffolded(images model.CIImages) scaffold.Tree {
	c.t.Helper()
	in := modelfacts.ScaffoldInput(c.container, c.github, nil, nil, model.ChecksEveryChange, images,
		scaffoldedOperations(c.container, c.status), "", scaffold.License{})
	tree, err := scaffold.RenderLayer(in, scaffold.Layer{Side: model.SideStandard, Name: "go-standard", Ref: "R1",
		Files: c.templates["R1"]})
	if err != nil {
		c.t.Fatal(err)
	}
	return tree
}

// pass is one pass of the Standard update by an operator given images.
func (c *ciUpdate) pass(images model.CIImages) *standardUpdate {
	c.host.pushed, c.host.removes = nil, nil
	return &standardUpdate{
		r:         &ContainerReconciler{Git: c.templates, CIImages: images},
		container: c.container,
		status:    c.status,
		git: &gitAccess{gitProvider: &c4v1beta1.GitProvider{Spec: c4v1beta1.GitProviderSpec{URL: "http://git"}},
			provider: c.host, ci: c.github},
		licenseRead: true, frameworkRead: true,
	}
}

// images are the images the check names, once per job that runs in one.
func checkImages(check []byte) []string {
	var images []string
	for line := range strings.SplitSeq(string(check), "\n") {
		if _, rest, ok := strings.Cut(line, "container: ${{ vars.THEOS_CI_IMAGE_GO || '"); ok {
			image, _, _ := strings.Cut(rest, "'")
			images = append(images, image)
		}
	}
	return images
}

// TestStandardUpdateRendersWithTheRecordedCIImages284 covers #284: a Standard
// update renders the files main holds with the CI images status records they
// were rendered with, and the files it brings with the operator's own. A
// repository scaffolded at one release and updated by the next gets its
// check's image moved by the next change of the Standard's ref or checks as
// an ordinary Standard change, not reported as the repository's own, not
// left out and never with two images; a release alone changes nothing.
func TestStandardUpdateRendersWithTheRecordedCIImages284(t *testing.T) {
	ctx := context.Background()
	// R2 changes the line next to the image's, as a template edit of the job does.
	templates := ciStandardTemplates(t, map[string][2]string{"R2": {"timeout-minutes: 60", "timeout-minutes: 45"}})

	t.Run("#284 a release alone plans no update", func(t *testing.T) {
		c := newCIUpdate(t, templates, ciImagesRecord(imagesA))
		c.host.main = c.scaffolded(imagesA)
		u := c.pass(imagesB)
		u.run(ctx, ctx, c.standard)
		if c.host.pushed != nil || c.status.Standard.Update != nil {
			t.Fatalf("an operator release planned an update: pushed %v, update %+v", c.host.pushed.Paths(),
				c.status.Standard.Update)
		}
		if got := recordedCIImages(c.status.Standard.CIImages); got != imagesA {
			t.Errorf("the record is %+v, want %+v: the repository's files didn't change", got, imagesA)
		}
		cond := meta.FindStatusCondition(c.status.Conditions, c4v1beta1.ConditionStandardUpdated)
		if cond == nil || cond.Reason != c4v1beta1.ReasonUpToDate {
			t.Errorf("StandardUpdated = %+v, want %s", cond, c4v1beta1.ReasonUpToDate)
		}
	})

	for _, tc := range []struct {
		name     string
		recorded *c4v1beta1.CIImages
		main     model.CIImages
	}{
		{"#284 a ref bump moves the image as a Standard change", ciImagesRecord(imagesA), imagesA},
		{"#284 an empty record reads as the legacy images", nil, model.LegacyCIImages()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newCIUpdate(t, templates, tc.recorded)
			c.host.main = c.scaffolded(tc.main)
			c.standard.Spec.Template.Ref = "R2"
			u := c.pass(imagesB)
			u.plan(ctx, ctx, c.standard)
			update := c.status.Standard.Update
			if update == nil || update.Stopped != "" {
				t.Fatalf("no update opened: %+v, %+v", update, c.status.Conditions)
			}
			if !slices.Equal(update.Paths, []string{ciCheckFile}) || len(update.Merged) > 0 || len(update.LeftOut) > 0 {
				t.Errorf("update writes %q, merged %q, left out %q; want %s written as the Standard's change alone",
					update.Paths, update.Merged, update.LeftOut, ciCheckFile)
			}
			check := c.host.pushed[ciCheckFile].Data
			if got := checkImages(check); !slices.Equal(got, []string{imagesB.Go}) {
				t.Errorf("the update's check runs in %q, want only %s", got, imagesB.Go)
			}
			if !strings.Contains(string(check), "timeout-minutes: 45") {
				t.Errorf("the update's check lacks the Standard's change:\n%s", check)
			}
			if got := recordedCIImages(update.CIImages); got != imagesB {
				t.Errorf("the update renders with %+v, want %+v", got, imagesB)
			}
			if got := recordedCIImages(c.status.Standard.CIImages); got != tc.main {
				t.Errorf("before the merge the record is %+v, want %+v", got, tc.main)
			}
			u.merged(ctx)
			if s := c.status.Standard; s.Ref != "R2" || recordedCIImages(s.CIImages) != imagesB {
				t.Errorf("after the merge the Standard is at %s with %+v, want R2 with %+v", s.Ref,
					recordedCIImages(s.CIImages), imagesB)
			}
		})
	}

	t.Run("#284 a change of checks writes the check with one image", func(t *testing.T) {
		c := newCIUpdate(t, templates, ciImagesRecord(imagesA))
		c.host.main = c.scaffolded(imagesA)
		c.container.Spec.Checks = c4v1beta1.ChecksBeforeRelease
		c.standard.Spec.ChecksBeforeRelease = true
		u := c.pass(imagesB)
		u.plan(ctx, ctx, c.standard)
		update := c.status.Standard.Update
		if update == nil || update.Stopped != "" {
			t.Fatalf("no update opened: %+v, %+v", update, c.status.Conditions)
		}
		if update.Checks != c4v1beta1.ChecksBeforeRelease || len(update.Merged) > 0 || len(update.LeftOut) > 0 {
			t.Errorf("update to checks %s merged %q, left out %q; want BeforeRelease and neither", update.Checks,
				update.Merged, update.LeftOut)
		}
		if got := checkImages(c.host.pushed[ciCheckFile].Data); !slices.Equal(got, []string{imagesB.Go, imagesB.Go}) {
			t.Errorf("the check's two jobs run in %q, want both in %s", got, imagesB.Go)
		}
	})
}

// TestStandardFileBehindKeepsItsCIImages284 covers #284 for a file an update
// left out: it is merged later against its ref rendered with the images its
// copy has, which status.standard.behind records with it, not the images the
// Standard's other files moved to since. Left out at R1 with images A, kept
// behind through the update to R2 under images B, it merges cleanly into the
// update to R3 under images C, with C alone.
func TestStandardFileBehindKeepsItsCIImages284(t *testing.T) {
	ctx := context.Background()
	const comment = "# A newer push to a pull request or a branch ends the run of the older one."
	// R2 changes a line the repository changed too; R3 takes the change back.
	templates := ciStandardTemplates(t, map[string][2]string{
		"R2": {comment, "# The Standard's comment at R2."},
		"R3": {comment, comment},
	})
	c := newCIUpdate(t, templates, ciImagesRecord(imagesA))
	c.host.main = c.scaffolded(imagesA)
	own := strings.Replace(string(c.host.main[ciCheckFile].Data), comment, "# The repository's comment.", 1)
	c.host.main[ciCheckFile] = scaffold.File{Data: []byte(own)}

	c.standard.Spec.Template.Ref = "R2"
	c.pass(imagesB).plan(ctx, ctx, c.standard)
	s := c.status.Standard
	if s.Ref != "R2" || s.Update != nil || recordedCIImages(s.CIImages) != imagesB {
		t.Fatalf("after the update to R2 the Standard is at %s with %+v, update %+v; want R2 with %+v and none",
			s.Ref, recordedCIImages(s.CIImages), s.Update, imagesB)
	}
	if len(s.Behind) != 1 || s.Behind[0].Path != ciCheckFile || s.Behind[0].Ref != "R1" ||
		recordedCIImages(s.Behind[0].CIImages) != imagesA {
		t.Fatalf("behind = %+v, want %s at R1 with %+v", s.Behind, ciCheckFile, imagesA)
	}

	// A person who takes the Standard's version at R2 gets it with the images
	// the Standard's files are at, merged from the file's own.
	taking := *c
	taking.status = c.status.DeepCopy()
	taking.pass(imagesC).take(ctx, ctx, c.standard)
	if update := taking.status.Standard.Update; update == nil || !update.Take ||
		recordedCIImages(update.CIImages) != imagesB {
		t.Fatalf("take = %+v, want a take rendered with %+v; %+v", update, imagesB, taking.status.Conditions)
	}
	if got := checkImages(c.host.pushed[ciCheckFile].Data); !slices.Equal(got, []string{imagesB.Go}) {
		t.Errorf("the take's check runs in %q, want only %s", got, imagesB.Go)
	}

	c.standard.Spec.Template.Ref = "R3"
	c.pass(imagesC).plan(ctx, ctx, c.standard)
	update := s.Update
	if update == nil || update.Stopped != "" {
		t.Fatalf("no update to R3 opened: %+v, %+v", update, c.status.Conditions)
	}
	if !slices.Equal(update.Merged, []string{ciCheckFile}) || len(update.LeftOut) > 0 {
		t.Errorf("update merged %q, left out %q; want %s merged with the repository's change", update.Merged,
			update.LeftOut, ciCheckFile)
	}
	check := c.host.pushed[ciCheckFile].Data
	if got := checkImages(check); !slices.Equal(got, []string{imagesC.Go}) {
		t.Errorf("the update's check runs in %q, want only %s", got, imagesC.Go)
	}
	if !strings.Contains(string(check), "# The repository's comment.") {
		t.Errorf("the update dropped the repository's change:\n%s", check)
	}
}

// TestStandardAdoptionComparesWithItsCIImages284 covers #284 for an adoption:
// once its pull request is open, main is compared with what the pull request
// rendered, CI images included, so a later operator release finds main
// unchanged; a new adoption renders with the operator's images.
func TestStandardAdoptionComparesWithItsCIImages284(t *testing.T) {
	ctx := context.Background()
	templates := ciStandardTemplates(t, nil)
	for _, tc := range []struct {
		name     string
		adoption *c4v1beta1.StandardAdoptionStatus
		want     []string
	}{
		{"#284 an adoption in flight renders with its own images",
			&c4v1beta1.StandardAdoptionStatus{Name: "go-standard", Ref: "R1", CIImages: ciImagesRecord(imagesA)}, nil},
		{"#284 a new adoption renders with the operator's", nil, []string{ciCheckFile}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newCIUpdate(t, templates, nil)
			c.container.Spec.Adopt = true
			c.status.Standard, c.status.StandardAdoption = nil, tc.adoption
			c.host.main = c.scaffolded(imagesA)
			gp := &c4v1beta1.GitProvider{ObjectMeta: objectMeta("webshop", "github"),
				Spec: c4v1beta1.GitProviderSpec{Provider: c4v1beta1.GitProviderKind("github"), URL: "http://git"}}
			u := c.pass(imagesB)
			u.r.Client = fake.NewClientBuilder().WithScheme(briefScheme(t)).WithObjects(gp, c.standard).Build()
			compared, diff, ok := u.compareAdoption(ctx, ctx, c.standard,
				model.StandardVersion{Ref: "R1", Checks: model.ChecksEveryChange})
			if !ok {
				t.Fatalf("compareAdoption() failed: %+v", c.status.Conditions)
			}
			if !slices.Equal(diff.Paths, tc.want) {
				t.Errorf("main differs in %q, want %q", diff.Paths, tc.want)
			}
			want := imagesB
			if tc.adoption != nil {
				want = imagesA
			}
			if compared.in.CIImages != want {
				t.Errorf("compared with %+v, want %+v", compared.in.CIImages, want)
			}
		})
	}
}
