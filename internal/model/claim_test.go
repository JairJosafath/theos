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
	"strings"
	"testing"
	"time"
)

// claimT0 is the host's stamp the claim table counts from.
var claimT0 = time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)

// hostAt is a comment the host stamped min minutes after claimT0.
func hostAt(id int64, author string, min int, body string) HostComment {
	return HostComment{ID: id, Author: author, Created: claimT0.Add(time.Duration(min) * time.Minute), Body: body}
}

// marker is the comment body of a session's marker, or t fails.
func marker(t *testing.T, kind, session string) string {
	t.Helper()
	body, err := SessionMarker(kind, session)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

// #267: the claim table, a row per sentence of the issue (no-cluster.md
// decision 8, with lead-runtime.md decisions 5 and 10).
func TestNextClaim267(t *testing.T) {
	const me, other, stranger = "theos-bot", "theos-bot", "drive-by"
	offer := "<!-- theos:offer role=implementer round=1 task=web-12 -->\n\nThe work order."
	claim := func(s string) string { return marker(t, MarkerClaim, s) }
	renew := func(s string) string { return marker(t, MarkerRenew, s) }
	release := func(s string) string { return marker(t, MarkerRelease, s) }
	rules := DefaultClaimRules(me)

	cases := []struct {
		name     string
		branch   bool
		comments []HostComment
		now      int // minutes after claimT0, the session's latest time
		session  string
		want     ClaimStep
		holder   string // ClaimOf's session after the comments
	}{
		{"#267 a claim on an offer", true,
			[]HostComment{hostAt(1, me, 0, offer)}, 1, "lead-b", ClaimPost, ""},
		{"#267 a live claim refuses another", true,
			[]HostComment{hostAt(1, me, 0, offer), hostAt(2, other, 1, claim("lead-a"))}, 30, "lead-b", ClaimTaken, "lead-a"},
		{"#267 the lease run out but not the grace: still taken", true,
			[]HostComment{hostAt(1, me, 0, offer), hostAt(2, other, 1, claim("lead-a"))}, 63, "lead-b", ClaimTaken, "lead-a"},
		{"#267 a takeover after the lease and the grace", true,
			[]HostComment{hostAt(1, me, 0, offer), hostAt(2, other, 1, claim("lead-a"))}, 66, "lead-b", ClaimPost, "lead-a"},
		{"#267 the old holder's own claim needs no grace", true,
			[]HostComment{hostAt(1, me, 0, offer), hostAt(2, other, 1, claim("lead-a"))}, 62, "lead-a", ClaimPost, "lead-a"},
		{"#267 the holder's renew before the lease extends it", true,
			[]HostComment{hostAt(1, me, 0, offer), hostAt(2, other, 1, claim("lead-a")), hostAt(3, other, 50, renew("lead-a"))},
			100, "lead-b", ClaimTaken, "lead-a"},
		{"#267 the holder's late renew counts for nothing", true,
			[]HostComment{hostAt(1, me, 0, offer), hostAt(2, other, 1, claim("lead-a")), hostAt(3, other, 62, renew("lead-a"))},
			70, "lead-b", ClaimPost, "lead-a"},
		{"#267 a release frees the work at once, with no grace", true,
			[]HostComment{hostAt(1, me, 0, offer), hostAt(2, other, 1, claim("lead-a")), hostAt(3, other, 5, release("lead-a"))},
			6, "lead-b", ClaimPost, ""},
		{"#267 another session's release counts for nothing", true,
			[]HostComment{hostAt(1, me, 0, offer), hostAt(2, other, 1, claim("lead-a")), hostAt(3, other, 5, release("lead-b"))},
			6, "lead-b", ClaimTaken, "lead-a"},
		{"#267 a marker from outside claimants counts for nothing", true,
			[]HostComment{hostAt(1, me, 0, offer), hostAt(2, stranger, 1, claim("lead-a"))}, 2, "lead-b", ClaimPost, ""},
		{"#267 an offer from outside claimants is no offer", true,
			[]HostComment{hostAt(1, stranger, 0, offer)}, 1, "lead-b", ClaimNotOffered, ""},
		{"#267 a task/ branch without an offer is the engine's start: nothing posted", true,
			nil, 1, "lead-b", ClaimNotOffered, ""},
		{"#267 no branch: no Task to claim, and the session makes none", false,
			[]HostComment{hostAt(1, me, 0, offer)}, 1, "lead-b", ClaimNotOffered, ""},
		{"#267 a claim before the offer counts for nothing", true,
			[]HostComment{hostAt(1, other, 0, claim("lead-a")), hostAt(2, me, 1, offer)}, 2, "lead-b", ClaimPost, ""},
		{"#267 a new offer starts the work afresh", true,
			[]HostComment{hostAt(1, me, 0, offer), hostAt(2, other, 1, claim("lead-a")), hostAt(3, me, 10, offer)},
			11, "lead-b", ClaimPost, ""},
		{"#267 a quoted marker is none", true,
			[]HostComment{hostAt(1, me, 0, offer), hostAt(2, other, 1, "> "+claim("lead-a"))}, 2, "lead-b", ClaimPost, ""},
		{"#267 the holder with half its lease or more left posts nothing", true,
			[]HostComment{hostAt(1, me, 0, offer), hostAt(2, me, 1, claim("lead-b"))}, 20, "lead-b", ClaimHeld, "lead-b"},
		{"#267 the holder with under half its lease left renews", true,
			[]HostComment{hostAt(1, me, 0, offer), hostAt(2, me, 1, claim("lead-b"))}, 32, "lead-b", ClaimRenew, "lead-b"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := ClaimFacts{Branch: c.branch, Comments: c.comments, Now: claimT0.Add(time.Duration(c.now) * time.Minute)}
			d, err := NextClaim(f, rules, me, c.session)
			if err != nil {
				t.Fatal(err)
			}
			if d.Step != c.want {
				t.Errorf("step %s, want %s", d.Step, c.want)
			}
			if d.Claim.Session != c.holder {
				t.Errorf("holder %q, want %q", d.Claim.Session, c.holder)
			}
			// #267: a refused claim posts nothing; a step that posts posts
			// one marker naming the session.
			switch d.Step {
			case ClaimPost, ClaimRenew:
				m, ok := ParseMarker(HostComment{Author: me, Body: d.Post})
				if !ok || m.Session() != c.session {
					t.Errorf("posts %q, want a marker naming %s", d.Post, c.session)
				}
			default:
				if d.Post != "" {
					t.Errorf("step %s posts %q, want nothing", d.Step, d.Post)
				}
			}
		})
	}
}

