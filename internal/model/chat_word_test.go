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
	"reflect"
	"slices"
	"strings"
	"testing"
)

// A word from chat counts only for the head commit and only as the person
// its -by companion names; anything else is said in the wait's message.
func TestChatSpecVerdict(t *testing.T) {
	const head = "def5678abc"
	tests := []struct {
		name             string
		approve, changes ChatWord
		want             PersonVerdict
		ignored          []string
	}{
		{name: "nothing said"},
		{name: "an approval of the head", approve: ChatWord{Value: head, By: "ada"},
			want: PersonVerdict{ApprovedBy: "ada", ApprovedInChat: true}},
		{name: "an approval of an older commit", approve: ChatWord{Value: "abc1234ff", By: "ada"},
			ignored: []string{"ada's approval from chat is of commit abc1234, not the head def5678, so it doesn't count"}},
		{name: "an approval naming nobody", approve: ChatWord{Value: head},
			ignored: []string{"the approval from chat (theos.factory.io/approve-spec) names nobody in " +
				"theos.factory.io/approve-spec-by, so it doesn't count"}},
		{name: "changes asked of the head", changes: ChatWord{Value: head + " Name the tokens. ", By: "bob"},
			want: PersonVerdict{ChangesBy: "bob", Changes: "Name the tokens.", ChangesInChat: true}},
		{name: "changes asked of an older commit", changes: ChatWord{Value: "abc1234 Name them", By: "bob"},
			ignored: []string{"bob's changes from chat are asked of commit abc1234, not the head def5678, so they " +
				"don't count"}},
		{name: "changes without a text", changes: ChatWord{Value: head, By: "bob"},
			ignored: []string{"bob's changes from chat aren't a commit and the changes, so they don't count"}},
		{name: "changes naming nobody", changes: ChatWord{Value: head + " x"},
			ignored: []string{"the changes asked from chat (theos.factory.io/spec-changes) name nobody in " +
				"theos.factory.io/spec-changes-by, so they don't count"}},
		{name: "changes outweigh an approval", approve: ChatWord{Value: head, By: "ada"},
			changes: ChatWord{Value: head + " Not yet.", By: "bob"},
			want:    PersonVerdict{ChangesBy: "bob", Changes: "Not yet.", ChangesInChat: true}},
	}
	for _, tt := range tests {
		got, ignored := ChatSpecVerdict(tt.approve, tt.changes, head)
		if got != tt.want || !slices.Equal(ignored, tt.ignored) {
			t.Errorf("%s: ChatSpecVerdict = %+v, %q; want %+v, %q", tt.name, got, ignored, tt.want, tt.ignored)
		}
	}
}

func TestCombineVerdicts(t *testing.T) {
	hostApproved, hostChanges := PersonVerdict{ApprovedBy: "ada"}, PersonVerdict{ChangesBy: "ada", Changes: "x"}
	chatApproved := PersonVerdict{ApprovedBy: "bob", ApprovedInChat: true}
	chatChanges := PersonVerdict{ChangesBy: "bob", Changes: "y", ChangesInChat: true}
	tests := []struct {
		name       string
		host, chat PersonVerdict
		want       PersonVerdict
	}{
		{"nobody", PersonVerdict{}, PersonVerdict{}, PersonVerdict{}},
		{"the Git host's approval", hostApproved, PersonVerdict{}, hostApproved},
		{"chat's approval", PersonVerdict{}, chatApproved, chatApproved},
		{"both approve: the Git host's first", hostApproved, chatApproved, hostApproved},
		{"chat's changes outweigh the Git host's approval", hostApproved, chatChanges, chatChanges},
		{"the Git host's changes outweigh chat's approval", hostChanges, chatApproved, hostChanges},
		{"both ask: the Git host's first", hostChanges, chatChanges, hostChanges},
	}
	for _, tt := range tests {
		if got := CombineVerdicts(tt.host, tt.chat); got != tt.want {
			t.Errorf("%s: CombineVerdicts = %+v, want %+v", tt.name, got, tt.want)
		}
	}
}

