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

// nov is a time on a day of November 2026, UTC: the 6th is a Friday.
func nov(day, hour, minute, second int) time.Time {
	return time.Date(2026, 11, day, hour, minute, second, 0, time.UTC)
}

var daily = ReleaseCadence{Every: CadenceDay, At: "18:00", TimeZone: "UTC"}

func TestNextCut(t *testing.T) {
	amsterdam, err := time.LoadLocation("Europe/Amsterdam")
	if err != nil {
		t.Fatal(err)
	}
	local := func(y int, m time.Month, d, h int) time.Time { return time.Date(y, m, d, h, 0, 0, 0, amsterdam) }
	tests := []struct {
		name          string
		cadence       ReleaseCadence
		lastCut, now  time.Time
		want          time.Time
		wantErrSubstr string
	}{
		{"daily, before at: today's", daily, nov(5, 18, 0, 5), nov(6, 10, 0, 0), nov(6, 18, 0, 0), ""},
		{"daily, after at: tomorrow's", daily, nov(6, 18, 0, 5), nov(6, 19, 0, 0), nov(7, 18, 0, 0), ""},
		{"a cut on the very minute of at is not due again", daily, nov(6, 18, 0, 0), nov(6, 18, 0, 0),
			nov(7, 18, 0, 0), ""},
		{"a cut missed while the operator was down is due at once", daily, nov(4, 18, 0, 5), nov(6, 10, 0, 0),
			nov(5, 18, 0, 0), ""},
		{"no cut yet counts from now, before at", daily, time.Time{}, nov(6, 10, 0, 0), nov(6, 18, 0, 0), ""},
		{"no cut yet counts from now, after at", daily, time.Time{}, nov(6, 19, 0, 0), nov(7, 18, 0, 0), ""},
		{"unset fields take their defaults", ReleaseCadence{Every: CadenceDay}, nov(6, 9, 0, 0), nov(6, 9, 0, 0),
			nov(6, 18, 0, 0), ""},
		{"another hour", ReleaseCadence{Every: CadenceDay, At: "07:30"}, nov(6, 9, 0, 0), nov(6, 9, 0, 0),
			time.Date(2026, 11, 7, 7, 30, 0, 0, time.UTC), ""},
		{"spring forward in Europe/Amsterdam: 23 hours after the last cut",
			ReleaseCadence{Every: CadenceDay, At: "18:00", TimeZone: "Europe/Amsterdam"},
			local(2026, time.March, 28, 18), local(2026, time.March, 28, 18), local(2026, time.March, 29, 18), ""},
		{"fall back in Europe/Amsterdam: 25 hours after the last cut",
			ReleaseCadence{Every: CadenceDay, At: "18:00", TimeZone: "Europe/Amsterdam"},
			local(2026, time.October, 24, 18), local(2026, time.October, 24, 18), local(2026, time.October, 25, 18),
			""},
		{"weekly on Friday from a Saturday: next week's",
			ReleaseCadence{Every: CadenceWeek, At: "18:00", Weekday: "Friday"}, time.Time{}, nov(7, 10, 0, 0),
			nov(13, 18, 0, 0), ""},
		{"weekly, Friday by default, before at on a Friday: today's", ReleaseCadence{Every: CadenceWeek},
			nov(30, 18, 0, 0).AddDate(0, -1, 0), nov(6, 10, 0, 0), nov(6, 18, 0, 0), ""},
		{"weekly on Monday", ReleaseCadence{Every: CadenceWeek, Weekday: "Monday"}, nov(6, 18, 0, 5),
			nov(6, 18, 0, 5), nov(9, 18, 0, 0), ""},
		{"a time zone the operator can't load", ReleaseCadence{Every: CadenceDay, TimeZone: "Mars/Olympus"},
			time.Time{}, nov(6, 10, 0, 0), time.Time{}, `time zone "Mars/Olympus" is not one the operator can load`},
		{"the operator's own zone is no IANA name", ReleaseCadence{Every: CadenceDay, TimeZone: "Local"},
			time.Time{}, nov(6, 10, 0, 0), time.Time{}, `time zone "Local"`},
		{"an at that is no time of day", ReleaseCadence{Every: CadenceDay, At: "24:00"}, time.Time{},
			nov(6, 10, 0, 0), time.Time{}, `at "24:00" is not a time of day`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NextCut(tt.cadence, tt.lastCut, tt.now)
			if tt.wantErrSubstr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrSubstr) {
					t.Fatalf("NextCut() error = %v, want one saying %q", err, tt.wantErrSubstr)
				}
				if CheckCadence(tt.cadence) == nil {
					t.Errorf("CheckCadence() = nil, want the error")
				}
				return
			}
			if err != nil || !got.Equal(tt.want) {
				t.Errorf("NextCut() = %v, %v; want %v", got, err, tt.want)
			}
		})
	}
	if CheckCadence(ReleaseCadence{Every: CadenceRelease, TimeZone: "Mars/Olympus"}) != nil {
		t.Error("CheckCadence() of every: Release fails on a time zone it never reads")
	}
}

