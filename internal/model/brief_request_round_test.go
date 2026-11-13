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

package model

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

func TestTakenAndNewRequests(t *testing.T) {
	requests := []Request{
		{Repository: "o/r", Number: 1, Stage: RequestAccepted},
		{Repository: "o/r", Number: 2, Stage: RequestWaiting},
		{Repository: "o/r", Number: 3, Stage: RequestPlanning, Round: 2},
		{Repository: "o/r", Number: 4, Stage: RequestAccepted},
		{Repository: "o/r", Number: 5, Stage: RequestDeclined},
	}
	names := func(rs []Request) []string {
		out := make([]string, 0, len(rs))
		for _, r := range rs {
			out = append(out, RequestName(r.Repository, r.Number))
		}
		return out
	}
	if got, want := names(TakenRequests(requests)), []string{"o/r#1", "o/r#4"}; !slices.Equal(got, want) {
		t.Errorf("TakenRequests = %v, want %v", got, want)
	}
	for _, tc := range []struct {
		name string
		last []string
		want int
	}{
		{"none taken before", nil, 2},
		{"one left by a failed round waits for something new", []string{"o/r#1"}, 1},
		{"both left by a round that asked for input", []string{"o/r#1", "o/r#4"}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := NewRequests(requests, tc.last); got != tc.want {
				t.Errorf("NewRequests = %d, want %d", got, tc.want)
			}
		})
	}
	running := names(WorkspaceRequests(requests, []string{"o/r#3", "o/r#9"}, true))
	if want := []string{"o/r#3"}; !slices.Equal(running, want) {
		t.Errorf("WorkspaceRequests while a round runs = %v, want %v: the round's, still open", running, want)
	}
	between := names(WorkspaceRequests(requests, []string{"o/r#3"}, false))
	if want := []string{"o/r#1", "o/r#4"}; !slices.Equal(between, want) {
		t.Errorf("WorkspaceRequests between rounds = %v, want %v: the next round's", between, want)
	}
}

// #160: a round names a source's item source:key, so an item and a GitHub
// issue of one number are two requests.
func TestRoundRequestsFromASource(t *testing.T) {
	requests := []Request{
		{Repository: "o/r", Number: 412, Stage: RequestAccepted},
		{Source: "product", Key: "ENG-412", Number: 412, Stage: RequestAccepted},
		{Source: "support", Key: "SHOP-7", Number: 7, Stage: RequestPlanning, Round: 2},
	}
	if got := NewRequests(requests, []string{"o/r#412"}); got != 1 {
		t.Errorf("NewRequests = %d, want 1: product:ENG-412 is new", got)
	}
	if got := NewRequests(requests, []string{"product:ENG-412"}); got != 1 {
		t.Errorf("NewRequests = %d, want 1: o/r#412 is new", got)
	}
	running := WorkspaceRequests(requests, []string{"support:SHOP-7"}, true)
	if len(running) != 1 || running[0].Key != "SHOP-7" {
		t.Errorf("WorkspaceRequests while a round runs = %+v, want support:SHOP-7", running)
	}
}

func TestParsePlanRequests(t *testing.T) {
	files := map[string]string{
		PlanModelFile: "",
		PlanRequestsFile: `- request: o/r#1
  answer: take
  issues: [1, 2]
- request: o/r#2
  answer: skip
  why: "Already there: #5 did it."
`,
	}
	plan, err := ParsePlan(files)
	if err != nil {
		t.Fatal(err)
	}
	want := []RequestAnswer{{Request: "o/r#1", Answer: AnswerTake, Issues: []int{1, 2}},
		{Request: "o/r#2", Answer: AnswerSkip, Why: "Already there: #5 did it."}}
	if len(plan.Requests) != 2 || plan.Requests[0].Request != want[0].Request ||
		!slices.Equal(plan.Requests[0].Issues, want[0].Issues) || plan.Requests[1].Why != want[1].Why {
		t.Errorf("Requests = %+v, want %+v", plan.Requests, want)
	}
	for name, bad := range map[string]string{
		"an unknown field":    "- request: o/r#1\n  answer: take\n  issue: 1\n",
		"not a list":          "request: o/r#1\n",
		"a why cut by a hash": "- request: o/r#1\n  answer: skip\n  why: Done in #5 already\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ParsePlan(map[string]string{PlanModelFile: "", PlanRequestsFile: bad})
			if err == nil || !strings.Contains(err.Error(), PlanRequestsFile) {
				t.Errorf("ParsePlan = %v, want an error naming %s", err, PlanRequestsFile)
			}
		})
	}
	if _, err := ParseTendPlan(map[string]string{PlanRequestsFile: "[]"}); err == nil {
		t.Error("a tending round's plan answers requests, want it refused: a tending round takes none")
	}
}

