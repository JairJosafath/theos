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
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// alertFixture reads a recorded delivery (docs/plans/alerts.md says where
// each shape comes from).
func alertFixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", "alerts", name))
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func mustAlerts(t *testing.T, typ, resource string, body []byte, labels AlertLabels) []Alert {
	t.Helper()
	alerts, _, err := ParseAlerts(typ, resource, body, labels)
	if err != nil {
		t.Fatal(err)
	}
	return alerts
}

func TestParseAlertmanager(t *testing.T) {
	alerts, more, err := ParseAlertmanager(alertFixture(t, "alertmanager-firing.json"), AlertLabels{})
	if err != nil || more != 0 {
		t.Fatalf("more %d, err %v", more, err)
	}
	want := []Alert{{
		Key:     alerts[0].Key,
		Name:    "HighErrorRate",
		Summary: "More than 5% of requests fail",
		Description: "orders-api answered 7.4% of its requests with a 5xx over the last 10 minutes.\n" +
			"The budget is 1%.",
		StartsAt: time.Date(2026, 11, 12, 8, 14, 30, 123000000, time.UTC),
		Labels: map[string]string{"alertname": "HighErrorRate", "severity": "critical", "namespace": "shop-prod",
			"service": "orders-api", "theos_container": "orders-api"},
		Annotations: map[string]string{"summary": "More than 5% of requests fail",
			"description": "orders-api answered 7.4% of its requests with a 5xx over the last 10 minutes. " +
				"The budget is 1%.",
			"runbook_url": "https://runbooks.example.com/high-error-rate"},
		URL:       "http://prometheus.monitoring.svc:9090/graph?g0.expr=rate%28http_requests_total%5B10m%5D%29&g0.tab=1",
		Container: "orders-api", Namespace: "shop-prod",
	}, {
		Key:      alerts[1].Key,
		Name:     "HighErrorRate",
		Summary:  "More than 5% of requests fail",
		StartsAt: time.Date(2026, 11, 12, 8, 16, 0, 0, time.UTC),
		Labels: map[string]string{"alertname": "HighErrorRate", "severity": "critical",
			"namespace": "shop-staging", "service": "storefront"},
		Annotations: map[string]string{"summary": "More than 5% of requests fail"},
		// No generatorURL: the link back is the Alertmanager's own.
		URL:       "http://alertmanager.monitoring.svc:9093",
		Namespace: "shop-staging",
	}}
	if !reflect.DeepEqual(alerts, want) {
		t.Errorf("alerts:\n got %+v\nwant %+v", alerts, want)
	}
	if len(alerts[0].Key) != 24 || alerts[0].Key == alerts[1].Key {
		t.Errorf("keys %q and %q: each alert has its own 24 digits", alerts[0].Key, alerts[1].Key)
	}

	resolved, more, err := ParseAlertmanager(alertFixture(t, "alertmanager-resolved.json"), AlertLabels{})
	if err != nil || len(resolved) != 1 {
		t.Fatalf("resolved %+v, err %v", resolved, err)
	}
	r := resolved[0]
	if !r.Resolved || !r.EndsAt.Equal(time.Date(2026, 11, 12, 9, 2, 0, 0, time.UTC)) {
		t.Errorf("resolved %v at %v", r.Resolved, r.EndsAt)
	}
	if r.Key != alerts[0].Key {
		t.Errorf("the resolution's key %q is not the firing alert's %q: other annotations and a "+
			"different order of labels make no other alert", r.Key, alerts[0].Key)
	}
	if more != 3 {
		t.Errorf("more = %d, want the 3 Alertmanager says it truncated", more)
	}
}

func TestParseAlertmanagerContainerLabel(t *testing.T) {
	alerts := mustAlerts(t, AlertTypeAlertmanager, "", alertFixture(t, "alertmanager-firing.json"),
		AlertLabels{Container: "service"})
	if alerts[0].Container != "orders-api" || alerts[1].Container != "storefront" {
		t.Errorf("containers %q and %q, want the service label's", alerts[0].Container, alerts[1].Container)
	}
}

