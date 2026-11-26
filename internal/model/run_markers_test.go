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
	"strings"
	"testing"
	"time"
)

const (
	runHead     = "4f2a9c1d8e7b6a5f4e3d2c1b0a9f8e7d6c5b4a39"
	runOperator = "theos-operator"
	runAgent    = "outside-dev"
)

// runMarkers are the markers a run's table writes, posted by the right
// account or t fails.
type runMarkers struct{ t *testing.T }

func (r runMarkers) offer(role string) string {
	r.t.Helper()
	line, err := OfferMarker("web-12", 1, role, "claude")
	if err != nil {
		r.t.Fatal(err)
	}
	return line + "\n\nThe work order."
}

func (r runMarkers) session(kind, session, text string) string {
	r.t.Helper()
	line, err := SessionMarker(kind, session)
	if err != nil {
		r.t.Fatal(err)
	}
	if text != "" {
		line += "\n\n" + text
	}
	return line
}

func (r runMarkers) done(session, commit, verdict, summary string) string {
	r.t.Helper()
	body, err := DoneMarker(session, commit, verdict, summary)
	if err != nil {
		r.t.Fatal(err)
	}
	return body
}

func (r runMarkers) ended(why string) string {
	r.t.Helper()
	line, err := EndedMarker(why)
	if err != nil {
		r.t.Fatal(err)
	}
	return line
}

// runWant is what a row of the run table expects of the RunState.
type runWant struct {
	started, done, interrupted bool
	unclaimed                  bool
	endDue                     string
	doneWaits                  string
	commit, verdict, summary   string
	blocked, nothing           string
	failure                    string // a part of it
}

func checkRun(t *testing.T, run RunState, w runWant) {
	t.Helper()
	if run.Started != w.started || run.Done != w.done || run.Interrupted != w.interrupted {
		t.Errorf("started %v, done %v, interrupted %v; want %v, %v, %v", run.Started, run.Done, run.Interrupted,
			w.started, w.done, w.interrupted)
	}
	if run.Checkpoint || run.Pod != "" {
		t.Errorf("a run on the host has a checkpoint (%v) or a pod (%q)", run.Checkpoint, run.Pod)
	}
	if !run.Started {
		if run.Markers != nil {
			t.Errorf("no run has markers %+v", run.Markers)
		}
		return
	}
	mr := run.Markers
	if mr.Unclaimed != w.unclaimed || mr.EndDue != w.endDue || mr.DoneWaits != w.doneWaits {
		t.Errorf("unclaimed %v, end due %q, done waits %q; want %v, %q, %q", mr.Unclaimed, mr.EndDue, mr.DoneWaits,
			w.unclaimed, w.endDue, w.doneWaits)
	}
	if !strings.Contains(run.Failure, w.failure) || (w.failure == "") != (run.Failure == "") {
		t.Errorf("failure %q, want one with %q", run.Failure, w.failure)
	}
	var res RunResult
	if run.Result != nil {
		res = *run.Result
	}
	if (run.Result != nil) != (w.done && !w.interrupted) {
		t.Errorf("result %+v with done %v and interrupted %v", run.Result, w.done, w.interrupted)
	}
	if res.Commit != w.commit || res.Verdict != w.verdict || res.Summary != w.summary || res.Blocked != w.blocked ||
		res.Nothing != w.nothing {
		t.Errorf("result %+v, want commit %q verdict %q summary %q blocked %q nothing %q", res, w.commit, w.verdict,
			w.summary, w.blocked, w.nothing)
	}
}

