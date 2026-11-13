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
	"reflect"
	"testing"
	"time"
)

func TestAlertContainer(t *testing.T) {
	places := []AlertPlace{
		{Container: "orders-api", Namespaces: []string{"shop-prod", "shop-staging"}},
		{Container: "orders-web", Namespaces: []string{"web-prod"}},
		{Container: "storefront", Namespaces: []string{"shop-staging"}},
		{Container: "docs"},
	}
	routes := []AlertRoute{
		{Container: "storefront", Namespace: "edge"},
		{Container: "orders-web", Project: "frontend"},
		{Container: "gone", Project: "legacy"},
		{Container: "docs", Namespace: "shop-staging"},
	}
	for _, tc := range []struct {
		name      string
		alert     Alert
		routes    []AlertRoute
		container string
		why       string
	}{
		{"the alert names its Container", Alert{Container: "docs", Namespace: "shop-prod", Project: "frontend"},
			routes, "docs", ""},
		{"a name that is no Container is not second-guessed",
			Alert{Container: "payments", Namespace: "shop-prod"}, routes, "",
			`the alert names Container "payments", which this namespace doesn't hold`},
		{"a route by namespace", Alert{Namespace: "edge"}, routes, "storefront", ""},
		{"a route by project", Alert{Project: "frontend"}, routes, "orders-web", ""},
		{"a route comes before the deploy targets", Alert{Namespace: "shop-staging"}, routes, "docs", ""},
		{"a route to a Container that is gone", Alert{Project: "legacy"}, routes, "",
			`a route sends the alert to Container "gone", which this namespace doesn't hold`},
		{"an empty hint matches no route", Alert{}, []AlertRoute{{Container: "docs"}}, "",
			"the alert names no Container, namespace or project"},
		{"the one Container that deploys to the namespace", Alert{Namespace: "shop-prod"}, nil, "orders-api", ""},
		{"two Containers deploy to the namespace", Alert{Namespace: "shop-staging"}, nil, "",
			`2 Containers deploy to namespace "shop-staging" (orders-api, storefront): the alert has to name ` +
				`one, or a route`},
		{"the project is a Container's name", Alert{Project: "orders-web"}, nil, "orders-web", ""},
		{"the namespace leads nowhere, the project does", Alert{Namespace: "elsewhere", Project: "docs"}, nil,
			"docs", ""},
		{"nothing leads anywhere", Alert{Namespace: "elsewhere", Project: "billing"}, nil, "",
			`the alert names no Container, and namespace "elsewhere" and project "billing" leads to none`},
		{"nothing is named", Alert{}, routes, "", "the alert names no Container, namespace or project"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			container, why := AlertContainer(tc.alert, tc.routes, places)
			if container != tc.container || why != tc.why {
				t.Errorf("got %q, %q; want %q, %q", container, why, tc.container, tc.why)
			}
		})
	}
}

