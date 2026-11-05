package model

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// theos-chat tells a Chat's people what the factory does
// (docs/plans/done/chat-matrix.md, "The rooms"): it reads theos's objects, makes
// each thing worth saying a ChatNews with a key of its own, and posts it as
// ChatRouteFor says: in a digest, as a plain post, or as a thread whose
// root it edits as the item moves and marks ✅ once it is done. What it
// posted it keeps under the news's key (ChatPosted), so nothing is said
// twice, also across restarts.

// ChatNews is one thing theos-chat tells a Chat's people.
type ChatNews struct {
	// Key names the news among a namespace's, the same on every pass
	// (ChatItemKey).
	Key   string
	Event ChatEventKind
	// Post is the message, or a thread's root as it reads now.
	Post ChatPost
	// Line is the news in one line, for the updates digest.
	Line string
	// Open means a thread's item needs a person's decision now: its thread
	// is started, or started anew when the last one was done.
	Open bool
	// Done means a thread's item ended: its root gets ✅.
	Done bool
	// Item is what a thread is about, for the commands said in it.
	Item ChatItem
}

// chatKeyUnsafe are the characters a ConfigMap key can't hold.
var chatKeyUnsafe = regexp.MustCompile(`[^-._a-zA-Z0-9]`)

// chatMaxKey bounds a key, under a ConfigMap key's 253 characters with
// theos-chat's prefix.
const chatMaxKey = 200

// ChatItemKey is the key of news of kind event about parts (an object's
// name, a target, a version): event and parts joined by dots, characters a
// ConfigMap key can't hold as _, and a long key cut and told apart by a
// hash of the whole.
func ChatItemKey(event ChatEventKind, parts ...string) string {
	key := chatKeyUnsafe.ReplaceAllString(strings.Join(append([]string{string(event)}, parts...), "."), "_")
	if len(key) <= chatMaxKey {
		return key
	}
	sum := sha256.Sum256([]byte(key))
	return key[:chatMaxKey-17] + "." + hex.EncodeToString(sum[:8])
}

// ChatHash tells the texts of two posts apart, so a root is edited only when
// its text changed.
func ChatHash(p ChatPost) string {
	sum := sha256.Sum256([]byte(p.Text() + "\x00" + p.HTML()))
	return hex.EncodeToString(sum[:8])
}

// ChatPosted is what theos-chat posted of one news item, kept in its
// namespace's ConfigMap under the news's key.
type ChatPosted struct {
	// Room is where it was posted.
	Room ChatPurpose `json:"room"`
	// Event is the event ID of the post, the thread's root or the digest it
	// went out in; "" for news seen before the Chat's first pass, never
	// posted.
	Event string `json:"event,omitempty"`
	// Hash is ChatHash of the text last posted.
	Hash string `json:"hash,omitempty"`
	// Done means the thread's item ended and its root has ✅.
	Done bool `json:"done,omitempty"`
	// Item is what the thread is about, for its commands.
	Item ChatItem `json:"item,omitzero"`
}

// ChatPostStep is what theos-chat does for one news item.
type ChatPostStep struct {
	// New posts it, or starts its thread.
	New bool
	// Edit replaces its thread's root with the news's text.
	Edit bool
	// React puts ✅ on its thread's root.
	React bool
	// Digest has it wait for the updates digest.
	Digest bool
	// Seen records it as said without a post: a Chat's first pass doesn't
	// tell the history.
	Seen bool
}

// ChatDoneReaction marks a thread whose item is done.
const ChatDoneReaction = "✅"

