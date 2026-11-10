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
	"regexp"
	"strings"
	"testing"
	"time"
)

// An item's key is stable, a ConfigMap key, and long ones stay apart.
func TestChatItemKey(t *testing.T) {
	if got := ChatItemKey(ChatEventSpecWaiting, "api-4-spec"); got != "SpecWaiting.api-4-spec" {
		t.Errorf("key = %q", got)
	}
	if got := ChatItemKey(ChatEventDeployed, "api", "prod", "v1.2.0+build/7"); got != "Deployed.api.prod.v1.2.0_build_7" {
		t.Errorf("key = %q", got)
	}
	valid := regexp.MustCompile(`^[-._a-zA-Z0-9]+$`)
	long1 := ChatItemKey(ChatEventDeployed, strings.Repeat("a", 250), "1")
	long2 := ChatItemKey(ChatEventDeployed, strings.Repeat("a", 250), "2")
	for _, k := range []string{long1, long2} {
		if len(k) > chatMaxKey || !valid.MatchString(k) {
			t.Errorf("key %q (%d) is no ConfigMap key", k, len(k))
		}
	}
	if long1 == long2 {
		t.Errorf("two long keys are one: %q", long1)
	}
}

// The thread state machine: started when its item needs a decision, its
// root edited as the item's text changes, ✅ once done, a new thread when it
// needs a decision again; plain and digest news once; the first pass tells
// no history.
func TestChatNextPost(t *testing.T) {
	waiting := ChatNews{Key: "SpecWaiting.a", Event: ChatEventSpecWaiting, Open: true,
		Post: ChatPost{Title: "Spec of issue #4", Status: "Waits for your approval"}}
	moved := waiting
	moved.Open, moved.Post.Status = false, "Round 2: Implementing."
	done := waiting
	done.Open, done.Done, done.Post.Status = false, true, "Merged."
	posted := &ChatPosted{Room: ChatSpecs, Event: "$root", Hash: ChatHash(waiting.Post)}
	finished := &ChatPosted{Room: ChatSpecs, Event: "$root", Hash: ChatHash(done.Post), Done: true}
	plain := ChatNews{Key: "TaskFailed.a", Event: ChatEventTaskFailed, Post: ChatPost{Title: "failed"}}
	digest := ChatNews{Key: "Merged.a", Event: ChatEventMerged, Line: "Merged #3"}
	tests := []struct {
		name     string
		prev     *ChatPosted
		news     ChatNews
		baseline bool
		want     ChatPostStep
	}{
		{"a thread starts when its item needs a decision", nil, waiting, false, ChatPostStep{New: true}},
		{"and on the first pass too: it is live", nil, waiting, true, ChatPostStep{New: true}},
		{"no thread for an item that needs nothing", nil, moved, false, ChatPostStep{}},
		{"nor for one done before the Chat saw it", nil, done, true, ChatPostStep{}},
		{"the same text is not edited", posted, waiting, false, ChatPostStep{}},
		{"a phase change edits the root", posted, moved, false, ChatPostStep{Edit: true}},
		{"done edits it and adds ✅", posted, done, false, ChatPostStep{Edit: true, React: true}},
		{"a done thread is left alone", finished, done, false, ChatPostStep{}},
		{"a done item that needs a decision again starts a new thread", finished, waiting, false,
			ChatPostStep{New: true}},
		{"a plain post goes out once", nil, plain, false, ChatPostStep{New: true}},
		{"and never again", &ChatPosted{Room: ChatAlerts, Event: "$p"}, plain, false, ChatPostStep{}},
		{"the history is seen, not posted", nil, plain, true, ChatPostStep{Seen: true}},
		{"a digest line waits for the digest", nil, digest, false, ChatPostStep{Digest: true}},
		{"once in a digest, never again", &ChatPosted{Room: ChatUpdates, Event: "$d"}, digest, false, ChatPostStep{}},
		{"a digest line of the history is seen", nil, digest, true, ChatPostStep{Seen: true}},
	}
	for _, tt := range tests {
		got, err := ChatNextPost(tt.prev, tt.news, tt.baseline)
		if err != nil || got != tt.want {
			t.Errorf("%s: ChatNextPost = %+v, %v; want %+v", tt.name, got, err, tt.want)
		}
	}
	if _, err := ChatNextPost(nil, ChatNews{Event: "Unknown"}, false); err == nil {
		t.Error("news of no room is posted")
	}
}