func TestAlertStep(t *testing.T) {
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	today, yesterday := "2026-11-12", now.Add(-24*time.Hour)
	earlier := now.Add(-2 * time.Hour)
	firing, resolved := Alert{Key: "k1"}, Alert{Key: "k1", Resolved: true}
	day := AlertDay{Date: today, Filed: 2, LastIssue: 8}
	full := AlertDay{Date: today, Filed: 5, LastIssue: 11, Folded: 3, FoldReported: 3, FoldReportedAt: earlier}
	fresh := AlertRecord{Key: "k1", Container: "orders-api", FiledAt: now, LastSeen: now, Count: 1, Reported: 1,
		ReportedAt: now}
	open := AlertRecord{Key: "k1", Container: "orders-api", Issue: 7, FiledAt: earlier, LastSeen: earlier,
		Count: 3, Reported: 1, ReportedAt: earlier}
	with := func(r AlertRecord, change func(*AlertRecord)) *AlertRecord {
		change(&r)
		return &r
	}
	for _, tc := range []struct {
		name     string
		alert    Alert
		facts    AlertFacts
		settings AlertSettings
		want     AlertDecision
	}{
		{"a new alert files", firing, AlertFacts{Day: day}, AlertSettings{},
			AlertDecision{Action: AlertFile, Record: fresh, Day: AlertDay{Date: today, Filed: 3, LastIssue: 8}}},
		{"the first of a day starts its count", firing,
			AlertFacts{Day: AlertDay{Date: "2026-11-11", Filed: 5, LastIssue: 4, Folded: 9}}, AlertSettings{},
			AlertDecision{Action: AlertFile, Record: fresh, Day: AlertDay{Date: today, Filed: 1}}},
		{"the last one under the limit files", firing, AlertFacts{Day: AlertDay{Date: today, Filed: 4, LastIssue: 9}},
			AlertSettings{},
			AlertDecision{Action: AlertFile, Record: fresh, Day: AlertDay{Date: today, Filed: 5, LastIssue: 9}}},
		{"past the limit a new alert is folded into the day's last issue", firing, AlertFacts{Day: full},
			AlertSettings{},
			AlertDecision{Action: AlertFold,
				Record: *with(fresh, func(r *AlertRecord) { r.Issue, r.Folded = 11, true }),
				Day: AlertDay{Date: today, Filed: 5, LastIssue: 11, Folded: 4, FoldReported: 3,
					FoldReportedAt: earlier}}},
		{"the limit is the source's", firing, AlertFacts{Day: day}, AlertSettings{DailyLimit: 2},
			AlertDecision{Action: AlertFold,
				Record: *with(fresh, func(r *AlertRecord) { r.Issue, r.Folded = 8, true }),
				Day:    AlertDay{Date: today, Filed: 2, LastIssue: 8, Folded: 1}}},
		{"a higher limit files what the default would fold", firing, AlertFacts{Day: full},
			AlertSettings{DailyLimit: 6},
			AlertDecision{Action: AlertFile, Record: fresh,
				Day: AlertDay{Date: today, Filed: 6, LastIssue: 11, Folded: 3, FoldReported: 3,
					FoldReportedAt: earlier}}},

		{"firing again with an open issue is counted, never filed", firing,
			AlertFacts{Record: &open, IssueOpen: true, Day: full}, AlertSettings{},
			AlertDecision{Action: AlertNote,
				Record: *with(open, func(r *AlertRecord) { r.Count, r.LastSeen = 4, now }), Day: full}},
		{"an open issue of an earlier day still takes the count", firing,
			AlertFacts{Record: with(open, func(r *AlertRecord) { r.FiledAt = yesterday }), IssueOpen: true, Day: day},
			AlertSettings{},
			AlertDecision{Action: AlertNote,
				Record: *with(open, func(r *AlertRecord) { r.FiledAt, r.Count, r.LastSeen = yesterday, 4, now }),
				Day:    day}},
		{"firing again after it resolved is firing", firing,
			AlertFacts{Record: with(open, func(r *AlertRecord) { r.Resolved, r.SaidResolved = true, true }),
				IssueOpen: true, Day: day}, AlertSettings{},
			AlertDecision{Action: AlertNote,
				Record: *with(open, func(r *AlertRecord) { r.SaidResolved, r.Count, r.LastSeen = true, 4, now }),
				Day:    day}},
		{"an issue closed today is not filed again today", firing,
			AlertFacts{Record: &open, IssueOpen: false, Day: day}, AlertSettings{},
			AlertDecision{Action: AlertNote,
				Record: *with(open, func(r *AlertRecord) { r.Count, r.LastSeen = 4, now }), Day: day}},
		{"an issue closed on an earlier day is filed anew", firing,
			AlertFacts{Record: with(open, func(r *AlertRecord) { r.FiledAt = yesterday }), IssueOpen: false, Day: day},
			AlertSettings{},
			AlertDecision{Action: AlertFile, Record: fresh, Day: AlertDay{Date: today, Filed: 3, LastIssue: 8}}},
		{"an issue closed on an earlier day is folded past the limit", firing,
			AlertFacts{Record: with(open, func(r *AlertRecord) { r.FiledAt = yesterday }), IssueOpen: false, Day: full},
			AlertSettings{},
			AlertDecision{Action: AlertFold,
				Record: *with(fresh, func(r *AlertRecord) { r.Issue, r.Folded = 11, true }),
				Day: AlertDay{Date: today, Filed: 5, LastIssue: 11, Folded: 4, FoldReported: 3,
					FoldReportedAt: earlier}}},
		{"an alert folded today is counted once", firing,
			AlertFacts{Record: with(open, func(r *AlertRecord) { r.Folded = true }), IssueOpen: true, Day: full},
			AlertSettings{},
			AlertDecision{Action: AlertNote,
				Record: *with(open, func(r *AlertRecord) { r.Folded, r.Count, r.LastSeen = true, 4, now }),
				Day:    full}},
		{"an alert folded on an earlier day gets its issue", firing,
			AlertFacts{Record: with(open, func(r *AlertRecord) { r.Folded, r.FiledAt = true, yesterday }),
				IssueOpen: true, Day: day}, AlertSettings{},
			AlertDecision{Action: AlertFile, Record: fresh, Day: AlertDay{Date: today, Filed: 3, LastIssue: 8}}},

		{"resolved says so on the open issue, with what it hadn't said", resolved,
			AlertFacts{Record: &open, IssueOpen: true, Day: day}, AlertSettings{},
			AlertDecision{Action: AlertResolve, Issue: 7, Again: 2,
				Record: *with(open, func(r *AlertRecord) {
					r.Resolved, r.SaidResolved, r.Reported, r.ReportedAt, r.LastSeen = true, true, 3, now, now
				}), Day: day}},
		{"resolved is said once: the issue's last word is already that", resolved,
			AlertFacts{Record: with(open, func(r *AlertRecord) { r.SaidResolved = true }), IssueOpen: true, Day: day},
			AlertSettings{},
			AlertDecision{Action: AlertNote,
				Record: *with(open, func(r *AlertRecord) { r.SaidResolved, r.Resolved, r.LastSeen = true, true, now }),
				Day:    day}},
		{"resolved twice is nothing", resolved,
			AlertFacts{Record: with(open, func(r *AlertRecord) { r.Resolved = true }), IssueOpen: true, Day: day},
			AlertSettings{}, AlertDecision{Action: AlertIgnore}},
		{"resolved on a closed issue says nothing", resolved, AlertFacts{Record: &open, Day: day}, AlertSettings{},
			AlertDecision{Action: AlertNote,
				Record: *with(open, func(r *AlertRecord) { r.Resolved, r.LastSeen = true, now }), Day: day}},
		{"a folded alert resolves without a word", resolved,
			AlertFacts{Record: with(open, func(r *AlertRecord) { r.Folded = true }), IssueOpen: true, Day: day},
			AlertSettings{},
			AlertDecision{Action: AlertNote,
				Record: *with(open, func(r *AlertRecord) { r.Folded, r.Resolved, r.LastSeen = true, true, now }),
				Day:    day}},
		{"resolved, never seen firing, is nothing", resolved, AlertFacts{Day: day}, AlertSettings{},
			AlertDecision{Action: AlertIgnore}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := AlertStep(tc.alert, "orders-api", tc.facts, tc.settings, now)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("\n got %+v\nwant %+v", got, tc.want)
			}
		})
	}

	filed := AlertStep(firing, "orders-api", AlertFacts{Day: day}, AlertSettings{}, now).Filed(12)
	if filed.Record.Issue != 12 || filed.Day.LastIssue != 12 || filed.Day.Filed != 3 {
		t.Errorf("Filed(12): record %+v, day %+v", filed.Record, filed.Day)
	}
}

