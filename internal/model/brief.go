package model

import (
	"bytes"
	"cmp"
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

// PlanAPIVersion is the apiVersion the planner writes on every object in a
// plan: the storage version (docs/plans/done/api-versions.md, decision 5).
const PlanAPIVersion = "theos.factory.io/v1beta1"

// planAPIVersions are the apiVersions a plan may carry: every served version,
// since a plan a person wrote by hand, or one written before the upgrade, may
// still say v1alpha1. Nothing is converted: the operator reads a plan's fields
// the same at either version and applies them as v1beta1 objects.
var planAPIVersions = []string{PlanAPIVersion, "theos.factory.io/v1alpha1"}

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
	PlanParallelFile   = "plan/parallel.yaml"
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
	// Needs, KPIs and Location are a SoftwareSystem's. A plan's Location is
	// LocationInternal when model.yaml leaves it out; a round sets it only on
	// a system it creates, and later rounds keep the one it has.
	Needs    []string
	KPIs     []string
	Location string
	// Domains are a SoftwareSystem's domainRefs: the installed Domains it is
	// about, picked by the round that creates it and never changed by a later
	// one (#1760).
	Domains []string

	// SoftwareSystem is a Container's or DataStore's.
	SoftwareSystem string
	// Type, GitProvider, Bundle, Framework, Standard, Compute and Deployment
	// are a Container's. A plan's Container names a Bundle or its sides, never
	// both; the namespace's carries both the Bundle and what it expanded to.
	Type        string
	GitProvider string
	Bundle      string
	Framework   string
	Standard    string
	Compute     string
	Deployment  string
	// Checks is a Container's spec.checks: from a plan, empty when model.yaml
	// leaves it out, which leaves the Container's setting alone.
	Checks string
	// Operations is a Container's spec.operations: nil when model.yaml leaves
	// it out, which leaves the defaults to the factory.
	Operations *OperationsChoices
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
	// Uses are the Containers whose released Go module this one builds on:
	// it is readied once each has published one (docs/plans/done/app-modules.md).
	Uses []string
	// ReleaseNow says the front matter's release: now marks it bigger: once
	// merged, its release goes out at once (docs/plans/release-cadence.md, C9).
	ReleaseNow bool
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
	// Requests are the plan's answers to the requests its round took.
	Requests []RequestAnswer
	// Summary is summary.md's text: what the round does for the person's app,
	// in a sentence or two.
	Summary string
	// Releases are the Containers releases.yaml says ship releases, sorted,
	// each once (plans/done/brief-releases.md, R1).
	Releases []string
	// Parallel is parallel.yaml's say on how many issues the Brief's Crews
	// build at once, relayed from a new prompt; nil when the file is absent
	// (plans/done/brief-parallel.md, decision 4).
	Parallel *PlanParallel
}

// PlanParallel is plan/parallel.yaml: the number a new prompt asked for, and
// the planner's quote of it.
type PlanParallel struct {
	N   int32
	Why string
}

// maxRoundSummary is the most of a round's summary a Brief's history keeps.
const maxRoundSummary = 1000

// RoundSummary is what a Brief's history says the plan's round did, for the
// person: the planner's summary.md, or else what the plan holds. It ends on a
// whole sentence and fits the history.
func RoundSummary(plan Plan) string { return roundSummary(plan, maxRoundSummary) }