// TestParseAlertsNamespaceLabel is #64's review: the label that names an
// alert's namespace is the AlertSource's to say, as the one that names its
// Container is. A Prometheus that relabels has it as exported_namespace.
func TestParseAlertsNamespaceLabel(t *testing.T) {
	names := AlertLabels{Namespace: "exported_namespace"}
	for _, tc := range []struct {
		name, typ, body string
		labels          AlertLabels
		namespace       string
	}{
		{"Alertmanager, the default label", AlertTypeAlertmanager,
			`{"alerts":[{"labels":{"namespace":"monitoring","exported_namespace":"shop-prod"}}]}`, AlertLabels{},
			"monitoring"},
		{"Alertmanager, the source's label", AlertTypeAlertmanager,
			`{"alerts":[{"labels":{"namespace":"monitoring","exported_namespace":"shop-prod"}}]}`, names,
			"shop-prod"},
		{"Alertmanager, the source's label is all that counts", AlertTypeAlertmanager,
			`{"alerts":[{"labels":{"namespace":"monitoring"}}]}`, names, ""},
		{"generic, the source's label", AlertTypeGeneric,
			`{"title":"T","labels":{"namespace":"monitoring","exported_namespace":"shop-prod"}}`, names, "shop-prod"},
		{"generic, the field comes before any label", AlertTypeGeneric,
			`{"title":"T","namespace":"shop-staging","labels":{"exported_namespace":"shop-prod"}}`, names,
			"shop-staging"},
	} {
		if got := mustAlerts(t, tc.typ, "", []byte(tc.body), tc.labels)[0].Namespace; got != tc.namespace {
			t.Errorf("%s: namespace %q, want %q", tc.name, got, tc.namespace)
		}
	}
}

// alertmanagerBody is a webhook body of the alerts given, each a JSON object.
func alertmanagerBody(alerts ...string) []byte {
	return []byte(`{"version":"4","status":"firing","externalURL":"http://am:9093","alerts":[` +
		strings.Join(alerts, ",") + `]}`)
}

func TestParseAlertmanagerKeys(t *testing.T) {
	key := func(alert string) string {
		t.Helper()
		alerts, _, err := ParseAlertmanager(alertmanagerBody(alert), AlertLabels{})
		if err != nil {
			t.Fatal(err)
		}
		return alerts[0].Key
	}
	base := key(`{"labels":{"alertname":"A","pod":"x"},"annotations":{"summary":"one"},"fingerprint":"aa"}`)
	for _, tc := range []struct {
		name, alert string
		same        bool
	}{
		{"other annotations, time and fingerprint", `{"status":"resolved","labels":{"pod":"x","alertname":"A"},
			"annotations":{"summary":"two"},"startsAt":"2026-11-12T08:00:00Z","fingerprint":"bb"}`, true},
		{"another label value", `{"labels":{"alertname":"A","pod":"y"}}`, false},
		{"one label more", `{"labels":{"alertname":"A","pod":"x","zone":"a"}}`, false},
		{"a value that spells a second label", `{"labels":{"alertname":"A\u0000\"pod\"=x"}}`, false},
	} {
		if got := key(tc.alert); (got == base) != tc.same {
			t.Errorf("%s: key %q against %q, want same = %v", tc.name, got, base, tc.same)
		}
	}
	// #64: a label's name is quoted in the digest, so a name that spells the
	// start of its value is another alert than the name with that value.
	if a, b := key(`{"labels":{"a=b":"c"}}`), key(`{"labels":{"a":"b=c"}}`); a == b {
		t.Errorf(`the labels {"a=b":"c"} and {"a":"b=c"} have one key, %q`, a)
	}
}

