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
	"strconv"
	"strings"
	"time"
)

// A Crew's schedules (docs/plans/crew-schedules.md, #63): work that starts
// from the clock. When a schedule is due the Crew files one issue with its
// goal, and the normal loop takes it from there.

// LabelSchedule marks an issue a Crew's schedule filed.
const LabelSchedule = "kind/schedule"

// ScheduleLabel is the label the operator keeps for scheduled issues.
func ScheduleLabel() IssueLabel {
	return IssueLabel{LabelSchedule, "#1d76db", "Recurring work, filed by the Crew's schedule when it came due", false}
}

// Reasons of a Crew's Scheduled condition.
const (
	// ReasonInvalidSchedule: a schedule's cron expression or time zone can't
	// be used; the message names the schedule.
	ReasonInvalidSchedule = "InvalidSchedule"
	// ReasonScheduled: every schedule is waiting for its next time.
	ReasonScheduled = "Scheduled"
	// ReasonScheduleNotFiled: a due schedule's issue couldn't be filed; the
	// next poll tries again.
	ReasonScheduleNotFiled = "ScheduleNotFiled"
)

// DefaultScheduleZone is a schedule's time zone when it names none.
const DefaultScheduleZone = "UTC"

// Schedule is one recurring goal of a Crew.
type Schedule struct {
	Name string
	// Cron is five fields: minute, hour, day of month, month, day of week.
	Cron string
	// TimeZone is an IANA name; empty means DefaultScheduleZone.
	TimeZone string
	Goal     string
}

// Zone is the schedule's time zone.
func (s Schedule) Zone() string {
	if s.TimeZone == "" {
		return DefaultScheduleZone
	}
	return s.TimeZone
}

// Cron is a parsed cron expression: the minutes, hours, days of the month,
// months and days of the week it names, as bit sets.
type Cron struct {
	minute, hour, dom, month, dow uint64
	// domAny and dowAny say the field was "*": when both days fields are
	// restricted a day matches either, as cron has always done.
	domAny, dowAny bool
}

// cronFields are the five fields' names and bounds, in order.
var cronFields = []struct {
	name     string
	min, max int
}{{"minute", 0, 59}, {"hour", 0, 23}, {"day of month", 1, 31}, {"month", 1, 12}, {"day of week", 0, 7}}

// ParseCron reads a five-field cron expression: each field a "*", a number,
// a range "a-b", a step "*/n" or "a-b/n", or a comma-separated list of those.
// Sunday is 0 or 7. Names (MON, JAN) and macros (@daily) are not read.
func ParseCron(expr string) (Cron, error) {
	fields := strings.Fields(expr)
	if len(fields) != len(cronFields) {
		return Cron{}, fmt.Errorf("cron %q has %d fields, want 5: minute, hour, day of month, month, day of week",
			expr, len(fields))
	}
	var sets [5]uint64
	for i, field := range fields {
		f := cronFields[i]
		for part := range strings.SplitSeq(field, ",") {
			lo, hi, step, err := cronRange(part, f.min, f.max)
			if err != nil {
				return Cron{}, fmt.Errorf("cron %q: %s %q: %w", expr, f.name, part, err)
			}
			for v := lo; v <= hi; v += step {
				sets[i] |= 1 << uint(v)
			}
		}
	}
	// Sunday is 0 and 7.
	if sets[4]&(1<<7) != 0 {
		sets[4] = sets[4]&^(1<<7) | 1
	}
	return Cron{minute: sets[0], hour: sets[1], dom: sets[2], month: sets[3], dow: sets[4],
		domAny: fields[2] == "*", dowAny: fields[4] == "*"}, nil
}