// #307: a row for each sentence of external-agents.md's decision 3, with
// an outside agent's account apart from the operator's.
func TestRunFromMarkers307(t *testing.T) {
	m := runMarkers{t}
	rules := ClaimRules{Lease: ClaimLease, Grace: ClaimGrace, Claimants: []string{runAgent},
		Engine: []string{runOperator}}
	offer := hostAt(1, runOperator, 0, m.offer(RoleImplementer))
	claim := hostAt(2, runAgent, 1, m.session(MarkerClaim, "s1", ""))
	cases := []struct {
		name     string
		comments []HostComment
		now      int
		want     runWant
	}{
		{"#307 no offer: no run", nil, 1, runWant{}},
		{"#307 an offer waits, unclaimed, at no cost", []HostComment{offer}, 90,
			runWant{started: true, unclaimed: true}},
		{"#307 a claimed run is under way", []HostComment{offer, claim}, 30, runWant{started: true}},
		{"#307 done counts once the commit it names is the branch's head", []HostComment{offer, claim,
			hostAt(3, runAgent, 20, m.done("s1", runHead, "", "Added the cart."))}, 21,
			runWant{started: true, done: true, commit: runHead, summary: "Added the cart."}},
		{"#307 a done that names a prefix of the head counts", []HostComment{offer, claim,
			hostAt(3, runAgent, 20, m.done("s1", runHead[:12], "", "Added the cart."))}, 21,
			runWant{started: true, done: true, commit: runHead, summary: "Added the cart."}},
		{"#307 a done for a commit that isn't the head waits", []HostComment{offer, claim,
			hostAt(3, runAgent, 20, m.done("s1", "0123456789abcdef", "", "Added the cart."))}, 21,
			runWant{started: true, doneWaits: "0123456789abcdef"}},
		{"#307 a done too short to name a commit waits", []HostComment{offer, claim,
			hostAt(3, runAgent, 20, m.done("s1", runHead[:6], "", "Added the cart."))}, 21,
			runWant{started: true, doneWaits: runHead[:6]}},
		{"#307 the last done is the one that counts", []HostComment{offer, claim,
			hostAt(3, runAgent, 20, m.done("s1", runHead, "", "First.")),
			hostAt(4, runAgent, 25, m.done("s1", "0123456789abcdef", "", "Second."))}, 26,
			runWant{started: true, doneWaits: "0123456789abcdef"}},
		{"#307 a done from a session that doesn't hold the run counts for nothing", []HostComment{offer, claim,
			hostAt(3, runAgent, 20, m.done("s2", runHead, "", "Not mine."))}, 21, runWant{started: true}},
		{"#307 blocked ends the run with its note", []HostComment{offer, claim,
			hostAt(3, runAgent, 5, m.session(MarkerBlocked, "s1", "The issue needs a payment account."))}, 6,
			runWant{started: true, done: true, summary: "The issue needs a payment account.",
				blocked: "The issue needs a payment account."}},
		{"#307 a blocked with no reason still blocks", []HostComment{offer, claim,
			hostAt(3, runAgent, 5, m.session(MarkerBlocked, "s1", ""))}, 6,
			runWant{started: true, done: true, blocked: "no reason given"}},
		{"#307 nothing ends the run with its note", []HostComment{offer, claim,
			hostAt(3, runAgent, 5, m.session(MarkerNothing, "s1", "The cart already does this."))}, 6,
			runWant{started: true, done: true, summary: "The cart already does this.",
				nothing: "The cart already does this."}},
		{"#307 silent past the lease and the grace: an ended is due", []HostComment{offer, claim}, 66,
			runWant{started: true, endDue: EndSilent}},
		{"#307 past the lease but not the grace: no ended yet", []HostComment{offer, claim}, 64,
			runWant{started: true}},
		{"#307 a counted ended with no done is interrupted with no checkpoint", []HostComment{offer, claim,
			hostAt(3, runOperator, 66, m.ended(EndSilent))}, 67,
			runWant{started: true, done: true, interrupted: true, failure: EndSilent}},
		{"#307 an ended that came too early counts for nothing", []HostComment{offer, claim,
			hostAt(3, runOperator, 64, m.ended(EndSilent))}, 66,
			runWant{started: true, endDue: EndSilent}},
		{"#307 the engine posts again later, and that ended counts", []HostComment{offer, claim,
			hostAt(3, runOperator, 64, m.ended(EndSilent)), hostAt(4, runOperator, 66, m.ended(EndSilent))}, 67,
			runWant{started: true, done: true, interrupted: true, failure: EndSilent}},
		{"#307 a renew pushes the ended back", []HostComment{offer, claim,
			hostAt(3, runAgent, 50, m.session(MarkerRenew, "s1", "")),
			hostAt(4, runOperator, 66, m.ended(EndSilent))}, 67, runWant{started: true}},
		{"#307 a release ends the run the same way without the wait", []HostComment{offer, claim,
			hostAt(3, runAgent, 5, m.session(MarkerRelease, "s1", ""))}, 6,
			runWant{started: true, endDue: EndReleased}},
		{"#307 the ended after a release counts at once", []HostComment{offer, claim,
			hostAt(3, runAgent, 5, m.session(MarkerRelease, "s1", "")),
			hostAt(4, runOperator, 6, m.ended(EndReleased))}, 7,
			runWant{started: true, done: true, interrupted: true, failure: EndReleased}},
		{"#307 a done after the ended counts for nothing", []HostComment{offer, claim,
			hostAt(3, runOperator, 66, m.ended(EndSilent)),
			hostAt(4, runAgent, 70, m.done("s1", runHead, "", "Late."))}, 71,
			runWant{started: true, done: true, interrupted: true, failure: EndSilent}},
		{"#307 a done before the ended still counts", []HostComment{offer, claim,
			hostAt(3, runAgent, 20, m.done("s1", runHead, "", "In time.")),
			hostAt(4, runOperator, 66, m.ended(EndSilent))}, 67,
			runWant{started: true, done: true, commit: runHead, summary: "In time."}},
		{"#307 a takeover's run needs its own done", []HostComment{offer, claim,
			hostAt(3, runAgent, 20, m.done("s1", "0123456789abcdef", "", "Pushed too late.")),
			hostAt(4, runAgent, 82, m.session(MarkerClaim, "s2", ""))}, 83, runWant{started: true}},
		{"#307 rounds: a new offer starts the run afresh", []HostComment{offer, claim,
			hostAt(3, runAgent, 20, m.done("s1", runHead, "", "Round one.")),
			hostAt(4, runOperator, 40, m.offer(RoleImplementer))}, 41, runWant{started: true, unclaimed: true}},
		{"#307 an offer from the agent's account is none", []HostComment{
			hostAt(1, runAgent, 0, m.offer(RoleImplementer))}, 1, runWant{}},
		{"#307 an ended from the agent's account is none", []HostComment{offer, claim,
			hostAt(3, runAgent, 66, m.ended(EndSilent))}, 67, runWant{started: true, endDue: EndSilent}},
		{"#307 the operator's claim counts for nothing", []HostComment{offer,
			hostAt(2, runOperator, 1, m.session(MarkerClaim, "s1", ""))}, 2,
			runWant{started: true, unclaimed: true}},
		{"#307 a comment that quotes a done after other text counts as none", []HostComment{offer, claim,
			hostAt(3, runAgent, 20, "As I said:\n"+m.done("s1", runHead, "", "Added the cart."))}, 21,
			runWant{started: true}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := RunFacts{Comments: c.comments, Head: runHead, Rules: rules,
				Now: claimT0.Add(time.Duration(c.now) * time.Minute)}
			checkRun(t, RunFromMarkers(f), c.want)
		})
	}
}