func TestParseAlertmanagerRefused(t *testing.T) {
	t.Run("what is not its body", func(t *testing.T) {
		for name, body := range map[string]string{
			"not JSON":             `<html>`,
			"empty":                ``,
			"no alerts":            `{"version":"4","alerts":[]}`,
			"another object":       `{"action":"triggered"}`,
			"alerts of wrong type": `{"alerts":"all of them"}`,
			"a label not a string": `{"alerts":[{"labels":{"alertname":7}}]}`,
			"labels not an object": `{"alerts":[{"labels":["alertname"]}]}`,
		} {
			if alerts, _, err := ParseAlertmanager([]byte(body), AlertLabels{}); err == nil {
				t.Errorf("%s: read as %+v, want an error", name, alerts)
			}
		}
	})

	t.Run("#64: a count of truncated alerts that is no count", func(t *testing.T) {
		many := make([]string, 0, 150)
		for i := range 150 {
			many = append(many, fmt.Sprintf(`{"labels":{"alertname":"A%d"}}`, i))
		}
		for truncated, want := range map[string]int{"9223372036854775807": maxAlertsUnread, "-7": 50,
			"999999": maxAlertsUnread, "12": 62} {
			body := []byte(`{"truncatedAlerts":` + truncated + `,"alerts":[` + strings.Join(many, ",") + `]}`)
			alerts, more, err := ParseAlertmanager(body, AlertLabels{})
			if err != nil || len(alerts) != MaxAlertsPerDelivery || more != want {
				t.Errorf("truncatedAlerts %s: %d alerts, %d more, err %v; want %d more", truncated, len(alerts), more,
					err, want)
			}
		}
	})

	t.Run("more alerts than are read", func(t *testing.T) {
		many := make([]string, 0, 150)
		for i := range 150 {
			many = append(many, fmt.Sprintf(`{"labels":{"alertname":"A%d"}}`, i))
		}
		alerts, more, err := ParseAlertmanager(alertmanagerBody(many...), AlertLabels{})
		if err != nil || len(alerts) != MaxAlertsPerDelivery || more != 50 {
			t.Errorf("%d alerts, %d more, err %v; want %d and 50", len(alerts), more, err, MaxAlertsPerDelivery)
		}
	})
}

func TestParseAlertmanagerHugeAndMissing(t *testing.T) {
	huge := strings.Repeat("A", 1<<20)
	labels := map[string]string{}
	for i := range 60 {
		labels[fmt.Sprintf("label%02d", i)] = "v"
	}
	manyLabels, err := json.Marshal(map[string]any{"labels": labels})
	if err != nil {
		t.Fatal(err)
	}
	t.Run("huge fields are cut", func(t *testing.T) {
		body := alertmanagerBody(
			`{"labels":{"alertname":"` + huge + `","theos_container":"` + huge + `","namespace":"` + huge + `"},
			  "annotations":{"summary":"` + huge + `","description":"` + huge + `"},"generatorURL":"http://p/` + huge + `"}`,
		)
		alerts, _, err := ParseAlertmanager(body, AlertLabels{})
		if err != nil || len(alerts) != 1 {
			t.Fatalf("%d alerts, err %v", len(alerts), err)
		}
		a := alerts[0]
		for field, got := range map[string]struct {
			text  string
			limit int
		}{
			"name": {a.Name, maxAlertName}, "summary": {a.Summary, maxAlertSummary},
			"description": {a.Description, maxAlertDescription}, "container": {a.Container, maxAlertHint},
			"namespace": {a.Namespace, maxAlertHint}, "a label": {a.Labels["alertname"], maxAlertPairValue},
			"an annotation": {a.Annotations["description"], maxAlertPairValue},
		} {
			if n := utf8.RuneCountInString(got.text); n != got.limit || !strings.HasSuffix(got.text, "…") {
				t.Errorf("%s is %d characters, want it cut to %d with an ellipsis", field, n, got.limit)
			}
		}
		if a.URL != "http://am:9093" {
			t.Errorf("URL %q: a link too long to show gives way to the next", a.URL)
		}
		if _, body := AlertIssue(a, AlertIssueFacts{Source: "prom", Container: "orders-api"}); len(body) > 8<<10 {
			t.Errorf("the issue of an alert of megabytes is %d bytes", len(body))
		}
	})

	t.Run("missing fields and labels past the limit", func(t *testing.T) {
		alerts, _, err := ParseAlertmanager(alertmanagerBody(`{}`, string(manyLabels)), AlertLabels{})
		if err != nil || len(alerts) != 2 {
			t.Fatalf("%d alerts, err %v", len(alerts), err)
		}
		empty := alerts[0]
		if empty.Name != unnamedAlert || len(empty.Key) != 24 || empty.Labels != nil || !empty.StartsAt.IsZero() ||
			empty.Resolved || empty.URL != "http://am:9093" {
			t.Errorf("an alert of no fields: %+v", empty)
		}

		if got := alerts[1]; len(got.Labels) != maxAlertPairs || got.Truncated != 20 {
			t.Errorf("60 labels: %d kept, %d left out; want %d and 20", len(got.Labels), got.Truncated, maxAlertPairs)
		}
	})
}

