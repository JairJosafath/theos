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
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// A run on the Git host (#307; docs/plans/external-agents.md, decision 3;
// docs/plans/lead-runtime.md, decision 10). The engine offers a run and
// ends it; a session claims, renews, releases and answers it with done,
// blocked or nothing. RunFromMarkers reads the markers as the RunState the
// Task's decision takes, as the operator reads a Sandbox's pod, so
// NextTaskStep never knows whether a run was pushed to a sandbox or pulled
// by a session.

// Why the engine ends a run that has no answer: its holder went silent past
// the lease and the grace, or gave it up.
const (
	EndSilent   = "silent"
	EndReleased = "released"
)

// The keys of a marker line's key=value words.
const (
	markerKeySession = "session"
	markerKeyTask    = "task"
	markerKeyRound   = "round"
	markerKeyRole    = "role"
	markerKeyAgent   = "agent"
	markerKeyCommit  = "commit"
	markerKeyVerdict = "verdict"
	markerKeyWhy     = "why"
)

// minCommitPrefix is the shortest commit a done may name: a prefix of the
// branch's head at least this long counts as the head.
const minCommitPrefix = 7

// WorkOrder is one role's turn on a Task, as the engine offers it: the
// round of Task, the role and the Agent that plays it, and the text that
// tells it what to do (#266). The operator's Workers make a sandbox of it;
// the lead runtime's post it as an offer (OfferMarker) that a session pulls.
type WorkOrder struct {
	Task string
	// Issue is the Task's issue, Branch the branch its work goes on.
	Issue  int
	Branch string
	Round  int
	Role   string
	Agent  string
	// Goal is the issue as people wrote it, its title and body: data, which
	// a work order fences (IssueBlock).
	Goal string
	// Feedback is what the round has to address, from the round before.
	Feedback string
	// Spec means the round writes the issue's spec (SpecPath) and nothing
	// else: the implementer's turn of a Spec Task, which the Crew's spec
	// writer plays.
	Spec bool
	// Lease is how long a claim of the turn holds before it is renewed.
	Lease time.Duration
	// Text is what the role reads: the work order the method renders (#266).
	Text string
}

// OfferMarker is the first line of the engine's offer of round of task to
// role, played by agent; the work order's text (#266) follows it.
func OfferMarker(task string, round int, role, agent string) (string, error) {
	return MarkerLine(MarkerOffer, map[string]string{markerKeyTask: task, markerKeyRound: strconv.Itoa(round),
		markerKeyRole: role, markerKeyAgent: agent})
}

// EndedMarker is the engine's comment that ends a run, why one of EndSilent
// and EndReleased or the engine's own word.
func EndedMarker(why string) (string, error) {
	return MarkerLine(MarkerEnded, map[string]string{markerKeyWhy: why})
}

// DoneMarker is the comment a session posts when its run is done: the
// commit it pushed, for an implementer, or a reviewer's verdict, then its
// summary. A triager's decision goes in the summary as a JSON object, and a
// reviewer's or a verifier's may carry one in the runner's verdict format
// (ParseTriage, ParseVerdict).
func DoneMarker(session, commit, verdict, summary string) (string, error) {
	if err := CheckSessionName(session); err != nil {
		return "", err
	}
	attrs := map[string]string{markerKeySession: session}
	if commit != "" {
		attrs[markerKeyCommit] = commit
	}
	if verdict != "" {
		attrs[markerKeyVerdict] = verdict
	}
	line, err := MarkerLine(MarkerDone, attrs)
	if summary = strings.TrimSpace(summary); summary != "" {
		line += "\n\n" + summary
	}
	return line, err
}

// MarkerRun is what only a run on the Git host has, besides its RunState.
type MarkerRun struct {
	// Offer is the run's counted offer: its round, role and agent.
	Offer Marker
	// Claim is who holds the run.
	Claim Claim
	// Unclaimed is an offer nobody has claimed yet: it waits at no cost,
	// WaitingForAgent, and after a lease's length is a Needs-you item.
	Unclaimed bool
	// EndDue is why the engine should post an ended now, "" while it should
	// not: EndSilent once its clock says the holder's lease and grace are
	// past with no answer, EndReleased once the holder gave the run up. Only
	// the counted ended ends the run: its stamp is the host's.
	EndDue string
	// DoneWaits is the commit of the holder's done while it isn't the
	// branch's head: the run is not done until it is.
	DoneWaits string
}