func TestCutTime(t *testing.T) {
	for _, tc := range []struct {
		cadence  ReleaseCadence
		cut, now time.Time
		want     string
	}{
		{daily, nov(6, 18, 0, 0), nov(6, 10, 0, 0), "18:00 UTC"},
		{daily, nov(7, 18, 0, 0), nov(6, 19, 0, 0), "18:00 UTC on Saturday"},
		// 18:00 in Amsterdam is 17:00 UTC; at 23:30 UTC on the 6th it is
		// the 7th there.
		{ReleaseCadence{Every: CadenceDay, TimeZone: "Europe/Amsterdam"}, nov(7, 17, 0, 0), nov(6, 23, 30, 0),
			"18:00 Europe/Amsterdam"},
	} {
		if got := CutTime(tc.cadence, tc.cut, tc.now); got != tc.want {
			t.Errorf("CutTime(%v, %v) = %q, want %q", tc.cut, tc.now, got, tc.want)
		}
	}
}

func TestNextReleaseCadence(t *testing.T) {
	day := func(d int) time.Time { return nov(d, 0, 0, 0) }
	merged := func(issue int, commit string, d int) MergedTask {
		return MergedTask{Name: "t-" + commit, Issue: issue, Commit: commit, At: day(d)}
	}
	release := func(id int64, title string, due time.Time, open, closed []int) ReleaseMilestone {
		return ReleaseMilestone{ID: id, Release: Release{Title: title, Due: due}, Open: open, Closed: closed}
	}
	cut := func(m ReleaseMilestone) ReleaseMilestone { m.Cut = true; return m }
	prepared := func(m ReleaseMilestone, n ...int) ReleaseMilestone { m.Prepared = n; return m }
	// v0.14.0 went out; v0.15.0, the day's release, holds two merged issues
	// and one still at work.
	previous := &ReleasedTag{Release: Release{Title: "v0.14.0", Due: day(1)}, Commit: "c140"}
	v015 := release(15, "v0.15.0", nov(6, 18, 0, 0), []int{3}, []int{1, 2})
	work := []MergedTask{merged(1, "c1", 5), merged(2, "c2", 6)}
	titles := []string{"Later", "v0.14.0", "v0.15.0"}
	facts := func(now time.Time, milestones ...ReleaseMilestone) ReleaseFacts {
		return ReleaseFacts{Cadence: daily, Now: now, LastCut: nov(5, 18, 0, 5), Milestones: milestones,
			Merged: work, Previous: previous, Titles: titles, Tags: map[string]string{}}
	}
	morning, cutTime := nov(6, 10, 0, 0), nov(6, 18, 0, 30)
	with := func(f ReleaseFacts, change func(*ReleaseFacts)) ReleaseFacts { change(&f); return f }

	tests := []struct {
		name  string
		facts ReleaseFacts
		want  ReleaseStep
	}{
		{"before its cut a release waits, saying what it holds and when it goes out",
			facts(morning, v015),
			ReleaseStep{Tag: "v0.15.0", Reason: ReleaseWaitingForCut,
				Held: "v0.15.0 holds 2 merged issues and goes out at 18:00 UTC"}},
		{"a done release waits as well",
			facts(morning, release(15, "v0.15.0", nov(6, 18, 0, 0), nil, []int{1, 2})),
			ReleaseStep{Tag: "v0.15.0", Reason: ReleaseWaitingForCut,
				Held: "v0.15.0 holds 2 merged issues and goes out at 18:00 UTC"}},
		{"one with nothing merged yet says so",
			with(facts(morning, v015), func(f *ReleaseFacts) { f.Merged = nil }),
			ReleaseStep{Tag: "v0.15.0", Reason: ReleaseWaitingForCut,
				Held: "v0.15.0 holds no merged issue yet; its cut is at 18:00 UTC"}},
		{"after today's cut the next is tomorrow's",
			with(facts(nov(6, 19, 0, 0), v015), func(f *ReleaseFacts) { f.LastCut = nov(6, 18, 0, 5) }),
			ReleaseStep{Tag: "v0.15.0", Reason: ReleaseWaitingForCut,
				Held: "v0.15.0 holds 2 merged issues and goes out at 18:00 UTC on Saturday"}},
		{"at the cut its open issue is carried into a new release, due at the next cut",
			facts(cutTime, v015),
			ReleaseStep{Tag: "v0.15.0", Milestone: 15, CutAt: cutTime,
				Cut: &ReleaseCut{Carry: []int{3}, IntoTitle: "v0.16.0", IntoDue: nov(7, 18, 0, 0)}}},
		{"a cut long overdue is made at once",
			with(facts(cutTime, v015), func(f *ReleaseFacts) { f.LastCut = nov(1, 18, 0, 5) }),
			ReleaseStep{Tag: "v0.15.0", Milestone: 15, CutAt: cutTime,
				Cut: &ReleaseCut{Carry: []int{3}, IntoTitle: "v0.16.0", IntoDue: nov(7, 18, 0, 0)}}},
		{"it is carried into the next open release that can still take work",
			facts(cutTime, v015, release(16, "v0.16.0", day(13), []int{9}, nil)),
			ReleaseStep{Tag: "v0.15.0", Milestone: 15, CutAt: cutTime, Cut: &ReleaseCut{Carry: []int{3}, Into: 16}}},
		{"an empty one later takes it too",
			facts(cutTime, v015, release(16, "v0.16.0", day(13), nil, nil)),
			ReleaseStep{Tag: "v0.15.0", Milestone: 15, CutAt: cutTime, Cut: &ReleaseCut{Carry: []int{3}, Into: 16}}},
		{"a prepared one is passed over, and the new one is due after it",
			with(facts(cutTime, v015, prepared(release(16, "v0.16.0", day(13), []int{10}, nil), 10)),
				func(f *ReleaseFacts) { f.Titles = append(titles, "v0.16.0") }),
			ReleaseStep{Tag: "v0.15.0", Milestone: 15, CutAt: cutTime,
				Cut: &ReleaseCut{Carry: []int{3}, IntoTitle: "v0.17.0", IntoDue: day(13).Add(time.Hour)}}},
		{"a done release has nothing to carry",
			facts(cutTime, release(15, "v0.15.0", nov(6, 18, 0, 0), nil, []int{1, 2})),
			ReleaseStep{Tag: "v0.15.0", Milestone: 15, CutAt: cutTime, Cut: &ReleaseCut{}}},
		{"with no merged work anywhere nothing is cut, and the cut counts as made",
			with(facts(cutTime, v015), func(f *ReleaseFacts) { f.Merged = nil }),
			ReleaseStep{CutAt: cutTime}},
		{"with no release at all the same",
			facts(cutTime), ReleaseStep{CutAt: cutTime}},
		{"with no release before the cut, nothing",
			facts(morning), ReleaseStep{}},
		{"a release with no merged work of its own is carried on and closed untagged, for a later one with work",
			with(facts(cutTime, release(15, "v0.15.0", nov(6, 18, 0, 0), []int{3}, nil),
				release(16, "v0.16.0", day(13), nil, []int{9})),
				func(f *ReleaseFacts) { f.Merged = []MergedTask{merged(9, "c9", 6)} }),
			ReleaseStep{Tag: "v0.15.0", Milestone: 15, Cut: &ReleaseCut{Carry: []int{3}, Into: 16, Untagged: true}}},
		{"a merged release/now issue cuts its release before the cut time",
			with(facts(morning, v015), func(f *ReleaseFacts) { f.ReleaseNow = []int{2} }),
			ReleaseStep{Tag: "v0.15.0", Milestone: 15, CutAt: morning,
				Cut: &ReleaseCut{Carry: []int{3}, IntoTitle: "v0.16.0", IntoDue: nov(6, 19, 0, 0), ReleaseNow: true}}},
		{"one in a later release cuts the release before it first",
			with(facts(morning, v015, release(16, "v0.16.0", day(13), nil, []int{9})), func(f *ReleaseFacts) {
				f.ReleaseNow, f.Merged = []int{9}, append(work, merged(9, "c9", 6))
			}),
			ReleaseStep{Tag: "v0.15.0", Milestone: 15, CutAt: morning,
				Cut: &ReleaseCut{Carry: []int{3}, Into: 16, ReleaseNow: true}}},
		{"an open release/now issue cuts nothing",
			with(facts(morning, v015), func(f *ReleaseFacts) { f.ReleaseNow = []int{3} }),
			ReleaseStep{Tag: "v0.15.0", Reason: ReleaseWaitingForCut,
				Held: "v0.15.0 holds 2 merged issues and goes out at 18:00 UTC"}},
		{"a day of fixes takes the next patch title, and the carry the minor after it",
			with(facts(cutTime, v015), func(f *ReleaseFacts) { f.Fixes = []int{1, 2} }),
			ReleaseStep{Tag: "v0.15.0", Milestone: 15, CutAt: cutTime, Cut: &ReleaseCut{Retitle: "v0.14.1",
				Carry: []int{3}, IntoTitle: "v0.15.0", IntoDue: nov(7, 18, 0, 0)}}},
		{"the open issue at work counts for nothing: only merged work decides",
			with(facts(cutTime, release(15, "v0.15.0", nov(6, 18, 0, 0), nil, []int{1, 2})),
				func(f *ReleaseFacts) { f.Fixes = []int{1, 2} }),
			ReleaseStep{Tag: "v0.15.0", Milestone: 15, CutAt: cutTime, Cut: &ReleaseCut{Retitle: "v0.14.1"}}},
		{"mixed work keeps its minor title",
			with(facts(cutTime, v015), func(f *ReleaseFacts) { f.Fixes = []int{1} }),
			ReleaseStep{Tag: "v0.15.0", Milestone: 15, CutAt: cutTime,
				Cut: &ReleaseCut{Carry: []int{3}, IntoTitle: "v0.16.0", IntoDue: nov(7, 18, 0, 0)}}},
		{"a patch title that exists is skipped",
			with(facts(cutTime, v015), func(f *ReleaseFacts) {
				f.Fixes, f.Titles = []int{1, 2}, append(titles, "v0.14.1")
			}),
			ReleaseStep{Tag: "v0.15.0", Milestone: 15, CutAt: cutTime, Cut: &ReleaseCut{Retitle: "v0.14.2",
				Carry: []int{3}, IntoTitle: "v0.15.0", IntoDue: nov(7, 18, 0, 0)}}},
		{"a release titled with a new major keeps it",
			with(facts(cutTime, release(15, "v1.0.0", nov(6, 18, 0, 0), nil, []int{1, 2})),
				func(f *ReleaseFacts) { f.Fixes = []int{1, 2} }),
			ReleaseStep{Tag: "v1.0.0", Milestone: 15, CutAt: cutTime, Cut: &ReleaseCut{}}},
		{"a patch keeps its title",
			with(facts(cutTime, release(15, "v0.14.1", nov(6, 18, 0, 0), nil, []int{1, 2})),
				func(f *ReleaseFacts) { f.Fixes = []int{1, 2} }),
			ReleaseStep{Tag: "v0.14.1", Milestone: 15, CutAt: cutTime, Cut: &ReleaseCut{}}},
		{"with no previous release the first keeps its title",
			with(facts(cutTime, release(15, "v0.1.0", nov(6, 18, 0, 0), nil, []int{1, 2})),
				func(f *ReleaseFacts) { f.Fixes, f.Previous = []int{1, 2}, nil }),
			ReleaseStep{Tag: "v0.1.0", Milestone: 15, CutAt: cutTime, Cut: &ReleaseCut{}}},
		{"a cut release carries what is left of its issues, a restart part way included",
			facts(nov(6, 18, 1, 0), cut(v015), release(16, "v0.16.0", nov(7, 18, 0, 0), []int{4}, nil)),
			ReleaseStep{Tag: "v0.15.0", Milestone: 15, Cut: &ReleaseCut{Marked: true, Carry: []int{3}, Into: 16}}},
		{"and is retitled, when it was not yet",
			with(facts(nov(6, 18, 1, 0), cut(release(15, "v0.15.0", nov(6, 18, 0, 0), nil, []int{1, 2}))),
				func(f *ReleaseFacts) { f.Fixes = []int{1, 2} }),
			ReleaseStep{Tag: "v0.15.0", Milestone: 15, Cut: &ReleaseCut{Marked: true, Retitle: "v0.14.1"}}},
		{"once carried out it goes out as with every Release",
			facts(nov(6, 18, 1, 0), cut(release(15, "v0.15.0", nov(6, 18, 0, 0), nil, []int{1, 2})),
				release(16, "v0.16.0", nov(7, 18, 0, 0), []int{3}, nil)),
			ReleaseStep{Tag: "v0.15.0", Commit: "c2", Milestone: 15, CreateTag: true}},
		{"its prepare issue is filed while other Tasks work (C10)",
			with(facts(nov(6, 18, 1, 0), cut(release(15, "v0.15.0", nov(6, 18, 0, 0), nil, []int{1, 2})),
				release(16, "v0.16.0", nov(7, 18, 0, 0), []int{3}, nil)),
				func(f *ReleaseFacts) { f.Prepare, f.Active, f.Working = true, []int{3}, []string{"t-3"} }),
			ReleaseStep{Tag: "v0.15.0", Milestone: 15, File: true, Merged: []int{1, 2}}},
		{"its open prepare issue stays and holds it, and once prepared it keeps its title",
			with(facts(nov(6, 19, 0, 0), cut(prepared(release(15, "v0.15.0", nov(6, 18, 0, 0), []int{7}, []int{1, 2}),
				7))), func(f *ReleaseFacts) { f.Prepare, f.Fixes = true, []int{1, 2} }),
			ReleaseStep{}},
		{"so does an open issue of its full check",
			facts(nov(6, 19, 0, 0), ReleaseMilestone{ID: 15, Release: Release{Title: "v0.15.0", Due: nov(6, 18, 0, 0)},
				Open: []int{8}, Closed: []int{1, 2}, Cut: true, FullChecks: []FullCheckFix{{Issue: 8, Commit: "c2"}}}),
			ReleaseStep{}},
		{"a Task at work on its closed issue holds it, with no word about later releases",
			with(facts(nov(6, 18, 1, 0), cut(release(15, "v0.15.0", nov(6, 18, 0, 0), nil, []int{1, 2})),
				release(16, "v0.16.0", nov(7, 18, 0, 0), nil, []int{9})),
				func(f *ReleaseFacts) { f.Active, f.Merged = []int{2}, append(work, merged(9, "c9", 6)) }),
			ReleaseStep{}},
		{"a time zone the operator can't load holds every release",
			with(facts(cutTime, v015), func(f *ReleaseFacts) { f.Cadence.TimeZone = "Mars/Olympus" }),
			ReleaseStep{Tag: "v0.15.0", Reason: ReasonInvalidTimeZone, Held: `time zone "Mars/Olympus" is not one ` +
				"the operator can load; name an IANA time zone, such as UTC or Europe/Amsterdam"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NextRelease(tt.facts); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("NextRelease() =\n %+v %+v\nwant\n %+v %+v", got, got.Cut, tt.want, tt.want.Cut)
			}
		})
	}
}