// cronRange reads one part of a field: its first and last value and its step.
func cronRange(part string, lowest, highest int) (lo, hi, step int, err error) {
	span, stepText, stepped := strings.Cut(part, "/")
	step = 1
	if stepped {
		if step, err = strconv.Atoi(stepText); err != nil || step < 1 {
			return 0, 0, 0, fmt.Errorf("the step %q is not a number from 1 up", stepText)
		}
	}
	switch from, to, ranged := strings.Cut(span, "-"); {
	case span == "*":
		lo, hi = lowest, highest
		if highest == 7 { // the day of week's 7 is Sunday again
			hi = 6
		}
	case ranged:
		if lo, err = strconv.Atoi(from); err != nil {
			return 0, 0, 0, fmt.Errorf("%q is not a number", from)
		}
		if hi, err = strconv.Atoi(to); err != nil {
			return 0, 0, 0, fmt.Errorf("%q is not a number", to)
		}
	default:
		if lo, err = strconv.Atoi(span); err != nil {
			return 0, 0, 0, fmt.Errorf("%q is not a number, a range or *", span)
		}
		hi = lo
		if stepped { // "5/15" is from 5 on, every 15
			hi = highest
		}
	}
	if lo < lowest || hi > highest || lo > hi {
		return 0, 0, 0, fmt.Errorf("out of %d to %d", lowest, highest)
	}
	return lo, hi, step, nil
}

// matchesDay says whether the expression names t's day.
func (c Cron) matchesDay(t time.Time) bool {
	dom := c.dom&(1<<uint(t.Day())) != 0
	dow := c.dow&(1<<uint(t.Weekday())) != 0
	if c.domAny || c.dowAny {
		return dom && dow
	}
	return dom || dow
}

// Next is the first minute after t that the expression names, on loc's
// clock; false when there is none within five years (the 30th of February).
// A wall-clock time the zone skips when its clock goes forward doesn't come
// that day, and one it repeats when the clock goes back comes twice; a
// schedule that must not, names UTC.
func (c Cron) Next(t time.Time, loc *time.Location) (time.Time, bool) {
	t = t.In(loc).Truncate(time.Minute).Add(time.Minute)
	limit := t.AddDate(5, 0, 0)
	for t.Before(limit) {
		switch {
		case c.month&(1<<uint(t.Month())) == 0:
			t = time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, loc)
		case !c.matchesDay(t):
			t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, loc)
		case c.hour&(1<<uint(t.Hour())) == 0:
			// The top of the next hour on the wall clock; where the clock
			// goes back that can be an instant already passed, so step on.
			next := time.Date(t.Year(), t.Month(), t.Day(), t.Hour()+1, 0, 0, 0, loc)
			if !next.After(t) {
				next = t.Add(time.Hour)
			}
			t = next
		case c.minute&(1<<uint(t.Minute())) == 0:
			t = t.Add(time.Minute)
		default:
			return t, true
		}
	}
	return time.Time{}, false
}

// scheduleParts reads the schedule's expression and location.
func scheduleParts(s Schedule) (Cron, *time.Location, error) {
	zone := s.Zone()
	loc, err := time.LoadLocation(zone)
	if err != nil || strings.TrimSpace(zone) != zone || strings.EqualFold(zone, "Local") {
		return Cron{}, nil, fmt.Errorf("time zone %q is not one the operator can load; name an IANA time zone, "+
			"such as UTC or Europe/Amsterdam", zone)
	}
	cron, err := ParseCron(s.Cron)
	if err != nil {
		return Cron{}, nil, err
	}
	if _, ok := cron.Next(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC), loc); !ok {
		return Cron{}, nil, fmt.Errorf("cron %q names no day that exists", s.Cron)
	}
	return cron, loc, nil
}

// CheckSchedule says why a schedule can't be used: a cron expression that
// doesn't parse or names no day, or a time zone the operator can't load. The
// CRD checks the expression's shape; the rest only the operator can.
func CheckSchedule(s Schedule) error {
	_, _, err := scheduleParts(s)
	return err
}

// ScheduleFacts is what a schedule's next step depends on.
type ScheduleFacts struct {
	Schedule Schedule
	// Since is the time the schedule last filed an issue, last passed a due
	// time over, or was first seen: it is due at its first time after Since.
	Since time.Time
	// Open says an issue the schedule filed is still open.
	Open bool
}

