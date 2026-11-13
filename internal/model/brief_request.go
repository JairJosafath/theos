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
	"regexp"
	"slices"
	"strings"
	"time"
)

// A request is an open GitHub issue of a Brief's releases repository, opened
// by a person the Brief allows, that the Brief takes as input to a planning
// round (docs/plans/done/brief-requests.md). These are its rules: which issues are
// requests, the stage a request is in, and what the operator says on it.

// The modes of a releases entry's requests.
const (
	RequestsOff = "Off"
	RequestsAsk = "Ask"
	RequestsOn  = "On"
)

// The trackers requests come from, by the names the operator keys their
// adapters by (ports.IssueTrackers): a releases entry's GitHub repository's
// issues, and a spec.requestSources entry's Linear team or Jira project.
const (
	TrackerGitHub = "github"
	TrackerLinear = "linear"
	TrackerJira   = "jira"
)

// The stages of a request, as status.requests shows them.
const (
	// RequestWaiting waits for a person to accept or decline it, in Ask.
	RequestWaiting = "Waiting"
	// RequestAccepted is let through and waits for a round.
	RequestAccepted = "Accepted"
	// RequestPlanning is taken by a round not applied yet.
	RequestPlanning = "Planning"
	// RequestPlanned became issues of an applied round.
	RequestPlanned = "Planned"
	// RequestSkipped is one an applied round chose not to plan.
	RequestSkipped = "Skipped"
	// RequestDeclined is one a person turned down.
	RequestDeclined = "Declined"
	// RequestReleased has every issue it became closed and released.
	RequestReleased = "Released"
)

// AttentionRequest is the status.needsYou kind of a request waiting in Ask.
const AttentionRequest = "Request"

// MaxRequests is how many open requests a Brief lists in its status.
const MaxRequests = 50

// MaxRequestBody is how much of an issue's body a request keeps.
const MaxRequestBody = 4000

// MaxRequestAuthor and MaxRequestKey are the longest author and key a
// request keeps, as status.requests holds them.
const (
	MaxRequestAuthor = 100
	MaxRequestKey    = 120
)

// RequestIssue is an open issue of a releases repository, as GitHub lists it,
// or an open item of a request source that carries its label.
type RequestIssue struct {
	Number int
	// Key is the item's key on its tracker, such as ENG-412; a source's
	// request keeps it.
	Key    string
	Author string
	Title  string
	Body   string
	URL    string
	// PullRequest says it is a pull request: GitHub lists those as issues.
	PullRequest bool
}

// IsRequest reports whether an open issue is a request: an issue, not a pull
// request, whose author is in authors or among members, the logins of the
// members of the entry's teams. GitHub logins match whatever their case.
func IsRequest(issue RequestIssue, authors, members []string) bool {
	if issue.PullRequest || issue.Author == "" {
		return false
	}
	is := func(login string) bool { return strings.EqualFold(login, issue.Author) }
	return slices.ContainsFunc(authors, is) || slices.ContainsFunc(members, is)
}

// RequestBody is the part of an issue's body a request keeps: cut at
// MaxRequestBody on a whole sentence.
func RequestBody(body string) string {
	return ClipSentences(body, MaxRequestBody)
}

// RequestName names a GitHub request in status.needsYou and
// status.roundRequests: owner/name#number.
func RequestName(repository string, number int) string {
	return fmt.Sprintf("%s#%d", repository, number)
}

// SourceRequestName names a request from a spec.requestSources entry in
// status.needsYou and status.roundRequests: source:key, such as
// product:ENG-412. A source's name is a DNS label, which holds neither '/'
// nor '#', so it never equals a GitHub request's owner/name#number.
func SourceRequestName(source, key string) string {
	return source + ":" + key
}

// RequestFacts are what a request's stage follows from.
type RequestFacts struct {
	// LetThrough says it may reach a round: the mode is On, or its number is
	// in accepted.
	LetThrough bool
	// Declined says its number is in declined.
	Declined bool
	// Round is the round that took it; 0 before one did.
	Round int32
	// Applied says that round was applied.
	Applied bool
	// Skipped says the applied round chose not to plan it.
	Skipped bool
	// Issues is how many issues it became.
	Issues int
	// Done says every issue it became is closed.
	Done bool
	// Published says the releases that hold those issues are published, or
	// that their Container publishes nowhere.
	Published bool
}