// roundSummary is RoundSummary in at most n bytes of the planner's summary.
func roundSummary(plan Plan, n int) string {
	if plan.NeedsInput != "" {
		return "Asked for input before planning."
	}
	if plan.Summary != "" {
		return ClipSentences(strings.Join(strings.Fields(plan.Summary), " "), n)
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
func ParsePlan(files map[string]string) (Plan, error) { return parsePlan(files, true) }

// parsePlan is ParsePlan; a plan without needModel may leave plan/model.yaml out.
func parsePlan(files map[string]string, needModel bool) (Plan, error) {
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
	} else if plan.NeedsInput == "" && needModel {
		return Plan{}, fmt.Errorf("%s is missing: a plan writes the whole story model there", PlanModelFile)
	}
	plan.Summary = strings.TrimSpace(files[PlanSummaryFile])
	releases, err := parsePlanReleases(files[PlanReleasesFile])
	if err != nil {
		return Plan{}, fmt.Errorf("%s: %w", PlanReleasesFile, err)
	}
	plan.Releases = releases
	if s, ok := files[PlanParallelFile]; ok {
		parallel, err := parsePlanParallel(s)
		if err != nil {
			return Plan{}, fmt.Errorf("%s: %w", PlanParallelFile, err)
		}
		plan.Parallel = &parallel
	}
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
	if s, ok := files[PlanRequestsFile]; ok {
		answers, err := parseRequestAnswers(s)
		if err != nil {
			return Plan{}, fmt.Errorf("%s: %w", PlanRequestsFile, err)
		}
		plan.Requests = answers
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

// parsePlanParallel reads parallel.yaml, `parallel: N` and `why:`, strictly.
// Whether N is one the Brief takes is CheckPlan's to say.
func parsePlanParallel(s string) (PlanParallel, error) {
	const shape = "the file holds only parallel: N and why: quoting the prompt that asks"
	var parallel struct {
		Parallel *int32 `yaml:"parallel"`
		Why      string `yaml:"why"`
	}
	dec := yaml.NewDecoder(strings.NewReader(s))
	dec.KnownFields(true)
	if err := dec.Decode(&parallel); err != nil && !errors.Is(err, io.EOF) {
		return PlanParallel{}, fmt.Errorf("%w; %s", err, shape)
	}
	if err := checkCutText(s); err != nil {
		return PlanParallel{}, err
	}
	if parallel.Parallel == nil {
		return PlanParallel{}, fmt.Errorf("parallel: is missing; %s, or leave the file out", shape)
	}
	why := strings.Join(strings.Fields(parallel.Why), " ")
	if why == "" {
		return PlanParallel{}, fmt.Errorf("why: is empty; %s", shape)
	}
	return PlanParallel{N: *parallel.Parallel, Why: why}, nil
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
	planText   `yaml:",inline"`
	Needs      []string `yaml:"needs"`
	KPIs       []string `yaml:"kpis"`
	Location   string   `yaml:"location"`
	DomainRefs []string `yaml:"domainRefs"`
}

type planContainerSpec struct {
	planText          `yaml:",inline"`
	SoftwareSystemRef planLocalRef    `yaml:"softwareSystemRef"`
	Type              string          `yaml:"type"`
	GitProviderRef    planLocalRef    `yaml:"gitProviderRef"`
	BundleRef         *planLocalRef   `yaml:"bundleRef"`
	FrameworkRef      planLocalRef    `yaml:"frameworkRef"`
	StandardRef       *planLocalRef   `yaml:"standardRef"`
	ComputeRef        *planLocalRef   `yaml:"computeRef"`
	DeploymentRef     *planLocalRef   `yaml:"deploymentRef"`
	Checks            string          `yaml:"checks"`
	Operations        *planOperations `yaml:"operations"`
}

type planOperations struct {
	Backups *struct {
		RetentionDays *int32 `yaml:"retentionDays"`
		CopyToRegion  string `yaml:"copyToRegion"`
	} `yaml:"backups"`
	Recovery *struct {
		RPO string `yaml:"rpo"`
		RTO string `yaml:"rto"`
	} `yaml:"recovery"`
	Alerts *struct {
		Email string `yaml:"email"`
	} `yaml:"alerts"`
}

// choices is the block as model reads it.
func (p *planOperations) choices() *OperationsChoices {
	if p == nil {
		return nil
	}
	var c OperationsChoices
	if b := p.Backups; b != nil {
		c.RetentionDays, c.CopyToRegion = b.RetentionDays, b.CopyToRegion
	}
	if r := p.Recovery; r != nil {
		c.RPO, c.RTO = r.RPO, r.RTO
	}
	if a := p.Alerts; a != nil {
		c.Email = a.Email
	}
	return &c
}

// errPlanTier refuses a tier on a plan's Container: where the app runs, and so
// its tier, is a deploy target's (plans/aws-production.md, decision 1).
var errPlanTier = errors.New("a Container has no tier: production or development is the deploy target's to " +
	"say, never the plan's; leave tier out, and write in operations only what the idea states")

// hasTier reports whether a Container's spec node writes a tier, in spec or
// in spec.operations.
func hasTier(spec *yaml.Node) bool {
	value := func(n *yaml.Node, key string) *yaml.Node {
		if n == nil || n.Kind != yaml.MappingNode {
			return nil
		}
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == key {
				return n.Content[i+1]
			}
		}
		return nil
	}
	return value(spec, "tier") != nil || value(value(spec, "operations"), "tier") != nil
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
		o.Location, o.Domains = cmp.Or(spec.Location, LocationInternal), spec.DomainRefs
	case KindContainer:
		if hasTier(node) {
			return errPlanTier
		}
		var spec planContainerSpec
		if err := strict(&spec); err != nil {
			return err
		}
		o.DisplayName, o.Description = spec.DisplayName, spec.Description
		o.SoftwareSystem, o.Type, o.GitProvider = spec.SoftwareSystemRef.Name, spec.Type, spec.GitProviderRef.Name
		o.Bundle, o.Framework, o.Standard = spec.BundleRef.name(), spec.FrameworkRef.Name, spec.StandardRef.name()
		o.Compute, o.Deployment = spec.ComputeRef.name(), spec.DeploymentRef.name()
		o.Checks, o.Operations = spec.Checks, spec.Operations.choices()
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
		Title   string   `yaml:"title"`
		Labels  []string `yaml:"labels"`
		After   []string `yaml:"after"`
		Uses    []string `yaml:"uses"`
		Release string   `yaml:"release"`
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
	for _, u := range meta.Uses {
		issue.Uses = append(issue.Uses, strings.TrimSpace(u))
	}
	switch strings.TrimSpace(meta.Release) {
	case "":
	case ReleaseNowValue:
		issue.ReleaseNow = true
	default:
		return PlanIssue{}, fmt.Errorf("release: has %q; the only value is %s, for work that ships at once",
			meta.Release, ReleaseNowValue)
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
	// Bundles are the namespace's Bundles.
	Bundles []Bundle
	// GitProvider is the Brief's; every new Container names it.
	GitProvider string
	// Designer says the Brief names a designer, so a new Container's Crew has one.
	Designer bool
	// CrewDesigners are the existing Containers whose Crew has a designer.
	CrewDesigners []string
	// Released are the existing Containers whose Crew has made a release.
	Released []string
	// NewPrompt says the round takes a new prompt, or the idea no round
	// planned yet: only then may the plan say how much is built at once.
	NewPrompt bool
	// SpecParallel is the Brief's spec.parallel, 0 when unset; set, it wins
	// over a prompt, so a plan may not write parallel.yaml.
	SpecParallel int32
	// BriefContainers are the existing Containers the Brief made: a plan
	// sets the checks of only these.
	BriefContainers []string
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
		case !sameObject(before, o) || checksChange(before, o):
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
		if !slices.Contains(planKinds, o.Kind) || !slices.Contains(planAPIVersions, o.APIVersion) {
			refuse(subject, "a plan writes only %s objects of %s; "+
				"anything else (a GitProvider, Secret, Agent, Crew, catalog entry, Task or Brief) is the person's to make",
				joinAnd(planKinds), PlanAPIVersion)
			continue
		}
		if reason := checkDNSLabel(o.Name); reason != "" {
			refuse(subject, "%s", reason)
		}
		checkSpecLimits(o, refuse)
		if l := SystemLocation(o); o.Kind == KindSoftwareSystem && l != LocationInternal && l != LocationExternal {
			refuse(subject, "location %q is not a SoftwareSystem location: write Internal or External", l)
		}
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
					"description, needs and kpis of what exists, and a Container's checks; to change it, file an "+
					"issue in its repository",
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
		checkUses(plan, current, issue, refuse)
	}
	checkOrderCycle(plan, refuse)
	for _, name := range plan.Releases {
		if _, ok := findObject(plan.Objects, Ref{KindContainer, name}); !ok {
			refuse(PlanReleasesFile, "names %q, which is not a Container of %s; release only the plan's Containers",
				name, PlanModelFile)
		}
	}
	checkParallel(plan, current, refuse)
	checkChecks(plan, current, refuse)
	checkOperations(plan, current, refuse)
	checkDomains(plan, current, refuse)
	return out
}

// checkParallel checks parallel.yaml (plans/done/brief-parallel.md, decisions 4
// and 5): a number of 1 to 5, a new prompt that asks for it, and no
// spec.parallel that wins over it.
func checkParallel(plan Plan, current CurrentModel, refuse func(string, string, ...any)) {
	if plan.Parallel == nil {
		return
	}
	if n := plan.Parallel.N; n < 1 || n > 5 {
		refuse(PlanParallelFile, "says parallel: %d; a Crew builds 1 to 5 issues at once, so write the number "+
			"the prompt asks for within that, and say so in %s", n, PlanChoicesFile)
	}
	if !current.NewPrompt {
		refuse(PlanParallelFile, "is written in a round with no new prompt; how much the Crews build at once "+
			"is the person's say, so write it only when a new prompt asks, and leave the file out")
	}
	if current.SpecParallel > 0 {
		refuse(PlanParallelFile, "is written, but the Brief's spec.parallel sets it to %d, which wins over a "+
			"prompt; leave the file out and say in %s that the number is set on the Brief",
			current.SpecParallel, PlanChoicesFile)
	}
}

func checkCreated(plan Plan, current CurrentModel, o PlanObject, refuse func(string, string, ...any)) {
	subject := o.Ref().String()
	needSystem := func() {
		ref := Ref{KindSoftwareSystem, o.SoftwareSystem}
		system, ok := findObject(plan.Objects, ref)
		if !ok {
			refuse(subject, "softwareSystemRef names %q, which is not a SoftwareSystem of the plan", o.SoftwareSystem)
			return
		}
		// An existing system keeps its location, whatever the plan says.
		if before, ok := findObject(current.Objects, ref); ok {
			system = before
		}
		if SystemLocation(system) == LocationExternal {
			refuse(subject, "softwareSystemRef names %q, which is External: the factory builds nothing for a "+
				"system the story only names, so put the %s in an Internal system", o.SoftwareSystem, o.Kind)
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
// api/v1beta1 set them; a test holds the two together.
func specLimits(kind string) []specLimit {
	displayName := specLimit{"displayName", 100, false, func(o PlanObject) any { return o.DisplayName }}
	description := specLimit{"description", 1000, false, func(o PlanObject) any { return o.Description }}
	switch kind {
	case KindProject, KindPerson, KindContainer, KindDataStore:
		return []specLimit{displayName, description}
	case KindSoftwareSystem:
		return []specLimit{displayName, description,
			{"needs", 20, true, func(o PlanObject) any { return o.Needs }},
			{"kpis", 20, true, func(o PlanObject) any { return o.KPIs }},
			{"domainRefs", 5, true, func(o PlanObject) any { return o.Domains }}}
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
	if o.Bundle != "" {
		checkCreatedBundle(current, o, refuse)
		return
	}
	if o.Framework == "" {
		refuse(subject, "names no bundleRef and no frameworkRef; a Container names a Bundle of %s or its sides", WorkspaceCatalogFile)
		return
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
		switch side.side {
		case SideFramework:
			if err := CheckDeploysItself(entry, o.Type, o.Compute, o.Deployment); err != nil {
				refuse(subject, "frameworkRef: %v", err)
			}
		case SideStandard:
			if err := CheckStandard(o.Framework, &entry); err != nil {
				refuse(subject, "standardRef: %v", err)
			}
		}
	}
}

// checkCreatedBundle checks a Container that names a Bundle: no side beside
// it, a Bundle of the namespace whose entries are installed, and the entries
// checked as the operator checks them, so the round fails with the message
// the Container would show.
func checkCreatedBundle(current CurrentModel, o PlanObject, refuse func(string, string, ...any)) {
	subject := o.Ref().String()
	if o.Framework != "" || o.Standard != "" || o.Compute != "" || o.Deployment != "" {
		refuse(subject, "bundleRef replaces frameworkRef, standardRef, computeRef and deploymentRef: name the "+
			"Bundle or the sides, never both")
		return
	}
	bundle, ok := findBundle(current.Bundles, o.Bundle)
	if !ok {
		names := []string{}
		for _, b := range current.Bundles {
			names = append(names, b.Name)
		}
		slices.Sort(names)
		refuse(subject, "bundleRef names %q, which is not a Bundle of the namespace; installed: %s",
			o.Bundle, cmp.Or(joinAnd(names), "none"))
		return
	}
	entries, missing := BundleEntries(bundle, current.Catalog)
	if missing != "" {
		refuse(subject, "bundleRef: bundle %s names %s, which is not installed; fix the Bundle or name another",
			bundle.Name, missing)
		return
	}
	if err := CheckBundle(bundle, o.Type, entries); err != nil {
		refuse(subject, "bundleRef: %v", err)
	}
}

func findBundle(bundles []Bundle, name string) (Bundle, bool) {
	for _, b := range bundles {
		if b.Name == name {
			return b, true
		}
	}
	return Bundle{}, false
}

// planFramework is the Framework a plan's Container is on: its frameworkRef,
// or its Bundle's; empty when the Bundle isn't the namespace's.
func planFramework(o PlanObject, bundles []Bundle) string {
	if o.Bundle == "" {
		return o.Framework
	}
	b, _ := findBundle(bundles, o.Bundle)
	return b.Framework
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
			if entry.Side != SideConnector || entry.Name != target.Connector {
				continue
			}
			if !entry.Supports(source.Type) {
				refuse(subject, "DataStore %s's Connector %s does not support %s, the type of Container %s",
					target.Name, entry.Name, source.Type, source.Name)
			}
			// #1958: the operator's NotWired check, so the round fails with
			// the message the Container would show. An empty Framework is
			// refused on the Container itself.
			framework := planFramework(source, current.Bundles)
			if framework == "" {
				continue
			}
			if err := CheckWiring(framework, []CatalogEntry{entry}); err != nil {
				refuse(subject, "DataStore %s's Connector %v, the Framework of Container %s",
					target.Name, err, source.Name)
			}
		}
	}
}

// SystemLocation is a SoftwareSystem's location, LocationInternal when it
// has none, as the API defaults it.
func SystemLocation(o PlanObject) string { return cmp.Or(o.Location, LocationInternal) }

var containerTypes = []string{TypeStaticSite, TypeService, TypeJob, TypeWorker, TypeMobileApp, TypeCLI, TypeExtension, TypeOperator, TypeFunction}

var elementKinds = []string{KindPerson, KindSoftwareSystem, KindContainer, KindDataStore}

func checkPlanIssue(plan Plan, current CurrentModel, issue PlanIssue, refuse func(string, string, ...any)) {
	checkNeverPaths(issue, refuse)
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

// checkNeverPaths refuses an issue whose title or acceptance criteria name a
// path under AgentNeverPaths: no change may make it, so its Task could only end
// Blocked. The rest of the body may name one ("as AGENTS.md says").
func checkNeverPaths(issue PlanIssue, refuse func(string, string, ...any)) {
	criteria, _ := criteriaSection(issue.Body)
	for _, field := range [][2]string{{"title", issue.Title}, {"acceptance criteria", criteria}} {
		if path := NeverPathNamed(field[1]); path != "" {
			refuse(issue.File, "names %s in its %s, a path no change may make: %s are the agents' rules and "+
				"the platform's settings. File no issue for it; say what wasn't planned in %s and why in %s. "+
				"Notes for the agents go in %s, which an issue may write", path, field[0], neverPathsCode(),
				PlanSummaryFile, PlanChoicesFile, ProjectNotesPath)
			return
		}
	}
}

// checkAfter refuses an after: that names no issue of the plan, the issue
// itself, a number two issues share, a design idea, a by-hand issue, a number
// twice or more than MaxPlanIssues issues; and a design idea that has an
// after:. A design idea goes to a designer and a by-hand issue to a person,
// not a Crew's Task, so no merge closes them. A by-hand issue may wait on
// agents' work: the screenshot of a screen once the screen is built.
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
		case IsByHand(blockers[0].Labels):
			refuse(issue.File, "after: names %d, a person's work (%s); nothing waits on it, "+
				"since no Task merges it", n, LabelByHand)
		}
	}
}

// checkUses refuses a uses: on a design idea, and one that names the issue's
// own Container, a Container twice, one not in the plan, one the issue's
// Container has no Relationship to, or one with neither a release made nor
// one the round makes: released in releases.yaml, with an issue of the round
// to release, since a release with no work is never made (R7).
func checkUses(plan Plan, current CurrentModel, issue PlanIssue, refuse func(string, string, ...any)) {
	if len(issue.Uses) == 0 {
		return
	}
	if slices.Contains(issue.Labels, LabelDesign) {
		refuse(issue.File, "is a design idea (%s) and has uses:; a design idea builds no code, "+
			"so drop uses: or make it a ready issue", LabelDesign)
		return
	}
	seen := map[string]bool{}
	for _, name := range issue.Uses {
		if seen[name] {
			refuse(issue.File, "uses: names %q twice; name each Container once", name)
			continue
		}
		seen[name] = true
		if name == issue.Container {
			refuse(issue.File, "uses: names %q, the issue's own Container; it builds on its own code without a release", name)
			continue
		}
		if _, ok := findObject(plan.Objects, Ref{KindContainer, name}); !ok {
			refuse(issue.File, "uses: names %q, which is not a Container of %s", name, PlanModelFile)
			continue
		}
		if !hasRelationship(plan.Objects, Ref{KindContainer, issue.Container}, Ref{KindContainer, name}) {
			refuse(issue.File, "uses: names %q, but no Relationship goes from Container %s to it; "+
				"add one to %s that says what %s uses it for", name, issue.Container, PlanModelFile, issue.Container)
			continue
		}
		if slices.Contains(current.Released, name) {
			continue
		}
		switch {
		case !slices.Contains(plan.Releases, name):
			refuse(issue.File, "uses: names %q, which has made no release and gets none this round; "+
				"name it in %s so the round releases its module", name, PlanReleasesFile)
		case len(IssuesFor(plan.Issues, name)) == 0:
			refuse(issue.File, "uses: names %q, which has made no release, and the round files no issue for it; "+
				"a release with no work is never made, so file its work in this round", name)
		}
	}
}

// hasRelationship reports whether a Relationship goes from source to target.
func hasRelationship(objects []PlanObject, source, target Ref) bool {
	return slices.ContainsFunc(objects, func(o PlanObject) bool {
		return o.Kind == KindRelationship && o.Source == source && o.Target == target
	})
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
// A Container on a Bundle locks the Bundle, not the sides it expanded to:
// the namespace's carries both, a plan's only the Bundle (#1622).
func lockedFields(o PlanObject) [][2]string {
	switch o.Kind {
	case KindContainer:
		fields := make([][2]string, 0, 8)
		fields = append(fields, [2]string{"softwareSystemRef", o.SoftwareSystem}, [2]string{"type", o.Type},
			[2]string{"gitProviderRef", o.GitProvider}, [2]string{"bundleRef", o.Bundle})
		if o.Bundle != "" {
			return fields
		}
		return append(fields, [2]string{"frameworkRef", o.Framework}, [2]string{"standardRef", o.Standard},
			[2]string{"computeRef", o.Compute}, [2]string{"deploymentRef", o.Deployment})
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
	return slices.DeleteFunc(slices.Clone(prompts[promptsPlanned:]), func(p string) bool { return p == "" })
}

// KeptPlannedPrompts is how many of the prompts rounds have planned a Brief
// keeps in spec.prompts when a prompt is appended to a full list; the writer
// that appends retires older ones in the same write, so a Brief takes prompts
// for as long as its project runs (#835). The operator never writes the spec
// (#855).
const KeptPlannedPrompts = 10

// PromptsToRetire returns how many of the oldest of listed prompts a writer
// appending to a full list retires, given how many were retired before and
// how many prompts, retired ones included, the last applied round planned:
// the planned ones past the newest KeptPlannedPrompts. A prompt no round
// planned stays. The UI server's append (ui/server.mjs) follows the same rule.
func PromptsToRetire(retired, listed, promptsPlanned int) int {
	return max(min(promptsPlanned-retired-KeptPlannedPrompts, listed), 0)
}

// PlacedPrompts returns a Brief's prompts where the status counts them: every
// prompt ever written, oldest first, the retired ones empty.
func PlacedPrompts(retired int, prompts []string) []string {
	return append(make([]string, max(retired, 0), max(retired, 0)+len(prompts)), prompts...)
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
	return BriefIssueBodyUsing(issue, brief, round, buildsOn, nil)
}

// UsedPackage is what a uses: issue builds on: one thing a used Container's
// release published, at that release's version. Ecosystem is EcosystemGo for
// its Go module (Name is the module path, Version the tag), EcosystemNpm for
// an npm package (Name is scoped, Version the tag without its v) or
// EcosystemPyPI for a PyPI package, whose Index is the simple index it is
// installed from.
type UsedPackage struct {
	Ecosystem string
	Name      string
	Version   string
	Index     string
}

// UsesLine is the body line that pins one thing a used release published,
// with the command the app's AGENTS.md has an agent run for it.
func UsesLine(p UsedPackage) string {
	switch p.Ecosystem {
	case EcosystemNpm:
		return fmt.Sprintf("Uses npm package %s %s: npm install --save-exact %s@%s.", p.Name, p.Version, p.Name, p.Version)
	case EcosystemPyPI:
		return fmt.Sprintf("Uses PyPI package %s %s from the theos index %s: uv add %s==%s, pinned to that index "+
			"as AGENTS.md says.", p.Name, p.Version, p.Index, p.Name, p.Version)
	}
	return fmt.Sprintf("Uses %s %s: go get it at that version.", p.Name, p.Version)
}

// UsesNothingPublished is what a uses: issue waits for when the used app's
// release settled but published nothing another app installs: neither a Go
// module (goMessage says why) nor a package (packagesMessage says why).
func UsesNothingPublished(app, tag, goMessage, packagesMessage string) string {
	return fmt.Sprintf("%s, whose release %s published nothing other apps install: %s; %s", app, tag, goMessage,
		packagesMessage)
}

// BriefIssueBodyUsing is BriefIssueBody with a UsesLine for each thing the
// issue uses, after the Builds on lines.
func BriefIssueBodyUsing(issue PlanIssue, brief string, round int, buildsOn []BuildsOn, uses []UsedPackage) string {
	var body strings.Builder
	body.WriteString(strings.ReplaceAll(issue.Body, "<!--", "&lt;!--") + "\n\n")
	for _, b := range buildsOn {
		title := strings.Join(strings.Fields(strings.ReplaceAll(b.Title, "<!--", "&lt;!--")), " ")
		fmt.Fprintf(&body, "Builds on %s#%d (%s).\n", b.Repository, b.Number, title)
	}
	if len(buildsOn) > 0 {
		body.WriteString("\n")
	}
	for _, m := range uses {
		body.WriteString(UsesLine(m) + "\n")
	}
	if len(uses) > 0 {
		body.WriteString("\n")
	}
	return body.String() + IssueMarker(brief, round, issue.N) + "\n"
}