// Role is the role the offer names.
func (r MarkerRun) Role() string { return r.Offer.Attrs[markerKeyRole] }

// Round is the round the offer names, 0 when it names none.
func (r MarkerRun) Round() int {
	n, _ := strconv.Atoi(r.Offer.Attrs[markerKeyRound])
	return n
}

// RunFacts are what a run on the Git host is read from: its issue's
// comments, the head of the Task's branch, the rules the markers are
// counted by, and the engine's latest time, which only decides whether an
// ended is due.
type RunFacts struct {
	Comments []HostComment
	Head     string
	Rules    ClaimRules
	Now      time.Time
}

// runReplay is an issue's counted markers since the last offer.
type runReplay struct {
	claim Claim
	// done is the holder's last done; answer is its blocked or nothing,
	// which closes the run; ended is the counted ended.
	done, answer, ended *Marker
}

// replayMarkers reads an issue's comments in the host's order: its stamp,
// then its ID. A session's marker counts from rules' claimants, an offer
// or an ended from its engine; every marker counts only after a counted
// offer and before the run closes. A session's answer counts from the
// holder alone. An ended counts only once no claim holds at its stamp: the
// holder's lease and the grace are past, or nobody holds the run; one that
// came too early counts for nothing, and the engine posts again later.
func replayMarkers(comments []HostComment, rules ClaimRules) runReplay {
	ordered := slices.Clone(comments)
	slices.SortStableFunc(ordered, func(a, b HostComment) int {
		return cmp.Or(a.Created.Compare(b.Created), cmp.Compare(a.ID, b.ID))
	})
	var r runReplay
	for _, comment := range ordered {
		m, ok := ParseMarker(comment)
		if !ok {
			continue
		}
		at, session := comment.Created, m.Session()
		c := &r.claim
		if m.Kind == MarkerOffer || m.Kind == MarkerEnded {
			switch {
			case !rules.engineCounts(comment.Author):
			case m.Kind == MarkerOffer:
				r = runReplay{claim: Claim{Offered: true, Offer: m}}
			case c.Offered && !c.Closed && (c.Session == "" || !at.Before(c.Until.Add(rules.Grace))):
				r.ended, c.Closed = &m, true
			}
			continue
		}
		if !rules.counts(comment.Author) || !c.Offered || c.Closed || CheckSessionName(session) != nil {
			continue
		}
		mine := c.Names(comment.Author, session)
		switch m.Kind {
		case MarkerClaim:
			if c.Session == "" || mine || !at.Before(c.Until.Add(rules.Grace)) {
				c.Account, c.Session, c.Until, c.Released = comment.Author, session, at.Add(rules.Lease), false
				r.done = nil
			}
		case MarkerRenew:
			if mine && at.Before(c.Until) {
				c.Until = at.Add(rules.Lease)
			}
		case MarkerRelease:
			if mine {
				c.Account, c.Session, c.Until, c.Released = "", "", time.Time{}, true
				r.done = nil
			}
		case MarkerDone:
			if mine {
				r.done = &m
			}
		case MarkerBlocked, MarkerNothing:
			if mine {
				r.answer, c.Closed = &m, true
			}
		}
	}
	return r
}