// RequestStage is the stage a request is in. A person's decline counts until
// a round took the request; once one did, what was planned stays planned.
func RequestStage(f RequestFacts) string {
	switch {
	case f.Round == 0 && f.Declined:
		return RequestDeclined
	case f.Round == 0 && f.LetThrough:
		return RequestAccepted
	case f.Round == 0:
		return RequestWaiting
	case !f.Applied:
		return RequestPlanning
	case f.Skipped:
		return RequestSkipped
	case f.Issues > 0 && f.Done && f.Published:
		return RequestReleased
	default:
		return RequestPlanned
	}
}

// The stages the operator comments on an issue at, once each.
const (
	CommentPlanned  = "planned"
	CommentDeclined = "declined"
	CommentReleased = "released"
)

// RequestCommentStage is the comment a request in stage is owed: planned for
// Planned and Skipped, declined, released, or "" for a stage that says
// nothing on GitHub.
func RequestCommentStage(stage string) string {
	switch stage {
	case RequestPlanned, RequestSkipped:
		return CommentPlanned
	case RequestDeclined:
		return CommentDeclined
	case RequestReleased:
		return CommentReleased
	}
	return ""
}

// RequestMarker is the hidden line that starts each comment the operator
// posts on a request, so it finds its own before posting again.
func RequestMarker(brief, stage string) string {
	return fmt.Sprintf("<!-- theos:request brief=%s stage=%s -->", brief, stage)
}

// RequestRoundMarker is the hidden line that ends the planned comment of the
// round that answered a request. The Brief looks for it, not the stage's
// marker alone, so a request reopened on GitHub and answered by a later
// round hears that round's answer too.
func RequestRoundMarker(brief string, round int) string {
	return fmt.Sprintf("<!-- theos:request brief=%s round=%d -->", brief, round)
}

// requestMarker reads a marker back: the Brief and the stage.
var requestMarker = regexp.MustCompile(`<!-- theos:request brief=(\S+) stage=(\S+) -->`)

// HasRequestComment reports whether one of an issue's comments is the
// Brief's comment for stage.
func HasRequestComment(comments []string, brief, stage string) bool {
	for _, c := range comments {
		for _, m := range requestMarker.FindAllStringSubmatch(c, -1) {
			if m[1] == brief && m[2] == stage {
				return true
			}
		}
	}
	return false
}

// requestComment is a comment for stage: the marker, then the text.
func requestComment(brief, stage, text string) string {
	return RequestMarker(brief, stage) + "\n" + text + "\n"
}

// PlannedIssue is an issue a round filed for a request: its title and its
// page.
type PlannedIssue struct {
	Title string
	URL   string
}

// PlannedComment says what a taken request became: the issues a round filed
// for it, each by its title and its page (#161), and the release they are
// in, if any.
func PlannedComment(brief string, issues []PlannedIssue, release string) string {
	quoted := make([]string, 0, len(issues))
	for _, i := range issues {
		q := fmt.Sprintf("%q", i.Title)
		if i.URL != "" {
			q += " (" + i.URL + ")"
		}
		quoted = append(quoted, q)
	}
	text := "Planned as " + joinAnd(quoted)
	if release != "" {
		text += ", in release " + release
	}
	return requestComment(brief, CommentPlanned, text+".")
}

// SkippedComment says why a round chose not to plan a request; the issue is
// then closed as not planned.
func SkippedComment(brief, why string) string {
	why = ClipSentences(strings.Join(strings.Fields(why), " "), MaxAnswerWhy)
	if why == "" {
		return requestComment(brief, CommentPlanned, "Not planned.")
	}
	return requestComment(brief, CommentPlanned, "Not planned: "+why)
}

// DeclinedComment says a person turned a request down; the issue is then
// closed as not planned.
func DeclinedComment(brief string) string {
	return requestComment(brief, CommentDeclined, "Not taken by the app's owners.")
}

// Shipped is where a request's issues went out: a published release, by its
// tag and page, or with no tag the page of an issue done in a Container that
// publishes nowhere.
type Shipped struct {
	Tag string
	URL string
}

