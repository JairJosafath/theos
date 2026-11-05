package model

import (
	"slices"
	"strings"
	"testing"
)

// goodSpec is a spec with the three parts and two criteria.
const goodSpec = "# Export orders\n\n## Problem\n\nPeople copy orders by hand.\n\n## Approach\n\nA CSV endpoint.\n\n" +
	"## Acceptance criteria\n\n- [ ] GET /orders.csv returns every order\n-  [x]  An empty  shop gives a header only\n" +
	"\nNot a criterion.\n\n## Notes\n\n- [ ] not one either\n"

func TestSpecNames(t *testing.T) {
	if got := SpecPath(12); got != "docs/specs/12.md" {
		t.Errorf("SpecPath(12) = %q", got)
	}
	for _, tt := range []struct {
		attempt int
		want    string
	}{{1, "orders-api-12-spec"}, {2, "orders-api-12-spec-2"}} {
		if got := SpecTaskName("orders-api", 12, tt.attempt); got != tt.want {
			t.Errorf("SpecTaskName(attempt %d) = %q, want %q", tt.attempt, got, tt.want)
		}
	}
	if got := SpecTitle(" Export orders "); got != "Spec: Export orders" {
		t.Errorf("SpecTitle() = %q", got)
	}
}

func TestSpecCriteria(t *testing.T) {
	want := []string{"GET /orders.csv returns every order", "An empty shop gives a header only"}
	if got := SpecCriteria(goodSpec); !slices.Equal(got, want) {
		t.Errorf("SpecCriteria() = %q, want %q", got, want)
	}
	if got := SpecCriteria("# Spec\n\n- [ ] a checklist with no heading\n"); got != nil {
		t.Errorf("SpecCriteria() without the heading = %q, want none", got)
	}
	many := "## Acceptance criteria\n\n" + strings.Repeat("- [ ] one more\n", MaxSpecCriteria+5)
	if got := SpecCriteria(many); len(got) != MaxSpecCriteria {
		t.Errorf("SpecCriteria() kept %d criteria, want %d", len(got), MaxSpecCriteria)
	}
}

// A spec round's pull request goes to review only with the spec file alone,
// and criteria in it (docs/plans/done/sdlc-loop.md, step 3).
func TestSpecRefusal(t *testing.T) {
	const path = "docs/specs/12.md"
	tests := []struct {
		name    string
		changed []string
		spec    string
		found   bool
		want    string
	}{
		{"the spec alone", []string{path}, goodSpec, true, ""},
		{"a file beside the spec", []string{path, "app/orders.py", "README.md"}, goodSpec, true,
			"the change touches app/orders.py, README.md; a spec changes only docs/specs/12.md"},
		{"another issue's spec", []string{"docs/specs/13.md"}, "", false, "touches docs/specs/13.md"},
		{"no spec file", nil, "", false, "The pull request has no spec: write it in docs/specs/12.md"},
		{"an empty spec", []string{path}, "\n", true, "has no spec"},
		{"no criteria", []string{path}, "# Spec\n\n## Problem\n\nSlow.\n", true,
			`docs/specs/12.md has no "Acceptance criteria" checklist`},
		{"a criteria heading without items", []string{path}, "## Acceptance criteria\n\nSome prose.\n", true,
			"has no \"Acceptance criteria\" checklist"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SpecRefusal(path, tt.changed, tt.spec, tt.found)
			if tt.want == "" && got != "" || !strings.Contains(got, tt.want) {
				t.Errorf("SpecRefusal() = %q, want %q", got, tt.want)
			}
		})
	}
	if got := SpecRefusal(path, []string{"x.go"}, goodSpec, true); !IsScopeRefusal(got) {
		t.Errorf("a stray file's refusal %q is no scope refusal: two in a row would not stop the Task", got)
	}
}

