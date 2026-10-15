package model

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

// How a round takes requests (docs/plans/done/brief-requests.md, decisions 7 and
// 8): the requests it takes, what the planner reads of them, and its answers.

// WorkspaceRequestsFile is where the planner reads the requests its round
// took, and PlanRequestsFile where it answers each.
const (
	WorkspaceRequestsFile = "model/requests.yaml"
	PlanRequestsFile      = "plan/requests.yaml"
)

// TakenRequests are the requests a new round takes: every one let through
// that no round took yet, in the order the Brief lists them.
func TakenRequests(requests []Request) []Request {
	var out []Request
	for _, r := range requests {
		if r.Stage == RequestAccepted && r.Round == 0 {
			out = append(out, r)
		}
	}
	return out
}

// NewRequests counts the requests that start a round: those a new round
// takes, other than the ones the last round took and left unplanned, when it
// failed or asked for input. Those wait for a new prompt or request, as the
// prompts of such a round do, so a failing round doesn't start again and again.
func NewRequests(requests []Request, lastRound []string) int {
	n := 0
	for _, r := range TakenRequests(requests) {
		if !slices.Contains(lastRound, RequestName(r.Repository, r.Number)) {
			n++
		}
	}
	return n
}

// WorkspaceRequests are the requests model/requests.yaml shows the planner:
// while a round runs, the ones it took that are still open; between rounds,
// the ones the next round takes.
func WorkspaceRequests(requests []Request, roundRequests []string, roundRuns bool) []Request {
	if !roundRuns {
		return TakenRequests(requests)
	}
	var out []Request
	for _, r := range requests {
		if slices.Contains(roundRequests, RequestName(r.Repository, r.Number)) {
			out = append(out, r)
		}
	}
	return out
}

// requestOut is one request in model/requests.yaml.
type requestOut struct {
	Request    string `yaml:"request"`
	Repository string `yaml:"repository"`
	Number     int    `yaml:"number"`
	Author     string `yaml:"author"`
	Title      string `yaml:"title"`
	Body       string `yaml:"body"`
}

// workspaceRequests renders model/requests.yaml: the round's requests, in
// the order the Brief lists them.
func workspaceRequests(requests []Request) []requestOut {
	out := make([]requestOut, 0, len(requests))
	for _, r := range requests {
		out = append(out, requestOut{RequestName(r.Repository, r.Number), r.Repository, r.Number, r.Author,
			r.Title, strings.TrimSpace(r.Body)})
	}
	return out
}

// RequestAnswer is a plan's answer to one request its round took.
type RequestAnswer struct {
	// Request is owner/name#number, as model/requests.yaml names it.
	Request string `yaml:"request"`
	Answer  string `yaml:"answer"`
	// Issues are the plan's numbers (N) of the issues a taken request became.
	Issues []int `yaml:"issues,omitempty"`
	// Why says why a request is skipped; it goes on GitHub as written.
	Why string `yaml:"why,omitempty"`
}

// parseRequestAnswers reads plan/requests.yaml: a list of answers.
func parseRequestAnswers(s string) ([]RequestAnswer, error) {
	var answers []RequestAnswer
	dec := yaml.NewDecoder(strings.NewReader(s))
	dec.KnownFields(true)
	if err := dec.Decode(&answers); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("want a list of {request, answer, issues, why}: %w", err)
	}
	if err := checkCutText(s); err != nil {
		return nil, err
	}
	return answers, nil
}

// CheckRequestAnswers returns every reason a plan's answers to its round's
// requests can't be applied: each request the round took and that is still
// open is answered once, a request the round didn't take is not, a take
// names the plan's issues it became, and a skip says why. took are the
// requests the round took, open the ones of them still open on GitHub.
func CheckRequestAnswers(answers []RequestAnswer, took, open []string, issues []PlanIssue) []Refusal {
	var out []Refusal
	refuse := func(format string, args ...any) {
		out = append(out, Refusal{PlanRequestsFile, fmt.Sprintf(format, args...)})
	}
	seen := map[string]bool{}
	for _, a := range answers {
		subject := a.Request
		switch {
		case seen[subject]:
			refuse("answers %s twice; answer each request once", subject)
			continue
		case !slices.Contains(took, subject):
			refuse("answers %s, which this round didn't take; answer only the requests %s lists",
				subject, WorkspaceRequestsFile)
			continue
		}
		seen[subject] = true
		switch a.Answer {
		case AnswerTake:
			if len(a.Issues) == 0 {
				refuse("takes %s with no issue: name the plan's issues it became in issues:, or skip it "+
					"saying why", subject)
			}
			for _, n := range a.Issues {
				if !slices.ContainsFunc(issues, func(i PlanIssue) bool { return i.N == n }) {
					refuse("takes %s as issue %d, which the plan doesn't file; name the numbers of %s",
						subject, n, planIssuesDir)
				}
			}
		case AnswerSkip:
			if len(a.Issues) > 0 {
				refuse("skips %s with issues; only a take names the issues a request became", subject)
			}
			if strings.TrimSpace(a.Why) == "" {
				refuse("skips %s with no why: say in plain words why, such as already there, out of scope "+
					"or unclear; it is posted on the request", subject)
			}
		default:
			refuse("answers %s %q; an answer is %s or %s", subject, a.Answer, AnswerTake, AnswerSkip)
		}
		if len(a.Why) > MaxAnswerWhy {
			refuse("answers %s with a why of %d bytes; keep it under %d", subject, len(a.Why), MaxAnswerWhy)
		}
	}
	for _, name := range open {
		if !seen[name] {
			refuse("leaves %s unanswered: take it with the issues it became, or skip it saying why", name)
		}
	}
	return out
}

// RequestIssues are the issues a taken request became, as status.requests
// names them, container#number, given the numbers the round's issues were
// filed as by their N; an issue not filed is left out.
func RequestIssues(answer RequestAnswer, issues []PlanIssue, filed map[int]int) []string {
	var out []string
	for _, n := range answer.Issues {
		i := slices.IndexFunc(issues, func(p PlanIssue) bool { return p.N == n })
		number, ok := filed[n]
		if i < 0 || !ok {
			continue
		}
		if name := fmt.Sprintf("%s#%d", issues[i].Container, number); !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	return out
}