// Merged is the pull request that merged the work of one of the issues a
// request became: the issue's page and the pull request's.
type Merged struct {
	Issue string
	Pull  string
}

// ReleasedComment says which published releases shipped a request, where
// the issues done in a Container that publishes nowhere are, and the merged
// pull request of each issue that has one (#161); the issue is then closed
// as completed.
func ReleasedComment(brief string, shipped []Shipped, merged []Merged) string {
	var releases, done []string
	for _, s := range shipped {
		switch {
		case s.Tag == "":
			done = append(done, s.URL)
		case s.URL == "":
			releases = append(releases, s.Tag)
		default:
			releases = append(releases, s.Tag+": "+s.URL)
		}
	}
	var lines []string
	if len(releases) > 0 {
		lines = append(lines, "Shipped in "+joinAnd(releases))
	}
	if len(done) > 0 {
		lines = append(lines, "Done in "+joinAnd(done)+".")
	}
	var pulls []string
	for _, m := range merged {
		if m.Pull != "" {
			pulls = append(pulls, m.Pull+" for "+m.Issue)
		}
	}
	if len(pulls) > 0 {
		lines = append(lines, "Merged in "+joinAnd(pulls)+".")
	}
	return requestComment(brief, CommentReleased, strings.Join(lines, "\n"))
}

// MarkersLast moves the marker a request comment starts with to its end, for
// a tracker that shows no hidden text: there the comment reads as its words,
// and the markers as its last lines (decision 17 of
// docs/plans/requests-and-chat.md). A comment that starts with no marker is
// left as it is.
func MarkersLast(comment string) string {
	first, rest, ok := strings.Cut(comment, "\n")
	if !ok || !requestMarker.MatchString(first) || requestMarker.FindStringIndex(first)[0] != 0 {
		return comment
	}
	if !strings.HasSuffix(rest, "\n") {
		rest += "\n"
	}
	return rest + first + "\n"
}

// RequestReleasedMarker is the hidden line that ends the released comment of
// the round that planned a request, so a request reopened and planned again
// hears when that round's work ships too.
func RequestReleasedMarker(brief string, round int) string {
	return fmt.Sprintf("<!-- theos:request brief=%s released=%d -->", brief, round)
}

// ShipIssue is what the Brief knows of one issue a request became, to tell
// whether it shipped.
type ShipIssue struct {
	// URL is the issue's page.
	URL    string
	Closed bool
	// Milestone is the title of the issue's milestone, its release.
	Milestone string
	// Publishes says the issue's Container publishes its releases.
	Publishes bool
	// LastTag is the Container's latest release, LastPublished whether it is
	// published, and LastURL its published page.
	LastTag       string
	LastPublished bool
	LastURL       string
}

// RequestShipped is where a planned request shipped, once every issue it
// became is closed and, for each in a Container that publishes, a published
// release holds it: its milestone's, or a later one, which holds every
// earlier release's work. An issue in a Container that publishes nowhere, or
// closed outside any release, is done when it closes. It reports false while
// one is still to go.
func RequestShipped(issues []ShipIssue) ([]Shipped, bool) {
	if len(issues) == 0 {
		return nil, false
	}
	var releases, done []Shipped
	for _, i := range issues {
		if !i.Closed {
			return nil, false
		}
		if !i.Publishes || i.Milestone == "" {
			if !slices.ContainsFunc(done, func(s Shipped) bool { return s.URL == i.URL }) {
				done = append(done, Shipped{URL: i.URL})
			}
			continue
		}
		if !i.LastPublished || !ReleaseHolds(i.LastTag, i.Milestone) {
			return nil, false
		}
		if !slices.ContainsFunc(releases, func(s Shipped) bool { return s.Tag == i.LastTag }) {
			releases = append(releases, Shipped{Tag: i.LastTag, URL: i.LastURL})
		}
	}
	return append(releases, done...), true
}

// ReleaseHolds reports whether release tag holds the work of milestone: it
// is that release, or a later version.
func ReleaseHolds(tag, milestone string) bool {
	if tag == milestone {
		return true
	}
	t, m := parseVersion(tag), parseVersion(milestone)
	return t != nil && m != nil && compareVersions(t, m) > 0
}