func TestCheckRequestAnswers(t *testing.T) {
	issues := []PlanIssue{{N: 1, Container: "cli"}, {N: 2, Container: "cli"}}
	took := []string{"o/r#1", "o/r#2", "o/r#3"}
	open := []string{"o/r#1", "o/r#2"} // #3 was closed on GitHub during the round
	take := func(name string, ns ...int) RequestAnswer {
		return RequestAnswer{Request: name, Answer: AnswerTake, Issues: ns}
	}
	skip := func(name, why string) RequestAnswer {
		return RequestAnswer{Request: name, Answer: AnswerSkip, Why: why}
	}
	for _, tc := range []struct {
		name    string
		answers []RequestAnswer
		want    []string
	}{
		{"each open one answered", []RequestAnswer{take("o/r#1", 1, 2), skip("o/r#2", "Out of scope.")}, nil},
		{"a closed one may be answered too", []RequestAnswer{take("o/r#1", 1), skip("o/r#2", "Unclear."),
			skip("o/r#3", "Closed.")}, nil},
		{"one left out", []RequestAnswer{take("o/r#1", 1)}, []string{"leaves o/r#2 unanswered"}},
		{"one not taken", []RequestAnswer{take("o/r#1", 1), skip("o/r#2", "No."), take("o/r#7", 2)},
			[]string{"answers o/r#7, which this round didn't take"}},
		{"a take with no issue", []RequestAnswer{take("o/r#1"), skip("o/r#2", "No.")},
			[]string{"takes o/r#1 with no issue"}},
		{"a take of an issue the plan doesn't file", []RequestAnswer{take("o/r#1", 3), skip("o/r#2", "No.")},
			[]string{"takes o/r#1 as issue 3, which the plan doesn't file"}},
		{"a skip with no why", []RequestAnswer{take("o/r#1", 1), skip("o/r#2", " ")},
			[]string{"skips o/r#2 with no why"}},
		{"a skip with issues", []RequestAnswer{take("o/r#1", 1),
			{Request: "o/r#2", Answer: AnswerSkip, Issues: []int{2}, Why: "No."}},
			[]string{"skips o/r#2 with issues"}},
		{"twice", []RequestAnswer{take("o/r#1", 1), take("o/r#1", 2), skip("o/r#2", "No.")},
			[]string{"answers o/r#1 twice"}},
		{"another answer", []RequestAnswer{take("o/r#1", 1), {Request: "o/r#2", Answer: "later"}},
			[]string{`answers o/r#2 "later"`}},
		{"a why too long", []RequestAnswer{take("o/r#1", 1), skip("o/r#2", strings.Repeat("x", MaxAnswerWhy+1))},
			[]string{"with a why of 501 bytes"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			refusals := CheckRequestAnswers(tc.answers, took, open, issues)
			if len(refusals) != len(tc.want) {
				t.Fatalf("refusals %v, want %d: %q", refusals, len(tc.want), tc.want)
			}
			for i, r := range refusals {
				if r.Subject != PlanRequestsFile || !strings.Contains(r.Reason, tc.want[i]) {
					t.Errorf("refusal %d = %v, want %s: ...%s...", i, r, PlanRequestsFile, tc.want[i])
				}
			}
		})
	}
}

