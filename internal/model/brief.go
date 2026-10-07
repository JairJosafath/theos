package model

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
)

// The story kinds a plan may write besides the element kinds (plans/done/brief.md, B6).
const (
	KindProject      = "Project"
	KindRelationship = "Relationship"
)

// PlanAPIVersion is the apiVersion of every object in a plan.
const PlanAPIVersion = "c4.factory.io/v1alpha1"

// LabelDesign marks an issue that is a design idea for a Crew's designer.
const LabelDesign = "status/design"

// Limits on one round's plan (B7).
const (
	MaxPlanObjects = 20
	MaxPlanIssues  = 10
)

// The files of a plan, on the planner's plan/<round> branch (B5).
const (
	PlanModelFile      = "plan/model.yaml"
	PlanChoicesFile    = "plan/choices.md"
	PlanNeedsInputFile = "plan/needs-input.md"
	PlanSummaryFile    = "plan/summary.md"
	PlanReleasesFile   = "plan/releases.yaml"
	planIssuesDir      = "plan/issues/"
)

// PlanDir is the folder of the brief repository the planner writes; the
// operator writes the rest of it (B4).
const PlanDir = "plan"

// The files the operator keeps on the brief repository's main for the planner (B4).
const (
	BriefFile    = "brief.md"
	PlannerGuide = "PLANNER.md"
)

// PlanBranch is the branch a round's plan is pushed to (B5).
func PlanBranch(round int) string { return "plan/" + strconv.Itoa(round) }

// planKinds are the kinds a plan may write (B6).
var planKinds = []string{KindProject, KindPerson, KindSoftwareSystem, KindContainer, KindDataStore, KindRelationship}

// PlanObject is one story object, from a plan or from the namespace. Fields a
// kind doesn't have stay empty.
type PlanObject struct {
	// APIVersion is set for a plan's objects; the namespace's leave it empty.
	APIVersion string
	Kind       string
	Name       string

	DisplayName string
	Description string
	// Needs and KPIs are a SoftwareSystem's.
	Needs []string
	KPIs  []string

	// SoftwareSystem is a Container's or DataStore's.
	SoftwareSystem string
	// Type, GitProvider, Framework, Standard, Compute and Deployment are a
	// Container's; the last three may be empty.
	Type        string
	GitProvider string
	Framework   string
	Standard    string
	Compute     string
	Deployment  string
	// Connector is a DataStore's.
	Connector string

	// Source, Target and Bundles are a Relationship's.
	Source  Ref
	Target  Ref
	Bundles bool
}

// Ref returns the object's kind and name.
func (o PlanObject) Ref() Ref { return Ref{o.Kind, o.Name} }

// PlanIssue is one issue a plan files, from plan/issues/<nn>-<container>.md.
type PlanIssue struct {
	File      string
	N         int
	Container string
	Title     string
	Labels    []string
	Body      string
	// After are the round numbers (N) of the plan's issues whose work this one
	// needs merged first, from any Container of the round.
	After []int
}

// Plan is what a planner pushed for one round.
type Plan struct {
	// Objects is the whole story model the round leaves: what the namespace
	// holds, changed or not, and what the round adds.
	Objects []PlanObject
	// Issues are ordered by N.
	Issues []PlanIssue
	// Choices are choices.md's lines, list markers dropped.
	Choices []string
	// NeedsInput is needs-input.md's sentence; a plan that has one is not applied.
	NeedsInput string
	// Answers are the plan's answers to reviewers' suggestions.
	Answers []SuggestionAnswer
	// FailedAnswers are the plan's answers to failed issues.
	FailedAnswers []FailedAnswer
	// Summary is summary.md's text: what the round does for the person's app,
	// in a sentence or two.
	Summary string
	// Releases are the Containers releases.yaml says ship releases, sorted,
	// each once (plans/done/brief-releases.md, R1).
	Releases []string
}

// maxRoundSummary is the most of a round's summary a Brief's history keeps.
const maxRoundSummary = 1000

// RoundSummary is what a Brief's history says the plan's round did, for the
// person: the planner's summary.md, or else what the plan holds. It ends on a
// whole sentence and fits the history.
func RoundSummary(plan Plan) string {
	if plan.NeedsInput != "" {
		return "Asked for input before planning."
	}
	if plan.Summary != "" {
		return ClipSentences(strings.Join(strings.Fields(plan.Summary), " "), maxRoundSummary)
	}
	containers := 0
	for _, o := range plan.Objects {
		if o.Kind == KindContainer {
			containers++
		}
	}
	return fmt.Sprintf("The model has %s; the round files %s.", count(containers, "Container"),
		count(len(plan.Issues), "issue"))
}