// Request is a request as a Brief's status keeps it: a GitHub issue of
// Repository, or an item of the spec.requestSources entry Source, whose key
// on its tracker is Key (ENG-412) and whose Number is the key's digits.
type Request struct {
	Repository string
	Source     string
	Key        string
	Number     int
	// Author is the GitHub login, or the display name of whoever made a
	// source's item; empty when the tracker names nobody.
	Author string
	Title  string
	// Body is the issue's body when it was let through; empty while it waits.
	Body  string
	URL   string
	Stage string
	// Round is the round that took it; 0 before one did.
	Round int
	// Since is when the Brief first saw it.
	Since time.Time
	// Issues are the issues the round that took it filed for it,
	// container#number, and Release the release they are in; Why is the
	// round's reason when it skipped it.
	Issues  []string
	Release string
	Why     string
	// Shipped is where it went out, once Released.
	Shipped []Shipped
}

// Name names r in status.needsYou and status.roundRequests: source:key for
// a source's item, owner/name#number for a GitHub issue.
func (r Request) Name() string {
	if r.Source != "" {
		return SourceRequestName(r.Source, r.Key)
	}
	return RequestName(r.Repository, r.Number)
}

// RequestsRead is a read of the open issues of a releases repository, or of
// the labelled items of a request source, whose requests mode isn't Off.
type RequestsRead struct {
	Repository string
	// Source is the request source read, for which Repository is empty.
	Source  string
	Mode    string
	Authors []string
	// Members are the logins found to be members of the entry's teams.
	Members []string
	// Unsure are the logins whose membership couldn't be read.
	Unsure   []string
	Accepted []int32
	Declined []int32
	// Open are the repository's open issues, or the source's labelled items.
	Open []RequestIssue
}

// owns reports whether r is a request of what read reads.
func (read RequestsRead) owns(r Request) bool {
	return r.Repository == read.Repository && r.Source == read.Source
}

// admits reports whether an open issue of the read is a request. A source's
// tracker lists only the items that carry its label, and the label is the
// door (decision 13 of docs/plans/requests-and-chat.md): each is a request,
// whoever made it, with an author or none. A GitHub issue is one by
// IsRequest.
func (read RequestsRead) admits(issue RequestIssue) bool {
	if read.Source != "" {
		return !issue.PullRequest
	}
	return IsRequest(issue, read.Authors, read.Members)
}

// ReadRequests is what a read of its repository makes of the Brief's
// requests from it, known as its status keeps them (decisions 2, 5 and 6):
// each open issue of an allowed author is one, Waiting, Accepted or
// Declined; one a round took stays as it is; one closed on GitHub is
// dropped, and so is one whose author is no longer allowed before a round
// took it. A request whose author's membership couldn't be read stays as it
// was. A mode of Off reads nothing: only what a round took stays.
func ReadRequests(known []Request, read RequestsRead, now time.Time) []Request {
	mine := map[int]Request{}
	for _, r := range known {
		if read.owns(r) {
			mine[r.Number] = r
		}
	}
	var out []Request
	if read.Mode == RequestsOff || read.Mode == "" {
		for _, r := range known {
			if read.owns(r) && r.Round > 0 {
				out = append(out, r)
			}
		}
		return out
	}
	for _, issue := range read.Open {
		prev, seen := mine[issue.Number]
		switch {
		case seen && prev.Round > 0:
			out = append(out, prev)
		case read.admits(issue):
			var p *Request
			if seen {
				p = &prev
			}
			out = append(out, readRequest(p, read, issue, now))
		case seen && slices.ContainsFunc(read.Unsure, func(l string) bool { return strings.EqualFold(l, issue.Author) }):
			out = append(out, prev)
		}
	}
	return out
}