func TestParseAlertmanagerHiddenText(t *testing.T) {
	t.Run("text that hides or breaks out", func(t *testing.T) {
		// A right-to-left override and a zero-width space: what hides text.
		hidden := string([]rune{0x202e, 0x200b})
		body := alertmanagerBody(`{"labels":{"alertname":"Disk\nfull\u0000` + hidden + ` \t now","na\nme":"v\r\nw"},
			"annotations":{"description":"line one\r\nline\u0007 two` + hidden + `"},
			"startsAt":"yesterday","endsAt":"2026-11-12T09:00:00Z",
			"generatorURL":"javascript:alert(1)"}`)
		alerts, _, err := ParseAlertmanager(body, AlertLabels{})
		if err != nil {
			t.Fatal(err)
		}
		a := alerts[0]
		if a.Name != "Disk full now" {
			t.Errorf("name %q, want one line without control or format characters", a.Name)
		}
		if a.Description != "line one \nline two" {
			t.Errorf("description %q, want its lines kept and nothing else of the control characters", a.Description)
		}
		if got := a.Labels["na me"]; got != "v w" {
			t.Errorf("labels %q, want every name and value one line", a.Labels)
		}
		if !a.StartsAt.IsZero() || !a.EndsAt.IsZero() {
			t.Errorf("times %v and %v: a time that isn't one is none, and a firing alert has no end",
				a.StartsAt, a.EndsAt)
		}
		if a.URL != "http://am:9093" {
			t.Errorf("URL %q: only http and https are links", a.URL)
		}
	})
}

// TestAlertTime is the time a delivery says: RFC 3339, and nothing before
// 1970, which is how senders write "not yet" and "never".
func TestAlertTime(t *testing.T) {
	for in, want := range map[string]time.Time{
		"2026-11-12T08:14:30.123Z":  time.Date(2026, 11, 12, 8, 14, 30, 123000000, time.UTC),
		"2026-11-12T09:00:00+01:00": time.Date(2026, 11, 12, 8, 0, 0, 0, time.UTC),
		" 1970-01-01T00:00:00Z ":    time.Unix(0, 0).UTC(),
		"0001-01-01T00:00:00Z":      {},
		"0001-01-02T00:00:00Z":      {},
		"1969-12-31T23:59:59Z":      {},
		"yesterday":                 {},
		"":                          {},
	} {
		if got := alertTime(in); !got.Equal(want) || got.IsZero() != want.IsZero() || got.Location() != time.UTC {
			t.Errorf("alertTime(%q) = %v, want %v", in, got, want)
		}
	}
}

// TestParseAlertmanagerStatus: an alert that says no status of its own has
// its delivery's.
func TestParseAlertmanagerStatus(t *testing.T) {
	for _, tc := range []struct {
		name, delivery, alert string
		resolved              bool
	}{
		{"the delivery's status stands in", "resolved", "", true},
		{"the alert's own status wins", "resolved", "firing", false},
		{"a firing delivery with a resolved alert", "firing", "resolved", true},
		{"no status anywhere is firing", "", "", false},
	} {
		body := `{"status":"` + tc.delivery + `","alerts":[{"status":"` + tc.alert + `","labels":{"alertname":"A"},` +
			`"endsAt":"2026-11-12T09:02:00Z"}]}`
		alerts, _, err := ParseAlertmanager([]byte(body), AlertLabels{})
		if err != nil || len(alerts) != 1 {
			t.Fatalf("%s: alerts %+v, err %v", tc.name, alerts, err)
		}
		if a := alerts[0]; a.Resolved != tc.resolved || a.EndsAt.IsZero() == tc.resolved {
			t.Errorf("%s: resolved %v, ends %v; want resolved %v, with its end only then", tc.name, a.Resolved,
				a.EndsAt, tc.resolved)
		}
	}
}

