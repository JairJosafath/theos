package model

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

// standInTitleRunes is how much of a prompt the stand-in planner puts in an
// issue's title; the body has all of it.
const standInTitleRunes = 80

var (
	briefHeading     = regexp.MustCompile(`(?m)^# Brief (\S+)$`)
	briefGitProvider = regexp.MustCompile("`gitProviderRef: \\{name: (\\S+)\\}`")
	briefNewPrompt   = regexp.MustCompile(`(?m)^\d+\. \(\*\*new\*\*\) (.+)$`)
)

// StandInPlan is the stand-in planner's plan for the workspace the operator
// wrote (BriefWorkspaceFiles), by path. It plans from the catalog alone: a
// round that plans the idea adds one Container named after the Brief, on the
// catalog's first Framework and type a Deployment ships and a Standard fits,
// with both, else on its first Framework and that Framework's first type, with
// the first Standard that fits both, in the first Internal SoftwareSystem, and
// files one issue for it; a Container with a Deployment is released
// (plan/releases.yaml), so its Crew prepares, tags and ships a release once
// the issue is merged; each new prompt is one issue for the Brief's Container, else the model's first, and so is each
// request of model/requests.yaml, taken in plan/requests.yaml (a request it
// has no room or no Container for is skipped). It changes nothing
// that exists, so CheckPlan takes it; with no Framework to plan the idea on,
// it asks for one. It runs the whole loop without a model, for tests and
// demos (plans/done/brief.md, step 8).
// StandInTendSummary is the stand-in planner's summary of a tending round.
const StandInTendSummary = "The stand-in planner decides no failed issue: each waits for the person."

