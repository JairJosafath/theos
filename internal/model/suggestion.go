package model

import (
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

// A reviewer's suggestion is a follow-up a reviewer filed (FollowUpIssue) that
// still waits, proposed, for someone to take or skip it. For a Brief's apps the
// person answers it in a prompt, and the operator does the rest (#375).

// Answers to a suggestion.
const (
	AnswerTake = "take"
	AnswerSkip = "skip"
)

// MaxSuggestions is how many waiting suggestions a Brief lists in its status,
// MaxWorkspaceSuggestions how many its workspace lists, and
// MaxSuggestionAnswers how many a plan answers (#1019).
const (
	MaxSuggestions          = 50
	MaxWorkspaceSuggestions = 500
	MaxSuggestionAnswers    = 100
)

// PlanSuggestionsFile is where a plan answers the suggestions its round's
// prompts speak to, and WorkspaceSuggestionsFile where the planner reads them.
const (
	PlanSuggestionsFile      = "plan/suggestions.yaml"
	WorkspaceSuggestionsFile = "model/suggestions.yaml"
)

// suggestedBy is the line FollowUpIssue ends a follow-up with.
var suggestedBy = regexp.MustCompile("(?m)^Suggested by .+ while reviewing .+ for #\\d+ \\(Task `[^`]+`\\)\\.")

// IsSuggestion reports whether an open issue is a reviewer's suggestion that
// waits for an answer: labelled proposed, its body ending as FollowUpIssue
// ends it.
func IsSuggestion(labels []string, body string) bool {
	return slices.Contains(labels, LabelProposed) && suggestedBy.MatchString(body)
}

// Suggestion is a reviewer's suggestion waiting in a Container's repository.
type Suggestion struct {
	Container string
	// Repository is the repository's name on the Git host, as a prompt
	// writes it.
	Repository string
	Number     int
	Title      string
	URL        string
	// Criteria says its body has an "Acceptance criteria" checklist, so it can
	// be taken as it stands.
	Criteria bool
}

// SuggestionAnswer is a plan's answer to one suggestion.
type SuggestionAnswer struct {
	Container string `yaml:"container"`
	Issue     int    `yaml:"issue"`
	Answer    string `yaml:"answer"`
	// Why says, in plain words, which prompt asked for it and how.
	Why string `yaml:"why"`
	// Fix says a taken suggestion fixes a bug or adds or fixes a test, so it
	// goes into the patch release after the release in progress (F5 of
	// plans/done/fixes-patch-next.md).
	Fix bool `yaml:"fix,omitempty"`
}

// MaxAnswerWhy is how long an answer's why may be.
const MaxAnswerWhy = 500

// parseAnswers reads plan/suggestions.yaml: a list of answers.
func parseAnswers(s string) ([]SuggestionAnswer, error) {
	var answers []SuggestionAnswer
	dec := yaml.NewDecoder(strings.NewReader(s))
	dec.KnownFields(true)
	if err := dec.Decode(&answers); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("want a list of {container, issue, answer, why, fix}: %w", err)
	}
	if err := checkCutText(s); err != nil {
		return nil, err
	}
	return answers, nil
}