func TestNextCutOf(t *testing.T) {
	f := ReleaseFacts{Cadence: daily, Now: nov(6, 10, 0, 0), LastCut: nov(5, 18, 0, 5),
		Milestones: []ReleaseMilestone{
			{ID: 16, Release: Release{Title: "v0.16.0", Due: nov(7, 18, 0, 0)}, Open: []int{4}, Closed: []int{5}},
			{ID: 15, Release: Release{Title: "v0.15.0", Due: nov(6, 18, 0, 0)}, Closed: []int{1, 2}, Cut: true},
		},
		Merged: []MergedTask{{Name: "t-1", Issue: 1, Commit: "c1", At: nov(5, 1, 0, 0)},
			{Name: "t-5", Issue: 5, Commit: "c5", At: nov(6, 1, 0, 0)}}}
	got, err := NextCutOf(f)
	if want := (UpcomingCut{At: nov(6, 18, 0, 0), Release: "v0.16.0", Merged: 1}); err != nil || got != want {
		t.Errorf("NextCutOf() = %+v, %v; want %+v, the release after the one cut", got, err, want)
	}
	f.Milestones = nil
	if got, err := NextCutOf(f); err != nil || got != (UpcomingCut{At: nov(6, 18, 0, 0)}) {
		t.Errorf("NextCutOf() with no release = %+v, %v; want only the time", got, err)
	}
	f.Cadence.TimeZone = "Mars/Olympus"
	if _, err := NextCutOf(f); err == nil {
		t.Error("NextCutOf() with a time zone the operator can't load: no error")
	}
}