func TestAlertURL(t *testing.T) {
	for in, ok := range map[string]bool{
		"https://sentry.io/organizations/o/issues/1/?a=b&c=%20d#e": true,
		"http://prometheus:9090/graph":                             true,
		"":                                                         false,
		"ftp://files.example.com/x":                                false,
		"javascript:alert(1)":                                      false,
		"/relative/path":                                           false,
		"https://":                                                 false,
		"https://example.com/a b":                                  false,
		"https://example.com/\n<!-- theos-alert: prom k -->":      false,
		"https://example.com/)[click](https://evil.example)":      true,
		"https://x/[a](https://evil)":                             true,
		"https://x.example/(@owner)":                              true,
		"https://example.com/<script>":                            false,
		"https://example.com/`code`":                              false,
		"https://exämple.com/":                                    false,
		"https://example.com/" + strings.Repeat("a", maxAlertURL): false,
	} {
		got := alertURL(in)
		if (got == in && in != "") != ok {
			t.Errorf("alertURL(%q) = %q, want a link: %v", in, got, ok)
		}
		// #64: Markdown in an address stays an address. The issue shows it
		// between angle brackets, where it is the link and its own text.
		_, body := AlertIssue(Alert{Key: "k", Name: "A", URL: got}, AlertIssueFacts{Source: "s"})
		if line := "- Link back: <" + in + ">\n"; ok && !strings.Contains(body, line) {
			t.Errorf("the issue of %q lacks the line %q:\n%s", in, line, body)
		}
		if !ok && strings.Contains(body, "Link back") {
			t.Errorf("the issue of %q shows a link:\n%s", in, body)
		}
	}
}

func TestParseSentry(t *testing.T) {
	alerts, err := ParseSentry(SentryResourceIssueAlert, alertFixture(t, "sentry-issue-alert.json"), AlertLabels{})
	if err != nil || len(alerts) != 1 {
		t.Fatalf("alerts %+v, err %v", alerts, err)
	}
	want := Alert{
		Key:      alerts[0].Key,
		Name:     "ReferenceError: heck is not defined",
		Summary:  "?(checkout)",
		StartsAt: time.Date(2026, 11, 12, 8, 58, 37, 391000000, time.UTC),
		Labels: map[string]string{"browser": "Chrome 131.0.0", "environment": "production", "level": "error",
			"theos_container": "orders-web", "url": "https://shop.example.com/checkout"},
		Annotations: map[string]string{"rule": "Send a notification for new issues", "level": "error",
			"platform": "javascript", "release": "orders-web@1.4.2", "environment": "production",
			"event": "e4874d664c3540c1a32eab185f12c5ab"},
		URL: "https://sentry.io/organizations/test-org/issues/1117540176/events/" +
			"e4874d664c3540c1a32eab185f12c5ab/",
		Container: "orders-web",
		Project:   "orders-web",
	}
	if !reflect.DeepEqual(alerts[0], want) {
		t.Errorf("alert:\n got %+v\nwant %+v", alerts[0], want)
	}

	resolved, err := ParseSentry(SentryResourceIssue, alertFixture(t, "sentry-issue-resolved.json"), AlertLabels{})
	if err != nil || len(resolved) != 1 {
		t.Fatalf("resolved %+v, err %v", resolved, err)
	}
	wantResolved := Alert{
		Key:      alerts[0].Key,
		Resolved: true,
		Name:     "ReferenceError: heck is not defined",
		Summary:  "?(checkout)",
		URL:      "https://sentry.io/organizations/test-org/issues/1117540176/",
		Project:  "orders-web",
	}
	if !reflect.DeepEqual(resolved[0], wantResolved) {
		t.Errorf("resolved:\n got %+v\nwant %+v", resolved[0], wantResolved)
	}
}

// sentryAlertBody is an issue alert whose event has the fields given.
func sentryAlertBody(event string) []byte {
	return []byte(`{"action":"triggered","data":{"event":{` + event + `},"triggered_rule":"r"}}`)
}

