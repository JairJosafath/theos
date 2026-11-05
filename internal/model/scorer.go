package model

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
)

// Scorers (docs/plans/measure-and-improve.md, step 4): plain-words criteria a
// judge Agent checks finished Tasks against. For each finished Task a Scorer
// selects and samples, the operator makes a Task of kind Score, one read-only
// judge run on the finished change, whose verdicts are its status.score.
// Scores advise: they change nothing about the Task they judge.

// RoleScorer is a Score Task's run: the judge checks a finished change against
// a Scorer's criteria.
const RoleScorer = "score"

// The verdicts of a judge on a criterion.
const (
	ScorePass    = "Pass"
	ScoreFail    = "Fail"
	ScoreUnclear = "Unclear"
)

// ScoreCriterion is a judge's verdict on one criterion, and why.
type ScoreCriterion struct {
	Criterion string `json:"criterion"`
	Verdict   string `json:"verdict"`
	Reason    string `json:"reason,omitempty"`
}

// The bounds a Score keeps: a reason's characters, and the criteria.
const (
	maxScoreReason   = 1000
	MaxScoreCriteria = 20
)

// ScorerSelection is which finished Tasks a Scorer judges; empty lists mean
// Implement Tasks, of every Container, that merged.
type ScorerSelection struct {
	Kinds      []string
	Containers []string
	Phases     []string
}

// ScoredTask is a Task as a Scorer selects it.
type ScoredTask struct {
	Name, Kind, Container, Phase string
	// Completed is when it finished; zero while it hasn't.
	Completed time.Time
}

// ScorerSelects reports whether a Scorer made at created, of selection s,
// judges t: a finished Task of a kind, Container and outcome it selects that
// finished after the Scorer was made. A Score Task is never judged; nor is the
// history from before the Scorer, which a Benchmark compares instead.
func ScorerSelects(s ScorerSelection, created time.Time, t ScoredTask) bool {
	kind := orImplement(t.Kind)
	kinds, phases := s.Kinds, s.Phases
	if len(kinds) == 0 {
		kinds = []string{chatKindImplement}
	}
	if len(phases) == 0 {
		phases = []string{TaskMerged}
	}
	return kind != "Score" && slices.Contains(kinds, kind) && slices.Contains(phases, t.Phase) &&
		(len(s.Containers) == 0 || slices.Contains(s.Containers, t.Container)) &&
		!t.Completed.IsZero() && !t.Completed.Before(created)
}

// ScoreSampled reports whether the Scorer of UID scorer judges Task task, at
// percent: a hash of the two falls under it, so a restart or a second pass
// picks the same Tasks.
func ScoreSampled(scorer, task string, percent int) bool {
	if percent <= 0 {
		return false
	}
	if percent >= 100 {
		return true
	}
	sum := sha256.Sum256([]byte(scorer + "/" + task))
	return int(binary.BigEndian.Uint64(sum[:8])%100) < percent
}

// ScoreTaskName is the name of the Score Task in which Scorer scorer judges
// Task task: one per pair, so a second create fails. A long one is cut and
// told apart by a hash of the whole, leaving room in its sandbox's name.
func ScoreTaskName(scorer, task string) string {
	const maxName = 40
	name := scorer + "-" + task
	if len(name) <= maxName {
		return name
	}
	sum := sha256.Sum256([]byte(name))
	return strings.TrimRight(name[:maxName-9], "-") + "-" + hex.EncodeToString(sum[:])[:8]
}

// ScoreEvidence is what a judge is told of the Task it judges, besides the
// change itself, which it reads in the repository.
type ScoreEvidence struct {
	Task, Kind, Container, Phase string
	Issue                        int
	IssueTitle                   string
	Rounds                       int
	PullRequest                  int
	// Commit is the merge commit on the default branch; empty for a Task
	// that didn't merge.
	Commit, Branch                string
	ReviewVerdict, ReviewSummary  string
	Summary, Checks, FailedReason string
	PersonCommits                 *int
}

// ScoreGoal is a Score Task's goal: the evidence of the Task it judges, which
// the judge reads as data. The criteria go to the judge apart from it.
func ScoreGoal(e ScoreEvidence) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Task %s (%s) on Container %s ended %s after %d round(s).\n", e.Task, orImplement(e.Kind),
		e.Container, e.Phase, max(e.Rounds, 1))
	if e.Issue > 0 {
		fmt.Fprintf(&b, "Its issue: #%d %s\n", e.Issue, strings.TrimSpace(e.IssueTitle))
	}
	switch {
	case e.Commit != "":
		fmt.Fprintf(&b, "Its change merged as commit %s on the default branch (pull request #%d): "+
			"read it with git show %s.\n", e.Commit, e.PullRequest, e.Commit)
	case e.Branch != "":
		fmt.Fprintf(&b, "It merged nothing; its last commit was on branch %s (pull request #%d), which may be gone.\n",
			e.Branch, e.PullRequest)
	}
	for _, line := range [][2]string{
		{"The implementer's summary", e.Summary},
		{"The checks", e.Checks},
		{"The reviewer's verdict", strings.TrimSpace(e.ReviewVerdict + ": " + e.ReviewSummary)},
		{"Why it failed", e.FailedReason},
	} {
		if v := strings.Trim(strings.TrimSpace(line[1]), ":"); v != "" {
			fmt.Fprintf(&b, "%s: %s\n", line[0], strings.TrimSpace(v))
		}
	}
	if e.PersonCommits != nil {
		fmt.Fprintf(&b, "Commits a person pushed to it: %d.\n", *e.PersonCommits)
	}
	return strings.TrimSpace(b.String())
}