// CheckAnswers returns every reason a plan's answers can't be applied: at
// most MaxSuggestionAnswers, each answers a waiting suggestion once, takes or
// skips it and says why, takes only one that has acceptance criteria, and
// marks only one it takes a fix. waiting holds every suggestion an answer
// names that waits, wherever the operator read it.
func CheckAnswers(answers []SuggestionAnswer, waiting []Suggestion) []Refusal {
	var out []Refusal
	refuse := func(format string, args ...any) {
		out = append(out, Refusal{PlanSuggestionsFile, fmt.Sprintf(format, args...)})
	}
	if len(answers) > MaxSuggestionAnswers {
		refuse("answers %d suggestions; answer at most %d in a round", len(answers), MaxSuggestionAnswers)
		return out
	}
	seen := map[string]bool{}
	for _, a := range answers {
		subject := fmt.Sprintf("%s#%d", a.Container, a.Issue)
		i := slices.IndexFunc(waiting, func(s Suggestion) bool { return s.Container == a.Container && s.Number == a.Issue })
		switch {
		case seen[subject]:
			refuse("answers %s twice; answer each suggestion once", subject)
			continue
		case i < 0:
			refuse("answers %s, which is not a waiting suggestion; answer only those %s lists%s",
				subject, WorkspaceSuggestionsFile, waitsElsewhere(waiting, a.Issue))
			continue
		}
		seen[subject] = true
		switch a.Answer {
		case AnswerTake:
			if !waiting[i].Criteria {
				refuse("takes %s, which has no acceptance criteria: file it again as an issue with them, "+
					"and skip it saying so", subject)
			}
		case AnswerSkip:
			if a.Fix {
				refuse("skips %s as a fix; only a suggestion taken is a fix", subject)
			}
		default:
			refuse("answers %s %q; an answer is %s or %s", subject, a.Answer, AnswerTake, AnswerSkip)
		}
		if strings.TrimSpace(a.Why) == "" {
			refuse("answers %s with no why: say in plain words which prompt asked for it", subject)
		} else if len(a.Why) > MaxAnswerWhy {
			refuse("answers %s with a why of %d bytes; keep it under %d", subject, len(a.Why), MaxAnswerWhy)
		}
	}
	return out
}

// waitsElsewhere names the Containers where suggestion number waits, for a
// refusal of an answer that named another Container.
func waitsElsewhere(waiting []Suggestion, number int) string {
	var at []string
	for _, s := range waiting {
		if s.Number == number && !slices.Contains(at, s.Container) {
			at = append(at, s.Container)
		}
	}
	if len(at) == 0 {
		return ""
	}
	slices.Sort(at)
	return fmt.Sprintf(" (#%d waits in Container %s: a suggestion is its Container and number)", number,
		strings.Join(at, ", "))
}

// SuggestionComment is what the operator writes on a suggestion it answers
// for Brief brief in round: the answer, the planner's why and the round's
// prompts, quoted.
func SuggestionComment(a SuggestionAnswer, brief string, round int, prompts []string) string {
	var b strings.Builder
	if a.Answer == AnswerTake {
		fmt.Fprintf(&b, "Taken for Brief `%s` in round %d: the Crew picks it up as %s.", brief, round, LabelReady)
	} else {
		fmt.Fprintf(&b, "Closed for Brief `%s` in round %d: not taken.", brief, round)
	}
	fmt.Fprintf(&b, "\n\nThe planner: %s", oneParagraph(a.Why))
	if len(prompts) > 0 {
		b.WriteString("\n\nThe round's prompts:\n")
		for _, p := range prompts {
			fmt.Fprintf(&b, "\n> %s\n", oneParagraph(p))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

type suggestionOut struct {
	Container  string `yaml:"container"`
	Repository string `yaml:"repository"`
	Issue      int    `yaml:"issue"`
	Title      string `yaml:"title"`
	URL        string `yaml:"url"`
	Criteria   bool   `yaml:"criteria"`
}

// workspaceSuggestions renders model/suggestions.yaml: the first
// MaxWorkspaceSuggestions waiting suggestions, by Container and number.
func workspaceSuggestions(suggestions []Suggestion) []suggestionOut {
	sorted := slices.Clone(suggestions)
	slices.SortFunc(sorted, func(a, b Suggestion) int {
		if c := strings.Compare(a.Container, b.Container); c != 0 {
			return c
		}
		return a.Number - b.Number
	})
	sorted = sorted[:min(len(sorted), MaxWorkspaceSuggestions)]
	out := make([]suggestionOut, 0, len(sorted))
	for _, s := range sorted {
		out = append(out, suggestionOut{s.Container, s.Repository, s.Number, oneParagraph(s.Title), s.URL,
			s.Criteria})
	}
	return out
}