func TestCadenceSlot(t *testing.T) {
	milestone := func(id int64, title string, due time.Time) ReleaseMilestone {
		return ReleaseMilestone{ID: id, Release: Release{Title: title, Due: due}}
	}
	closed := func(m ReleaseMilestone) ReleaseMilestone { m.MilestoneClosed = true; return m }
	now := nov(6, 10, 0, 0)
	tests := []struct {
		name       string
		milestones []ReleaseMilestone
		want       Slot
	}{
		{"no milestone at all: v0.1.0, due at the next cut", nil, Slot{Title: "v0.1.0", Due: nov(6, 18, 0, 0)}},
		{"the release in progress is joined",
			[]ReleaseMilestone{milestone(4, "Later", time.Time{}), closed(milestone(1, "v0.14.0", nov(1, 0, 0, 0))),
				milestone(3, "v0.16.0", nov(13, 0, 0, 0)), milestone(2, "v0.15.0", nov(6, 18, 0, 0))},
			Slot{ID: 2, Title: "v0.15.0", Due: nov(6, 18, 0, 0)}},
		{"a prepared one is passed over",
			[]ReleaseMilestone{{ID: 2, Release: Release{Title: "v0.15.0", Due: nov(6, 18, 0, 0)}, Prepared: []int{7}},
				milestone(3, "v0.16.0", nov(7, 18, 0, 0))},
			Slot{ID: 3, Title: "v0.16.0", Due: nov(7, 18, 0, 0)}},
		{"and so is a cut one",
			[]ReleaseMilestone{{ID: 2, Release: Release{Title: "v0.15.0", Due: nov(6, 18, 0, 0)}, Cut: true},
				milestone(3, "v0.16.0", nov(7, 18, 0, 0))},
			Slot{ID: 3, Title: "v0.16.0", Due: nov(7, 18, 0, 0)}},
		{"with none open taking work, the next minor after every version, due after every open release",
			[]ReleaseMilestone{{ID: 2, Release: Release{Title: "v0.15.0", Due: nov(6, 18, 0, 0)}, Cut: true},
				closed(milestone(1, "v0.14.0", nov(1, 0, 0, 0)))},
			Slot{Title: "v0.16.0", Due: nov(6, 19, 0, 0)}},
		{"an undated version milestone is no release to join",
			[]ReleaseMilestone{milestone(2, "v0.15.0", time.Time{})}, Slot{Title: "v0.16.0", Due: nov(6, 18, 0, 0)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, err := CadenceSlot(tt.milestones, daily, now); err != nil || !reflect.DeepEqual(got, tt.want) {
				t.Errorf("CadenceSlot() = %+v, %v; want %+v", got, err, tt.want)
			}
		})
	}
	if _, err := CadenceSlot(nil, ReleaseCadence{Every: CadenceDay, TimeZone: "Mars/Olympus"}, now); err == nil {
		t.Error("CadenceSlot() with a time zone the operator can't load: no error")
	}
}