// count says n things, such as "one issue" or "3 issues".
func count(n int, thing string) string {
	switch n {
	case 0:
		return "no " + thing + "s"
	case 1:
		return "one " + thing
	}
	return fmt.Sprintf("%d %ss", n, thing)
}

var issueFile = regexp.MustCompile(`^plan/issues/(\d+)-([a-z0-9]([-a-z0-9]*[a-z0-9])?)\.md$`)

// ParsePlan reads a plan from the files of its branch, by path. Files outside
// the plan are ignored. Malformed input gives an error naming the file.
func ParsePlan(files map[string]string) (Plan, error) {
	var plan Plan
	if s, ok := files[PlanNeedsInputFile]; ok {
		plan.NeedsInput = strings.TrimSpace(s)
		if plan.NeedsInput == "" {
			return Plan{}, fmt.Errorf("%s is empty: write one sentence saying what you need and why, "+
				"or leave the file out", PlanNeedsInputFile)
		}
	}
	if s, ok := files[PlanModelFile]; ok {
		objects, err := parsePlanModel(s)
		if err != nil {
			return Plan{}, fmt.Errorf("%s: %w", PlanModelFile, err)
		}
		plan.Objects = objects
	} else if plan.NeedsInput == "" {
		return Plan{}, fmt.Errorf("%s is missing: a plan writes the whole story model there", PlanModelFile)
	}
	plan.Summary = strings.TrimSpace(files[PlanSummaryFile])
	releases, err := parsePlanReleases(files[PlanReleasesFile])
	if err != nil {
		return Plan{}, fmt.Errorf("%s: %w", PlanReleasesFile, err)
	}
	plan.Releases = releases
	for line := range strings.SplitSeq(files[PlanChoicesFile], "\n") {
		line = strings.TrimSpace(line)
		for _, marker := range []string{"-", "*", "[ ]", "[x]"} {
			line = strings.TrimSpace(strings.TrimPrefix(line, marker))
		}
		if line != "" && !strings.HasPrefix(line, "#") {
			plan.Choices = append(plan.Choices, line)
		}
	}
	if s, ok := files[PlanSuggestionsFile]; ok {
		answers, err := parseAnswers(s)
		if err != nil {
			return Plan{}, fmt.Errorf("%s: %w", PlanSuggestionsFile, err)
		}
		plan.Answers = answers
	}
	if s, ok := files[PlanFailedFile]; ok {
		answers, err := parseFailedAnswers(s)
		if err != nil {
			return Plan{}, fmt.Errorf("%s: %w", PlanFailedFile, err)
		}
		plan.FailedAnswers = answers
	}
	paths := make([]string, 0, len(files))
	for path := range files {
		if strings.HasPrefix(path, planIssuesDir) && !strings.HasPrefix(strings.TrimPrefix(path, planIssuesDir), ".") {
			paths = append(paths, path)
		}
	}
	slices.Sort(paths)
	for _, path := range paths {
		issue, err := parsePlanIssue(path, files[path])
		if err != nil {
			return Plan{}, fmt.Errorf("%s: %w", path, err)
		}
		plan.Issues = append(plan.Issues, issue)
	}
	slices.SortStableFunc(plan.Issues, func(a, b PlanIssue) int { return a.N - b.N })
	for i := 1; i < len(plan.Issues); i++ {
		if a, b := plan.Issues[i-1], plan.Issues[i]; a.N == b.N {
			return Plan{}, fmt.Errorf("%s: number %d is also %s's; give each issue its own number", b.File, b.N, a.File)
		}
	}
	return plan, nil
}

// parsePlanReleases reads releases.yaml, `containers: [name, ...]`, strictly.
// An absent or empty file releases nothing.
func parsePlanReleases(s string) ([]string, error) {
	var releases struct {
		Containers []string `yaml:"containers"`
	}
	dec := yaml.NewDecoder(strings.NewReader(s))
	dec.KnownFields(true)
	if err := dec.Decode(&releases); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w; the file holds only containers: [name, ...]", err)
	}
	names := slices.Clone(releases.Containers)
	slices.Sort(names)
	return slices.Compact(names), nil
}

type planManifest struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Name string `yaml:"name"`
	} `yaml:"metadata"`
	Spec yaml.Node `yaml:"spec"`
}

type planText struct {
	DisplayName string `yaml:"displayName"`
	Description string `yaml:"description"`
}

type planLocalRef struct {
	Name string `yaml:"name"`
}

type planElementRef struct {
	Kind string `yaml:"kind"`
	Name string `yaml:"name"`
}

