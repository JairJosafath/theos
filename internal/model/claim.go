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
	"maps"
	"regexp"
	"slices"
	"strings"
	"time"
)

// A claim on the Git host (#267; docs/plans/no-cluster.md, decision 8, with
// docs/plans/lead-runtime.md, decisions 5 and 10). theos makes the lock: the
// engine creates a Task's branch, task/<container>-<issue>, and posts an
// offer marker on its issue. A session never creates a branch; it claims
// the offer. The lease is the issue's marker comments, each a new comment
// and never edited, ordered by the host's creation stamp and then its ID:
// every time compared is the host's, never a session's clock.

// The marker kinds of a run, one grammar for both runtimes
// (docs/plans/external-agents.md, decision 3). offer and ended are the
// engine's; the rest are a session's.
const (
	// MarkerOffer is the engine's: the work is offered on the Task's branch.
	MarkerOffer = "offer"
	// MarkerClaim is a session's: it takes the offered work.
	MarkerClaim = "claim"
	// MarkerRenew is the holder's: it still holds the work.
	MarkerRenew = "renew"
	// MarkerRelease is the holder's: it gives the work up.
	MarkerRelease = "release"
	// MarkerDone is the holder's: the commit it pushed and a summary, a
	// reviewer's verdict, or a triager's decision (#307).
	MarkerDone = "done"
	// MarkerBlocked is the holder's: it can't start the issue, and says why.
	MarkerBlocked = "blocked"
	// MarkerNothing is the holder's: the issue needs no change, and why.
	MarkerNothing = "nothing"
	// MarkerEnded is the engine's: the run is over, and its later markers
	// count for nothing.
	MarkerEnded = "ended"
)

// ClaimLease is how long a claim or a renew holds the work from its
// comment's stamp; ClaimGrace is how long past that another session waits
// before its claim counts. Both are settings (model/settings.yaml); these
// are their defaults.
const (
	ClaimLease = 60 * time.Minute
	ClaimGrace = 5 * time.Minute
)

// markerPrefix starts every marker line; a marker kind follows it.
const markerPrefix = "<!-- theos:"

var (
	markerKinds = []string{MarkerOffer, MarkerClaim, MarkerRenew, MarkerRelease, MarkerDone, MarkerBlocked,
		MarkerNothing, MarkerEnded}
	// markerLine is a whole marker line: its kind and its key=value words.
	markerLine = regexp.MustCompile(`^<!-- theos:([a-z]+)((?: [a-z][a-zA-Z]*=[^\s=]+)*) -->$`)
	// markerKey is a key of a marker line's key=value words.
	markerKey = regexp.MustCompile(`^[a-z][a-zA-Z]*$`)
	// markerWord is a session's name or any other value in a marker line.
	markerWord = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)
)

// HostComment is a comment on an issue as the Git host shows it: its ID,
// its author's login, the host's stamp of its creation, and its text.
type HostComment struct {
	ID      int64
	Author  string
	Created time.Time
	Body    string
}

// Marker is a marker comment: its kind, its key=value words and the
// comment it is.
type Marker struct {
	Kind    string
	Attrs   map[string]string
	Comment HostComment
}

// Session is the session the marker names, "" for none.
func (m Marker) Session() string { return m.Attrs[markerKeySession] }

// MarkerLine is the first line of a marker comment of kind with attrs, in
// key order. Each value is one word, so a marker reads back as it was
// written.
func MarkerLine(kind string, attrs map[string]string) (string, error) {
	if !slices.Contains(markerKinds, kind) {
		return "", fmt.Errorf("%q is not a marker kind; want one of %s", kind, strings.Join(markerKinds, ", "))
	}
	var b strings.Builder
	b.WriteString(markerPrefix + kind)
	for _, k := range slices.Sorted(maps.Keys(attrs)) {
		if !markerKey.MatchString(k) {
			return "", fmt.Errorf("marker key %q is not a lower-case word", k)
		}
		if !markerWord.MatchString(attrs[k]) {
			return "", fmt.Errorf("marker %s=%q is not one word of letters, digits, '.', '_', '/' and '-'", k, attrs[k])
		}
		b.WriteString(" " + k + "=" + attrs[k])
	}
	b.WriteString(" -->")
	return b.String(), nil
}

