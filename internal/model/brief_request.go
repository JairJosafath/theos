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
// round (docs/plans/brief-requests.md). These are its rules: which issues are
// requests, the stage a request is in, and what the operator says on it.

// The modes of a releases entry's requests.
const (
	RequestsOff = "Off"
	RequestsAsk = "Ask"
	RequestsOn  = "On"
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

// RequestIssue is an open issue of a releases repository, as GitHub lists it.
type RequestIssue struct {
	Number int
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

// RequestName names a request in status.needsYou and status.roundRequests:
// owner/name#number.
func RequestName(repository string, number int) string {
	return fmt.Sprintf("%s#%d", repository, number)
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
	return fmt.Sprintf("<!-- c4factory:request brief=%s stage=%s -->", brief, stage)
}

// requestMarker reads a marker back: the Brief and the stage.
var requestMarker = regexp.MustCompile(`<!-- c4factory:request brief=(\S+) stage=(\S+) -->`)

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

// PlannedComment says what a taken request became: the titles of the issues
// a round filed for it and the release they are in, if any.
func PlannedComment(brief string, titles []string, release string) string {
	quoted := make([]string, 0, len(titles))
	for _, t := range titles {
		quoted = append(quoted, fmt.Sprintf("%q", t))
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

// ReleasedComment says which published release shipped a request; the issue
// is then closed as completed.
func ReleasedComment(brief, tag, url string) string {
	text := "Shipped in " + tag
	if url != "" {
		text += ": " + url
	}
	return requestComment(brief, CommentReleased, text)
}

// DoneComment says where a request was done when its issues landed in a
// Container that publishes nowhere: the issues' pages.
func DoneComment(brief string, urls []string) string {
	return requestComment(brief, CommentReleased, "Done in "+joinAnd(urls)+".")
}

// Request is a request as a Brief's status keeps it.
type Request struct {
	Repository string
	Number     int
	Author     string
	Title      string
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
}

// RequestsRead is a read of the open issues of a releases repository whose
// requests mode isn't Off.
type RequestsRead struct {
	Repository string
	Mode       string
	Authors    []string
	// Members are the logins found to be members of the entry's teams.
	Members []string
	// Unsure are the logins whose membership couldn't be read.
	Unsure   []string
	Accepted []int32
	Declined []int32
	// Open are the repository's open issues.
	Open []RequestIssue
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
		if r.Repository == read.Repository {
			mine[r.Number] = r
		}
	}
	var out []Request
	if read.Mode == RequestsOff || read.Mode == "" {
		for _, r := range known {
			if r.Repository == read.Repository && r.Round > 0 {
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
		case IsRequest(issue, read.Authors, read.Members):
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

// readRequest is an open request no round took yet. Its text is taken when
// it is let through, and kept from then on; one that waits keeps no body.
func readRequest(known *Request, read RequestsRead, issue RequestIssue, now time.Time) Request {
	n := int32(issue.Number)
	stage := RequestStage(RequestFacts{LetThrough: read.Mode == RequestsOn || slices.Contains(read.Accepted, n),
		Declined: slices.Contains(read.Declined, n)})
	r := Request{Repository: read.Repository, Number: issue.Number, Stage: stage, Since: now,
		Author: issue.Author, Title: Clip(oneParagraph(issue.Title), 256), URL: Clip(issue.URL, 300)}
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
		return a.Number - b.Number
	})
	return all[:min(len(all), MaxRequests)], len(all)
}

// RequestNeedsYou is the status.needsYou item of a request waiting in Ask:
// its title and author, and none of its body.
func RequestNeedsYou(r Request) NeedsYou {
	name := RequestName(r.Repository, r.Number)
	return NeedsYou{Kind: AttentionRequest, Name: name,
		Question:  fmt.Sprintf("Request %s %q by %s waits for you.", name, r.Title, r.Author),
		Recommend: fmt.Sprintf("Add %d to the accepted requests of %s to plan it, or to the declined ones.", r.Number, r.Repository),
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