type planSystemSpec struct {
	planText `yaml:",inline"`
	Needs    []string `yaml:"needs"`
	KPIs     []string `yaml:"kpis"`
}

type planContainerSpec struct {
	planText          `yaml:",inline"`
	SoftwareSystemRef planLocalRef  `yaml:"softwareSystemRef"`
	Type              string        `yaml:"type"`
	GitProviderRef    planLocalRef  `yaml:"gitProviderRef"`
	FrameworkRef      planLocalRef  `yaml:"frameworkRef"`
	StandardRef       *planLocalRef `yaml:"standardRef"`
	ComputeRef        *planLocalRef `yaml:"computeRef"`
	DeploymentRef     *planLocalRef `yaml:"deploymentRef"`
}

type planDataStoreSpec struct {
	planText          `yaml:",inline"`
	SoftwareSystemRef planLocalRef `yaml:"softwareSystemRef"`
	ConnectorRef      planLocalRef `yaml:"connectorRef"`
}

type planRelationshipSpec struct {
	Source      planElementRef `yaml:"source"`
	Target      planElementRef `yaml:"target"`
	Description string         `yaml:"description"`
	Bundles     bool           `yaml:"bundles"`
}

func (r *planLocalRef) name() string {
	if r == nil {
		return ""
	}
	return r.Name
}

// parsePlanModel reads model.yaml's documents. A kind outside B6 keeps only its
// kind and name, for CheckPlan to refuse; the story kinds' specs are strict.
func parsePlanModel(s string) ([]PlanObject, error) {
	dec := yaml.NewDecoder(strings.NewReader(s))
	var objects []PlanObject
	for doc := 1; ; doc++ {
		var m planManifest
		err := dec.Decode(&m)
		if errors.Is(err, io.EOF) {
			return objects, nil
		}
		if err != nil {
			return nil, fmt.Errorf("document %d: %w", doc, err)
		}
		if m.Kind == "" && m.Metadata.Name == "" && m.Spec.Kind == 0 {
			continue
		}
		o := PlanObject{APIVersion: m.APIVersion, Kind: m.Kind, Name: m.Metadata.Name}
		if o.Kind == "" || o.Name == "" {
			return nil, fmt.Errorf("document %d needs a kind and a metadata.name", doc)
		}
		if err := decodePlanSpec(&m.Spec, &o); err != nil {
			return nil, fmt.Errorf("document %d (%s): spec: %w", doc, o.Ref(), err)
		}
		objects = append(objects, o)
	}
}

func decodePlanSpec(node *yaml.Node, o *PlanObject) error {
	strict := func(into any) error {
		if node.Kind == 0 {
			return nil
		}
		raw, err := yaml.Marshal(node)
		if err != nil {
			return err
		}
		dec := yaml.NewDecoder(bytes.NewReader(raw))
		dec.KnownFields(true)
		return dec.Decode(into)
	}
	switch o.Kind {
	case KindProject, KindPerson:
		var spec planText
		if err := strict(&spec); err != nil {
			return err
		}
		o.DisplayName, o.Description = spec.DisplayName, spec.Description
	case KindSoftwareSystem:
		var spec planSystemSpec
		if err := strict(&spec); err != nil {
			return err
		}
		o.DisplayName, o.Description, o.Needs, o.KPIs = spec.DisplayName, spec.Description, spec.Needs, spec.KPIs
	case KindContainer:
		var spec planContainerSpec
		if err := strict(&spec); err != nil {
			return err
		}
		o.DisplayName, o.Description = spec.DisplayName, spec.Description
		o.SoftwareSystem, o.Type, o.GitProvider = spec.SoftwareSystemRef.Name, spec.Type, spec.GitProviderRef.Name
		o.Framework, o.Standard = spec.FrameworkRef.Name, spec.StandardRef.name()
		o.Compute, o.Deployment = spec.ComputeRef.name(), spec.DeploymentRef.name()
	case KindDataStore:
		var spec planDataStoreSpec
		if err := strict(&spec); err != nil {
			return err
		}
		o.DisplayName, o.Description = spec.DisplayName, spec.Description
		o.SoftwareSystem, o.Connector = spec.SoftwareSystemRef.Name, spec.ConnectorRef.Name
	case KindRelationship:
		var spec planRelationshipSpec
		if err := strict(&spec); err != nil {
			return err
		}
		o.Description, o.Bundles = spec.Description, spec.Bundles
		o.Source = Ref{spec.Source.Kind, spec.Source.Name}
		o.Target = Ref{spec.Target.Kind, spec.Target.Name}
	}
	return nil
}