func TestFactoryAccount(t *testing.T) {
	tests := []struct {
		user string
		want bool
	}{
		{"theos", true},
		{"Theos", true},
		{"webshop-orders-api-agent", true},
		{"webshop-billing-agent", true},
		{"ada", false},
		{"other-orders-api-agent", false},
		{"webshop-agents", false},
	}
	for _, tt := range tests {
		if got := FactoryAccount(tt.user, "theos", "webshop"); got != tt.want {
			t.Errorf("FactoryAccount(%q) = %v, want %v", tt.user, got, tt.want)
		}
	}
}

// Only a person's approval of the reviewed commit counts: never the
// factory's own, a stale, dismissed or older one; a person's later request
// for changes takes their approval back and outweighs another's.
func TestPersonReviews(t *testing.T) {
	factory := func(user string) bool { return FactoryAccount(user, "theos", "webshop") }
	approve := func(user, commit string) PullReview {
		return PullReview{User: user, State: reviewApproved, Commit: commit, Body: "Fine."}
	}
	changes := func(user, commit, body string) PullReview {
		return PullReview{User: user, State: reviewChanges, Commit: commit, Body: body}
	}
	tests := []struct {
		name    string
		reviews []PullReview
		want    PersonVerdict
	}{
		{"none", nil, PersonVerdict{}},
		{"the factory's own approvals", []PullReview{approve("theos", "c2"), approve("webshop-orders-api-agent", "c2")},
			PersonVerdict{}},
		{"a person's", []PullReview{approve("theos", "c2"), approve("ada", "c2")}, PersonVerdict{ApprovedBy: "ada"}},
		{"a person's of no named commit", []PullReview{approve("ada", "")}, PersonVerdict{ApprovedBy: "ada"}},
		{"of an older commit", []PullReview{approve("ada", "c1")}, PersonVerdict{}},
		{"stale", []PullReview{{User: "ada", State: reviewApproved, Commit: "c2", Stale: true}}, PersonVerdict{}},
		{"dismissed", []PullReview{{User: "ada", State: reviewApproved, Commit: "c2", Dismissed: true}}, PersonVerdict{}},
		{"a comment is no verdict", []PullReview{approve("ada", "c2"), {User: "ada", State: "COMMENT", Commit: "c2"}},
			PersonVerdict{ApprovedBy: "ada"}},
		{"changes after an approval", []PullReview{approve("ada", "c2"), changes("ada", "c2", " Say how errors look. ")},
			PersonVerdict{ChangesBy: "ada", Changes: "Say how errors look."}},
		{"an approval after changes", []PullReview{changes("ada", "c2", "No."), approve("ada", "c2")},
			PersonVerdict{ApprovedBy: "ada"}},
		{"one person's changes outweigh another's approval",
			[]PullReview{approve("grace", "c2"), changes("ada", "c2", "Not yet.")},
			PersonVerdict{ChangesBy: "ada", Changes: "Not yet."}},
		{"changes asked of an older commit", []PullReview{changes("ada", "c1", "No."), approve("grace", "c2")},
			PersonVerdict{ApprovedBy: "grace"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PersonReviews(tt.reviews, factory, "c2"); got != tt.want {
				t.Errorf("PersonReviews() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestWithSpecCriteria(t *testing.T) {
	const url = "http://gitea/webshop/orders-api/src/branch/main/docs/specs/4.md"
	criteria := []string{"It exports", "It is tested"}
	section := "From the spec [docs/specs/4.md](" + url + ").\n\n- [ ] It exports\n- [ ] It is tested\n"
	tests := []struct {
		name, body, want string
	}{
		{"an empty body", "", "## Acceptance criteria\n\n" + section},
		{"a body without the section", "Export orders.\n\n", "Export orders.\n\n## Acceptance criteria\n\n" + section},
		{"a body whose section is replaced, later sections kept",
			"Export orders.\n\n## Acceptance criteria\n\n- [ ] Old one\n\n## Notes\n\nKeep me.\n",
			"Export orders.\n\n## Acceptance criteria\n\n" + section + "\n## Notes\n\nKeep me.\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := WithSpecCriteria(tt.body, "docs/specs/4.md", url, criteria)
			if got != tt.want {
				t.Errorf("WithSpecCriteria() =\n%q\nwant\n%q", got, tt.want)
			}
			if again := WithSpecCriteria(got, "docs/specs/4.md", url, criteria); again != got {
				t.Errorf("WithSpecCriteria() twice =\n%q\nwant it unchanged", again)
			}
			if MissingAcceptanceCriteria(got) != "" {
				t.Errorf("the issue is not ready to build from %q", got)
			}
		})
	}
}

// What the operator does to the issue once its spec merged: criteria in,
// status/ready, open (docs/plans/done/sdlc-loop.md, step 3).
func TestPlanSpecMerged(t *testing.T) {
	const (
		path = "docs/specs/4.md"
		url  = "http://gitea/webshop/orders-api/src/branch/main/docs/specs/4.md"
		pull = "http://gitea/webshop/orders-api/pulls/1"
	)
	got := PlanSpecMerged("orders-api-4-spec", SpecIssue{Number: 4, Body: "Export orders."}, path, url, pull, goodSpec)
	if got.Skip != "" || got.Label != LabelReady || len(got.Criteria) != 2 ||
		!strings.Contains(got.Body, "- [ ] GET /orders.csv returns every order") ||
		!strings.Contains(got.Body, "From the spec [docs/specs/4.md]("+url+")") {
		t.Errorf("PlanSpecMerged() = %+v, want the criteria written in and status/ready", got)
	}
	for _, want := range []string{"Task `orders-api-4-spec` merged the spec in " + pull, "[docs/specs/4.md](" + url + ")",
		"Its 2 acceptance criteria are now in the description", LabelReady} {
		if !strings.Contains(got.Comment, want) {
			t.Errorf("comment %q lacks %q", got.Comment, want)
		}
	}
	if got.Applied != "Wrote 2 acceptance criteria from docs/specs/4.md into the description and labelled status/ready" {
		t.Errorf("Applied = %q", got.Applied)
	}

	none := PlanSpecMerged("orders-api-4-spec", SpecIssue{Number: 4}, path, url, pull, "# Spec\n")
	if none.Label != LabelNeedsHuman || none.Body != "" || !strings.Contains(none.Comment, "has no \"Acceptance criteria\"") {
		t.Errorf("PlanSpecMerged() of a spec without criteria = %+v, want it handed to a person", none)
	}
	closed := PlanSpecMerged("orders-api-4-spec", SpecIssue{Number: 4, Closed: true}, path, url, pull, goodSpec)
	if closed.Skip == "" || closed.Label != "" || closed.Body != "" {
		t.Errorf("PlanSpecMerged() of a closed issue = %+v, want nothing done", closed)
	}
}

// The spec's steps through the implement flow and its review point
// (docs/plans/done/sdlc-loop.md, step 3).
func TestSpecSteps(t *testing.T) {
	done := func(result *RunResult) RunState { return RunState{Started: true, Done: true, Result: result} }
	approved := done(&RunResult{Verdict: VerdictApprove, Summary: "Clear spec."})
	spec := func(f TaskFacts) TaskFacts {
		f.Spec, f.Protected, f.ContainerReady, f.Container = true, true, true, "orders-api"
		if f.MaxRounds == 0 {
			f.MaxRounds = 3
		}
		if f.Round == 0 {
			f.Round = 1
		}
		return f
	}
	awaiting := func(approval string, pull SpecPullFacts) TaskFacts {
		return spec(TaskFacts{Phase: TaskAwaitingApproval, SpecApproval: approval, SpecPull: pull})
	}
	stray := SpecScopeRefusal("docs/specs/4.md", []string{"app.py"})
	tests := []struct {
		name    string
		facts   TaskFacts
		want    TaskAction
		message string
		reason  string
	}{
		{"an approved spec waits for a person", spec(TaskFacts{Phase: TaskReviewing, Run: approved}),
			ActionAwaitApproval, "waits for a person to approve its pull request", ""},
		{"Person is the approval asked for", spec(TaskFacts{Phase: TaskReviewing, Run: approved,
			SpecApproval: SpecApprovalPerson}), ActionAwaitApproval, "", ""},
		{"with Reviewer, the reviewer's approval merges it", spec(TaskFacts{Phase: TaskReviewing, Run: approved,
			SpecApproval: SpecApprovalReviewer}), ActionMerge, "Approved; merging", ""},
		{"an Implement Task merges on approval as ever", TaskFacts{Phase: TaskReviewing, Run: approved, Round: 1,
			MaxRounds: 3}, ActionMerge, "Approved; merging", ""},
		{"no merge before a person's approval", awaiting("", SpecPullFacts{}), ActionWait,
			"waits for a person to approve", ReasonAwaitingApproval},
		{"the factory's own approval is no person's", awaiting(SpecApprovalPerson, SpecPullFacts{}), ActionWait, "",
			ReasonAwaitingApproval},
		{"a person's approval merges it", awaiting("", SpecPullFacts{People: PersonVerdict{ApprovedBy: "ada"}}),
			ActionMerge, "ada approved the spec; merging", ""},
		{"approved, but main moved", func() TaskFacts {
			f := awaiting("", SpecPullFacts{People: PersonVerdict{ApprovedBy: "ada"}})
			f.Behind = true
			return f
		}(), ActionUpdateBranch, "main moved", ""},
		{"Reviewer set while it waits merges it", awaiting(SpecApprovalReviewer, SpecPullFacts{}), ActionMerge,
			"The reviewer's approval is enough", ""},
		{"a person merged it", awaiting("", SpecPullFacts{Merged: true}), ActionMerge, "A person merged", ""},
		{"a person closed it: declined", awaiting("", SpecPullFacts{Closed: true,
			People: PersonVerdict{ApprovedBy: "ada"}}), ActionSpecDeclined, "declined", ReasonSpecDeclined},
		{"its branch moved: checked again", awaiting("", SpecPullFacts{HeadMoved: true,
			People: PersonVerdict{ApprovedBy: "ada"}}), ActionMerge, "checking it again", ""},
		{"a person's changes start the next round", awaiting("", SpecPullFacts{
			People: PersonVerdict{ChangesBy: "ada", Changes: "Say how errors look."}}), ActionRetry,
			"Round 1 was not accepted", ""},
		{"an approval from chat merges it, said so", awaiting("", SpecPullFacts{
			People: PersonVerdict{ApprovedBy: "ada@example.com", ApprovedInChat: true}}),
			ActionMerge, "ada@example.com approved the spec in chat; merging", ""},
		{"changes from chat start the next round with them", awaiting("", SpecPullFacts{
			People: PersonVerdict{ChangesBy: "ada@example.com", Changes: "Name the tokens.", ChangesInChat: true}}),
			ActionRetry, "", ""},
		{"a word from chat that didn't count is said in the wait", awaiting("", SpecPullFacts{
			Ignored: []string{"ada's approval from chat is of commit abc1234, not the head def5678, so it doesn't count"}}),
			ActionWait, "waits for a person to approve its pull request; ada's approval from chat is of commit abc1234",
			ReasonAwaitingApproval},
		{"a person's changes in the last round fail it", spec(TaskFacts{Phase: TaskAwaitingApproval, Round: 3,
			MaxRounds: 3, SpecPull: SpecPullFacts{People: PersonVerdict{ChangesBy: "ada", Changes: "No."}}}),
			ActionFail, "", ""},
		{"a spec beside other files goes back", spec(TaskFacts{Phase: TaskChecking, Checks: checkSuccess,
			SpecRefusal: stray}), ActionRetry, "", ""},
		{"twice in a row stops it", spec(TaskFacts{Phase: TaskChecking, Checks: checkSuccess, Round: 2,
			SpecRefusal: stray, Feedback: stray}), ActionFail, "Two rounds in a row", ReasonBlocked},
		{"a spec without criteria goes back", spec(TaskFacts{Phase: TaskChecking, Checks: checkSuccess,
			SpecRefusal: "docs/specs/4.md has no \"Acceptance criteria\" checklist"}), ActionRetry, "", ""},
		{"a good spec is reviewed", spec(TaskFacts{Phase: TaskChecking, Checks: checkSuccess}), ActionStartReview,
			"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NextTaskStep(tt.facts)
			if got.Action != tt.want || !strings.Contains(got.Message, tt.message) || got.Reason != tt.reason {
				t.Errorf("NextTaskStep() = %+v, want %s with %q, reason %q", got, tt.want, tt.message, tt.reason)
			}
		})
	}
	changes := NextTaskStep(awaiting("", SpecPullFacts{People: PersonVerdict{ChangesBy: "ada", Changes: "Say how."}}))
	if !strings.Contains(changes.Feedback, "ada asked for changes to the spec on its pull request:\nSay how.") {
		t.Errorf("feedback = %q, want the person's review", changes.Feedback)
	}
	chat := NextTaskStep(awaiting("", SpecPullFacts{People: PersonVerdict{ChangesBy: "ada@example.com",
		Changes: "Name the tokens.", ChangesInChat: true}}))
	if !strings.Contains(chat.Feedback, "ada@example.com asked for changes to the spec in chat:\nName the tokens.") {
		t.Errorf("feedback = %q, want the person's changes from chat", chat.Feedback)
	}
	if p := AwaitApproval(TaskProgress{Phase: TaskReviewing, Round: 2}); p.Phase != TaskAwaitingApproval || p.Round != 2 {
		t.Errorf("AwaitApproval() = %+v", p)
	}
}

func TestStandInSpec(t *testing.T) {
	spec := StandInSpec(4, "Issue #4: Export orders\n\nstand-in: spec")
	if got := SpecRefusal("docs/specs/4.md", []string{"docs/specs/4.md"}, spec, true); got != "" {
		t.Errorf("the stand-in spec is refused: %s\n%s", got, spec)
	}
	if !strings.Contains(spec, "Issue #4: Export orders") {
		t.Errorf("the stand-in spec doesn't name its issue:\n%s", spec)
	}
}

// A Crew with a spec writer starts a Spec Task for an issue labelled
// status/needs-spec as it starts an Implement Task for a ready one: in order,
// within parallel, with tries of its own (docs/plans/done/sdlc-loop.md, step 3).
func TestNextCrewStepSpec(t *testing.T) {
	needsSpec := func(n int) Issue { return Issue{Number: n, Title: "Export orders", Body: "Big.", Spec: true} }
	routes := []AgentRoute{{Label: "kind/api", Implementer: "opus", Reviewer: "strict"}}
	tests := []struct {
		name     string
		facts    CrewFacts
		start    []IssueStart
		notReady []IssueRejection
		message  string
	}{
		{"a spec needs no criteria yet", CrewFacts{ContainerReady: true, Issues: []Issue{needsSpec(4)}},
			[]IssueStart{{Issue: 4, Task: "orders-api-4-spec", Attempt: 1, Spec: true}}, nil,
			"Starting Task orders-api-4-spec for the spec of issue #4"},
		{"a route picks the reviewer, never the spec writer", CrewFacts{ContainerReady: true, Routes: routes,
			Issues: []Issue{func() Issue { i := needsSpec(4); i.Labels = []string{"kind/api"}; return i }()}},
			[]IssueStart{{Issue: 4, Task: "orders-api-4-spec", Attempt: 1, Spec: true, Reviewer: "strict"}}, nil, ""},
		{"in order with ready issues, within parallel", CrewFacts{ContainerReady: true, Parallel: 1,
			Issues: []Issue{{Number: 5, Body: criteria}, needsSpec(4)}},
			[]IssueStart{{Issue: 4, Task: "orders-api-4-spec", Attempt: 1, Spec: true}}, nil, ""},
		{"a second try at the spec", CrewFacts{ContainerReady: true, Issues: []Issue{needsSpec(4)},
			Tasks: []CrewTask{{Name: "orders-api-4-spec", Issue: 4, Spec: true, Final: true, Failed: true}}},
			[]IssueStart{{Issue: 4, Task: "orders-api-4-spec-2", Attempt: 2, Spec: true}}, nil, ""},
		{"the spec's tries are its own", CrewFacts{ContainerReady: true, Issues: []Issue{needsSpec(4)},
			Tasks: []CrewTask{{Name: "orders-api-4", Issue: 4, Final: true, Failed: true},
				{Name: "orders-api-4-2", Issue: 4, Final: true, Failed: true},
				{Name: "orders-api-4-3", Issue: 4, Final: true, Failed: true}}},
			[]IssueStart{{Issue: 4, Task: "orders-api-4-spec", Attempt: 1, Spec: true}}, nil, ""},
		{"three failed specs need a person", CrewFacts{ContainerReady: true, Issues: []Issue{needsSpec(4)},
			Tasks: []CrewTask{{Name: "orders-api-4-spec", Issue: 4, Spec: true, Final: true, Failed: true},
				{Name: "orders-api-4-spec-2", Issue: 4, Spec: true, Final: true, Failed: true},
				{Name: "orders-api-4-spec-3", Issue: 4, Spec: true, Final: true, Failed: true}}},
			nil, []IssueRejection{{Issue: 4, Spec: true, Reason: "Agents tried this issue's spec 3 times (Tasks " +
				"orders-api-4-spec, orders-api-4-spec-2, orders-api-4-spec-3) and failed each time; it needs a person: " +
				"split it, clarify it, or do it by hand"}}, ""},
		{"a merged spec is not written again", CrewFacts{ContainerReady: true, Issues: []Issue{needsSpec(4)},
			Tasks: []CrewTask{{Name: "orders-api-4-spec", Issue: 4, Spec: true, Final: true}}},
			nil, []IssueRejection{{Issue: 4, Spec: true, Reason: "Task orders-api-4-spec already merged this " +
				"issue's spec; label it status/ready to build it, or close it"}}, ""},
		{"after its spec merged, the issue is built from try 1", CrewFacts{ContainerReady: true,
			Issues: []Issue{{Number: 4, Body: criteria}},
			Tasks:  []CrewTask{{Name: "orders-api-4-spec", Issue: 4, Spec: true, Final: true}}},
			[]IssueStart{{Issue: 4, Task: "orders-api-4", Attempt: 1}}, nil, ""},
		{"a spec at work keeps its issue", CrewFacts{ContainerReady: true, Parallel: 2,
			Issues: []Issue{needsSpec(4)}, Tasks: []CrewTask{{Name: "orders-api-4-spec", Issue: 4, Spec: true}}},
			nil, nil, "Task orders-api-4-spec is working on issue #4"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NextCrewStep("orders-api", tt.facts)
			if !slices.EqualFunc(got.Start, tt.start, func(a, b IssueStart) bool {
				return a.Issue == b.Issue && a.Task == b.Task && a.Attempt == b.Attempt && a.Spec == b.Spec &&
					a.Implementer == b.Implementer && a.Reviewer == b.Reviewer
			}) {
				t.Errorf("Start = %+v, want %+v", got.Start, tt.start)
			}
			if !slices.Equal(got.NotReady, tt.notReady) {
				t.Errorf("NotReady = %+v, want %+v", got.NotReady, tt.notReady)
			}
			if !strings.Contains(got.Message, tt.message) {
				t.Errorf("Message = %q, want %q in it", got.Message, tt.message)
			}
		})
	}
}
