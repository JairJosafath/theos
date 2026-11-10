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

// TestCronNext covers #63: the time a Crew's schedule is next due.
func TestCronNext(t *testing.T) {
	amsterdam, err := time.LoadLocation("Europe/Amsterdam")
	if err != nil {
		t.Fatal(err)
	}
	kolkata, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		t.Fatal(err)
	}
	at := func(loc *time.Location, s string) time.Time {
		t.Helper()
		v, err := time.ParseInLocation("2006-01-02 15:04", s, loc)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	tests := []struct {
		name  string
		cron  string
		loc   *time.Location
		after string
		want  string
	}{
		{"every Monday at six, from a Wednesday", "0 6 * * 1", time.UTC, "2026-11-04 12:00", "2026-11-09 06:00"},
		{"the same minute is not next: the one a week on is", "0 6 * * 1", time.UTC, "2026-11-09 06:00", "2026-11-16 06:00"},
		{"a second before the minute still gets that minute", "0 6 * * 1", time.UTC, "2026-11-09 05:59", "2026-11-09 06:00"},
		{"every quarter of an hour", "*/15 * * * *", time.UTC, "2026-11-09 06:07", "2026-11-09 06:15"},
		{"a list and a range", "30 8,17 * * 1-5", time.UTC, "2026-11-06 17:30", "2026-11-09 08:30"},
		{"a start with a step: from 5 on, every 20", "5/20 * * * *", time.UTC, "2026-11-09 06:26", "2026-11-09 06:45"},
		{"the first of the month, over a year's end", "0 0 1 * *", time.UTC, "2026-12-15 10:00", "2027-01-01 00:00"},
		{"one month only", "0 9 15 3 *", time.UTC, "2026-03-15 09:00", "2027-03-15 09:00"},
		{"Sunday as 7", "0 12 * * 7", time.UTC, "2026-11-09 00:00", "2026-11-15 12:00"},
		{"Sunday as 0", "0 12 * * 0", time.UTC, "2026-11-09 00:00", "2026-11-15 12:00"},
		{"both days named: either matches, here the 13th before Friday", "0 0 13 * 5", time.UTC, "2026-11-09 00:00", "2026-11-13 00:00"},
		{"both days named: here the Friday before the 13th", "0 0 13 * 5", time.UTC, "2026-11-14 00:00", "2026-11-20 00:00"},
		{"the 29th of February waits for a leap year", "0 0 29 2 *", time.UTC, "2026-11-09 00:00", "2028-02-29 00:00"},
		{"six in Amsterdam is five in UTC in winter", "0 6 * * *", amsterdam, "2026-11-09 06:00", "2026-11-10 06:00"},
		{"a zone half an hour off: the top of its own hour", "0 * * * *", kolkata, "2026-11-09 10:10", "2026-11-09 11:00"},
		{"the hour the clock skips doesn't come that day", "30 2 * * *", amsterdam, "2026-03-28 03:00", "2026-03-30 02:30"},
		{"after the skipped hour the day goes on", "0 3 * * *", amsterdam, "2026-03-29 01:00", "2026-03-29 03:00"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cron, err := ParseCron(tt.cron)
			if err != nil {
				t.Fatal(err)
			}
			got, ok := cron.Next(at(tt.loc, tt.after), tt.loc)
			if want := at(tt.loc, tt.want); !ok || !got.Equal(want) {
				t.Errorf("Next(%s) = %s, %v; want %s", tt.after, got.Format("2006-01-02 15:04 MST"), ok, want.Format("2006-01-02 15:04 MST"))
			}
		})
	}

	t.Run("six in Amsterdam is 05:00 UTC in winter and 04:00 in summer", func(t *testing.T) {
		cron, _ := ParseCron("0 6 * * *")
		winter, _ := cron.Next(time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC), amsterdam)
		summer, _ := cron.Next(time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC), amsterdam)
		if winter.UTC().Hour() != 5 || summer.UTC().Hour() != 4 {
			t.Errorf("winter %s, summer %s", winter.UTC(), summer.UTC())
		}
	})
	t.Run("a day that never comes has no next time", func(t *testing.T) {
		cron, err := ParseCron("0 0 30 2 *")
		if err != nil {
			t.Fatal(err)
		}
		if got, ok := cron.Next(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.UTC); ok {
			t.Errorf("the 30th of February came at %s", got)
		}
	})
}