// #267: a session that posted a claim reads the issue back and works only
// if the markers name it; the host's stamps decide a race, never a clock.
func TestClaimReadBack267(t *testing.T) {
	const me = "theos-bot"
	offer := "<!-- theos:offer task=web-12 -->"
	claim := func(s string) string { return marker(t, MarkerClaim, s) }
	rules := DefaultClaimRules(me)
	cases := []struct {
		name     string
		comments []HostComment
		winner   string
	}{
		{"#267 two claims on an offer: the host's first stamp wins",
			[]HostComment{hostAt(1, me, 0, offer), hostAt(3, me, 2, claim("lead-b")), hostAt(2, me, 1, claim("lead-a"))}, "lead-a"},
		{"#267 the same stamp: the lower ID wins",
			[]HostComment{hostAt(1, me, 0, offer), hostAt(9, me, 1, claim("lead-b")), hostAt(8, me, 1, claim("lead-a"))}, "lead-a"},
		{"#267 a takeover claim posted too early counts for nothing",
			[]HostComment{hostAt(1, me, 0, offer), hostAt(2, me, 1, claim("lead-a")), hostAt(3, me, 64, claim("lead-b"))},
			"lead-a"},
		{"#267 a takeover claim after the lease and the grace takes the work",
			[]HostComment{hostAt(1, me, 0, offer), hostAt(2, me, 1, claim("lead-a")), hostAt(3, me, 66, claim("lead-b"))},
			"lead-b"},
		{"#267 the slow holder's renew after a takeover counts for nothing",
			[]HostComment{hostAt(1, me, 0, offer), hostAt(2, me, 1, claim("lead-a")), hostAt(3, me, 66, claim("lead-b")),
				hostAt(4, me, 67, marker(t, MarkerRenew, "lead-a"))}, "lead-b"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ClaimOf(c.comments, rules)
			if !got.Names(me, c.winner) {
				t.Errorf("the markers name %q, want %q", got.Session, c.winner)
			}
			for _, loser := range []string{"lead-a", "lead-b"} {
				if loser != c.winner && got.Names(me, loser) {
					t.Errorf("the markers name the loser %s too", loser)
				}
			}
		})
	}
	// #267: the lease runs from the counted marker's stamp.
	got := ClaimOf([]HostComment{hostAt(1, me, 0, offer), hostAt(2, me, 1, claim("lead-a")),
		hostAt(3, me, 40, marker(t, MarkerRenew, "lead-a"))}, rules)
	if want := claimT0.Add(100 * time.Minute); !got.Until.Equal(want) {
		t.Errorf("lease until %s, want %s", got.Until, want)
	}
	if !got.Held(claimT0.Add(99*time.Minute)) || got.Held(claimT0.Add(100*time.Minute)) {
		t.Errorf("held from the renew's stamp for the lease, until %s", got.Until)
	}
}

