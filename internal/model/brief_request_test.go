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
	"time"
)

func TestIsRequest(t *testing.T) {
	authors := []string{"ada", "A-Colleague"}
	members := []string{"team-mate"}
	tests := []struct {
		name  string
		issue RequestIssue
		want  bool
	}{
		{"an allowed author's issue", RequestIssue{Number: 1, Author: "ada"}, true},
		{"an author in another case", RequestIssue{Number: 2, Author: "a-colleague"}, true},
		{"a team member's issue", RequestIssue{Number: 3, Author: "Team-Mate"}, true},
		{"a stranger's issue", RequestIssue{Number: 4, Author: "stranger"}, false},
		{"an allowed author's pull request", RequestIssue{Number: 5, Author: "ada", PullRequest: true}, false},
		{"no author (a deleted user)", RequestIssue{Number: 6}, false},
		{"an author whose login only starts like an allowed one", RequestIssue{Number: 7, Author: "ada2"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsRequest(tc.issue, authors, members); got != tc.want {
				t.Errorf("IsRequest = %v, want %v", got, tc.want)
			}
		})
	}
	if IsRequest(RequestIssue{Number: 1, Author: "ada"}, nil, nil) {
		t.Error("IsRequest with no authors or teams took an issue")
	}
}

func TestRequestBody(t *testing.T) {
	short := "Please add a dark theme. It hurts my eyes."
	if got := RequestBody("  " + short + "\n"); got != short {
		t.Errorf("RequestBody(short) = %q, want %q", got, short)
	}
	sentence := strings.Repeat("word ", 19) + "end. "
	long := strings.Repeat(sentence, 50)
	got := RequestBody(long)
	if len(got) > MaxRequestBody {
		t.Errorf("RequestBody kept %d bytes, want at most %d", len(got), MaxRequestBody)
	}
	if !strings.HasSuffix(got, "end.") {
		t.Errorf("RequestBody cut inside a sentence: ends %q", got[len(got)-20:])
	}
}

func TestRequestName(t *testing.T) {
	if got := RequestName("ada/cel-lens", 12); got != "ada/cel-lens#12" {
		t.Errorf("RequestName = %q", got)
	}
}