// SessionMarker is the comment a session posts to claim, renew or release
// the work: one marker line naming it.
func SessionMarker(kind, session string) (string, error) {
	if kind == MarkerOffer || kind == MarkerEnded {
		return "", fmt.Errorf("an %s marker is the engine's, not a session's", kind)
	}
	if err := CheckSessionName(session); err != nil {
		return "", err
	}
	return MarkerLine(kind, map[string]string{markerKeySession: session})
}

// CheckSessionName says why session can't name a session in a marker, nil
// when it can. The name is the session's own word: the accounts are the
// same by default, and the name tells the sessions apart.
func CheckSessionName(session string) error {
	if !markerWord.MatchString(session) || len(session) > 63 {
		return fmt.Errorf("session name %q is not one word of up to 63 letters, digits, '.', '_', '/' and '-'", session)
	}
	return nil
}

// ParseMarker reads c as a marker. A marker is a whole comment that begins
// with its marker line; a comment that quotes one after other text, or
// names a kind theos doesn't know, is none.
func ParseMarker(c HostComment) (Marker, bool) {
	first, _, _ := strings.Cut(strings.TrimRight(c.Body, "\r\n"), "\n")
	g := markerLine.FindStringSubmatch(strings.TrimRight(first, " \r"))
	if g == nil || !slices.Contains(markerKinds, g[1]) {
		return Marker{}, false
	}
	attrs := map[string]string{}
	for word := range strings.FieldsSeq(g[2]) {
		k, v, _ := strings.Cut(word, "=")
		if _, dup := attrs[k]; dup {
			return Marker{}, false
		}
		attrs[k] = v
	}
	return Marker{Kind: g[1], Attrs: attrs, Comment: c}, true
}

// ClaimRules are the settings a claim is judged by: the lease, the grace,
// and the accounts whose markers count. Anyone may comment on a public
// repository, so a marker from any other account counts for nothing.
type ClaimRules struct {
	Lease     time.Duration
	Grace     time.Duration
	Claimants []string
	// Engine are the accounts whose offer and ended markers count: the
	// operator's login for an outside agent. Empty, as in the lead runtime,
	// where theos and the sessions share one account, it is Claimants, and an
	// offer or an ended counts by its kind.
	Engine []string
}

// DefaultClaimRules are the rules when no setting says otherwise: the
// default lease and grace, and only the credential's own login, login,
// counting.
func DefaultClaimRules(login string) ClaimRules {
	return ClaimRules{Lease: ClaimLease, Grace: ClaimGrace, Claimants: []string{login}}
}

// counts reports whether a marker by author counts: logins are compared as
// the hosts do, without case.
func (r ClaimRules) counts(author string) bool { return loginIn(r.Claimants, author) }

// engineCounts reports whether an offer or an ended by author counts.
func (r ClaimRules) engineCounts(author string) bool {
	if len(r.Engine) == 0 {
		return r.counts(author)
	}
	return loginIn(r.Engine, author)
}

// loginIn reports whether login is one of logins, without case.
func loginIn(logins []string, login string) bool {
	return slices.ContainsFunc(logins, func(c string) bool { return c != "" && strings.EqualFold(c, login) })
}

// Claim is who holds an offered Task's work, read from its issue's counted
// markers in the host's order.
type Claim struct {
	// Offered is whether a counted offer was posted; Offer is the last one.
	Offered bool
	Offer   Marker
	// Account and Session are the holder's: the author of the counted
	// claim and the session it names; both "" while nobody holds the work.
	Account string
	Session string
	// Until is when the holder's lease runs out: its last counted claim's
	// or renew's stamp and the lease.
	Until time.Time
	// Released is whether the last holder gave the work up and nobody has
	// claimed it since.
	Released bool
	// Closed is whether the run is over: a counted ended, blocked or
	// nothing came after the offer. Nothing is claimed until the next offer.
	Closed bool
}

// Held reports whether a session holds the work at host time t.
func (c Claim) Held(t time.Time) bool { return c.Session != "" && t.Before(c.Until) }

// Names reports whether the counted markers name session, of account, as
// the holder. A session that posted a claim works only once they do.
func (c Claim) Names(account, session string) bool {
	return c.Session != "" && c.Session == session && strings.EqualFold(c.Account, account)
}

