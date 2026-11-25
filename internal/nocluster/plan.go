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

package nocluster

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/modelfacts"
	"github.com/JairJosafath/theos/internal/ports"
)

// theos plan turns an idea into a model and filed issues with no Brief and
// no Crew (docs/plans/no-cluster.md, step 5; #293). A round's files are
// under plan/, in the Brief's plan format: the session that plays the
// designer writes them, and theos plan checks them with the Brief's own
// rules (model.ParsePlan, model.CheckPlan) before it writes anything.

// PlanFiledFile is theos plan's record of a round: its number and the
// issues it filed, so filing twice files once. theos plan writes it; the
// round's other files are the designer's.
const PlanFiledFile = "plan/filed.yaml"

// planRoundFiles are the files of a round that theos plan -new removes,
// besides plan/issues/: those model.ParsePlan reads with no cluster.
var planRoundFiles = []string{model.PlanModelFile, model.PlanChoicesFile, model.PlanSummaryFile,
	model.PlanNeedsInputFile, model.PlanReleasesFile, model.PlanParallelFile}

// planModelHeader is plan/model.yaml's first line when theos plan writes it.
const planModelHeader = "# The project's story now, from model/model.yaml. Change it here; theos plan merges it back.\n"

// ProjectFiles is the project repository as a command reads and writes it:
// paths are slash-separated, from its root.
type ProjectFiles struct {
	FS     fs.FS
	Write  func(path string, data []byte) error
	Remove func(path string) error
}

// PlanOptions are theos plan's flags.
type PlanOptions struct {
	// New starts the next round once the one in plan/ is applied and filed.
	New bool
}

// Plan works the round in the project repository's plan/. With no
// plan/model.yaml it starts a round: it writes the story of model/model.yaml
// there, as a Brief's planner gets it. With one, it reads the round, checks
// it as a Brief's round is checked, against CurrentModel, then merges the
// plan's objects into model/model.yaml and files its issues in FilingOrder
// into each Container's repository that has its first commit. A refused
// plan writes and files nothing and prints each refusal. A second run files
// nothing twice. With opts.New it starts the next round, once every issue
// of this one is filed.
func Plan(ctx context.Context, env Env, files ProjectFiles, opts PlanOptions) error {
	m, err := Read(files.FS)
	if err != nil {
		return err
	}
	record, err := readPlanRecord(files.FS)
	if err != nil {
		return err
	}
	_, err = fs.Stat(files.FS, model.PlanModelFile)
	started := err == nil
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	switch {
	case opts.New && !started:
		return fmt.Errorf("%s holds no round; theos plan starts one", model.PlanDir)
	case opts.New:
		if err := checkRoundDone(m, files, record); err != nil {
			return err
		}
		if err := removeRound(files); err != nil {
			return err
		}
		return startRound(env.Out, m, files, record.Round+1)
	case !started:
		left, err := roundFiles(files.FS)
		if err != nil {
			return err
		}
		if len(left) > 0 {
			return fmt.Errorf("%s holds %s but no %s: a round's files go with its model, so remove them to "+
				"start a round", model.PlanDir, strings.Join(left, ", "), model.PlanModelFile)
		}
		return startRound(env.Out, m, files, record.Round+1)
	}
	return applyRound(ctx, env, m, files, record)
}