// The updates digest batches its news into one message per window.
func TestChatDigest(t *testing.T) {
	at := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	window := 10 * time.Minute
	tests := []struct {
		name    string
		pending int
		last    time.Time
		now     time.Time
		want    bool
	}{
		{"nothing to say", 0, time.Time{}, at, false},
		{"the first goes out at once", 2, time.Time{}, at, true},
		{"within the window it waits", 3, at, at.Add(9 * time.Minute), false},
		{"at the window's end it goes", 3, at, at.Add(window), true},
		{"with no window, at once", 1, at, at, false},
	}
	for _, tt := range tests {
		w := window
		if tt.name == "with no window, at once" {
			w, tt.want = 0, true
		}
		if got := ChatDigestDue(tt.pending, tt.last, tt.now, w); got != tt.want {
			t.Errorf("%s: ChatDigestDue = %t", tt.name, got)
		}
	}
	news := []ChatNews{{Line: "Merged pull request #3 for issue #4"}, {Post: ChatPost{Title: "Brief shop round 2"}}}
	got := ChatDigest(news)
	if got.Title != "Updates (2)" || got.Text() != "Updates (2)\n\n• Merged pull request #3 for issue #4\n• Brief shop round 2" {
		t.Errorf("ChatDigest = %q", got.Text())
	}
	many := make([]ChatNews, chatDigestMax+5)
	for i := range many {
		many[i].Line = "x"
	}
	if text := ChatDigest(many).Text(); strings.Count(text, "• x") != chatDigestMax || !strings.HasSuffix(text, "and 5 more") {
		t.Errorf("a long digest:\n%s", text)
	}
}

func newsByEvent(news []ChatNews) map[ChatEventKind]ChatNews {
	out := map[ChatEventKind]ChatNews{}
	for _, n := range news {
		out[n.Event] = n
	}
	return out
}

// A Spec Task's thread: open while it waits, its item the head commit,
// edited as it moves and done once merged or failed.
func TestChatTaskNewsSpec(t *testing.T) {
	spec := ChatTaskView{Name: "api-4-spec", Kind: "Spec", Phase: "AwaitingApproval", Round: 1, Issue: 4,
		IssueTitle: "Export orders", PullRequest: 3, PullURL: "http://git/api/pulls/3", Commit: "def5678abc",
		SpecApproval: "Person"}
	n := newsByEvent(ChatTaskNews(spec))[ChatEventSpecWaiting]
	if n.Key != "SpecWaiting.api-4-spec" || !n.Open || n.Done || n.Item != (ChatItem{Event: ChatEventSpecWaiting,
		Name: "api-4-spec", Commit: "def5678abc"}) {
		t.Errorf("waiting: %+v", n)
	}
	if text := n.Post.Text(); !strings.Contains(text, "Spec of issue #4: Export orders") ||
		!strings.Contains(text, "Waits for your approval of commit def5678") ||
		!strings.Contains(text, "Pull request #3: http://git/api/pulls/3") {
		t.Errorf("waiting text:\n%s", text)
	}
	spec.Phase, spec.Round = "Implementing", 2
	n = newsByEvent(ChatTaskNews(spec))[ChatEventSpecWaiting]
	if n.Open || n.Done || n.Item.Commit != "" || n.Post.Status != "Round 2: Implementing." {
		t.Errorf("a new round: %+v", n)
	}
	spec.Phase, spec.ApprovedBy, spec.ApprovedInChat = "Merged", "ada@example.com", true
	news := newsByEvent(ChatTaskNews(spec))
	if n := news[ChatEventSpecWaiting]; !n.Done || n.Post.Status != "Merged: ada@example.com approved it in chat." {
		t.Errorf("merged: %+v", n)
	}
	if n := news[ChatEventMerged]; n.Line != "Merged the spec in pull request #3 for issue #4: Export orders" {
		t.Errorf("merged digest line: %+v", n)
	}
	spec.SpecApproval = "Reviewer"
	if _, ok := newsByEvent(ChatTaskNews(spec))[ChatEventSpecWaiting]; ok {
		t.Error("a spec the reviewer approves has a thread")
	}
}