func TestPlanChatAnswer(t *testing.T) {
	answer := ChatWord{Value: "Under 200 ms at p95.", By: "ada@example.com"}
	parked := TriageIssue{Number: 4, Labels: []string{LabelNeedsHuman}}
	tests := []struct {
		name       string
		decision   string
		answeredBy string
		answer     ChatWord
		issue      TriageIssue
		want       ChatAnswerActions
	}{
		{name: "no answer", decision: TriagePark, issue: parked},
		{name: "one taken already", decision: TriagePark, answeredBy: "bob", answer: answer, issue: parked},
		{name: "an answer to a parked issue", decision: TriagePark, answer: answer, issue: parked,
			want: ChatAnswerActions{Comment: "ada@example.com answered in chat:\n\nUnder 200 ms at p95.", Relabel: true,
				Answered: "Commented ada@example.com's answer on issue #4 and labelled it status/proposed to be " +
					"triaged again"}},
		{name: "a person moved the issue meanwhile", decision: TriagePark, answer: answer,
			issue: TriageIssue{Number: 4, Labels: []string{LabelReady}},
			want: ChatAnswerActions{Comment: "ada@example.com answered in chat:\n\nUnder 200 ms at p95.",
				Answered: "Commented ada@example.com's answer on issue #4, which keeps status/ready: a person moved " +
					"it meanwhile"}},
		{name: "a closed issue", decision: TriagePark, answer: answer, issue: TriageIssue{Number: 4, Closed: true},
			want: ChatAnswerActions{Answered: "Issue #4 was closed; ada@example.com's answer was not posted"}},
		{name: "no question asked", decision: TriageReady, answer: answer, issue: parked,
			want: ChatAnswerActions{Ignore: "the answer from chat is to no question: the triager decided ready, not park"}},
		{name: "an answer naming nobody", decision: TriagePark, answer: ChatWord{Value: "x"}, issue: parked,
			want: ChatAnswerActions{Ignore: "the answer from chat names nobody in theos.factory.io/answer-by, so it " +
				"doesn't count"}},
	}
	for _, tt := range tests {
		if got := PlanChatAnswer(tt.decision, tt.answeredBy, tt.answer, tt.issue); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s: PlanChatAnswer = %+v, want %+v", tt.name, got, tt.want)
		}
	}
}

// Only a Chat's people are obeyed, matched on their exact Matrix ID.
func TestChatPersonFor(t *testing.T) {
	people := []ChatPerson{{MatrixID: "@ada:example.org", User: "ada@example.com", Groups: []string{"shop"}},
		{MatrixID: "@nobody:example.org"}}
	tests := []struct {
		sender string
		want   string
		ok     bool
	}{
		{"@ada:example.org", "ada@example.com", true},
		{"@Ada:example.org", "", false},
		{"@ada:example.org.evil", "", false},
		{"@ada:other.org", "", false},
		{"@mallory:example.org", "", false},
		{"@nobody:example.org", "", false},
		{"", "", false},
	}
	for _, tt := range tests {
		got, ok := ChatPersonFor(people, tt.sender)
		if ok != tt.ok || got.User != tt.want {
			t.Errorf("ChatPersonFor(%q) = %+v, %t; want %q, %t", tt.sender, got, ok, tt.want, tt.ok)
		}
	}
	if got, _ := ChatPersonFor(people, "@ada:example.org"); !slices.Equal(got.Groups, []string{"shop"}) {
		t.Errorf("groups = %q", got.Groups)
	}
}

// What theos-chat answers: a stranger's refusal in one line, the API
// server's refusal with its words, and what was done as whom.
func TestChatAnswers(t *testing.T) {
	refusal := ChatStrangerRefusal("@mallory:example.org", "webshop")
	if strings.Contains(refusal, "\n") || !strings.Contains(refusal, "@mallory:example.org is not one of Chat "+
		"webshop's people, so nothing was done") {
		t.Errorf("ChatStrangerRefusal = %q", refusal)
	}
	if got := ChatRefused("ada@example.com", 403, ` tasks "api-1-spec" is forbidden `); got !=
		`The cluster refused it for ada@example.com (403): tasks "api-1-spec" is forbidden` {
		t.Errorf("ChatRefused = %q", got)
	}
	if got := ChatRefused("ada", 500, ""); !strings.HasSuffix(got, "(500): no reason given") {
		t.Errorf("ChatRefused without words = %q", got)
	}
	tests := []struct {
		cmd  ChatCommand
		item ChatItem
		made string
		want string
	}{
		{ChatCommand{Kind: ChatCommandApproveSpec}, ChatItem{Commit: "def5678abc"}, "",
			"Done as ada: approved the spec at def5678; the operator merges it."},
		{ChatCommand{Kind: ChatCommandChanges}, ChatItem{Commit: "def5678abc"}, "",
			"Done as ada: asked for changes to the spec at def5678; the next round takes them."},
		{ChatCommand{Kind: ChatCommandBrief}, ChatItem{}, "brief-x7k2p", "Done as ada: made Brief brief-x7k2p."},
		{ChatCommand{Kind: ChatCommandApproveDeploy, Version: "v1.2.0"}, ChatItem{Target: "prod"}, "",
			"Done as ada: let v1.2.0 into prod."},
		{ChatCommand{Kind: ChatCommandRetry}, ChatItem{Name: "api"}, "", "Done as ada: asked Container api to scaffold again."},
	}
	for _, tt := range tests {
		if got := ChatDone(tt.cmd, tt.item, "ada", tt.made); got != tt.want {
			t.Errorf("ChatDone(%s) = %q, want %q", tt.cmd.Kind, got, tt.want)
		}
	}
}