// TestAlertStorm is the issue's promise: a storm becomes the day's limit of
// issues and one "and N more", never an issue for each alert.
func TestAlertStorm(t *testing.T) {
	start := time.Date(2026, 11, 12, 23, 0, 0, 0, time.UTC)
	settings := AlertSettings{}
	var records []AlertRecord
	day := AlertDay{}
	issues, comments := 0, []int{}
	record := func(key string) *AlertRecord {
		for i := range records {
			if records[i].Key == key {
				return &records[i]
			}
		}
		return nil
	}
	deliver := func(key string, now time.Time) AlertAction {
		if owed, _ := AlertDayOwed(day, settings, now); owed != nil {
			comments, day = append(comments, owed.More), owed.Day
		}
		d := AlertStep(Alert{Key: key}, "orders-api", AlertFacts{Record: record(key), IssueOpen: true, Day: day},
			settings, now)
		if d.Action == AlertFile {
			issues++
			d = d.Filed(100 + issues)
		}
		records, day = PutAlertRecord(records, d.Record), d.Day
		if owed, _ := AlertDayOwed(day, settings, now); owed != nil {
			comments, day = append(comments, owed.More), owed.Day
		}
		return d.Action
	}

	// 45 alerts in 45 seconds, each delivered twice.
	for round := range 2 {
		for i := range 45 {
			deliver(fmt.Sprintf("alert-%02d", i), start.Add(time.Duration(round*45+i)*time.Second))
		}
	}
	if issues != DefaultAlertDailyLimit || day.Folded != 40 || len(records) != 45 {
		t.Fatalf("%d issues, %d folded, %d records; want %d, 40 and 45", issues, day.Folded, len(records),
			DefaultAlertDailyLimit)
	}
	if !reflect.DeepEqual(comments, []int{1}) {
		t.Errorf("fold comments %v: the first fold says so at once, the rest wait for the interval", comments)
	}
	if owed, wait := AlertDayOwed(day, settings, start.Add(2*time.Minute)); owed != nil || wait <= 0 ||
		wait > time.Hour {
		t.Errorf("two minutes in: owed %+v, wait %v; want a wait within the hour", owed, wait)
	}

	// The day turns before the interval passes: the old day is settled
	// first, then a folded alert that still fires gets its issue.
	next := start.Add(61 * time.Minute)
	if got := deliver("alert-44", next); got != AlertFile {
		t.Errorf("a folded alert on the next day: %s, want %s", got, AlertFile)
	}
	if !reflect.DeepEqual(comments, []int{1, 40}) {
		t.Errorf("fold comments %v, want the day's last to say all 40", comments)
	}
	if day.Date != "2026-11-13" || day.Filed != 1 || day.Folded != 0 || issues != DefaultAlertDailyLimit+1 {
		t.Errorf("the next day: %+v, %d issues", day, issues)
	}
	// One whose issue of yesterday is still open is only counted.
	if got := deliver("alert-00", next); got != AlertNote || record("alert-00").Count != 3 {
		t.Errorf("an alert with an open issue on the next day: %s, count %d", got, record("alert-00").Count)
	}
}

