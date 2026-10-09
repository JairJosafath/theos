package model

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
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
