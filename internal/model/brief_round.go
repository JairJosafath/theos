package model

import (
	"slices"
	"strings"
	"unicode/utf8"
)

// A Brief's phases (B13), as its status.phase.
const (
	BriefPending    = "Pending"
	BriefPlanning   = "Planning"
	BriefApplying   = "Applying"
	BriefPlanned    = "Planned"
	BriefNeedsInput = "NeedsInput"
	BriefFailed     = "Failed"
)

// How a Brief's round ended, the reason of its Planned condition.
const (
	ReasonApplied           = "Applied"
	ReasonPlannerNeedsInput = "PlannerNeedsInput"
	ReasonRoundFailed       = "RoundFailed"
)

// The Plan Task phases a Brief's round reads.
const (
	planTaskMerged = "Merged"
	planTaskFailed = "Failed"
)

// BriefRoundFacts is what a Brief's next round and its phase depend on.
type BriefRoundFacts struct {
	// Prompts is how many prompts the Brief has.
	Prompts int
	// Round is the round running now or last run; 0 before the first.
	Round int
	// RoundPrompts is how many prompts that round took.
	RoundPrompts int
	// RoundOver says the round is in the history: it applied, failed or asked
	// for input.
	RoundOver bool
	// TaskPhase is the phase of the round's Plan Task; "" when it has none yet.
	TaskPhase string
	// Outcome is the reason of the Planned condition: how the last round ended.
	Outcome string
	// OperatorNeedsInput says the operator waits for the person to make
	// something: an Agent, the GitProvider, its Secret or a catalog entry.
	OperatorNeedsInput bool
}

// StartsRound reports whether the Brief starts its next round: none ran yet,
// or the last one is over and prompts were appended after the ones it took
// (B12). A prompt appended during a round waits for the next one.
func StartsRound(f BriefRoundFacts) bool {
	if f.Round == 0 {
		return true
	}
	return f.RoundOver && f.Prompts > f.RoundPrompts
}

// BriefPhaseOf derives a Brief's phase from its round (B13). The operator's
// own NeedsInput shows over any phase while it lasts.
func BriefPhaseOf(f BriefRoundFacts) string {
	switch {
	case f.OperatorNeedsInput:
		return BriefNeedsInput
	case f.Round == 0:
		return BriefPending
	case !f.RoundOver && f.TaskPhase == planTaskMerged:
		return BriefApplying
	case !f.RoundOver && f.TaskPhase == planTaskFailed:
		return BriefFailed
	case !f.RoundOver:
		return BriefPlanning
	case f.Outcome == ReasonPlannerNeedsInput:
		return BriefNeedsInput
	case f.Outcome == ReasonRoundFailed:
		return BriefFailed
	}
	return BriefPlanned
}

// BriefIssueLabels are the labels a plan's issue is filed with: the planner's,
// with every status/* replaced by status/ready, each once (B11).
func BriefIssueLabels(labels []string) []string {
	out := []string{LabelReady}
	for _, l := range labels {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "status/") || slices.Contains(out, l) {
			continue
		}
		out = append(out, l)
	}
	return out
}

// applyOrder is the order a plan is applied in (B8): Relationships before the
// Containers they start from, so each Connector is in the Container's
// scaffold, not added after it.
var applyOrder = []string{KindProject, KindPerson, KindSoftwareSystem, KindDataStore, KindRelationship, KindContainer}

// ApplyOrder returns the plan's objects in the order they are applied, keeping
// the plan's order within a kind.
func ApplyOrder(objects []PlanObject) []PlanObject {
	out := slices.Clone(objects)
	slices.SortStableFunc(out, func(a, b PlanObject) int {
		return slices.Index(applyOrder, a.Kind) - slices.Index(applyOrder, b.Kind)
	})
	return out
}

// RoundContainers are the Containers a round waits for before it is done: the
// ones it created, which get a Crew, and every Container one of its issues is
// for. Each is named once, the created ones first.
func RoundContainers(created []string, issues []PlanIssue) []string {
	out := []string{}
	for _, name := range created {
		if !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	for _, issue := range issues {
		if !slices.Contains(out, issue.Container) {
			out = append(out, issue.Container)
		}
	}
	return out
}

// IssuesFor returns the plan's issues for a Container, in the plan's order.
func IssuesFor(issues []PlanIssue, container string) []PlanIssue {
	var out []PlanIssue
	for _, issue := range issues {
		if issue.Container == container {
			out = append(out, issue)
		}
	}
	return out
}

// MissingCatalog names the catalog entries the objects a plan creates refer to
// and the namespace doesn't have, as "Side name", each once: a plan checked
// against the catalog can't be applied when an entry went since.
func MissingCatalog(plan Plan, current CurrentModel) []string {
	var out []string
	need := func(side, name string) {
		if name == "" {
			return
		}
		for _, e := range current.Catalog {
			if e.Side == side && e.Name == name {
				return
			}
		}
		if missing := side + " " + name; !slices.Contains(out, missing) {
			out = append(out, missing)
		}
	}
	for _, o := range plan.Objects {
		if _, exists := findObject(current.Objects, o.Ref()); exists {
			continue
		}
		switch o.Kind {
		case KindContainer:
			need(SideFramework, o.Framework)
			need(SideStandard, o.Standard)
			need(SideCompute, o.Compute)
			need(SideDeployment, o.Deployment)
		case KindDataStore:
			need(SideConnector, o.Connector)
		}
	}
	return out
}

// HasFramework reports whether the catalog has a Framework, without which no
// plan can make a Container.
func HasFramework(catalog []CatalogEntry) bool {
	return slices.ContainsFunc(catalog, func(e CatalogEntry) bool { return e.Side == SideFramework })
}

// RefusalText is the refusals of a plan as the planner's feedback, one per line.
func RefusalText(refusals []Refusal) string {
	lines := make([]string, 0, len(refusals))
	for _, r := range refusals {
		lines = append(lines, r.String())
	}
	return strings.Join(lines, "\n")
}

// KeepNewest returns the last n of items.
func KeepNewest[T any](items []T, n int) []T {
	if len(items) <= n {
		return items
	}
	return slices.Clone(items[len(items)-n:])
}

// Clip cuts s to at most n bytes, on a character boundary.
func Clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// ClipList keeps the first max items of list, each clipped to n bytes.
func ClipList(list []string, maxItems, n int) []string {
	if len(list) > maxItems {
		list = list[:maxItems]
	}
	out := make([]string, 0, len(list))
	for _, s := range list {
		out = append(out, Clip(s, n))
	}
	return out
}

// WorkspacePrompts are the prompts brief.md shows the planner: all of them
// between rounds, and while a round runs only the ones it took, so a prompt
// appended meanwhile, which waits for the next round, isn't planned twice.
func WorkspacePrompts(prompts []string, roundPrompts int, roundRuns bool) []string {
	if roundRuns && roundPrompts >= 0 && roundPrompts < len(prompts) {
		return prompts[:roundPrompts]
	}
	return prompts
}

// IdeaPlanned reports whether a round of the Brief applied, given the
// outcomes of the rounds in its history and how many prompts the last applied
// round took. A round that failed or asked for input planned nothing.
func IdeaPlanned(outcomes []string, promptsPlanned int) bool {
	return promptsPlanned > 0 || slices.Contains(outcomes, ReasonApplied)
}