// ClaimOf reads the claim from an issue's comments by rules. Only markers
// from rules' claimants count, and only after a counted offer: each offer
// starts the work afresh. Then, in the host's order:
//   - a claim with nobody holding takes the work;
//   - the holder's own claim takes it again, with no grace;
//   - another session's claim counts once the lease and the grace are past;
//   - the holder's renew before its lease runs out extends it, and a later
//     one counts for nothing;
//   - the holder's release gives the work up, for the next claim at once;
//   - a counted ended, or the holder's blocked or nothing, closes the run
//     (RunFromMarkers).
func ClaimOf(comments []HostComment, rules ClaimRules) Claim {
	return replayMarkers(comments, rules).claim
}

// StartStep is what the engine does next to start a Task for an issue.
type StartStep string

const (
	// StartCreateBranch: no Task branch yet; create it. The host lets one
	// creation through, so two leads start a Task once.
	StartCreateBranch StartStep = "CreateBranch"
	// StartPostOffer: the branch is there with no offer and no pull
	// request, the engine's own start that stopped halfway; post the offer.
	StartPostOffer StartStep = "PostOffer"
	// StartDone: the Task is started; its offer or its pull request is up.
	StartDone StartStep = "Started"
)

// NextStart is the engine's next step in starting a Task: from whether its
// branch exists, whether a counted offer is on its issue, and whether a
// pull request is open from the branch. A task/ branch is theos's alone, so
// one with no offer and no pull request is always its own start, never a
// lock for a person to clear.
func NextStart(branch, offered, pullRequest bool) StartStep {
	switch {
	case offered || pullRequest:
		return StartDone
	case branch:
		return StartPostOffer
	default:
		return StartCreateBranch
	}
}

// ClaimStep is what a session's claim, or any later theos call of the
// holder's, does.
type ClaimStep string

const (
	// ClaimNotOffered: nothing to claim. With no branch the issue is no
	// Task; with a branch and no offer it is the engine's start, which its
	// next tick finishes; with its run closed it waits for the next offer.
	// Nothing is posted.
	ClaimNotOffered ClaimStep = "NotOffered"
	// ClaimTaken: another session's claim is alive; nothing is posted and
	// the session takes other work.
	ClaimTaken ClaimStep = "Taken"
	// ClaimPost: post the claim, read the issue back until it shows, and
	// work only if ClaimOf then names the session.
	ClaimPost ClaimStep = "PostClaim"
	// ClaimRenew: the session holds the work with under half its lease
	// left; post a renew.
	ClaimRenew ClaimStep = "PostRenew"
	// ClaimHeld: the session holds the work with half its lease or more
	// left; nothing is posted.
	ClaimHeld ClaimStep = "Held"
)

// ClaimFacts are what a session's claim reads on the host.
type ClaimFacts struct {
	// Branch is whether the Task's branch exists.
	Branch bool
	// Comments are the issue's comments.
	Comments []HostComment
	// Now is the latest time the session knows. It only decides whether a
	// marker is worth posting: whether the marker counts is ClaimOf's, by
	// the host's stamps.
	Now time.Time
}

// ClaimDecision is a claim's step and the marker comment it posts, "" when
// it posts nothing.
type ClaimDecision struct {
	Step  ClaimStep
	Post  string
	Claim Claim
}

// NextClaim is what session, posting as account, does about a Task's work
// by rules. A refused claim posts nothing.
func NextClaim(f ClaimFacts, rules ClaimRules, account, session string) (ClaimDecision, error) {
	if err := CheckSessionName(session); err != nil {
		return ClaimDecision{}, err
	}
	c := ClaimOf(f.Comments, rules)
	d := ClaimDecision{Claim: c}
	switch {
	case !f.Branch || !c.Offered || c.Closed:
		d.Step = ClaimNotOffered
	case c.Names(account, session) && c.Held(f.Now):
		d.Step = ClaimHeld
		if c.Until.Sub(f.Now) < rules.Lease/2 {
			d.Step = ClaimRenew
		}
	case c.Session == "" || c.Names(account, session) || !f.Now.Before(c.Until.Add(rules.Grace)):
		d.Step = ClaimPost
	default:
		d.Step = ClaimTaken
	}
	kind := map[ClaimStep]string{ClaimPost: MarkerClaim, ClaimRenew: MarkerRenew}[d.Step]
	if kind == "" {
		return d, nil
	}
	post, err := SessionMarker(kind, session)
	d.Post = post
	return d, err
}