func TestParseCronRefuses(t *testing.T) {
	for cron, want := range map[string]string{
		"0 6 * *":      "has 4 fields, want 5",
		"0 6 * * 1 *":  "has 6 fields, want 5",
		"60 6 * * 1":   `minute "60": out of 0 to 59`,
		"0 24 * * 1":   `hour "24": out of 0 to 23`,
		"0 6 0 * *":    `day of month "0": out of 1 to 31`,
		"0 6 * 13 *":   `month "13": out of 1 to 12`,
		"0 6 * * 8":    `day of week "8": out of 0 to 7`,
		"0 6 * * MON":  `day of week "MON": "MON" is not a number, a range or *`,
		"*/0 6 * * 1":  `the step "0" is not a number from 1 up`,
		"5-1 6 * * 1":  `minute "5-1": out of 0 to 59`,
		"0 6 * * 1,":   `day of week ""`,
		"@daily":       "has 1 fields, want 5",
		"0 6 1-x * *":  `"x" is not a number`,
		"a-3 6 * * *":  `"a" is not a number`,
		"0 6 * * 1/x":  `the step "x" is not a number from 1 up`,
		"0 6 * * -1":   `"" is not a number`,
		"0  6 * *":     "has 4 fields, want 5",
		"0 6 32 * *":   `day of month "32": out of 1 to 31`,
		"0 6 * 0 *":    `month "0": out of 1 to 12`,
		"0 6 * * 1-9":  `day of week "1-9": out of 0 to 7`,
		"0 6 * * 1--2": `day of week "1--2": out of 0 to 7`,
	} {
		if _, err := ParseCron(cron); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("ParseCron(%q) = %v, want an error with %q", cron, err, want)
		}
	}
}

func TestCheckSchedule(t *testing.T) {
	ok := Schedule{Name: "dependencies", Cron: "0 6 * * 1", TimeZone: "Europe/Amsterdam", Goal: "Update them."}
	if err := CheckSchedule(ok); err != nil {
		t.Errorf("a good schedule: %v", err)
	}
	if err := CheckSchedule(Schedule{Cron: "0 6 * * 1"}); err != nil {
		t.Errorf("no time zone means UTC: %v", err)
	}
	for name, tt := range map[string]struct {
		s    Schedule
		want string
	}{
		"a time zone the operator can't load": {Schedule{Cron: "0 6 * * 1", TimeZone: "Mars/Olympus"}, `time zone "Mars/Olympus" is not one the operator can load`},
		"the device's own zone":               {Schedule{Cron: "0 6 * * 1", TimeZone: "Local"}, `time zone "Local"`},
		"a zone with a space":                 {Schedule{Cron: "0 6 * * 1", TimeZone: " UTC"}, `time zone " UTC"`},
		"a cron that doesn't parse":           {Schedule{Cron: "0 6 * *"}, "has 4 fields"},
		"a cron that names no day":            {Schedule{Cron: "0 0 31 2 *"}, "names no day that exists"},
	} {
		if err := CheckSchedule(tt.s); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: got %v, want an error with %q", name, err, tt.want)
		}
	}
}