// CurrentModel is what theos plan checks a plan against, with no Brief and
// no Crew: the Project's name as the namespace; the story objects, catalog
// and Bundles of the two model files; the one GitProvider new Containers
// go on (planGitProvider); a new prompt, since the person's idea is always
// new; a designer, and every Container among CrewDesigners, since a session
// can always play the designer; no BriefContainers, so an existing
// Container's checks stay the person's edit; and every Container among
// Released, since the tool knows of no release, so nothing checks that a
// Container another one uses has a tag.
func CurrentModel(m *Model) (model.CurrentModel, error) {
	story := modelfacts.Story{People: m.People, Systems: m.Systems, Containers: m.Containers,
		DataStores: m.Objects.DataStores, Relationships: m.Objects.Relationships}
	if m.Objects.Project != nil {
		story.Projects = []c4v1beta1.Project{*m.Objects.Project}
	}
	gp, err := planGitProvider(m)
	if err != nil {
		return model.CurrentModel{}, err
	}
	names := make([]string, 0, len(m.Containers))
	for _, c := range m.Containers {
		names = append(names, c.Name)
	}
	slices.Sort(names)
	return model.CurrentModel{Namespace: m.Project, Objects: modelfacts.StoryObjects(story),
		Catalog: modelfacts.Catalog(m.Objects), Bundles: modelfacts.Bundles(m.Objects.Bundles), GitProvider: gp,
		NewPrompt: true, Designer: true, CrewDesigners: names, Released: slices.Clone(names)}, nil
}

// planGitProvider is the GitProvider a plan's new Containers name: the
// setup file's one, or, when it holds several, the one every Container
// names. Several that the Containers split between are refused.
func planGitProvider(m *Model) (string, error) {
	gps := m.Objects.GitProviders
	switch len(gps) {
	case 0:
		return "", nil
	case 1:
		return gps[0].Name, nil
	}
	var used []string
	for _, c := range m.Containers {
		if name := c.Spec.GitProviderRef.Name; !slices.Contains(used, name) {
			used = append(used, name)
		}
	}
	if len(used) == 1 {
		return used[0], nil
	}
	names := make([]string, 0, len(gps))
	for _, gp := range gps {
		names = append(names, gp.Name)
	}
	return "", fmt.Errorf("%s holds GitProviders %s, and the Containers don't all name one: theos plan puts a "+
		"plan's new Containers on one, so keep one GitProvider", SetupFile, strings.Join(names, ", "))
}

// startRound writes plan/model.yaml from the model and the round's record.
func startRound(out io.Writer, m *Model, files ProjectFiles, round int) error {
	current, err := CurrentModel(m)
	if err != nil {
		return err
	}
	story, err := model.RenderWorkspaceModel(current.Objects)
	if err != nil {
		return err
	}
	story = planModelHeader + strings.TrimPrefix(story, model.WorkspaceModelHeader)
	if err := files.Write(model.PlanModelFile, []byte(story)); err != nil {
		return fmt.Errorf("writing %s: %w", model.PlanModelFile, err)
	}
	if err := writePlanRecord(files, planRecord{Round: round}); err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "round %d: wrote %s from %s. Write the round in %s/ (%s, %s/issues/<nn>-<container>.md, "+
		"and %s, %s or %s where the round needs them), then run theos plan again to check it, write %s and "+
		"file its issues.\n", round, model.PlanModelFile, ModelFile, model.PlanDir, model.PlanChoicesFile,
		model.PlanDir, model.PlanSummaryFile, model.PlanReleasesFile, model.PlanParallelFile, ModelFile)
	return err
}

// readRound reads the round's files under plan/, by path, record aside.
func readRound(fsys fs.FS) (map[string]string, error) {
	out := map[string]string{}
	err := fs.WalkDir(fsys, model.PlanDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || p == PlanFiledFile {
			return err
		}
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		out[p] = string(data)
		return nil
	})
	return out, err
}

// readPlan reads and parses the round in plan/.
func readPlan(fsys fs.FS) (model.Plan, error) {
	round, err := readRound(fsys)
	if err != nil {
		return model.Plan{}, err
	}
	return model.ParsePlan(round)
}