func TestCutMarker(t *testing.T) {
	cut := nov(6, 18, 0, 30)
	marker := CutMarker("orders-api", cut)
	if marker != "<!-- theos-crew: orders-api/cut 2026-11-06T18:00:30Z -->" {
		t.Errorf("CutMarker() = %q", marker)
	}
	description := AddMarker("The work Brief shop's round 3 filed.", marker)
	if got, ok := CutMarkTime(description); !ok || !got.Equal(cut) {
		t.Errorf("CutMarkTime(%q) = %v, %v; want %v", description, got, ok, cut)
	}
	for _, d := range []string{"", "no marker", "<!-- theos-crew: orders-api/cut yesterday -->",
		CarryMarker("orders-api", "v0.15.0"), "text " + marker} {
		if _, ok := CutMarkTime(d); ok {
			t.Errorf("CutMarkTime(%q) found a cut", d)
		}
	}
	if got := LastCut(nov(5, 18, 0, 0), []string{"", marker, CutMarker("orders-api", nov(4, 18, 0, 0))}); !got.Equal(cut) {
		t.Errorf("LastCut() = %v, want the newest mark %v", got, cut)
	}
	if got := LastCut(nov(7, 18, 0, 0), []string{marker}); !got.Equal(nov(7, 18, 0, 0)) {
		t.Errorf("LastCut() = %v, want the later one recorded", got)
	}
}