func TestAlertRecordOwed(t *testing.T) {
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	rec := AlertRecord{Key: "k1", Container: "orders-api", Issue: 7, FiledAt: now.Add(-3 * time.Hour),
		LastSeen: now.Add(-time.Minute), Count: 6, Reported: 2, ReportedAt: now.Add(-time.Hour)}
	with := func(change func(*AlertRecord)) AlertRecord {
		r := rec
		change(&r)
		return r
	}
	said := func(r AlertRecord, resolved bool) AlertRecord {
		r.Reported, r.ReportedAt, r.SaidResolved = r.Count, now, resolved
		return r
	}
	for _, tc := range []struct {
		name     string
		rec      AlertRecord
		open     bool
		settings AlertSettings
		want     *AlertReport
		wait     time.Duration
	}{
		{"the interval has passed", rec, true, AlertSettings{},
			&AlertReport{Issue: 7, Again: 4, Total: 6, Since: rec.ReportedAt, Record: said(rec, false)}, 0},
		{"a minute short of the interval", with(func(r *AlertRecord) { r.ReportedAt = now.Add(-59 * time.Minute) }),
			true, AlertSettings{}, nil, time.Minute},
		{"the interval is the source's",
			with(func(r *AlertRecord) { r.ReportedAt = now.Add(-10 * time.Minute) }), true,
			AlertSettings{CommentInterval: 10 * time.Minute},
			&AlertReport{Issue: 7, Again: 4, Total: 6, Since: now.Add(-10 * time.Minute),
				Record: said(with(func(r *AlertRecord) { r.ReportedAt = now.Add(-10 * time.Minute) }), false)}, 0},
		{"a longer interval waits", rec, true, AlertSettings{CommentInterval: 6 * time.Hour}, nil, 5 * time.Hour},
		{"nothing fired since the issue last said", with(func(r *AlertRecord) { r.Reported = 6 }), true,
			AlertSettings{}, nil, 0},
		{"a closed issue is owed nothing", rec, false, AlertSettings{}, nil, 0},
		{"a folded alert has no issue of its own", with(func(r *AlertRecord) { r.Folded = true }), true,
			AlertSettings{}, nil, 0},
		{"no issue yet", with(func(r *AlertRecord) { r.Issue = 0 }), true, AlertSettings{}, nil, 0},
		{"firing again after the issue said resolved", with(func(r *AlertRecord) { r.SaidResolved = true }), true,
			AlertSettings{},
			&AlertReport{Issue: 7, Again: 4, Total: 6, Since: rec.ReportedAt, AfterResolved: true,
				Record: said(rec, false)}, 0},
		{"fired and resolved again within the interval",
			with(func(r *AlertRecord) { r.SaidResolved, r.Resolved = true, true }), true, AlertSettings{},
			&AlertReport{Issue: 7, Again: 4, Total: 6, Since: rec.ReportedAt, Resolved: true, AfterResolved: true,
				Record: said(with(func(r *AlertRecord) { r.Resolved = true }), true)}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, wait := AlertRecordOwed(tc.rec, tc.open, tc.settings, now)
			if !reflect.DeepEqual(got, tc.want) || wait != tc.wait {
				t.Errorf("\n got %+v, %v\nwant %+v, %v", got, wait, tc.want, tc.wait)
			}
		})
	}
}