// #267: claimants are a setting; by default only the credential's own
// login counts, compared without case, and the lease and the grace are 60
// and 5 minutes.
func TestClaimRules267(t *testing.T) {
	r := DefaultClaimRules("Theos-Bot")
	if r.Lease != 60*time.Minute || r.Grace != 5*time.Minute {
		t.Errorf("default lease %s and grace %s", r.Lease, r.Grace)
	}
	if !r.counts("theos-bot") || r.counts("drive-by") || r.counts("") {
		t.Errorf("claimants %v count the wrong logins", r.Claimants)
	}
	if (ClaimRules{Claimants: []string{""}}).counts("") {
		t.Error("an empty claimant counts an empty author")
	}
	two := ClaimRules{Lease: 10 * time.Minute, Grace: time.Minute, Claimants: []string{"lead-1", "lead-2"}}
	offer := "<!-- theos:offer task=web-12 -->"
	got := ClaimOf([]HostComment{hostAt(1, "lead-1", 0, offer), hostAt(2, "lead-2", 1, marker(t, MarkerClaim, "a")),
		hostAt(3, "lead-1", 2, marker(t, MarkerRenew, "a"))}, two)
	if !got.Names("lead-2", "a") || !got.Until.Equal(claimT0.Add(11*time.Minute)) {
		t.Errorf("a renew of the same session name from another account extended %+v", got)
	}
}

// #267: theos makes the lock. The engine creates the branch, then posts the
// offer; a task/ branch with no offer and no pull request is its own start
// that stopped halfway, which the next tick finishes.
func TestNextStart267(t *testing.T) {
	cases := []struct {
		name                   string
		branch, offered, pullR bool
		want                   StartStep
	}{
		{"#267 no branch: create it", false, false, false, StartCreateBranch},
		{"#267 a branch with no offer and no pull request: post the offer", true, false, false, StartPostOffer},
		{"#267 offered: started", true, true, false, StartDone},
		{"#267 a pull request is open: started", true, false, true, StartDone},
	}
	for _, c := range cases {
		if got := NextStart(c.branch, c.offered, c.pullR); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
}

// #267: the marker grammar. A marker is a whole comment that begins with
// its marker line, which reads back as it was written.
func TestMarkers267(t *testing.T) {
	line, err := MarkerLine(MarkerOffer, map[string]string{"task": "web-12", "round": "2", "role": "implementer"})
	if err != nil {
		t.Fatal(err)
	}
	if want := "<!-- theos:offer role=implementer round=2 task=web-12 -->"; line != want {
		t.Errorf("offer line %q, want %q", line, want)
	}
	m, ok := ParseMarker(HostComment{Body: line + "\n\nThe work order, with <!-- theos:claim session=x --> in it."})
	if !ok || m.Kind != MarkerOffer || m.Attrs["round"] != "2" || m.Session() != "" {
		t.Errorf("read back %+v, %v", m, ok)
	}
	for _, body := range []string{
		"",
		"Taking this. <!-- theos:claim session=lead-a -->",
		"> <!-- theos:claim session=lead-a -->",
		"<!-- theos:merge session=lead-a -->",
		"<!-- theos:claim session=lead-a session=lead-b -->",
		"<!-- theos:claim session=lead a -->",
		"<!-- theos:request brief=shop stage=plan -->",
	} {
		if m, ok := ParseMarker(HostComment{Body: body}); ok {
			t.Errorf("%q read as a marker %+v", body, m)
		}
	}
	for _, bad := range []string{"", "lead a", "-x", strings.Repeat("a", 64), "a-->"} {
		if _, err := SessionMarker(MarkerClaim, bad); err == nil {
			t.Errorf("session name %q taken", bad)
		}
	}
	if _, err := SessionMarker(MarkerOffer, "lead-a"); err == nil {
		t.Error("a session posted an offer")
	}
	if _, err := MarkerLine("ended", nil); err == nil {
		t.Error("a kind #307 adds was taken before it")
	}
	if _, err := MarkerLine(MarkerClaim, map[string]string{"Session": "a"}); err == nil {
		t.Error("an upper-case key was taken")
	}
	if _, err := NextClaim(ClaimFacts{}, DefaultClaimRules("me"), "me", "lead a"); err == nil {
		t.Error("NextClaim took a session name of two words")
	}
}