// parsePlanIssue reads an issue file: front matter with a title and labels
// between --- lines, then the body.
func parsePlanIssue(path, s string) (PlanIssue, error) {
	m := issueFile.FindStringSubmatch(path)
	if m == nil {
		return PlanIssue{}, errors.New("an issue file is named plan/issues/<nn>-<container>.md, such as 01-web.md")
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return PlanIssue{}, err
	}
	issue := PlanIssue{File: path, N: n, Container: m[2]}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	rest, ok := strings.CutPrefix(s, "---\n")
	if !ok {
		return PlanIssue{}, errors.New("an issue starts with front matter: a --- line, title: and labels:, another --- line")
	}
	front, body, ok := strings.Cut(rest, "\n---\n")
	if !ok {
		front, ok = strings.CutSuffix(rest, "\n---")
		if !ok {
			return PlanIssue{}, errors.New("the front matter has no closing --- line")
		}
	}
	var meta struct {
		Title  string   `yaml:"title"`
		Labels []string `yaml:"labels"`
		After  []string `yaml:"after"`
	}
	dec := yaml.NewDecoder(strings.NewReader(front))
	dec.KnownFields(true)
	if err := dec.Decode(&meta); err != nil && !errors.Is(err, io.EOF) {
		return PlanIssue{}, fmt.Errorf("front matter: %w", err)
	}
	issue.Title, issue.Labels, issue.Body = strings.TrimSpace(meta.Title), meta.Labels, strings.TrimSpace(body)
	// Read each number as written, base 10 like the file name: YAML would
	// read 010 as octal 8 and refuse 08.
	for _, a := range meta.After {
		n, err := strconv.Atoi(strings.TrimSpace(a))
		if err != nil || n < 0 {
			return PlanIssue{}, fmt.Errorf("after: has %q; list the nn of the issues it waits for, such as after: [2, 3]", a)
		}
		issue.After = append(issue.After, n)
	}
	if issue.Title == "" {
		return PlanIssue{}, errors.New("the front matter has no title")
	}
	return issue, nil
}

// CurrentModel is what a plan is checked against: the namespace now, the
// installed catalog and the Brief.
type CurrentModel struct {
	// Namespace is the Brief's; a Project the plan makes is named after it.
	Namespace string
	// Objects are the namespace's story objects.
	Objects []PlanObject
	// Catalog is every installed catalog entry.
	Catalog []CatalogEntry
	// GitProvider is the Brief's; every new Container names it.
	GitProvider string
	// Designer says the Brief names a designer, so a new Container's Crew has one.
	Designer bool
	// CrewDesigners are the existing Containers whose Crew has a designer.
	CrewDesigners []string
}

// Refusal is one reason a plan can't be applied, for the planner's next round.
type Refusal struct {
	// Subject is the object (Kind/name) or the file the reason is about.
	Subject string
	Reason  string
}

func (r Refusal) String() string { return r.Subject + ": " + r.Reason }

// PlanDiff splits a plan's objects into the ones it creates and the ones that
// exist and it changes; unchanged ones are in neither.
func PlanDiff(plan Plan, current CurrentModel) (created, changed []PlanObject) {
	for _, o := range plan.Objects {
		before, ok := findObject(current.Objects, o.Ref())
		switch {
		case !ok:
			created = append(created, o)
		case !sameObject(before, o):
			changed = append(changed, o)
		}
	}
	return created, changed
}