// ScheduleStep is what a Crew does about one schedule now.
type ScheduleStep struct {
	// Due is the time that came due, zero when none has since Since.
	Due time.Time
	// File says to file the schedule's issue: a time came due and no issue of
	// the schedule is open. However many times came due since Since, one
	// issue is filed.
	File bool
	// Skip says a time came due while the schedule's last issue is still
	// open: nothing is filed, and the time is passed over, so a schedule
	// never piles up.
	Skip bool
	// Next is the schedule's next time after now.
	Next time.Time
}

// ReasonUnknownSchedule is the Scheduled condition's reason when the Crew is
// asked to run a schedule now that it doesn't have.
const ReasonUnknownSchedule = "UnknownSchedule"

// RunNowWaits says why a schedule a person asked to run now (#72) has filed
// no issue yet: the Crew is paused, or the schedule's last issue is still
// open, which holds a run asked for as it holds a due one.
func RunNowWaits(name string, paused bool) string {
	if paused {
		return fmt.Sprintf("schedule %s is asked to run now, and runs when the Crew resumes", name)
	}
	return fmt.Sprintf("schedule %s is asked to run now, and waits for its open issue to close", name)
}

// RunNowUnknown says a Crew was asked to run a schedule it doesn't have,
// naming the ones it has.
func RunNowUnknown(asked string, names []string) string {
	have := "the Crew has no schedules"
	if len(names) > 0 {
		have = "the Crew's schedules are " + strings.Join(names, ", ")
	}
	return fmt.Sprintf("asked to run schedule %q now, which is none of the Crew's: %s", asked, have)
}

// NextScheduleStep decides what to do about a schedule at now.
func NextScheduleStep(f ScheduleFacts, now time.Time) (ScheduleStep, error) {
	cron, loc, err := scheduleParts(f.Schedule)
	if err != nil {
		return ScheduleStep{}, err
	}
	var step ScheduleStep
	step.Next, _ = cron.Next(now, loc)
	due, ok := cron.Next(f.Since, loc)
	if !ok || due.After(now) {
		return step, nil
	}
	step.Due = due
	step.File, step.Skip = !f.Open, f.Open
	return step, nil
}

// ScheduleMarker is the line in an issue's body that says which schedule
// filed it.
func ScheduleMarker(name string) string {
	return "<!-- theos-schedule: " + name + " -->"
}

// HasScheduleMarker says whether body is the issue of the schedule name.
func HasScheduleMarker(body, name string) bool {
	marker := ScheduleMarker(name)
	for line := range strings.SplitSeq(body, "\n") {
		if strings.TrimSpace(line) == marker {
			return true
		}
	}
	return false
}

// ScheduleIssue renders the issue a due schedule files: its goal in the
// Crew's words, and criteria a reviewer can check.
func ScheduleIssue(s Schedule, due time.Time) (title, body string) {
	loc, err := time.LoadLocation(s.Zone())
	if err != nil {
		loc = time.UTC
	}
	due = due.In(loc)
	title = fmt.Sprintf("%s, %s", s.Name, due.Format("2006-01-02"))
	var b strings.Builder
	fmt.Fprintf(&b, "The Crew's schedule `%s` (`%s`, %s) came due at %s.\n\n", s.Name, s.Cron, s.Zone(),
		due.Format("2006-01-02 15:04"))
	fmt.Fprintf(&b, "## Goal\n\n%s\n\n", strings.TrimSpace(s.Goal))
	b.WriteString("## Acceptance criteria\n\n")
	b.WriteString("- [ ] The goal is met, or the pull request says what was left and why\n")
	b.WriteString("- [ ] `make check` passes\n\n")
	// A person wrote the goal: no comment of theirs may pass for a marker.
	body = strings.ReplaceAll(b.String(), "<!--", "&lt;!--") + ScheduleMarker(s.Name) + "\n"
	return title, body
}