func TestRequestIssues(t *testing.T) {
	issues := []PlanIssue{{N: 1, Container: "cli"}, {N: 2, Container: "api"}, {N: 3, Container: "cli"}}
	got := RequestIssues(RequestAnswer{Request: "o/r#1", Answer: AnswerTake, Issues: []int{2, 1, 3, 1}}, issues,
		map[int]int{1: 14, 2: 7})
	if want := []string{"api#7", "cli#14"}; !slices.Equal(got, want) {
		t.Errorf("RequestIssues = %v, want %v: each filed one once, an unfiled one left out", got, want)
	}
}

// The workspace lists the round's requests for the planner, and brief.md
// says what they are and where to answer them.
func TestBriefWorkspaceRequests(t *testing.T) {
	ws := BriefWorkspace{Brief: "lens", GitProvider: "gitea", Idea: "A linter.", IdeaPlanned: true,
		Requests: []Request{{Repository: "acme/lens", Number: 12, Author: "ada", Title: "Dark theme",
			Body: "Please add a dark theme.\n"}}}
	files, err := BriefWorkspaceFiles(ws)
	if err != nil {
		t.Fatal(err)
	}
	want := "- request: acme/lens#12\n  repository: acme/lens\n  number: 12\n  author: ada\n  title: Dark theme\n" +
		"  body: Please add a dark theme.\n"
	if got := files[WorkspaceRequestsFile]; got != want {
		t.Errorf("%s =\n%s\nwant\n%s", WorkspaceRequestsFile, got, want)
	}
	brief := files[BriefFile]
	for _, s := range []string{"## Requests (**new**)", "The round takes one request", "GitHub user who opened it",
		WorkspaceRequestsFile, PlanRequestsFile} {
		if !strings.Contains(brief, s) {
			t.Errorf("brief.md lacks %q:\n%s", s, brief)
		}
	}
	ws.Requests = nil
	files, err = BriefWorkspaceFiles(ws)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(files[BriefFile], "## Requests") || files[WorkspaceRequestsFile] != "[]\n" {
		t.Errorf("no requests: brief.md says some, or %s is %q", WorkspaceRequestsFile, files[WorkspaceRequestsFile])
	}
}

// #160: model/requests.yaml names a source's item by its source and key, with
// no repository, and leaves the author out when the tracker names nobody.
func TestBriefWorkspaceRequestsFromASource(t *testing.T) {
	ws := BriefWorkspace{Brief: "lens", Idea: "A linter.", IdeaPlanned: true,
		Requests: []Request{
			{Source: "product", Key: "ENG-412", Number: 412, Author: "Ada Lovelace", Title: "Dark theme",
				Body: "Please add a dark theme."},
			{Source: "support", Key: "SHOP-7", Number: 7, Title: "Refunds", Body: "From the service desk."},
		}}
	files, err := BriefWorkspaceFiles(ws)
	if err != nil {
		t.Fatal(err)
	}
	want := "- request: product:ENG-412\n  source: product\n  key: ENG-412\n  number: 412\n" +
		"  author: Ada Lovelace\n  title: Dark theme\n  body: Please add a dark theme.\n" +
		"- request: support:SHOP-7\n  source: support\n  key: SHOP-7\n  number: 7\n" +
		"  title: Refunds\n  body: From the service desk.\n"
	if got := files[WorkspaceRequestsFile]; got != want {
		t.Errorf("%s =\n%s\nwant\n%s", WorkspaceRequestsFile, got, want)
	}
}

