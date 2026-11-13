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
	"slices"
	"time"
)

// What an alert's delivery does (docs/plans/alerts.md, decisions 8 to 11): it
// files an issue, is folded into the day's "and N more", says the alert
// resolved, or is only noted. A delivery never comments on a count; what an
// issue is owed about repeats is a rule of its own, AlertRecordOwed, so the
// comments keep their interval however the deliveries arrive.

// What an AlertSource does unless its spec says otherwise.
const (
	DefaultAlertCommentInterval = time.Hour
	DefaultAlertDailyLimit      = 5
)

// MaxAlertRecords is how many alerts an AlertSource's status remembers.
const MaxAlertRecords = 100

// AlertSettings are an AlertSource's limits; a zero one is its default.
type AlertSettings struct {
	// CommentInterval is the least time between two comments on an issue
	// about its alert firing again, and between two "and N more".
	CommentInterval time.Duration
	// DailyLimit is how many issues the source files for one Container in
	// one UTC day.
	DailyLimit int
}

func (s AlertSettings) withDefaults() AlertSettings {
	if s.CommentInterval <= 0 {
		s.CommentInterval = DefaultAlertCommentInterval
	}
	if s.DailyLimit <= 0 {
		s.DailyLimit = DefaultAlertDailyLimit
	}
	return s
}

// AlertRecord is what an AlertSource's status keeps of one alert.
type AlertRecord struct {
	Key, Container string
	// Issue is the issue the alert was filed as, or the one its day's
	// "and N more" is said on when it was Folded.
	Issue  int
	Folded bool
	// Resolved says the last delivery reported the alert over.
	Resolved bool
	// FiledAt is when it was filed or folded, LastSeen its last delivery.
	FiledAt, LastSeen time.Time
	// Count is its firing deliveries since FiledAt, the first included, and
	// Reported the Count its issue has said, in the issue or a comment.
	Count, Reported int
	// ReportedAt is when its issue last said anything of it, and
	// SaidResolved whether that was that it resolved.
	ReportedAt   time.Time
	SaidResolved bool
}

// AlertDay is what an AlertSource's status keeps of one Container's day.
type AlertDay struct {
	// Date is the UTC day, 2006-01-02.
	Date string
	// Filed counts the issues filed that day and LastIssue is the last.
	Filed, LastIssue int
	// Folded counts the alerts past the limit that day, and FoldReported
	// the Folded the last "and N more" said, at FoldReportedAt.
	Folded, FoldReported int
	FoldReportedAt       time.Time
}

// AlertFacts are what is known when an alert's delivery arrives.
type AlertFacts struct {
	// Record is what status holds for the alert's key; nil when nothing.
	Record *AlertRecord
	// IssueOpen says Record's issue is open on the Git host.
	IssueOpen bool
	// Day is the Container's day as status holds it, whatever its date.
	Day AlertDay
}

// AlertAction is what a delivery does.
type AlertAction string

const (
	// AlertFile files the alert's issue.
	AlertFile AlertAction = "File"
	// AlertFold counts the alert into the day's "and N more": the limit of
	// issues for its Container is reached.
	AlertFold AlertAction = "Fold"
	// AlertResolve says on the alert's issue that it resolved.
	AlertResolve AlertAction = "Resolve"
	// AlertNote changes the record alone: nothing goes to the Git host.
	AlertNote AlertAction = "Note"
	// AlertIgnore does nothing and keeps nothing.
	AlertIgnore AlertAction = "Ignore"
)

// AlertDecision is what to do about a delivery, and what status holds once
// it is done. After AlertFile the caller gives the issue's number to Filed.
type AlertDecision struct {
	Action AlertAction
	// Issue is the issue an AlertResolve comments on.
	Issue int
	// Again is how often the alert fired that its issue hasn't said, which
	// an AlertResolve's comment adds.
	Again int
	// Record and Day are status after the action; unset for AlertIgnore.
	Record AlertRecord
	Day    AlertDay
}

// Filed is d with the number of the issue an AlertFile filed.
func (d AlertDecision) Filed(issue int) AlertDecision {
	d.Record.Issue, d.Day.LastIssue = issue, issue
	return d
}

func alertDate(t time.Time) string {
	return t.UTC().Format(time.DateOnly)
}

// AlertStep decides what the delivery of a, an alert of container, does.
//
// A firing alert files an issue when it has none to go on with: it is new,
// its issue was closed on an earlier day, or it was folded on an earlier
// day. Past the Container's limit for the day it is folded instead. With an
// open issue, or one filed, folded or closed today, it is only counted: an
// alert files at most once a day, and closing its issue doesn't silence it
// for longer. A resolved alert says so once on its open issue, and is noted
// otherwise; one never seen is ignored.
//
// A step on a new day starts the day's count afresh, so the caller settles
// what the old day is owed (AlertDayOwed) first.
func AlertStep(a Alert, container string, f AlertFacts, s AlertSettings, now time.Time) AlertDecision {
	s = s.withDefaults()
	today := alertDate(now)
	day := f.Day
	if day.Date != today {
		day = AlertDay{Date: today}
	}
	rec := f.Record
	if a.Resolved {
		return alertResolved(rec, f.IssueOpen, day, now)
	}
	if rec != nil && (alertDate(rec.FiledAt) == today || (!rec.Folded && f.IssueOpen)) {
		next := *rec
		next.Count++
		next.LastSeen, next.Resolved = now, false
		return AlertDecision{Action: AlertNote, Record: next, Day: day}
	}
	next := AlertRecord{Key: a.Key, Container: container, FiledAt: now, LastSeen: now, Count: 1, Reported: 1,
		ReportedAt: now}
	if day.Filed < s.DailyLimit {
		day.Filed++
		return AlertDecision{Action: AlertFile, Record: next, Day: day}
	}
	next.Issue, next.Folded = day.LastIssue, true
	day.Folded++
	return AlertDecision{Action: AlertFold, Record: next, Day: day}
}