func StandInPlan(workspace map[string]string) (map[string]string, error) {
	brief := workspace[BriefFile]
	m, provider := briefHeading.FindStringSubmatch(brief), briefGitProvider.FindStringSubmatch(brief)
	if m == nil || provider == nil {
		return nil, fmt.Errorf("%s names no Brief or no Git provider", BriefFile)
	}
	name := m[1]
	if IsTendingBrief(brief) {
		// It decides nothing (plans/done/brief-autopilot.md, decision 12): every
		// due issue counts as an ask.
		return map[string]string{PlanSummaryFile: StandInTendSummary + "\n"}, nil
	}
	objects, err := parsePlanModel(workspace[WorkspaceModelFile])
	if err != nil {
		return nil, fmt.Errorf("%s: %w", WorkspaceModelFile, err)
	}
	var catalog catalogOut
	if err := yaml.Unmarshal([]byte(workspace[WorkspaceCatalogFile]), &catalog); err != nil {
		return nil, fmt.Errorf("%s: %w", WorkspaceCatalogFile, err)
	}
	// The idea comes first, then the prompts, one line each, then the Git
	// provider; the idea's own text can't fake the later headings.
	idea := strings.Index(brief, "\n## Idea")
	ideaNew := idea >= 0 && strings.HasPrefix(brief[idea:], "\n## Idea (**new**)\n")
	var prompts []string
	if i, end := strings.LastIndex(brief, "\n## Prompts\n"), strings.LastIndex(brief, "\n## Git provider\n"); i > idea && end > i {
		for _, p := range briefNewPrompt.FindAllStringSubmatch(brief[i:end], -1) {
			prompts = append(prompts, p[1])
		}
	}

	files := map[string]string{}
	var choices []string
	var issues [][2]string // title after "Stand-in: ", and the goal
	// The Brief's Container is named after the Brief, without the prefix of
	// the Brief's own repository.
	base := strings.TrimPrefix(name, BriefRepository(""))
	container := standInContainer(objects, base)
	if ideaNew {
		framework, kind, standard, deployment := standInCatalog(catalog)
		if framework == "" {
			return map[string]string{PlanNeedsInputFile: "The catalog has no Framework with a Container type, " +
				"so the stand-in planner can't add a Container: install one.\n"}, nil
		}
		system := ""
		// The first Internal system: the factory builds nothing in an
		// External one (#1428).
		for _, o := range objects {
			if o.Kind == KindSoftwareSystem && SystemLocation(o) == LocationInternal {
				system = o.Name
				break
			}
		}
		if system == "" {
			system = name
			objects = append(objects, PlanObject{APIVersion: PlanAPIVersion, Kind: KindSoftwareSystem, Name: system,
				Description: "What Brief " + name + " describes."})
			choices = append(choices, fmt.Sprintf("The stand-in planner makes SoftwareSystem %s: the namespace has no Internal one.",
				system))
		}
		container = standInName(objects, base)
		objects = append(objects, PlanObject{APIVersion: PlanAPIVersion, Kind: KindContainer, Name: container,
			SoftwareSystem: system, Type: kind, GitProvider: provider[1], Framework: framework, Standard: standard,
			Deployment: deployment})
		with := ""
		if standard != "" {
			with = " with the Standard " + standard
		}
		if deployment == "" {
			choices = append(choices, fmt.Sprintf("The stand-in planner adds Container %s, a %s on %s%s: "+
				"no Deployment ships a Framework of the catalog, so it takes the first and reads nothing else.",
				container, kind, framework, with))
		} else {
			choices = append(choices, fmt.Sprintf("The stand-in planner adds Container %s, a %s on %s%s and the "+
				"Deployment %s: it takes the catalog's first Framework a Deployment ships and reads nothing else.",
				container, kind, framework, with, deployment),
				fmt.Sprintf("The stand-in planner releases %s: once its issue is merged, its Crew prepares and tags "+
					"the release, and %s ships it.", container, deployment))
			files[PlanReleasesFile] = fmt.Sprintf("containers: [%s]\n", container)
		}
		issues = append(issues, [2]string{"start " + name, fmt.Sprintf("Start %s as Brief %s's idea says.", container, name)})
	}
	switch {
	case len(prompts) == 0:
	case container == "":
		choices = append(choices, "The stand-in planner files no issue: the namespace has no Container to file it for.")
	default:
		for _, p := range prompts {
			issues = append(issues, [2]string{shortTitle(p), oneParagraph(p)})
		}
		choices = append(choices, fmt.Sprintf("The stand-in planner files each new prompt as an issue for %s.", container))
	}
	if len(issues) > MaxPlanIssues {
		issues = issues[:MaxPlanIssues]
	}
	answers, err := standInRequests(workspace[WorkspaceRequestsFile], container, &issues)
	if err != nil {
		return nil, err
	}
	if len(answers) > 0 {
		data, err := yaml.Marshal(answers)
		if err != nil {
			return nil, err
		}
		files[PlanRequestsFile] = string(data)
		choices = append(choices, "The stand-in planner files each request it can as an issue.")
	}
	for n, issue := range issues {
		files[fmt.Sprintf("%s%02d-%s.md", planIssuesDir, n+1, container)] = fmt.Sprintf(
			"---\ntitle: %q\nlabels: []\n---\n## Goal\n\n%s\n\n## Acceptance criteria\n\n- [ ] A change for it is merged to main\n",
			"Stand-in: "+issue[0], issue[1])
	}
	if len(choices) == 0 {
		choices = append(choices, "The stand-in planner keeps the model as it is.")
	}

	story, err := renderWorkspaceModel(objects)
	if err != nil {
		return nil, err
	}
	_, story, _ = strings.Cut(story, "\n") // the workspace's "copy it" header
	files[PlanModelFile] = story
	files[PlanChoicesFile] = "- " + strings.Join(choices, "\n- ") + "\n"
	files[PlanSummaryFile] = fmt.Sprintf("The stand-in planner files %s.\n", count(len(issues), "issue"))
	return files, nil
}