// #307: a marker from any other account changes nothing, whatever it says.
func TestRunStrangers307(t *testing.T) {
	m := runMarkers{t}
	rules := ClaimRules{Lease: ClaimLease, Grace: ClaimGrace, Claimants: []string{runAgent},
		Engine: []string{runOperator}}
	base := []HostComment{hostAt(1, runOperator, 0, m.offer(RoleImplementer)),
		hostAt(2, runAgent, 1, m.session(MarkerClaim, "s1", ""))}
	strangers := []string{m.offer(RoleImplementer), m.session(MarkerClaim, "s9", ""),
		m.session(MarkerRelease, "s1", ""), m.done("s1", runHead, "", "Done, says a stranger."),
		m.session(MarkerBlocked, "s1", "Blocked, says a stranger."), m.ended(EndSilent)}
	for _, now := range []int{2, 30, 70} {
		f := RunFacts{Comments: base, Head: runHead, Rules: rules, Now: claimT0.Add(time.Duration(now) * time.Minute)}
		want := RunFromMarkers(f)
		for i, body := range strangers {
			f.Comments = append(append([]HostComment(nil), base...), hostAt(int64(10+i), "drive-by", 3+i, body))
			if got := RunFromMarkers(f); !reflect.DeepEqual(got, want) {
				t.Errorf("at %d minutes, a stranger's %q changed the run to %+v", now, body, got)
			}
		}
	}
}