func TestCarryMarker(t *testing.T) {
	description := CarryMilestoneDescription("orders-api", "v0.15.0")
	if !strings.HasSuffix(description, "<!-- theos-crew: orders-api/carry v0.15.0 -->\n") ||
		!HasCarryMarker(description, "orders-api", "v0.15.0") {
		t.Errorf("CarryMilestoneDescription() = %q, want it to end with its marker", description)
	}
	if HasCarryMarker(description, "orders-api", "v0.14.1") || HasCarryMarker(description, "web", "v0.15.0") {
		t.Error("HasCarryMarker() matches another release's or Crew's carry")
	}
	if _, ok := CutMarkTime(description); ok {
		t.Error("a carry marker reads as a cut")
	}
}

func TestIsReleaseNow(t *testing.T) {
	if !IsReleaseNow([]string{"kind/bug", " release/now"}) || IsReleaseNow([]string{"kind/bug", "release/later"}) ||
		IsReleaseNow(nil) {
		t.Error("IsReleaseNow() wrong")
	}
	if l := ReleaseNowLabel(); l.Name != LabelReleaseNow || l.Exclusive {
		t.Errorf("ReleaseNowLabel() = %+v, want release/now, the only one in its scope", l)
	}
}

func TestReleaseTakingWorkSkipsACutRelease(t *testing.T) {
	due := nov(6, 18, 0, 0)
	got := ReleaseTakingWork([]ReleaseMilestone{
		{ID: 15, Release: Release{Title: "v0.15.0", Due: due}, Cut: true},
		{ID: 16, Release: Release{Title: "v0.16.0", Due: due.AddDate(0, 0, 1)}},
	})
	if got != 16 {
		t.Errorf("ReleaseTakingWork() = %d, want 16: a cut release takes no more work", got)
	}
}