// CheckPlan returns every reason the plan can't be applied, or nil when it can
// (B7). Check a plan only when it has no NeedsInput.
func CheckPlan(plan Plan, current CurrentModel) []Refusal {
	var out []Refusal
	refuse := func(subject, format string, args ...any) {
		out = append(out, Refusal{subject, fmt.Sprintf(format, args...)})
	}
	created, changed := PlanDiff(plan, current)
	if n := len(created) + len(changed); n > MaxPlanObjects {
		refuse(PlanModelFile, "creates or changes %d objects; a round does at most %d, so leave the rest for a later round",
			n, MaxPlanObjects)
	}
	if n := len(plan.Issues); n > MaxPlanIssues {
		refuse(planIssuesDir, "has %d issues; a round files at most %d, so keep the most important ones", n, MaxPlanIssues)
	}
	if len(plan.Choices) == 0 {
		refuse(PlanChoicesFile, "is empty: write one line per choice you made, in plain words")
	}

	seen := map[Ref]bool{}
	for _, o := range plan.Objects {
		subject := o.Ref().String()
		if seen[o.Ref()] {
			refuse(subject, "is in the plan twice; write each object once")
			continue
		}
		seen[o.Ref()] = true
		if !slices.Contains(planKinds, o.Kind) || o.APIVersion != PlanAPIVersion {
			refuse(subject, "a plan writes only %s objects of %s; "+
				"anything else (a GitProvider, Secret, Agent, Crew, catalog entry, Task or Brief) is the person's to make",
				joinAnd(planKinds), PlanAPIVersion)
			continue
		}
		if reason := checkDNSLabel(o.Name); reason != "" {
			refuse(subject, "%s", reason)
		}
		checkSpecLimits(o, refuse)
	}
	for _, o := range current.Objects {
		if !seen[o.Ref()] {
			refuse(o.Ref().String(), "is in the namespace but not in %s; a plan deletes nothing, so keep it there unchanged",
				PlanModelFile)
		}
	}
	for _, o := range changed {
		before, _ := findObject(current.Objects, o.Ref())
		for _, field := range lockedFields(o) {
			was := lockedField(before, field[0])
			if was != field[1] {
				refuse(o.Ref().String(), "%s can't change (from %q to %q): a plan changes only the displayName, "+
					"description, needs and kpis of what exists; to change it, file an issue in its repository",
					field[0], was, field[1])
			}
		}
	}
	for _, o := range created {
		checkCreated(plan, current, o, refuse)
	}
	for _, issue := range plan.Issues {
		checkPlanIssue(plan, current, issue, refuse)
		checkAfter(plan, issue, refuse)
	}
	checkOrderCycle(plan, refuse)
	for _, name := range plan.Releases {
		if _, ok := findObject(plan.Objects, Ref{KindContainer, name}); !ok {
			refuse(PlanReleasesFile, "names %q, which is not a Container of %s; release only the plan's Containers",
				name, PlanModelFile)
		}
	}
	return out
}

func checkCreated(plan Plan, current CurrentModel, o PlanObject, refuse func(string, string, ...any)) {
	subject := o.Ref().String()
	needSystem := func() {
		if _, ok := findObject(plan.Objects, Ref{KindSoftwareSystem, o.SoftwareSystem}); !ok {
			refuse(subject, "softwareSystemRef names %q, which is not a SoftwareSystem of the plan", o.SoftwareSystem)
		}
	}
	switch o.Kind {
	case KindProject:
		if o.Name != current.Namespace {
			refuse(subject, "a Project is named after its namespace; name it %s", current.Namespace)
		}
		for _, p := range current.Objects {
			if p.Kind == KindProject {
				refuse(subject, "the namespace has Project %s already; a plan makes a Project only when there is none", p.Name)
				return
			}
		}
		for _, p := range plan.Objects {
			if p.Kind == KindProject && p.Name != o.Name {
				refuse(subject, "the plan makes more than one Project; a namespace has one")
				return
			}
		}
	case KindContainer:
		needSystem()
		checkCreatedContainer(current, o, refuse)
	case KindDataStore:
		needSystem()
		if _, reason := catalogEntry(current.Catalog, SideConnector, o.Connector, ""); reason != "" {
			refuse(subject, "connectorRef %s", reason)
		}
	case KindRelationship:
		checkCreatedRelationship(plan, current, o, refuse)
	}
}

// specLimit is one of the CRDs' limits on a story object's spec: at most max
// characters, or max items when items is set. The API server refuses an object
// over one, so the plan check does first, naming the field (#382).
type specLimit struct {
	field string
	max   int
	items bool
	value func(PlanObject) any
}

// specLimits are the limits of kind's spec, as its CRD's markers in
// api/v1alpha1 set them; a test holds the two together.
func specLimits(kind string) []specLimit {
	displayName := specLimit{"displayName", 100, false, func(o PlanObject) any { return o.DisplayName }}
	description := specLimit{"description", 1000, false, func(o PlanObject) any { return o.Description }}
	switch kind {
	case KindProject, KindPerson, KindContainer, KindDataStore:
		return []specLimit{displayName, description}
	case KindSoftwareSystem:
		return []specLimit{displayName, description,
			{"needs", 20, true, func(o PlanObject) any { return o.Needs }},
			{"kpis", 20, true, func(o PlanObject) any { return o.KPIs }}}
	case KindRelationship:
		return []specLimit{{"description", 200, false, func(o PlanObject) any { return o.Description }}}
	}
	return nil
}

func checkSpecLimits(o PlanObject, refuse func(string, string, ...any)) {
	for _, limit := range specLimits(o.Kind) {
		switch v := limit.value(o).(type) {
		case string:
			if n := utf8.RuneCountInString(v); n > limit.max {
				refuse(o.Ref().String(), "spec.%s is %d characters; it may be at most %d, so shorten it",
					limit.field, n, limit.max)
			}
		case []string:
			if n := len(v); n > limit.max {
				refuse(o.Ref().String(), "spec.%s has %d items; it may have at most %d, so keep the most important ones",
					limit.field, n, limit.max)
			}
		}
	}
}