// SettledAnswers are the numbers in an entry's accepted and declined that
// no longer do anything: their issues are closed on GitHub, or never were an
// open issue, as the last full read of the repository saw it, and no request
// of the repository has them (#824). A person may drop them from the spec, and
// the UI does when it answers a request, so the lists stay under the CRD's
// cap. A settled number dropped and then reopened on GitHub is a new request,
// asked again in Ask. Sorted, each once.
func SettledAnswers(read RequestsRead, requests []Request) []int32 {
	live := map[int32]bool{}
	for _, issue := range read.Open {
		live[int32(issue.Number)] = true
	}
	for _, r := range requests {
		if read.owns(r) {
			live[int32(r.Number)] = true
		}
	}
	var out []int32
	for _, n := range slices.Concat(read.Accepted, read.Declined) {
		if !live[n] {
			out = append(out, n)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// readRequest is an open request no round took yet. Its text is taken when
// it is let through, and kept from then on; one that waits keeps no body.
func readRequest(known *Request, read RequestsRead, issue RequestIssue, now time.Time) Request {
	n := int32(issue.Number)
	stage := RequestStage(RequestFacts{LetThrough: read.Mode == RequestsOn || slices.Contains(read.Accepted, n),
		Declined: slices.Contains(read.Declined, n)})
	r := Request{Repository: read.Repository, Number: issue.Number, Stage: stage, Since: now,
		Author: Clip(oneParagraph(issue.Author), MaxRequestAuthor), Title: Clip(oneParagraph(issue.Title), 256),
		URL: Clip(issue.URL, 300)}
	if read.Source != "" {
		r.Source, r.Key = read.Source, Clip(issue.Key, MaxRequestKey)
	}
	if known != nil {
		r.Since = known.Since
	}
	switch {
	case known != nil && stage != RequestWaiting && (known.Stage == RequestAccepted || known.Stage == stage):
		// Let through before: the text it was let through with.
		r.Author, r.Title, r.Body, r.URL = known.Author, known.Title, known.Body, known.URL
	case stage == RequestAccepted:
		r.Body = RequestBody(issue.Body)
	}
	return r
}

// ListRequests orders a Brief's requests oldest first, by repository and
// number when they were first seen together, and returns the MaxRequests its
// status lists and how many there are.
func ListRequests(all []Request) ([]Request, int) {
	all = slices.Clone(all)
	slices.SortStableFunc(all, func(a, b Request) int {
		if c := a.Since.Compare(b.Since); c != 0 {
			return c
		}
		if c := strings.Compare(a.Repository, b.Repository); c != 0 {
			return c
		}
		if c := strings.Compare(a.Source, b.Source); c != 0 {
			return c
		}
		return a.Number - b.Number
	})
	return all[:min(len(all), MaxRequests)], len(all)
}

// RequestNeedsYou is the status.needsYou item of a request waiting in Ask:
// its title and author, and none of its body. A source's item that names no
// author is named without one (#160).
func RequestNeedsYou(r Request) NeedsYou {
	name := r.Name()
	question := fmt.Sprintf("Request %s %q waits for you.", name, r.Title)
	if r.Author != "" {
		question = fmt.Sprintf("Request %s %q by %s waits for you.", name, r.Title, r.Author)
	}
	answers := r.Repository
	if r.Source != "" {
		answers = "request source " + r.Source
	}
	return NeedsYou{Kind: AttentionRequest, Name: name, Question: question,
		Recommend: fmt.Sprintf("Add %d to the accepted requests of %s to plan it, or to the declined ones.", r.Number, answers),
		Why:       "In Ask, a request reaches the planner only once a person lets it through.",
		Since:     r.Since}
}

// WithNeedsYou adds more items to a's Needs you, oldest first, listing at
// most MaxNeedsYou and counting them all.
func WithNeedsYou(a Attention, more []NeedsYou) Attention {
	if len(more) == 0 {
		return a
	}
	a.NeedsYou = append(slices.Clone(a.NeedsYou), more...)
	slices.SortStableFunc(a.NeedsYou, func(x, y NeedsYou) int { return x.Since.Compare(y.Since) })
	a.Waiting += len(more)
	a.NeedsYou = a.NeedsYou[:min(len(a.NeedsYou), MaxNeedsYou)]
	return a
}

// InsteadOf adds more to a's Needs you entries in place of any of the same
// kind and name, so each item is listed once.
func InsteadOf(a Attention, more []NeedsYou) Attention {
	if len(more) == 0 {
		return a
	}
	kept := make([]NeedsYou, 0, len(a.NeedsYou))
	for _, n := range a.NeedsYou {
		if slices.ContainsFunc(more, func(m NeedsYou) bool { return m.Kind == n.Kind && m.Name == n.Name }) {
			a.Waiting--
			continue
		}
		kept = append(kept, n)
	}
	a.NeedsYou = kept
	return WithNeedsYou(a, more)
}