// TestAlertCommentsKeepTheirInterval delivers an alert that flaps every five
// minutes for a day: its issue gets at most two comments an interval, one of
// the count and one that it resolved.
func TestAlertCommentsKeepTheirInterval(t *testing.T) {
	start := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	settings := AlertSettings{}
	first := AlertStep(Alert{Key: "k"}, "orders-api", AlertFacts{}, settings, start).Filed(7)
	rec, day := first.Record, first.Day
	reports, resolves := 0, 0
	for i := 1; i < 24*12; i++ {
		now := start.Add(time.Duration(i) * 5 * time.Minute)
		d := AlertStep(Alert{Key: "k", Resolved: i%2 == 1}, "orders-api",
			AlertFacts{Record: &rec, IssueOpen: true, Day: day}, settings, now)
		switch d.Action {
		case AlertResolve:
			resolves++
		case AlertFile, AlertFold:
			t.Fatalf("delivery %d: %s, want one issue for the alert", i, d.Action)
		}
		if d.Action != AlertIgnore {
			rec, day = d.Record, d.Day
		}
		if owed, _ := AlertRecordOwed(rec, true, settings, now); owed != nil {
			reports++
			rec = owed.Record
		}
	}
	if reports < 20 || reports > 24 || resolves > 24 {
		t.Errorf("%d reports and %d resolutions in a day of flapping; want about one of each an hour", reports,
			resolves)
	}
	if rec.Count != 144 {
		t.Errorf("count %d, want every one of the 144 firings", rec.Count)
	}
}