func orImplement(kind string) string {
	if kind == "" {
		return chatKindImplement
	}
	return kind
}

// ParseScore reads a judge's verdicts on criteria from the last JSON object
// in its output that holds them: {"verdicts": [{"criterion": <1-based
// number>, "verdict": "pass", "fail" or "unclear", "reason": "..."}]}. Every
// criterion gets one verdict, in the criteria's order: one the judge said
// nothing of, or gave a verdict it can't read for, is Unclear and says so.
// It fails when the output holds no such object.
func ParseScore(output string, criteria []string) ([]ScoreCriterion, error) {
	type verdict struct {
		Criterion int    `json:"criterion"`
		Verdict   string `json:"verdict"`
		Reason    string `json:"reason"`
	}
	for end := strings.LastIndex(output, "}"); end >= 0; end = strings.LastIndex(output[:end], "}") {
		for start := strings.LastIndex(output[:end], "{"); start >= 0; start = strings.LastIndex(output[:start], "{") {
			var got struct {
				Verdicts []verdict `json:"verdicts"`
			}
			if json.Unmarshal([]byte(output[start:end+1]), &got) != nil || got.Verdicts == nil {
				continue
			}
			out := UnclearScore(criteria, "the judge said nothing of it")
			for _, v := range got.Verdicts {
				i := v.Criterion - 1
				if i < 0 || i >= len(out) {
					continue
				}
				switch strings.ToLower(strings.TrimSpace(v.Verdict)) {
				case "pass":
					out[i].Verdict = ScorePass
				case "fail":
					out[i].Verdict = ScoreFail
				default:
					out[i].Verdict = ScoreUnclear
				}
				out[i].Reason = clip(strings.Join(strings.Fields(v.Reason), " "), maxScoreReason)
			}
			return out, nil
		}
	}
	return nil, fmt.Errorf("the judge gave no JSON verdicts")
}

// UnclearScore is every criterion Unclear, for reason: a judge run that failed.
func UnclearScore(criteria []string, reason string) []ScoreCriterion {
	out := make([]ScoreCriterion, 0, min(len(criteria), MaxScoreCriteria))
	for _, c := range criteria[:min(len(criteria), MaxScoreCriteria)] {
		out = append(out, ScoreCriterion{Criterion: c, Verdict: ScoreUnclear, Reason: clip(reason, maxScoreReason)})
	}
	return out
}

// StandInScore is the stand-in judge's verdicts: every criterion passes.
func StandInScore(criteria []string) string {
	type verdict struct {
		Criterion int    `json:"criterion"`
		Verdict   string `json:"verdict"`
		Reason    string `json:"reason"`
	}
	out := struct {
		Verdicts []verdict `json:"verdicts"`
	}{Verdicts: []verdict{}}
	for i := range criteria {
		out.Verdicts = append(out.Verdicts, verdict{Criterion: i + 1, Verdict: "pass", Reason: "Stand-in judge."})
	}
	b, _ := json.Marshal(out)
	return string(b)
}

// ScoreCount is how Scores judged one criterion.
type ScoreCount struct {
	Criterion           string
	Pass, Fail, Unclear int
}

// TallyScores counts the verdicts of scores on each of criteria, by the
// criterion's place; a Score of other criteria (another generation) is left
// to its caller.
func TallyScores(criteria []string, scores [][]ScoreCriterion) []ScoreCount {
	out := make([]ScoreCount, len(criteria))
	for i, c := range criteria {
		out[i].Criterion = c
	}
	for _, s := range scores {
		for i, v := range s {
			if i >= len(out) {
				break
			}
			switch v.Verdict {
			case ScorePass:
				out[i].Pass++
			case ScoreFail:
				out[i].Fail++
			default:
				out[i].Unclear++
			}
		}
	}
	return out
}

// The reasons a Scorer's Ready condition gives.
const (
	ScorerReady           = "Judging"
	ScorerJudgeNotFound   = "JudgeNotFound"
	ScorerPaused          = "Paused"
	ScorerDailyCapReached = "DailyCapReached"
	ScorerBudgetReached   = "BudgetReached"
)

// ScorerDay is a UTC day, YYYY-MM-DD.
func ScorerDay(t time.Time) string { return t.UTC().Format(time.DateOnly) }

// ScorerLimit is why a Scorer starts no judge run now, given what its runs of
// today started and cost: a reason of its Ready condition and the message,
// or "" when it may start one.
func ScorerLimit(startedToday, maxPerDay int, costTodayMilliUSD int64, budgetUSD int) (reason, message string) {
	switch {
	case maxPerDay > 0 && startedToday >= maxPerDay:
		return ScorerDailyCapReached, fmt.Sprintf("Started %d judge runs today, its maxPerDay; it goes on tomorrow (UTC)",
			startedToday)
	case budgetUSD > 0 && costTodayMilliUSD >= int64(budgetUSD)*1000:
		return ScorerBudgetReached, fmt.Sprintf("Its judge runs cost $%.2f today, its budgetUSD of $%d; it goes on "+
			"tomorrow (UTC), or when the budget is raised", float64(costTodayMilliUSD)/1000, budgetUSD)
	}
	return "", ""
}