func alertResolved(rec *AlertRecord, issueOpen bool, day AlertDay, now time.Time) AlertDecision {
	if rec == nil || rec.Resolved {
		return AlertDecision{Action: AlertIgnore}
	}
	next := *rec
	next.Resolved, next.LastSeen = true, now
	if rec.Folded || !issueOpen || rec.SaidResolved {
		return AlertDecision{Action: AlertNote, Record: next, Day: day}
	}
	again := max(rec.Count-rec.Reported, 0)
	next.Reported, next.ReportedAt, next.SaidResolved = rec.Count, now, true
	return AlertDecision{Action: AlertResolve, Issue: rec.Issue, Again: again, Record: next, Day: day}
}

// AlertReport is a comment an alert's open issue is owed: how often the
// alert fired since the issue last said, and how it stands.
type AlertReport struct {
	Issue int
	// Again is the firings since Since, when the issue last said anything
	// of the alert; Total is all of them since it was filed.
	Again, Total int
	Since        time.Time
	// Resolved says the alert is over again, and AfterResolved that the
	// issue's last word was that it had resolved.
	Resolved, AfterResolved bool
	// Record is status once the comment is made.
	Record AlertRecord
}

// AlertRecordOwed says whether rec's issue is owed a comment now: it is
// open, the alert fired since the issue last said so, and the interval has
// passed since then. When one is owed later, wait says how long from now;
// zero means nothing is owed until a delivery changes the record.
func AlertRecordOwed(rec AlertRecord, issueOpen bool, s AlertSettings, now time.Time) (
	report *AlertReport, wait time.Duration) {
	s = s.withDefaults()
	if rec.Folded || rec.Issue == 0 || !issueOpen || rec.Count <= rec.Reported {
		return nil, 0
	}
	if due := rec.ReportedAt.Add(s.CommentInterval); now.Before(due) {
		return nil, due.Sub(now)
	}
	next := rec
	next.Reported, next.ReportedAt, next.SaidResolved = rec.Count, now, rec.Resolved
	return &AlertReport{Issue: rec.Issue, Again: rec.Count - rec.Reported, Total: rec.Count, Since: rec.ReportedAt,
		Resolved: rec.Resolved, AfterResolved: rec.SaidResolved, Record: next}, 0
}

// AlertFoldReport is the "and N more" comment a Container's day is owed.
type AlertFoldReport struct {
	Issue int
	// More is the alerts folded that day, and Date the day.
	More int
	Date string
	// Day is status once the comment is made.
	Day AlertDay
}

// AlertDayOwed says whether the last issue of a Container's day is owed an
// "and N more" now: alerts were folded since the last one said so, and the
// interval has passed since then, or the day is over. wait is as
// AlertRecordOwed's.
func AlertDayOwed(day AlertDay, s AlertSettings, now time.Time) (report *AlertFoldReport, wait time.Duration) {
	s = s.withDefaults()
	if day.LastIssue == 0 || day.Folded <= day.FoldReported {
		return nil, 0
	}
	if due := day.FoldReportedAt.Add(s.CommentInterval); day.Date == alertDate(now) &&
		!day.FoldReportedAt.IsZero() && now.Before(due) {
		return nil, due.Sub(now)
	}
	next := day
	next.FoldReported, next.FoldReportedAt = day.Folded, now
	return &AlertFoldReport{Issue: day.LastIssue, More: day.Folded, Date: day.Date, Day: next}, 0
}

// PutAlertRecord is records with rec in the place of the one with its key,
// or added. Past MaxAlertRecords the alert seen longest ago makes room, a
// resolved one before a firing one: its issue stays, and is found again by
// its marker (HasAlertMarker) if the alert comes back.
func PutAlertRecord(records []AlertRecord, rec AlertRecord) []AlertRecord {
	out := slices.Clone(records)
	if i := slices.IndexFunc(out, func(r AlertRecord) bool { return r.Key == rec.Key }); i >= 0 {
		out[i] = rec
		return out
	}
	for len(out) >= MaxAlertRecords {
		oldest := 0
		for i, r := range out {
			o := out[oldest]
			if (r.Resolved && !o.Resolved) || (r.Resolved == o.Resolved && r.LastSeen.Before(o.LastSeen)) {
				oldest = i
			}
		}
		out = slices.Delete(out, oldest, oldest+1)
	}
	return append(out, rec)
}