func checkCreatedContainer(current CurrentModel, o PlanObject, refuse func(string, string, ...any)) {
	subject := o.Ref().String()
	if strings.HasPrefix(o.Name, BriefRepository("")) {
		refuse(subject, "a Container may not be named brief-*: that name is a Brief's repository")
	}
	if !slices.Contains(containerTypes, o.Type) {
		refuse(subject, "type %q is not a Container type; one of %s", o.Type, joinAnd(containerTypes))
		return
	}
	if o.GitProvider != current.GitProvider {
		refuse(subject, "gitProviderRef names %q; a Brief's Containers use its GitProvider, %q", o.GitProvider, current.GitProvider)
	}
	for _, side := range []struct{ side, field, name string }{
		{SideFramework, "frameworkRef", o.Framework},
		{SideStandard, "standardRef", o.Standard},
		{SideCompute, "computeRef", o.Compute},
		{SideDeployment, "deploymentRef", o.Deployment},
	} {
		if side.name == "" && side.side != SideFramework {
			continue
		}
		entry, reason := catalogEntry(current.Catalog, side.side, side.name, o.Type)
		if reason != "" {
			refuse(subject, "%s %s", side.field, reason)
			continue
		}
		if side.side == SideStandard {
			if err := CheckStandard(o.Framework, &entry); err != nil {
				refuse(subject, "standardRef: %v", err)
			}
		}
	}
}

func checkCreatedRelationship(plan Plan, current CurrentModel, o PlanObject, refuse func(string, string, ...any)) {
	subject := o.Ref().String()
	for _, end := range []struct {
		field string
		ref   Ref
	}{{"source", o.Source}, {"target", o.Target}} {
		if _, ok := findObject(plan.Objects, end.ref); !ok || !slices.Contains(elementKinds, end.ref.Kind) {
			refuse(subject, "%s names %s, which is not a Person, SoftwareSystem, Container or DataStore of the plan",
				end.field, end.ref)
		}
	}
	if o.Source == o.Target {
		refuse(subject, "points to itself; a Relationship links two elements")
	}
	if o.Bundles && (o.Source.Kind != KindContainer || o.Target.Kind != KindContainer) {
		refuse(subject, "bundles is set, but only a Container to Container link can bundle")
	}
	source, _ := findObject(plan.Objects, o.Source)
	target, _ := findObject(plan.Objects, o.Target)
	if source.Kind == KindContainer && target.Kind == KindDataStore {
		for _, entry := range current.Catalog {
			if entry.Side == SideConnector && entry.Name == target.Connector && !entry.Supports(source.Type) {
				refuse(subject, "DataStore %s's Connector %s does not support %s, the type of Container %s",
					target.Name, entry.Name, source.Type, source.Name)
			}
		}
	}
}

var containerTypes = []string{TypeStaticSite, TypeService, TypeJob, TypeWorker, TypeMobileApp, TypeCLI, TypeExtension}

var elementKinds = []string{KindPerson, KindSoftwareSystem, KindContainer, KindDataStore}

func checkPlanIssue(plan Plan, current CurrentModel, issue PlanIssue, refuse func(string, string, ...any)) {
	container, ok := findObject(plan.Objects, Ref{KindContainer, issue.Container})
	if !ok {
		refuse(issue.File, "is for Container %q, which is not in the namespace or the plan", issue.Container)
		return
	}
	if slices.Contains(issue.Labels, LabelDesign) {
		_, existing := findObject(current.Objects, container.Ref())
		if (!existing && current.Designer) || (existing && slices.Contains(current.CrewDesigners, container.Name)) {
			return
		}
		refuse(issue.File, "is a design idea (%s), but %s's Crew has no designer; write a ready issue "+
			"with acceptance criteria instead", LabelDesign, container.Name)
		return
	}
	if MissingAcceptanceCriteria(issue.Body) != "" {
		refuse(issue.File, "MissingAcceptanceCriteria: the body needs an \"## Acceptance criteria\" heading with "+
			"a checklist (\"- [ ] ...\") under it, one check a reviewer can verify per item")
	}
}