// applyRound checks the round, merges it into model/model.yaml and files its
// issues.
func applyRound(ctx context.Context, env Env, m *Model, files ProjectFiles, record planRecord) error {
	plan, err := readPlan(files.FS)
	if err != nil {
		return fmt.Errorf("nothing written or filed: %w", err)
	}
	if plan.NeedsInput != "" {
		return fmt.Errorf("nothing written or filed: %s asks the person: %s; answer it, remove the file and "+
			"run theos plan again", model.PlanNeedsInputFile, plan.NeedsInput)
	}
	current, err := CurrentModel(m)
	if err != nil {
		return err
	}
	if refusals := model.CheckPlan(plan, current); len(refusals) > 0 {
		for _, r := range refusals {
			if _, err := fmt.Fprintf(env.Out, "refused: %s\n", r); err != nil {
				return err
			}
		}
		return fmt.Errorf("the plan is refused for %d reasons, printed above; nothing written or filed",
			len(refusals))
	}
	if record.Round == 0 {
		record.Round = 1
	}
	if m, err = writeModel(env.Out, m, files, plan, current); err != nil {
		return err
	}
	if err := printRoundNotes(env.Out, plan); err != nil {
		return err
	}
	return fileRound(ctx, env, m, files, plan, &record)
}

// writeModel merges the plan into model/model.yaml, writes it when it
// changed, and returns the model it now holds. The merged file is read back
// as the commands read it, and must hold the plan.
func writeModel(out io.Writer, m *Model, files ProjectFiles, plan model.Plan,
	current model.CurrentModel) (*Model, error) {
	created, changed := model.PlanDiff(plan, current)
	if len(created) == 0 && len(changed) == 0 {
		_, err := fmt.Fprintf(out, "%s: unchanged, it holds the plan\n", ModelFile)
		return m, err
	}
	story, err := fs.ReadFile(files.FS, ModelFile)
	if err != nil {
		return nil, err
	}
	merged, err := MergePlan(story, m, created, changed)
	if err != nil {
		return nil, err
	}
	next, err := readWith(files.FS, merged)
	if err != nil {
		return nil, fmt.Errorf("the merged %s does not read back, so it is not written: %w", ModelFile, err)
	}
	after, err := CurrentModel(next)
	if err != nil {
		return nil, err
	}
	if c, ch := model.PlanDiff(plan, after); len(c)+len(ch) > 0 {
		return nil, fmt.Errorf("the merged %s does not hold the plan's %s, so it is not written", ModelFile,
			refs(append(c, ch...)))
	}
	if err := files.Write(ModelFile, merged); err != nil {
		return nil, fmt.Errorf("writing %s: %w", ModelFile, err)
	}
	says := []string{}
	if len(created) > 0 {
		says = append(says, "created "+refs(created))
	}
	if len(changed) > 0 {
		says = append(says, "changed the story text of "+refs(changed))
	}
	_, err = fmt.Fprintf(out, "%s: written; %s\n", ModelFile, strings.Join(says, "; "))
	return next, err
}

// readWith reads the model files of fsys with story in place of model/model.yaml.
func readWith(fsys fs.FS, story []byte) (*Model, error) {
	setup, err := fs.ReadFile(fsys, SetupFile)
	if err != nil {
		return nil, err
	}
	settings, err := fs.ReadFile(fsys, SettingsFile)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	return Decode(story, setup, settings)
}

// refs names objects as "Kind/name, Kind/name".
func refs(objects []model.PlanObject) string {
	names := make([]string, 0, len(objects))
	for _, o := range objects {
		names = append(names, o.Ref().String())
	}
	return strings.Join(names, ", ")
}

// printRoundNotes prints the round's summary, and what plan/releases.yaml
// and plan/parallel.yaml say, which nothing acts on without a cluster.
func printRoundNotes(out io.Writer, plan model.Plan) error {
	var lines []string
	if plan.Summary != "" {
		lines = append(lines, "summary: "+strings.Join(strings.Fields(plan.Summary), " "))
	}
	if len(plan.Releases) > 0 {
		lines = append(lines, fmt.Sprintf("%s: %s ship releases. Nothing acts on it without a cluster: a "+
			"release is a tag a person pushes.", model.PlanReleasesFile, strings.Join(plan.Releases, ", ")))
	}
	if p := plan.Parallel; p != nil {
		lines = append(lines, fmt.Sprintf("%s: parallel %d (%s). Nothing acts on it without a cluster: how "+
			"many Tasks a Crew starts at once is its spec.parallel in %s.", model.PlanParallelFile, p.N, p.Why,
			SetupFile))
	}
	for _, l := range lines {
		if _, err := fmt.Fprintln(out, l); err != nil {
			return err
		}
	}
	return nil
}