// #307: the lead runtime's shared account. theos and the sessions post as
// one login: an offer or an ended counts by its kind and claimants, a
// session's marker by its session name.
func TestRunSharedAccount307(t *testing.T) {
	m := runMarkers{t}
	const login = "jair"
	rules := DefaultClaimRules(login)
	offer := hostAt(1, login, 0, m.offer(RoleImplementer))
	claim := hostAt(2, login, 1, m.session(MarkerClaim, "lead-a", ""))
	cases := []struct {
		name     string
		comments []HostComment
		now      int
		want     runWant
		holder   string
	}{
		{"#307 the shared account's offer counts", []HostComment{offer}, 1,
			runWant{started: true, unclaimed: true}, ""},
		{"#307 a session's claim by the same account counts by its session", []HostComment{offer, claim}, 2,
			runWant{started: true}, "lead-a"},
		{"#307 another session's done counts for nothing", []HostComment{offer, claim,
			hostAt(3, login, 10, m.done("lead-b", runHead, "", "Not mine."))}, 11, runWant{started: true}, "lead-a"},
		{"#307 the holder's done counts", []HostComment{offer, claim,
			hostAt(3, login, 10, m.done("lead-a", runHead, "", "Mine."))}, 11,
			runWant{started: true, done: true, commit: runHead, summary: "Mine."}, "lead-a"},
		{"#307 the shared account's ended counts by its kind", []HostComment{offer, claim,
			hostAt(3, login, 66, m.ended(EndSilent))}, 67,
			runWant{started: true, done: true, interrupted: true, failure: EndSilent}, "lead-a"},
		{"#307 a login with other case is the same account", []HostComment{offer,
			hostAt(2, "Jair", 1, m.session(MarkerClaim, "lead-a", ""))}, 2, runWant{started: true}, "lead-a"},
		{"#307 nobody else's markers count", []HostComment{offer,
			hostAt(2, "drive-by", 1, m.session(MarkerClaim, "lead-a", ""))}, 2,
			runWant{started: true, unclaimed: true}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			run := RunFromMarkers(RunFacts{Comments: c.comments, Head: runHead, Rules: rules,
				Now: claimT0.Add(time.Duration(c.now) * time.Minute)})
			checkRun(t, run, c.want)
			if run.Markers.Claim.Session != c.holder {
				t.Errorf("holder %q, want %q", run.Markers.Claim.Session, c.holder)
			}
		})
	}
}

