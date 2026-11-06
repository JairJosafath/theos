package model

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"time"
)

// Self-improvement (docs/plans/done/measure-and-improve.md, step 7): a Scorer
// criterion that keeps failing for one configuration files one issue that
// proposes the fix, status/proposed, which the normal loop then decides. It
// files none while one is open, found by the marker in its body.

// NotRecordedConfig is the configuration key of runs that recorded none, as
// the UI names it.
const NotRecordedConfig = "not recorded"

// ConfigKey is a configuration's key, as the UI shows it: "<harness> · <model>",
// "default model" for none.
func ConfigKey(harness, model string) string {
	if model == "" {
		model = "default model"
	}
	return harness + " · " + model
}

// LabelImprovement marks an issue a Scorer filed about how the factory works.
const LabelImprovement = "kind/improvement"

// ImprovementLabel is LabelImprovement as the operator makes it.
func ImprovementLabel() IssueLabel {
	return IssueLabel{LabelImprovement, "#006b75",
		"The factory proposes a change to how it works, from a Scorer's failures", false}
}

// ScoreRecord is one finished Score as self-improvement reads it.
type ScoreRecord struct {
	// Task is the judged Task, Container its Container, PullURL its pull
	// request.
	Task, Container, PullURL string
	// Config is the judged implementer's configuration key.
	Config    string
	Completed time.Time
	Verdicts  []ScoreCriterion
}

// ScoreFailure is one failing verdict an improvement names.
type ScoreFailure struct {
	Task, Container, PullURL, Reason string
}

// Improvement is a criterion that failed often enough for one configuration
// to propose a change.
type Improvement struct {
	Criterion string
	Config    string
	// Window is how many Scores of the configuration it looked at.
	Window   int
	Failures []ScoreFailure
}

// ImprovementsDue are the criteria that failed at least failures times among
// the newest window Scores of one configuration, one per criterion and
// configuration, in the criteria's order, then by configuration.
func ImprovementsDue(criteria []string, scores []ScoreRecord, failures, window int) []Improvement {
	byConfig := map[string][]ScoreRecord{}
	for _, s := range scores {
		byConfig[s.Config] = append(byConfig[s.Config], s)
	}
	configs := make([]string, 0, len(byConfig))
	for c := range byConfig {
		configs = append(configs, c)
	}
	slices.Sort(configs)
	var out []Improvement
	for i, criterion := range criteria {
		for _, config := range configs {
			newest := slices.Clone(byConfig[config])
			slices.SortFunc(newest, func(a, b ScoreRecord) int { return b.Completed.Compare(a.Completed) })
			newest = newest[:min(len(newest), window)]
			imp := Improvement{Criterion: criterion, Config: config, Window: len(newest)}
			for _, s := range newest {
				if i < len(s.Verdicts) && s.Verdicts[i].Verdict == ScoreFail {
					imp.Failures = append(imp.Failures, ScoreFailure{Task: s.Task, Container: s.Container,
						PullURL: s.PullURL, Reason: s.Verdicts[i].Reason})
				}
			}
			if failures > 0 && len(imp.Failures) >= failures {
				out = append(out, imp)
			}
		}
	}
	return out
}

// improvementMarker is the line that ties an issue to its Scorer, criterion
// and configuration.
func improvementMarker(scorer, criterion, config string) string {
	sum := sha256.Sum256([]byte(criterion + "\x00" + config))
	return fmt.Sprintf("<!-- theos-improvement scorer=%s key=%s -->", scorer, hex.EncodeToString(sum[:8]))
}

// HasImprovementMarker reports whether body is the issue Scorer scorer filed
// about criterion under config.
func HasImprovementMarker(body, scorer, criterion, config string) bool {
	return strings.Contains(body, improvementMarker(scorer, criterion, config))
}

// ImprovementIssue is the issue Scorer scorer files about imp: what keeps
// failing, under which configuration, each failure's why and link, and the
// change it proposes, which the normal loop decides.
func ImprovementIssue(scorer string, imp Improvement) (title, body string) {
	title = clip(fmt.Sprintf("Scorer %s: %q keeps failing (%s)", scorer, imp.Criterion, imp.Config), 200)
	var b strings.Builder
	fmt.Fprintf(&b, "Scorer `%s` found this criterion failed in %d of the newest %d judged changes whose implementer "+
		"ran on `%s`:\n\n> %s\n\n", scorer, len(imp.Failures), imp.Window, imp.Config, imp.Criterion)
	b.WriteString("Why each failed, in the judge's words:\n\n")
	for _, f := range imp.Failures {
		link := ""
		if f.PullURL != "" {
			link = fmt.Sprintf(" ([pull request](%s))", f.PullURL)
		}
		fmt.Fprintf(&b, "- Task `%s` on `%s`%s: %s\n", f.Task, f.Container, link, orUnknown(strings.TrimSpace(f.Reason)))
	}
	b.WriteString("\n## Proposal\n\nTell the agents what this criterion asks, where they read it: a line in " +
		"`docs/project-notes.md`, the project's own word to its agents, or, when every project's agents need it, " +
		"a change to the Standard's agent guides (`docs/agents/`), which reaches this repository as a Standard " +
		"update. Triage decides whether and how; nothing changes until a change merges through the loop.\n\n")
	b.WriteString("## Acceptance criteria\n\n- [ ] The agents are told what the criterion asks, where they read it.\n\n")
	fmt.Fprintf(&b, "Filed by Scorer `%s` (docs/plans/done/measure-and-improve.md); it files no other while this one is "+
		"open.\n\n%s\n", scorer, improvementMarker(scorer, imp.Criterion, imp.Config))
	return title, b.String()
}