// standInRequests answers each request of model/requests.yaml: an issue for
// container, appended to issues, while the round has room for one, else a skip.
func standInRequests(file, container string, issues *[][2]string) ([]RequestAnswer, error) {
	var requests []requestOut
	if err := yaml.Unmarshal([]byte(file), &requests); err != nil {
		return nil, fmt.Errorf("%s: %w", WorkspaceRequestsFile, err)
	}
	var answers []RequestAnswer
	for _, req := range requests {
		switch {
		case container == "":
			answers = append(answers, RequestAnswer{Request: req.Request, Answer: AnswerSkip,
				Why: "The stand-in planner files no issue: the app has no Container to file it for."})
		case len(*issues) >= MaxPlanIssues:
			answers = append(answers, RequestAnswer{Request: req.Request, Answer: AnswerSkip,
				Why: fmt.Sprintf("The round files %d issues already, as many as one round may.", MaxPlanIssues)})
		default:
			goal := oneParagraph(req.Body)
			if goal == "" {
				goal = oneParagraph(req.Title)
			}
			*issues = append(*issues, [2]string{shortTitle(req.Title), goal})
			answers = append(answers, RequestAnswer{Request: req.Request, Answer: AnswerTake, Issues: []int{len(*issues)}})
		}
	}
	return answers, nil
}

// standInCatalog picks the catalog's first Framework and type a Deployment
// ships: a Framework that doesn't deploy itself, its first type a Deployment
// supports and a Standard fits (gitea-release builds with the Standard's
// make dist), the first such Deployment and Standard. With none, it picks the
// first Framework that has a Container type, its first type, the first
// Standard that fits both, if any, and no Deployment.
func standInCatalog(catalog catalogOut) (framework, kind, standard, deployment string) {
	for _, f := range catalog.Frameworks {
		if f.DeploysItself {
			continue
		}
		for _, t := range f.SupportedTypes {
			standard := standInStandard(catalog, f.Name, t)
			if standard == "" {
				continue
			}
			for _, d := range catalog.Deployments {
				if slices.Contains(d.SupportedTypes, t) {
					return f.Name, t, standard, d.Name
				}
			}
		}
	}
	for _, f := range catalog.Frameworks {
		if len(f.SupportedTypes) > 0 {
			kind = f.SupportedTypes[0]
			return f.Name, kind, standInStandard(catalog, f.Name, kind), ""
		}
	}
	return "", "", "", ""
}

// standInStandard is the catalog's first Standard that fits framework and
// kind, or none.
func standInStandard(catalog catalogOut, framework, kind string) string {
	for _, s := range catalog.Standards {
		if slices.Contains(s.SupportedFrameworks, framework) && slices.Contains(s.SupportedTypes, kind) {
			return s.Name
		}
	}
	return ""
}

// standInContainer is the Container named base, the Brief's, else the model's
// first, else none.
func standInContainer(objects []PlanObject, base string) string {
	first := ""
	for _, o := range objects {
		if o.Kind != KindContainer {
			continue
		}
		if o.Name == base {
			return o.Name
		}
		if first == "" {
			first = o.Name
		}
	}
	return first
}

// standInName is base, or base-2, base-3, ... when a Container has that name.
func standInName(objects []PlanObject, base string) string {
	name := base
	for n := 2; slices.ContainsFunc(objects, func(o PlanObject) bool {
		return o.Kind == KindContainer && o.Name == name
	}); n++ {
		name = fmt.Sprintf("%s-%d", base, n)
	}
	return name
}

// shortTitle is s on one line, cut at a word to standInTitleRunes, naming no
// path under AgentNeverPaths, so the plan check takes it.
func shortTitle(s string) string {
	s = oneParagraph(withoutNeverPaths(s))
	if r := []rune(s); len(r) > standInTitleRunes {
		cut := string(r[:standInTitleRunes])
		if i := strings.LastIndex(cut, " "); i > 0 {
			cut = cut[:i]
		}
		return strings.TrimRight(cut, " ,.;:") + "…"
	}
	return s
}