func TestParseSentryShapes(t *testing.T) {
	one := func(resource string, body []byte) Alert {
		t.Helper()
		alerts, err := ParseSentry(resource, body, AlertLabels{})
		if err != nil || len(alerts) != 1 {
			t.Fatalf("alerts %+v, err %v", alerts, err)
		}
		return alerts[0]
	}
	recorded := one(SentryResourceIssueAlert, alertFixture(t, "sentry-issue-alert.json"))

	t.Run("every event of an issue is the same alert", func(t *testing.T) {
		other := one(SentryResourceIssueAlert, sentryAlertBody(`"issue_id":1117540176,"title":"another title",
			"event_id":"ffff","url":"https://sentry.io/api/0/projects/test-org/other/events/ffff/"`))
		if other.Key != recorded.Key {
			t.Errorf("key %q, want the issue's %q whatever the event, title and the id's JSON type",
				other.Key, recorded.Key)
		}
		if next := one(SentryResourceIssueAlert, sentryAlertBody(`"issue_id":"1117540177"`)); next.Key == recorded.Key {
			t.Error("another issue has the same key")
		}
	})

	t.Run("an event that names no issue is told apart by project and title", func(t *testing.T) {
		a := one(SentryResourceIssueAlert, sentryAlertBody(`"title":"T","project":7`))
		b := one(SentryResourceIssueAlert, sentryAlertBody(`"title":"T","project":7,"event_id":"x"`))
		c := one(SentryResourceIssueAlert, sentryAlertBody(`"title":"U","project":7`))
		if a.Key != b.Key || a.Key == c.Key || a.Project != "7" {
			t.Errorf("keys %q, %q, %q and project %q", a.Key, b.Key, c.Key, a.Project)
		}
	})

	t.Run("tags as objects, and a tag that names the Container", func(t *testing.T) {
		a, err := ParseSentry(SentryResourceIssueAlert, sentryAlertBody(`"issue_id":"1",
			"tags":[{"key":"app","value":"orders-web"},["level","error"],["odd"],7,{"value":"keyless"}],
			"timestamp":1794473917.391,"message":"boom"`), AlertLabels{Container: "app"})
		if err != nil {
			t.Fatal(err)
		}
		if a[0].Container != "orders-web" || !reflect.DeepEqual(a[0].Labels,
			map[string]string{"app": "orders-web", "level": "error"}) {
			t.Errorf("container %q, labels %v", a[0].Container, a[0].Labels)
		}
		if !a[0].StartsAt.Equal(time.Unix(1794473917, 0)) || a[0].Description != "boom" {
			t.Errorf("since %v, description %q: the timestamp stands in for datetime", a[0].StartsAt, a[0].Description)
		}
	})

	t.Run("fields of the wrong type are missing fields", func(t *testing.T) {
		a := one(SentryResourceIssueAlert, sentryAlertBody(`"issue_id":{"a":1},"title":["x"],"tags":"none",
			"url":7,"web_url":null,"datetime":true,"timestamp":"1e400","project":{"slug":"p"},"culprit":false`))
		if a.Name != unnamedAlert || a.URL != "" || !a.StartsAt.IsZero() || a.Labels != nil || a.Project != "" ||
			a.Summary != "false" {
			t.Errorf("alert %+v", a)
		}
	})

	t.Run("what is no alert", func(t *testing.T) {
		resolved := alertFixture(t, "sentry-issue-resolved.json")
		for name, tc := range map[string]struct {
			resource string
			body     []byte
			err      bool
		}{
			"an issue that was created":    {SentryResourceIssue, []byte(`{"action":"created","data":{"issue":{"id":"1"}}}`), false},
			"an issue that was assigned":   {SentryResourceIssue, []byte(`{"action":"assigned","data":{"issue":{"id":"1"}}}`), false},
			"an installation":              {"installation", []byte(`{"action":"created","data":{"installation":{}}}`), false},
			"a resolution with no header":  {"", resolved, false},
			"an issue alert with no event": {SentryResourceIssueAlert, resolved, true},
			"an issue with no issue":       {SentryResourceIssue, sentryAlertBody(`"issue_id":"1"`), true},
			"an event that is no object":   {SentryResourceIssueAlert, []byte(`{"data":{"event":"e"}}`), true},
			"not JSON":                     {SentryResourceIssueAlert, []byte(`action=triggered`), true},
			"a list":                       {SentryResourceIssueAlert, []byte(`[]`), true},
		} {
			alerts, err := ParseSentry(tc.resource, tc.body, AlertLabels{})
			if len(alerts) != 0 || (err != nil) != tc.err {
				t.Errorf("%s: alerts %+v, err %v, want an error: %v", name, alerts, err, tc.err)
			}
		}
	})
}