// ChatNextPost decides what theos-chat does for news n, given what it
// posted of it before (nil for nothing). On the Chat's first pass
// (baseline) the history is recorded as seen and only live threads are
// started. A digest or plain news item is posted once. A thread starts when
// its item needs a decision; while it is open its root follows the item's
// text, and it gets ✅ once the item is done; an item that needs a decision
// again after that gets a new thread.
func ChatNextPost(prev *ChatPosted, n ChatNews, baseline bool) (ChatPostStep, error) {
	route, err := ChatRouteFor(n.Event)
	if err != nil {
		return ChatPostStep{}, err
	}
	switch {
	case !route.Thread && prev != nil:
		return ChatPostStep{}, nil
	case !route.Thread && baseline:
		return ChatPostStep{Seen: true}, nil
	case route.Digest:
		return ChatPostStep{Digest: true}, nil
	case !route.Thread:
		return ChatPostStep{New: true}, nil
	case prev == nil || prev.Done || prev.Event == "":
		return ChatPostStep{New: n.Open}, nil
	}
	return ChatPostStep{Edit: ChatHash(n.Post) != prev.Hash, React: n.Done}, nil
}

// ChatDigestDue reports whether the updates digest posts now: news waits
// for it, and window has passed since the last one (or there was none).
func ChatDigestDue(pending int, last, now time.Time, window time.Duration) bool {
	return pending > 0 && (last.IsZero() || !now.Before(last.Add(window)))
}

// chatDigestMax is how many lines one digest lists.
const chatDigestMax = 30

// ChatDigest is the updates digest of pending, one line each in their order,
// at most chatDigestMax of them and a count of the rest.
func ChatDigest(pending []ChatNews) ChatPost {
	lines := make([]string, 0, min(len(pending), chatDigestMax)+1)
	for i, n := range pending {
		if i == chatDigestMax {
			lines = append(lines, fmt.Sprintf("and %d more", len(pending)-chatDigestMax))
			break
		}
		line := strings.TrimSpace(n.Line)
		if line == "" {
			line = strings.TrimSpace(n.Post.Title)
		}
		lines = append(lines, "• "+line)
	}
	return ChatPost{Title: fmt.Sprintf("Updates (%d)", len(pending)), Body: []string{strings.Join(lines, "\n")}}
}

// The kinds and phases of a Task as the API names them.
const (
	chatKindImplement = "Implement"
	chatKindSpec      = "Spec"
	chatKindTriage    = "Triage"
	chatKindPlan      = "Plan"
	chatPhaseWaiting  = "AwaitingApproval"
	chatPhaseMerged   = "Merged"
	chatPhaseFailed   = "Failed"
)

// ChatTaskView is a Task as theos-chat reads it.
type ChatTaskView struct {
	Name  string
	Kind  string
	Phase string
	Round int
	// Issue, its title and address; the pull request and its address.
	Issue       int
	IssueTitle  string
	IssueURL    string
	PullRequest int
	PullURL     string
	// Commit is the round's head.
	Commit string
	// SpecApproval is a Spec Task's Person or Reviewer; ApprovedBy and
	// ApprovedInChat who approved its spec.
	SpecApproval   string
	ApprovedBy     string
	ApprovedInChat bool
	// Triage is a Triage Task's decision, nil before it has one.
	Triage *ChatTriageView
	// ReviewVerdict and ReviewSummary are the round's review.
	ReviewVerdict string
	ReviewSummary string
	// Message says why the Task waits, or why it failed.
	Message string
	// PageURL is the Task's page in the UI (ChatPageURL), "" for none.
	PageURL string
}

// ChatTriageView is a Triage Task's decision and what came of it.
type ChatTriageView struct {
	Decision, Reason, Question string
	Duplicate                  int
	Applied                    string
	AnsweredBy                 string
}

// TriageAsked reports whether a park decision's question went to its issue:
// PlanTriage applied it, rather than finding a person had triaged it.
func TriageAsked(applied string) bool {
	return strings.HasPrefix(applied, "Labelled "+LabelNeedsHuman)
}

// chatLinks are the links of a Task's posts: its pull request, its issue
// and its page in the UI, where it has them.
func (t ChatTaskView) chatLinks() []ChatLink {
	var links []ChatLink
	if t.PullURL != "" {
		links = append(links, ChatLink{Text: fmt.Sprintf("Pull request #%d", t.PullRequest), URL: t.PullURL})
	}
	if t.IssueURL != "" {
		links = append(links, ChatLink{Text: fmt.Sprintf("Issue #%d", t.Issue), URL: t.IssueURL})
	}
	return append(links, chatPageLink("Task", t.PageURL)...)
}