// #307: a reviewer's done carries its verdict, a triager's its decision in
// one json block; the offer names the run's round, role and agent.
func TestRunAnswers307(t *testing.T) {
	m := runMarkers{t}
	const login = "jair"
	rules := DefaultClaimRules(login)
	answer := func(role, body string) RunState {
		t.Helper()
		return RunFromMarkers(RunFacts{Head: runHead, Rules: rules, Now: claimT0.Add(10 * time.Minute),
			Comments: []HostComment{hostAt(1, login, 0, m.offer(role)),
				hostAt(2, login, 1, m.session(MarkerClaim, "lead-a", "")), hostAt(3, login, 5, body)}})
	}

	review := answer(RoleReviewer, m.done("lead-a", "", VerdictChanges, "Test the empty cart."))
	if !review.Done || review.Result.Verdict != VerdictChanges || review.Result.Commit != "" ||
		review.Result.Role != RoleReviewer || review.Result.Summary != "Test the empty cart." {
		t.Errorf("a reviewer's done reads %+v", review.Result)
	}
	if mr := review.Markers; mr.Role() != RoleReviewer || mr.Round() != 1 || review.Config.Agent != "claude" {
		t.Errorf("the offer reads role %q round %d agent %q", mr.Role(), mr.Round(), review.Config.Agent)
	}

	decision := "Ready as it stands.\n\n```json\n{\"decision\": \"ready\", \"reason\": \"Clear criteria.\"}\n```"
	triage := answer(RoleTriager, m.done("lead-a", "", "", decision))
	if !triage.Done || triage.Result.Triage == nil || triage.Result.Triage.Decision != "ready" ||
		triage.Result.Error != "" {
		t.Errorf("a triager's done reads %+v", triage.Result)
	}
	// #307: a session never posts the engine's markers, and a done names its session.
	if _, err := DoneMarker("lead a", runHead, "", ""); err == nil {
		t.Error("a done took a session name of two words")
	}
	if _, err := OfferMarker("web 12", 1, RoleImplementer, "claude"); err == nil {
		t.Error("an offer took a task name of two words")
	}
}

// #337: a triager's, a reviewer's and a verifier's done are read by the
// runner's parsers (ParseTriage, ParseVerdict).
func TestRunAnswers337(t *testing.T) {
	m := runMarkers{t}
	const login = "jair"
	rules := DefaultClaimRules(login)
	answer := func(role, body string) RunState {
		t.Helper()
		return RunFromMarkers(RunFacts{Head: runHead, Rules: rules, Now: claimT0.Add(10 * time.Minute),
			Comments: []HostComment{hostAt(1, login, 0, m.offer(role)),
				hostAt(2, login, 1, m.session(MarkerClaim, "lead-a", "")), hostAt(3, login, 5, body)}})
	}
	// #337: the triager's done is read by the runner's parser, ParseTriage:
	// the last JSON object with a decision counts, fenced or not.
	for name, tt := range map[string]struct{ body, decision string }{
		"an unknown field":   {"```json\n{\"decision\": \"ready\", \"reason\": \"r\", \"why\": \"x\"}\n```", "ready"},
		"two decisions":      {"{\"decision\": \"ready\"} then {\"decision\": \"park\", \"question\": \"q\"}", "park"},
		"unfenced":           {"Looked. {\"decision\": \"spec\", \"reason\": \"Too big.\"}", "spec"},
		"unknown decision":   {"{\"decision\": \"later\"}", ""},
		"no json":            {"```json\nready\n```", ""},
		"no decision at all": {"I looked.", ""},
	} {
		got := answer(RoleTriager, m.done("lead-a", "", "", tt.body))
		switch {
		case tt.decision == "" && (got.Result.Triage != nil || got.Result.Error == ""):
			t.Errorf("%s: a triager's done reads %+v, want no decision and an error", name, got.Result)
		case tt.decision != "" && (got.Result.Triage == nil || got.Result.Triage.Decision != tt.decision):
			t.Errorf("%s: a triager's done reads %+v, want %s", name, got.Result, tt.decision)
		}
	}

	// #337: a verifier's or a reviewer's done may carry its verdict in the
	// runner's format, with what it leaves for the merge.
	verify := answer(RoleVerifier, m.done("lead-a", "", VerdictApprove, "Ran it.\n\n"+
		`{"verdict": "approve", "summary": "Ran it.", "open": [{"criterion": "Works on a phone", "why": "no phone"}],`+
		` "followUps": [{"title": "Not the verifier's"}]}`))
	if r := verify.Result; r.Verdict != VerdictApprove || len(r.Open) != 1 || r.Open[0].Criterion != "Works on a phone" ||
		len(r.FollowUps) != 0 || r.Error != "" {
		t.Errorf("a verifier's done reads %+v, want its open criterion and no follow-ups", r)
	}
	reviewed := answer(RoleReviewer, m.done("lead-a", "", "", `{"verdict": "approve", "summary": "Fine.",`+
		` "followUps": [{"title": "Cache the prices", "criteria": ["It caches"]}]}`))
	if r := reviewed.Result; r.Verdict != VerdictApprove || len(r.FollowUps) != 1 || r.Error != "" {
		t.Errorf("a reviewer's done with its verdict in JSON alone reads %+v", r)
	}
	torn := answer(RoleReviewer, m.done("lead-a", "", VerdictApprove, `{"verdict": "changes", "summary": "No."}`))
	if r := torn.Result; r.Verdict != "" || r.Error == "" {
		t.Errorf("a reviewer's done that says two verdicts reads %+v, want none", r)
	}

}