// checkRoundDone refuses to leave a round whose plan is not merged into
// model/model.yaml or whose issues are not all filed. A round that asks
// the person for input has nothing to lose.
func checkRoundDone(m *Model, files ProjectFiles, record planRecord) error {
	plan, err := readPlan(files.FS)
	if err != nil {
		return fmt.Errorf("the round in %s/ does not read, so theos plan -new leaves it: %w", model.PlanDir, err)
	}
	if plan.NeedsInput != "" {
		return nil
	}
	current, err := CurrentModel(m)
	if err != nil {
		return err
	}
	var left []string
	if created, changed := model.PlanDiff(plan, current); len(created)+len(changed) > 0 {
		left = append(left, fmt.Sprintf("%s does not hold %s", ModelFile, refs(append(created, changed...))))
	}
	for _, issue := range plan.Issues {
		if record.filed(issue.N) == nil {
			left = append(left, fmt.Sprintf("issue %d (%s) is not filed", issue.N, issue.File))
		}
	}
	if len(left) > 0 {
		return fmt.Errorf("round %d is not done: %s; run theos plan to finish it", record.Round,
			strings.Join(left, "; "))
	}
	return nil
}

// roundFiles are the round's files fsys holds, the record aside.
func roundFiles(fsys fs.FS) ([]string, error) {
	issues, err := fs.Glob(fsys, model.PlanDir+"/issues/*")
	if err != nil {
		return nil, err
	}
	var out []string
	for _, p := range append(slices.Clone(planRoundFiles), issues...) {
		_, err := fs.Stat(fsys, p)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// removeRound removes the round's files, the record aside.
func removeRound(files ProjectFiles) error {
	paths, err := roundFiles(files.FS)
	if err != nil {
		return err
	}
	for _, p := range paths {
		if err := files.Remove(p); err != nil {
			return fmt.Errorf("removing %s: %w", p, err)
		}
	}
	return nil
}

// planRecord is plan/filed.yaml.
type planRecord struct {
	// Round is the round's number, in each issue's marker.
	Round  int          `yaml:"round"`
	Issues []filedIssue `yaml:"issues"`
}

// filedIssue is one of the round's issues, filed.
type filedIssue struct {
	// N is its number in the round, the nn of its file.
	N         int    `yaml:"nn"`
	Container string `yaml:"container"`
	// Repository is owner/name.
	Repository string `yaml:"repository"`
	Number     int    `yaml:"number"`
	Title      string `yaml:"title"`
	URL        string `yaml:"url"`
}

// filed is the record of the round's issue n, nil when it is not filed.
func (r planRecord) filed(n int) *filedIssue {
	i := slices.IndexFunc(r.Issues, func(f filedIssue) bool { return f.N == n })
	if i < 0 {
		return nil
	}
	return &r.Issues[i]
}

const planRecordHeader = "# Written by theos plan: the round's number and the issues it filed, so it files " +
	"none twice. Commit it with the round.\n"

// readPlanRecord reads plan/filed.yaml strictly; an absent file is round 0.
func readPlanRecord(fsys fs.FS) (planRecord, error) {
	data, err := fs.ReadFile(fsys, PlanFiledFile)
	if errors.Is(err, fs.ErrNotExist) {
		return planRecord{}, nil
	}
	if err != nil {
		return planRecord{}, err
	}
	var r planRecord
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&r); err != nil && !errors.Is(err, io.EOF) {
		return planRecord{}, fmt.Errorf("%s: %w; theos plan writes it, so leave it as it wrote it", PlanFiledFile, err)
	}
	return r, nil
}

func writePlanRecord(files ProjectFiles, r planRecord) error {
	if r.Issues == nil {
		r.Issues = []filedIssue{}
	}
	var buf bytes.Buffer
	buf.WriteString(planRecordHeader)
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(r); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	if err := files.Write(PlanFiledFile, buf.Bytes()); err != nil {
		return fmt.Errorf("writing %s: %w", PlanFiledFile, err)
	}
	return nil
}

// IssueHost is what theos plan reaches on a Container's host: its
// repository and its issues.
type IssueHost interface {
	ports.GitHost
	ports.Issues
}

// planRepo is one Container's repository, as fileRound reads it once.
type planRepo struct {
	host IssueHost
	repo ports.Repository
	// waits says why its issues aren't filed yet; "" when they can be.
	waits string
	// open are its open issues, read once its labels are made.
	open []ports.Issue
}

// fileRound files the plan's issues, blockers first, into the repositories
// of their Containers that have a first commit, and records each in
// plan/filed.yaml as it goes. An issue whose blocker isn't filed waits with
// it; one filed before but not recorded is found by its marker.
func fileRound(ctx context.Context, env Env, m *Model, files ProjectFiles, plan model.Plan,
	record *planRecord) error {
	if len(plan.Issues) == 0 {
		_, err := fmt.Fprintln(env.Out, "the round files no issues")
		return err
	}
	order := model.FilingOrder(plan.Issues)
	if !slices.ContainsFunc(order, func(i model.PlanIssue) bool { return record.filed(i.N) == nil }) {
		return printFiled(env.Out, order, *record)
	}
	cs, err := containers(ctx, env, m)
	if err != nil {
		return err
	}
	repos := map[string]*planRepo{}
	var waiting int
	for _, issue := range order {
		if f := record.filed(issue.N); f != nil {
			if _, err := fmt.Fprintf(env.Out, "issue %d: filed already as %s#%d\n", issue.N, f.Repository,
				f.Number); err != nil {
				return err
			}
			continue
		}
		r, err := repoFor(ctx, cs, repos, plan, issue.Container)
		if err != nil {
			return fmt.Errorf("container %s: %w", issue.Container, err)
		}
		waits := r.waits
		buildsOn := buildsOnFiled(plan.Issues, issue.N, *record)
		if waits == "" && buildsOn == nil {
			waits = "an issue it is after is not filed yet"
		}
		if waits != "" {
			waiting++
			if _, err := fmt.Fprintf(env.Out, "issue %d (%s): waits: %s\n", issue.N, issue.Container,
				waits); err != nil {
				return err
			}
			continue
		}
		filed, err := fileIssue(ctx, r, m.Project, record.Round, issue, buildsOn)
		if err != nil {
			return fmt.Errorf("filing issue %d of %s: %w", issue.N, issue.Container, err)
		}
		record.Issues = append(record.Issues, filed)
		if err := writePlanRecord(files, *record); err != nil {
			return err
		}
		if err := printIssueFiled(env.Out, issue, filed); err != nil {
			return err
		}
	}
	if waiting > 0 {
		_, err := fmt.Fprintf(env.Out, "%d issues wait; run theos plan again once they can be filed\n", waiting)
		return err
	}
	return nil
}

// printFiled says every issue of the round is filed.
func printFiled(out io.Writer, order []model.PlanIssue, record planRecord) error {
	for _, issue := range order {
		f := record.filed(issue.N)
		if _, err := fmt.Fprintf(out, "issue %d: filed already as %s#%d\n", issue.N, f.Repository,
			f.Number); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(out, "round %d is done; theos plan -new starts the next\n", record.Round)
	return err
}

// printIssueFiled says an issue is filed, and what it uses, which nothing
// reads a release of without a cluster.
func printIssueFiled(out io.Writer, issue model.PlanIssue, filed filedIssue) error {
	if _, err := fmt.Fprintf(out, "issue %d: filed as %s#%d, %s\n", issue.N, filed.Repository, filed.Number,
		filed.URL); err != nil {
		return err
	}
	if len(issue.Uses) == 0 {
		return nil
	}
	_, err := fmt.Fprintf(out, "  it uses %s: without a cluster theos reads no release, so the issue names no "+
		"version of it; a release is a tag a person pushes\n", strings.Join(issue.Uses, ", "))
	return err
}

// buildsOnFiled are the filed issues issue n is after, nil when one isn't
// filed; empty, not nil, when it is after none.
func buildsOnFiled(issues []model.PlanIssue, n int, record planRecord) []model.BuildsOn {
	out := []model.BuildsOn{}
	for _, b := range model.IssueBlockers(issues, n) {
		f := record.filed(b)
		if f == nil {
			return nil
		}
		out = append(out, model.BuildsOn{Repository: f.Repository, Number: f.Number, Title: f.Title})
	}
	return out
}

// repoFor is the repository of Container name, read once: whether it has its
// first commit, and once it has, its labels made and its open issues read.
func repoFor(ctx context.Context, cs []container, repos map[string]*planRepo, plan model.Plan,
	name string) (*planRepo, error) {
	if r := repos[name]; r != nil {
		return r, nil
	}
	i := slices.IndexFunc(cs, func(c container) bool { return c.obj.Name == name })
	if i < 0 {
		return nil, fmt.Errorf("%s holds no Container %s", ModelFile, name)
	}
	host, ok := cs[i].host.(IssueHost)
	if !ok {
		return nil, fmt.Errorf("the host of %s files no issues", name)
	}
	r := &planRepo{host: host, repo: host.Repository(name)}
	repos[name] = r
	commits, err := host.History(ctx, r.repo, 1)
	if errors.Is(err, ports.ErrRepositoryNotFound) {
		commits, err = nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(commits) == 0 {
		r.waits = fmt.Sprintf("%s has no repository with a first commit yet; theos scaffold makes it", name)
		return r, nil
	}
	if err := host.EnsureLabels(ctx, r.repo, modelfacts.PlanIssueLabels(model.IssuesFor(plan.Issues,
		name))); err != nil {
		return nil, err
	}
	if r.open, err = host.ListIssues(ctx, r.repo, ""); err != nil {
		return nil, err
	}
	return r, nil
}

// fileIssue files one of the round's issues, or finds it by its marker
// when an earlier run filed it and stopped before recording it, then makes
// it depend on what it builds on and labels it ready, as the Brief does.
func fileIssue(ctx context.Context, r *planRepo, project string, round int, issue model.PlanIssue,
	buildsOn []model.BuildsOn) (filedIssue, error) {
	body := model.BriefIssueBodyFor(issue, project, round, buildsOn, nil, nil)
	var filed ports.Issue
	if i := slices.IndexFunc(r.open, func(o ports.Issue) bool {
		return model.HasIssueMarker(o.Body, project, round, issue.N)
	}); i >= 0 {
		filed = r.open[i]
	} else {
		labels := model.BriefIssueLabels(issue.Labels)
		if len(buildsOn) > 0 {
			labels = slices.DeleteFunc(labels, func(l string) bool { return l == model.LabelReady })
		}
		var err error
		if filed, err = r.host.CreateIssue(ctx, r.repo, issue.Title, body, labels, 0); err != nil {
			return filedIssue{}, err
		}
	}
	if len(buildsOn) > 0 {
		for _, b := range buildsOn {
			owner, name, _ := strings.Cut(b.Repository, "/")
			if err := r.host.AddIssueDependency(ctx, r.repo, filed.Number, ports.Repository{Owner: owner, Name: name},
				b.Number); err != nil {
				return filedIssue{}, fmt.Errorf("making it depend on %s#%d: %w", b.Repository, b.Number, err)
			}
		}
		if !model.IsByHand(issue.Labels) && !slices.Contains(filed.Labels, model.LabelReady) {
			if err := r.host.AddIssueLabel(ctx, r.repo, filed.Number, model.LabelReady); err != nil {
				return filedIssue{}, err
			}
		}
	}
	return filedIssue{N: issue.N, Container: issue.Container, Repository: r.repo.Owner + "/" + r.repo.Name,
		Number: filed.Number, Title: filed.Title, URL: filed.URL}, nil
}