// checkAfter refuses an after: that names no issue of the plan, the issue
// itself, a number two issues share, a design idea, a number twice or more
// than MaxPlanIssues issues; and a design idea that has an after:. A design
// idea goes to a designer, not a Crew's Task, and no merge closes it.
func checkAfter(plan Plan, issue PlanIssue, refuse func(string, string, ...any)) {
	if len(issue.After) == 0 {
		return
	}
	if slices.Contains(issue.Labels, LabelDesign) {
		refuse(issue.File, "is a design idea (%s) and has after:; a design idea waits on nothing, "+
			"so drop after: or make it a ready issue", LabelDesign)
		return
	}
	if len(issue.After) > MaxPlanIssues {
		refuse(issue.File, "after: names %d issues; it may name at most %d", len(issue.After), MaxPlanIssues)
		return
	}
	seen := map[int]bool{}
	for _, n := range issue.After {
		if seen[n] {
			refuse(issue.File, "after: names %d twice; name each issue once", n)
			continue
		}
		seen[n] = true
		if n == issue.N {
			refuse(issue.File, "after: names %d, the issue itself; an issue can't wait for itself", n)
			continue
		}
		blockers := issuesNumbered(plan.Issues, n)
		switch {
		case len(blockers) == 0:
			refuse(issue.File, "after: names %d, which is no issue of the plan; name the nn of a plan/issues/<nn>-<container>.md", n)
		case len(blockers) > 1:
			refuse(issue.File, "after: names %d, which %d issues share; give each issue its own number", n, len(blockers))
		case slices.Contains(blockers[0].Labels, LabelDesign):
			refuse(issue.File, "after: names %d, a design idea (%s); nothing waits on a design idea, "+
				"since no merge closes it", n, LabelDesign)
		}
	}
}

// checkOrderCycle refuses a plan whose after: lines make a cycle, once per
// cycle, on the file of its lowest-numbered issue.
func checkOrderCycle(plan Plan, refuse func(string, string, ...any)) {
	const (
		unvisited = iota
		visiting
		done
	)
	state := map[int]int{}
	var stack []int
	var visit func(n int)
	visit = func(n int) {
		state[n] = visiting
		stack = append(stack, n)
		for _, m := range IssueBlockers(plan.Issues, n) {
			switch state[m] {
			case unvisited:
				visit(m)
			case visiting:
				cycle := append(slices.Clone(stack[slices.Index(stack, m):]), m)
				first := issuesNumbered(plan.Issues, slices.Min(cycle))[0]
				names := make([]string, len(cycle))
				for i, c := range cycle {
					names[i] = strconv.Itoa(c)
				}
				refuse(first.File, "after: makes a cycle (%s); an issue can't wait, through others, for itself",
					strings.Join(names, " after "))
			}
		}
		stack = stack[:len(stack)-1]
		state[n] = done
	}
	for _, issue := range plan.Issues {
		if state[issue.N] == unvisited {
			visit(issue.N)
		}
	}
}

// IssueBlockers is the after: of the one issue numbered n, keeping only numbers
// of exactly one other issue; checkAfter refuses the rest.
func IssueBlockers(issues []PlanIssue, n int) []int {
	own := issuesNumbered(issues, n)
	if len(own) != 1 {
		return nil
	}
	var out []int
	for _, m := range own[0].After {
		if m != n && len(issuesNumbered(issues, m)) == 1 && !slices.Contains(out, m) {
			out = append(out, m)
		}
	}
	return out
}

func issuesNumbered(issues []PlanIssue, n int) []PlanIssue {
	var out []PlanIssue
	for _, issue := range issues {
		if issue.N == n {
			out = append(out, issue)
		}
	}
	return out
}

// FilingOrder returns a checked plan's issues blockers-first: each issue after
// every issue its after: names, across Containers, and otherwise by N. Issues
// left in a cycle, which CheckPlan refuses, come last by N.
func FilingOrder(issues []PlanIssue) []PlanIssue {
	sorted := slices.Clone(issues)
	slices.SortStableFunc(sorted, func(a, b PlanIssue) int { return a.N - b.N })
	filed := map[int]bool{}
	out := make([]PlanIssue, 0, len(sorted))
	for len(sorted) > 0 {
		i := slices.IndexFunc(sorted, func(issue PlanIssue) bool {
			for _, m := range IssueBlockers(issues, issue.N) {
				if !filed[m] {
					return false
				}
			}
			return true
		})
		if i < 0 {
			return append(out, sorted...)
		}
		filed[sorted[i].N] = true
		out = append(out, sorted[i])
		sorted = slices.Delete(sorted, i, i+1)
	}
	return out
}