// #307: the markers feed the Task's decision as a sandbox's run does. A
// counted ended reads as interrupted with no checkpoint, so the run is
// offered again on its branch, twice in a round.
func TestRunAfterInterruption307(t *testing.T) {
	m := runMarkers{t}
	const login = "jair"
	run := RunFromMarkers(RunFacts{Head: runHead, Rules: DefaultClaimRules(login), Now: claimT0.Add(70 * time.Minute),
		Comments: []HostComment{hostAt(1, login, 0, m.offer(RoleImplementer)),
			hostAt(2, login, 1, m.session(MarkerClaim, "lead-a", "")), hostAt(3, login, 66, m.ended(EndSilent))}})
	for reruns, want := range []Interruption{InterruptionRerun, InterruptionRerun, InterruptionNone} {
		if got := AfterInterruption(run, 0, reruns); got != want {
			t.Errorf("after %d reruns: %q, want %q", reruns, got, want)
		}
	}
	if milli, without := run.Cost(); milli != 0 || without != 1 {
		t.Errorf("a run on the host costs %d and %d without a cost", milli, without)
	}
}

// #335: the lead runtime's progress marker is the engine's: a session
// can't post one, and no run reads it, as if it weren't there.
func TestProgressMarkerChangesNoRun335(t *testing.T) {
	m := runMarkers{t}
	if _, err := SessionMarker(MarkerProgress, "s1"); err == nil {
		t.Error("a session posted a progress marker")
	}
	line, err := MarkerLine(MarkerProgress, map[string]string{"task": "web-12"})
	if err != nil {
		t.Fatal(err)
	}
	rules := DefaultClaimRules(runAgent)
	offer := hostAt(1, runAgent, 0, m.offer(RoleImplementer))
	claim := hostAt(2, runAgent, 1, m.session(MarkerClaim, "s1", ""))
	progress := hostAt(3, runAgent, 2, line+"\n\n```json\n{}\n```\n")
	without := RunFromMarkers(RunFacts{Comments: []HostComment{offer, claim}, Head: runHead, Rules: rules,
		Now: claim.Created.Add(time.Minute)})
	with := RunFromMarkers(RunFacts{Comments: []HostComment{offer, claim, progress}, Head: runHead, Rules: rules,
		Now: claim.Created.Add(time.Minute)})
	if !reflect.DeepEqual(without, with) {
		t.Errorf("with a progress marker the run is %+v, want %+v", with, without)
	}
	if _, ok := ParseMarker(progress); !ok {
		t.Error("a progress marker doesn't parse")
	}
}