func TestAlertDayOwed(t *testing.T) {
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	day := AlertDay{Date: "2026-11-12", Filed: 5, LastIssue: 11, Folded: 40, FoldReported: 1,
		FoldReportedAt: now.Add(-time.Hour)}
	with := func(change func(*AlertDay)) AlertDay {
		d := day
		change(&d)
		return d
	}
	said := func(d AlertDay) AlertDay {
		d.FoldReported, d.FoldReportedAt = d.Folded, now
		return d
	}
	for _, tc := range []struct {
		name     string
		day      AlertDay
		settings AlertSettings
		want     *AlertFoldReport
		wait     time.Duration
	}{
		{"the interval has passed", day, AlertSettings{},
			&AlertFoldReport{Issue: 11, More: 40, Date: "2026-11-12", Day: said(day)}, 0},
		{"within the interval", with(func(d *AlertDay) { d.FoldReportedAt = now.Add(-20 * time.Minute) }),
			AlertSettings{}, nil, 40 * time.Minute},
		{"the first fold of a day says so at once",
			with(func(d *AlertDay) { d.Folded, d.FoldReported, d.FoldReportedAt = 1, 0, time.Time{} }),
			AlertSettings{},
			&AlertFoldReport{Issue: 11, More: 1, Date: "2026-11-12",
				Day: said(with(func(d *AlertDay) { d.Folded, d.FoldReported = 1, 0 }))}, 0},
		{"nothing folded since the last word", with(func(d *AlertDay) { d.FoldReported = 40 }), AlertSettings{},
			nil, 0},
		{"nothing folded", AlertDay{Date: "2026-11-12", Filed: 2, LastIssue: 3}, AlertSettings{}, nil, 0},
		{"no issue to say it on", with(func(d *AlertDay) { d.LastIssue = 0 }), AlertSettings{}, nil, 0},
		{"a day that is over is settled at once",
			with(func(d *AlertDay) { d.Date, d.FoldReportedAt = "2026-11-11", now.Add(-time.Minute) }),
			AlertSettings{},
			&AlertFoldReport{Issue: 11, More: 40, Date: "2026-11-11",
				Day: said(with(func(d *AlertDay) { d.Date = "2026-11-11" }))}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, wait := AlertDayOwed(tc.day, tc.settings, now)
			if !reflect.DeepEqual(got, tc.want) || wait != tc.wait {
				t.Errorf("\n got %+v, %v\nwant %+v, %v", got, wait, tc.want, tc.wait)
			}
		})
	}
}

func TestPutAlertRecord(t *testing.T) {
	at := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	var records []AlertRecord
	for i := range MaxAlertRecords {
		records = PutAlertRecord(records, AlertRecord{Key: fmt.Sprintf("k%03d", i),
			LastSeen: at.Add(time.Duration(i) * time.Minute), Resolved: i == 50 || i == 60})
	}
	has := func(records []AlertRecord, key string) bool {
		for _, r := range records {
			if r.Key == key {
				return true
			}
		}
		return false
	}

	replaced := PutAlertRecord(records, AlertRecord{Key: "k007", Count: 9})
	if len(replaced) != MaxAlertRecords || replaced[7].Count != 9 || records[7].Count != 0 {
		t.Errorf("a known key: %d records, count %d; want it replaced in place and the input untouched",
			len(replaced), replaced[7].Count)
	}

	// Full: the resolved alert seen longest ago makes room, then the next
	// resolved one, then the firing one seen longest ago.
	out := records
	for i, gone := range []string{"k050", "k060", "k000"} {
		out = PutAlertRecord(out, AlertRecord{Key: fmt.Sprintf("new%d", i), LastSeen: at.Add(24 * time.Hour)})
		if len(out) != MaxAlertRecords || has(out, gone) || !has(out, fmt.Sprintf("new%d", i)) {
			t.Errorf("record %d over the limit: %d records, %s still there: %v", i, len(out), gone, has(out, gone))
		}
	}
	if !has(out, "k001") || !has(out, "new0") {
		t.Error("an alert that should have stayed made room")
	}
}