// catalogEntry finds the entry of side named name, and says why it can't be
// used: missing, or not supporting containerType (when that is set).
func catalogEntry(catalog []CatalogEntry, side, name, containerType string) (CatalogEntry, string) {
	var installed, supporting []string
	var found *CatalogEntry
	for i, entry := range catalog {
		if entry.Side != side {
			continue
		}
		installed = append(installed, entry.Name)
		if containerType == "" || entry.Supports(containerType) {
			supporting = append(supporting, entry.Name)
		}
		if entry.Name == name {
			found = &catalog[i]
		}
	}
	list := func(names []string) string {
		if len(names) == 0 {
			return "none"
		}
		return joinAnd(names)
	}
	switch {
	case found == nil:
		return CatalogEntry{}, fmt.Sprintf("names %q, which is not an installed %s; installed: %s", name, side, list(installed))
	case containerType != "" && !found.Supports(containerType):
		return CatalogEntry{}, fmt.Sprintf("names %s, which does not support %s; %ss that do: %s",
			name, containerType, side, list(supporting))
	}
	return *found, ""
}

func findObject(objects []PlanObject, ref Ref) (PlanObject, bool) {
	for _, o := range objects {
		if o.Ref() == ref {
			return o, true
		}
	}
	return PlanObject{}, false
}

// lockedFields are an object's fields besides its story text, by API name.
func lockedFields(o PlanObject) [][2]string {
	switch o.Kind {
	case KindContainer:
		return [][2]string{
			{"softwareSystemRef", o.SoftwareSystem}, {"type", o.Type}, {"gitProviderRef", o.GitProvider},
			{"frameworkRef", o.Framework}, {"standardRef", o.Standard}, {"computeRef", o.Compute},
			{"deploymentRef", o.Deployment},
		}
	case KindDataStore:
		return [][2]string{{"softwareSystemRef", o.SoftwareSystem}, {"connectorRef", o.Connector}}
	case KindRelationship:
		return [][2]string{{"source", o.Source.String()}, {"target", o.Target.String()},
			{"bundles", strconv.FormatBool(o.Bundles)}}
	}
	return nil
}

func lockedField(o PlanObject, name string) string {
	for _, field := range lockedFields(o) {
		if field[0] == name {
			return field[1]
		}
	}
	return ""
}

func sameObject(a, b PlanObject) bool {
	return a.DisplayName == b.DisplayName && a.Description == b.Description &&
		slices.Equal(a.Needs, b.Needs) && slices.Equal(a.KPIs, b.KPIs) &&
		slices.Equal(lockedFields(a), lockedFields(b))
}

var dnsLabel = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

// checkDNSLabel says why name isn't a DNS label, or returns "".
func checkDNSLabel(name string) string {
	if len(name) > 63 || !dnsLabel.MatchString(name) {
		return fmt.Sprintf("name %q is not a DNS label: at most 63 lowercase letters, digits and '-', "+
			"starting and ending with a letter or digit", name)
	}
	return ""
}

// RoundPrompts returns the prompts a new round takes: those after the
// promptsPlanned the last applied round took. The round records how many it
// took when it started, so one appended while it runs waits for the next round.
func RoundPrompts(prompts []string, promptsPlanned int) []string {
	if promptsPlanned < 0 || promptsPlanned >= len(prompts) {
		return nil
	}
	return slices.Clone(prompts[promptsPlanned:])
}

// IssueMarker is the line that ties an issue to the Brief's round and the
// plan's issue number n (B11).
func IssueMarker(brief string, round, n int) string {
	return fmt.Sprintf("<!-- c4-brief: %s/%d/%d -->", brief, round, n)
}

// HasIssueMarker reports whether body carries the marker on a line of its own,
// as BriefIssueBody writes it.
func HasIssueMarker(body, brief string, round, n int) bool {
	marker := IssueMarker(brief, round, n)
	for line := range strings.SplitSeq(body, "\n") {
		if strings.TrimSpace(line) == marker {
			return true
		}
	}
	return false
}

// BuildsOn is a filed issue another issue of the round builds on: its
// repository (owner/name), number and title.
type BuildsOn struct {
	Repository string
	Number     int
	Title      string
}

// BriefIssueBody is the body the operator files for a plan's issue: the
// planner's text, in which no comment may pass for a marker, a line
// "Builds on owner/name#N (title)." for each issue it builds on, then the
// marker.
func BriefIssueBody(issue PlanIssue, brief string, round int, buildsOn ...BuildsOn) string {
	var body strings.Builder
	body.WriteString(strings.ReplaceAll(issue.Body, "<!--", "&lt;!--") + "\n\n")
	for _, b := range buildsOn {
		title := strings.Join(strings.Fields(strings.ReplaceAll(b.Title, "<!--", "&lt;!--")), " ")
		fmt.Fprintf(&body, "Builds on %s#%d (%s).\n", b.Repository, b.Number, title)
	}
	if len(buildsOn) > 0 {
		body.WriteString("\n")
	}
	return body.String() + IssueMarker(brief, round, issue.N) + "\n"
}