func TestRequestStage(t *testing.T) {
	tests := []struct {
		name  string
		facts RequestFacts
		want  string
	}{
		{"new in Ask", RequestFacts{}, RequestWaiting},
		{"new in On, or accepted in Ask", RequestFacts{LetThrough: true}, RequestAccepted},
		{"declined before a round", RequestFacts{Declined: true}, RequestDeclined},
		{"declined and accepted: decline wins", RequestFacts{LetThrough: true, Declined: true}, RequestDeclined},
		{"taken by a running round", RequestFacts{LetThrough: true, Round: 3}, RequestPlanning},
		{"declined after a round took it", RequestFacts{LetThrough: true, Declined: true, Round: 3}, RequestPlanning},
		{"skipped by an applied round", RequestFacts{LetThrough: true, Round: 3, Applied: true, Skipped: true},
			RequestSkipped},
		{"planned, issues open", RequestFacts{LetThrough: true, Round: 3, Applied: true, Issues: 2}, RequestPlanned},
		{"planned, issues closed, release not published",
			RequestFacts{LetThrough: true, Round: 3, Applied: true, Issues: 2, Done: true}, RequestPlanned},
		{"planned, release published, an issue open",
			RequestFacts{LetThrough: true, Round: 3, Applied: true, Issues: 2, Published: true}, RequestPlanned},
		{"issues closed and released",
			RequestFacts{LetThrough: true, Round: 3, Applied: true, Issues: 2, Done: true, Published: true},
			RequestReleased},
		{"applied with no issue", RequestFacts{LetThrough: true, Round: 3, Applied: true, Done: true, Published: true},
			RequestPlanned},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := RequestStage(tc.facts); got != tc.want {
				t.Errorf("RequestStage = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRequestCommentStage(t *testing.T) {
	want := map[string]string{
		RequestWaiting: "", RequestAccepted: "", RequestPlanning: "",
		RequestPlanned: CommentPlanned, RequestSkipped: CommentPlanned,
		RequestDeclined: CommentDeclined, RequestReleased: CommentReleased,
	}
	for stage, comment := range want {
		if got := RequestCommentStage(stage); got != comment {
			t.Errorf("RequestCommentStage(%s) = %q, want %q", stage, got, comment)
		}
	}
}

func TestRequestComments(t *testing.T) {
	tests := []struct {
		name string
		got  string
		want string
	}{
		{"planned in a release", PlannedComment("cel-lens", []string{"Add a dark theme"}, "v0.3.0"),
			"<!-- theos:request brief=cel-lens stage=planned -->\n" +
				"Planned as \"Add a dark theme\", in release v0.3.0.\n"},
		{"planned as several issues", PlannedComment("cel-lens",
			[]string{"Add a theme setting", "Theme the panel", "Theme the hover"}, "v0.3.0"),
			"<!-- theos:request brief=cel-lens stage=planned -->\n" +
				"Planned as \"Add a theme setting\", \"Theme the panel\" and \"Theme the hover\", in release v0.3.0.\n"},
		{"planned in no release", PlannedComment("cel-lens", []string{"Add a dark theme"}, ""),
			"<!-- theos:request brief=cel-lens stage=planned -->\nPlanned as \"Add a dark theme\".\n"},
		{"skipped", SkippedComment("cel-lens", "  The app\nalready has a dark theme. "),
			"<!-- theos:request brief=cel-lens stage=planned -->\nNot planned: The app already has a dark theme.\n"},
		{"skipped with no why", SkippedComment("cel-lens", ""),
			"<!-- theos:request brief=cel-lens stage=planned -->\nNot planned.\n"},
		{"declined", DeclinedComment("cel-lens"),
			"<!-- theos:request brief=cel-lens stage=declined -->\nNot taken by the app's owners.\n"},
		{"released", ReleasedComment("cel-lens", []Shipped{
			{Tag: "v0.3.0", URL: "https://github.com/ada/cel-lens/releases/tag/v0.3.0"}}),
			"<!-- theos:request brief=cel-lens stage=released -->\n" +
				"Shipped in v0.3.0: https://github.com/ada/cel-lens/releases/tag/v0.3.0\n"},
		{"released in two", ReleasedComment("cel-lens", []Shipped{{Tag: "v0.3.0", URL: "https://x/v0.3.0"},
			{Tag: "v1.0.0"}}),
			"<!-- theos:request brief=cel-lens stage=released -->\nShipped in v0.3.0: https://x/v0.3.0 and v1.0.0\n"},
		{"done where nothing publishes", ReleasedComment("cel-lens", []Shipped{
			{URL: "http://gitea/cel-lens/linter/issues/4"}}),
			"<!-- theos:request brief=cel-lens stage=released -->\nDone in http://gitea/cel-lens/linter/issues/4.\n"},
		{"done in two issues", ReleasedComment("cel-lens", []Shipped{{URL: "http://gitea/cel-lens/linter/issues/4"},
			{URL: "http://gitea/cel-lens/linter/issues/5"}}),
			"<!-- theos:request brief=cel-lens stage=released -->\n" +
				"Done in http://gitea/cel-lens/linter/issues/4 and http://gitea/cel-lens/linter/issues/5.\n"},
		{"shipped and done", ReleasedComment("cel-lens", []Shipped{{Tag: "v0.3.0", URL: "https://x/v0.3.0"},
			{URL: "http://gitea/cel-lens/server/issues/2"}}),
			"<!-- theos:request brief=cel-lens stage=released -->\n" +
				"Shipped in v0.3.0: https://x/v0.3.0\nDone in http://gitea/cel-lens/server/issues/2.\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("comment =\n%s\nwant\n%s", tc.got, tc.want)
			}
		})
	}
	long := SkippedComment("cel-lens", strings.Repeat("Out of scope. ", 100))
	if n := len(strings.TrimPrefix(long, RequestMarker("cel-lens", CommentPlanned)+"\nNot planned: ")); n > MaxAnswerWhy+1 {
		t.Errorf("SkippedComment kept a why of %d bytes, want at most %d", n-1, MaxAnswerWhy)
	}
}

func TestHasRequestComment(t *testing.T) {
	planned := PlannedComment("cel-lens", []string{"Add a dark theme"}, "v0.3.0")
	tests := []struct {
		name     string
		comments []string
		brief    string
		stage    string
		want     bool
	}{
		{"its own comment", []string{"Thanks!", planned}, "cel-lens", CommentPlanned, true},
		{"another stage", []string{planned}, "cel-lens", CommentReleased, false},
		{"another Brief's", []string{planned}, "other", CommentPlanned, false},
		{"a Brief whose name starts like it", []string{PlannedComment("cel-lens-2", nil, "")}, "cel-lens",
			CommentPlanned, false},
		{"a person's quote of the text, no marker", []string{"> Planned as \"Add a dark theme\""}, "cel-lens",
			CommentPlanned, false},
		{"no comments", nil, "cel-lens", CommentPlanned, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := HasRequestComment(tc.comments, tc.brief, tc.stage); got != tc.want {
				t.Errorf("HasRequestComment = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestReadRequests(t *testing.T) {
	repo := "ada/cel-lens"
	then := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	now := then.Add(time.Hour)
	mine := RequestIssue{Number: 1, Author: "ada", Title: "Dark theme", Body: "Please. Thanks.", URL: "u1"}
	edited := RequestIssue{Number: 1, Author: "ada", Title: "Edited", Body: "Edited.", URL: "u1"}
	mate := RequestIssue{Number: 2, Author: "team-mate", Title: "Light theme", Body: "Too.", URL: "u2"}
	stranger := RequestIssue{Number: 3, Author: "stranger", Title: "Spam", Body: "Spam."}
	read := func(mode string, open ...RequestIssue) RequestsRead {
		return RequestsRead{Repository: repo, Mode: mode, Authors: []string{"ada"}, Members: []string{"team-mate"},
			Open: open}
	}
	waiting := Request{Repository: repo, Number: 1, Author: "ada", Title: "Dark theme", URL: "u1",
		Stage: RequestWaiting, Since: then}
	accepted := Request{Repository: repo, Number: 1, Author: "ada", Title: "Dark theme", Body: "Please. Thanks.",
		URL: "u1", Stage: RequestAccepted, Since: then}
	taken := Request{Repository: repo, Number: 1, Author: "ada", Title: "Dark theme", Stage: RequestPlanning,
		Round: 2, Since: then}
	other := Request{Repository: "ada/other", Number: 1, Stage: RequestWaiting, Since: then}
	tests := []struct {
		name  string
		known []Request
		read  RequestsRead
		want  []Request
	}{
		{"Ask: a new request waits, without its body", nil, read(RequestsAsk, mine, stranger),
			[]Request{{Repository: repo, Number: 1, Author: "ada", Title: "Dark theme", URL: "u1",
				Stage: RequestWaiting, Since: now}}},
		{"On: a new request is let through with its body", nil, read(RequestsOn, mine),
			[]Request{{Repository: repo, Number: 1, Author: "ada", Title: "Dark theme", Body: "Please. Thanks.",
				URL: "u1", Stage: RequestAccepted, Since: now}}},
		{"a team member's issue is a request", nil, read(RequestsOn, mate),
			[]Request{{Repository: repo, Number: 2, Author: "team-mate", Title: "Light theme", Body: "Too.", URL: "u2",
				Stage: RequestAccepted, Since: now}}},
		{"a stranger's issue is nothing", nil, read(RequestsOn, stranger), nil},
		{"Ask: accepted takes the text then", []Request{waiting},
			func() RequestsRead { r := read(RequestsAsk, edited); r.Accepted = []int32{1}; return r }(),
			[]Request{{Repository: repo, Number: 1, Author: "ada", Title: "Edited", Body: "Edited.", URL: "u1",
				Stage: RequestAccepted, Since: then}}},
		{"an edit after it was let through changes nothing", []Request{accepted}, read(RequestsOn, edited),
			[]Request{accepted}},
		{"declined", []Request{waiting},
			func() RequestsRead { r := read(RequestsAsk, mine); r.Declined = []int32{1}; return r }(),
			[]Request{{Repository: repo, Number: 1, Author: "ada", Title: "Dark theme", URL: "u1",
				Stage: RequestDeclined, Since: then}}},
		{"closed before it was taken: dropped", []Request{accepted}, read(RequestsOn), nil},
		{"taken by a round: kept as it is", []Request{taken}, read(RequestsOn, edited), []Request{taken}},
		{"taken by a round and closed: dropped", []Request{taken}, read(RequestsOn), nil},
		{"an author no longer allowed: dropped",
			[]Request{accepted}, func() RequestsRead { r := read(RequestsOn, mine); r.Authors = nil; return r }(), nil},
		{"an author whose membership couldn't be read: kept",
			[]Request{accepted}, func() RequestsRead {
				r := read(RequestsOn, mine)
				r.Authors, r.Unsure = nil, []string{"Ada"}
				return r
			}(), []Request{accepted}},
		{"Off: only what a round took stays", []Request{waiting, taken, other}, read(RequestsOff, mine),
			[]Request{taken}},
		{"another repository's requests are not this read's", []Request{other}, read(RequestsAsk), nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ReadRequests(tc.known, tc.read, now); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ReadRequests =\n%+v\nwant\n%+v", got, tc.want)
			}
		})
	}
}

// TestSettledAnswers: an answered number whose issue is closed, and no
// request has, can go; an open issue's number, a request's and another
// repository's request don't count.
func TestSettledAnswers(t *testing.T) {
	repo := "ada/cel-lens"
	open := []RequestIssue{{Number: 5}, {Number: 9}}
	tests := []struct {
		name               string
		accepted, declined []int32
		requests           []Request
		want               []int32
	}{
		{"nothing answered", nil, nil, nil, nil},
		{"an open issue's number stays", []int32{5}, []int32{9}, nil, nil},
		{"a closed issue's numbers go, sorted, once", []int32{7, 1, 5}, []int32{3, 9}, nil, []int32{1, 3, 7}},
		{"a request's number stays", []int32{4}, nil, []Request{{Repository: repo, Number: 4}}, nil},
		{"another repository's request doesn't count", nil, []int32{4},
			[]Request{{Repository: "ada/other", Number: 4}}, []int32{4}},
		{"a number in both lists is listed once", []int32{2}, []int32{2}, nil, []int32{2}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			read := RequestsRead{Repository: repo, Mode: RequestsAsk, Accepted: tt.accepted, Declined: tt.declined,
				Open: open}
			if got := SettledAnswers(read, tt.requests); !slices.Equal(got, tt.want) {
				t.Errorf("SettledAnswers() = %v; want %v", got, tt.want)
			}
		})
	}
}

// TestSettledAnswersOver200: an entry that answered 200 requests whose issues
// closed has them all settled, so dropping them leaves room for the next.
func TestSettledAnswersOver200(t *testing.T) {
	read := RequestsRead{Repository: "o/r", Mode: RequestsAsk, Open: []RequestIssue{{Number: 200}}}
	for n := int32(1); n <= 200; n++ {
		read.Accepted = append(read.Accepted, n)
		read.Declined = append(read.Declined, 200+n)
	}
	if got := SettledAnswers(read, nil); len(got) != 399 || slices.Contains(got, 200) {
		t.Errorf("SettledAnswers() of 400 answers, one open = %d numbers; want 399 without 200", len(got))
	}
}

func TestListRequests(t *testing.T) {
	then := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	all := []Request{
		{Repository: "b/b", Number: 1, Since: then},
		{Repository: "a/a", Number: 2, Since: then},
		{Repository: "a/a", Number: 1, Since: then},
		{Repository: "a/a", Number: 9, Since: then.Add(-time.Hour)},
	}
	listed, count := ListRequests(all)
	got := make([]string, 0, len(listed))
	for _, r := range listed {
		got = append(got, RequestName(r.Repository, r.Number))
	}
	if want := []string{"a/a#9", "a/a#1", "a/a#2", "b/b#1"}; !reflect.DeepEqual(got, want) || count != 4 {
		t.Errorf("ListRequests = %v, %d; want %v, 4", got, count, want)
	}
	many := make([]Request, MaxRequests+3)
	for i := range many {
		many[i] = Request{Repository: "a/a", Number: i + 1, Since: then}
	}
	if listed, count := ListRequests(many); len(listed) != MaxRequests || count != MaxRequests+3 ||
		listed[MaxRequests-1].Number != MaxRequests {
		t.Errorf("ListRequests of %d = %d listed, %d counted", len(many), len(listed), count)
	}
}

func TestRequestNeedsYou(t *testing.T) {
	r := Request{Repository: "ada/cel-lens", Number: 12, Author: "ada", Title: "Dark theme",
		Body: "secret words", Since: time.Unix(5, 0)}
	n := RequestNeedsYou(r)
	if n.Kind != AttentionRequest || n.Name != "ada/cel-lens#12" || !n.Since.Equal(r.Since) {
		t.Errorf("RequestNeedsYou = %+v", n)
	}
	for _, s := range []string{n.Question, n.Recommend, n.Why} {
		if strings.Contains(s, "secret") {
			t.Errorf("RequestNeedsYou says the body: %q", s)
		}
	}
	if !strings.Contains(n.Question, `"Dark theme"`) || !strings.Contains(n.Question, "ada") ||
		!strings.Contains(n.Recommend, "12") {
		t.Errorf("RequestNeedsYou = %+v", n)
	}
}

func TestWithNeedsYou(t *testing.T) {
	at := func(s int64) time.Time { return time.Unix(s, 0) }
	a := Attention{NeedsYou: []NeedsYou{{Name: "x", Since: at(1)}, {Name: "z", Since: at(3)}}, Waiting: 2}
	got := WithNeedsYou(a, []NeedsYou{{Name: "y", Since: at(2)}})
	if names := []string{got.NeedsYou[0].Name, got.NeedsYou[1].Name, got.NeedsYou[2].Name}; !reflect.DeepEqual(names,
		[]string{"x", "y", "z"}) || got.Waiting != 3 {
		t.Errorf("WithNeedsYou = %+v", got)
	}
	if len(a.NeedsYou) != 2 {
		t.Error("WithNeedsYou changed its argument")
	}
	full := Attention{NeedsYou: make([]NeedsYou, MaxNeedsYou), Waiting: MaxNeedsYou + 4}
	got = WithNeedsYou(full, []NeedsYou{{Name: "late", Since: at(9)}})
	if len(got.NeedsYou) != MaxNeedsYou || got.Waiting != MaxNeedsYou+5 {
		t.Errorf("WithNeedsYou past the cap = %d listed, %d waiting", len(got.NeedsYou), got.Waiting)
	}
}

func TestRequestShipped(t *testing.T) {
	published := func(milestone, tag string) ShipIssue {
		return ShipIssue{URL: "http://gitea/linter/issues/1", Closed: true, Milestone: milestone, Publishes: true,
			LastTag: tag, LastPublished: true, LastURL: "https://github.com/o/r/releases/tag/" + tag}
	}
	nowhere := ShipIssue{URL: "http://gitea/server/issues/2", Closed: true}
	tests := []struct {
		name   string
		issues []ShipIssue
		want   []Shipped
		ok     bool
	}{
		{"no issues", nil, nil, false},
		{"published in its milestone", []ShipIssue{published("v0.3.0", "v0.3.0")},
			[]Shipped{{"v0.3.0", "https://github.com/o/r/releases/tag/v0.3.0"}}, true},
		{"held by a later release", []ShipIssue{published("v0.3.0", "v0.4.0")},
			[]Shipped{{"v0.4.0", "https://github.com/o/r/releases/tag/v0.4.0"}}, true},
		{"an earlier release doesn't hold it", []ShipIssue{published("v0.3.0", "v0.2.0")}, nil, false},
		{"an older major doesn't hold it", []ShipIssue{published("v1.0.0", "v0.9.0")}, nil, false},
		{"not published yet", []ShipIssue{func() ShipIssue {
			i := published("v0.3.0", "v0.3.0")
			i.LastPublished = false
			return i
		}()}, nil, false},
		{"still open", []ShipIssue{func() ShipIssue {
			i := published("v0.3.0", "v0.3.0")
			i.Closed = false
			return i
		}()}, nil, false},
		{"done where nothing publishes", []ShipIssue{nowhere}, []Shipped{{URL: "http://gitea/server/issues/2"}}, true},
		{"closed outside any release", []ShipIssue{published("", "v0.3.0")},
			[]Shipped{{URL: "http://gitea/linter/issues/1"}}, true},
		{"split over two Containers waits for both", []ShipIssue{published("v0.3.0", "v0.3.0"),
			{URL: "http://gitea/server/issues/2"}}, nil, false},
		{"split over two Containers, both done", []ShipIssue{nowhere, published("v0.3.0", "v0.3.0"),
			published("v0.3.0", "v0.3.0")},
			[]Shipped{{"v0.3.0", "https://github.com/o/r/releases/tag/v0.3.0"}, {URL: "http://gitea/server/issues/2"}},
			true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := RequestShipped(tc.issues)
			if ok != tc.ok || !slices.Equal(got, tc.want) {
				t.Errorf("RequestShipped = %v, %v; want %v, %v", got, ok, tc.want, tc.ok)
			}
		})
	}
}