// issue is "issue #N: title", or the Task's name without an issue.
func (t ChatTaskView) issue() string {
	if t.Issue == 0 {
		return "Task " + t.Name
	}
	if title := strings.TrimSpace(t.IssueTitle); title != "" {
		return fmt.Sprintf("issue #%d: %s", t.Issue, title)
	}
	return fmt.Sprintf("issue #%d", t.Issue)
}

// ChatTaskNews is what theos-chat says of Task t: a Spec Task waiting for a
// person's approval opens a thread in specs (its commit is what approve
// approves); a parked issue's question opens one in triage; another triage
// decision, a review's verdict and a failure are plain posts; a merge is in
// the updates digest.
func ChatTaskNews(t ChatTaskView) []ChatNews {
	if t.Kind == "Score" {
		// A judge run for a Scorer is the factory measuring itself: its
		// verdicts are the Scorer's to show, and its failure no alert.
		return nil
	}
	var news []ChatNews
	final := t.Phase == chatPhaseMerged || t.Phase == chatPhaseFailed
	if t.Kind == chatKindSpec && t.SpecApproval != "Reviewer" {
		news = append(news, chatSpecNews(t, final))
	}
	if t.Kind == chatKindTriage && t.Triage != nil && t.Phase == chatPhaseMerged {
		news = append(news, chatTriageNews(t))
	}
	if t.ReviewVerdict != "" && t.Kind != chatKindTriage && t.Kind != chatKindPlan {
		news = append(news, ChatNews{Key: ChatItemKey(ChatEventReviewVerdict, t.Name, fmt.Sprint(t.Round)),
			Event: ChatEventReviewVerdict, Post: ChatPost{
				Title: fmt.Sprintf("Review of %s, round %d: %s", t.issue(), t.Round, t.ReviewVerdict),
				Body:  []string{t.ReviewSummary},
				Links: t.chatLinks(),
			}})
	}
	switch {
	case t.Phase == chatPhaseMerged && t.PullRequest != 0 && (t.Kind == chatKindImplement || t.Kind == chatKindSpec ||
		t.Kind == ""):
		what := "Merged pull request #%d for " + t.issue()
		if t.Kind == chatKindSpec {
			what = "Merged the spec in pull request #%d for " + t.issue()
		}
		line := fmt.Sprintf(what, t.PullRequest)
		news = append(news, ChatNews{Key: ChatItemKey(ChatEventMerged, t.Name), Event: ChatEventMerged, Line: line,
			Post: ChatPost{Title: line, Links: t.chatLinks()}})
	case t.Phase == chatPhaseFailed:
		news = append(news, ChatNews{Key: ChatItemKey(ChatEventTaskFailed, t.Name), Event: ChatEventTaskFailed,
			Post: ChatPost{Title: fmt.Sprintf("Task %s on %s failed", t.Name, t.issue()), Status: t.Message,
				Links: t.chatLinks()}})
	}
	return news
}

// chatSpecNews is a Spec Task's thread in specs.
func chatSpecNews(t ChatTaskView, final bool) ChatNews {
	n := ChatNews{Key: ChatItemKey(ChatEventSpecWaiting, t.Name), Event: ChatEventSpecWaiting,
		Open: t.Phase == chatPhaseWaiting, Done: final, Item: ChatItem{Event: ChatEventSpecWaiting, Name: t.Name}}
	status := fmt.Sprintf("Round %d: %s.", max(t.Round, 1), t.Phase)
	switch t.Phase {
	case chatPhaseWaiting:
		n.Item.Commit = t.Commit
		status = fmt.Sprintf("Waits for your approval of commit %s: say approve in this thread, or changes: "+
			"<what to change>.", shortCommit(t.Commit))
	case chatPhaseMerged:
		status = "Merged."
		if t.ApprovedBy != "" {
			where := ""
			if t.ApprovedInChat {
				where = " in chat"
			}
			status = fmt.Sprintf("Merged: %s approved it%s.", t.ApprovedBy, where)
		}
	case chatPhaseFailed:
		status = "Failed: " + orUnknown(t.Message)
	}
	n.Post = ChatPost{Title: "Spec of " + t.issue(), Status: status, Links: t.chatLinks()}
	return n
}