// TestNextScheduleStep covers #63: one issue when a time came due, none while
// the last one is open, and one, not one per missed time, after a long gap.
func TestNextScheduleStep(t *testing.T) {
	weekly := Schedule{Name: "dependencies", Cron: "0 6 * * 1", Goal: "Update the dependencies."}
	monday := time.Date(2026, 11, 9, 6, 0, 0, 0, time.UTC)
	next := monday.AddDate(0, 0, 7)
	tests := []struct {
		name  string
		facts ScheduleFacts
		now   time.Time
		want  ScheduleStep
	}{
		{name: "before its time nothing is due",
			facts: ScheduleFacts{Schedule: weekly, Since: monday.Add(-48 * time.Hour)}, now: monday.Add(-time.Minute),
			want: ScheduleStep{Next: monday}},
		{name: "at its time it files",
			facts: ScheduleFacts{Schedule: weekly, Since: monday.Add(-48 * time.Hour)}, now: monday,
			want: ScheduleStep{Due: monday, File: true, Next: next}},
		{name: "a poll that comes late still files",
			facts: ScheduleFacts{Schedule: weekly, Since: monday.Add(-48 * time.Hour)}, now: monday.Add(3 * time.Hour),
			want: ScheduleStep{Due: monday, File: true, Next: next}},
		{name: "having filed, it waits a week",
			facts: ScheduleFacts{Schedule: weekly, Since: monday.Add(time.Second)}, now: monday.Add(3 * time.Hour),
			want: ScheduleStep{Next: next}},
		{name: "three missed weeks file one issue, for the first time missed",
			facts: ScheduleFacts{Schedule: weekly, Since: monday.Add(-time.Hour)}, now: monday.AddDate(0, 0, 16),
			want: ScheduleStep{Due: monday, File: true, Next: monday.AddDate(0, 0, 21)}},
		{name: "a due time is passed over while the last issue is open",
			facts: ScheduleFacts{Schedule: weekly, Since: monday.Add(-time.Hour), Open: true}, now: monday.Add(time.Minute),
			want: ScheduleStep{Due: monday, Skip: true, Next: next}},
		{name: "an open issue changes nothing before the time",
			facts: ScheduleFacts{Schedule: weekly, Since: monday.Add(-time.Hour), Open: true}, now: monday.Add(-time.Minute),
			want: ScheduleStep{Next: monday}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NextScheduleStep(tt.facts, tt.now)
			if err != nil {
				t.Fatal(err)
			}
			if !got.Due.Equal(tt.want.Due) || got.File != tt.want.File || got.Skip != tt.want.Skip || !got.Next.Equal(tt.want.Next) {
				t.Errorf("got %+v\nwant %+v", got, tt.want)
			}
		})
	}
	t.Run("a schedule that can't be read says why", func(t *testing.T) {
		if _, err := NextScheduleStep(ScheduleFacts{Schedule: Schedule{Cron: "nope"}}, monday); err == nil {
			t.Error("no error")
		}
	})
}

func TestScheduleIssue(t *testing.T) {
	s := Schedule{Name: "dependencies", Cron: "0 6 * * 1", TimeZone: "Europe/Amsterdam",
		Goal: "  Update the dependencies. <!-- theos-schedule: other -->\n"}
	title, body := ScheduleIssue(s, time.Date(2026, 11, 9, 5, 0, 0, 0, time.UTC))
	if title != "dependencies, 2026-11-09" {
		t.Errorf("title %q", title)
	}
	for _, want := range []string{
		"The Crew's schedule `dependencies` (`0 6 * * 1`, Europe/Amsterdam) came due at 2026-11-09 06:00.",
		"## Goal\n\nUpdate the dependencies. &lt;!-- theos-schedule: other -->\n\n",
		"## Acceptance criteria\n\n- [ ] The goal is met",
		"- [ ] `make check` passes",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the body lacks %q:\n%s", want, body)
		}
	}
	if reason := MissingAcceptanceCriteria(body); reason != "" {
		t.Errorf("the Crew would hand its own issue back: %s", reason)
	}
	if !HasScheduleMarker(body, "dependencies") {
		t.Error("the body lacks its schedule's marker")
	}
	if HasScheduleMarker(body, "other") {
		t.Error("a marker in the goal's text passes for the schedule's")
	}
	if HasScheduleMarker("see "+ScheduleMarker("dependencies")+" here", "dependencies") {
		t.Error("a marker inside a line passes for one on its own line")
	}
}

// #72: what the Scheduled condition says of a request to run a schedule now.
func TestRunNowMessages(t *testing.T) {
	for _, tt := range []struct{ name, got, want string }{
		{"held by its open issue", RunNowWaits("dependencies", false),
			"schedule dependencies is asked to run now, and waits for its open issue to close"},
		{"held by a paused Crew", RunNowWaits("dependencies", true),
			"schedule dependencies is asked to run now, and runs when the Crew resumes"},
		{"a schedule the Crew doesn't have", RunNowUnknown("dependecies", []string{"dependencies", "audit"}),
			`asked to run schedule "dependecies" now, which is none of the Crew's: the Crew's schedules are ` +
				"dependencies, audit"},
		{"a Crew with no schedules", RunNowUnknown("dependencies", nil),
			`asked to run schedule "dependencies" now, which is none of the Crew's: the Crew has no schedules`},
	} {
		if tt.got != tt.want {
			t.Errorf("%s: %q, want %q", tt.name, tt.got, tt.want)
		}
	}
}