// The stand-in planner files an issue per request and takes it, and skips
// what it has no Container or no room for; CheckRequestAnswers takes its plan.
func TestStandInPlanRequests(t *testing.T) {
	catalog := []CatalogEntry{{Side: SideFramework, Name: "go-cli", SupportedTypes: []string{TypeCLI}}}
	system := PlanObject{Kind: KindSoftwareSystem, Name: "tools"}
	lend := PlanObject{Kind: KindContainer, Name: "lend", SoftwareSystem: "tools", Type: TypeCLI,
		GitProvider: "gitea", Framework: "go-cli"}
	request := func(n int, title, body string) Request {
		return Request{Repository: "acme/lend", Number: n, Author: "ada", Title: title, Body: body}
	}
	many := make([]Request, 0, MaxPlanIssues+1)
	for n := 1; n <= MaxPlanIssues+1; n++ {
		many = append(many, request(n, "Ask "+strings.Repeat("x", n), ""))
	}
	for _, tc := range []struct {
		name    string
		ws      BriefWorkspace
		issues  []string
		answers []string
	}{
		{
			name: "a request and a prompt, for the Brief's Container",
			ws: BriefWorkspace{IdeaPlanned: true, Objects: []PlanObject{system, lend}, PromptsPlanned: 0,
				Prompts:  []string{"Add a calendar."},
				Requests: []Request{request(12, "Dark theme", "Please add\na dark theme.")}},
			issues:  []string{"lend: Stand-in: Add a calendar.", "lend: Stand-in: Dark theme"},
			answers: []string{"acme/lend#12 take [2]"},
		},
		{
			name:    "a request with the idea, for its new Container",
			ws:      BriefWorkspace{Objects: []PlanObject{system}, Requests: []Request{request(3, "Dark theme", "")}},
			issues:  []string{"lend: Stand-in: start lend", "lend: Stand-in: Dark theme"},
			answers: []string{"acme/lend#3 take [2]"},
		},
		{
			name:    "no Container to file it for",
			ws:      BriefWorkspace{IdeaPlanned: true, Objects: []PlanObject{system}, Requests: []Request{request(3, "Dark theme", "")}},
			answers: []string{"acme/lend#3 skip []"},
		},
		{
			name: "more than a round has room for",
			ws:   BriefWorkspace{IdeaPlanned: true, Objects: []PlanObject{system, lend}, Requests: many},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.ws.Brief, tc.ws.GitProvider, tc.ws.Catalog, tc.ws.Idea = "lend", "gitea", catalog, "Lend tools."
			workspace, err := BriefWorkspaceFiles(tc.ws)
			if err != nil {
				t.Fatal(err)
			}
			files, err := StandInPlan(workspace)
			if err != nil {
				t.Fatal(err)
			}
			plan, err := ParsePlan(files)
			if err != nil {
				t.Fatalf("ParsePlan: %v\n%v", err, files)
			}
			var took []string
			for _, r := range tc.ws.Requests {
				took = append(took, RequestName(r.Repository, r.Number))
			}
			if refusals := CheckRequestAnswers(plan.Requests, took, took, plan.Issues); len(refusals) > 0 {
				t.Fatalf("CheckRequestAnswers refuses the stand-in's plan: %v", refusals)
			}
			if tc.name == "more than a round has room for" {
				last := plan.Requests[len(plan.Requests)-1]
				if len(plan.Issues) != MaxPlanIssues || last.Answer != AnswerSkip || last.Why == "" {
					t.Errorf("issues %d, last answer %+v; want %d issues and the rest skipped", len(plan.Issues),
						last, MaxPlanIssues)
				}
				return
			}
			var issues, answers []string
			for _, i := range plan.Issues {
				issues = append(issues, i.Container+": "+i.Title)
			}
			for _, a := range plan.Requests {
				answers = append(answers, fmt.Sprintf("%s %s %v", a.Request, a.Answer, a.Issues))
			}
			if !slices.Equal(issues, tc.issues) {
				t.Errorf("issues %q, want %q", issues, tc.issues)
			}
			if !slices.Equal(answers, tc.answers) {
				t.Errorf("answers %q, want %q", answers, tc.answers)
			}
		})
	}
}