// chatTriageNews is a Triage Task's decision: a parked issue's question as a
// thread in triage, any other decision as a post there.
func chatTriageNews(t ChatTaskView) ChatNews {
	d := t.Triage
	links := t.chatLinks()
	if d.Decision == TriagePark && TriageAsked(d.Applied) {
		n := ChatNews{Key: ChatItemKey(ChatEventTriageQuestion, t.Name), Event: ChatEventTriageQuestion,
			Open: d.AnsweredBy == "", Done: d.AnsweredBy != "",
			Item: ChatItem{Event: ChatEventTriageQuestion, Name: t.Name}}
		status := "Answer in this thread: your words go to the issue, which is then triaged again."
		if d.AnsweredBy != "" {
			status = fmt.Sprintf("Answered by %s; the issue is triaged again.", d.AnsweredBy)
		}
		n.Post = ChatPost{Title: "A question on " + t.issue(), Status: status,
			Body: []string{d.Question, "Why: " + d.Reason}, Links: links}
		return n
	}
	words := map[string]string{TriageReady: "ready to build", TriageSpec: "needs a spec",
		TriagePark: "parked", TriageDuplicate: fmt.Sprintf("a duplicate of #%d", d.Duplicate)}[d.Decision]
	if words == "" {
		words = d.Decision
	}
	return ChatNews{Key: ChatItemKey(ChatEventTriaged, t.Name), Event: ChatEventTriaged, Post: ChatPost{
		Title: fmt.Sprintf("Triaged %s: %s", t.issue(), words), Status: d.Applied, Body: []string{d.Reason},
		Links: links}}
}

// ChatContainerView is a Container as theos-chat reads it.
type ChatContainerView struct {
	Name          string
	Phase         string
	Message       string
	RepositoryURL string
	NewestRelease string
	Deploys       []ChatDeployState
	// PageURL is the Container's page in the UI (ChatPageURL), "" for none.
	PageURL string
}

// ChatDeployState is one of a Container's status.deploys.
type ChatDeployState struct {
	Target, Version, WantedVersion, Phase, Reason, Message, URL string
}

// ChatContainerNews is what theos-chat says of Container c: a failed
// scaffold opens a thread in alerts, where retry scaffolds it again, done
// once it is Ready; a deploy waiting for a person opens one in reviews,
// done once the version runs; a failed deploy is a post in alerts; a
// release and a deploy are in the updates digest. Each links to the
// Container's page in the UI, when it has one.
func ChatContainerNews(c ChatContainerView) []ChatNews {
	var links []ChatLink
	if c.RepositoryURL != "" {
		links = append(links, ChatLink{Text: "Repository", URL: c.RepositoryURL})
	}
	links = append(links, chatPageLink("Container", c.PageURL)...)
	failed := ChatNews{Key: ChatItemKey(ChatEventContainerFailed, c.Name), Event: ChatEventContainerFailed,
		Open: c.Phase == chatPhaseFailed, Done: c.Phase == "Ready",
		Item: ChatItem{Event: ChatEventContainerFailed, Name: c.Name}}
	status := "Phase " + orUnknown(c.Phase) + "."
	switch c.Phase {
	case chatPhaseFailed:
		status = "Say retry in this thread to scaffold it again."
	case "Ready":
		status = "Scaffolded: Ready."
	}
	failed.Post = ChatPost{Title: "Container " + c.Name + " failed to scaffold", Status: status,
		Body: []string{c.Message}, Links: links}
	if !failed.Open {
		failed.Post.Body = nil
	}
	news := []ChatNews{failed}
	if c.NewestRelease != "" {
		line := fmt.Sprintf("Container %s released %s", c.Name, c.NewestRelease)
		news = append(news, ChatNews{Key: ChatItemKey(ChatEventReleased, c.Name, c.NewestRelease),
			Event: ChatEventReleased, Line: line, Post: ChatPost{Title: line, Links: links}})
	}
	for _, d := range c.Deploys {
		news = append(news, chatDeployNews(c.Name, c.PageURL, d)...)
	}
	return news
}