// RunFromMarkers is the run of a Task on the Git host as a RunState, with
// what only such a run has in its Markers:
//   - no counted offer: no run (Started is false);
//   - offered: started, with no pod and no checkpoint;
//   - the holder's done counts once the commit it names is the branch's
//     head (a done that names none counts at once): the run is done, with
//     its summary, its verdict, or a triager's decision;
//   - the holder's blocked or nothing: done, with its note;
//   - a counted ended with no done counted: interrupted with no
//     checkpoint, so AfterInterruption offers it again on its branch.
func RunFromMarkers(f RunFacts) RunState {
	r := replayMarkers(f.Comments, f.Rules)
	if !r.claim.Offered {
		return RunState{}
	}
	mr := &MarkerRun{Offer: r.claim.Offer, Claim: r.claim, Unclaimed: r.claim.Session == "" && !r.claim.Released}
	run := RunState{Started: true, Markers: mr, Config: RunConfig{Agent: mr.Offer.Attrs[markerKeyAgent]}}
	switch {
	case r.answer != nil:
		run.Done, run.Result = true, answerResult(*r.answer, mr.Role())
	case r.done != nil && doneCounts(*r.done, f.Head):
		run.Done, run.Result = true, doneResult(*r.done, mr.Role(), f.Head)
	case r.ended != nil:
		run.Done, run.Interrupted = true, true
		run.Failure = "the run ended with no answer: " + orUnknown(r.ended.Attrs[markerKeyWhy])
		mr.Unclaimed = false
	default:
		if r.done != nil {
			mr.DoneWaits = r.done.Attrs[markerKeyCommit]
		}
		switch {
		case r.claim.Released:
			mr.EndDue = EndReleased
		case r.claim.Session != "" && !f.Now.Before(r.claim.Until.Add(f.Rules.Grace)):
			mr.EndDue = EndSilent
		}
	}
	return run
}

// doneCounts reports whether done names head, or names no commit at all.
func doneCounts(done Marker, head string) bool {
	commit := done.Attrs[markerKeyCommit]
	if commit == "" {
		return true
	}
	return head != "" && (commit == head || len(commit) >= minCommitPrefix && strings.HasPrefix(head, commit))
}

// markerText is a marker comment's text after its marker line.
func markerText(m Marker) string {
	_, rest, _ := strings.Cut(m.Comment.Body, "\n")
	return strings.TrimSpace(rest)
}

// doneResult is the result a counted done reports: the head it names, its
// summary and verdict; for a triager the decision in its text, and for a
// reviewer or a verifier what its text's JSON verdict leaves for the merge,
// each read by the parser the operator's runner reads the agent's output
// with (ParseTriage, ParseVerdict; #337).
func doneResult(done Marker, role, head string) *RunResult {
	res := &RunResult{Summary: markerText(done), Verdict: done.Attrs[markerKeyVerdict], Role: role}
	if done.Attrs[markerKeyCommit] != "" {
		res.Commit = head
	}
	switch role {
	case RoleTriager:
		if d, err := ParseTriage(res.Summary); err != nil {
			res.Error = err.Error()
		} else {
			res.Triage = &d
		}
	case RoleReviewer, RoleVerifier:
		verdictFindings(res, role)
	}
	return res
}

// verdictFindings adds to a reviewer's or a verifier's result what the JSON
// verdict in its done's text leaves for the merge: the reviewer's
// follow-ups and risk, and the criteria either accepted as open. A done
// whose text has none is its verdict= alone; one whose verdict= is missing
// takes the JSON's, and one whose JSON says another verdict gave none.
func verdictFindings(res *RunResult, role string) {
	who := "reviewer"
	if role == RoleVerifier {
		who = "verifier"
	}
	v, err := ParseVerdict(res.Summary, who)
	switch {
	case err != nil:
		return
	case res.Verdict == "":
		res.Verdict = v.Verdict
	case res.Verdict != v.Verdict:
		res.Error = fmt.Sprintf("the %s's done says verdict=%s, and its JSON %q", who, res.Verdict, v.Verdict)
		res.Verdict = ""
		return
	}
	res.Open = v.Open
	if role == RoleReviewer {
		// A verifier suggests no follow-ups: that is the reviewer's part.
		res.FollowUps, res.Risk = v.FollowUps, v.Risk
	}
}

// answerResult is the result a blocked or a nothing reports: its note, at
// most MaxBlockedNote bytes, and a blocked with no reason still blocks.
func answerResult(m Marker, role string) *RunResult {
	note := ClipSentences(markerText(m), MaxBlockedNote)
	res := &RunResult{Summary: markerText(m), Role: role}
	if m.Kind == MarkerBlocked {
		res.Blocked = cmp.Or(note, "no reason given")
	} else {
		res.Nothing = note
	}
	return res
}