// A Triage Task: a parked issue's question is a thread until answered; any
// other decision a post.
func TestChatTaskNewsTriage(t *testing.T) {
	parked := ChatTaskView{Name: "api-4-triage", Kind: "Triage", Phase: "Merged", Issue: 4,
		Triage: &ChatTriageView{Decision: TriagePark, Reason: "No target.", Question: "How fast?",
			Applied: "Labelled status/needs-human and asked its question"}}
	n := newsByEvent(ChatTaskNews(parked))[ChatEventTriageQuestion]
	if !n.Open || n.Done || n.Item != (ChatItem{Event: ChatEventTriageQuestion, Name: "api-4-triage"}) ||
		!strings.Contains(n.Post.Text(), "How fast?") {
		t.Errorf("parked: %+v", n)
	}
	parked.Triage.AnsweredBy = "ada"
	n = newsByEvent(ChatTaskNews(parked))[ChatEventTriageQuestion]
	if n.Open || !n.Done || !strings.Contains(n.Post.Status, "Answered by ada") {
		t.Errorf("answered: %+v", n)
	}
	parked.Triage = &ChatTriageView{Decision: TriagePark, Applied: "Issue #4 was triaged by a person meanwhile"}
	if news := ChatTaskNews(parked); len(news) != 1 || news[0].Event != ChatEventTriaged {
		t.Errorf("a park a person had overtaken asks nothing: %+v", news)
	}
	ready := ChatTaskView{Name: "api-5-triage", Kind: "Triage", Phase: "Merged", Issue: 5,
		Triage: &ChatTriageView{Decision: TriageDuplicate, Duplicate: 2, Reason: "Same."}}
	if n := newsByEvent(ChatTaskNews(ready))[ChatEventTriaged]; n.Post.Title != "Triaged issue #5: a duplicate of #2" {
		t.Errorf("duplicate: %+v", n)
	}
	failed := ChatTaskView{Name: "api-6", Kind: "Implement", Phase: "Failed", Issue: 6, Message: "Out of rounds"}
	if n := newsByEvent(ChatTaskNews(failed))[ChatEventTaskFailed]; n.Post.Status != "Out of rounds" {
		t.Errorf("failed: %+v", n)
	}
	judge := ChatTaskView{Name: "tests-api-6", Kind: "Score", Phase: "Failed", Message: "no verdicts"}
	if news := ChatTaskNews(judge); len(news) != 0 {
		t.Errorf("a Score Task's failure is news: %+v", news)
	}
	run := ChatTaskView{Name: "cmp-fast-g1-r1", Kind: "Implement", Phase: "Failed", Benchmark: true,
		ReviewVerdict: "changes"}
	if news := ChatTaskNews(run); len(news) != 0 {
		t.Errorf("a Benchmark's run is news: %+v", news)
	}
}

// A Container: a failed scaffold's thread, done when Ready; a deploy waiting
// for a person, done when it runs; releases and deploys in the digest.
func TestChatContainerNews(t *testing.T) {
	c := ChatContainerView{Name: "api", Phase: "Failed", Message: "two layers write Makefile", NewestRelease: "v1.2.0",
		Deploys: []ChatDeployState{{Target: "prod", Version: "v1.1.0", WantedVersion: "v1.2.0", Phase: "Waiting",
			Reason: DeployWaitingForApproval}, {Target: "staging", Version: "v1.2.0", WantedVersion: "v1.2.0",
			Phase: "Deployed"}}}
	news := ChatContainerNews(c)
	byKey := map[string]ChatNews{}
	for _, n := range news {
		byKey[n.Key] = n
	}
	if n := byKey["ContainerFailed.api"]; !n.Open || !strings.Contains(n.Post.Text(), "two layers write Makefile") {
		t.Errorf("failed: %+v", n)
	}
	if n := byKey["DeployWaiting.api.prod.v1.2.0"]; !n.Open || n.Item.Target != "prod" ||
		!strings.Contains(n.Post.Status, "say approve v1.2.0") {
		t.Errorf("waiting deploy: %+v", n)
	}
	if n := byKey["DeployWaiting.api.staging.v1.2.0"]; n.Open || !n.Done {
		t.Errorf("deployed: %+v", n)
	}
	if n := byKey["Deployed.api.staging.v1.2.0"]; n.Line != "api v1.2.0 runs at staging" {
		t.Errorf("deployed line: %+v", n)
	}
	if n := byKey["Released.api.v1.2.0"]; n.Line != "Container api released v1.2.0" {
		t.Errorf("released: %+v", n)
	}
	c.Phase = "Ready"
	if n := ChatContainerNews(c)[0]; n.Open || !n.Done || n.Post.Status != "Scaffolded: Ready." {
		t.Errorf("ready again: %+v", n)
	}
	if news := ChatBriefNews(ChatBriefRound{Name: "shop", Round: 2, Phase: "Planning"}); len(news) != 1 ||
		news[0].Line != "Brief shop is in round 2 (Planning)" || news[0].Key != "BriefRound.shop.2" {
		t.Errorf("brief: %+v", news)
	}
	if text := ChatStatus(ChatStatusCounts{Tasks: 3, TasksWaiting: 1, Containers: 2, ContainersReady: 1,
		Briefs: 1}).Text(); !strings.Contains(text, "3 Tasks at work, 1 waiting for a person, 0 failed") {
		t.Errorf("status:\n%s", text)
	}
}