func TestParseGenericAlert(t *testing.T) {
	alerts, err := ParseGenericAlert(alertFixture(t, "generic-firing.json"), AlertLabels{})
	if err != nil || len(alerts) != 1 {
		t.Fatalf("alerts %+v, err %v", alerts, err)
	}
	want := Alert{
		Key:         alerts[0].Key,
		Name:        "Checkout probe fails",
		Summary:     "The synthetic checkout has failed 3 times in a row",
		Description: "GET https://shop.example.com/checkout answered 502.\nLast success 08:01 UTC.",
		StartsAt:    time.Date(2026, 11, 12, 8, 5, 0, 0, time.UTC),
		Labels:      map[string]string{"severity": "page", "region": "eu-west-1"},
		Annotations: map[string]string{"attempts": "3"},
		URL:         "https://probes.example.com/checks/checkout",
		Container:   "storefront",
		Namespace:   "shop-prod",
	}
	if !reflect.DeepEqual(alerts[0], want) {
		t.Errorf("alert:\n got %+v\nwant %+v", alerts[0], want)
	}

	one := func(body string) Alert {
		t.Helper()
		alerts, err := ParseGenericAlert([]byte(body), AlertLabels{})
		if err != nil || len(alerts) != 1 {
			t.Fatalf("%s: alerts %+v, err %v", body, alerts, err)
		}
		return alerts[0]
	}
	resolved := one(`{"key":"checkout-probe","status":"resolved","title":"Checkout works",
		"endsAt":"2026-11-12T09:00:00+01:00"}`)
	if !resolved.Resolved || resolved.Key != alerts[0].Key ||
		!resolved.EndsAt.Equal(time.Date(2026, 11, 12, 8, 0, 0, 0, time.UTC)) {
		t.Errorf("resolved %+v: the key, not the title, makes it the same alert", resolved)
	}
	if a, b := one(`{"title":"Disk full"}`), one(`{"title":"Disk full","summary":"again"}`); a.Key != b.Key ||
		a.Key == alerts[0].Key || a.Resolved {
		t.Errorf("keys %q and %q: without a key the title is the key, and no status is firing", a.Key, b.Key)
	}
	labelled := one(`{"title":"T","labels":{"theos_container":"orders-api","namespace":"shop-prod"}}`)
	if labelled.Container != "orders-api" || labelled.Namespace != "shop-prod" {
		t.Errorf("container %q, namespace %q: the labels name them when the fields don't",
			labelled.Container, labelled.Namespace)
	}

	for name, body := range map[string]string{
		"no title":             `{"key":"k","status":"firing"}`,
		"a title of spaces":    `{"title":" \n "}`,
		"a title not a string": `{"title":{"text":"T"}}`,
		"an unknown status":    `{"title":"T","status":"pending"}`,
		"labels not an object": `{"title":"T","labels":["a"]}`,
		"a list of alerts":     `[{"title":"T"}]`,
		"not JSON":             `title: T`,
	} {
		if alerts, err := ParseGenericAlert([]byte(body), AlertLabels{}); err == nil {
			t.Errorf("%s: read as %+v, want an error", name, alerts)
		}
	}
}

func TestParseAlerts(t *testing.T) {
	for _, tc := range []struct {
		typ, resource, fixture string
		alerts                 int
	}{
		{AlertTypeAlertmanager, "", "alertmanager-firing.json", 2},
		{AlertTypeSentry, SentryResourceIssueAlert, "sentry-issue-alert.json", 1},
		{AlertTypeSentry, SentryResourceIssue, "sentry-issue-resolved.json", 1},
		{AlertTypeGeneric, "", "generic-firing.json", 1},
	} {
		if got := mustAlerts(t, tc.typ, tc.resource, alertFixture(t, tc.fixture), AlertLabels{}); len(got) != tc.alerts {
			t.Errorf("%s %s: %d alerts, want %d", tc.typ, tc.fixture, len(got), tc.alerts)
		}
	}
	// A body sent to a source of another type is refused, never misread.
	for _, tc := range []struct{ typ, resource, fixture string }{
		{AlertTypeAlertmanager, "", "sentry-issue-alert.json"},
		{AlertTypeAlertmanager, "", "generic-firing.json"},
		{AlertTypeSentry, SentryResourceIssueAlert, "alertmanager-firing.json"},
		{AlertTypeGeneric, "", "alertmanager-firing.json"},
		{"pagerduty", "", "generic-firing.json"},
	} {
		if alerts, _, err := ParseAlerts(tc.typ, tc.resource, alertFixture(t, tc.fixture), AlertLabels{}); err == nil {
			t.Errorf("%s read %s as %+v, want an error", tc.typ, tc.fixture, alerts)
		}
	}
	// The same key in two types is two alerts.
	generic := mustAlerts(t, AlertTypeGeneric, "", []byte(`{"key":"1117540176","title":"T"}`), AlertLabels{})
	sentry := mustAlerts(t, AlertTypeSentry, SentryResourceIssueAlert, sentryAlertBody(`"issue_id":"1117540176"`), AlertLabels{})
	if generic[0].Key == sentry[0].Key {
		t.Error("a generic alert and a Sentry issue share a key")
	}
}