func TestParsePlanReleaseNow(t *testing.T) {
	issue := func(front string) map[string]string {
		return map[string]string{PlanModelFile: happyModel, PlanChoicesFile: "- one\n",
			"plan/issues/01-todo.md": "---\ntitle: Fix the login crash\nlabels: [kind/bug]\n" + front +
				"---\nThe login crashes since v0.14.0.\n"}
	}
	plan, err := ParsePlan(issue("release: now\n"))
	if err != nil || len(plan.Issues) != 1 || !plan.Issues[0].ReleaseNow {
		t.Errorf("release: now: issues %+v, err %v; want one marked now", plan.Issues, err)
	}
	if plan, err := ParsePlan(issue("")); err != nil || plan.Issues[0].ReleaseNow {
		t.Errorf("no release: issues %+v, err %v; want one not marked", plan.Issues, err)
	}
	if _, err := ParsePlan(issue("release: tomorrow\n")); err == nil ||
		!strings.Contains(err.Error(), `release: has "tomorrow"; the only value is now`) {
		t.Errorf("release: tomorrow: err = %v, want it refused", err)
	}

	answers := map[string]string{PlanModelFile: happyModel, PlanChoicesFile: "- one\n",
		PlanSuggestionsFile: "- container: todo\n  issue: 5\n  answer: take\n  why: The prompt ships it today.\n" +
			"  fix: true\n  now: true\n"}
	if plan, err := ParsePlan(answers); err != nil || len(plan.Answers) != 1 || !plan.Answers[0].Now {
		t.Errorf("now: true: answers %+v, err %v; want one now", plan.Answers, err)
	}
	waiting := []Suggestion{{Container: "todo", Number: 5, Criteria: true}, {Container: "todo", Number: 6}}
	for _, tc := range []struct {
		answer SuggestionAnswer
		want   string
	}{
		{SuggestionAnswer{Container: "todo", Issue: 5, Answer: AnswerTake, Why: "today", Now: true}, ""},
		{SuggestionAnswer{Container: "todo", Issue: 6, Answer: AnswerSkip, Why: "later", Now: true},
			"skips todo#6 as now"},
		{SuggestionAnswer{Container: "todo", Issue: 7, Answer: AnswerMissing, Why: "gone", Now: true},
			"answers todo#7 missing as now"},
	} {
		got := CheckAnswers([]SuggestionAnswer{tc.answer}, waiting)
		if tc.want == "" && len(got) != 0 || tc.want != "" && (len(got) != 1 || !strings.Contains(got[0].Reason, tc.want)) {
			t.Errorf("CheckAnswers(%+v) = %v, want %q", tc.answer, got, tc.want)
		}
	}
}