// chatDeployNews is what theos-chat says of one of Container name's deploys,
// with links to the deploy's address and the Container's page in the UI.
func chatDeployNews(name, pageURL string, d ChatDeployState) []ChatNews {
	var links []ChatLink
	if d.URL != "" {
		links = append(links, ChatLink{Text: d.Target, URL: d.URL})
	}
	links = append(links, chatPageLink("Container", pageURL)...)
	var news []ChatNews
	if d.WantedVersion != "" {
		n := ChatNews{Key: ChatItemKey(ChatEventDeployWaiting, name, d.Target, d.WantedVersion),
			Event: ChatEventDeployWaiting, Open: d.Reason == DeployWaitingForApproval,
			Done: d.Phase == "Deployed" && d.Version == d.WantedVersion,
			Item: ChatItem{Event: ChatEventDeployWaiting, Name: name, Target: d.Target}}
		status := fmt.Sprintf("%s: %s", orUnknown(d.Phase), d.Message)
		switch {
		case n.Open:
			status = fmt.Sprintf("Waits for a person: say approve %s in this thread to let it in.", d.WantedVersion)
		case n.Done:
			status = "Deployed."
		}
		n.Post = ChatPost{Title: fmt.Sprintf("%s %s to %s", name, d.WantedVersion, d.Target), Status: status,
			Links: links}
		news = append(news, n)
	}
	switch {
	case d.Phase == "Deployed" && d.Version != "":
		line := fmt.Sprintf("%s %s runs at %s", name, d.Version, d.Target)
		news = append(news, ChatNews{Key: ChatItemKey(ChatEventDeployed, name, d.Target, d.Version),
			Event: ChatEventDeployed, Line: line, Post: ChatPost{Title: line, Links: links}})
	case d.Phase == chatPhaseFailed:
		news = append(news, ChatNews{Key: ChatItemKey(ChatEventDeployFailed, name, d.Target, d.WantedVersion),
			Event: ChatEventDeployFailed, Post: ChatPost{
				Title:  fmt.Sprintf("The deploy of %s %s to %s failed", name, d.WantedVersion, d.Target),
				Status: d.Message, Links: links}})
	}
	return news
}

// ChatBriefRound is a Brief's round as theos-chat reads it.
type ChatBriefRound struct {
	Name  string
	Round int
	Phase string
}

// ChatBriefNews is the updates digest's line for a Brief's round.
func ChatBriefNews(b ChatBriefRound) []ChatNews {
	if b.Round < 1 {
		return nil
	}
	line := fmt.Sprintf("Brief %s is in round %d (%s)", b.Name, b.Round, orUnknown(b.Phase))
	return []ChatNews{{Key: ChatItemKey(ChatEventBriefRound, b.Name, fmt.Sprint(b.Round)), Event: ChatEventBriefRound,
		Line: line, Post: ChatPost{Title: line}}}
}

// ChatStatusCounts is what status answers with: the namespace's work.
type ChatStatusCounts struct {
	Tasks, TasksWaiting, TasksFailed int
	Containers, ContainersReady      int
	Briefs                           int
}

// ChatStatus is the answer to status.
func ChatStatus(c ChatStatusCounts) ChatPost {
	return ChatPost{Title: "Status", Body: []string{fmt.Sprintf(
		"%d %s at work, %d waiting for a person, %d failed\n%d of %d %s Ready\n%d %s",
		c.Tasks, plural(c.Tasks, "Task", "Tasks"), c.TasksWaiting, c.TasksFailed,
		c.ContainersReady, c.Containers, plural(c.Containers, "Container", "Containers"),
		c.Briefs, plural(c.Briefs, "Brief", "Briefs"))}}
}
